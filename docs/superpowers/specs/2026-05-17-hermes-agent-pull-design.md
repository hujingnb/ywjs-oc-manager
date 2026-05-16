# Hermes 镜像 Agent 自行拉取设计

**日期**：2026-05-17  
**状态**：已批准

## 背景与动机

当前架构中，manager 先在本机拉取 hermes 镜像，再通过 agent `/v1/images/load` 端点把 6GB+ tar 包传输到目标节点。存在以下问题：

1. **镜像 ID 不一致**：manager 用 `docker inspect` 返回的 ID（可能是 v1 兼容格式），agent `docker load` 后产生的 ID 不一致，导致 re-tag 失败（502 错误）。
2. **传输成本高**：每次部署需要跨网络传输数 GB 归档文件。
3. **冗余实现**：agent 暴露了 `/v1/docker/*` proxy，manager 完全可以通过它直接操作 agent 上的 Docker daemon，但 imagesync 包另起炉灶实现了一套自定义的 inspect/load 端点。

hermes 镜像本身公网可访问、无需鉴权，让 agent 直接从公网 registry 拉取是最简方案。

## 核心变更概览

| 层 | 变更 |
|----|------|
| 状态机 | 新增 `PullingRuntimeImage`，删除 `PullingImage`/`SyncingImage` 阶段 |
| DB | `apps` 表新增 `runtime_image_ref`、`runtime_image_sha256` 列 |
| Manager | 新增 `phasePullRuntimeImage`（通过 agent docker proxy 拉取），删除本地拉取和传输逻辑 |
| Agent | 删除 `/v1/images/inspect`、`/v1/images/load` 端点 |
| 容器创建 | 新增 `restart=always` 策略 |
| 恢复逻辑 | Reaper 使用 `app.RuntimeImageRef`（DB 存储值），不依赖当前配置 |
| UI | 实例详情页展示镜像 tag 和 sha256（仅平台管理员可见） |

---

## 1. 状态机变更

### 新增状态

```go
AppStatusPullingRuntimeImage AppStatus = "pulling_runtime_image"
```

### 新部署路径

```
Draft → PullingRuntimeImage → PreparingRuntime → CreatingContainer → Starting → BindingWaiting → Running
```

### 旧状态处理

`AppStatusPullingImage`、`AppStatusSyncingImage` 枚举值保留（不删除），确保历史数据的 app 不会产生未知状态。不再有新的 app 进入这两个状态。

---

## 2. 数据库变更

### `apps` 表新增列

```sql
ALTER TABLE apps ADD COLUMN runtime_image_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN runtime_image_sha256 TEXT NOT NULL DEFAULT '';
```

字段语义：
- `runtime_image_ref`：部署时实际使用的镜像引用，如 `registry.example.com/hermes:v1.2.3`。`phasePullRuntimeImage` 完成后写入，此后不再变更。
- `runtime_image_sha256`：拉取后通过 `docker inspect` 获取的镜像 ID（`sha256:...`），用于展示和排查。

### 状态枚举扩展

`apps.status` 的 check constraint / 枚举中增加 `pulling_runtime_image`。

### SQLC 查询

新增 `UpdateAppRuntimeImage(appID, imageRef, imageSha256)` 查询，在 `phasePullRuntimeImage` 成功后调用。

---

## 3. Manager：phasePullRuntimeImage

替换当前 `phasePull`（本地拉取）+ `phaseSync`（传输）两个阶段，合并为单阶段。

### 阶段序列（修改后）

```go
{domain.AppStatusPullingRuntimeImage, h.phasePullRuntimeImage},
{domain.AppStatusPreparingRuntime, h.phasePrepare},
{domain.AppStatusCreatingContainer, h.phaseCreate},
{domain.AppStatusStarting, h.phaseStart},
```

### phasePullRuntimeImage 逻辑

```
phasePullRuntimeImage(ctx, app, nodeID):
  imageRef = cfg.RuntimeImage   // 从配置读取，如 "registry.example.com/hermes:v1.2.3"
  docker = NewStreamingDockerClientForNode(node)

  // 预检：tag 已存在则跳过拉取（tag 不可变，存在即等于内容一致）
  info = docker.ImageInspectWithRaw(ctx, imageRef)
  if info exists:
    store app.RuntimeImageRef = imageRef, RuntimeImageSha256 = info.ID
    return

  // 按 (nodeID, imageRef) 加分布式锁，同一 agent 同一镜像串行，不同 agent 并行
  lockKey = "ocm:image:nodepull:lock:<nodeID>:<imageRef>"
  lock.acquire(lockKey)
  defer lock.release()

  // 锁内二次预检（等锁期间可能已被其他并发部署拉取完毕）
  info = docker.ImageInspectWithRaw(ctx, imageRef)
  if info exists:
    store app.RuntimeImageRef = imageRef, RuntimeImageSha256 = info.ID
    return

  // 拉取：NDJSON 流 → PullAggregator → 广播进度给 SSE 订阅者
  stream = docker.ImagePull(ctx, imageRef, PullOptions{})
  aggregator.Consume(stream, subscriber)

  // 验证：拉取后 inspect 确认镜像存在并获取 sha256
  info = docker.ImageInspectWithRaw(ctx, imageRef)
  if not exists:
    return error("pull succeeded but image not found after inspect")

  // 持久化镜像信息到 DB
  store app.RuntimeImageRef = imageRef, RuntimeImageSha256 = info.ID
```

### 锁粒度

