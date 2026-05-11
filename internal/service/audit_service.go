package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AuditStore 抽象审计日志的数据访问能力。
type AuditStore interface {
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	ListAuditLogsByOrg(ctx context.Context, arg sqlc.ListAuditLogsByOrgParams) ([]sqlc.AuditLog, error)
	ListAuditLogsByTarget(ctx context.Context, arg sqlc.ListAuditLogsByTargetParams) ([]sqlc.AuditLog, error)
}

// AuditResult 表示对外返回的审计日志记录。
// IP 与元数据以字符串形式输出，避免暴露内部 pgtype 结构。
type AuditResult struct {
	ID           string                 `json:"id"`
	ActorID      string                 `json:"actor_id,omitempty"`
	ActorRole    string                 `json:"actor_role"`
	OrgID        string                 `json:"org_id,omitempty"`
	TargetType   string                 `json:"target_type"`
	TargetID     string                 `json:"target_id"`
	Action       string                 `json:"action"`
	Result       string                 `json:"result"`
	ErrorMessage string                 `json:"error_message,omitempty"`
	IPAddress    string                 `json:"ip_address,omitempty"`
	Metadata     map[string]any         `json:"metadata,omitempty"`
	CreatedAt    pgtype.Timestamptz     `json:"created_at" swaggertype:"string" format:"date-time"`
}

// AuditEvent 是其他服务记录审计时的入参。
// service 层在执行写操作后调用 AuditService.Record，将操作主体、目标和结果统一落库。
type AuditEvent struct {
	ActorID      string
	ActorRole    string
	OrgID        string
	TargetType   string
	TargetID     string
	Action       string
	Result       string
	ErrorMessage string
	IPAddress    string
	Metadata     map[string]any
}

// AuditService 处理审计日志的写入和查询。
type AuditService struct {
	store          AuditStore
	maxPageSize    int32
	defaultPageNum int32
}

// NewAuditService 创建审计服务。
func NewAuditService(store AuditStore) *AuditService {
	return &AuditService{store: store, maxPageSize: 200, defaultPageNum: 50}
}

// Record 异步写入审计记录由调用方决定，service 内部目前同步落库。
// 当前实现忽略反序列化失败的 metadata，但会在错误中携带原因，避免日志写失败影响主流程。
func (s *AuditService) Record(ctx context.Context, event AuditEvent) (AuditResult, error) {
	if event.ActorRole == "" || event.TargetType == "" || event.Action == "" || event.Result == "" {
		return AuditResult{}, fmt.Errorf("审计事件缺少必填字段")
	}
	params := sqlc.CreateAuditLogParams{
		ActorRole:  event.ActorRole,
		TargetType: event.TargetType,
		TargetID:   event.TargetID,
		Action:     event.Action,
		Result:     event.Result,
	}
	if event.ActorID != "" {
		actorID, err := parseUUID(event.ActorID)
		if err != nil {
			return AuditResult{}, fmt.Errorf("审计 actor_id 非法: %w", err)
		}
		params.ActorID = actorID
	}
	if event.OrgID != "" {
		orgID, err := parseUUID(event.OrgID)
		if err != nil {
			return AuditResult{}, fmt.Errorf("审计 org_id 非法: %w", err)
		}
		params.OrgID = orgID
	}
	if event.ErrorMessage != "" {
		params.ErrorMessage = pgtype.Text{String: event.ErrorMessage, Valid: true}
	}
	if event.IPAddress != "" {
		addr, err := netip.ParseAddr(event.IPAddress)
		if err == nil {
			params.IpAddress = &addr
		}
	}
	if len(event.Metadata) > 0 {
		raw, err := json.Marshal(event.Metadata)
		if err != nil {
			return AuditResult{}, fmt.Errorf("序列化审计元数据失败: %w", err)
		}
		params.MetadataJson = raw
	}
	row, err := s.store.CreateAuditLog(ctx, params)
	if err != nil {
		return AuditResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	return toAuditResult(row), nil
}

// ListByOrg 按组织维度分页列出审计日志。
// 平台管理员可查询任意组织；组织管理员仅能查询本组织；普通成员无审计视角。
func (s *AuditService) ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AuditResult, error) {
	if principal.Role == domain.UserRoleOrgMember {
		return nil, ErrForbidden
	}
	if !auth.CanViewOrg(principal, orgID) {
		return nil, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
	}
	limit, offset = s.normalizePagination(limit, offset)
	rows, err := s.store.ListAuditLogsByOrg(ctx, sqlc.ListAuditLogsByOrgParams{
		OrgID:  id,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询审计日志失败: %w", err)
	}
	return toAuditResults(rows), nil
}

// ListByTarget 按目标资源维度查询审计日志。
// app 目标先加载应用归属再判定权限：平台管理员和本组织管理员可看管理范围内应用，
// 普通成员只能查看自己拥有的应用审计。其他目标类型仍限制为平台管理员或组织管理员。
func (s *AuditService) ListByTarget(ctx context.Context, principal auth.Principal, targetType, targetID string, limit, offset int32) ([]AuditResult, error) {
	if targetType == "app" {
		if err := s.authorizeAppTarget(ctx, principal, targetID); err != nil {
			return nil, err
		}
	} else {
		if principal.Role != domain.UserRolePlatformAdmin && principal.Role != domain.UserRoleOrgAdmin {
			return nil, ErrForbidden
		}
	}
	limit, offset = s.normalizePagination(limit, offset)
	rows, err := s.store.ListAuditLogsByTarget(ctx, sqlc.ListAuditLogsByTargetParams{
		TargetType: targetType,
		TargetID:   targetID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询资源审计日志失败: %w", err)
	}
	results := toAuditResults(rows)
	if principal.Role == domain.UserRoleOrgAdmin {
		filtered := make([]AuditResult, 0, len(results))
		for _, item := range results {
			if item.OrgID == principal.OrgID {
				filtered = append(filtered, item)
			}
		}
		results = filtered
	}
	return results, nil
}

func (s *AuditService) authorizeAppTarget(ctx context.Context, principal auth.Principal, targetID string) error {
	id, err := parseUUID(targetID)
	if err != nil {
		return ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewAppAudit(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return ErrForbidden
	}
	return nil
}

func (s *AuditService) normalizePagination(limit, offset int32) (int32, int32) {
	if limit <= 0 {
		limit = s.defaultPageNum
	}
	if limit > s.maxPageSize {
		limit = s.maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func toAuditResults(rows []sqlc.AuditLog) []AuditResult {
	results := make([]AuditResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAuditResult(row))
	}
	return results
}

func toAuditResult(row sqlc.AuditLog) AuditResult {
	result := AuditResult{
		ID:         uuidToString(row.ID),
		ActorID:    uuidToOptionalString(row.ActorID),
		ActorRole:  row.ActorRole,
		OrgID:      uuidToOptionalString(row.OrgID),
		TargetType: row.TargetType,
		TargetID:   row.TargetID,
		Action:     row.Action,
		Result:     row.Result,
		CreatedAt:  row.CreatedAt,
	}
	if row.ErrorMessage.Valid {
		result.ErrorMessage = row.ErrorMessage.String
	}
	if row.IpAddress != nil {
		result.IPAddress = row.IpAddress.String()
	}
	if len(row.MetadataJson) > 0 {
		metadata := map[string]any{}
		if err := json.Unmarshal(row.MetadataJson, &metadata); err == nil {
			result.Metadata = metadata
		}
	}
	return result
}
