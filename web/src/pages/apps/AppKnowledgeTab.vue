<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Knowledge</p>
        <h2 style="margin: 0">实例知识库</h2>
      </div>
    </template>
    <template #header-extra>
      <div v-if="canManage || canEditQuota" class="upload-actions">
        <n-button v-if="canEditQuota" size="small" @click="openQuotaModal">编辑空间</n-button>
        <template v-if="canManage">
          <span class="upload-limit">{{ KNOWLEDGE_UPLOAD_MAX_MESSAGE }}</span>
          <label class="secondary-button file-picker" :class="{ disabled: uploading }">
            上传文件
            <input type="file" :disabled="uploading" @change="onUploadFile" />
          </label>
        </template>
      </div>
    </template>

    <p v-if="quotaSummary" class="state-text">{{ quotaSummary }}</p>
    <p v-if="!app" class="state-text">尚未加载实例信息</p>
    <p v-else-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
    <div v-else-if="listing.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="listing.error.value" class="state-text danger">查询失败：{{ listing.error.value?.message }}</p>
    <n-data-table
      v-else
      :columns="columns"
      :data="listing.data.value?.items ?? []"
      size="small"
      :bordered="false"
      :row-key="(row) => row.id"
    />

    <n-modal v-model:show="showQuotaModal" preset="card" title="编辑实例知识库空间" style="width: 420px">
      <n-form label-placement="top" @submit.prevent="submitQuota">
        <n-form-item label="空间大小 (GB)">
          <n-input-number v-model:value="quotaGB" :min="1" :precision="0" style="width: 100%" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showQuotaModal = false">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="updateQuotaMutation.isPending.value">保存</n-button>
        </n-space>
        <p v-if="quotaFeedback" class="state-text" :class="{ danger: quotaError }">{{ quotaFeedback }}</p>
      </n-form>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
import { computed, h, inject, ref, type Ref } from 'vue'
import { NButton, NCard, NDataTable, NForm, NFormItem, NInputNumber, NModal, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'

import { useUpdateAppKnowledgeQuota, type AppDTO } from '@/api/hooks/useApps'
import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  downloadAppKnowledgeFile,
  formatKnowledgeBytes,
  isKnowledgeUploadOverRemaining,
  isKnowledgeUploadTooLarge,
  useAppKnowledgeQuery,
  useDeleteAppKnowledge,
  useReparseAppKnowledge,
  useUploadAppKnowledge,
  type KnowledgeDocument,
} from '@/api/hooks/useKnowledge'
import { canManageApp, canUpdateAppKnowledgeQuota } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import { useUploadProgressStore } from '@/stores/uploadProgress'

// AppKnowledgeTab 管理单个应用的 RAGFlow 知识库文件，权限来自应用详情注入。
const props = defineProps<{ appId: string }>()
const bytesPerGB = 1024 * 1024 * 1024
const appIdRef = computed<string | undefined>(() => props.appId)
const auth = useAuthStore()

const app = inject<Ref<AppDTO | null>>('app')

const listing = useAppKnowledgeQuery(appIdRef)
const uploadMutation = useUploadAppKnowledge(appIdRef)
const deleteMutation = useDeleteAppKnowledge(appIdRef)
const reparseMutation = useReparseAppKnowledge(appIdRef)
const updateQuotaMutation = useUpdateAppKnowledgeQuota(appIdRef)
const errorMessage = ref<string>('')
const showQuotaModal = ref(false)
const quotaGB = ref<number>(1)
const quotaFeedback = ref('')
const quotaError = ref(false)
const uploadProgress = useUploadProgressStore()
const message = useMessage()

