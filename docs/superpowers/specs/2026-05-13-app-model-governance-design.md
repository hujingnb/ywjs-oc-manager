# 实例模型治理设计

## 背景

当前应用初始化时使用 `openclaw.llm.default_model` 作为全局默认模型，并在
`app_initialize` 中通过 `openclaw config patch --stdin` 写入 OpenClaw 配置。
new-api token 创建时传入 `Models: []string{}`，表示 token 不限制模型范围。

本次需求是把“实例使用哪个模型”纳入 manager 管理：

- 平台管理员在创建或编辑组织时选择该组织可用模型。
- 组织下创建实例时选择具体模型。
- 后续可以修改实例模型。
- 修改模型需要重启实例后生效。
- new-api 侧不做模型权限控制，所有 manager 创建的账号和 token 仍允许使用全部模型。

## 目标

- 支持从 new-api 实时获取当前可用模型列表。
- 支持平台管理员配置组织可用模型列表。
- 支持创建实例时选择模型，并保存为实例当前模型。
- 支持修改实例模型，并通过重启任务让 OpenClaw 使用新模型。
- 保证 manager API 层强校验：实例模型必须属于组织可用模型。
- 完成浏览器端完整验证：不同实例模型独立展示、切换和实际调用成功。

## 非目标

- 不在 new-api 用户或 token 层限制模型权限。
- 不新增本地模型目录缓存表。
- 不实现实例多模型或运行中无重启热切换。
- 不自动批量迁移正在使用旧模型的实例。
- 不管理 Ollama 模型下载、删除或渠道配置。

## 已确认决策

- 模型列表实时从 new-api 拉取，不落本地缓存表。
- 如果创建或编辑组织时无法拉取模型列表，阻止提交。
- 组织模型 allowlist 是 manager 层强约束。
- 修改实例模型需要重启实例。
- 平台管理员不能直接移除仍被未删除实例使用的模型。
- 本地历史数据库都是测试数据，可以清理旧实例数据，不做兼容补全。

## 已比较方案

### 方案一：manager-only 模型治理

这是推荐方案。manager 保存组织可用模型和实例当前模型，new-api 只作为模型列表来源和推理网关。

优点：

- 符合“new-api 不做权限控制”的要求。
- 改动集中在 manager 的组织、实例和 OpenClaw 初始化链路。
- 不需要补 new-api token models 更新接口。
- 与当前 `CreateAPIKey` 传空 `Models` 的行为兼容。

代价：

- 如果有人绕过 manager 直接使用实例 token 调其它模型，new-api 不会拦截。

### 方案二：manager-only 加用量异常告警

在方案一基础上，使用 usage logs 检测某实例 token 是否调用了非当前 `app.model_id` 的模型。

优点是能发现绕过 manager 的调用；代价是要增加额外日志比对和页面提示。本期不做。

### 方案三：new-api token 模型权限同步

创建或修改实例模型时同步更新 new-api token 的 `models` 权限。

优点是外部绕过 manager 也会被 new-api 拦截；缺点是当前 new-api client 只有创建 token 和状态切换能力，
还需要新增 token 更新接口，并且用户已明确 new-api 侧不需要做权限控制。本期不采用。

## 数据模型

新增迁移建议命名为 `000015_app_model_governance`。

`organizations` 增加：

```sql
enabled_models jsonb NOT NULL DEFAULT '[]'::jsonb
```

约束：

- 必须是 JSON array。
- service 层保证创建和更新时至少包含一个模型。
- service 层保证每个模型来自实时 new-api 模型列表。

`apps` 增加：

```sql
model_id text NOT NULL
```

约束：

- 创建实例时必填。
- service 层保证 `model_id` 属于所属组织 `enabled_models`。
- 未删除实例参与组织模型移除冲突检查。

历史数据处理：

- 本地历史数据视为测试数据，不为旧实例猜测模型。
- 迁移时清理旧 app 相关测试数据，避免新增 `apps.model_id NOT NULL` 时写入错误默认值。
- 如果实现时发现硬删 app 会受外键关系影响过大，可以改为软删旧 app 并填充固定保留值 `__deleted_legacy__`；
  service 必须继续排除软删 app，避免该保留值进入任何业务选择。
- 组织和用户可以保留，组织默认 `enabled_models=[]`。平台管理员必须编辑组织并选择模型后才能创建新实例。

## new-api 模型列表

