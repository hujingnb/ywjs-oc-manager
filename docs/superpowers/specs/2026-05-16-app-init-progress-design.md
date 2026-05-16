# 应用初始化进度可视化与状态机细化设计

- 状态：草稿
- 日期：2026-05-16
- 作者：hujing + AI 协作

## 背景

当前应用初始化（`internal/worker/handlers/app_initialize.go`）是一个串行的"黑盒"过程，对外只暴露单一 `initializing` 状态。Worker 内部依次执行：

1. `EnsureRuntimeImage`：把 manager 本地的 hermes-runtime 镜像同步到目标 runtime node（manager 本地无镜像时**没有兜底**，直接失败）；
2. `writeHermesFiles`：渲染并上传 SOUL.md / config.yaml / .env / skills 到节点 agent；
3. `CreateContainer`：通过 agent docker 代理创建容器；
4. `StartContainer` + `WaitContainerHealthy`：启动容器并等待 HEALTHCHECK（最多 120s）。

存在的问题：

- **黑盒**：用户在前端只能看到「初始化中」转圈，不知道当前在做什么、还要多久；
- **`draft` 状态语义模糊**：前端展示为「草稿」，但业务上它只是"刚落库、worker 未拾取"的瞬时状态，文案让人摸不着头脑；
- **失败定位差**：所有失败都收敛到 `error`，前端不知道在哪一步出错；
- **缺镜像兜底**：manager 本机没有 `hermes-runtime:dev` 时无法从 registry 拉取；
- **manager 容器内无 `docker` CLI**：当前 `LocalDockerCLIProvider` 用 `exec.Command("docker", ...)` 在容器内根本跑不起来；即便挂载宿主机 `docker save -o`，写出来的 tar 也在宿主机路径，manager 容器看不到；
- **无并发协调**：同时建 2 个 app 时，两个 worker 会并发对同一镜像执行 pull/save/load，浪费带宽；这个问题在未来 manager 水平扩展（多副本部署）后会更突出；
- **无重启恢复 / 无跨实例接管**：manager 进程在初始化中途死掉，apps 表会留下 status=initializing 的孤儿行，没人继续推进；多副本部署时一个实例崩溃，其他实例也无法接管它正在跑的 init。

本设计同时解决以上 7 个问题。

## 设计目标

1. 把 `initializing` 拆成 5 个对用户有意义的子状态，前端能直观看到"现在在做什么"；
2. 暴露镜像 pull / sync 的字节级进度，长耗时阶段可视化；
3. manager 与 docker daemon 通过 Docker Engine HTTP API 交互（走 `/var/run/docker.sock`），完全避免 shell 出 CLI；
4. 复用宿主机 `~/.docker/config.json` 凭据，支持从 registry 兜底拉镜像；
5. **跨 manager 实例**串行化同一镜像的 pull、同一节点的 sync，进度通过 Redis Pub/Sub 广播给所有等待者；
6. manager 任意时刻重启或某个实例崩溃后，**任何其他存活的 manager 实例**都能接管未完成的初始化（为未来 API 水平扩展铺路）；
7. 把"草稿"改为"待初始化"，让前端文案与业务语义对齐。

## 当前能力盘点

- 状态机：`internal/domain/app_state_machine.go:38-52` 维护 13 条 transition；`AppStatusInitializing` 仅在 `enums.go` 与 `app_state_machine.go` 出现，**无 service / handler / 前端硬编码字符串依赖**（grep `AppStatusInitializing` 仅 4 处命中）。
- 镜像同步：`internal/runtime/imagesync/service.go` 已抽象为 `LocalImageProvider` + `AgentImageClient` 双接口；同步逻辑（inspect → save → upload → load）是干净的，**仅 LocalImageProvider 的实现需要换**。
- Worker handler：`internal/worker/handlers/app_initialize.go:240` 已对 `running / binding_waiting` 幂等；其余子步骤（new-api token、container_id）也都有"已存在则跳过"的局部幂等。
- 前端状态展示：`web/src/domain/status.ts` 是单文件统一映射；新增子状态只需扩 map。
- 重试入口：`runtime_operation_service.go:281` 的 `RequestInitialize` 仅允许 `error` / `draft` 重试。

## 设计

### 一、整体数据流

