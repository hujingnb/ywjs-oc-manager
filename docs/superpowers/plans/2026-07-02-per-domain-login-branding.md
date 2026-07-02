# 按域名白标登录页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按浏览器访问的 hostname 给登录页渲染不同的白标整页，纯前端实现，认证逻辑与后端 API 不变。

**Architecture:** 抽取登录行为到共享 `useLogin()` composable；`/login` 路由改挂 `LoginHost`，它按 `window.location.hostname` 经精确注册表 `resolveVariant` 选定变体组件渲染，并应用可选 title/favicon。现有 `AuthLayout.vue`（背景+hero）+ `LoginPage.vue`（登录卡片）重构为默认变体 `DefaultLoginPage.vue` 内嵌 `LoginForm.vue`，视觉与现状 100% 一致，作为回归基线。首期只搭机制 + 默认变体，不落示例白标变体。

**Tech Stack:** Vue 3 `<script setup>` + TypeScript、vue-router、Pinia、vue-i18n、Vitest + @vue/test-utils、Vite。

---

## 文件结构

全部落在 `web/src/pages/login/` 下（新增/迁移）：

- `useLogin.ts` — 共享登录行为 composable（登录状态、验证码探测/交互、onSubmit+redirect）。唯一行为实现。
- `variants/registry.ts` — `LoginVariant` 类型、`DEFAULT_VARIANT`、`VARIANTS` 精确 hostname 映射、`resolveVariant()`、`applyVariantChrome()`（title/favicon 副作用，纯函数便于测试）。
- `LoginHost.vue` — `/login` 路由入口，按 hostname 选变体渲染并应用 chrome。
- `variants/default/LoginForm.vue` — 默认变体登录卡片（源自 `LoginPage.vue`，改用 `useLogin`）。
- `variants/default/DefaultLoginPage.vue` — 默认变体整页（源自 `AuthLayout.vue`，`<RouterView>` 换成 `<LoginForm>`）。

删除（内容已迁移、无其它引用）：

- `web/src/layouts/AuthLayout.vue` 与 `web/src/layouts/AuthLayout.spec.ts`
- `web/src/pages/login/LoginPage.vue` 与 `web/src/pages/login/LoginPage.spec.ts`

修改：

- `web/src/app/router.ts` — `/login` 由 `AuthLayout` + 子 `LoginPage` 改为直接挂 `LoginHost`。

**测试策略说明**：spec 列出的「`useLogin` 单测」通过其真实消费者 `LoginForm.spec.ts` 覆盖（默认表单即 composable 的默认宿主），避免再建一个哑宿主组件重复测同一套 fetch 探测逻辑（DRY）。`registry`/`applyVariantChrome`/`LoginHost` 各有独立测试。

---

### Task 1: 抽取 useLogin composable 与默认登录表单 LoginForm

把登录行为集中到 `useLogin.ts`，默认表单 `LoginForm.vue` 只保留标记并接线 composable。行为通过 `LoginForm.spec.ts` 覆盖。

**Files:**
- Create: `web/src/pages/login/useLogin.ts`
- Create: `web/src/pages/login/variants/default/LoginForm.vue`
- Test: `web/src/pages/login/variants/default/LoginForm.spec.ts`

- [ ] **Step 1: 写失败测试 `LoginForm.spec.ts`**

内容源自现有 `web/src/pages/login/LoginPage.spec.ts`，改为挂载 `LoginForm`，并新增「成功提交后 replace 到 redirect」断言：

