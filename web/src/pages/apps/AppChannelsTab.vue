<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Channels</p>
        <h2>渠道绑定</h2>
      </div>
      <div class="topbar-actions">
        <button class="primary-button" type="button" :disabled="!appId || beginning" @click="beginAuth">
          {{ beginning ? '触发中…' : '发起登录' }}
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

      <AuthChallengeRenderer :challenge="challenge" />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, toRef } from 'vue'

import AuthChallengeRenderer from '@/components/AuthChallengeRenderer.vue'
import {
  useBeginChannelAuth,
  useChannelProgressQuery,
  useUnbindChannel,
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

const statusLabel = computed(() => {
  if (!progress.value) return '未发起'
  return progress.value.status
})

const canUnbind = computed(() => progress.value?.status === 'bound')

async function beginAuth() {
  beginning.value = true
  try {
    challenge.value = await beginMutation.mutateAsync()
  } finally {
    beginning.value = false
  }
}

async function unbind() {
  await unbindMutation.mutateAsync()
}
</script>
