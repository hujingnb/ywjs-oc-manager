# 对话任务进行中的消息排队 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让实例会话页在 AI 流式回复进行中不再禁用输入区，用户发送的消息进入可见可控队列，当前回复结束后逐条串行自动发送，中途失败则停止并保留失败项供重试。

**Architecture:** 纯前端改动。把队列的纯数据操作抽到 `src/domain/messageQueue.ts`（可单测），组件 `AppConversationsTab.vue` 负责 Vue 响应式状态与串行消费编排。关键约束：`api.chatStream` **从不 reject**（失败只走 `onError` 回调后正常 resolve），故 `sendMessage` 必须在 `onError` 里捕获错误、待流结束后自行抛出，`drainQueue` 才能感知失败并停止。

**Tech Stack:** Vue 3 `<script setup>` + TypeScript、naive-ui、vue-i18n、vitest（jsdom 环境）。

---

## 文件结构

- **新建** `web/src/domain/messageQueue.ts` — 队列纯数据操作（类型 + 无副作用函数），与 `src/domain/conversation.ts` 同目录、同风格。
- **新建** `web/src/domain/messageQueue.spec.ts` — 队列纯逻辑单测。
- **修改** `web/src/i18n/locales/zh/apps/conversations.ts` — 新增排队相关中文文案。
- **修改** `web/src/i18n/locales/en/apps/conversations.ts` — 对应英文文案（`completeness.spec.ts` 强制中英键一致）。
- **修改** `web/src/pages/apps/AppConversationsTab.vue` — 队列状态、`onSend` 重构为 `sendMessage`、`onComposerSubmit`/`drainQueue`/编辑处理、输入区闸门与按钮文案、队列面板模板与样式。

> 所有命令默认在 `web/` 目录下执行。

---

### Task 1: 队列纯逻辑领域模块

**Files:**
- Create: `web/src/domain/messageQueue.ts`
- Test: `web/src/domain/messageQueue.spec.ts`

- [ ] **Step 1: 写失败测试**

创建 `web/src/domain/messageQueue.spec.ts`：

```ts
// messageQueue 纯逻辑单测：队列的取项、删除、失败回插、改状态、编辑写回、按会话过滤，
// 均为无副作用的数组变换，覆盖排队消息在「任务进行中入队 → 串行消费 → 失败保留 → 重试」链路上用到的全部操作。
import { describe, it, expect } from 'vitest'
import {
  nextPending,
  removeById,
  prependFailed,
  setStatus,
  applyEdit,
  forSession,
  type QueuedMessage,
} from './messageQueue'

// mk 构造一条队列项，简化用例书写；files 默认空。
function mk(id: string, sessionId: string, status: 'pending' | 'failed' = 'pending'): QueuedMessage {
  return { id, sessionId, text: id, files: [], status }
}

describe('messageQueue', () => {
  // nextPending 应返回当前会话中第一个 pending 项，跳过其它会话与 failed 项
  it('nextPending 返回当前会话首个 pending 项', () => {
    const q = [mk('a', 's2'), mk('b', 's1', 'failed'), mk('c', 's1'), mk('d', 's1')]
    expect(nextPending(q, 's1')?.id).toBe('c') // b 是 failed 跳过，c 是 s1 首个 pending
  })

  // 队列无当前会话的 pending 项时返回 undefined，用于 drainQueue 结束循环
  it('nextPending 无可发送项时返回 undefined', () => {
    const q = [mk('a', 's2'), mk('b', 's1', 'failed')]
    expect(nextPending(q, 's1')).toBeUndefined()
  })

  // removeById 按 id 移除且不改动其余项（消费开始时把项移出队列）
  it('removeById 按 id 移除项', () => {
    const q = [mk('a', 's1'), mk('b', 's1')]
    expect(removeById(q, 'a').map((x) => x.id)).toEqual(['b'])
  })

  // prependFailed 把项以 failed 放回队列头（发送失败停止并保留）
  it('prependFailed 以 failed 状态回插到队头', () => {
    const q = [mk('b', 's1')]
    const out = prependFailed(q, mk('a', 's1'))
    expect(out.map((x) => x.id)).toEqual(['a', 'b'])
    expect(out[0].status).toBe('failed')
  })

  // setStatus 把指定项改回 pending（重试）
  it('setStatus 改指定项状态', () => {
    const q = [mk('a', 's1', 'failed')]
    expect(setStatus(q, 'a', 'pending')[0].status).toBe('pending')
  })

  // applyEdit 写回文本与文件，不影响其它项（队列项编辑）
  it('applyEdit 写回文本与文件', () => {
    const f = new File(['x'], 'x.txt')
    const q = [mk('a', 's1'), mk('b', 's1')]
    const out = applyEdit(q, 'a', '改后', [f])
    expect(out[0]).toMatchObject({ id: 'a', text: '改后' })
    expect(out[0].files).toHaveLength(1)
    expect(out[1].text).toBe('b') // 其它项不变
  })

  // forSession 仅返回当前会话的项（队列面板按会话隔离渲染）
  it('forSession 按会话过滤', () => {
    const q = [mk('a', 's1'), mk('b', 's2'), mk('c', 's1')]
    expect(forSession(q, 's1').map((x) => x.id)).toEqual(['a', 'c'])
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm run test -- src/domain/messageQueue.spec.ts`
Expected: FAIL，报找不到模块 `./messageQueue` 或各函数未定义。

