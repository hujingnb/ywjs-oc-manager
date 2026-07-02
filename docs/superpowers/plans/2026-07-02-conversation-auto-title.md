# 对话页会话自动命名 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** manager 对话页里 title 为空的会话，自动用「用户发起的第一句话」回填 title，替代无意义的 `api_123456` 显示；同时覆盖新建会话与已存在旧会话。

**Architecture:** 纯前端方案。新增可独立单测的纯函数 `deriveSessionTitle` 从会话消息派生标题；在 `AppConversationsTab.vue` 的 `selectSession()` 加载完消息后调用 `maybeAutoTitle`，对 title 为空的会话调用**已有**的 `PATCH` 重命名接口回填。不改后端 / oc-ops / hermes 引擎 / OpenAPI 契约 / DB。

**Tech Stack:** Vue 3 + TypeScript + naive-ui；vitest 单测；npm 脚本。

参考设计：`docs/superpowers/specs/2026-07-02-conversation-auto-title-design.md`

---

## File Structure

- `web/src/domain/conversation.ts`（Modify）— 新增纯函数 `deriveSessionTitle` 与内部 helper `extractTitleText`，复用同文件已有的 `parseFileMarkers`。此文件是「对话查看域纯逻辑」的收敛点，风格与 `isDialogueMessage` / `hasRenderableContent` 一致。
- `web/src/domain/conversation.spec.ts`（Modify）— 为 `deriveSessionTitle` 追加 vitest 用例。
- `web/src/pages/apps/AppConversationsTab.vue`（Modify）— 引入 `deriveSessionTitle`，新增内存 `autoTitleAttempted` 与 `maybeAutoTitle`，在 `selectSession` 尾部接入。

关键类型（`web/src/api/conversations.ts`，已存在，无需改）：

```ts
export interface ConversationMessage { role: string; content: unknown; /* ... */ }
export interface ConversationSession { id: string; source: string; title?: string; /* ... */ }
export type ConversationPart = ConversationTextPart | ConversationFilePart
// ConversationFilePart: { type: 'input_file'; file_id: string; filename: string; mime?: string }
```

已有可复用函数（`web/src/domain/conversation.ts`）：

```ts
// parseFileMarkers 剥离服务端回写的 <oc-file:id[:enc_filename]> 标记与英文注记，
// 返回 { clean: string; files: { fileId: string; filename: string }[] }（clean 已 trim）。
export function parseFileMarkers(text: string): { clean: string; files: ConversationFileRef[] }
```

---

## Task 1: 纯函数 `deriveSessionTitle`

**Files:**
- Modify: `web/src/domain/conversation.ts`（在文件末尾追加）
- Test: `web/src/domain/conversation.spec.ts`（追加 describe 块）

- [ ] **Step 1: 写失败测试**

在 `web/src/domain/conversation.spec.ts` 顶部 import 增加 `deriveSessionTitle`：

```ts
import { hasRenderableContent, isDialogueMessage, parseFileMarkers, deriveSessionTitle } from './conversation'
```

在文件末尾追加以下 describe 块（每条用例带中文注释，覆盖设计里列出的全部场景）：