```ts
// LoginForm.spec.ts — 默认变体登录表单交互单测。
// 通过默认表单这一 useLogin 的真实宿主，覆盖 composable 行为：
// 验证码开启时未 verified 按钮禁用、verified 后带 payload 提交并 redirect、
// 失败后重置 widget、关闭(204)时按钮直接可用。
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import LoginForm from './LoginForm.vue'

const loginMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ loading: false, login: loginMock }),
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ currentRoute: { value: { query: {} } }, replace: replaceMock }),
}))
// LocaleSwitcher 依赖 Pinia/i18n 插件，用占位桩替代，避免无关插件配置。
vi.mock('@/components/LocaleSwitcher.vue', () => ({
  default: { template: '<div />' },
}))

// 把出题探测 fetch 固定为指定状态码。
function stubChallenge(status: number) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status }))
}

// mountForm：挂载登录表单并注入 i18n 插件（useLogin 与模板均用到 useI18n）。
function mountForm() {
  return mount(LoginForm, { global: { plugins: [i18n] } })
}

// dispatchVerified：在 altcha-widget 上派发 verified statechange，模拟工作量证明完成。
function dispatchVerified(wrapper: ReturnType<typeof mountForm>, payload: string) {
  const widget = wrapper.find('altcha-widget')
  widget.element.dispatchEvent(
    new CustomEvent('statechange', { detail: { state: 'verified', payload } }),
  )
}

describe('LoginForm 登录交互', () => {
  beforeEach(() => {
    loginMock.mockReset()
    replaceMock.mockReset()
  })

  // 开启验证码(200)时，未 verified 前提交按钮禁用。
  it('未 verified 时按钮禁用', async () => {
    stubChallenge(200)
    const wrapper = mountForm()
    await flushPromises()
    const widget = wrapper.find('altcha-widget')
    const btn = wrapper.find('button.login-submit')
    expect(widget.attributes('challenge')).toBe('/api/v1/auth/altcha-challenge')
    expect(widget.attributes('configuration')).toContain('"hideFooter":true')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(wrapper.find('.login-captcha-hint').exists()).toBe(true)
  })

  // verified 后按钮可用，提交把 payload 传给 auth.login，成功后 replace 到默认 redirect。
  it('verified 后带 payload 提交并 redirect', async () => {
    stubChallenge(200)
    loginMock.mockResolvedValue({})
    const wrapper = mountForm()
    await flushPromises()
    dispatchVerified(wrapper, 'PAYLOAD123')
    await flushPromises()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(loginMock).toHaveBeenCalledWith('', '', '', 'PAYLOAD123')
    expect(replaceMock).toHaveBeenCalledWith('/')
  })

  // 登录失败后 widget 重置并重新验证，清空 verified → 按钮重新禁用。
  it('登录失败后重置 widget', async () => {
    stubChallenge(200)
    loginMock.mockRejectedValue(new Error('账号或密码错误'))
    const wrapper = mountForm()
    await flushPromises()

    const resetSpy = vi.fn()
    const verifySpy = vi.fn().mockResolvedValue(null)
    const widgetEl = wrapper.find('altcha-widget').element as HTMLElement & {
      reset?: () => void
      verify?: () => Promise<unknown>
    }
    widgetEl.reset = resetSpy
    widgetEl.verify = verifySpy

    dispatchVerified(wrapper, 'P')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(resetSpy).toHaveBeenCalled()
    expect(verifySpy).toHaveBeenCalled()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeDefined()
  })

  // 关闭验证码(204)时不渲染 widget、按钮直接可用。
  it('204 时按钮直接可用且无 widget', async () => {
    stubChallenge(204)
    const wrapper = mountForm()
    await flushPromises()
    expect(wrapper.find('altcha-widget').exists()).toBe(false)
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/login/variants/default/LoginForm.spec.ts`
Expected: FAIL —— 找不到模块 `./LoginForm`。

- [ ] **Step 3: 创建 `useLogin.ts`**

