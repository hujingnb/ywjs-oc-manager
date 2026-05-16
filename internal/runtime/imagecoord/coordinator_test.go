package imagecoord

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocredis "oc-manager/internal/redis"
)

// fakeLocalProvider 实现 LocalImageProvider:
//   - imageExists 控制 ImageID 是否返回成功,模拟"本地是否已就绪"。
//   - pullCalls 用于断言 single-flight:并发场景应只 +1。
//   - pullDelay / pullErr 控制 Pull 行为,模拟真实拉取耗时与失败路径。
type fakeLocalProvider struct {
	mu          sync.Mutex
	imageExists bool
	pullCalls   int32
	pullDelay   time.Duration
	pullBody    string
	pullErr     error
}

// ImageID 模拟 docker inspect:imageExists=true 返回固定 ID,否则报 not found。
func (f *fakeLocalProvider) ImageID(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.imageExists {
		return "sha256:exists", nil
	}
	return "", errors.New("not found")
}

// Archive 模拟 docker save:返回固定字节流,这里测试不依赖具体内容。
func (f *fakeLocalProvider) Archive(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("tar")), nil
}

// Pull 模拟 docker pull:延迟 pullDelay 后(模拟下载耗时),把 imageExists 置真,
// 返回一段 NDJSON 给 PullAggregator 解析。pullErr 非空时直接报错走失败路径。
func (f *fakeLocalProvider) Pull(_ context.Context, _ string) (io.ReadCloser, error) {
	atomic.AddInt32(&f.pullCalls, 1)
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	time.Sleep(f.pullDelay)
	f.mu.Lock()
	f.imageExists = true
	f.mu.Unlock()
	body := f.pullBody
	if body == "" {
		body = `{"id":"a","status":"Pull complete"}` + "\n"
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// newTestCoord 构造一个连接本地 redis 的 Coordinator;redis 不可用即 Skip。
// 每次返回 cleanup 清掉本次写入的 key,避免测试间相互污染。
//
// 选用 DB 12 与 internal/redis 包测试(DB 11)隔离:两包测试 cleanup 都
// 走 FlushDB,go test 默认按包并行时同 DB 会互相清掉对方测试中的 key,
// 导致 SingleFlight 锁被清、Renew 期间 key 失踪等间歇失败。
func newTestCoord(t *testing.T, prov LocalImageProvider) (*Coordinator, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "123456", DB: 12})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用,跳过 Coordinator 集成测试: " + err.Error())
	}
	bus := ocredis.NewRedisProgressBus(client)
	locker := ocredis.NewRedisDistLocker(client)
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	c := NewCoordinator(prov, nil, locker, bus, "test-instance")
	return c, cleanup
}

// TestCoordinator_PullImage_AlreadyPresent 覆盖"本地已存在直接返回"路径:
// 既不应触发 docker pull,也不应抢锁,subscriber 必须被 close。
func TestCoordinator_PullImage_AlreadyPresent(t *testing.T) {
	// 业务场景:首次 worker 已把镜像拉好,后续 worker 调 PullImage 应零开销。
	prov := &fakeLocalProvider{imageExists: true}
	c, cleanup := newTestCoord(t, prov)
	defer cleanup()

	sub := make(chan ProgressEvent, 4)
	require.NoError(t, c.PullImage(context.Background(), "x:1", sub))
	assert.EqualValues(t, 0, atomic.LoadInt32(&prov.pullCalls), "本地已就绪不应再触发 Pull")
	// subscriber 已被 close:再读会拿到零值 + ok=false。
	_, ok := <-sub
	assert.False(t, ok, "subscriber 应在返回前被关闭")
}

// TestCoordinator_PullImage_SingleFlight 覆盖并发跨实例合并语义:
// 两个并发 PullImage 应只触发一次 docker pull(leader 拉,follower 等)。
func TestCoordinator_PullImage_SingleFlight(t *testing.T) {
	// 业务场景:同一组织同时部署两个 app 用同一镜像,Pull 应合并。
	prov := &fakeLocalProvider{pullDelay: 300 * time.Millisecond}
	c, cleanup := newTestCoord(t, prov)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := make(chan ProgressEvent, 16)
			// 即便是 follower 路径,也应该返回 nil(leader 成功完成)。
			assert.NoError(t, c.PullImage(context.Background(), "x:1", sub))
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, atomic.LoadInt32(&prov.pullCalls), "并发 PullImage 应只触发一次 docker pull")
}

// TestCoordinator_PullImage_LeaderFailureBubblesToFollower 覆盖失败冒泡路径:
// leader pull 失败,自身返回该错误;follower 收到 PhaseDone(带 ErrMessage)
// 后,自身返回的 error 应包含同样语义。
func TestCoordinator_PullImage_LeaderFailureBubblesToFollower(t *testing.T) {
	// 业务场景:镜像仓库不可达,集群里所有 pending app 都应在同一轮失败,
	// 而不是 leader 失败 follower 误以为成功继续后续阶段。
	prov := &fakeLocalProvider{pullErr: errors.New("registry unreachable"), pullDelay: 100 * time.Millisecond}
	c, cleanup := newTestCoord(t, prov)
	defer cleanup()

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			sub := make(chan ProgressEvent, 16)
			errCh <- c.PullImage(context.Background(), "x:1", sub)
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			require.Error(t, err, "leader 失败,leader/follower 都应返回 error")
			assert.Contains(t, err.Error(), "registry unreachable")
		case <-time.After(3 * time.Second):
			t.Fatal("PullImage 未在 3s 内返回,可能 follower 没收到 PhaseDone")
		}
	}
}
