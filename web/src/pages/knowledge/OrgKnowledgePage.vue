<template>
  <div style="display: grid; gap: 18px">
    <!-- 文件列表 -->
    <n-card
      :bordered="true"
      class="knowledge-drop-zone"
      :class="{ 'drag-active': dragActive && canManage }"
      @dragenter.prevent="onDragEnter"
      @dragover.prevent="onDragOver"
      @dragleave.prevent="onDragLeave"
      @drop.prevent="onDropUpload"
    >
      <template #header>
        <div>
          <p class="eyebrow">{{ eyebrow }}</p>
          <h2 style="margin: 0">{{ t('knowledge.page.heading') }}</h2>
        </div>
      </template>
      <template #header-extra>
        <div v-if="canManage" class="upload-actions">
          <n-button
            v-if="canManageRAGFlowInfo && effectiveOrgId"
            size="small"
            @click="ragflowDialogOpen = true"
          >
            {{ t('knowledge.actions.ragflowInfo') }}
          </n-button>
          <span class="upload-limit">{{ t('knowledge.messages.uploadAcceptedTypes', { label: KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL }) }}</span>
          <span class="upload-limit">{{ t('knowledge.messages.uploadMaxMessage', { label: KNOWLEDGE_UPLOAD_MAX_LABEL }) }}</span>
          <n-button
            size="small"
            type="error"
            :disabled="!hasFiles || clearMutation.isPending.value"
            :loading="clearMutation.isPending.value"
            @click="clearConfirmOpen = true"
          >
            {{ t('knowledge.actions.clearFiles') }}
          </n-button>
          <label class="primary-button">
            <input class="hidden-input" type="file" multiple :accept="KNOWLEDGE_UPLOAD_ACCEPT" :disabled="!canManage" @change="onUpload" />
            {{ t('knowledge.actions.uploadFiles') }}
          </label>
        </div>
      </template>

      <n-space align="center" style="margin-bottom: 12px">
        <n-select
          v-if="isPlatformAdmin"
          v-model:value="selectedOrgId"
          :options="orgOptions"
          style="width: 220px"
          :placeholder="t('knowledge.filters.selectOrg')"
        />
        <n-input
          v-model:value="keyword"
          :placeholder="t('knowledge.filters.searchFileName')"
          clearable
          style="width: 220px"
        />
        <!-- parseStatusOptions 是 computed，语言切换时选项文案响应式更新。 -->
        <n-select
          v-model:value="status"
          :options="parseStatusOptions"
          clearable
          :placeholder="t('knowledge.filters.allStatuses')"
          style="width: 160px"
        />
      </n-space>

      <p v-if="quotaSummary" class="state-text">{{ quotaSummary }}</p>
      <div v-if="!effectiveOrgId" class="state-text">{{ emptyOrgMessage }}</div>
      <div v-else-if="isLoading || organizationsLoading" class="state-text">{{ t('common.status.loading') }}</div>
      <div v-else-if="error" class="state-text danger">{{ t('knowledge.state.queryFailed', { msg: error.message }) }}</div>
      <n-data-table
        v-else
        :columns="fileColumns"
        :data="listing?.items ?? []"
        size="small"
        :bordered="false"
        :remote="true"
        :pagination="tablePagination"
        :row-key="(row) => row.id"
      />
    </n-card>

    <ConfirmActionModal
      :visible="clearConfirmOpen"
      :title="t('knowledge.confirm.clearTitle')"
      :message="t('knowledge.confirm.clearMessage')"
      :confirm-label="t('knowledge.confirm.clearLabel')"
      :busy="clearMutation.isPending.value"
      :verify-value="t('knowledge.confirm.clearVerifyValue')"
      :verify-hint="t('knowledge.confirm.clearVerifyHint')"
      @confirm="onConfirmClear"
      @cancel="clearConfirmOpen = false"
    />

    <RAGFlowDatasetInfoDialog
      v-if="effectiveOrgId"
      v-model:visible="ragflowDialogOpen"
      scope="org"
      :target-id="effectiveOrgId"
      :target-name="t('knowledge.page.heading')"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { NButton, NCard, NDataTable, NInput, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import {
  KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL,
  KNOWLEDGE_UPLOAD_ACCEPT,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  downloadOrgKnowledgeFile,
  formatKnowledgeBytes,
  useClearOrgKnowledge,
  useDeleteOrgKnowledge,
  useOrgKnowledgeQuery,
  useReparseOrgKnowledge,
  useUploadOrgKnowledge,
  type KnowledgeDocument,
} from '@/api/hooks/useKnowledge'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from '@/domain/parseStatus'
import { canManageOrgKnowledge, canManageRAGFlowDatasetInfo } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import { useUploadProgressStore } from '@/stores/uploadProgress'
import {
  filterKnowledgeUploadFiles,
  hasKnowledgeFilesInDrag,
  knowledgeFilesFromDrop,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from './knowledgeUploadBatch'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import RAGFlowDatasetInfoDialog from '@/components/RAGFlowDatasetInfoDialog.vue'

// OrgKnowledgePage 管理组织级共享知识库；文件主库由 RAGFlow 承担，页面只展示扁平 document 列表。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const uploadProgress = useUploadProgressStore()
const message = useMessage()
const { t } = useI18n()
// parseStatusOptions 在 computed 中翻译选项标签，确保语言切换时下拉选项文案响应式更新。
const parseStatusOptions = computed(() => PARSE_STATUS_FILTER_OPTIONS.map(opt => ({ ...opt, label: t(opt.label) })))
// 平台管理员通过组织选择器查看组织知识库，组织用户默认使用自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))

// eyebrow 根据角色动态返回副标题，随语言切换响应式更新。
const eyebrow = computed(() =>
  auth.user?.role === 'platform_admin'
    ? t('knowledge.page.eyebrowPlatform')
    : t('knowledge.page.eyebrowOrg'),
)
// canManage 决定上传、删除和重解析入口是否可见，接口层仍执行最终权限校验。
const canManage = computed(() => canManageOrgKnowledge(auth.user, effectiveOrgId.value))
// canManageRAGFlowInfo 控制企业知识库远端 dataset 运维入口，仅平台管理员可见。
const canManageRAGFlowInfo = computed(() => canManageRAGFlowDatasetInfo(auth.user))
// emptyOrgMessage 根据角色区分空态提示：平台管理员无可选企业 vs 用户未关联企业。
const emptyOrgMessage = computed(() =>
  isPlatformAdmin.value ? t('knowledge.state.noOrg') : t('knowledge.state.noOrgLinked'),
)

const keyword = ref('')
const normalizedKeyword = computed(() => keyword.value.trim())
// status 为「解析状态」筛选值，null/空＝不过滤（全部状态）。
const status = ref<string | null>(null)
const normalizedStatus = computed(() => status.value ?? undefined)
const page = ref(1)
const pageSize = ref(50)
const { data: listing, isLoading, error } = useOrgKnowledgeQuery(effectiveOrgId, {
  page,
  pageSize,
  keyword: normalizedKeyword,
  status: normalizedStatus,
})
const uploadMutation = useUploadOrgKnowledge(effectiveOrgId)
const deleteMutation = useDeleteOrgKnowledge(effectiveOrgId)
const clearMutation = useClearOrgKnowledge(effectiveOrgId)
const reparseMutation = useReparseOrgKnowledge(effectiveOrgId)
// quotaSummary 展示已用/上限/剩余容量，随 listing 数据刷新。
const quotaSummary = computed(() => listing.value
  ? t('knowledge.quota.summary', {
      used: formatKnowledgeBytes(listing.value.used_bytes),
      quota: formatKnowledgeBytes(listing.value.quota_bytes),
      remaining: formatKnowledgeBytes(listing.value.remaining_bytes),
    })
  : '')
// hasFiles 控制整库清空入口，避免空知识库提交破坏性请求。
const hasFiles = computed(() => (listing.value?.total ?? 0) > 0)
// downloading 标记当前页面正在触发浏览器下载，防止同一页面重复点击下载按钮。
const downloading = ref(false)
// dragActive 标记当前卡片是否处于可上传拖拽态，仅有写权限时才会置 true。
const dragActive = ref(false)
// clearConfirmOpen 控制整库清空二次确认弹窗，避免误点直接删除全部文件。
const clearConfirmOpen = ref(false)
const ragflowDialogOpen = ref(false)
// tablePagination 使用后端 total 驱动远程分页；搜索条件变化时回到第一页避免空页。
const tablePagination = computed(() => ({
  page: page.value,
  pageSize: pageSize.value,
  itemCount: listing.value?.total ?? 0,
  showSizePicker: true,
  pageSizes: [10, 20, 50, 100],
  prefix: () => t('knowledge.pagination.totalFiles', { n: listing.value?.total ?? 0 }),
  onUpdatePage: (nextPage: number) => {
    page.value = nextPage
  },
  onUpdatePageSize: (nextPageSize: number) => {
    pageSize.value = nextPageSize
    page.value = 1
  },
}))

watch([effectiveOrgId, normalizedKeyword, normalizedStatus], () => {
  page.value = 1
})

// formatTime 对可选创建时间做本地化展示，缺失时统一显示占位符。
function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

// documentTypeLabel 优先展示后端归一化后的后缀，其次展示 MIME type。
function documentTypeLabel(row: KnowledgeDocument): string {
  return row.suffix || row.mime_type || '—'
}

// formatSize 用于文件大小展示，RAGFlow 未返回大小时降级为 0 B。
function formatSize(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}

// uploadFiles 把多选或拖拽得到的文件交给全局上传队列；容量不足等动态失败由后端逐个返回。
async function uploadFiles(files: File[]) {
  const uploadableFiles = filterKnowledgeUploadFiles(files, message.warning)
  if (uploadableFiles.length === 0) return
  try {
    await uploadProgress.run(toKnowledgeUploadItems(uploadableFiles), async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
        onFinalizing: ctx.onFinalizing,
      })
    })
  } catch (err) {
    // 唯一会被抛出的错误是「会话互斥」：仅此一种情况下提示用户。
    message.warning(err instanceof Error ? err.message : t('knowledge.messages.uploadBusy'))
  }
}

