<template>
  <div class="cron-tab">
    <!-- 工具栏：调度器摘要 / 搜索 / 状态筛选 / 刷新 / 新建任务。 -->
    <n-card :bordered="true" class="toolbar-card">
      <n-space align="center" :size="8">
        <span class="status-summary">{{ statusSummary }}</span>
        <n-input v-model:value="search" size="small" placeholder="搜索定时任务" style="width: 200px" />
        <n-select
          v-model:value="statusFilter"
          :options="statusOptions"
          size="small"
          style="width: 150px"
        />
        <span class="spacer" />
        <n-button size="small" tertiary @click="refreshAll">刷新</n-button>
        <n-button
          v-if="canWrite"
          class="create-cron-btn"
          size="small"
          type="primary"
          @click="onCreateClick"
        >+ 新建任务</n-button>
      </n-space>
    </n-card>

    <!-- stub 镜像降级提示：后端以 CRON_NOT_SUPPORTED_ON_STUB 标识当前实例无 oc-cron。 -->
    <n-card v-if="isStubInstance" :bordered="true">
      <n-empty description="该实例运行的是本地 dev 镜像，定时任务不可用；切换到生产镜像后该功能自动启用。" />
    </n-card>

    <!-- 左右分屏：左侧任务列表 + 右侧详情、历史和输出。 -->
    <div v-else class="split">
      <div class="list-col">
        <p v-if="jobsQuery.isLoading.value" class="state-text">加载中…</p>
        <p v-else-if="jobsQuery.error.value" class="state-text danger">{{ errorText }}</p>
        <CronJobList
          v-else
          :jobs="jobsQuery.data.value ?? []"
          :selected-id="selectedJobId"
          @select="onSelectJob"
        />
      </div>
      <div class="detail-col">
        <p v-if="jobQuery.error.value" class="state-text danger">{{ jobQuery.error.value.message }}</p>
        <CronJobDetail
          v-else
          :job="selectedJob"
          :history="historyQuery.data.value ?? []"
          :output="outputQuery.data.value ?? null"
          :selected-file="selectedOutputFile"
          :is-platform-admin="isPlatformAdmin"
          :can-write="canWrite"
          @action="onAction"
          @edit="onEdit"
          @select-output="onSelectOutput"
        />
      </div>
    </div>

    <CronJobFormModal
      v-model:show="showForm"
      :submitting="formSubmitting"
      :job="editingJob"
      :is-platform-admin="canShowAdvancedFields"
      @submit="onSubmitForm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NCard, NEmpty, NInput, NSelect, NSpace, useMessage } from 'naive-ui'

import {
  useCreateCronJob,
  useCronCapabilitiesQuery,
  useCronHistoryQuery,
  useCronJobAction,
  useCronJobQuery,
  useCronJobsQuery,
  useCronOutputQuery,
  useCronStatusQuery,
  useUpdateCronJob,
  type CreateCronJobRequest,
  type CronJob,
  type CronJobAction,
  type CronJobFilters,
  type UpdateCronJobRequest,
  type UpdateCronJobVariables,
} from '@/api/hooks/useCron'
import type { ApiError } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import CronJobDetail from './cron/CronJobDetail.vue'
import CronJobFormModal from './cron/CronJobFormModal.vue'
import CronJobList from './cron/CronJobList.vue'

// AppCronTab 是实例 Hermes Cron 管理页，负责数据查询、URL query 同步和写操作编排。
const props = defineProps<{
  // appId 由 router props: true 从 /apps/:appId/cron 注入。
  appId: string
}>()

const appId = computed(() => props.appId)
const route = useRoute()
const router = useRouter()
const message = useMessage()
const auth = useAuthStore()

// search 和 statusFilter 控制列表查询；后端支持 q/status，页面保持本地状态即可。
const search = ref('')
const statusFilter = ref('')
const showForm = ref(false)
const editingJob = ref<CronJob | null>(null)

// normalizeQueryValue 把 Vue Router 的 string | string[] | null 规整为首个非空字符串。
function normalizeQueryValue(value: unknown): string | undefined {
  if (typeof value === 'string') return value || undefined
  if (Array.isArray(value)) {
    return value.find((item): item is string => typeof item === 'string' && item !== '')
  }
  return undefined
}

// selectedJobId / selectedOutputFile 来自 URL query，支持刷新后恢复详情和输出选择。
const selectedJobId = computed(() => normalizeQueryValue(route.query.job))
const selectedOutputFile = computed(() => normalizeQueryValue(route.query.file))

// filters 是 useCronJobsQuery 的响应式筛选条件；all=true 保留暂停/禁用等非活跃任务。
const filters = computed<CronJobFilters>(() => ({
  q: search.value.trim(),
  status: statusFilter.value,
  all: true,
}))

const capabilitiesQuery = useCronCapabilitiesQuery(appId)
const statusQuery = useCronStatusQuery(appId)
const jobsQuery = useCronJobsQuery(appId, filters)
const jobQuery = useCronJobQuery(appId, selectedJobId)
const historyQuery = useCronHistoryQuery(appId, selectedJobId)
const outputQuery = useCronOutputQuery(appId, selectedJobId, selectedOutputFile)
const createMutation = useCreateCronJob(appId)
const updateMutation = useUpdateCronJob(appId)
const actionMutation = useCronJobAction(appId)

// cronFeatures 为 undefined 表示能力未知，只有明确 false 才隐藏对应 UI。
const cronFeatures = computed(() => capabilitiesQuery.data.value?.features)
const canWrite = computed(() => cronFeatures.value?.write !== false)
const isPlatformAdmin = computed(() => Boolean(auth.isPlatformAdmin))
const canShowAdvancedFields = computed(() =>
  isPlatformAdmin.value && cronFeatures.value?.advanced_fields !== false,
)
const formSubmitting = computed(() => createMutation.isPending.value || updateMutation.isPending.value)