```
[Onboarding] ──事务──→ apps(status=draft) + jobs(app_initialize, pending)
                                  │
                                  ▼
                 [任意 manager 上的 Worker 拾取 job]
                  ↑                              ↑
                  │                              │
   Redis ZSET 队列（已存在）          Reaper（启动 + 60s tick）
   多副本天然支持                    Redis 锁 ocm:reaper:lock 互斥
                                  │
            ┌─────────────────────┼─────────────────────────────────────┐
            ▼                     ▼                                     ▼
  [ImageCoordinator.Pull]  [ImageCoordinator.SyncToNode]   [其余 3 阶段串行]
   集群内单飞                节点级集群内单飞                   秒级，无进度
   Redis 锁 + Pub/Sub        Redis 锁 + Pub/Sub
            │                     │
            └──────┬──────────────┘
                   ▼
          [progressReporter]
        1s/5% 节流 + 阶段切换 flush
                   │
                   ▼
         apps.status / progress_*  ← Postgres 是事实来源
                   │
                   ▼
         [前端轮询 GET /apps/:id]
```

### 二、状态机变化

#### 2.1 status 字段值变化

| 变化 | 旧值 | 新值 |
|---|---|---|
| 删除 | `initializing` | — |
| 新增 | — | `pulling_image`（manager 从 registry 拉镜像） |
| 新增 | — | `syncing_image`（manager 把镜像 save→upload→agent load 到节点） |
| 新增 | — | `preparing_runtime`（new-api token + 上传 SOUL.md / config.yaml / .env / skills） |
| 新增 | — | `creating_container`（agent docker create） |
| 新增 | — | `starting`（启动容器 + 等 HEALTHCHECK healthy） |

`draft` 保留（瞬时态，job 未拾取时停留），其余顶层状态（`binding_waiting / binding_failed / running / stopped / error / deleted`）不变。

#### 2.2 状态转移表（21 条）

```go
draft → pulling_image
pulling_image → syncing_image
syncing_image → preparing_runtime
preparing_runtime → creating_container
creating_container → starting
starting → binding_waiting

binding_waiting → running
binding_waiting → binding_failed
binding_failed → binding_waiting
binding_failed → error

running → stopped
running → error
stopped → running
stopped → error

pulling_image → error            ┐
syncing_image → error            │ 任意 init 子状态
preparing_runtime → error        │ 失败都进 error，5 条
creating_container → error       │
starting → error                 ┘

error → pulling_image            （重试入口，重新走完整流程）
error → deleted                  （SoftDeleteApp 路径，原本就有）
```

#### 2.3 失败信息保留

`error` 是吸入态，状态字段本身丢失"在哪一步失败"的信息。新增 `last_error_status` 字段记录最后一次进入 error 时所在的状态值；任何状态进 error 都写它（不仅限于 init 段——未来 `running → error`、`stopped → error`、`binding_failed → error` 都复用），重新启动该转移时清空。

### 三、数据库变更

新增 migration `internal/migrations/000017_app_progress_fields.up.sql`：

```sql
-- apps 表：扩展 status CHECK 约束、新增通用进度字段与上次错误状态字段。
-- status 值由 5 个 init 子状态替换原 'initializing'，存量行就地迁移。
-- progress_current / progress_total / last_error_status 设计为通用字段，
-- 不绑死 init 段，未来重启容器、停止等待优雅退出等长耗时操作都可复用。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

UPDATE apps SET status = 'pulling_image' WHERE status = 'initializing';

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft',
        'pulling_image', 'syncing_image', 'preparing_runtime',
        'creating_container', 'starting',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    ADD COLUMN progress_current bigint NULL,
    ADD COLUMN progress_total bigint NULL,
    ADD COLUMN last_error_status text NULL;

COMMENT ON COLUMN apps.progress_current IS '当前 status 对应阶段的已完成量；语义随 status 变化（字节 / 秒 / count），不可知时为 NULL。';
COMMENT ON COLUMN apps.progress_total IS '当前 status 对应阶段的总量；不可知时为 NULL（前端展示为不定进度）。';
COMMENT ON COLUMN apps.last_error_status IS '上次进入 error 时所在的状态值；进入 error 时写入，重新发起对应转移时清空。不加 CHECK，靠应用层在写入时校验。';
```

不为 `last_error_status` 加 CHECK 约束的取舍：进入 error 的来源状态本身就受 `apps_status_check` 约束（由应用层写入前校验），再加一层 CHECK 收益不大、且未来加新状态时还要同步改约束。这与项目内 `jobs.last_error` 等已有 text 字段的处理方式一致。

down.sql 反向：把 5 个 init 子状态合并回 `initializing`，删字段与 status CHECK 调整。

### 四、本地 Docker 客户端改造

#### 4.1 替换 LocalDockerCLIProvider

删除 `internal/runtime/imagesync/clients.go:17-72` 的 `LocalDockerCLIProvider`（用 `exec.Command("docker", ...)`），新增 `LocalDockerSDKProvider`：

