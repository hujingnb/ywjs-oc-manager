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
            <div v-if="message.sources?.length" class="source-list">
              <span v-for="source in message.sources" :key="source.reference_id || source.url || source.title" class="source-label">
                {{ source.title || t('aicc.publicChat.sourceLabel') }}
                <em v-if="source.unconfirmed">{{ t('aicc.publicChat.unconfirmedNetwork') }}</em>
              </span>
            </div>
            <img v-if="message.imageUrl" :src="message.imageUrl" :alt="t('aicc.publicChat.uploadedImageAlt')" />
            <p v-if="message.status" class="message-status">{{ messageStatusText(message.status, Boolean(message.clientMessageId)) }}</p>
            <n-button v-if="message.status === 'failed' && message.clientMessageId" size="small" secondary @click="retryPendingMessage(message.pendingKey)">
              {{ t('aicc.publicChat.retry') }}
            </n-button>
          </div>
          <time v-if="formatMessageTime(message.sentAt)" class="message-time">{{ formatMessageTime(message.sentAt) }}</time>
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
        <div class="action-buttons">
          <n-button type="primary" attr-type="submit" :loading="leadBusy">{{ t('aicc.publicChat.submitLead') }}</n-button>
          <n-button secondary :disabled="leadBusy" @click="declineLeadInvitation">{{ t('aicc.publicChat.declineLead') }}</n-button>
        </div>
      </form>
      <section v-else-if="showResolutionCard" class="resolution-card">
        <strong>{{ t('aicc.publicChat.resolutionTitle') }}</strong>
        <div class="action-buttons">
          <n-button type="primary" :loading="resolutionBusy === 'resolved'" @click="markSessionResolution('resolved')">{{ t('aicc.publicChat.resolved') }}</n-button>
          <n-button secondary :loading="resolutionBusy === 'unresolved'" @click="markSessionResolution('unresolved')">{{ t('aicc.publicChat.unresolved') }}</n-button>
          <n-button quaternary @click="hideResolutionCard">{{ t('aicc.publicChat.continueChat') }}</n-button>
        </div>
      </section>
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
  ImagePlus, MessageCircle, Plus, Send, ShieldCheck, X,
} from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import {
  consentAICCPublicSession,
  createAICCPublicSession,
  clearAICCPublicStoredSessionToken,
  declineAICCPublicLeadInvitation,
  fetchAICCPublicConfig,
  fetchAICCPublicMessageStatus,
  fetchAICCPublicSession,
  readAICCPublicStoredSessionToken,
  sendAICCPublicMessage,
  submitAICCPublicLeadValues,
  updateAICCPublicSessionResolution,
  uploadAICCPublicImage,
} from '@/api/hooks/useAICC'
import type { ApiError } from '@/api/client'
import type { AICCLeadField, AICCMessage, AICCPublicConfig, AICCPublicMessageResult, AICCResponseSource } from '@/domain/aicc'
import { normalizeAICCPublicChannel } from '@/domain/aicc'

// PublicAICCChatPage 是访客公开客服页，不依赖后台登录态。
// 会话 token 由 API hook 按 publicToken + channel 写入 localStorage，用于刷新后的短期续接。
interface ChatMessage {
  id: string
  role: 'visitor' | 'assistant'
  text?: string
  imageUrl?: string
  // sentAt 是用于公开聊天页展示的发送时间；服务端历史消息沿用 created_at，即时消息在浏览器创建。
  sentAt?: string
  // status 仅用于当前浏览器内存中的异步助手占位，历史完成消息不携带该字段。
  status?: AICCPublicMessageResult['status']
  // clientMessageId 用于失败占位的手动重试，始终复用首次提交的幂等键。
  clientMessageId?: string
  // pendingKey 是浏览器内存中的任务索引；旧消息没有幂等键时以消息 ID 继续轮询。
  pendingKey?: string
  nextAction?: string
  sources?: AICCResponseSource[]
}

