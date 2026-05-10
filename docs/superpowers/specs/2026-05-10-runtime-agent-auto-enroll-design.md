# Runtime Agent 自动注册 + manager 主动探测 设计

- 日期：2026-05-10
- 作者：hujing / Claude
- 状态：待 review
- 相关代码：`internal/service/runtime_node_service.go`、`internal/service/reconciler.go`、
  `internal/api/handlers/agent.go`、`internal/api/handlers/runtime_nodes.go`、
  `runtime/agent/main.go`、`runtime/agent/config/`、
  `internal/integrations/agent/`、`internal/migrations/`

## 1. 背景与目标

当前 runtime node 接入需要 5 步人工动作（平台管理员在后台点「注册节点」拿
bootstrap_token → 运维抄进节点 yaml 启 agent → 从日志抄回 agent_token →
回填 yaml 再重启一次 agent → 回后台看状态），中间 3 次 copy/paste、2 次
agent 重启。对日益增多的节点部署场景，这套流程既拖慢新节点接入，也是
人为失误的高发区。

本设计目标：

1. **消除管理员后台「创建节点」动作**。节点起 agent 即自动进入 `runtime_nodes`
   列表，后台只用于查看、启停、调 max_apps 等运行期管理。
2. **消除运维 copy/paste**。agent 自身持有身份 UUID 与 token，运维 yaml 只保留
   长期共享的 enrollment secret。
3. **manager 主动探测 agent 双端口连通性**。只看 agent 出站心跳会漏掉
   manager→agent 入站（7001 docker proxy、7002 file API）的中断场景；本次加
   probe reconciler 补齐能力。

不在本设计范围：

- enrollment_secret 热轮换（改配置 + 重启 manager 与所有 agent）
- 基于 trusted_cidr / mTLS / 审批的多因子鉴权加固
- 已有手工创建节点的迁移兼容（项目当前无存量节点，直接切换）

## 2. 核心决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 信任模型 | 共享 enrollment_secret | 实现最小、ops 心智与 `security.master_key` 一致 |
| 节点身份键 | 节点 state_dir 持久化 `agent-id` (UUID) | 容器重建、yaml 改名都不影响身份连续性 |
| 节点 name | agent.yaml 填、可重名 | name 仅做 UI 显示，不承担唯一性 |
| 状态机扩展 | 新增 `degraded` 状态 | 心跳通但 manager→agent 入站不通需区别于 active/unreachable |
| 失败调度语义 | `degraded` 与 `unreachable` 都不可调度新应用；`degraded` 不自动把节点上应用推 error | 入站不通不代表应用已崩 |
| enrollment_secret 轮换 | 不支持热轮换 | 第一版单值，改配置重启即可 |
| trusted_cidr 加固 | 不做 | 运维负责密钥不泄漏 |

## 3. 总体架构

```
┌──────────── Runtime Node ────────────┐         ┌──── Manager ────┐
│                                       │         │                  │
│  agent.yaml                           │         │ manager.yaml     │
│    manager.endpoint                   │         │   runtime:       │
│    manager.enrollment_secret  ────────┼─[1]─────┤     enrollment_  │
│    agent.name / advertise_host        │  enroll │     secret       │
│                                       │         │     probe.*      │
│  state_dir/                           │         │                  │
│    agent-id      ◄ UUID, 首次生成     │         │  PostgreSQL:     │
│    agent-token   ◄ enroll 后写入      │         │    runtime_nodes │
│    node-id       ◄ enroll 后写入      │         │      .agent_id   │
│    agent-tls.*   ◄ 已有               ├─[2]─────┤      (UNIQUE)    │
│                                       │ heartbeat│      .last_probe*│
│  docker proxy  :7001 (TLS+Bearer)     │◄────[3]─┤                  │
│  file API      :7002 (Bearer)         │ probe   │ probe reconciler │
└───────────────────────────────────────┘ 60s/拍  └──────────────────┘
```

三条通信通道：

1. **agent → manager 出站**：enroll（Bearer enrollment_secret）、heartbeat
   （Bearer agent_token）。