```go
// 走 Docker Engine HTTP API，完全在 manager 容器内消费 response body 流。
import "github.com/docker/docker/client"

type LocalDockerSDKProvider struct {
    cli       *client.Client          // client.NewClientWithOpts(client.FromEnv)，读 DOCKER_HOST
    authStore RegistryAuthStore       // 解析 ~/.docker/config.json
}

// ImageID: client.ImageInspect(ctx, image) → ID
// Archive: client.ImageSave(ctx, []string{image}) → io.ReadCloser（流式 tar，全程在容器内）
// Pull:    client.ImagePull(ctx, image, types.ImagePullOptions{RegistryAuth: ...})
//          → 返回流式 NDJSON，逐行解析 progressDetail.{current,total} 上报
```

`ImageSave` 返回的 reader 是 Docker daemon 直接写 HTTP response body 的流，**不经过宿主机文件系统**，规避了"宿主机 docker save 写入宿主机目录、manager 容器看不到"的问题。

#### 4.2 RegistryAuthStore

启动时一次性加载 `~/.docker/config.json`（路径可由 `MANAGER_DOCKER_CONFIG` 环境变量覆盖；docker-compose 把宿主机 `~/.docker/config.json` 只读挂到 manager 容器对应路径）：

```go
type RegistryAuthStore struct {
    auths map[string]types.AuthConfig  // key 为 registry hostname
}

// AuthFor("docker.io/library/hermes-runtime:dev") → AuthConfig{Username, Password, ServerAddress}
// 失败时返回零值（拉取公共镜像不需要 auth）。
// 调用方用 base64.URLEncode(json.Marshal(authConfig)) 塞进 X-Registry-Auth。
```

不在运行时反复读文件——manager 启动时载入一次即可，凭据轮换通过重启 manager 生效（与现有运维节奏一致）。

#### 4.3 Docker socket 与 config 挂载

docker-compose 改动：

```yaml
manager:
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock     # 已存在的需检查
    - ${HOME}/.docker/config.json:/root/.docker/config.json:ro
```

如果 docker-compose 是在非 root 用户下运行 manager 容器，需要把 config.json 路径相应改为 `/home/<user>/.docker/config.json`。具体映射在 docker-compose 配置里直接写死，不引入额外环境变量。

### 五、ImageCoordinator（Redis 分布式单飞 + Pub/Sub 进度广播）

新增包 `internal/runtime/imagecoord/`，承担两件事：

1. **跨 manager 串行化**：同一 image 在整个 manager 集群内最多一次 pull；同一 (image, nodeID) 集群内最多一次 sync；
2. **跨 manager 广播**：进度事件通过 Redis Pub/Sub 复制给所有等待者（不论调用方在哪个 manager），让"2 个 app 同时初始化时进度字段都能更新"在多副本部署下也成立。

> 与项目既有 `internal/redis/queue.go` 的设计哲学一致：**Postgres 是事实来源（apps.status / progress_*），Redis 仅是信号通道**。Redis 失联或重启最多导致重复 pull / 进度短暂不更新，不影响业务正确性。

```go
package imagecoord

type ProgressEvent struct {
    Phase    string                 // "pulling_image" / "syncing_image"
    Current  int64
    Total    int64                  // 0 表示未知
}

type Coordinator struct {
    local       LocalImageProvider
    agent       AgentImageClient
    locker      DistLocker      // Redis 分布式锁
    bus         ProgressBus     // Redis Pub/Sub 广播
    instanceID  string          // 进程启动时生成的 UUID，作为锁 token
}

// PullImage 确保 manager 集群内本机已存在 image。
// 集群内多个 PullImage 调用合并为一次实际 docker pull；所有 subscriber 都能收到进度。
func (c *Coordinator) PullImage(
    ctx context.Context,
    image string,
    subscriber chan<- ProgressEvent,
) error

// SyncToNode 把 manager 本机镜像同步到指定 node。
// 同一 (image, nodeID) 集群内合并为一次 sync；不同节点的 sync 互不干扰。
func (c *Coordinator) SyncToNode(
    ctx context.Context,
    image string,
    nodeID string,
    subscriber chan<- ProgressEvent,
) error
```

#### 5.1 分布式锁（DistLocker）

不引入 redislock / redsync 第三方库，手写一层薄封装（与 `internal/redis/queue.go` 自实现 ZSET 队列的风格一致）：

