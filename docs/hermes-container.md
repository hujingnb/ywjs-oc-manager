# Hermes 容器运行机制

> manager 如何让 runtime-agent 把 Hermes 容器跑起来:创建链路、镜像同步、
> 环境变量、挂载目录、工作目录、知识库注入、注入 vs 运行时生成、生命周期事件。
> 读完本文应能独立排查"容器没起来 / 配置没生效 / 知识库看不到"这一类问题。

## 1. 总览:一次 app 上线时发生了什么

时序(自上而下,数字与 `internal/worker/handlers/app_initialize.go` 的步骤
顺序一致):

1. 组织成员注册或自助创建应用 → manager 写 `apps` 行,状态 `provisioning`,
   入队 `app_initialize` job(payload 含 `app_id` + `runtime_node`)。
2. worker 拉到 job 后,`AppInitializeHandler.Handle` 加载 app / org / owner /
   runtime_node 上下文;状态已是 `running` / `binding_waiting` 直接返回幂等成功
   (`app_initialize.go:240`)。
3. `ImageDistributor.EnsureRuntimeImage` 把 Hermes runtime 镜像同步到目标节点
   (`app_initialize.go:254`,实际走 `internal/runtime/imagesync/service.go`)。
4. `AgentDirInitializer.InitAppDirs` 通过 agent `POST /v1/scopes/apps/<id>/init`
   预建 `.hermes/` `.hermes/workspace/` `knowledge/` 三个目录
   (`app_initialize.go:262`、`runtime/agent/scopes.go:203`)。
5. `ensureAPIKey` 在 new-api 上以"组织业务 user"身份创 token,拉完整 sk-,
   加密后写 `apps.newapi_key_ciphertext`(`app_initialize.go:269`)。
6. `writeHermesFiles` 把 `SOUL.md` / `config.yaml` / `.env` / 知识库展开后的
   `skills/kb-{org,app}-<slug>/SKILL.md` 通过 `UploadAppRuntimeFile` PUT
   到 agent `/v1/scopes/apps/<id>/runtime/file`(`app_initialize.go:279`、
   `internal/integrations/agent/file_client.go:241`)。
7. `ContainerCreator.CreateContainer` 通过 agent docker proxy 在节点上 create
   容器(`app_initialize.go:318`、`internal/integrations/runtime/agent_backed.go:97`)。
8. `ContainerStarter.StartContainer` 启动容器(`app_initialize.go:333`);
   starter 实现 `HermesHealthChecker` 时再等 docker HEALTHCHECK 报 healthy
   (`app_initialize.go:339`)。
9. 推应用状态到 `binding_waiting`,写一条 audit log(`app_initialize.go:348`)。
10. 后续渠道扫码绑定成功后,`ChannelLoginHandler` 把 `WEIXIN_*` 写进 `.env`
    并 restart 容器,状态推到 `running`(`internal/worker/handlers/channel_login.go:231`)。

涉及的代码文件一览:

| 角色 | 文件 |
|---|---|
| 编排 | `internal/worker/handlers/app_initialize.go` |
| 镜像同步 | `internal/runtime/imagesync/service.go` |
| 容器创建 / 启停 | `internal/integrations/runtime/agent_backed.go` |
| Hermes 文件渲染 | `internal/integrations/hermes/{config.go,skills.go,prompt.go}` |
| Hermes 配置文件上传 | `internal/integrations/agent/file_client.go` |
| 容器内目录约定 | `runtime/hermes/Dockerfile`、`runtime/hermes/CONTRACT.md` |
| 节点 agent 文件 API | `runtime/agent/scopes.go` |
| docker socket 反向代理 | `runtime/agent/proxy.go` |

## 2. 镜像同步(imagesync)

构建产物为 `hermes-runtime:dev`(默认 tag),由仓库内 `runtime/hermes/Dockerfile`
构建。`AppInitializeConfig.RuntimeImage` 在装配时如未设置则 fallback 到
`hermes-runtime:dev`(`app_initialize.go:191`)。镜像版本锁通过
`runtime/hermes/version.txt`(当前 `main`)与 Dockerfile `HERMES_REF` ARG 传入。

