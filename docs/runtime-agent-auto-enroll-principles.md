# Runtime Agent 自动注册原理

## 1. 目标

自动注册模式把节点生命周期从「管理员后台创建」改为「agent 自描述注册」：

1. agent 持久化自己的 `agent-id`。
2. agent 使用共享 `enrollment_secret` 调 manager enroll。
3. manager 按 `agent_id` 幂等创建或刷新 `runtime_nodes`。
4. manager 签发新的 `agent_token`，加密存库并返回给 agent。
5. agent 用 `agent_token` 做心跳，manager 用同一 token 探测 agent 入站端口。
6. `agent.max_apps` 作为节点容量的单一来源，随 enroll 同步到 manager 并持久化到 `runtime_nodes.max_apps`。

这样新增节点只需要部署 agent 配置，不需要管理员在后台手工创建节点或复制 bootstrap token。

## 2. 身份与凭证

`agent_id` 是节点的稳定身份，保存在 `state_dir/agent-id`。只要该文件不变，重复启动或重新 enroll 都会更新同一条 `runtime_nodes` 记录。

`enrollment_secret` 是部署级共享密钥，只用于 enroll。它不作为日常调用凭证。

`agent_token` 是节点级长期凭证。manager 返回后：

- agent 写入 `state_dir/agent-token`
- manager 写入内存缓存
- manager 加密写入 `runtime_nodes.agent_token_ciphertext`
- manager 同时保存 `agent_token_hash` 供心跳校验

## 3. Enroll 流程

agent 启动时执行：

```text
load/create agent-id
ensure self-signed TLS cert
load node-id + agent-token
if agent-token missing:
  POST /api/v1/agent/enroll
  Authorization: Bearer <enrollment_secret>
  body: agent_id, endpoints, CA cert, version, data_root, metadata, max_apps
  persist node-id + agent-token
start heartbeat
start docker/file TLS servers
```

manager 处理 enroll 时：

```text
constant-time compare enrollment_secret
validate agent_id / HTTPS endpoints / CA PEM
if agent_id absent:
  INSERT runtime_nodes(status=active)
else:
  UPDATE endpoints/version/token/status=active
return node_id + agent_token + heartbeat_interval_seconds
```

## 4. 心跳与重新注册

agent 周期调用 `POST /api/v1/agent/heartbeat`，请求体携带 `agent_token`。

如果 manager 返回 401，agent 会重新调用 enroll，刷新本地 `agent_token`。这覆盖了 manager 侧 token 丢失、密钥重置后重签发等场景。

## 5. 主动探测与 degraded

心跳只能证明 agent 能出站访问 manager，不能证明 manager 能入站访问 agent。因此 manager 另有 `RuntimeNodeProbeReconciler`：

1. 扫描 `active` / `degraded` 节点。
2. 用节点 CA 与 `agent_token` 探测：
   - `GET <docker_endpoint>/v1/docker/_ping`
   - `GET <file_endpoint>/v1/files/ping`
3. 连续失败达到 `failure_threshold`：`active -> degraded`。
4. 连续成功达到 `recovery_threshold`：`degraded -> active`。

`degraded` 不会把节点上的运行中应用推到 `error`，也不会参与新应用调度。它只表示 manager 到 agent 的管理通道异常。

`unreachable` 仍由心跳超时 reconciler 负责，表示 agent 长时间没有向 manager 上报心跳。该状态会触发节点上运行中应用转 `error`。

## 6. 安全边界

- enroll 使用 `Authorization: Bearer <enrollment_secret>`，比较使用常量时间比较。
- manager 到 agent 的调用使用 agent 自签 CA 做 TLS 校验。
- agent docker/file API 使用 `Authorization: Bearer <agent_token>`。
- agent 可配置 `trusted_cidr` 额外限制 manager 出口 IP。
- 日志脱敏覆盖 `enrollment_secret`、`agent_token`、`Authorization` 等字段。

## 7. 运维含义

- 新增节点：部署 agent 配置并启动。
- 调整容量：修改 `agent.max_apps` 后重启 agent，manager 会在下一次 enroll 时同步新值。
- 替换节点硬件但保留逻辑节点：迁移 `state_dir/agent-id`。
- 让新机器成为新节点：使用新的空 `state_dir` 启动。
- token 异常：重启 agent 触发重新 enroll。
- enrollment secret 轮换：同步修改 manager 与所有 agent，重启进程。
