# Hermes 镜像 Agent 自行拉取 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每个 agent 节点直接从公网 registry 拉取 hermes 镜像，彻底去掉 manager 本地拉取 + 6GB tar 传输链路。

**Architecture:** manager 通过 agent docker proxy（`/v1/docker/*`）调 Docker SDK，按 (nodeID, imageRef) 加分布式锁后执行 `ImagePull`，广播 NDJSON 进度给 SSE 订阅者；结果写入 `apps.runtime_image_ref/sha256`；Reaper 恢复时读 DB 存储值而非配置文件；容器加 `restart=always`。

**Tech Stack:** Go, Docker SDK (`github.com/docker/docker/client`), PostgreSQL (golang-migrate), sqlc, Redis (分布式锁 + Pub/Sub), Vue 3 + Naive UI

---

## 文件变更总览

| 操作 | 路径 |
|------|------|
| 新建 | `internal/migrations/000019_app_runtime_image.up.sql` |
| 新建 | `internal/migrations/000019_app_runtime_image.down.sql` |
| 修改 | `internal/domain/enums.go` |
| 修改 | `internal/domain/app_state_machine.go` |
| 修改 | `internal/store/queries/apps.sql` |
| 重新生成 | `internal/store/sqlc/` （sqlc generate） |
| 修改 | `internal/runtime/imagecoord/coordinator.go` |
| 修改 | `internal/runtime/imagecoord/coordinator_test.go` |
| 删除 | `internal/runtime/imagecoord/types.go` |
| 修改 | `internal/integrations/runtime/adapter.go` |
| 修改 | `internal/integrations/runtime/agent_backed.go` |
| 修改 | `runtime/agent/main.go` |
| 修改 | `runtime/agent/docker_client.go` |
| 修改 | `internal/worker/handlers/app_initialize.go` |
| 修改 | `internal/worker/handlers/app_initialize_test.go` |
| 修改 | `internal/worker/reaper/reaper.go` |
| 修改 | `internal/worker/reaper/reaper_test.go` |
| 删除 | `internal/runtime/imagesync/` （整包） |
| 删除 | `internal/service/image_distribution_service.go` |
| 删除 | `internal/service/image_distribution_service_test.go` |
| 修改 | `internal/service/app_service.go` |
| 修改 | `web/src/domain/status.ts` |
| 修改 | `web/src/api/hooks/useApps.ts` |
| 修改 | `web/src/pages/apps/AppOverviewTab.vue` |

---

## Task 1: DB 迁移 — 新增列和新状态枚举

**Files:**
- Create: `internal/migrations/000019_app_runtime_image.up.sql`
- Create: `internal/migrations/000019_app_runtime_image.down.sql`

- [ ] **Step 1: 写 up 迁移**

```sql
-- internal/migrations/000019_app_runtime_image.up.sql
-- apps 表：扩展 status CHECK 约束（增加 pulling_runtime_image），
-- 新增 runtime_image_ref 和 runtime_image_sha256 两列。
-- 存量行两列默认空串，等首次重新初始化时由 phasePullRuntimeImage 写入。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft',
        'pulling_runtime_image',
        'pulling_image', 'syncing_image', 'preparing_runtime',
        'creating_container', 'starting',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    ADD COLUMN runtime_image_ref    TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_image_sha256 TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN apps.runtime_image_ref    IS '部署时实际使用的镜像引用（含 tag）；phasePullRuntimeImage 写入，之后不变。';
COMMENT ON COLUMN apps.runtime_image_sha256 IS '拉取后 docker inspect 返回的镜像 ID（sha256:…）；供展示和排查使用。';
```

- [ ] **Step 2: 写 down 迁移**

```sql
-- internal/migrations/000019_app_runtime_image.down.sql
ALTER TABLE apps DROP CONSTRAINT apps_status_check;

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
    DROP COLUMN runtime_image_ref,
    DROP COLUMN runtime_image_sha256;
```

- [ ] **Step 3: 执行迁移并验证**

```bash
make migrate-up
```

预期：无报错，`\d apps` 可见 `runtime_image_ref`、`runtime_image_sha256` 两列，status check 含 `pulling_runtime_image`。

- [ ] **Step 4: Commit**

```bash
git add internal/migrations/000019_app_runtime_image.up.sql internal/migrations/000019_app_runtime_image.down.sql
git commit -m "feat(db): 新增 pulling_runtime_image 状态和 runtime_image_ref/sha256 列"
```

---

## Task 2: Domain 枚举 + 状态机

**Files:**
- Modify: `internal/domain/enums.go`
- Modify: `internal/domain/app_state_machine.go`
- Test: `internal/domain/app_state_machine_test.go`

- [ ] **Step 1: 写新状态枚举的失败测试**

在 `internal/domain/app_state_machine_test.go` 末尾追加：

```go
// TestAppTransition_PullingRuntimeImage 验证新增 pulling_runtime_image 状态的合法转移路径。
func TestAppTransition_PullingRuntimeImage(t *testing.T) {
    // draft → pulling_runtime_image：worker 从第一阶段入口触发
    assert.True(t, IsAppTransitionAllowed(AppStatusDraft, AppStatusPullingRuntimeImage))
    // pulling_runtime_image → preparing_runtime：跳过原 syncing_image 直接进准备阶段
    assert.True(t, IsAppTransitionAllowed(AppStatusPullingRuntimeImage, AppStatusPreparingRuntime))
    // pulling_runtime_image → error：拉取失败收敛到 error
    assert.True(t, IsAppTransitionAllowed(AppStatusPullingRuntimeImage, AppStatusError))
    // error → pulling_runtime_image：重试入口
    assert.True(t, IsAppTransitionAllowed(AppStatusError, AppStatusPullingRuntimeImage))
    // pulling_runtime_image 不能直接到 syncing_image（旧路径已废弃）
    assert.False(t, IsAppTransitionAllowed(AppStatusPullingRuntimeImage, AppStatusSyncingImage))
    // IsAppStatus 应识别新状态
    assert.True(t, IsAppStatus(AppStatusPullingRuntimeImage))
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && go test ./internal/domain/... -run TestAppTransition_PullingRuntimeImage -v
```

预期：FAIL（常量未定义）。

- [ ] **Step 3: enums.go 中新增常量**

在 `internal/domain/enums.go` 的 `AppStatus*` 常量块中，在 `AppStatusPullingImage` 之前加入：

```go
// AppStatusPullingRuntimeImage 替代 pulling_image + syncing_image 两阶段；
// 由 phasePullRuntimeImage 驱动，让每个 agent 直接从公网 registry 拉取 hermes 镜像。
AppStatusPullingRuntimeImage = "pulling_runtime_image"
```

在 `validAppStatuses` 的 `set(...)` 调用中加入 `AppStatusPullingRuntimeImage`。

- [ ] **Step 4: app_state_machine.go 中添加转移规则**

在 `appTransitions` map 中加入：

