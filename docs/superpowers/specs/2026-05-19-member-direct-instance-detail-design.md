# 设计文档：org_member 跳过实例列表直达详情页

## 背景

当前所有角色访问侧边栏"实例"入口时都会进入 `/apps` 实例列表页。但 `org_member` 由数据库唯一索引 `apps_owner_active` 保证最多只有一个活跃实例，列表页对他们来说是多余的中间层。

## 需求

- `org_member` 侧边栏"实例"入口直接导航到其唯一实例的详情页
- 如果该用户尚无实例，显示空状态页提示"请联系管理员创建实例"
- `org_admin` 和 `platform_admin` 行为不变

## 设计方案

### 1. 新增 composable：`useMemberApp`

文件：`web/src/composables/useMemberApp.ts`

职责：对 `org_member` 查询其唯一活跃实例的 appId，供侧边栏和路由守卫使用。

```ts
// 返回值
interface MemberAppState {
  appId: Ref<string | undefined>  // 实例 ID，无实例时为 undefined
  isLoading: Ref<boolean>
  hasApp: Ref<boolean>            // 是否有活跃实例
}
```

实现：复用 `useAppsByOrgQuery`，从返回列表中取 `owner_user_id === currentUser.id` 的第一条记录。仅在 `isOrgMember` 时启用查询。

### 2. 侧边栏改动

文件：`web/src/layouts/DashboardLayout.vue`

- 引入 `useMemberApp`
- `org_member` 的"实例"菜单项 key 改为动态值：
  - `hasApp && appId` → `/apps/${appId}/overview`
  - 否则 → `/apps/empty`
- `activeKey` 计算逻辑对 `org_member` 做适配：当路径匹配 `/apps/` 前缀时激活"实例"项

### 3. 新增空状态页

文件：`web/src/pages/apps/AppEmptyPage.vue`

简单页面，居中显示空状态图标 + 文案"请联系管理员创建实例"。使用 Naive UI 的 `<n-empty>` 组件。

### 4. 路由变更

文件：`web/src/app/router.ts`

- 新增路由 `apps/empty`，组件为 `AppEmptyPage`
- 为 `/apps` 列表路由添加 `beforeEnter` 守卫：`org_member` 访问时重定向到 `/apps/:appId/overview` 或 `/apps/empty`

### 5. activeKey 适配

当前 `activeKey` 通过前缀匹配 `/apps` 来高亮菜单项。`org_member` 的菜单 key 变为 `/apps/:appId/overview`，需要确保前缀匹配仍然生效。

方案：`activeKey` 对 `org_member` 特殊处理——当路径以 `/apps` 开头时，返回当前菜单项的动态 key 值（即 `/apps/${appId}/overview` 或 `/apps/empty`）。

## 不改动的部分

- 后端 API 无变更
- `AppDetailPage` 及其子 tab 无变更
- `org_admin` / `platform_admin` 的实例列表行为无变更
- `AppsPage.vue` 保留不删除（其他角色仍使用）

## 数据流

```
org_member 登录
  → auth store 加载用户信息
  → DashboardLayout 挂载
  → useMemberApp 发起查询（复用 useAppsByOrgQuery）
  → 侧边栏菜单项 key 动态绑定
  → 用户点击"实例" → 直达详情页或空状态页
```

## 边界情况

| 场景 | 行为 |
|------|------|
| org_member 有实例 | 侧边栏指向 `/apps/:appId/overview` |
| org_member 无实例 | 侧边栏指向 `/apps/empty`，显示空状态 |
| org_member 手动输入 `/apps` | 路由守卫重定向到详情或空状态 |
| 查询加载中 | 侧边栏暂时指向 `/apps/empty`，加载完成后自动更新 |
| org_admin / platform_admin | 行为完全不变 |
