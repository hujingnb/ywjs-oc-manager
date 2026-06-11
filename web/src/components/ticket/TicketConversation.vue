<template>
  <section class="ticket-conversation">
    <div class="message-list">
      <div v-if="!messages.length" class="message-empty">暂无消息</div>
      <article
        v-for="message in messages"
        :key="message.id"
        class="message-item"
        :class="message.author_user_id === currentUserId ? 'mine' : 'theirs'"
      >
        <div class="message-meta">
          <span>{{ message.author_user_id === currentUserId ? '我' : '对方' }}</span>
          <time>{{ fmtTime(message.created_at) }}</time>
        </div>
        <div class="message-bubble">
          <p v-if="message.kind === 'text'" class="message-text">{{ message.text }}</p>
          <button
            v-else-if="message.kind === 'image'"
            class="image-message"
            type="button"
            @click="downloadTicketMessage(ticketId, message)"
          >
            <img v-if="imageUrls[message.id]" :src="imageUrls[message.id]" :alt="message.file_name || '图片消息'" />
            <span v-else>图片加载中</span>
          </button>
          <button v-else class="file-message" type="button" @click="downloadTicketMessage(ticketId, message)">
            <span class="file-name">{{ message.file_name || '文件' }}</span>
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
        placeholder="输入消息"
        @keydown.ctrl.enter.prevent="onSendText"
      />
      <div class="composer-actions">
        <input ref="fileInput" class="file-input" type="file" @change="onPickFile" />
        <n-button size="small" @click="triggerFileInput">图片/文件</n-button>
        <n-button
          type="primary"
          size="small"
          :loading="sendMut.isPending.value"
          :disabled="!text.trim()"
          @click="onSendText"
        >
          发送
        </n-button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { NButton, NInput, useMessage } from 'naive-ui'

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
const ticketIDRef = ref<string | undefined>(props.ticketId)
const sendMut = useSendTicketMessage(ticketIDRef)
const uploadMut = useUploadTicketMessage(ticketIDRef)
const fileInput = ref<HTMLInputElement | null>(null)
const text = ref('')
const imageUrls = reactive<Record<string, string>>({})

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
    message.error('消息发送失败')
  }
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
    message.error('文件发送失败')
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
