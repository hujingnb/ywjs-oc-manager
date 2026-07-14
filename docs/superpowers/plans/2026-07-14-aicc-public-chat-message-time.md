# AICC Public Chat Message Time Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a local-time `HH:mm` timestamp below every message bubble in the public AICC chat.

**Architecture:** Keep the change in `PublicAICCChatPage.vue`. Extend its presentation-only `ChatMessage` type with an optional timestamp: restored messages retain `created_at`, while newly created visitor and assistant messages use the current browser time. A small formatter produces an empty value for missing or invalid dates, so the template does not render misleading text.

**Tech Stack:** Vue 3 Composition API, TypeScript, Vitest, Vue Test Utils, Playwright.

---

## File structure

- Modify `web/src/pages/aicc/PublicAICCChatPage.vue`: retain/assign message timestamps, format them locally, render and style the timestamp below each bubble.
- Modify `web/src/pages/aicc/PublicAICCChatPage.spec.ts`: assert timestamp rendering for restored server messages and newly exchanged messages.

### Task 1: Add timestamp rendering regression tests

**Files:**
- Modify: `web/src/pages/aicc/PublicAICCChatPage.spec.ts`

- [ ] **Step 1: Write failing tests for restored and newly exchanged message timestamps**

Add these tests in the `PublicAICCChatPage` suite. Use a locally constructed ISO string for the restored-message assertion so the expectation follows the browser running the test.

```ts
// 场景：刷新恢复的服务端历史消息必须在各自气泡下方显示本地 HH:mm 发送时间。
it('renders local timestamps below restored message bubbles', async () => {
  const sentAt = new Date(2026, 6, 14, 9, 5).toISOString()
  apiState.readStoredSession.mockReturnValue('stored-session-token')
  apiState.fetchSession.mockResolvedValue({
    resolution_status: 'unknown',
    messages: [
      { id: 'msg-1', direction: 'visitor', text: '历史问题', created_at: sentAt },
      { id: 'msg-2', direction: 'assistant', text: '历史回复', created_at: sentAt },
    ],
  })

  const wrapper = mountPublicChat()
  await flushPromises()

  expect(wrapper.findAll('.message-time')).toHaveLength(2)
  expect(wrapper.findAll('.message-time').map(time => time.text())).toEqual(['09:05', '09:05'])
})

// 场景：当前页面新发送的访客消息及客服回复无需刷新，也必须立即显示发送时分。
it('renders timestamps for newly exchanged messages', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(2026, 6, 14, 14, 30))
  const wrapper = mountPublicChat()
  await flushPromises()

  await wrapper.find('textarea').setValue('报价多少')
  await wrapper.find('form.composer').trigger('submit')
  await flushPromises()

  expect(wrapper.findAll('.message-time').map(time => time.text())).toEqual(['14:30', '14:30', '14:30'])
  vi.useRealTimers()
})
```

- [ ] **Step 2: Run the component test to verify it fails for missing timestamp markup**

Run:

```bash
cd web && npm test -- PublicAICCChatPage.spec.ts
```

Expected: FAIL because `.message-time` does not exist.

### Task 2: Retain, generate, format, and display timestamps

**Files:**
- Modify: `web/src/pages/aicc/PublicAICCChatPage.vue`

- [ ] **Step 1: Extend the view model and render timestamp markup**

Change `ChatMessage` and the message template as follows:

```ts
interface ChatMessage {
  id: string
  role: 'visitor' | 'assistant'
  text?: string
  imageUrl?: string
  sentAt?: string
}
```

```vue
<article v-for="message in messages" :key="message.id" class="message-row" :class="message.role">
  <div class="bubble">
    <p v-if="message.text">{{ message.text }}</p>
    <img v-if="message.imageUrl" :src="message.imageUrl" :alt="t('aicc.publicChat.uploadedImageAlt')" />
  </div>
  <time v-if="formatMessageTime(message.sentAt)" class="message-time">{{ formatMessageTime(message.sentAt) }}</time>
</article>
```

- [ ] **Step 2: Populate the timestamp from server data and immediate browser events**

Add `sentAt: new Date().toISOString()` to both `messages.value.push` calls in `submitMessage`. Also add it to the greeting created by `resetMessagesToGreeting`, because the greeting is a visible assistant message and must meet the same per-message requirement. Preserve `created_at` in `toChatMessage`:

```ts
function toChatMessage(message: AICCMessage): ChatMessage | null {
  if (message.direction !== 'visitor' && message.direction !== 'assistant') return null
  return {
    id: message.id,
    role: message.direction === 'visitor' ? 'visitor' : 'assistant',
    text: message.text,
    sentAt: message.created_at,
  }
}
```

- [ ] **Step 3: Add local `HH:mm` formatting with invalid-date fallback**

Add this helper near `toChatMessage`:

```ts
function formatMessageTime(value?: string): string {
  if (!value) return ''
  const time = new Date(value)
  if (!Number.isFinite(time.getTime())) return ''
  return time.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
}
```

- [ ] **Step 4: Add scoped styles so the time remains visually secondary and follows bubble alignment**

```css
.message-time {
  display: block;
  margin-top: 4px;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1;
}

.message-row.visitor .message-time {
  text-align: right;
}
```

- [ ] **Step 5: Run the component test to verify it passes**

Run:

```bash
cd web && npm test -- PublicAICCChatPage.spec.ts
```

Expected: PASS with all `PublicAICCChatPage` tests green.

- [ ] **Step 6: Commit the focused implementation**

```bash
git add web/src/pages/aicc/PublicAICCChatPage.vue web/src/pages/aicc/PublicAICCChatPage.spec.ts
git commit -m "feat(aicc): 展示公开客服消息发送时间" -m "公开客服聊天页在每条消息气泡下方显示浏览器本地时区的 HH:mm。\n\n历史消息沿用服务端 created_at，新发送消息即时记录浏览器时间。"
```

### Task 3: Complete verification

**Files:**
- Verify only: `web/src/pages/aicc/PublicAICCChatPage.vue`
- Verify only: `web/src/pages/aicc/PublicAICCChatPage.spec.ts`

- [ ] **Step 1: Run type checking and production build**

Run:

```bash
cd web && npm run typecheck && npm run build
```

Expected: both commands exit with status 0.

- [ ] **Step 2: Verify through a real browser**

Run the relevant local Playwright AICC flow and inspect the public chat page after sending a message. Verify that the greeting, the newly sent visitor message, and the assistant reply each show a `HH:mm` time below the bubble; refresh the page and verify restored messages still show their timestamps.

```bash
cd web && npm run test:e2e -- aicc.spec.ts
```

Expected: the AICC end-to-end suite exits with status 0 and the browser-visible page satisfies the timestamp checks.

## Plan self-review

- Spec coverage: Task 2 implements browser-local `HH:mm`, server `created_at` retention, immediate timestamps, and invalid-time suppression. Task 1 proves the two data paths; Task 3 verifies type, build, and real browser behavior.
- Placeholder scan: no TBD/TODO or unspecified test/implementation actions remain.
- Type consistency: `ChatMessage.sentAt` is optional throughout; `AICCMessage.created_at` and `new Date().toISOString()` are both strings accepted by the formatter.
