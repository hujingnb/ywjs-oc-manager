# 设计文档：实例详情页集成 Hermes 定时任务管理

## 背景

Hermes runtime 已内置 Cron 调度能力，支持任务 CRUD、暂停/恢复、立即触发、执行输出落盘和调度器状态查询。当前 oc-manager 只集成了 Hermes Kanban 任务看板，实例详情页没有 Cron 管理入口，用户需要进入容器或其他 UI 才能管理定时任务。

现有任务看板集成已经形成了稳定模式：

- runtime 镜像内置 `oc-kanban` 适配命令，对 manager 暴露稳定 JSON 信封；
- manager 后端通过 `ContainerExecJSON` 调用容器内命令；
- service 层做 app 定位、权限校验、参数白名单和错误映射；
- 前端在实例详情 tab 内提供左侧列表、右侧详情的工作区。

Cron 管理采用同样模式，不让 manager 直接依赖上游 `hermes cron` 的文本输出、文件布局细节或 API Server 8642 的启用状态。所有版本适配细节都隐藏在各 runtime variant 自带的 `oc-cron` 命令中，manager 只消费统一输入输出契约，便于未来多个 Hermes 版本共存。

## 目标

- 在实例详情页新增「定时任务」tab。
- 支持 Hermes Cron 任务列表、详情、新建、编辑、暂停、恢复、立即运行、删除。
- 支持调度器状态/健康概览：是否可用、active jobs、下一次执行、最近异常等。
- 支持查看任务执行历史和 `cron/output/<job_id>/` 下的 markdown 输出。
- 读写权限与 Kanban 一致：能查看实例详情的角色都能管理该实例的 Cron。
- 字段权限按角色区分：
  - 所有可见角色：基础字段 + `script` / `no_agent` / `workdir`；
  - `platform_admin`：额外可见并可写全量高级字段，如 `skills`、`model`、`provider`、`base_url`。
- manager 不持久化 Hermes Cron 快照，Cron 数据仍由 Hermes 容器内 `/opt/data/cron/` 管理。

## 非目标

- 首版不接 Hermes API Server 8642，不新增容器内 HTTP 代理。
- 首版不做 SSE 实时流；列表、状态和历史通过轮询与 mutation 后缓存失效刷新。
- 首版不把 `jobs.json`、执行输出或调度器状态写入 manager 数据库。
- 首版不做复杂自然语言调度表达式转换；按 Hermes 原生支持的 cron/interval/once 格式输入。

## 总体方案

采用「runtime 适配命令 + manager 稳定代理 + 前端实例 tab」三层架构。

```text
AppCronTab.vue
  → /api/v1/apps/:appId/hermes/cron/*
  → HermesCronService
  → runtime.Adapter.ContainerExecJSON(...)
  → hermes container: oc-cron ...
  → hermes cron / /opt/data/cron/*
```

### 方案取舍

1. **推荐方案：内置 `oc-cron` 适配命令**
   - 与 `oc-kanban` 架构一致，manager 只依赖稳定契约。
   - 上游 Hermes CLI/API 的版本差异集中在 runtime 镜像内处理；每个 Hermes 版本或 variant 可维护自己的 `oc-cron` 实现。
   - 缺点是需要同时修改 runtime 镜像、Go 后端、OpenAPI 和前端。

2. **manager 直接执行 `hermes cron`**
   - 初始代码少，但会把 CLI 文本解析和文件结构泄露到 Go service。
   - 后续 Hermes 版本变动会放大维护成本。

3. **走 Hermes API Server 8642**
   - REST 语义清晰，但当前项目缺少容器内 HTTP 代理抽象。
   - 还要处理 `API_SERVER_ENABLED`、`API_SERVER_KEY`、版本和健康探测问题。

本设计选择方案 1。

## Runtime：`oc-cron` 契约

新增 `runtime/hermes/hermes-main/oc-cron.py`，并在 Dockerfile 中安装到 PATH。契约文档放在 `runtime/hermes/cron-contract/SPEC.md`，结构参考 `kanban-contract`。

### 多版本兼容边界

`oc-cron` 是 Hermes Cron 的版本适配边界。每个 runtime variant 随镜像携带与自身 Hermes 版本匹配的 `oc-cron`，在命令内部处理上游 CLI 参数、输出格式、文件路径、错误文本和功能差异。manager 不读取 Hermes 版本后写分支，也不解析上游 `hermes cron` 原始输出。

