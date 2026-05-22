package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AppStore 抽象 app 服务的数据访问能力。
type AppStore interface {
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) (sqlc.App, error)
	// GetAppWithVersion 联查实例与绑定版本的 revision / image_id，用于计算 version_synced。
	GetAppWithVersion(ctx context.Context, id pgtype.UUID) (sqlc.GetAppWithVersionRow, error)
	// ListAppsByOrgWithVersion 批量联查组织实例及绑定版本信息，用于 version_synced 批量计算。
	ListAppsByOrgWithVersion(ctx context.Context, arg sqlc.ListAppsByOrgWithVersionParams) ([]sqlc.ListAppsByOrgWithVersionRow, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	SoftDeleteApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
	// GetOrganization 按 id 加载组织记录，用于 allowlist 校验等场景。
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	// SetAppVersion 更新实例绑定的助手版本 id，返回更新后的实例记录。
	SetAppVersion(ctx context.Context, arg sqlc.SetAppVersionParams) (sqlc.App, error)
}

// AppImageResolver 把版本 image_id 解析成镜像 ref，用于计算 version_synced 的镜像维度。
// 由 cmd/server 用 hermes.runtime_images 配置适配注入。
type AppImageResolver interface {
	ResolveRuntimeImage(id string) (ref string, ok bool)
}

