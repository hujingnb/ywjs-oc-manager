package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"

	"oc-manager/internal/auth"
	"oc-manager/internal/files"
)

// KnowledgeSyncDispatcher 抽象向 worker 入队 knowledge_sync_node 任务的能力。
// 实现负责按写入对象（org / app）找到目标节点并去重生成 job。
type KnowledgeSyncDispatcher interface {
	DispatchOrgChange(ctx context.Context, orgID, relPath, changeType, masterPath string) error
	DispatchAppChange(ctx context.Context, orgID, appID, relPath, changeType, masterPath string) error
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
	master           *files.KnowledgeMaster
	dispatcher       KnowledgeSyncDispatcher
	statusSource     KnowledgeSyncStatusSource
	retryDispatcher  KnowledgeRetryDispatcher
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

// GetOrgSyncStatus 列出组织在所有节点上的最近同步状态。
// 仅组织管理员 / 平台管理员可调。
func (s *KnowledgeService) GetOrgSyncStatus(ctx context.Context, principal auth.Principal, orgID string) ([]SyncStatusResult, error) {
	if !auth.CanManageOrg(principal, orgID) {
		return nil, ErrKnowledgeForbidden
	}
	if s.statusSource == nil {
		return []SyncStatusResult{}, nil
	}
	return s.statusSource.ListByOrg(ctx, orgID)
}

// RetryOrgNodeSync 触发指定 (org, node) 重新同步；通常由前端「重试同步」按钮调用。
// 仅组织管理员 / 平台管理员可调。
func (s *KnowledgeService) RetryOrgNodeSync(ctx context.Context, principal auth.Principal, orgID, nodeID string) error {
	if !auth.CanManageOrg(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	if s.retryDispatcher == nil {
		return fmt.Errorf("重试分发器未配置")
	}
	return s.retryDispatcher.RetryOrgNode(ctx, orgID, nodeID)
}

// KnowledgeListResult 是列表接口的返回。
type KnowledgeListResult struct {
	Path    string                  `json:"path"`
	Entries []KnowledgeEntryResult  `json:"entries"`
}

// KnowledgeEntryResult 是对外的条目视图。
type KnowledgeEntryResult struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// 与知识库路径相关的错误。
var (
	ErrKnowledgeForbidden = errors.New("无权访问该知识库")
	ErrKnowledgeMissing   = errors.New("知识库主副本未配置")
)

// SaveOrgFile 将文件写入指定组织的主副本。
func (s *KnowledgeService) SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string, content io.Reader, size int64) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !auth.CanManageOrg(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "knowledge", relative)
	if err := s.master.Save(target, content, size); err != nil {
		return err
	}
	if s.dispatcher != nil {
		_ = s.dispatcher.DispatchOrgChange(ctx, orgID, relative, "upload_file", target)
	}
	return nil
}

// SaveAppFile 写入应用维度的知识库。
// 仅 owner、组织管理员、平台管理员可写。
func (s *KnowledgeService) SaveAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string, content io.Reader, size int64) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !canWriteApp(principal, orgID, ownerUserID) {
		return ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "app", appID, "knowledge", relative)
	if err := s.master.Save(target, content, size); err != nil {
		return err
	}
	if s.dispatcher != nil {
		_ = s.dispatcher.DispatchAppChange(ctx, orgID, appID, relative, "upload_file", target)
	}
	return nil
}

// DeleteOrgFile 删除组织级文件。
func (s *KnowledgeService) DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !auth.CanManageOrg(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "knowledge", relative)
	if err := s.master.Delete(target); err != nil {
		return err
	}
	if s.dispatcher != nil {
		_ = s.dispatcher.DispatchOrgChange(ctx, orgID, relative, "delete_file", target)
	}
	return nil
}

// DeleteAppFile 删除应用级文件。
func (s *KnowledgeService) DeleteAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !canWriteApp(principal, orgID, ownerUserID) {
		return ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "app", appID, "knowledge", relative)
	if err := s.master.Delete(target); err != nil {
		return err
	}
	if s.dispatcher != nil {
		_ = s.dispatcher.DispatchAppChange(ctx, orgID, appID, relative, "delete_file", target)
	}
	return nil
}

// ListOrg 列出组织级知识库；组织成员只读。
func (s *KnowledgeService) ListOrg(_ context.Context, principal auth.Principal, orgID, relative string) (KnowledgeListResult, error) {
	if s.master == nil {
		return KnowledgeListResult{}, ErrKnowledgeMissing
	}
	if !auth.CanViewOrg(principal, orgID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "knowledge", relative)
	entries, err := s.master.List(target)
	if err != nil {
		return KnowledgeListResult{}, fmt.Errorf("读取组织知识库失败: %w", err)
	}
	return toKnowledgeListResult(target, entries), nil
}

// ListApp 列出应用级知识库；只能由 owner 或更高权限读取。
func (s *KnowledgeService) ListApp(_ context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (KnowledgeListResult, error) {
	if s.master == nil {
		return KnowledgeListResult{}, ErrKnowledgeMissing
	}
	if !canReadApp(principal, orgID, ownerUserID) {
		return KnowledgeListResult{}, ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "app", appID, "knowledge", relative)
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

// canWriteApp 判断主体能否写入指定应用的知识库。
func canWriteApp(principal auth.Principal, orgID, ownerUserID string) bool {
	switch principal.Role {
	case "platform_admin":
		return true
	case "org_admin":
		return principal.OrgID == orgID
	case "org_member":
		return principal.UserID == ownerUserID
	default:
		return false
	}
}

// canReadApp 是 canWriteApp 的只读版本；当前规则一致，但保留独立函数便于后续扩展。
func canReadApp(principal auth.Principal, orgID, ownerUserID string) bool {
	return canWriteApp(principal, orgID, ownerUserID)
}
