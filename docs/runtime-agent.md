# runtime-agent 工作原理

> runtime-agent 的身份模型、enroll 流程、心跳与重新注册、manager 主动探测、安全边界。部署细节见 deploy/runtime-agent/README.md。

---

## 1. 目标与设计原则

runtime-agent 的核心设计目标是让节点生命周期从「管理员手工建节点」变为「agent 自描述注册」。

**自动注册（agent 自描述）：** agent 启动时主动向 manager 发起 enroll，自行上报端点、TLS CA、版本号、数据根目录等信息。manager 按 `agent_id` 幂等写入 `runtime_nodes`。管理员无需在后台提前创建节点或复制任何 token。

**agent_id 持久化：** agent 把自己的 UUID 写入 `state_dir/agent-id`。只要该文件存在，无论 agent 进程重启或重新 enroll 多少次，manager 始终更新同一条 `runtime_nodes` 记录。迁移该文件等同于迁移逻辑节点身份。

**max_apps 单一来源：** 节点可承载的应用数量上限由 agent 配置文件决定，随每次 enroll 同步到 `runtime_nodes.max_apps`。manager 不维护独立的容量配置。

---

## 2. 身份与凭证

runtime-agent 涉及三类身份标识，各有不同的生命周期和存储位置。

**agent_id**
节点的稳定外部身份，UUID v4 格式。首次启动由 agent 自动生成并写入 `state_dir/agent-id`（权限 `0600`）。后续启动直接读取，不重新生成。

**enrollment_secret**
部署级共享密钥，在 manager 配置文件和 agent 配置文件中各保存一份。仅在 enroll 阶段用于鉴权，通过 `Authorization: Bearer <enrollment_secret>` 传递。日常运行不使用该密钥。manager 使用 `crypto/subtle.ConstantTimeCompare` 比较，避免时序旁路。

**agent_token**
节点级长期凭证，由 manager 在 enroll 时生成并返回。

- agent 侧：写入 `state_dir/agent-token`（权限 `0600`）；进程运行期间通过 `tokenGetter` 函数懒读取，始终取最新磁盘值。
- manager 侧：写入 `runtime_nodes.agent_token_hash`（bcrypt hash，用于心跳校验）和 `runtime_nodes.agent_token_ciphertext`（AES-256-GCM 密文，base64 编码，用于主动探测时解密还原）。

---

## 3. Enroll 流程

### agent 侧启动逻辑

```text
load/create state_dir/agent-id
ensure self-signed TLS cert（首次启动生成，后续复用）
load state_dir/node-id + state_dir/agent-token
if agent-token 缺失 or manager.endpoint 非空:
    loop until success:
        POST /api/v1/agent/enroll
        Authorization: Bearer <enrollment_secret>
        body: agent_id, name, max_apps,
              agent_docker_endpoint, agent_file_endpoint,
              agent_tls_ca_cert, agent_version, node_data_root,
              sampled_at, node_resource, resource_snapshot, metadata
        on success: persist node-id + agent-token to state_dir
        on failure: wait 5s and retry
start heartbeat goroutine
start docker-proxy TLS server（:7001）
start file-api TLS server（:7002）
```

`shouldEnrollOnStartup` 的判断逻辑：`manager.endpoint` 非空，**或者** 本地 `agent-token` 文件不存在，则进行 enroll。

enroll 请求使用 manager 配置中的 `ca_bundle` 或系统根 CA 验证 manager TLS。

### manager 侧处理逻辑

```text
constant-time compare Bearer token vs enrollment_secret
parse + validate 请求体（agent_id required；endpoints 格式校验）
if runtime_nodes where agent_id = $agent_id exists:
    UPDATE endpoints, version, CA, max_apps, status=active, 重新生成 agent_token
else:
    INSERT runtime_nodes(status=active, ...)
store agent_token_hash + agent_token_ciphertext
return {node_id, agent_token, heartbeat_interval_seconds}
```

enroll 是幂等 upsert，同一 `agent_id` 多次调用只更新节点信息，不会产生重复记录。状态在 enroll 成功后强制置为 `active`，覆盖之前可能的 `unreachable` 或 `degraded` 状态。

`heartbeat_interval_seconds` 由 manager 配置（`runtime.heartbeat_interval_seconds`）决定，默认 30 秒；返回给 agent 后 agent 以此为心跳周期。

---

## 4. 心跳与重新注册

### 心跳端点

```
POST /api/v1/agent/heartbeat
```

请求体字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `agent_token` | string（required） | 节点令牌，用于鉴权 |
| `agent_version` | string | agent 当前版本 |
| `sampled_at` | string（RFC3339） | 资源采样时间 |
| `node_resource` | object | CPU / 内存 / 磁盘 / 网络 / 实例数等指标 |
| `resource_snapshot` | object | agent 进程级快照（goroutine 数、内存分配等） |
| `metadata` | object | 附加元数据，覆盖节点当前值 |

心跳鉴权通过请求体中的 `agent_token` 字段完成（不使用 `Authorization` header），避免 agent 侧额外拼接 header。

### 401 触发重新注册

agent 的心跳 goroutine 在收到 `HTTP 401` 时，自动重新调用 `enrollAgent`，刷新本地 `agent-token` 和 `node-id`。触发场景：

