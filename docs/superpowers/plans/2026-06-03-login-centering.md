# Login Centering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Center the homepage login layout as one responsive content group while preserving the existing hero, animated background, and login behavior.

**Architecture:** `AuthLayout.vue` keeps ownership of the full-screen auth stage and decorative background layers. A new `.auth-content` wrapper owns the hero/login grid, maximum content width, horizontal centering, responsive single-column behavior, and small-height scrolling. A focused component test locks the wrapper structure so layout responsibility stays separated from background decoration.

**Tech Stack:** Vue 3 SFC, Vue Router `RouterView`, scoped CSS, Vitest, Vue Test Utils, Vite, Chrome browser verification.

---

## Scope Check

The approved spec covers one front-end layout change in the login/auth surface. It does not touch backend code, OpenAPI, authentication state, login form behavior, or generated files, so this can be implemented as one small plan.

## File Structure

- `web/src/layouts/AuthLayout.vue`
  - Modify the template to add `.auth-content` around `.auth-hero` and `.auth-login-shell`.
  - Move grid column, gap, content width, and responsive layout responsibility from `.auth-stage` into `.auth-content`.
  - Keep background canvas, aurora, grid, scan layers, hero copy, metrics, and `RouterView` unchanged.

- `web/src/layouts/AuthLayout.spec.ts`
  - Create a focused structure test for the new `.auth-content` wrapper.
  - Stub `RouterView` with a simple login form placeholder.
  - Mock canvas `getContext` to avoid jsdom canvas implementation noise.

---

### Task 1: Add AuthLayout Structure Test

**Files:**
- Create: `web/src/layouts/AuthLayout.spec.ts`

- [ ] **Step 1: Write the failing structure test**

Create `web/src/layouts/AuthLayout.spec.ts` with this content:

```ts
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AuthLayout from './AuthLayout.vue'

function mountLayout() {
  return mount(AuthLayout, {
    global: {
      stubs: {
        RouterView: { template: '<form class="login-card">登录表单</form>' },
      },
    },
  })
}

describe('AuthLayout', () => {
  beforeEach(() => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // 该用例验证背景装饰层与登录内容层分离，避免后续居中布局再次回到 auth-stage 上耦合。
  it('uses a centered content wrapper for hero and login shell', () => {
    const wrapper = mountLayout()
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

- [ ] **Step 2: Run the new test and verify it fails for the expected reason**

Run:

```bash
cd web && npm run test -- src/layouts/AuthLayout.spec.ts --run
```

Expected: FAIL. The failure should be on `expect(content.exists()).toBe(true)` because the current `AuthLayout.vue` has no `.auth-content` wrapper.

---

### Task 2: Add Explicit Auth Content Layout

**Files:**
- Modify: `web/src/layouts/AuthLayout.vue`
- Test: `web/src/layouts/AuthLayout.spec.ts`

- [ ] **Step 1: Replace the template with the explicit content wrapper**

Replace the full `<template>` block in `web/src/layouts/AuthLayout.vue` with:

```vue
<template>
  <!-- AuthLayout 承载登录相关页面：背景层铺满视口，内容层负责 hero 与登录卡片整体居中。 -->
  <main class="auth-stage">
    <!-- 背景层：神经网络粒子画布 + 极光 + 网格 + 扫描光带，全部为纯装饰，不参与交互。 -->
    <canvas ref="neural" class="auth-neural" aria-hidden="true"></canvas>
    <div class="auth-aurora" aria-hidden="true"></div>
    <div class="auth-grid" aria-hidden="true"></div>
    <div class="auth-scan" aria-hidden="true"></div>

    <!-- 内容层：把平台介绍和登录卡片作为一个整体居中，避免大屏下左右分散。 -->
    <div class="auth-content">
      <section class="auth-hero" aria-label="平台介绍">
        <div class="auth-hero-copy">
          <div class="auth-eyebrow">ENTERPRISE AI AGENT PLATFORM</div>
          <h1 class="auth-title">让<span class="auth-title-hot">智能体</span>融入企业工作流</h1>
          <p class="auth-lead">
            企业 AI 数智员工运行管理平台，用 Agent 连通云资源、企业知识库和多模型能力，深度接管员工日常工作任务。
          </p>
        </div>
        <div class="auth-metrics" aria-label="平台能力">
          <div class="auth-metric">
            <strong>一人一 Agent</strong>
            <span>配置独立运行环境、专属任务管理及独享个人知识库</span>
          </div>
          <div class="auth-metric">
            <strong>统一管控</strong>
            <span>实现账号权限、知识库、大模型与 Token 消耗的统一管控</span>
          </div>
          <div class="auth-metric">
            <strong>可定制化</strong>
            <span>支持定制需求，可私有化部署，完全适配企业安全规范</span>
          </div>
        </div>
      </section>

      <section class="auth-login-shell" aria-label="登录控制台">
        <RouterView />
      </section>
    </div>
  </main>
