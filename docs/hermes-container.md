# Hermes 容器运行机制

> manager 如何让 runtime-agent 把 Hermes 容器跑起来：创建链路、镜像同步、
> input 目录、容器自渲染、挂载目录、知识库、渠道绑定和生命周期事件。
> 读完本文应能独立排查“容器没起来 / 配置没生效 / 知识库看不到”这一类问题。

## 1. 总览：一次 app 上线时发生了什么

时序（自上而下，数字与 `internal/worker/handlers/app_initialize.go` 的阶段
顺序一致）：

1. 企业成员注册或自助创建应用后，manager 写 `apps` 行，状态为
   `provisioning`，并入队 `app_initialize` job。
2. worker 拉到 job 后，`AppInitializeHandler.Handle` 加载 app / org / owner /
   runtime_node 上下文；状态已是 `running` / `binding_waiting` 时直接返回幂等成功。
3. `ImageDistributor.EnsureRuntimeImage` 把 Hermes runtime 镜像同步到目标节点，
   实际走 `internal/runtime/imagesync/service.go`。
4. `AgentDirInitializer.InitAppDirs` 通过 agent `POST /v1/scopes/apps/<id>/init`
   预建 app 目录。
5. `ensureAPIKey` 在 new-api 上以“企业业务 user”身份创建 token，拉完整 sk-，
   加密后写 `apps.newapi_key_ciphertext`。
6. manager 只写中立输入：`manifest.yaml`、`resources/*.md` 和固定 `oc-kb`
   skill 所需配置到节点 `apps/<id>/input/`。这些文件由
   `internal/integrations/hermes/app_input.go` 生成，
   通过 agent `/v1/scopes/apps/<id>/input/file` 上传。
7. `ContainerCreator.CreateContainer` 创建容器，挂载
   `apps/<id>/input` 到 `/opt/oc-input`（只读），挂载 `apps/<id>/data` 到
   `/opt/data`（可写）。
8. `ContainerStarter.StartContainer` 启动容器；镜像入口
   `oc-entrypoint` 读取 `/opt/oc-input`，在 `/opt/data` 内渲染
   `config.yaml`、`SOUL.md`、`.env` 与 `skills/oc-kb`，然后 exec
   `hermes gateway run`。
9. starter 实现 `HermesHealthChecker` 时，worker 等 docker HEALTHCHECK 报
   healthy，再把应用状态推进到 `binding_waiting` 并写 audit log。
10. 后续渠道扫码由 oc-ops HTTP 服务的 channel 登录端点
    （`POST /oc/channels/weixin/login`，SSE）完成；凭证落在
    Hermes 自管的 `/opt/data/weixin/accounts/`。绑定成功后 manager 只重启容器，
    让下一次 `oc-entrypoint` 从 Hermes 自管数据重新渲染 `.env`。

涉及的代码文件一览：

| 角色 | 文件 |
|---|---|
| 编排 | `internal/worker/handlers/app_initialize.go` |
| 镜像同步 | `internal/runtime/imagesync/service.go` |
| 容器创建 / 启停 | `internal/integrations/runtime/agent_backed.go` |
| input manifest / resources 生成 | `internal/integrations/hermes/app_input.go` |
| input 文件上传 | `internal/integrations/agent/file_client.go` |
| 容器内自渲染入口 | `runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py` |
| 容器内 renderer | `runtime/hermes/hermes-v2026.5.16/renderer/` |
| 容器内目录约定 | `runtime/hermes/hermes-v2026.5.16/Dockerfile`、`runtime/hermes/hermes-v2026.5.16/CONTRACT.md` |
| 节点 agent 文件 API | `runtime/agent/scopes.go` |
| docker socket 反向代理 | `runtime/agent/proxy.go` |

## 2. 镜像同步（imagesync）