- [ ] **Step 3: 写最小实现**

创建 `web/src/domain/messageQueue.ts`：

```ts
// messageQueue 收敛「实例会话页任务进行中消息排队」的纯数据操作，与组件的 Vue 响应式状态解耦，
// 便于单测。所有函数无副作用：接收队列数组、返回新数组或查询结果，不修改入参。
// 串行消费编排（调用 chatStream、开关 sending）留在组件，因其依赖运行时副作用不宜在此实现。

// QueueStatus 队列项状态：pending 待发送；failed 发送失败、停留队列供重试。
export type QueueStatus = 'pending' | 'failed'

// QueuedMessage 一条排队消息。
export interface QueuedMessage {
  // id 本地生成的唯一标识，用于 v-for key、编辑与删除定位。
  id: string
  // sessionId 该消息归属的会话 id，支持切会话时按会话隔离。
  sessionId: string
  // text 文本内容。
  text: string
  // files 用户已选文件，暂存内存，轮到发送时才上传。
  files: File[]
  // status 见 QueueStatus。
  status: QueueStatus
}

// nextPending 返回指定会话中第一个 pending 项，供 drainQueue 逐条取用；无则 undefined。
export function nextPending(queue: QueuedMessage[], sessionId: string): QueuedMessage | undefined {
  return queue.find((q) => q.sessionId === sessionId && q.status === 'pending')
}

// removeById 按 id 移除项，返回新数组（消费开始时把项移出队列，使其变为真实消息气泡）。
export function removeById(queue: QueuedMessage[], id: string): QueuedMessage[] {
  return queue.filter((q) => q.id !== id)
}

// prependFailed 把项以 failed 状态放回队头，返回新数组（发送失败：停止并保留失败项）。
export function prependFailed(queue: QueuedMessage[], item: QueuedMessage): QueuedMessage[] {
  return [{ ...item, status: 'failed' }, ...queue]
}

// setStatus 把指定项改为目标状态，返回新数组（重试时把 failed 改回 pending）。
export function setStatus(queue: QueuedMessage[], id: string, status: QueueStatus): QueuedMessage[] {
  return queue.map((q) => (q.id === id ? { ...q, status } : q))
}

// applyEdit 写回指定项的文本与文件，返回新数组（队列项内联编辑保存）。
export function applyEdit(queue: QueuedMessage[], id: string, text: string, files: File[]): QueuedMessage[] {
  return queue.map((q) => (q.id === id ? { ...q, text, files } : q))
}

// forSession 返回指定会话的全部项，保持原顺序（队列面板按当前会话渲染）。
export function forSession(queue: QueuedMessage[], sessionId: string): QueuedMessage[] {
  return queue.filter((q) => q.sessionId === sessionId)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `npm run test -- src/domain/messageQueue.spec.ts`
Expected: PASS，全部 7 个用例通过。

- [ ] **Step 5: 提交**

```bash
git add web/src/domain/messageQueue.ts web/src/domain/messageQueue.spec.ts
git commit -m "feat(web): 增加对话排队队列纯逻辑领域模块

