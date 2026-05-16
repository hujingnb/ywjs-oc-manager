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
- **无并发协调**：同时建 2 个 app 时，两个 worker 会并发对同一镜像执行 pull/save/load，浪费带宽；
- **无重启恢复**：manager 进程在初始化中途死掉，apps 表会留下 status=initializing 的孤儿行，没人继续推进。

本设计同时解决以上 7 个问题。

## 设计目标

1. 把 `initializing` 拆成 5 个对用户有意义的子状态，前端能直观看到"现在在做什么"；
2. 暴露镜像 pull / sync 的字节级进度，长耗时阶段可视化；
3. manager 与 docker daemon 通过 Docker Engine HTTP API 交互（走 `/var/run/docker.sock`），完全避免 shell 出 CLI；
4. 复用宿主机 `~/.docker/config.json` 凭据，支持从 registry 兜底拉镜像；
5. 同一镜像的 pull、同一节点的 sync 串行化，但进度对所有等待者广播；
6. manager 任意时刻重启后能继续推进未完成的初始化；
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
                 [Worker 拾取 job] ── reaper 启动时也会回放孤儿
                                  │
            ┌─────────────────────┼─────────────────────────────────────┐
            ▼                     ▼                                     ▼
  [ImageCoordinator.Pull]  [ImageCoordinator.SyncToNode]   [其余 3 阶段串行]
   manager 本机             节点级串行                       秒级，无进度
   单飞 + 进度广播          单飞 + 进度广播
            │                     │
            └──────┬──────────────┘
                   ▼
          [progressReporter]
        1s/5% 节流 + 阶段切换 flush
                   │
                   ▼
         apps.status / init_progress_*
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

`error` 是吸入态，状态字段本身丢失"在哪步失败"的信息。新增 `init_failed_phase` 字段记录最后一次进入 error 时的子状态值；`RequestInitialize` 重置时清空。

### 三、数据库变更

新增 migration `internal/migrations/000017_app_init_progress.up.sql`：

```sql
-- apps 表：扩展 status CHECK 约束、新增 init_failed_phase 与进度字段。
-- status 值由 5 个 init 子状态替换原 'initializing'，存量行就地迁移。

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
    ADD COLUMN init_failed_phase text NULL,
    ADD COLUMN init_progress_current bigint NULL,
    ADD COLUMN init_progress_total bigint NULL,
    ADD CONSTRAINT apps_init_failed_phase_check CHECK (
        init_failed_phase IS NULL OR init_failed_phase IN (
            'pulling_image', 'syncing_image', 'preparing_runtime',
            'creating_container', 'starting'
        )
    );

COMMENT ON COLUMN apps.init_failed_phase IS '上次进入 error 时所在的初始化子状态；RequestInitialize 重置时清空。';
COMMENT ON COLUMN apps.init_progress_current IS '当前 init 子状态的已完成量（字节或秒），语义随 status 变化。';
COMMENT ON COLUMN apps.init_progress_total IS '当前 init 子状态的总量；不可知时为 NULL（前端展示为不定进度）。';
```

down.sql 反向：把 5 个 init 子状态合并回 `initializing`，删字段与约束。

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

### 五、ImageCoordinator（单飞 + 进度广播）

新增包 `internal/runtime/imagecoord/`，承担两件事：

1. **串行化**：同一 image 在 manager 进程内最多一次 pull；同一节点最多一次 sync；
2. **广播**：进度事件复制给所有等待者，让"2 个 app 同时初始化时进度字段都能更新"自然成立。

```go
package imagecoord

type ProgressEvent struct {
    Phase    string                 // "pulling_image" / "syncing_image"
    Current  int64
    Total    int64                  // 0 表示未知
    Layers   map[string]layerState  // 仅 pull 阶段使用，便于聚合
}

type Coordinator struct {
    local   LocalImageProvider
    agent   AgentImageClient

    mu      sync.Mutex
    pulls   map[string]*pullJob              // key: image
    syncs   map[string]*syncJob              // key: image|nodeID
}

// PullImage 确保 manager 本机存在指定 image。已存在时直接返回；
// 多个调用者并发请求同一 image 时合并为一次 docker pull，subscriber 都能收到事件。
func (c *Coordinator) PullImage(
    ctx context.Context,
    image string,
    subscriber chan<- ProgressEvent,
) error

// SyncToNode 把 manager 本机镜像同步到指定 node。
// 同一 (image, nodeID) 并发合并；不同节点的 sync 仍可并发。
func (c *Coordinator) SyncToNode(
    ctx context.Context,
    image string,
    nodeID string,
    subscriber chan<- ProgressEvent,
) error
```

