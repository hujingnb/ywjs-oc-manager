package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

// AppStore 抽象 app 服务的数据访问能力。
type AppStore interface {
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) error
	// MarkAppAICCHidden 将 AICC 自动创建的隐藏 app 从普通应用列表中隔离。
	MarkAppAICCHidden(ctx context.Context, id string) error
	// GetUser 读取创建者语言偏好，用于隐藏 app 初始化时快照 locale。
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	// GetAppWithVersion 联查实例与绑定版本的 revision / image_id，用于计算 version_synced。
	GetAppWithVersion(ctx context.Context, id string) (sqlc.GetAppWithVersionRow, error)
	// ListAppsByOrgWithVersion 批量联查组织实例及绑定版本信息，用于 version_synced 批量计算。
	ListAppsByOrgWithVersion(ctx context.Context, arg sqlc.ListAppsByOrgWithVersionParams) ([]sqlc.ListAppsByOrgWithVersionRow, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	SoftDeleteApp(ctx context.Context, id string) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	// GetOrganization 按 id 加载组织记录，用于 allowlist 校验等场景。
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// SetAppVersion 更新实例绑定的助手版本 id，返回更新后的实例记录。
	SetAppVersion(ctx context.Context, arg sqlc.SetAppVersionParams) error
	// UpdateAppLocale 更新实例语言偏好（hermes 对终端用户说话的语言）。
	UpdateAppLocale(ctx context.Context, arg sqlc.UpdateAppLocaleParams) error
	// GetWebPublishConfig 查询企业 web-publish 开通配置；用于判断实例是否「能力已开通但需重启生效」。
	// 企业未配置时返回 sql.ErrNoRows。
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
}

// AppImageResolver 把版本 image_id 解析成镜像 ref，用于计算 version_synced 的镜像维度。
// 由 cmd/server 用 hermes.runtime_images 配置适配注入。
type AppImageResolver interface {
	ResolveRuntimeImage(id string) (ref string, ok bool)
}

// configOps 抽象 oc-ops 的实例运行配置查询，供 AppLocaleStatus 实时读取实例当前语言。
// 方法签名镜像 *ocops.Client.Config；由 *ocops.Client 满足（见编译期断言）。
type configOps interface {
	Config(ctx context.Context, ep ocops.Endpoint) (ocops.OcConfig, error)
}

// 编译期断言：生产实现 *ocops.Client 必须满足 configOps 窄接口，方法签名漂移即编译失败。
var _ configOps = (*ocops.Client)(nil)

// AppService 维护应用的查询、状态读取和轻量配置更新。
// 创建应用必须经过 onboarding 事务，因为应用的初始化需要联动 channel binding、audit、job。
type AppService struct {
	store    AppStore
	notifier JobNotifier
	// imageResolver 把版本 image_id 解析成镜像 ref，供 computeVersionSynced 比对镜像维度。
	// nil 时降级为仅比较修订号（镜像维度跳过）。
	imageResolver AppImageResolver
	// configOps 是 oc-ops 实例配置查询客户端，AppLocaleStatus 用它实时读取实例当前语言。
	// nil 时无法实时查询，current_language 一律返回 nil（不阻断详情渲染）。
	configOps configOps
	// ocResolver 把 appID 解析为 oc-ops 调用坐标（基址 + per-app token）。
	// 复用与 cron/kanban/conversation 一致的 endpoint 构造路径，避免自造寻址逻辑。
	ocResolver OcOpsResolver
}

// NewAppService 创建 app 服务。
func NewAppService(store AppStore) *AppService { return &AppService{store: store} }

// SetJobNotifier 注入即时 job 入队器；nil 时只写 jobs 表，由 scheduler 周期兜底。
func (s *AppService) SetJobNotifier(notifier JobNotifier) {
	s.notifier = notifier
}

// SetImageResolver 注入版本镜像解析能力；nil 时 version_synced 降级为仅比较修订。
func (s *AppService) SetImageResolver(resolver AppImageResolver) { s.imageResolver = resolver }

// SetOcOps 注入实时查询实例运行配置所需的 oc-ops 客户端与坐标解析器，供 AppLocaleStatus 使用。
// 两者任一为 nil 时，AppLocaleStatus 跳过实时查询、current_language 返回 nil（不阻断详情渲染）。
func (s *AppService) SetOcOps(ops configOps, resolver OcOpsResolver) {
	s.configOps = ops
	s.ocResolver = resolver
}

