package main

import (
	"context"

	"oc-manager/internal/integrations/k8sorch"
)

// orchChannelRestarter 用 Orchestrator.RolloutRestart 实现 handlers.ChannelRestarter，
// 把渠道绑定后的 hermes platform 重载落到 k8s pod 滚动重建（patch restartedAt 注解）。
type orchChannelRestarter struct{ orch k8sorch.Orchestrator }

func (r orchChannelRestarter) RestartApp(ctx context.Context, appID string) error {
	if r.orch == nil {
		// k8s 未启用（降级）：无编排器可重启，返回 nil 不阻断绑定状态闭环。
		return nil
	}
	return r.orch.RolloutRestart(ctx, appID)
}