#### 5.1 单飞实现

不直接用 `golang.org/x/sync/singleflight`——因为标准 singleflight 不支持"等待者拿进度流"。自己实现：

```go
type pullJob struct {
    done       chan struct{}      // leader 完成时关闭
    err        error
    subscribers []chan<- ProgressEvent
    mu         sync.Mutex
}

// PullImage 进入临界区：
//   1. lookup pulls[image]：
//      - 命中：把 subscriber 加进 job.subscribers，挂等 <-job.done；
//      - 未命中：建 job 写 map、自己当 leader、解锁后真正调 ImagePull；
//   2. leader 跑完后 broadcast 关闭 done、删 map entry。
// 进度事件 leader 解析 NDJSON 时遍历 subscribers 用 non-blocking send（满了直接丢，避免慢消费拖累 leader）。
```

#### 5.2 进度聚合

Docker pull 是 layer 维度的多路并发流，每行 NDJSON 形如：

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

每秒（或 Layer 状态变化时）发一次聚合 ProgressEvent。

#### 5.3 Sync 进度

`ImageSave` 返回 reader 后，wrap 一层 `countingReader` 累加字节；总量从 `client.ImageInspect` 的 `Size` 字段拿（精确到单镜像总字节）。Agent 侧 `docker load` 的进度无法分阶段读取，sync 阶段进度仅覆盖 manager → agent 上传段（占主要时间）。

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
- 清空 `init_progress_current/total`（新阶段从 0 开始）。

`markFailed` 把 status 推到 `error`，同时写 `init_failed_phase = step.phase`。

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

#### 7.1 启动时机

`cmd/server/main.go` 装配阶段，**在 worker 启动前**跑 reaper；reaper 完成才放 worker pool 接 job。

```go
// cmd/server/main.go 装配顺序
// 1. db / redis / agent client 等基础组件
// 2. reaper.Run(ctx, store, jobNotifier)
// 3. workerPool.Start()
```

#### 7.2 reaper 实现

新增 `internal/worker/reaper/reaper.go`：

```go
// ReapStaleInits 扫描所有 status ∈ 5 init 子状态的 apps，重置为 pulling_image，
// 找最近一份 app_initialize job 重置为 pending（或重新入队），
// 然后 enqueue 通知 scheduler 立即拾取。
func ReapStaleInits(ctx context.Context, store ReaperStore, notifier JobNotifier) error
```

逻辑：

```sql
-- 扫描孤儿
SELECT id, runtime_node_id FROM apps
WHERE status IN ('pulling_image','syncing_image','preparing_runtime','creating_container','starting')
  AND deleted_at IS NULL;
```

对每条记录：

1. 单事务里：
   - `UPDATE apps SET status='pulling_image', init_progress_current=NULL, init_progress_total=NULL, init_failed_phase=NULL WHERE id=$1`；
   - 找该 app 最近一份 `app_initialize` job：
     - 存在且 status ∈ {running, succeeded}：`UPDATE jobs SET status='pending', started_at=NULL WHERE id=$id`；
     - 存在且 status = pending：跳过（scheduler 自然会拾取）；
     - 不存在：新建一份；
2. 事务外：`notifier.Enqueue(jobID)`；通知失败仅记日志（scheduler 兜底扫表）。

#### 7.3 单 manager 假设

当前架构是单 manager 实例，reaper 安全。若未来引入多 manager：
- 加 `apps.manager_instance_id` 字段（reaper 只处理本实例 id）；
- 或 reaper 走 advisory lock（`SELECT pg_advisory_lock(...)` 防多实例同时 reap）。

本 spec 在注释中声明假设，不实现多 manager 逻辑。

#### 7.4 进度字段恢复语义

reaper 把 `init_progress_*` 全清空。重启后用户会看到状态从 `starting` 回退到 `pulling_image` → 1 秒内通过各阶段幂等检查 → 跳回原位继续。这个回退在 UI 上是视觉抖动，但**业务正确性不受影响**，且实现极简。可接受。

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
// 清空 init_progress_* 与 init_failed_phase。
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
    v-if="app.init_progress_total"
    :value="app.init_progress_current"
    :max="app.init_progress_total"
  />
  <span v-if="app.init_progress_total">
    {{ formatBytes(app.init_progress_current) }} / {{ formatBytes(app.init_progress_total) }}
  </span>
