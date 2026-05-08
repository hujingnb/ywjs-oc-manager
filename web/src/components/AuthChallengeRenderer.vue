<template>
  <div class="challenge-renderer">
    <div v-if="!challenge" class="state-text">尚未发起挑战</div>
    <template v-else-if="challenge.challenge_type === 'qrcode' && challenge.qrcode">
      <!--
        Sprint 0 实测：上游 wechat plugin 的 qrcode 字段是纯文本 URL
        （形如 https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3），
        不是图片二进制，所以不能直接 <img :src=...>。这里用 qrcode 库把 URL
        重新编码为 PNG dataURL 渲染出来，用户用微信扫描即可登录。
      -->
      <img v-if="qrDataUrl" :src="qrDataUrl" alt="登录二维码" class="challenge-qr" />
      <p v-else class="state-text">正在生成二维码…</p>
      <p v-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
      <p class="state-text">{{ expiresLabel }}</p>
      <p class="state-text fallback-hint">
        若扫码失败，可手动打开此链接以继续：
        <a :href="challenge.qrcode" target="_blank" rel="noopener">{{ challenge.qrcode }}</a>
      </p>
    </template>
    <template v-else-if="challenge.challenge_type === 'code' && challenge.code">
      <p class="challenge-code">{{ challenge.code }}</p>
      <p class="state-text">{{ expiresLabel }}</p>
    </template>
    <p v-else class="state-text danger">未知挑战类型：{{ challenge.challenge_type ?? challenge.status }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import QRCode from 'qrcode'

import type { ChannelChallenge } from '@/api/hooks/useChannel'

const props = defineProps<{ challenge?: ChannelChallenge | null }>()

const qrDataUrl = ref<string>('')
const errorMessage = ref<string>('')

const expiresLabel = computed(() => {
  if (!props.challenge?.expires_at) return ''
  const date = new Date(props.challenge.expires_at)
  if (Number.isNaN(date.getTime())) return ''
  return `挑战将于 ${date.toLocaleString('zh-CN', { hour12: false })} 过期`
})

watch(
  () => props.challenge?.qrcode,
  async (raw) => {
    qrDataUrl.value = ''
    errorMessage.value = ''
    if (!raw) {
      return
    }
    if (raw.startsWith('data:image/')) {
      // 兼容旧契约：上游若已直接给 base64 image，跳过本地编码。
      qrDataUrl.value = raw
      return
    }
    try {
      qrDataUrl.value = await QRCode.toDataURL(raw, {
        margin: 1,
        width: 240,
        errorCorrectionLevel: 'M',
      })
    } catch (err) {
      errorMessage.value = `二维码生成失败：${err instanceof Error ? err.message : String(err)}`
    }
  },
  { immediate: true },
)
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
  background: #ffffff; /* 保持白底确保 QR 可读 */
}

.challenge-code {
  margin: 0;
  padding: 16px;
  border: 1px dashed rgba(0, 240, 255, 0.3);
  border-radius: 8px;
  background: rgba(15, 21, 53, 0.8);
  color: #00F0FF;
  font-size: 22px;
  font-weight: 800;
  letter-spacing: 4px;
}

.fallback-hint {
  font-size: 12px;
  color: #8A94C6;
  word-break: break-all;
}
</style>
