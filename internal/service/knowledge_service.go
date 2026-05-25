package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"oc-manager/internal/auth"
	"oc-manager/internal/files"
	"oc-manager/internal/store/sqlc"
)

// KnowledgeSyncDispatcher 抽象向 worker 入队 knowledge_sync_node 任务的能力。
// 实现负责按写入对象（org / app）找到目标节点并去重生成 job。
type KnowledgeSyncDispatcher interface {
	DispatchOrgChange(ctx context.Context, orgID, relPath, changeType, masterPath string) error
	DispatchAppChange(ctx context.Context, orgID, appID, relPath, changeType, masterPath string) error
}

// KnowledgeAuditRecorder 抽象写一条 audit_logs 的能力。
// 与 service.AuditService.Record 同构,但 service 层不直接依赖具体实现,
// 测试可注入内存 fake。装配在 cmd/server,生产实现是 *service.AuditService。
//
// 用途:dispatcher 入队失败时记录,主副本写入成功不回滚,但失败痕迹必须可观测。
// 不返回 error——审计失败仅打日志,不影响主流程。
type KnowledgeAuditRecorder interface {
	Record(ctx context.Context, event AuditEvent) (AuditResult, error)
}

// KnowledgeSyncStatusSource 抽象按 org 取最近同步状态的能力。
// 由前端 OrgKnowledgePage 通过 GetOrgSyncStatus → 列表展示节点徽章。
type KnowledgeSyncStatusSource interface {
	ListByOrg(ctx context.Context, orgID string) ([]SyncStatusResult, error)
}

// KnowledgeRetryDispatcher 抽象「触发该 (org, node) 立即重试同步」的能力。
// dev 实现可走与首次入队相同的 dispatcher（DispatchOrgChange 用 noop change_type）；
// 简化版直接 enqueue 一个 'noop' job 让 worker 推 status=synced。
type KnowledgeRetryDispatcher interface {
	RetryOrgNode(ctx context.Context, orgID, nodeID string) error
}

// KnowledgeAppStore 抽象按 app_id 读取应用真实归属的能力。
// 应用知识库接口仍保留 org_id / owner_user_id query 以兼容前端缓存上下文，
// 但权限和路径拼接必须以数据库中的真实归属为准，不能信任客户端传入值。
type KnowledgeAppStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// KnowledgeService 维护组织和应用维度的知识库主副本。
//
// 设计要点：
//   - 主副本统一存放在 manager 容器内（bind mount 到宿主），各 runtime node 上的工作目录由 worker 同步；
//   - 路径必须经过 files.SafeRoot 校验，防止越权访问；
//   - 写入路径会按租户拆分：org/{orgID}/...、org/{orgID}/app/{appID}/...；
//   - 应用级同步在主副本写入失败时回滚（这里是文件操作，整体最多一次写入，不需要 SQL 事务）；
//   - 组织级同步走异步任务，不阻塞主流程。
//
// 同步状态：组织级 dispatcher 入队时写 pending、worker 完成时写 synced/failed，
// 由独立的 KnowledgeSyncStatusService（statusSource + retryDispatcher）维护。
type KnowledgeService struct {
	master          *files.KnowledgeMaster
	dispatcher      KnowledgeSyncDispatcher
	statusSource    KnowledgeSyncStatusSource
	retryDispatcher KnowledgeRetryDispatcher
	appStore        KnowledgeAppStore
	// auditor 用于把 dispatcher 入队失败落到 audit_logs;nil 时静默(仅打日志)。
	// 主副本已经写成功,业务不能因为同步失败而 200 → 500 翻转,但必须留可观测痕迹。
	auditor KnowledgeAuditRecorder
}

// NewKnowledgeService 创建知识库服务。
func NewKnowledgeService(master *files.KnowledgeMaster) *KnowledgeService {
	return &KnowledgeService{master: master}
}

// SetSyncDispatcher 注入同步分发器（可选）。
// 不注入时主副本仍正常写入，但不会触发节点同步——cmd/server 装配阶段必须传入。
func (s *KnowledgeService) SetSyncDispatcher(d KnowledgeSyncDispatcher) {
	s.dispatcher = d
}

// SetSyncStatusSource 注入同步状态读取器，让 GetOrgSyncStatus 暴露每节点状态。
func (s *KnowledgeService) SetSyncStatusSource(src KnowledgeSyncStatusSource) {
	s.statusSource = src
}

// SetRetryDispatcher 注入「重试该 (org, node) 同步」分发器。
func (s *KnowledgeService) SetRetryDispatcher(d KnowledgeRetryDispatcher) {
	s.retryDispatcher = d
}

