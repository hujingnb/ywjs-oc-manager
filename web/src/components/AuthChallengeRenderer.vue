<template>
  <div class="challenge-renderer">
    <template v-if="challenge?.challenge_type === 'qrcode' && challenge.qrcode">
      <!--
        Sprint 0 实测：上游 wechat plugin 的 qrcode 字段是纯文本 URL
        （形如 https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3），
        不是图片二进制，所以不能直接 <img :src=...>。这里用 qrcode 库把 URL
        重新编码为 PNG dataURL 渲染出来，用户用微信扫描即可登录。
      -->
      <img v-if="qrDataUrl" :src="qrDataUrl" :alt="t('components.authChallengeRenderer.qrcodeAlt')" class="challenge-qr" />
      <p v-else class="state-text">{{ t('components.authChallengeRenderer.generatingQrcode') }}</p>
      <p v-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
      <p class="state-text">{{ expiresLabel }}</p>
      <p class="state-text fallback-hint">
        {{ t('components.authChallengeRenderer.fallbackHint') }}
        <a :href="challenge.qrcode" target="_blank" rel="noopener">{{ challenge.qrcode }}</a>
      </p>
    </template>
    <template v-else-if="challenge?.challenge_type === 'code' && challenge.code">
      <p class="challenge-code">{{ challenge.code }}</p>
      <p class="state-text">{{ expiresLabel }}</p>
    </template>
    <p v-else-if="challenge" class="state-text danger">{{ t('components.authChallengeRenderer.unknownChallenge', { type: challenge.challenge_type ?? challenge.status }) }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import QRCode from 'qrcode'
import { useI18n } from 'vue-i18n'

import type { ChannelChallenge } from '@/api/hooks/useChannel'

// AuthChallengeRenderer 负责展示渠道登录挑战，当前覆盖扫码和验证码两类挑战。
// challenge 可能来自立即发起登录的响应，也可能来自轮询进度接口。
const props = defineProps<{ challenge?: ChannelChallenge | null }>()

// rendered 事件在二维码 dataURL 设置完成（img 即将渲染新内容）时触发。
// 父组件用这个事件作为"刷新二维码 loading 结束"的判据，避免在新图实际可见前误判结束。
const emit = defineEmits<{ (e: 'rendered', qrcode: string): void }>()

const { t } = useI18n()

// qrDataUrl 保存本地编码后的二维码图片；上游给纯 URL 时必须先转成 dataURL 才能渲染。
const qrDataUrl = ref<string>('')
// errorMessage 只表示前端二维码编码失败，不覆盖渠道认证本身的后端错误。
const errorMessage = ref<string>('')

// expiresLabel 只在后端提供有效过期时间时显示，避免无效时间字符串误导用户。
const expiresLabel = computed(() => {
  if (!props.challenge?.expires_at) return ''
  const date = new Date(props.challenge.expires_at)
  if (Number.isNaN(date.getTime())) return ''
  return t('components.authChallengeRenderer.expiresAt', {
    datetime: date.toLocaleString('zh-CN', { hour12: false }),
  })
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
      emit('rendered', raw)
      return
    }
    try {
      qrDataUrl.value = await QRCode.toDataURL(raw, {
        margin: 1,
        width: 240,
        errorCorrectionLevel: 'M',
      })
      emit('rendered', raw)
    } catch (err) {
      errorMessage.value = t('components.authChallengeRenderer.qrcodeGenFailed', {
        message: err instanceof Error ? err.message : String(err),
      })
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
  border: 1px dashed var(--color-border);
  border-radius: 6px;
  background: var(--color-info-soft);
  color: var(--color-info);
  font-size: 22px;
  font-weight: 800;
  letter-spacing: 4px;
}

.fallback-hint {
  font-size: 12px;
  color: var(--color-text-secondary);
  word-break: break-all;
}
</style>
