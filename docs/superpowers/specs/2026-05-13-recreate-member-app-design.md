# 平台管理员为已有成员重建实例设计

## 背景

当前 `apps_owner_active` 是 `WHERE deleted_at IS NULL` 的部分唯一索引，已经允许同一用户在旧实例软删除后再次拥有一个新的活跃实例。现有缺口在业务入口：`OnboardMember` 只覆盖“创建新成员并初始化实例”，会创建新的用户账号；它不能表达“成员账号已存在，仅为该成员重建实例”的场景。

本次需求是：当成员的实例已删除后，平台管理员可以为该用户再次创建新的实例。该操作不重新创建成员账号，不复用已删除的 app 行，只新增一条 app 记录并重新走初始化任务。

## 目标

- 平台管理员可为任意组织内的已有成员创建新实例。
- 创建前必须确认目标用户存在、属于路径中的组织、账号未下线，并且没有未删除实例。
- 新实例创建后应与 onboarding 保持一致：创建应用、渠道绑定、审计日志和 `app_initialize` job。
- 保持“一名用户最多一个未删除实例”的约束，继续依赖现有 `apps_owner_active` 部分唯一索引。

## 非目标

- 不恢复或复用已删除的 app 行。
- 不改变实例删除语义；删除后的旧实例仍作为历史记录保留。
- 不改变成员创建流程；`OnboardMember` 仍只负责新成员开户。
- 不开放普通组织成员自行创建实例。

## 方案

新增一个面向已有成员的实例创建流程，API 为：

```text
POST /api/v1/organizations/{orgId}/members/{userId}/apps
```

请求体包含实例初始化所需字段：

```json
{
  "app_name": "实例名称",
  "app_prompt": "可选实例 prompt",
  "persona_mode": "org_inherited",
  "channel_type": "wechat",
  "node_id": "可选 runtime 节点 ID"
}
```

响应返回新建实例和初始化任务：

```json
{
  "app": { "...": "service.AppResult" },
  "job_id": "..."
}
```

## 权限

权限谓词放在 `internal/auth/authorizer.go`。新增或扩展应用创建权限，明确区分“组织管理员为本组织开户”和“平台管理员为已有成员重建实例”：

- `platform_admin` 可以为任意组织内已有成员创建实例。
- `org_admin` 可以继续在本组织边界内创建实例；前端入口仅平台管理员可见。
- `org_member` 不允许创建实例。

service 层只调用 authorizer 谓词，不内联角色判断。

## 后端流程

新增 service 方法，例如 `CreateAppForMember(ctx, principal, orgID, userID, input)`：

1. 校验 principal 对目标组织具备实例创建权限。
2. 解析 `orgID` 和 `userID`，查询组织与用户。
3. 确认组织未删除且状态可用；确认用户属于该组织。
4. 确认用户状态不是 `disabled`，避免给已下线账号创建可运行实例。
5. 调用 `GetActiveAppByOwner`；若命中，返回业务错误，提示该用户已有实例。
6. 若 `node_id` 为空，沿用现有 `NodeSelector` 自动选节点；若显式传入，校验 UUID 格式。
7. 在事务内创建 app、默认 channel binding、应用创建审计和 `app_initialize` job。
8. 返回 `AppResult` 和 `job_id`。

该流程应复用 onboarding 中已经存在的节点选择、默认 `persona_mode`、默认 `channel_type` 和 job payload 语义，避免两个创建入口行为漂移。

## 错误处理

- 组织或用户不存在：返回 `ErrNotFound`。
- 用户不属于路径组织：返回 `ErrNotFound`，避免泄露跨组织用户存在性。
- 用户已 disabled：返回成员创建/实例创建的非法状态错误。
- 已有未删除实例：返回明确的业务校验错误。
- 没有可用 runtime 节点：沿用 `ErrNoNodeAvailable`。
- 数据库唯一索引冲突仍作为兜底错误处理；正常路径应在插入前通过 `GetActiveAppByOwner` 给出可读错误。

## 前端

前端入口放在成员列表的行操作中，仅平台管理员可见。平台管理员选择目标组织后，在成员列表中对目标成员触发“创建新实例”。表单字段与现有“创建成员并初始化实例”的实例部分保持一致：

- 实例名
- 人设模式
- 实例 prompt
- 可选 runtime 节点

提交成功后展示新实例名称、状态和 job id，并可跳转实例详情。若该成员已有未删除实例或账号已下线，前端展示后端返回的业务错误，不在本地猜测状态。

## OpenAPI

新增 handler、请求体或响应类型后必须运行：

```text
make openapi-gen
make web-types-gen
```

`openapi/openapi.yaml` 与 `web/src/api/generated.ts` 作为生成产物随实现提交，不手工编辑。

## 测试

service 单测覆盖：

- 平台管理员为无活跃实例的成员创建新实例成功。
- 已有未删除实例时拒绝创建。
- 用户属于其他组织时拒绝。
- disabled 用户拒绝创建。
- 无可用节点时返回 `ErrNoNodeAvailable`。

handler 单测覆盖：

- 新路由解析 `orgId`、`userId` 和请求体，并返回 `201`。
- service 返回业务错误时映射到现有错误响应。

authorizer 单测覆盖：

- 平台管理员允许。
- 本组织管理员允许。
- 其他组织管理员与普通成员拒绝。

前端测试覆盖平台管理员能看到创建入口、普通组织角色看不到入口，以及提交成功后展示新实例结果。
