package imagecoord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	ocredis "oc-manager/internal/redis"
)

// Coordinator 串联本地 docker 能力 + agent 节点能力 + Redis 分布式锁 + Pub/Sub 总线,
// 对外暴露 PullImage / SyncToNode 两个跨 manager 实例安全的入口。
//
// 设计目标:同一 image(或 image+nodeID)在集群范围内只跑一次实际 docker 操作,
// 其他抢锁失败的实例作为 follower 复用 leader 的进度事件,并在 leader 失败时
// 把错误冒泡给自己的 subscriber,由上层 worker 决定 job 重试。
//
// 与 Postgres 的关系:apps.status / progress_* 是事实来源;本协调器只是
// 让"谁在跑、跑到哪一步"通过 Redis 复用。Redis 失联最多导致重复 pull
// 或进度短暂不更新,不影响业务正确性。
type Coordinator struct {
	local      LocalImageProvider
	agent      AgentImageClient
	locker     ocredis.DistLocker
	bus        ocredis.ProgressBus
	instanceID string

	// 同进程 fanout:本机 leader 在 send 时既调用 bus.Publish 跨进程广播,
	// 也通过 subscribers map 把事件直接镜像给同进程内的所有 subscriber,
	// 省掉 redis publish -> 自己 subscribe 的一次往返。
	// key=channel(锁/总线 channel 同名后缀)。
	mu          sync.Mutex
	subscribers map[string][]chan<- ProgressEvent
}

// 与超时相关的常量集中放在这里,方便调参与测试快速覆盖。
const (
	// defaultLockTTL 是镜像 pull / sync 锁的初始过期时间。
	// 取 5 分钟覆盖绝大多数 pull/sync 场景;watchdog 会周期续期,真正运行可以更久。
	defaultLockTTL = 5 * time.Minute
	// watchdogInterval 是 leader 锁续期周期。
	// 取 TTL/3 量级,既留有续期失败的重试余量,又不至于高频 redis 调用。
	watchdogInterval = 90 * time.Second
	// followerWaitGrace 是 follower 等 leader 完成的额外宽限。
	// leader 锁 TTL 内未广播完成且未续期成功,follower 会主动放弃。
	followerWaitGrace = 30 * time.Second
	// progressTickInterval 是 leader 周期 send 聚合进度的节流间隔。
	// 1s/次:既覆盖前端可感知粒度,也避免对 redis 产生高 QPS。
	progressTickInterval = time.Second
)

// 错误变量集中定义,便于上层 errors.Is 比对。
var (
	// ErrLeaderLost 表示 follower 等待 leader 完成超时。
	// 触发场景:leader 进程崩溃且锁过期未被广播 done,或 redis 抖动导致事件丢失。
	// 上层 worker 应让 job 失败重试,新一轮会重新抢锁。
	ErrLeaderLost = errors.New("imagecoord: leader timed out, please retry")
)

// NewCoordinator 创建实例。
//
// instanceID 建议使用 manager 进程启动时生成的 UUID,使锁 token 携带进程身份,
// 防止跨实例误释放锁。
func NewCoordinator(local LocalImageProvider, agent AgentImageClient, locker ocredis.DistLocker, bus ocredis.ProgressBus, instanceID string) *Coordinator {
	return &Coordinator{
		local:       local,
		agent:       agent,
		locker:      locker,
		bus:         bus,
		instanceID:  instanceID,
		subscribers: map[string][]chan<- ProgressEvent{},
	}
}

// PullImage 在 manager 本地拉取 image,跨实例 single-flight。
//
// 流程(对应 spec §5.3):
//  1. 本地已存在则直接 close subscriber 返回 nil,不走 redis。
//  2. TryAcquire pull 锁;抢到则 leader,否则 follower。
//  3. leader:启 watchdog 续期 -> 跑 doPull -> publish 进度 -> publish PhaseDone -> 释放锁。
//  4. follower:Subscribe channel -> 二次 Exists 锁(规避 Pub/Sub 先发后订)
//     -> 消费事件直到 PhaseDone 或超时。
//
// subscriber 用于把进度事件透传给调用方(handler/SSE);函数返回前必然被 close。
func (c *Coordinator) PullImage(ctx context.Context, image string, subscriber chan<- ProgressEvent) error {
	defer closeIfOpen(subscriber)

	// 本机镜像已就绪:不走任何 redis 交互。
	if _, err := c.local.ImageID(ctx, image); err == nil {
		return nil
	}

	lockKey := fmt.Sprintf("ocm:image:pull:lock:%s", image)
	channel := fmt.Sprintf("ocm:image:pull:bus:%s", image)
	token := c.instanceID + ":" + uuid.NewString()

	got, err := c.locker.TryAcquire(ctx, lockKey, token, defaultLockTTL)
	if err != nil {
		return fmt.Errorf("抢镜像 pull 锁: %w", err)
	}
	if got {
		return c.runLeader(ctx, channel, subscriber, lockKey, token, func(ctx context.Context, send func(ProgressEvent)) error {
			return c.doPull(ctx, image, send)
		})
	}
	return c.runFollower(ctx, channel, lockKey, subscriber)
}