// canManage 控制上传和删除入口，后端仍会基于应用归属做最终权限校验。
const canManage = computed(() => canManageApp(auth.user, app?.value))
// canEditQuota 单独控制容量入口，平台管理员可编辑容量但不一定拥有应用写操作入口。
const canEditQuota = computed(() => canUpdateAppKnowledgeQuota(auth.user, app?.value))
const uploading = computed(() => uploadMutation.isPending.value)
const deleting = computed(() => deleteMutation.isPending.value)
const quotaSummary = computed(() => listing.data.value
  ? `已用 ${formatKnowledgeBytes(listing.data.value.used_bytes)} / 上限 ${formatKnowledgeBytes(listing.data.value.quota_bytes)}，剩余 ${formatKnowledgeBytes(listing.data.value.remaining_bytes)}`
  : '')
// downloading 标记当前页面正在触发浏览器下载，避免重复点击生成多次下载请求。
const downloading = ref(false)

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

// onUploadFile 处理原生 file input 事件；上传进度统一由全局 UploadProgressModal 展示。
// 失败 / 取消的视觉反馈也来自 Modal 汇总区，本页只承担互斥提示。
async function onUploadFile(event: Event) {
  errorMessage.value = ''
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!canManage.value) return
  if (!file) return
  // 前端先拦截超过知识库业务上限的文件，避免创建进度会话后再被网关或后端拒绝。
  if (isKnowledgeUploadTooLarge(file)) {
    message.warning(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    return
  }
  // 剩余容量依赖后端返回的实时统计，超出时直接提示，避免创建无法完成的上传任务。
  if (isKnowledgeUploadOverRemaining(file, listing.data.value)) {
    message.warning(`知识库空间不足，剩余 ${formatKnowledgeBytes(listing.data.value?.remaining_bytes ?? 0)}`)
    return
  }
  try {
    await uploadProgress.run([{ file, label: file.name }], async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}

// openQuotaModal 将后端 bytes 上限转换成管理员可编辑的 GB 单位，并重置上次提交反馈。
function openQuotaModal() {
  quotaGB.value = Math.max(1, Math.round((app?.value?.knowledge_quota_bytes ?? bytesPerGB) / bytesPerGB))
  quotaFeedback.value = ''
  quotaError.value = false
  showQuotaModal.value = true
}

// submitQuota 提交前把 GB 转回 bytes；失败时保留弹窗并展示后端错误。
async function submitQuota() {
  quotaFeedback.value = ''
  quotaError.value = false
  try {
    await updateQuotaMutation.mutateAsync(Math.max(1, Math.round(quotaGB.value)) * bytesPerGB)
    showQuotaModal.value = false
  } catch (err) {
    quotaError.value = true
    quotaFeedback.value = err instanceof Error ? err.message : '更新空间失败'
  }
}

// deleteEntry 删除知识库条目并把 mutation 错误转为页面内反馈文案。
async function deleteEntry(documentId: string) {
  errorMessage.value = ''
  if (!canManage.value) return
  try {
    await deleteMutation.mutateAsync(documentId)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '删除失败'
  }
}

// onDownload 通过 manager 受保护接口下载 RAGFlow document 原文件。
async function onDownload(entry: KnowledgeDocument) {
  if (downloading.value) return
  downloading.value = true
  try {
    await downloadAppKnowledgeFile(props.appId, entry.id, entry.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
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
    errorMessage.value = err instanceof Error ? err.message : '重解析失败'
  }
}

function canReparse(row: KnowledgeDocument): boolean {
  return row.parse_status === 'failed' || row.parse_status === 'stopped'
}

// columns 展示 RAGFlow 文档；可读用户可下载，可管理用户额外可删除和重解析。
const columns: DataTableColumns<KnowledgeDocument> = [
  { title: '文件名称', key: 'name', render: (row) => h('strong', row.name) },
  { title: '大小', key: 'size', render: (row) => formatBytes(row.size) },
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
            onClick: () => reparseEntry(row.id),
          }, { default: () => reparseMutation.isPending.value ? '提交中…' : '重解析' }))
        }
        actions.push(h(NButton, {
          size: 'small',
          type: 'error',
          disabled: deleting.value,
          onClick: () => deleteEntry(row.id),
        }, { default: () => '删除' }))
      }
      return actions.length ? h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions) : null
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