// AppService 维护应用的查询和状态读取。
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
type AppResult struct {
	ID            string `json:"id"`
	OrgID         string `json:"org_id"`
	OwnerUserID   string `json:"owner_user_id"`
	RuntimeNodeID string `json:"runtime_node_id,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Status        string `json:"status"`
	ModelID       string `json:"model_id"`
	ContainerID   string `json:"container_id,omitempty"`
	APIKeyStatus  string `json:"api_key_status"`
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
	// ModelSynced 标记实例运行中的模型是否与数据库记录一致；false 表示需重启生效。
	ModelSynced bool `json:"model_synced"`
	// VersionID 是实例绑定的助手版本 id；空表示未绑定（仅历史数据）。
	VersionID string `json:"version_id,omitempty"`
	// VersionSynced 标记实例运行时是否已与绑定版本对齐（修订 + 镜像都一致）；
	// false 表示版本被编辑过，需重启实例生效。
	VersionSynced bool `json:"version_synced"`
}

// Get 查询应用。
func (s *AppService) Get(ctx context.Context, principal auth.Principal, appID string) (AppResult, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return AppResult{}, ErrNotFound
	}
	row, err := s.store.GetAppWithVersion(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, uuidToString(row.App.OrgID), uuidToString(row.App.OwnerUserID)) {
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
	return filterAppResultByRole(result, principal), nil
}

// ListByOrg 列出组织内的应用。
func (s *AppService) ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AppResult, error) {
	if !auth.CanViewOrg(principal, orgID) {
		return nil, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
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
	rows, err := s.store.ListAppsByOrgWithVersion(ctx, sqlc.ListAppsByOrgWithVersionParams{OrgID: id, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询应用列表失败: %w", err)
	}
	results := make([]AppResult, 0, len(rows))
	for _, row := range rows {
		// 组织成员只能在列表中看到自己拥有的应用。
		// schema 上每个用户最多一个活跃应用，分页含义对该角色无影响。
		if principal.Role == domain.UserRoleOrgMember && principal.UserID != uuidToString(row.App.OwnerUserID) {
			continue
		}
		r := toAppResult(row.App)
		// version_synced：修订 + 镜像双维度对比，判断实例是否需要重启。
		r.VersionSynced = computeVersionSynced(row.App, row.VersionRevision, row.VersionImageID, s.imageResolver)
		results = append(results, filterAppResultByRole(r, principal))
	}
	return results, nil
}

func toAppResult(app sqlc.App) AppResult {
	result := AppResult{
		ID:           uuidToString(app.ID),
		OrgID:        uuidToString(app.OrgID),
		OwnerUserID:  uuidToString(app.OwnerUserID),
		Name:         app.Name,
		Status:       app.Status,
		ModelID:      app.ModelID,
		APIKeyStatus: app.ApiKeyStatus,
		ModelSynced:  app.ModelSynced,
	}
	if app.RuntimeNodeID.Valid {
		result.RuntimeNodeID = uuidToOptionalString(app.RuntimeNodeID)
	}
	if app.Description.Valid {
		result.Description = app.Description.String
	}
	if app.ContainerID.Valid {
		result.ContainerID = app.ContainerID.String
	}
	if app.NewapiKeyID.Valid {
		// schema 上 newapi_key_id 是 text，但 manager 写入的恒是 int64 字符串。
		// 解析失败一律视为未绑定，避免污染 service 层。
		if id, err := strconv.ParseInt(app.NewapiKeyID.String, 10, 64); err == nil {
			result.NewapiKeyID = id
		}
	}
	// 进度三字段：pgtype Valid=false 时 .Int64/.String 为零值，正好与 omitempty 对齐。
	// 单位由 status 决定（拉取镜像走字节，启动容器走秒），service 层不做语义换算。
	result.ProgressCurrent = app.ProgressCurrent.Int64
	result.ProgressTotal = app.ProgressTotal.Int64
	result.LastErrorStatus = app.LastErrorStatus.String
	result.LastErrorMessage = app.LastErrorMessage.String
	// VersionID：Valid=false 时跳过（历史数据无版本绑定）。
	if app.VersionID.Valid {
		result.VersionID = uuidToString(app.VersionID)
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

// filterAppResultByRole 根据调用者角色过滤敏感字段；非平台管理员不可见模型信息。
func filterAppResultByRole(result AppResult, principal auth.Principal) AppResult {
	if principal.Role != domain.UserRolePlatformAdmin {
		result.ModelID = ""
	}
	return result
}

// SwitchAppVersion 切换实例绑定的助手版本。
// 校验调用者可管理该实例、目标版本在实例所属组织的 allowlist 内，写入新 version_id 后
// 返回最新实例视图——SetAppVersion 切换时清零 applied_*，切换后 version_synced 必为 false，提示需重启。
func (s *AppService) SwitchAppVersion(ctx context.Context, principal auth.Principal, appID, versionID string) (AppResult, error) {
	// 解析实例 id；格式非法时等同于资源不存在。
	id, err := parseUUID(appID)
	if err != nil {
		return AppResult{}, ErrNotFound
	}
	// 加载实例及绑定版本信息，用于权限校验和 version_synced 计算。
	row, err := s.store.GetAppWithVersion(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	// 权限校验：仅组织管理员（本组织）或实例 owner 成员可管理；平台管理员无写权限。
	if !auth.CanManageApp(principal, uuidToString(row.App.OrgID), uuidToString(row.App.OwnerUserID)) {
		return AppResult{}, ErrForbidden
	}
	// 加载组织记录，用于 allowlist 校验。
	org, err := s.store.GetOrganization(ctx, row.App.OrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	// 目标版本必须在组织 allowlist 内，否则拒绝切换。
	if !versionInOrgAllowlist(org, versionID) {
		return AppResult{}, ErrVersionNotInAllowlist
	}
	// allowlist 内的 id 必然是合法 UUID；解析失败视为输入非法，同样拒绝。
	versionUUID, err := parseUUID(versionID)
	if err != nil {
		return AppResult{}, ErrVersionNotInAllowlist
	}
	// 写入新 version_id，并由 SetAppVersion 同步清零 applied_*，确保切换后必然进入需重启态。
	if _, err := s.store.SetAppVersion(ctx, sqlc.SetAppVersionParams{ID: row.App.ID, VersionID: versionUUID}); err != nil {
		return AppResult{}, fmt.Errorf("切换助手版本失败: %w", err)
	}
	// 重新加载而非复用写前行：新 version_id 绑定的版本可能在并发写入时已推进 revision / image_id，
	// 重读可获取最新版本数据，确保 version_synced 基于新版本的真实状态计算。
	newRow, err := s.store.GetAppWithVersion(ctx, id)
	if err != nil {
		return AppResult{}, fmt.Errorf("重新查询应用失败: %w", err)
	}
	result := toAppResult(newRow.App)
	result.VersionSynced = computeVersionSynced(newRow.App, newRow.VersionRevision, newRow.VersionImageID, s.imageResolver)
	return filterAppResultByRole(result, principal), nil
}