// SyncToNode 把 manager 本地 image 同步到指定 agent 节点,跨实例按 (image, nodeID) single-flight。
//
// 流程与 PullImage 同形态,差异:
//   - 锁与 channel 带 nodeID,允许同一 image 并发同步到不同节点。
//   - 远端已是同 ID 时直接跳过(沿用 imagesync 旧行为)。
func (c *Coordinator) SyncToNode(ctx context.Context, image, nodeID string, subscriber chan<- ProgressEvent) error {
	defer closeIfOpen(subscriber)
	if c.agent == nil {
		return fmt.Errorf("imagecoord: agent client 未配置")
	}

	// 远端已就绪:直接跳过,既不抢锁也不广播。
	localID, err := c.local.ImageID(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect local image: %w", err)
	}
	remote, err := c.agent.InspectImage(ctx, nodeID, image)
	if err != nil {
		return fmt.Errorf("inspect remote image: %w", err)
	}
	if remote.Exists && remote.ID == localID {
		return nil
	}

	lockKey := fmt.Sprintf("ocm:image:sync:lock:%s:%s", nodeID, image)
	channel := fmt.Sprintf("ocm:image:sync:bus:%s:%s", nodeID, image)
	token := c.instanceID + ":" + uuid.NewString()

	got, err := c.locker.TryAcquire(ctx, lockKey, token, defaultLockTTL)
	if err != nil {
		return fmt.Errorf("抢镜像 sync 锁: %w", err)
	}
	if got {
		return c.runLeader(ctx, channel, subscriber, lockKey, token, func(ctx context.Context, send func(ProgressEvent)) error {
			return c.doSync(ctx, image, nodeID, localID, send)
		})
	}
	return c.runFollower(ctx, channel, lockKey, subscriber)
}

// runLeader 是 PullImage / SyncToNode 共享的 leader 流程。
// 负责启 watchdog 续期、注册本地 fanout、调 op、广播 PhaseDone、释放锁。
//
// op 内部通过 send 回调上报进度;send 同时把事件 fanout 给同进程 subscriber
// 与 Publish 到 redis 给其他 manager。
func (c *Coordinator) runLeader(
	ctx context.Context,
	channel string,
	subscriber chan<- ProgressEvent,
	lockKey, token string,
	op func(ctx context.Context, send func(ProgressEvent)) error,
) error {
	// 用独立 watchCtx 控制 watchdog;续期连续失败时 cancel,逼 op 一起退出。
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go c.watchdog(watchCtx, lockKey, token, cancelWatch)

	// 本机 subscriber 注册进 fanout map,leader 自己 send 的事件能镜像回自己。
	c.registerSubscriber(channel, subscriber)
	defer c.unregisterSubscriber(channel, subscriber)

	send := func(ev ProgressEvent) {
		// 同进程 fanout:跳过 redis 直接送本机 subscriber(含自己)。
		c.fanout(channel, ev)
		// 跨进程广播:其他 manager 的 follower 通过 redis Pub/Sub 接收。
		// 失败不致命,follower 端可以靠后续事件或最终的 PhaseDone 收敛。
		_ = c.bus.Publish(ctx, channel, ev)
	}

	opErr := op(watchCtx, send)

	// PhaseDone 必须广播,把 op 的 err 嵌入事件让 follower 退出 wait。
	// 用 background ctx,避免上游 ctx 被取消时 follower 收不到完成信号。
	_ = c.bus.PublishDone(context.Background(), channel, opErr)

	cancelWatch()
	_ = c.locker.Release(context.Background(), lockKey, token)
	return opErr
}