```go
package imagecoord

type DistLocker interface {
    // TryAcquire 用 SET key token NX PX ttl 抢锁；返回是否抢到。
    TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
    // Renew 续期：Lua 校验 token 一致后 PEXPIRE。
    Renew(ctx context.Context, key, token string, ttl time.Duration) error
    // Release 释放：Lua 校验 token 一致后 DEL（防误删别人的锁）。
    Release(ctx context.Context, key, token string) error
    // Exists 仅用于 follower 在 SUBSCRIBE 后 double-check leader 是否还在。
    Exists(ctx context.Context, key string) (bool, error)
}
```

锁 key 设计：

| 用途 | key | TTL |
|---|---|---|
| pull 单飞 | `ocm:image:pull:lock:<image>` | 5 分钟 |
| sync 单飞 | `ocm:image:sync:lock:<nodeID>:<image>` | 5 分钟 |
| reaper 互斥 | `ocm:reaper:lock` | 30 秒 |

token 是 manager `instanceID + ':' + uuid()`，让 Release / Renew 能精确识别"是不是自己的锁"。

#### 5.2 进度总线（ProgressBus）

```go
type ProgressBus interface {
    Publish(ctx context.Context, channel string, event ProgressEvent) error
    Subscribe(ctx context.Context, channels ...string) (<-chan BusMessage, func(), error)
}

type BusMessage struct {
    Channel string
    Event   ProgressEvent  // 解码后的事件，Phase=="__done__" 表示 leader 完成
    Err     error          // leader publish 的失败信息
}
```

channel 命名：

| 用途 | channel |
|---|---|
| pull 进度 | `ocm:image:pull:bus:<image>` |
| sync 进度 | `ocm:image:sync:bus:<nodeID>:<image>` |

`__done__` 是哨兵 phase（不是真实子状态值），仅在总线协议内使用，follower 收到即可退出。失败时 Event 带 Err。

#### 5.3 Leader / Follower 流程

```
PullImage(ctx, image, sub):
    if local.ImageInspect(image) 已存在 → 直接 close(sub) 返回 nil
    token := instanceID + ":" + uuid()
    if locker.TryAcquire(pullLockKey, token, 5min):
        return leaderPull(ctx, image, sub, token)
    return followerWait(ctx, image, sub)

leaderPull:
    1. 启动 watchdog goroutine：每 90s locker.Renew（防 5min 内未完成时锁过期）
    2. 调 docker ImagePull，解析 NDJSON
       每条聚合后的 ProgressEvent:
         a. bus.Publish 到 channel
         b. 同进程 fanout 给本机 sub（避免 redis 来回延迟）
    3. 完成 / 失败 → bus.Publish 一条 phase=__done__ 的事件（带 err）
    4. 关闭 watchdog
    5. locker.Release（Lua check-and-del）
    6. 把 done 事件也 fanout 给本机 sub，关闭 sub，return err

followerWait:
    1. ch, cancel, _ := bus.Subscribe(progressChannel)
       defer cancel()
    2. 关键：SUBSCRIBE 后再 EXISTS 一次锁。
       Pub/Sub 没有持久化，如果 leader 在 SUBSCRIBE 之前就 publish 完 done，
       follower 会永远等不到事件。EXISTS 不到锁说明 leader 已经完成：
         - 若本机镜像已就绪 → return nil
         - 若仍未就绪（极少见的 leader 失败 case）→ 递归调用 PullImage 重新抢锁
    3. for 循环消费 ch：
         - 进度事件 → fanout 给 sub
         - __done__ 事件 → 关闭 sub，return Event.Err
    4. ctx 取消或 5min30s deadline 触发 → return ErrLeaderLost
       上层 worker 通过 job 失败重试机制重新派发，新一轮 PullImage 会自然抢锁
```

`SyncToNode` 流程结构相同，只是锁 key 与 channel 带 `nodeID`，串行粒度按节点划分（不同节点可并发 sync 同一镜像）。

#### 5.4 进度聚合（manager 本机 leader 侧）

Docker pull 是 layer 维度多路并发流，NDJSON 形如：

```json
{"id":"abc123","status":"Downloading","progressDetail":{"current":1234,"total":5678}}
{"id":"def456","status":"Extracting","progressDetail":{"current":7890,"total":9012}}
{"id":"abc123","status":"Pull complete"}
```

leader 维护 `map[layerID]layerState`，每收一条就累加：

```go
total   = Σ layer.total
current = Σ layer.current  (Pull complete 的 layer 视为 current = total)
```

每秒（或显著变化时）发一次聚合 ProgressEvent，**不是按 NDJSON 行频率发**——避免 Redis Pub/Sub 高频写入。

#### 5.5 Sync 进度

