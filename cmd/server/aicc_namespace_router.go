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

// appKindResolver 从应用记录读取 app_type，避免以名称或镜像推断 namespace。
type appKindResolver struct{ store appKindStore }

// ResolveAppType 返回应用的持久化类型；未知类型必须失败，避免被路由到普通 namespace。
func (r appKindResolver) ResolveAppType(ctx context.Context, appID string) (domain.AppType, error) {
	if r.store == nil {
		return "", fmt.Errorf("应用类型查询 store 未配置")
	}
	app, err := r.store.GetApp(ctx, appID)
	if err != nil {
		return "", err
	}
	appType := domain.AppType(app.AppType)
	if appType != domain.AppTypeStandard && !domain.IsAICCAppType(appType) {
		return "", fmt.Errorf("应用 %s 的类型 %q 不支持编排路由", appID, app.AppType)
	}
	return appType, nil
}