`imagesync.Service.SyncRuntimeImage` 的对账逻辑(`internal/runtime/imagesync/service.go:64`):

1. `LocalImageProvider.ImageID` 读 manager 本机 `docker image inspect` 拿到
   `localID`(image content digest)。
2. `AgentImageClient.InspectImage` 通过 agent `GET /v1/images/inspect?image=<ref>`
   拿到节点上的 `remote.ID`(`runtime/agent/main.go:393`)。
3. `remote.Exists && remote.ID == localID` → 跳过同步,直接返回 `Transferred: false`。
4. ID 不一致或节点无此镜像 → `LocalImageProvider.Archive` 流式 `docker save` 出 tar,
   通过 agent `POST /v1/images/load?image=<ref>` 发到节点
   (`runtime/agent/main.go:414`)。
5. 节点 load 完成后再 inspect 一次;loaded.ID 与 localID 仍不一致直接报错,
   阻止 `app_initialize` 拿错误镜像继续创建容器(`service.go:98`)。

对账锚点是 docker `ImageID`,**不是 tag**——重复用同一个 tag 推不同内容时,
imagesync 能识别并强制重新分发。

## 3. 容器创建参数

入口:`ContainerCreator.CreateContainer`(实现 = `runtime.AgentBackedAdapter.CreateContainer`,
`internal/integrations/runtime/agent_backed.go:97`),底层通过 agent
`/v1/docker/*` 反向代理转发到节点 docker socket(`runtime/agent/proxy.go:39`)。

### 3.1 环境变量

下表列出 manager 直接注入 docker `Env` 与 `.env` 文件两种渠道的所有
环境变量。**Hermes 启动时同时读取这两路**:`Env` 是 docker create 时的直接
覆盖,`.env` 由 Hermes 进程自身加载。

| key | 含义 | 来源 | 何时追加 |
|---|---|---|---|
| `OPENAI_API_KEY` | new-api token(真实 sk-) | `ensureAPIKey` 解密自 `apps.newapi_key_ciphertext` | app 创建时(`Env` + `.env`) |
| `OPENAI_BASE_URL` | new-api OpenAI 兼容 endpoint(`http://new-api:3000/v1`) | `AppInitializeConfig.NewAPIBaseURL` + `/v1` | app 创建时(`Env` + `.env`) |
| `GATEWAY_ALLOW_ALL_USERS` | 固定 `true`,绕过 Hermes user pairing 流程 | 硬编码(`hermes/config.go:91`) | app 创建时(仅 `.env`) |
| `WEIXIN_DM_POLICY` | 固定 `open`,允许微信平台接收未授权 DM | 硬编码(`hermes/config.go:91`) | app 创建时(仅 `.env`) |
| `WEIXIN_ACCOUNT_ID` | 微信账号 ID(`<hex>@im.bot`) | 渠道扫码绑定时从 `oc-weixin-login` stdout JSON 提取 | 绑定后由 `ChannelLoginHandler` 重写 `.env` |
| `WEIXIN_TOKEN` | 微信会话 token | 同上 | 同上 |
| `WEIXIN_BASE_URL` | 微信平台 API base URL(默认 `https://weixin.novac2c.com`) | 同上 | 同上 |
| `WEIXIN_CDN_BASE_URL` | 微信 CDN base URL(固定 `https://novac2c.cdn.weixin.qq.com/c2c`) | 硬编码(`hermes/config.go:100`) | 绑定后随其他 WEIXIN_* 一起写入 `.env` |

绑定流程关键细节:微信扫码 bound 事件触发时,handler 用 `RenderEnv` **重新生成
完整 .env**(含 `OPENAI_*` + `GATEWAY_*` + `WEIXIN_DM_POLICY` + `WEIXIN_*`)
并通过 `UploadAppRuntimeFile` 覆盖写,而不是追加,避免追加把 `OPENAI_*` 行漏掉
(`channel_login.go:229`)。