构建产物为 `hermes-runtime:v2026.5.16-dev`（本地 dev tag），由仓库内
`runtime/hermes/hermes-v2026.5.16/Dockerfile` 构建。生产发布 tag 形如
`ywjs-26257ea5.ecis.huabei-3.cmecloud.cn/app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00`
这种时间戳 tag。镜像版本锁通过
`runtime/hermes/hermes-v2026.5.16/version.txt` 与 Dockerfile `HERMES_REF`
ARG 传入，Makefile 会拒绝 `main`、`master`、`latest` 等浮动 ref。

`imagesync.Service.SyncRuntimeImage` 的对账逻辑：

1. `LocalImageProvider.ImageID` 读 manager 本机 `docker image inspect` 拿到
   `localID`（image content digest）。
2. `AgentImageClient.InspectImage` 通过 agent `GET /v1/images/inspect?image=<ref>`
   拿到节点上的 `remote.ID`。
3. `remote.Exists && remote.ID == localID` 时跳过同步，直接返回
   `Transferred: false`。
4. ID 不一致或节点无此镜像时，`LocalImageProvider.Archive` 流式
   `docker save` 出 tar，通过 agent `POST /v1/images/load?image=<ref>` 发到节点。
5. 节点 load 完成后再 inspect 一次；loaded.ID 与 localID 仍不一致直接报错，
   阻止 `app_initialize` 拿错误镜像继续创建容器。

对账锚点是 docker `ImageID`，不是 tag。重复用同一个 tag 推不同内容时，
imagesync 能识别并强制重新分发。

## 3. 容器创建参数

入口：`ContainerCreator.CreateContainer`，实现为
`runtime.AgentBackedAdapter.CreateContainer`，底层通过 agent `/v1/docker/*`
反向代理转发到节点 docker socket。

### 3.1 环境变量

manager 不再通过 docker `Env` 或运行时文件 API 注入 `OPENAI_*`、`WEIXIN_*`
等 Hermes schema。业务配置统一写入 `/opt/oc-input/manifest.yaml`，由
`oc-entrypoint` 渲染到 `/opt/data/config.yaml`。

容器启动时仍会产生 `/opt/data/.env`，但写入方是镜像内
`renderer/render_env.py`：

| key | 来源 | 何时写入 |
|---|---|---|
| `GATEWAY_ALLOW_ALL_USERS` | `render_env.py` 固定行为开关 | 每次容器启动时由 `oc-entrypoint` 写入 `.env` |
| `WEIXIN_DM_POLICY` | `render_env.py` 固定行为开关 | 每次容器启动时由 `oc-entrypoint` 写入 `.env` |
| `WEIXIN_ACCOUNT_ID` | `/opt/data/weixin/accounts/<account_id>.json` 文件名 | 微信账号已绑定后的下一次容器启动 |
| `WEIXIN_TOKEN` | `/opt/data/weixin/accounts/<account_id>.json` 内容 | 微信账号已绑定后的下一次容器启动 |
| `WEIXIN_BASE_URL` | `/opt/data/weixin/accounts/<account_id>.json` 内容，存在时写入 | 微信账号已绑定后的下一次容器启动 |

OpenAI 兼容 endpoint 与 sk- 不写 `.env`，而是来自
`manifest.credentials.openai`，由 `renderer/render_config_yaml.py` 写入
`config.yaml` 的 provider 配置。manager 只负责刷新 input，不直接维护
Hermes 内部 schema 文件。

### 3.2 挂载

容器有两条 bind mount：

| HostPath | ContainerPath | 读写 | 备注 |
|---|---|---|---|
| `<nodeDataRoot>/apps/<appID>/input` | `/opt/oc-input` | ro | manager 写入的 manifest、rules、persona、knowledge 输入。 |
| `<nodeDataRoot>/apps/<appID>/data` | `/opt/data` | rw | Hermes 主目录，包含渲染产物、workspace、日志、SQLite、渠道凭证等运行时数据。 |

`Dockerfile` 里声明了 `VOLUME /opt/data`
（`runtime/hermes/hermes-v2026.5.16/Dockerfile`），保证未 bind mount 时也能用
匿名卷启动；生产路径使用上面的显式 bind。

