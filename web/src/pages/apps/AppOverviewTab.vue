<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Overview</p>
        <h2 style="margin: 0">{{ t('apps.overview.heading') }}</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button
        type="primary"
        :disabled="!canRetryInit || initMutation.isPending.value"
        @click="onRetryInit"
      >
        {{ initMutation.isPending.value ? t('apps.overview.retryInitPending') : t('apps.overview.retryInit') }}
      </n-button>
    </template>

    <!-- web-publish 能力已开通但本实例尚未注入（实例在企业开通前就已运行）：
         提示需重启使发布能力生效，并提供直接重启入口（复用 restart 操作）。 -->
    <n-alert
      v-if="app && app.web_publish_pending_restart"
      type="warning"
      :title="t('apps.overview.webPublish.pendingTitle')"
      style="margin-bottom: 12px"
    >
      <n-space align="center" :size="12">
        <span>{{ t('apps.overview.webPublish.pendingDesc') }}</span>
        <n-button
          v-if="canRestartForWebPublish"
          size="small"
          type="primary"
          :disabled="restartMutation.isPending.value"
          @click="onRestartForVersionSync"
        >
          {{ restartMutation.isPending.value ? t('apps.overview.restartNowPending') : t('apps.overview.restartNow') }}
        </n-button>
      </n-space>
    </n-alert>

    <p v-if="!app" class="state-text">{{ t('apps.overview.noApp') }}</p>
    <n-descriptions v-else :column="2" bordered size="small">
      <n-descriptions-item :label="t('apps.overview.labelStatus')">
        <AppStatusTag :status="app.status" />
        <!-- 仅当处于初始化子状态且拿到了可量化进度(total>0)才显示进度条；拉镜像 / 起容器
             这类底层不暴露细粒度进度的阶段(total=0)不显示进度条，避免 0% / 不定进度条让用户
             误以为卡死，此时只保留上方的状态文案（如「启动容器」）。 -->
        <div v-if="isInitPhase(app.status) && !initIndeterminate" class="init-progress">
          <n-progress type="line" :percentage="initPercentage" indicator-placement="inside" />
          <span class="init-progress-bytes">
            {{ formatBytes(app.progress_current) }} / {{ formatBytes(app.progress_total) }}
          </span>
        </div>
        <!-- error 状态附加显示最近失败阶段的中文文案及具体错误原因 -->
        <div v-if="app.status === 'error' && app.last_error_status" class="init-failure">
          <!-- formatAppStatus 返回 i18n 键；先用 t() 解析为当前语言文案再传入 errorStageFmt 插值。 -->
          <span>{{ t('apps.overview.errorStageFmt', { stage: t(formatAppStatus(app.last_error_status).label, formatAppStatus(app.last_error_status).params ?? {}) }) }}</span>
          <span v-if="app.last_error_message" class="init-failure-reason">{{ app.last_error_message }}</span>
        </div>
      </n-descriptions-item>
      <n-descriptions-item :label="t('apps.overview.labelApiKey')">
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
            {{ t('apps.overview.apiKeyRestore') }}
          </n-button>
        </n-space>
      </n-descriptions-item>
      <!-- 助手版本：展示绑定的版本名，version_synced=false 时附加需重启标签，组织管理员可切换 -->
      <n-descriptions-item :label="t('apps.overview.labelVersion')">
        <n-space align="center" :size="8">
          <span>{{ versionName }}</span>
          <!-- version_synced 为 false 时提示需重启，与实例列表的需重启提示视觉一致 -->
          <n-tag v-if="app.version_synced === false" type="warning" size="small" :bordered="false">{{ t('apps.overview.restartNeeded') }}</n-tag>
          <!-- 切换按钮仅对有应用管理权限且可查看版本目录的用户展示 -->
          <n-button
            v-if="canSwitchAppVersion(auth.user, app) && canViewVersions"
            size="small"
            @click="openSwitchVersionModal"
          >
            {{ t('apps.overview.switchVersion') }}
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
            {{ restartMutation.isPending.value ? t('apps.overview.restartNowPending') : t('apps.overview.restartNow') }}
          </n-button>
        </n-space>
      </n-descriptions-item>
      <!-- 实例语言：实时展示实例当前运行语言；未运行显示提示；当前≠期望时显示需重启徽标与重启入口 -->
      <n-descriptions-item :label="t('apps.overview.language.label')">
        <n-space align="center" :size="8">
          <span>{{ localeLabel }}</span>
          <!-- 需重启徽标：与版本同步的需重启提示视觉一致（warning tag），插值期望语言名 -->
          <n-tag v-if="localeNeedsRestart" type="warning" size="small" :bordered="false">
            {{ t('apps.overview.language.needsRestart', { lang: desiredLocaleLabel }) }}
          </n-tag>
          <!-- 重启按钮：仅在有运行时操作权限且需重启时展示，复用 restart 操作 -->
          <n-button
            v-if="canRestartForLocale"
            size="small"
            type="primary"
            :disabled="restartMutation.isPending.value"
            @click="onRestartForLocale"
          >
            {{ restartMutation.isPending.value ? t('apps.overview.restartNowPending') : t('apps.overview.language.restart') }}
          </n-button>
        </n-space>
      </n-descriptions-item>
      <n-descriptions-item :label="t('apps.overview.labelOrg')">
        {{ organizationName }}
      </n-descriptions-item>
      <n-descriptions-item v-if="app.description" :label="t('apps.overview.labelDesc')" :span="2">
        {{ app.description }}
      </n-descriptions-item>
      <!-- runtime_image_ref / sha256 由后端仅对平台管理员填充，非管理员字段恒为空，此处不渲染 -->
      <n-descriptions-item v-if="auth.isPlatformAdmin && app.runtime_image_ref" :label="t('apps.overview.labelImageRef')" :span="2">
        <code>{{ app.runtime_image_ref }}</code>
      </n-descriptions-item>
      <n-descriptions-item v-if="auth.isPlatformAdmin && app.runtime_image_sha256" :label="t('apps.overview.labelImageDigest')" :span="2">
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
    <n-modal v-model:show="showSwitchVersionModal" preset="card" :title="t('apps.overview.switchVersionTitle')" style="width: 420px">
      <n-select
        v-model:value="selectedVersionId"
        :options="versionOptions"
        :loading="versionsQuery.isLoading.value"
        :placeholder="t('apps.overview.switchVersionPlaceholder')"
        style="margin-bottom: 16px"
      />
      <n-space justify="end">
        <n-button @click="showSwitchVersionModal = false">{{ t('common.actions.cancel') }}</n-button>
        <n-button
          type="primary"
          :loading="switchVersionMutation.isPending.value"
          :disabled="!selectedVersionId || switchVersionMutation.isPending.value"
          @click="onConfirmSwitchVersion"
        >
          {{ t('apps.overview.switchVersionConfirm') }}
        </n-button>
      </n-space>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
