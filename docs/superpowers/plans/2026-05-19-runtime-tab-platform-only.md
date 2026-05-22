# 仅平台管理员可见"运行时"Tab 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实例详情页的"运行时"tab 仅对 platform_admin 可见，其他角色在 UI 和路由层均无法访问。

**Architecture:** 前端两处变更——AppDetailPage.vue 将 tabs 改为 computed 按角色过滤；router.ts 给 runtime 子路由加 `meta: { allowedRoles: PLATFORM_ONLY }` 复用现有 beforeEach guard。

**Tech Stack:** Vue 3, vue-router, Pinia (useAuthStore)

---

### Task 1: router.ts — runtime 子路由添加角色限制

**Files:**
- Modify: `web/src/app/router.ts:95`

- [ ] **Step 1: 给 runtime 子路由添加 meta.allowedRoles**

在 `web/src/app/router.ts` 第 95 行，将：

```ts
{ path: 'runtime', component: AppRuntimeTab, props: true },
```

改为：

```ts
{ path: 'runtime', component: AppRuntimeTab, props: true, meta: { allowedRoles: PLATFORM_ONLY } },
```

- [ ] **Step 2: 验证 TypeScript 编译通过**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head -20`
Expected: 无错误输出

- [ ] **Step 3: Commit**

```bash
git add web/src/app/router.ts
git commit -m "feat(web/router): runtime 子路由限制仅平台管理员访问

为 apps/:appId/runtime 添加 meta.allowedRoles: PLATFORM_ONLY，
复用现有 beforeEach guard 拦截非平台管理员的直接 URL 访问。"
```

---

### Task 2: AppDetailPage.vue — Tab 列表按角色动态过滤

**Files:**
- Modify: `web/src/pages/apps/AppDetailPage.vue:30-57`

- [ ] **Step 1: 引入 useAuthStore**

在 `<script setup>` 的 import 区域（第 36 行 `import AppStatusTag` 之后）添加：

```ts
import { useAuthStore } from '@/stores/auth'
```

在 `const route = useRoute()` 之前添加：

```ts
const auth = useAuthStore()
```

- [ ] **Step 2: 将静态 tabs 改为 computed**

将第 50-57 行：

```ts
// tabs 定义详情页的业务分区，path 必须和子路由末段保持一致。
const tabs: ReadonlyArray<{ path: string; label: string }> = [
  { path: 'overview', label: '概览' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '实例知识库' },
  { path: 'workspace', label: '工作目录' },
  { path: 'audit', label: '审计' },
]
```

替换为：

```ts
// allTabs 定义详情页的全部业务分区，path 必须和子路由末段保持一致。
const allTabs: ReadonlyArray<{ path: string; label: string }> = [
  { path: 'overview', label: '概览' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '实例知识库' },
  { path: 'workspace', label: '工作目录' },
  { path: 'audit', label: '审计' },
]

// 运行时 tab 仅对平台管理员可见，属基础设施层信息不向组织用户暴露。
const tabs = computed(() =>
  auth.isPlatformAdmin ? allTabs : allTabs.filter(t => t.path !== 'runtime')
)
```

- [ ] **Step 3: 验证 TypeScript 编译通过**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | head -20`
Expected: 无错误输出

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/apps/AppDetailPage.vue
git commit -m "feat(web/app-detail): 运行时 tab 仅平台管理员可见

将 tabs 从静态数组改为 computed，非 platform_admin 角色过滤掉
runtime tab，配合路由 guard 实现 UI + URL 双重拦截。"
```

---

### Task 3: 浏览器验证

- [ ] **Step 1: 以 platform_admin 登录验证**

1. 启动开发服务器：`cd web && npm run dev`
2. 浏览器打开 http://localhost:5173/login
3. 使用 `admin` / `admin123` 登录（平台管理员）
4. 进入任意实例详情页
5. 确认 tab 栏包含"运行时"，点击可正常展示容器状态

Expected: "运行时"tab 可见且功能正常

- [ ] **Step 2: 以 org_admin 或 org_member 登录验证**

1. 使用组织用户账号登录
2. 进入实例详情页
3. 确认 tab 栏不包含"运行时"
4. 手动在地址栏输入 `/apps/<appId>/runtime`
5. 确认被重定向到首页

Expected: tab 不可见，URL 直接访问被拦截重定向
