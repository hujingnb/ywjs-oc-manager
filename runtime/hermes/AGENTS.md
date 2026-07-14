# AGENTS.md

本文件约束 `runtime/hermes/**` 下所有 Hermes runtime variant。更深层目录如果新增
`AGENTS.md`，以更近层级文件为准。

## 基本原则

- 每个 Hermes variant 必须自包含：镜像构建、入口脚本、renderer、migrator、ocops 能力、测试和版本契约都放在自己的 variant 目录内。
- 当前构建流程会把共享 `ocops-contract` 契约注入镜像；自包含要求最终镜像包含运行所需文件，不要求 variant 源目录物理 vendoring 这些共享契约。
- 不复用 `main`、`master`、`latest`、`dev` 等浮动上游 ref 作为生产镜像来源；生产镜像必须能追溯到固定 Hermes ref、variant 版本和 manager 源码 commit。
- 不把 app 运行时数据写进镜像层；镜像只提供代码、工具、默认模板和内置能力。
- 不在 renderer 中覆盖 Hermes 长期记忆；长期记忆由 Hermes 进程或其 memory/user_profile 能力维护。
- 新增或修改 variant 时，优先复用当前 variant 的目录结构和测试习惯，不做无关重排。

## 目录与版本约定

- variant 目录命名形如 `hermes-v2026.5.16`。
- 目录名中的版本必须与同目录 `version.txt` 的版本一致，但 `version.txt` 只写 `v2026.5.16` 这类裸版本。
- 每个 variant 必须包含：
  - `Dockerfile`
  - `version.txt`
  - `CONTRACT.md`
  - `oc-entrypoint.py`
  - `renderer/`
  - `migrator/`
  - `ocops/`
  - `tests/`
- `CONTRACT.md` 记录该 variant 的上游 ref、安装方式、对外命令、迁移说明和版本特有差异。
- 顶层 `runtime/hermes/README.md` 只说明通用目录和构建入口；variant 细节放进对应 `CONTRACT.md`。

## `/opt/data` 持久化边界

Hermes app 的运行时数据根目录是 `/opt/data`。镜像更新时必须区分以下数据类别。
本节定义的 S3 持久化、恢复与文件过渡约束**仅适用于**
`app_type='standard'` 的应用。`app_type='aicc'` 是无状态运行时：每次 Pod 启动均由
初始化容器通过 bootstrap 写入启动所需 manifest，并由当前 AICC 镜像重新生成运行时文件；
其 `/opt/data` 不跨 Pod 保留，不得执行 `oc-restore`、`oc-sync` 或 `oc-presync`，也不得
因 S3 恢复、同步或临时凭证失败阻塞启动。

### 必须保留的长期记忆

以下路径属于用户长期偏好、稳定事实和用户画像，必须通过 app 级持久化机制保存到 S3，并在新 pod 启动前恢复：

- `/opt/data/memories/`
- `/opt/data/MEMORY.md`
- `/opt/data/USER.md`

这些路径不存在时视为首启或尚未产生长期记忆，不应阻塞启动。

### 默认保留的 app 运行时数据

除长期记忆外，以下路径也是 app 运行过程中产生的非会话数据。除非后续契约明确把某类路径归入可清理数据，否则应按保留优先处理，不得被镜像升级或新 session 清理误删：

| 路径 | 数据类别 | 保留要求 |
|---|---|---|
| `/opt/data/workspace/` | 用户工作区文件 | 保留用户上传、生成或编辑的工作成果。 |
| `/opt/data/cron/` | Hermes Cron 任务与运行输出 | 保留定时任务定义和可查询的历史输出。 |
| `/opt/data/kanban.db` | Hermes kanban 状态 | 通过 SQLite 一致性快照保存；不得被会话快照清理误删。 |
| `/opt/data/weixin/accounts/` | 渠道登录凭证 | 保留渠道绑定状态；不得写入镜像层。 |
| `/opt/data/skills/` 中的自创 skill | 用户或 app 运行期创建的 skill | 保留自创 skill；`skills/oc-kb/` 是 renderer 输出，受当前镜像重渲染，内置或受管 skill 按 variant 契约处理。 |
| `/opt/data/skills/.bundled_manifest` | 镜像内置 skill 基线 | 与自创 skill 一起保留，避免 pod 重建后把自创 skill 误判为内置。 |
| `/opt/data/.oc-state.json` | variant 状态锚点 | 保留上次渲染和迁移状态，供新镜像判断是否需要 migrator。 |

