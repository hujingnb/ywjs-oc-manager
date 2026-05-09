package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/store/sqlc"
)

// ChannelStore 抽象渠道服务的数据访问能力。
type ChannelStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetChannelBindingByAppAndType(ctx context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingStatus(ctx context.Context, arg sqlc.SetChannelBindingStatusParams) (sqlc.ChannelBinding, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

// ChannelService 协调 channel adapter 与 channel_bindings 表。
type ChannelService struct {
	store    ChannelStore
	registry *channel.Registry
	notifier JobNotifier
}

// NewChannelService 创建 service。
func NewChannelService(store ChannelStore, registry *channel.Registry, notifier ...JobNotifier) *ChannelService {
	var n JobNotifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	return &ChannelService{store: store, registry: registry, notifier: n}
}

// ChallengeResult 是 BeginAuth 对外返回的视图。
type ChallengeResult struct {
	Status        string            `json:"status"`
	ChannelType   string            `json:"channel_type"`
	ChallengeType string            `json:"challenge_type,omitempty"`
	QRCode        string            `json:"qrcode,omitempty"`
	Code          string            `json:"code,omitempty"`
	ExpiresAt     time.Time         `json:"expires_at,omitempty"`
	Hints         map[string]string `json:"hints,omitempty"`
	JobID         string            `json:"job_id,omitempty"`
}

// ProgressResult 是 PollAuth 对外返回的视图。
type ProgressResult struct {
	Status        string            `json:"status"`
	BoundIdentity string            `json:"bound_identity,omitempty"`
	ChannelName   string            `json:"channel_name,omitempty"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// BeginAuth 启动指定应用、指定渠道的登录挑战。
// HTTP 层不直接执行 OpenClaw CLI：真实登录由 channel_start_login worker 完成。
// 这里只负责权限校验、渠道可用性校验、状态置为 pending_auth 并入队任务，
// 避免微信插件加载或二维码生成阻塞请求线程。
func (s *ChannelService) BeginAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (ChallengeResult, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(channelType); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, channelType)
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChallengeResult{}, ErrNotFound
		}
		return ChallengeResult{}, fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	if binding.Status == domain.ChannelStatusBound {
		return ChallengeResult{Status: domain.ChannelStatusBound, ChannelType: channelType}, nil
	}
	if _, err := s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
		AppID:       binding.AppID,
		ChannelType: binding.ChannelType,
		Status:      domain.ChannelStatusPendingAuth,
		LastError:   pgtype.Text{},
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("更新渠道状态失败: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"app_id":       uuidToString(app.ID),
		"channel_type": channelType,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化渠道登录任务失败: %w", err)
	}
	job, err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        domain.JobTypeChannelStartLogin,
		Priority:    90,
		RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		MaxAttempts: 3,
		PayloadJson: payload,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("创建渠道登录任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, uuidToString(job.ID))
	}
	return ChallengeResult{
		Status:      domain.ChannelStatusPendingAuth,
		ChannelType: channelType,
		JobID:       uuidToString(job.ID),
	}, nil
}

// PollAuth 查询登录进度。真实状态推进由 channel_check_binding worker 完成；
// 这里只读取 DB 中的 channel_bindings，保证轮询接口轻量且可恢复。
func (s *ChannelService) PollAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (ProgressResult, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return ProgressResult{}, err
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProgressResult{}, ErrNotFound
		}
		return ProgressResult{}, fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	metadata := map[string]string{}
	if len(binding.MetadataJson) > 0 {
		metadata = channelBindingMetadata(binding.MetadataJson)
	}
	updatedAt := time.Now()
	if binding.UpdatedAt.Valid {
		updatedAt = binding.UpdatedAt.Time
	}
	errorMessage := ""
	if binding.LastError.Valid {
		errorMessage = binding.LastError.String
	}
	boundIdentity := ""
	if binding.BoundIdentity.Valid {
		boundIdentity = binding.BoundIdentity.String
	}
	channelName := ""
	if binding.ChannelName.Valid {
		channelName = binding.ChannelName.String
	}
	return ProgressResult{
		Status:        binding.Status,
		BoundIdentity: boundIdentity,
		ChannelName:   channelName,
		ErrorMessage:  errorMessage,
		UpdatedAt:     updatedAt,
		Metadata:      metadata,
	}, nil
}

// Unbind 解绑指定渠道，状态置为 unbound_by_user。
func (s *ChannelService) Unbind(ctx context.Context, principal auth.Principal, appID, channelType string) error {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return err
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	if _, err := s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
		AppID:       binding.AppID,
		ChannelType: binding.ChannelType,
		Status:      domain.ChannelStatusUnboundByUser,
		LastError:   pgtype.Text{},
	}); err != nil {
		return fmt.Errorf("解绑渠道失败: %w", err)
	}
	return nil
}

func (s *ChannelService) loadAuthorizedApp(ctx context.Context, principal auth.Principal, appID string) (sqlc.App, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return sqlc.App{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.App{}, ErrNotFound
	}
	if err != nil {
		return sqlc.App{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return sqlc.App{}, ErrForbidden
	}
	if !auth.CanManageApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return sqlc.App{}, ErrForbidden
	}
	return app, nil
}

func channelBindingMetadata(raw []byte) map[string]string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return map[string]string{}
	}
	metadata := make(map[string]string, len(data))
	for key, value := range data {
		switch v := value.(type) {
		case string:
			metadata[key] = v
		case map[string]any:
			for hintKey, hintValue := range v {
				if hint, ok := hintValue.(string); ok {
					metadata[hintKey] = hint
				}
			}
		}
	}
	return metadata
}
