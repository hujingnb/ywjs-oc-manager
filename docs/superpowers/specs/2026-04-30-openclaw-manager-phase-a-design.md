# OpenClaw Manager Sub-project A 设计：闭环

日期：2026-04-30  
关联文档：[openclaw-manager-design.md](../../openclaw-manager-design.md) · [openclaw-manager-technical-design.md](../../openclaw-manager-technical-design.md)

## 1. 目标与范围

把容器生命周期、微信扫码绑定、应用详情聚合页串成一个可演示的真实闭环：管理员从“创建成员”能一路走到“容器跑起来 → 微信扫码 → 工作目录有产物”。

本 sub-project 不做：

- 充值 / 人设 / 多维度用量 / 节点心跳超时 reconciler / 知识库节点同步（属于 Sub-project B）。
- 集成测试套件 / Playwright E2E / OpenAPI client 生成（属于 Sub-project C）。

完成后能演示的链路：

1. 平台管理员创建组织 + 注册节点；
2. agent 用 bootstrap token 注册成功，开始心跳；
3. 组织管理员创建成员账号联动应用，worker 完成 `app_initialize`：分发镜像 → 创建 api_key（密文加密入库）→ 在 agent 上创建容器 → 启动 → 健康检查；
4. 应用详情页“渠道”tab 触发微信登录，弹出二维码；扫码后状态 → `running`；
5. 应用详情页“工作目录”tab 列出 OpenClaw 输出文件，可下载；
6. 应用详情页可启停 / 重启 / 删除容器，软删后 agent 把目录归档。

## 2. 架构

```
┌──────────────────┐
│  manager-api     │
│  (Gin)           │
│                  │
│  worker × N ─────┼──┐
│  scheduler × 1   │  │   Bearer + TLS
│                  │  │   Docker SDK over HTTPS
└──────────────────┘  ▼
                  ┌──────────────────────────┐
                  │ oc-runtime-agent (节点)  │
                  │  /v1/docker/* (透传)     │──► /var/run/docker.sock
                  │  /v1/files/*  (沙箱)     │──► /var/lib/oc-agent/
                  │  /healthz                │
                  └──────────────────────────┘
```

manager 进程内部由 `errgroup` 统一编排：HTTP server + N 个 worker goroutine + 1 个 scheduler goroutine，共享同一个 `context.Context`，SIGINT/SIGTERM 取消 ctx → 各 goroutine 退出。

## 3. 模块拆分

### A1 · Docker proxy + 自签 TLS + Bearer transport

**agent 端（`runtime/agent/`）**：

- 新增 `proxy.go`，使用 `httputil.ReverseProxy` 把 `/v1/docker/*` 请求透传到 `unix:///var/run/docker.sock`：
  - 请求 path 重写：`/v1/docker/<rest>` → `/<rest>`，让 docker daemon 看到原生 path。
  - 中间件：`Authorization: Bearer <agent_token>` 必须匹配 agent 启动时持有的 token；可选环境变量 `AGENT_TRUSTED_CIDR` 限制源 IP。
- 新增 `tls.go`：启动期生成 self-signed CA + leaf 证书（如本地未持久化），`runtime/agent/state/agent-tls.{crt,key,ca.crt}`；CA 证书在 agent register 时把 PEM 上报给 manager。
- 入口 `main.go` 改造：
  - 端口 `:7001`（docker proxy）和 `:7002`（file API）继续；前者强制 TLS，后者保持现状。
  - 文件目录扩展：`/var/lib/oc-agent/{orgs,apps,archived}/` 由 agent 自行创建。

**manager 端（`internal/integrations/agent/`）**：

- `docker_proxy.go`：`func NewDockerClientForNode(node sqlc.RuntimeNode, agentToken string) (*client.Client, error)`
  - 用 `node.AgentTLSCACert` 构造 `*x509.CertPool` → `*tls.Config{RootCAs: ..., ServerName: ...}`
  - 自定义 `http.RoundTripper` 注入 `Authorization: Bearer <agentToken>`
  - `client.NewClientWithOpts(client.WithHost(node.AgentDockerEndpoint), client.WithHTTPClient(...))`
- `agent_token_resolver.go`：从持久化层（DB 或加密 sidecar）取出 nodeID 对应的明文 agent_token。
  - **关键设计**：DB 只存 `agent_token_hash`；明文 token 只在 register 响应里返回过一次。Sub-project A 在 manager 内存里维护一份 `agentTokenCache`：`map[nodeID]string` 由 register 处理函数填充；进程重启后通过 rotate-bootstrap 重新注册。
  - 这是为 A 阶段保证可演示而做的 *已知妥协*；Sub-project B 会扩展为加密持久化（用 master_key 包裹 token，存到 `runtime_nodes.agent_token_ciphertext`）。设计文档已经在 16.4 风险节点提示 agent_token 持久化方案，这里把妥协写明。