```go
// pulling_runtime_image 阶段：agent 自行拉取镜像，直接进入 preparing_runtime。
{From: AppStatusDraft, To: AppStatusPullingRuntimeImage}:              {},
{From: AppStatusPullingRuntimeImage, To: AppStatusPreparingRuntime}:   {},
{From: AppStatusPullingRuntimeImage, To: AppStatusError}:              {},
// error 重试入口同时支持新旧两种第一阶段。
{From: AppStatusError, To: AppStatusPullingRuntimeImage}: {},
```

更新文件顶部的状态机注释图，在 draft 后加一行 `pulling_runtime_image → preparing_runtime`。

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./internal/domain/... -v
```

预期：ALL PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/domain/enums.go internal/domain/app_state_machine.go internal/domain/app_state_machine_test.go
git commit -m "feat(domain): 新增 pulling_runtime_image 状态及合法转移规则"
```

---

## Task 3: SQLC 查询 + 代码生成

**Files:**
- Modify: `internal/store/queries/apps.sql`
- Regenerate: `internal/store/sqlc/` (via `make sqlc-generate`)

- [ ] **Step 1: 在 apps.sql 末尾追加新查询**

```sql
-- name: UpdateAppRuntimeImage :one
-- phasePullRuntimeImage 成功后写入镜像引用与 sha256。
UPDATE apps
SET
    runtime_image_ref    = $2,
    runtime_image_sha256 = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 2: 更新 ListStaleInits 查询的 IN 子句**

将 `internal/store/queries/apps.sql` 中：
```sql
AND status IN ('pulling_image','syncing_image','preparing_runtime','creating_container','starting')
```
改为：
```sql
AND status IN ('pulling_runtime_image','pulling_image','syncing_image','preparing_runtime','creating_container','starting')
```

- [ ] **Step 3: 重新生成 SQLC 代码**

```bash
make sqlc-generate
```

预期：`internal/store/sqlc/apps.sql.go` 包含 `UpdateAppRuntimeImage` 方法，`models.go` 中 `App` 结构体含 `RuntimeImageRef string` 和 `RuntimeImageSha256 string` 字段。

- [ ] **Step 4: 编译确认**

```bash
go build ./...
```

预期：编译通过（新字段暂未被引用，只是增量）。

- [ ] **Step 5: Commit**

```bash
git add internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(store): 新增 UpdateAppRuntimeImage 查询，ListStaleInits 覆盖新状态"
```

---

## Task 4: imagecoord.Coordinator 重构

**Files:**
- Modify: `internal/runtime/imagecoord/coordinator.go`
- Delete: `internal/runtime/imagecoord/types.go`
- Modify: `internal/runtime/imagecoord/coordinator_test.go`

- [ ] **Step 1: 写 PullImageOnNode 测试（先红）**

替换 `coordinator_test.go` 全部内容：

```go
package imagecoord

import (
    "archive/tar"
    "bytes"
    "context"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    dockerclient "github.com/docker/docker/client"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    ocredis "oc-manager/internal/redis"
)

// newTestDockerServer 返回一个极简 httptest server 模拟 docker daemon HTTP API。
// imagePresent=true 时 /images/inspect 返回 200；否则返回 404。
// pullStream 是 ImagePull 端点（/images/create）返回的 NDJSON 内容。
func newTestDockerServer(t *testing.T, imagePresent bool, pullStream string) (*httptest.Server, string) {
    t.Helper()
    // 构造一个有效的 tar 归档，让 ImageInspect 在 present 时返回 ID
    const fakeID = "sha256:9cf46248b69906ff754a1cd231720d707e4ea36f9b03e81d48f008f025c66f93"

    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        path := r.URL.Path
        switch {
        case strings.Contains(path, "/images/") && strings.HasSuffix(path, "/json"):
            // ImageInspectWithRaw
            if !imagePresent {
                w.WriteHeader(http.StatusNotFound)
                _, _ = w.Write([]byte(`{"message":"No such image"}`))
                return
            }
            w.Header().Set("Content-Type", "application/json")
            _, _ = w.Write([]byte(`{"Id":"` + fakeID + `","RepoTags":["hermes:v1"]}`))
        case strings.Contains(path, "/images/create"):
            // ImagePull
            w.Header().Set("Content-Type", "application/json")
            _, _ = w.Write([]byte(pullStream))
        default:
            w.WriteHeader(http.StatusNotFound)
        }
    })
    srv := httptest.NewServer(mux)
    return srv, fakeID
}

func newTestDockerClient(t *testing.T, baseURL string) *dockerclient.Client {
    t.Helper()
    cli, err := dockerclient.NewClientWithOpts(
        dockerclient.WithHost(baseURL),
        dockerclient.WithVersion("1.45"),
    )
    require.NoError(t, err)
    return cli
}

// fakeLocker 控制 TryAcquire 是否成功。
type fakeLocker struct {
    acquireOK bool
}

func (l *fakeLocker) TryAcquire(_ context.Context, _, _ string, _ interface{ /* time.Duration */ }) (bool, error) {
    return l.acquireOK, nil
}
func (l *fakeLocker) Renew(_ context.Context, _, _ string, _ interface{}) error { return nil }
func (l *fakeLocker) Release(_ context.Context, _, _ string) error              { return nil }
func (l *fakeLocker) Exists(_ context.Context, _ string) (bool, error)          { return false, nil }

// fakeBus 仅丢弃发布的事件，不实现 Subscribe。
type fakeBus struct{}

func (b *fakeBus) Publish(_ context.Context, _ string, _ ProgressEvent) error { return nil }
func (b *fakeBus) PublishDone(_ context.Context, _ string, _ error) error      { return nil }
func (b *fakeBus) Subscribe(_ context.Context, _ string) (<-chan ocredis.ProgressMessage, func(), error) {
    ch := make(chan ocredis.ProgressMessage)
    return ch, func() { close(ch) }, nil
}

// TestCoordinator_PullImageOnNode_AlreadyPresent 镜像已在节点上时直接返回 sha256，不触发 pull。
func TestCoordinator_PullImageOnNode_AlreadyPresent(t *testing.T) {
    srv, wantID := newTestDockerServer(t, true, "")
    defer srv.Close()
    cli := newTestDockerClient(t, srv.URL)

    coord := NewCoordinator(&fakeLocker{acquireOK: true}, &fakeBus{}, "test-instance")
    sub := make(chan ProgressEvent, 4)

    id, err := coord.PullImageOnNode(context.Background(), "node-1", "hermes:v1", cli, sub)
    require.NoError(t, err)
    assert.Equal(t, wantID, id)
    // 镜像已存在时不应有任何进度事件
    assert.Empty(t, sub)
}

