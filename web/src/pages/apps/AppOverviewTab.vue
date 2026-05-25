<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Overview</p>
        <h2 style="margin: 0">概览</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button
        type="primary"
        :disabled="!canRetryInit || initMutation.isPending.value"
        @click="onRetryInit"
      >
        {{ initMutation.isPending.value ? '提交中…' : '重新初始化' }}
      </n-button>
    </template>

    <p v-if="!app" class="state-text">尚未加载实例信息</p>
    <n-descriptions v-else :column="2" bordered size="small">
      <n-descriptions-item label="状态">
        <AppStatusTag :status="app.status" />
        <!-- 初始化 5 个子状态时额外展示进度条:total>0 走百分比,total=0 走不定进度条 -->
        <div v-if="isInitPhase(app.status)" class="init-progress">
          <n-progress
            type="line"
            :percentage="initPercentage"
            indicator-placement="inside"
            :processing="initIndeterminate"
          />
          <span v-if="!initIndeterminate" class="init-progress-bytes">
            {{ formatBytes(app.progress_current) }} / {{ formatBytes(app.progress_total) }}
          </span>
        </div>
        <!-- error 状态附加显示最近失败阶段的中文文案及具体错误原因 -->
        <div v-if="app.status === 'error' && app.last_error_status" class="init-failure">
          <span>在「{{ formatAppStatus(app.last_error_status).label }}」阶段失败</span>
          <span v-if="app.last_error_message" class="init-failure-reason">{{ app.last_error_message }}</span>
        </div>
      </n-descriptions-item>
      <n-descriptions-item label="API key">
        <n-space align="center" :size="8">
          <n-tag :type="keyTagType(app.api_key_status)" size="small" :bordered="false">
            {{ apiKeyLabel(app.api_key_status) }}
          </n-tag>
          <!-- 仅保留「恢复」入口：UI 不再提供主动禁用 API key 的能力，避免用户误操作把仍在使用的
               实例 key 关停；若历史/外部流程已把 key 置为 disabled，组织管理员仍可在此恢复。 -->
          <n-button
            v-if="canToggleKey && app.api_key_status === 'disabled'"
            size="small"
            :disabled="keyMutation.isPending.value"
            @click="onRestoreKey"
          >
            恢复
          </n-button>
        </n-space>
      </n-descriptions-item>
      <n-descriptions-item label="容器 ID">
        <code>{{ app.container_id || '—' }}</code>
      </n-descriptions-item>
      <n-descriptions-item label="Runtime Node">
        <code>{{ app.runtime_node_id || '—' }}</code>
      </n-descriptions-item>
      <!-- 助手版本：展示绑定的版本名，version_synced=false 时附加需重启标签，组织管理员可切换 -->
      <n-descriptions-item label="助手版本">
        <n-space align="center" :size="8">
          <span>{{ versionName }}</span>
          <!-- version_synced 为 false 时提示需重启，与实例列表的需重启提示视觉一致 -->
          <n-tag v-if="app.version_synced === false" type="warning" size="small" :bordered="false">需重启</n-tag>
          <!-- 切换按钮仅对有应用管理权限且可查看版本目录的用户展示 -->
          <n-button
            v-if="canSwitchAppVersion(auth.user, app) && canViewVersions"
            size="small"
            @click="openSwitchVersionModal"
          >
            切换
          </n-button>
          <!-- 立即重启按钮：version_synced=false 且实例正在运行时，提供本页直接重启入口，
               避免用户为了同步镜像还要切换到运行时 tab。restart job 后端已实现镜像变更
               自动重建分支（worker/handlers/app_runtime_ops.go 的 Handle 镜像变更分支），
               因此这里直接复用 restart 操作即可生效。 -->
          <n-button
            v-if="canRestartForVersionSync"
            size="small"
            type="primary"
            :disabled="restartMutation.isPending.value"
            @click="onRestartForVersionSync"
          >
            {{ restartMutation.isPending.value ? '提交中…' : '立即重启' }}
          </n-button>
        </n-space>
      </n-descriptions-item>
      <n-descriptions-item label="所属组织">
        {{ organizationName }}
      </n-descriptions-item>
      <n-descriptions-item v-if="app.description" label="描述" :span="2">
        {{ app.description }}
      </n-descriptions-item>
      <!-- runtime_image_ref / sha256 由后端仅对平台管理员填充，非管理员字段恒为空，此处不渲染 -->
      <n-descriptions-item v-if="auth.isPlatformAdmin && app.runtime_image_ref" label="镜像引用" :span="2">
        <code>{{ app.runtime_image_ref }}</code>
      </n-descriptions-item>
      <n-descriptions-item v-if="auth.isPlatformAdmin && app.runtime_image_sha256" label="镜像 Digest" :span="2">
        <code>{{ app.runtime_image_sha256 }}</code>
      </n-descriptions-item>
    </n-descriptions>

    <p v-if="initFeedback" class="state-text" :class="{ danger: initError }" style="margin-top: 8px">{{ initFeedback }}</p>
    <p v-if="keyFeedback" class="state-text" :class="{ danger: keyError }" style="margin-top: 8px">{{ keyFeedback }}</p>
    <p v-if="versionFeedback" class="state-text" :class="{ danger: versionError }" style="margin-top: 8px">{{ versionFeedback }}</p>
    <!-- 立即重启的反馈与版本切换、初始化反馈走相同样式，便于用户在同一卡片内捕获最近一次操作结果 -->
    <p v-if="restartFeedback" class="state-text" :class="{ danger: restartError }" style="margin-top: 8px">{{ restartFeedback }}</p>

    <JobProgressPanel
      v-if="trackingJobId"
      :title="trackingJobTitle"
      :subtitle="trackingJobId"
      :job="trackedJob ?? undefined"
      style="margin-top: 12px"
    />

    <!-- 切换助手版本弹窗：从组织 allowlist 与版本目录交集中选择目标版本 -->
    <n-modal v-model:show="showSwitchVersionModal" preset="card" title="切换助手版本" style="width: 420px">
      <n-select
        v-model:value="selectedVersionId"
        :options="versionOptions"
        :loading="versionsQuery.isLoading.value"
        placeholder="请选择助手版本"
        style="margin-bottom: 16px"
      />
      <n-space justify="end">
        <n-button @click="showSwitchVersionModal = false">取消</n-button>
        <n-button
          type="primary"
          :loading="switchVersionMutation.isPending.value"
          :disabled="!selectedVersionId || switchVersionMutation.isPending.value"
          @click="onConfirmSwitchVersion"
        >
          确认切换
        </n-button>
      </n-space>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'