2. **agent → manager 心跳**：同上，30s 一拍。
3. **manager → agent 入站探测**：docker proxy `/v1/docker/_ping` +
   file API `/v1/files/ping`，60s 一拍，双端口都绿才算健康。

## 4. 配置

### 4.1 manager.yaml 新增

```yaml
runtime:
  enrollment_secret: "<base64, 解码后 32 字节>"   # 必填，缺失 fail-fast
  probe:
    interval_seconds: 60     # 探测周期
    timeout_seconds: 3       # 单次 HTTP 超时
    failure_threshold: 3     # active 连续失败 N 次 → degraded
    recovery_threshold: 2    # degraded 连续成功 N 次 → active
```

`RuntimeConfig.EnrollmentSecret` 校验规则：非空 + 合法 base64 + 解码后 32 字节，
与现有 `security.master_key` 校验对称（`internal/config/loader.go`）。

`runtime.probe.*` 所有字段都有合理默认值（即示例值），缺失时 loader 兜底填
默认；出现非法值（≤0）视为 fail-fast。

### 4.2 agent.yaml 调整

新增 / 改动：

```yaml
manager:
  endpoint: https://manager.example/api/v1
  enrollment_secret: "<同 manager>"   # 新增，必填
  ca_bundle: ...                      # 保留
  skip_verify: ...                    # 保留
  # 删除：node_id / agent_token

agent:
  name: node-prod-shanghai-01         # 新增，留空则用 hostname；仅显示，不唯一
  advertise_host: 10.1.2.3            # 新增，节点对 manager 可见的 IP/域名；留空回落 hostname
  data_root: ...                      # 已有
  state_dir: /var/lib/oc-agent/state  # 已有
  docker_addr: ":7001"                # 已有
  file_addr:   ":7002"                # 已有
  # 删除：agent.token（被 state_dir/agent-token 取代）
  trusted_cidr: ...                   # 已有，与本设计无关
```

loader 兼容：`manager.node_id` / `manager.agent_token` / `agent.token` 若
yaml 仍存在，加载时打 WARN 日志说明已废弃、值被忽略。

### 4.3 state_dir 新增三个文件

| 文件 | 内容 | 权限 | 写入时机 |
|---|---|---|---|
| `agent-id` | UUID v4 文本行 | 0600 | agent 启动时若不存在则生成 |
| `agent-token` | manager 颁发的明文 token | 0600 | enroll 成功后写入 |
| `node-id` | manager 返回的 node UUID | 0600 | enroll 成功后写入 |

与现有 `agent-tls.crt` / `agent-tls.key` 同级，保护等级一致。state_dir 必须
挂载为持久化卷是部署强约束（README 已明确）；丢失视为节点身份丢失，按
「disable 旧行，agent 会以新 agent_id 自动 enroll 为新行」处置。

## 5. 数据库迁移 000008_runtime_nodes_auto_enroll

```sql
-- up
ALTER TABLE runtime_nodes
  ADD COLUMN agent_id             text        NULL,
  ADD COLUMN last_probe_ok_at     timestamptz NULL,
  ADD COLUMN last_probe_failed_at timestamptz NULL,
  ADD COLUMN last_probe_error     text        NULL,
  ADD COLUMN probe_failure_streak integer     NOT NULL DEFAULT 0,
  ADD COLUMN probe_success_streak integer     NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX runtime_nodes_agent_id_uniq
  ON runtime_nodes(agent_id) WHERE agent_id IS NOT NULL;

ALTER TABLE runtime_nodes DROP CONSTRAINT runtime_nodes_name_key;
ALTER TABLE runtime_nodes DROP COLUMN bootstrap_token_hash;
ALTER TABLE runtime_nodes DROP COLUMN bootstrap_token_expires_at;

ALTER TABLE runtime_nodes DROP CONSTRAINT runtime_nodes_status_check;
ALTER TABLE runtime_nodes
  ADD CONSTRAINT runtime_nodes_status_check
  CHECK (status IN ('pending','active','unreachable','disabled','degraded'));

COMMENT ON COLUMN runtime_nodes.agent_id IS
  'agent 自身持久化的 UUID（state_dir/agent-id），全表唯一；enroll 幂等键。';
COMMENT ON COLUMN runtime_nodes.last_probe_ok_at IS
  'manager 最近一次对 agent 双端口探测全部通过的时间。';
COMMENT ON COLUMN runtime_nodes.last_probe_failed_at IS
  'manager 最近一次对 agent 双端口探测任一失败的时间。';
COMMENT ON COLUMN runtime_nodes.last_probe_error IS
  '最近一次 probe 失败的简短错误分类（docker:tls / file:401 / both:timeout 等）。';
```