```ts
// useLogin.ts — 登录页共享行为 composable。
// 抽取自原 LoginPage.vue：承载本地账号登录的全部交互状态、验证码探测/交互与提交逻辑，
// 供所有登录变体（默认变体与各白标变体）复用，保证认证行为只有一份实现，不因各变体
// 重复编写而漂移。
//
// 变体作者契约：任何登录变体的模板都必须——
//   1. 表单 submit 绑定 onSubmit；输入框 v-model 绑定 orgCode/username/password；
//   2. captchaActive 为真时渲染 altcha 挂载点，ref 绑 captchaRef、@statechange 绑 onCaptchaState；
//   3. submit 按钮 disabled 绑定 auth.loading || (captchaActive && !captchaVerified)；
//   4. 展示 errorMessage。
// 缺任一接线点会导致验证码或登录失效。
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'

// useLogin 返回登录页所需的响应式状态与交互方法；auth 直接透出以便模板绑定 auth.loading。
export function useLogin() {
  const auth = useAuthStore()
  const router = useRouter()
  const { t } = useI18n()

  const orgCode = ref('')
  const username = ref('')
  const password = ref('')
  // showPassword 控制密码框明文显示，仅前端交互不影响提交逻辑。
  const showPassword = ref(false)
  // errorMessage 只保存本次登录失败原因，下一次提交前会清空。
  const errorMessage = ref<string | null>(null)

  // captchaActive：是否启用验证码（挂载时探测出题接口决定）；初值 true 以默认禁用按钮（安全侧）。
  const captchaActive = ref(true)
  // captchaVerified：widget 是否已算出有效解。
  const captchaVerified = ref(false)
  // captchaPayload：已验证的 Altcha payload，提交时带上。
  const captchaPayload = ref('')
  // captchaRef：widget 元素引用，失败后 reset()+verify() 触发重新出题和重算。
  const captchaRef = ref<
    (HTMLElement & { reset?: () => void; verify?: () => Promise<unknown> }) | null
  >(null)

  // 挂载时探测出题接口：204 表示后端关闭验证码 → 不渲染 widget、不挡按钮；
  // 其它（200 或网络错误）按开启处理。
  onMounted(async () => {
    try {
      const res = await fetch('/api/v1/auth/altcha-challenge')
      captchaActive.value = res.status !== 204
    } catch {
      captchaActive.value = true
    }
  })

  // onCaptchaState 监听 widget 状态：verified 时存 payload 并放行按钮，其它状态清空。
  function onCaptchaState(e: Event) {
    const detail = (e as CustomEvent).detail as { state?: string; payload?: string } | undefined
    if (detail?.state === 'verified' && detail.payload) {
      captchaPayload.value = detail.payload
      captchaVerified.value = true
    } else {
      captchaVerified.value = false
      captchaPayload.value = ''
    }
  }

  // onSubmit 调用 auth store 登录；redirect 查询参数由全局 401 处理器写入，缺省回根路径。
  async function onSubmit() {
    errorMessage.value = null
    try {
      await auth.login(
        username.value,
        password.value,
        orgCode.value,
        captchaActive.value ? captchaPayload.value : undefined,
      )
      const target = (router.currentRoute.value.query.redirect as string | undefined) ?? '/'
      await router.replace(target)
    } catch (err) {
      // 后端错误信息优先展示；无具体信息时使用本地化兜底文案。
      errorMessage.value = err instanceof Error ? err.message : t('login.loginFailed')
      // payload 一次性：本次已消费，重置 widget 触发重新出题+重算，让用户可再试。
      if (captchaActive.value) {
        captchaVerified.value = false
        captchaPayload.value = ''
        captchaRef.value?.reset?.()
        // Altcha auto=onload 只在加载时触发；失败后必须显式 verify 才会重新出题。
        void captchaRef.value?.verify?.()
      }
    }
  }

  return {
    auth,
    orgCode,
    username,
    password,
    showPassword,
    errorMessage,
    captchaActive,
    captchaVerified,
    captchaPayload,
    captchaRef,
    onCaptchaState,
    onSubmit,
  }
}
```

- [ ] **Step 4: 创建 `variants/default/LoginForm.vue`**

模板与样式**逐字复制**现有 `web/src/pages/login/LoginPage.vue` 的 `<template>` 与 `<style scoped>` 段（不做任何改动），仅把 `<script setup>` 整段替换为下面这段（改用 useLogin，删除原本地 ref 与 onSubmit 定义）：

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import { useLogin } from '../../useLogin'

// LoginForm 是默认变体的登录卡片：只负责表单标记，登录行为全部来自 useLogin()。
// t 供模板渲染标签/占位文案；登录状态与提交逻辑由 composable 提供。
const { t } = useI18n()
const {
  auth,
  orgCode,
  username,
  password,
  showPassword,
  errorMessage,
  captchaActive,
  captchaVerified,
  captchaRef,
  onCaptchaState,
  onSubmit,
} = useLogin()
</script>
```

说明：`<template>`/`<style>` 与原 `LoginPage.vue` 完全一致——所有模板引用（`orgCode`/`username`/`password`/`showPassword`/`errorMessage`/`captchaActive`/`captchaVerified`/`captchaRef`/`onCaptchaState`/`auth.loading`/`onSubmit`/`t`）均由上面的 setup 返回，无需改模板。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/login/variants/default/LoginForm.spec.ts`
Expected: PASS —— 4 个用例全绿。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/login/useLogin.ts web/src/pages/login/variants/default/LoginForm.vue web/src/pages/login/variants/default/LoginForm.spec.ts
git commit -m "$(cat <<'EOF'
refactor(web): 抽取 useLogin composable 与默认登录表单 LoginForm

