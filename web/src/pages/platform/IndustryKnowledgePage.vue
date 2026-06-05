<template>
  <div style="display: grid; gap: 18px">
    <DataTableList
      title="行业知识库"
      eyebrow="Platform"
      subtitle="平台级通用资料库，可被助手版本选择参与运行时检索。"
      :columns="baseColumns"
      :data="bases?.items ?? []"
      :loading="basesLoading"
      :error-message="basesError?.message"
      :row-key="(row: IndustryKnowledgeBase) => row.id"
    >
      <template #toolbar>
        <n-input v-model:value="keyword" placeholder="搜索行业名称" clearable style="width: 180px" />
        <n-button
          type="primary"
          :disabled="createMutation.isPending.value"
          :loading="createMutation.isPending.value"
          @click="openCreateDialog"
        >
          <template #icon><Plus :size="16" /></template>
          新建行业库
        </n-button>
      </template>
    </DataTableList>

    <n-card v-if="selectedBase" :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Industry</p>
          <h2 style="margin: 0">{{ selectedBase.name }}</h2>
        </div>
      </template>
      <template #header-extra>
        <div class="upload-actions">
          <span class="upload-limit">{{ KNOWLEDGE_UPLOAD_MAX_MESSAGE }}</span>
          <label class="primary-button">
            <input class="hidden-input" type="file" multiple @change="onUpload" />
            上传文件
          </label>
        </div>
      </template>

      <n-alert type="warning" :bordered="false" style="margin-bottom: 12px">
        同名文件会覆盖当前行业库内的旧文件。
      </n-alert>
      <div v-if="filesLoading" class="state-text">加载中…</div>
      <div v-else-if="filesError" class="state-text danger">查询失败：{{ filesError.message }}</div>
      <n-data-table
        v-else
        :columns="fileColumns"
        :data="files?.items ?? []"
        size="small"
        :bordered="false"
        :row-key="(row: KnowledgeDocument) => row.id"
      />
    </n-card>

    <n-card v-else :bordered="true">
      <div class="state-text">暂无行业知识库</div>
    </n-card>

    <n-modal v-model:show="createDialogOpen" transform-origin="center">
      <n-card
        class="create-dialog-card"
        title="新建行业库"
        :bordered="false"
        role="dialog"
        aria-modal="true"
      >
        <div class="create-dialog-body">
          <span class="field-label">行业名称</span>
          <n-input
            v-model:value="newBaseName"
            placeholder="请输入行业名称"
            clearable
            @keyup.enter="onCreateBase"
          />
        </div>
        <template #footer>
          <div class="dialog-actions">
            <n-button :disabled="createMutation.isPending.value" @click="closeCreateDialog">取消</n-button>
            <n-button
              type="primary"
              :disabled="createMutation.isPending.value"
              :loading="createMutation.isPending.value"
              @click="onCreateBase"
            >
              确认创建
            </n-button>
          </div>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { Plus } from 'lucide-vue-next'
import { NAlert, NButton, NCard, NDataTable, NInput, NModal, NTag, useMessage, type DataTableColumns } from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  formatKnowledgeBytes,
  type KnowledgeDocument,
} from '@/api/hooks/useKnowledge'
import {
  downloadIndustryKnowledgeFile,
  useCreateIndustryKnowledgeBase,
  useDeleteIndustryKnowledgeBase,
  useDeleteIndustryKnowledgeFile,
  useIndustryKnowledgeBasesQuery,
  useIndustryKnowledgeFilesQuery,
  useRenameIndustryKnowledgeBase,
  useReparseIndustryKnowledgeFile,
  useUploadIndustryKnowledgeFile,
  type IndustryKnowledgeBase,
} from '@/api/hooks/useIndustryKnowledge'
import { useUploadProgressStore } from '@/stores/uploadProgress'
import {
  filterKnowledgeUploadFiles,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from '@/pages/knowledge/knowledgeUploadBatch'

// IndustryKnowledgePage 是平台管理员管理行业知识库和库内文件的页面。
const message = useMessage()
const uploadProgress = useUploadProgressStore()

const keyword = ref('')
const newBaseName = ref('')
const createDialogOpen = ref(false)
const selectedBaseId = ref<string | undefined>(undefined)
const downloading = ref(false)

const { data: bases, isLoading: basesLoading, error: basesError } = useIndustryKnowledgeBasesQuery(undefined, keyword)
const selectedBase = computed(() => (bases.value?.items ?? []).find(item => item.id === selectedBaseId.value) ?? null)
const selectedBaseIdRef = computed(() => selectedBase.value?.id)
const { data: files, isLoading: filesLoading, error: filesError } = useIndustryKnowledgeFilesQuery(selectedBaseIdRef)

const createMutation = useCreateIndustryKnowledgeBase()
const renameMutation = useRenameIndustryKnowledgeBase()
const deleteBaseMutation = useDeleteIndustryKnowledgeBase()
const uploadMutation = useUploadIndustryKnowledgeFile(selectedBaseIdRef)
const deleteFileMutation = useDeleteIndustryKnowledgeFile(selectedBaseIdRef)
const reparseMutation = useReparseIndustryKnowledgeFile(selectedBaseIdRef)

watch(
  () => bases.value?.items ?? [],
  (items) => {
    if (items.length === 0) {
      selectedBaseId.value = undefined
      return
    }
    if (!items.some(item => item.id === selectedBaseId.value)) {
      selectedBaseId.value = items[0].id
    }
  },
  { immediate: true },
)

function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

function parseTagType(status: string): 'success' | 'warning' | 'error' | 'default' {
  if (status === 'completed') return 'success'
  if (status === 'queued' || status === 'running') return 'warning'
  if (status === 'failed' || status === 'stopped') return 'error'
  return 'default'
}

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

function canReparse(row: KnowledgeDocument): boolean {
  return row.parse_status === 'failed' || row.parse_status === 'stopped'
}

function openCreateDialog() {
  newBaseName.value = ''
  createDialogOpen.value = true
}

function closeCreateDialog() {
  if (createMutation.isPending.value) return
  createDialogOpen.value = false
  newBaseName.value = ''
}

async function onCreateBase() {
  const name = newBaseName.value.trim()
  if (!name) {
    message.warning('请输入行业名称')
    return
  }
  try {
    const created = await createMutation.mutateAsync(name)
    selectedBaseId.value = created.id
    newBaseName.value = ''
    createDialogOpen.value = false
    message.success(`已创建行业库 ${created.name}`)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '创建失败')
  }
}