`ImageSave` 返回 reader 后，wrap 一层 `countingReader` 累加字节；总量从 `client.ImageInspect` 的 `Size` 字段拿。Agent 侧 `docker load` 的进度无法分阶段读取，sync 阶段进度仅覆盖 manager → agent 上传段（占主要时间）。

### 六、Worker handler 改造

#### 6.1 阶段化执行 + 状态推进

`app_initialize.go` 顶层 `Handle()` 改造：

```go
func (h *AppInitializeHandler) Handle(ctx context.Context, job sqlc.Job) error {
    app := loadAppContext(...)

    // 已离开初始化阶段直接成功
    if app.Status == AppStatusBindingWaiting || app.Status == AppStatusRunning {
        return nil
    }

    // 顺序执行 5 个阶段，每个阶段进入前推 status，已完成则跳过实际工作（幂等检查）
    for _, step := range []phaseStep{
        {phase: AppStatusPullingImage,      run: h.phasePull},
        {phase: AppStatusSyncingImage,      run: h.phaseSync},
        {phase: AppStatusPreparingRuntime,  run: h.phasePrepare},
        {phase: AppStatusCreatingContainer, run: h.phaseCreate},
        {phase: AppStatusStarting,          run: h.phaseStart},
    } {
        if err := h.transitionTo(ctx, &app, step.phase); err != nil {
            return err
        }
        if err := step.run(ctx, &app); err != nil {
            h.markFailed(ctx, app, step.phase, err)
            return err
        }
    }

    return h.transitionTo(ctx, &app, AppStatusBindingWaiting)
}
```

`transitionTo` 内部：
- 用 `EnsureAppTransition(from, to)` 校验转移合法；
- 调 `SetAppStatus`；
- 清空 `progress_current/total`（新阶段从 0 开始）。

`markFailed` 把 status 推到 `error`，同时写 `last_error_status = step.phase`。

#### 6.2 各阶段幂等

| 阶段 | 幂等检查（已具备 / 强化） |
|---|---|
| `pulling_image` | `Coordinator.PullImage` 内部先 `ImageInspect`，存在直接返回 |
| `syncing_image` | imagesync `SyncRuntimeImage` 已对 ID 一致跳过 |
| `preparing_runtime` | `ensureAPIKey` 已对 `api_key_status=active` 幂等；文件上传是覆盖写 |
| `creating_container` | `app.container_id != ""` 跳过创建（`app_initialize.go:284` 已有） |
| `starting` | 启动前 `agent inspect container` 看 State：`running` 直接进健康检查；`exited / created` 才 start |

`starting` 阶段需要新增 agent 接口或扩 `ContainerStarter` 接口加一个 `InspectContainer(nodeID, containerID)` 方法。

#### 6.3 进度上报

新增 `progressReporter`：

```go
type progressReporter struct {
    appID         pgtype.UUID
    store         AppInitializeStore
    lastFlushTime time.Time
    lastCurrent   int64
}

// Receive 在 worker goroutine 调，对收到的 ProgressEvent 做节流后落库。
// 节流规则：距离上次 flush ≥ 1s 或 current 增量 ≥ total*5%，立即写库。
// 阶段切换时由 transitionTo 强制 flush（current=0, total=0）。
func (r *progressReporter) Receive(event imagecoord.ProgressEvent)
```

`phasePull` / `phaseSync` 启动一个 goroutine 消费 ImageCoordinator 的 subscriber chan，调 `progressReporter.Receive`，主 goroutine 等 coordinator 调用返回后关 chan。

### 七、Manager 重启恢复（reaper）

#### 7.1 启动时机 + 周期 tick

为支持多 manager 水平扩展，reaper 不再仅在启动时跑一次，而是改为：

- **进程启动时跑一次**（保证刚重启的实例能立刻接管自己之前留下的孤儿）；
- **周期性 tick**：每 60 秒跑一次（防其他 manager 崩溃后无人接管）；
- **跨实例互斥**：每次 tick 前用 Redis 锁 `ocm:reaper:lock` (TTL 30s) 抢占，**抢到才执行**，没抢到直接退出本轮（其他 manager 已经在跑）。

```go
// cmd/server/main.go 装配顺序
// 1. db / redis / agent client / locker 等基础组件
// 2. workerPool.Start()           ← 不再要求 reaper 完成才启动 worker
// 3. reaper.Start(ctx)             ← 后台 goroutine：先立即跑一次，再每 60s tick
```

> 顺序变化：原方案要求 reaper 在 worker pool 之前完成，目的是避免 worker 抢到正在跑的 job。多 manager 下这个保证本来就拿不到（其他 manager 的 worker 可能正在跑），所以这个顺序约束没意义；幂等性已经在每个阶段保证（见 6.2）。