// selectedJob 优先使用详情接口返回的权威数据，详情未返回时退回列表行以减少空白闪烁。
const selectedJob = computed<CronJob | null>(() => {
  if (jobQuery.isLoading.value) return null
  return jobQuery.data.value
    ?? jobsQuery.data.value?.find((job) => job.id === selectedJobId.value)
    ?? null
})

const statusOptions = [
  { label: '全部状态', value: '' },
  { label: 'scheduled', value: 'scheduled' },
  { label: 'paused', value: 'paused' },
  { label: 'running', value: 'running' },
  { label: 'disabled', value: 'disabled' },
  { label: 'error', value: 'error' },
]

// statusSummary 按产品要求保留英文 Gateway cron running 文案，后接关键运行指标。
const statusSummary = computed(() => {
  const status = statusQuery.data.value
  if (!status) return 'Cron status unknown'
  const base = status.gateway_running ? 'Gateway cron running' : 'Gateway cron stopped'
  const active = typeof status.active_jobs === 'number' ? ` · ${status.active_jobs} active` : ''
  const next = status.next_run_at ? ` · next ${status.next_run_at}` : ''
  return `${base}${active}${next}`
})

// isStubError 必须看 ApiError.body.code，message 可能被本地化或包装。
function isStubError(err: unknown): boolean {
  const body = (err as ApiError | null | undefined)?.body
  if (body && typeof body === 'object' && 'code' in body) {
    return (body as { code: string }).code === 'CRON_NOT_SUPPORTED_ON_STUB'
  }
  return false
}

// 任一核心查询返回 stub sentinel 即降级整页，避免继续展示半残缺操作区。
const isStubInstance = computed(() =>
  isStubError(jobsQuery.error.value)
  || isStubError(statusQuery.error.value)
  || isStubError(capabilitiesQuery.error.value),
)

const errorText = computed(() => String(jobsQuery.error.value?.message ?? '加载失败'))

// replaceQuery 只保留字符串 query，并用 undefined 删除 job/file，防止 URL 残留过期输出。
function replaceQuery(patch: Record<string, string | undefined>) {
  const query: Record<string, string> = {}
  for (const [key, value] of Object.entries(route.query)) {
    const normalized = normalizeQueryValue(value)
    if (normalized) query[key] = normalized
  }
  for (const [key, value] of Object.entries(patch)) {
    if (value) {
      query[key] = value
    } else {
      delete query[key]
    }
  }
  void router.replace({ query })
}

// refreshAll 手动刷新能力/状态/列表/详情相关查询，供用户排查运行时延迟。
function refreshAll() {
  void capabilitiesQuery.refetch()
  void statusQuery.refetch()
  void jobsQuery.refetch()
  void jobQuery.refetch()
  void historyQuery.refetch()
  void outputQuery.refetch()
}

// onSelectJob 把选中任务写入 URL，并清空旧输出文件选择。
function onSelectJob(jobId: string) {
  replaceQuery({ job: jobId, file: undefined })
}

// onSelectOutput 把历史输出文件写入 URL，output query 随之读取内容。
function onSelectOutput(fileName: string) {
  replaceQuery({ file: fileName })
}

// onCreateClick 打开空表单；编辑状态必须清空，避免复用上一个 job。
function onCreateClick() {
  editingJob.value = null
  showForm.value = true
}

// onEdit 使用当前详情作为编辑初值；未选中任务时忽略。
function onEdit() {
  if (!selectedJob.value) return
  editingJob.value = selectedJob.value
  showForm.value = true
}

// onSubmitForm 根据 editingJob 决定 create/update，成功后关闭弹窗并刷新选中态。
async function onSubmitForm(payload: CreateCronJobRequest | UpdateCronJobRequest) {
  try {
    if (editingJob.value?.id) {
      await updateMutation.mutateAsync({
        ...(payload as UpdateCronJobRequest),
        jobId: editingJob.value.id,
      } satisfies UpdateCronJobVariables)
      message.success('定时任务已更新')
    } else {
      const job = await createMutation.mutateAsync(payload as CreateCronJobRequest)
      if (job?.id) replaceQuery({ job: job.id, file: undefined })
      message.success('定时任务已创建')
    }
    showForm.value = false
    editingJob.value = null
  } catch (e) {
    message.error(e instanceof Error ? e.message : '保存失败')
  }
}

// onAction 统一处理 run/pause/resume/delete；delete 先弹原生确认框。
async function onAction(verb: CronJobAction['verb']) {
  if (actionMutation.isPending.value) return
  const jobId = selectedJobId.value
  if (!jobId) return
  if (verb === 'delete' && !window.confirm('确定要删除这个定时任务吗？')) return

  try {
    await actionMutation.mutateAsync({ verb, jobId } as CronJobAction)
    if (verb === 'delete') {
      replaceQuery({ job: undefined, file: undefined })
    }
    message.success('操作成功')
  } catch (e) {
    message.error(e instanceof Error ? e.message : '操作失败')
  }
}
</script>

<style scoped>
.cron-tab {
  display: grid;
  gap: 12px;
}
.toolbar-card :deep(.n-card__content) {
  padding: 10px 14px;
}
.status-summary {
  color: var(--color-text-primary, #1f2329);
  font-size: 12px;
  white-space: nowrap;
}
.spacer {
  flex: 1;
}
.split {
  display: grid;
  grid-template-columns: 420px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
}
.state-text {
  color: var(--color-text-secondary, #6b7280);
  font-size: 13px;
}
.danger {
  color: var(--color-danger, #d93026);
}
@media (max-width: 1200px) {
  .split {
    grid-template-columns: 1fr;
  }
}
</style>
