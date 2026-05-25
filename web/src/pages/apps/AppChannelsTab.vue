<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Channels</p>
        <h2 style="margin: 0">渠道绑定</h2>
      </div>
    </template>

    <div v-if="!appId" class="state-text">请选择目标实例</div>
    <div v-else class="channels-layout">
      <aside class="channel-list" aria-label="渠道列表">
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
        >
          <span
            class="channel-logo"
            :class="[channel.logoClass, { muted: !channel.supported }]"
            aria-hidden="true"
          >
            {{ channel.logoText }}
          </span>
          <span class="channel-copy">
            <strong>{{ channel.name }}</strong>
            <span>{{ channel.description }}</span>
          </span>
          <span class="channel-support-label">{{ channel.statusLabel }}</span>
        </button>
      </aside>

      <section class="channel-detail" aria-label="微信渠道详情">
        <div class="channel-detail-head">
          <div class="channel-title">
            <span
              class="channel-logo large"
              :class="activeChannel.logoClass"
              aria-hidden="true"
            >
              {{ activeChannel.logoText }}
            </span>
            <div>
              <p class="channel-title-kicker">当前渠道</p>
              <h3>{{ activeChannel.name }}</h3>
            </div>
          </div>

          <n-space :size="8">
            <n-button
              type="primary"
              :disabled="!appId || !canManage"
              :loading="beginning"
              @click="beginAuth"
            >
              {{ primaryButtonLabel }}
            </n-button>
            <n-button
              v-if="showRefreshChallenge"
              :disabled="!canManage"
              :loading="beginning"
              @click="beginAuth"
            >
              {{ beginning ? '生成中…' : '刷新二维码' }}
            </n-button>
            <n-button v-if="canUnbind" @click="unbind">解绑</n-button>
          </n-space>
        </div>

        <p class="state-text">
          当前状态：<strong>{{ statusLabel }}</strong>
          <span v-if="progress?.bound_identity"> ｜ 已绑定：{{ progress.bound_identity }}</span>
        </p>
        <p v-if="progress?.error_message" class="state-text danger">最近错误：{{ progress.error_message }}</p>
        <p v-if="isWaitingForChallenge" class="state-text">正在生成登录二维码…</p>
        <p v-if="challengeExpired" class="state-text danger">
          当前二维码已过期，请点击"刷新二维码"重新生成。
        </p>

        <AuthChallengeRenderer v-if="visibleChallenge" :challenge="visibleChallenge" @rendered="onQrRendered" />
      </section>
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { computed, inject, ref, toRef, watch, type Ref } from 'vue'
import { NButton, NCard, NSpace } from 'naive-ui'

import type { AppDTO } from '@/api/hooks/useApps'
import AuthChallengeRenderer from '@/components/AuthChallengeRenderer.vue'
import {
  useBeginChannelAuth,
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

// ChannelDisplay 是渠道 tab 的纯前端展示模型；当前仅 wechat 接入真实绑定能力。
// 其他渠道作为能力边界展示，不参与 API 参数或后端状态机。
interface ChannelDisplay {
  type: 'wechat' | 'work_wechat' | 'feishu' | 'dingtalk'
  name: string
  description: string
  supported: boolean
  statusLabel: string
  logoText: string
  logoClass: string
}

// channels 固定列出当前产品规划中需要展示的渠道；supported=false 的渠道只做灰色预告。
const channels: ReadonlyArray<ChannelDisplay> = [
  {
    type: 'wechat',
    name: '微信',
    description: '扫码绑定后接收助手消息',
    supported: true,
    statusLabel: '已支持',
    logoText: '微',
    logoClass: 'wechat',
  },
  {
    type: 'work_wechat',
    name: '企业微信',
    description: '企业内部协作场景',
    supported: false,
    statusLabel: '暂不支持',
    logoText: '企',
    logoClass: 'work-wechat',
  },
  {
    type: 'feishu',
    name: '飞书',
    description: '团队消息与工作台场景',
    supported: false,
    statusLabel: '暂不支持',
    logoText: '飞',
    logoClass: 'feishu',
  },
  {
    type: 'dingtalk',
    name: '钉钉',
    description: '组织通讯与审批场景',
    supported: false,
    statusLabel: '暂不支持',
    logoText: '钉',
    logoClass: 'dingtalk',
  },
]

const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
const appId = toRef(props, 'appId')
// supportedChannelType 是当前唯一可操作渠道；外部传入的非微信 channelType 只影响未来扩展场景，
// 在后端正式支持前必须忽略，避免详情区和绑定接口误用暂不支持渠道。
const supportedChannelType = 'wechat'
const channelType = computed(() => supportedChannelType)
const channelTypeRef = computed(() => channelType.value)
// activeChannel 当前始终落在微信；保留 computed 是为了让模板只依赖展示模型。
const activeChannel = computed(() => channels.find(channel => channel.type === channelType.value) ?? channels[0])

const { data: progress } = useChannelProgressQuery(appId, channelTypeRef)
const beginMutation = useBeginChannelAuth(appId, channelTypeRef)
const unbindMutation = useUnbindChannel(appId, channelTypeRef)

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

const statusLabel = computed(() => formatChannelStatus(progress.value?.status))

// canManage 控制发起登录和解绑按钮，真正权限仍由后端接口再次校验。
const canManage = computed(() => canManageApp(auth.user, app?.value))
const canUnbind = computed(() => canManage.value && progress.value?.status === 'bound')

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
  if (beginning.value) return '触发中…'
  if (challengeExpired.value) return '重新生成二维码'
  if (progress.value?.status === 'bound') return '重新登录'
  return '发起登录'
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

.channel-logo {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 8px;
  color: #ffffff;
  font-size: 14px;
  font-weight: 800;
  line-height: 1;
  flex-shrink: 0;
}

.channel-logo.large {
  width: 44px;
  height: 44px;
  border-radius: 10px;
  font-size: 17px;
}

.channel-logo.wechat {
  background: #1aad19;
}

.channel-logo.work-wechat,
.channel-logo.feishu,
.channel-logo.dingtalk,
.channel-logo.muted {
  background: #c7ccd1;
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

.channel-title h3,
.channel-title-kicker {
  margin: 0;
}

.channel-title h3 {
  font-size: 16px;
}

.channel-title-kicker {
  color: var(--color-text-secondary);
  font-size: 12px;
}

@media (max-width: 760px) {
  .channels-layout {
    grid-template-columns: minmax(0, 1fr);
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
