<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Channels</p>
        <h2 style="margin: 0">{{ t('apps.channels.heading') }}</h2>
      </div>
    </template>

    <div v-if="!appId" class="state-text">{{ t('apps.channels.noApp') }}</div>
    <div v-else class="channels-layout">
      <aside class="channel-list" :aria-label="t('apps.channels.ariaList')">
        <button
          v-for="channel in channels"
          :key="channel.type"
          type="button"
          class="channel-list-item"
          :class="{
            active: channel.type === activeChannel.type,
            supported: channel.supported,
            unsupported: !channel.supported,
          }"
          :disabled="!channel.supported"
          :aria-disabled="channel.supported ? 'false' : 'true'"
          :aria-current="channel.type === activeChannel.type ? 'true' : undefined"
          @click="selectChannel(channel)"
        >
          <ChannelLogo :type="channel.type" :muted="!channel.supported" />
          <span class="channel-copy">
            <strong>{{ channel.name }}</strong>
            <span>{{ channel.description }}</span>
          </span>
          <span class="channel-support-label">{{ channel.statusLabel }}</span>
        </button>
      </aside>

      <section class="channel-detail" :aria-label="t('apps.channels.ariaDetail')">
        <div class="channel-detail-head">
          <div class="channel-title">
            <ChannelLogo :type="activeChannel.type" large />
            <h3 class="channel-title-text">
              {{ activeChannel.name }}
              <span class="channel-status-inline">· {{ statusLabel }}</span>
            </h3>
          </div>

          <n-space v-if="selectedChannelType === 'wechat'" :size="8">
            <n-button
              type="primary"
              :disabled="!appId || !canManage || !instanceReady"
              :loading="beginning"
              @click="beginAuth"
            >
              {{ primaryButtonLabel }}
            </n-button>
            <n-button
              v-if="showRefreshChallenge"
              :disabled="!canManage || !instanceReady"
              :loading="beginning"
              @click="beginAuth"
            >
              {{ beginning ? t('apps.channels.refreshQrPending') : t('apps.channels.refreshQr') }}
            </n-button>
            <n-button v-if="canUnbind" @click="unbind">{{ t('apps.channels.unbind') }}</n-button>
          </n-space>
        </div>

        <!-- 微信渠道详情：扫码绑定 + 状态提示，沿用既有逻辑，飞书选中时不渲染。 -->
        <template v-if="selectedChannelType === 'wechat'">
          <!-- 实例非运行态(重启中/升级中)时发起被禁用，给出原因提示，避免误以为按钮坏了。 -->
          <p v-if="canManage && !instanceReady" class="state-text">{{ t('apps.channels.instanceNotReady') }}</p>
          <p v-if="progress?.bound_identity" class="state-text">{{ t('apps.channels.boundIdentity') }}{{ progress.bound_identity }}</p>
          <p v-if="progress?.error_message" class="state-text danger">{{ t('apps.channels.errorMsg') }}{{ progress.error_message }}</p>
          <p v-if="isWaitingForChallenge" class="state-text">{{ t('apps.channels.waitingQr') }}</p>
          <p v-if="challengeExpired" class="state-text danger">
            {{ t('apps.channels.qrExpired') }}
          </p>

          <AuthChallengeRenderer v-if="visibleChallenge" :challenge="visibleChallenge" @rendered="onQrRendered" />
        </template>

        <!-- 飞书渠道详情：部署域 + 扫码自动创建 + 图文指引 + 已绑定详情/解绑。 -->
        <template v-else-if="selectedChannelType === 'feishu'">
          <div class="feishu-panel">
            <!-- 部署域：下拉决定后端调用的开放平台域（飞书国内 / Lark 国际）。 -->
            <div class="feishu-controls">
              <label class="feishu-field">
                <span class="feishu-field-label">{{ t('apps.channels.feishuDomainLabel') }}</span>
                <n-select
                  v-model:value="feishuDomain"
                  :options="feishuDomainOptions"
                  :disabled="!canManage || feishuBound"
                  class="feishu-domain-select"
                />
              </label>
            </div>

            <!-- 已绑定详情：展示 bot 信息与部署域，提供解绑入口。 -->
            <template v-if="feishuBound">
              <div class="feishu-bound">
                <p v-if="feishuProgress?.channel_name" class="state-text">{{ t('apps.channels.feishuBotName') }}{{ feishuProgress.channel_name }}</p>
                <p v-if="feishuProgress?.bound_identity" class="state-text">{{ t('apps.channels.boundIdentity') }}{{ feishuProgress.bound_identity }}</p>
                <p class="state-text">{{ t('apps.channels.feishuDomainCurrent') }}{{ feishuEffectiveDomain === 'lark' ? t('apps.channels.feishuDomainLark') : t('apps.channels.feishuDomainFeishu') }}</p>
              </div>
              <n-space :size="8">
                <n-button v-if="feishuCanUnbind" @click="unbindFeishu">{{ t('apps.channels.unbind') }}</n-button>
              </n-space>
            </template>

            <!-- 未绑定：扫码自动创建，点发起后轮询二维码并复用 AuthChallengeRenderer 渲染。 -->
            <template v-else>
              <n-space :size="8">
                <n-button
                  type="primary"
                  :disabled="!appId || !canManage || !instanceReady"
                  :loading="feishuBeginning"
                  @click="beginFeishuScan"
                >
                  {{ t('apps.channels.beginLogin') }}
                </n-button>
              </n-space>
              <!-- 实例非运行态(重启中/升级中)时发起被禁用，提示原因避免误以为是 bug。 -->
              <p v-if="canManage && !instanceReady" class="state-text">{{ t('apps.channels.instanceNotReady') }}</p>
              <p v-if="feishuProgress?.error_message" class="state-text danger">{{ t('apps.channels.errorMsg') }}{{ feishuProgress.error_message }}</p>
              <!-- 扫码后凭证已回传（二维码消费、注入连接中）显示“验证连接中”，未出码时才显示“生成二维码”，
                   避免飞书扫码后误显示微信导向的“正在生成登录二维码”。 -->
              <p v-if="feishuConnecting" class="state-text">{{ t('apps.channels.feishuConnecting') }}</p>
              <p v-else-if="feishuWaitingForChallenge" class="state-text">{{ t('apps.channels.feishuGeneratingQr') }}</p>
              <AuthChallengeRenderer v-if="feishuVisibleChallenge" :challenge="feishuVisibleChallenge" />
            </template>

            <!-- 图文指引：展示扫码授权步骤，默认折叠。 -->
            <n-collapse class="feishu-guide">
              <n-collapse-item :title="t('apps.channels.feishuGuideTitle')" name="guide">
                <p class="feishu-guide-text">{{ t('apps.channels.feishuScanGuide') }}</p>
              </n-collapse-item>
            </n-collapse>
          </div>
        </template>
      </section>
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { computed, inject, ref, toRef, watch, type Ref } from 'vue'
import {
  NButton,
  NCard,
  NCollapse,
  NCollapseItem,
  NSelect,
  NSpace,
} from 'naive-ui'
import { useI18n } from 'vue-i18n'

