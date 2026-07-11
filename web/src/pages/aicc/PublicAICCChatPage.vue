<template>
  <main class="public-chat">
    <section class="chat-window">
      <header class="chat-header">
        <div class="agent-badge">
          <MessageCircle :size="22" />
        </div>
        <div>
          <p>AI Integrated Customer Care</p>
          <h1>{{ config?.name || t('aicc.publicChat.defaultTitle') }}</h1>
        </div>
        <div class="header-actions">
          <n-tag :type="sessionToken ? 'success' : 'default'" :bordered="false">
            {{ sessionToken ? t('aicc.publicChat.online') : t('aicc.publicChat.ready') }}
          </n-tag>
          <n-button size="small" secondary :type="isResolved ? 'success' : 'default'" :disabled="!sessionToken || isResolved || isSending" :loading="resolutionBusy === 'resolved'" @click="markSessionResolution('resolved')">
            <template #icon><CheckCircle2 :size="14" /></template>
            {{ t('aicc.publicChat.resolved') }}
          </n-button>
          <n-button size="small" secondary :type="isUnresolved ? 'warning' : 'default'" :disabled="!sessionToken || isUnresolved || isSending" :loading="resolutionBusy === 'unresolved'" @click="markSessionResolution('unresolved')">
            <template #icon><CircleAlert :size="14" /></template>
            {{ t('aicc.publicChat.unresolved') }}
          </n-button>
          <n-button size="small" secondary :disabled="isSending" @click="startNewConversation">
            <template #icon><Plus :size="14" /></template>
            {{ t('aicc.publicChat.newConversation') }}
          </n-button>
        </div>
      </header>

      <n-alert v-if="errorMessage" type="error" :bordered="false" class="inline-alert">
        {{ errorMessage }}
      </n-alert>

      <section ref="messageListEl" class="message-list" :aria-label="t('aicc.publicChat.messageListLabel')">
        <article v-for="message in messages" :key="message.id" class="message-row" :class="message.role">
          <div class="bubble">
            <p v-if="message.text">{{ message.text }}</p>
            <img v-if="message.imageUrl" :src="message.imageUrl" :alt="t('aicc.publicChat.uploadedImageAlt')" />
          </div>
        </article>
        <article v-if="isSending" class="message-row assistant">
          <div class="bubble typing">{{ t('aicc.publicChat.typing') }}</div>
        </article>
      </section>

      <section v-if="needsConsent" class="privacy-gate">
        <ShieldCheck :size="20" />
        <div>
          <strong>{{ t('aicc.publicChat.consentTitle') }}</strong>
          <p>{{ privacyText }}</p>
        </div>
        <n-button type="primary" :loading="consentBusy" @click="acceptConsent">{{ t('aicc.publicChat.consentButton') }}</n-button>
      </section>
      <form v-else-if="showLeadForm" class="lead-gate" @submit.prevent="submitLeadForm">
        <div class="lead-gate-heading">
          <ShieldCheck :size="18" />
          <strong>{{ t('aicc.publicChat.leadTitle') }}</strong>
        </div>
        <div class="lead-fields">
          <label v-for="field in leadFields" :key="field.field_key">
            <span>{{ field.label }}{{ field.required ? ' *' : '' }}</span>
            <n-input
              v-model:value="leadValues[field.field_key]"
              :type="field.field_type === 'number' ? 'text' : 'text'"
              :placeholder="field.prompt_text || field.label"
              :input-props="leadFieldInputProps(field)"
            />
          </label>
        </div>
        <n-button type="primary" attr-type="submit" :loading="leadBusy">{{ t('aicc.publicChat.submitLead') }}</n-button>
      </form>
      <p v-else-if="showPrivacyNotice" class="privacy-copy">{{ privacyText }}</p>

      <form class="composer" @submit.prevent="submitMessage">
        <button class="icon-control" type="button" :disabled="!canSend" :title="t('aicc.publicChat.chooseImage')" @click="fileInputEl?.click()">
          <ImagePlus :size="18" />
        </button>
        <input
          ref="fileInputEl"
          id="aicc-public-image"
          name="aicc_public_image"
          class="hidden-input"
          type="file"
          accept="image/png,image/jpeg,image/gif,image/webp"
          @change="onFileChange"
        />
        <div v-if="pendingImage" class="pending-image">
          <img :src="pendingImage.previewUrl" :alt="t('aicc.publicChat.pendingImageAlt')" />
          <button type="button" :title="t('aicc.publicChat.removeImage')" @click="clearPendingImage"><X :size="14" /></button>
        </div>
        <n-input
          v-model:value="draft"
          type="textarea"
          :autosize="{ minRows: 1, maxRows: 4 }"
          :placeholder="t('aicc.publicChat.composerPlaceholder')"
          :disabled="!canSend"
          :input-props="{ name: 'aicc-public-message' }"
          @keydown.enter.exact.prevent="submitMessage"
        />
        <n-button type="primary" attr-type="submit" :disabled="!canSubmit" :loading="isSending">
          <template #icon><Send :size="16" /></template>
          {{ t('aicc.publicChat.send') }}
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
  CheckCircle2, CircleAlert, ImagePlus, MessageCircle, Plus, Send, ShieldCheck, X,
} from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import {
  consentAICCPublicSession,
  createAICCPublicSession,
  clearAICCPublicStoredSessionToken,
  fetchAICCPublicConfig,
  fetchAICCPublicSession,
  readAICCPublicStoredSessionToken,
  sendAICCPublicMessage,
  submitAICCPublicLeadValues,
  updateAICCPublicSessionResolution,
  uploadAICCPublicImage,
} from '@/api/hooks/useAICC'
import type { ApiError } from '@/api/client'
import type { AICCLeadField, AICCMessage, AICCPublicConfig } from '@/domain/aicc'
import { normalizeAICCPublicChannel } from '@/domain/aicc'

