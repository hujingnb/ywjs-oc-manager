<template>
  <!-- 单条消息渲染：兼容字符串 content 与多模态 parts（文字/图片）。
       assistant 文本走 markdown 渲染（标题/列表/代码/表格等），user 等其他角色保持纯文本，
       避免把用户原样输入当 markdown 解析造成意外格式化。 -->
  <div class="message-view">
    <template v-if="typeof message.content === 'string'">
      <!-- eslint-disable-next-line vue/no-v-html — 内容经 markdown-it(html:false) 转义，原始 HTML 不会被渲染 -->
      <div v-if="isAssistant" class="markdown-body" v-html="renderMarkdown(message.content)" />
      <p v-else>{{ message.content }}</p>
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
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import MarkdownIt from 'markdown-it'
import { computed } from 'vue'

import type { ConversationMessage } from '@/api/conversations'

const props = defineProps<{ message: ConversationMessage }>()

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