manager 后端新增模型列表 client 能力。优先调用 new-api Dashboard 模型接口：

```text
GET /api/models
```

该接口返回 channel ID 到模型列表的映射，manager 将其扁平化、去重、排序后返回前端。

兼容兜底：

```text
GET /v1/models
```

这是 OpenAI 兼容模型列表接口。若 `/api/models` 在部署版本不可用，可以使用该接口解析 `data[].id`。

参考：

- <https://docs.newapi.ai/en/api/get-available-models-list/>
- <https://docs.newapi.pro/en/docs/guide/feature-guide/user/api>

## API 设计

新增：

```text
GET /api/v1/models
```

响应：

```json
{
  "models": [
    { "id": "qwen2.5:7b", "name": "qwen2.5:7b" }
  ]
}
```

`CreateOrganizationRequest` 增加：

```json
{
  "enabled_models": ["qwen2.5:7b", "deepseek-r1:14b"]
}
```

`OrganizationRequest` 增加同名字段，用于编辑组织可用模型。

`OrganizationResult` 增加：

```json
{
  "enabled_models": ["qwen2.5:7b"]
}
```

`OnboardMemberRequest` 和 `CreateMemberAppRequest` 增加：

```json
{
  "model_id": "qwen2.5:7b"
}
```

`AppResult` 增加：

```json
{
  "model_id": "qwen2.5:7b"
}
```

新增实例模型修改接口：

```text
PATCH /api/v1/apps/{appId}/model
```

请求：

```json
{
  "model_id": "deepseek-r1:14b"
}
```

响应：

```json
{
  "app": { "...": "service.AppResult" },
  "restart_job_id": "...",
  "requires_restart": true
}
```

该接口语义是“保存模型并在需要时提交重启任务”。如果应用尚未创建容器，仍更新模型，
但返回 `restart_job_id=""` 且 `requires_restart=false`，表示下次初始化或启动时生效。

## 权限

权限谓词继续集中在 `internal/auth/authorizer.go`。

- 平台管理员可以创建和编辑组织可用模型。
- 组织管理员可以在本组织创建实例并选择组织 allowlist 内的模型。
- 平台管理员和本组织管理员可以为已有成员创建实例并选择模型。
- 可触发实例运行操作的角色可以修改实例模型；普通组织成员是否可修改自己的实例模型，沿用现有 runtime 操作权限判断。
- service 层不新增本地 `canX` 函数。

## 后端流程

### 组织创建

1. handler 绑定 `enabled_models`。
2. service 校验平台管理员权限。
3. 调 new-api 实时拉取模型列表。
4. 校验 `enabled_models` 非空、去重后均存在于实时列表。
5. 写入组织、new-api user、组织管理员。
6. 返回包含 `enabled_models` 的组织结果。

如果 new-api 模型列表不可用，直接返回错误，不创建组织。

### 组织更新

1. service 拉取实时模型列表并校验新 `enabled_models`。
2. 对比旧模型列表，找出被移除的模型。
3. 查询本组织未删除 app 是否仍使用这些模型。
4. 若存在使用者，返回 409，错误信息包含模型名和使用数量。
5. 更新组织基础资料和模型列表。

### 创建实例

`OnboardMember` 与 `CreateAppForMember` 都增加 `ModelID` 入参：

1. 加载组织。
2. 校验组织状态可用。
3. 校验 `ModelID` 属于 `org.enabled_models`。
4. 写入 app 时同步写入 `model_id`。
5. 创建 channel binding、audit log 和 `app_initialize` job。

如果前端未传 `model_id`，service 返回明确校验错误。默认选择由前端完成，避免后端静默选错。

### 应用初始化

`app_initialize` 使用 `app.model_id` 构造 OpenClaw 模型配置：

- `agents.defaults.model = app.model_id`
- `models.providers[provider].models = [{ id: app.model_id, name: app.model_id }]`

`provider` 和 `baseUrl` 仍来自 `openclaw.llm.default_provider` 与 `openclaw.llm.base_url`。
`openclaw.llm.default_model` 只作为旧配置项保留，不再决定实例模型。

`CreateAPIKey` 继续传：

```go
Models: []string{}
```

表示 new-api token 不限制模型。

### 修改实例模型

新增 service 方法，例如 `UpdateAppModel(ctx, principal, appID, modelID)`：

