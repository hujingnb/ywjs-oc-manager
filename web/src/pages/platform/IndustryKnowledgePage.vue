<template>
  <div style="display: grid; gap: 18px">
    <DataTableList
      :title="t('platform.industry.title')"
      eyebrow="Platform"
      :subtitle="t('platform.industry.subtitle')"
      :columns="baseColumns"
      :data="bases?.items ?? []"
      :loading="basesLoading"
      :error-message="basesError?.message"
      :row-key="(row: IndustryKnowledgeBase) => row.id"
    >
      <template #toolbar>
        <n-input v-model:value="keyword" :placeholder="t('platform.industry.toolbar.searchPlaceholder')" clearable style="width: 180px" />
        <n-button @click="apiDocDialogOpen = true">{{ t('platform.industry.toolbar.apiDocButton') }}</n-button>
        <n-button
          type="primary"
          :disabled="createMutation.isPending.value"
          :loading="createMutation.isPending.value"
          @click="openCreateDialog"
        >
          <template #icon><Plus :size="16" /></template>
          {{ t('platform.industry.toolbar.createButton') }}
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
          <n-button
            size="small"
            type="error"
            :disabled="!hasSelectedBaseFiles || clearFilesMutation.isPending.value"
            :loading="clearFilesMutation.isPending.value"
            @click="clearFilesDialogOpen = true"
          >
            {{ t('platform.industry.fileSection.clearButton') }}
          </n-button>
          <label class="primary-button">
            <input class="hidden-input" type="file" multiple @change="onUpload" />
            {{ t('platform.industry.fileSection.uploadButton') }}
          </label>
        </div>
      </template>

      <n-alert type="warning" :bordered="false" style="margin-bottom: 12px">
        {{ t('platform.industry.fileSection.overwriteAlert') }}
      </n-alert>
      <n-space align="center" style="margin-bottom: 12px">
        <n-input
          v-model:value="fileKeyword"
          :placeholder="t('platform.industry.fileSection.fileSearchPlaceholder')"
          clearable
          style="width: 220px"
        />
        <n-select
          v-model:value="fileStatus"
          :options="PARSE_STATUS_FILTER_OPTIONS"
          clearable
          :placeholder="t('platform.industry.fileSection.fileStatusPlaceholder')"
          style="width: 160px"
        />
        <n-date-picker
          v-model:value="createdDateRange"
          type="daterange"
          clearable
          :start-placeholder="t('platform.industry.fileSection.dateStartPlaceholder')"
          :end-placeholder="t('platform.industry.fileSection.dateEndPlaceholder')"
          style="width: 280px"
        />
      </n-space>
      <div v-if="filesLoading" class="state-text">{{ t('platform.industry.fileSection.loading') }}</div>
      <div v-else-if="filesError" class="state-text danger">{{ t('platform.industry.fileSection.queryFail', { msg: filesError.message }) }}</div>
      <n-data-table
        v-else
        :columns="fileColumns"
        :data="files?.items ?? []"
        size="small"
        :bordered="false"
        :remote="true"
        :pagination="fileTablePagination"
        :row-key="(row: KnowledgeDocument) => row.id"
      />
    </n-card>

    <n-card v-else :bordered="true">
      <div class="state-text">{{ t('platform.industry.empty') }}</div>
    </n-card>

    <n-modal v-model:show="createDialogOpen" transform-origin="center">
      <n-card
        class="create-dialog-card"
        :title="t('platform.industry.createDialog.title')"
        :bordered="false"
        role="dialog"
        aria-modal="true"
      >
        <div class="create-dialog-body">
          <span class="field-label">{{ t('platform.industry.createDialog.fieldLabel') }}</span>
          <n-input
            v-model:value="newBaseName"
            :placeholder="t('platform.industry.createDialog.placeholder')"
            clearable
            @keyup.enter="onCreateBase"
          />
        </div>
        <template #footer>
          <div class="dialog-actions">
            <n-button :disabled="createMutation.isPending.value" @click="closeCreateDialog">{{ t('common.actions.cancel') }}</n-button>
            <n-button
              type="primary"
              :disabled="createMutation.isPending.value"
              :loading="createMutation.isPending.value"
              @click="onCreateBase"
            >
              {{ t('platform.industry.createDialog.confirmButton') }}
            </n-button>
          </div>
        </template>
      </n-card>
    </n-modal>

    <n-modal v-model:show="apiDocDialogOpen" transform-origin="center">
      <n-card
        class="api-doc-card"
        :title="t('platform.industry.apiDoc.title')"
        :bordered="false"
        role="dialog"
        aria-modal="true"
      >
        <div class="api-doc-head">
          <p class="api-doc-summary">
            {{ t('platform.industry.apiDoc.summary') }}
          </p>
          <n-button type="primary" :disabled="apiDocCopyDisabled" :loading="uploadTokenLoading" @click="copyApiDocMarkdown">
            {{ t('platform.industry.apiDoc.copyMarkdownButton') }}
          </n-button>
        </div>

        <div class="api-doc-section">
          <h3>{{ t('platform.industry.apiDoc.sectionRequest') }}</h3>
          <p><strong>POST</strong> <code>/api/v1/external/industry-knowledge/files</code></p>
          <p>
            {{ t('platform.industry.apiDoc.authHeader') }}
            <code>X-OC-Industry-Knowledge-Token</code>，{{ t('platform.industry.apiDoc.authHeaderCurrentValue') }}
            <code>{{ industryUploadTokenText }}</code>。
          </p>
        </div>

        <div class="api-doc-section">
          <h3>{{ t('platform.industry.apiDoc.sectionFields') }}</h3>
          <ul>
            <li><code>industry_name</code>：{{ t('platform.industry.apiDoc.fieldIndustryName') }}</li>
            <li><code>file</code>：{{ t('platform.industry.apiDoc.fieldFile') }}</li>
          </ul>
        </div>

        <div class="api-doc-section">
          <h3>{{ t('platform.industry.apiDoc.sectionCurl') }}</h3>
          <pre class="api-doc-code">{{ industryExternalUploadCurl }}</pre>
        </div>

        <div class="api-doc-section">
          <h3>{{ t('platform.industry.apiDoc.sectionStatusCodes') }}</h3>
          <ul>
            <li><code>202</code>：{{ t('platform.industry.apiDoc.status202') }}</li>
            <li><code>400</code>：{{ t('platform.industry.apiDoc.status400') }}</li>
            <li><code>401</code>：{{ t('platform.industry.apiDoc.status401') }} <code>X-OC-Industry-Knowledge-Token</code>。</li>
            <li><code>413</code>：{{ t('platform.industry.apiDoc.status413') }}</li>
          </ul>
        </div>

        <template #footer>
          <div class="dialog-actions">
            <n-button @click="apiDocDialogOpen = false">{{ t('common.actions.close') }}</n-button>
          </div>
        </template>
      </n-card>
    </n-modal>

    <ConfirmActionModal
      :visible="clearFilesDialogOpen"
      :title="t('platform.industry.clearDialog.title')"
      :message='selectedBase ? t("platform.industry.clearDialog.message", { name: selectedBase.name }) : ""'
      :confirm-label="t('platform.industry.clearDialog.confirmLabel')"
      :busy="clearFilesMutation.isPending.value"
      :verify-value="selectedBase?.name"
      :verify-hint='selectedBase ? t("platform.industry.clearDialog.verifyHint", { name: selectedBase.name }) : ""'
      @confirm="onConfirmClearFiles"
      @cancel="clearFilesDialogOpen = false"
    />

    <RAGFlowDatasetInfoDialog
      v-if="ragflowDialogTarget"
      v-model:visible="ragflowDialogOpen"
      scope="industry"
      :target-id="ragflowDialogTarget.id"
      :target-name="ragflowDialogTarget.name"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus } from 'lucide-vue-next'
