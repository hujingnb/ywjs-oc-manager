# 设计文档：放开 Job 查询权限并保留组织隔离

**日期：** 2026-05-25
**状态：** 待批准

## 背景

在实例概览页点击「立即重启」后，前端会出现两条互相矛盾的反馈：

1. 上方反馈条：「已提交重启任务：e24e05c3-f668-4e85-b713-0ebb36fcc14d」
2. 下方 `JobProgressPanel`：标签显示「未触发」，正文显示「尚未触发任务」

而后端 worker 实际已经把该 job 处理成 `succeeded`，容器也确实被重启。

### 根因

后端 `GET /api/v1/jobs/:jobId`（`internal/api/handlers/jobs.go:69`）写死「仅 `platform_admin` 可访问」。但触发重启的入口 `POST /apps/:id/runtime/restart` 由 `auth.CanTriggerRuntimeOperation` 鉴权，`org_admin` / `org_member` 都允许触发。

因此组织管理员或组织成员点完重启后，前端 `useJobQuery`（`web/src/api/hooks/useApps.ts:272`）每 2s 轮询 `/api/v1/jobs/${jobId}` 都返回 403，TanStack Query `data` 始终为 `null`，`JobProgressPanel` 退化到「尚未触发任务」分支。

这是权限设计与 UI 反馈不一致的 bug，不是任务调度的 bug。

## 设计目标

- 让能够触发 job 的角色，也能查看自己触发的 job 的进度，使 `JobProgressPanel` 正常工作。
- 保留 `auth.CanViewApp` 的组织隔离语义：跨组织成员不应看到别组织的 job。
- 修复前端 `useJobQuery` 在 4xx 时一直轮询的隐患。
- 不动数据库 schema（`jobs` 表不加 `org_id` / `app_id` 列），用 `payload_json.app_id` 反查即可。

## 后端变更

### 1. `JobsHandler.Get` 改为按 app 资源鉴权

文件：`internal/api/handlers/jobs.go`

行为：

- `platform_admin`：直接放行（保持原行为）
- 其他角色：
  1. 解 `job.PayloadJson` 拿 `app_id`；
  2. 若 payload 没有 `app_id`，返回 403（兜底，留给未来纯平台级 / 组织级 job）；
  3. 用 `app_id` 调 store 查 app；查不到返回 404；
  4. 调用 `auth.CanViewApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID))`，false 返回 403，true 放行。

实现要点：

- payload 反序列化只取 `app_id` 字段，定义局部匿名结构体，避免拉满 payload 全字段。
- 目前 runtime ops（start / stop / restart / delete / disable_api_key / restore_api_key）、`app_initialize`、`channel_*`、`member_*` 等所有面向用户的 job 类型 payload 都包含 `app_id`（见 `internal/service/{runtime_operation,onboarding,channel,member}_service.go`），不会回归。

### 2. `JobsStore` 接口扩展

文件：`internal/api/handlers/jobs.go`

```go
type JobsStore interface {
    GetJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
    GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error) // 新增
}
```

实际注入仍是 `*sqlc.Queries`，无需新建装饰器。`internal/api/handlers/jobs_test.go` 的 stub 需要同步实现 `GetApp`。

### 3. 单元测试覆盖

文件：`internal/api/handlers/jobs_test.go`

新增用例（按 AGENTS.md 要求，每个子测试和 table 行都补中文场景注释，断言用 `testify/require` + `assert`）：

| 场景 | 期望 |
|---|---|
| platform_admin 查任意 job（含 payload 无 app_id） | 200 |
| org_admin 查本组织 app 的 job | 200 |
| org_member 查本组织内自己拥有的 app 的 job | 200 |
| org_admin 查别组织 app 的 job | 403 |
| org_member 查别人拥有的 app 的 job（非 owner、非 admin） | 403（CanViewApp 已覆盖） |
| 任意非 admin 角色查 payload 无 app_id 的 job | 403 |
| job_id 找不到 | 404 |
| app_id 解出来但 app 找不到（脏数据） | 404 |

### 4. OpenAPI 同步

handler 的 swag 注释里把「平台管理员按 ID 查询」改成按资源可见性鉴权的描述；跑 `make openapi-gen` + `make web-types-gen`，把 yaml 与前端类型一起提交。路由签名不变，前端类型无实质变化。

## 前端变更

### 1. `JobProgressPanel` / `AppOverviewTab.vue`：不动

后端修好后 `useJobQuery` 直接拿到 200，`JobProgressPanel` 自动正常显示。

### 2. `useJobQuery` 4xx 停轮询

文件：`web/src/api/hooks/useApps.ts:272`

当前 `refetchInterval` 只看 `data`，请求失败时 `data` 是 `undefined`，会一直 2s 轮询。改为：拿到 4xx（403/404/400）后停轮询，5xx 继续重试以容忍偶发故障。

实现思路：在 `refetchInterval` 回调里检查 `query.state.error`，若是 `ApiError` 且 `status >= 400 && status < 500`，返回 `false`。同时把 `retry` 设为「4xx 不重试」（TanStack Query 标准模式）。

这一改动对后端修复后的主路径无影响，仅作为防御。

## 涉及文件清单

### 后端

| 文件 | 变更内容 |
|---|---|
| `internal/api/handlers/jobs.go` | `Get` 重写鉴权逻辑；`JobsStore` 增加 `GetApp`；swag 注释更新 |
| `internal/api/handlers/jobs_test.go` | 新增 7~8 个用例覆盖三角色 × payload / 资源边界 |
| `openapi/openapi.yaml` | `make openapi-gen` 生成 |
| `web/src/api/generated.ts` | `make web-types-gen` 生成 |

### 前端

| 文件 | 变更内容 |
|---|---|
| `web/src/api/hooks/useApps.ts` | `useJobQuery` 在 4xx 时停轮询、不重试 |

## 不在本次范围

- `jobs` 表加 `org_id` / `app_id` 列：需要 migration + 回填 + 双写策略，超出 bug 修复范围。当前 payload 反查足够。
- 新增按组织 / 按 app 列出 job 的接口。
- 重做 `JobProgressPanel` 的状态收口（如已完成 job 收起 / 历史 job 列表）。
- 重启反馈条文案优化（「已提交重启任务：UUID」当前形态保留）。
