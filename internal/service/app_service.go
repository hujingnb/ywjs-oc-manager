package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AppStore 抽象 app 服务的数据访问能力。
type AppStore interface {
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) (sqlc.App, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID pgtype.UUID) (sqlc.App, error)
	ListAppsByOrg(ctx context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error)
	SetAppModel(ctx context.Context, arg sqlc.SetAppModelParams) (sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	SoftDeleteApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// AppTxRunner 抽象实例模型修改所需的事务边界。
type AppTxRunner interface {
	WithAppTx(ctx context.Context, fn func(AppStore) error) error
}

// AppService 维护应用的查询和状态读取。
// 创建应用必须经过 onboarding 事务，因为应用的初始化需要联动 channel binding、audit、job。
type AppService struct {
	store    AppStore
	txRunner AppTxRunner
	notifier JobNotifier
}

// NewAppService 创建 app 服务。
func NewAppService(store AppStore) *AppService { return &AppService{store: store} }

// SetJobNotifier 注入即时 job 入队器；nil 时只写 jobs 表，由 scheduler 周期兜底。
func (s *AppService) SetJobNotifier(notifier JobNotifier) {
	s.notifier = notifier
}

// SetTxRunner 注入事务执行器；模型修改需要把 model、job、audit 作为同一提交边界。
func (s *AppService) SetTxRunner(txRunner AppTxRunner) {
	s.txRunner = txRunner
}

// AppResult 是对外的应用视图。
type AppResult struct {
	ID            string `json:"id"`
	OrgID         string `json:"org_id"`
	OwnerUserID   string `json:"owner_user_id"`
	RuntimeNodeID string `json:"runtime_node_id,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Status        string `json:"status"`
	PersonaMode   string `json:"persona_mode"`
	AppPrompt     string `json:"app_prompt,omitempty"`
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
}

// AppModelUpdateResult 是修改实例模型后的响应；App 字段返回已保存的新模型视图。
type AppModelUpdateResult struct {
	App AppResult `json:"app"`
	// RestartJobID 是已有容器实例提交的重启任务 ID；无容器时为空。
	RestartJobID string `json:"restart_job_id,omitempty"`
	// RequiresRestart 表示本次模型修改是否需要通过重启容器生效。
	RequiresRestart bool `json:"requires_restart"`
}

// Get 查询应用。
func (s *AppService) Get(ctx context.Context, principal auth.Principal, appID string) (AppResult, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return AppResult{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return AppResult{}, ErrForbidden
	}
	result := toAppResult(app)
	// runtime_image_ref / sha256 含节点内部镜像信息，仅对平台管理员开放。
	if principal.Role == domain.UserRolePlatformAdmin {
		result.RuntimeImageRef = app.RuntimeImageRef
		result.RuntimeImageSha256 = app.RuntimeImageSha256
	}
	return result, nil
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
	apps, err := s.store.ListAppsByOrg(ctx, sqlc.ListAppsByOrgParams{OrgID: id, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询应用列表失败: %w", err)
	}
	results := make([]AppResult, 0, len(apps))
	for _, app := range apps {
		// 组织成员只能在列表中看到自己拥有的应用。
		// schema 上每个用户最多一个活跃应用，分页含义对该角色无影响。
		if principal.Role == domain.UserRoleOrgMember && principal.UserID != uuidToString(app.OwnerUserID) {
			continue
		}
		results = append(results, toAppResult(app))
	}
	return results, nil
}

// UpdateModel 修改实例模型；已有容器的实例会提交重启任务让新模型生效。
func (s *AppService) UpdateModel(ctx context.Context, principal auth.Principal, appID, modelID string) (AppModelUpdateResult, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return AppModelUpdateResult{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || app.DeletedAt.Valid {
		return AppModelUpdateResult{}, ErrNotFound
	}
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if err := s.ensurePrincipalActive(ctx, principal); err != nil {
		return AppModelUpdateResult{}, err
	}
	if !auth.CanTriggerRuntimeOperation(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return AppModelUpdateResult{}, ErrForbidden
	}
	org, err := s.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	normalizedModelID, err := ensureModelAllowed(org, modelID)
	if err != nil {
		return AppModelUpdateResult{}, err
	}
	if normalizedModelID == app.ModelID {
		return AppModelUpdateResult{App: toAppResult(app)}, nil
	}
	var result AppModelUpdateResult
	write := func(store AppStore) error {
		updated, err := store.SetAppModel(ctx, sqlc.SetAppModelParams{ID: app.ID, ModelID: normalizedModelID})
		if err != nil {
			return fmt.Errorf("更新实例模型失败: %w", err)
		}
		result = AppModelUpdateResult{App: toAppResult(updated)}
		if !app.ContainerID.Valid || app.ContainerID.String == "" {
			return nil
		}
		payload, err := json.Marshal(map[string]any{
			"app_id":       uuidToString(app.ID),
			"operation":    string(RuntimeOperationRestart),
			"runtime_node": uuidToOptionalString(app.RuntimeNodeID),
			"requested_by": principal.UserID,
		})
		if err != nil {
			return fmt.Errorf("序列化重启任务 payload 失败: %w", err)
		}
		job, err := store.CreateJob(ctx, sqlc.CreateJobParams{
			Type:        domain.JobTypeAppRestartContainer,
			Priority:    100,
			RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			MaxAttempts: 3,
			PayloadJson: payload,
		})
		if err != nil {
			return fmt.Errorf("创建模型生效重启任务失败: %w", err)
		}
		actorUUID, _ := optionalUUID(principal.UserID)
		metadata, _ := json.Marshal(map[string]any{
			"old_model_id":   app.ModelID,
			"new_model_id":   normalizedModelID,
			"restart_job_id": uuidToString(job.ID),
		})
		if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ActorID:      actorUUID,
			ActorRole:    principal.Role,
			OrgID:        app.OrgID,
			TargetType:   "app",
			TargetID:     uuidToString(app.ID),
			Action:       "update_model",
			Result:       "succeeded",
			MetadataJson: metadata,
		}); err != nil {
			return fmt.Errorf("写入模型修改审计日志失败: %w", err)
		}
		result.RestartJobID = uuidToString(job.ID)
		result.RequiresRestart = true
		return nil
	}
	if s.txRunner != nil {
		err = s.txRunner.WithAppTx(ctx, write)
	} else {
		err = write(s.store)
	}
	if err != nil {
		return AppModelUpdateResult{}, err
	}
	if s.notifier != nil && result.RestartJobID != "" {
		// Redis 即时入队失败不回滚模型修改和 job 创建，scheduler 会周期性扫描 pending job 兜底。
		_ = s.notifier.Enqueue(ctx, result.RestartJobID)
	}
	return result, nil
}

// ensurePrincipalActive 拒绝已禁用用户在 token 未过期期间继续修改实例模型。
func (s *AppService) ensurePrincipalActive(ctx context.Context, principal auth.Principal) error {
	id, err := parseUUID(principal.UserID)
	if err != nil {
		return ErrForbidden
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("查询主体状态失败: %w", err)
	}
	if user.Status == domain.StatusDisabled {
		return ErrForbidden
	}
	return nil
}

func toAppResult(app sqlc.App) AppResult {
	result := AppResult{
		ID:           uuidToString(app.ID),
		OrgID:        uuidToString(app.OrgID),
		OwnerUserID:  uuidToString(app.OwnerUserID),
		Name:         app.Name,
		Status:       app.Status,
		PersonaMode:  app.PersonaMode,
		ModelID:      app.ModelID,
		APIKeyStatus: app.ApiKeyStatus,
	}
	if app.RuntimeNodeID.Valid {
		result.RuntimeNodeID = uuidToOptionalString(app.RuntimeNodeID)
	}
	if app.Description.Valid {
		result.Description = app.Description.String
	}
	if app.AppPrompt.Valid {
		result.AppPrompt = app.AppPrompt.String
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
	return result
}
