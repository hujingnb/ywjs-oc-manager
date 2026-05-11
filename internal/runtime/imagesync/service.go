// Package imagesync 负责把 manager 本地可用的 OpenClaw runtime 镜像同步到指定 runtime node。
//
// 同步边界刻意放在 nodeID 维度：本包只比较“本地镜像 ID”和“目标节点镜像 ID”，
// 不跨节点复用结果，避免某个节点镜像已更新而另一个节点仍旧落后的情况被误判为成功。
package imagesync

import (
	"context"
	"fmt"
	"io"
)

// RemoteImageInfo 描述目标 runtime node 上某个镜像的 inspect 结果。
type RemoteImageInfo struct {
	// Exists 表示 agent 能否在目标节点 docker daemon 中找到该镜像。
	Exists bool
	// ID 是 docker image inspect 返回的内容摘要；为空表示 agent 未返回可比对 ID。
	ID string
}

// AgentImageClient 抽象 manager 与目标节点 agent 之间的镜像查询和加载协议。
// nodeID 由上层 resolver 用来选择 agent endpoint，本接口实现不得把不同节点的响应混用。
type AgentImageClient interface {
	InspectImage(ctx context.Context, nodeID string, image string) (RemoteImageInfo, error)
	LoadImage(ctx context.Context, nodeID string, image string, archive io.Reader) (RemoteImageInfo, error)
}

// LocalImageProvider 抽象 manager 本机 docker 镜像能力。
// Archive 必须返回流式 reader，避免 docker save 产物一次性进入内存。
type LocalImageProvider interface {
	ImageID(ctx context.Context, image string) (string, error)
	Archive(ctx context.Context, image string) (io.ReadCloser, error)
}

// Service 串联本地镜像 inspect/archive 与远端 agent inspect/load。
type Service struct {
	local LocalImageProvider
	agent AgentImageClient
}

// SyncResult 描述一次镜像同步的最终状态。
type SyncResult struct {
	// Image 是调用方要求同步的镜像引用，通常是 tag 或 digest。
	Image string
	// NodeID 是本次同步的目标节点，结果不得用于其他节点。
	NodeID string
	// LocalID 是 manager 本地 inspect 到的镜像 ID。
	LocalID string
	// RemoteID 是目标节点同步后 inspect/load 返回的镜像 ID。
	RemoteID string
	// Transferred 表示本次是否实际执行 docker save/load 传输。
	Transferred bool
}

// New 创建镜像同步服务；local 或 agent 为 nil 时 SyncOpenClawImage 会返回配置错误。
func New(local LocalImageProvider, agent AgentImageClient) *Service {
	return &Service{local: local, agent: agent}
}

// SyncOpenClawImage 确保 runtime node 上的 OpenClaw runtime 镜像与 manager 本地构建结果一致。
// 远端不存在或 image ID 不一致时，manager 会把本地 docker save 产物通过 agent 文件接口传过去，
// agent 在节点侧执行 docker load；若 ID 已一致则直接跳过，避免重复传输大镜像。
func (s *Service) SyncOpenClawImage(ctx context.Context, nodeID string, image string) (SyncResult, error) {
	if s == nil || s.local == nil || s.agent == nil {
		return SyncResult{}, fmt.Errorf("image sync service is not configured")
	}

	localID, err := s.local.ImageID(ctx, image)
	if err != nil {
		return SyncResult{}, fmt.Errorf("inspect local image: %w", err)
	}
	result := SyncResult{Image: image, NodeID: nodeID, LocalID: localID}

	remote, err := s.agent.InspectImage(ctx, nodeID, image)
	if err != nil {
		return SyncResult{}, fmt.Errorf("inspect remote image: %w", err)
	}
	if remote.Exists && remote.ID == localID {
		result.RemoteID = remote.ID
		return result, nil
	}

	// 只有目标节点缺镜像或 ID 不一致时才创建 archive；这是同步的重试边界，
	// 上游 worker 失败重试会重新从本地 docker save 流式生成一份 tar。
	archive, err := s.local.Archive(ctx, image)
	if err != nil {
		return SyncResult{}, fmt.Errorf("create local image archive: %w", err)
	}
	defer archive.Close()

	loaded, err := s.agent.LoadImage(ctx, nodeID, image, archive)
	if err != nil {
		return SyncResult{}, fmt.Errorf("load remote image: %w", err)
	}
	result.RemoteID = loaded.ID
	result.Transferred = true
	if loaded.ID != "" && loaded.ID != localID {
		// agent load 成功但 ID 不一致说明节点侧 docker 解析到的镜像不是 manager 期望版本，
		// 必须返回错误阻止 app_initialize 用错误 runtime 镜像继续创建容器。
		return result, fmt.Errorf("remote image id mismatch after load: local=%s remote=%s", localID, loaded.ID)
	}
	return result, nil
}