`sessions/` 与 `state.db*` 是可以清理的会话快照；除这些已明确分类的路径外，其他 app 运行时数据默认保留，只有在契约中显式归类后才能清理。`cache/`、`logs/`、`gateway_state.json`、`gateway.lock`、`sandboxes/` 当前视为缓存、诊断或进程态数据，不纳入默认 S3 恢复闭环；未来若把其中任一路径升级为业务真相源，必须同步扩展 ops sync/restore 和 `runtime/ops/README.md` 的 S3 映射。

### 可以清理的会话快照

以下路径属于会话级状态，可能冻结旧 `SOUL.md`、旧模型配置或旧平台规则。配置变更或镜像升级需要新 session 时，可以清理：

- `/opt/data/sessions/`
- `/opt/data/state.db`
- `/opt/data/state.db-shm`
- `/opt/data/state.db-wal`

清理逻辑必须只命中会话快照，不得扩大到长期记忆、workspace、kanban 或渠道凭证。

### 启动时重渲染的文件

以下路径由 `oc-entrypoint` 从 `/opt/oc-input` 和 Hermes 自管数据生成，新镜像启动时应由当前镜像重渲染：

- `/opt/data/config.yaml`
- `/opt/data/SOUL.md`
- `/opt/data/.env`
- `/opt/data/skills/oc-kb/`

renderer 只拥有这些由它生成的输出；不得把 `MEMORY.md`、`USER.md` 或 `memories/` 当作 renderer 输出。

### 敏感数据

以下敏感数据不得写入 S3 或镜像层：

- new-api `api_key`
- app control token
- RAGFlow API key
- manager 内部管理凭证

这些值由 manager bootstrap 通过认证通道下发，DB 加密字段是持久真相源。

## S3 保存与恢复（仅 `app_type='standard'`）

app 级运行时数据使用 `apps/<appID>/` S3 前缀。

- `runtime/ops` 镜像负责通用恢复和同步命令：`oc-restore`、`oc-sync`、`oc-presync`。
- `oc-restore` 在 initContainer 中运行，负责调用 bootstrap、写入 `/opt/oc-input`，并把 S3 中的 app 数据恢复到 `/opt/data`。
- `oc-sync` 在 sidecar 中运行，负责周期性同步 `/opt/data` 中需要保留的非敏感数据。
- `oc-presync` 在 preStop hook 中运行，负责旧 pod 终止前做最后一次同步。
- 长期记忆必须纳入 `oc-sync` 与 `oc-presync` 的上传范围，并纳入 `oc-restore` 的恢复范围。
- Cron、kanban、`.oc-state.json` 与 `skills/.bundled_manifest` 属于非会话运行时状态，也必须纳入同步和恢复范围。
- S3 恢复失败时应让 initContainer 失败，不得静默启动一个空记忆实例。
- `sessions/` 与 `state.db*` 可以同步为会话快照，但不能被描述或处理为长期记忆。

`app_type='aicc'` 不适用本节：其初始化容器只完成 bootstrap manifest 与必要目录的
初始化，不恢复 `/opt/data`；Pod 中不创建同步 sidecar，也不配置调用 `oc-presync` 的
preStop hook。

## 对外暴露能力契约

每个 Hermes variant 镜像必须对 manager 和 Kubernetes 暴露同一组能力。新增
variant 只能在保持兼容的前提下扩展，不能删除、改名或改变既有输入输出语义。
底层 Hermes 上游能力缺失时，HTTP 接口必须返回 `UNSUPPORTED`，不能让端点消失。

### 镜像级接口

| 接口 | 输入 | 输出 / 行为 | 稳定要求 |
|---|---|---|---|
| `ENTRYPOINT` | `/opt/oc-input/manifest.yaml`、`/opt/data`、环境变量 | 执行 `oc-entrypoint`，完成文件过渡、渲染后 `exec hermes gateway run` | 所有 variant 保持相同入口语义。 |
| `oc-healthcheck` | 无；读取本机 Hermes gateway 状态 | 退出码 `0` 表示可用，非 `0` 表示不可用 | Kubernetes healthcheck 只依赖退出码。 |
| `oc-kb` | CLI 参数和 `/opt/data/.env` 中的 manager runtime 配置 | 调 manager runtime API 做知识库检索/导入 | 不直接持有 RAGFlow API key，不绕过 manager。 |
| `ocops.server` | HTTP/SSE，请求头 `Authorization: Bearer ${OC_OPS_TOKEN}` | 端口通常为 `8080`，提供下表全部 REST/SSE 接口 | 随 Hermes variant 版本走，直接操作该 variant 的 Python 包、Hermes 命令和 `/opt/data`。 |
| `ocops-contract` | 构建期注入，镜像内路径 `/usr/local/lib/ocops/contract/` | `SPEC.md` + JSON Schema | 是接口层机器契约；构建期测试必须校验路由、schema 与实现一致。 |