interface PendingImage {
  file: File
  previewUrl: string
}

// PendingPublicMessage 保存异步任务重试所需的原始请求数据，避免失败重试重新创建访客气泡或幂等键。
interface PendingPublicMessage {
  // key 统一作为 pending map 与定时器索引；它不必是可手动重试的 client_message_id。
  key: string
  clientMessageId?: string
  placeholderId: string
  text: string
  image?: PendingImage
  imageFileId?: string
  messageId?: string
  sessionToken?: string
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
const resolutionCardDismissed = ref(false)
const messageListEl = ref<HTMLElement | null>(null)
const fileInputEl = ref<HTMLInputElement | null>(null)
const pendingPublicMessages = new Map<string, PendingPublicMessage>()
const pollTimers = new Map<string, ReturnType<typeof setTimeout>>()
let isPageActive = false

// 公开轮询的最小、默认与最大等待时间；服务端异常值和网络错误都不能形成紧凑循环。
const aiccPublicMinPollSeconds = 1
const aiccPublicDefaultPollSeconds = 2
const aiccPublicMaxPollSeconds = 30

const privacyText = computed(() => config.value?.privacy_text || t('aicc.publicChat.defaultPrivacyText'))
const needsConsent = computed(() => config.value?.privacy_mode === 'consent_required' && !hasConsent.value)
const leadFields = computed<AICCLeadField[]>(() => config.value?.lead_fields ?? [])
const lastAssistantMessage = computed(() => [...messages.value].reverse().find(message => message.role === 'assistant' && !message.status))
// 留资只在服务端明确给出 offer_lead 后展示，不能在对话开始前阻塞访客输入。
const showLeadForm = computed(() => lastAssistantMessage.value?.nextAction === 'offer_lead' && leadFields.value.length > 0 && !leadComplete.value && !needsConsent.value)
const showResolutionCard = computed(() => lastAssistantMessage.value?.nextAction === 'ask_resolution' && !resolutionCardDismissed.value && !needsConsent.value && resolutionStatus.value === 'unknown')
const canSend = computed(() => Boolean(config.value) && !needsConsent.value && !isSending.value)
const canSubmit = computed(() => canSend.value && (draft.value.trim().length > 0 || Boolean(pendingImage.value)))
const hasVisitorMessage = computed(() => messages.value.some(message => message.role === 'visitor'))
// notice 模式的隐私说明只用于进入页面时告知访客，访客开始对话后隐藏以减少输入区占用。
const showPrivacyNotice = computed(() => Boolean(privacyText.value) && !hasVisitorMessage.value)

// 公开端只接受与 file input accept 约束一致的图片类型，避免访客选择无效文件后再等待服务端拒绝。
const aiccPublicImageTypes = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp'])

onMounted(() => {
  isPageActive = true
  void boot()
})

onBeforeUnmount(() => {
  isPageActive = false
  clearAllMessagePolls()
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
  const clientMessageId = crypto.randomUUID()
  const placeholderId = crypto.randomUUID()
  const sentAt = new Date().toISOString()
  draft.value = ''
  pendingImage.value = null
  errorMessage.value = ''
  isSending.value = true
  messages.value.push({
    id: crypto.randomUUID(),
    role: 'visitor',
    text,
    imageUrl: image?.previewUrl,
    sentAt,
  })
  messages.value.push({
    id: placeholderId,
    role: 'assistant',
    status: 'queued',
    clientMessageId,
    pendingKey: clientMessageId,
    sentAt,
  })
  pendingPublicMessages.set(clientMessageId, { key: clientMessageId, clientMessageId, placeholderId, text, image: image ?? undefined })
  await scrollToBottom()
  try {
    await sendPendingMessage(clientMessageId)
  } catch (err) {
    errorMessage.value = publicMessageErrorText(err)
    setPendingMessageStatus(clientMessageId, 'failed')
  } finally {
    isSending.value = false
    await scrollToBottom()
  }
}

// sendPendingMessage 首次提交和访客手动重试共用同一条路径，确保 client_message_id 在整个任务生命周期内保持稳定。
async function sendPendingMessage(clientMessageId: string) {
  const pending = pendingPublicMessages.get(clientMessageId)
  if (!pending?.clientMessageId) return
  const token = pending.sessionToken || await ensureSessionReadyForSend()
  pending.sessionToken = token
  if (!pending.imageFileId && pending.image) {
    const imageResult = await uploadAICCPublicImage(token, pending.image.file)
    pending.imageFileId = imageResult.image_file_id
  }
  const response = await sendAICCPublicMessage(token, {
    client_message_id: pending.clientMessageId,
    text: pending.text || undefined,
    image_file_id: pending.imageFileId,
  })
  pending.messageId = response.message_id
  applyPublicMessageStatus(pending, response)
}

// retryPendingMessage 为 failed 状态提供显式恢复入口；复用待处理记录而不是重新插入访客消息。
async function retryPendingMessage(pendingKey?: string) {
  if (!pendingKey || isSending.value) return
  const pending = pendingPublicMessages.get(pendingKey)
  if (!pending?.clientMessageId) return
  errorMessage.value = ''
  isSending.value = true
  setPendingMessageStatus(pendingKey, 'queued')
  try {
    await sendPendingMessage(pendingKey)
  } catch (err) {
    errorMessage.value = publicMessageErrorText(err)
    setPendingMessageStatus(pendingKey, 'failed')
  } finally {
    isSending.value = false
    await scrollToBottom()
  }
}

// applyPublicMessageStatus 将后端任务状态映射为唯一的助手占位气泡，并在完成后停止本消息的轮询。
function applyPublicMessageStatus(pending: PendingPublicMessage, result: AICCPublicMessageResult) {
  // 新建会话或页面卸载已清除该记录时，忽略仍在飞行中的旧请求响应。
  if (!pendingPublicMessages.has(pending.key)) return
  const status = result.status || 'queued'
  if (status === 'completed') {
    const message = messages.value.find(item => item.id === pending.placeholderId)
    if (message) {
      message.status = undefined
      message.text = result.text || ''
      message.nextAction = result.next_action
      message.sources = result.sources
    }
    stopMessagePolling(pending.key)
    pendingPublicMessages.delete(pending.key)
    return
  }
  setPendingMessageStatus(pending.key, status)
  if (status === 'failed') {
    stopMessagePolling(pending.key)
    return
  }
  scheduleMessagePoll(pending.key, result.retry_after_seconds)
}

// setPendingMessageStatus 只更新关联占位，不影响同一会话内其他正在处理的消息。
function setPendingMessageStatus(clientMessageId: string, status: AICCPublicMessageResult['status']) {
  const pending = pendingPublicMessages.get(clientMessageId)
  const message = pending && messages.value.find(item => item.id === pending.placeholderId)
  if (message) message.status = status
}

// scheduleMessagePoll 根据服务端 retry_after_seconds 安排下次查询；未指定时采用两秒默认间隔。
function scheduleMessagePoll(pendingKey: string, retryAfterSeconds?: number) {
  stopMessagePolling(pendingKey)
  const seconds = retryAfterSeconds ?? aiccPublicDefaultPollSeconds
  const waitMilliseconds = Math.min(aiccPublicMaxPollSeconds, Math.max(aiccPublicMinPollSeconds, seconds)) * 1_000
  const timer = setTimeout(() => {
    pollTimers.delete(pendingKey)
    void pollPendingMessage(pendingKey)
  }, waitMilliseconds)
  pollTimers.set(pendingKey, timer)
}

// pollPendingMessage 在页面仍存活且任务仍未结束时查询状态，网络瞬时错误保持排队并按默认间隔继续查询。
async function pollPendingMessage(pendingKey: string) {
  const pending = pendingPublicMessages.get(pendingKey)
  if (!isPageActive || !pending?.messageId || !pending.sessionToken) return
  try {
    const result = await fetchAICCPublicMessageStatus(pending.sessionToken, pending.messageId)
    if (!isPageActive || !pendingPublicMessages.has(pendingKey)) return
    applyPublicMessageStatus(pending, result)
    await scrollToBottom()
  } catch {
    if (!isPageActive || !pendingPublicMessages.has(pendingKey)) return
    setPendingMessageStatus(pendingKey, 'retry_wait')
    scheduleMessagePoll(pendingKey, aiccPublicDefaultPollSeconds)
  }
}

// stopMessagePolling 清除指定任务的定时器，避免重试或完成后形成重复轮询。
function stopMessagePolling(pendingKey: string) {
  const timer = pollTimers.get(pendingKey)
  if (timer) clearTimeout(timer)
  pollTimers.delete(pendingKey)
}

// clearAllMessagePolls 在卸载或新建对话时统一释放定时器，防止旧会话回写新页面状态。
function clearAllMessagePolls() {
  for (const pendingKey of pollTimers.keys()) stopMessagePolling(pendingKey)
  pendingPublicMessages.clear()
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
  clearAllMessagePolls()
  clearAICCPublicStoredSessionToken(publicToken.value, publicChannel.value)
  sessionToken.value = ''
  draft.value = ''
  errorMessage.value = ''
  isSending.value = false
  resolutionBusy.value = ''
  resolutionStatus.value = 'unknown'
  resolutionCardDismissed.value = false
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
    sentAt: new Date().toISOString(),
  }]
}

