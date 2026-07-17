# Remove Dashboard Environment Label Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从后台顶栏完整移除固定的环境提示和内部角色代码，同时保持其余顶栏功能不变。

**Architecture:** 直接收缩现有 `DashboardLayout` 的展示职责：模板不再渲染环境标识，脚本不再组装该文案，语言包删除对应死文案。使用现有 Vue Test Utils 布局测试覆盖最终 DOM，不改变认证、权限或路由数据流。

**Tech Stack:** Vue 3、TypeScript、vue-i18n、Vitest、Vue Test Utils、Playwright（浏览器验证）

---

### Task 1: 移除顶栏环境与角色标识

**Files:**
- Modify: `web/src/layouts/DashboardLayout.spec.ts:254`
- Modify: `web/src/layouts/DashboardLayout.vue:77-80,206-210`
- Modify: `web/src/i18n/locales/zh/layout.ts:68-71`
- Modify: `web/src/i18n/locales/en/layout.ts:68-71`

- [ ] **Step 1: 编写失败的布局回归测试**

在 `DashboardLayout.spec.ts` 的 `describe('DashboardLayout')` 中、整体骨架测试之前加入：

```ts
  // 覆盖后台顶栏产品文案：任何角色都不再展示调试环境提示或内部角色代码。
  it('does not render the environment or internal role label', () => {
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgMember = true

    const wrapper = mountLayout()

    expect(wrapper.find('.eyebrow').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('本地调试环境')
    expect(wrapper.text()).not.toContain('org_member')
    expect(wrapper.text()).toContain('控制台')
  })
```

- [ ] **Step 2: 运行测试并确认因旧标识仍存在而失败**

Run: `cd web && npm test -- --run src/layouts/DashboardLayout.spec.ts`

Expected: FAIL；`.eyebrow` 实际存在，且页面文本仍包含“本地调试环境 · org_member”。

- [ ] **Step 3: 删除模板节点和无用计算属性**

将 `DashboardLayout.vue` 顶栏左侧模板改为：

```vue
        <div>
          <h1 style="margin: 0; font-size: 20px">{{ t('layout.header.console') }}</h1>
        </div>
```

并完整删除以下计算属性：

```ts
// environmentLabel 根据是否登录以及当前语言返回环境标识文案，响应语言切换。
const environmentLabel = computed(() => {
  if (!auth.user) return t('layout.header.envLabel')
  return t('layout.header.envLabelWithRole', { role: auth.user.role })
})
```

- [ ] **Step 4: 删除中英文死文案**

从 `web/src/i18n/locales/zh/layout.ts` 的 `header` 中删除：

```ts
    // envLabel：未登录或无用户时的环境标签
    envLabel: '本地调试环境',
    // envLabelWithRole：已登录时拼接角色的环境标签（{role} 为插值占位符）
    envLabelWithRole: '本地调试环境 · {role}',
```

从 `web/src/i18n/locales/en/layout.ts` 的 `header` 中删除：

```ts
    // envLabel：未登录或无用户时的环境标签
    envLabel: 'Local Dev Environment',
    // envLabelWithRole：已登录时拼接角色的环境标签（{role} 为插值占位符）
    envLabelWithRole: 'Local Dev Environment · {role}',
```

- [ ] **Step 5: 运行定向单元测试并确认通过**

Run: `cd web && npm test -- --run src/layouts/DashboardLayout.spec.ts`

Expected: PASS；新增回归用例及该文件原有用例全部通过。

- [ ] **Step 6: 运行前端类型检查**

Run: `cd web && npm run typecheck`

Expected: PASS，无 TypeScript 或 Vue 模板类型错误。

- [ ] **Step 7: 使用本地真实浏览器无头模式定向验证**

以无头（headless）模式访问 `http://ocm.localhost`，使用本地账号 `admin` / `admin123` 登录（组织标识留空）。确认顶栏仅保留“控制台”，不再出现“本地调试环境”或角色代码；语言切换、API 状态、使用手册和刷新入口仍可见。只运行覆盖该顶栏改动的定向浏览器场景，不执行全量 E2E。

- [ ] **Step 8: 检查差异并提交实现**

Run: `git diff --check && git status --short`

Expected: 仅包含上述四个实现/测试文件和本计划文件的预期改动；用户已有的 `a.sh` 保持未跟踪且不纳入提交。

```bash
git add web/src/layouts/DashboardLayout.spec.ts web/src/layouts/DashboardLayout.vue web/src/i18n/locales/zh/layout.ts web/src/i18n/locales/en/layout.ts docs/superpowers/plans/2026-07-17-remove-dashboard-environment-label.md
git commit -m "fix(web): 移除后台环境与角色标识" -m "删除顶栏固定的本地调试环境提示和内部角色代码，避免线上页面展示误导性文案。\n\n同步清理无用计算属性及中英文语言项，并补充布局回归测试。"
```
