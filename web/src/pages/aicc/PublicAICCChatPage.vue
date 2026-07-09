<template>
  <main class="public-chat">
    <section class="chat-window">
      <header class="chat-header">
        <div class="agent-badge">
          <MessageCircle :size="22" />
        </div>
        <div>
          <p>AI Contact Center</p>
          <h1>{{ config?.name || '在线客服' }}</h1>
        </div>
        <n-tag :type="sessionToken ? 'success' : 'default'" :bordered="false">
          {{ sessionToken ? '在线' : '连接中' }}
        </n-tag>
      </header>

      <n-alert v-if="errorMessage" type="error" :bordered="false" class="inline-alert">
        {{ errorMessage }}
      </n-alert>

      <section ref="messageListEl" class="message-list" aria-label="客服消息">
        <article v-for="message in messages" :key="message.id" class="message-row" :class="message.role">
          <div class="bubble">
            <p v-if="message.text">{{ message.text }}</p>
            <img v-if="message.imageUrl" :src="message.imageUrl" alt="访客上传的图片" />
            <div v-if="message.role === 'assistant' && message.messageId" class="feedback-row">
              <button type="button" :disabled="message.feedbackSent" @click="sendFeedback(message, true)">
                <ThumbsUp :size="14" />
              </button>
              <button type="button" :disabled="message.feedbackSent" @click="sendFeedback(message, false)">
                <ThumbsDown :size="14" />
              </button>
              <span v-if="message.feedbackSent">已反馈</span>
            </div>
          </div>
        </article>
        <article v-if="isSending" class="message-row assistant">
          <div class="bubble typing">正在回复...</div>
        </article>
      </section>

      <section v-if="needsConsent" class="privacy-gate">
        <ShieldCheck :size="20" />
        <div>
          <strong>继续前请确认隐私说明</strong>
          <p>{{ privacyText }}</p>
        </div>
        <n-button type="primary" :loading="consentBusy" @click="acceptConsent">同意并开始</n-button>
      </section>
      <section v-else-if="privacyText" class="privacy-note">
        <ShieldCheck :size="16" />
        <span>{{ privacyText }}</span>
      </section>

      <form class="composer" @submit.prevent="submitMessage">
        <button class="icon-control" type="button" :disabled="!canSend" title="选择图片" @click="fileInputEl?.click()">
          <ImagePlus :size="18" />
        </button>
        <input
          ref="fileInputEl"
          class="hidden-input"
          type="file"
          accept="image/png,image/jpeg,image/gif,image/webp"
          @change="onFileChange"
        />
        <div v-if="pendingImage" class="pending-image">
          <img :src="pendingImage.previewUrl" alt="待发送图片" />
          <button type="button" title="移除图片" @click="clearPendingImage"><X :size="14" /></button>
        </div>
        <n-input
          v-model:value="draft"
          type="textarea"
          :autosize="{ minRows: 1, maxRows: 4 }"
          placeholder="输入您的问题"
          :disabled="!canSend"
          @keydown.enter.exact.prevent="submitMessage"
        />
        <n-button type="primary" attr-type="submit" :disabled="!canSubmit" :loading="isSending">
          <template #icon><Send :size="16" /></template>
          发送
        </n-button>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { NAlert, NButton, NInput, NTag } from 'naive-ui'
import {
  ImagePlus, MessageCircle, Send, ShieldCheck, ThumbsDown, ThumbsUp, X,
} from 'lucide-vue-next'

import {
  consentAICCPublicSession,
  createAICCPublicSession,
  fetchAICCPublicConfig,
  sendAICCPublicMessage,
  submitAICCPublicFeedback,
  uploadAICCPublicImage,
} from '@/api/hooks/useAICC'
import type { ApiError } from '@/api/client'
import type { AICCPublicConfig } from '@/domain/aicc'

// PublicAICCChatPage 是访客公开客服页，不依赖后台登录态。
// 会话 token 只保存在页面内存，刷新页面会重新创建会话，避免把访客凭证持久化到本地存储。
interface ChatMessage {
  id: string
  role: 'visitor' | 'assistant'
  text?: string
  imageUrl?: string
  messageId?: string
  feedbackSent?: boolean
}