容器 `WorkingDir` 固定为 `/opt/data/workspace`，让 agent 默认在 workspace
子目录下执行 terminal / file 工具。

### 3.3 网络

`ContainerSpec.Networks` 来自 `AppInitializeConfig.ContainerNetworks`，必须包含
new-api 所在 docker network，否则容器内 `http://new-api:3000` 无法解析。

### 3.4 容器命名

固定为 `hermes-<appID>`。

### 3.5 健康检查

镜像内 `HEALTHCHECK` 跑 `oc-healthcheck`，内部执行 `hermes gateway status`
（`runtime/hermes/hermes-v2026.5.16/Dockerfile`、
`runtime/hermes/hermes-v2026.5.16/healthcheck.sh`）。
`start-period=60s`、`interval=30s`、`timeout=10s`、`retries=3`。

## 4. 挂载目录结构

容器内 `/opt/oc-input` 与节点 `<nodeDataRoot>/apps/<appID>/input/` 是同一份
只读输入；容器内 `/opt/data` 与节点 `<nodeDataRoot>/apps/<appID>/data/`
是同一份可写运行时数据。

```text
/opt/oc-input/                       # [manager input] 只读挂载
├── manifest.yaml                    # app/model/credentials/resources 索引
└── resources/
    ├── persona.md                   # manager 已替换占位符的人设输入
    ├── platform-rules.md            # 平台规则输入
    ├── organization-rules.md        # 企业规则输入
    └── application-rules.md         # 应用规则输入

/opt/data/                           # [Hermes data] 可写挂载
├── SOUL.md                          # [oc-entrypoint] system prompt 渲染产物
├── config.yaml                      # [oc-entrypoint] model provider / memory / cwd 配置
├── .env                             # [oc-entrypoint] 行为开关 + 渠道凭证转译
├── .oc-state.json                   # [oc-entrypoint] variant / manifest hash / render outputs
├── cache/
│   ├── documents/                   # [Hermes] 文档解析缓存
│   └── images/                      # [Hermes] 图片缓存
├── channel_directory.json           # [Hermes] 渠道目录运行时状态
├── cron/
│   └── output/                      # [Hermes] 定时任务输出
├── gateway.lock                     # [Hermes] 网关进程锁
├── gateway_state.json               # [Hermes] 网关运行状态快照
├── kanban.db                        # [Hermes] kanban 数据
├── logs/                            # [Hermes] gateway / agent / error 日志
├── memories/                        # [Hermes] 长期记忆
├── platforms/
│   └── pairing/                     # [Hermes] 平台配对状态
├── sandboxes/
│   └── singularity/                 # [Hermes] skill 执行沙盒
├── sessions/                        # [Hermes] 会话 jsonl / request_dump 等附属文件
├── skills/
│   ├── oc-kb/                       # [oc-entrypoint] manager runtime 知识库 skill
│   └── ...                          # [Hermes] 镜像内置 skill 类目
├── state.db                         # [Hermes] 主状态库（SQLite WAL）
├── state.db-shm
├── state.db-wal
├── weixin/
│   └── accounts/                    # [Hermes] 微信账号 token / sync state
└── workspace/                       # [agent 预建] terminal.cwd 和工具产物落地点
```

节点 agent 在 scope 初始化时预建 app 目录；其余运行时子目录由
`oc-entrypoint` 或 Hermes 进程按需创建。

## 5. skills/ 目录的混合归属

`/opt/data/skills/` 是“镜像内置 + input 渲染”双重来源目录：

- `oc-kb/` 由 `oc-entrypoint` 固定生成，提供 `oc-kb search` 与 `oc-kb add`
  两个入口；它只调用 manager runtime API，不包含 RAGFlow API key 或 dataset ID。
- 其他类目来自 Hermes 镜像安装的内置 skill 库，首次启动或镜像升级时由 Hermes
  自身维护。
- 知识库内容不再渲染成本地 `kb-*` skill；文件主库、解析状态和 chunk 都在 RAGFlow。