```ts
describe('deriveSessionTitle', () => {
  // mk 构造一条消息，简化用例书写；默认 user 角色。
  const mk = (m: Partial<ConversationMessage>): ConversationMessage =>
    ({ role: 'user', content: '', ...m }) as ConversationMessage

  it('取第一条 user 消息的纯文本作为标题', () => {
    // 最常见形态：首条 user 文字直接作会话名。
    expect(deriveSessionTitle([mk({ content: '查一下我的订单' })])).toBe('查一下我的订单')
  })

  it('折叠换行与连续空白为单个空格', () => {
    // 首句含换行/多空格时归一化为单空格，避免标题里出现断行与大段空白。
    expect(deriveSessionTitle([mk({ content: '第一行\n第二行   有空格' })])).toBe('第一行 第二行 有空格')
  })

  it('超过 20 字符时截断并补省略号', () => {
    // 21 字符输入应截到前 20 字符并追加 …（省略号不计入 20）。
    const long = '一二三四五六七八九十一二三四五六七八九十甲' // 21 个字符
    expect(deriveSessionTitle([mk({ content: long })])).toBe('一二三四五六七八九十一二三四五六七八九十…')
  })

  it('恰好 20 字符不加省略号', () => {
    // 边界：长度等于上限时原样返回，不截断、不加省略号。
    const exact = '一二三四五六七八九十一二三四五六七八九十' // 20 个字符
    expect(deriveSessionTitle([mk({ content: exact })])).toBe(exact)
  })

  it('content 为数组时取第一个非空 text part', () => {
    // 多模态消息优先用文字 part 作标题。
    const parts = [{ type: 'text', text: '来自数组的标题' }]
    expect(deriveSessionTitle([mk({ content: parts })])).toBe('来自数组的标题')
  })

  it('纯附件数组取第一个 input_file 的文件名', () => {
    // 首句只发了文件、没有文字时，用文件名当会话名。
    const parts = [{ type: 'input_file', file_id: 'f1', filename: '季度报告.pdf' }]
    expect(deriveSessionTitle([mk({ content: parts })])).toBe('季度报告.pdf')
  })

  it('字符串含 oc-file 标记与正文时剥标记后取正文', () => {
    // 服务端回读的带文件消息，标记需剥除，只保留用户正文。
    const enc = encodeURIComponent('发票.pdf')
    const content = `<oc-file:f1:${enc}>\n帮我看看这个文件`
    expect(deriveSessionTitle([mk({ content })])).toBe('帮我看看这个文件')
  })

  it('字符串仅含 oc-file 标记（纯附件回读）时取文件名', () => {
    // 纯附件消息回读后只剩标记，正文为空，退回用解码后的文件名。
    const enc = encodeURIComponent('发票.pdf')
    expect(deriveSessionTitle([mk({ content: `<oc-file:f1:${enc}>` })])).toBe('发票.pdf')
  })

  it('跳过引擎开场白 assistant，取第一条 user 消息', () => {
    // 首条是引擎自动问候（assistant），标题应取用户真正发起的第一句。
    const msgs = [mk({ role: 'assistant', content: '您好，有什么可以帮您？' }), mk({ content: '我要退货' })]
    expect(deriveSessionTitle(msgs)).toBe('我要退货')
  })

  it('没有 user 消息时返回 null', () => {
    // 只有 assistant / 无对话时无法派生标题。
    expect(deriveSessionTitle([mk({ role: 'assistant', content: '在的' })])).toBeNull()
    expect(deriveSessionTitle([])).toBeNull()
  })

  it('首条 user 内容为空或全空白时返回 null', () => {
    // 空内容不派生标题，调用方保持 id 兜底显示。
    expect(deriveSessionTitle([mk({ content: '   \n\t ' })])).toBeNull()
    expect(deriveSessionTitle([mk({ content: [] })])).toBeNull()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/domain/conversation.spec.ts`
Expected: FAIL —— 报 `deriveSessionTitle is not a function` / 导出不存在（`deriveSessionTitle` 尚未实现）。

- [ ] **Step 3: 实现纯函数**

在 `web/src/domain/conversation.ts` 文件**末尾**追加：

```ts
// MAX_TITLE_LEN 自动派生标题的最大字符数，超过则截断并补省略号。
const MAX_TITLE_LEN = 20

// extractTitleText 从单条消息的 content 取用于标题的原始文本（未归一化），取不到返回 null：
//   - 字符串：先 parseFileMarkers 剥离服务端回写的 <oc-file:...> 标记与英文注记，
//     clean 正文非空则用 clean；否则退回第一个带文件名的附件 filename（纯附件回读场景）；
//   - ConversationPart[] 数组：优先第一个非空 text part 的文本，否则第一个 input_file 的 filename。
function extractTitleText(content: unknown): string | null {
  if (typeof content === 'string') {
    const { clean, files } = parseFileMarkers(content)
    if (clean) return clean
    const named = files.find((f) => f.filename)
    return named ? named.filename : null
  }
  if (Array.isArray(content)) {
    // 优先文字 part。
    for (const p of content) {
      if (!p || typeof p !== 'object') continue
      const part = p as { type?: string; text?: unknown }
      if (part.type === 'text' && typeof part.text === 'string' && part.text.trim() !== '') {
        return part.text
      }
    }
    // 无文字则退回第一个有文件名的附件。
    for (const p of content) {
      if (!p || typeof p !== 'object') continue
      const part = p as { type?: string; filename?: unknown }
      if (part.type === 'input_file' && typeof part.filename === 'string' && part.filename !== '') {
        return part.filename
      }
    }
    return null
  }
  return null
}

// deriveSessionTitle 从会话消息派生一个可读标题，供自动命名 title 为空的会话使用。
// 取第一条 role==='user' 的消息（跳过引擎开场白 assistant，标题应是用户发起的第一句）；
// 归一化（折叠空白 + trim）后超过 MAX_TITLE_LEN 则截断补 '…'；无法派生（无 user 消息、
// 内容为空/全空白）时返回 null，由调用方保持原有 id 兜底显示、不触发命名。
export function deriveSessionTitle(messages: ConversationMessage[]): string | null {
  const first = messages.find((m) => m.role === 'user')
  if (!first) return null
  const raw = extractTitleText(first.content)
  if (!raw) return null
  const normalized = raw.replace(/\s+/g, ' ').trim()
  if (!normalized) return null
  return normalized.length > MAX_TITLE_LEN ? `${normalized.slice(0, MAX_TITLE_LEN)}…` : normalized
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/domain/conversation.spec.ts`
Expected: PASS —— 新增 `deriveSessionTitle` 全部用例通过，且原有 `hasRenderableContent` / `isDialogueMessage` / `parseFileMarkers` 用例不回归。