down 文件对称恢复，包括：删除新增列与索引、恢复 `name` 唯一约束、
恢复旧状态约束、恢复 `bootstrap_token_hash` / `bootstrap_token_expires_at` 列。

不修改 `agent_token_hash` / `agent_token_ciphertext` / `node_data_root` /
`resource_snapshot_json` / `metadata_json` / `max_apps` / `registered_at`，
语义延续。

## 6. 协议

### 6.1 新端点：POST /api/v1/agent/enroll

```http
POST /api/v1/agent/enroll
Authorization: Bearer <enrollment_secret>
Content-Type: application/json

{
  "agent_id":              "550e8400-e29b-41d4-a716-446655440000",
  "name":                  "node-prod-shanghai-01",
  "hostname":              "ip-10-1-2-3",
  "agent_docker_endpoint": "https://10.1.2.3:7001",
  "agent_file_endpoint":   "https://10.1.2.3:7002",
  "agent_tls_ca_cert":     "<base64 PEM>",
  "agent_version":         "v1.0.2",
  "node_data_root":        "/var/lib/oc-agent/data",
  "resource_snapshot":     { "cpu": ..., "mem": ..., "disk": ... },
  "metadata":              { "docker_version": "27.0", "os_kernel": "6.1" }
}
```

响应：

- `200 OK` → `{ "node_id": "...", "agent_token": "...", "heartbeat_interval_seconds": 30 }`
- `401 Unauthorized` → enrollment_secret 不匹配（常量时间比较）
- `400 Bad Request` → agent_id 缺失 / 非 UUID；name 为空；endpoint 字段缺失或非合法 URL；agent_tls_ca_cert 非合法 PEM

服务端语义：

1. 中间件常量时间比较 `Authorization: Bearer <enrollment_secret>`，失败返 401。
2. 基础字段校验（agent_id UUID、name 非空、endpoint URL、CA PEM）。
3. 按 `agent_id` 查 `runtime_nodes`：
   - **命中**（同节点重启 / token 失效重 enroll）：
     - UPDATE `agent_docker_endpoint` / `agent_file_endpoint` / `agent_tls_ca_cert` /
       `agent_version` / `node_data_root` / `resource_snapshot_json` / `metadata_json`
     - 重新生成 `agent_token`，写入 `agent_token_hash` 与
       `agent_token_ciphertext`（沿用现有 token_resolver 加密）
     - `status` → `active`；`registered_at` COALESCE 保留首次值；`last_heartbeat_at = now()`
     - 重置 `probe_failure_streak = 0` / `probe_success_streak = 0`（新 token 后清空探测计数）
     - **name 不刷新**，避免 yaml 改名后 UI 悄悄变
   - **未命中**：
     - INSERT 新行：填入所有自报字段；`status = active`；
       `heartbeat_interval_seconds = 30`；`registered_at = now()`；
       `last_heartbeat_at = now()`
     - 发放首个 `agent_token`（同上 hash + ciphertext）
4. 写一条 `audit_logs`：
   - `actor_role = 'runtime_agent'`、`actor_id = NULL`
   - 命中 → `action = 'agent_re_enrolled'`；未命中 → `action = 'agent_enrolled'`
   - `target_type = 'runtime_node'`、`target_id = node.id`、`result = 'succeeded'`
5. 返回 node_id + 明文 agent_token。

### 6.2 保留端点：POST /api/v1/agent/heartbeat

agent 端从 `state_dir/agent-token` 读取 token 置入 Bearer 头。

