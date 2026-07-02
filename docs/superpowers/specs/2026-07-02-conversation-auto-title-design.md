# 对话页会话自动命名设计

日期：2026-07-02
状态：已评审，待实现

## 背景与问题

manager 实例对话页（`web/src/pages/apps/AppConversationsTab.vue`）里，通过 hermes
api_server 创建的会话，`title` 字段为空，前端以 `s.title || s.id` 兜底显示，于是
左栏露出形如 `api_123456` 的 session id，无意义、难以辨认。

链路事实（已读源码确认）：

- `api_123456` 是 **hermes api_server 生成的 session id**，manager → oc-ops →
  handler 全程只是透传，manager 本地不生成会话名，也不缓存。
- 会话数据已有 `title` 字段（可空），且**重命名接口全链路可用**：
  `PATCH /api/v1/apps/{appId}/hermes/conversations/{sid}`，body `{"title":"..."}`，
  service 层 `Rename` 校验 title 非空后透传 oc-ops `UpdateSessionTitle`。
- 会话列表返回**不含「首条消息」字段**（只有 `preview` = 末条消息预览），要拿首条
  必须调 `GET .../conversations/{sid}/messages`。
- manager 对话页是**交互式聊天**：`onCreate()` 建空会话 → 用户发首条消息
  `sendMessage()` 流式对话；发送前 `messages.value.length === 0` 天然可判「首条」。

已确认前提（用户答复）：**所有 `api_xxx` 会话都在 manager 对话页里新建**，不存在
外部 web 客户端直连 hermes api_server 建会话的场景。因此纯前端方案即可完整覆盖。

## 目标

对话页里 `title` 为空的会话，自动用**用户发起的第一句话**作为会话名；同时覆盖：

1. **新建会话**：发完首条消息后自动命名；
2. **已存在旧会话**：用户点开时自动补名。

不改后端 / oc-ops / 引擎。

## 方案

纯前端，复用已有 `PATCH` 重命名接口。落点选在 `selectSession()` 加载完消息之后，
因为此处天然同时覆盖两种场景：

- 新会话：`sendMessage()` 末尾会 `await selectSession(currentId.value)` 刷新，此时
  消息已就位 → 触发自动命名；
- 旧会话：用户点开任一 `title` 为空的会话 → 加载出历史消息 → 触发自动命名。

一套逻辑，无需分别处理。

### 组件拆分

#### 1. 纯函数 `deriveSessionTitle(messages)`

位置：`web/src/domain/conversation.ts`（已有 `isDialogueMessage` / `parseFileMarkers`
等对话域纯逻辑，风格一致，可独立单测）。

签名：

```ts
export function deriveSessionTitle(messages: ConversationMessage[]): string | null
```

行为：

- 取**第一条 `role === 'user'` 的消息**（而非首条，避免引擎开场白 assistant 抢标题）；
  无 user 消息 → 返回 `null`。
- 从该消息内容取文本：
  - `content` 是字符串：先用现有 `parseFileMarkers()` 剥离服务端回写的
    `<oc-file:...>` 标记及其英文注记，得到 `clean` 文本与 `files`；
    - `clean` 非空 → 用 `clean`；
    - 否则若 `files` 有带 `filename` 的项 → 用第一个非空 `filename`（纯附件场景）；
    - 否则 → `null`。
  - `content` 是 `ConversationPart[]`：
    - 优先取第一个非空 `text` part 的文本；
    - 否则取第一个 `input_file` part 的 `filename`（纯附件场景）；
    - 否则 → `null`。
- 归一化：把换行 / 连续空白折叠为单个空格 → `trim`。
- 截断：超过 **20 个字符**时截到 20 并补 `…`（末尾省略号不计入 20）。
- 归一化后为空 → 返回 `null`。

返回 `string | null`；`null` 表示无法派生标题（调用方不触发命名）。

#### 2. `AppConversationsTab.vue` 里的 `maybeAutoTitle(sid)`

在 `selectSession` 尾部（消息加载并 `scrollToBottom` 后）调用。

- 组件内维护内存 `Set<string>` `autoTitleAttempted`，记录已尝试过自动命名的 sid，
  避免同一页内每次打开重复 PATCH。
- 触发条件（全部满足）：
  - `sid` 不在 `autoTitleAttempted` 中；
  - `sessions.value` 中该会话存在且 `title` 为空（`!s.title`）；
  - `deriveSessionTitle(messages.value)` 返回非 `null`。
- 命中后：先把 `sid` 加入 `autoTitleAttempted`，再
  `await api.renameConversation(props.appId, sid, title)`；成功则更新本地：
  把 `sessions.value` 中该会话对象的 `title` 就地设为新标题（左栏即时刷新），
  当前不强制 `loadSessions()`（避免多一次列表请求；就地更新已足够）。

### 关键边界处理

- **权限**：只读角色（有 view 无 manage）PATCH 会被拒 403。自动命名是「锦上添花」，
  **失败一律静默吞掉**（`try/catch` 不弹 `message.error`）；配合 `autoTitleAttempted`
  Set，失败后同一页内不再重试该会话。
- **空会话**：刚 `onCreate` 出来、尚未发消息的会话，`messages.value` 为空，
  `deriveSessionTitle` 返回 `null`，不命名，继续 `title || id` 兜底显示 id。
- **用户手动命名优先**：仅在 `title` 为空时触发；一旦有 title（自动或手动），不再改动，
  用户手动重命名永远优先、不被覆盖。
- **消息过滤**：`messages.value` 是经 `isDialogueMessage` 过滤后的对话正文；user 消息
  不会被过滤掉，`deriveSessionTitle` 基于它即可，无需读原始未过滤消息。

## 测试

给 `deriveSessionTitle` 补单测（`web/src/domain/conversation.spec.ts`），table-driven，
每条用例带中文注释，覆盖：

- 纯文本首句 → 原样返回；
- 多行 / 多空格 → 折叠为单空格；
- 超长（>20 字符）→ 截到 20 + `…`；
- 恰好 20 字符 → 不加省略号；
- `content` 为数组含 text part → 取该文本；
- 纯附件（数组仅 input_file）→ 取 filename；
- 字符串含 `<oc-file:...>` 标记 + 正文 → 剥标记后取正文；
- 字符串仅含 `<oc-file:...>` 标记（纯附件回读）→ 取 filename；
- 首条是 assistant、第二条才是 user → 取 user 那条；
- 无 user 消息 → 返回 `null`；
- 空 / 全空白内容 → 返回 `null`。

## 验证

按 AGENTS.md 要求真实浏览器验证：

1. 新建会话发首句 → 左栏标题自动变为首句（超长截断带 `…`）；
2. 点开旧 `api_xxx` 会话 → 自动补名为其首条 user 消息；
3. 纯附件首条消息 → 会话名取文件名；
4. 手动重命名后 → 不被自动命名覆盖；
5. 只读角色打开会话 → 不弹错误、页面正常。

## 影响范围

- 改动文件：`web/src/domain/conversation.ts`（新增纯函数）、
  `web/src/domain/conversation.spec.ts`（新增单测）、
  `web/src/pages/apps/AppConversationsTab.vue`（`selectSession` 尾部接入 `maybeAutoTitle`
  与 `autoTitleAttempted` 状态）。
- 不涉及后端、oc-ops、hermes 引擎、OpenAPI 契约、DB。