`GATEWAY_ALLOW_ALL_USERS=true` 与 `WEIXIN_DM_POLICY=open` 在 spec / 部署
文档里容易忽略,但 Hermes 容器化部署没有交互式 CLI 跑 `hermes pairing approve`,
不带这两个变量首条消息直接被拒(参考 `hermes/config.go:80-86` 行内注释)。

### 3.2 挂载

容器只有一条 bind mount(`app_initialize.go:311-316`):

| HostPath | ContainerPath | 备注 |
|---|---|---|
| `<nodeDataRoot>/apps/<appID>/.hermes` | `/opt/data` | 全量 bind mount,Hermes 主目录。`nodeDataRoot` 取自 `runtime_nodes.node_data_root`,空则 fallback `/var/lib/oc-agent` |

`Dockerfile` 里声明了 `VOLUME /opt/data`(`runtime/hermes/Dockerfile:49`),
保证未 bind mount 时也能用匿名卷启动,但生产路径都是上面这条 bind。

容器 `WorkingDir` 固定为 `/opt/data/workspace`(`app_initialize.go:304`),
让 agent 默认在 workspace 子目录下执行 terminal / file 工具。

### 3.3 网络

`ContainerSpec.Networks` 来自 `AppInitializeConfig.ContainerNetworks`,必须
包含 new-api 所在 docker network,否则容器内 `http://new-api:3000` 无法解析
(`app_initialize.go:131-133`)。

### 3.4 容器命名

固定为 `hermes-<appID>`(`app_initialize.go:297`)。

### 3.5 健康检查

镜像内 `HEALTHCHECK` 跑 `oc-healthcheck`,内部执行 `hermes gateway status`
(`runtime/hermes/Dockerfile:46`、`runtime/hermes/scripts/healthcheck.sh`)。
`start-period=60s`、`interval=30s`、`timeout=10s`、`retries=3`。

## 4. 挂载目录结构

容器内 `/opt/data` 与节点 `<nodeDataRoot>/apps/<appID>/.hermes/` 是同一份数据。
按"来源"分类标注:

- **[注入]** = manager 通过 agent `/v1/scopes/apps/<id>/runtime/file` PUT 写入
- **[镜像]** = Hermes 镜像内置(install.sh 装好的资产,首次启动从镜像复制到挂载点)
- **[运行时]** = Hermes 进程或其内部组件运行时生成