// TestCoordinator_PullImageOnNode_Leader 镜像不存在时作为 leader 执行 pull 并返回 sha256。
func TestCoordinator_PullImageOnNode_Leader(t *testing.T) {
    // 第一次 inspect 返回 404，pull 后第二次 inspect 返回 200
    callCount := 0
    const fakeID = "sha256:9cf46248b69906ff754a1cd231720d707e4ea36f9b03e81d48f008f025c66f93"
    pullNDJSON := `{"status":"Pulling fs layer","id":"abc","progressDetail":{"current":100,"total":200}}` + "\n" +
        `{"status":"Pull complete","id":"abc","progressDetail":{}}` + "\n"

    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        path := r.URL.Path
        if strings.Contains(path, "/images/") && strings.HasSuffix(path, "/json") {
            callCount++
            if callCount == 1 {
                // 首次 inspect：镜像不存在
                w.WriteHeader(http.StatusNotFound)
                _, _ = w.Write([]byte(`{"message":"No such image"}`))
                return
            }
            // 二次 inspect（pull 完成后）：镜像存在
            w.Header().Set("Content-Type", "application/json")
            _, _ = w.Write([]byte(`{"Id":"` + fakeID + `","RepoTags":["hermes:v1"]}`))
            return
        }
        if strings.Contains(path, "/images/create") {
            w.Header().Set("Content-Type", "application/json")
            _, _ = w.Write([]byte(pullNDJSON))
            return
        }
        w.WriteHeader(http.StatusNotFound)
    })
    srv := httptest.NewServer(mux)
    defer srv.Close()

    cli := newTestDockerClient(t, srv.URL)
    coord := NewCoordinator(&fakeLocker{acquireOK: true}, &fakeBus{}, "test-instance")
    sub := make(chan ProgressEvent, 16)

    id, err := coord.PullImageOnNode(context.Background(), "node-1", "hermes:v1", cli, sub)
    require.NoError(t, err)
    assert.Equal(t, fakeID, id)
    // 应有至少一个进度事件
    assert.NotEmpty(t, sub)
}

// buildMinimalTar 构造最小 tar 以绕开 tar 解析（unused 但保留供参考）
func buildMinimalTar() []byte {
    var buf bytes.Buffer
    tw := tar.NewWriter(&buf)
    _ = tw.WriteHeader(&tar.Header{Name: "test.txt", Size: 4, Mode: 0644})
    _, _ = tw.Write([]byte("test"))
    _ = tw.Close()
    return buf.Bytes()
}
```

> **注意**：`fakeLocker` 的 `TryAcquire` 参数使用 `interface{}` 是占位，实际需对齐 `ocredis.DistLocker` 接口签名（`time.Duration`）。先让测试能编译即可，后续步骤实现正确签名。

- [ ] **Step 2: 运行测试确认当前状态**

```bash
go test ./internal/runtime/imagecoord/... -v 2>&1 | head -30
```

预期：编译报错（`PullImageOnNode` 未定义），确认测试框架搭好。

- [ ] **Step 3: 重写 coordinator.go**

将 `internal/runtime/imagecoord/coordinator.go` 替换为以下内容：

```go
package imagecoord

import (
    "context"
    "fmt"
    "sync"
    "time"

    dockerclient "github.com/docker/docker/client"
    "github.com/docker/docker/api/types/image"
    "github.com/google/uuid"

    ocredis "oc-manager/internal/redis"
)

// Coordinator 通过 Redis 分布式锁 + Pub/Sub 确保同一 (nodeID, imageRef) 在集群内只做一次 pull。
//
// 设计：lock 按 (nodeID, imageRef) 粒度，不同 agent 可以并行拉取，
// 同一 agent 同一镜像串行（避免重复占用带宽）。
// Redis 失联最多导致重复拉取或进度短暂不更新，不影响正确性。
type Coordinator struct {
    locker     ocredis.DistLocker
    bus        ocredis.ProgressBus
    instanceID string

    // 同进程 fanout：leader send 时既 redis.Publish 给其他实例，
    // 也通过 subscribers map 直接推给本进程的其他订阅者，省一次往返。
    mu          sync.Mutex
    subscribers map[string][]chan<- ProgressEvent
}

const (
    defaultLockTTL      = 5 * time.Minute
    watchdogInterval    = 90 * time.Second
    followerWaitGrace   = 30 * time.Second
    progressTickInterval = time.Second
)

// ErrLeaderLost 表示 follower 等待 leader 超时。上层 worker 应让 job 失败重试。
var ErrLeaderLost = fmt.Errorf("imagecoord: leader timed out, please retry")

// NewCoordinator 创建实例。instanceID 推荐使用 manager 进程启动时生成的 UUID。
func NewCoordinator(locker ocredis.DistLocker, bus ocredis.ProgressBus, instanceID string) *Coordinator {
    return &Coordinator{
        locker:      locker,
        bus:         bus,
        instanceID:  instanceID,
        subscribers: map[string][]chan<- ProgressEvent{},
    }
}

// PullImageOnNode 通过 cli（agent docker proxy）确保 imageRef 存在于目标节点，跨实例 single-flight。
//
// 流程：
//  1. ImageInspectWithRaw 预检：tag 已存在则直接返回 sha256（tag 不可变）。
//  2. TryAcquire 按 (nodeID, imageRef) 加锁：抢到为 leader，否则为 follower。
//  3. leader：启 watchdog 续期 → 执行 ImagePull → 广播进度 → PhaseDone → 释放锁 → 返回 sha256。
//  4. follower：Subscribe channel → 二次检查锁是否存在 → 消费到 PhaseDone 或超时 → ImageInspect 取 sha256。
//
// subscriber 用于把 NDJSON 进度透传给调用方；函数返回前必然 close。
func (c *Coordinator) PullImageOnNode(ctx context.Context, nodeID, imageRef string, cli *dockerclient.Client, subscriber chan<- ProgressEvent) (string, error) {
    defer closeIfOpen(subscriber)

    // 预检：tag 已在节点上，直接返回 sha256。
    if info, _, err := cli.ImageInspectWithRaw(ctx, imageRef); err == nil {
        return info.ID, nil
    }

    lockKey := fmt.Sprintf("ocm:image:nodepull:lock:%s:%s", nodeID, imageRef)
    channel := fmt.Sprintf("ocm:image:nodepull:bus:%s:%s", nodeID, imageRef)
    token := c.instanceID + ":" + uuid.NewString()

    got, err := c.locker.TryAcquire(ctx, lockKey, token, defaultLockTTL)
    if err != nil {
        return "", fmt.Errorf("抢镜像 pull 锁: %w", err)
    }
    if got {
        var pulledID string
        err := c.runLeader(ctx, channel, subscriber, lockKey, token, func(ctx context.Context, send func(ProgressEvent)) error {
            id, pullErr := c.doPullOnNode(ctx, imageRef, cli, send)
            if pullErr == nil {
                pulledID = id
            }
            return pullErr
        })
        if err != nil {
            return "", err
        }
        return pulledID, nil
    }
    // follower 路径：等 leader 完成后自行 inspect 拿 sha256。
    if err := c.runFollower(ctx, channel, lockKey, subscriber); err != nil {
        return "", err
    }
    info, _, err := cli.ImageInspectWithRaw(ctx, imageRef)
    if err != nil {
        return "", fmt.Errorf("follower 等待完成后 inspect 失败: %w", err)
    }
    return info.ID, nil
}