- `internal/integrations/runtime/agent_backed.go` 增加 `dockerResolver func(nodeID) (*client.Client, error)`，cache 按 nodeID。

### A2 · AgentBackedAdapter 容器 ops + 4 个 worker handler

**Adapter 实现**：

| 方法 | 实现 |
|---|---|
| `EnsureImage` | 已实现（imagesync），保留 |
| `CreateContainer` | `cli.ImagePull` (跳过，镜像分发由 ImageDistributor 完成) → `cli.ContainerCreate(...)` 注入 env+mounts → `cli.ContainerStart` |
| `StartContainer` | `cli.ContainerStart` 幂等（already started 视为成功） |
| `StopContainer` | `cli.ContainerStop` with 30s timeout |
| `RestartContainer` | `cli.ContainerRestart` |
| `RemoveContainer` | `cli.ContainerRemove` force=true |
| `InspectContainer` | `cli.ContainerInspect` → `ContainerInfo` |
| `ListFiles/UploadFile/DownloadFile/ArchiveDirectory/DeletePath` | 已实现 (file_client) |

容器命名 `ocm-{app_id}`；mount 集合按 design.md §11.2：

| host (节点) | container | env |
|---|---|---|
| `{node_data_root}/apps/{app_id}/workspace` | `/workspace` | `OPENCLAW_WORKSPACE_DIR=/workspace` |
| `{node_data_root}/orgs/{org_id}/knowledge` | `/knowledge/org` (ro) | `OPENCLAW_KNOWLEDGE_ORG_DIR=/knowledge/org` |
| `{node_data_root}/apps/{app_id}/knowledge` | `/knowledge/app` (ro) | `OPENCLAW_KNOWLEDGE_APP_DIR=/knowledge/app` |
| `{node_data_root}/apps/{app_id}/state` | `/state` | — |
| `{node_data_root}/apps/{app_id}/logs` | `/logs` | — |

env 还包括 `OPENCLAW_API_BASE` / `OPENCLAW_API_KEY` / `OPENCLAW_SYSTEM_PROMPT` / `OPENCLAW_CHANNEL_PLUGIN`。

**Worker handler 新增**（`internal/worker/handlers/`）：

- `app_start_container.go` / `app_stop_container.go` / `app_restart_container.go`
  - payload `{app_id, runtime_node, requested_by}`
  - lookup app → 取 nodeID → 调对应 adapter 方法 → 写审计 → 状态推进
  - 幂等：start 已 running 直接 success；stop 已 stopped 同样 success
- `app_delete.go`
  1. `RemoveContainer`（容器不存在视为成功）
  2. `newapi.SetAPIKeyStatus(disable)`（key 不存在视为成功）
  3. `AgentFileClient.ArchiveApp(nodeID, appID)` —— agent 侧新增 `POST /v1/files/archive-app?app_id=...` 端点：把 `apps/{app_id}/` 整体 `mv` 到 `archived/{app_id}-{ts}/`；`file_client.go` 增加对应方法
  4. 删 manager 主副本 `apps/{app_id}/knowledge/`
  5. `apps.SoftDeleteApp` → 状态 `deleted`
- `app_initialize.go` 扩展：在 prompt 渲染之后真正调 `RuntimeAdapter.CreateContainer/InspectContainer`，把 container_id/name 写入 apps 表（`SetAppContainer`）。

cmd/server 启动时把这些 handler 注册到 `Registry`。

### A3 · WeChat CommandRunner

- `internal/integrations/channel/wechat_runner.go`：
  ```go
  type DockerCommandRunner struct {
      adapter  runtime.Adapter
      apps     AppLookup       // GetApp by uuid
  }
  func (r *DockerCommandRunner) StreamWeChatLogin(ctx, input) (<-chan string, error)
  ```
  - 用 `apps.GetApp(input.AppID)` 取 `container_id` 与 `runtime_node_id`
  - 通过 adapter 拿到 docker client，做 `ContainerExecCreate({Cmd: ["openclaw","channels","login","--channel","openclaw-weixin","--json"], AttachStdout: true})` + `ContainerExecAttach`
  - goroutine 读 attach 流，按行写入 `chan string`，stderr 合并；`ctx.Done` 时关闭流
- `cmd/server` 把 `DockerCommandRunner` 注入 `WeChatAdapter`，整体进 `channel.Registry`
- 新增 worker handler `channel_start_login.go` / `channel_check_binding.go`：服务于将来通过 job 异步触发，本期 ChannelService.BeginAuth 仍走同步路径（保留 worker handler 等 Sub-project B 启动周期任务时用）

### A4 · master_key 加密 + prompt template 配置

**配置加载**（`internal/config`）：

