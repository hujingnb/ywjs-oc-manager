# oc-manager 上 Kubernetes 迁移设计

> 状态：设计已确认（2026-05-29），待评审后拆分实现 spec。
> 本文档是**主设计文档**，记录目标架构与全部关键决策；实现拆分见 §9。

## 1. 背景与目标

### 1.1 现状

manager 是控制面，通过 **runtime-agent 节点**间接管理 app：

- 每个节点跑一个独立 `runtime-agent` 进程，暴露两个 HTTPS 端口——docker proxy(7001) + file API(7002)，自签 TLS、自注册（enroll）、30s 心跳、manager 周期 probe。
- app 实例 = 一个跑 **Hermes 镜像**的 docker 容器，挂两个宿主卷：`input`(只读)→`/opt/oc-input`、`data`(可写)→`/opt/data`。
- manager 把 docker 命令代理到节点 daemon 来管容器；用 docker exec 跑 Hermes 命令；用 file API 读写 app 文件。
- 节点选择按 `max_apps` 剩余容量；数据库 Postgres(pgx+sqlc)；Redis 做队列+分布式锁。

### 1.2 目标

线上全栈跑在 k8s 上，具体：

1. **去掉 runtime-agent 节点概念**——manager 直接用 k8s API 管理 app（pod 调度交给 k8s）。
2. **文件存储 S3 化**——manager 与 app 不再依赖宿主本地盘。
3. **Postgres → 自建 MySQL 8**。
4. **全栈上 k8s**：manager-api / manager-web / new-api / ragflow（含其依赖）/ 共享 MySQL·Redis·MinIO；**删除 ollama**。
5. 本地用 **k3d + minio** 模拟；生产按 vendor-neutral 标准 k8s 设计（S3/StorageClass 可插拔）。

### 1.3 非目标

- 不引入 service mesh、不做自动扩缩容（HPA）调优（v1 用固定副本 + ResourceQuota）。
- 不保留 Postgres 兼容（生产+本地都切 MySQL，单方言）。
- ollama 渠道能力（去除后由 new-api 其他供应商承接）。

---

## 2. 关键决策汇总

| # | 决策点 | 选择 | 理由 |
|---|---|---|---|
| D1 | DB | Postgres → 自建 MySQL 8（生产+本地） | 硬要求 |
| D2 | 存量数据 | manager 与 new-api 均全新库，无 ETL | 新部署 |
| D3 | migration | 29 个压成单个 MySQL 基线 | 切换场景历史无意义，省 churn |
| D4 | UUID | 应用层生成、存 CHAR(36) | DB 无关、可读；本量级性能差异可忽略 |
| D5 | app 对象 | Deployment replicas=1 + strategy=Recreate | 控制器自愈 + 单写者保证 |
| D6 | app 数据持久化 | pod 零 PVC；emptyDir scratch + S3 为持久源 | pod 可任意调度、节点概念消失 |
| D7 | app 启动信息 | 启动时回调 manager-api `bootstrap` 拉取 | api_key 不落盘/落 S3，DB 为唯一真相源 |
| D8 | pod→manager 认证 | per-app bootstrap token（hash 存 DB，k8s Secret 注入） | 简单可携、与厂商无关 |
| D9 | hermes 命令(oc-*) | 收敛为 pod 内 **oc-ops HTTP 服务**（第二容器，基于 hermes 镜像、共享 /opt/data），替代 k8s exec | 解决 watch 流式风险、去 `pods/exec` 权限、类型化契约 |
| D10 | 命名空间 | app pod 统一放 `oc-apps` | 简单、单 ns RBAC |
| D11 | 指标 | metrics-server（仅 CPU/内存） | 零额外组件；砍掉网络/磁盘两项 |
| D12 | 打包 | 裸 manifest | 用户选择；环境差异隔离进少量文件 |
| D13 | ragflow | 手写全套 manifest | 可控；顺带指向共享 MySQL/Redis/MinIO |
| D14 | 状态件 | MySQL/Redis/MinIO 共享实例（生产可切云托管） | 本地轻量 |
| D15 | 同步策略 | sidecar `mc mirror` 5-10s 增量 + preStop 全量 | 近实时、零额外组件 |
| D16 | sqlite | `.backup` 一致性快照定时上传 | WAL 库唯一安全的进 S3 方式 |
| D17 | 丢失窗口 | 优雅终止零丢失；硬 kill 丢会话尾部增量 | 已接受 |

