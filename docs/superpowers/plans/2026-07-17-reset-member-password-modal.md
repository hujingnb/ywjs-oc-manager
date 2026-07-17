# Member Password Reset Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the broken `window.prompt` plus username-verification flow with one masked, in-app password reset modal that validates and submits the new password.

**Architecture:** Add a focused `ResetMemberPasswordModal` component that owns the short-lived password input, minimum-length gating, visibility toggle, and reset-on-close behavior. Keep `MembersPage` responsible for selecting the target member, invoking the existing mutation, and presenting success or failure feedback; do not change the API or generic `ConfirmActionModal`.

**Tech Stack:** Vue 3 `<script setup>`, TypeScript, Naive UI, vue-i18n, Vitest, Vue Test Utils, Playwright Chromium

---

## File Structure

- Create `web/src/components/ResetMemberPasswordModal.vue`: render the dedicated modal, own sensitive input state, validate length, and emit `confirm(password)` or `cancel`.
- Create `web/src/components/__tests__/ResetMemberPasswordModal.spec.ts`: verify masked input configuration, button gating, password emission, busy state, state retention, and close-time clearing.
- Modify `web/src/pages/org/MembersPage.vue`: replace the reset-password `ConfirmActionModal` and `window.prompt` flow with the dedicated component.
- Modify `web/src/pages/org/MembersPage.spec.ts`: verify the page opens the dedicated modal directly and passes the emitted password to the existing mutation on the correct member.
- Modify `web/tests/e2e/members.spec.ts`: replace the obsolete prompt/username-confirmation scenario with a headless, non-submitting modal interaction scenario.

### Task 1: Build the dedicated password reset modal

**Files:**
- Create: `web/src/components/ResetMemberPasswordModal.vue`
- Create: `web/src/components/__tests__/ResetMemberPasswordModal.spec.ts`

- [ ] **Step 1: Write the failing component tests**

Create `web/src/components/__tests__/ResetMemberPasswordModal.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { NInput } from 'naive-ui'
import { nextTick } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'

import { i18n } from '@/i18n'
import ResetMemberPasswordModal from '../ResetMemberPasswordModal.vue'

const wrappers: Array<ReturnType<typeof mountModal>> = []

// mountModal 使用真实 Naive UI 组件验证密码输入和确认按钮的最终 DOM 行为。
function mountModal(overrides: Partial<{ visible: boolean; username: string; busy: boolean }> = {}) {
  const wrapper = mount(ResetMemberPasswordModal, {
    attachTo: document.body,
    props: {
      visible: true,
      username: 'zhangsan',
      busy: false,
      ...overrides,
    },
    global: { plugins: [i18n] },
  })
  wrappers.push(wrapper)
  return wrapper
}

// 每条用例后移除 Teleport 内容，避免前一个弹窗污染后续 DOM 断言。
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  document.body.innerHTML = ''
})

describe('ResetMemberPasswordModal', () => {
  // 新密码属于敏感信息，输入框必须默认掩码，并允许点击图标切换显示。
  it('默认使用可切换显示的密码输入框', async () => {
    const wrapper = mountModal()
    await nextTick()

    const input = document.querySelector('.n-modal input') as HTMLInputElement
    expect(input.type).toBe('password')
    expect(wrapper.findComponent(NInput).props('showPasswordOn')).toBe('click')
  })

  // 少于 8 位的新密码不满足后端边界，确认按钮应保持禁用。
  it('密码不足 8 位时禁用确认按钮', async () => {
    mountModal()
    await nextTick()

    const input = document.querySelector('.n-modal input') as HTMLInputElement
    input.value = 'short'
    input.dispatchEvent(new Event('input'))
    await nextTick()

    const button = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(button.disabled).toBe(true)
  })

  // 合法密码解锁确认按钮，点击后只通过 confirm 事件把当前密码交给页面层。
  it('合法密码解锁按钮并回传新密码', async () => {
    const wrapper = mountModal()
    await nextTick()

    const input = document.querySelector('.n-modal input') as HTMLInputElement
    input.value = 'Zs12345612'
    input.dispatchEvent(new Event('input'))
    await nextTick()

    const button = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(button.disabled).toBe(false)
    button.click()
    await nextTick()
    expect(wrapper.emitted('confirm')).toEqual([['Zs12345612']])
  })

  // 请求进行中必须禁止重复提交，并由按钮展示 loading 状态。
  it('busy 时禁用确认按钮', async () => {
    mountModal({ busy: true })
    await nextTick()

    const input = document.querySelector('.n-modal input') as HTMLInputElement
    input.value = 'Zs12345612'
    input.dispatchEvent(new Event('input'))
    await nextTick()

    const button = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(button.disabled).toBe(true)
  })

  // API 失败期间父组件保持 visible 时密码应保留；真正关闭后再次打开必须清空。
  it('保持打开时保留密码，关闭后重新打开清空密码', async () => {
    const wrapper = mountModal()
    await nextTick()

    const input = document.querySelector('.n-modal input') as HTMLInputElement
    input.value = 'Zs12345612'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    expect(input.value).toBe('Zs12345612')

    await wrapper.setProps({ visible: false })
    await wrapper.setProps({ visible: true })
    await nextTick()
    const reopenedInput = document.querySelector('.n-modal input') as HTMLInputElement
    expect(reopenedInput.value).toBe('')
  })
})
```