#### 7.2 reaper 实现

新增 `internal/worker/reaper/reaper.go`：

```go
// Start 启动后台 goroutine 周期跑 reaper。
// 每次 tick 前抢 Redis 锁 ocm:reaper:lock (TTL 30s)，抢到才执行。
func (r *Reaper) Start(ctx context.Context)

// reapOnce 由 Start 内部调，单次扫描重置孤儿。
func (r *Reaper) reapOnce(ctx context.Context) error
```

逻辑：

```sql
-- 扫描孤儿
SELECT id, runtime_node_id FROM apps
WHERE status IN ('pulling_image','syncing_image','preparing_runtime','creating_container','starting')
  AND deleted_at IS NULL;
```

对每条记录（需要识别"是否真的卡住"，避免把别的 manager 正在跑的 init 误重置）：

1. **判定条件**：`updated_at < now() - 90s`（progressReporter 至少每秒 flush，连续 90s 无更新视作卡死或 manager 死亡）；
2. 满足条件后单事务里：
   - `UPDATE apps SET status='pulling_image', progress_current=NULL, progress_total=NULL, last_error_status=NULL WHERE id=$1`；
   - 找该 app 最近一份 `app_initialize` job：
     - 存在且 status ∈ {running, succeeded}：`UPDATE jobs SET status='pending', started_at=NULL WHERE id=$id`；
     - 存在且 status = pending：跳过（scheduler 自然会拾取）；
     - 不存在：新建一份；
3. 事务外：`queue.Enqueue(jobID)`（已有的 Redis ZSET 队列，多副本天然支持）；入队失败仅记日志（scheduler 兜底扫表）。

> `updated_at < now() - 90s` 的阈值选择：worker 进度上报节流是 1s（见 6.3），90s 是约 100x 余量，足以覆盖正常 worker 在阶段切换时的瞬时停顿，又能在 manager 死亡后 90s 内被接管。

#### 7.3 多 manager 安全性

| 风险 | 缓解 |
|---|---|
| 两个 manager 同时跑 reaper，重复重置同一 app | Redis 锁 `ocm:reaper:lock` 互斥；锁 TTL > 单次 reap 预期耗时 |
| 抢到锁的 manager 在 reap 中途崩溃 | 锁 30s TTL 自动释放；下个 tick 由其他 manager 接管 |
| 一个 manager 的 worker 正在正常推进，另一个 manager 的 reaper 误判孤儿 | `updated_at` 判定阈值（90s）远大于 progressReporter 节流间隔（1s） |
| reaper 重置后，原 manager 的 worker 醒过来继续写 progress | worker 在每个阶段开始前会调 `transitionTo` 校验 `from→to` 合法性；`pulling_image → preparing_runtime` 之类不合法转移会失败，原 worker 自然终止；reaper 触发的新 job 重新走 5 阶段 |

#### 7.4 进度字段恢复语义

reaper 把 `progress_*` 全清空。重启后用户会看到状态从 `starting` 回退到 `pulling_image` → 1 秒内通过各阶段幂等检查 → 跳回原位继续。这个回退在 UI 上是视觉抖动，但**业务正确性不受影响**，且实现极简。可接受。

### 八、`RequestInitialize` 重置策略

`runtime_operation_service.go:281` 改：

```go
// 仅当应用 status ∈ {error, draft} 时允许重试。
// 进入 5 init 子状态期间不允许重试（worker 仍在跑）。
if app.Status != domain.AppStatusError && app.Status != domain.AppStatusDraft {
    return ErrAppNotReinitializable
}

// 重置：status → pulling_image（不再用 draft，因为 draft 只用于 onboarding 阶段）；
// 清空 container_id / api_key（保留原行为）；
// 清空 progress_* 与 last_error_status。
```

`draft` 入参时直接走 `pulling_image` 转移即可（`draft → pulling_image` 在状态机中合法）。

### 九、前端改动

#### 9.1 status.ts 新增映射

`web/src/domain/status.ts`：

```ts
const appStatusViews: Record<string, StatusView> = {
  draft:               { label: '待初始化',         tone: 'neutral' },
  pulling_image:       { label: '拉取运行时镜像',   tone: 'warning' },
  syncing_image:       { label: '同步镜像到节点',   tone: 'warning' },
  preparing_runtime:   { label: '准备运行时配置',   tone: 'warning' },
  creating_container:  { label: '创建容器',         tone: 'warning' },
  starting:            { label: '启动容器',         tone: 'warning' },
  binding_waiting:     { label: '待绑定',           tone: 'warning' },
  binding_failed:      { label: '绑定失败',         tone: 'danger' },
  running:             { label: '运行中',           tone: 'success' },
  stopped:             { label: '已停止',           tone: 'neutral' },
  error:               { label: '异常',             tone: 'danger' },
  deleted:             { label: '已删除',           tone: 'neutral' },
}
```