## 6. 工作目录如何定位

- `config.yaml` 由 `oc-entrypoint` 渲染，强制 `terminal.cwd = "/opt/data/workspace"`。
- 容器 `WorkingDir` 也固定为 `/opt/data/workspace`。
- 节点 agent 在初始化时预建 workspace，保证 Hermes 第一次 `cd` 不会因目录缺失失败。
- manager workspace API 读取的是节点 `apps/<id>/data/workspace`，与容器内
  `/opt/data/workspace` 是同一份物理数据。

也就是说宿主机 data/workspace 与容器内 `/opt/data/workspace` 没有路径映射差异。

## 7. 知识库链路：Hermes → manager runtime API → RAGFlow

manager 在 `manifest.yaml` 写入：

```yaml
knowledge:
  runtime_base_url: "http://manager-api:8080"
  app_token: "<实例 runtime token>"
```

`oc-entrypoint` 将这两个值转成 `OC_KB_RUNTIME_BASE_URL` 与 `OC_KB_APP_TOKEN`，
供 `oc-kb` CLI 使用。Hermes 不直接连接 RAGFlow，也不持有 RAGFlow 凭证。

`oc-entrypoint` 的渲染路径：

| input | output | 用途 |
|---|---|---|
| `manifest.yaml` | `config.yaml` | provider、model、memory、terminal.cwd 等 Hermes 配置 |
| `manifest.yaml knowledge` | `.env` | `oc-kb` 访问 manager runtime API 的 endpoint 与 app token |
| `resources/persona.md` + `resources/*-rules.md` | `SOUL.md` | system prompt 主体 |
| 镜像内固定模板 | `skills/oc-kb/SKILL.md` | 指导 Hermes 使用 `oc-kb search` / `oc-kb add` |

权限语义由 manager 控制：企业知识库只读，实例知识库读写。`oc-kb add` 上传的是
当前实例 dataset，`oc-kb search` 同时检索当前实例 dataset 与所属企业 dataset。

### 7.1 生效时机

知识库文件本身由 RAGFlow 解析和检索，不依赖 app restart 生效。restart 仅在
manager runtime endpoint 或 app token 变更时需要，用于刷新 `manifest.yaml` 和 `.env`。

配置变更进入对话的业务路径仍是 app restart / recreate：

1. manager 先刷新节点 `apps/<id>/input/`，确保 `manifest.yaml` 与 resources 反映
   当前 DB 快照。
2. 容器 stop 后清空 sessions 与 `state.db` 三件套。
3. 容器 start 后，`oc-entrypoint` 重新渲染 `config.yaml`、`SOUL.md`、`.env`、
   `skills/oc-kb`。
4. 新 session 启动时 snapshot 最新 `SOUL.md`。

## 8. input vs 运行时生成（总表）

表头说明：

- **manager input**：manager 通过 agent input/file API 写入
  `apps/<id>/input/`，容器只读。
- **oc-entrypoint**：镜像入口每次启动时从 `/opt/oc-input` 与 Hermes 自管数据
  渲染到 `/opt/data`。
- **Hermes**：Hermes 进程或其平台组件运行时生成。
- **agent 预建**：节点 agent 在 scope 初始化时创建。

