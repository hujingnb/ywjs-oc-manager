<template>
  <!-- 单条消息渲染：兼容字符串 content 与多模态 parts（文字/图片/文件）。
       assistant 文本走 markdown 渲染（标题/列表/代码/表格等），user 等其他角色保持纯文本，
       避免把用户原样输入当 markdown 解析造成意外格式化。
       字符串 content 中可能包含 <oc-file:FILEID> 标记（oc-ops 对历史文件的注记），
       渲染前先解析剥离，再将文件 id 渲染为可下载卡片。 -->
  <div class="message-view">
    <template v-if="typeof message.content === 'string'">
      <!-- eslint-disable-next-line vue/no-v-html — 内容经 markdown-it(html:false) 转义，原始 HTML 不会被渲染 -->
      <div v-if="isAssistant" class="markdown-body" v-html="renderMarkdown(stringParts.clean)" />
      <p v-else>{{ stringParts.clean }}</p>
      <!-- 解析出的 <oc-file:id> 标记逐个渲染为文件下载卡片 -->
      <a
        v-for="id in stringParts.fileIds"
        :key="id"
        class="file-card"
        :href="markerFileUrl(id)"
        target="_blank"
        rel="noopener"
      >
        📎 文件
      </a>
    </template>
    <template v-else-if="Array.isArray(message.content)">
      <template v-for="(p, i) in (message.content as any[])" :key="i">
        <template v-if="p.type === 'text'">
          <!-- eslint-disable-next-line vue/no-v-html — 同上，markdown-it 已转义原始 HTML -->
          <div v-if="isAssistant" class="markdown-body" v-html="renderMarkdown(p.text)" />
          <p v-else>{{ p.text }}</p>
        </template>
        <img
          v-else-if="p.type === 'image_url'"
          :src="imageUrl(p)"
          alt=""
          class="msg-image"
        />
        <!-- input_file part：图片类型直接预览，其它类型渲染为可下载文件卡片 -->
        <template v-else-if="p.type === 'input_file'">
          <img v-if="isImageFile(p)" :src="fileUrl(p)" alt="" class="msg-image" />
          <a v-else class="file-card" :href="fileUrl(p)" target="_blank" rel="noopener">
            📎 {{ p.filename || 'file' }}
          </a>
        </template>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import MarkdownIt from 'markdown-it'
import { computed } from 'vue'

import type { ConversationMessage } from '@/api/conversations'
import { conversationFileDownloadUrl } from '@/api/conversations'

// appId / sessionId 可选，用于构造文件下载 URL；不传时文件卡片 href 为空串（不可点击）。
const props = defineProps<{ message: ConversationMessage; appId?: string; sessionId?: string }>()

// markdown-it 模块级单例：避免每条消息重建解析器。
// html: false —— 不渲染源串中的原始 HTML 标签而是转义输出，配合 markdown-it 默认的
// 危险链接协议校验（javascript:/data: 等），可安全渲染来自 LLM 的不可信 assistant 输出，
// 无需额外引入 DOMPurify 之类的 sanitize 依赖。
// linkify —— 自动把裸 URL 识别为链接；breaks —— 单换行也转 <br>，更贴合对话逐行排版预期。
const md = new MarkdownIt({ html: false, linkify: true, breaks: true })

// 仅对 assistant 角色启用 markdown 渲染；user / system 等保持纯文本。
const isAssistant = computed(() => props.message.role === 'assistant')

// renderMarkdown 把 markdown 源串渲染为安全 HTML；空值兜底为空串避免 render 抛错。
function renderMarkdown(text: unknown): string {
  return md.render(typeof text === 'string' ? text : '')
}

// imageUrl 从多模态 image_url part 中提取图片地址；兼容字符串和 {url} 对象两种格式。
function imageUrl(p: { type: string; image_url?: string | { url?: string } }): string {
  const v = p.image_url
  if (typeof v === 'string') return v
  return v?.url ?? ''
}

// parseFileMarkers 从文字里解析所有 <oc-file:id> 标记，返回 fileId 列表与剥离标记后的纯文字。
// oc-ops 改写历史消息时，会在文字末尾追加 <oc-file:FILEID> 注记。
function parseFileMarkers(text: string): { fileIds: string[]; clean: string } {
  const fileIds: string[] = []
  const clean = text.replace(/<oc-file:([^>]+)>/g, (_m, id) => { fileIds.push(id); return '' }).trim()
  return { fileIds, clean }
}