- `Config.Security.MasterKey` —— base64(32 字节)，启动期 `crypto/aes` 实例化时校验长度
- `Config.OpenClaw.SystemPromptTemplate` —— 启动期 `regexp.MustCompile` 检查必含 `{{workspace_dir}}`、`{{knowledge_org_dir}}`、`{{knowledge_app_dir}}`，否则 `log.Fatalf`
- 配置 `Config.OpenClaw.RuntimeImage`（已有）保持

**加密原语**（`internal/auth/crypto.go`）：

```go
type Cipher struct{ aead cipher.AEAD }
func NewCipher(masterKey []byte) (*Cipher, error)
func (c *Cipher) Encrypt(plaintext []byte) (string, error)  // 返回 base64(nonce||ciphertext||tag)
func (c *Cipher) Decrypt(token string) ([]byte, error)
```

**集成点**：

- cmd/server 启动期 `auth.NewCipher(decodedKey)` 实例化 `*Cipher`，注入到 `service.NewAppInitializeHandler(...)` 的 `Config`（`AppInitializeConfig` 增加 `Cipher *auth.Cipher` 与 `PlatformPrompt` 字段，PlatformPrompt 来自 `cfg.OpenClaw.SystemPromptTemplate`）
- `app_initialize` worker：`SetAppNewAPIKey` 时把 plaintext 经 `Cipher.Encrypt` 后写入 `newapi_key_ciphertext`
- 启动容器时若需要 plaintext，用 `Cipher.Decrypt` 现解，**不打日志**
- 既有数据：`apps.newapi_key_ciphertext` 之前都是空字符串，无需 backfill；新写入即是密文

### A5 · worker/scheduler 启动循环

`internal/worker/runner.go`：

```go
type Pool struct {
    worker      *Worker
    concurrency int
    interval    time.Duration
}
func (p *Pool) Run(ctx context.Context) error  // 启动 N 个 goroutine，各自 ticker 触发 Tick
```

`internal/scheduler/runner.go`：

```go
type Loop struct {
    scheduler *Scheduler
    interval  time.Duration
}
func (l *Loop) Run(ctx context.Context) error  // ticker 触发 Tick
```

`cmd/server/main.go`：

```go
g, ctx := errgroup.WithContext(rootCtx)
g.Go(func() error { return httpServer.ListenAndServe() })
g.Go(func() error { return workerPool.Run(ctx) })
g.Go(func() error { return schedulerLoop.Run(ctx) })
// signal handler -> cancel rootCtx
return g.Wait()
```

每个 worker goroutine 自带 `defer recover`，把 panic 转成 ERROR 日志而不是退出整个进程。

### A6 · App 详情聚合页 + 初始化重试

**后端**：

- `POST /api/v1/apps/:appId/initialize`
  - 权限：平台/组织管理员；普通成员仅当应用归己且当前 `status=error`
  - service.AppService 新增 `RetryInitialize(ctx, principal, appID)`
  - 实现：检查 `status ∈ {error, draft}` → 写库置 `status=draft`、`api_key_status=pending` → 入队 `app_initialize` job
- 应用 GET 接口已存在，前端通过它取最新状态

**前端**：

- 路由：
  ```
  /apps/:appId            → AppDetailLayout (默认子路由 overview)
    ├ overview            AppOverviewTab.vue   (状态 + 容器 + api_key + 最近 job + 重试)
    ├ channels            (复用现有 AppChannelsTab.vue)
    ├ knowledge           AppKnowledgeTab.vue
    ├ workspace           (复用现有 AppWorkspaceTab.vue)
    └ runtime             AppRuntimeTab.vue    (Inspect + 启停)
  ```
- `AppDetailLayout.vue`：左侧 tab 列表 + RouterView，顶部展示 `AppStatusTag`；公共数据通过 `provide/inject` 暴露 `app: Ref<AppDTO>`
- `AppKnowledgeTab.vue`：基本仿 `OrgKnowledgePage`，路径前缀替换为 `/api/v1/apps/:appId/knowledge`，hooks 复用 `useKnowledge`（已有 `useAppKnowledgeQuery`，扩展 upload/delete）
- `AppRuntimeTab.vue`：调 `apiRequest('/api/v1/apps/:appId/runtime')`（A2 阶段新增的 InspectContainer 透传 endpoint，路由：`GET /api/v1/apps/:appId/runtime` → 调 `RuntimeAdapter.InspectContainer`）
- 应用列表 AppsPage 的“查看详情”改成 `RouterLink to={path:`/apps/${app.id}`}`

## 4. 数据模型变更

仅一处：

- `apps` 表 **不变**；既有 `container_id` `container_name` `newapi_key_ciphertext` `newapi_key_id` 字段都已存在。
- 不引入新表。

## 5. 接口契约新增

| 方法 | 路径 | 备注 |
|---|---|---|
| POST | `/api/v1/apps/:appId/initialize` | 重试 |
| GET | `/api/v1/apps/:appId/runtime` | InspectContainer 透传 |
| 已有 GET `/api/v1/apps/:appId` 与 runtime ops | — | 复用 |