manager 只依赖 `oc-cron` 的统一契约：

- 输入：固定 verb、flag 和 JSON/argv 语义；
- 输出：固定信封、错误码和 `CronJob` / `CronStatus` / `CronRunEntry` / `CronRunOutput` 类型；
- 能力发现：只通过 `capabilities.features` 和 `capabilities.verbs` 做 UI 降级，不把特定 Hermes 版本名写进业务逻辑；
- 契约演进：新增字段必须向后兼容；破坏性变更提升 contract major version，并由 manager 拒绝不兼容版本。

这样未来同一 manager 可以同时管理多个 Hermes runtime 版本：只要各镜像内的 `oc-cron` 对 manager 暴露同一 major 契约，后端和前端无需按实例版本分叉。

### 输出信封

非流式命令输出单行 JSON：

```json
{"ok": true, "data": {}}
```

失败时：

```json
{"ok": false, "error": {"code": "BAD_REQUEST", "message": "schedule is required"}}
```

错误码首版定义：

| code | 语义 |
|---|---|
| `BAD_REQUEST` | 参数非法，如 job id、script、repeat、输出文件名不合法 |
| `NOT_FOUND` | 任务或输出文件不存在 |
| `UNSUPPORTED` | 镜像不支持真实 Hermes Cron |
| `HERMES_CLI_FAILED` | 底层 `hermes cron` 执行失败 |
| `INTERNAL` | 输出解析失败、文件结构异常等适配层内部错误 |

### 命令

| 命令 | data |
|---|---|
| `capabilities` | 契约版本、`oc_cron_version`、Hermes 版本、支持的 verbs/features |
| `status` | 调度器健康、active job 数、next run、最近异常摘要 |
| `list --all` | 任务列表 |
| `show --id <job_id>` | 单个任务详情 |
| `create ...` | 创建任务并返回任务详情 |
| `edit --id <job_id> ...` | 编辑任务并返回任务详情 |
| `pause/resume/run/remove --id <job_id>` | 操作任务；remove 返回 `{ok:true}` |
| `history --id <job_id>` | 输出历史列表，含 synthetic 运行记录 |
| `output --id <job_id> --file <name>` | 读取某次 markdown 输出 |

### 数据规整

`oc-cron` 对外规整为稳定类型：

- `CronJob`：`id`、`name`、`prompt`、`schedule`、`repeat`、`enabled`、`state`、`created_at`、`next_run_at`、`last_run_at`、`last_status`、`last_error`、`last_delivery_error`、`deliver`、`script`、`no_agent`、`workdir`、`skills`、`model`、`provider`、`base_url` 等。
- `CronStatus`：`available`、`gateway_running`、`active_jobs`、`next_run_at`、`next_job_id`、`tick_seconds`、`pid`、`message`。
- `CronRunEntry`：`job_id`、`file_name`、`run_time`、`size`、`has_output`、`synthetic`、`status`、`error`。
- `CronRunOutput`：`job_id`、`file_name`、`run_time`、`content`。

列表和详情优先读取 Hermes 权威数据；写操作通过 `hermes cron create/edit/pause/resume/run/remove` 完成，随后重新读取任务数据返回给 manager。`status` 调用 `hermes cron status` 获取 gateway/cron 运行状态，并用任务元数据补充 `active_jobs`、`next_run_at`、最近错误等摘要；如果上游状态文本格式变化，`oc-cron` 只降级 `message` 字段，不把文本解析细节泄露给 manager。执行历史读取 `cron/output/<job_id>/`，当 `last_run_at` 存在但没有 markdown 输出时，生成文件名为 `__scheduler_metadata__.md` 的 synthetic 记录，说明调度器记录了运行但无输出文件。

### 参数安全