- [ ] **Step 2: Run the component test to verify it fails**

Run:

```bash
cd web && npm test -- --run src/components/__tests__/ResetMemberPasswordModal.spec.ts
```

Expected: FAIL because `ResetMemberPasswordModal.vue` does not exist.

- [ ] **Step 3: Implement the minimal modal component**

Create `web/src/components/ResetMemberPasswordModal.vue`:

```vue
<template>
  <n-modal :show="visible" :mask-closable="true" @update:show="(value) => { if (!value) onCancel() }">
    <n-card
      :title="t('org.members.modal.resetTitle')"
      :bordered="false"
      role="dialog"
      aria-modal="true"
      style="width: min(440px, 92vw)"
    >
      <p class="reset-message">
        {{ t('org.members.modal.resetMessage', { username }) }}
      </p>

      <n-form-item
        :label="t('org.members.modal.resetPasswordPrompt', { username })"
        :show-feedback="false"
      >
        <n-input
          v-model:value="password"
          type="password"
          show-password-on="click"
          autocomplete="new-password"
        />
      </n-form-item>

      <template #footer>
        <n-space justify="end">
          <n-button @click="onCancel">{{ t('common.actions.cancel') }}</n-button>
          <n-button
            type="error"
            :disabled="busy || password.length < 8"
            :loading="busy"
            @click="onConfirm"
          >
            {{ t('org.members.modal.resetConfirm') }}
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NButton, NCard, NFormItem, NInput, NModal, NSpace } from 'naive-ui'
import { useI18n } from 'vue-i18n'

// ResetMemberPasswordModal 只负责收集一次新密码并执行前端最小长度校验。
const props = defineProps<{
  visible: boolean
  username: string
  busy?: boolean
}>()

// confirm 将新密码交给页面层提交；cancel 统一覆盖按钮与遮罩关闭路径。
const emit = defineEmits<{
  (event: 'confirm', password: string): void
  (event: 'cancel'): void
}>()

const { t } = useI18n()
// password 仅保存在弹窗生命周期内，关闭后立即清空，避免下次打开复用敏感值。
const password = ref('')

watch(
  () => props.visible,
  () => { password.value = '' },
)

function onConfirm() {
  if (props.busy || password.value.length < 8) return
  emit('confirm', password.value)
}

function onCancel() {
  password.value = ''
  emit('cancel')
}
</script>

<style scoped>
.reset-message {
  margin: 0 0 16px;
  color: var(--color-text-secondary);
}
</style>
```

- [ ] **Step 4: Run the component test to verify it passes**

Run:

```bash
cd web && npm test -- --run src/components/__tests__/ResetMemberPasswordModal.spec.ts
```

Expected: PASS with 5 tests.

- [ ] **Step 5: Commit the dedicated component**

```bash
git add web/src/components/ResetMemberPasswordModal.vue web/src/components/__tests__/ResetMemberPasswordModal.spec.ts
git commit -m "feat(web): 增加成员密码重置弹窗" -m "使用默认掩码且可切换显示的输入框收集新密码。

在弹窗内校验最小长度、阻止重复提交，并在关闭后清理敏感输入。"
```

### Task 2: Integrate the modal into the member page

**Files:**
- Modify: `web/src/pages/org/MembersPage.spec.ts`
- Modify: `web/src/pages/org/MembersPage.vue`
- Modify: `web/tests/e2e/members.spec.ts`

- [ ] **Step 1: Add the reset mutation mock and focused modal stub**

In `web/src/pages/org/MembersPage.spec.ts`, add this hoisted mock next to `createMemberAppMock`:

```ts
const resetMemberPasswordMock = vi.hoisted(() => ({
  mutateAsync: vi.fn(async () => undefined),
}))
```

Replace the inline reset hook mock with:

```ts
useResetMemberPassword: () => ({
  mutateAsync: resetMemberPasswordMock.mutateAsync,
  isPending: ref(false),
}),
```

Define this stub before `describe`:

