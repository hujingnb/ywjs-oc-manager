package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// APIKeyStatusStore 是 newapi disable/restore handler 共用的 sqlc 子集。
type APIKeyStatusStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) error
}

// NewAPIKeyStatusHandler 处理 newapi_disable_key / newapi_restore_key 两类 job。
//
// 行为：
//   - 解 payload.app_id；
//   - 取 app.newapi_key_id（解析 string→int64），无值时直接成功（防御）；
//   - 用 NewAPIClientFactory 构造该 app 所属组织的 user-scoped client；
//   - 调 client.SetAPIKeyStatus 翻转 token 状态；
//   - 把 apps.api_key_status 同步到 active / disabled，便于 UI 直接读 apps 表渲染状态徽章。
type NewAPIKeyStatusHandler struct {
	store    APIKeyStatusStore
	factory  NewAPIClientFactory
	jobType  string
	newState int
	tag      string
}

// NewDisableAPIKeyHandler 构造 newapi_disable_key 处理器（status=2）。
func NewDisableAPIKeyHandler(store APIKeyStatusStore, factory NewAPIClientFactory) *NewAPIKeyStatusHandler {
	return &NewAPIKeyStatusHandler{store: store, factory: factory, jobType: domain.JobTypeNewAPIDisableKey, newState: 2, tag: domain.APIKeyStatusDisabled}
}

// NewRestoreAPIKeyHandler 构造 newapi_restore_key 处理器（status=1）。
func NewRestoreAPIKeyHandler(store APIKeyStatusStore, factory NewAPIClientFactory) *NewAPIKeyStatusHandler {
	return &NewAPIKeyStatusHandler{store: store, factory: factory, jobType: domain.JobTypeNewAPIRestoreKey, newState: 1, tag: domain.APIKeyStatusActive}
}

// Handle 执行 disable/restore job。
func (h *NewAPIKeyStatusHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != h.jobType {
		return fmt.Errorf("非 %s 任务: %s", h.jobType, job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, err := h.store.GetApp(ctx, payload.AppID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	if !app.NewapiKeyID.Valid || app.NewapiKeyID.String == "" {
		return nil // 无 token 无操作。
	}
	keyID, err := strconv.ParseInt(app.NewapiKeyID.String, 10, 64)
	if err != nil {
		return fmt.Errorf("解析 newapi_key_id 失败: %w", err)
	}
	if h.factory == nil {
		return fmt.Errorf("new-api client factory 未配置，无法切换 token 状态")
	}
	client, err := h.factory.UserScopedFor(ctx, app)
	if err != nil {
		return fmt.Errorf("构造 user-scoped client 失败: %w", err)
	}
	if err := client.SetAPIKeyStatus(ctx, keyID, h.newState); err != nil {
		return fmt.Errorf("调 new-api 切换 token 状态失败: %w", err)
	}
	if err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
		ID:                  app.ID,
		NewapiKeyID:         app.NewapiKeyID,
		NewapiKeyCiphertext: app.NewapiKeyCiphertext,
		ApiKeyStatus:        h.tag,
	}); err != nil {
		return fmt.Errorf("更新 apps.api_key_status 失败: %w", err)
	}
	return nil
}