- `job_id` 仅允许 `[A-Za-z0-9_-]{1,64}`。
- 输出文件名仅允许单段文件名，不允许 `/`、`\`、`..`，且必须是 `.md` 或 synthetic 文件名。
- `script` 必须是相对文件名，不允许绝对路径和 `..`。
- `repeat` 必须是正整数或空值；空值表示无限重复。
- 自由文本限制：`name` 最长 200 字符，`schedule` 最长 200 字符，`prompt` 最长 5000 字符，`deliver` / `script` / `workdir` 最长 512 字符。
- 所有调用都以 argv 数组传递，不拼 shell。

## Manager 后端

### 权限

在 `internal/auth/authorizer.go` 新增：

- `CanViewAppCron(p, appOrgID, appOwnerUserID) bool`
- `CanManageAppCron(p, appOrgID, appOwnerUserID) bool`

两者首版均委托 `CanViewApp`，与 Kanban 权限保持一致。service 包不定义本地 `canX` 函数，也不在 handler/service 内联角色判断。

### Service

新增 `internal/service/hermes_cron.go` 和 `internal/service/hermes_cron_types.go`。

`HermesCronService` 复用 Kanban 的 app 定位思路：

- 查询 app 的 `org_id`、`owner_user_id`、`runtime_node_id`、`container_id`、stub 标识；
- 读操作走 `CanViewAppCron`；
- 写操作走 `CanManageAppCron`；
- stub 镜像返回 `ErrCronNotSupported`；
- 容器未运行返回 `ErrCronRuntimeUnavailable`；
- 调用 `oc-cron` 并解析信封。

写输入结构分两层：

- service input 表达 `oc-cron` 支持的完整字段；
- handler 根据角色剥离非 `platform_admin` 不允许写的高级字段，再传入 service。

非平台管理员可写：

- `name`
- `schedule`
- `prompt`
- `deliver`
- `repeat`
- `script`
- `no_agent`
- `workdir`

仅平台管理员可写：

- `skills`
- `model`
- `provider`
- `base_url`
- 其他 `oc-cron` 后续暴露的高级字段

### API

路由前缀：

```text
/api/v1/apps/:appId/hermes/cron
```

首版端点：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/capabilities` | Cron 适配命令能力 |
| `GET` | `/status` | 调度器状态 |
| `GET` | `/jobs` | 任务列表，默认包含暂停任务 |
| `POST` | `/jobs` | 创建任务 |
| `GET` | `/jobs/:jobId` | 任务详情 |
| `PATCH` | `/jobs/:jobId` | 编辑任务 |
| `DELETE` | `/jobs/:jobId` | 删除任务 |
| `POST` | `/jobs/:jobId/pause` | 暂停 |
| `POST` | `/jobs/:jobId/resume` | 恢复 |
| `POST` | `/jobs/:jobId/run` | 立即触发 |
| `GET` | `/jobs/:jobId/history` | 执行历史 |
| `GET` | `/jobs/:jobId/output/:fileName` | 输出 markdown |

新增 handler DTO 放 `internal/api/handlers/dto.go`，并通过 swag 注解生成 OpenAPI。修改后必须运行：

```bash
make openapi-gen
make web-types-gen
```

### 错误映射

新增 service sentinel error：

- `ErrCronForbidden`
- `ErrCronRuntimeUnavailable`
- `ErrCronNotSupported`
- `ErrCronCLI`
- `ErrCronOutputInvalid`
- `ErrCronBadRequest`

HTTP 映射：

| service error | HTTP | code |
|---|---:|---|
| `ErrCronForbidden` | 403 | `CRON_FORBIDDEN` |
| `ErrCronRuntimeUnavailable` | 503 | `RUNTIME_NOT_AVAILABLE` |
| `ErrCronNotSupported` | 503 | `CRON_NOT_SUPPORTED_ON_STUB` |
| `ErrCronBadRequest` | 400 | `CRON_BAD_REQUEST` |
| `ErrCronCLI` | 502 | `CRON_CLI_ERROR` |
| `ErrCronOutputInvalid` | 502 | `CRON_OUTPUT_INVALID` |

## 前端设计

### 路由与 tab

在实例详情页新增 tab：

```ts
{ path: 'cron', label: '定时任务' }
```

路由新增：

```ts
{ path: 'cron', component: AppCronTab, props: true }
```

该 tab 对所有能进入实例详情页的角色可见，与 Kanban 一致。

### 页面结构

`AppCronTab.vue` 参考 `AppKanbanTab.vue` 的布局：

- 顶部工具栏：调度器状态摘要、搜索、筛选、刷新、新建按钮。
- 左侧列表：任务名称、调度表达式、状态、投递目标、上次/下次执行、最近错误。
- 右侧详情：选中任务的完整字段、操作按钮、执行历史、markdown 输出预览。

交互规则：