import { NAlert, NButton, NCard, NDataTable, NDatePicker, NInput, NModal, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'

import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableList from '@/components/DataTableList.vue'
import RAGFlowDatasetInfoDialog from '@/components/RAGFlowDatasetInfoDialog.vue'
import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  formatKnowledgeBytes,
  type KnowledgeDocument,
} from '@/api/hooks/useKnowledge'
import {
  downloadIndustryKnowledgeFile,
  useClearIndustryKnowledgeFiles,
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
import zhPlatform from '@/i18n/locales/zh/platform'
import { canManageRAGFlowDatasetInfo } from '@/domain/permissions'
import { PARSE_STATUS_FILTER_OPTIONS, parseStatusLabel, parseStatusTagType } from '@/domain/parseStatus'
import { useAuthStore } from '@/stores/auth'
import { useUploadProgressStore } from '@/stores/uploadProgress'
import {
  filterKnowledgeUploadFiles,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from '@/pages/knowledge/knowledgeUploadBatch'

// IndustryKnowledgePage 是平台管理员管理行业知识库和库内文件的页面。
const { t } = useI18n()
const message = useMessage()
const auth = useAuthStore()
const uploadProgress = useUploadProgressStore()

const keyword = ref('')
const newBaseName = ref('')
const createDialogOpen = ref(false)
const apiDocDialogOpen = ref(false)
const clearFilesDialogOpen = ref(false)
const ragflowDialogOpen = ref(false)
const ragflowDialogTarget = ref<IndustryKnowledgeBase | null>(null)
const selectedBaseId = ref<string | undefined>(undefined)
const downloading = ref(false)
const fileKeyword = ref('')
const normalizedFileKeyword = computed(() => fileKeyword.value.trim())
// fileStatus 为行业库文件「解析状态」筛选值，null/空＝不过滤（全部状态）。
const fileStatus = ref<string | null>(null)
const normalizedFileStatus = computed(() => fileStatus.value ?? undefined)
const createdDateRange = ref<[number, number] | null>(null)
const createdFrom = computed(() => createdDateRange.value ? formatDatePickerDay(createdDateRange.value[0]) : undefined)
const createdTo = computed(() => createdDateRange.value ? formatDatePickerDay(createdDateRange.value[1]) : undefined)
const filePage = ref(1)
const filePageSize = ref(50)

const { data: bases, isLoading: basesLoading, error: basesError } = useIndustryKnowledgeBasesQuery(undefined, keyword)
const selectedBase = computed(() => (bases.value?.items ?? []).find(item => item.id === selectedBaseId.value) ?? null)
const selectedBaseIdRef = computed(() => selectedBase.value?.id)
const { data: files, isLoading: filesLoading, error: filesError } = useIndustryKnowledgeFilesQuery(selectedBaseIdRef, {
  page: filePage,
  pageSize: filePageSize,
  keyword: normalizedFileKeyword,
  status: normalizedFileStatus,
  createdFrom,
  createdTo,
})

const createMutation = useCreateIndustryKnowledgeBase()
const renameMutation = useRenameIndustryKnowledgeBase()
const deleteBaseMutation = useDeleteIndustryKnowledgeBase()
const uploadMutation = useUploadIndustryKnowledgeFile(selectedBaseIdRef)
const deleteFileMutation = useDeleteIndustryKnowledgeFile(selectedBaseIdRef)
const clearFilesMutation = useClearIndustryKnowledgeFiles(selectedBaseIdRef)
const reparseMutation = useReparseIndustryKnowledgeFile(selectedBaseIdRef)
const {
  data: uploadTokenConfig,
  isLoading: uploadTokenLoading,
  error: uploadTokenError,
} = useIndustryKnowledgeUploadTokenQuery()

const industryUploadToken = computed(() => uploadTokenConfig.value?.upload_token ?? '')
const industryUploadTokenText = computed(() => {
  if (uploadTokenLoading.value) return t('platform.industry.apiDoc.tokenLoading')
  if (uploadTokenError.value) return t('platform.industry.apiDoc.tokenError')
  return industryUploadToken.value || t('platform.industry.apiDoc.tokenMissing')
})
const apiDocCopyDisabled = computed(() => uploadTokenLoading.value || Boolean(uploadTokenError.value))
// canManageRAGFlowInfo 控制远端 dataset 运维入口；后端接口仍做最终平台管理员校验。
const canManageRAGFlowInfo = computed(() => canManageRAGFlowDatasetInfo(auth.user))
// hasSelectedBaseFiles 控制清空入口，避免空行业库触发破坏性请求。
const hasSelectedBaseFiles = computed(() => (files.value?.total ?? selectedBase.value?.document_count ?? 0) > 0)
// fileTablePagination 使用后端 total 驱动远程分页；筛选条件变化时回到第一页避免停在空页。
const fileTablePagination = computed(() => ({
  page: filePage.value,
  pageSize: filePageSize.value,
  itemCount: files.value?.total ?? 0,
  showSizePicker: true,
  pageSizes: [10, 20, 50, 100],
  prefix: () => t('platform.industry.fileSection.fileCountPrefix', { n: files.value?.total ?? 0 }),
  onUpdatePage: (nextPage: number) => {
    filePage.value = nextPage
  },
  onUpdatePageSize: (nextPageSize: number) => {
    filePageSize.value = nextPageSize
    filePage.value = 1
  },
}))

// shellSingleQuote 生成可直接复制执行的 shell 单引号参数，兼容 token 中可能出现的特殊字符。
function shellSingleQuote(value: string): string {
  const escaped = value.replace(/'/g, "'\\''")
  return `'${escaped}'`
}

// industryExternalUploadCurl 是页面展示和 Markdown 文档共用的 curl 调用模板，直接内联当前配置 token。
// industry_name 示例值取自 zh 语言包，与正文一致（此处直接读对象绕过 vue-i18n 编译器限制）。
const industryExternalUploadCurl = computed(() => `curl -i \\
  -H ${shellSingleQuote(`X-OC-Industry-Knowledge-Token: ${industryUploadTokenText.value}`)} \\
  -F "industry_name=${zhPlatform.industry.apiDoc.curlExampleIndustryName}" \\
  -F "file=@./policy.pdf;type=application/pdf" \\
  https://<manager-domain>/api/v1/external/industry-knowledge/files`)

// industryExternalUploadMarkdown 是复制给外部商业知识库服务方的 Markdown 接口文档。
// Markdown 内含 Pipe 表格语法，会被 vue-i18n 消息编译器误判为复数分隔符；因此直接从 zh 语言包
// 读取原始模板字符串，用 replace() 注入当前 token 与 curl 示例，绕过 vue-i18n 编译器。
const industryExternalUploadMarkdown = computed(() =>
  zhPlatform.industry.apiDoc.apiDocMarkdown
    .replace('{uploadToken}', industryUploadTokenText.value)
    .replace('{curlExample}', industryExternalUploadCurl.value)
)

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

watch([selectedBaseIdRef, normalizedFileKeyword, normalizedFileStatus, createdFrom, createdTo], () => {
  filePage.value = 1
})

// formatDatePickerDay 把 Naive DatePicker 的本地毫秒时间戳转换成后端接受的 YYYY-MM-DD 日期。
function formatDatePickerDay(value: number): string {
  const date = new Date(value)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
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

// openRAGFlowInfo 打开当前行业库的 RAGFlow dataset 信息弹框，不触发 dataset 懒创建。
function openRAGFlowInfo(row: IndustryKnowledgeBase) {
  ragflowDialogTarget.value = row
  ragflowDialogOpen.value = true
}

async function onCreateBase() {
  const name = newBaseName.value.trim()
  if (!name) {
    message.warning(t('platform.industry.createDialog.emptyWarning'))
    return
  }
  try {
    const created = await createMutation.mutateAsync(name)
    selectedBaseId.value = created.id
    newBaseName.value = ''
    createDialogOpen.value = false
    message.success(t('platform.industry.createDialog.successMsg', { name: created.name }))
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('platform.industry.createDialog.failMsg'))
  }
}

async function copyApiDocMarkdown() {
  try {
    await navigator.clipboard.writeText(industryExternalUploadMarkdown.value)
    message.success(t('platform.industry.apiDoc.copySuccess'))
  } catch {
    message.error(t('platform.industry.apiDoc.copyFail'))
  }
}

async function onRenameBase(row: IndustryKnowledgeBase) {
  const name = window.prompt(t('platform.industry.baseActions.renamePrompt'), row.name)?.trim()
  if (!name || name === row.name) return
  try {
    const renamed = await renameMutation.mutateAsync({ id: row.id, name })
    message.success(t('platform.industry.baseActions.renameSuccess', { name: renamed.name }))
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('platform.industry.baseActions.renameFail'))
  }
}

async function onDeleteBase(row: IndustryKnowledgeBase) {
  if (!window.confirm(t('platform.industry.baseActions.deleteConfirm', { name: row.name }))) return
  try {
    await deleteBaseMutation.mutateAsync(row.id)
    message.success(t('platform.industry.baseActions.deleteSuccess', { name: row.name }))
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('platform.industry.baseActions.deleteFail'))
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
    message.warning(err instanceof Error ? err.message : t('platform.industry.uploadConflict'))
  }
}

async function onDownload(row: KnowledgeDocument) {
  if (!selectedBase.value) return
  downloading.value = true
  try {
    await downloadIndustryKnowledgeFile(selectedBase.value.id, row.id, row.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('platform.industry.fileActions.downloadFail'))
  } finally {
    downloading.value = false
  }
}

async function onDeleteFile(row: KnowledgeDocument) {
  if (!window.confirm(t('platform.industry.fileActions.deleteConfirm', { name: row.name }))) return
  await deleteFileMutation.mutateAsync(row.id)
}

// onConfirmClearFiles 清空当前行业库全部文件，后端保留行业库记录和版本关联。
async function onConfirmClearFiles() {
  if (!selectedBase.value) return
  try {
    const baseName = selectedBase.value.name
    await clearFilesMutation.mutateAsync()
    clearFilesDialogOpen.value = false
    message.success(t('platform.industry.clearDialog.successMsg', { name: baseName }))
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('platform.industry.clearDialog.failMsg'))
  }
}

