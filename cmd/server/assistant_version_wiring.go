package main

import (
	"context"

	"oc-manager/internal/config"
	"oc-manager/internal/service"
)

// runtimeImageAdapter 用配置文件的镜像列表实现版本 service 需要的镜像校验与列举能力。
// 结构体只持有静态配置切片，无并发状态，可安全复用。
type runtimeImageAdapter struct {
	// images 是从 cfg.Hermes.RuntimeImages 传入的镜像配置列表。
	images []config.RuntimeImageConfig
}

// HasRuntimeImage 判断 image_id 是否存在于配置，供助手版本创建/更新时校验镜像合法性。
func (a runtimeImageAdapter) HasRuntimeImage(id string) bool {
	_, ok := config.ResolveRuntimeImage(a.images, id)
	return ok
}

// ResolveRuntimeImage 把 image_id 解析成镜像 ref，供 AppService 计算 version_synced。
func (a runtimeImageAdapter) ResolveRuntimeImage(id string) (string, bool) {
	return config.ResolveRuntimeImage(a.images, id)
}

// ListRuntimeImages 返回全部镜像选项（仅 id + label），供前端版本编辑表单的镜像 select 使用。
func (a runtimeImageAdapter) ListRuntimeImages() []service.RuntimeImageOption {
	out := make([]service.RuntimeImageOption, 0, len(a.images))
	for _, img := range a.images {
		out = append(out, service.RuntimeImageOption{ID: img.ID, Label: img.Label})
	}
	return out
}

// modelValidatorAdapter 把 ModelCatalogService 适配成版本 service 需要的无 ctx HasModel。
// 调用时使用 context.Background()，校验逻辑属于短时查询，不需要请求 ctx 传递。
type modelValidatorAdapter struct {
	// catalog 是 new-api 实时模型目录服务。
	catalog *service.ModelCatalogService
}

// HasModel 判断模型名是否存在于 new-api 实时模型列表。查询失败时保守返回 false，
// 避免因 new-api 临时不可用而放行非法模型名。
func (a modelValidatorAdapter) HasModel(id string) bool {
	exists, err := a.catalog.HasModelInCatalog(context.Background(), id)
	// 助手版本旧校验接口无法表达目录故障，保持原 fail-closed 语义。
	return err == nil && exists
}