```ts
// resetMemberPasswordModalStub 只暴露页面集成所需的目标账号与 confirm/cancel 事件。
const resetMemberPasswordModalStub = defineComponent({
  props: {
    visible: Boolean,
    username: String,
    busy: Boolean,
  },
  emits: ['confirm', 'cancel'],
  setup(props, { emit }) {
    return () => props.visible
      ? h('section', { class: 'reset-password-modal' }, [
          h('span', { class: 'reset-username' }, props.username),
          h('button', {
            class: 'confirm-reset-password',
            onClick: () => emit('confirm', 'Zs12345612'),
          }, '确认重置'),
          h('button', {
            class: 'cancel-reset-password',
            onClick: () => emit('cancel'),
          }, '取消'),
        ])
      : null
  },
})
```

Register it in `mountPage` alongside the existing generic modal stub:

```ts
ConfirmActionModal: true,
ResetMemberPasswordModal: resetMemberPasswordModalStub,
```

- [ ] **Step 2: Write failing member-page reset tests**

Add inside the existing `describe('MembersPage', ...)` block:

```ts
  // 点击重置入口应直接打开站内密码弹窗，不再调用浏览器原生 prompt。
  it('直接打开密码重置弹窗并提交目标成员的新密码', async () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }
    resetMemberPasswordMock.mutateAsync.mockReset()
    resetMemberPasswordMock.mutateAsync.mockResolvedValue(undefined)
    const promptSpy = vi.spyOn(window, 'prompt')
    const wrapper = mountPage()

    const resetButtons = wrapper.findAll('button').filter(button => button.text() === '重置密码')
    await resetButtons[1].trigger('click')
    expect(promptSpy).not.toHaveBeenCalled()
    expect(wrapper.find('.reset-password-modal').exists()).toBe(true)
    expect(wrapper.find('.reset-username').text()).toBe('member')

    await wrapper.find('.confirm-reset-password').trigger('click')
    expect(resetMemberPasswordMock.mutateAsync).toHaveBeenCalledWith({
      userId: 'member-1',
      password: 'Zs12345612',
    })
    await nextTick()
    expect(wrapper.find('.reset-password-modal').exists()).toBe(false)
    expect(wrapper.text()).toContain('已重置密码')

    promptSpy.mockRestore()
  })

  // API 失败时保留弹窗目标并展示错误，允许管理员修改密码后重试。
  it('密码重置失败时保留弹窗并展示错误', async () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }
    resetMemberPasswordMock.mutateAsync.mockReset()
    resetMemberPasswordMock.mutateAsync.mockRejectedValue(new Error('重置服务暂不可用'))
    const wrapper = mountPage()

    const resetButtons = wrapper.findAll('button').filter(button => button.text() === '重置密码')
    await resetButtons[1].trigger('click')
    await wrapper.find('.confirm-reset-password').trigger('click')
    await nextTick()

    expect(wrapper.find('.reset-password-modal').exists()).toBe(true)
    expect(wrapper.text()).toContain('重置服务暂不可用')
  })
```

- [ ] **Step 3: Run the member-page test to verify the new cases fail**

Run:

```bash
cd web && npm test -- --run src/pages/org/MembersPage.spec.ts
```

Expected: FAIL because `MembersPage` still calls `window.prompt` and does not render the dedicated modal.

- [ ] **Step 4: Replace the obsolete E2E scenario before changing the implementation**

Replace `web/tests/e2e/members.spec.ts` with:

```ts
import { expect, test } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// 组织管理员直接在站内弹窗输入新密码；本用例只验证交互并取消，不修改 fixture 密码。
test('org_admin 在站内弹窗输入新密码并取消', async ({ page }) => {
  const fx = loadE2EFixture()
  let nativeDialogOpened = false
  page.on('dialog', async (dialog) => {
    nativeDialogOpened = true
    await dialog.dismiss()
  })

  await loginAs(page, 'org_admin', fx, 'zh')
  await page.goto('/members')

  const row = page.getByRole('row', { name: new RegExp(fx.org_member_login) })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '重置密码' }).click()
  expect(nativeDialogOpened).toBe(false)

  const passwordInput = page.getByLabel(new RegExp(`输入成员 ${fx.org_member_login} 的新密码`))
  await expect(passwordInput).toHaveAttribute('type', 'password')
  await passwordInput.fill('1234567')
  await expect(page.getByRole('button', { name: '确认重置' })).toBeDisabled()

  await passwordInput.fill('Zs12345612')
  await expect(page.getByRole('button', { name: '确认重置' })).toBeEnabled()
  await page.locator('.n-input__eye').click()
  await expect(passwordInput).toHaveAttribute('type', 'text')

  await page.getByRole('button', { name: '取消' }).click()
  await expect(passwordInput).toBeHidden()

  await row.getByRole('button', { name: '重置密码' }).click()
  const reopenedInput = page.getByLabel(new RegExp(`输入成员 ${fx.org_member_login} 的新密码`))
  await expect(reopenedInput).toHaveValue('')
  await page.getByRole('button', { name: '取消' }).click()
})
```

