<template>
  <n-modal :show="visible" preset="card" title="RAGFlow 信息" style="width: min(560px, 92vw)" @update:show="emitVisible">
    <div class="ragflow-info-dialog">
      <n-spin :show="infoQuery.isLoading.value || modelsQuery.isLoading.value">
        <n-alert v-if="infoQuery.error.value" type="error" :bordered="false">
          {{ infoQuery.error.value.message }}
          <n-button class="retry-button" size="small" @click="refetchAll">重试</n-button>
        </n-alert>
        <n-alert v-else-if="info?.status === 'error'" type="error" :bordered="false">
          {{ info.error_message || '读取 RAGFlow 信息失败' }}
          <n-button class="retry-button" size="small" @click="refetchAll">重试</n-button>
        </n-alert>
        <n-alert v-else-if="info?.status === 'not_created'" type="warning" :bordered="false">
          当前知识库尚未创建 RAGFlow dataset，上传文件或初始化完成后再查看。
        </n-alert>

        <n-descriptions v-if="info" :column="1" bordered size="small">
          <n-descriptions-item label="知识库">
            {{ scopeLabel }} · {{ targetName || info.target_name || targetId }}
          </n-descriptions-item>
          <n-descriptions-item label="RAGFlow dataset ID">
            {{ info.ragflow_dataset_id || '-' }}
          </n-descriptions-item>
          <n-descriptions-item label="RAGFlow dataset 名称">
            {{ info.ragflow_dataset_name || '-' }}
          </n-descriptions-item>
          <n-descriptions-item label="当前模型">
            {{ info.embedding_model?.label || info.embedding_model?.name || '-' }}
          </n-descriptions-item>
          <n-descriptions-item v-if="info.doc_num !== undefined" label="文档数">
            {{ info.doc_num }}
          </n-descriptions-item>
          <n-descriptions-item v-if="info.chunk_num !== undefined" label="Chunk 数">
            {{ info.chunk_num }}
          </n-descriptions-item>
        </n-descriptions>

        <n-form class="model-form" label-placement="top" @submit.prevent="openConfirm">
          <n-form-item label="Embedding 模型">
            <n-select
              v-model:value="selectedModelKey"
              :options="modelOptions"
              :disabled="!canSubmit"
              placeholder="选择模型"
            />
          </n-form-item>
          <n-space justify="end">
            <n-button @click="emitVisible(false)">关闭</n-button>
            <n-button :disabled="!canReparse" :loading="reparseMutation.isPending.value" @click="openReparseConfirm">
              重新解析失败文件
            </n-button>
            <n-button type="primary" attr-type="submit" :disabled="!canSubmit || selectedModelUnchanged">
              保存并重新解析
            </n-button>
          </n-space>
        </n-form>
      </n-spin>
    </div>

    <ConfirmActionModal
      :visible="confirmOpen"
      title="确认修改 RAGFlow 模型"
      message="将更新 RAGFlow dataset 的 embedding 模型，并使该知识库下全部文件重新进入解析流程。"
      confirm-label="确认修改"
      :busy="mutation.isPending.value"
      @confirm="submit"
      @cancel="confirmOpen = false"
    />

    <ConfirmActionModal
      :visible="reparseConfirmOpen"
      title="确认重新解析失败文件"
      message="将把该知识库下解析失败或已停止的文件重新进入解析流程，不更换 embedding 模型。"
      confirm-label="确认重新解析"
      :busy="reparseMutation.isPending.value"
      @confirm="submitReparse"
      @cancel="reparseConfirmOpen = false"
    />
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import {
  NAlert,
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NForm,
  NFormItem,
  NModal,
  NSelect,
  NSpace,
  NSpin,
} from 'naive-ui'

import {
  useKnowledgeEmbeddingModelsQuery,
  useRAGFlowDatasetInfoQuery,
  useReparseFailedRAGFlowDataset,
  useUpdateRAGFlowDatasetEmbeddingModel,
} from '@/api/hooks/useKnowledge'
import type { KnowledgeEmbeddingModel, KnowledgeRAGFlowScope } from '@/api/hooks/useKnowledge'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'

const props = defineProps<{
  visible: boolean
  scope: KnowledgeRAGFlowScope
  targetId: string
  targetName?: string
}>()

const emit = defineEmits<{
  (event: 'update:visible', value: boolean): void
  (event: 'updated'): void
}>()

const scope = toRef(props, 'scope')
const targetId = toRef(props, 'targetId')
const selectedModelKey = ref<string | null>(null)
const confirmOpen = ref(false)
const reparseConfirmOpen = ref(false)

const infoQuery = useRAGFlowDatasetInfoQuery(scope, targetId, () => props.visible)
const modelsQuery = useKnowledgeEmbeddingModelsQuery(() => props.visible)
const mutation = useUpdateRAGFlowDatasetEmbeddingModel(scope, targetId)
const reparseMutation = useReparseFailedRAGFlowDataset(scope, targetId)

const info = computed(() => infoQuery.data.value)
const currentModelKey = computed(() => {
  const model = info.value?.embedding_model
  return model ? modelKey(model.name, model.provider) : null
})
const canSubmit = computed(() => info.value?.status === 'ok' && modelOptions.value.length > 0)
const selectedModelUnchanged = computed(() => selectedModelKey.value === null || selectedModelKey.value === currentModelKey.value)
// 重解析失败文件只要求 dataset 已创建可用，与是否选择/切换模型无关。
const canReparse = computed(() => info.value?.status === 'ok')

const scopeLabel = computed(() => {
  if (props.scope === 'org') return '企业知识库'
  if (props.scope === 'app') return '实例知识库'
  return '行业知识库'
})

const modelOptions = computed(() => {
  return (modelsQuery.data.value?.items ?? []).map(model => ({
    label: model.provider ? `${model.label || model.name} (${model.provider})` : model.label || model.name,
    value: modelKey(model.name, model.provider),
    disabled: !model.available,
  }))
})

watch(
  currentModelKey,
  (key) => {
    if (key) selectedModelKey.value = key
  },
  { immediate: true },
)

function modelKey(name: string, provider?: string): string {
  return `${name}|${provider ?? ''}`
}

function parseModelKey(key: string | null): Pick<KnowledgeEmbeddingModel, 'name' | 'provider'> | null {
  if (!key) return null
  const [name, ...providerParts] = key.split('|')
  return { name, provider: providerParts.join('|') }
}

function emitVisible(value: boolean) {
  emit('update:visible', value)
}

function refetchAll() {
  void infoQuery.refetch()
  void modelsQuery.refetch()
}

function openConfirm() {
  if (!canSubmit.value || selectedModelUnchanged.value) return
  confirmOpen.value = true
}

async function submit() {
  const selected = parseModelKey(selectedModelKey.value)
  if (!selected) return
  await mutation.mutateAsync({
    name: selected.name,
    provider: selected.provider || undefined,
  })
  confirmOpen.value = false
  emit('updated')
  refetchAll()
}

function openReparseConfirm() {
  if (!canReparse.value) return
  reparseConfirmOpen.value = true
}

async function submitReparse() {
  await reparseMutation.mutateAsync()
  reparseConfirmOpen.value = false
  emit('updated')
  refetchAll()
}
</script>

<style scoped>
.ragflow-info-dialog {
  min-height: 220px;
}

.model-form {
  margin-top: 14px;
}

.retry-button {
  margin-left: 12px;
}
</style>