// AppResult 是对外的应用视图。
// spec-A2b：runtime_node_id / container_id / container_name 已从 schema 删除，本结构体亦不再携带。
type AppResult struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	OwnerUserID string `json:"owner_user_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	// RuntimePhase 是运行时就绪维度(与 status 正交):ready/starting/restarting/unknown。
	// 前端发起闸门 = status allowlist 且 runtime_phase==ready;非 ready 时按 phase 展示
	// 正在启动 / 重启中 / 状态确认中。
	RuntimePhase string `json:"runtime_phase"`
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
	// WebPublishPendingRestart 标记「企业已开通 web-publish，但本实例尚未注入发布能力」——
	// 即实例在企业开通前就已运行，需重启重新 bootstrap 才能获得发布能力。
	// true 时前端在概览页提示「能力已开通，需重启实例生效」并提供重启入口。
	WebPublishPendingRestart bool `json:"web_publish_pending_restart"`
	// PlatformPromptPendingRestart 标记「平台层身份 prompt 常量已更新，但本实例上次 bootstrap
	// 写入的是旧文本」——需重启重渲染 SOUL.md 平台层才能生效。
	PlatformPromptPendingRestart bool `json:"platform_prompt_pending_restart"`
	// Locale 是 hermes bot 对终端用户说话的语言（en/zh）；
	// 空表示使用平台默认语言（历史数据或未设置）。
	Locale string `json:"locale,omitempty"`
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
	// web_publish_pending_restart：企业已开通 web-publish（enabled + provisioning ready）但本实例
	// 上次 bootstrap 未注入发布能力（web_publish_applied=false）→ 需重启使能力生效。
	// 企业未配置/未开通（含 sql.ErrNoRows）或查询出错时一律视为不需提示，避免误报。
	result.WebPublishPendingRestart = s.computeWebPublishPendingRestart(ctx, row.App)
	// platform_prompt_pending_restart：实例上次 bootstrap stamp 的平台 prompt hash 与当前常量
	// hash 不一致（含存量实例 applied 为空）→ 需重启重渲染 SOUL.md 平台层。
	result.PlatformPromptPendingRestart = computePlatformPromptPendingRestart(row.App)
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

// CreateHiddenAICCApp 创建 AICC 智能体专用隐藏 app，并复用现有 app_initialize worker 初始化链路。
//
// 取舍说明：成员 onboarding 会同时创建成员、渠道绑定和审计，AICC 只需要一个 hermes runtime，
// 因此这里保留最小共享边界：创建 apps 行、创建 app_initialize job、标记 aicc_hidden。
// new-api token、runtime token、k8s Deployment、知识注入等细节继续由 app_initialize worker 统一处理。
func (s *AppService) CreateHiddenAICCApp(ctx context.Context, principal auth.Principal, input AICCHiddenAppInput) (string, error) {
	if input.AppID == "" || input.OrgID == "" || input.UserID == "" || strings.TrimSpace(input.Name) == "" {
		return "", fmt.Errorf("%w: AICC 隐藏 app 缺少必要参数", ErrInvalidArgument)
	}
	if !auth.CanManageAICCAgent(principal, input.OrgID) {
		return "", ErrForbidden
	}
	org, err := s.store.GetOrganization(ctx, input.OrgID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("查询企业失败: %w", err)
	}
	versionID, err := firstAssistantVersionID(org)
	if err != nil {
		return "", err
	}
	appLocale := null.String{}
	if user, err := s.store.GetUser(ctx, input.UserID); err == nil && user.Locale.Valid && user.Locale.String != "" {
		appLocale = null.StringFrom(user.Locale.String)
	}
	if err := s.store.CreateApp(ctx, sqlc.CreateAppParams{
		ID:                  input.AppID,
		OrgID:               input.OrgID,
		OwnerUserID:         input.UserID,
		Name:                strings.TrimSpace(input.Name),
		Description:         null.String{},
		Status:              domain.AppStatusDraft,
		ApiKeyStatus:        domain.APIKeyStatusPending,
		VersionID:           null.StringFrom(versionID),
		Locale:              appLocale,
		KnowledgeQuotaBytes: org.DefaultAppKnowledgeQuotaBytes,
		AiccHidden:          true,
	}); err != nil {
		return "", fmt.Errorf("创建 AICC 隐藏 app 失败: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"app_id": input.AppID})
	if err != nil {
		return "", fmt.Errorf("序列化 AICC 初始化 job payload 失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeAppInitialize,
		Priority:    100,
		RunAfter:    time.Now(),
		MaxAttempts: 5,
		PayloadJson: payload,
	}); err != nil {
		return "", fmt.Errorf("创建 AICC 隐藏 app 初始化任务失败: %w", err)
	}
	if err := s.store.MarkAppAICCHidden(ctx, input.AppID); err != nil {
		return "", fmt.Errorf("标记 AICC 隐藏 app 失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return input.AppID, nil
}

func toAppResult(app sqlc.App) AppResult {
	// spec-A2b：runtime_node_id / container_id / container_name 已从 schema 删除，不再映射。
	result := AppResult{
		ID:                  app.ID,
		OrgID:               app.OrgID,
		OwnerUserID:         app.OwnerUserID,
		Name:                app.Name,
		Status:              app.Status,
		RuntimePhase:        app.RuntimePhase,
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
	// Locale：NULL 时省略（由 omitempty 控制），前端按平台默认值处理。
	if app.Locale.Valid {
		result.Locale = app.Locale.String
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

// computeWebPublishPendingRestart 判断实例是否「企业已开通 web-publish 但本实例尚未注入发布能力，需重启生效」。
// 条件：企业 web-publish 已开通且 provisioning ready，且实例最近一次 bootstrap 未注入（web_publish_applied=false）。
// 企业未配置（sql.ErrNoRows）/未开通/未就绪/查询出错 → 一律返回 false（不提示），避免误报。
func (s *AppService) computeWebPublishPendingRestart(ctx context.Context, app sqlc.App) bool {
	if app.WebPublishApplied {
		return false // 实例已注入发布能力，无需重启
	}
	cfg, err := s.store.GetWebPublishConfig(ctx, app.OrgID)
	if err != nil {
		return false // 含 sql.ErrNoRows（企业未配置）：不提示
	}
	return cfg.Enabled && cfg.ProvisioningStatus == domain.ProvisioningReady
}

// computePlatformPromptPendingRestart 判断实例是否「平台 prompt 已更新需重启」：
// 上次 bootstrap stamp 的 applied_platform_prompt_hash 与当前常量 hash 不等即为真
// （空 hash 的存量实例天然不等，一律判为需重启）。
func computePlatformPromptPendingRestart(app sqlc.App) bool {
	return app.AppliedPlatformPromptHash != config.PlatformPromptHash()
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

// UpdateAppLocale 更新实例语言偏好（hermes bot 对终端用户说话的语言）。
//
// 持久化新 locale 后入队 app_restart_container job，让容器以新语言配置重新启动；
// 同时写入审计日志，记录操作者与目标语言。
//
// 权限：平台管理员、本组织管理员或实例 owner 可修改。
// 校验：locale 必须在 SupportedLocales 中，否则返回 ErrInvalidLocale。
func (s *AppService) UpdateAppLocale(ctx context.Context, principal auth.Principal, appID, locale string) (AppResult, error) {
	// 校验目标语言在受支持集合内。
	if !isSupportedLocale(locale) {
		return AppResult{}, ErrInvalidLocale
	}
	// 加载实例记录（含绑定版本信息，用于 version_synced 计算）。
	row, err := s.store.GetAppWithVersion(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	// 权限校验：平台管理员、本组织管理员或实例 owner 可修改语言。
	if !auth.CanUpdateAppLocale(principal, row.App.OrgID, row.App.OwnerUserID) {
		return AppResult{}, ErrForbidden
	}
	// 持久化新语言偏好。
	if err := s.store.UpdateAppLocale(ctx, sqlc.UpdateAppLocaleParams{
		ID:     row.App.ID,
		Locale: null.StringFrom(locale),
	}); err != nil {
		return AppResult{}, fmt.Errorf("更新实例语言失败: %w", err)
	}
	// 入队重启任务，让 hermes 容器以新语言配置重新初始化。
	// payload 与 runtime_operation_service.Trigger 的 restart 分支一致。
	restartPayload, err := json.Marshal(map[string]any{
		"app_id":       row.App.ID,
		"operation":    string(RuntimeOperationRestart),
		"requested_by": principal.UserID,
	})
	if err != nil {
		return AppResult{}, fmt.Errorf("序列化重启 payload 失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeAppRestartContainer,
		Priority:    100,
		RunAfter:    time.Now(),
		MaxAttempts: 3,
		PayloadJson: restartPayload,
	}); err != nil {
		return AppResult{}, fmt.Errorf("创建重启任务失败: %w", err)
	}
	// 写入审计日志；metadata 存储语言代码，前端按语言渲染详情文案。
	auditMeta, err := json.Marshal(map[string]any{
		"locale": locale,
	})
	if err != nil {
		return AppResult{}, fmt.Errorf("序列化审计元数据失败: %w", err)
	}
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:           newUUID(),
		ActorID:      null.StringFrom(principal.UserID),
		ActorRole:    principal.Role,
		OrgID:        null.StringFrom(row.App.OrgID),
		TargetType:   "app",
		TargetID:     row.App.ID,
		Action:       "update_locale",
		Result:       "succeeded",
		MetadataJson: auditMeta,
	}); err != nil {
		return AppResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	// 即时唤醒 worker（如有 notifier 注入）。
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	// 重新加载以确保返回值含最新状态。
	newRow, err := s.store.GetAppWithVersion(ctx, appID)
	if err != nil {
		return AppResult{}, fmt.Errorf("重新查询应用失败: %w", err)
	}
	result := toAppResult(newRow.App)
	result.VersionSynced = computeVersionSynced(newRow.App, newRow.VersionRevision, newRow.VersionImageID, s.imageResolver)
	return result, nil
}

// appLocaleStatusTimeout 是实时查询实例当前语言的最长等待时间。
// 实例未运行 / 不可达时，oc-ops 调用应在此时限内失败返回，避免详情页因实例宕机而长时间卡顿。
const appLocaleStatusTimeout = 3 * time.Second

// AppLocaleStatusResult 是实例语言状态查询结果。
//
// 设计铁律：current_language 必须实时取自实例侧（oc-ops），不读 manager DB 快照；
// desired_language 才是配置/期望值（apps.locale）。两者口径不同，不能互相替代。
type AppLocaleStatusResult struct {
	// CurrentLanguage 是实例当前真实运行的语言；实例未运行 / 不可达 / 超时时为 nil。
	CurrentLanguage *string
	// DesiredLanguage 是期望语言（apps.locale 配置值），合法读自 manager DB。
	DesiredLanguage string
	// NeedsRestart 表示运行中实例当前语言与期望不一致、需重启生效；
	// 仅当 CurrentLanguage 非 nil 且与 DesiredLanguage 不等时为 true。
	NeedsRestart bool
}

// AppLocaleStatus 返回实例语言状态：实时实例语言、期望语言与是否需重启。
//
// 流程：
//  1. 读 app（含绑定版本联查）取 desired = apps.locale，并按 Get 一致的 ErrNotFound 映射；
//  2. 用 CanViewApp 校验访问权限（与详情页同一谓词）；
//  3. 经 ocResolver 解析 oc-ops 坐标（复用 per-app token + Service DNS 构造），
//     用短超时 ctx 实时查 oc-ops Config，取 current = display.language。
//
// 实例未运行 / 不可达 / 超时 / 解析失败时，current 保持 nil 且不报错，确保详情页可正常渲染。
func (s *AppService) AppLocaleStatus(ctx context.Context, principal auth.Principal, appID string) (AppLocaleStatusResult, error) {
	// 读取 app（沿用 Get 的 GetAppWithVersion + ErrNoRows→ErrNotFound 映射）。
	row, err := s.store.GetAppWithVersion(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppLocaleStatusResult{}, ErrNotFound
	}
	if err != nil {
		return AppLocaleStatusResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	// 访问权限：与详情页一致用 CanViewApp（平台管理员 / 本组织 org_admin / 实例 owner）。
	if !auth.CanViewApp(principal, row.App.OrgID, row.App.OwnerUserID) {
		return AppLocaleStatusResult{}, ErrForbidden
	}
	// desired 合法读 DB：Locale 为 NULL（历史数据 / 未设置）时为空字符串，由前端按平台默认处理。
	result := AppLocaleStatusResult{DesiredLanguage: strOrEmpty(row.App.Locale)}
	// 未注入 oc-ops 能力（如 dev / 测试未配）时跳过实时查询，current 保持 nil。
	if s.configOps == nil || s.ocResolver == nil {
		return result, nil
	}
	// 解析 oc-ops 坐标：失败（实例不存在 / token 解密失败等）时不阻断详情渲染，current 仍为 nil。
	loc, err := s.ocResolver.Resolve(ctx, appID)
	if err != nil {
		return result, nil
	}
	// dev stub 实例不含真实 hermes，或基址未就绪（实例尚未运行）时无从实时查询，current 保持 nil。
	if !loc.Supported || loc.Endpoint.BaseURL == "" {
		return result, nil
	}
	// 实时查 oc-ops：短超时避免实例宕机时详情页长时间卡顿；任何错误均降级为 current=nil。
	queryCtx, cancel := context.WithTimeout(ctx, appLocaleStatusTimeout)
	defer cancel()
	cfg, err := s.configOps.Config(queryCtx, loc.Endpoint)
	if err == nil && cfg.DisplayLanguage != "" {
		cur := cfg.DisplayLanguage
		result.CurrentLanguage = &cur
		result.NeedsRestart = cur != result.DesiredLanguage
	}
	return result, nil
}
