# 实例渠道 Tab 文案修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正实例详情渠道 tab 中 `bound` 等状态原值和“尚未发起挑战”空态文案，让用户看到清晰的中文业务状态。

**Architecture:** 在渠道 hook 中新增纯展示函数 `formatChannelStatus`，让渠道页复用同一状态映射。`AuthChallengeRenderer` 只渲染真实 challenge，父页面在没有二维码或验证码时不挂载它，避免展示内部 challenge 概念。

**Tech Stack:** Vue 3 `<script setup>`、TanStack Vue Query、Vitest、Vue Test Utils、Naive UI 组件 mock。

---

## File Structure

- Modify: `web/src/api/hooks/useChannel.ts`
  - 新增渠道状态中文映射和 `formatChannelStatus(status?: string)`。
- Modify: `web/src/api/hooks/useChannel.test.ts`
  - 覆盖 `bound`、常见状态、空状态和未知状态。
- Modify: `web/src/components/AuthChallengeRenderer.vue`
  - 删除无 challenge 时的“尚未发起挑战”默认文案。
- Create: `web/src/components/__tests__/AuthChallengeRenderer.spec.ts`
  - 覆盖无 challenge 时不渲染内部空态文案。
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
  - 使用 `formatChannelStatus`，并仅在 `visibleChallenge` 存在时挂载 `AuthChallengeRenderer`。
- Create: `web/src/pages/apps/AppChannelsTab.spec.ts`
  - 覆盖已绑定状态展示中文、不展示原始 `bound`、不展示“尚未发起挑战”。

---

### Task 1: Channel Status Formatter

**Files:**
- Modify: `web/src/api/hooks/useChannel.test.ts`
- Modify: `web/src/api/hooks/useChannel.ts`

- [ ] **Step 1: Write the failing formatter tests**

In `web/src/api/hooks/useChannel.test.ts`, update the import list:

```ts
import {
  channelChallengeFromProgress,
  formatChannelStatus,
  shouldShowChallengePending,
  type ChannelProgress,
} from '@/api/hooks/useChannel'
```

Add this block after the `channelChallengeFromProgress` tests:

```ts
describe('formatChannelStatus', () => {
  // 常见渠道状态：后端原值应映射为用户能理解的中文业务文案。
  it.each([
    ['unbound', '未绑定'], // 初始渠道记录：还没有绑定账号。
    ['pending_auth', '等待扫码授权'], // 登录任务已发起，等待二维码扫码或确认。
    ['bound', '已绑定'], // 用户反馈的核心场景：不能直接展示 bound。
    ['failed', '绑定失败'], // worker 或渠道侧返回失败。
    ['expired', '二维码已过期'], // 当前登录二维码已经过期。
    ['unbound_by_user', '已解绑'], // 用户主动解绑后的状态。
    ['deleted', '已删除'], // 渠道记录被删除后的兜底展示。
  ])('status=%s 映射为 %s', (status, label) => {
    expect(formatChannelStatus(status)).toBe(label)
  })

  // 空进度：轮询尚未返回数据时展示“未发起”，不展示 undefined。
  it('status 为空时展示未发起', () => {
    expect(formatChannelStatus(undefined)).toBe('未发起')
  })

  // 未知状态：保留原值帮助发现后端新增状态，同时用中文前缀解释异常。
  it('未知 status 保留原值并加中文前缀', () => {
    expect(formatChannelStatus('new_state')).toBe('未知状态：new_state')
  })
})
```

- [ ] **Step 2: Run the formatter tests and verify failure**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useChannel.test.ts
```

Expected: FAIL because `formatChannelStatus` is not exported.

- [ ] **Step 3: Implement the formatter**

In `web/src/api/hooks/useChannel.ts`, add this after the `ChannelProgress` interface:

```ts
// channelStatusLabels 将后端渠道状态机原值映射为渠道 tab 的中文业务文案。
// 未知状态由 formatChannelStatus 保留原值，方便后端新增状态时前端及时暴露差异。
const channelStatusLabels: Record<string, string> = {
  unbound: '未绑定',
  pending_auth: '等待扫码授权',
  bound: '已绑定',
  failed: '绑定失败',
  expired: '二维码已过期',
  unbound_by_user: '已解绑',
  deleted: '已删除',
}

// formatChannelStatus 将渠道绑定状态转成用户可读文案；空值表示轮询尚未返回或尚未发起登录。
export function formatChannelStatus(status?: string): string {
  if (!status) return '未发起'
  return channelStatusLabels[status] ?? `未知状态：${status}`
}
```

- [ ] **Step 4: Run formatter tests and verify pass**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useChannel.test.ts
```