`runtime/ops` 镜像不跟 Hermes variant 版本走。它是平台基础设施镜像，跟
manager bootstrap、S3 key 约定和 k8s 编排契约保持兼容。`ocops.server`
当前必须保留在 Hermes 镜像内；若未来拆到独立 ops 镜像，需要先定义跨镜像读写
`/opt/data` 的稳定 ABI。

### ocops HTTP 通用规则

- 除 `GET /healthz` 外，所有接口必须校验 Bearer `OC_OPS_TOKEN`。
- 非流式成功响应直接返回业务 JSON payload，不包 `{ok,data}` 信封。
- 业务失败统一返回 HTTP 状态码 + `{"code":"...","message":"..."}`。
- 已知错误码：`BAD_REQUEST`、`UNAUTHORIZED`、`NOT_FOUND`、`UNSUPPORTED`、
  `HERMES_CLI_FAILED`、`INTERNAL`。
- SSE 成功帧使用 `data: <json>\n\n`；流内业务失败使用
  `event: error\ndata: {"code":"...","message":"..."}\n\n`。
- 精确 JSON Schema 位于 `runtime/hermes/ocops-contract/schema/**`，本节表格描述
  语义；schema 是机器校验依据。

### ocops Core 接口

| 方法 | 路径 | 输入 | 输出 |
|---|---|---|---|
| `GET` | `/healthz` | 无鉴权 | 文本 `ok`。 |
| `GET` | `/oc/info` | 无 body | 镜像身份：`variant`、`hermes_upstream_ref`、`built_at`、`oc_entrypoint_version`，允许附加只读元数据。 |
| `GET` | `/oc/doctor` | 无 body | 诊断快照：`variant`、`last_render_at`、`manifest_sha256`、`hermes_pid`、`hermes_status`、`issues`。 |

### ocops Channel 接口

当前稳定 channel 名称至少包含 `weixin`。未知 channel 返回 `BAD_REQUEST`。

| 方法 | 路径 | 输入 | 输出 |
|---|---|---|---|
| `GET` | `/oc/channels/{channel}/status` | path `channel` | 未绑定：`{"channel":"weixin","bound":false}`；已绑定：额外含 `account_id`。 |
| `POST` | `/oc/channels/{channel}/unbind` | path `channel` | 幂等解绑，返回 `{"status":"unbound"}`。 |
| `POST` | `/oc/channels/{channel}/login` | path `channel` | SSE：`{"event":"qrcode","url":"..."}`，最终 `{"event":"bound"}`、`{"event":"timeout"}` 或 `{"event":"failed","reason":"..."}`。 |

### ocops Cron 接口

Cron 接口以 `/opt/data/cron/jobs.json` 和 `/opt/data/cron/output/` 为文件真相源，
由当前 variant 适配底层 Hermes Cron 差异。