#### 9.2 进度展示

`AppOverviewTab.vue` 在 status ∈ 5 init 子状态时额外渲染：

```vue
<div v-if="isInitPhase(app.status)" class="init-progress">
  <span>{{ formatAppStatus(app.status).label }}</span>
  <progress
    v-if="app.progress_total"
    :value="app.progress_current"
    :max="app.progress_total"
  />
  <span v-if="app.progress_total">
    {{ formatBytes(app.progress_current) }} / {{ formatBytes(app.progress_total) }}
  </span>
</div>
```

`isInitPhase` 在 `status.ts` 导出，返回 `status` 是否在 5 个子状态里。

#### 9.3 失败提示

status=error 且 `last_error_status != null` 时，把"重新初始化"按钮上方的提示文案换为：

```
在「{{ formatAppStatus(last_error_status).label }}」阶段失败
```

#### 9.4 「重新初始化」按钮可见条件

`AppOverviewTab.vue:148` 当前是 `status === 'error' || status === 'draft'`。保持不变（5 个 init 子状态期间不展示重试按钮，避免用户在 worker 还在跑的时候点出第二份 job）。

#### 9.5 OpenAPI 与生成产物

按 `AGENTS.md` 流程：
- `internal/api/handlers/dto.go` 暴露 `progress_current/total/last_error_status` 到 App 响应 DTO；
- 跑 `make openapi-gen` + `make web-types-gen`；
- `web/src/api/generated.ts` 同步更新。

### 十、测试

#### 10.1 后端单测

| 文件 | 覆盖点 |
|---|---|
| `internal/domain/app_state_machine_test.go` | 表驱动覆盖 21 条合法转移 + 关键非法转移（如 `running → pulling_image` 必须失败） |
| `internal/redis/dist_locker_test.go` | TryAcquire / Renew / Release Lua 脚本正确性；token 不匹配时 Release 不会误删 |
| `internal/redis/progress_bus_test.go` | Publish/Subscribe 端到端；__done__ 哨兵识别；channel 关闭语义 |
| `internal/runtime/imagecoord/coordinator_test.go` | 并发 PullImage 跨实例单飞合并（用真实 redis 或 miniredis）；subscriber 能收到事件；leader 失败后下个 subscriber 升级 |
| `internal/runtime/imagecoord/progress_test.go` | NDJSON 解析；多 layer 字节累加；`Pull complete` 视为 current=total |
| `internal/runtime/imagesync/sdk_provider_test.go` | mock docker SDK 验证 ImageID / ImageSave / ImagePull 能正确串接 |
| `internal/worker/handlers/app_initialize_test.go` | 表驱动覆盖每阶段 status 推进；任意阶段 mock 失败应写 `last_error_status`；幂等检查（重跑相同 job 不重复创建容器） |
| `internal/worker/handlers/progress_reporter_test.go` | 1s 节流边界；5% 阈值边界；阶段切换 flush；context 取消不写库 |
| `internal/worker/reaper/reaper_test.go` | 5 个孤儿状态都能被扫到；`updated_at < now()-90s` 阈值边界；Redis 锁抢占失败时直接退出本轮；job 状态分支（无 / pending / running / succeeded）都能正确处置 |

#### 10.2 前端单测

| 文件 | 覆盖点 |
|---|---|
| `web/src/domain/status.spec.ts` | 5 个新 status 都能映射到正确 label/tone；isInitPhase 边界 |
| `web/src/pages/apps/AppOverviewTab.spec.ts` | progress_total=null 时不渲染 progress；status=error + last_error_status 时显示阶段文案 |

#### 10.3 浏览器验证

按 `AGENTS.md` 要求，开发完成后必须用浏览器跑通：

