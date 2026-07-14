package main

import (
	"context"
	"fmt"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// appKindStore 是编排路由判断应用类型所需的最小查询能力。
type appKindStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
}

// appKindResolver 从应用记录读取 AICC 隐藏标识，避免以名称或镜像推断 namespace。
type appKindResolver struct{ store appKindStore }

// IsAICCHidden 返回应用是否必须使用 AICC 专用运行时 namespace。
func (r appKindResolver) IsAICCHidden(ctx context.Context, appID string) (bool, error) {
	if r.store == nil {
		return false, fmt.Errorf("应用类型查询 store 未配置")
	}
	app, err := r.store.GetApp(ctx, appID)
	if err != nil {
		return false, err
	}
	return domain.IsAICCAppType(domain.AppType(app.AppType)), nil
}