async function restoreSessionMessages(token: string) {
  const detail = await fetchAICCPublicSession(token)
  resolutionStatus.value = detail.resolution_status || 'unknown'
  if (detail.lead_status === 'complete' || detail.lead_status === 'skipped') {
    leadComplete.value = true
    deferredLeadValues.value = null
  }
  const restored: ChatMessage[] = []
  const pendingToPoll: Array<{ pendingKey: string; retryAfterSeconds?: number }> = []
  for (const message of detail.messages) {
    const chatMessage = toChatMessage(message)
    if (!chatMessage) continue
    restored.push(chatMessage)
    if (message.direction !== 'visitor' || !isPendingPublicMessageStatus(message.task_status)) continue
    const clientMessageId = message.client_message_id || undefined
    const pendingKey = clientMessageId || `message:${message.id}`
    const placeholderId = crypto.randomUUID()
    const pending: PendingPublicMessage = {
      key: pendingKey,
      clientMessageId,
      placeholderId,
      text: message.text || '',
      messageId: message.id,
      sessionToken: token,
    }
    // 旧会话没有 client_message_id 时仍可按消息 ID 轮询，但不能凭空新建幂等键进行手动重试。
    pendingPublicMessages.set(pendingKey, pending)
    restored.push({
      id: placeholderId,
      role: 'assistant',
      status: message.task_status,
      clientMessageId,
      pendingKey,
      sentAt: message.created_at,
    })
    if (message.task_status !== 'failed') {
      pendingToPoll.push({ pendingKey, retryAfterSeconds: message.retry_after_seconds })
    }
  }
  if (restored.length > 0) {
    messages.value = restored
    for (const pending of pendingToPoll) scheduleMessagePoll(pending.pendingKey, pending.retryAfterSeconds)
    return
  }
  resetMessagesToGreeting()
}