import { computed, inject, ref, watch, type Ref } from 'vue'
import { NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NModal, NProgress, NSelect, NSpace, NTag, type SelectOption } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import {
  useAppLocaleStatusQuery,
  useInitializeAppMutation,
  useJobQuery,
  useSwitchAppVersion,
  useToggleAppAPIKey,
  useTriggerRuntimeOperation,
  useInvalidateAppData,
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
const { t, messages } = useI18n()

// localeName 把语言代码（zh/en）映射为该语言的母语自报名（languageName），与 LocaleSwitcher
// 取名口径一致：母语者总能认出自己的语言；未知代码回退原代码。
// 这样无需在 en 文案里写中文（会触发 i18n 完整性测试的「en 不得含 CJK」规则）。
function localeName(code: string): string {
  const msg = messages.value[code as keyof typeof messages.value] as { common?: { languageName?: string } } | undefined
  return msg?.common?.languageName ?? code
}
const appId = computed<string | undefined>(() => props.appId)

const app = inject<Ref<AppDTO | null>>('app')
const auth = useAuthStore()
// orgId 只用于展示组织名称；权限和业务 API 仍继续使用 app.org_id。
const orgId = computed<string | undefined>(() => app?.value?.org_id)
const organizationQuery = useOrganizationQuery(orgId)
const organizationName = computed(() => organizationQuery.data.value?.name || t('apps.overview.unknownOrg'))

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
    versionFeedback.value = t('apps.overview.switchVersionSuccess')
  } catch (err: unknown) {
    versionError.value = true
    versionFeedback.value = err instanceof Error ? err.message : t('apps.overview.switchVersionError')
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

// canRestartForWebPublish 控制 web-publish「能力已开通需重启」横幅里的重启按钮：
// 口径与 canRestartForVersionSync 一致（有运行时操作权限 + 实例 running/binding_waiting），
// 仅触发条件换成 web_publish_pending_restart=true（企业已开通但本实例尚未注入发布能力）。
const canRestartForWebPublish = computed(() => {
  if (!app?.value) return false
  if (!canTriggerRuntimeOperation(auth.user, app.value)) return false
  if (app.value.web_publish_pending_restart !== true) return false
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
    trackingJobTitle.value = t('apps.overview.restartJobTitle')
    restartFeedback.value = `${t('apps.overview.restartSubmitted')}${result.job_id}`
  } catch (err: unknown) {
    restartError.value = true
    restartFeedback.value = err instanceof Error ? err.message : t('apps.overview.restartError')
  }
}

// localeStatusQuery 实时查询实例语言状态：current_language（实例未运行时为 null）、
// desired_language（apps.locale 期望语言）、needs_restart（当前≠期望，需重启生效）。
const localeStatusQuery = useAppLocaleStatusQuery(appId)
const localeStatus = computed(() => localeStatusQuery.data.value ?? null)

// localeLabel 把语言代码（zh/en）映射为母语自报名；实例未运行（current 为空）时显示「实例未运行」。
const localeLabel = computed(() => {
  const code = localeStatus.value?.current_language
  if (!code) return t('apps.overview.language.notRunning')
  return localeName(code)
})

// localeNeedsRestart 仅在实例运行（current 有值）且后端判定 needs_restart=true 时为真；
// current 为空（未运行）时不展示需重启提示与重启按钮，因为此时无运行容器可重启。
const localeNeedsRestart = computed(() => {
  const status = localeStatus.value
  if (!status) return false
  return Boolean(status.current_language) && status.needs_restart === true
})

// desiredLocaleLabel 把期望语言代码映射为母语自报名，用于需重启提示插值。
const desiredLocaleLabel = computed(() => {
  const code = localeStatus.value?.desired_language
  return code ? localeName(code) : ''
})

// canRestartForLocale 控制语言「重启应用」按钮：需有运行时操作权限 + 后端判定需重启。
const canRestartForLocale = computed(() => {
  if (!app?.value) return false
  if (!canTriggerRuntimeOperation(auth.user, app.value)) return false
  return localeNeedsRestart.value
})

// onRestartForLocale 复用与版本同步一致的 restart 入口（useTriggerRuntimeOperation），
// 提交后交给 JobProgressPanel 跟踪进度，并刷新 locale-status 让需重启提示在重启完成后消失。
async function onRestartForLocale() {
  restartFeedback.value = ''
  restartError.value = false
  try {
    const result = await restartMutation.mutateAsync('restart')
    trackingJobId.value = result.job_id
    trackingJobTitle.value = t('apps.overview.restartJobTitle')
    restartFeedback.value = `${t('apps.overview.restartSubmitted')}${result.job_id}`
    void localeStatusQuery.refetch()
  } catch (err: unknown) {
    restartError.value = true
    restartFeedback.value = err instanceof Error ? err.message : t('apps.overview.restartError')
  }
}

const initMutation = useInitializeAppMutation(appId)
// trackingJobId 记录最近一次后台任务，供 JobProgressPanel 轮询展示执行进度。
const trackingJobId = ref<string | undefined>()
// trackingJobTitle 初始为空串，提交任务时按操作类型动态赋值。
const trackingJobTitle = ref('')
const jobIdRef = computed<string | undefined>(() => trackingJobId.value)
const jobQuery = useJobQuery(jobIdRef)
const trackedJob = computed(() => jobQuery.data.value ?? null)

// invalidateAppData 在后台任务到达终态后刷新实例详情与运行时视图。
const invalidateAppData = useInvalidateAppData(appId)

// 监听后台任务（重启 / 初始化 / 恢复 key）状态：当 job 由非终态切换到终态（succeeded /
// failed / canceled）的那一刻，主动失效实例详情与运行时缓存，让概览页的「需重启」标签、
// 状态 tag、助手版本同步状态及运行时快照无需用户手动刷新即可对齐最新结果。
// 只在「非终态 → 终态」的边沿触发一次：终态会停止 job 轮询，因此 status 不会反复进入终态；
// prev 为 undefined（页面初次进入即拿到终态，理论上不会发生）时不触发，避免无意义失效。
const terminalJobStatuses = new Set(['succeeded', 'failed', 'canceled'])
watch(
  () => trackedJob.value?.status,
  (status, prev) => {
    if (status && terminalJobStatuses.has(status) && prev && !terminalJobStatuses.has(prev)) {
      invalidateAppData()
      // 任务终态时一并刷新实例语言状态，让重启完成后「需重启」提示无需手动刷新即可消失。
      void localeStatusQuery.refetch()
    }
  },
)

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
    trackingJobTitle.value = t('apps.overview.initJobTitle')
    initFeedback.value = `${t('apps.overview.initSubmitted')}${result.job_id}`
  } catch (err: unknown) {
    initError.value = true
    initFeedback.value = err instanceof Error ? err.message : t('apps.overview.initError')
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
  return s === 'active' ? t('apps.overview.apiKeyActive') : s === 'disabled' ? t('apps.overview.apiKeyDisabled') : s
}

// onRestoreKey 提交 restore 后端任务；任务完成由 JobProgressPanel 继续轮询。
// 禁用入口已下线后，本组件对 keyMutation 仅有 restore 一种调用，逻辑内联在此函数中。
async function onRestoreKey() {
  keyFeedback.value = ''
  keyError.value = false
  try {
    const result = await keyMutation.mutateAsync('restore')
    trackingJobId.value = result.job_id
    trackingJobTitle.value = t('apps.overview.keyJobTitle')
    keyFeedback.value = t('apps.overview.keyRestoreSubmitted', { jobId: result.job_id })
  } catch (err: unknown) {
    keyError.value = true
    keyFeedback.value = err instanceof Error ? err.message : t('apps.overview.keyRestoreError')
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