---

## 3. 目标架构

```
┌─────────────────────────────── k8s 集群 ───────────────────────────────┐
│                                                                         │
│  ns: oc-system                          ns: oc-apps                     │
│  ┌──────────────┐                       ┌─────────────────────────────┐ │
│  │ manager-api  │──client-go (in-cluster)──▶ Deployment(1)+Recreate    │ │
│  │  (Deploy, HA)│   k8s API: create/exec/  │  app=<id>                  │ │
│  │     │  ▲     │   scale/delete/watch     │  ┌──────────────────────┐ │ │
│  │     │  └─────┼── pod bootstrap 回调 ─────┼──│ initContainer(restore)│ │ │
│  │     │        │   (per-app token)        │  │ main: hermes (no chg) │ │ │
│  │  manager-web │                          │  │ sidecar: mc sync      │ │ │
│  └─────┼────────┘                          │  └──────────────────────┘ │ │
│        │                                   │  emptyDir /opt/{data,oc-input}│
│  ┌─────┴───────── 共享状态件 ──────────────┐ └─────────────────────────────┘ │
│  │ MySQL8(StatefulSet+PVC): ocm/new-api/ragflow 各 db                    │ │
│  │ Redis(prefix 隔离)   MinIO(bucket 隔离, 当 S3)                          │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│  ┌──────────┐ ┌──────────────────────────┐ ┌────────────┐               │
│  │ new-api  │ │ ragflow(+ES 独占)          │ │metrics-srv │  Ingress      │
│  └──────────┘ └──────────────────────────┘ └────────────┘  traefik/nginx │
└─────────────────────────────────────────────────────────────────────────┘
   删除: runtime-agent / docker proxy / file API / 节点 enroll-heartbeat-probe / ollama
```

**数据流要点**：

- manager-api 只对 **k8s API + S3 + MySQL + Redis** 说话；不再有 manager→节点的 docker/file 通道。
- app pod 反向调用 **manager-api bootstrap** 拉配置（新增的 pod→manager 内网路径，走 Service DNS）。
- app 读 S3 用预签名 URL，写 S3 用按 app prefix 限定的临时凭证（STS）——pod 不持有长期 S3 密钥。
- app 连 new-api 走 Service DNS（同 ns 下 `http://new-api:3000` 短名大概率免改 base_url）。

---

## 4. Workstream A — 编排 k8s 化

### 4.1 运行时适配器

新增 `KubernetesAdapter` 实现现有 runtime 接口，替换 `internal/integrations/runtime/agent_backed.go` 的 `AgentBackedAdapter`。文件类操作从适配器剥离，改由 manager 侧 S3 SDK + bootstrap 端点承接。

### 4.2 生命周期映射（docker → k8s）

| 操作 | 现状 | k8s 形态 |
|---|---|---|
| 创建（原 5 阶段） | upload input→pull image→create→start→wait healthy | 渲染 bootstrap 数据 → 建 Secret(bootstrap token)+Deployment+Service → watch pod Ready。**选节点/预拉镜像/分步 create+start 全消失** |
| 启动 | StartContainer | scale 0→1 |
| 停止 | StopContainer | scale 1→0（preStop 触发最后一次 S3 全量同步） |
| 重启-换镜像 | Remove + 重新 init | patch `Deployment.spec...image`（Recreate 自动停旧起新） |
| 重启-不换镜像 | Stop+ClearSessions+Start | 删 S3 `sessions/`+`state.db` → 重启 pod |
| 删除 | stop→禁 new-api key→archive→清 KB→软删 | 删 Deployment/Service/Secret →禁 key→S3 prefix 搬 `archive/`→清 KB→软删 |

### 4.3 Hermes 命令 → oc-ops HTTP 服务（D9）

原来 manager 通过 docker/k8s exec 跑容器内 oc-* 脚本；现收敛为 pod 内一个 **oc-ops HTTP 服务**，manager 改用 HTTP 调用，**彻底取消 exec**。