// onUpload 将文件上传到 RAGFlow 组织 dataset；上传进度统一由全局 UploadProgressModal 展示。
// 互斥规则：会话进行中 store.run 抛错，业务侧用 n-message 提示用户。
async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = knowledgeFilesFromInput(input)
  // 不论是否真正发起上传，都清空 input.value 以便用户重新选择同名文件。
  input.value = ''
  if (!canManage.value) return
  await uploadFiles(files)
}

// onDragEnter 在拖入文件时打开视觉态；纯文本拖拽不影响知识库卡片。
function onDragEnter(event: DragEvent) {
  if (!canManage.value || !hasKnowledgeFilesInDrag(event)) return
  dragActive.value = true
}

// onDragOver 持续维持可上传视觉态，并让浏览器显示 copy dropEffect。
function onDragOver(event: DragEvent) {
  if (!canManage.value || !hasKnowledgeFilesInDrag(event)) return
  dragActive.value = true
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'copy'
  }
}

// onDragLeave 在真正离开卡片时关闭视觉态；子元素之间移动会产生 dragleave，需要保留视觉态。
function onDragLeave(event: DragEvent) {
  const current = event.currentTarget
  const related = event.relatedTarget
  if (current instanceof Node && related instanceof Node && current.contains(related)) return
  dragActive.value = false
}

