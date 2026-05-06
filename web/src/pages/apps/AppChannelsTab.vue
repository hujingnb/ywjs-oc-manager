<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Channels</p>
        <h2>渠道绑定</h2>
      </div>
      <div class="topbar-actions">
        <button class="primary-button" type="button" :disabled="!appId || beginning" @click="beginAuth">
          {{ primaryButtonLabel }}
        </button>
        <button
          v-if="showRefreshChallenge"
          class="secondary-button"
          type="button"
          :disabled="beginning"
          @click="beginAuth"
        >
          {{ beginning ? '生成中…' : '刷新二维码' }}
        </button>
        <button v-if="canUnbind" class="secondary-button" type="button" @click="unbind">解绑</button>
      </div>
    </div>

    <div v-if="!appId" class="state-text">请选择目标应用</div>
    <template v-else>
      <p class="state-text">
        当前状态：
        <strong>{{ statusLabel }}</strong>
        <span v-if="progress?.bound_identity"> ｜ 已绑定：{{ progress.bound_identity }}</span>
      </p>
      <p v-if="progress?.error_message" class="state-text danger">最近错误：{{ progress.error_message }}</p>
      <p v-if="isWaitingForChallenge" class="state-text">正在生成登录二维码…</p>
      <p v-if="challengeExpired" class="state-text danger">
        当前二维码已过期，请点击右上角"刷新二维码"重新生成。
      </p>

      <AuthChallengeRenderer :challenge="visibleChallenge" />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'

import AuthChallengeRenderer from '@/components/AuthChallengeRenderer.vue'
import {
  useBeginChannelAuth,
  useChannelProgressQuery,
  useUnbindChannel,
  channelChallengeFromProgress,
  shouldShowChallengePending,
  type ChannelChallenge,
} from '@/api/hooks/useChannel'

const props = defineProps<{ appId?: string; channelType?: string }>()

const appId = toRef(props, 'appId')
const channelType = computed(() => props.channelType ?? 'wechat')
const channelTypeRef = computed(() => channelType.value)

const { data: progress } = useChannelProgressQuery(appId, channelTypeRef)
const beginMutation = useBeginChannelAuth(appId, channelTypeRef)
const unbindMutation = useUnbindChannel(appId, channelTypeRef)

const beginning = ref(false)
const challenge = ref<ChannelChallenge | null>(null)
const authStarted = ref(false)

const statusLabel = computed(() => {
  if (!progress.value) return '未发起'
  return progress.value.status
})

const canUnbind = computed(() => progress.value?.status === 'bound')

const progressChallenge = computed<ChannelChallenge | null>(() => {
  return channelChallengeFromProgress(progress.value, channelType.value)
})

const visibleChallenge = computed(() => progressChallenge.value ?? (challenge.value?.qrcode || challenge.value?.code ? challenge.value : null))
const isWaitingForChallenge = computed(() => shouldShowChallengePending(authStarted.value, visibleChallenge.value, progress.value?.status))

// 当前显示的 challenge 是否已过期：expires_at 早于现在。
// 使用 Date.now() 在 computed 内即可在每次重渲染时（progress 轮询触发）重新求值。
const challengeExpired = computed(() => {
  const expiresAt = visibleChallenge.value?.expires_at
  if (!expiresAt) return false
  const ts = Date.parse(expiresAt)
  return Number.isFinite(ts) && ts < Date.now()
})

// 满足以下任一条件时显示「刷新二维码」按钮：
// (1) 已经发起过登录；(2) 当前 binding 状态是 pending_auth/expired/failed；(3) 当前 challenge 已过期。
// "发起登录"按钮始终可见，但当用户已经走过一次流程后，"刷新"是更准确的语义。
const showRefreshChallenge = computed(() => {
  if (challengeExpired.value) return true
  const status = progress.value?.status
  if (status === 'pending_auth' || status === 'expired' || status === 'failed') return true
  return false
})

// 主按钮文案随状态调整：首次/未绑定 → 发起登录；进行中 → 触发中；已 bound → 重新登录；
// challenge 过期 → "重新生成二维码"。
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

async function beginAuth() {
  beginning.value = true
  try {
    challenge.value = await beginMutation.mutateAsync()
    authStarted.value = true
  } finally {
    beginning.value = false
  }
}

async function unbind() {
  await unbindMutation.mutateAsync()
  authStarted.value = false
  challenge.value = null
}
</script>
