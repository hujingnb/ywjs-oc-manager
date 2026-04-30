package service

import (
	"context"
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

// ErrChannelAdapterMissing 表示 service 调用时未注册对应渠道。
var ErrChannelAdapterMissing = errors.New("当前渠道未启用")

// ChannelStore 抽象渠道服务的数据访问能力。
type ChannelStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetChannelBindingByAppAndType(ctx context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingChallenge(ctx context.Context, arg sqlc.SetChannelBindingChallengeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingStatus(ctx context.Context, arg sqlc.SetChannelBindingStatusParams) (sqlc.ChannelBinding, error)
	MarkChannelBindingBound(ctx context.Context, arg sqlc.MarkChannelBindingBoundParams) (sqlc.ChannelBinding, error)
}

// ChannelService 协调 channel adapter 与 channel_bindings 表。
type ChannelService struct {
	store    ChannelStore
	registry *channel.Registry
}

// NewChannelService 创建 service。
func NewChannelService(store ChannelStore, registry *channel.Registry) *ChannelService {
	return &ChannelService{store: store, registry: registry}
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
// 启动后会把挑战信息写入 channel_bindings.metadata_json 与 status='pending_auth'，
// 即使 manager 重启，前端仍能从数据库恢复挑战状态。
func (s *ChannelService) BeginAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (ChallengeResult, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	adapter, err := s.registry.Lookup(channelType)
	if err != nil {
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

	challenge, err := adapter.BeginAuth(ctx, channel.AuthInput{
		AppID:       uuidToString(app.ID),
		OwnerUserID: uuidToString(app.OwnerUserID),
		NodeID:      uuidToOptionalString(app.RuntimeNodeID),
		ContainerID: textOrEmpty(app.ContainerID),
	})
	if err != nil {
		_, _ = s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      domain.ChannelStatusFailed,
			LastError:   pgtype.Text{String: err.Error(), Valid: true},
		})
		return ChallengeResult{}, fmt.Errorf("发起渠道登录失败: %w", err)
	}

	if _, err := s.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
		AppID:        binding.AppID,
		ChannelType:  binding.ChannelType,
		MetadataJson: challengeToJSON(challenge),
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("保存渠道挑战失败: %w", err)
	}
	if _, err := s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
		AppID:       binding.AppID,
		ChannelType: binding.ChannelType,
		Status:      domain.ChannelStatusPendingAuth,
		LastError:   pgtype.Text{},
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("更新渠道状态失败: %w", err)
	}
	return ChallengeResult{
		Status:        domain.ChannelStatusPendingAuth,
		ChannelType:   channelType,
		ChallengeType: challenge.Type,
		QRCode:        challenge.QRCode,
		Code:          challenge.Code,
		ExpiresAt:     challenge.ExpiresAt,
		Hints:         challenge.Hints,
	}, nil
}

// PollAuth 查询登录进度，必要时把已完成状态写回 channel_bindings。
func (s *ChannelService) PollAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (ProgressResult, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return ProgressResult{}, err
	}
	if s.registry == nil {
		return ProgressResult{}, ErrChannelAdapterMissing
	}
	adapter, err := s.registry.Lookup(channelType)
	if err != nil {
		return ProgressResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, channelType)
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProgressResult{}, ErrNotFound
		}
		return ProgressResult{}, fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	progress, err := adapter.PollAuth(ctx, channel.AuthInput{AppID: uuidToString(app.ID), OwnerUserID: uuidToString(app.OwnerUserID)})
	if err != nil {
		return ProgressResult{}, fmt.Errorf("查询渠道进度失败: %w", err)
	}
	if progress.Status == channel.AuthStatusBound {
		if _, err := s.store.MarkChannelBindingBound(ctx, sqlc.MarkChannelBindingBoundParams{
			AppID:         binding.AppID,
			ChannelType:   binding.ChannelType,
			BoundIdentity: pgtype.Text{String: progress.BoundIdentity, Valid: progress.BoundIdentity != ""},
			ChannelName:   pgtype.Text{String: progress.ChannelName, Valid: progress.ChannelName != ""},
		}); err != nil {
			return ProgressResult{}, fmt.Errorf("标记渠道绑定成功失败: %w", err)
		}
	}
	if progress.Status == channel.AuthStatusFailed || progress.Status == channel.AuthStatusExpired {
		_, _ = s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      string(progress.Status),
			LastError:   pgtype.Text{String: progress.ErrorMessage, Valid: progress.ErrorMessage != ""},
		})
	}
	return ProgressResult{
		Status:        string(progress.Status),
		BoundIdentity: progress.BoundIdentity,
		ChannelName:   progress.ChannelName,
		ErrorMessage:  progress.ErrorMessage,
		UpdatedAt:     progress.UpdatedAt,
		Metadata:      progress.Metadata,
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
	if !canViewApp(principal, app) {
		return sqlc.App{}, ErrForbidden
	}
	if !canManageApp(principal, app) {
		return sqlc.App{}, ErrForbidden
	}
	return app, nil
}

// canManageApp 判断主体是否可以执行渠道写操作。
// 当前规则：平台/组织管理员可以管理本组织内任何应用，普通成员只能管理自己拥有的应用。
func canManageApp(principal auth.Principal, app sqlc.App) bool {
	switch principal.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return principal.OrgID == uuidToString(app.OrgID)
	case domain.UserRoleOrgMember:
		return principal.UserID == uuidToString(app.OwnerUserID)
	default:
		return false
	}
}

func challengeToJSON(c channel.AuthChallenge) []byte {
	bytes, err := jsonMarshal(map[string]any{
		"type":       c.Type,
		"qrcode":     c.QRCode,
		"code":       c.Code,
		"expires_at": c.ExpiresAt,
		"hints":      c.Hints,
	})
	if err != nil {
		return nil
	}
	return bytes
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