服务端行为变更：现有 `UpdateRuntimeNodeHeartbeat` SQL 硬编码 `SET status = 'active'`，
会把 `degraded` 节点凭一次心跳直接拉回 active、绕过 probe 判断。必须改成
**只把 `unreachable` 推回 `active`、对 `degraded` 节点不动状态、其他状态保持**：

```sql
-- 改造后的 UpdateRuntimeNodeHeartbeat
UPDATE runtime_nodes
SET status = CASE
              WHEN status = 'unreachable' THEN 'active'
              ELSE status
             END,
    agent_version = $2,
    last_heartbeat_at = now(),
    resource_snapshot_json = $3,
    metadata_json = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

degraded → active 的翻转统一由 probe reconciler 负责，避免心跳与 probe 打架。

### 6.3 删除端点

| 端点 | 处置 |
|---|---|
| `POST /api/v1/agent/register` | 删路由 + handler + `service.RegisterAgent` |
| `POST /api/v1/runtime-nodes` | 删路由 + handler.Create + `service.CreateNode` |
| `POST /api/v1/runtime-nodes/:id/rotate-bootstrap` | 删路由 + handler.RotateBootstrap + `service.RotateBootstrap` + `store.BootstrapRotator` 接口与实现 |

对应 DTO（`AgentRegisterRequest`、`CreateRuntimeNodeRequest`）同步删除。
swag 注解重新生成，openapi.yaml 与 web 类型跟随更新（`make openapi-gen` + `make web-types-gen`）。

### 6.4 保留的管理员端点

- `GET  /api/v1/runtime-nodes`
- `GET  /api/v1/runtime-nodes/:id`
- `PATCH /api/v1/runtime-nodes/:id`（仅 max_apps）
- `POST /api/v1/runtime-nodes/:id/disable`
- `POST /api/v1/runtime-nodes/:id/enable`

响应 `RuntimeNodeResult` 增补字段：`agent_id` / `last_probe_ok_at` /
`last_probe_failed_at` / `last_probe_error` / `probe_failure_streak`。
删除 `bootstrap_token` / `bootstrap_token_expires_at` 字段。

## 7. agent 启动状态机

```
                  ┌────────────────────┐
                  │ ensure agent-id     │
                  │ (gen UUID if absent)│
                  └─────────┬───────────┘
                            ▼
                  ┌────────────────────┐
                  │ token & node-id     │
                  │ in state_dir?       │
                  └────┬─────────────┬──┘
                    no │             │ yes
                       ▼             ▼
                       │       ┌──────────────┐
                       │       │ heartbeat 1x │
                       │       └──┬───────┬───┘
                       │          │ 200   │ 401
                       │          │       │
                       ▼          ▼       ▼
                  ┌──────────────────────────┐
                  │  enroll                   │
                  │  Bearer enrollment_secret │
                  └─────┬──────────────┬──────┘
                        │ 200          │ ≠200
                        ▼              ▼
               ┌──────────────────┐  exponential backoff
               │ write agent-token│  5s → 10s → 20s → 40s → 60s（cap）
               │ write node-id    │  持续重试直到成功
               │ (perm 0600)      │
               └────────┬─────────┘
                        ▼
               ┌──────────────────┐
               │ 心跳循环          │
               │ 401 → 触发 enroll │
               └──────────────────┘
```

关键点：

- `agent-id` 一旦生成即 sticky，容器重建只要 state_dir 还在就不变。
- `advertise_host` 决定 enroll body 里自报的 endpoint：agent 把 `docker_addr`
  / `file_addr` 中的端口（例如 `:7001`、`:7002`）与 `advertise_host` 拼成
  `https://{advertise_host}:7001` / `https://{advertise_host}:7002` 自报；
  `advertise_host` 留空则用 `os.Hostname()` 回落。文档强调公有云 / NAT 场景
  必填具体可达 IP 或域名。
- 心跳连续收到 401 视为「manager 那边 token 已失效」（比如运维 disable 又 enable），
  触发一次 re-enroll；其他网络错误走现有失败计数路径。
- enroll 重试采用指数退避 + 60s 上限；不做失败次数上限（agent 是长生命周期进程，
  网络恢复后就应该能恢复）。