| 路径 | 来源 | 写入方 | app restart 行为 |
|---|---|---|---|
| `/opt/oc-input/manifest.yaml` | manager input | `hermes.WriteAppInput` | stop 前刷新为当前 DB 快照 |
| `/opt/oc-input/resources/*.md` | manager input | `hermes.WriteAppInput` | stop 前刷新为当前 DB 快照 |
| `/opt/data/config.yaml` | oc-entrypoint | `renderer/render_config_yaml.py` | 容器 start 时重渲染 |
| `/opt/data/SOUL.md` | oc-entrypoint | `renderer/render_soul_md.py` | 容器 start 时重渲染 |
| `/opt/data/.env` | oc-entrypoint | `renderer/render_env.py` | 容器 start 时从 Hermes 自管渠道数据重渲染 |
| `/opt/data/skills/oc-kb/SKILL.md` | oc-entrypoint | `renderer/render_skills.py` | 容器 start 时重渲染 |
| `/opt/data/skills/<非 kb-* 类目>/` | Hermes 镜像 | Hermes 内置 skill 库 | 不由 manager 管理 |
| `/opt/data/workspace/` | agent 预建 | `handleAppInit` | 不动，用户产物保留 |
| `/opt/data/sessions/` | Hermes | Hermes 进程 | restart 时清空 |
| `/opt/data/state.db` / `-shm` / `-wal` | Hermes | Hermes 进程 | restart 时删除，让新 session snapshot 最新 `SOUL.md` |
| `/opt/data/memories/` | Hermes | Hermes 进程 | 不动，长期偏好与稳定事实保留 |
| `/opt/data/logs/` | Hermes | Hermes 进程 | 不动 |
| `/opt/data/kanban.db` | Hermes | Hermes 进程 | 不动 |
| `/opt/data/weixin/accounts/` | Hermes | oc-ops channel 登录端点 / Hermes 微信平台 | 不动，`oc-entrypoint` 下次启动转译为 `.env` |

## 9. 生命周期事件

### 9.1 启动

容器启动入口为 `tini -g -- /usr/local/bin/oc-entrypoint`
（`runtime/hermes/hermes-v2026.5.16/Dockerfile`）。`oc-entrypoint` 执行：

1. 读取 `/opt/oc-input/manifest.yaml`。
2. 读取 `/opt/data/.oc-state.json`，决定是否需要 variant 迁移。
3. 渲染 `/opt/data/config.yaml`、`/opt/data/.env`、`/opt/data/SOUL.md`、
   `/opt/data/skills/oc-kb`。
4. 写回 `.oc-state.json`，记录 variant、manifest hash、渲染产物。
5. exec `hermes gateway run`。

Hermes gateway 随后读取 `/opt/data/config.yaml`、`/opt/data/.env`、
`/opt/data/SOUL.md` 与 `/opt/data/skills/`，并在新 session 中冻结当前
system prompt。

### 9.2 停止

调用 `AgentBackedAdapter.StopContainer`，给 docker 30s 优雅退出窗口。
挂载内容全部保留；下次启动时 `oc-entrypoint` 会基于现有 Hermes data 与最新
input 重新渲染。

### 9.3 app_health_check 自动拉起

`AppHealthCheckHandler` 通过 `docker inspect` 拿到容器 `Status` 与
`Health.Status`：

- `Status != "running"`：在 `restart_policy` budget 内主动调 `StartContainer`
  自愈，超 budget 才推 `apps.status = error`。
- `Status = "running"` 且 `Health = "healthy"`：写 `last_success_at`。
- `Status = "running"` 但 `Health != "healthy"`：累计 failures，超 budget
  推 `error`。

### 9.4 app restart 命令

`AppRestartContainerHandler` 按“刷新 input → stop → clear sessions → start”
执行，而不是依赖 manager 重写 Hermes 内部 schema：

1. `AppInputRefresher.RefreshAppInput` 把节点 `apps/<id>/input/manifest.yaml` 与
   `resources/*.md` 刷新成当前 DB 快照。
2. `StopContainer` 停容器；`state.db` 持有 SQLite 文件锁，必须停容器后才能安全删除。
3. `ClearAppSessions` 删除 `sessions/` 目录与 `state.db` / `-shm` / `-wal`。
4. `StartContainer` 后，`oc-entrypoint` 重新渲染 `/opt/data` 内的 Hermes 文件。

为什么必须清 session：Hermes 把 `system_prompt` 在 session 启动时冻结存进
SQLite。配置变更类操作（改 model / persona / runtime endpoint / 重启）如果只 restart
不清 session，旧 session 仍用冻结的旧 `SOUL.md`。`memories/` 不在清空范围，
长期偏好与稳定事实跨 session 保留。

未注入 `SessionCleaner` 时会退回原子 `RestartContainer`，仅用于旧装配或测试装配兼容。