OpenAPI 同步更新；Sub-project C 才把 OpenAPI client 生成全跑通，A 阶段只手工同步 yaml 文件。

## 6. 错误处理策略

| 场景 | 策略 |
|---|---|
| Docker proxy TLS 失败 | manager 端在 `NewDockerClientForNode` 时 dial 一次 `/_ping`；连续失败 3 次把 node 标 `unreachable`（A 阶段做被动判定，主动 reconcile 留给 Sub-project B） |
| 容器 already exists | `CreateContainer` 检测 409 → `InspectContainer` 拿 ID 复用 |
| 容器 already started/stopped | start/stop 把 304/304-like 视为成功 |
| WeChat exec 卡住 | `ContainerExecAttach` 用 `ctx` 30s 超时 + `ContainerExecInspect` 兜底 |
| Worker panic | `defer recover` → log + `MarkJobFailed` 当前 attempt |
| master_key 不合法 | 启动 `log.Fatalf` |
| api_key 解密失败 | 容器创建失败 → 应用 `error`，提示“密钥已损坏，请重置” |

## 7. 测试矩阵

| 范围 | 用例 |
|---|---|
| `internal/auth/crypto` | 加密 → 解密往返；篡改后 GCM 校验失败；nonce 唯一性（统计 1k 次冲突） |
| `internal/config` | master_key 长度 / prompt 占位符校验 |
| `internal/integrations/agent/docker_proxy` | 用 `httptest.Server` 模拟 agent，验证 transport 注入 Bearer 与 CA 校验路径 |
| `internal/integrations/runtime/agent_backed` | 用 fake docker client 验证 ContainerCreate 参数（image、env、mounts） |
| `internal/integrations/channel/wechat_runner` | 用 fake adapter 模拟 ExecAttach 流，断言 stdout 行被切分 |
| `internal/worker/handlers/app_start/stop/restart/delete` | fake adapter + audit + 状态推进 |
| `internal/worker/runner` | ctx cancel → goroutine 退出；panic 不影响其它 worker |
| `internal/scheduler/runner` | ticker 触发 Tick；ctx cancel 退出 |
| `internal/api/handlers/apps` | retry initialize 状态前置校验；runtime endpoint Inspect 透传 |
| 前端 vitest | AppDetailLayout 路由切换；AppOverviewTab 在不同 status 下按钮可见性 |
| 前端 vue-tsc | 全量类型 |
| 浏览器 | chrome-devtools MCP（一旦解除阻塞）：登录 → 应用列表 → 详情 → 各 tab |

## 8. 安全与运维

- agent token 在 manager 进程内的内存 cache 在 Sub-project A 落地；明文不写日志、不落库（hash 已存）。Sub-project B 会做加密持久化。
- master_key 通过 `${MASTER_KEY}` 环境变量注入，本仓库 `.env.example` 加占位（不放真值）。
- Docker proxy 端口 `:7001` agent 容器内部监听，宿主机 bind 由 docker-compose 控制；生产部署文档（C 阶段）会要求加防火墙。
- 关闭时间预算：SIGTERM 后 30s 内完成 worker / scheduler / http server 退出；超时则强制退出。

## 9. 已知妥协与后续衔接

| 项 | A 阶段 | 后续 |
|---|---|---|
| agent_token 持久化 | 内存 cache，重启需重新 rotate-bootstrap | B 用 master_key 加密入库 |
| 节点心跳超时 reconciler | 仅被动（manager 调 docker 失败累计） | B 引入定时 job 主动判定 |
| 容器健康检查 | 创建后单次 `cli.ContainerInspect` 验证 running | B 引入 `app_health_check` 周期任务 |
| 知识库 tar 流批量同步 | 现有 file_client.UploadFile 单文件循环 | B 引入 `SyncOrgKnowledge` tar 端到端 |
| OpenAPI client 生成 | 手工 yaml | C |
| Playwright E2E | 不做 | C |

## 10. 验收标准

- 所有新增/改动 Go 代码 `go vet ./...` + `go test ./... -count=1` 全绿
- 前端 `vitest --run` + `vue-tsc --noEmit` + 容器内 `npm run build` 全绿
- `make check-compose` 通过
- 在本地 docker-compose 环境完成一次 happy path 演示：登录 → 创建组织 → 注册节点 → agent 注册 → 创建成员（应用 → 容器 running）→ 微信扫码（用 mock OpenClaw runtime 时跳过实际扫码，验证 challenge 走通）→ 查看工作目录 → 停容器 → 删除应用
- 每个新 worker handler 有对应单测覆盖 happy + 失败重试 + 幂等
- master_key 缺失 / 非法时 manager 启动失败，日志清楚指明