import { NButton, NCard, NDescriptions, NDescriptionsItem, NModal, NProgress, NSelect, NSpace, NTag, type SelectOption } from 'naive-ui'

import {
  useInitializeAppMutation,
  useJobQuery,
  useSwitchAppVersion,
  useToggleAppAPIKey,
  useTriggerRuntimeOperation,
  type AppDTO,
} from '@/api/hooks/useApps'
import { useAssistantVersionsQuery } from '@/api/hooks/useAssistantVersions'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
import AppStatusTag from '@/components/AppStatusTag.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'
import { canManageApp, canSwitchAppVersion, canTriggerRuntimeOperation } from '@/domain/permissions'
import { formatAppStatus, isInitPhase } from '@/domain/status'
import { useAuthStore } from '@/stores/auth'

// AppOverviewTab 展示应用基础信息，并提供初始化重试和 API key 启停入口。
const props = defineProps<{ appId: string }>()
const appId = computed<string | undefined>(() => props.appId)

const app = inject<Ref<AppDTO | null>>('app')
const auth = useAuthStore()
// orgId 只用于展示组织名称；权限和业务 API 仍继续使用 app.org_id。
const orgId = computed<string | undefined>(() => app?.value?.org_id)
const organizationQuery = useOrganizationQuery(orgId)
const organizationName = computed(() => organizationQuery.data.value?.name || '未知组织')

// canViewVersions：三角色均可查看助手版本目录（CanViewAssistantVersion 已扩展至 org_member）。
const canViewVersions = computed(() => !!auth.user)

// 仅在有权限时拉取助手版本目录，避免普通成员触发 403。
const versionsQuery = useAssistantVersionsQuery(() => canViewVersions.value)