1. 加载 app 和所属组织。
2. 校验调用方有权修改该实例运行配置。
3. 校验 `modelID` 属于 `org.enabled_models`。
4. 在事务中更新 `apps.model_id`、写审计、创建 `app_restart_container` job。
5. job notifier 入队；notifier 失败不影响数据库事实，scheduler 兜底。
6. 返回更新后的 `AppResult`、`restart_job_id` 和 `requires_restart`。

如果 app 没有容器，仍允许更新 `model_id`，但不创建重启 job；下一次初始化或启动时生效。前端提示“模型已保存，实例未运行，启动后生效”。

## 前端交互

### 组织创建和编辑

平台组织页表单增加“可用模型”多选。

- 打开表单时请求 `GET /api/v1/models`。
- 加载中显示禁用态。
- 加载失败显示错误并禁用保存。
- 至少选择一个模型才能提交。
- 编辑组织时已有模型默认勾选。
- 保存返回 409 时显示后端错误，例如“模型 qwen2.5:7b 已被 2 个实例使用，请先切换实例模型”。

### 创建成员并初始化实例

`CreateMemberPage` 的“实例信息”区域增加“模型”下拉框：

- 选项来自当前组织 `enabled_models`。
- 默认选择第一个模型。
- 组织没有可用模型时禁用提交，提示联系平台管理员配置模型。
- 提交体带 `model_id`。

### 为已有成员创建实例

成员列表中的“创建新实例”表单增加同样的模型下拉框。

### 实例详情修改模型

应用详情概览页增加“模型设置”区域：

- 展示当前模型。
- 下拉框展示组织 allowlist 中的其它模型。
- 按钮文案为“保存并重启实例”。
- 成功后展示重启 job id，并沿用现有 job/runtime 状态查看重启结果。
- 如果实例未运行，按钮文案可显示“保存模型”，成功后提示启动后生效。

## 错误处理

- 模型列表拉取失败：组织创建/编辑不可提交。
- 组织模型为空：创建/更新组织返回 400。
- 模型不存在于 new-api 实时列表：返回 400。
- 组织移除正在使用的模型：返回 409。
- 创建实例模型不属于组织 allowlist：返回 400。
- 修改实例模型不属于组织 allowlist：返回 400。
- 重启任务创建失败：事务回滚，避免出现模型已改但无重启任务的半成功状态。
- worker 重启失败：沿用现有 job failed 和 runtime 错误展示；用户可手动重试重启。

## OpenAPI

修改 handler 请求体、响应类型和新增路由后必须运行：

```text
make openapi-gen
make web-types-gen
```

`openapi/openapi.yaml` 与 `web/src/api/generated.ts` 作为生成产物随实现提交，不手工编辑。

## 测试

后端单测覆盖：

- 组织创建必须传非空 `enabled_models`。
- 组织创建在模型列表拉取失败时阻止写库。
- 组织更新不能移除被未删除实例使用的模型。
- 创建实例时 `model_id` 必须属于组织 allowlist。
- 修改实例模型会更新 app、写审计、创建 restart job。
- 未运行实例修改模型不创建 restart job。
- `app_initialize` 使用 `app.model_id` 注入 OpenClaw。
- new-api token 创建仍传空 `Models`。

前端单测覆盖：

- 组织表单模型加载成功、加载失败和保存禁用状态。
- 创建实例模型下拉只展示组织可用模型。
- 平台管理员复建实例表单提交 `model_id`。
- 实例详情展示当前模型并提交模型修改。
- 组织编辑移除使用中模型时展示 409 错误。

浏览器验证覆盖：

- 平台管理员登录，打开组织创建表单，确认实时模型列表出现。
- 模型列表失败时表单不可提交。
- 创建组织并选择模型 A/B。
- 组织管理员创建成员实例，选择模型 A，确认实例详情显示模型 A。
- 修改该实例模型为模型 B，确认产生重启任务，重启后详情仍显示模型 B。
- 创建或切换到另一个实例，确认不同实例模型独立显示。
- 发起一次实际调用，确认 OpenClaw 使用当前实例模型完成响应。
- 平台管理员尝试移除正在使用的模型，确认被 409 阻止。
- 切换实例模型后，再移除旧模型成功。

## 交付检查

- 数据库迁移、sqlc、service、handler、前端 hooks 和页面同步更新。
- `make openapi-gen` 与 `make web-types-gen` 后生成产物入仓。
- 相关 Go 单测、前端单测和浏览器验证完成。
- 工作区不混入 `.superpowers/` 等浏览器伴随临时文件。