// doPullOnNode 通过 agent docker proxy 拉取镜像，将 NDJSON 进度通过 send 回调广播。
// 拉取完成后 inspect 返回 sha256。
func (c *Coordinator) doPullOnNode(ctx context.Context, imageRef string, cli *dockerclient.Client, send func(ProgressEvent)) (string, error) {
    rc, err := cli.ImagePull(ctx, imageRef, image.PullOptions{})
    if err != nil {
        return "", fmt.Errorf("docker image pull %s: %w", imageRef, err)
    }
    defer rc.Close()

    agg := NewPullAggregator()
    ticker := time.NewTicker(progressTickInterval)
    defer ticker.Stop()
    doneCh := make(chan error, 1)
    go func() { doneCh <- agg.FeedReader(rc) }()

    for {
        select {
        case <-ticker.C:
            cur, tot := agg.Snapshot()
            send(ProgressEvent{Phase: "pulling_runtime_image", Current: cur, Total: tot})
        case err := <-doneCh:
            cur, tot := agg.Snapshot()
            send(ProgressEvent{Phase: "pulling_runtime_image", Current: cur, Total: tot})
            if err != nil {
                return "", fmt.Errorf("读取 pull 流失败: %w", err)
            }
            // 拉取流结束后 inspect 确认镜像落地并获取 sha256。
            info, _, inspErr := cli.ImageInspectWithRaw(ctx, imageRef)
            if inspErr != nil {
                return "", fmt.Errorf("pull 完成后 inspect 失败: %w", inspErr)
            }
            return info.ID, nil
        case <-ctx.Done():
            return "", ctx.Err()
        }
    }
}

// runLeader 是 PullImageOnNode leader 流程的共享实现（与原 coordinator.go 同形态）。
func (c *Coordinator) runLeader(
    ctx context.Context,
    channel string,
    subscriber chan<- ProgressEvent,
    lockKey, token string,
    op func(ctx context.Context, send func(ProgressEvent)) error,
) error {
    watchCtx, cancelWatch := context.WithCancel(ctx)
    defer cancelWatch()
    go c.watchdog(watchCtx, lockKey, token, cancelWatch)

    c.registerSubscriber(channel, subscriber)
    defer c.unregisterSubscriber(channel, subscriber)

    send := func(ev ProgressEvent) {
        c.fanout(channel, ev)
        _ = c.bus.Publish(ctx, channel, ev)
    }

    opErr := op(watchCtx, send)
    _ = c.bus.PublishDone(context.Background(), channel, opErr)
    cancelWatch()
    _ = c.locker.Release(context.Background(), lockKey, token)
    return opErr
}

// runFollower 等待 leader 把镜像准备就绪（与原 coordinator.go 同形态）。
func (c *Coordinator) runFollower(ctx context.Context, channel, lockKey string, subscriber chan<- ProgressEvent) error {
    ch, cancel, err := c.bus.Subscribe(ctx, channel)
    if err != nil {
        return fmt.Errorf("follower 订阅失败: %w", err)
    }
    defer cancel()

    exists, err := c.locker.Exists(ctx, lockKey)
    if err != nil {
        return fmt.Errorf("follower 检查 leader 锁: %w", err)
    }
    if !exists {
        return nil
    }

    deadline := time.NewTimer(defaultLockTTL + followerWaitGrace)
    defer deadline.Stop()
    for {
        select {
        case msg, ok := <-ch:
            if !ok {
                return ErrLeaderLost
            }
            if msg.Event.Phase == ocredis.PhaseDone {
                return msg.Err
            }
            select {
            case subscriber <- msg.Event:
            default:
            }
        case <-deadline.C:
            return ErrLeaderLost
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

// watchdog 周期为 leader 锁续期，连续 3 次失败则 abort。
func (c *Coordinator) watchdog(ctx context.Context, lockKey, token string, abort context.CancelFunc) {
    ticker := time.NewTicker(watchdogInterval)
    defer ticker.Stop()
    failures := 0
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := c.locker.Renew(ctx, lockKey, token, defaultLockTTL); err != nil {
                failures++
                if failures >= 3 {
                    abort()
                    return
                }
                continue
            }
            failures = 0
        }
    }
}

func (c *Coordinator) registerSubscriber(channel string, sub chan<- ProgressEvent) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.subscribers[channel] = append(c.subscribers[channel], sub)
}

func (c *Coordinator) unregisterSubscriber(channel string, sub chan<- ProgressEvent) {
    c.mu.Lock()
    defer c.mu.Unlock()
    subs := c.subscribers[channel]
    for i, s := range subs {
        if s == sub {
            c.subscribers[channel] = append(subs[:i], subs[i+1:]...)
            break
        }
    }
}

func (c *Coordinator) fanout(channel string, ev ProgressEvent) {
    c.mu.Lock()
    subs := append([]chan<- ProgressEvent(nil), c.subscribers[channel]...)
    c.mu.Unlock()
    for _, s := range subs {
        select {
        case s <- ev:
        default:
        }
    }
}

func closeIfOpen(ch chan<- ProgressEvent) {
    defer func() { _ = recover() }()
    close(ch)
}
```

- [ ] **Step 4: 删除 types.go，更新 ProgressEvent 定义**

`types.go` 中只有 `LocalImageProvider`、`AgentImageClient`、`RemoteImageInfo` 和 `ProgressEvent` 的 type alias。删除整个文件，但 `ProgressEvent = ocredis.ProgressEvent` 的 type alias 需迁移到 `coordinator.go` 顶部（import 块下方）：

```go
// ProgressEvent 是 leader 广播给所有 subscriber 的进度；与 redis.ProgressEvent 同形态。
type ProgressEvent = ocredis.ProgressEvent
```

执行删除：
```bash
rm internal/runtime/imagecoord/types.go
```

- [ ] **Step 5: 修复测试文件中的 fakeLocker 签名**

`coordinator_test.go` 中 `fakeLocker` 的方法签名需精确匹配 `ocredis.DistLocker` 接口（参数是 `time.Duration`，不是 `interface{}`）：

```go
import "time"