- manager 重启后内存 token cache 丢失（极端情况）
- manager 数据库重置，`runtime_nodes` 记录消失
- `enrollment_secret` 轮换后 agent 尚未更新但 token 已失效

重新注册成功后，心跳失败计数归零，下次 tick 正常继续。

---

## 5. 主动探测与节点状态机

### 状态概览

| 状态 | 含义 | 触发路径 | 对新应用调度的影响 |
|------|------|----------|------------------|
| `active` | 正常 | enroll 成功；心跳恢复；探测连续成功达 recovery_threshold | 参与调度 |
| `degraded` | manager 探测失败 | 探测连续失败达 failure_threshold | 不参与新应用调度 |
| `unreachable` | agent 心跳超时 | NodeHealthReconciler 检测到 last_heartbeat_at 超出阈值 | 不参与新应用调度；running app → error |
| `disabled` | 手动下线 | 管理员操作 | 不参与调度 |

### RuntimeNodeProbeReconciler（主动探测）

manager 运行 `RuntimeNodeProbeReconciler`，周期扫描所有 `active` 和 `degraded` 节点：

1. 按 `runtime_nodes.agent_token_ciphertext` 解密得到 `agent_token`。
2. 使用节点 `agent_tls_ca_cert` 构造 mTLS 客户端。
3. 分别探测：
   - `GET <agent_docker_endpoint>/v1/docker/_ping`
   - `GET <agent_file_endpoint>/v1/files/ping`
4. 探测结果写入 `probe_failure_streak` / `probe_success_streak` 计数器。

**active → degraded：** 连续探测失败次数 ≥ `failure_threshold`（默认 3 次）。写入 audit log `node_probe_degraded`。

**degraded → active：** 连续探测成功次数 ≥ `recovery_threshold`（默认 2 次）。写入 audit log `node_probe_recovered`。

`degraded` 不影响该节点上已运行的应用；它仅表示 manager 到 agent 的管理通道异常，不是应用运行故障。

### NodeHealthReconciler（心跳超时）

`NodeHealthReconciler` 周期扫描 `active` 节点，若 `last_heartbeat_at` 早于 `now() - heartbeat_grace`（默认 90 秒，建议配置为 3× 心跳间隔），则：

- 将节点状态推为 `unreachable`。
- 将该节点上所有 `running` 状态的应用推为 `error`，让前端立即可见。

`unreachable → active` 的恢复由 agent 重新发心跳触发（心跳成功时 manager 自动把节点推回 `active`），reconciler 不主动恢复该状态。

---

## 6. 安全边界

**enrollment_secret 校验：** manager 使用 `crypto/subtle.ConstantTimeCompare` 做常量时间比较，避免在密钥长度相同时因短路退出泄露 timing 信息。enrollment_secret 为空时 enroll 端点直接拒绝所有请求。

**manager → agent TLS：** agent 启动时生成自签名证书（存储于 `state_dir/`），并在 enroll 请求体中上报 CA PEM（`agent_tls_ca_cert`）。manager 在主动探测时使用该 CA 验证 agent TLS，不信任系统根。

**agent API 鉴权：** agent 的 docker-proxy 和 file-api 均要求 `Authorization: Bearer <agent_token>` 才能访问（`/healthz` 除外），manager 探测时携带此 token。

**trusted_cidr 源 IP 限制：** agent 可在配置中指定 `trusted_cidr`，docker-proxy 的 `wrapAuth` 中间件会额外检查请求方源 IP，不在 CIDR 段内的请求返回 403，在网络隔离较弱的部署环境中提供纵深防御。

**日志脱敏：** manager 的 `RedactingWriter` 包装 `os.Stderr`，对每行日志应用正则替换，覆盖 `agent_token`、`enrollment_secret`、`Bearer <token>` 等敏感字段，确保不明文落盘。

---

## 7. 运维含义

**新增节点**
部署 agent 配置文件（`manager.endpoint`、`manager.enrollment_secret`、`agent.data_root` 等），启动 agent 容器或进程。agent 自动完成 enroll，manager 侧立即出现新节点记录。无需手工操作。

**调整节点容量**
修改 agent 配置文件中的 `agent.max_apps`，重启 agent。重启触发 re-enroll，新的 `max_apps` 值同步到 manager `runtime_nodes.max_apps`。

**替换节点硬件，保留逻辑节点**
将旧机器的 `state_dir/agent-id` 文件拷贝到新机器相同路径。新 agent 启动后 enroll 时携带原 `agent_id`，manager 识别为同一节点并更新记录，节点 ID 不变，历史应用记录保持关联。

**新机器作为全新节点**
在新机器上使用**空的** `state_dir` 启动 agent。agent 生成新的 `agent_id`，manager 插入新节点记录。

**token 异常或 401**
重启 agent 即可触发重新 enroll，manager 签发新 `agent_token` 并更新数据库。无需手工介入。

**enrollment_secret 轮换**
同步修改 manager 配置（`runtime.enrollment_secret`）和所有 agent 配置（`manager.enrollment_secret`），然后依次重启 manager 和各 agent。agent 重启后用新密钥 enroll，完成令牌刷新。建议在业务低峰期操作，轮换窗口内 agent 无法 re-enroll（心跳不受影响，直到下次 401 或重启）。
