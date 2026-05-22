# org_member 跳过实例列表直达详情页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** org_member 点击侧边栏"实例"时直接进入其唯一实例的详情页，无实例时显示空状态提示页。

**Architecture:** 新增 `useMemberApp` composable 查询 org_member 的唯一实例 ID；侧边栏和路由守卫根据该状态动态导航；新增 `AppEmptyPage` 处理无实例场景。

**Tech Stack:** Vue 3, TanStack Query, Naive UI, vue-router, Vitest

---

## File Structure

| 文件 | 职责 |
|------|------|
| `web/src/composables/useMemberApp.ts` | 新建。查询 org_member 唯一实例 ID |
| `web/src/composables/__tests__/useMemberApp.spec.ts` | 新建。composable 单元测试 |
| `web/src/pages/apps/AppEmptyPage.vue` | 新建。无实例空状态页 |
| `web/src/app/router.ts` | 修改。新增 `/apps/empty` 路由 + `/apps` beforeEnter 守卫 |
| `web/src/layouts/DashboardLayout.vue` | 修改。侧边栏菜单项动态 key + activeKey 适配 |
| `web/src/pages/dashboard/RoleAwareHome.vue` | 修改。org_member "我的实例"卡片路径动态化 |

---

### Task 1: 创建 `useMemberApp` composable

**Files:**
- Create: `web/src/composables/useMemberApp.ts`
- Create: `web/src/composables/__tests__/useMemberApp.spec.ts`

- [ ] **Step 1: 编写 composable 测试**

```ts
// web/src/composables/__tests__/useMemberApp.spec.ts
import { computed, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import { useMemberApp } from '../useMemberApp'

// org_member 有一个活跃实例时，composable 应返回该实例 ID
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 'user-1', org_id: 'org-1', role: 'org_member' },
    isOrgMember: true,
  }),
}))

vi.mock('@/api/hooks/useApps', () => ({
  useAppsByOrgQuery: () => ({
    data: ref([
      { id: 'app-1', org_id: 'org-1', owner_user_id: 'user-1', name: '我的实例', status: 'running' },
    ]),
    isLoading: ref(false),
  }),
}))

describe('useMemberApp', () => {
  // org_member 有实例时应返回 appId
  it('返回 org_member 拥有的实例 ID', () => {
    const { appId, hasApp, isLoading } = useMemberApp()
    expect(appId.value).toBe('app-1')
    expect(hasApp.value).toBe(true)
    expect(isLoading.value).toBe(false)
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/composables/__tests__/useMemberApp.spec.ts`
Expected: FAIL — `useMemberApp` 模块不存在

- [ ] **Step 3: 实现 composable**

```ts
// web/src/composables/useMemberApp.ts
// useMemberApp 为 org_member 提供其唯一活跃实例的 ID。
// 侧边栏和路由守卫依赖此 composable 决定导航目标。
import { computed } from 'vue'

import { useAppsByOrgQuery } from '@/api/hooks/useApps'
import { useAuthStore } from '@/stores/auth'

export function useMemberApp() {
  const auth = useAuthStore()

  // org_member 的 org_id 即为查询范围；非 org_member 时 orgId 为 undefined，query 不启用。
  const orgId = computed(() => auth.isOrgMember ? auth.user?.org_id : undefined)
  const { data: apps, isLoading } = useAppsByOrgQuery(orgId)

  // 从组织实例列表中筛选当前用户拥有的实例（数据库保证最多一个）。
  const memberApp = computed(() =>
    apps.value?.find(app => app.owner_user_id === auth.user?.id),
  )

  const appId = computed(() => memberApp.value?.id)
  const hasApp = computed(() => Boolean(appId.value))

  return { appId, hasApp, isLoading }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/composables/__tests__/useMemberApp.spec.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/composables/useMemberApp.ts web/src/composables/__tests__/useMemberApp.spec.ts
git commit -m "feat(web): 新增 useMemberApp composable

为 org_member 提供其唯一活跃实例 ID 的查询能力，
供侧边栏导航和路由守卫使用。"
```

---

