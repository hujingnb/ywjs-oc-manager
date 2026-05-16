package imagecoord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/google/uuid"

	ocredis "oc-manager/internal/redis"
)

// ProgressEvent 是 leader 广播给所有 subscriber 的进度。
// 与 redis.ProgressEvent 同形态；此处单独导出便于上层 import 不直接依赖 redis 包。
type ProgressEvent = ocredis.ProgressEvent

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

// 与超时相关的常量集中放在这里，方便调参与测试快速覆盖。
const (
	// defaultLockTTL 是镜像 pull 锁的初始过期时间；watchdog 会周期续期。
	defaultLockTTL = 5 * time.Minute
	// watchdogInterval 是 leader 锁续期周期。
	watchdogInterval = 90 * time.Second
	// followerWaitGrace 是 follower 等 leader 完成的额外宽限。
	followerWaitGrace = 30 * time.Second
	// progressTickInterval 是 leader 周期 send 聚合进度的节流间隔。
	progressTickInterval = time.Second
)

// ErrLeaderLost 表示 follower 等待 leader 超时。上层 worker 应让 job 失败重试。
var ErrLeaderLost = errors.New("imagecoord: leader timed out, please retry")

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
//  1. ImageInspectWithRaw 预检：tag 已存在则直接返回 sha256（tag 不可变，存在即内容一致）。
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
	// FeedReader 阻塞直到 NDJSON 流 EOF；放独立 goroutine，主循环 select 节流。
	doneCh := make(chan error, 1)
	go func() { doneCh <- agg.FeedReader(rc) }()

	for {
		select {
		case <-ticker.C:
			cur, tot := agg.Snapshot()
			send(ProgressEvent{Phase: "pulling_runtime_image", Current: cur, Total: tot})
		case err := <-doneCh:
			// 收尾 snapshot：即使中间一次 tick 没赶上，follower 也能看到最终值。
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

// runLeader 是 PullImageOnNode leader 流程的共享实现。
// 负责启 watchdog 续期、注册本地 fanout、调 op、广播 PhaseDone、释放锁。
func (c *Coordinator) runLeader(
	ctx context.Context,
	channel string,
	subscriber chan<- ProgressEvent,
	lockKey, token string,
	op func(ctx context.Context, send func(ProgressEvent)) error,
) error {
	// 用独立 watchCtx 控制 watchdog；续期连续失败时 cancel，逼 op 一起退出。
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go c.watchdog(watchCtx, lockKey, token, cancelWatch)

	// 本机 subscriber 注册进 fanout map，leader 自己 send 的事件能镜像回自己。
	c.registerSubscriber(channel, subscriber)
	defer c.unregisterSubscriber(channel, subscriber)

	send := func(ev ProgressEvent) {
		// 同进程 fanout：跳过 redis 直接送本机 subscriber（含自己）。
		c.fanout(channel, ev)
		// 跨进程广播：其他 manager 的 follower 通过 redis Pub/Sub 接收。
		_ = c.bus.Publish(ctx, channel, ev)
	}

	opErr := op(watchCtx, send)

	// PhaseDone 必须广播，把 op 的 err 嵌入事件让 follower 退出 wait。
	// 用 background ctx，避免上游 ctx 被取消时 follower 收不到完成信号。
	_ = c.bus.PublishDone(context.Background(), channel, opErr)

	cancelWatch()
	_ = c.locker.Release(context.Background(), lockKey, token)
	return opErr
}

// runFollower 等待 leader 把镜像准备就绪。
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
		// leader 已收尾，直接返回让上层 inspect 判断结果。
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

// closeIfOpen 关闭 subscriber chan，double-close 触发的 panic 用 recover 兜底。
func closeIfOpen(ch chan<- ProgressEvent) {
	defer func() { _ = recover() }()
	close(ch)
}