抽出实例会话页消息排队的纯数据操作(取项/删除/失败回插/改状态/编辑/
按会话过滤)到 domain/messageQueue.ts,与组件响应式状态解耦并补齐单测。"
```

---

### Task 2: 排队相关 i18n 文案

**Files:**
- Modify: `web/src/i18n/locales/zh/apps/conversations.ts`
- Modify: `web/src/i18n/locales/en/apps/conversations.ts`

- [ ] **Step 1: 加中文文案**

把 `web/src/i18n/locales/zh/apps/conversations.ts` 的对象改为（在 `attach` 后追加新键）：

```ts
// apps/conversations 文案（zh）：会话管理 tab 的全部可见字符串。
export default {
  // 新建会话按钮
  new: '新建会话',
  // 发送按钮（空闲态）
  send: '发送',
  // 发送按钮（流式发送中）
  sending: '发送中…',
  // 输入框占位文案
  placeholder: '输入消息…',
  // 会话条目重命名操作
  rename: '重命名',
  // 会话条目删除操作
  delete: '删除',
  // 无会话时的空状态提示
  empty: '暂无会话',
  // 附件按钮文案
  attach: '文件',
  // 发送按钮（任务进行中，点击入队而非立即发送）
  queueSend: '排队发送',
  // 待发送队列面板标题
  queueTitle: '待发送队列',
  // 任务进行中的状态提示（队列面板内）
  generating: '回复生成中…',
  // 队列项编辑操作
  queueEdit: '编辑',
  // 队列项编辑态保存
  queueSave: '保存',
  // 队列项编辑态取消
  queueCancel: '取消',
  // 队列项删除操作
  queueRemove: '删除',
  // 失败队列项重试操作
  queueRetry: '重试',
  // 失败队列项状态标记
  queueFailed: '发送失败',
} as const
```

- [ ] **Step 2: 加英文文案**

把 `web/src/i18n/locales/en/apps/conversations.ts` 改为：

```ts
// apps/conversations locale (en): all visible strings for the conversations tab.
export default {
  // New conversation button
  new: 'New chat',
  // Send button (idle)
  send: 'Send',
  // Send button (streaming in progress)
  sending: 'Sending…',
  // Input placeholder
  placeholder: 'Type a message…',
  // Rename action on session item
  rename: 'Rename',
  // Delete action on session item
  delete: 'Delete',
  // Empty state when no sessions exist
  empty: 'No conversations',
  // Attach file button label
  attach: 'File',
  // Send button while a reply is streaming (click enqueues instead of sending)
  queueSend: 'Queue',
  // Queued messages panel title
  queueTitle: 'Queued',
  // Status hint while a reply is generating (inside queue panel)
  generating: 'Generating…',
  // Edit action on a queued item
  queueEdit: 'Edit',
  // Save action in queued-item edit mode
  queueSave: 'Save',
  // Cancel action in queued-item edit mode
  queueCancel: 'Cancel',
  // Remove action on a queued item
  queueRemove: 'Remove',
  // Retry action on a failed queued item
  queueRetry: 'Retry',
  // Status badge on a failed queued item
  queueFailed: 'Failed',
} as const
```

- [ ] **Step 3: 运行 i18n 完整性测试确认中英一致**

Run: `npm run test -- src/i18n/locales/completeness.spec.ts`
Expected: PASS（zh 与 en 键完全对齐）。

- [ ] **Step 4: 提交**

```bash
git add web/src/i18n/locales/zh/apps/conversations.ts web/src/i18n/locales/en/apps/conversations.ts
git commit -m "feat(web): 补充对话消息排队相关中英文案