- [ ] **Step 5: 提交**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git add web/src/domain/conversation.ts web/src/domain/conversation.spec.ts
git commit -F - <<'EOF'
feat(web): 增加会话标题派生纯函数 deriveSessionTitle

为对话页会话自动命名提供纯逻辑：取第一条 user 消息，剥离 oc-file 标记、
折叠空白，超过 20 字符截断补省略号；纯附件取文件名，无法派生返回 null。
补充覆盖正常/边界/纯附件/开场白跳过等场景的单测。
EOF
```

---

## Task 2: 对话页接入自动命名

**Files:**
- Modify: `web/src/pages/apps/AppConversationsTab.vue`

本任务无独立单测（组件层自动命名副作用靠 Task 3 浏览器验证；核心派生逻辑已由 Task 1 单测覆盖）。

- [ ] **Step 1: 引入 `deriveSessionTitle`**

编辑 `web/src/pages/apps/AppConversationsTab.vue`，把第 232 行的 import 改为同时引入 `deriveSessionTitle`：

原：

```ts
import { isDialogueMessage } from '@/domain/conversation'
```

改为：

```ts
import { isDialogueMessage, deriveSessionTitle } from '@/domain/conversation'
```

- [ ] **Step 2: 新增 `autoTitleAttempted` 状态**

在「数据状态」区，紧接 `const currentId = ref('')`（第 254 行附近）之后新增：

```ts
// autoTitleAttempted 记录本页内已尝试过自动命名的会话 id，避免每次打开会话重复 PATCH；
// 失败（如只读角色无重命名权限被拒 403）也计入，防止反复请求。纯内存、不持久化。
const autoTitleAttempted = new Set<string>()
```

- [ ] **Step 3: 新增 `maybeAutoTitle` 函数**

在 `selectSession` 函数**之后**（第 370 行 `}` 之后、`// ─── 操作` 分隔注释之前）新增：

```ts
// maybeAutoTitle 在会话消息加载完成后，尝试用「用户发起的第一句话」自动补全空标题：
// 仅当该会话 title 为空、本页尚未尝试过、且能从当前消息派生出标题时才触发；
// 命中即调用已有的重命名接口回填 title，并就地更新左栏会话对象（响应式，无需整表刷新）。
// 自动命名属锦上添花：无论成功与否都先记入 autoTitleAttempted 防重复；失败（如只读角色
// 无重命名权限被拒 403）一律静默吞掉，不弹错误、不影响查看会话。
async function maybeAutoTitle(sid: string) {
  if (autoTitleAttempted.has(sid)) return
  const s = sessions.value.find((x) => x.id === sid)
  if (!s || s.title) return
  const title = deriveSessionTitle(messages.value)
  if (!title) return
  autoTitleAttempted.add(sid)
  try {
    await api.renameConversation(props.appId, sid, title)
    s.title = title
  } catch {
    // 静默：自动命名失败不影响查看会话（如无重命名权限）。
  }
}
```

- [ ] **Step 4: 在 `selectSession` 尾部调用**

修改 `selectSession`（第 359-370 行），在 `await scrollToBottom()` 之后增加一行调用。

原：

```ts
async function selectSession(sid: string) {
  currentId.value = sid
  try {
    // 只展示对话正文：过滤掉引擎的工具消息（role==='tool'）与仅含工具调用的空内容步骤，
    // 详见 isDialogueMessage。后端透传全量消息，是否展示由查看页决定。
    const all = await api.listMessages(props.appId, sid)
    messages.value = all.filter(isDialogueMessage)
    await scrollToBottom()
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}
```

改为（仅新增 `await maybeAutoTitle(sid)` 一行）：