```text
/opt/data/                           # bind mount 根,= 节点 .hermes/
├── SOUL.md                          # [注入] agent identity + system prompt(三层 platform + org + app + 知识库 always-on)
├── config.yaml                      # [注入] model provider + auxiliary + memory + terminal.cwd
├── .env                             # [注入] OPENAI_* + GATEWAY_* + WEIXIN_DM_POLICY(+ 绑定后 WEIXIN_*)
├── bin/                             # [镜像] Hermes 内置可执行入口(install.sh 创建,首启复制到挂载点)
├── cache/
│   ├── documents/                   # [运行时] Hermes 文档解析缓存
│   └── images/                      # [运行时] Hermes 图片缓存
├── channel_directory.json           # [运行时] 渠道目录运行时状态
├── cron/
│   └── output/                      # [运行时] Hermes 定时任务输出
├── gateway.lock                     # [运行时] 网关进程锁(每次启动重写)
├── gateway_state.json               # [运行时] 网关运行状态快照
├── kanban.db                        # [运行时] kanban 数据(SQLite)
├── logs/
│   ├── agent.log                    # [运行时] agent 主进程日志
│   ├── curator/                     # [运行时] curator 子系统日志
│   ├── errors.log                   # [运行时] 错误聚合日志
│   ├── gateway.log                  # [运行时] 网关日志
│   ├── gateway-exit-diag.log        # [运行时] 网关退出诊断
│   └── gateway-shutdown-diag.log    # [运行时] 网关关停诊断
├── memories/                        # [运行时] Hermes 长期记忆
├── platforms/
│   └── pairing/                     # [运行时] 平台配对状态
├── sandboxes/
│   └── singularity/                 # [运行时] skill 执行沙盒
├── sessions/                        # [运行时] 会话 jsonl / request_dump 等附属文件
├── skills/                          # 混合归属,见 §5
│   ├── apple/                       # [镜像] 内置技能类目
│   ├── autonomous-ai-agents/        # [镜像]
│   ├── creative/                    # [镜像]
│   ├── devops/                      # [镜像]
│   ├── github/                      # [镜像]
│   ├── mlops/                       # [镜像]
│   ├── ...                          # [镜像] 其他 Hermes 自带类目
│   ├── kb-app-<slug>/               # [注入] 应用级知识库 → SKILL.md
│   └── kb-org-<slug>/               # [注入] 组织级知识库 → SKILL.md
├── state.db                         # [运行时] 主状态库(SQLite WAL,session/system_prompt 冻结存储)
├── state.db-shm                     # [运行时] SQLite WAL shared memory
├── state.db-wal                     # [运行时] SQLite WAL log
├── weixin/
│   └── accounts/                    # [运行时] 微信账号 token / sync state(绑定后才出现)
└── workspace/                       # [agent 预建] terminal.cwd,Hermes 工具产物落地点
```

`runtime/hermes/CONTRACT.md` 列出了与 Hermes 上游约定的目录约定,本表与之
保持一致。节点 agent 在 `handleAppInit` 时只显式 `MkdirAll` 三个目录
(`.hermes/`、`.hermes/workspace/`、`knowledge/`,`runtime/agent/scopes.go:208`);
其余子目录由 Hermes 启动时按需创建。

## 5. skills/ 目录的混合归属

`/opt/data/skills/` 是唯一一个"manager 注入 + 镜像自带"双重来源的目录:

- manager 注入仅限 `kb-app-<slug>/` 与 `kb-org-<slug>/` 两类子目录,内部固定
  只有一份 `SKILL.md`(由 `hermes.RenderKnowledgeSkill` 生成,
  `internal/integrations/hermes/skills.go:57`)。
- 其他类目(`apple/` `autonomous-ai-agents/` `creative/` `devops/` `github/`
  `mlops/` ...)来自 Hermes 镜像 install.sh 装下来的内置 skill 库,首次容器
  启动时由 Hermes 自身把这些目录复制到挂载点。
- 知识库新增 / 修改时只写 `kb-*` 目录,**不会动**任何 Hermes 内置类目;反之,
  Hermes 镜像升级覆写 `kb-*` 之外的内置类目时,manager 写入的知识库 skill
  也不会被影响——双方在文件路径层面互不重叠。

`slug` 由 `hermes.SlugifyKnowledgePath` 从主副本相对路径生成
(`internal/integrations/hermes/skills.go:90` 附近);例如组织级
`policies/refund.md` → `kb-org-policies-refund/SKILL.md`。

## 6. 工作目录如何定位

- `config.yaml` 渲染时强制 `terminal.cwd = "/opt/data/workspace"`
  (`internal/integrations/hermes/config.go:72`)。
- 容器 `WorkingDir` 也固定为 `/opt/data/workspace`(`app_initialize.go:304`)。
- 节点 agent 在 `handleAppInit` 时预建 `.hermes/workspace`(`scopes.go:210`),
  保证 Hermes 第一次 `cd` 不会因目录缺失而失败。
- manager workspace API(列目录 / 下载文件 / 打包 zip)读取的是节点
  `apps/<id>/.hermes/workspace`,与容器内 `/opt/data/workspace` 是**同一份
  物理数据**(`runtime/agent/scopes.go:127-131`)。