## 8. manager 主动探测

### 8.1 探测协议

两个 HTTP GET，并发发出：

| 目标 | URL | Header | 判定通过 |
|---|---|---|---|
| Docker 端口 | `{agent_docker_endpoint}/v1/docker/_ping` | `Authorization: Bearer {agent_token}` | HTTP 200 且 body = `"OK"` |
| 文件端口 | `{agent_file_endpoint}/v1/files/ping` | `Authorization: Bearer {agent_token}` | HTTP 200 且 JSON `{"status":"ok"}` |

`/v1/files/ping` 已存在（`runtime/agent/main.go:230`）；需要在 agent 侧
给它加 Bearer 鉴权（现在是完全公开），与 docker `_ping` 对齐。

`/v1/docker/_ping` 是 docker proxy 已透传的 docker daemon API，agent 无需新增代码。

超时：`runtime.probe.timeout_seconds`（默认 3s）。
HTTP client 用现有的 agent TLS root pool（`runtime_nodes.agent_tls_ca_cert`）。

### 8.2 ProbeResult 结构

```go
type ProbeResult struct {
    DockerOK   bool
    FileOK     bool
    DockerErr  string  // "" / "tls:*" / "timeout" / "401" / "5xx:502" / "dns" / "connrefused"
    FileErr    string
    LatencyMs  int64   // 总耗时，取两个并发中较大值
}
```

错误分类规则：DNS 解析失败 → `"dns"`；TLS 握手失败 → `"tls:<cause>"`；
net.Error.Timeout → `"timeout"`；HTTP 401 → `"401"`；HTTP 5xx → `"5xx:<code>"`；
连接被拒 → `"connrefused"`；其他 → `"other:<简短错误>"`。

### 8.3 NodeProbeReconciler

新文件 `internal/service/probe_reconciler.go`，与 `NodeHealthReconciler` 平级。

扫描范围：`status IN ('active','degraded')` 的节点（忽略 `disabled` / `unreachable` /
`pending`）。

逐节点行为：

```
解密 agent_token ciphertext（token_resolver）
  ciphertext 缺失或解密失败 → 跳过该节点 + WARN 日志 + 写 last_probe_error="token:decrypt-failed"
  （不翻 degraded：probe 是诊断手段，自身故障不应让被监控对象背锅）
并发 probe Docker + File
  两个都 OK：
    UPDATE last_probe_ok_at = now(),
           probe_success_streak = probe_success_streak + 1,
           probe_failure_streak = 0,
           last_probe_error = NULL
    IF status = 'degraded' AND probe_success_streak >= recovery_threshold:
      status → 'active'
  任一 FAIL：
    UPDATE last_probe_failed_at = now(),
           probe_failure_streak = probe_failure_streak + 1,
           probe_success_streak = 0,
           last_probe_error = 组合字符串（例："docker:tls:x509 / file:timeout"）
    IF status = 'active' AND probe_failure_streak >= failure_threshold:
      status → 'degraded'
```

状态翻转写 audit_logs（`action = 'node_probe_degraded' / 'node_probe_recovered'`、
`actor_role = 'system'`）。

`degraded` **不**把节点上应用推 `error`（与 `unreachable` 行为区别）；
原因：入站不通不代表应用已崩，推 error 会造成误报。

### 8.4 调度排除

`onboarding_service.CreateAppForMember` 现在通过 `node_selector` 选节点时
走 `ListActiveNodesWithAppCounts`，其 WHERE 是 `n.status = 'active'`。
`degraded` 自然被排除，`node_selector.go` 无需改动。

### 8.5 生命周期接入

`cmd/server/main.go` 里现有 `NodeHealthReconciler` 已经通过
`PeriodicReconciler` 挂在 errgroup。新 `NodeProbeReconciler` 同样方式挂载：

```go
probe := service.NewNodeProbeReconciler(store, probeClient, tokenResolver, probeCfg)
probeTask := service.NewPeriodicReconciler("node-probe", probeInterval, probe.Reconcile)
g.Go(func() error { return probeTask.Run(ctx, logger) })
```

## 9. 文件改动清单