### 9.5 配置变更触发

| 变更类型 | manager 入口 | manager 做什么 | 容器如何生效 |
|---|---|---|---|
| 模型变更 | `PATCH /apps/<id>/model` | 入队 restart；restart 前刷新 input | `oc-entrypoint` 重新渲染 `config.yaml` 与 `SOUL.md` |
| prompt / persona 变更 | 对应业务写 DB 后 restart | restart 前刷新 input resources | `oc-entrypoint` 重新渲染 `SOUL.md` |
| 渠道绑定成功 | channel login / check binding job | 标记 binding，重启容器 | oc-ops channel 登录端点已落盘账号；`oc-entrypoint` 下次启动渲染 `.env` |
| 知识库变更 | `KnowledgeService.Save*` / `Delete*` | 代理 RAGFlow document 并刷新 manager 元数据 | RAGFlow parse 完成后即可被 `oc-kb search` 检索 |
| Hermes runtime 镜像升级 | manager 本地 build / push 后下次初始化 | `imagesync.SyncRuntimeImage` 分发新镜像 | recreate / start 时由新镜像入口自渲染 |

## 10. 排查 cheatsheet

| 现象 | 第一步看 | 关键命令 / 路径 |
|---|---|---|
| 容器没起来 | `app_initialize` job 失败记录 | manager-api 日志搜 `runtime_node` + `app_id`；前端“应用详情”看最近 audit log |
| 镜像同步失败 | `imagesync` 调用 | manager-api 日志 `inspect remote image` / `load remote image`；在节点跑 `docker images hermes-runtime:v2026.5.16-dev` 对比 ID |
| input 没刷新 | restart job 日志 | 节点看 `<nodeDataRoot>/apps/<appID>/input/manifest.yaml` 与 `resources/` 时间戳 |
| `config.yaml` 没生效 | `oc-entrypoint` 渲染日志 | 节点看 `<nodeDataRoot>/apps/<appID>/data/.oc-state.json` 的 `manifest_sha256` 与 `renderer_outputs` |
| `.env` 没有 `WEIXIN_*` | 容器侧账号文件 | 节点看 `<nodeDataRoot>/apps/<appID>/data/weixin/accounts/` 是否存在账号 JSON；再看重启后的 `data/.env` |
| 知识库看不到 | manager runtime API 与 RAGFlow | 容器内跑 `oc-kb search "测试"`；检查 `manifest.yaml` 的 `knowledge` 字段、manager `ragflow.*` 配置和 RAGFlow 文档解析状态 |
| 改了知识库对话不变 | RAGFlow 解析状态 | 确认文档状态为已完成；失败文件在 manager 知识库页面点“重解析” |
| workspace 不存在 | agent `handleAppInit` 是否成功 | 节点 agent 日志搜 `/v1/scopes/apps/<id>/init`；失败时 workspace API 返回空目录但 cwd 拉起会报错 |
| 容器频繁被拉起 | `restart_policy` budget 是否 trip | 查 `apps.health_state_json`；`Failures` 数组长度接近 `MaxPerWindow` 时已熔断为 `status=error` |
| docker proxy 401 / 403 | agent token / IP 白名单 | manager-api 日志搜 `agent token 校验失败` 或 `源 IP 不在白名单内` |
| HEALTHCHECK 卡 starting | gateway 启动慢 / iLink 连接失败 | 节点跑 `docker exec hermes-<id> hermes gateway status`；看 `/opt/data/logs/gateway.log` 与 `gateway-exit-diag.log` |

更多依赖文档：

- [架构总览](./architecture.md)：manager / agent / Hermes 的整体拓扑
- [runtime-agent 工作原理](./runtime-agent.md)：agent 自动注册、心跳、docker proxy
- [配置参考](./configuration.md)：`manager.yaml` 里 `runtime` / `hermes` 节段字段
- [运维手册](../deploy/operations.md)：节点 `state_dir` / `data_root` 备份与升级