也就是说宿主机 `.hermes/workspace` 与容器内 `/opt/data/workspace` 没有路径
映射差异——manager 不再做历史上的双挂载与路径翻译。

## 7. 知识库链路:从 manager 主副本到 skills/kb-*

### 7.1 主副本

manager 端按以下路径组织主副本(由 `internal/files/knowledge_master.go` 或
`internal/service/knowledge_service.go` 维护):

```text
org/<orgID>/knowledge/              # 组织级
org/<orgID>/app/<appID>/knowledge/  # 应用级
```

`KnowledgeReader` 接口(`app_initialize.go:72`)抽象 `WalkFiles` 遍历 +
`Open` 读取两个能力。

### 7.2 app 初始化时的批量渲染

`app_initialize.go:485` 调 `writeSkillsFromKnowledge` →
`uploadKnowledgeSkills`(`app_initialize.go:580`):

1. `WalkFiles(prefix, ...)` 递归遍历主副本目录,每个文件回调一次。
2. `hermes.SlugifyKnowledgePath(relPath)` 把相对路径展平成合法 slug。
3. `hermes.RenderKnowledgeSkill` 渲染 frontmatter + body 得到 `SKILL.md`。
4. `UploadAppRuntimeFile` PUT 到 agent
   `/v1/scopes/apps/<id>/runtime/file?path=skills/kb-{org,app}-<slug>/SKILL.md`。

同一份知识库内容还会被 `collectKnowledgeForSoul`(`app_initialize.go:500`)
inline 进 `SOUL.md` 末尾作为 always-on context,确保即使 agent 没主动调
`skill_view`,知识库内容也已在 system prompt 里(单文件超 8 KiB 截断)。

### 7.3 增量同步(legacy `knowledge/` 路径)

`knowledge_sync_node` job(`internal/worker/handlers/knowledge_sync.go`)按
(scope, scopeID, relPath, change_type)单文件触发,通过 agent
`PUT /v1/scopes/apps/<id>/knowledge/file?path=...` 或
`PUT /v1/scopes/orgs/<id>/knowledge/file?path=...` 推送差异
(`internal/integrations/agent/file_client.go` 同包内 `doKnowledgeFile`)。

**注意**:增量同步走的是 legacy `knowledge/` 沙箱,即
`<nodeDataRoot>/apps/<id>/knowledge/` 与
`<nodeDataRoot>/orgs/<id>/knowledge/`;它**不会**直接重写
`.hermes/skills/kb-*/SKILL.md`。`kb-*/SKILL.md` 的最新内容需要等到下次
`app_initialize`(或走 app restart / recreate 重新触发 `writeHermesFiles`
的流程)才会完整重新渲染。如果业务需要"改完知识库立刻在对话里生效",
请走 app restart 命令——它会重写 `config.yaml` 并清空 session
(参考 §9)。

历史 `apps/<id>/knowledge/` 目录在 Hermes 时代主要作为 legacy sandbox
保留,manager 当前不再在容器内读这条路径——Hermes 实际读的是
`skills/kb-*/SKILL.md`。

## 8. 注入 vs 运行时生成(总表)