async function onReparse(row: KnowledgeDocument) {
  await reparseMutation.mutateAsync(row.id)
}

// baseColumns 随语言响应式切换，转为 computed。
const baseColumns = computed<DataTableColumns<IndustryKnowledgeBase>>(() => [
  {
    title: t('platform.industry.baseColumns.name'),
    key: 'name',
    render: row => h('strong', row.name),
  },
  { title: t('platform.industry.baseColumns.docCount'), key: 'document_count', render: row => String(row.document_count ?? 0) },
  { title: t('platform.industry.baseColumns.updatedAt'), key: 'updated_at', render: row => formatTime(row.updated_at) },
  {
    title: t('common.table.actions'),
    key: 'actions',
    render: row => {
      const actions = [
        h(NButton, { size: 'small', type: selectedBaseId.value === row.id ? 'primary' : 'default', onClick: () => { selectedBaseId.value = row.id } }, { default: () => t('platform.industry.baseActions.files') }),
      ]
      if (canManageRAGFlowInfo.value) {
        actions.push(h(NButton, { size: 'small', onClick: () => openRAGFlowInfo(row) }, { default: () => t('platform.industry.baseActions.ragflow') }))
      }
      actions.push(
        h(NButton, { size: 'small', onClick: () => onRenameBase(row) }, { default: () => t('platform.industry.baseActions.rename') }),
        h(NButton, { size: 'small', type: 'error', disabled: deleteBaseMutation.isPending.value, onClick: () => onDeleteBase(row) }, { default: () => t('platform.industry.baseActions.delete') }),
      )
      return h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions)
    },
  },
])