// PublicAICCChatPage 是访客公开客服页，不依赖后台登录态。
// 会话 token 由 API hook 按 publicToken + channel 写入 localStorage，用于刷新后的短期续接。
interface ChatMessage {
  id: string
  role: 'visitor' | 'assistant'
  text?: string
  imageUrl?: string
}

interface PendingImage {
  file: File
  previewUrl: string
}

const route = useRoute()
const { t } = useI18n()
const publicToken = computed(() => String(route.params.publicToken ?? ''))
const publicChannel = computed(() => normalizeAICCPublicChannel(route.query.aicc_channel))
const config = ref<AICCPublicConfig | null>(null)
const sessionToken = ref('')
const draft = ref('')
const messages = ref<ChatMessage[]>([])
const errorMessage = ref('')
const isSending = ref(false)
const resolutionBusy = ref<'resolved' | 'unresolved' | ''>('')
const consentBusy = ref(false)
const leadBusy = ref(false)
const leadComplete = ref(false)
const deferredLeadValues = ref<Record<string, string> | null>(null)
const hasConsent = ref(false)
const leadValues = ref<Record<string, string>>({})
const pendingImage = ref<PendingImage | null>(null)
const resolutionStatus = ref<'resolved' | 'unresolved' | 'unknown' | string>('unknown')
const messageListEl = ref<HTMLElement | null>(null)
const fileInputEl = ref<HTMLInputElement | null>(null)

const privacyText = computed(() => config.value?.privacy_text || t('aicc.publicChat.defaultPrivacyText'))
const needsConsent = computed(() => config.value?.privacy_mode === 'consent_required' && !hasConsent.value)
const leadFields = computed<AICCLeadField[]>(() => config.value?.lead_fields ?? [])
const needsLead = computed(() => leadFields.value.some(field => field.required) && !leadComplete.value)
const showLeadForm = computed(() => leadFields.value.length > 0 && !leadComplete.value && !needsConsent.value)
const canSend = computed(() => Boolean(config.value) && !needsConsent.value && !needsLead.value && !isSending.value)
const canSubmit = computed(() => canSend.value && (draft.value.trim().length > 0 || Boolean(pendingImage.value)))
const hasVisitorMessage = computed(() => messages.value.some(message => message.role === 'visitor'))
const isResolved = computed(() => resolutionStatus.value === 'resolved')
const isUnresolved = computed(() => resolutionStatus.value === 'unresolved')
// notice 模式的隐私说明只用于进入页面时告知访客，访客开始对话后隐藏以减少输入区占用。
const showPrivacyNotice = computed(() => Boolean(privacyText.value) && !hasVisitorMessage.value)

onMounted(() => {
  void boot()
})

onBeforeUnmount(() => {
  clearPendingImage()
})