新增排队发送/待发送队列/生成中/编辑/保存/取消/删除/重试/发送失败
等 conversations 命名空间文案,中英各一份保持 completeness 一致。"
```

---

### Task 3: 组件 script —— 队列状态与串行消费编排

**Files:**
- Modify: `web/src/pages/apps/AppConversationsTab.vue`（`<script setup>` 部分）

本任务只改脚本逻辑，模板与样式在 Task 4。改完后 `npm run typecheck` 应通过（模板引用的新符号此任务已全部定义）。

- [ ] **Step 1: 扩充 import**

把第 153 行的 vue import 与第 157 行的类型 import 改为（新增 `computed`，以及领域模块与类型）：

```ts
import { ref, reactive, computed, nextTick, onMounted } from 'vue'
```

在第 158 行 `import { isDialogueMessage } from '@/domain/conversation'` 之后新增一行：

```ts
import {
  nextPending,
  removeById,
  prependFailed,
  setStatus,
  applyEdit,
  forSession,
  type QueuedMessage,
} from '@/domain/messageQueue'
```

- [ ] **Step 2: 新增队列与编辑状态**

在第 178 行 `const dragActive = ref(false)` 之后新增：

```ts
// queue 是任务进行中入队的待发送消息，逐条串行自动发送；纯内存、不持久化。
const queue = ref<QueuedMessage[]>([])
// queueSeq 本地自增序号，用于生成队列项唯一 id（不依赖时间/随机源）。
let queueSeq = 0
// draining 防止 drainQueue 重入（如失败重试与自动消费并发）。
let draining = false
// queuedForCurrent 仅暴露当前会话的队列项，供面板按会话隔离渲染。
const queuedForCurrent = computed(() => forSession(queue.value, currentId.value))

// ─── 队列项内联编辑状态 ───────────────────────────────────────────────────────
// editingId 为正在编辑的队列项 id；null 表示无编辑态。
const editingId = ref<string | null>(null)
// editDraft / editFiles 是编辑态的文本与文件草稿，保存时写回队列项。
const editDraft = ref('')
const editFiles = ref<File[]>([])
```

- [ ] **Step 3: 用 sendMessage 替换 onSend，并新增 onComposerSubmit / drainQueue**

删除第 313-375 行整段 `onSend`（含其上方第 313-319 行注释块），替换为以下内容：

```ts
// sendMessage 以 SSE 流式发送一条消息（文本 + 文件），手动发送与队列消费共用：
//   1. 逐个上传 files 拿 file_id 组 ConversationPart[]（上传失败直接抛出）；
//   2. 乐观推入用户消息与空 assistant 占位；
//   3. 逐帧把 onDelta 追加到占位消息；
//   4. 成功后 refetch 保持一致。
// 关键：api.chatStream 从不 reject，失败只走 onError 回调后正常 resolve，故此处在 onError
// 里捕获错误信息，流结束后若有错误：移除刚才乐观推入的两条消息、抛出错误，让调用方（drainQueue /
// onComposerSubmit）感知失败并处理，避免失败消息残留在气泡里。
async function sendMessage(text: string, files: File[]) {
  if ((!text && files.length === 0) || !currentId.value) return

  sending.value = true

  // 逐个上传文件，拿到 file_id 组装 file parts；失败则复位 sending 并抛出。
  const fileParts: ConversationPart[] = []
  try {
    for (const f of files) {
      const meta = await api.uploadConversationFile(props.appId, currentId.value, f)
      fileParts.push({ type: 'input_file', file_id: meta.file_id, filename: meta.filename, mime: meta.mime })
    }
  } catch (e) {
    sending.value = false
    throw e instanceof Error ? e : new Error(String(e))
  }

  // 有文件时组装多模态 parts；纯文字时保持字符串，与旧行为兼容。
  const payload: string | ConversationPart[] =
    fileParts.length > 0
      ? [...(text ? [{ type: 'text' as const, text }] : []), ...fileParts]
      : text

  // 乐观推入用户消息与 assistant 占位（保存引用，失败时按引用精准移除）。
  const userMsg: api.ConversationMessage = { role: 'user', content: payload }
  messages.value.push(userMsg)
  const asst = reactive<api.ConversationMessage>({ role: 'assistant', content: '' })
  messages.value.push(asst)
  await scrollToBottom()

  // streamErr 捕获流内错误：chatStream 不 reject，靠 onError 回填。
  let streamErr: string | null = null
  try {
    await api.chatStream(props.appId, currentId.value, payload, {
      onDelta: (d) => {
        asst.content = (asst.content as string) + d
        void scrollToBottom()
      },
      onDone: () => {},
      onError: (m) => {
        streamErr = m
      },
    })
  } finally {
    sending.value = false
  }

  if (streamErr) {
    // 失败：移除刚才乐观推入的占位与用户消息，使该条不残留在气泡；抛出供调用方处理。
    const ai = messages.value.indexOf(asst)
    if (ai >= 0) messages.value.splice(ai, 1)
    const ui = messages.value.indexOf(userMsg)
    if (ui >= 0) messages.value.splice(ui, 1)
    throw new Error(streamErr)
  }

  // 成功后重新拉取消息，使列表与服务端状态一致（含 token/finish_reason 等完整字段）。
  await selectSession(currentId.value)
}