// fileColumns 随语言响应式切换，转为 computed。
const fileColumns = computed<DataTableColumns<KnowledgeDocument>>(() => [
  { title: t('platform.industry.fileColumns.name'), key: 'name', render: row => h('strong', row.name) },
  { title: t('platform.industry.fileColumns.size'), key: 'size', render: row => formatKnowledgeBytes(row.size) },
  { title: t('platform.industry.fileColumns.type'), key: 'type', render: row => row.suffix || row.mime_type || '—' },
  {
    title: t('platform.industry.fileColumns.parseStatus'),
    key: 'parse_status',
    render: row => h('div', { style: 'display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
      h(NTag, { type: parseStatusTagType(row.parse_status), size: 'small', bordered: false }, { default: () => parseStatusLabel(row.parse_status) }),
      row.parse_status === 'running' ? h('span', { class: 'state-text', style: 'margin: 0; font-size: 12px' }, `${row.progress}%`) : null,
      row.last_error ? h('span', { style: 'color: var(--color-danger); font-size: 12px' }, row.last_error) : null,
    ]),
  },
  { title: t('platform.industry.fileColumns.createdAt'), key: 'created_at', render: row => formatTime(row.created_at) },
  {
    title: t('common.table.actions'),
    key: 'actions',
    render: row => {
      const actions = [
        h(NButton, { size: 'small', disabled: downloading.value, onClick: () => onDownload(row) }, { default: () => downloading.value ? t('platform.industry.fileActions.downloading') : t('platform.industry.fileActions.download') }),
      ]
      if (canReparse(row)) {
        actions.push(h(NButton, { size: 'small', disabled: reparseMutation.isPending.value, onClick: () => onReparse(row) }, { default: () => reparseMutation.isPending.value ? t('platform.industry.fileActions.reparsing') : t('platform.industry.fileActions.reparse') }))
      }
      actions.push(h(NButton, { size: 'small', type: 'error', disabled: deleteFileMutation.isPending.value, onClick: () => onDeleteFile(row) }, { default: () => t('platform.industry.fileActions.delete') }))
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