// isPendingPublicMessageStatus 标识刷新后仍需展示助手占位的任务状态；完成任务已有持久化助手消息，无需重建。
function isPendingPublicMessageStatus(status?: string): status is 'queued' | 'processing' | 'retry_wait' | 'failed' {
  return status === 'queued' || status === 'processing' || status === 'retry_wait' || status === 'failed'
}

function toChatMessage(message: AICCMessage): ChatMessage | null {
  if (message.direction !== 'visitor' && message.direction !== 'assistant') return null
  return {
    id: message.id,
    role: message.direction === 'visitor' ? 'visitor' : 'assistant',
    text: message.text,
    sentAt: message.created_at,
    nextAction: message.next_action,
    sources: message.sources,
  }
}

// messageStatusText 将运行时状态转换成访客可理解的提示，避免把内部任务枚举直接展示到公开页。
function messageStatusText(status: AICCPublicMessageResult['status'], canRetry = true): string {
  if (status === 'processing') return t('aicc.publicChat.busy')
  if (status === 'retry_wait') return t('aicc.publicChat.retrying')
  if (status === 'failed') return canRetry ? t('aicc.publicChat.failed') : t('aicc.publicChat.failedNoRetry')
  return t('aicc.publicChat.queued')
}

// formatMessageTime 将服务端 ISO 时间或浏览器即时记录按访客本地时区收敛为 HH:mm；无效值不展示，避免暴露 Invalid Date。
function formatMessageTime(value?: string): string {
  if (!value) return ''
  const time = new Date(value)
  if (!Number.isFinite(time.getTime())) return ''
  return time.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
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
  if (isApiErrorCode(error, 'AICC_SENSITIVE_WORD') || text.includes('AICC_SENSITIVE_WORD')) return t('aicc.publicChat.sensitiveWord')
  if (isApiErrorCode(error, 'AICC_MESSAGE_LIMIT_EXCEEDED') || text.includes('AICC_MESSAGE_LIMIT_EXCEEDED')) return t('aicc.publicChat.messageLimit')
  if (isApiErrorCode(error, 'AICC_VISITOR_BLOCKED') || text.includes('AICC_VISITOR_BLOCKED')) return t('aicc.publicChat.visitorBlocked')
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
    resolutionCardDismissed.value = true
  } catch (err) {
    errorMessage.value = friendlyAICCError(err)
  } finally {
    resolutionBusy.value = ''
  }
}