interface PendingImage {
  file: File
  previewUrl: string
}

const route = useRoute()
const publicToken = computed(() => String(route.params.publicToken ?? ''))
const config = ref<AICCPublicConfig | null>(null)
const sessionToken = ref('')
const draft = ref('')
const messages = ref<ChatMessage[]>([])
const errorMessage = ref('')
const isSending = ref(false)
const consentBusy = ref(false)
const hasConsent = ref(false)
const pendingImage = ref<PendingImage | null>(null)
const messageListEl = ref<HTMLElement | null>(null)
const fileInputEl = ref<HTMLInputElement | null>(null)

const privacyText = computed(() => config.value?.privacy_text || '我们会使用本次对话内容来回答您的问题。')
const needsConsent = computed(() => config.value?.privacy_mode === 'consent_required' && !hasConsent.value)
const canSend = computed(() => Boolean(sessionToken.value) && !needsConsent.value && !isSending.value)
const canSubmit = computed(() => canSend.value && (draft.value.trim().length > 0 || Boolean(pendingImage.value)))

onMounted(() => {
  void boot()
})

onBeforeUnmount(() => {
  clearPendingImage()
})

async function boot() {
  errorMessage.value = ''
  try {
    config.value = await fetchAICCPublicConfig(publicToken.value)
    const session = await createAICCPublicSession(publicToken.value)
    sessionToken.value = session.session_token ?? ''
    hasConsent.value = config.value.privacy_mode !== 'consent_required'
    messages.value = [{
      id: crypto.randomUUID(),
      role: 'assistant',
      text: config.value.greeting || '您好，我是在线客服，请问有什么可以帮您？',
    }]
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '客服入口暂时不可用'
  }
}

async function acceptConsent() {
  if (!sessionToken.value) return
  consentBusy.value = true
  errorMessage.value = ''
  try {
    await consentAICCPublicSession(sessionToken.value)
    hasConsent.value = true
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '隐私确认失败'
  } finally {
    consentBusy.value = false
  }
}

async function submitMessage() {
  if (!canSubmit.value || !sessionToken.value) return
  const text = draft.value.trim()
  const image = pendingImage.value
  draft.value = ''
  pendingImage.value = null
  errorMessage.value = ''
  isSending.value = true
  messages.value.push({
    id: crypto.randomUUID(),
    role: 'visitor',
    text,
    imageUrl: image?.previewUrl,
  })
  await scrollToBottom()
  try {
    const imageResult = image ? await uploadAICCPublicImage(sessionToken.value, image.file) : null
    const response = await sendAICCPublicMessage(sessionToken.value, {
      text: text || undefined,
      image_file_id: imageResult?.image_file_id,
    })
    messages.value.push({
      id: crypto.randomUUID(),
      role: 'assistant',
      text: response.text || '我已收到，请继续补充您的问题。',
      messageId: response.message_id,
    })
  } catch (err) {
    errorMessage.value = publicMessageErrorText(err)
    if (image) pendingImage.value = image
  } finally {
    isSending.value = false
    await scrollToBottom()
  }
}

function publicMessageErrorText(err: unknown): string {
  if (isApiErrorCode(err, 'AICC_LEAD_REQUIRED')) {
    return '当前客服要求先提交联系信息；此公开页暂未收到可展示的留资字段配置，请联系企业管理员。'
  }
  return err instanceof Error ? err.message : '消息发送失败'
}

function isApiErrorCode(err: unknown, code: string): boolean {
  const apiErr = err as ApiError | undefined
  if (!apiErr || typeof apiErr !== 'object') return false
  const body = apiErr.body
  return typeof body === 'object' && body !== null && 'code' in body && (body as { code?: unknown }).code === code
}

async function sendFeedback(message: ChatMessage, helpful: boolean) {
  if (!sessionToken.value || !message.messageId || message.feedbackSent) return
  try {
    await submitAICCPublicFeedback(sessionToken.value, message.messageId, helpful)
    message.feedbackSent = true
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '反馈提交失败'
  }
}

function onFileChange(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  target.value = ''
  if (!file) return
  if (file.size > 10 * 1024 * 1024) {
    errorMessage.value = '图片不能超过 10MiB'
    return
  }
  clearPendingImage()
  pendingImage.value = { file, previewUrl: URL.createObjectURL(file) }
}

