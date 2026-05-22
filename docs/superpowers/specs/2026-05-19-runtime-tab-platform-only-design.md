# 设计：仅平台管理员可见实例"运行时"Tab

## 背景

实例详情页 `AppDetailPage.vue` 当前对所有角色展示相同的 tab 列表，包括"运行时"。
"运行时"tab 展示容器状态、资源趋势图和操作按钮（启动/停止/重启/删除），属于基础设施层信息，不应对普通组织用户暴露。

## 目标

- 仅 `platform_admin` 角色可以看到并访问实例详情页的"运行时"tab
- `org_admin` 和 `org_member` 在 tab 栏中看不到该入口，且无法通过 URL 直接访问

## 方案

采用 Tab 过滤 + 路由 meta guard，复用项目现有权限模式。

### 变更 1：AppDetailPage.vue — Tab 列表动态化

将静态 `tabs` 数组改为 `computed`，根据 `auth.isPlatformAdmin` 过滤：

```ts
const allTabs = [
  { path: 'overview', label: '概览' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '实例知识库' },
  { path: 'workspace', label: '工作目录' },
  { path: 'audit', label: '审计' },
]

const tabs = computed(() =>
  auth.isPlatformAdmin
    ? allTabs
    : allTabs.filter(t => t.path !== 'runtime')
)
```

### 变更 2：router.ts — runtime 子路由添加 allowedRoles

给 `apps/:appId` 下的 `runtime` 子路由添加路由级权限：

```ts
{
  path: 'runtime',
  component: () => import('@/pages/apps/AppRuntimeTab.vue'),
  meta: { allowedRoles: PLATFORM_ONLY },
}
```

现有 `beforeEach` guard 已处理 `allowedRoles` 检查，非平台管理员访问该路由会被重定向到首页。

### 不变的部分

- `AppRuntimeTab.vue` 组件内部不改动
- `permissions.ts` 不新增函数
- 后端 API 无变更（运行时相关 API 已有后端权限校验）

## 验证标准

| 角色 | Tab 栏 | 直接 URL 访问 `/apps/:id/runtime` |
|------|--------|----------------------------------|
| platform_admin | 可见"运行时" | 正常展示 |
| org_admin | 不可见 | 重定向到首页 |
| org_member | 不可见 | 重定向到首页 |

## 影响范围

仅前端两个文件，无后端变更，无数据库变更，无 API 契约变更。