// onComposerSubmit 是发送按钮 / 回车的统一入口：
//   - 任务进行中（sending）：把当前草稿入队，不立即发送；
//   - 空闲：立即发送，成功后驱动 drainQueue 消费期间新入的队列项。
async function onComposerSubmit() {
  const text = draft.value.trim()
  const files = pendingFiles.value.slice()
  if ((!text && files.length === 0) || !currentId.value) return

  if (sending.value) {
    // 入队：生成唯一 id，绑定当前会话；清空草稿让用户感知已受理。
    queue.value = [
      ...queue.value,
      { id: `q${++queueSeq}`, sessionId: currentId.value, text, files, status: 'pending' },
    ]
    draft.value = ''
    pendingFiles.value = []
    return
  }

  // 空闲：立即发送。清空草稿后发送，失败则回填草稿并提示。
  draft.value = ''
  pendingFiles.value = []
  try {
    await sendMessage(text, files)
  } catch (e) {
    draft.value = text
    pendingFiles.value = files
    message.error(e instanceof Error ? e.message : String(e))
    return
  }
  await drainQueue()
}

// drainQueue 串行消费当前会话的 pending 队列：逐条取出发送，上一条流式跑完再发下一条。
// 任一条失败即停止（方案 A：停止并保留），把该条以 failed 放回队头供重试。
// draining 防重入；sending 为真时不启动（避免与在飞发送并发）。
async function drainQueue() {
  if (draining || sending.value) return
  draining = true
  try {
    for (;;) {
      const next = nextPending(queue.value, currentId.value)
      if (!next) return
      // 先移出队列：发送时它会经乐观更新变成真实用户气泡，从队列面板消失。
      queue.value = removeById(queue.value, next.id)
      try {
        await sendMessage(next.text, next.files)
      } catch (e) {
        queue.value = prependFailed(queue.value, next)
        message.error(e instanceof Error ? e.message : String(e))
        return
      }
    }
  } finally {
    draining = false
  }
}

// ─── 队列项操作 ───────────────────────────────────────────────────────────────
// startEdit 进入某队列项的内联编辑态，预填其文本与文件。
function startEdit(item: QueuedMessage) {
  editingId.value = item.id
  editDraft.value = item.text
  editFiles.value = item.files.slice()
}

// removeEditFile 在编辑态移除某个待发文件。
function removeEditFile(i: number) {
  editFiles.value.splice(i, 1)
}