### Task 2: 创建 `AppEmptyPage` 空状态页

**Files:**
- Create: `web/src/pages/apps/AppEmptyPage.vue`

- [ ] **Step 1: 创建空状态页组件**

```vue
<!-- web/src/pages/apps/AppEmptyPage.vue -->
<template>
  <div class="empty-container">
    <n-empty description="请联系管理员创建实例">
      <template #icon>
        <Bot :size="48" :stroke-width="1" />
      </template>
    </n-empty>
  </div>
</template>

<script setup lang="ts">
import { NEmpty } from 'naive-ui'
import { Bot } from 'lucide-vue-next'
</script>

<style scoped>
.empty-container {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
  min-height: 400px;
}
</style>
```

- [ ] **Step 2: 提交**

```bash
git add web/src/pages/apps/AppEmptyPage.vue
git commit -m "feat(web): 新增 AppEmptyPage 空状态页

org_member 无实例时展示此页面，提示联系管理员创建。"
```

---

### Task 3: 路由变更 — 新增 `/apps/empty` 路由和 `/apps` 守卫

**Files:**
- Modify: `web/src/app/router.ts`

- [ ] **Step 1: 添加路由和守卫**

在 `router.ts` 中：

1. 导入 `AppEmptyPage` 和 `useAuthStore`（已导入）
2. 在 `apps/:appId` 路由之前添加 `apps/empty` 路由
3. 为 `apps` 列表路由添加 `beforeEnter` 守卫

修改后的 apps 相关路由部分：

```ts
import AppEmptyPage from '@/pages/apps/AppEmptyPage.vue'
```

路由定义中替换原来的 `{ path: 'apps', component: AppsPage }` 为：

```ts
{
  path: 'apps',
  component: AppsPage,
  beforeEnter: async (to, from, next) => {
    const auth = useAuthStore()
    if (auth.user?.role !== 'org_member') return next()
    // org_member 不进列表页，重定向到详情或空状态
    const orgId = auth.user.org_id
    if (!orgId) return next('/apps/empty')
    try {
      const { apiRequest } = await import('@/api/client')
      const response = await apiRequest<{ apps?: { id: string; owner_user_id: string }[] }>(
        `/api/v1/organizations/${orgId}/apps`, { query: { limit: 10 } },
      )
      const myApp = response.apps?.find(a => a.owner_user_id === auth.user!.id)
      return next(myApp ? `/apps/${myApp.id}/overview` : '/apps/empty')
    } catch {
      return next('/apps/empty')
    }
  },
},
{ path: 'apps/empty', component: AppEmptyPage },
```

注意：`apps/empty` 必须在 `apps/:appId` 之前定义，否则 `empty` 会被当作 `:appId` 参数匹配。

- [ ] **Step 2: 验证路由顺序正确**

确认最终路由顺序为：
1. `apps` (列表，带 beforeEnter 守卫)
2. `apps/empty` (空状态)
3. `apps/:appId` (详情)

- [ ] **Step 3: 运行现有测试确认无回归**

Run: `cd web && npx vitest run src/pages/apps/AppsPage.spec.ts`
Expected: PASS（平台管理员不触发守卫）

- [ ] **Step 4: 提交**

```bash
git add web/src/app/router.ts
git commit -m "feat(web/router): org_member 访问 /apps 时重定向到详情或空状态

新增 /apps/empty 路由和 /apps beforeEnter 守卫，
org_member 不再看到实例列表页。"
```

---

### Task 4: 侧边栏菜单项动态化

**Files:**
- Modify: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1: 引入 useMemberApp 并修改菜单项**

在 `<script setup>` 中添加：

```ts
import { useMemberApp } from '@/composables/useMemberApp'

const { appId: memberAppId, hasApp: memberHasApp } = useMemberApp()

// org_member 的实例菜单目标：有实例指向详情，无实例指向空状态。
const memberAppPath = computed(() => {
  if (!isOrgMember.value) return '/apps'
  if (memberHasApp.value && memberAppId.value) return `/apps/${memberAppId.value}/overview`
  return '/apps/empty'
})
```