**为何需要 hermes 镜像环境**（决定部署形态）：oc-* 多数依赖 hermes 运行环境——`oc-channel-login` import hermes venv 的 weixin SDK；`oc-cron`/`oc-kanban` `subprocess` 调 hermes CLI（kanban 是本地 sqlite 读写）；仅 `oc-info`/`oc-channel-status` 是纯文件读。故 oc-ops 服务须带 hermes 的 venv+CLI。

**部署形态**：同 pod 内**第二容器** `oc-ops`，**基于 hermes 镜像构建**、共享 `/opt/data` emptyDir。它内部仍做与现脚本相同的 venv import / `subprocess hermes` 调用，只是把入口从"被 exec 的脚本"换成"HTTP handler"。无需在 hermes 容器内做进程守护。

**端点映射**：
- 一次性 verb：`/oc/info`、`/oc/doctor`、`/oc/channel/{status,login,unbind}`、`/oc/cron/*`、`/oc/kanban/*` —— 返回类型化 JSON + 状态码，取代 stdout 解析。
- 流式：`/oc/kanban/watch` 用 **SSE/websocket**，取代长连接 exec 流，连接模型稳定。

**鉴权**：manager→oc-ops 用 per-app 控制 token（与 §5.3 bootstrap token 同一把，k8s Secret 注入；oc-ops 读 env 副本校验入站请求）。manager 对 `oc-apps` 不再需要 `pods/exec`。

**落地前置 spike**：确认 `oc-channel-login`/`oc-cron` 不依赖 hermes **活进程**（仅 venv/CLI + /opt/data 文件）。多进程并发读写 sqlite 由 WAL 锁保证安全（与现状"exec 独立进程"语义一致）。若发现需活进程交互，退路是 `shareProcessNamespace: true` 或改回 hermes 容器内协进程。

`oc-kb` 不在此列：它是 hermes 自身当 skill 出站调 manager 的，保持原样。

### 4.4 容器规格 → pod spec

| translateSpec 字段 | pod spec |
|---|---|
| Image | container image **+ imagePullSecrets**（替代节点 docker login） |
| Env(OC_INPUT_DIR/OC_DATA_DIR/OC_IMAGE_VARIANT…) | container env |
| Binds(input ro + data rw) | **删除** → emptyDir + initContainer(restore) + sidecar(sync) |
| NanoCPUs / Memory | resources.requests/limits |
| RestartPolicy always | Deployment 控制器管 |
| 多 docker network | Service DNS（无需显式网络） |
| Labels | pod labels（selector 定位） |
| HEALTHCHECK(oc-healthcheck) | exec readiness/liveness probe（`hermes gateway status`） |

### 4.5 RBAC

manager-api 用 in-cluster ServiceAccount，对 `oc-apps` ns 授予：`deployments`/`services`/`secrets`/`configmaps` 的 CRUD、`pods` get/list/watch、`pods/log` get。**不再需要 `pods/exec`**（hermes 命令改走 oc-ops HTTP，见 §4.3）——manager 权限收窄、安全姿态更好。

### 4.6 删除清单（节点概念影响面）

- 表与查询：`runtime_nodes`、`instance_resource_samples`、`internal/store/queries/runtime_nodes.sql`、`resource_samples.sql`。
- 服务/handler：enroll/heartbeat（`internal/api/handlers/agent.go`）、`probe_reconciler.go`、`internal/integrations/agent/probe.go`、`onboarding_service.go` 的 `selectNode`/`NodeWithCount`、`runtime_refresh_status` job、`internal/runtime/imagecoord`。
- 进程/部署：`runtime/agent/` 二进制、`deploy/runtime-agent/`、agent 的 docker_proxy + file API + 自签 TLS/token 机制。
- 配置：`runtime.enrollment_secret`、agent docker/file endpoint、`max_apps` 容量逻辑。

---

## 5. Workstream B — app 数据模型（S3 + 启动回调）

### 5.1 为什么不能「全走 S3 / 无本地文件」

Hermes 是文件系统原生 agent：