async function boot() {
  errorMessage.value = ''
  try {
    config.value = await fetchAICCPublicConfig(publicToken.value, publicChannel.value)
    sessionToken.value = readAICCPublicStoredSessionToken(publicToken.value, publicChannel.value)
    hasConsent.value = config.value.privacy_mode !== 'consent_required'
    leadValues.value = Object.fromEntries((config.value.lead_fields ?? []).map(field => [field.field_key, '']))
    leadComplete.value = !(config.value.lead_fields ?? []).some(field => field.required)
    if (sessionToken.value) {
      await restoreSessionMessages(sessionToken.value)
    } else {
      resetMessagesToGreeting()
    }
  } catch (err) {
    errorMessage.value = friendlyAICCError(err)
  }
}

async function submitLeadForm() {
  const values: Record<string, string> = {}
  for (const field of leadFields.value) {
    const value = (leadValues.value[field.field_key] ?? '').trim()
    if (field.required && !value) {
      errorMessage.value = t('aicc.publicChat.missingField', { label: field.label })
      return
    }
    if (value) values[field.field_key] = value
  }
  if (Object.keys(values).length === 0) {
    leadComplete.value = true
    deferredLeadValues.value = null
    return
  }
  leadBusy.value = true
  errorMessage.value = ''
  try {
    // 留资字段先保存在浏览器内存中，等首次发送消息创建 session 后再提交。
    deferredLeadValues.value = values
    leadComplete.value = true
  } catch (err) {
    errorMessage.value = friendlyAICCError(err)
  } finally {
    leadBusy.value = false
  }
}

async function acceptConsent() {
  consentBusy.value = true
  errorMessage.value = ''
  try {
    // 仅记录访客已点击同意；服务端同意动作延迟到首次消息创建 session 后执行。
    hasConsent.value = true
  } catch (err) {
    errorMessage.value = friendlyAICCError(err)
  } finally {
    consentBusy.value = false
  }
}

async function submitMessage() {
  if (!canSubmit.value) return
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
    const token = await ensureSessionReadyForSend()
    const imageResult = image ? await uploadAICCPublicImage(token, image.file) : null
    const response = await sendAICCPublicMessage(token, {
      text: text || undefined,
      image_file_id: imageResult?.image_file_id,
    })
    messages.value.push({
      id: crypto.randomUUID(),
      role: 'assistant',
      text: response.text || t('aicc.publicChat.defaultAssistantReply'),
    })
  } catch (err) {
    errorMessage.value = publicMessageErrorText(err)
    if (image) pendingImage.value = image
  } finally {
    isSending.value = false
    await scrollToBottom()
  }
}

async function ensureSessionReadyForSend(): Promise<string> {
  if (!sessionToken.value) {
    const session = await createAICCPublicSession(publicToken.value, publicChannel.value)
    sessionToken.value = session.session_token ?? ''
    resolutionStatus.value = 'unknown'
  }
  if (!sessionToken.value) {
    throw new Error(t('aicc.publicChat.sendFailed'))
  }
  if (config.value?.privacy_mode === 'consent_required') {
    await consentAICCPublicSession(sessionToken.value)
  }
  if (deferredLeadValues.value && Object.keys(deferredLeadValues.value).length > 0) {
    const result = await submitAICCPublicLeadValues(sessionToken.value, deferredLeadValues.value)
    if (result.lead_status !== 'complete') {
      throw new Error(t('aicc.publicChat.missingRequired'))
    }
    deferredLeadValues.value = null
  }
  return sessionToken.value
}

function startNewConversation() {
  clearAICCPublicStoredSessionToken(publicToken.value, publicChannel.value)
  sessionToken.value = ''
  draft.value = ''
  errorMessage.value = ''
  isSending.value = false
  resolutionBusy.value = ''
  resolutionStatus.value = 'unknown'
  deferredLeadValues.value = null
  leadValues.value = Object.fromEntries(leadFields.value.map(field => [field.field_key, '']))
  leadComplete.value = !leadFields.value.some(field => field.required)
  hasConsent.value = config.value?.privacy_mode !== 'consent_required'
  clearPendingImage()
  resetMessagesToGreeting()
}

function resetMessagesToGreeting() {
  messages.value = [{
    id: crypto.randomUUID(),
    role: 'assistant',
    text: config.value?.greeting || t('aicc.publicChat.defaultGreeting'),
  }]
}

