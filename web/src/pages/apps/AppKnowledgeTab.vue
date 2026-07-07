<template>
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
        <p class="eyebrow">Instance · Knowledge</p>
        <h2 style="margin: 0">{{ t('apps.knowledge.heading') }}</h2>
      </div>
    </template>
    <template #header-extra>
      <div v-if="canManage || canManageRAGFlowInfo" class="upload-actions">
        <n-button v-if="canManageRAGFlowInfo" size="small" @click="ragflowDialogOpen = true">
          {{ t('apps.knowledge.ragflowInfo') }}
        </n-button>
        <template v-if="canManage">
          <span class="upload-limit">{{ t('knowledge.messages.uploadAcceptedTypes', { label: KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL }) }}</span>
          <span class="upload-limit">{{ t('knowledge.messages.uploadMaxMessage', { label: KNOWLEDGE_UPLOAD_MAX_LABEL }) }}</span>
          <label class="secondary-button file-picker" :class="{ disabled: uploading }">
            {{ t('apps.knowledge.upload') }}
            <input type="file" multiple :accept="KNOWLEDGE_UPLOAD_ACCEPT" :disabled="uploading" @change="onUploadFile" />
          </label>
        </template>
      </div>
    </template>

    <n-space align="center" style="margin-bottom: 12px">
      <n-input
        v-model:value="keyword"
        :placeholder="t('apps.knowledge.searchPlaceholder')"
        clearable
        style="width: 220px"
      />
      <!-- parseStatusOptions 是 computed，语言切换时选项文案响应式更新。 -->
      <n-select
        v-model:value="status"
        :options="parseStatusOptions"
        clearable
        :placeholder="t('apps.knowledge.statusAll')"
        style="width: 160px"
      />
    </n-space>

    <p v-if="quotaSummary" class="state-text">{{ quotaSummary }}</p>
    <p v-if="!app" class="state-text">{{ t('apps.knowledge.noApp') }}</p>
    <p v-else-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
    <div v-else-if="listing.isLoading.value" class="state-text">{{ t('common.status.loading') }}</div>
    <p v-else-if="listing.error.value" class="state-text danger">{{ t('apps.knowledge.queryError') }}{{ listing.error.value?.message }}</p>
    <n-data-table
      v-else
      :columns="columns"
      :data="listing.data.value?.items ?? []"
      size="small"
      :bordered="false"
      :remote="true"
      :pagination="tablePagination"
      :row-key="(row) => row.id"
    />

    <RAGFlowDatasetInfoDialog
      v-model:visible="ragflowDialogOpen"
      scope="app"
      :target-id="props.appId"
      :target-name="ragflowTargetName"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed, h, inject, ref, watch, type Ref } from 'vue'