Expected: PASS.

---

### Task 2: Challenge Renderer Empty State

**Files:**
- Create: `web/src/components/__tests__/AuthChallengeRenderer.spec.ts`
- Modify: `web/src/components/AuthChallengeRenderer.vue`

- [ ] **Step 1: Write the failing renderer test**

Create `web/src/components/__tests__/AuthChallengeRenderer.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import AuthChallengeRenderer from '../AuthChallengeRenderer.vue'

describe('AuthChallengeRenderer', () => {
  // 无 challenge：组件不再展示“尚未发起挑战”，该状态由父级渠道页解释。
  it('challenge 为空时不渲染内部空态文案', () => {
    const wrapper = mount(AuthChallengeRenderer, { props: { challenge: null } })

    expect(wrapper.text()).not.toContain('尚未发起挑战')
    expect(wrapper.text()).toBe('')
  })
})
```

- [ ] **Step 2: Run the renderer test and verify failure**

Run:

```bash
rtk npm --prefix web test -- --run src/components/__tests__/AuthChallengeRenderer.spec.ts
```

Expected: FAIL because the current component renders “尚未发起挑战”.

- [ ] **Step 3: Remove the internal empty-state copy**

In `web/src/components/AuthChallengeRenderer.vue`, replace the opening template branch:

```vue
<template>
  <div class="challenge-renderer">
    <div v-if="!challenge" class="state-text">尚未发起挑战</div>
    <template v-else-if="challenge.challenge_type === 'qrcode' && challenge.qrcode">
```

with:

```vue
<template>
  <div class="challenge-renderer">
    <template v-if="challenge?.challenge_type === 'qrcode' && challenge.qrcode">
```

Then replace the final unknown branch:

```vue
    <p v-else class="state-text danger">未知挑战类型：{{ challenge.challenge_type ?? challenge.status }}</p>
```

with:

```vue
    <p v-else-if="challenge" class="state-text danger">未知挑战类型：{{ challenge.challenge_type ?? challenge.status }}</p>
```

- [ ] **Step 4: Run renderer test and verify pass**

Run:

```bash
rtk npm --prefix web test -- --run src/components/__tests__/AuthChallengeRenderer.spec.ts
```

Expected: PASS.

---

### Task 3: Channel Tab Integration

**Files:**
- Create: `web/src/pages/apps/AppChannelsTab.spec.ts`
- Modify: `web/src/pages/apps/AppChannelsTab.vue`

- [ ] **Step 1: Write the failing page test**

Create `web/src/pages/apps/AppChannelsTab.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { defineComponent, h, provide, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AppChannelsTab from './AppChannelsTab.vue'
import type { ChannelProgress } from '@/api/hooks/useChannel'
import type { AppDTO } from '@/api/hooks/useApps'

const progress = ref<ChannelProgress | null>(null)
const beginAuth = vi.fn()
const unbindChannel = vi.fn()

vi.mock('naive-ui', () => ({
  NButton: defineComponent({
    name: 'NButton',
    props: ['disabled', 'loading'],
    emits: ['click'],
    setup(props, { slots, emit }) {
      return () => h('button', {
        disabled: props.disabled as boolean,
        onClick: () => emit('click'),
      }, slots.default?.())
    },
  }),
  NCard: defineComponent({
    name: 'NCard',
    setup(_, { slots }) {
      return () => h('section', [
        slots.header?.(),
        slots['header-extra']?.(),
        slots.default?.(),
      ])
    },
  }),
  NSpace: defineComponent({
    name: 'NSpace',
    setup(_, { slots }) {
      return () => h('div', slots.default?.())
    },
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 'user-1', role: 'org_member', org_id: 'org-1' },
  }),
}))

vi.mock('@/api/hooks/useChannel', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/hooks/useChannel')>()
  return {
    ...actual,
    useChannelProgressQuery: () => ({ data: progress }),
    useBeginChannelAuth: () => ({ mutateAsync: beginAuth }),
    useUnbindChannel: () => ({ mutateAsync: unbindChannel }),
  }
})

const app = ref<AppDTO>({
  id: 'app-1',
  org_id: 'org-1',
  owner_user_id: 'user-1',
  name: '测试实例',
  status: 'running',
  api_key_status: 'succeeded',
})

function mountChannelsTab() {
  return mount(defineComponent({
    setup() {
      provide('app', app)
      return () => h(AppChannelsTab, { appId: 'app-1' })
    },
  }))
}

describe('AppChannelsTab', () => {
  beforeEach(() => {
    progress.value = null
    beginAuth.mockReset()
    unbindChannel.mockReset()
  })

  // 已绑定状态：页面展示中文“已绑定”，不泄露后端原值 bound 或内部 challenge 空态。
  it('已绑定渠道展示中文状态且不显示 challenge 空态', () => {
    progress.value = {
      status: 'bound',
      bound_identity: 'alice',
      updated_at: '2026-05-25T12:00:00Z',
    }

    const wrapper = mountChannelsTab()

    expect(wrapper.text()).toContain('当前状态：已绑定')
    expect(wrapper.text()).toContain('已绑定：alice')
    expect(wrapper.text()).not.toContain('当前状态：bound')
    expect(wrapper.text()).not.toContain('尚未发起挑战')
  })
})
```