// declineLeadInvitation 只在访客明确拒绝时调用，保留匿名继续咨询的能力。
async function declineLeadInvitation() {
  if (!sessionToken.value || leadBusy.value) return
  leadBusy.value = true
  try {
    await declineAICCPublicLeadInvitation(sessionToken.value)
    leadComplete.value = true
  } catch (err) {
    errorMessage.value = friendlyAICCError(err)
  } finally {
    leadBusy.value = false
  }
}

function hideResolutionCard() {
  resolutionCardDismissed.value = true
}

function onFileChange(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  target.value = ''
  if (!file) return
  if (!aiccPublicImageTypes.has(file.type)) {
    errorMessage.value = t('aicc.publicChat.imageTypeInvalid')
    return
  }
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
  flex-direction: column;
  align-items: flex-start;
}

.message-row.visitor {
  align-items: flex-end;
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

.message-time {
  margin-top: 4px;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1;
}

.bubble img {
  display: block;
  max-width: 220px;
  max-height: 180px;
  border-radius: 6px;
  object-fit: cover;
}

.source-list,
.action-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.source-label {
  max-width: 100%;
  overflow: hidden;
  padding: 2px 6px;
  border-radius: 999px;
  color: var(--color-text-secondary);
  background: var(--color-surface-muted);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-label em {
  margin-left: 4px;
  color: var(--color-warning-text);
  font-style: normal;
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

.resolution-card {
  display: grid;
  gap: 10px;
  margin: 0 16px 12px;
  padding: 12px;
  border: 1px solid var(--color-brand);
  border-radius: 8px;
  background: #ffffff;
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

  .chat-header {
    gap: 8px;
    padding: 12px;
  }

  .header-actions {
    max-width: 96px;
  }

  .message-list,
  .composer {
    padding-right: 12px;
    padding-left: 12px;
  }

  .lead-fields {
    grid-template-columns: 1fr;
  }
}
</style>
