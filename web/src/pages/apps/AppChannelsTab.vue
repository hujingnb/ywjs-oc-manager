<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Channels</p>
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

    <div v-if="!appId" class="state-text">请选择目标实例</div>
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

// AppChannelsTab 负责应用渠道登录绑定流程，当前默认处理 wechat 渠道。
// appId 和 channelType 来自路由，父级注入的 app 用于判断当前用户是否可管理。
const props = defineProps<{ appId?: string; channelType?: string }>()

const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
const appId = toRef(props, 'appId')
const channelType = computed(() => props.channelType ?? 'wechat')
const channelTypeRef = computed(() => channelType.value)

const { data: progress } = useChannelProgressQuery(appId, channelTypeRef)
const beginMutation = useBeginChannelAuth(appId, channelTypeRef)
const unbindMutation = useUnbindChannel(appId, channelTypeRef)

// beginning 是本地提交态，覆盖 beginMutation 尚未返回挑战内容前的按钮文案。
const beginning = ref(false)
// challenge 保存本次发起登录立即返回的挑战，轮询进度若有更新会优先使用 progressChallenge。
const challenge = ref<ChannelChallenge | null>(null)
// authStarted 区分“用户已点发起但后端还未返回二维码”和“完全未开始”的展示状态。
const authStarted = ref(false)

const statusLabel = computed(() => {
  if (!progress.value) return '未发起'
  return progress.value.status
})

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

// beginAuth 发起渠道登录 mutation；成功后保存挑战，渠道侧失败由进度轮询暴露 error_message，mutation 传输错误仍会冒泡到调用方或控制台。
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

// unbind 解绑后清空本地挑战状态，等待查询失效后由 hook 拉取最新绑定进度。
async function unbind() {
  if (!canManage.value) return
  await unbindMutation.mutateAsync()
  authStarted.value = false
  challenge.value = null
}
</script>