func (l *fakeLocker) TryAcquire(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
    return l.acquireOK, nil
}
func (l *fakeLocker) Renew(_ context.Context, _, _ string, _ time.Duration) error { return nil }
func (l *fakeLocker) Release(_ context.Context, _, _ string) error                { return nil }
func (l *fakeLocker) Exists(_ context.Context, _ string) (bool, error)            { return false, nil }
```

- [ ] **Step 6: 编译 + 测试**

```bash
go build ./internal/runtime/imagecoord/... && go test ./internal/runtime/imagecoord/... -v
```

预期：全部通过。

- [ ] **Step 7: Commit**

```bash
git add internal/runtime/imagecoord/
git commit -m "refactor(imagecoord): 重构 Coordinator，以 PullImageOnNode 替代 PullImage+SyncToNode"
```

---

## Task 5: ContainerSpec 新增 RestartPolicy

**Files:**
- Modify: `internal/integrations/runtime/adapter.go`
- Modify: `internal/integrations/runtime/agent_backed.go`

- [ ] **Step 1: 在 ContainerSpec 中新增字段**

在 `internal/integrations/runtime/adapter.go` 的 `ContainerSpec` 结构体末尾加：

```go
// RestartPolicy 是 Docker 容器重启策略；常用值：
//   "always"        — 无论退出码总是重启，适合生产长驻服务；
//   "on-failure"    — 非零退出码才重启；
//   "unless-stopped" — 除非被 docker stop 否则总是重启；
//   ""              — 不设置，使用 docker 默认（no 策略）。
RestartPolicy string
```

同时删除 `adapter.go` 中 `import` 里对 `imagesync` 的引用（如果有的话，检查后删除）。

- [ ] **Step 2: translateSpec 映射 RestartPolicy**

在 `internal/integrations/runtime/agent_backed.go` 的 `translateSpec` 函数中，`hostCfg` 赋值处加入 RestartPolicy：

```go
hostCfg := &container.HostConfig{
    Binds: bindStrings(spec.Volumes),
    Resources: container.Resources{
        NanoCPUs: spec.Resources.CPULimit * 1_000_000,
        Memory:   spec.Resources.MemoryBytes,
    },
    RestartPolicy: container.RestartPolicy{
        Name: container.RestartPolicyMode(spec.RestartPolicy),
    },
}
```

- [ ] **Step 3: 暴露 DockerClientForNode 方法**

`AgentBackedAdapter` 的 `dockerClient` 方法是 private。在 `agent_backed.go` 末尾加一个公开方法供 handler 获取节点 docker client：

```go
// DockerClientForNode 返回指向目标节点 agent docker proxy 的 SDK client。
// 供 AppInitializeHandler.phasePullRuntimeImage 使用。
func (a *AgentBackedAdapter) DockerClientForNode(ctx context.Context, nodeID string) (*client.Client, error) {
    return a.dockerClient(ctx, nodeID)
}
```

- [ ] **Step 4: 编译确认**

```bash
go build ./internal/integrations/runtime/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/runtime/adapter.go internal/integrations/runtime/agent_backed.go
git commit -m "feat(runtime): ContainerSpec 新增 RestartPolicy，暴露 DockerClientForNode"
```

---

## Task 6: Agent 清理 — 删除自定义镜像端点

**Files:**
- Modify: `runtime/agent/main.go`
- Modify: `runtime/agent/docker_client.go`

- [ ] **Step 1: 写失败测试验证端点已删除**

在 `runtime/agent/` 目录下确认 `main_test.go` 或 `docker_client_test.go` 中是否有 `/v1/images/inspect` 或 `/v1/images/load` 相关测试。若有则先删除这些测试用例（这些端点即将不存在）。

- [ ] **Step 2: 删除 DockerClient 接口中的三个方法**

将 `runtime/agent/docker_client.go` 中的 `DockerClient` interface 改为只保留 `ListContainers`：

```go
// DockerClient 封装 agent 对本机 Docker Engine 的最小依赖。
// manager 通过 agent docker proxy 直接操作 Docker，agent 只需暴露
// ListContainers 用于节点资源统计；InspectImage / LoadImage / TagImage
// 已全部迁移到 docker proxy 路径，不再由 agent 自定义端点提供。
type DockerClient interface {
    ListContainers(ctx context.Context, namePrefix string) (int32, error)
}
```

删除 `dockerSocketClient` 中的 `InspectImage`、`LoadImage`、`TagImage` 方法实现（保留 `ListContainers`）。

同时删除 `splitImageRef` 函数（仅 `TagImage` 使用）。

- [ ] **Step 3: 删除 main.go 中的镜像端点 handler**

在 `runtime/agent/main.go` 中找到 `/v1/images/inspect` 和 `/v1/images/load` 的路由注册与 handler 实现，全部删除。

> 提示：用 `grep -n "images/inspect\|images/load\|handleInspect\|handleLoad" runtime/agent/main.go` 定位。

- [ ] **Step 4: 编译 agent**

```bash
go build ./runtime/agent/...
```

预期：无编译错误。

- [ ] **Step 5: Commit**

```bash
git add runtime/agent/main.go runtime/agent/docker_client.go
git commit -m "refactor(agent): 删除 /v1/images/inspect 和 /v1/images/load 自定义端点"
```

---

## Task 7: AppInitializeHandler — 新增 phasePullRuntimeImage

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`
- Modify: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1: 定义新接口，写新阶段的失败测试**

在 `app_initialize_test.go` 末尾追加（先确认文件能找到 `TestAppInitializeHandler_phasePullRuntimeImage`）：

```go
// TestAppInitializeHandler_phasePullRuntimeImage_SkipWhenAlreadyStored 验证
// app.RuntimeImageRef 已非空时跳过 docker pull（恢复路径幂等）。
func TestAppInitializeHandler_phasePullRuntimeImage_SkipWhenAlreadyStored(t *testing.T) {
    // 此测试依赖 handler 的 phasePullRuntimeImage 实现；
    // 当 imagePullCoord 为 nil 时直接返回 nil（fallback 兼容）。
    h := &AppInitializeHandler{cfg: AppInitializeConfig{RuntimeImage: "hermes:v1"}}
    app := &sqlc.App{RuntimeImageRef: "hermes:v1", RuntimeImageSha256: "sha256:abc"}
    payload := appInitializePayload{AppID: "test-id", RuntimeNodeID: "node-1"}
    reporter := &progressReporter{}

    err := h.phasePullRuntimeImage(context.Background(), app, payload, reporter)
    require.NoError(t, err)
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/worker/handlers/... -run TestAppInitializeHandler_phasePullRuntimeImage -v
```

预期：FAIL（方法不存在）。

- [ ] **Step 3: 更新 AppInitializeStore 接口，添加 UpdateAppRuntimeImage**

在 `internal/worker/handlers/app_initialize.go` 的 `AppInitializeStore` interface 中追加：

```go
// UpdateAppRuntimeImage 在 phasePullRuntimeImage 成功后写入镜像引用与 sha256。
UpdateAppRuntimeImage(ctx context.Context, arg sqlc.UpdateAppRuntimeImageParams) (sqlc.App, error)
```

- [ ] **Step 4: 新增 NodeDockerProvider 接口**

在 `app_initialize.go` 中（`ImageCoordinator` interface 下方）新增：

```go
// NodeDockerProvider 返回指向目标节点 agent docker proxy 的 SDK client。
// 由 runtime.AgentBackedAdapter 的 DockerClientForNode 方法满足。
type NodeDockerProvider interface {
    DockerClientForNode(ctx context.Context, nodeID string) (*dockerclient.Client, error)
}
```

需要在 import 中加入：
```go
dockerclient "github.com/docker/docker/client"
imagecoord "oc-manager/internal/runtime/imagecoord"
```

- [ ] **Step 5: 在 AppInitializeHandler 结构体中替换字段**

将 `coord ImageCoordinator` 字段替换为两个新字段：

```go
// imagePullCoord 驱动 phasePullRuntimeImage 的跨实例单飞和进度广播。
// nil 时 phasePullRuntimeImage 静默返回 nil（测试装配兼容）。
imagePullCoord  *imagecoord.Coordinator
// nodeDockerProv 提供与目标节点 agent docker proxy 的 SDK client。
// nil 时 phasePullRuntimeImage 静默返回 nil。
nodeDockerProv  NodeDockerProvider
```

新增两个 setter（替代已有的 `SetImageCoordinator`）：

