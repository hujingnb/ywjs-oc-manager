<template>
  <n-modal :show="visible" preset="card" :title="t('components.ragflowDatasetInfoDialog.title')" style="width: min(560px, 92vw)" @update:show="emitVisible">
    <div class="ragflow-info-dialog">
      <n-spin :show="infoQuery.isLoading.value || modelsQuery.isLoading.value">
        <n-alert v-if="infoQuery.error.value" type="error" :bordered="false">
          {{ infoQuery.error.value.message }}
          <n-button class="retry-button" size="small" @click="refetchAll">{{ t('components.ragflowDatasetInfoDialog.retryBtn') }}</n-button>
        </n-alert>
        <n-alert v-else-if="info?.status === 'error'" type="error" :bordered="false">
          {{ info.error_message || t('components.ragflowDatasetInfoDialog.loadFailed') }}
          <n-button class="retry-button" size="small" @click="refetchAll">{{ t('components.ragflowDatasetInfoDialog.retryBtn') }}</n-button>
        </n-alert>
        <n-alert v-else-if="info?.status === 'not_created'" type="warning" :bordered="false">
          {{ t('components.ragflowDatasetInfoDialog.notCreatedHint') }}
        </n-alert>

        <n-descriptions v-if="info" :column="1" bordered size="small">
          <n-descriptions-item :label="t('components.ragflowDatasetInfoDialog.labelKnowledgeBase')">
            {{ scopeLabel }} · {{ targetName || info.target_name || targetId }}
          </n-descriptions-item>
          <n-descriptions-item :label="t('components.ragflowDatasetInfoDialog.labelDatasetId')">
            {{ info.ragflow_dataset_id || '-' }}
          </n-descriptions-item>
          <n-descriptions-item :label="t('components.ragflowDatasetInfoDialog.labelDatasetName')">
            {{ info.ragflow_dataset_name || '-' }}
          </n-descriptions-item>
          <n-descriptions-item :label="t('components.ragflowDatasetInfoDialog.labelCurrentModel')">
            {{ info.embedding_model?.label || info.embedding_model?.name || '-' }}
          </n-descriptions-item>
          <n-descriptions-item v-if="info.doc_num !== undefined" :label="t('components.ragflowDatasetInfoDialog.labelDocNum')">
            {{ info.doc_num }}
          </n-descriptions-item>
          <n-descriptions-item v-if="info.chunk_num !== undefined" :label="t('components.ragflowDatasetInfoDialog.labelChunkNum')">
            {{ info.chunk_num }}
          </n-descriptions-item>
        </n-descriptions>

        <n-form class="model-form" label-placement="top" @submit.prevent="openConfirm">
          <n-form-item :label="t('components.ragflowDatasetInfoDialog.labelEmbeddingModel')">
            <n-select
              v-model:value="selectedModelKey"
              :options="modelOptions"
              :disabled="!canSubmit"
              :placeholder="t('components.ragflowDatasetInfoDialog.selectModelPlaceholder')"
            />
          </n-form-item>
          <n-space justify="end">
            <n-button @click="emitVisible(false)">{{ t('common.actions.close') }}</n-button>
            <n-button type="primary" attr-type="submit" :disabled="!canSubmit || selectedModelUnchanged">
              {{ t('components.ragflowDatasetInfoDialog.saveReparse') }}
            </n-button>
          </n-space>
        </n-form>
      </n-spin>
    </div>

    <ConfirmActionModal
      :visible="confirmOpen"
      :title="t('components.ragflowDatasetInfoDialog.confirmTitle')"
      :message="t('components.ragflowDatasetInfoDialog.confirmMessage')"
      :confirm-label="t('components.ragflowDatasetInfoDialog.confirmLabel')"
      :busy="mutation.isPending.value"
      @confirm="submit"
      @cancel="confirmOpen = false"
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
import { useI18n } from 'vue-i18n'

import {
  useKnowledgeEmbeddingModelsQuery,
  useRAGFlowDatasetInfoQuery,
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

const { t } = useI18n()

const scope = toRef(props, 'scope')
const targetId = toRef(props, 'targetId')
const selectedModelKey = ref<string | null>(null)
const confirmOpen = ref(false)

const infoQuery = useRAGFlowDatasetInfoQuery(scope, targetId, () => props.visible)
const modelsQuery = useKnowledgeEmbeddingModelsQuery(() => props.visible)
const mutation = useUpdateRAGFlowDatasetEmbeddingModel(scope, targetId)

const info = computed(() => infoQuery.data.value)
const currentModelKey = computed(() => {
  const model = info.value?.embedding_model
  return model ? modelKey(model.name, model.provider) : null
})
const canSubmit = computed(() => info.value?.status === 'ok' && modelOptions.value.length > 0)
const selectedModelUnchanged = computed(() => selectedModelKey.value === null || selectedModelKey.value === currentModelKey.value)

const scopeLabel = computed(() => {
  if (props.scope === 'org') return t('components.ragflowDatasetInfoDialog.scopeOrg')
  if (props.scope === 'app') return t('components.ragflowDatasetInfoDialog.scopeApp')
  return t('components.ragflowDatasetInfoDialog.scopeIndustry')
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
