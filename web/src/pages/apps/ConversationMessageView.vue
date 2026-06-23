<template>
  <!-- 单条消息渲染：兼容字符串 content 与多模态 parts（文字/图片）。 -->
  <div class="message-view">
    <p v-if="typeof message.content === 'string'">{{ message.content }}</p>
    <template v-else-if="Array.isArray(message.content)">
      <template v-for="(p, i) in (message.content as any[])" :key="i">
        <p v-if="p.type === 'text'">{{ p.text }}</p>
        <img
          v-else-if="p.type === 'image_url'"
          :src="imageUrl(p)"
          alt=""
          class="msg-image"
        />
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import type { ConversationMessage } from '@/api/conversations'

defineProps<{ message: ConversationMessage }>()

// imageUrl 从多模态 image_url part 中提取图片地址；兼容字符串和 {url} 对象两种格式。
function imageUrl(p: { type: string; image_url?: string | { url?: string } }): string {
  const v = p.image_url
  if (typeof v === 'string') return v
  return v?.url ?? ''
}
</script>

<style scoped>
.message-view {
  word-break: break-word;
}
.msg-image {
  max-width: 240px;
  border-radius: 6px;
  display: block;
  margin-top: 4px;
}
</style>