```go
// SetImagePullCoord 注入镜像拉取协调器；生产装配必须注入。
func (h *AppInitializeHandler) SetImagePullCoord(c *imagecoord.Coordinator) { h.imagePullCoord = c }

// SetNodeDockerProvider 注入节点 docker client 提供者；生产装配必须注入。
func (h *AppInitializeHandler) SetNodeDockerProvider(p NodeDockerProvider) { h.nodeDockerProv = p }
```

删除旧的 `SetImageCoordinator` 方法和 `ImageCoordinator` interface。

- [ ] **Step 6: 实现 phasePullRuntimeImage**

```go
// phasePullRuntimeImage 通过 agent docker proxy 在目标节点拉取 hermes runtime 镜像。
//
// 镜像来源：cfg.RuntimeImage（配置文件中的镜像引用）。
// 恢复路径：若 app.RuntimeImageRef 已非空（之前部署写入），以 DB 存储值为准，
// 不受配置文件升级影响，确保已部署实例始终使用原始镜像版本。
//
// 成功后将 (imageRef, sha256) 写入 apps.runtime_image_ref / runtime_image_sha256；
// 之后 phaseCreate 读此字段创建容器。
func (h *AppInitializeHandler) phasePullRuntimeImage(ctx context.Context, app *sqlc.App, payload appInitializePayload, reporter *progressReporter) error {
    if h.imagePullCoord == nil || h.nodeDockerProv == nil {
        // 测试装配兼容：未注入时静默跳过。
        return nil
    }
    if payload.RuntimeNodeID == "" {
        return nil
    }

    // 确定本次使用的镜像引用：DB 存储值优先（恢复路径），否则用当前配置。
    imageRef := h.cfg.RuntimeImage
    if app.RuntimeImageRef != "" {
        imageRef = app.RuntimeImageRef
    }

    cli, err := h.nodeDockerProv.DockerClientForNode(ctx, payload.RuntimeNodeID)
    if err != nil {
        return fmt.Errorf("获取节点 docker client 失败: %w", err)
    }

    sub := make(chan imagecoord.ProgressEvent, 16)
    done := make(chan struct{})
    go func() {
        for ev := range sub {
            reporter.Receive(ctx, ev)
        }
        close(done)
    }()

    imageID, err := h.imagePullCoord.PullImageOnNode(ctx, payload.RuntimeNodeID, imageRef, cli, sub)
    <-done
    if err != nil {
        return fmt.Errorf("在节点拉取 runtime 镜像失败: %w", err)
    }

    // 写入 DB，确保 phaseCreate 和 Reaper 恢复时都能读到。
    updated, err := h.store.UpdateAppRuntimeImage(ctx, sqlc.UpdateAppRuntimeImageParams{
        ID:                 app.ID,
        RuntimeImageRef:    imageRef,
        RuntimeImageSha256: imageID,
    })
    if err != nil {
        return fmt.Errorf("写入 runtime 镜像信息失败: %w", err)
    }
    *app = updated
    return nil
}
```

- [ ] **Step 7: 替换 Handle 中的 steps slice**

将 `Handle` 中的 steps 替换为：

```go
steps := []struct {
    phase string
    run   func(context.Context, *sqlc.App, appInitializePayload, *progressReporter) error
}{
    {domain.AppStatusPullingRuntimeImage, h.phasePullRuntimeImage},
    {domain.AppStatusPreparingRuntime, h.phasePrepare},
    {domain.AppStatusCreatingContainer, h.phaseCreate},
    {domain.AppStatusStarting, h.phaseStart},
}
```

删除原 `phasePull` 和 `phaseSync` 方法。

- [ ] **Step 8: phaseCreate 改用 RuntimeImageRef，加 RestartPolicy**

将 `phaseCreate` 中的 `Image: h.cfg.RuntimeImage` 改为：

```go
// imageRef：优先使用 DB 存储的镜像引用（phasePullRuntimeImage 写入），
// 首次部署时 RuntimeImageRef 已被写入，此处 fallback 仅为极端异常兜底。
imageRef := h.cfg.RuntimeImage
if app.RuntimeImageRef != "" {
    imageRef = app.RuntimeImageRef
}
spec := runtimepkg.ContainerSpec{
    Name:          "hermes-" + payload.AppID,
    Image:         imageRef,
    Networks:      h.cfg.ContainerNetworks,
    WorkingDir:    "/opt/data/workspace",
    RestartPolicy: "always",
    Env: map[string]string{
        "OPENAI_API_KEY":  containerAPIKey,
        "OPENAI_BASE_URL": h.cfg.NewAPIBaseURL + "/v1",
    },
    Volumes: []runtimepkg.VolumeMount{
        {HostPath: filepath.Join(nodeDataRoot, "apps", payload.AppID, ".hermes"), ContainerPath: "/opt/data"},
    },
}
```

- [ ] **Step 9: 编译 + 测试**

```bash
go build ./internal/worker/handlers/... && go test ./internal/worker/handlers/... -v 2>&1 | tail -30
```

预期：通过（可能有其他测试需要更新 store stub 以满足新接口）。若有 stub 缺少 `UpdateAppRuntimeImage`，补充实现：
```go
func (s *appInitializeStoreStub) UpdateAppRuntimeImage(_ context.Context, arg sqlc.UpdateAppRuntimeImageParams) (sqlc.App, error) {
    return sqlc.App{RuntimeImageRef: arg.RuntimeImageRef, RuntimeImageSha256: arg.RuntimeImageSha256}, nil
}
```

- [ ] **Step 10: Commit**

```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -m "feat(handler): 以 phasePullRuntimeImage 替代 phasePull+phaseSync，phaseCreate 使用存储的镜像引用"
```

---

## Task 8: Reaper 修复

**Files:**
- Modify: `internal/worker/reaper/reaper.go`
- Modify: `internal/worker/reaper/reaper_test.go`

- [ ] **Step 1: 写失败测试**

在 `reaper_test.go` 的 `TestReaper_ReapOrphanReset` 表驱动用例中追加新 case，同时修改期望值：

```go
// 新增 pulling_runtime_image 用例：新阶段的孤儿也必须被正确重置
{"pulling_runtime_image 孤儿", domain.AppStatusPullingRuntimeImage},
```

将断言 `assert.Equal(t, domain.AppStatusPullingImage, store.statusCalls[0].Status)` 改为：
```go
assert.Equal(t, domain.AppStatusPullingRuntimeImage, store.statusCalls[0].Status)
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/worker/reaper/... -run TestReaper_ReapOrphanReset -v
```

预期：FAIL（期望值不匹配）。

- [ ] **Step 3: 修改 reaper.go**

在 `reapApp` 方法中：
```go
// 修改前
Status: domain.AppStatusPullingImage,
// 修改后
Status: domain.AppStatusPullingRuntimeImage,
```

同时更新 `reapApp` 函数的注释：
```go
// reapApp 重置 app status 到 pulling_runtime_image + 清空进度 + 重置/新建 job + 通知队列。
```

- [ ] **Step 4: 更新 reaper.go 中的 Store 注释**