import type { AppDTO } from '@/api/hooks/useApps'
import AuthChallengeRenderer from '@/components/AuthChallengeRenderer.vue'
import ChannelLogo, { type ChannelLogoType } from './ChannelLogo.vue'
import {
  useBeginChannelAuth,
  useBeginFeishuAuth,
  useChannelProgressQuery,
  useUnbindChannel,
  channelChallengeFromProgress,
  formatChannelStatus,
  shouldShowChallengePending,
  type ChannelChallenge,
} from '@/api/hooks/useChannel'
import { canManageApp } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppChannelsTab 负责应用渠道登录绑定流程，当前默认处理 wechat 渠道。
// appId 和 channelType 来自路由，父级注入的 app 用于判断当前用户是否可管理。
const props = defineProps<{ appId?: string; channelType?: string }>()
const { t } = useI18n()

// ChannelDisplay 是渠道 tab 的纯前端展示模型；当前仅 wechat 接入真实绑定能力。
// 其他渠道作为能力边界展示，不参与 API 参数或后端状态机。type 直接复用 ChannelLogo 的
// ChannelLogoType，由类型系统强制两处渠道枚举一致，新增渠道时编译器会提示同步。
interface ChannelDisplay {
  type: ChannelLogoType
  name: string
  description: string
  supported: boolean
  statusLabel: string
}