```ts
async function selectSession(sid: string) {
  currentId.value = sid
  try {
    // 只展示对话正文：过滤掉引擎的工具消息（role==='tool'）与仅含工具调用的空内容步骤，
    // 详见 isDialogueMessage。后端透传全量消息，是否展示由查看页决定。
    const all = await api.listMessages(props.appId, sid)
    messages.value = all.filter(isDialogueMessage)
    await scrollToBottom()
    // 消息就绪后尝试用首句自动命名空标题会话（新会话发完首句、旧会话点开时均在此收敛）。
    await maybeAutoTitle(sid)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}
```

- [ ] **Step 5: 类型检查 / 构建确认**

Run: `cd web && npm run build`
Expected: 构建成功（vue-tsc 无类型错误），无关于 `deriveSessionTitle` / `maybeAutoTitle` / `autoTitleAttempted` 的报错。

- [ ] **Step 6: 提交**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git add web/src/pages/apps/AppConversationsTab.vue
git commit -F - <<'EOF'
feat(web): 对话页自动用首句命名空标题会话

会话消息加载后调用 deriveSessionTitle，对 title 为空的会话用「用户第一句话」
自动调重命名接口回填，替代 api_xxx 兜底显示；新会话发完首句、旧会话点开时
均在 selectSession 尾部收敛。用内存 Set 防重复 PATCH，失败静默，手动命名优先。
EOF
```

---

## Task 3: 真实浏览器验证

**Files:** 无代码改动，按 AGENTS.md「交付前检查」要求做真实浏览器功能验证。

前置：本地 k3d 环境可用（`make local-up`，浏览器需绕 7890 代理；本地账号见 AGENTS.md），
manager 后台 http://ocm.localhost 登录，进入一个已绑定运行中实例的「对话」tab。

- [ ] **Step 1: 新建会话首句自动命名**

新建会话 → 发送一句普通文字（如「帮我查一下最近的订单」）→ 等 assistant 回复完 →
左栏该会话标题应自动变为该句（不再是 `api_xxx`）。

- [ ] **Step 2: 超长截断**

新建会话发送一句超过 20 字的长句 → 左栏标题截断到 20 字并以 `…` 结尾。

- [ ] **Step 3: 旧会话打开补名**

点开一个原本显示 `api_xxx` 的旧会话（其首条为用户文字）→ 加载出历史后左栏标题
自动补为其首条用户消息。

- [ ] **Step 4: 纯附件会话**

新建会话，首条只发附件（不输入文字）→ 会话名取该文件名。

- [ ] **Step 5: 手动命名优先**

对某会话手动「重命名」为自定义标题 → 再切走切回 → 标题保持手动值，不被自动命名覆盖。

- [ ] **Step 6: 只读角色不报错**

以只读角色（有查看会话权限、无管理权限的成员）打开会话 → 页面正常展示消息，
不弹重命名失败错误（自动命名静默）。

- [ ] **Step 7: 记录验证结果**

在交付说明里给出逐项验证结果（通过 / 问题 + 截图或现象描述）。若发现问题，
回到 Task 1/2 修复并重新验证，直到全部通过再交付。

---

## Self-Review

**Spec coverage：**
- 「新建会话首句命名」→ Task 2 Step 3/4（sendMessage 末尾 selectSession → maybeAutoTitle）+ Task 3 Step 1。✓
- 「旧会话打开补名」→ Task 2 Step 4（selectSession 收敛）+ Task 3 Step 3。✓
- 「取第一条 user 消息、跳过开场白」→ Task 1（deriveSessionTitle 用 `find(role==='user')`）+ 单测。✓
- 「剥离 oc-file 标记 / 纯附件取文件名 / 数组 text part」→ Task 1 extractTitleText + 单测。✓
- 「20 字符截断补 …」→ Task 1（MAX_TITLE_LEN=20）+ 单测超长/恰好 20 用例。✓
- 「权限失败静默 + attempted Set 防重复」→ Task 2 Step 2/3 + Task 3 Step 6。✓
- 「空会话不命名 / 手动命名优先」→ Task 1 返回 null 分支 + Task 2 `!s.title` 守卫 + Task 3 Step 5。✓
- 「不改后端 / 契约 / DB」→ 全部改动集中在 web，无 handler/dto/openapi 触碰。✓

**Placeholder scan：** 无 TBD/TODO；所有代码步骤含完整代码；命令与预期输出具体。✓

**Type consistency：** `deriveSessionTitle(messages: ConversationMessage[]): string | null`、
`extractTitleText(content: unknown): string | null`、`maybeAutoTitle(sid: string)`、
`autoTitleAttempted: Set<string>` 在 Task 1/2 间命名与签名一致；复用的 `parseFileMarkers`
返回结构 `{ clean, files: [{fileId, filename}] }` 与 conversation.ts 现有实现一致。✓
