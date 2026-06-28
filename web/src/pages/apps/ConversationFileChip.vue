<template>
  <!-- 统一渲染历史文件：图片走鉴权 objectURL 预览，其它类型渲染为可点击下载卡片。
       历史文件下载端点在 user 组下需 Bearer，浏览器对 <img src>/<a href> 不带 header 必 401，
       故图片与下载都改走 apiDownload（带 Bearer，跟随 manager 302→S3 预签名）取 blob。 -->
  <img v-if="isImage && objectUrl" :src="objectUrl" alt="" class="msg-image" />
  <button
    v-else
    type="button"
    class="file-card"
    :disabled="downloading"
    @click="onDownload"
  >
    📎 {{ filename || '文件' }}
  </button>
</template>

<script setup lang="ts">
import { useMessage } from 'naive-ui'
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { downloadConversationFile, loadConversationFileObjectUrl } from '@/api/conversations'

// appId / sessionId 可选：缺任一则无法构造下载端点，图片不发请求、显示降级卡片。
const props = defineProps<{
  appId?: string
  sessionId?: string
  fileId: string
  filename?: string
}>()

const message = useMessage()

// isImage 按文件名扩展名判定是否图片（marker 形态没有 mime，只能凭文件名）。
const isImage = /\.(jpe?g|png|gif|webp|bmp)$/i.test(props.filename ?? '')

// objectUrl 预览图片的 object URL；加载失败或非图片时为空，模板回退到下载卡片。
const objectUrl = ref('')
// downloading 下载中标志，用于禁用按钮避免重复触发。
const downloading = ref(false)

// onMounted 时若为图片且具备 appId/sessionId，则带鉴权拉取 blob 生成预览 URL；
// 失败（如文件缺失/网络错误）时静默降级为下载卡片，不打断会话渲染。
onMounted(async () => {
  if (!isImage || !props.appId || !props.sessionId) return
  try {
    objectUrl.value = await loadConversationFileObjectUrl(props.appId, props.sessionId, props.fileId)
  } catch {
    objectUrl.value = ''
  }
})

// onBeforeUnmount 释放 object URL，避免内存泄漏。
onBeforeUnmount(() => {
  if (objectUrl.value) URL.revokeObjectURL(objectUrl.value)
})

// onDownload 点击卡片时带鉴权下载文件；缺 appId/sessionId 时不可下载，失败用 toast 提示。
async function onDownload() {
  if (!props.appId || !props.sessionId || downloading.value) return
  downloading.value = true
  try {
    await downloadConversationFile(props.appId, props.sessionId, props.fileId, props.filename)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '文件下载失败')
  } finally {
    downloading.value = false
  }
}
</script>

<style scoped>
.msg-image {
  max-width: 240px;
  border-radius: 6px;
  display: block;
  margin-top: 4px;
}

/* 文件卡片：内联弹性布局，类似附件标签；点击触发鉴权下载 */
.file-card {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  margin-top: 4px;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 6px;
  font-size: 13px;
  color: var(--color-text-primary, #1f2329);
  background: var(--color-surface, #fff);
  cursor: pointer;
  transition: background 0.15s;
}

.file-card:hover:not(:disabled) {
  background: var(--color-bg-hover, #f5f5f5);
}

.file-card:disabled {
  cursor: default;
  opacity: 0.6;
}
</style>