| 方法 | 路径 | 输入 | 输出 |
|---|---|---|---|
| `GET` | `/oc/cron/capabilities` | 无 | `contract_version`、`oc_cron_version`、`hermes_version`、`variant`、`verbs`、`features`。 |
| `GET` | `/oc/cron/status` | 无 | 调度状态：`available`、`gateway_running`、`active_jobs`、`next_run_at`、`next_job_id`、`message` 等。 |
| `GET` | `/oc/cron/jobs` | query `all=true|false|1|0|yes|no` 可选 | `CronJob[]`；默认隐藏 disabled/removed 任务。 |
| `POST` | `/oc/cron/jobs` | JSON：`name`、`schedule` 必填；可选 `prompt`、`deliver`、`repeat`、`script`、`no_agent`、`workdir`、`skills`、`model`、`provider`、`base_url` | 新建后的 `CronJob`。 |
| `GET` | `/oc/cron/jobs/{id}` | path `id` | 指定 `CronJob`；不存在返回 `NOT_FOUND`。 |
| `PATCH` | `/oc/cron/jobs/{id}` | path `id`；JSON 字段同 create，另有 `agent`、`clear_skills` | 更新后的 `CronJob`。 |
| `POST` | `/oc/cron/jobs/{id}/toggle` | path `id`；JSON `{"enabled":true|false}` | 切换后的 `CronJob`。 |
| `POST` | `/oc/cron/jobs/{id}/run` | path `id` | 触发后的 `CronJob`。 |
| `DELETE` | `/oc/cron/jobs/{id}` | path `id` | 成功返回 `204 No Content`。 |
| `GET` | `/oc/cron/jobs/{id}/history` | path `id` | `RunEntry[]`，列出 markdown 输出历史。 |
| `GET` | `/oc/cron/jobs/{id}/output` | path `id`；query `file=<markdown file>` | `RunOutput`：`job_id`、`file_name`、`run_time`、`content`。 |

### ocops Kanban 接口

Kanban 接口以 Hermes kanban 能力和 `/opt/data/kanban.db` 为真相源。底层
Hermes 不支持 kanban 时，除 capabilities 外的功能接口返回 `UNSUPPORTED`。

| 方法 | 路径 | 输入 | 输出 |
|---|---|---|---|
| `GET` | `/oc/kanban/capabilities` | 无 | `contract_version`、`oc_kanban_version`、`hermes_version`、`variant`、`verbs`、`features`。 |
| `GET` | `/oc/kanban/boards` | 无 | `Board[]`。 |
| `GET` | `/oc/kanban/tasks` | query `board` 可选，默认 `default`；`status`、`assignee` 可选 | `Task[]`。 |
| `POST` | `/oc/kanban/tasks` | JSON：`board`、`title`、`assignee` 必填；可选 `priority`、`body`、`skills`、`workspace`、`parent`、`max_retries` | 新建后的 `TaskDetail`。 |
| `GET` | `/oc/kanban/tasks/{id}` | path `id`；query `board` 可选 | `TaskDetail`。 |
| `GET` | `/oc/kanban/tasks/{id}/runs` | path `id`；query `board` 可选 | `Run[]`。 |
| `GET` | `/oc/kanban/stats` | query `board` 可选 | `Stats`。 |
| `POST` | `/oc/kanban/tasks/{id}/comment` | path `id`；JSON `body` 必填，`board` 可选 | 更新后的 `TaskDetail`。 |
| `POST` | `/oc/kanban/tasks/{id}/complete` | path `id`；JSON `board`、`result` 可选 | 更新后的 `TaskDetail`。 |
| `POST` | `/oc/kanban/tasks/{id}/block` | path `id`；JSON `reason` 必填，`board` 可选 | 更新后的 `TaskDetail`。 |
| `POST` | `/oc/kanban/tasks/{id}/unblock` | path `id`；JSON `board` 可选 | 更新后的 `TaskDetail`。 |
| `POST` | `/oc/kanban/tasks/{id}/archive` | path `id`；JSON `board` 可选 | 更新后的 `TaskDetail`。 |
| `POST` | `/oc/kanban/tasks/{id}/reassign` | path `id`；JSON `to` 必填，`board` 可选 | 更新后的 `TaskDetail`。 |
| `POST` | `/oc/kanban/tasks/{id}/reclaim` | path `id`；JSON `board` 可选 | 更新后的 `TaskDetail`。 |
| `GET` | `/oc/kanban/watch` | query `board` 可选 | SSE `Event` 帧：`task_id`、`kind`、`payload`、`created_at`、`run_id`。 |

### ocops Skills 接口

Skills 接口操作 `/opt/data/skills/`。内置 skill、自创 skill、manager 热装 skill
必须通过 `.oc-managed` 与 `.bundled_manifest` 区分，不得误删用户自创内容。

| 方法 | 路径 | 输入 | 输出 |
|---|---|---|---|
| `GET` | `/oc/skills` | 无 | `{"skills":[{"name":"...","managed":bool,"builtin":bool,"description":"..."}]}`。 |
| `POST` | `/oc/skills` | multipart form：`name` 字段 + `archive` 文件字段，归档为 tar 或 zip | `{"name":"..."}`。 |
| `POST` | `/oc/skills/reload` | 无 | Hermes API server reload 结果，通常含 `added`、`removed`、`total`。 |
| `DELETE` | `/oc/skills/{name}` | path `name` | 幂等删除，返回 `{"name":"..."}`。 |