// onDropUpload 处理拖拽文件上传；目录或非文件项会在 helper 中被过滤。
async function onDropUpload(event: DragEvent) {
  dragActive.value = false
  if (!canManage.value) return
  await uploadFiles(knowledgeFilesFromDrop(event))
}

// onDelete 使用浏览器确认框拦截误删，删除后由 mutation hook 负责刷新列表缓存。
async function onDelete(entry: KnowledgeDocument) {
  if (!confirm(t('knowledge.messages.deleteConfirm', { name: entry.name }))) return
  await deleteMutation.mutateAsync(entry.id)
}

// onConfirmClear 清空企业知识库全部文件；后端按权限和企业 ID 做最终校验。
async function onConfirmClear() {
  try {
    await clearMutation.mutateAsync()
    clearConfirmOpen.value = false
    message.success(t('knowledge.messages.clearSuccess'))
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('knowledge.messages.clearFailed'))
  }
}

// onDownload 通过 manager 受保护接口下载 RAGFlow document 原文件。
async function onDownload(entry: KnowledgeDocument) {
  if (!effectiveOrgId.value) return
  downloading.value = true
  try {
    await downloadOrgKnowledgeFile(effectiveOrgId.value, entry.id, entry.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('knowledge.messages.downloadFailed'))
  } finally {
    downloading.value = false
  }
}