// versionOptions 取组织 allowlist 与全量版本目录的交集，仅展示本组织允许使用的版本。
const versionOptions = computed<SelectOption[]>(() => {
  const org = organizationQuery.data.value
  const versions = versionsQuery.data.value
  if (!org || !versions) return []
  const allowedIds = new Set(org.assistant_version_ids ?? [])
  return versions
    .filter(v => allowedIds.has(v.id))
    .map(v => ({ label: v.name, value: v.id }))
})

// versionName 根据 version_id 从版本目录反查版本名称；目录不可用时回退到 id 原值或 '—'。
const versionName = computed(() => {
  const id = app?.value?.version_id
  if (!id) return '—'
  const found = versionsQuery.data.value?.find(v => v.id === id)
  return found ? found.name : id
})

// 切换版本 modal 的开关与选中版本 ref。
const showSwitchVersionModal = ref(false)
const selectedVersionId = ref<string | null>(null)

// openSwitchVersionModal 打开切换版本弹窗，并预选当前绑定的版本。
function openSwitchVersionModal() {
  selectedVersionId.value = app?.value?.version_id ?? null
  showSwitchVersionModal.value = true
}

const switchVersionMutation = useSwitchAppVersion(appId)
const versionFeedback = ref('')
const versionError = ref(false)

// onConfirmSwitchVersion 提交版本切换请求，成功关闭弹窗并展示需重启提示；失败展示错误文案。
async function onConfirmSwitchVersion() {
  if (!selectedVersionId.value) return
  versionFeedback.value = ''
  versionError.value = false
  try {
    await switchVersionMutation.mutateAsync(selectedVersionId.value)
    showSwitchVersionModal.value = false
    versionFeedback.value = '已切换助手版本，重启实例后生效'
  } catch (err: unknown) {
    versionError.value = true
    versionFeedback.value = err instanceof Error ? err.message : '切换版本失败'
  }
}

// restartMutation 与运行时 tab 共用同一个后端入口（POST /apps/:id/runtime/restart）；
// 后端 restart handler 在检测到 image_ref 变化时会自动停旧容器 + 入队 app_initialize
// 重建容器，因此这里无需新增专用 API。
const restartMutation = useTriggerRuntimeOperation(appId)
const restartFeedback = ref('')
const restartError = ref(false)

// canRestartForVersionSync 控制「立即重启」按钮的展示：
//   1) 必须有触发运行时操作的权限（与 AppRuntimeTab 的启停按钮口径一致）；
//   2) version_synced=false，即当前确实存在「需重启」的同步需求；
//   3) 实例状态为 running 或 binding_waiting，与 AppRuntimeTab.canStop 一致——
//      只有跑着容器的实例才能走 restart 路径；stopped/error/draft 等状态用户应当
//      去运行时 tab 的「启动」或概览页的「重新初始化」走对应入口。
const canRestartForVersionSync = computed(() => {
  if (!app?.value) return false
  if (!canTriggerRuntimeOperation(auth.user, app.value)) return false
  if (app.value.version_synced !== false) return false
  const status = app.value.status
  return status === 'running' || status === 'binding_waiting'
})

// onRestartForVersionSync 提交 restart 任务，并复用 trackingJobId 让 JobProgressPanel
// 接管进度展示；与 AppRuntimeTab 的 restart 流程实质等价，只是入口前移到概览卡片。
async function onRestartForVersionSync() {
  restartFeedback.value = ''
  restartError.value = false
  try {
    const result = await restartMutation.mutateAsync('restart')
    trackingJobId.value = result.job_id
    trackingJobTitle.value = '重启任务'
    restartFeedback.value = `已提交重启任务：${result.job_id}`
  } catch (err: unknown) {
    restartError.value = true
    restartFeedback.value = err instanceof Error ? err.message : '重启失败'
  }
}

const initMutation = useInitializeAppMutation(appId)
// trackingJobId 记录最近一次后台任务，供 JobProgressPanel 轮询展示执行进度。
const trackingJobId = ref<string | undefined>()
const trackingJobTitle = ref('后台任务')
const jobIdRef = computed<string | undefined>(() => trackingJobId.value)
const jobQuery = useJobQuery(jobIdRef)
const trackedJob = computed(() => jobQuery.data.value ?? null)