async function restoreSessionMessages(token: string) {
  const detail = await fetchAICCPublicSession(token)
  resolutionStatus.value = detail.resolution_status || 'unknown'
  if (detail.lead_status === 'complete' || detail.lead_status === 'skipped') {
    leadComplete.value = true
    deferredLeadValues.value = null
  }
  const restored = detail.messages.map(toChatMessage).filter((message): message is ChatMessage => Boolean(message))
  if (restored.length > 0) {
    messages.value = restored
    return
  }
  resetMessagesToGreeting()
}

function toChatMessage(message: AICCMessage): ChatMessage | null {
  if (message.direction !== 'visitor' && message.direction !== 'assistant') return null
  return {
    id: message.id,
    role: message.direction === 'visitor' ? 'visitor' : 'assistant',
    text: message.text,
  }
}

function leadFieldInputProps(field: AICCLeadField) {
  let inputmode: 'numeric' | 'tel' | 'email' | 'text' = 'text'
  if (field.field_type === 'number') inputmode = 'numeric'
  if (field.field_type === 'phone') inputmode = 'tel'
  if (field.field_type === 'email') inputmode = 'email'
  return {
    id: `aicc-public-lead-${field.field_key}`,
    name: `aicc_public_lead_${field.field_key}`,
    inputmode,
  }
}

function publicMessageErrorText(err: unknown): string {
  if (isApiErrorCode(err, 'AICC_LEAD_REQUIRED')) {
    return t('aicc.publicChat.leadRequired')
  }
  return friendlyAICCError(err)
}

function friendlyAICCError(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error || '')
  if (text.includes('AICC_SENSITIVE_WORD')) return t('aicc.publicChat.sensitiveWord')
  if (text.includes('AICC_MESSAGE_LIMIT_EXCEEDED')) return t('aicc.publicChat.messageLimit')
  if (text.includes('AICC_VISITOR_BLOCKED')) return t('aicc.publicChat.visitorBlocked')
  return text || t('aicc.publicChat.sendFailed')
}

function isApiErrorCode(err: unknown, code: string): boolean {
  const apiErr = err as ApiError | undefined
  if (!apiErr || typeof apiErr !== 'object') return false
  const body = apiErr.body
  return typeof body === 'object' && body !== null && 'code' in body && (body as { code?: unknown }).code === code
}

async function markSessionResolution(status: 'resolved' | 'unresolved') {
  if (!sessionToken.value || resolutionStatus.value === status || resolutionBusy.value) return
  resolutionBusy.value = status
  errorMessage.value = ''
  try {
    const result = await updateAICCPublicSessionResolution(sessionToken.value, status)
    resolutionStatus.value = result.resolution_status || status
  } catch (err) {
    errorMessage.value = friendlyAICCError(err)
  } finally {
    resolutionBusy.value = ''
  }
}

function onFileChange(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  target.value = ''
  if (!file) return
  if (file.size > 10 * 1024 * 1024) {
    errorMessage.value = t('aicc.publicChat.imageTooLarge')
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
  display: flex;
  flex-direction: column;
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

.header-actions {
  display: flex;
  flex: 0 0 auto;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  justify-content: flex-end;
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
  flex: 1 1 auto;
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

.icon-control,
.pending-image button {
  display: grid;
  place-items: center;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: #ffffff;
  cursor: pointer;
}

.privacy-gate,
.lead-gate {
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

.lead-gate {
  display: grid;
  align-items: stretch;
  border-color: var(--color-brand);
  color: var(--color-text-primary);
  background: #ffffff;
}

.lead-gate-heading {
  display: flex;
  align-items: center;
  gap: 8px;
}

.lead-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.lead-fields label {
  display: grid;
  gap: 5px;
  min-width: 0;
  color: var(--color-text-secondary);
  font-size: 12px;
}

.privacy-copy {
  margin: 0 16px 8px;
  color: var(--color-text-tertiary);
  font-size: 12px;
  line-height: 1.5;
}

.composer {
  flex: 0 0 auto;
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr) auto;
  align-items: end;
  gap: 10px;
  padding: 14px 16px;
  border-top: 1px solid var(--color-divider);
  background: #ffffff;
}

.composer :deep(.n-input) {
  min-width: 0;
}

.composer :deep(.n-input-wrapper) {
  min-height: 36px;
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

  .chat-header {
    align-items: flex-start;
  }

  .header-actions {
    max-width: 118px;
  }

  .composer {
    grid-template-columns: 36px minmax(0, 1fr) auto;
    align-items: end;
  }

  .lead-fields {
    grid-template-columns: 1fr;
  }
}
</style>
