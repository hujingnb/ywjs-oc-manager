<template>
  <div class="challenge-renderer">
    <div v-if="!challenge" class="state-text">尚未发起挑战</div>
    <template v-else-if="challenge.challenge_type === 'qrcode' && challenge.qrcode">
      <img :src="challenge.qrcode" alt="登录二维码" class="challenge-qr" />
      <p class="state-text">{{ expiresLabel }}</p>
    </template>
    <template v-else-if="challenge.challenge_type === 'code' && challenge.code">
      <p class="challenge-code">{{ challenge.code }}</p>
      <p class="state-text">{{ expiresLabel }}</p>
    </template>
    <p v-else class="state-text danger">未知挑战类型：{{ challenge.challenge_type ?? challenge.status }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { ChannelChallenge } from '@/api/hooks/useChannel'

const props = defineProps<{ challenge?: ChannelChallenge | null }>()

const expiresLabel = computed(() => {
  if (!props.challenge?.expires_at) return ''
  const date = new Date(props.challenge.expires_at)
  if (Number.isNaN(date.getTime())) return ''
  return `挑战将于 ${date.toLocaleString('zh-CN', { hour12: false })} 过期`
})
</script>

<style scoped>
.challenge-renderer {
  display: grid;
  gap: 8px;
  margin-top: 16px;
  text-align: center;
}

.challenge-qr {
  margin: 0 auto;
  max-width: 240px;
  border-radius: 8px;
  background: #ffffff;
}

.challenge-code {
  margin: 0;
  padding: 16px;
  border: 1px dashed #cfd8e5;
  border-radius: 8px;
  background: #f8fafc;
  font-size: 22px;
  font-weight: 800;
  letter-spacing: 4px;
}
</style>