// SetAppStore 注入应用归属查询器。
// 应用级知识库读写需要先按 app_id 查出真实 org / owner，再做权限判断。
func (s *KnowledgeService) SetAppStore(store KnowledgeAppStore) {
	s.appStore = store
}

// SetAuditor 注入 audit_logs 写入器,用于 dispatcher 入队失败时落痕。
// 不注入时静默,与旧装配兼容(仅日志告警)。
func (s *KnowledgeService) SetAuditor(a KnowledgeAuditRecorder) {
	s.auditor = a
}

// recordDispatchFailure 把 dispatcher 入队失败写到 audit_logs。
// 主副本已成功落盘,此处仅做"留痕",不阻断主流程,任何审计写入失败只打日志。
// target_type=knowledge_sync 与 worker handler 端的 app_knowledge_sync 形成
// 完整的"入队-执行"事件链,排障时按 org_id / target_id 串起来。
func (s *KnowledgeService) recordDispatchFailure(ctx context.Context, orgID, appID, relPath, action string, dispatchErr error) {
	slog.WarnContext(ctx, "知识库同步入队失败",
		"org_id", orgID, "app_id", appID, "rel_path", relPath, "action", action, "error", dispatchErr)
	if s.auditor == nil {
		return
	}
	targetID := orgID
	if appID != "" {
		targetID = appID
	}
	// 详情字段说明事件作用对象（组织级 vs 应用级 + 文件路径），方便审计列表筛选。
	detail := fmt.Sprintf("组织文件 %s", relPath)
	if appID != "" {
		detail = fmt.Sprintf("应用文件 %s", relPath)
	}
	event := AuditEvent{
		ActorRole:    "system",
		OrgID:        orgID,
		TargetType:   "knowledge_sync",
		TargetID:     targetID,
		Action:       action, // 例如 dispatch_org_upload_file / dispatch_app_delete_file
		Result:       "failed",
		ErrorMessage: dispatchErr.Error(),
		Metadata: map[string]any{
			"app_id":   appID,
			"rel_path": relPath,
		},
		DetailMessage: detail,
	}
	if _, err := s.auditor.Record(ctx, event); err != nil {
		slog.ErrorContext(ctx, "写 audit_logs 失败", "error", err)
	}
}

// GetOrgSyncStatus 列出组织在所有节点上的最近同步状态。
// 该状态属于组织知识库运维面，只允许本组织管理员查看。
func (s *KnowledgeService) GetOrgSyncStatus(ctx context.Context, principal auth.Principal, orgID string) ([]SyncStatusResult, error) {
	if !auth.CanViewOrgKnowledgeSyncStatus(principal, orgID) {
		return nil, ErrKnowledgeForbidden
	}
	if s.statusSource == nil {
		return []SyncStatusResult{}, nil
	}
	return s.statusSource.ListByOrg(ctx, orgID)
}

// RetryOrgNodeSync 触发指定 (org, node) 重新同步；通常由前端「重试同步」按钮调用。
// 重试会改变组织知识库同步状态，因此只允许本组织管理员执行。
func (s *KnowledgeService) RetryOrgNodeSync(ctx context.Context, principal auth.Principal, orgID, nodeID string) error {
	if !auth.CanRetryOrgKnowledgeSync(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	if s.retryDispatcher == nil {
		return fmt.Errorf("重试分发器未配置")
	}
	return s.retryDispatcher.RetryOrgNode(ctx, orgID, nodeID)
}

// KnowledgeListResult 是列表接口的返回。
type KnowledgeListResult struct {
	Path    string                 `json:"path"`
	Entries []KnowledgeEntryResult `json:"entries"`
}

// KnowledgeEntryResult 是对外的条目视图。
type KnowledgeEntryResult struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// SaveOrgFile 将文件写入指定组织的主副本。
func (s *KnowledgeService) SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string, content io.Reader, size int64) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, false)
	if err != nil {
		return err
	}
	target := path.Join("org", orgID, "knowledge", cleaned)
	if err := s.master.Save(target, content, size); err != nil {
		return err
	}
	if s.dispatcher != nil {
		if dispatchErr := s.dispatcher.DispatchOrgChange(ctx, orgID, cleaned, "upload_file", target); dispatchErr != nil {
			s.recordDispatchFailure(ctx, orgID, "", cleaned, "dispatch_org_upload_file", dispatchErr)
		}
	}
	return nil
}