// cancelEdit 放弃本次编辑，丢弃编辑草稿。
function cancelEdit() {
  editingId.value = null
}

// saveEdit 把编辑草稿写回队列项并退出编辑态。
function saveEdit(id: string) {
  queue.value = applyEdit(queue.value, id, editDraft.value.trim(), editFiles.value.slice())
  editingId.value = null
}

// removeQueued 从队列删除某项。
function removeQueued(id: string) {
  queue.value = removeById(queue.value, id)
  if (editingId.value === id) editingId.value = null
}

// retryQueued 把失败项改回 pending 并重新驱动消费。
async function retryQueued(id: string) {
  queue.value = setStatus(queue.value, id, 'pending')
  await drainQueue()
}
```

- [ ] **Step 4: 类型检查通过（此时模板仍引用旧 onSend，会有未使用告警但类型应可编译）**

> 说明：Task 4 会把模板里的 `onSend` 引用改为 `onComposerSubmit`。本步骤仅确认 script 段无类型错误；`onSend` 已删除，模板 Task 4 未改前 `npm run typecheck` 会报模板找不到 `onSend`——这是预期的，Task 4 修复。故本步骤只做人工检查：确认新增符号 `sendMessage`/`onComposerSubmit`/`drainQueue`/`startEdit`/`removeEditFile`/`cancelEdit`/`saveEdit`/`removeQueued`/`retryQueued`/`queuedForCurrent`/`editingId`/`editDraft`/`editFiles` 均已定义。

不单独提交，与 Task 4 一起提交（同一文件、同一功能边界）。

---

### Task 4: 组件 template + 样式 —— 队列面板与输入区闸门

**Files:**
- Modify: `web/src/pages/apps/AppConversationsTab.vue`（`<template>` 与 `<style>` 部分）

- [ ] **Step 1: 放开输入区在 sending 时的禁用**

- 第 103 行输入框：把 `:disabled="!currentId || sending"` 改为 `:disabled="!currentId"`。
- 第 104 行：把 `@keydown.enter.exact.prevent="onSend"` 改为 `@keydown.enter.exact.prevent="onComposerSubmit"`。
- 第 109 行附件 label class：把 `:class="{ 'attach-button--disabled': !currentId || sending }"` 改为 `:class="{ 'attach-button--disabled': !currentId }"`。
- 第 115 行隐藏 file input：把 `:disabled="!currentId || sending"` 改为 `:disabled="!currentId"`。

- [ ] **Step 2: 调整发送按钮：去掉 sending 禁用与 loading，文案随 sending 切换**

把第 120-128 行的发送按钮改为：

```vue
          <n-button
            type="primary"
            :disabled="!currentId || (!draft.trim() && pendingFiles.length === 0)"
            data-test="send"
            @click="onComposerSubmit"
          >
            {{ sending ? t('apps.conversations.queueSend') : t('apps.conversations.send') }}
          </n-button>
