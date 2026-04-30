package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/openclaw"
	"oc-manager/internal/store/sqlc"
)

// AppInitializeStore 是 app_initialize handler 需要的最小数据访问能力。
type AppInitializeStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
}

// ImageDistributor 抽象镜像分发能力。
type ImageDistributor interface {
	EnsureRuntimeImage(ctx context.Context, nodeID, image string) (any, error)
}

// NewAPIClient 是 worker 与 new-api 交互的最小集合。
type NewAPIClient interface {
	CreateAPIKey(ctx context.Context, input newapi.CreateAPIKeyInput) (newapi.APIKey, error)
}

// AppInitializeConfig 提供 handler 运行所需的外部配置。
type AppInitializeConfig struct {
	RuntimeImage   string
	PlatformPrompt string
}

// AppInitializeHandler 编排应用初始化流程。
//
// 当前版本完成下列步骤；后续 task 把容器创建从 ErrUnimplemented 替换为真实 docker proxy 调用：
//  1. 加载应用、组织、用户上下文；
//  2. 校验已有 api_key 状态以保证幂等：active 直接跳过；
//  3. 调用 ImageDistributor 同步镜像；
//  4. 渲染 prompt，未替换占位符直接失败；
//  5. 调用 new-api 创建 api_key 并持久化；
//  6. 更新应用状态为 binding_waiting，由 channel 流程接管后续状态。
type AppInitializeHandler struct {
	store     AppInitializeStore
	images    ImageDistributor
	newapi    NewAPIClient
	cfg       AppInitializeConfig
}

// NewAppInitializeHandler 创建 handler。
func NewAppInitializeHandler(store AppInitializeStore, images ImageDistributor, client NewAPIClient, cfg AppInitializeConfig) *AppInitializeHandler {
	if cfg.RuntimeImage == "" {
		cfg.RuntimeImage = "openclaw-runtime:dev"
	}
	return &AppInitializeHandler{store: store, images: images, newapi: client, cfg: cfg}
}

// Handle 是 worker 调用入口，签名匹配 handlers.HandlerFunc。
func (h *AppInitializeHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppInitialize {
		return fmt.Errorf("非 app_initialize 任务: %s", job.Type)
	}
	payload, err := decodePayload(job.PayloadJson)
	if err != nil {
		return err
	}
	appUUID, err := parseUUID(payload.AppID)
	if err != nil {
		return fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := h.store.GetApp(ctx, appUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("应用 %s 不存在", payload.AppID)
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	if app.Status == domain.AppStatusRunning || app.Status == domain.AppStatusBindingWaiting {
		// 幂等：应用已经离开初始化阶段，重复执行直接成功。
		return nil
	}
	org, err := h.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := h.store.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return fmt.Errorf("查询应用 owner 失败: %w", err)
	}

	if h.images != nil && payload.RuntimeNodeID != "" {
		if _, err := h.images.EnsureRuntimeImage(ctx, payload.RuntimeNodeID, h.cfg.RuntimeImage); err != nil {
			return fmt.Errorf("分发 OpenClaw 镜像失败: %w", err)
		}
	}

	if _, err := openclaw.Render(openclaw.PromptInput{
		PlatformPrompt: h.cfg.PlatformPrompt,
		OrgPrompt:      "",
		AppPrompt:      textOrEmpty(app.AppPrompt),
		Variables:      openclaw.VariablesFromContext(org.Name, app.Name, owner.DisplayName),
	}); err != nil {
		return fmt.Errorf("渲染 prompt 失败: %w", err)
	}

	if app.ApiKeyStatus != domain.APIKeyStatusActive {
		if h.newapi == nil {
			return fmt.Errorf("new-api client 未配置，无法创建 api_key")
		}
		key, err := h.newapi.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
			Name:   fmt.Sprintf("%s-%s", org.Name, app.Name),
			Models: []string{},
			Quota:  0,
		})
		if err != nil {
			return fmt.Errorf("调用 new-api 创建 api_key 失败: %w", err)
		}
		if _, err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
			ID:                  app.ID,
			NewapiKeyID:         pgtype.Text{String: fmt.Sprintf("%d", key.ID), Valid: key.ID != 0},
			NewapiKeyCiphertext: pgtype.Text{String: key.Key, Valid: key.Key != ""},
			ApiKeyStatus:        domain.APIKeyStatusActive,
		}); err != nil {
			return fmt.Errorf("写入 api_key 失败: %w", err)
		}
	}

	if app.Status != domain.AppStatusBindingWaiting {
		if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
			ID:     app.ID,
			Status: domain.AppStatusBindingWaiting,
		}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
	}
	return nil
}

type appInitializePayload struct {
	AppID         string `json:"app_id"`
	RuntimeNodeID string `json:"runtime_node"`
}

func decodePayload(raw []byte) (appInitializePayload, error) {
	var payload appInitializePayload
	if len(raw) == 0 {
		return payload, fmt.Errorf("app_initialize payload 为空")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("解析 payload 失败: %w", err)
	}
	if payload.AppID == "" {
		return payload, fmt.Errorf("payload 缺少 app_id")
	}
	return payload, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