- `config.yaml` 渲染出 `terminal.backend: local`、`cwd: /opt/data/workspace`（`render_config_yaml.py:53-55`）——它在本地 shell 跑命令、改文件、跑 node/git。**live shell 工作目录必须 POSIX FS**，S3 缺原地/随机写、原子 rename、文件锁、可执行位。
- 会话状态是 **WAL sqlite**：`state.db` / `state.db-shm` / `state.db-wal`（`scopes.go:484`），依赖文件锁+mmap+原地写，对象存储与裸 S3 FUSE 必坏。

故 pod 运行期必然挂一个可写 POSIX 卷（emptyDir）；**S3 是持久化源头，本地 FS 是运行期临时盘**。

### 5.2 pod 结构（零 PVC）

```
initContainer "bootstrap-restore":
  1. 带 bootstrap token 调 manager-api: GET /internal/apps/{id}/bootstrap
  2. 把返回的 manifest(含 api_key)/resources 写入 emptyDir /opt/oc-input
  3. 用返回的预签名 URL 下载 skills → /opt/oc-input/skills
  4. 用预签名 URL restore workspace/sessions/state.db → /opt/data（首启则跳过）
main "hermes" (镜像零改动):
  oc-entrypoint 读 /opt/oc-input/manifest.yaml（无感来源），HERMES_HOME=/opt/data
sidecar "s3-sync" (独立 mc 镜像 + 脚本):
  - 每 5-10s: mc mirror /opt/data/workspace → S3（增量；排除 node_modules 等可重建大目录）
  - 每 N s: sqlite3 state.db ".backup snap.db" → 上传 snap.db 为 state.db
  - preStop: 一次全量同步 + sqlite 快照
sidecar "oc-ops" (基于 hermes 镜像构建; 见 §4.3):
  - 暴露 oc-* 的 HTTP 端点 + /oc/kanban/watch 流；manager 用控制 token 调用
  - 内部仍走 hermes venv/CLI 操作 /opt/data
共享: emptyDir 挂 /opt/data 与 /opt/oc-input，各容器可见
凭证: sidecar 用按 app prefix 限定的临时 STS 凭证（来自 bootstrap 响应）
```

### 5.3 启动回调（bootstrap）与认证

- 新增 manager-api 内部端点 `GET /internal/apps/{id}/bootstrap`：从 DB 实时渲染 manifest（含 api_key）+ resources，并签发 skills/workspace 的预签名读 URL 与上传用临时凭证。
- 认证（D8）：manager 创建 app 时生成随机 bootstrap token，**hash 存 DB**，明文以 **k8s Secret 注入 pod env**；pod 调 bootstrap 时带 token，manager 校验 hash。token 是仅能「拉本 app 配置」的窄凭证（非 api_key）。
- **接受的后果**：app pod 启动/重启强依赖 manager-api 在线 → manager-api 须多副本 HA。

### 5.4 S3 数据分类

| 数据 | 进 S3 方式 |
|---|---|
| manifest(含 api_key) + resources | **不进 S3**：bootstrap 端点内存渲染、经认证通道交给 pod |
| skills tar | S3（预签名下载） |
| workspace 产物 | S3（sidecar `mc mirror` 5-10s 近实时） |
| sqlite 会话/记忆(state.db+wal+shm) + sessions/ | S3（`.backup` 一致性快照定时） |
| 渲染产物 config.yaml/SOUL.md/env | 不持久化（每次启动幂等重渲染） |
| 删除归档 | S3 `archive/` |
| manager skill blob | S3（write-once/read-once） |

### 5.5 单写者与丢失窗口

- Deployment `replicas=1` + `Recreate`（先停旧再起新）保证同一 app 至多一个 pod 在写，避免 S3 脑裂。
- sqlite 绝不分别上传 `state.db`/`-wal`/`-shm`（时点不一致会损坏）；用 backup API 出一致副本再传，restore 时删 `-wal/-shm` 干净重开。
- 丢失窗口：优雅终止走 preStop 零丢失；仅硬 kill（节点宕机/OOMKilled/超 grace SIGKILL）丢「上次快照以来的会话增量」——已接受（D17）。

---

## 6. Workstream C — Postgres → MySQL 8

### 6.1 特性替换