把原 LoginPage.vue 的登录状态、验证码探测/交互与 onSubmit+redirect
逻辑抽取为共享 useLogin() composable，默认表单 LoginForm.vue 仅保留
标记并接线 composable，为按域名白标登录页的多变体复用同一份认证行为
打基础。行为经 LoginForm.spec 覆盖并新增成功 redirect 断言。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 默认变体整页 DefaultLoginPage

把 `AuthLayout.vue` 的背景/hero 迁移为默认变体整页，内嵌 `LoginForm`。

**Files:**
- Create: `web/src/pages/login/variants/default/DefaultLoginPage.vue`
- Test: `web/src/pages/login/variants/default/DefaultLoginPage.spec.ts`

- [ ] **Step 1: 写失败测试 `DefaultLoginPage.spec.ts`**

源自 `web/src/layouts/AuthLayout.spec.ts`，把子内容桩由 `RouterView` 改为 `LoginForm`：

```ts
// DefaultLoginPage.spec.ts — 默认变体整页布局单测。
// 验证背景装饰层与登录内容层分离，且登录表单挂在内容层的 login-shell 内。
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import DefaultLoginPage from './DefaultLoginPage.vue'

// mountPage：挂载默认整页，注入 i18n（hero 文案用 useI18n）。
// LoginForm 依赖 useLogin（fetch 探测 / store / router），此处用占位桩替代，
// 让布局测试聚焦于结构而非登录行为（登录行为由 LoginForm.spec 覆盖）。
function mountPage() {
  return mount(DefaultLoginPage, {
    global: {
      plugins: [i18n],
      stubs: {
        LoginForm: { template: '<form class="login-card">登录表单</form>' },
      },
    },
  })
}

describe('DefaultLoginPage', () => {
  beforeEach(() => {
    // 画布 2D 上下文取不到时动画早退，避免 jsdom 无 canvas 实现报错。
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // 背景装饰层与登录内容层分离，登录卡片落在内容层的 login-shell 内。
  it('内容层承载 hero 与登录表单，背景层独立', () => {
    const wrapper = mountPage()
    const stage = wrapper.get('.auth-stage')
    const content = wrapper.find('.auth-content')

    expect(content.exists()).toBe(true)
    expect(content.find('.auth-hero').exists()).toBe(true)
    expect(content.find('.auth-login-shell').exists()).toBe(true)
    expect(content.find('.login-card').exists()).toBe(true)
    expect(content.find('.auth-neural').exists()).toBe(false)

    const directChildClasses = Array.from(stage.element.children).map((child) =>
      Array.from((child as HTMLElement).classList),
    )
    expect(directChildClasses).toEqual([
      ['auth-neural'],
      ['auth-aurora'],
      ['auth-grid'],
      ['auth-scan'],
      ['auth-content'],
    ])
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/login/variants/default/DefaultLoginPage.spec.ts`
Expected: FAIL —— 找不到模块 `./DefaultLoginPage`。

- [ ] **Step 3: 创建 `DefaultLoginPage.vue`**

**逐字复制**现有 `web/src/layouts/AuthLayout.vue` 全文到该新文件，然后仅做两处改动：

1. 在 `<script setup>` 顶部 import 区加一行：

```ts
import LoginForm from './LoginForm.vue'
```

2. 把模板里的登录内容出口 `<RouterView />` 替换为 `<LoginForm />`：

```vue
      <section class="auth-login-shell" :aria-label="t('layout.auth.loginLabel')">
        <LoginForm />
      </section>
```

其余模板、script（粒子动画 onMounted/onBeforeUnmount）、style 全部保持与 `AuthLayout.vue` 一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/login/variants/default/DefaultLoginPage.spec.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/login/variants/default/DefaultLoginPage.vue web/src/pages/login/variants/default/DefaultLoginPage.spec.ts
git commit -m "$(cat <<'EOF'
refactor(web): 迁移 AuthLayout 为默认变体整页 DefaultLoginPage

把 AuthLayout 的粒子背景与 hero 介绍区迁移为默认登录变体整页，
登录卡片出口由 RouterView 改为内嵌 LoginForm，视觉与交互保持不变，
作为按域名白标登录页机制的默认（回归基线）变体。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: 变体注册表 registry

