package k8sorch

import (
	"context"
	"fmt"
	"time"

	"oc-manager/internal/domain"
)

// AppKindResolver 查询应用类型，供状态类操作选择正确的运行时 namespace。
type AppKindResolver interface {
	ResolveAppType(ctx context.Context, appID string) (domain.AppType, error)
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
	appType, err := r.resolver.ResolveAppType(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("解析应用 %s 的类型失败: %w", appID, err)
	}
	return r.targetForAppType(appID, appType)
}

// targetForAppType 按已校验的应用类型选择 namespace 编排器；未知类型必须失败，不能默认普通应用。
func (r *RoutingOrchestrator) targetForAppType(appID string, appType domain.AppType) (Orchestrator, error) {
	if appType != domain.AppTypeStandard && !domain.IsAICCAppType(appType) {
		return nil, fmt.Errorf("应用 %s 的类型 %q 不支持编排路由", appID, appType)
	}
	// 仅由领域谓词决定 AICC 专属 adapter；其余已校验类型统一走普通 adapter。
	if domain.IsAICCAppType(appType) {
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
	o, err := r.targetForAppType(spec.AppID, spec.AppType)
	if err != nil {
		return err
	}
	return o.EnsureApp(ctx, spec)
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