| PG 特性 | 用量 | MySQL 替换 |
|---|---|---|
| `gen_random_uuid()` 默认 | 16 处/5 表 | 去 DB 默认，UUID 应用层生成（去 pgcrypto） |
| `RETURNING` | 59 处 | UUID 应用层已知 → INSERT 去掉；需服务端默认值的改 INSERT+SELECT |
| `timestamptz` | 51 处 | `DATETIME(6)` 存 UTC；驱动 `parseTime=true&loc=UTC` |
| JSONB | 17 列 | `JSON`；`jsonb_typeof`→`JSON_TYPE`、`@>`→`JSON_CONTAINS` |
| 唯一部分索引 `UNIQUE...WHERE` | ~5 个 | **生成列 + 唯一索引**（条件成立取键值否则 NULL；MySQL 唯一索引允许多 NULL）。如 `apps_owner_active`、`channel_bindings_app_active`、3 个 `ragflow_datasets_*` |
| 普通部分索引 | ~3 个 | 退化为普通索引 |
| CHECK 约束 | 多处 | MySQL 8.0.16+ 原生（未用 PG enum） |
| `ON CONFLICT` | 2 处 | `ON DUPLICATE KEY UPDATE` / `INSERT IGNORE` |
| boolean | 几处 | `TINYINT(1)` |
| `ANY(uuid[])` | 1 处 | 所属表随节点概念删除，不迁 |

### 6.2 工具链

- sqlc：`engine: postgresql` → `mysql`；驱动 `pgx/v5` → `database/sql + go-sql-driver/mysql`；生成代码全量重生成。
- golang-migrate：postgres driver → mysql driver。
- migration（D3）：29 个文件压成单个 MySQL 基线，直接写最终态 schema。
- 范围（D2）：manager(`ocm` 库)、new-api(`new-api` 库) 均全新库，渠道/key 由 manager 重新下发。

---

## 7. Workstream D — 全栈部署

### 7.1 组件清单

| 组件 | 形态 | 存储 | 备注 |
|---|---|---|---|
| manager-api | Deployment(多副本) + Service + SA/RBAC | 无（skill blob→S3） | 删 docker.sock 挂载 |
| manager-web | Deployment + Service | 无 | 静态 |
| new-api | Deployment + Service | 无 | DB 指共享 MySQL（改 DSN） |
| ragflow | 手写 Deployment + Service | 日志 PVC | 指共享 MySQL/Redis/MinIO |
| Elasticsearch | StatefulSet + PVC | PVC | ragflow 独占 |
| MySQL 8 | StatefulSet + PVC | PVC | 多 db：ocm/new-api/ragflow |
| Redis | StatefulSet/Deployment + PVC | PVC | prefix/db 隔离 |
| MinIO | StatefulSet + PVC | PVC | 当 S3；多 bucket；生产换云 OSS |
| metrics-server | addon | - | CPU/内存 |
| Ingress | traefik(本地)/nginx(prod) | - | 路由 web/api/new-api/ragflow 控制台 |
| app pod | Deployment(1)+Recreate @ oc-apps | emptyDir | 见 §5 |

> 注：「零 PVC」仅针对 Hermes app pod；基础设施（MySQL/Redis/MinIO/ES）本就有状态，正常用 PVC。

### 7.2 共享状态件（D14）

手写 ragflow（而非官方 chart）的利好：可让 ragflow 直接指向共享 MySQL/Redis/MinIO，仅 ES 独占。生产可通过环境差异文件切到云托管（RDS/云 Redis/OSS）。

### 7.3 裸 manifest 的环境参数化（D12）

裸 manifest 无模板，本地↔生产差异隔离进少量文件：Secret（DB/S3/registry/master_key 凭证）、一个 config ConfigMap、StorageClass、镜像 tag；用 Makefile target 分环境 `kubectl apply`。

### 7.4 本地开发环境（k3d）

本地开发统一用 **k3d 创建集群**，替代现有 docker-compose 联调栈（`docker-compose.yml`、`docs/local-development.md` 需相应更新）。

**集群与组件基线**：

- 内置 local-path 供 infra PVC；内置 traefik 做 ingress；minio 当 S3。
- manager client-go 一套配置两用：pod 内 `rest.InClusterConfig()`，本地开发 fallback 到 `KUBECONFIG` 指向 k3d。

**创建流程（设计级，具体命令在 spec-D 落地为 Makefile target）**：