1. 创建一个新 app（manager 本机已有镜像）：观察 status 序列 `draft → pulling_image（瞬间）→ syncing_image（带进度）→ preparing_runtime → creating_container → starting → binding_waiting`；
2. 故意让 manager 本机删掉镜像后创建：验证 pulling_image 阶段进度条能动；
3. 同时点 2 个补建：验证两个 app 都能看到镜像同步进度（Redis Pub/Sub 广播生效）；
4. 在 syncing_image 中途 `docker compose restart manager`：重启后两个 app 仍能完成初始化；
5. 让 agent 临时拒绝 inspect 调用：验证 status=error + last_error_status=syncing_image，前端"重新初始化"按钮可点；
6. **多副本场景**（docker-compose scale manager=2）：在 manager-A 上发起 app1 init、manager-B 上发起 app2 init，且都需要拉取同一镜像。验证：
   - 只有一个 manager 实际执行 docker pull（看 docker daemon 日志或 `docker events`）；
   - 两个 app 的 progress_* 字段都在更新；
   - kill 掉 leader manager 容器，剩余 manager 上 reaper 60s 内接管 app1，app1 重新走完 5 阶段。

### 十一、不做的事（Out of scope）

- **不引入 SSE**：用户对实时性的要求是"看到进度在动"，前端 3-5 秒轮询足够；SSE 引入断连重试、多副本部署等复杂度，本期不值。
- **不持久化进度阶段历史**：每次 reaper 重置时 progress_* 直接清空。如果未来要做"初始化耗时分析"，单独设计 metrics 表，不与运行态字段耦合。
- **不实现镜像 GC**：manager 容器的 docker daemon 镜像清理与 manager 应用无关，由运维侧周期任务负责。
- **不改 binding_waiting 之后的状态机**：本期只动 init 段。

### 十二、迁移与兼容

1. 数据库 migration `000017` 把存量 `initializing` 行就地改成 `pulling_image`。重启 manager 后 reaper 会立即接管这些行，正常情况下都会被 worker 推进。
2. OpenAPI 契约：`status` enum 增加 5 个值。前端旧版本若部署滞后，会落到 `formatAppStatus` 的 unknown 分支显示"未知状态"——这是 status.ts 设计本意，非阻塞。
3. `AppStatusInitializing` 常量从 enums.go 删除前，跑一遍 grep 确认无残留引用。

## 风险

| 风险 | 缓解 |
|---|---|
| Docker SDK 版本与 daemon 不兼容 | go.mod 锁定 `github.com/docker/docker` 版本；CI 用与生产相近版本测试 |
| `~/.docker/config.json` 凭据格式多样（credentials helper、auth keychain） | 一期只支持 `auths.<registry>.auth` 字段（base64(user:pass)）；helper 场景报错并提示运维静态写 auth |
| `ImagePull` 长时间不返回事件被误判为卡住 | leader 维护 lastEventAt，超过 60s 无事件主动 ctx.Cancel；subscriber 收到 err 后冒泡到 error |
| reaper 误把正在正常推进的 app 重置 | reaper 用 `apps.updated_at < now()-90s` 判定孤儿；progressReporter 至少每秒 flush，正常 worker 不会被误伤 |
| 多 manager 同时跑 reaper 重复重置 | Redis 锁 `ocm:reaper:lock` (TTL 30s) 互斥；锁超时自动释放，崩溃可由其他实例接管 |
| Redis 短暂不可用导致 ImageCoordinator 抢锁失败 | 抢锁失败时返回错误并冒泡为 worker job 失败；scheduler 的 PG 兜底扫表会重新派发；最坏后果是几次 pull/sync 串行性丢失，不影响正确性 |
| Redis Pub/Sub "先发后订"导致 follower 错过 done 事件 | follower SUBSCRIBE 后再 EXISTS 检查锁，锁不在则视作 leader 已完成，再次走 PullImage 入口（详见 5.3） |
| watchdog 续期失败 leader 仍持锁跑 | leader 在 Renew 失败超过 N 次时主动放弃（cancel ctx），让其他 manager 接管；防止"假持锁"长时间阻塞 |

## 实现顺序建议

1. **数据库 + 状态机**：migration、enums.go、state_machine.go、status.ts 文案 —— 一个独立 PR，先把契约改了；
2. **Docker SDK 替换**：LocalDockerSDKProvider 落地，去掉 LocalDockerCLIProvider；不改 worker 流程；
3. **Redis DistLocker + ProgressBus**：放在 `internal/redis/` 包内（与现有 ZSET 队列同级），独立测试，不绑定具体业务；
4. **ImageCoordinator + progressReporter**：依赖步骤 3 的锁与总线，串起单飞、广播、节流；
5. **Worker handler 改造**：5 阶段化 + 进度上报 + 幂等强化；
6. **Reaper**：周期 tick + Redis 锁互斥，在 cmd/server 装配；加重启冒烟测试；
7. **前端展示**：进度条 + 失败阶段文案；
8. **联调 + 浏览器验证**（含多副本验证）。

每步都能独立 commit、可回滚。
