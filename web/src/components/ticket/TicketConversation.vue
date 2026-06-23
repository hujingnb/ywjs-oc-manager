<template>
  <section class="ticket-conversation">
    <div class="message-list">
      <div v-if="!messages.length" class="message-empty">{{ t('components.ticketConversation.noMessages') }}</div>
      <article
        v-for="message in messages"
        :key="message.id"
        class="message-item"
        :class="message.author_user_id === currentUserId ? 'mine' : 'theirs'"
      >
        <div class="message-meta">
          <span>{{ message.author_user_id === currentUserId ? t('components.ticketConversation.authorMe') : t('components.ticketConversation.authorOther') }}</span>
          <time>{{ fmtTime(message.created_at) }}</time>
        </div>
        <div class="message-bubble">
          <p v-if="message.kind === 'text'" class="message-text">{{ message.text }}</p>
          <button
            v-else-if="message.kind === 'image'"
            class="image-message"
            type="button"
            @click="onImageMessageClick(message)"
          >
            <img v-if="imageUrls[message.id]" :src="imageUrls[message.id]" :alt="message.file_name || t('components.ticketConversation.imageAlt')" />
            <span v-else>{{ t('components.ticketConversation.imagePlaceholder') }}</span>
          </button>
          <button v-else class="file-message" type="button" @click="downloadTicketMessage(ticketId, message)">
            <span class="file-name">{{ message.file_name || t('components.ticketConversation.fileDefault') }}</span>
            <span class="file-size">{{ formatSize(message.file_size) }}</span>
          </button>
        </div>
      </article>
    </div>

    <div class="composer">
      <n-input
        v-model:value="text"
        type="textarea"
        :autosize="{ minRows: 2, maxRows: 5 }"
        :placeholder="t('components.ticketConversation.inputPlaceholder')"
        @keydown.ctrl.enter.prevent="onSendText"
      />
      <div class="composer-actions">
        <input ref="fileInput" class="file-input" type="file" @change="onPickFile" />
        <n-button size="small" @click="triggerFileInput">{{ t('components.ticketConversation.attachBtn') }}</n-button>
        <n-button
          type="primary"
          size="small"
          :loading="sendMut.isPending.value"
          :disabled="!text.trim()"
          @click="onSendText"
        >
          {{ t('components.ticketConversation.sendBtn') }}
        </n-button>
      </div>
    </div>

    <n-modal v-model:show="previewOpen" preset="card" :style="{ width: 'min(960px, calc(100vw - 48px))' }">
      <div class="image-preview-modal">
        <img
          v-if="previewImage"
          :src="previewImage.url"
          :alt="previewImage.alt"
          class="preview-image"
        />
      </div>
    </n-modal>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { NButton, NInput, NModal, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import type { SkillTicketMessage } from '@/api'
import {
  downloadTicketMessage,
  fetchTicketMessageBlobUrl,
  useSendTicketMessage,
  useUploadTicketMessage,
} from '@/api/hooks/useSkillTickets'

const props = withDefaults(
  defineProps<{
    ticketId: string
    messages: SkillTicketMessage[]
    currentUserId?: string
  }>(),
  { currentUserId: undefined },
)

const message = useMessage()
const { t } = useI18n()
const ticketIDRef = ref<string | undefined>(props.ticketId)
const sendMut = useSendTicketMessage(ticketIDRef)
const uploadMut = useUploadTicketMessage(ticketIDRef)
const fileInput = ref<HTMLInputElement | null>(null)
const text = ref('')
const imageUrls = reactive<Record<string, string>>({})
const previewOpen = ref(false)
const previewImage = ref<{ id: string; url: string; alt: string } | null>(null)

watch(
  () => props.ticketId,
  (id) => {
    ticketIDRef.value = id
  },
)

watch(
  () => props.messages,
  async (messages) => {
    const imageIDs = new Set(messages.filter((item) => item.kind === 'image').map((item) => item.id))
    for (const id of Object.keys(imageUrls)) {
      if (!imageIDs.has(id)) {
        if (previewImage.value?.id === id) {
          previewOpen.value = false
          previewImage.value = null
        }
        URL.revokeObjectURL(imageUrls[id])
        delete imageUrls[id]
      }
    }
    for (const item of messages) {
      if (item.kind !== 'image' || imageUrls[item.id]) continue
      try {
        imageUrls[item.id] = await fetchTicketMessageBlobUrl(props.ticketId, item)
      } catch {
        // 图片预览失败不阻断对话渲染,用户仍可点击消息尝试下载原文件。
      }
    }
  },
  { immediate: true, deep: true },
)

watch(previewOpen, (open) => {
  if (!open) previewImage.value = null
})

onBeforeUnmount(() => {
  for (const url of Object.values(imageUrls)) {
    URL.revokeObjectURL(url)
  }
})

async function onSendText() {
  const body = text.value.trim()
  if (!body) return
  try {
    await sendMut.mutateAsync({ text: body })
    text.value = ''
  } catch {
    message.error(t('components.ticketConversation.sendFailed'))
  }
}

function onImageMessageClick(item: SkillTicketMessage) {
  const url = imageUrls[item.id]
  if (!url) {
    downloadTicketMessage(props.ticketId, item)
    return
  }
  previewImage.value = {
    id: item.id,
    url,
    alt: item.file_name || t('components.ticketConversation.imageAlt'),
  }
  previewOpen.value = true
}

function triggerFileInput() {
  fileInput.value?.click()
}

async function onPickFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    await uploadMut.mutateAsync(file)
    input.value = ''
  } catch {
    message.error(t('components.ticketConversation.uploadFailed'))
  }
}

function fmtTime(value: string | undefined) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatSize(size: number | undefined) {
  if (!size) return ''
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(1)} MB`
}
</script>

<style scoped>
.ticket-conversation {
  display: grid;
  gap: 14px;
}

.message-list {
  display: grid;
  gap: 12px;
}

.message-empty {
  color: #64748b;
  font-size: 13px;
}

.message-item {
  display: grid;
  gap: 4px;
  max-width: min(680px, 88%);
}

.message-item.mine {
  justify-self: end;
}

.message-item.theirs {
  justify-self: start;
}

.message-meta {
  display: flex;
  gap: 8px;
  color: #64748b;
  font-size: 12px;
}

.message-item.mine .message-meta {
  justify-content: flex-end;
}

.message-bubble {
  border: 1px solid #dbe4ef;
  border-radius: 8px;
  padding: 10px 12px;
  background: #fff;
}

.message-item.mine .message-bubble {
  background: #ecfeff;
  border-color: #99f6e4;
}

.message-text {
  margin: 0;
  white-space: pre-wrap;
}

.image-message,
.file-message {
  border: 0;
  padding: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.image-message img {
  display: block;
  max-width: 220px;
  max-height: 160px;
  border-radius: 6px;
  object-fit: cover;
}

.image-preview-modal {
  display: grid;
  place-items: center;
}

.preview-image {
  display: block;
  max-width: 100%;
  max-height: min(76vh, 760px);
  border-radius: 8px;
  object-fit: contain;
}

.file-message {
  display: grid;
  gap: 2px;
}

.file-name {
  font-weight: 600;
}

.file-size {
  color: #64748b;
  font-size: 12px;
}

.composer {
  display: grid;
  gap: 8px;
}

.composer-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.file-input {
  display: none;
}
</style>