1. `k3d cluster create`：单 server 节点，映射 ingress 端口（如 `80:80@loadbalancer`），并挂一个 k3d 内置 **local registry**，便于把本地构建的 manager/web/hermes 镜像推上去（避免每次 `k3d image import` 的慢路径；ACR 私有镜像走 imagePullSecrets）。
2. 应用 **infra manifest**：共享 MySQL/Redis/MinIO（StatefulSet+PVC）+ ragflow 的 ES，等待 Ready。
3. **初始化 S3**：在 minio 建 app 存储所需 bucket（+ragflow bucket）。
4. **建库与迁移**：在共享 MySQL 上建 `ocm`/`new-api`/`ragflow` 库，跑 manager 的 MySQL 基线 migration。
5. 应用 **控制面/业务 manifest**：manager-api、manager-web、new-api、ragflow，配 traefik ingress 路由。
6. **manager 运行位置**：开发态可二选一——跑在集群内（贴近生产），或本地直接 `go run` + `KUBECONFIG` 指向 k3d（最快迭代）。app pod 的 bootstrap 回调地址相应指向集群内 Service 或宿主可达地址。

整个流程用一个 `make local-up` 串起，`make local-down` 销毁集群。镜像：本地构建推 k3d registry；ACR 私有镜像直接拉。

---

## 8. 配置与密钥变更

**删除**：`runtime.enrollment_secret`、agent docker/file endpoint、docker socket 挂载、节点容量配置。

**新增**：
- `k8s.*`：namespace(`oc-apps`)、in-cluster/kubeconfig、imagePullSecrets、app 资源 requests/limits 默认。
- `storage.s3.*`：endpoint、bucket、region、credentials、SSE。
- MySQL DSN（替换 `database.url`）。

**k8s Secret 清单**：master_key、MySQL 凭证、S3/MinIO 凭证、registry(ACR) 凭证、**per-app 控制 token**（manager↔pod 双向复用：pod→manager bootstrap 拉配置、manager→oc-ops 调命令）、new-api/ragflow 各自 secret。

---

## 9. 实现拆分与顺序

拆 4 份实现 spec（各自独立 spec → plan → 实现）：

1. **spec-C：MySQL 迁移**——独立、可先行不阻塞其他工作。
2. **spec-A+B：编排 k8s 化 + app 数据模型**——耦合最紧、本迁移核心。
3. **spec-E：oc-* 收敛为 oc-ops HTTP 服务**——Hermes 镜像侧改动，相对独立；起手做一个 spike 确认无 hermes 活进程依赖（§4.3）。被 A（manager 改调 HTTP）依赖。
4. **spec-D：全栈部署 + 本地 k3d**——收口，依赖 A/B/C/E 产物。

**建议顺序**：C 与 E 可并行先行（互不依赖） →（A+B，A 依赖 E 的 oc-ops 契约） → D。

---

## 10. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| manager-api 启动期成为 app 硬依赖 | manager 挂则 app 起不来（§5.3） | manager-api 多副本 HA |
| 硬 kill 丢会话尾部 | 非优雅终止丢上次快照后的增量 | preStop 覆盖优雅路径；缩短快照间隔可减小窗口；已接受 |
| oc-ops 是否依赖 hermes 活进程 | 若 channel-login/cron 需活进程而非仅 venv/CLI+文件，第二容器方案不成立 | spec-E 起手 spike 确认；退路 `shareProcessNamespace` 或容器内协进程（§4.3） |
| oc-ops 容器须跟 hermes 版本 | 它基于 hermes 镜像构建，hermes 升级需同步 | 与 hermes 镜像同一构建链、同版本标签发布 |
| oc-ops 端点认证 | 重新引入容器 HTTP+token（窄于原 agent） | 复用 per-app 控制 token（k8s Secret），仅 oc-* verb 范围 |
| 唯一部分索引→生成列 | schema 形变，需逐个验证唯一语义 | 单元测试覆盖每个唯一约束的边界 |
| workspace 大目录同步成本 | node_modules 等高频变更 | 排除可重建大目录；`mc mirror` 仅传变更 |
| metrics 降级 | 失去网络/磁盘 I/O 指标 | 资源面板砍两项；后续需要再上 Prometheus |
