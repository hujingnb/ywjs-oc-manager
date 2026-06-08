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
          <h2 style="margin: 0">企业知识库</h2>
        </div>
      </template>
      <template #header-extra>
        <div v-if="canManage" class="upload-actions">
          <span class="upload-limit">{{ KNOWLEDGE_UPLOAD_MAX_MESSAGE }}</span>
          <n-button
            size="small"
            type="error"
            :disabled="!hasFiles || clearMutation.isPending.value"
            :loading="clearMutation.isPending.value"
            @click="clearConfirmOpen = true"
          >
            清空文件
          </n-button>
          <label class="primary-button">
            <input class="hidden-input" type="file" multiple :disabled="!canManage" @change="onUpload" />
            上传文件
          </label>
        </div>
      </template>

      <n-space align="center" style="margin-bottom: 12px">
        <n-select
          v-if="isPlatformAdmin"
          v-model:value="selectedOrgId"
          :options="orgOptions"
          style="width: 220px"
          placeholder="选择企业"
        />
        <n-input
          v-model:value="keyword"
          placeholder="搜索文件名称"
          clearable
          style="width: 220px"
        />
      </n-space>

      <p v-if="quotaSummary" class="state-text">{{ quotaSummary }}</p>
      <div v-if="!effectiveOrgId" class="state-text">{{ emptyOrgMessage }}</div>
      <div v-else-if="isLoading || organizationsLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
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
      title="确认清空企业知识库文件"
      message="将删除当前企业知识库中的全部文件内容，企业和知识库配置会保留。该操作不可撤销。"
      confirm-label="确认清空"
      :busy="clearMutation.isPending.value"
      verify-value="清空文件"
      verify-hint='输入 "清空文件" 以确认清空'
      @confirm="onConfirmClear"
      @cancel="clearConfirmOpen = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { NButton, NCard, NDataTable, NInput, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'

import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
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
import { canManageOrgKnowledge } from '@/domain/permissions'
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

// OrgKnowledgePage 管理组织级共享知识库；文件主库由 RAGFlow 承担，页面只展示扁平 document 列表。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const uploadProgress = useUploadProgressStore()
const message = useMessage()
// 平台管理员通过组织选择器查看组织知识库，组织用户默认使用自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))

const eyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 知识库' : '企业 · 知识库'))
// canManage 决定上传、删除和重解析入口是否可见，接口层仍执行最终权限校验。
const canManage = computed(() => canManageOrgKnowledge(auth.user, effectiveOrgId.value))
const emptyOrgMessage = computed(() => isPlatformAdmin.value ? '暂无可查看企业' : '当前账号未关联企业')

const keyword = ref('')
const normalizedKeyword = computed(() => keyword.value.trim())
const page = ref(1)
const pageSize = ref(50)
const { data: listing, isLoading, error } = useOrgKnowledgeQuery(effectiveOrgId, {
  page,
  pageSize,
  keyword: normalizedKeyword,
})
const uploadMutation = useUploadOrgKnowledge(effectiveOrgId)
const deleteMutation = useDeleteOrgKnowledge(effectiveOrgId)
const clearMutation = useClearOrgKnowledge(effectiveOrgId)
const reparseMutation = useReparseOrgKnowledge(effectiveOrgId)
const quotaSummary = computed(() => listing.value
  ? `已用 ${formatKnowledgeBytes(listing.value.used_bytes)} / 上限 ${formatKnowledgeBytes(listing.value.quota_bytes)}，剩余 ${formatKnowledgeBytes(listing.value.remaining_bytes)}`
  : '')