修改 `menuOptions` 中实例菜单项：

```ts
// 原来：
items.push(
  { key: '/apps', label: '实例', icon: () => h(Bot, { size: 18 }) },
  ...
)

// 改为：
items.push(
  { key: memberAppPath.value, label: '实例', icon: () => h(Bot, { size: 18 }) },
  ...
)
```

- [ ] **Step 2: 修改 activeKey 计算逻辑**

```ts
// 原来的 activeKey：
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return '/'
  const prefixes = [
    '/platform/dashboard',
    '/organizations',
    '/members',
    '/apps',
    '/knowledge',
    '/usage',
    '/balance',
    '/audit-logs',
    '/runtime-nodes',
    '/org/persona',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})

// 改为：
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return '/'
  // org_member 的实例菜单 key 是动态路径，需要特殊匹配。
  if (p.startsWith('/apps')) return memberAppPath.value
  const prefixes = [
    '/platform/dashboard',
    '/organizations',
    '/members',
    '/knowledge',
    '/usage',
    '/balance',
    '/audit-logs',
    '/runtime-nodes',
    '/org/persona',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})
```

- [ ] **Step 3: 本地验证**

启动 dev server，分别以 org_member 和 org_admin 登录：
- org_member：侧边栏"实例"点击后直达详情页，菜单高亮正确
- org_admin：侧边栏"实例"点击后仍进入列表页

- [ ] **Step 4: 提交**

```bash
git add web/src/layouts/DashboardLayout.vue
git commit -m "feat(web/sidebar): org_member 实例菜单直达详情页

侧边栏对 org_member 动态计算实例入口路径，
有实例时指向详情，无实例时指向空状态页。"
```

---

### Task 5: RoleAwareHome 首页卡片路径适配

**Files:**
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`

- [ ] **Step 1: 修改 org_member 的"我的实例"卡片路径**

在 `<script setup>` 中引入 `useMemberApp`：

```ts
import { useMemberApp } from '@/composables/useMemberApp'

const { appId: memberAppId, hasApp: memberHasApp } = useMemberApp()
```

修改 `cards` computed 中 `org_member` 分支：

```ts
if (role === 'org_member') {
  // 有实例时直达详情，无实例时进入空状态页。
  const appPath = memberHasApp.value && memberAppId.value
    ? `/apps/${memberAppId.value}/overview`
    : '/apps/empty'
  return [
    { path: appPath, title: '我的实例', subtitle: '查看状态、用量与实例审计' },
    { path: '/usage', title: '我的用量', subtitle: '查看自己实例的调用记录' },
    { path: '/knowledge', title: '组织知识库', subtitle: '可读资料' },
  ]
}
```

- [ ] **Step 2: 本地验证**

以 org_member 登录，首页"我的实例"卡片点击后直达详情页。

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/dashboard/RoleAwareHome.vue
git commit -m "feat(web/home): org_member 首页实例卡片直达详情

复用 useMemberApp 动态计算路径，与侧边栏行为一致。"
```

---

### Task 6: 端到端验证

- [ ] **Step 1: 启动 dev server 并验证所有场景**

Run: `cd web && npm run dev`

验证矩阵：

| 角色 | 操作 | 预期 |
|------|------|------|
| org_member（有实例） | 点击侧边栏"实例" | 进入 `/apps/:appId/overview` |
| org_member（有实例） | 手动输入 `/apps` | 重定向到 `/apps/:appId/overview` |
| org_member（有实例） | 首页"我的实例"卡片 | 进入 `/apps/:appId/overview` |
| org_member（无实例） | 点击侧边栏"实例" | 进入 `/apps/empty`，显示提示文案 |
| org_admin | 点击侧边栏"实例" | 进入 `/apps` 列表页 |
| platform_admin | 点击侧边栏"实例" | 进入 `/apps` 列表页 |

- [ ] **Step 2: 运行全部前端测试**

Run: `cd web && npx vitest run`
Expected: 全部 PASS

- [ ] **Step 3: 修复发现的问题（如有）**

如果测试或手动验证发现问题，修复后追加提交。