// canRetryInit 仅允许可管理用户在草稿或错误状态重新提交初始化任务。
const canRetryInit = computed(() => {
  const status = app?.value?.status
  return canManageApp(auth.user, app?.value) && (status === 'error' || status === 'draft')
})

// initPercentage 把 progress_current / progress_total 折算为 0~100 整数;
// total<=0 时返回 0,真正的不定状态由 initIndeterminate 控制。
const initPercentage = computed(() => {
  const total = app?.value?.progress_total ?? 0
  const current = app?.value?.progress_current ?? 0
  if (total <= 0) return 0
  return Math.min(100, Math.round((current / total) * 100))
})

// initIndeterminate 表示当前阶段总量未知,UI 应走不定进度条(processing=true)避免误导。
const initIndeterminate = computed(() => {
  const total = app?.value?.progress_total ?? 0
  return total <= 0
})

// formatBytes 把字节展示为人类可读的 B / KB / MB / GB,本地实现避免引入额外依赖。
// 同时兼容 progress_current 在非字节单位下被复用的场景(目前 init 阶段全部按字节)。
function formatBytes(n: number | null | undefined): string {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

const initFeedback = ref('')
const initError = ref(false)

// onRetryInit 提交初始化任务，成功后切换到任务跟踪视图，失败时保留错误文案。
async function onRetryInit() {
  initFeedback.value = ''
  initError.value = false
  try {
    const result = await initMutation.mutateAsync()
    trackingJobId.value = result.job_id
    trackingJobTitle.value = '初始化任务'
    initFeedback.value = `已提交初始化任务：${result.job_id}`
  } catch (err: unknown) {
    initError.value = true
    initFeedback.value = err instanceof Error ? err.message : '初始化失败'
  }
}

// canToggleKey 沿用应用管理权限控制 API key 启停入口；后端和 mutation 仍可能执行更严格的 API key 权限校验。
// UI 当前仅保留「恢复」操作，禁用入口已被移除（详见 API key 描述项注释）。
const canToggleKey = computed(() => canManageApp(auth.user, app?.value))

const keyMutation = useToggleAppAPIKey(appId)
const keyFeedback = ref('')
const keyError = ref(false)

// keyTagType 将 API key 状态映射为标签色，未知状态用 warning 提醒确认。
function keyTagType(s: string): 'success' | 'warning' | 'error' | 'default' {
  return s === 'active' ? 'success' : s === 'disabled' ? 'error' : 'warning'
}

// apiKeyLabel 将后端 key 状态转换为用户可读文案，未知状态保留原值。
function apiKeyLabel(s: string): string {
  return s === 'active' ? '启用' : s === 'disabled' ? '已禁用' : s
}

// onRestoreKey 提交 restore 后端任务；任务完成由 JobProgressPanel 继续轮询。
// 禁用入口已下线后，本组件对 keyMutation 仅有 restore 一种调用，逻辑内联在此函数中。
async function onRestoreKey() {
  keyFeedback.value = ''
  keyError.value = false
  try {
    const result = await keyMutation.mutateAsync('restore')
    trackingJobId.value = result.job_id
    trackingJobTitle.value = '恢复 API key 任务'
    keyFeedback.value = `已提交 restore 任务：${result.job_id}`
  } catch (err: unknown) {
    keyError.value = true
    keyFeedback.value = err instanceof Error ? err.message : 'restore 失败'
  }
}
</script>

<style scoped>
/* init-progress 包裹 n-progress 与字节文案,确保进度条紧贴状态 tag 显示 */
.init-progress {
  margin-top: 8px;
}
/* 字节文案使用 12px 弱化色,作为进度条的从属辅助信息 */
.init-progress-bytes {
  font-size: 12px;
  color: var(--color-text-secondary, #6b7280);
  margin-left: 8px;
}
/* 失败阶段提示用错误红色,与现有 state-text.danger 视觉一致 */
.init-failure {
  margin-top: 4px;
  color: var(--color-danger, #d93026);
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
/* 具体错误原因用更小字号和低透明度,区别于阶段标题 */
.init-failure-reason {
  font-size: 12px;
  opacity: 0.8;
  word-break: break-all;
}
</style>
