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
        <n-button @click="apiDocDialogOpen = true">接口文档</n-button>
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

    <n-modal v-model:show="apiDocDialogOpen" transform-origin="center">
      <n-card
        class="api-doc-card"
        title="行业知识库外部上传接口"
        :bordered="false"
        role="dialog"
        aria-modal="true"
      >
        <div class="api-doc-head">
          <p class="api-doc-summary">
            外部商业知识库服务可通过固定 token 上传行业资料。manager 会按行业名称自动创建或复用行业库，同名文件会覆盖旧文件。
          </p>
          <n-button type="primary" :disabled="apiDocCopyDisabled" :loading="uploadTokenLoading" @click="copyApiDocMarkdown">
            复制 Markdown
          </n-button>
        </div>

        <div class="api-doc-section">
          <h3>请求</h3>
          <p><strong>POST</strong> <code>/api/v1/external/industry-knowledge/files</code></p>
          <p>鉴权 Header：<code>X-OC-Industry-Knowledge-Token</code>，当前值：<code>{{ industryUploadTokenText }}</code>。</p>
        </div>

        <div class="api-doc-section">
          <h3>表单字段</h3>
          <ul>
            <li><code>industry_name</code>：行业名称，必填；不存在时自动创建行业库。</li>
            <li><code>file</code>：上传文件，必填；同一行业库内同名文件会覆盖。</li>
          </ul>
        </div>

        <div class="api-doc-section">
          <h3>curl 示例</h3>
          <pre class="api-doc-code">{{ industryExternalUploadCurl }}</pre>
        </div>

        <div class="api-doc-section">
          <h3>返回码</h3>
          <ul>
            <li><code>202</code>：上传成功，文件进入 RAGFlow 解析队列。</li>
            <li><code>400</code>：参数缺失、行业名称为空或请求体格式错误。</li>
            <li><code>401</code>：缺少或错误的 <code>X-OC-Industry-Knowledge-Token</code>。</li>
            <li><code>413</code>：文件大小超过平台上传限制。</li>
          </ul>
        </div>

        <template #footer>
          <div class="dialog-actions">
            <n-button @click="apiDocDialogOpen = false">关闭</n-button>
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
  useIndustryKnowledgeUploadTokenQuery,
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
const apiDocDialogOpen = ref(false)
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
const {
  data: uploadTokenConfig,
  isLoading: uploadTokenLoading,
  error: uploadTokenError,
} = useIndustryKnowledgeUploadTokenQuery()

// uploadTokenUnavailableText 说明配置缺失时的真实接口状态，避免外部服务误以为占位 token 可调用。
const uploadTokenUnavailableText = '未配置，外部上传入口禁用'
const industryUploadToken = computed(() => uploadTokenConfig.value?.upload_token ?? '')
const industryUploadTokenText = computed(() => {
  if (uploadTokenLoading.value) return '读取中...'
  if (uploadTokenError.value) return '读取失败，请刷新页面'
  return industryUploadToken.value || uploadTokenUnavailableText
})
const apiDocCopyDisabled = computed(() => uploadTokenLoading.value || Boolean(uploadTokenError.value))

// shellSingleQuote 生成可直接复制执行的 shell 单引号参数，兼容 token 中可能出现的特殊字符。
function shellSingleQuote(value: string): string {
  const escaped = value.replace(/'/g, "'\\''")
  return `'${escaped}'`
}

// industryExternalUploadCurl 是页面展示和 Markdown 文档共用的 curl 调用模板，直接内联当前配置 token。
const industryExternalUploadCurl = computed(() => `curl -i \\
  -H ${shellSingleQuote(`X-OC-Industry-Knowledge-Token: ${industryUploadTokenText.value}`)} \\
  -F "industry_name=保险" \\
  -F "file=@./policy.pdf;type=application/pdf" \\
  https://<manager-domain>/api/v1/external/industry-knowledge/files`)

// industryExternalUploadMarkdown 是复制给外部商业知识库服务方的 Markdown 接口文档。
const industryExternalUploadMarkdown = computed(() => `# 行业知识库外部上传接口

外部商业知识库服务通过固定鉴权字符串把文件上传到平台级行业知识库。manager 会按行业名称自动创建或复用行业库，同一行业库内同名文件会覆盖旧文件。

## 接口

- Method: \`POST\`
- URL: \`https://<manager-domain>/api/v1/external/industry-knowledge/files\`
- Content-Type: \`multipart/form-data\`

## 鉴权

请求必须携带 Header：

\`\`\`text
X-OC-Industry-Knowledge-Token: ${industryUploadTokenText.value}
\`\`\`

token 来自 manager 配置项 \`industry_knowledge.upload_token\`。该配置为空时外部上传入口禁用；只包含空白字符时 manager 会启动失败。

## 表单字段

| 字段 | 必填 | 说明 |
|---|---|---|
| \`industry_name\` | 是 | 行业名称。不存在时自动创建行业库；未删除行业库中名称唯一。 |
| \`file\` | 是 | 上传文件。同一行业库内同名文件会覆盖旧文件。 |

## curl 示例

\`\`\`bash
${industryExternalUploadCurl.value}
\`\`\`

## 返回码

| 状态码 | 说明 |
|---|---|
| \`202\` | 上传成功，文件已进入 RAGFlow 解析队列。 |
| \`400\` | 参数缺失、行业名称为空或请求体格式错误。 |
| \`401\` | 缺少或错误的 \`X-OC-Industry-Knowledge-Token\`。 |
| \`413\` | 文件大小超过平台上传限制。 |

## 注意事项

- 上传成功后通常先返回 \`parse_status=queued\`，解析完成后才能稳定参与检索。
- 外部上传只负责写入行业库；实例是否检索该行业库，由助手版本的行业知识库关联决定。
- 每个关联行业库都会在检索时单独召回最多 \`top_k\` 条结果，关联过多会增加上下文长度和响应成本。`)

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

async function copyApiDocMarkdown() {
  try {
    await navigator.clipboard.writeText(industryExternalUploadMarkdown.value)
    message.success('已复制 Markdown 文档')
  } catch {
    message.error('复制失败，请手动复制文档内容')
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

.api-doc-card {
  max-width: 760px;
  width: min(760px, calc(100vw - 32px));
}

.api-doc-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.api-doc-summary {
  margin: 0;
  color: var(--color-text-secondary);
  line-height: 1.7;
}

.api-doc-section {
  margin-top: 16px;
}

.api-doc-section h3 {
  margin: 0 0 8px;
  color: var(--color-text-primary);
  font-size: 14px;
}

.api-doc-section p,
.api-doc-section li {
  color: var(--color-text-secondary);
  line-height: 1.7;
}

.api-doc-code {
  overflow: auto;
  max-height: 280px;
  margin: 0;
  padding: 12px;
  border-radius: 6px;
  background: var(--color-surface-muted, #f6f7f9);
  color: var(--color-text-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
}
</style>