import { NButton, NCard, NDataTable, NInput, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import { type AppDTO } from '@/api/hooks/useApps'
import {
  KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL,
  KNOWLEDGE_UPLOAD_ACCEPT,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  downloadAppKnowledgeFile,
  formatKnowledgeBytes,
  useAppKnowledgeQuery,
  useDeleteAppKnowledge,
  useReparseAppKnowledge,
  useUploadAppKnowledge,
  type KnowledgeDocument,
} from '@/api/hooks/useKnowledge'
import { canManageApp, canManageRAGFlowDatasetInfo } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import { useUploadProgressStore } from '@/stores/uploadProgress'
import {
  filterKnowledgeUploadFiles,
  hasKnowledgeFilesInDrag,
  knowledgeFilesFromDrop,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from '@/pages/knowledge/knowledgeUploadBatch'
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from '@/domain/parseStatus'
// parseStatusOptions 在 computed 中翻译选项标签，确保语言切换时下拉选项文案响应式更新。
const parseStatusOptions = computed(() => PARSE_STATUS_FILTER_OPTIONS.map(opt => ({ ...opt, label: t(opt.label) })))
import RAGFlowDatasetInfoDialog from '@/components/RAGFlowDatasetInfoDialog.vue'

// AppKnowledgeTab 管理单个应用的 RAGFlow 知识库文件，权限来自应用详情注入。
const props = defineProps<{ appId: string }>()
const { t } = useI18n()
const appIdRef = computed<string | undefined>(() => props.appId)
const auth = useAuthStore()

const app = inject<Ref<AppDTO | null>>('app')

const keyword = ref('')
const normalizedKeyword = computed(() => keyword.value.trim())
// status 为「解析状态」筛选值，null/空＝不过滤（全部状态）。
const status = ref<string | null>(null)
const normalizedStatus = computed(() => status.value ?? undefined)
const page = ref(1)
const pageSize = ref(50)
const listing = useAppKnowledgeQuery(appIdRef, {
  page,
  pageSize,
  keyword: normalizedKeyword,
  status: normalizedStatus,
})
const uploadMutation = useUploadAppKnowledge(appIdRef)
const deleteMutation = useDeleteAppKnowledge(appIdRef)
const reparseMutation = useReparseAppKnowledge(appIdRef)
const errorMessage = ref<string>('')
const ragflowDialogOpen = ref(false)
const uploadProgress = useUploadProgressStore()
const message = useMessage()

// canManage 控制上传和删除入口，后端仍会基于应用归属做最终权限校验。
const canManage = computed(() => canManageApp(auth.user, app?.value))
// canManageRAGFlowInfo 控制远端 dataset 运维入口，仅平台管理员可见。
const canManageRAGFlowInfo = computed(() => canManageRAGFlowDatasetInfo(auth.user))
// ragflowTargetName 给运维弹框展示实例名；注入值缺失时保留稳定兜底文案。
// app 未加载时用「实例知识库」文案作为兜底，确保运维弹框标题始终有值。
const ragflowTargetName = computed(() => app?.value?.name || t('apps.knowledge.heading'))
const uploading = computed(() => uploadMutation.isPending.value)
const deleting = computed(() => deleteMutation.isPending.value)
const quotaSummary = computed(() => listing.data.value
  ? t('apps.knowledge.quotaSummary', {
      used: formatKnowledgeBytes(listing.data.value.used_bytes),
      quota: formatKnowledgeBytes(listing.data.value.quota_bytes),
      remaining: formatKnowledgeBytes(listing.data.value.remaining_bytes),
    })
  : '')
// downloading 标记当前页面正在触发浏览器下载，避免重复点击生成多次下载请求。
const downloading = ref(false)
// dragActive 标记当前卡片是否处于可上传拖拽态，仅有写权限时才会置 true。
const dragActive = ref(false)
// tablePagination 使用后端 total 驱动远程分页；切换搜索词或实例时回到第一页。
const tablePagination = computed(() => ({
  page: page.value,
  pageSize: pageSize.value,
  itemCount: listing.data.value?.total ?? 0,
  showSizePicker: true,
  pageSizes: [10, 20, 50, 100],
  prefix: () => t('apps.knowledge.fileCountPrefix', { count: listing.data.value?.total ?? 0 }),
  onUpdatePage: (nextPage: number) => {
    page.value = nextPage
  },
  onUpdatePageSize: (nextPageSize: number) => {
    pageSize.value = nextPageSize
    page.value = 1
  },
}))

watch([appIdRef, normalizedKeyword, normalizedStatus], () => {
  page.value = 1
})

// formatBytes 仅用于文件大小展示，RAGFlow 未返回大小时由后端归一化为 0。
function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

// formatTime 对可选创建时间做本地化展示，缺失时统一显示占位符。
function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

// documentTypeLabel 优先展示后端归一化后的后缀，其次展示 MIME type。
function documentTypeLabel(row: KnowledgeDocument): string {
  return row.suffix || row.mime_type || '—'
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
    message.warning(err instanceof Error ? err.message : t('apps.knowledge.uploadError'))
  }
}

// onUploadFile 处理原生 file input 事件；上传进度统一由全局 UploadProgressModal 展示。
// 失败 / 取消的视觉反馈也来自 Modal 汇总区，本页只承担互斥提示。
async function onUploadFile(event: Event) {
  errorMessage.value = ''
  const input = event.target as HTMLInputElement
  const files = knowledgeFilesFromInput(input)
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
  errorMessage.value = ''
  dragActive.value = false
  if (!canManage.value) return
  await uploadFiles(knowledgeFilesFromDrop(event))
}

// deleteEntry 删除知识库条目并把 mutation 错误转为页面内反馈文案。
async function deleteEntry(documentId: string) {
  errorMessage.value = ''
  if (!canManage.value) return
  try {
    await deleteMutation.mutateAsync(documentId)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : t('apps.knowledge.deleteError')
  }
}

// onDownload 通过 manager 受保护接口下载 RAGFlow document 原文件。
async function onDownload(entry: KnowledgeDocument) {
  if (downloading.value) return
  downloading.value = true
  try {
    await downloadAppKnowledgeFile(props.appId, entry.id, entry.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('apps.knowledge.downloadError'))
  } finally {
    downloading.value = false
  }
}

// reparseEntry 重新触发 RAGFlow 解析，仅失败或停止的文件允许重新入队。
async function reparseEntry(documentId: string) {
  errorMessage.value = ''
  if (!canManage.value) return
  try {
    await reparseMutation.mutateAsync(documentId)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : t('apps.knowledge.reparseError')
  }
}

function canReparse(row: KnowledgeDocument): boolean {
  return row.parse_status === 'failed' || row.parse_status === 'stopped'
}

// columns 展示 RAGFlow 文档；可读用户可下载，可管理用户额外可删除和重解析。
// 使用 computed 包裹以确保语言切换时列标题响应式更新。
const columns = computed<DataTableColumns<KnowledgeDocument>>(() => [
  { title: t('apps.knowledge.colName'), key: 'name', render: (row) => h('strong', row.name) },
  { title: t('apps.knowledge.colSize'), key: 'size', render: (row) => formatBytes(row.size) },
  { title: t('apps.knowledge.colType'), key: 'type', render: (row) => documentTypeLabel(row) },
  {
    title: t('apps.knowledge.colParseStatus'), key: 'parse_status',
    render: (row) => h('div', { style: 'display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
      // parseStatusLabel 返回 i18n 键（已知状态）或原始值（未知状态）；t() 对非键字符串原样返回。
      h(NTag, { type: parseStatusTagType(row.parse_status), size: 'small', bordered: false }, { default: () => t(parseStatusLabel(row.parse_status)) }),
      row.parse_status === 'running' ? h('span', { class: 'state-text', style: 'margin: 0; font-size: 12px' }, `${row.progress}%`) : null,
      row.last_error ? h('span', { style: 'color: var(--color-danger); font-size: 12px' }, row.last_error) : null,
    ]),
  },
  { title: t('apps.knowledge.colCreatedAt'), key: 'created_at', render: (row) => formatTime(row.created_at) },
  {
    title: t('apps.knowledge.colActions'), key: 'actions',
    render: (row) => {
      const actions = [
        h(NButton, {
          size: 'small',
          disabled: downloading.value,
          onClick: () => onDownload(row),
        }, { default: () => downloading.value ? t('apps.knowledge.actionDownloading') : t('apps.knowledge.actionDownload') }),
      ]
      if (canManage.value) {
        if (canReparse(row)) {
          actions.push(h(NButton, {
            size: 'small',
            disabled: reparseMutation.isPending.value,
            onClick: () => reparseEntry(row.id),
          }, { default: () => reparseMutation.isPending.value ? t('apps.knowledge.actionReparsePending') : t('apps.knowledge.actionReparse') }))
        }
        actions.push(h(NButton, {
          size: 'small',
          type: 'error',
          disabled: deleting.value,
          onClick: () => deleteEntry(row.id),
        }, { default: () => t('apps.knowledge.actionDelete') }))
      }
      return actions.length ? h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions) : null
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

.file-picker {
  position: relative;
  overflow: hidden;
}

.file-picker input {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
}

.file-picker.disabled {
  opacity: 0.6;
  pointer-events: none;
}
</style>