// SaveAppFile 写入应用维度的知识库。
// 仅 owner 与本组织管理员可写，平台管理员只保留读取能力。
func (s *KnowledgeService) SaveAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string, content io.Reader, size int64) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	appCtx, err := s.resolveAppKnowledgeContext(ctx, appID, orgID, ownerUserID)
	if err != nil {
		return err
	}
	if !auth.CanWriteAppKnowledge(principal, appCtx.orgID, appCtx.ownerUserID) {
		return ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, false)
	if err != nil {
		return err
	}
	target := path.Join("org", appCtx.orgID, "app", appID, "knowledge", cleaned)
	if err := s.master.Save(target, content, size); err != nil {
		return err
	}
	if s.dispatcher != nil {
		if dispatchErr := s.dispatcher.DispatchAppChange(ctx, appCtx.orgID, appID, cleaned, "upload_file", target); dispatchErr != nil {
			s.recordDispatchFailure(ctx, appCtx.orgID, appID, cleaned, "dispatch_app_upload_file", dispatchErr)
		}
	}
	return nil
}

// DeleteOrgFile 删除组织级文件。
func (s *KnowledgeService) DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !auth.CanWriteOrgKnowledge(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, false)
	if err != nil {
		return err
	}
	target := path.Join("org", orgID, "knowledge", cleaned)
	if err := s.master.Delete(target); err != nil {
		return err
	}
	if s.dispatcher != nil {
		if dispatchErr := s.dispatcher.DispatchOrgChange(ctx, orgID, cleaned, "delete_file", target); dispatchErr != nil {
			s.recordDispatchFailure(ctx, orgID, "", cleaned, "dispatch_org_delete_file", dispatchErr)
		}
	}
	return nil
}

// DeleteAppFile 删除应用级文件。
func (s *KnowledgeService) DeleteAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	appCtx, err := s.resolveAppKnowledgeContext(ctx, appID, orgID, ownerUserID)
	if err != nil {
		return err
	}
	if !auth.CanWriteAppKnowledge(principal, appCtx.orgID, appCtx.ownerUserID) {
		return ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, false)
	if err != nil {
		return err
	}
	target := path.Join("org", appCtx.orgID, "app", appID, "knowledge", cleaned)
	if err := s.master.Delete(target); err != nil {
		return err
	}
	if s.dispatcher != nil {
		if dispatchErr := s.dispatcher.DispatchAppChange(ctx, appCtx.orgID, appID, cleaned, "delete_file", target); dispatchErr != nil {
			s.recordDispatchFailure(ctx, appCtx.orgID, appID, cleaned, "dispatch_app_delete_file", dispatchErr)
		}
	}
	return nil
}

type appKnowledgeContext struct {
	orgID       string
	ownerUserID string
}

func (s *KnowledgeService) resolveAppKnowledgeContext(ctx context.Context, appID, requestedOrgID, requestedOwnerUserID string) (appKnowledgeContext, error) {
	if s.appStore == nil {
		return appKnowledgeContext{}, ErrKnowledgeMissing
	}
	id, err := parseUUID(appID)
	if err != nil {
		return appKnowledgeContext{}, ErrNotFound
	}
	app, err := s.appStore.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return appKnowledgeContext{}, ErrNotFound
	}
	if err != nil {
		return appKnowledgeContext{}, fmt.Errorf("查询应用归属失败: %w", err)
	}
	if app.DeletedAt.Valid {
		return appKnowledgeContext{}, ErrNotFound
	}
	actual := appKnowledgeContext{
		orgID:       uuidToString(app.OrgID),
		ownerUserID: uuidToString(app.OwnerUserID),
	}
	// query 参数仅作为前端上下文一致性校验。权限判断与文件路径均使用数据库中的真实归属，
	// 防止调用方用 victim app_id + 自己的 owner_user_id 组合绕过应用知识库读取边界。
	if requestedOrgID != actual.orgID || requestedOwnerUserID != actual.ownerUserID {
		return appKnowledgeContext{}, ErrKnowledgeForbidden
	}
	return actual, nil
}