hostname → 变体精确映射、默认兜底、title/favicon 副作用纯函数。

**Files:**
- Create: `web/src/pages/login/variants/registry.ts`
- Test: `web/src/pages/login/variants/registry.spec.ts`

- [ ] **Step 1: 写失败测试 `registry.spec.ts`**

```ts
// registry.spec.ts — 登录变体注册表单测。
// 覆盖：精确 hostname 命中、未命中回默认、title/favicon 副作用应用与缺省不改动。
import type { Component } from 'vue'
import { beforeEach, describe, expect, it } from 'vitest'

import { DEFAULT_VARIANT, applyVariantChrome, resolveVariant } from './registry'

describe('resolveVariant', () => {
  // 精确 hostname 命中返回对应变体。
  it('精确 hostname 命中', () => {
    const fake = { component: {} as Component, documentTitle: 'ABC' }
    const map = { 'abc.example.com': fake }
    expect(resolveVariant('abc.example.com', map)).toBe(fake)
  })

  // 未命中任何 hostname 时回默认变体。
  it('未命中回默认', () => {
    expect(resolveVariant('nope.example.com', {})).toBe(DEFAULT_VARIANT)
  })
})

describe('applyVariantChrome', () => {
  beforeEach(() => {
    // 每例前清理可能残留的图标链接，避免相互污染。
    document.querySelectorAll('link[rel="icon"]').forEach((n) => n.remove())
  })

  // 有覆盖项时设置 document.title 并写入 favicon 链接。
  it('设置标题与 favicon', () => {
    applyVariantChrome({
      component: {} as Component,
      documentTitle: 'ACME',
      faviconHref: '/acme.ico',
    })
    expect(document.title).toBe('ACME')
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link).not.toBeNull()
    expect(link!.getAttribute('href')).toBe('/acme.ico')
  })

  // 无覆盖项时不改动现有标题。
  it('无覆盖项时不改动标题', () => {
    document.title = 'KEEP'
    applyVariantChrome({ component: {} as Component })
    expect(document.title).toBe('KEEP')
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/login/variants/registry.spec.ts`
Expected: FAIL —— 找不到模块 `./registry`。

- [ ] **Step 3: 创建 `registry.ts`**

```ts
// registry.ts — 登录页按域名选变体的注册表与 chrome 副作用。
// 机制：LoginHost 用 window.location.hostname 经 resolveVariant 精确匹配到变体组件；
// 未命中回默认变体。域名不绑组织，仅决定登录页外观；新增白标在 VARIANTS 登记一行。
import type { Component } from 'vue'

import DefaultLoginPage from './default/DefaultLoginPage.vue'

// LoginVariant 描述一个域名对应的登录页变体：整页组件 + 可选的页面标题/favicon 覆盖。
export type LoginVariant = {
  // component：整页登录组件（布局自由，须按 useLogin 契约接线登录行为）。
  component: Component
  // documentTitle：可选，覆盖浏览器标签标题；缺省不改动。
  documentTitle?: string
  // faviconHref：可选，覆盖站点 favicon；缺省不改动。
  faviconHref?: string
}

// DEFAULT_VARIANT：默认变体，ai.ywjs.com / ocm.localhost / 任何未命中 hostname 都用它。
export const DEFAULT_VARIANT: LoginVariant = { component: DefaultLoginPage }

// VARIANTS：精确 hostname → 变体映射。首期为空，新增白标域名在此登记一行。
export const VARIANTS: Record<string, LoginVariant> = {}

// resolveVariant 按精确 hostname 命中变体，未命中回默认。
// variants 参数默认取全局 VARIANTS，仅为便于单测注入 fixture。
export function resolveVariant(
  hostname: string,
  variants: Record<string, LoginVariant> = VARIANTS,
): LoginVariant {
  return variants[hostname] ?? DEFAULT_VARIANT
}

// applyVariantChrome 应用变体的标题/favicon 副作用；无覆盖项则不改动。
// doc 参数默认取全局 document，仅为便于单测注入。
export function applyVariantChrome(variant: LoginVariant, doc: Document = document): void {
  if (variant.documentTitle) {
    doc.title = variant.documentTitle
  }
  if (variant.faviconHref) {
    // 复用已有的 rel=icon 链接，缺失时创建，避免重复插入多个图标链接。
    let link = doc.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!link) {
      link = doc.createElement('link')
      link.rel = 'icon'
      doc.head.appendChild(link)
    }
    link.setAttribute('href', variant.faviconHref)
  }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/login/variants/registry.spec.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/login/variants/registry.ts web/src/pages/login/variants/registry.spec.ts
git commit -m "$(cat <<'EOF'
feat(web): 增加登录变体注册表 registry

新增 hostname → 登录变体的精确映射注册表、默认变体兜底 resolveVariant，
以及应用 title/favicon 的 applyVariantChrome 纯函数（便于单测注入）。
首期 VARIANTS 为空，新增白标域名在此登记一行。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 登录路由入口 LoginHost

按 hostname 选变体渲染并应用 chrome。

**Files:**
- Create: `web/src/pages/login/LoginHost.vue`
- Test: `web/src/pages/login/LoginHost.spec.ts`

- [ ] **Step 1: 写失败测试 `LoginHost.spec.ts`**

mock `./variants/registry`，避免拉入真实默认变体的重依赖，聚焦验证 LoginHost 的选变体渲染 + chrome 应用这条粘合逻辑：

```ts
// LoginHost.spec.ts — 登录路由入口单测。
// 验证：按 resolveVariant 选定的变体组件被渲染，且挂载时应用了 chrome（标题）。
// registry 被 mock，隔离真实默认变体的 store/router/fetch 依赖，聚焦粘合逻辑。
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

