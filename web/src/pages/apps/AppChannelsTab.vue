<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">App · Channels</p>
        <h2 style="margin: 0">渠道绑定</h2>
      </div>
    </template>
    <template #header-extra>
      <n-space :size="8">
        <n-button type="primary" :disabled="!appId || !canManage || beginning" @click="beginAuth">
          {{ primaryButtonLabel }}
        </n-button>
        <n-button v-if="showRefreshChallenge" :disabled="!canManage || beginning" @click="beginAuth">
          {{ beginning ? '生成中…' : '刷新二维码' }}
        </n-button>
        <n-button v-if="canUnbind" @click="unbind">解绑</n-button>
      </n-space>
    </template>

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
  shouldShowChallengePending,
  type ChannelChallenge,
} from '@/api/hooks/useChannel'
import { canManageApp } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ appId?: string; channelType?: string }>()

const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
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

const canManage = computed(() => canManageApp(auth.user, app?.value))
const canUnbind = computed(() => canManage.value && progress.value?.status === 'bound')

const progressChallenge = computed<ChannelChallenge | null>(() => {
  return channelChallengeFromProgress(progress.value, channelType.value)
})

const visibleChallenge = computed(() => progressChallenge.value ?? (challenge.value?.qrcode || challenge.value?.code ? challenge.value : null))
const isWaitingForChallenge = computed(() => shouldShowChallengePending(authStarted.value, visibleChallenge.value, progress.value?.status))

const challengeExpired = computed(() => {
  const expiresAt = visibleChallenge.value?.expires_at
  if (!expiresAt) return false
  const ts = Date.parse(expiresAt)
  return Number.isFinite(ts) && ts < Date.now()
})

const showRefreshChallenge = computed(() => {
  if (challengeExpired.value) return true
  const status = progress.value?.status
  if (status === 'pending_auth' || status === 'expired' || status === 'failed') return true
  return false
})

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
  if (!canManage.value) return
  beginning.value = true
  try {
    challenge.value = await beginMutation.mutateAsync()
    authStarted.value = true
  } finally {
    beginning.value = false
  }
}

async function unbind() {
  if (!canManage.value) return
  await unbindMutation.mutateAsync()
  authStarted.value = false
  challenge.value = null
}
</script>