// onReparse 重新触发 RAGFlow 解析，仅失败或停止的文件允许重新入队。
async function onReparse(entry: KnowledgeDocument) {
  await reparseMutation.mutateAsync(entry.id)
}

function canReparse(row: KnowledgeDocument): boolean {
  return row.parse_status === 'failed' || row.parse_status === 'stopped'
}

// fileColumns 展示 RAGFlow 文档；组织成员可下载，管理者额外可删除和重解析。
// 使用 computed 确保语言切换时列头文案响应式更新。
const fileColumns = computed<DataTableColumns<KnowledgeDocument>>(() => [
  {
    title: t('knowledge.table.fileName'), key: 'name',
    render: (row) => h('strong', row.name),
  },
  { title: t('knowledge.table.size'), key: 'size', render: (row) => formatSize(row.size) },
  { title: t('knowledge.table.type'), key: 'type', render: (row) => documentTypeLabel(row) },
  {
    title: t('knowledge.table.parseStatus'), key: 'parse_status',
    render: (row) => h('div', { style: 'display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
      // parseStatusLabel 返回 i18n 键（已知状态）或原始值（未知状态）；t() 对非键字符串原样返回。
      h(NTag, { type: parseStatusTagType(row.parse_status), size: 'small', bordered: false }, { default: () => t(parseStatusLabel(row.parse_status)) }),
      row.parse_status === 'running' ? h('span', { class: 'state-text', style: 'margin: 0; font-size: 12px' }, `${row.progress}%`) : null,
      row.last_error ? h('span', { style: 'color: var(--color-danger); font-size: 12px' }, row.last_error) : null,
    ]),
  },
  { title: t('common.table.createdAt'), key: 'created_at', render: (row) => formatTime(row.created_at) },
  {
    title: t('common.table.actions'), key: 'actions',
    render: (row) => {
      const actions = [
        h(NButton, {
          size: 'small',
          disabled: downloading.value,
          onClick: () => onDownload(row),
        }, { default: () => downloading.value ? t('knowledge.fileActions.downloading') : t('knowledge.fileActions.download') }),
      ]
      if (canManage.value) {
        if (canReparse(row)) {
          actions.push(h(NButton, {
            size: 'small',
            disabled: reparseMutation.isPending.value,
            onClick: () => onReparse(row),
          }, { default: () => reparseMutation.isPending.value ? t('knowledge.fileActions.reparsing') : t('knowledge.fileActions.reparse') }))
        }
        actions.push(h(NButton, {
          size: 'small',
          type: 'error',
          disabled: deleteMutation.isPending.value,
          onClick: () => onDelete(row),
        }, { default: () => t('common.actions.delete') }))
      }
      return h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions)
    },
  },
])
</script>

<style scoped>
.upload-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

.upload-limit {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.knowledge-drop-zone {
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.knowledge-drop-zone.drag-active {
  border-color: var(--color-brand);
  box-shadow: 0 0 0 2px rgba(255, 106, 0, 0.14);
}

.hidden-input {
  display: none;
}

.primary-button.disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