| 路径(以 `/opt/data/` 为根) | 来源 | 写入方 | 何时写 | app 普通重启清空 | app restart 命令清空 |
|---|---|---|---|---|---|
| `SOUL.md` | manager 注入 | `AppInitializeHandler.writeHermesFiles` | app 创建时 / 配置变更时 | 否 | 否 |
| `config.yaml` | manager 注入 | 同上 + `HermesConfigRefresher.RefreshConfigYAML`(restart 时) | 创建时 / 模型变更时 | 否 | 否(restart 前会先重写) |
| `.env` | manager 注入 | `writeHermesFiles` + `ChannelLoginHandler`(绑微信后) | 创建时 / 渠道绑定时 | 否 | 否 |
| `skills/kb-app-<slug>/SKILL.md` | manager 注入 | `writeSkillsFromKnowledge` → `UploadAppRuntimeFile` | app 创建时 | 否 | 否(restart 不触发重新渲染,需 recreate) |
| `skills/kb-org-<slug>/SKILL.md` | manager 注入 | 同上 | 同上 | 否 | 否 |
| `skills/<非 kb-* 类目>/` | 镜像自带 | Hermes 首次启动复制 | 容器首次启动 | 否 | 否 |
| `bin/` | 镜像自带 | Hermes 首次启动复制 | 容器首次启动 | 否 | 否 |
| `workspace/` | agent 预建 | `handleAppInit`(`runtime/agent/scopes.go:210`) | scope 建立时 | 否 | 否(用户文件,不能清) |
| `sessions/` | Hermes 生成 | Hermes 进程 | 运行时按会话 append | 否 | **是**(commit `40f01a8`) |
| `state.db` / `-shm` / `-wal` | Hermes 生成 | Hermes 进程(SQLite WAL) | 运行时 | 否 | **是**(与 sessions 同步清,使新 SOUL 进入对话) |
| `memories/` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `logs/` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `kanban.db` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `gateway.lock` | Hermes 生成 | Hermes 进程 | 每次启动重写 | 是(重启自动重写) | 是(随 restart) |
| `gateway_state.json` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `channel_directory.json` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `cache/{documents,images}/` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `cron/output/` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `sandboxes/singularity/` | Hermes 生成 | Hermes 进程(skill 执行) | 运行时 | 否 | 否 |
| `platforms/pairing/` | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
| `weixin/accounts/` | Hermes 生成 | Hermes 微信平台 | 绑定后运行时 | 否 | 否 |

## 9. 生命周期事件

### 9.1 启动

容器启动入口为 `tini -g -- hermes gateway run`(`runtime/hermes/Dockerfile:51`)。
Hermes 启动时:

- 读 `/opt/data/.env` 装载凭证。
- 读 `/opt/data/config.yaml` 取 model provider 配置(`base_url` 指向 new-api、
  `api_key` 真实 sk-)。
- 读 `/opt/data/SOUL.md` 作为 system prompt(冻结存进 `state.db`)。
- 扫描 `/opt/data/skills/` 注册 skill(含 manager 注入的 `kb-*`)。
- 节点 agent 已预建 `/opt/data/workspace`,容器进程启动 cwd 即此目录。

`AppInitializeHandler` 在调 `StartContainer` 之后,若 starter 实现
`HermesHealthChecker` 接口,会轮询 `docker inspect` 等待 `State.Health.Status`
变为 `healthy`,最多 120s(`app_initialize.go:339-344`)。

### 9.2 停止

调用 `AgentBackedAdapter.StopContainer`(`agent_backed.go:133`),给 docker
30s 优雅退出窗口(`containerStopTimeout = 30`)。挂载内容全部保留——下次
启动时 Hermes 会继续读现有 `state.db` / `sessions/` 等运行时数据。

### 9.3 app_health_check 自动拉起(commit `040878c`)

`AppHealthCheckHandler`(`internal/worker/handlers/app_health_check.go`)
通过 `docker inspect` 拿到容器 `Status` + `Health.Status`:

- `Status != "running"`:被 docker 重启 / OOM / 节点重启等基础设施事件
  意外停掉。在 `restart_policy` budget 内主动调 `StartContainer` 自愈,
  超 budget 才推 `apps.status = error`(`app_health_check.go:129-143`)。
- `Status = "running"` 且 `Health = "healthy"`:写 `last_success_at`。
- `Status = "running"` 但 `Health != "healthy"`:累计 failures,超 budget
  推 `error`(不自动重启,等下一周期或人工干预)。

### 9.4 app restart 命令清空 session(commit `40f01a8`)