## 启动流程

Hermes 主容器启动顺序必须保持：

1. 读取 `/opt/oc-input/manifest.yaml`。
2. 读取 `/opt/data/.oc-state.json`。
3. 对 `/opt/data` 执行当前镜像负责的文件过渡。文件过渡可以由 `migrator/`
   承载，但语义不是“从上一个版本升级”，而是“把任意历史版本留下的文件规整到当前镜像可读状态”。
4. 渲染 `/opt/data/config.yaml`、`/opt/data/.env`、`/opt/data/SOUL.md`、`/opt/data/skills/oc-kb/`。
5. 写回 `.oc-state.json`，记录当前镜像 variant 和渲染状态。
6. exec `hermes gateway run`。

如果文件过渡失败，必须阻止 Hermes 启动并保留原始 `/opt/data`。任何过渡逻辑
都必须先读后写、原子替换，不得在校验成功前删除原文件。

## 版本切换与文件过渡

本系统不假设用户按版本顺序升级。未来用户可能从任意旧 Hermes variant 直接切换
到当前 variant，也可能从当前 variant 切回其他 variant。兼容策略必须基于文件，
不能依赖“上一个版本 -> 当前版本”的线性升级链。

- 当前 variant 必须能读取由 S3 恢复到 `/opt/data` 的任意历史文件集合；`.oc-state.json`
  只能作为提示，不能作为唯一判断依据。
- 文件过渡输入只有 `/opt/data` 和 `/opt/oc-input/manifest.yaml`；不得依赖旧镜像还能运行、
  不得要求先启动中间版本，也不得要求 manager 额外调用旧版本接口。
- 文件过渡必须幂等。相同 `/opt/data` 重复启动多次，结果应稳定，不得重复追加、重复删除或重复改写用户数据。
- 文件过渡必须保守。遇到未知文件、未知字段或无法识别的历史格式时，优先保留原样；
  只有明确归类为会话快照、缓存或当前镜像生成物的路径才允许清理或覆盖。
- 文件过渡不得删除长期记忆、workspace、cron、kanban、weixin 凭证、自创 skill、
  `skills/.bundled_manifest` 或 `.oc-state.json`。需要改格式时，必须保留原始语义并采用原子写入。
- 当前镜像生成物可以重渲染：`config.yaml`、`.env`、`SOUL.md`、`skills/oc-kb/`。
  重渲染不得把用户长期记忆或自创 skill 当成模板输出覆盖。
- SQLite 文件必须用一致性快照处理。`kanban.db` 及其 WAL/SHM 状态不得被普通文件复制
  破坏；如果未来引入更多 SQLite 真相源，必须同步扩展 ops sync/restore 和本文件。
- `.oc-state.json` 是状态锚点，但不是兼容边界。未知、缺失或旧 variant 值都应触发
  当前镜像的文件检测与过渡逻辑，而不是直接失败。
- `skills/.bundled_manifest` 是识别镜像内置 skill 与运行期自创 skill 的共享锚点；
  新 variant 不得改变该文件位置，除非同时修改 ops 同步/恢复、ocops skills 识别和文件过渡逻辑。
- 如果某类历史文件无法安全过渡，必须让启动失败并保留现场，不能静默启动一个丢失记忆、
  丢失任务或丢失凭证的实例。

## 测试要求

新增或修改 Hermes variant 时至少确认以下测试：

- renderer 不覆盖 `/opt/data/memories/`、`/opt/data/MEMORY.md`、`/opt/data/USER.md`。
- 文件过渡覆盖首启、同版本启动、任意历史 variant 文件集合、重复运行和无法安全过渡的失败路径。
- `ocops.server` 覆盖本文件定义的全部 HTTP/SSE 端点；`ocops-contract/SPEC.md`
  必须与实际 `Route(...)` 表一致，schema 必须能通过 JSON Schema 自检。
- ops 恢复/同步覆盖长期记忆、workspace、weixin 凭证、自创 skill、`skills/.bundled_manifest`、cron、kanban、variant 状态锚点、会话快照和 sqlite 快照。
- 镜像构建阶段必须运行 variant 自检，失败不得产出生产镜像。