- 点击左侧任务，把 `job` 写入 URL query，刷新后保持选中态。
- 新建/编辑使用 modal 表单。
- 暂停/恢复/立即运行/删除在右侧详情触发；删除需要二次确认。
- 运行历史点击某条输出后，在详情区下方展示 markdown 原文预览。
- stub 实例显示降级空状态：该实例运行的是本地 dev 镜像，定时任务不可用。
- 容器未运行显示可操作提示：请先到运行时 tab 启动实例。

### 前端 hooks

新增 `web/src/api/hooks/useCron.ts`：

- `useCronCapabilitiesQuery(appId)`
- `useCronStatusQuery(appId)`
- `useCronJobsQuery(appId, filters)`
- `useCronJobQuery(appId, jobId)`
- `useCronHistoryQuery(appId, jobId)`
- `useCronOutputQuery(appId, jobId, fileName)`
- `useCreateCronJob(appId)`
- `useUpdateCronJob(appId)`
- `useCronJobAction(appId)`：pause/resume/run/delete

查询 key 统一以 `['cron', ...]` 为前缀。任务列表与状态轮询 5-10 秒；mutation 成功后失效 jobs/status/history 相关缓存。

### 字段显隐

表单基础字段：

- 名称
- 调度表达式
- Prompt
- 投递目标
- 重复次数
- script
- no_agent
- workdir

平台管理员额外字段：

- skills
- model
- provider
- base_url

前端隐藏高级字段只是 UX；后端 handler 必须按 `principal.Role` 再次 strip，避免绕过 UI 直接提交。

## 测试策略

### Runtime pytest

新增 `runtime/hermes/hermes-main/tests/test_cron_contract.py`：

- 成功信封和失败信封格式。
- `capabilities` 返回契约版本和 verbs。
- `list/show` 对 `jobs.json` 字段规整。
- `create/edit/pause/resume/run/remove` 生成正确 `hermes cron` argv。
- `history` 读取 markdown 输出并按时间倒序。
- `history` 对无输出但有 `last_run_at` 的任务生成 synthetic 记录。
- 非法 `job_id`、`script`、输出文件名返回 `BAD_REQUEST`。

### Go 单元测试

新增或扩展：

- `internal/auth/authorizer_test.go`：Cron 读写权限与 Kanban 同角色矩阵。
- `internal/service/hermes_cron_test.go`：resolve、stub、容器未运行、信封解析、错误码映射、参数校验、写命令 argv、字段解析。
- `internal/api/handlers/hermes_cron_test.go`：绑定请求体、角色字段 strip、错误响应、路由方法。

### 前端测试

新增：

- `AppDetailPage.spec.ts`：显示「定时任务」tab。
- `AppCronTab.spec.ts`：列表渲染、点击任务显示右侧详情、stub 降级、调度器状态展示。
- `CronJobFormModal.spec.ts`：普通角色和平台管理员字段显隐。
- `useCron` hooks 可用现有 API client 测试风格覆盖关键路径。

### 浏览器验证

功能完成后必须用真实浏览器验证：

1. 登录平台管理员，进入实例详情「定时任务」tab。
2. 看到调度器状态和任务列表。
3. 新建基础任务，列表出现任务。
4. 点击任务，右侧显示详情和历史区域。
5. 执行暂停、恢复、立即运行、编辑、删除。
6. 查看一次 markdown 输出。
7. 切换到非平台管理员账号，确认基础字段可见，高级字段不可见。

## 影响范围

- runtime Hermes 镜像：新增 `oc-cron` 和契约测试。
- manager 后端：新增 Cron service、types、handler、DTO、router、错误映射、权限谓词。
- OpenAPI：新增 `/apps/{appId}/hermes/cron/*` 端点。
- 前端：新增 API hooks、Cron tab 页面和表单/详情组件。
- 不涉及数据库 schema 变更。
- 不涉及 manager 本地持久化。

## 验收标准

- 实例详情页出现「定时任务」tab，布局为左侧列表、右侧详情/历史。
- 所有能查看实例详情的角色都能查看并管理 Cron 任务。
- `platform_admin` 能看到全量高级字段；其他角色只能看到基础 + `script/no_agent/workdir`。
- stub 镜像、容器未运行、Cron 命令失败时有明确错误或降级提示。
- OpenAPI 与前端 generated types 已同步。
- runtime pytest、Go 相关单测、前端相关 Vitest 通过。
- 完成真实浏览器功能验证。
