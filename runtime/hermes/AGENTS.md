# AGENTS.md

本文件约束 `runtime/hermes/**` 下所有 Hermes runtime variant。更深层目录如果新增
`AGENTS.md`，以更近层级文件为准。

## 基本原则

- 每个 Hermes variant 必须自包含：镜像构建、入口脚本、renderer、migrator、ocops 能力、测试和版本契约都放在自己的 variant 目录内。
- 当前构建流程会把共享 `kanban-contract` 和 `cron-contract` 契约注入镜像；自包含要求最终镜像包含运行所需文件，不要求 variant 源目录物理 vendoring 这些共享契约。
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
| `/opt/data/kanban.db` | Hermes kanban 状态 | 保留任务板、任务和运行状态。 |
| `/opt/data/weixin/accounts/` | 渠道登录凭证 | 保留渠道绑定状态；不得写入镜像层。 |
| `/opt/data/skills/` 中的自创 skill | 用户或 app 运行期创建的 skill | 保留自创 skill；`skills/oc-kb/` 是 renderer 输出，受当前镜像重渲染，内置或受管 skill 按 variant 契约处理。 |

`sessions/` 与 `state.db*` 是可以清理的会话快照；除这些已明确分类的路径外，其他 app 运行时数据默认保留，只有在契约中显式归类后才能清理。

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

## S3 保存与恢复

app 级运行时数据使用 `apps/<appID>/` S3 前缀。

- `runtime/ops` 镜像负责通用恢复和同步命令：`oc-restore`、`oc-sync`、`oc-presync`。
- `oc-restore` 在 initContainer 中运行，负责调用 bootstrap、写入 `/opt/oc-input`，并把 S3 中的 app 数据恢复到 `/opt/data`。
- `oc-sync` 在 sidecar 中运行，负责周期性同步 `/opt/data` 中需要保留的非敏感数据。
- `oc-presync` 在 preStop hook 中运行，负责旧 pod 终止前做最后一次同步。
- 长期记忆必须纳入 `oc-sync` 与 `oc-presync` 的上传范围，并纳入 `oc-restore` 的恢复范围。
- S3 恢复失败时应让 initContainer 失败，不得静默启动一个空记忆实例。
- `sessions/` 与 `state.db*` 可以同步为会话快照，但不能被描述或处理为长期记忆。

## 对外能力

每个 Hermes variant 镜像必须提供以下能力：

- 主容器入口：`ENTRYPOINT` 执行 `oc-entrypoint`，最终启动 `hermes gateway run`。
- 健康检查：`oc-healthcheck` 或等价命令，能判断 Hermes gateway 是否可用。
- 知识库 CLI：`oc-kb`，只调用 manager runtime API，不直接持有 RAGFlow API key。
- `ocops.server`：随 Hermes variant 版本走的 HTTP 控制面，使用 Bearer `OC_OPS_TOKEN` 鉴权。
- `ocops.server` 至少覆盖当前 manager 依赖的 info、doctor、cron、kanban、channel、skills 能力。

`runtime/ops` 镜像不跟 Hermes variant 版本走。它是平台基础设施镜像，跟 manager bootstrap、S3 key 约定和 k8s 编排契约保持兼容。

`ocops.server` 当前必须保留在 Hermes 镜像内，因为它直接操作该 variant 的 Python 包、Hermes 内部命令、`/opt/data` 布局、cron、kanban、channel 和 skill 热加载能力。若未来拆到独立 ops 镜像，需要先定义跨镜像读写 `/opt/data` 的稳定 ABI。

## 启动流程

Hermes 主容器启动顺序必须保持：

1. 读取 `/opt/oc-input/manifest.yaml`。
2. 读取 `/opt/data/.oc-state.json`。
3. 若本地记录的 variant 与当前镜像 variant 不一致，执行 `migrator`。
4. 渲染 `/opt/data/config.yaml`、`/opt/data/.env`、`/opt/data/SOUL.md`、`/opt/data/skills/oc-kb/`。
5. 写回 `.oc-state.json`。
6. exec `hermes gateway run`。

如果 migrator 失败，必须阻止 Hermes 启动并保留原始 `/opt/data`。

## 升级兼容

- 新 variant 必须能读取上一个已发布 variant 的 `/opt/data`。
- 持久化格式不兼容时，必须在新 variant 的 `migrator/` 中做原地迁移。
- migrator 必须幂等，重复启动不能重复破坏数据。
- migrator 不得删除长期记忆；需要改格式时必须保留原始语义。
- `.oc-state.json` 是判断本地数据 variant 的状态锚点，不得随意改字段语义。

## 测试要求

新增或修改 Hermes variant 时至少确认以下测试：

- renderer 不覆盖 `/opt/data/memories/`、`/opt/data/MEMORY.md`、`/opt/data/USER.md`。
- migrator 覆盖首启、同版本启动、跨版本迁移、重复运行。
- `ocops.server` 覆盖 manager 当前调用的 HTTP 端点。
- ops 恢复/同步覆盖长期记忆、workspace、weixin 凭证、自创 skill、会话快照和 sqlite 快照。
- 镜像构建阶段必须运行 variant 自检，失败不得产出生产镜像。