```

- [ ] **Step 3: 拖拽处理放开 sending 判断**

- 第 208 行 `onDragEnter`：把 `if (!currentId.value || sending.value || !hasFiles(e)) return` 改为 `if (!currentId.value || !hasFiles(e)) return`。
- 第 214 行 `onDragOver`：把 `if (!currentId.value || sending.value || !hasFiles(e)) return` 改为 `if (!currentId.value || !hasFiles(e)) return`。
- 第 231 行 `onDrop`：把 `if (!currentId.value || sending.value) return` 改为 `if (!currentId.value) return`。

> 第 60 行模板注释提到「未发送中（!sending）时才响应拖拽」，同步把该句注释改为「已选中会话（currentId 非空）时才响应拖拽」，避免注释与行为不符。

- [ ] **Step 4: 在 msg-list 与 composer 之间插入队列面板**

在第 80 行 `</div>`（msg-list 结束）与第 82-83 行 composer 之间插入：

```vue
      <!-- 待发送队列：任务进行中入队的消息，逐条串行自动发送；仅渲染当前会话的项。 -->
      <div v-if="queuedForCurrent.length" class="queue-panel">
        <div class="queue-header">
          <span class="queue-title">{{ t('apps.conversations.queueTitle') }}</span>
          <span v-if="sending" class="queue-generating">{{ t('apps.conversations.generating') }}</span>
        </div>
        <div
          v-for="item in queuedForCurrent"
          :key="item.id"
          class="queue-item"
          :class="{ 'queue-item--failed': item.status === 'failed' }"
          :data-test="`queued-${item.id}`"
        >
          <!-- 编辑态：内联 textarea + 可移除文件 tag + 保存/取消 -->
          <template v-if="editingId === item.id">
            <n-input
              v-model:value="editDraft"
              type="textarea"
              :autosize="{ minRows: 1, maxRows: 4 }"
            />
            <div v-if="editFiles.length" class="queue-files">
              <n-tag
                v-for="(f, i) in editFiles"
                :key="i"
                closable
                size="small"
                @close="removeEditFile(i)"
              >
                {{ f.name }}
              </n-tag>
            </div>
            <div class="queue-actions">
              <n-button size="tiny" type="primary" @click="saveEdit(item.id)">
                {{ t('apps.conversations.queueSave') }}
              </n-button>
              <n-button size="tiny" quaternary @click="cancelEdit">
                {{ t('apps.conversations.queueCancel') }}
              </n-button>
            </div>
          </template>
          <!-- 展示态：文本预览 + 只读文件 tag + 失败标记/重试/编辑/删除 -->
          <template v-else>
            <div class="queue-text">{{ item.text || '—' }}</div>
            <div v-if="item.files.length" class="queue-files">
              <n-tag v-for="(f, i) in item.files" :key="i" size="small">{{ f.name }}</n-tag>
            </div>
            <div class="queue-actions">
              <n-tag
                v-if="item.status === 'failed'"
                size="tiny"
                type="error"
                :bordered="false"
              >
                {{ t('apps.conversations.queueFailed') }}
              </n-tag>
              <n-button
                v-if="item.status === 'failed'"
                size="tiny"
                type="primary"
                @click="retryQueued(item.id)"
              >
                {{ t('apps.conversations.queueRetry') }}
              </n-button>
              <n-button size="tiny" quaternary @click="startEdit(item)">
                {{ t('apps.conversations.queueEdit') }}
              </n-button>
              <n-button size="tiny" quaternary type="error" @click="removeQueued(item.id)">
                {{ t('apps.conversations.queueRemove') }}
              </n-button>
            </div>
          </template>
        </div>
      </div>
```

- [ ] **Step 5: 加队列面板样式**

在 `<style scoped>` 内 `.composer` 规则之前插入：

```css
/* ─── 待发送队列面板 ──────────────────────────────
   位于消息列表与输入区之间，flex-shrink:0 不被压缩；内部自身可滚动，避免排多条时顶高布局。 */
