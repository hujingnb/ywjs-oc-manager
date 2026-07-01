# 对话任务进行中的消息排队 — 设计

**日期**：2026-07-01
**范围**：纯前端改动，集中在 `web/src/pages/apps/AppConversationsTab.vue` 与对应 i18n 文案；不改后端、不改 OpenAPI 契约、不需要重新生成前端类型。

## 背景与问题

实例会话页 `AppConversationsTab.vue` 当前以 SSE 流式发送消息。发送期间由 `sending`（`ref(false)`）作为闸门，`sending=true` 时会同时禁用：

- 输入框 `n-input`（`:disabled="!currentId || sending"`）
- 附件按钮与隐藏 `file input`
- 拖拽上传（`onDragEnter/onDragOver/onDrop` 内均判断 `sending.value` 提前 return）
- 发送按钮（`:disabled` 含 `sending`，且 `:loading="sending"`）

结果是「AI 正在输出时，用户完全不能操作输入框，也无法预先输入下一条消息」。

## 目标

任务（流式回复）进行中：

1. 用户可以照常在输入框打字、选文件、拖拽文件；
2. 点发送 / 回车不再被禁用，但**不立即发送**，而是进入一个可见、可编辑、可删除的排队队列；
3. 当前任务流式结束后，队列中的消息**逐条串行**自动发送，每条等自己的流式回复跑完再发下一条；
4. 串行发送中途若某条失败，则**停止并保留**失败项及其后未发项，由用户重试或删除。

以上四点分别对应设计评审中确认的选择：可见可控队列（A）/ 逐条串行（A）/ 排队消息支持附件（A）/ 出错停止并保留（A）。

## 非目标

- 不做队列持久化：队列仅存在于组件内存，刷新或关闭页面即丢失。
- 不支持给「已排队」的消息新增文件（编辑仅限改文本、移除已选文件）。
- 不改变工单对话组件 `TicketConversation.vue`（它是独立的非流式对话，不在本次范围）。
- 不做跨会话的队列自动迁移：队列按会话隔离，详见「会话切换」。

## 数据模型

在组件内新增排队队列状态（客户端内存，不持久化）：

```ts
interface QueuedMessage {
  id: string                  // 本地生成的 uid，用于 v-for key、编辑、删除定位
  sessionId: string           // 该消息归属的会话 id，支持切会话场景下的按会话隔离
  text: string                // 文本内容
  files: File[]               // File 对象暂存内存，轮到发送时才真正上传
  status: 'pending' | 'failed'// pending 待发送；failed 发送失败、停留在队列供重试
}

const queue = ref<QueuedMessage[]>([])
```

`id` 用一个简单的本地自增计数器或 `crypto.randomUUID()` 生成（避免依赖时间随机源的顺序语义即可）。

## 交互与 UI

### 输入区闸门调整

- 输入框、附件按钮、隐藏 file input、拖拽处理：**移除对 `sending` 的判断**，仅保留 `!currentId` 作为禁用条件。任务进行中输入区全程可用。
- 发送按钮：
  - `:disabled` 去掉 `sending`，保留「未选会话」与「文本与文件都为空」两个条件；
  - 去掉 `:loading="sending"`（任务进行中的反馈改由流式气泡 + 队列面板的「回复生成中…」提示承担）；
  - 文案：`sending` 为真时显示「排队发送」（`queueSend`），否则显示「发送」（`send`）。
- 回车键 `@keydown.enter.exact.prevent` 由原来的 `onSend` 改为新的 `onComposerSubmit`。

### 队列面板

在 `.msg-list` 与 `.composer` 之间新增「待发送队列」面板 `.queue-panel`：

- 仅当存在 `sessionId === currentId` 的队列项时才渲染；为空则整块不显示。
- 面板顶部：标题「待发送队列」（`queueTitle`）；当 `sending` 为真时同一行显示「回复生成中…」（`generating`）状态提示。
- 每条队列项渲染为一张卡片：
  - **展示态**：文本预览 + 文件名 tag（只读）+「编辑」「删除」按钮。
  - **编辑态**：点「编辑」→ 卡片内联切换为 `textarea`，文件 tag 变为可关闭（closable）以移除；提供「保存」「取消」。保存写回该队列项的 `text`/`files`，取消丢弃本次编辑。
  - **failed 态**：卡片加红色失败标记，按钮为「重试」「删除」。
- 删除：从 `queue` 中按 `id` 移除该项。

## 发送与消费逻辑

### 复用的发送函数

