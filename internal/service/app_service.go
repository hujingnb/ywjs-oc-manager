package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AppStore 抽象 app 服务的数据访问能力。
type AppStore interface {
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) error
	// GetAppWithVersion 联查实例与绑定版本的 revision / image_id，用于计算 version_synced。
	GetAppWithVersion(ctx context.Context, id string) (sqlc.GetAppWithVersionRow, error)
	// ListAppsByOrgWithVersion 批量联查组织实例及绑定版本信息，用于 version_synced 批量计算。
	ListAppsByOrgWithVersion(ctx context.Context, arg sqlc.ListAppsByOrgWithVersionParams) ([]sqlc.ListAppsByOrgWithVersionRow, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	// SetAppKnowledgeQuota 更新单个实例知识库容量上限。
	SetAppKnowledgeQuota(ctx context.Context, arg sqlc.SetAppKnowledgeQuotaParams) error
	SoftDeleteApp(ctx context.Context, id string) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	// GetOrganization 按 id 加载组织记录，用于 allowlist 校验等场景。
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// SetAppVersion 更新实例绑定的助手版本 id，返回更新后的实例记录。
	SetAppVersion(ctx context.Context, arg sqlc.SetAppVersionParams) error
}

// AppImageResolver 把版本 image_id 解析成镜像 ref，用于计算 version_synced 的镜像维度。
// 由 cmd/server 用 hermes.runtime_images 配置适配注入。
type AppImageResolver interface {
	ResolveRuntimeImage(id string) (ref string, ok bool)
}

// AppService 维护应用的查询、状态读取和轻量配置更新。
// 创建应用必须经过 onboarding 事务，因为应用的初始化需要联动 channel binding、audit、job。
type AppService struct {
	store    AppStore
	notifier JobNotifier
	// imageResolver 把版本 image_id 解析成镜像 ref，供 computeVersionSynced 比对镜像维度。
	// nil 时降级为仅比较修订号（镜像维度跳过）。
	imageResolver AppImageResolver
}

// NewAppService 创建 app 服务。
func NewAppService(store AppStore) *AppService { return &AppService{store: store} }

// SetJobNotifier 注入即时 job 入队器；nil 时只写 jobs 表，由 scheduler 周期兜底。
func (s *AppService) SetJobNotifier(notifier JobNotifier) {
	s.notifier = notifier
}

// SetImageResolver 注入版本镜像解析能力；nil 时 version_synced 降级为仅比较修订。
func (s *AppService) SetImageResolver(resolver AppImageResolver) { s.imageResolver = resolver }