// validateKnowledgeRelative 在拼接可信租户前缀前校验用户传入路径。
// 所有知识库入口都必须先约束 relative 本身，否则 path.Join 会把 ../../secret.md 规范化成
// org/<orgID>/secret.md，导致 SafeRoot 只能保护全局根目录，不能保护知识库子树边界。
func validateKnowledgeRelative(relative string, allowEmpty bool) (string, error) {
	if relative == "" && allowEmpty {
		return "", nil
	}
	if strings.ContainsRune(relative, 0) {
		return "", fmt.Errorf("%w: 路径包含 NUL", files.ErrInvalidPath)
	}
	if path.IsAbs(relative) {
		return "", fmt.Errorf("%w: 不允许绝对路径", files.ErrInvalidPath)
	}
	decoded := relative
	for {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", fmt.Errorf("%w: %s", files.ErrInvalidPath, err.Error())
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	if strings.ContainsRune(decoded, 0) {
		return "", fmt.Errorf("%w: 路径包含 NUL", files.ErrInvalidPath)
	}
	if path.IsAbs(decoded) {
		return "", fmt.Errorf("%w: 不允许绝对路径", files.ErrInvalidPath)
	}
	for _, part := range strings.Split(decoded, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: 不允许的相对前缀", files.ErrInvalidPath)
		}
	}
	cleaned := path.Clean(decoded)
	if cleaned == "." {
		if allowEmpty {
			return "", nil
		}
		return "", files.ErrInvalidPath
	}
	return cleaned, nil
}

// OpenOrgFile 打开组织级知识库中的普通文件供下载。
// 下载属于读取能力，权限沿用 CanReadOrgKnowledge；写入和同步权限不参与判断。
func (s *KnowledgeService) OpenOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) (io.ReadCloser, int64, error) {
	if s.master == nil {
		return nil, 0, ErrKnowledgeMissing
	}
	if !auth.CanReadOrgKnowledge(principal, orgID) {
		return nil, 0, ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, false)
	if err != nil {
		return nil, 0, err
	}
	target := path.Join("org", orgID, "knowledge", cleaned)
	stream, size, err := s.master.Open(target)
	if err != nil {
		return nil, 0, fmt.Errorf("打开组织知识库文件失败: %w", err)
	}
	return stream, size, nil
}

// OpenAppFile 打开应用级知识库中的普通文件供下载。
// 下载属于读取能力，权限沿用 CanReadAppKnowledge；平台管理员保留跨组织观察和下载能力。
func (s *KnowledgeService) OpenAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (io.ReadCloser, int64, error) {
	if s.master == nil {
		return nil, 0, ErrKnowledgeMissing
	}
	appCtx, err := s.resolveAppKnowledgeContext(ctx, appID, orgID, ownerUserID)
	if err != nil {
		return nil, 0, err
	}
	if !auth.CanReadAppKnowledge(principal, appCtx.orgID, appCtx.ownerUserID) {
		return nil, 0, ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, false)
	if err != nil {
		return nil, 0, err
	}
	target := path.Join("org", appCtx.orgID, "app", appID, "knowledge", cleaned)
	stream, size, err := s.master.Open(target)
	if err != nil {
		return nil, 0, fmt.Errorf("打开应用知识库文件失败: %w", err)
	}
	return stream, size, nil
}

// ListOrg 列出组织级知识库；组织成员只读。
func (s *KnowledgeService) ListOrg(_ context.Context, principal auth.Principal, orgID, relative string) (KnowledgeListResult, error) {
	if s.master == nil {
		return KnowledgeListResult{}, ErrKnowledgeMissing
	}
	if !auth.CanReadOrgKnowledge(principal, orgID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, true)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	target := path.Join("org", orgID, "knowledge", cleaned)
	entries, err := s.master.List(target)
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("读取组织知识库失败: %w", err)
	}
	return toKnowledgeListResult(target, entries), nil
}

// ListApp 列出应用级知识库；只能由 owner 或更高权限读取。
func (s *KnowledgeService) ListApp(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (KnowledgeListResult, error) {
	if s.master == nil {
		return KnowledgeListResult{}, ErrKnowledgeMissing
	}
	appCtx, err := s.resolveAppKnowledgeContext(ctx, appID, orgID, ownerUserID)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	if !auth.CanReadAppKnowledge(principal, appCtx.orgID, appCtx.ownerUserID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	cleaned, err := validateKnowledgeRelative(relative, true)
	if err != nil {
		return KnowledgeListResult{}, err
	}
	target := path.Join("org", appCtx.orgID, "app", appID, "knowledge", cleaned)
	entries, err := s.master.List(target)
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("读取应用知识库失败: %w", err)
	}
	return toKnowledgeListResult(target, entries), nil
}

func toKnowledgeListResult(targetPath string, entries []files.KnowledgeEntry) KnowledgeListResult {
	out := make([]KnowledgeEntryResult, 0, len(entries))
	for _, entry := range entries {
		out = append(out, KnowledgeEntryResult{
			Path:  entry.Path,
			Name:  entry.Name,
			Size:  entry.Size,
			IsDir: entry.IsDir,
		})
	}
	return KnowledgeListResult{Path: targetPath, Entries: out}
}