### 9.1 Go（manager）

新增：

- `internal/migrations/000008_runtime_nodes_auto_enroll.up.sql`
- `internal/migrations/000008_runtime_nodes_auto_enroll.down.sql`
- `internal/integrations/agent/probe.go` + `probe_test.go`
- `internal/service/probe_reconciler.go` + `probe_reconciler_test.go`
- `internal/api/middleware/enrollment_secret.go`（或直接内嵌 handler，按最小改动）

修改：

- `internal/store/queries/runtime_nodes.sql`：
  - 新增：`EnrollRuntimeNodeInsert`（按 agent_id 插入）、`EnrollRuntimeNodeUpdate`（按 agent_id 刷新）、`GetRuntimeNodeByAgentID`、`UpdateNodeProbeResult`
  - 现有 `ListActiveNodesWithAppCounts` 不改，`degraded` 自然被 `status = 'active'` 过滤
  - 删除：`CreateRuntimeNode`、`RegisterRuntimeNode`
  - 改造 `UpdateRuntimeNodeHeartbeat`：把硬编码 `status = 'active'` 改为 `CASE WHEN status='unreachable' THEN 'active' ELSE status END`（§6.2）
  - 改造 `SetRuntimeNodeStatus`：允许写入 `degraded`
- `internal/store/sqlc/*`（sqlc generate）
- `internal/store/runtime_node_store.go`：删 `BootstrapRotator` 实现
- `internal/service/runtime_node_service.go`：
  - 新增 `EnrollAgent(ctx, EnrollInput) (EnrollResult, error)`
  - 删除 `CreateNode` / `RotateBootstrap` / `RegisterAgent`、相关 Input/Result 类型
  - 保留 `ListNodes` / `GetNode` / `HandleHeartbeat` / `SetNodeStatus` / `UpdateMaxApps`
  - `RuntimeNodeResult` 增字段（§6.4）
- `internal/service/errors.go`：新增 `ErrEnrollmentSecretInvalid` / `ErrEnrollInputInvalid`；删除 `ErrRuntimeNodeBusy`（rotate 场景移除）
- `internal/service/reconciler.go`：无改动（NodeHealthReconciler 语义保留）
- `internal/api/handlers/agent.go`：
  - 新增 `Enroll(c *gin.Context)` + 路由 `POST /api/v1/agent/enroll`
  - 删除 `Register` + 路由
  - `withEnrollmentSecret(cfg)` 中间件，constant-time 比较
- `internal/api/handlers/runtime_nodes.go`：
  - 删 `Create` + `POST /runtime-nodes` 路由
  - 删 `RotateBootstrap` + `POST /runtime-nodes/:id/rotate-bootstrap` 路由
- `internal/api/handlers/dto.go`：
  - 新增 `AgentEnrollRequest`
  - 删 `AgentRegisterRequest` / `CreateRuntimeNodeRequest`
- `internal/config/config.go`：
  - 新增 `RuntimeConfig { EnrollmentSecret string; Probe ProbeConfig }`
  - 新增 `ProbeConfig { IntervalSeconds / TimeoutSeconds / FailureThreshold / RecoveryThreshold int }`
- `internal/config/loader.go`：
  - 校验 `runtime.enrollment_secret` 非空 + base64 + 32 字节
  - 默认填充 `runtime.probe.*`；非法值 fail-fast
- `internal/domain/enums.go`：新增 `RuntimeNodeStatusDegraded`
- `internal/audit/...`：新增 action 常量 `agent_enrolled` / `agent_re_enrolled` /
  `node_probe_degraded` / `node_probe_recovered`
- `internal/log/redact.go`：enrollment_secret 加入红色名单
- `cmd/server/main.go`：挂 probe reconciler；enrollment_secret 从 config 注入 handler
- `cmd/server/wire.go`（如果存在）同步更新依赖

### 9.2 Go（runtime agent）

新增：

- `runtime/agent/state.go` + `state_test.go`：封装 `LoadOrCreateAgentID` /
  `LoadCredentials` / `StoreCredentials`，文件权限 0600