// channels 固定列出当前产品规划中需要展示的渠道；supported=false 的渠道只做灰色预告。
// 使用 computed 包裹确保语言切换时渠道名称和描述响应式更新。
const channels = computed<ReadonlyArray<ChannelDisplay>>(() => [
  { type: 'wechat', name: t('apps.channels.channelWechat'), description: t('apps.channels.channelWechatDesc'), supported: true, statusLabel: t('apps.channels.supported') },
  { type: 'work_wechat', name: t('apps.channels.channelWorkWechat'), description: t('apps.channels.channelWorkWechatDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
  { type: 'feishu', name: t('apps.channels.channelFeishu'), description: t('apps.channels.channelFeishuDesc'), supported: true, statusLabel: t('apps.channels.supported') },
  { type: 'dingtalk', name: t('apps.channels.channelDingtalk'), description: t('apps.channels.channelDingtalkDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
  { type: 'telegram', name: t('apps.channels.channelTelegram'), description: t('apps.channels.channelTelegramDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
  { type: 'whatsapp', name: t('apps.channels.channelWhatsapp'), description: t('apps.channels.channelWhatsappDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
  { type: 'discord', name: t('apps.channels.channelDiscord'), description: t('apps.channels.channelDiscordDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
  { type: 'slack', name: t('apps.channels.channelSlack'), description: t('apps.channels.channelSlackDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
  { type: 'line', name: t('apps.channels.channelLine'), description: t('apps.channels.channelLineDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
])

const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
const appId = toRef(props, 'appId')
// selectedChannelType 是当前详情区展示的已支持渠道；目前可在 wechat / feishu 间切换。
// 暂不支持渠道点击被忽略，不会改变此值，确保详情区只渲染有真实绑定能力的渠道。
const selectedChannelType = ref<'wechat' | 'feishu'>('wechat')
// channelType / channelTypeRef 固定指向 wechat，供既有微信 hook（进度/发起/解绑）专用，
// 切换到飞书时微信轮询仍以原契约在后台运行，微信链路逻辑保持零改动。
const channelType = computed(() => 'wechat')
const channelTypeRef = computed<string | undefined>(() => 'wechat')
// activeChannel 跟随 selectedChannelType，让标题、状态与列表高亮反映当前选中渠道。
const activeChannel = computed(() => channels.value.find(channel => channel.type === selectedChannelType.value) ?? channels.value[0])

// selectChannel 仅接受已支持渠道（wechat / feishu）；暂不支持渠道保持禁用且不切换详情区。
function selectChannel(channel: ChannelDisplay) {
  if (!channel.supported) return
  if (channel.type === 'wechat' || channel.type === 'feishu') {
    selectedChannelType.value = channel.type
  }
}

const { data: progress } = useChannelProgressQuery(appId, channelTypeRef)
const beginMutation = useBeginChannelAuth(appId, channelTypeRef)
const unbindMutation = useUnbindChannel(appId, channelTypeRef)

// ---- 飞书渠道（扫码自动创建）----
// feishuProgressType 仅在选中飞书时返回 'feishu'，否则返回 undefined 关闭轮询，
// 避免停留在微信详情区时仍向飞书进度接口发请求。
const feishuProgressType = computed<string | undefined>(() => (selectedChannelType.value === 'feishu' ? 'feishu' : undefined))
// feishuChannelRef 固定为 'feishu'，供解绑接口构造路径与失效缓存 key 使用。
const feishuChannelRef = computed<string | undefined>(() => 'feishu')
const { data: feishuProgress } = useChannelProgressQuery(appId, feishuProgressType)
const beginFeishuMutation = useBeginFeishuAuth(appId)
const unbindFeishuMutation = useUnbindChannel(appId, feishuChannelRef)

// feishuDomain 决定后端调用的开放平台域，默认飞书国内。
const feishuDomain = ref<'feishu' | 'lark'>('feishu')
// feishuBeginning 是飞书发起扫码的本地提交态，覆盖按钮 loading。
const feishuBeginning = ref(false)
// feishuChallenge 保存发起后立即返回的挑战，轮询进度若带回二维码则优先使用进度结果。
const feishuChallenge = ref<ChannelChallenge | null>(null)
// feishuAuthStarted 区分“已点发起但后端尚未返回二维码”和“完全未发起”的展示状态。
const feishuAuthStarted = ref(false)

// feishuDomainOptions 提供部署域下拉项；label 随语言切换响应式更新。
const feishuDomainOptions = computed(() => [
  { label: t('apps.channels.feishuDomainFeishu'), value: 'feishu' },
  { label: t('apps.channels.feishuDomainLark'), value: 'lark' },
])

// feishuStatusLabel 展示飞书绑定进度状态文案。
const feishuStatusLabel = computed(() => formatChannelStatus(feishuProgress.value?.status))
// feishuBound 表示飞书已绑定，用于切换已绑定详情区与解绑按钮。
const feishuBound = computed(() => feishuProgress.value?.status === 'bound')
// feishuCanUnbind 受管理权限与绑定态共同约束，真正校验仍由后端兜底。
const feishuCanUnbind = computed(() => canManage.value && feishuBound.value)
// feishuEffectiveDomain 优先从绑定进度的 metadata.domain 读取（刷新页面后仍正确，
// 避免 lark 实例被本地 ref 默认值 'feishu' 误显示），未绑定或 metadata 无 domain 时
// 回退到本地 feishuDomain ref（即用户当前在下拉中选择的值）。
const feishuEffectiveDomain = computed<'feishu' | 'lark'>(() => {
  const d = feishuProgress.value?.metadata?.domain
  if (d === 'feishu' || d === 'lark') return d
  return feishuDomain.value
})

// feishuProgressChallenge 从轮询进度的 metadata.qrcode 还原扫码挑战，刷新页面也能恢复二维码。
const feishuProgressChallenge = computed<ChannelChallenge | null>(() => channelChallengeFromProgress(feishuProgress.value, 'feishu'))
// feishuVisibleChallenge 优先展示轮询结果，轮询尚未带回时回退到发起接口的立即响应。
const feishuVisibleChallenge = computed(() => feishuProgressChallenge.value ?? (feishuChallenge.value?.qrcode ? feishuChallenge.value : null))
// feishuWaitingForChallenge 在已发起但二维码尚未就绪时提示“生成中”。
const feishuWaitingForChallenge = computed(() => shouldShowChallengePending(feishuAuthStarted.value, feishuVisibleChallenge.value, feishuProgress.value?.status))
// feishuConnecting 区分“扫码后注入连接中”与“尚未出码”：扫码成功后 worker 用凭证 metadata
// 覆盖二维码（qrcode 消失）、status 仍 pending_auth，此时不应再提示“生成二维码”，而应提示
// “已扫码，验证连接中”。判据：已发起 + 无可见挑战 + status=pending_auth + metadata 已带回凭证
// （injected 标记或 app_id，由扫码 credentials 落库写入；app_secret 密文已被 PollAuth 过滤）。
const feishuConnecting = computed(() => {
  const p = feishuProgress.value
  if (!feishuAuthStarted.value || feishuVisibleChallenge.value) return false
  if (p?.status !== 'pending_auth') return false
  return p?.metadata?.injected === 'true' || Boolean(p?.metadata?.app_id)
})

// beginFeishuScan 发起扫码自动创建：仅提交 domain，由 worker 异步建应用并回写二维码。
async function beginFeishuScan() {
  if (!canManage.value) return
  feishuBeginning.value = true
  try {
    feishuChallenge.value = await beginFeishuMutation.mutateAsync({ domain: feishuDomain.value })
    feishuAuthStarted.value = true
  } finally {
    feishuBeginning.value = false
  }
}

// unbindFeishu 解绑飞书后清空本地挑战与发起态，等待进度缓存失效后回到未绑定展示。
async function unbindFeishu() {
  if (!canManage.value) return
  await unbindFeishuMutation.mutateAsync()
  feishuAuthStarted.value = false
  feishuChallenge.value = null
}

// beginning 是本地提交态，覆盖 beginMutation 尚未返回挑战内容前的按钮文案。
const beginning = ref(false)
// challenge 保存本次发起登录立即返回的挑战，轮询进度若有更新会优先使用 progressChallenge。
const challenge = ref<ChannelChallenge | null>(null)
// authStarted 区分“用户已点发起但后端还未返回二维码”和“完全未开始”的展示状态。
const authStarted = ref(false)
// renderedQrcode 记录 AuthChallengeRenderer 最近一次完成 QRCode 编码 + 触发 img 渲染的 qrcode 字符串。
// beginAuth 用这个 ref 判定 loading 何时结束——以"img 真正展示新二维码"为准，而不是 mutation 返回值。
const renderedQrcode = ref<string | null>(null)
function onQrRendered(qr: string) {
  renderedQrcode.value = qr
}

// statusLabel 跟随当前选中渠道展示对应进度状态，微信选中时取值与原逻辑一致。
const statusLabel = computed(() =>
  selectedChannelType.value === 'feishu'
    ? formatChannelStatus(feishuProgress.value?.status)
    : formatChannelStatus(progress.value?.status),
)

// canManage 控制发起登录和解绑按钮，真正权限仍由后端接口再次校验。
const canManage = computed(() => canManageApp(auth.user, app?.value))
const canUnbind = computed(() => canManage.value && progress.value?.status === 'bound')
// instanceReady 闸门：渠道发起依赖实例内 oc-ops 可用。允许集与后端
// domain.AppCanInitiateChannelAuth 严格一致——running / binding_waiting / binding_failed：
// 这三态 pod 在跑、oc-ops 可达，且【首次绑定合法地发生在 binding_waiting】
// （binding_waiting→running 是扫码成功后才迁移），故不能只放行 running。
// 其余状态（restarting 重启中、pulling_runtime_image 等升级/初始化、stopped/error）
// pod 用 Recreate 重建或未就绪、oc-ops 短暂不可达，发起会拿到 502/409，统一拦在前端并提示原因。
const AUTH_READY_STATUSES = new Set(['running', 'binding_waiting', 'binding_failed'])
const instanceReady = computed(() => AUTH_READY_STATUSES.has(app?.value?.status ?? ''))

// progressChallenge 从轮询结果恢复挑战，避免刷新页面后丢失二维码或验证码展示。
const progressChallenge = computed<ChannelChallenge | null>(() => {
  return channelChallengeFromProgress(progress.value, channelType.value)
})

// visibleChallenge 优先展示轮询结果，只有轮询尚未携带挑战时才使用本地刚提交的响应。
const visibleChallenge = computed(() => progressChallenge.value ?? (challenge.value?.qrcode || challenge.value?.code ? challenge.value : null))
const isWaitingForChallenge = computed(() => shouldShowChallengePending(authStarted.value, visibleChallenge.value, progress.value?.status))

// challengeExpired 用于提示用户重新生成二维码，过期时间缺失时不做前端过期判断。
const challengeExpired = computed(() => {
  const expiresAt = visibleChallenge.value?.expires_at
  if (!expiresAt) return false
  const ts = Date.parse(expiresAt)
  return Number.isFinite(ts) && ts < Date.now()
})

// showRefreshChallenge 覆盖过期、失败和等待授权状态，让用户可以重新拉起登录挑战。
const showRefreshChallenge = computed(() => {
  if (challengeExpired.value) return true
  const status = progress.value?.status
  if (status === 'pending_auth' || status === 'expired' || status === 'failed') return true
  return false
})

// primaryButtonLabel 聚合提交态、绑定态和过期态，避免模板中散落渠道状态判断。
const primaryButtonLabel = computed(() => {
  if (beginning.value) return t('apps.channels.triggering')
  if (challengeExpired.value) return t('apps.channels.regenQr')
  if (progress.value?.status === 'bound') return t('apps.channels.relogin')
  return t('apps.channels.beginLogin')
})

watch(
  () => progress.value?.status,
  (status) => {
    if (status === 'bound') {
      authStarted.value = false
      challenge.value = null
    }
  },
)

// beginAuth 发起渠道登录 mutation；loading 持续到 AuthChallengeRenderer emit 'rendered'
// 事件（即新二维码完成 QRCode.toDataURL 异步编码、img 即将渲染新内容）才结束，
// 而不是 mutation 返回就结束——避免出现 "loading 已结束、页面 3-6 秒后才换图" 的体验。
// 监听对象是 renderedQrcode，而非 visibleChallenge.qrcode：前者代表"img 真的展示了什么"，
// 后者只是 props，受 progress 4s 轮询 + 子组件异步编码影响，会比图片实际更新更早变化。
// 10 秒兜底防止 progress 轮询慢或网络异常时按钮卡死。
async function beginAuth() {
  if (!canManage.value) return
  const prevRendered = renderedQrcode.value
  beginning.value = true
  try {
    challenge.value = await beginMutation.mutateAsync()
    authStarted.value = true
    await new Promise<void>((resolve) => {
      const stop = watch(
        renderedQrcode,
        (qr) => {
          if (qr && qr !== prevRendered) {
            stop()
            resolve()
          }
        },
        { immediate: true },
      )
      setTimeout(() => {
        stop()
        resolve()
      }, 10000)
    })
  } finally {
    beginning.value = false
  }
}

// unbind 解绑后清空本地挑战状态，等待查询失效后由 hook 拉取最新绑定进度。
async function unbind() {
  if (!canManage.value) return
  await unbindMutation.mutateAsync()
  authStarted.value = false
  challenge.value = null
}
</script>

<style scoped>
.channels-layout {
  display: grid;
  grid-template-columns: minmax(200px, 260px) minmax(0, 1fr);
  gap: 18px;
  align-items: stretch;
}

.channel-list {
  display: grid;
  align-content: start;
  gap: 8px;
  padding-right: 16px;
  border-right: 1px solid var(--color-divider);
}

.channel-list-item {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr) auto;
  gap: 10px;
  align-items: center;
  width: 100%;
  min-height: 58px;
  padding: 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  color: var(--color-text-primary);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}

.channel-list-item.supported.active {
  border-color: var(--color-success-border);
  background: var(--color-success-soft);
}

.channel-list-item.unsupported {
  color: var(--color-text-tertiary);
  background: var(--color-neutral-soft);
  cursor: not-allowed;
}

.channel-list-item:disabled {
  opacity: 1;
}

.channel-copy {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.channel-copy strong {
  font-size: 14px;
}

.channel-copy span {
  overflow: hidden;
  color: var(--color-text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-list-item.unsupported .channel-copy span {
  color: var(--color-text-tertiary);
}

.channel-support-label {
  min-width: 58px;
  color: var(--color-text-secondary);
  font-size: 12px;
  text-align: right;
  white-space: nowrap;
}

.channel-list-item.supported .channel-support-label {
  color: var(--color-success-text);
  font-weight: 700;
}

.channel-detail {
  min-width: 0;
}

.channel-detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.channel-title {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: 12px;
}

.channel-title-text {
  margin: 0;
  font-size: 16px;
}

/* 状态文字与渠道名同行，用次要色 + 常规字重以区分主次。 */
.channel-status-inline {
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 400;
}

/* 飞书面板：纵向排布模式选择、表单、二维码与指引，整体与微信详情区视觉一致。 */
.feishu-panel {
  display: grid;
  gap: 16px;
}

/* 控制区排布部署域下拉，窄屏自动换行。 */
.feishu-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  align-items: flex-end;
}

.feishu-field {
  display: grid;
  gap: 6px;
  min-width: 0;
}

.feishu-field-label {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.feishu-domain-select {
  min-width: 200px;
}

/* 已绑定详情用次要文字密排展示 bot 信息与部署域。 */
.feishu-bound {
  display: grid;
  gap: 4px;
}

.feishu-guide {
  margin-top: 4px;
}

/* 指引正文保留换行，便于以多步骤文本呈现操作步骤。 */
.feishu-guide-text {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-line;
}

@media (max-width: 760px) {
  .channels-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .channel-list-item {
    grid-template-columns: 36px minmax(0, 1fr);
  }

  .channel-support-label {
    grid-column: 2;
    min-width: 0;
    text-align: left;
  }

  .channel-list {
    padding-right: 0;
    padding-bottom: 14px;
    border-right: 0;
    border-bottom: 1px solid var(--color-divider);
  }

  .channel-detail-head {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