// runFollower 等待 leader 把 image 准备就绪。
//
// 关键正确性:Subscribe 是 Pub/Sub,无消息持久化;leader 可能在 follower
// 调用 Subscribe 之前就已 publish 完 PhaseDone。这里在订阅成功之后再做
// 一次 EXISTS 锁:如果锁已不存在,说明 leader 已结束,本机直接返回 nil,
// 由上层 worker 在下个阶段重新 inspect / 决定是否重试。
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
		// leader 已收尾。本机不知道 leader 是成功还是失败,但 worker handler
		// 在 PullImage / SyncToNode 返回后通常会再做一次 inspect:
		//   - 若镜像已就绪 -> 继续后续阶段;
		//   - 若仍缺 -> job 失败重试,下一轮自然会重新抢锁。
		return nil
	}

	deadline := time.NewTimer(defaultLockTTL + followerWaitGrace)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				// 订阅链路被关闭(redis 断连或上游 cancel),按 leader 失联处理。
				return ErrLeaderLost
			}
			if msg.Event.Phase == ocredis.PhaseDone {
				// leader 已完成:msg.Err 已由 bus 把 ErrMessage 反序列化为 error。
				return msg.Err
			}
			// 非 done 事件透传给本机 subscriber。
			// 满了直接丢弃:进度类事件丢一条无关紧要,首要避免阻塞 redis 接收 goroutine。
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

// doPull 调 docker daemon 拉取 image,解析 NDJSON 进度走 PullAggregator,
// 节流为 1s 一次 send。
//
// 完成时再 send 一次最终聚合值,确保 follower 看到一个收尾事件,
// 然后才返回(返回后由 runLeader 广播 PhaseDone)。
func (c *Coordinator) doPull(ctx context.Context, image string, send func(ProgressEvent)) error {
	rc, err := c.local.Pull(ctx, image)
	if err != nil {
		return fmt.Errorf("docker pull: %w", err)
	}
	defer rc.Close()

	agg := NewPullAggregator()
	ticker := time.NewTicker(progressTickInterval)
	defer ticker.Stop()
	// FeedReader 阻塞直到 NDJSON 流 EOF;放独立 goroutine,主循环 select 节流。
	doneCh := make(chan error, 1)
	go func() { doneCh <- agg.FeedReader(rc) }()

	for {
		select {
		case <-ticker.C:
			cur, tot := agg.Snapshot()
			send(ProgressEvent{Phase: "pulling_image", Current: cur, Total: tot})
		case err := <-doneCh:
			// 收尾 snapshot:即使中间一次 tick 没赶上,follower 也能看到最终值。
			cur, tot := agg.Snapshot()
			send(ProgressEvent{Phase: "pulling_image", Current: cur, Total: tot})
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// doSync 把 manager 本地 image archive 上传到 agent 节点。
//
// 上传字节通过 countingReader 上报为 syncing_image 阶段进度。
// total 当前固定为 0(未知,前端展示不定进度):LocalImageProvider 现仅暴露
// ImageID,后续若需要精确百分比,可扩展接口加 ImageSize。
//
// LoadImage 完成后做一次 ID 校验,确保 agent 端落地的镜像就是 manager 这边的。
func (c *Coordinator) doSync(ctx context.Context, image, nodeID, localID string, send func(ProgressEvent)) error {
	archive, err := c.local.Archive(ctx, image)
	if err != nil {
		return fmt.Errorf("archive local image: %w", err)
	}
	defer archive.Close()

	const total int64 = 0
	counting := newCountingReader(archive, func(n int64) {
		send(ProgressEvent{Phase: "syncing_image", Current: n, Total: total})
	})
	loaded, err := c.agent.LoadImage(ctx, nodeID, image, localID, counting)
	if err != nil {
		return fmt.Errorf("agent load image: %w", err)
	}
	if loaded.ID != "" && loaded.ID != localID {
		// 远端落地的 image ID 与本地不一致:通常意味着 tag 复用了同名但内容不同,
		// 直接失败避免后续 container 用错镜像。
		return fmt.Errorf("remote image id mismatch after load: local=%s remote=%s", localID, loaded.ID)
	}
	return nil
}

// watchdog 周期为 leader 锁续期。
//
// 连续 3 次续期失败即主动 cancel 上层 ctx,leader 自我放弃锁让其他实例
// 在锁 TTL 过期后接管。续期成功会重置失败计数。
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

// registerSubscriber 把本机 subscriber 加入 fanout 列表。
func (c *Coordinator) registerSubscriber(channel string, sub chan<- ProgressEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribers[channel] = append(c.subscribers[channel], sub)
}

// unregisterSubscriber 从 fanout 列表移除本机 subscriber。
// 用 chan 指针相等比较,定位到首个匹配即移除。
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

// fanout 把事件镜像给同进程所有 subscriber。
// 满了直接丢弃,避免阻塞 leader 主流程。
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

// closeIfOpen 关闭 subscriber chan,double-close 触发的 panic 用 recover 兜底。
// 适用场景:PullImage / SyncToNode 多个 defer 路径都会走到 close,
// 上层调用方一般不会再自行 close,但即使发生也不影响主流程。
func closeIfOpen(ch chan<- ProgressEvent) {
	defer func() { _ = recover() }()
	close(ch)
}
