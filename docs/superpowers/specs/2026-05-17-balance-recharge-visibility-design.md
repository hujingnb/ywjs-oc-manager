# 设计文档：余额与充值记录可见性

**日期：** 2026-05-17
**状态：** 已批准

## 背景

当前系统中，充值记录（`ListRecharges`）仅平台管理员可查，余额查询（`GetBalance`）已支持平台管理员和组织管理员。但缺少两个能力：

1. 组织管理员无法查看自己组织的充值记录
2. 任何角色都无法直接获取"累计充值金额"

本次需求：组织管理员能看到自己的余额及充值记录；平台管理员也能在组织列表页看到各组织的余额和充值记录。

## 数据原则

遵循 AGENTS.md 规范：

- 余额（当前剩余额度）实时从 new-api 查询，manager 不做本地缓存
- 累计充值金额来源于 manager 自身操作产生的 `recharge_records` 聚合，不违反上述原则
- 充值记录来源于 `recharge_records`，同属 manager 自身操作日志

## 后端变更

### 1. `BalanceView` 新增 `total_recharged` 字段

```go
type BalanceView struct {
    NewAPIUserID   int64 `json:"newapi_user_id"`
    RemainQuota    int64 `json:"remain_quota"`    // 实时从 new-api 查
    UsedQuota      int64 `json:"used_quota"`      // 实时从 new-api 查
    TotalRecharged int64 `json:"total_recharged"` // SUM recharge_records WHERE status='succeeded'
}
```

`GetBalance` 并发执行两个查询：① 调 new-api 取 `RemainQuota`/`UsedQuota`，② 查本地 DB 聚合 `TotalRecharged`，合并后返回。

### 2. `ListRecharges` 权限放开给 org_admin

在 `internal/auth/authorizer.go` 扩展权限谓词（`CanViewRecharges` 或同类函数）：

| 角色 | 可查范围 |
|------|----------|
| `platform_admin` | 任意组织 |
| `org_admin` | 仅自己所属组织（`orgID == principal.OrgID`） |
| `org_member` | 无权限 |

### 3. 新增 SQL 聚合查询

```sql
-- name: SumRechargeAmountByOrg :one
SELECT COALESCE(SUM(credit_amount), 0)::bigint
FROM recharge_records
WHERE org_id = $1 AND status = 'succeeded';
```

无新路由，无新 handler，仅修改现有接口响应结构和权限逻辑。

## 前端变更

### 平台管理员 — 组织列表页扩展

**余额列：**

- 组织表格新增"当前余额"列
- 页面加载时对每行调 `GET /api/v1/organizations/:orgId/balance`，展示 `remain_quota`（复用现有 `BillingStatus` 单位换算逻辑）

**充值记录弹窗：**

- 每行新增"充值记录"操作按钮
- 点击弹出 Modal，内含：
  - 头部：累计充值（`total_recharged`）+ 当前余额（`remain_quota`）两个数字卡片
  - 分页充值记录表格，列：时间 / 金额 / 备注 / 状态 / 操作人

### 组织管理员 — 新增"账户余额"页

**导航：** 左侧顶级菜单新增"账户余额"，与"用量统计"同级

**页面结构：**

- 头部两张数字卡片：累计充值金额 / 当前剩余金额
- 下方分页充值记录表格，列：时间 / 金额 / 备注 / 状态（隐藏操作人，org_admin 无需关注）

**数据来源：** `orgId` 从当前登录 principal 取，调用与平台管理员相同的两个接口

## 涉及文件清单

### 后端

| 文件 | 变更内容 |
|------|----------|
| `internal/store/queries/recharge_records.sql` | 新增 `SumRechargeAmountByOrg` 查询 |
| `internal/store/sqlc/recharge_records.sql.go` | sqlc 生成，勿手改 |
| `internal/service/recharge_service.go` | `BalanceView` 增加字段；`GetBalance` 并发查聚合；`ListRecharges` 权限逻辑调整 |
| `internal/auth/authorizer.go` | 扩展充值记录查看权限谓词 |
| `openapi/openapi.yaml` | `make openapi-gen` 生成，勿手改 |
| `web/src/api/generated.ts` | `make web-types-gen` 生成，勿手改 |

### 前端

| 文件 | 变更内容 |
|------|----------|
| `web/src/pages/admin/organizations/` | 组织列表页：增加余额列 + 充值记录 Modal |
| `web/src/pages/org/balance/` | 新增账户余额页（新建） |
| `web/src/router/` 或路由配置文件 | 注册 org_admin 新路由 |
| `web/src/components/nav/` 或菜单配置 | org_admin 导航新增"账户余额"菜单项 |

## 不在本次范围内

- org_admin 自助充值入口
- 余额预警通知
- 充值记录导出