- `runtime/agent/enroll.go` + `enroll_test.go`：`Enroll(ctx, cfg) (nodeID, agentToken, error)`，
  指数退避

修改：

- `runtime/agent/config/config.go`：
  - `ManagerConfig`：删 `NodeID` / `AgentToken`；新增 `EnrollmentSecret`
  - `AgentConfig`：新增 `Name` / `AdvertiseHost`；删除 `Token`（被 state 文件取代）
- `runtime/agent/config/loader.go`：
  - 新增必填校验：`manager.enrollment_secret`、`manager.endpoint`
  - WARN 兼容：遇到已废弃 `manager.node_id` / `manager.agent_token` / `agent.token` 打一条警告
- `runtime/agent/main.go`：
  - `runAgent` 启动序改为 §7 状态机：ensure agent-id → 尝试心跳 → 失败触发 enroll → 写 state → 心跳循环
  - `agentToken` 参数从 config 改为 state 文件读
- `runtime/agent/heartbeat.go`：
  - 心跳 401 时调用 re-enroll 回调（由 main 注入），而不是单纯记 error
- `runtime/agent/main.go` 给 `/v1/files/ping` 加 `withAgentAuth` 包装（原公开，加 Bearer 鉴权与 docker `_ping` 对齐，§8.1）

### 9.3 前端

- `web/src/pages/runtime-nodes/`：
  - 删除注册节点弹窗组件 / bootstrap token 复制组件 / Rotate bootstrap 按钮
  - 列表展示 `agent_id`（短哈希前 8 位 + tooltip 全文）
  - 状态列支持 `degraded`（黄点 + tooltip 展示 `last_probe_error`）
  - 详情页新增「最近探测」区块：`last_probe_ok_at` / `last_probe_failed_at` / `last_probe_error` / `probe_failure_streak`
  - 空态文案改为「在节点上启动 oc-runtime-agent，节点会自动出现」
- `web/src/api/generated.ts`：由 `make web-types-gen` 跟随更新
- `web/src/domain/`：runtime-node 状态枚举加 `degraded`

### 9.4 配置示例

- `config/manager.example.yaml`：新增 `runtime.enrollment_secret` + `runtime.probe.*`
- `config/agent.example.yaml`：删 `manager.node_id` / `manager.agent_token` /
  `agent.token`；新增 `manager.enrollment_secret` / `agent.name` /
  `agent.advertise_host`

### 9.5 文档

- `docs/configuration.md`：`runtime.*` 与 `agent.*` 配置段同步
- `docs/architecture.md`：§「通信约定」重写注册流程为 enroll；新增 probe reconciler
- `docs/user-manual.md` §2.2：重写节点接入流程为「自动加入」
- `deploy/README.md` §「跨机部署」与「双 agent 同宿主演练」：从 5 步注册流程改为「起 agent → UI 自动出现」，并说明 `advertise_host` 必填场景
- `openapi/openapi.yaml`：由 `make openapi-gen` 跟随更新

## 10. 测试清单

### 10.1 新增单元测试

manager：

- `service.EnrollAgent`：
  - 新节点：插入成功；`registered_at` / `last_heartbeat_at` 写入；
    audit 写入 `agent_enrolled`；返回 agent_token 明文
  - 已存在 agent_id（重启 re-enroll）：字段刷新；token 旋转（旧 hash 失效）；
    `registered_at` 不被覆盖；`probe_failure_streak` 归 0；audit 写 `agent_re_enrolled`；
    **name 不变**
  - agent_id 缺失 / 非 UUID / CA 非法 PEM → `ErrEnrollInputInvalid`
- `handlers.Enroll`：
  - 200 happy path
  - 401 enrollment_secret 错误（且使用 constant-time 比较，不做测时长但断言行为）
  - 400 必填字段缺失
- `service.HandleHeartbeat`（现有测试扩展）：
  - `unreachable` 节点收到心跳 → 翻 `active`（已有行为保留）
  - `degraded` 节点收到心跳 → 状态保持 `degraded`（新断言，§6.2 改造验证）
  - `active` 节点收到心跳 → 状态保持 `active`