</template>
```

- [ ] **Step 2: Replace `.auth-stage` and add `.auth-content`**

In the `<style scoped>` block, replace the existing `.auth-stage` rule with these two rules:

```css
.auth-stage {
  /* 局部科技色板，通过 CSS 自定义属性向登录卡片（子组件）继承。 */
  --auth-cyan: #20d7ff;
  --auth-violet: #8a5cff;
  --auth-lime: #72ffb6;
  --auth-orange: #ff7a1a;

  position: relative;
  min-height: 100vh;
  min-height: 100dvh;
  display: grid;
  align-items: center;
  justify-items: center;
  padding: clamp(38px, 6vh, 56px) clamp(22px, 6vw, 92px);
  isolation: isolate;
  overflow-x: hidden;
  overflow-y: auto;
  color: #f8fbff;
}

.auth-content {
  width: min(100%, 1280px);
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(428px, 488px);
  align-items: center;
  gap: clamp(28px, 4vw, 56px);
}
```

- [ ] **Step 3: Update hero and login shell alignment rules**

Replace the existing `.auth-hero` rule with:

```css
.auth-hero {
  width: 100%;
  max-width: 760px;
  min-height: 576px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}
```

Replace the existing `.auth-login-shell` rule with:

```css
.auth-login-shell {
  position: relative;
  width: min(100%, 428px);
  justify-self: center;
}
```

- [ ] **Step 4: Replace responsive rules**

Replace the existing `@media (max-width: 980px)` block with:

```css
@media (max-width: 980px) {
  .auth-stage {
    align-items: start;
    padding: 38px 22px;
  }

  .auth-content {
    grid-template-columns: 1fr;
    justify-items: center;
    gap: 32px;
  }

  .auth-hero {
    max-width: none;
    min-height: 0;
    display: block;
  }

  .auth-login-shell {
    width: 100%;
    max-width: 520px;
  }

  .auth-metrics {
    grid-template-columns: 1fr;
    margin-top: 32px;
  }
}
```

Replace the existing `@media (max-width: 560px)` block with:

```css
@media (max-width: 560px) {
  .auth-stage {
    padding: 32px 20px;
  }

  .auth-content {
    gap: 32px;
  }

  .auth-login-shell::before {
    inset: -12px;
  }

  .auth-lead {
    font-size: 16px;
  }
}
```

Add this block after the `@media (max-width: 560px)` block:

```css
@media (max-height: 720px) and (min-width: 981px) {
  .auth-stage {
    align-items: start;
  }
}
```

- [ ] **Step 5: Run the AuthLayout test and verify it passes**

Run:

```bash
cd web && npm run test -- src/layouts/AuthLayout.spec.ts --run
```

Expected: PASS. The `.auth-content` wrapper exists, contains hero/login content, and does not contain background decoration layers.

- [ ] **Step 6: Run frontend typecheck**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS with no Vue or TypeScript errors.

---

### Task 3: Browser Verification and Commit

**Files:**
- Verify: `web/src/layouts/AuthLayout.vue`
- Verify: `web/src/layouts/AuthLayout.spec.ts`

- [ ] **Step 1: Start the local frontend dev server**

Run:

```bash
cd web && npm run dev -- --host 0.0.0.0
```

Expected: Vite prints a local URL. Use the printed URL and open `/login`.

- [ ] **Step 2: Verify desktop centered layout in a real browser**

Use Chrome DevTools or Playwright browser controls to check these viewports on `/login`:

```text
1440x900
1366x768
1024x768
```

Expected:

- The hero and login shell appear as one centered group.
- The group does not hug the left or right viewport edge.
- The login shell is visually centered inside the right column.
- The background animation and decorative layers still cover the viewport.

- [ ] **Step 3: Verify narrow and small-height behavior in a real browser**

Use Chrome DevTools or Playwright browser controls to check these viewports on `/login`:

```text
390x844
360x740
1440x640
```

Expected:

- At mobile widths, hero and login shell stack in one column and remain horizontally centered.
- The login card, inputs, password eye button, and submit button are fully visible and usable.
- At small height, the page scrolls vertically instead of cutting off the form.
- No horizontal scrollbar appears.

- [ ] **Step 4: Stop the dev server**

Stop the Vite process with `Ctrl+C`.

Expected: the dev server process exits.

- [ ] **Step 5: Review the final diff**

Run:

```bash
git diff -- web/src/layouts/AuthLayout.vue web/src/layouts/AuthLayout.spec.ts
```

Expected: the diff only contains the `.auth-content` structural wrapper, scoped layout CSS changes, and the new structure test.

- [ ] **Step 6: Commit the implementation**

Run:

```bash
git add web/src/layouts/AuthLayout.vue web/src/layouts/AuthLayout.spec.ts
git commit -m "fix(auth): 优化登录页居中适配" -m "新增 AuthLayout 内容容器，将登录页 hero 与登录卡片作为整体居中展示。" -m "补充结构测试，并通过类型检查和真实浏览器验证响应式表现。"
```

Expected: one implementation commit on `master`. Do not add unrelated files such as the pre-existing untracked `tasks/` directory.