Do not run it yet; the existing implementation would open a native prompt and fail the new scenario. The focused member-page unit test already provides the fast RED signal before implementation.

- [ ] **Step 5: Replace the old two-dialog flow with the dedicated modal**

In `web/src/pages/org/MembersPage.vue`, replace the reset `ConfirmActionModal` block with:

```vue
    <ResetMemberPasswordModal
      :visible="!!resetTarget"
      :username="resetTarget?.username ?? ''"
      :busy="resetMutation.isPending.value"
      @confirm="onConfirmReset"
      @cancel="resetTarget = null"
    />
```

Import the new component next to `ConfirmActionModal`:

```ts
import ResetMemberPasswordModal from '@/components/ResetMemberPasswordModal.vue'
```

Replace the reset state declaration and remove `resetNewPassword`:

```ts
// resetTarget 暂存正在重置密码的成员；敏感的新密码只保存在专用弹窗内。
const resetTarget = ref<Member | null>(null)
```

Replace `openResetForm`:

```ts
// openResetForm 直接打开站内密码弹窗，避免原生 prompt 与二次确认文案不一致。
function openResetForm(member: Member) {
  resetTarget.value = member
  resetFeedback.value = ''
  resetError.value = false
}
```

Change `onConfirmReset` to accept the emitted password:

```ts
// onConfirmReset 提交专用弹窗回传的新密码；失败时保留目标成员供原弹窗继续重试。
async function onConfirmReset(newPassword: string) {
  if (!resetTarget.value) return
  resetFeedback.value = ''
  resetError.value = false
  try {
    await resetMutation.mutateAsync({ userId: resetTarget.value.id, password: newPassword })
    resetFeedback.value = t('org.members.modal.resetSuccess')
    resetTarget.value = null
  } catch (err) {
    resetError.value = true
    resetFeedback.value = err instanceof Error ? err.message : t('org.members.modal.resetFailed')
  }
}
```

- [ ] **Step 6: Run both focused test files**

Run:

```bash
cd web && npm test -- --run src/components/__tests__/ResetMemberPasswordModal.spec.ts src/pages/org/MembersPage.spec.ts
```

Expected: both files PASS; the modal file reports 5 tests and the member page reports the 2 new reset-flow tests.

- [ ] **Step 7: Run the frontend type check**

Run:

```bash
cd web && npm run typecheck
```

Expected: exit code 0 with no Vue or TypeScript errors.

- [ ] **Step 8: Commit the page integration and focused E2E scenario**

```bash
git add web/src/pages/org/MembersPage.vue web/src/pages/org/MembersPage.spec.ts web/tests/e2e/members.spec.ts
git commit -m "fix(web): 修复成员密码重置确认流程" -m "移除浏览器原生 prompt 和错误的用户名强校验弹窗。

成员页改用专用密码弹窗，并覆盖成功提交与失败保留重试场景。"
```

### Task 3: Focused regression and headless browser verification

**Files:**
- Verify only; no planned source changes.

- [ ] **Step 1: Confirm no stale prompt-based flow remains**

Run:

```bash
rg -n "resetNewPassword|window\\.prompt|verify-value=\"resetTarget|resetPasswordPrompt" web/src/pages/org/MembersPage.vue web/src/components/ResetMemberPasswordModal.vue
```

Expected: only the intentional `resetPasswordPrompt` label reference remains in `ResetMemberPasswordModal.vue`.

- [ ] **Step 2: Run the focused regression once**

Run:

```bash
cd web && npm test -- --run src/components/__tests__/ResetMemberPasswordModal.spec.ts src/pages/org/MembersPage.spec.ts && npm run typecheck
```

Expected: all targeted tests PASS and typecheck exits 0.

- [ ] **Step 3: Run a real Chromium check in headless mode against the local manager**

Run only the updated member spec on the default headless Chromium project:

```bash
cd web && npm run test:e2e -- tests/e2e/members.spec.ts --project=chromium
```

Expected: 1 test PASS in headless Chromium. The spec uses the seeded local organization-admin fixture, cancels instead of submitting, and verifies that reopening starts with an empty password.

- [ ] **Step 4: Inspect the final diff and worktree**

Run:

```bash
git diff --check HEAD~2..HEAD
git status --short --branch
```

Expected: `git diff --check` exits 0. The only unrelated entry remains the pre-existing untracked `a.sh`; do not add or modify it.