- **当前**：全局单锁（所有节点、所有镜像互斥）
- **新设计**：按 `(nodeID, imageRef)` 加锁
  - 同一 agent 上拉取同一镜像：串行（避免重复拉取占用带宽）
  - 不同 agent：完全并行
  - 同一 agent 拉取不同镜像：并行

### 进度广播

拉取过程中，NDJSON 流解析结果通过现有 `PullAggregator` 广播。前端 SSE 订阅者收到 layer-level 进度。

---

## 4. phaseCreate 修复

### 镜像来源

```go
// 修改前
imageRef := h.cfg.RuntimeImage

// 修改后
imageRef := h.cfg.RuntimeImage  // 默认值（首次部署 phasePullRuntimeImage 还未运行的兜底）
if app.RuntimeImageRef.Valid && app.RuntimeImageRef.String != "" {
    imageRef = app.RuntimeImageRef.String
}
```

**设计意图**：`RuntimeImageRef` 由 `phasePullRuntimeImage` 写入，正常流程中 `phaseCreate` 执行时一定非空。Fallback 仅用于历史数据或极端异常场景。

### restart=always

`ContainerSpec` 结构体新增字段：

```go
type ContainerSpec struct {
    // ...现有字段...
    RestartPolicy string  // "always" / "on-failure" / "unless-stopped" / ""（不设置）
}
```

`CreateContainer` 中映射到 Docker SDK：

```go
HostConfig: &container.HostConfig{
    RestartPolicy: container.RestartPolicy{Name: spec.RestartPolicy},
}
```

`phaseCreate` 构造 spec 时传入 `RestartPolicy: "always"`。

---

## 5. Reaper 修复

### 状态重置

```go
// 修改前
Status: domain.AppStatusPullingImage,

// 修改后
Status: domain.AppStatusPullingRuntimeImage,
```

### 恢复流程

Reaper 将卡住的 app 重置为 `PullingRuntimeImage` 并重新入队 `app_initialize`。`app_initialize` handler 重新执行 `phasePullRuntimeImage` 时：

- 若 `app.RuntimeImageRef` 已非空（上次部署已写入），则 `phasePullRuntimeImage` 以该值为准，与 `cfg.RuntimeImage` 无关。
- 若 `app.RuntimeImageRef` 为空（极端情况：首次部署在写入 DB 前崩溃），则使用 `cfg.RuntimeImage`。

这确保**已部署 app 恢复时始终使用原始镜像版本**，不受配置文件升级影响。

---

## 6. UI 展示

### 展示位置

实例详情页（现有 app detail 页面）。

### 展示内容

| 字段 | 显示值 | 可见性 |
|------|--------|--------|
| 运行时镜像 | `runtime_image_ref`（完整镜像引用，含 tag） | 仅平台管理员 |
| 镜像 SHA256 | `runtime_image_sha256`（折叠/截断显示） | 仅平台管理员 |

### API 变更

- 现有 app detail API 响应中新增 `runtime_image_ref`、`runtime_image_sha256` 字段
- 后端按 `principal.Role == platform_admin` 判断是否填充（普通成员/组织管理员返回空字符串或省略）
- 权限判断放在 handler 层，遵循项目 `authorizer.go` 规范

---

## 7. 需要删除的代码

### Agent 侧

| 文件 | 删除内容 |
|------|---------|
| `runtime/agent/main.go` | `/v1/images/inspect`、`/v1/images/load` 路由及 handler 函数 |
| `runtime/agent/docker_client.go` | `InspectImage`、`LoadImage`、`TagImage` 方法及实现；`DockerClient` 接口对应方法声明 |

**保留**：`ListContainers`（被 `node_resource.go` 使用）。

### Manager 侧

| 路径 | 操作 |
|------|------|
| `internal/runtime/imagesync/` | **整包删除**（含 `sdk_provider.go` 本次会话的所有修改） |
| `internal/service/image_distribution_service.go` | **删除** |
| `internal/worker/handlers/app_initialize.go` | 删除 `phasePull`、`phaseSync` 方法及调用 |
| `internal/runtime/imagecoord/` | 删除 `LocalImageProvider`、`AgentImageClient` 接口；删除 `Coordinator.PullImage`（本地）、`Coordinator.SyncToNode` |
| `internal/integrations/runtime/agent_backed.go` | 删除 `ImageSyncer`/`EnsureImage` 相关实现 |

---

## 8. 不变的部分

- `NewDockerClientForNode` / `NewStreamingDockerClientForNode`（`internal/integrations/agent/docker_proxy.go`）：直接复用，无需修改。
- `PullAggregator`：直接复用，无需修改。
- `imagecoord.Coordinator` 结构体：保留，仅删除不再使用的方法。
- agent Docker proxy（`/v1/docker/*`）：不变。
- container 创建/启动逻辑（`phaseCreate`/`phaseStart`）：仅改镜像来源和 restart policy，其余不变。

---

## 9. 测试要求

| 场景 | 测试类型 |
|------|---------|
| `phasePullRuntimeImage`：镜像已存在，跳过拉取 | 单元测试（mock docker inspect 返回已有镜像） |
| `phasePullRuntimeImage`：镜像不存在，拉取并写入 DB | 单元测试 |
| `phasePullRuntimeImage`：锁等待期间被其他协程拉取完毕，二次预检命中 | 单元测试 |
| `phaseCreate`：优先使用 `app.RuntimeImageRef` | 单元测试 |
| `phaseCreate`：`RuntimeImageRef` 为空时 fallback 到 `cfg.RuntimeImage` | 单元测试 |
| Reaper 重置状态为 `PullingRuntimeImage` | 现有 reaper 测试更新 |