// 用带标记的桩变体替换真实注册表；resolveVariant 恒返回该桩。
const fakeVariant = {
  component: { template: '<div class="fake-variant" />' },
  documentTitle: 'FAKE-TITLE',
}
vi.mock('./variants/registry', () => ({
  resolveVariant: vi.fn(() => fakeVariant),
  applyVariantChrome: (v: { documentTitle?: string }, doc: Document = document) => {
    if (v.documentTitle) doc.title = v.documentTitle
  },
}))

import LoginHost from './LoginHost.vue'

describe('LoginHost', () => {
  // 渲染 resolveVariant 选定的变体组件，并在挂载时应用 chrome（覆盖 document.title）。
  it('渲染选定变体并应用 chrome', () => {
    const wrapper = mount(LoginHost)
    expect(wrapper.find('.fake-variant').exists()).toBe(true)
    expect(document.title).toBe('FAKE-TITLE')
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/login/LoginHost.spec.ts`
Expected: FAIL —— 找不到模块 `./LoginHost`。

- [ ] **Step 3: 创建 `LoginHost.vue`**

```vue
<template>
  <!-- LoginHost 是 /login 路由入口：按当前 hostname 选定的白标变体整页渲染。 -->
  <component :is="variant.component" />
</template>

<script setup lang="ts">
import { onMounted } from 'vue'

import { applyVariantChrome, resolveVariant } from './variants/registry'

// 登录页生命周期内 hostname 不变，故只在 setup 解析一次；未命中回默认变体。
const variant = resolveVariant(window.location.hostname)

// 标题/favicon 属副作用，放 onMounted 应用，避免 SSR/首屏解析期直接触碰 document。
onMounted(() => {
  applyVariantChrome(variant)
})
</script>
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/login/LoginHost.spec.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/login/LoginHost.vue web/src/pages/login/LoginHost.spec.ts
git commit -m "$(cat <<'EOF'
feat(web): 增加登录路由入口 LoginHost

新增 /login 入口组件 LoginHost：按 window.location.hostname 经注册表
选定白标变体整页渲染，并在挂载时应用变体的 title/favicon 副作用。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 接线路由、删除旧文件、全量验证

`/login` 改挂 `LoginHost`，删除已迁移的 `AuthLayout`/`LoginPage`，跑全量单测 + 类型检查 + 构建。

**Files:**
- Modify: `web/src/app/router.ts:52-56`
- Delete: `web/src/layouts/AuthLayout.vue`, `web/src/layouts/AuthLayout.spec.ts`
- Delete: `web/src/pages/login/LoginPage.vue`, `web/src/pages/login/LoginPage.spec.ts`

- [ ] **Step 1: 改路由 `web/src/app/router.ts`**

把顶部 import：

```ts
import AuthLayout from '@/layouts/AuthLayout.vue'
```
```ts
import LoginPage from '@/pages/login/LoginPage.vue'
```

替换为（删除这两行，新增一行 LoginHost import；放在原 LoginPage import 位置附近，保持既有分组）：

```ts
import LoginHost from '@/pages/login/LoginHost.vue'
```

把 `/login` 路由块：

```ts
    {
      path: '/login',
      component: AuthLayout,
      meta: { public: true },
      children: [{ path: '', component: LoginPage }],
    },
```

替换为：

```ts
    {
      path: '/login',
      component: LoginHost,
      meta: { public: true },
    },
```

- [ ] **Step 2: 删除已迁移的旧文件**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git rm web/src/layouts/AuthLayout.vue web/src/layouts/AuthLayout.spec.ts \
       web/src/pages/login/LoginPage.vue web/src/pages/login/LoginPage.spec.ts
```

- [ ] **Step 3: 确认无残留引用**

Run: `cd web && grep -rn "AuthLayout\|pages/login/LoginPage" src || echo "NO-REF"`
Expected: 仅输出 `NO-REF`（无任何残留引用）。

- [ ] **Step 4: 全量单测**

Run: `make web-test`
Expected: PASS —— 全部测试通过，无 `AuthLayout.spec` / `LoginPage.spec` 遗留失败。

- [ ] **Step 5: 类型检查**

Run: `make web-typecheck`
Expected: PASS —— `vue-tsc --noEmit` 无报错。

- [ ] **Step 6: 生产构建**

Run: `make web-build`
Expected: PASS —— `vite build` 成功。

- [ ] **Step 7: 提交**

```bash
git add web/src/app/router.ts
git commit -m "$(cat <<'EOF'
refactor(web): /login 改挂 LoginHost 并删除旧 AuthLayout/LoginPage

登录路由由 AuthLayout + 子 LoginPage 改为直接挂 LoginHost，由后者按
域名选白标变体渲染；原 AuthLayout.vue/LoginPage.vue 内容已迁移至默认
变体，连同其单测一并删除。全量单测、类型检查、构建通过。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## 交付验证（真实浏览器，按 CLAUDE.md）

单测/构建通过后，用真实浏览器验证机制（curl 不能替代）：

1. **默认域名回归**：本地经 k3d ingress 访问 `http://ocm.localhost/login`，确认页面与改动前完全一致（粒子背景 + hero + 登录卡片），并用本地账号 `admin` / 组织标识留空 / `admin123` 登录成功进入后台。
2. **白标命中验证**：临时在 `variants/registry.ts` 的 `VARIANTS` 登记一个测试 hostname（指向一个临时最小变体或复用默认变体但设 `documentTitle`），通过该 hostname 访问 `/login`，确认命中该变体（如标签标题变为设定值），且同样能登录成功。
3. 验证通过后**移除临时登记与临时变体**（首期不落示例变体），保持交付物只含机制 + 默认变体。

---

## Self-Review

- **Spec 覆盖**：
  - 「useLogin composable 抽取全部行为」→ Task 1（`useLogin.ts` + `LoginForm.spec` 覆盖成功 redirect/失败重置/验证码开关/payload 透传）。
  - 「registry 精确映射 + 默认兜底」→ Task 3（`resolveVariant` 命中/未命中）。
  - 「LoginHost 选变体 + title/favicon 副作用」→ Task 4（`LoginHost` 渲染变体 + `applyVariantChrome`，Task 3 测副作用）。
  - 「默认变体合并 AuthLayout + LoginPage、视觉不变、作为回归基线」→ Task 1（LoginForm）+ Task 2（DefaultLoginPage），结构断言迁移自原 AuthLayout.spec。
  - 「路由改挂、删除旧文件、迁移 spec」→ Task 5。
  - 「变体作者契约」→ 写入 `useLogin.ts` 文件注释（Task 1 Step 3）。
  - 「后端零改动、域名不绑组织」→ 全程无后端/auth 改动，登录仍手填组织标识。
  - 「首期只搭机制 + 默认变体」→ `VARIANTS` 为空；交付验证中临时变体用完即删。
- **占位符扫描**：无 TBD/TODO；每个代码步骤均含完整代码或对现有文件的精确复制+改动指令。
- **类型一致性**：`LoginVariant`/`DEFAULT_VARIANT`/`VARIANTS`/`resolveVariant`/`applyVariantChrome` 在 Task 3 定义，Task 4 按同名同签名使用；`useLogin()` 返回字段与 `LoginForm.vue` setup 解构、模板引用逐一对应。
