# 组织单模型绑定设计

## 概述

将模型管理从"组织多模型白名单 + 实例自选"简化为"组织绑定单一模型，实例自动继承"。组织/普通用户完全无法感知模型的存在。

## 目标

- 平台管理员在创建组织时指定唯一模型，后续该组织所有实例使用同一模型
- 组织管理员和普通成员无法看到或选择模型
- 用量列表中不展示模型信息
- 平台管理员修改组织模型后，实例列表提示"需重启生效"

## 数据模型变更

### organizations 表

- 移除 `enabled_models` (jsonb) 字段
- 新增 `model_id` (text NOT NULL) — 该组织统一使用的模型 ID

### apps 表

- `model_id` (text NOT NULL) 保留，创建时自动从 `organizations.model_id` 填入
- 新增 `model_synced` (boolean NOT NULL DEFAULT true) — 实例当前运行的模型是否与数据库记录一致
  - 组织模型变更时：更新所有关联实例 `apps.model_id`，置 `model_synced = false`
  - 实例重启完成后：置 `model_synced = true`

### 移除的索引

- `apps_org_model_active_idx` — 原用于防止缩减 allowlist 时模型仍在使用，不再需要

## 后端 API 变更

### 组织接口

| 接口 | 变更 |
|------|------|
| `POST /api/v1/organizations` | 请求体 `enabled_models []string` → `model_id string`（必填） |
| `PUT /api/v1/organizations/:id` | 支持修改 `model_id`；修改时同步更新该组织下所有实例的 `apps.model_id`，置 `model_synced = false` |
| 组织详情/列表响应 | `enabled_models` → `model_id` |

### 实例接口

| 接口 | 变更 |
|------|------|
| `PATCH /api/v1/apps/:appId/model` | 移除此接口 |
| `OnboardMember` / `CreateMemberApp` | 请求体移除 `model_id`，后端自动从组织读取 |
| 实例列表/详情响应 | 新增 `model_synced` 字段；`model_id` 仅平台管理员可见 |

### 模型目录

- `GET /api/v1/models` 不变，仅平台管理员可用

### 权限规则

- 模型相关所有写操作仅 `platform_admin`
- `model_id` 字段在 API 响应中仅对 `platform_admin` 返回
- 原 `ensureModelAllowed` 简化为验证 `org.ModelID` 在 new-api 模型目录中存在

## 前端变更

### 创建组织页面

- 模型选择从多选改为单选下拉框

### 创建成员/实例页面

- 移除模型选择字段（所有角色）

### 实例列表页面

- `model_synced = false` 时显示提示标签："模型已变更，需重启生效"
- `model_id` 列仅平台管理员可见；组织管理员/成员不展示

### 用量页面

- 隐藏模型列（所有角色均不展示）

### 组织详情/编辑页面

- 模型字段从多选改为单选
- 仅平台管理员可见

## 迁移策略

不做数据兼容迁移。线上手动清除现有组织、用户、实例数据后，直接应用新 schema。

清数据 SQL：
```sql
TRUNCATE channel_bindings, apps, audit_logs, users, organizations CASCADE;
```

## 影响范围

### 后端文件

- `internal/migrations/` — 新增迁移：改 organizations 字段、改 apps 字段、删索引
- `internal/store/sqlc/` — 重新生成，Organization 结构体字段变更
- `internal/service/organization_service.go` — 创建/更新组织逻辑适配单模型
- `internal/service/onboarding_service.go` — 移除 model_id 参数，自动从组织读取
- `internal/service/app_service.go` — 移除 UpdateModel 方法；重启回调中置 model_synced = true
- `internal/api/handlers/dto.go` — 请求/响应 DTO 适配
- `internal/api/handlers/apps.go` — 移除 model 更新路由
- `internal/api/handlers/organizations.go` — 适配单模型字段
- `internal/api/router.go` — 移除 PATCH model 路由

### 前端文件

- 创建组织表单组件 — 多选改单选
- 创建成员/实例表单 — 移除模型字段
- 实例列表组件 — 增加 model_synced 提示，model_id 按角色显隐
- 用量页面 — 隐藏模型列
- 组织详情/编辑 — 多选改单选
- `web/src/api/generated.ts` — 随 openapi 重新生成