- `service.NodeProbeReconciler`：
  - 全绿：streak 归 0、`last_probe_ok_at` 写入、状态保持
  - 部分失败：streak +1、状态保持
  - 失败到 `failure_threshold`：推 `degraded`、audit 写入、`last_probe_error` 分类正确
  - degraded 节点连续成功到 `recovery_threshold`：回 `active`、audit 写入
  - `degraded` 节点上 running 应用不被推 error（与 unreachable 对比测试）
  - `disabled` / `unreachable` 节点被跳过
- `integrations/agent.Probe`：httptest 模拟 401 / 超时 / 5xx / 200、TLS 握手失败；
  断言 `ProbeResult` 分类字符串正确

agent：

- `runtime/agent/state`：
  - 首次启动生成 agent-id、文件权限 0600
  - 重启复用 agent-id
  - `LoadCredentials` / `StoreCredentials` 读写正确
- `runtime/agent/enroll`：
  - HTTP 200 → 写入 state_dir、返回 token
  - HTTP 401 / 5xx → 指数退避重试
  - manager 不可达（connrefused）→ 同上
- `runtime/agent/main_test.go`：
  - 已有 token 走 heartbeat 路径、无多余 enroll 请求
  - heartbeat 401 → 触发 re-enroll；写 state 成功
  - `/v1/files/ping` 需要 Bearer（原公开用例改断言 401 → 200）
- 配置 loader：
  - `manager.enrollment_secret` 缺失 fail-fast
  - 废弃字段存在时打 WARN 但不 fail

### 10.2 删除测试

- 旧 `service.CreateNode` / `RotateBootstrap` / `RegisterAgent` 全部用例
- 旧 `handlers.Create` / `RotateBootstrap` / `Register` 全部用例
- `BootstrapRotator` 相关用例

### 10.3 集成 / e2e

- Playwright `/runtime-nodes` 页：不再有「注册节点」按钮；空态文案更新
- agent 本地 compose 跑通：起 agent → manager 列表出现 → 状态翻 active

## 11. 安全考虑

- `enrollment_secret` 与 `agent_token` 一样纳入 `internal/log/redact.go`
  红色名单；audit `metadata_json` 不回显明文
- 校验 `Authorization` 头走 `crypto/subtle.ConstantTimeCompare`
- state_dir 三个新文件 0600 权限；agent 进程启动时若检查到权限不符
  升级为 0600 并 WARN
- enroll body 字段校验：`agent_docker_endpoint` / `agent_file_endpoint` 必须
  是合法 `https://host:port` URL；`agent_tls_ca_cert` 必须是合法 PEM，
  否则 400 拒绝
- probe 请求使用现有 agent TLS root（`runtime_nodes.agent_tls_ca_cert`）；
  不启 `InsecureSkipVerify`
- enroll 端点不做 rate limit（单 manager 部署、小规模节点）；后续作为
  enhancement 可接入现有中间件
- `degraded` 不触发应用级副作用（应用状态不被推 error），防止 probe 抖动
  引发误告警

## 12. 权衡与不做的事

- **不做 enrollment_secret 热轮换**：单值；轮换 = 改 manager 配置 + 重启
  manager + 所有 agent 重启。第一版可接受。
- **不做 trusted_cidr 加固**：纯密钥模型；运维负责密钥不泄漏。
- **不做 agent 实际 docker ps 容器数对比**：1 app = 1 container 是强约束，
  不一致属于 reconciler 告警范畴，不应在 UI 上并列展示两个数字造成困惑。
- **不做 reset-identity API / UI 按钮**：state_dir 丢失是强约束违反，
  用「disable 旧行 + agent 以新 agent_id 自动产生新行」兜底即可。
- **不做新旧端点共存 / deprecation 期**：`/agent/register` / `/runtime-nodes` POST /
  `/rotate-bootstrap` 直接删，项目当前无存量节点。
- **不支持无 advertise_host 的公有云部署**：文档明确要求运维填 `advertise_host`；
  不做 manager 侧 NAT 打洞或 UPnP 之类的奇技。

## 13. 开放问题

无。所有关键决策已在讨论中拍板，详见 §2。