将现有 `onSend()` 重构为参数化的 `sendMessage(text: string, files: File[])`，内部保持现有流程：

1. 早退守卫（文本与文件都为空、或未选会话，则 return）；
2. `sending = true`；
3. 逐个上传 `files` 拿 `file_id` 组 `ConversationPart[]`（上传失败则 `message.error` 并抛出，让调用方感知）；
4. 组装 payload（有文件走多模态 parts，纯文字保持字符串，与旧行为兼容）；
5. 乐观推入用户消息 + 空 assistant 占位；
6. `api.chatStream(...)` 逐帧追加；
7. 结束后 `selectSession(currentId)` refetch 保持一致；
8. `finally { sending = false }`。

手动发送与队列消费都调用 `sendMessage`，避免逻辑重复。失败时 `sendMessage` 需向上抛出（供 `drainQueue` 捕获决定是否停止）。

### 提交入口

`onComposerSubmit()`（发送按钮 / 回车触发）：

```
text = draft.trim(); files = pendingFiles.slice()
若 text 与 files 均空 → 直接 return
若 sending 为真:
    enqueue：queue.push({ id, sessionId: currentId, text, files, status: 'pending' })
    清空 draft / pendingFiles
否则（空闲）:
    清空 draft / pendingFiles
    await sendMessage(text, files)
    await drainQueue()
```

### 串行消费

`drainQueue()`：

```
循环:
  next = queue 中第一个 sessionId === currentId 且 status === 'pending' 的项
  若无 next → return
  从 queue 移除 next（此刻它会经乐观更新变成真实用户气泡，从队列面板消失）
  try:
    await sendMessage(next.text, next.files)
    // 成功 → 继续下一轮；本轮期间新入队的项会被下一轮 find 命中
  catch:
    将 next 以 status='failed' 放回 queue 头部
    break  // 方案 A：停止并保留，不再自动发后续项
```

关键不变式：**「入队」只可能发生在 `sending === true`**（即已有一个 `sendMessage` 在飞）。那个在飞的发送——无论是手动发送（其 `onComposerSubmit` 空闲分支结尾会 `await drainQueue()`）还是队列消费（`drainQueue` 自身循环）——结束后必然触发/继续消费。因此队列总能被排空，且串行 `await` 天然保证「上一条流式跑完才发下一条」。

失败项的「重试」：把该项 `status` 置回 `pending`，并调用 `drainQueue()` 重新驱动（此时 `sending` 已为 false）。

## 会话切换

- 队列项带 `sessionId`，面板与 `drainQueue` 均只处理 `sessionId === currentId` 的项。
- 若在任务进行中切换到别的会话：原会话的队列**暂停**（不被当前会话的 drain 触及），切回原会话且其任务结束后继续消费。
- 简化取舍：不实现跨会话的后台并发消费，避免与「流式气泡追加到当前 `messages`」的既有单会话假设冲突。

## 测试

- 队列纯逻辑（入队、串行取项、失败放回并停止、编辑写回、删除、重试）尽量抽成不依赖 DOM 的可测单元，补 vitest 单测，覆盖：
  - 任务进行中提交 → 入队而非发送；空闲提交 → 立即发送；
  - 多条排队 → 逐条串行；
  - 中途失败 → 停止且失败项以 `failed` 保留在队列头，后续项不发；
  - 重试 → 失败项回到 pending 并继续消费；
  - 编辑写回文本/移除文件、删除项；
  - 按会话隔离（异会话项不被当前会话消费）。
- 每个测试方法/子测试均加中文注释说明覆盖场景（遵循仓库测试规范）。

## 验证（交付前）

按 CLAUDE.md 要求，最后必须用**真实浏览器**在本地 k3d 实例上逐项验证，不能只跑单测：

1. 任务进行中在输入框打字、点发送 → 进入队列且未立即发送；
2. 连续排多条（含带附件的）；
3. 编辑某条、删除某条；
4. 当前流式回复结束后，队列逐条串行自动发送，顺序正确、附件正常上传；
5. 构造一次发送失败（如断网），确认停止并保留、失败项可重试；
6. 切换会话时队列按会话隔离的行为符合预期。

发现问题先修复再重验，直到全部通过再交付。

## 影响面

- 改动文件：`web/src/pages/apps/AppConversationsTab.vue`，i18n catalog（`apps.conversations.*` 中英文案），以及新增的前端单测文件。
- 不涉及后端、数据库、OpenAPI 契约与生成产物。