</div>
```

`isInitPhase` 在 `status.ts` 导出，返回 `status` 是否在 5 个子状态里。

#### 9.3 失败提示

status=error 且 `init_failed_phase != null` 时，把"重新初始化"按钮上方的提示文案换为：

```
在「{{ formatAppStatus(init_failed_phase).label }}」阶段失败
```

#### 9.4 「重新初始化」按钮可见条件

`AppOverviewTab.vue:148` 当前是 `status === 'error' || status === 'draft'`。保持不变（5 个 init 子状态期间不展示重试按钮，避免用户在 worker 还在跑的时候点出第二份 job）。

#### 9.5 OpenAPI 与生成产物

按 `AGENTS.md` 流程：
- `internal/api/handlers/dto.go` 暴露 `init_progress_current/total/init_failed_phase` 到 App 响应 DTO；
- 跑 `make openapi-gen` + `make web-types-gen`；
- `web/src/api/generated.ts` 同步更新。

### 十、测试

#### 10.1 后端单测

| 文件 | 覆盖点 |
|---|---|
| `internal/domain/app_state_machine_test.go` | 表驱动覆盖 21 条合法转移 + 关键非法转移（如 `running → pulling_image` 必须失败） |
| `internal/runtime/imagecoord/coordinator_test.go` | 并发 PullImage 单飞合并；subscriber 能收到事件；leader 失败后下个 subscriber 升级 |
| `internal/runtime/imagecoord/progress_test.go` | NDJSON 解析；多 layer 字节累加；`Pull complete` 视为 current=total |
| `internal/runtime/imagesync/sdk_provider_test.go` | mock docker SDK 验证 ImageID / ImageSave / ImagePull 能正确串接 |
| `internal/worker/handlers/app_initialize_test.go` | 表驱动覆盖每阶段 status 推进；任意阶段 mock 失败应写 `init_failed_phase`；幂等检查（重跑相同 job 不重复创建容器） |
| `internal/worker/handlers/progress_reporter_test.go` | 1s 节流边界；5% 阈值边界；阶段切换 flush；context 取消不写库 |
| `internal/worker/reaper/reaper_test.go` | 5 个孤儿状态都能被扫到；job 状态分支（无 / pending / running / succeeded）都能正确处置 |

#### 10.2 前端单测

| 文件 | 覆盖点 |
|---|---|
| `web/src/domain/status.spec.ts` | 5 个新 status 都能映射到正确 label/tone；isInitPhase 边界 |
| `web/src/pages/apps/AppOverviewTab.spec.ts` | init_progress_total=null 时不渲染 progress；status=error + init_failed_phase 时显示阶段文案 |

#### 10.3 浏览器验证

按 `AGENTS.md` 要求，开发完成后必须用浏览器跑通：

1. 创建一个新 app（manager 本机已有镜像）：观察 status 序列 `draft → pulling_image（瞬间）→ syncing_image（带进度）→ preparing_runtime → creating_container → starting → binding_waiting`；
2. 故意让 manager 本机删掉镜像后创建：验证 pulling_image 阶段进度条能动；
3. 同时点 2 个补建：验证两个 app 都能看到镜像同步进度（广播生效）；
4. 在 syncing_image 中途 `docker compose restart manager`：重启后两个 app 仍能完成初始化；
5. 让 agent 临时拒绝 inspect 调用：验证 status=error + init_failed_phase=syncing_image，前端"重新初始化"按钮可点。

### 十一、不做的事（Out of scope）

- **不引入 SSE**：用户对实时性的要求是"看到进度在动"，前端 3-5 秒轮询足够；SSE 引入断连重试、多副本部署等复杂度，本期不值。
- **不持久化进度阶段历史**：每次 reaper 重置时 init_progress_* 直接清空。如果未来要做"初始化耗时分析"，单独设计 metrics 表，不与运行态字段耦合。
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
| reaper 误把刚启动的 worker 正在处理的 app 重置 | reaper 在 worker pool 启动前完成（顺序约束）；不存在并发 |
| 多 manager 部署时 reaper 互相覆盖 | 当前单 manager 假设；spec 显式声明，未来扩展走 advisory lock 或 manager_instance_id 字段 |

## 实现顺序建议

1. **数据库 + 状态机**：migration、enums.go、state_machine.go、status.ts 文案 —— 一个独立 PR，先把契约改了；
2. **Docker SDK 替换**：LocalDockerSDKProvider 落地，去掉 LocalDockerCLIProvider；不改 worker 流程；
3. **ImageCoordinator + progressReporter**：单飞与广播逻辑，独立测试；
4. **Worker handler 改造**：5 阶段化 + 进度上报 + 幂等强化；
5. **Reaper**：在 cmd/server 装配，加重启冒烟测试；
6. **前端展示**：进度条 + 失败阶段文案；
7. **联调 + 浏览器验证**。

每步都能独立 commit、可回滚。
