package imagesync

import (
	"context"
	"fmt"
	"io"
)

type RemoteImageInfo struct {
	Exists bool
	ID     string
}

type AgentImageClient interface {
	InspectImage(ctx context.Context, nodeID string, image string) (RemoteImageInfo, error)
	LoadImage(ctx context.Context, nodeID string, image string, archive io.Reader) (RemoteImageInfo, error)
}

type LocalImageProvider interface {
	ImageID(ctx context.Context, image string) (string, error)
	Archive(ctx context.Context, image string) (io.ReadCloser, error)
}

type Service struct {
	local LocalImageProvider
	agent AgentImageClient
}

type SyncResult struct {
	Image       string
	NodeID      string
	LocalID     string
	RemoteID    string
	Transferred bool
}

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
		return result, fmt.Errorf("remote image id mismatch after load: local=%s remote=%s", localID, loaded.ID)
	}
	return result, nil
}
