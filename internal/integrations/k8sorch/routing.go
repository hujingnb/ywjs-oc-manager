package k8sorch

import (
	"context"
	"fmt"
	"time"
)

// AppKindResolver 查询应用是否属于 AICC 隐藏运行时。
type AppKindResolver interface {
	IsAICCHidden(ctx context.Context, appID string) (bool, error)
}

// RoutingOrchestrator 按应用类型把操作分发至普通或 AICC 专用 namespace。
type RoutingOrchestrator struct {
	normal   Orchestrator
	aicc     Orchestrator
	resolver AppKindResolver
}

// NewRoutingOrchestrator 构造按应用类型路由的编排器。
func NewRoutingOrchestrator(normal, aicc Orchestrator, resolver AppKindResolver) *RoutingOrchestrator {
	return &RoutingOrchestrator{normal: normal, aicc: aicc, resolver: resolver}
}

func (r *RoutingOrchestrator) target(ctx context.Context, appID string) (Orchestrator, error) {
	if r.resolver == nil {
		return nil, fmt.Errorf("应用 %s 缺少 AICC 类型解析器", appID)
	}
	hidden, err := r.resolver.IsAICCHidden(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("解析应用 %s 的 AICC 类型失败: %w", appID, err)
	}
	if hidden {
		if r.aicc == nil {
			return nil, fmt.Errorf("AICC 应用 %s 缺少专用编排器", appID)
		}
		return r.aicc, nil
	}
	if r.normal == nil {
		return nil, fmt.Errorf("普通应用 %s 缺少编排器", appID)
	}
	return r.normal, nil
}

func (r *RoutingOrchestrator) EnsureApp(ctx context.Context, spec AppSpec) error {
	if spec.AICCHidden {
		if r.aicc == nil {
			return fmt.Errorf("AICC 应用 %s 缺少专用编排器", spec.AppID)
		}
		return r.aicc.EnsureApp(ctx, spec)
	}
	if r.normal == nil {
		return fmt.Errorf("普通应用 %s 缺少编排器", spec.AppID)
	}
	return r.normal.EnsureApp(ctx, spec)
}
func (r *RoutingOrchestrator) WaitReady(ctx context.Context, id string, timeout time.Duration, cb func(AppStatus)) error {
	o, e := r.target(ctx, id)
	if e != nil {
		return e
	}
	return o.WaitReady(ctx, id, timeout, cb)
}
func (r *RoutingOrchestrator) Scale(ctx context.Context, id string, n int32) error {
	o, e := r.target(ctx, id)
	if e != nil {
		return e
	}
	return o.Scale(ctx, id, n)
}
func (r *RoutingOrchestrator) UpdateImage(ctx context.Context, id, img string) error {
	o, e := r.target(ctx, id)
	if e != nil {
		return e
	}
	return o.UpdateImage(ctx, id, img)
}
func (r *RoutingOrchestrator) Delete(ctx context.Context, id string) error {
	o, e := r.target(ctx, id)
	if e != nil {
		return e
	}
	return o.Delete(ctx, id)
}
func (r *RoutingOrchestrator) Status(ctx context.Context, id string) (AppStatus, error) {
	o, e := r.target(ctx, id)
	if e != nil {
		return AppStatus{}, e
	}
	return o.Status(ctx, id)
}
func (r *RoutingOrchestrator) RolloutRestart(ctx context.Context, id string) error {
	o, e := r.target(ctx, id)
	if e != nil {
		return e
	}
	return o.RolloutRestart(ctx, id)
}

// PatchSecretKeys 把渠道凭据写到应用所属 namespace 的 Secret。
func (r *RoutingOrchestrator) PatchSecretKeys(ctx context.Context, id string, set map[string]string, del []string) error {
	o, err := r.target(ctx, id)
	if err != nil {
		return err
	}
	p, ok := o.(interface {
		PatchSecretKeys(context.Context, string, map[string]string, []string) error
	})
	if !ok {
		return fmt.Errorf("应用 %s 的编排器不支持更新 Secret", id)
	}
	return p.PatchSecretKeys(ctx, id, set, del)
}

var _ Orchestrator = (*RoutingOrchestrator)(nil)