`AppRestartContainerHandler`(`internal/worker/handlers/app_runtime_ops.go:232`)
按 `stop → clear sessions → start` 三步执行,而不是原子 docker restart。
原因:Hermes 把 `system_prompt` 在 session 启动时冻结存进 `state.db`(SQLite),
后续 `SOUL.md` 改动对老 session 不生效。`SessionCleaner.ClearAppSessions`
调 agent `DELETE /v1/scopes/apps/<id>/sessions`(`runtime/agent/scopes.go:629`),
删除 `sessions/` 目录 + `state.db` / `-shm` / `-wal` 三件套,让新 session
重新 snapshot 最新 SOUL.md。

restart 流程同时支持 `HermesConfigRefresher.RefreshConfigYAML`,在
`UpdateModel` 等触发的 restart 前先重写 `config.yaml`,保证 Hermes 启动
加载的就是 DB 里最新模型 ID(`app_runtime_ops.go:250-253`)。

### 9.5 配置变更触发的重新注入

- 模型变更(UpdateModel):走 restart 流程,refresher 重写 `config.yaml` →
  清 sessions → restart。
- 渠道绑定成功:`ChannelLoginHandler` 重写完整 `.env` → restart
  (`channel_login.go:231-259`)。
- 知识库变更:`knowledge_sync_node` 推到 `knowledge/` legacy 沙箱;
  对话级生效需走 app restart 或 recreate(重新触发 `writeSkillsFromKnowledge`
  渲染 `kb-*/SKILL.md`)。

## 10. 排查 cheatsheet

| 现象 | 第一步看 | 关键命令 / 路径 |
|---|---|---|
| 容器没起来 | `app_initialize` job 失败记录 | manager-api 日志搜 `runtime_node` + `app_id`;前端"应用详情" 看最近 audit log |
| 镜像同步失败 | `imagesync` 调用 | manager-api 日志 `inspect remote image` / `load remote image`;在节点跑 `docker images hermes-runtime:dev` 对比 ID |
| 环境变量没生效 | docker inspect | 进节点跑 `docker inspect hermes-<appID> --format '{{.Config.Env}}'`,确认 `OPENAI_API_KEY` 是否真实 sk- 而非占位符 |
| .env 没有 WEIXIN_* | `ChannelLoginHandler` 是否触发 | manager-api 日志搜 `weixin_account_id`;查 `apps.<id>.channel_bindings` 状态;节点上 `cat <nodeDataRoot>/apps/<appID>/.hermes/.env` |
| 知识库看不到 | `skills/kb-*` 是否注入 | 节点上 `ls <nodeDataRoot>/apps/<appID>/.hermes/skills/`;`SOUL.md` 末尾是否有 always-on context |
| 改了知识库对话不变 | 缓存来自老 session | 走"重启应用",`ClearAppSessions` 会清 `state.db` |
| workspace 不存在 | agent `handleAppInit` 是否成功 | 节点 agent 日志搜 `/v1/scopes/apps/<id>/init`;失败时 `apps.workspace` API 返回空目录但 cwd 拉起会报错 |
| 容器频繁被拉起 | `restart_policy` budget 是否在 trip | 查 `apps.health_state_json` 字段;`Failures` 数组长度接近 `MaxPerWindow` 时已熔断为 `status=error` |
| docker proxy 401 | agent token / IP 白名单 | manager-api 日志搜 `agent token 校验失败` 或 `源 IP 不在白名单内`(`runtime/agent/proxy.go:189`) |
| HEALTHCHECK 卡 starting | gateway 启动慢 / iLink 连接失败 | 节点跑 `docker exec hermes-<id> hermes gateway status`;看 `/opt/data/logs/gateway.log` 与 `gateway-exit-diag.log` |

更多依赖文档:

- [架构总览](./architecture.md):manager / agent / Hermes 的整体拓扑
- [runtime-agent 工作原理](./runtime-agent.md):agent 自动注册、心跳、docker proxy
- [配置参考](./configuration.md):`manager.yaml` 里 `runtime` / `hermes` 节段字段
- [运维手册](../deploy/operations.md):节点 `state_dir` / `data_root` 备份与升级
