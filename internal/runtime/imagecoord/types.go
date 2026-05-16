// Package imagecoord 跨 manager 实例协调镜像 pull / sync。
//
// 协调粒度:
//   - 同一 image 的 pull 在整个集群内合并为一次,subscriber 收到广播事件;
//   - 同一 (image, nodeID) 的 sync 在整个集群内合并为一次;不同节点可并发。
//
// 与 Postgres 关系:Postgres(apps.status / progress_*) 是事实来源,
// 本包只是把"谁在跑、跑到哪一步"的信号通过 Redis 锁 + Pub/Sub 复用,
// Redis 失联最多导致重复 pull / 进度短暂不更新,不影响业务正确性。
package imagecoord

import (
	"context"
	"io"

	ocredis "oc-manager/internal/redis"
)

// ProgressEvent 是 leader 广播给所有 subscriber 的进度。
// 与 redis.ProgressEvent 同形态;此处单独导出便于上层 import 不直接依赖 redis 包。
type ProgressEvent = ocredis.ProgressEvent

// LocalImageProvider 是 manager 本机 docker 能力的最小契约。
// imagesync.LocalDockerSDKProvider 直接满足。
type LocalImageProvider interface {
	ImageID(ctx context.Context, image string) (string, error)
	Archive(ctx context.Context, image string) (io.ReadCloser, error)
	Pull(ctx context.Context, image string) (io.ReadCloser, error)
}

// AgentImageClient 与 imagesync.AgentImageClient 同形态,本包重新声明
// 是为了让 Coordinator 不直接依赖 imagesync 包(避免后者反向 import)。
type AgentImageClient interface {
	InspectImage(ctx context.Context, nodeID, image string) (RemoteImageInfo, error)
	// LoadImage 将 archive 发送给 agent 执行 docker load。
	// expectedID 是 manager 本地镜像的内容 ID；agent 在 load 后若 tag 未指向该 ID
	// 会通过 docker tag 强制修正，确保 tag 始终指向正确内容。
	LoadImage(ctx context.Context, nodeID, image, expectedID string, archive io.Reader) (RemoteImageInfo, error)
}

// RemoteImageInfo 与 imagesync.RemoteImageInfo 同语义。
type RemoteImageInfo struct {
	Exists bool
	ID     string
}
