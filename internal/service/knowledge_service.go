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

// KnowledgeService 维护组织和应用维度的知识库主副本。
//
// 设计要点：
//   - 主副本统一存放在 manager 容器内（bind mount 到宿主），各 runtime node 上的工作目录由 worker 同步；
//   - 路径必须经过 files.SafeRoot 校验，防止越权访问；
//   - 写入路径会按租户拆分：org/{orgID}/...、org/{orgID}/app/{appID}/...；
//   - 应用级同步在主副本写入失败时回滚（这里是文件操作，整体最多一次写入，不需要 SQL 事务）；
//   - 组织级同步走异步任务，不阻塞主流程。
type KnowledgeService struct {
	master *files.KnowledgeMaster
}

// NewKnowledgeService 创建知识库服务。
func NewKnowledgeService(master *files.KnowledgeMaster) *KnowledgeService {
	return &KnowledgeService{master: master}
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
func (s *KnowledgeService) SaveOrgFile(_ context.Context, principal auth.Principal, orgID, relative string, content io.Reader, size int64) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !canManageOrg(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "knowledge", relative)
	return s.master.Save(target, content, size)
}

// SaveAppFile 写入应用维度的知识库。
// 仅 owner、组织管理员、平台管理员可写。
func (s *KnowledgeService) SaveAppFile(_ context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string, content io.Reader, size int64) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !canWriteApp(principal, orgID, ownerUserID) {
		return ErrKnowledgeForbidden
	}
	target := path.Join("org", orgID, "app", appID, "knowledge", relative)
	return s.master.Save(target, content, size)
}

// DeleteOrgFile 删除组织级文件。
func (s *KnowledgeService) DeleteOrgFile(_ context.Context, principal auth.Principal, orgID, relative string) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !canManageOrg(principal, orgID) {
		return ErrKnowledgeForbidden
	}
	return s.master.Delete(path.Join("org", orgID, "knowledge", relative))
}

// DeleteAppFile 删除应用级文件。
func (s *KnowledgeService) DeleteAppFile(_ context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) error {
	if s.master == nil {
		return ErrKnowledgeMissing
	}
	if !canWriteApp(principal, orgID, ownerUserID) {
		return ErrKnowledgeForbidden
	}
	return s.master.Delete(path.Join("org", orgID, "app", appID, "knowledge", relative))
}

// ListOrg 列出组织级知识库；组织成员只读。
func (s *KnowledgeService) ListOrg(_ context.Context, principal auth.Principal, orgID, relative string) (KnowledgeListResult, error) {
	if s.master == nil {
		return KnowledgeListResult{}, ErrKnowledgeMissing
	}
	if !canViewOrg(principal, orgID) {
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