// hasFiles 控制整库清空入口，避免空知识库提交破坏性请求。
const hasFiles = computed(() => (listing.value?.total ?? 0) > 0)
// downloading 标记当前页面正在触发浏览器下载，防止同一页面重复点击下载按钮。
const downloading = ref(false)
// dragActive 标记当前卡片是否处于可上传拖拽态，仅有写权限时才会置 true。
const dragActive = ref(false)
// clearConfirmOpen 控制整库清空二次确认弹窗，避免误点直接删除全部文件。
const clearConfirmOpen = ref(false)
// tablePagination 使用后端 total 驱动远程分页；搜索条件变化时回到第一页避免空页。
const tablePagination = computed(() => ({
  page: page.value,
  pageSize: pageSize.value,
  itemCount: listing.value?.total ?? 0,
  showSizePicker: true,
  pageSizes: [10, 20, 50, 100],
  prefix: () => `共 ${listing.value?.total ?? 0} 个文件`,
  onUpdatePage: (nextPage: number) => {
    page.value = nextPage
  },
  onUpdatePageSize: (nextPageSize: number) => {
    pageSize.value = nextPageSize
    page.value = 1
  },
}))

watch([effectiveOrgId, normalizedKeyword], () => {
  page.value = 1
})

// parseTagType 将 RAGFlow 解析状态映射为标签颜色，未知状态保留默认色便于兼容服务端新增状态。
function parseTagType(status: string): 'success' | 'warning' | 'error' | 'default' {
  if (status === 'completed') return 'success'
  if (status === 'queued' || status === 'running') return 'warning'
  if (status === 'failed' || status === 'stopped') return 'error'
  return 'default'
}

// parseStatusLabel 将 RAGFlow 状态转换为页面文案，未知值直接透出便于排障。
function parseStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    queued: '等待解析',
    running: '解析中',
    completed: '已完成',
    failed: '解析失败',
    stopped: '已停止',
  }
  return labels[status] ?? status
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
      })
    })
  } catch (err) {
    // 唯一会被抛出的错误是「会话互斥」：仅此一种情况下提示用户。
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
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
  if (!confirm(`确认删除 ${entry.name} ？`)) return
  await deleteMutation.mutateAsync(entry.id)
}

// onConfirmClear 清空企业知识库全部文件；后端按权限和企业 ID 做最终校验。
async function onConfirmClear() {
  try {
    await clearMutation.mutateAsync()
    clearConfirmOpen.value = false
    message.success('已清空企业知识库文件')
  } catch (err) {
    message.error(err instanceof Error ? err.message : '清空失败')
  }
}

// onDownload 通过 manager 受保护接口下载 RAGFlow document 原文件。
async function onDownload(entry: KnowledgeDocument) {
  if (!effectiveOrgId.value) return
  downloading.value = true
  try {
    await downloadOrgKnowledgeFile(effectiveOrgId.value, entry.id, entry.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
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
const fileColumns: DataTableColumns<KnowledgeDocument> = [
  {
    title: '文件名称', key: 'name',
    render: (row) => h('strong', row.name),
  },
  { title: '大小', key: 'size', render: (row) => formatSize(row.size) },
  { title: '类型', key: 'type', render: (row) => documentTypeLabel(row) },
  {
    title: '解析状态', key: 'parse_status',
    render: (row) => h('div', { style: 'display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
      h(NTag, { type: parseTagType(row.parse_status), size: 'small', bordered: false }, { default: () => parseStatusLabel(row.parse_status) }),
      row.parse_status === 'running' ? h('span', { class: 'state-text', style: 'margin: 0; font-size: 12px' }, `${row.progress}%`) : null,
      row.last_error ? h('span', { style: 'color: var(--color-danger); font-size: 12px' }, row.last_error) : null,
    ]),
  },
  { title: '创建时间', key: 'created_at', render: (row) => formatTime(row.created_at) },
  {
    title: '操作', key: 'actions',
    render: (row) => {
      const actions = [
        h(NButton, {
          size: 'small',
          disabled: downloading.value,
          onClick: () => onDownload(row),
        }, { default: () => downloading.value ? '下载中…' : '下载' }),
      ]
      if (canManage.value) {
        if (canReparse(row)) {
          actions.push(h(NButton, {
            size: 'small',
            disabled: reparseMutation.isPending.value,
            onClick: () => onReparse(row),
          }, { default: () => reparseMutation.isPending.value ? '提交中…' : '重解析' }))
        }
        actions.push(h(NButton, {
          size: 'small',
          type: 'error',
          disabled: deleteMutation.isPending.value,
          onClick: () => onDelete(row),
        }, { default: () => '删除' }))
      }
      return h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions)
    },
  },
]
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