- [ ] **Step 2: Run the page test and verify failure**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/apps/AppChannelsTab.spec.ts
```

Expected: FAIL because the page currently renders `当前状态：bound` and mounts the renderer with no challenge.

- [ ] **Step 3: Wire the formatter and conditional renderer**

In `web/src/pages/apps/AppChannelsTab.vue`, update the import list from `@/api/hooks/useChannel`:

```ts
import {
  useBeginChannelAuth,
  useChannelProgressQuery,
  useUnbindChannel,
  channelChallengeFromProgress,
  formatChannelStatus,
  shouldShowChallengePending,
  type ChannelChallenge,
} from '@/api/hooks/useChannel'
```

Replace the renderer line:

```vue
      <AuthChallengeRenderer :challenge="visibleChallenge" @rendered="onQrRendered" />
```

with:

```vue
      <AuthChallengeRenderer v-if="visibleChallenge" :challenge="visibleChallenge" @rendered="onQrRendered" />
```

Replace `statusLabel`:

```ts
const statusLabel = computed(() => {
  if (!progress.value) return '未发起'
  return progress.value.status
})
```

with:

```ts
const statusLabel = computed(() => formatChannelStatus(progress.value?.status))
```

- [ ] **Step 4: Run page test and verify pass**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/apps/AppChannelsTab.spec.ts
```

Expected: PASS.

---

### Task 4: Final Verification

**Files:**
- Verify: all files changed in Tasks 1-3.

- [ ] **Step 1: Run targeted frontend tests**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useChannel.test.ts src/components/__tests__/AuthChallengeRenderer.spec.ts src/pages/apps/AppChannelsTab.spec.ts
```

Expected: PASS.

- [ ] **Step 2: Run frontend typecheck**

Run:

```bash
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 3: Inspect the diff for unrelated changes**

Run:

```bash
rtk git diff -- web/src/api/hooks/useChannel.ts web/src/api/hooks/useChannel.test.ts web/src/components/AuthChallengeRenderer.vue web/src/components/__tests__/AuthChallengeRenderer.spec.ts web/src/pages/apps/AppChannelsTab.vue web/src/pages/apps/AppChannelsTab.spec.ts
```

Expected: diff only contains channel tab copy/status-display changes and tests.

- [ ] **Step 4: Commit the implementation**

Run:

```bash
rtk git add web/src/api/hooks/useChannel.ts web/src/api/hooks/useChannel.test.ts web/src/components/AuthChallengeRenderer.vue web/src/components/__tests__/AuthChallengeRenderer.spec.ts web/src/pages/apps/AppChannelsTab.vue web/src/pages/apps/AppChannelsTab.spec.ts
rtk git commit -m "fix(channel): 修正渠道页绑定状态文案" -m "将渠道绑定状态统一映射为中文展示，避免页面直接显示 bound 等后端状态原值。\n\n无二维码或验证码 challenge 时不再展示“尚未发起挑战”，由渠道 tab 的状态行表达当前进度。\n\n测试覆盖状态映射、无 challenge 空态和已绑定渠道页展示。"
```

Expected: commit succeeds.

---

## Self-Review

- Spec coverage: `bound` 改为“已绑定”由 Task 1 和 Task 3 覆盖；“尚未发起挑战”移除由 Task 2 和 Task 3 覆盖；类似渠道状态原值回退由 Task 1 的常见状态映射覆盖。
- Placeholder scan: 本计划没有占位式说明或缺少代码细节的步骤。
- Type consistency: `formatChannelStatus(status?: string): string` 在 Task 1 定义，Task 3 按相同名称导入使用；`ChannelProgress`、`AppDTO` 均来自现有 hook 类型。