function clearPendingImage() {
  if (pendingImage.value) {
    URL.revokeObjectURL(pendingImage.value.previewUrl)
  }
  pendingImage.value = null
}

async function scrollToBottom() {
  await nextTick()
  if (messageListEl.value) {
    messageListEl.value.scrollTop = messageListEl.value.scrollHeight
  }
}
</script>

<style scoped>
.public-chat {
  min-height: 100vh;
  padding: 24px;
  background:
    linear-gradient(120deg, rgba(17, 24, 39, 0.92), rgba(31, 41, 55, 0.72)),
    radial-gradient(circle at top right, rgba(255, 106, 0, 0.22), transparent 30%),
    #111827;
}

.chat-window {
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr) auto auto;
  width: min(920px, 100%);
  height: calc(100vh - 48px);
  margin: 0 auto;
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.16);
  border-radius: 8px;
  background: #f8fafc;
  box-shadow: 0 24px 80px rgba(0, 0, 0, 0.28);
}

.chat-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--color-divider);
  background: #ffffff;
}

.chat-header > div:nth-child(2) {
  flex: 1;
  min-width: 0;
}

.chat-header p,
.chat-header h1,
.bubble p,
.privacy-gate p {
  margin: 0;
}

.chat-header p {
  color: var(--color-text-secondary);
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.chat-header h1 {
  margin-top: 2px;
  font-size: 20px;
}

.agent-badge {
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  border-radius: 8px;
  color: #ffffff;
  background: #111827;
}

.inline-alert {
  margin: 12px 16px 0;
}

.message-list {
  display: grid;
  align-content: start;
  gap: 12px;
  min-height: 0;
  padding: 18px;
  overflow: auto;
}

.message-row {
  display: flex;
}

.message-row.visitor {
  justify-content: flex-end;
}

.bubble {
  display: grid;
  gap: 8px;
  max-width: min(620px, 82%);
  padding: 11px 13px;
  border-radius: 8px;
  color: var(--color-text-primary);
  background: #ffffff;
  border: 1px solid var(--color-divider);
  line-height: 1.6;
  white-space: pre-wrap;
}

.visitor .bubble {
  color: #ffffff;
  background: #1f2937;
  border-color: #1f2937;
}

.bubble img {
  display: block;
  max-width: 220px;
  max-height: 180px;
  border-radius: 6px;
  object-fit: cover;
}

.typing {
  color: var(--color-text-secondary);
}

.feedback-row {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.feedback-row button,
.icon-control,
.pending-image button {
  display: grid;
  place-items: center;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: #ffffff;
  cursor: pointer;
}

.feedback-row button {
  width: 28px;
  height: 28px;
}

.privacy-gate,
.privacy-note {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0 16px 12px;
  padding: 12px;
  border: 1px solid var(--color-warning-border);
  border-radius: 8px;
  color: var(--color-warning-text);
  background: var(--color-warning-soft);
}

.privacy-gate > div {
  flex: 1;
  min-width: 0;
}

.privacy-note {
  color: var(--color-text-secondary);
  border-color: var(--color-divider);
  background: var(--color-surface-muted);
  font-size: 12px;
}

.composer {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  padding: 14px 16px;
  border-top: 1px solid var(--color-divider);
  background: #ffffff;
}

.icon-control {
  width: 36px;
  height: 36px;
  flex: 0 0 auto;
}

.hidden-input {
  display: none;
}

.pending-image {
  position: relative;
  flex: 0 0 auto;
}

.pending-image img {
  display: block;
  width: 44px;
  height: 44px;
  border-radius: 6px;
  object-fit: cover;
}

.pending-image button {
  position: absolute;
  top: -7px;
  right: -7px;
  width: 20px;
  height: 20px;
}

@media (max-width: 640px) {
  .public-chat {
    padding: 0;
  }

  .chat-window {
    width: 100%;
    height: 100vh;
    border: 0;
    border-radius: 0;
  }

  .composer {
    align-items: stretch;
    flex-wrap: wrap;
  }

  .composer :deep(.n-input) {
    flex-basis: calc(100% - 46px);
  }
}
</style>