// AppResult 是对外的应用视图。
// spec-A2b：runtime_node_id / container_id / container_name 已从 schema 删除，本结构体亦不再携带。
type AppResult struct {
	ID           string `json:"id"`
	OrgID        string `json:"org_id"`
	OwnerUserID  string `json:"owner_user_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Status       string `json:"status"`
	APIKeyStatus string `json:"api_key_status"`
	// KnowledgeQuotaBytes 是实例知识库累计容量上限，单位字节。
	KnowledgeQuotaBytes int64 `json:"knowledge_quota_bytes"`
	// NewapiKeyID 是 new-api 中 token 的数值 id；schema 上是 text 列存的字符串，
	// 这里解析成 int64 方便 usage service 直接调 GetAPIKey。0 表示未绑定。
	NewapiKeyID int64 `json:"newapi_key_id,omitempty"`
	// ProgressCurrent 当前 status 阶段的已完成量,单位由 status 决定(字节 / 秒);
	// 0 或缺省表示未知 / 不显示进度条。
	ProgressCurrent int64 `json:"progress_current,omitempty"`
	// ProgressTotal 当前 status 阶段的总量;0 或缺省时前端展示为不定进度。
	ProgressTotal int64 `json:"progress_total,omitempty"`
	// LastErrorStatus 上次进入 error 时所在的状态值;前端用 formatAppStatus 转中文文案。
	LastErrorStatus string `json:"last_error_status,omitempty"`
	// LastErrorMessage 上次进入 error 时的错误原始文本;供前端直接展示给用户。
	LastErrorMessage string `json:"last_error_message,omitempty"`
	// RuntimeImageRef 是 phasePullRuntimeImage 拉取的镜像引用（如 ghcr.io/foo/hermes:v1.2.3）。
	// 仅平台管理员可见，用于运维排障和版本溯源。
	RuntimeImageRef string `json:"runtime_image_ref,omitempty"`
	// RuntimeImageSha256 是 docker inspect 返回的镜像 config digest（sha256:...）。
	// 仅平台管理员可见；与 RuntimeImageRef 共同标识节点上运行的精确镜像版本。
	RuntimeImageSha256 string `json:"runtime_image_sha256,omitempty"`
	// VersionID 是实例绑定的助手版本 id；空表示未绑定（仅历史数据）。
	VersionID string `json:"version_id,omitempty"`
	// VersionSynced 标记实例运行时是否已与绑定版本对齐（修订 + 镜像都一致）；
	// false 表示版本被编辑过，需重启实例生效。
	VersionSynced bool `json:"version_synced"`
}

// Get 查询应用。
func (s *AppService) Get(ctx context.Context, principal auth.Principal, appID string) (AppResult, error) {
	// appID 直接作为字符串传入；格式非法（不存在）时 store 返回 sql.ErrNoRows。
	row, err := s.store.GetAppWithVersion(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, row.App.OrgID, row.App.OwnerUserID) {
		return AppResult{}, ErrForbidden
	}
	result := toAppResult(row.App)
	// version_synced：修订 + 镜像双维度对比，判断实例是否需要重启。
	result.VersionSynced = computeVersionSynced(row.App, row.VersionRevision, row.VersionImageID, s.imageResolver)
	// runtime_image_ref / sha256 含节点内部镜像信息，仅对平台管理员开放。
	if principal.Role == domain.UserRolePlatformAdmin {
		result.RuntimeImageRef = row.App.RuntimeImageRef
		result.RuntimeImageSha256 = row.App.RuntimeImageSha256
	}
	return result, nil
}

// ListByOrg 列出组织内的应用。
func (s *AppService) ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AppResult, error) {
	if !auth.CanViewOrg(principal, orgID) {
		return nil, ErrForbidden
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.store.ListAppsByOrgWithVersion(ctx, sqlc.ListAppsByOrgWithVersionParams{OrgID: orgID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询应用列表失败: %w", err)
	}
	results := make([]AppResult, 0, len(rows))
	for _, row := range rows {
		// 组织成员只能在列表中看到自己拥有的应用。
		// schema 上每个用户最多一个活跃应用，分页含义对该角色无影响。
		if principal.Role == domain.UserRoleOrgMember && principal.UserID != row.App.OwnerUserID {
			continue
		}
		r := toAppResult(row.App)
		// version_synced：修订 + 镜像双维度对比，判断实例是否需要重启。
		r.VersionSynced = computeVersionSynced(row.App, row.VersionRevision, row.VersionImageID, s.imageResolver)
		results = append(results, r)
	}
	return results, nil
}

func toAppResult(app sqlc.App) AppResult {
	// spec-A2b：runtime_node_id / container_id / container_name 已从 schema 删除，不再映射。
	result := AppResult{
		ID:                  app.ID,
		OrgID:               app.OrgID,
		OwnerUserID:         app.OwnerUserID,
		Name:                app.Name,
		Status:              app.Status,
		APIKeyStatus:        app.ApiKeyStatus,
		KnowledgeQuotaBytes: app.KnowledgeQuotaBytes,
	}
	if app.Description.Valid {
		result.Description = app.Description.String
	}
	if app.NewapiKeyID.Valid {
		// schema 上 newapi_key_id 是 text，但 manager 写入的恒是 int64 字符串。
		// 解析失败一律视为未绑定，避免污染 service 层。
		if id, err := strconv.ParseInt(app.NewapiKeyID.String, 10, 64); err == nil {
			result.NewapiKeyID = id
		}
	}
	// 进度三字段：null.Int Valid=false 时 .Int64 为零值，正好与 omitempty 对齐。
	// 单位由 status 决定（拉取镜像走字节，启动容器走秒），service 层不做语义换算。
	result.ProgressCurrent = intOrZero(app.ProgressCurrent)
	result.ProgressTotal = intOrZero(app.ProgressTotal)
	result.LastErrorStatus = strOrEmpty(app.LastErrorStatus)
	result.LastErrorMessage = strOrEmpty(app.LastErrorMessage)
	// VersionID：Valid=false 时跳过（历史数据无版本绑定）。
	if app.VersionID.Valid {
		result.VersionID = app.VersionID.String
	}
	return result
}

// computeVersionSynced 判断实例运行时是否已与绑定版本对齐：
// 修订一致，且已应用镜像 ref 与版本 image_id 的解析结果一致。
// resolver 为 nil（未注入）时无法校验镜像维度，降级为仅比较修订。
func computeVersionSynced(app sqlc.App, versionRevision int32, versionImageID string, resolver AppImageResolver) bool {
	if app.AppliedVersionRevision != versionRevision {
		return false
	}
	if resolver == nil {
		return true
	}
	ref, ok := resolver.ResolveRuntimeImage(versionImageID)
	return ok && app.AppliedImageRef == ref
}

// SwitchAppVersion 切换实例绑定的助手版本。
// 校验调用者可通过 CanSwitchAppVersion（平台管理员、本组织管理员或实例 owner）、目标版本在实例所属组织的 allowlist 内，
// 写入新 version_id 后返回最新实例视图——SetAppVersion 切换时清零 applied_*，切换后 version_synced 必为 false，提示需重启。
func (s *AppService) SwitchAppVersion(ctx context.Context, principal auth.Principal, appID, versionID string) (AppResult, error) {
	// 加载实例及绑定版本信息，用于权限校验和 version_synced 计算。
	row, err := s.store.GetAppWithVersion(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	// 权限校验：平台管理员、本组织管理员或实例 owner 成员可切换版本。
	if !auth.CanSwitchAppVersion(principal, row.App.OrgID, row.App.OwnerUserID) {
		return AppResult{}, ErrForbidden
	}
	// 加载组织记录，用于 allowlist 校验。
	org, err := s.store.GetOrganization(ctx, row.App.OrgID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询企业失败: %w", err)
	}
	// 目标版本必须在组织 allowlist 内，否则拒绝切换。
	if !versionInOrgAllowlist(org, versionID) {
		return AppResult{}, ErrVersionNotInAllowlist
	}
	// 写入新 version_id，SetAppVersion 同步清零 applied_*，确保切换后必然进入需重启态。
	if err := s.store.SetAppVersion(ctx, sqlc.SetAppVersionParams{ID: row.App.ID, VersionID: null.StringFrom(versionID)}); err != nil {
		return AppResult{}, fmt.Errorf("切换助手版本失败: %w", err)
	}
	// 重新加载而非复用写前行：新 version_id 绑定的版本可能在并发写入时已推进 revision / image_id，
	// 重读可获取最新版本数据，确保 version_synced 基于新版本的真实状态计算。
	newRow, err := s.store.GetAppWithVersion(ctx, appID)
	if err != nil {
		return AppResult{}, fmt.Errorf("重新查询应用失败: %w", err)
	}
	result := toAppResult(newRow.App)
	result.VersionSynced = computeVersionSynced(newRow.App, newRow.VersionRevision, newRow.VersionImageID, s.imageResolver)
	return result, nil
}

// UpdateAppKnowledgeQuota 更新单个实例的知识库累计容量上限。
func (s *AppService) UpdateAppKnowledgeQuota(ctx context.Context, principal auth.Principal, appID string, quotaBytes int64) (AppResult, error) {
	// 容量上限必须为正数；允许低于当前已用，后续上传路径负责拦截超额写入。
	if err := validateKnowledgeQuotaBytes(quotaBytes); err != nil {
		return AppResult{}, err
	}
	// 先读取实例所属组织，用于容量编辑权限校验和更新后的版本同步计算。
	row, err := s.store.GetAppWithVersion(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanUpdateAppKnowledgeQuota(principal, row.App.OrgID) {
		return AppResult{}, ErrForbidden
	}
	if err := s.store.SetAppKnowledgeQuota(ctx, sqlc.SetAppKnowledgeQuotaParams{
		ID:                  row.App.ID,
		KnowledgeQuotaBytes: quotaBytes,
	}); err != nil {
		return AppResult{}, fmt.Errorf("更新实例知识库容量失败: %w", err)
	}
	// 重新读取数据库结果，确保返回值包含数据库触发器或并发更新后的最新实例状态。
	newRow, err := s.store.GetAppWithVersion(ctx, appID)
	if err != nil {
		return AppResult{}, fmt.Errorf("重新查询应用失败: %w", err)
	}
	result := toAppResult(newRow.App)
	result.VersionSynced = computeVersionSynced(newRow.App, newRow.VersionRevision, newRow.VersionImageID, s.imageResolver)
	// runtime image 信息只暴露给平台管理员，用于运维排障；企业管理员不能看到节点内部镜像细节。
	if principal.Role == domain.UserRolePlatformAdmin {
		result.RuntimeImageRef = newRow.App.RuntimeImageRef
		result.RuntimeImageSha256 = newRow.App.RuntimeImageSha256
	}
	return result, nil
}