// stringParts 对字符串 content 预处理：提取 <oc-file:id> 标记得到文件 id 列表，并返回剥离后的纯文字。
// 非字符串 content 时返回空结果，避免无效计算。
const stringParts = computed(() =>
  typeof props.message.content === 'string'
    ? parseFileMarkers(props.message.content)
    : { fileIds: [], clean: '' },
)

// isImageFile 判断 input_file part 是否图片（按 mime 或文件名扩展名）。
function isImageFile(p: { filename?: string; mime?: string }): boolean {
  const mime = p.mime ?? ''
  if (mime.startsWith('image/')) return true
  return /\.(jpe?g|png|gif|webp|bmp)$/i.test(p.filename ?? '')
}

// fileUrl 返回 input_file part 的下载/预览 URL；无 appId/sessionId/file_id 时返回空串。
function fileUrl(p: { file_id?: string }): string {
  if (!props.appId || !props.sessionId || !p.file_id) return ''
  return conversationFileDownloadUrl(props.appId, props.sessionId, p.file_id)
}

// markerFileUrl 返回文字标记里 fileId 对应的下载 URL。
function markerFileUrl(fileId: string): string {
  if (!props.appId || !props.sessionId) return ''
  return conversationFileDownloadUrl(props.appId, props.sessionId, fileId)
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

/* 文件卡片：内联弹性布局，类似附件标签；点击触发下载或跳转预览 */
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
  text-decoration: none;
  cursor: pointer;
  transition: background 0.15s;
}

.file-card:hover {
  background: var(--color-bg-hover, #f5f5f5);
}

/* markdown 渲染区基础排版：v-html 注入的内容不受 scoped 作用域影响，统一用 :deep 选择。
   首/尾元素去外边距，避免气泡内首行/末行多出空白；其余沿用项目色板变量。 */
.markdown-body {
  line-height: 1.6;
}
.markdown-body :deep(> *:first-child) {
  margin-top: 0;
}
.markdown-body :deep(> *:last-child) {
  margin-bottom: 0;
}
.markdown-body :deep(p) {
  margin: 0 0 8px;
}
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  margin: 12px 0 8px;
  font-weight: 600;
  line-height: 1.3;
}
.markdown-body :deep(h1) { font-size: 1.4em; }
.markdown-body :deep(h2) { font-size: 1.25em; }
.markdown-body :deep(h3) { font-size: 1.1em; }
.markdown-body :deep(h4) { font-size: 1em; }
.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  margin: 0 0 8px;
  padding-left: 22px;
}
.markdown-body :deep(li) {
  margin: 2px 0;
}
.markdown-body :deep(a) {
  color: var(--color-brand-text, #8a3700);
  text-decoration: underline;
}
/* 行内代码与代码块：浅底高亮，等宽字体，长内容横向滚动而非撑破气泡 */
.markdown-body :deep(code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 0.9em;
  background: var(--color-bg-hover, #f5f5f5);
  padding: 1px 5px;
  border-radius: 4px;
}
.markdown-body :deep(pre) {
  margin: 0 0 8px;
  padding: 10px 12px;
  background: var(--color-bg-hover, #f5f5f5);
  border-radius: 6px;
  overflow-x: auto;
}
.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
  font-size: 0.88em;
}
.markdown-body :deep(blockquote) {
  margin: 0 0 8px;
  padding: 2px 12px;
  border-left: 3px solid var(--color-border, #e5e7eb);
  color: var(--color-text-secondary, #6b7280);
}
.markdown-body :deep(table) {
  border-collapse: collapse;
  margin: 0 0 8px;
  font-size: 0.95em;
}
.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid var(--color-border, #e5e7eb);
  padding: 4px 8px;
}
.markdown-body :deep(th) {
  background: var(--color-bg-hover, #f5f5f5);
}
.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid var(--color-border, #e5e7eb);
  margin: 12px 0;
}
.markdown-body :deep(img) {
  max-width: 100%;
  border-radius: 6px;
}
</style>
