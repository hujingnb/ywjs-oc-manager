# 控制台页面设计（平台总览与平台仪表板合并）

## 背景

`platform_admin` 当前在侧边栏看到两个独立入口：

- **总览**（`/`）→ `RoleAwareHome`：仅有欢迎语和 3 个快捷卡片
- **平台**（`/platform/dashboard`）→ `PlatformDashboardPage`：6 个统计卡片（组织/成员/实例数等）

两者内容单薄且分散，用户需在两个入口间切换才能获取平台状态全貌。

## 目标

将两个页面合并为单一的"**控制台**"页（`/console`），并增加实时统计与图表能力，让平台管理员一屏掌握平台健康状态。其他角色（`org_admin` / `org_member`）首页体验不变。

## 不在范围内

- 其他角色的首页变更
- 图表时间范围选择器（可后续迭代）
- 快捷操作入口
- 统计卡片环比趋势（Delta 指标）

---

## 路由与导航变更

| 变更项 | 当前 | 目标 |
|---|---|---|
| `/`（platform_admin） | `RoleAwareHome` → 欢迎卡片 | 重定向到 `/console` |
| `/platform/dashboard` | `PlatformDashboardPage` | 重定向到 `/console` |
| `/dashboard` | `DashboardHome`（菜单无入口，实际废弃） | 废弃并删除 |
| `/console` | 不存在 | 新增，`PLATFORM_ONLY` |
| 菜单（platform_admin） | 总览 + 平台 = 2 项 | 控制台 = 1 项（置顶） |

`RoleAwareHome` 保留，仅对 `platform_admin` 角色在 `beforeEnter` 中重定向到 `/console`；其他角色路径不变。

---

## 页面结构

布局采用「统计条 + 标签页图表」：顶部一排统计卡片，下方图表区用 Tab 切换，图表区域最大化。

### 统计条

共 6 张卡片，横向排列，10 秒自动轮询（复用现有 `usePlatformOverviewQuery` 间隔）：

| 卡片 | 数据来源 | 备注 |
|---|---|---|
| 组织数 | `overview.organization_count` | |
| 成员数 | `overview.member_count` | 不含平台管理员 |
| 实例数 | `overview.app_count` | |
| 运行中 | `overview.running_app_count` | 绿色高亮 |
| 异常 | `overview.error_app_count` | 橙/红色高亮 |
| 今日 Token | `usage/platform` 今日 QuotaDate 项 | new-api 实时；不可用时显示 `—` |

"今日 Token"从 `GET /api/v1/usage/platform` 拉取当天时间区间的 `QuotaDate[]`，前端对 `quota` 字段求和后显示（`QuotaDate.Quota` 是 new-api 的 used quota 原始值）。

### 图表区（Tab 切换）

#### Tab 1：Token 趋势
- 类型：折线图
- 数据：近 7 天全平台每日 `used_quota`
- 来源：已有 `GET /api/v1/usage/platform?since=<7天前>&until=<今天>`
- 无需后端改动

#### Tab 2：各组织用量
- 类型：横向柱状图，按消耗量降序，最多显示 top 10
- 数据：近 7 天各组织 quota 消耗汇总
- 来源：新增 `GET /api/v1/platform/usage/org-breakdown?since=<unix>&until=<unix>`

#### Tab 3：实例状态
- 类型：饼图（运行中 / 停止 / 异常）
- 数据：`overview.running_app_count` / `overview.error_app_count` / 其余
- 来源：已有 overview 数据，零额外请求

图表渲染使用 `vue-echarts` + `echarts`（如未安装则 `pnpm add echarts vue-echarts`）。

---

## 后端变更

### 新增接口

**`GET /api/v1/platform/usage/org-breakdown`**

```
Query params:
  since  int64  Unix 秒，起始时间
  until  int64  Unix 秒，截止时间

Response:
  {
    "items": [
      { "org_id": "...", "org_name": "...", "total_quota": 123456 },
      ...
    ],
    "updated_at": "2026-05-22T10:00:00Z"
  }
```

- 权限：`platform_admin` only（`authorizer.go` 已有 `CanViewPlatformOverview` 或直接用角色判断）
- 实现：`UsageService` 新增 `GetOrgUsageBreakdown` 方法
  1. 新增 SQL 查询 `ListActiveOrganizations`（`deleted_at IS NULL`，不分页，全量返回），并扩展 `UsageStore` 接口
  2. 对每个有 `newapi_user_id` 和 `newapi_username` 的组织，并行调 `client.GetUserQuotaDates`
  3. 汇总各组织在 `[since, until]` 内的 `QuotaDate.Quota` 字段合计得到 `total_quota`
  4. 按 `total_quota` 降序返回，截取前 10 条
- 并发：使用 `errgroup` 限制并发数（建议 5），避免对 new-api 造成压力
- 降级：new-api 不可用时返回 `ErrUsageUnavailable`（前端图表显示"不可用"提示）

### 无变更接口

- `GET /api/v1/platform/overview` — 继续用，提供统计条前 5 项数据
- `GET /api/v1/usage/platform` — 继续用，提供 Token 趋势 Tab 和今日 Token 统计卡

---

## 前端变更

### 新增文件

- `web/src/pages/platform/ConsolePage.vue` — 主页面，包含统计条 + Tab 图表

### 修改文件

| 文件 | 变更说明 |
|---|---|
| `web/src/app/router.ts` | 新增 `/console` 路由（`PLATFORM_ONLY`）；`/platform/dashboard` 和 `/dashboard` 重定向到 `/console` |
| `web/src/layouts/DashboardLayout.vue` | `platform_admin` 菜单：移除"总览"和"平台"两项，新增"控制台"（`/console`，置顶） |
| `web/src/pages/dashboard/RoleAwareHome.vue` | `platform_admin` 进入时 `router.replace('/console')`，其他角色不变 |
| `web/src/api/hooks/usePlatform.ts` | 新增 `usePlatformOrgBreakdownQuery` hook |

### 废弃文件（删除）

- `web/src/pages/platform/PlatformDashboardPage.vue`
- `web/src/pages/dashboard/DashboardHome.vue`

### API 契约同步

修改 handler 后执行：
```bash
make openapi-gen
make web-types-gen
```

---

## 测试要点

- `UsageService.GetOrgUsageBreakdown` 单元测试：正常路径、new-api 不可用、部分组织无 `newapi_user_id` 时不报错
- 前端 `ConsolePage` 各 Tab 切换渲染正常，统计卡片在 usage 不可用时优雅降级（显示 `—`）
- 路由守卫：`platform_admin` 访问 `/` 和 `/platform/dashboard` 均能正确重定向到 `/console`；`org_admin` 和 `org_member` 访问 `/` 仍见原有欢迎页
