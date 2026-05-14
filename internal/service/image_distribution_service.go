package service

import (
	"context"
	"fmt"

	"oc-manager/internal/runtime/imagesync"
)

// ImageDistributionResult 是 image distribution 服务对外返回的同步结果。
// 转换 imagesync.SyncResult 主要为了让调用方持有的字段名稳定，并隔离 internal/runtime 的实现细节。
type ImageDistributionResult struct {
	Image       string `json:"image"`
	NodeID      string `json:"node_id"`
	LocalID     string `json:"local_id"`
	RemoteID    string `json:"remote_id"`
	Transferred bool   `json:"transferred"`
}

// ImageDistributor 抽象镜像分发能力，便于 service 与 worker 注入测试桩。
type ImageDistributor interface {
	SyncRuntimeImage(ctx context.Context, nodeID, image string) (imagesync.SyncResult, error)
}

// ImageDistributionService 提供面向 service/worker 的镜像分发入口。
// 与 imagesync.Service 不同，它在错误链上叠加业务上下文，便于在 worker handler 中分类重试或失败。
type ImageDistributionService struct {
	distributor ImageDistributor
}

// NewImageDistributionService 创建镜像分发 service。
func NewImageDistributionService(distributor ImageDistributor) *ImageDistributionService {
	return &ImageDistributionService{distributor: distributor}
}

// EnsureRuntimeImage 确保 runtime node 上存在指定镜像。
// 适配规则：
//   - 若 distributor 未配置，返回错误避免静默跳过镜像同步；
//   - 若分发底层报错，包一层带 nodeID/image 的上下文，让上层日志/审计能直接定位；
//   - 若同步成功，返回结构化结果，调用方可用于审计或前端展示。
func (s *ImageDistributionService) EnsureRuntimeImage(ctx context.Context, nodeID, image string) (ImageDistributionResult, error) {
	if s == nil || s.distributor == nil {
		return ImageDistributionResult{}, fmt.Errorf("image distribution service 未配置")
	}
	if nodeID == "" || image == "" {
		return ImageDistributionResult{}, fmt.Errorf("nodeID 与 image 不能为空")
	}
	result, err := s.distributor.SyncRuntimeImage(ctx, nodeID, image)
	if err != nil {
		return ImageDistributionResult{}, fmt.Errorf("同步镜像 %s 到节点 %s 失败: %w", image, nodeID, err)
	}
	return ImageDistributionResult{
		Image:       result.Image,
		NodeID:      result.NodeID,
		LocalID:     result.LocalID,
		RemoteID:    result.RemoteID,
		Transferred: result.Transferred,
	}, nil
}
