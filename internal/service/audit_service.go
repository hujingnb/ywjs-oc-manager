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
// ListAuditLogsByOrg / ListAuditLogsByTarget 由于 SELECT 含计算列，
// sqlc 为它们生成独立的 *Row 结构体；CreateAuditLog 仍然返回 sqlc.AuditLog。
type AuditStore interface {
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	ListAuditLogsByOrg(ctx context.Context, arg sqlc.ListAuditLogsByOrgParams) ([]sqlc.ListAuditLogsByOrgRow, error)
	ListAuditLogsByTarget(ctx context.Context, arg sqlc.ListAuditLogsByTargetParams) ([]sqlc.ListAuditLogsByTargetRow, error)
}

// AuditResult 表示对外返回的审计日志记录。
// IP 与元数据以字符串形式输出，避免暴露内部 pgtype 结构。
type AuditResult struct {
	ID           string             `json:"id"`
	ActorID      string             `json:"actor_id,omitempty"`
	ActorRole    string             `json:"actor_role"`
	OrgID        string             `json:"org_id,omitempty"`
	TargetType   string             `json:"target_type"`
	TargetID     string             `json:"target_id"`
	Action       string             `json:"action"`
	Result       string             `json:"result"`
	ErrorMessage string             `json:"error_message,omitempty"`
	IPAddress    string             `json:"ip_address,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
	CreatedAt    pgtype.Timestamptz `json:"created_at" swaggertype:"string" format:"date-time"`
	// 以下为展示用翻译字段，由 toAuditResult() 填充，未知值 fallback 到原始字符串。
	ActionLabel     string `json:"action_label"`
	TargetTypeLabel string `json:"target_type_label"`
	ActorRoleLabel  string `json:"actor_role_label"`
	ResultLabel     string `json:"result_label"`
	// ActorName 是 actor_id 对应用户的 display_name fallback username。
	// 写入时不取，查询时通过 LEFT JOIN 实时填充；空字符串表示无 actor / actor 已物理删除。
	ActorName string `json:"actor_name,omitempty"`
	// ActorDeleted 表示 actor 对应用户已被软删除（users.deleted_at 非空，本项目即「下线」）。
	ActorDeleted bool `json:"actor_deleted"`
	// TargetName 是 target_id 对应资源名称；按 target_type 走相关子查询，
	// 对 newapi_call 等无对应实体的类型返回空字符串。
	TargetName string `json:"target_name,omitempty"`
	// TargetDeleted 表示目标资源对应实体已软删除。
	TargetDeleted bool `json:"target_deleted"`
	// ActionDetail 是写入时冻结的详情字符串，直接读自 audit_logs.detail_message 列。
	// 空字符串表示无详情，前端展示「—」。
	ActionDetail string `json:"action_detail,omitempty"`
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
	// DetailMessage 由调用方拼好的中文短句；写入即冻结，查询时直接返回。
	// 空字符串表示无详情，前端展示「—」。
	DetailMessage string
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
	if event.DetailMessage != "" {
		params.DetailMessage = pgtype.Text{String: event.DetailMessage, Valid: true}
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
	return toAuditResultsFromOrgRows(rows), nil
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
	results := toAuditResultsFromTargetRows(rows)
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

// toAuditResult 把 INSERT 路径返回的 sqlc.AuditLog 转成 AuditResult。
// 写入路径没有 JOIN，所以 ActorName / TargetName / *Deleted 全部留空；
// ActionDetail 直接读 detail_message。
func toAuditResult(row sqlc.AuditLog) AuditResult {
	result := AuditResult{
		ID:              uuidToString(row.ID),
		ActorID:         uuidToOptionalString(row.ActorID),
		ActorRole:       row.ActorRole,
		OrgID:           uuidToOptionalString(row.OrgID),
		TargetType:      row.TargetType,
		TargetID:        row.TargetID,
		Action:          row.Action,
		Result:          row.Result,
		CreatedAt:       row.CreatedAt,
		ActionLabel:     labelAction(row.TargetType, row.Action),
		TargetTypeLabel: labelTargetType(row.TargetType),
		ActorRoleLabel:  labelActorRole(row.ActorRole),
		ResultLabel:     labelResult(row.Result),
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
	if row.DetailMessage.Valid {
		result.ActionDetail = row.DetailMessage.String
	}
	return result
}

// toAuditResultsFromOrgRows 转换 ListAuditLogsByOrg 的查询行。
func toAuditResultsFromOrgRows(rows []sqlc.ListAuditLogsByOrgRow) []AuditResult {
	results := make([]AuditResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAuditResultFromOrgRow(row))
	}
	return results
}

// toAuditResultFromOrgRow 把 ListAuditLogsByOrgRow 转 AuditResult。
// 用法：先组合一个等价的 sqlc.AuditLog 复用 toAuditResult 主逻辑，
// 再覆盖 actor / target 名称与软删除标记。
func toAuditResultFromOrgRow(row sqlc.ListAuditLogsByOrgRow) AuditResult {
	base := toAuditResult(sqlc.AuditLog{
		ID:            row.ID,
		ActorID:       row.ActorID,
		ActorRole:     row.ActorRole,
		OrgID:         row.OrgID,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		Action:        row.Action,
		Result:        row.Result,
		ErrorMessage:  row.ErrorMessage,
		IpAddress:     row.IpAddress,
		MetadataJson:  row.MetadataJson,
		CreatedAt:     row.CreatedAt,
		DetailMessage: row.DetailMessage,
	})
	applyNameColumns(&base, row.ActorName, row.ActorDeleted, row.TargetName, row.TargetDeleted)
	return base
}

// toAuditResultsFromTargetRows 同 OrgRows 路径。
func toAuditResultsFromTargetRows(rows []sqlc.ListAuditLogsByTargetRow) []AuditResult {
	results := make([]AuditResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAuditResultFromTargetRow(row))
	}
	return results
}

// toAuditResultFromTargetRow 把 ListAuditLogsByTargetRow 转 AuditResult。
// 实现同 toAuditResultFromOrgRow，字段名完全一致，故直接复用同一模式。
func toAuditResultFromTargetRow(row sqlc.ListAuditLogsByTargetRow) AuditResult {
	base := toAuditResult(sqlc.AuditLog{
		ID:            row.ID,
		ActorID:       row.ActorID,
		ActorRole:     row.ActorRole,
		OrgID:         row.OrgID,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		Action:        row.Action,
		Result:        row.Result,
		ErrorMessage:  row.ErrorMessage,
		IpAddress:     row.IpAddress,
		MetadataJson:  row.MetadataJson,
		CreatedAt:     row.CreatedAt,
		DetailMessage: row.DetailMessage,
	})
	applyNameColumns(&base, row.ActorName, row.ActorDeleted, row.TargetName, row.TargetDeleted)
	return base
}

// applyNameColumns 将 List 查询行的名称 / 软删除标记字段写入 AuditResult。
// sqlc 对 actor_name 推断为 string、其余三列推断为 interface{}（因为 COALESCE 表达式跨类型）；
// 这里用 string / bool 适配 helper 屏蔽差异。
func applyNameColumns(r *AuditResult, actorName string, actorDeleted any, targetName any, targetDeleted any) {
	r.ActorName = actorName
	r.ActorDeleted = boolFromColumn(actorDeleted)
	r.TargetName = stringFromColumn(targetName)
	r.TargetDeleted = boolFromColumn(targetDeleted)
}

// stringFromColumn 把 sqlc 推断为 interface{} 的文本列适配为 string。
// 兼容 nil、string、*string、pgtype.Text 几种实际承载形式。
func stringFromColumn(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case *string:
		if t == nil {
			return ""
		}
		return *t
	case pgtype.Text:
		if t.Valid {
			return t.String
		}
		return ""
	}
	return ""
}

// boolFromColumn 把 sqlc 推断为 interface{} 的布尔列适配为 bool。
func boolFromColumn(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case *bool:
		if t == nil {
			return false
		}
		return *t
	}
	return false
}