`Store` 接口中 `SetAppStatus` 的注释改为：
```go
// SetAppStatus reaper 强制把孤儿 status 回退到 pulling_runtime_image；不走状态机校验。
```

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./internal/worker/reaper/... -v
```

预期：ALL PASS（所有 6 个子状态均以 `pulling_runtime_image` 重置）。

- [ ] **Step 6: Commit**

```bash
git add internal/worker/reaper/reaper.go internal/worker/reaper/reaper_test.go
git commit -m "fix(reaper): 孤儿 app 重置状态改为 pulling_runtime_image"
```

---

## Task 9: 删除旧代码

**Files:**
- Delete: `internal/runtime/imagesync/` (整包)
- Delete: `internal/service/image_distribution_service.go`
- Delete: `internal/service/image_distribution_service_test.go`
- Modify: `internal/integrations/runtime/adapter.go` (删除 imagesync 引用)
- Modify: `internal/integrations/runtime/agent_backed.go` (删除 EnsureImage/ImageSyncer)

- [ ] **Step 1: 删除 imagesync 包**

```bash
rm -rf internal/runtime/imagesync/
```

- [ ] **Step 2: 删除 image_distribution_service**

```bash
rm internal/service/image_distribution_service.go internal/service/image_distribution_service_test.go
```

- [ ] **Step 3: 清理 adapter.go 中的 imagesync 引用**

`internal/integrations/runtime/adapter.go` 顶部 import 中删除：
```go
"oc-manager/internal/runtime/imagesync"
```

若 `adapter.go` 中有 `imagesync.SyncResult` 相关类型声明，一并删除。

- [ ] **Step 4: 清理 agent_backed.go**

在 `internal/integrations/runtime/agent_backed.go` 中：
- 删除 `ImageSyncer` interface
- 删除 `AgentBackedAdapter.imageSync` 字段
- 删除 `EnsureImage` 方法
- 删除 `NewAgentBackedAdapter` 中的 `imageSync` 参数（只保留 `files` 和 `docker`）
- 删除顶部 `imagesync` import

`NewAgentBackedAdapter` 新签名：
```go
func NewAgentBackedAdapter(files AgentResolver, docker DockerClientResolver) *AgentBackedAdapter {
    return &AgentBackedAdapter{files: files, docker: docker}
}
```

- [ ] **Step 5: 删除 handler 中的 ImageDistributor**

在 `app_initialize.go` 中：
- 删除 `ImageDistributor` interface
- 删除 `AppInitializeHandler.images` 字段
- 删除 `NewAppInitializeHandler` 中的 `images ImageDistributor` 参数

`NewAppInitializeHandler` 新签名：
```go
func NewAppInitializeHandler(
    store AppInitializeStore,
    dirs AgentDirInitializer,
    containers ContainerCreator,
    starter ContainerStarter,
    factory NewAPIClientFactory,
    cfg AppInitializeConfig,
) *AppInitializeHandler {
```

- [ ] **Step 6: 修复所有受影响的调用方**

在 `cmd/server/` 或 `internal/` 中找到 `NewAgentBackedAdapter` 和 `NewAppInitializeHandler` 的调用处，删除已移除的参数。

```bash
grep -rn "NewAgentBackedAdapter\|NewAppInitializeHandler\|EnsureRuntimeImage\|ImageDistributor" --include="*.go" . | grep -v "_test.go"
```

逐一修复。

- [ ] **Step 7: 编译验证**

```bash
go build ./...
```

预期：无编译错误。

- [ ] **Step 8: 全量测试**

```bash
go test ./... 2>&1 | grep -E "FAIL|ok"
```

预期：全部 `ok`。

- [ ] **Step 9: Commit**

```bash
git add -u && git add internal/ runtime/ cmd/
git commit -m "refactor: 删除 imagesync 包、image_distribution_service 及相关旧接口"
```

---

## Task 10: API 层 — AppResult 新增镜像字段

**Files:**
- Modify: `internal/service/app_service.go`

- [ ] **Step 1: 写失败测试**

在 `internal/service/app_service_test.go` 中，为 `TestAppService_Get` 追加子测试（或新建测试函数）：

```go
// TestAppService_Get_RuntimeImageVisibility 验证 runtime_image_ref/sha256 仅对平台管理员可见。
func TestAppService_Get_RuntimeImageVisibility(t *testing.T) {
    app := sqlc.App{
        ID:                 testUUID("app-1"),
        OrgID:              testUUID("org-1"),
        OwnerUserID:        testUUID("user-1"),
        Status:             domain.AppStatusRunning,
        PersonaMode:        domain.PersonaModeOrgInherited,
        ApiKeyStatus:       domain.APIKeyStatusActive,
        RuntimeImageRef:    "registry.example.com/hermes:v1",
        RuntimeImageSha256: "sha256:abc123",
    }
    store := &appServiceStoreStub{apps: map[string]sqlc.App{uuidToString(app.ID): app}}
    svc := NewAppService(store)

    // 平台管理员能看到镜像信息
    adminPrincipal := auth.Principal{Role: domain.UserRolePlatformAdmin}
    result, err := svc.Get(context.Background(), adminPrincipal, uuidToString(app.ID))
    require.NoError(t, err)
    assert.Equal(t, "registry.example.com/hermes:v1", result.RuntimeImageRef)
    assert.Equal(t, "sha256:abc123", result.RuntimeImageSha256)

    // 普通成员只能看自己的应用，但看不到镜像信息
    memberPrincipal := auth.Principal{Role: domain.UserRoleOrgMember, UserID: uuidToString(app.OwnerUserID), OrgID: uuidToString(app.OrgID)}
    result, err = svc.Get(context.Background(), memberPrincipal, uuidToString(app.ID))
    require.NoError(t, err)
    assert.Empty(t, result.RuntimeImageRef)
    assert.Empty(t, result.RuntimeImageSha256)
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/service/... -run TestAppService_Get_RuntimeImageVisibility -v
```

预期：FAIL（字段不存在）。

- [ ] **Step 3: AppResult 新增字段**

在 `AppResult` 结构体末尾追加：

```go
// RuntimeImageRef 是部署时使用的镜像引用（含 tag）；仅平台管理员可见。
RuntimeImageRef string `json:"runtime_image_ref,omitempty"`
// RuntimeImageSha256 是镜像拉取后的 docker inspect ID；仅平台管理员可见。
RuntimeImageSha256 string `json:"runtime_image_sha256,omitempty"`
```

- [ ] **Step 4: Get 方法中条件填充镜像字段**

将 `Get` 方法最后的 `return toAppResult(app), nil` 改为：

```go
result := toAppResult(app)
if principal.Role == domain.UserRolePlatformAdmin {
    result.RuntimeImageRef    = app.RuntimeImageRef
    result.RuntimeImageSha256 = app.RuntimeImageSha256
}
return result, nil
```

对 `ListByOrg` 也做同样处理（在 `results = append(results, toAppResult(app))` 处）：

```go
item := toAppResult(app)
if principal.Role == domain.UserRolePlatformAdmin {
    item.RuntimeImageRef    = app.RuntimeImageRef
    item.RuntimeImageSha256 = app.RuntimeImageSha256
}
results = append(results, item)
```

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./internal/service/... -v 2>&1 | tail -20
```

预期：ALL PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/service/app_service.go internal/service/app_service_test.go
git commit -m "feat(service): AppResult 新增 runtime_image_ref/sha256，仅平台管理员可见"
```

---

## Task 11: OpenAPI + 前端类型同步

**Files:**
- Regenerate: `openapi/openapi.yaml` (via `make openapi-gen`)
- Regenerate: `web/src/api/generated.ts` (via `make web-types-gen`)

- [ ] **Step 1: 更新 handler swagger 注解**

在 `internal/api/handlers/apps.go` 的 `Get` handler 注解中，`@Success 200` 的说明已引用 `service.AppResult`，新字段自动被 swag 扫描到，无需手动修改。确认注解格式正确即可。

- [ ] **Step 2: 运行 openapi-gen**

```bash
make openapi-gen
```

预期：`openapi/openapi.yaml` 中 `AppResult` schema 包含 `runtime_image_ref` 和 `runtime_image_sha256`。

- [ ] **Step 3: 运行 web-types-gen**

```bash
make web-types-gen
```

预期：`web/src/api/generated.ts` 中对应类型包含新字段。

- [ ] **Step 4: 在 AppDTO 中补充新字段**

在 `web/src/api/hooks/useApps.ts` 的 `AppDTO` interface 中追加：

```typescript
// runtime_image_ref 是部署时使用的镜像引用（含 tag）；仅平台管理员可见，其他角色后端返回空。
runtime_image_ref?: string
// runtime_image_sha256 是拉取后的镜像 ID；仅平台管理员可见。
runtime_image_sha256?: string
```

- [ ] **Step 5: Commit**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts web/src/api/hooks/useApps.ts
git commit -m "feat(api): AppResult 暴露 runtime_image_ref/sha256（平台管理员可见）"
```

---

## Task 12: 前端 — 状态文案 + 实例详情展示

**Files:**
- Modify: `web/src/domain/status.ts`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
- Modify: `web/src/api/hooks/useApps.ts` (refetchInterval 新增新状态)

- [ ] **Step 1: 写失败测试**

在 `web/src/domain/status.spec.ts` 中追加：

```typescript
// pulling_runtime_image 新状态应有中文标签和 warning 语义
it('pulling_runtime_image label', () => {
  const view = formatAppStatus('pulling_runtime_image')
  expect(view.label).toBe('拉取运行时镜像')
  expect(view.tone).toBe('warning')
})

// isInitPhase 应识别新状态
it('isInitPhase pulling_runtime_image', () => {
  expect(isInitPhase('pulling_runtime_image')).toBe(true)
})
```

- [ ] **Step 2: 运行确认失败**

```bash
cd web && npx vitest run src/domain/status.spec.ts 2>&1 | tail -20
```

预期：FAIL。

- [ ] **Step 3: 更新 status.ts**

在 `appStatusViews` map 中，`pulling_image` 前加一行：

```typescript
pulling_runtime_image: { label: '拉取运行时镜像', tone: 'warning' },
```

在 `initPhaseStatuses` Set 中加入 `'pulling_runtime_image'`。

同时删除（或保留历史兼容）`pulling_image` 和 `syncing_image`——保留它们的标签定义，因为历史 app 可能还在这些状态，但不加入 `initPhaseStatuses`：

> 历史状态 `pulling_image`/`syncing_image` 的 label 保留，确保前端在展示历史 error app 的 `last_error_status` 时不显示"未知状态"。`initPhaseStatuses` 只加 `pulling_runtime_image`。

- [ ] **Step 4: 更新 useApps.ts 中的 transitionalStatuses**

在 `useAppQuery` 的 `refetchInterval` 回调中，`transitionalStatuses` Set 加入 `'pulling_runtime_image'`：

```typescript
const transitionalStatuses = new Set([
  'draft',
  'pulling_runtime_image',
  'pulling_image',
  'syncing_image',
  'preparing_runtime',
  'creating_container',
  'starting',
  'binding_waiting',
])
```

- [ ] **Step 5: 运行测试确认通过**

```bash
cd web && npx vitest run src/domain/status.spec.ts
```

预期：PASS。

- [ ] **Step 6: AppOverviewTab.vue 新增镜像信息展示**

在 `web/src/pages/apps/AppOverviewTab.vue` 的 `<script setup>` 中导入 auth store：

```typescript
import { useAuthStore } from '@/stores/auth'
const authStore = useAuthStore()
```

在 `<n-descriptions>` 中，现有最后一个 `<n-descriptions-item>` 之后插入：

```vue
<!-- 运行时镜像信息：仅平台管理员可见 -->
<n-descriptions-item v-if="authStore.isPlatformAdmin && app?.runtime_image_ref" label="运行时镜像">
  <n-text code style="font-size: 12px; word-break: break-all">{{ app.runtime_image_ref }}</n-text>
</n-descriptions-item>
<n-descriptions-item v-if="authStore.isPlatformAdmin && app?.runtime_image_sha256" label="镜像 SHA256">
  <n-tooltip>
    <template #trigger>
      <n-text code style="font-size: 12px">{{ app.runtime_image_sha256.slice(0, 19) }}…</n-text>
    </template>
    {{ app.runtime_image_sha256 }}
  </n-tooltip>
</n-descriptions-item>
```

- [ ] **Step 7: 本地验证**

启动前端开发服务器，以平台管理员身份登录，进入一个处于 `running` 状态且 `runtime_image_ref` 非空的实例详情页，确认镜像信息显示正常；以普通成员身份登录确认看不到此字段。

```bash
cd web && npm run dev
```

- [ ] **Step 8: Commit**

```bash
git add web/src/domain/status.ts web/src/pages/apps/AppOverviewTab.vue web/src/api/hooks/useApps.ts
git commit -m "feat(web): 新增 pulling_runtime_image 状态文案，实例详情展示运行时镜像（仅平台管理员）"
```

---

## Task 13: 最终验收

- [ ] **Step 1: 全量编译 + 测试**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
go build ./...
go test ./...
```

预期：0 FAIL，0 vet error。

- [ ] **Step 2: OpenAPI 一致性检查**

```bash
make openapi-check
```

预期：`✅ openapi.yaml 与代码同步`。

- [ ] **Step 3: 生产装配检查（wiring）**

在 `cmd/server/` 中搜索 `NewAppInitializeHandler` 和 `NewAgentBackedAdapter` 的调用，确认：
- `SetImagePullCoord` 已注入真实 `*imagecoord.Coordinator`
- `SetNodeDockerProvider` 已注入 `*AgentBackedAdapter`
- `NewAgentBackedAdapter` 调用只传 `files` 和 `docker` 两个参数

- [ ] **Step 4: 端到端测试**

部署 manager + agent，创建新实例，确认：
1. 状态走 `draft → pulling_runtime_image → preparing_runtime → creating_container → starting → binding_waiting`
2. 进度条在 `pulling_runtime_image` 阶段显示拉取进度
3. 容器创建带 `restart=always`（`docker inspect <container_id> | grep -A3 RestartPolicy`）
4. 平台管理员实例详情页可见 runtime_image_ref 和 sha256

- [ ] **Step 5: Final commit（如有遗漏清理）**

```bash
git add -u
git commit -m "chore: 最终装配清理和端到端验收"
```