async function onRenameBase(row: IndustryKnowledgeBase) {
  const name = window.prompt('新的行业名称', row.name)?.trim()
  if (!name || name === row.name) return
  try {
    const renamed = await renameMutation.mutateAsync({ id: row.id, name })
    message.success(`已重命名为 ${renamed.name}`)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '重命名失败')
  }
}

async function onDeleteBase(row: IndustryKnowledgeBase) {
  if (!window.confirm(`确认删除行业库「${row.name}」？`)) return
  try {
    await deleteBaseMutation.mutateAsync(row.id)
    message.success(`已删除行业库 ${row.name}`)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '删除失败')
  }
}

async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const uploadableFiles = filterKnowledgeUploadFiles(knowledgeFilesFromInput(input), message.warning)
  input.value = ''
  if (!selectedBase.value || uploadableFiles.length === 0) return
  try {
    await uploadProgress.run(toKnowledgeUploadItems(uploadableFiles), async (_item, file, ctx) => {
      await uploadMutation.mutateAsync({
        file,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}

async function onDownload(row: KnowledgeDocument) {
  if (!selectedBase.value) return
  downloading.value = true
  try {
    await downloadIndustryKnowledgeFile(selectedBase.value.id, row.id, row.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  } finally {
    downloading.value = false
  }
}

async function onDeleteFile(row: KnowledgeDocument) {
  if (!window.confirm(`确认删除 ${row.name} ？`)) return
  await deleteFileMutation.mutateAsync(row.id)
}

async function onReparse(row: KnowledgeDocument) {
  await reparseMutation.mutateAsync(row.id)
}

const baseColumns: DataTableColumns<IndustryKnowledgeBase> = [
  {
    title: '行业名称',
    key: 'name',
    render: row => h('strong', row.name),
  },
  { title: '文件数', key: 'document_count', render: row => String(row.document_count ?? 0) },
  { title: '更新时间', key: 'updated_at', render: row => formatTime(row.updated_at) },
  {
    title: '操作',
    key: 'actions',
    render: row => h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, [
      h(NButton, { size: 'small', type: selectedBaseId.value === row.id ? 'primary' : 'default', onClick: () => { selectedBaseId.value = row.id } }, { default: () => '文件' }),
      h(NButton, { size: 'small', onClick: () => onRenameBase(row) }, { default: () => '重命名' }),
      h(NButton, { size: 'small', type: 'error', disabled: deleteBaseMutation.isPending.value, onClick: () => onDeleteBase(row) }, { default: () => '删除' }),
    ]),
  },
]

const fileColumns: DataTableColumns<KnowledgeDocument> = [
  { title: '文件名称', key: 'name', render: row => h('strong', row.name) },
  { title: '大小', key: 'size', render: row => formatKnowledgeBytes(row.size) },
  { title: '类型', key: 'type', render: row => row.suffix || row.mime_type || '—' },
  {
    title: '解析状态',
    key: 'parse_status',
    render: row => h('div', { style: 'display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
      h(NTag, { type: parseTagType(row.parse_status), size: 'small', bordered: false }, { default: () => parseStatusLabel(row.parse_status) }),
      row.parse_status === 'running' ? h('span', { class: 'state-text', style: 'margin: 0; font-size: 12px' }, `${row.progress}%`) : null,
      row.last_error ? h('span', { style: 'color: var(--color-danger); font-size: 12px' }, row.last_error) : null,
    ]),
  },
  { title: '创建时间', key: 'created_at', render: row => formatTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    render: row => {
      const actions = [
        h(NButton, { size: 'small', disabled: downloading.value, onClick: () => onDownload(row) }, { default: () => downloading.value ? '下载中…' : '下载' }),
      ]
      if (canReparse(row)) {
        actions.push(h(NButton, { size: 'small', disabled: reparseMutation.isPending.value, onClick: () => onReparse(row) }, { default: () => reparseMutation.isPending.value ? '提交中…' : '重解析' }))
      }
      actions.push(h(NButton, { size: 'small', type: 'error', disabled: deleteFileMutation.isPending.value, onClick: () => onDeleteFile(row) }, { default: () => '删除' }))
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

.hidden-input {
  display: none;
}

.create-dialog-card {
  max-width: 420px;
  width: min(420px, calc(100vw - 32px));
}

.create-dialog-body {
  display: grid;
  gap: 8px;
}

.field-label {
  color: var(--color-text-primary);
  font-size: 13px;
  font-weight: 600;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