.queue-panel {
  flex-shrink: 0;
  max-height: 30%;
  overflow-y: auto;
  padding: 8px 12px;
  border-top: 1px dashed var(--color-border, #e5e7eb);
  background: var(--color-bg-hover, #f5f5f5);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.queue-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--color-text-secondary, #6b7280);
}

.queue-title {
  font-weight: 600;
}

.queue-generating {
  color: var(--color-brand-text, #8a3700);
}

/* 单条队列项：卡片式，纵向堆叠文本/文件/操作。 */
.queue-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 10px;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 6px;
  background: var(--color-surface, #fff);
}

/* 失败项：红色左边框强调。 */
.queue-item--failed {
  border-left: 3px solid var(--color-error, #d03050);
}

.queue-text {
  font-size: 13px;
  color: var(--color-text-primary, #1f2329);
  white-space: pre-wrap;
  word-break: break-word;
}

.queue-files {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.queue-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
```

- [ ] **Step 6: 类型检查 + 构建**

Run: `npm run typecheck`
Expected: PASS，无 `onSend` 未定义、无未使用符号报错。

Run: `npm run build`
Expected: 构建成功。

- [ ] **Step 7: 运行前端单测确认无回归**

Run: `npm run test -- src/domain/ src/i18n/locales/completeness.spec.ts`
Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add web/src/pages/apps/AppConversationsTab.vue
git commit -m "feat(web): 对话任务进行中支持消息排队与串行自动发送

流式回复进行中不再禁用输入框/附件/拖拽,发送改为入队并在队列面板
展示(可编辑/删除/失败重试);当前回复结束后逐条串行自动发送,中途
失败即停止并保留失败项。onSend 重构为 sendMessage 供手动发送与队列
消费共用,并在 onError 捕获流内错误(chatStream 不 reject)后抛出以
驱动 drainQueue 的失败停止。"
```

---

### Task 5: 真实浏览器全流程验证

**Files:** 无（验证任务）。

> 依据 CLAUDE.md「所有新功能必须用真实浏览器验证，curl 不能替代」。在本地 k3d 环境用一个已运行的实例会话页验证。若发现问题，先修复并重验直到全部通过。

- [ ] **Step 1: 启动/确认本地环境**

确认本地 manager 可访问（`http://ocm.localhost`，见 AGENTS.md 本地账号），存在一个已运行、可正常对话的实例；打开其「会话」tab。

- [ ] **Step 2: 逐项验证并记录证据（截图 / 观察）**

1. 发一条能触发较长流式回复的消息；**在 AI 正在输出时**，输入框可继续打字、点「排队发送」→ 消息进入「待发送队列」面板、未立即发送。
2. 连续再排 2 条（其中一条带附件文件），队列显示 3 条。
3. 对某条点「编辑」→ 改文本 / 移除附件 → 保存，队列内容更新；对另一条点「删除」→ 移出队列。
4. 等当前流式回复结束 → 队列逐条串行自动发送，顺序与排队一致，带附件那条附件正常上传显示。
5. 构造一次发送失败（如临时断网后点发送触发排队消费，或断网状态下重试）→ 确认串行停止、失败那条以「发送失败」标记留在队列、后续项不再自动发。
6. 恢复网络后点失败项「重试」→ 该条重新发送成功并继续消费剩余队列。
7. 切换到另一个会话再切回，确认队列按会话隔离（异会话不串）。

- [ ] **Step 3: 交付说明**

在交付说明中给出逐项验证结果（通过 / 修复记录），列出改动文件矩阵。

---

## Self-Review 结果

- **Spec 覆盖**：目标 1（输入区不禁用）→ Task 4 Step 1/3；目标 2（入队）→ Task 3 `onComposerSubmit` + Task 4 队列面板；目标 3（逐条串行）→ Task 3 `drainQueue`；目标 4（失败停止保留 + 重试）→ Task 3 `drainQueue` catch + `retryQueued`；数据模型 → Task 1；队列 UI（编辑/删除/失败态）→ Task 4；i18n → Task 2；会话切换隔离 → `forSession`/`nextPending` 按 `currentId` 过滤（Task 1 + Task 3）；测试与浏览器验证 → Task 1 spec + Task 5。无遗漏。
- **占位符扫描**：无 TBD/TODO，所有代码步骤含完整代码。
- **类型一致性**：`QueuedMessage`/`nextPending`/`removeById`/`prependFailed`/`setStatus`/`applyEdit`/`forSession` 在 Task 1 定义，Task 3 import 与调用签名一致；i18n 键 `queueSend`/`queueTitle`/`generating`/`queueEdit`/`queueSave`/`queueCancel`/`queueRemove`/`queueRetry`/`queueFailed` 在 Task 2 定义、Task 4 模板一致引用。
- **已知取舍**：失败重试对「服务端可能已部分持久化」的消息不做去重（best-effort 客户端队列，符合 spec 选择）；队列纯内存、刷新丢失（spec 已声明）。
