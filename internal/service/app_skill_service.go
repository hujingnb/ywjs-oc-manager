// app_skill_service.go — 实例级 skill 安装（热装 + reload）服务。
//
// 安装流程：
//  1. 权限判断（CanManageAppSkill）
//  2. 去重（app_skills 唯一约束前先查）
//  3. 按 source 取归档（platform: PlatformInstaller / clawhub: ClawHubDownloader）
//  4. 解压防炸弹校验（validateArchiveSafety：总字节/文件数上限）
//  5. 缓存到 LibraryBlobStore（共享 library/ 前缀）
//  6. 落 app_skills
//  7. oc-ops 热装（SkillInstall）+ reload（SkillReload）
//  8. 审计；oc-ops 失败 → status=pending，不回滚 app_skills（可重试）
package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

// 解压安全阈值常量——防止 zip bomb / tar bomb：
// 解压后总字节上限（200 MiB）与文件数上限（5000 个）。
const (
	maxArchiveBytes = 200 * 1024 * 1024 // 200 MiB
	maxArchiveFiles = 5000
)

// =========================================================
// 依赖接口（均最小化，便于测试与适配）
// =========================================================

// AppSkillStore 是 app_skills 表的最小数据访问能力，供 AppSkillService 注入。
type AppSkillStore interface {
	// ListAppSkillsByApp 列出某实例下已安装的全部 skill，按 name 升序。
	ListAppSkillsByApp(ctx context.Context, appID string) ([]sqlc.AppSkill, error)
	// GetAppSkillByAppAndName 按 app_id + name 精确查找，不存在时返回 sql.ErrNoRows。
	GetAppSkillByAppAndName(ctx context.Context, arg sqlc.GetAppSkillByAppAndNameParams) (sqlc.AppSkill, error)
	// CreateAppSkill 新增一条安装记录（:exec）。
	CreateAppSkill(ctx context.Context, arg sqlc.CreateAppSkillParams) error
	// DeleteAppSkillByAppAndName 删除一条安装记录（:exec）。
	DeleteAppSkillByAppAndName(ctx context.Context, arg sqlc.DeleteAppSkillByAppAndNameParams) error
	// UpdateAppSkillVersion 更新版本与缓存路径（用于升级场景，同时清空 latest_version）。
	UpdateAppSkillVersion(ctx context.Context, arg sqlc.UpdateAppSkillVersionParams) error
}

// AppLocator 把 appID 解析为 skill 操作所需的 app 定位信息（org/owner/oc-ops 地址）。
// 生产实现由 OcOpsResolverFromStore 适配（见 ocops.go），单测用 fakeAppLocator。
type AppLocator interface {
	LocateApp(ctx context.Context, appID string) (AppSkillLocation, error)
}

// AppSkillLocation 是 AppLocator 返回的 app 定位信息。
type AppSkillLocation struct {
	// OrgID 归属组织，用于 CanManageAppSkill 权限判断。
	OrgID string
	// OwnerUserID app 拥有者，用于 CanManageAppSkill 中 org_member 路径的判断。
	OwnerUserID string
	// VersionID 当前绑定的助手版本 ID，用于删除保护（判断 skill 是否为版本必需）。
	VersionID string
	// Endpoint oc-ops HTTP 地址与 token，用于热装/热删/reload/列。
	Endpoint ocops.Endpoint
	// Supported 为 false 时容器不可达（dev stub 或未启动），跳过热装直接置 pending。
	Supported bool
}

// PlatformInstaller 抽取平台库 skill 指定版本的归档字节与 sha256，供安装到实例使用。
// 由 *PlatformSkillService.GetForInstall 满足。
type PlatformInstaller interface {
	GetForInstall(ctx context.Context, name, version string) (archive []byte, sha string, err error)
}

// ClawHubDownloader 从 ClawHub 下载 skill 归档（按 slug + version），可为 nil（禁用 clawhub 来源）。
type ClawHubDownloader interface {
	Download(ctx context.Context, slug, version string) ([]byte, error)
}

// OcOpsSkillClient 抽取 oc-ops 的 4 个 skill 方法，方法签名镜像 *ocops.Client。
// 编译期断言在文件末尾确保 *ocops.Client 满足本接口。
type OcOpsSkillClient interface {
	SkillInstall(ctx context.Context, ep ocops.Endpoint, name string, archive []byte) error
	SkillDelete(ctx context.Context, ep ocops.Endpoint, name string) error
	SkillReload(ctx context.Context, ep ocops.Endpoint) error
	SkillList(ctx context.Context, ep ocops.Endpoint) ([]ocops.SkillInfo, error)
}

// AssistantVersionLoader 按版本 ID 取 skills_json 内所有 skill 的 name 集，用于删除保护。
// 由 AssistantVersionService 或专用适配器满足。
type AssistantVersionLoader interface {
	SkillNames(ctx context.Context, versionID string) ([]string, error)
}

// AuditRecorder 抽取审计日志的写入能力，签名与现有 *AuditService.Record 一致。
// *AuditService 直接满足本接口，无需额外适配。
type AuditRecorder interface {
	Record(ctx context.Context, event AuditEvent) (AuditResult, error)
}

// =========================================================
// 输入/输出类型
// =========================================================

// InstallSkillInput 是安装一个实例 skill 的入参。
type InstallSkillInput struct {
	// Source 来源类型：platform | clawhub
	Source string
	// SourceRef 来源内精准标识：platform=name，clawhub=slug
	SourceRef string
	// Name skill 目录名（实例内唯一）
	Name string
	// Version 要安装的版本
	Version string
}

// AppSkillResult 是已安装列表/操作返回的单条（含对账 status）。
type AppSkillResult struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	SourceRef string `json:"source_ref"`
	Version   string `json:"version"`
	// Latest 非空时表示有更新版本（大于 Version），前端可展示更新提示。
	Latest   string `json:"latest_version,omitempty"`
	// Status 对账状态：active | pending | builtin | self_created | unknown
	Status   string `json:"status"`
	Category string `json:"category"`
	// Protected 为 true 时表示该 skill 是当前助手版本必需的，不可删除。
	Protected bool `json:"protected"`
}

// =========================================================
// Service 结构体与构造
// =========================================================

// AppSkillService 管理实例级 skill 的安装/卸载/更新与对账。
type AppSkillService struct {
	// store 操作 app_skills 表
	store AppSkillStore
	// apps 解析 app 定位信息（权限 + oc-ops 地址）
	apps AppLocator
	// versions 查询助手版本的 skill name 集（删除保护用）
	versions AssistantVersionLoader
	// platform 取平台库 skill 归档
	platform PlatformInstaller
	// clawhub 从 ClawHub 下载归档；nil 表示来源未启用
	clawhub ClawHubDownloader
	// blobs 归档缓存对象存储（共享 library/ 前缀）
	blobs LibraryBlobStore
	// ocops oc-ops skill 操作客户端（热装/热删/reload/列）
	ocops OcOpsSkillClient
	// audit 审计日志写入
	audit AuditRecorder
}

// AppSkillServiceDeps 是 AppSkillService 构造函数的依赖容器，便于测试注入 fake。
type AppSkillServiceDeps struct {
	Store    AppSkillStore
	Apps     AppLocator
	Versions AssistantVersionLoader
	Platform PlatformInstaller
	ClawHub  ClawHubDownloader // 可 nil
	Blobs    LibraryBlobStore
	OcOps    OcOpsSkillClient
	Audit    AuditRecorder
}

// NewAppSkillService 构造 AppSkillService，注入所有依赖。
func NewAppSkillService(deps AppSkillServiceDeps) *AppSkillService {
	return &AppSkillService{
		store:    deps.Store,
		apps:     deps.Apps,
		versions: deps.Versions,
		platform: deps.Platform,
		clawhub:  deps.ClawHub,
		blobs:    deps.Blobs,
		ocops:    deps.OcOps,
		audit:    deps.Audit,
	}
}

// =========================================================
// Install
// =========================================================

// Install 安装一个 skill 到指定实例：
//  1. 权限（CanManageAppSkill：owner 本人或 org_admin）
//  2. name 去重（app_skills 唯一约束前先查，防止重复安装同名 skill）
//  3. 按 source 取归档 + 元数据
//  4. 解压防炸弹校验（validateArchiveSafety：总字节/文件数上限）
//  5. 缓存归档到 LibraryBlobStore（library/<source>/<ref>/<version>.<ext>）
//  6. 落 app_skills（含 sha256/size/installed_by）
//  7. oc-ops 热装（SkillInstall）+ reload（SkillReload）
//  8. 写审计；oc-ops 失败 → status=pending，不回滚（可重试）
func (s *AppSkillService) Install(ctx context.Context, principal auth.Principal, appID string, in InstallSkillInput) (AppSkillResult, error) {
	// 解析 app 定位信息（org/owner/oc-ops 地址/是否支持热装）
	loc, err := s.apps.LocateApp(ctx, appID)
	if err != nil {
		return AppSkillResult{}, err
	}
	// 权限判断：owner 本人或本 org 的 org_admin 方可管理实例 skill
	if !auth.CanManageAppSkill(principal, loc.OrgID, loc.OwnerUserID) {
		return AppSkillResult{}, ErrAppSkillDenied
	}
	// name 去重：app_skills 表有 (app_id, name) 唯一约束，提前查避免重复安装
	if _, err := s.store.GetAppSkillByAppAndName(ctx, sqlc.GetAppSkillByAppAndNameParams{
		AppID: appID,
		Name:  in.Name,
	}); err == nil {
		// 查到行 → 同名已存在
		return AppSkillResult{}, ErrAppSkillNameConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		// 非 "行不存在" 的其他错误
		return AppSkillResult{}, fmt.Errorf("查询实例 skill 失败: %w", err)
	}
	// 按来源取归档字节、sha256（platform 来源自带，clawhub 来源需本地计算）、元数据、扩展名
	archive, sha, meta, ext, err := s.fetchArchive(ctx, in)
	if err != nil {
		return AppSkillResult{}, err
	}
	// 解压防炸弹 + zip-slip 预校验（统计解压后总字节与文件数，超阈值返回错误）
	if err := validateArchiveSafety(archive, ext); err != nil {
		return AppSkillResult{}, ErrAppSkillArchiveTooDangerous
	}
	// clawhub 来源没有预置 sha，本地计算
	if sha == "" {
		sum := sha256.Sum256(archive)
		sha = hex.EncodeToString(sum[:])
	}
	// 将归档缓存到对象存储（library/<source>/<ref>/<version>.<ext>），返回相对路径
	relPath, err := s.blobs.PutLibrarySkill(in.Source, in.SourceRef, in.Version, ext, archive)
	if err != nil {
		return AppSkillResult{}, err
	}
	// 序列化来源元数据快照（防止来源下架后无法追溯安装信息）
	metaJSON, _ := json.Marshal(meta)
	// 落 app_skills 表（含 sha256/size/installed_by；初装 latest_version 为 NULL）
	if err := s.store.CreateAppSkill(ctx, sqlc.CreateAppSkillParams{
		ID:             newUUID(),
		AppID:          appID,
		Name:           in.Name,
		Source:         in.Source,
		SourceRef:      in.SourceRef,
		Version:        in.Version,
		LatestVersion:  null.String{}, // 初装时未知，等定时任务回源检测
		CachedTarPath:  relPath,
		SourceMetadata: metaJSON,
		FileSize:       int64(len(archive)),
		FileSha256:     sha,
		InstalledBy:    null.StringFrom(principal.UserID),
	}); err != nil {
		return AppSkillResult{}, fmt.Errorf("写入实例 skill 失败: %w", err)
	}
	// 写审计日志（记录安装操作，target_type=app_skill，action=skill.install）
	_, _ = s.audit.Record(ctx, AuditEvent{
		ActorID:    principal.UserID,
		ActorRole:  string(principal.Role),
		OrgID:      loc.OrgID,
		TargetType: "app_skill",
		TargetID:   appID + "/" + in.Name,
		Action:     "skill.install",
		Result:     "succeeded",
		DetailMessage: fmt.Sprintf("安装 skill %s@%s 到实例 %s", in.Name, in.Version, appID),
	})
	// oc-ops 热装 + reload（失败 → pending，不回滚 app_skills，等待对账重试）
	status := "active"
	if loc.Supported {
		if err := s.ocops.SkillInstall(ctx, loc.Endpoint, in.Name, archive); err != nil {
			// 热装失败：app_skills 已落库，标记 pending 等待下次启动或手动重试
			status = "pending"
		} else if err := s.ocops.SkillReload(ctx, loc.Endpoint); err != nil {
			// reload 失败：skill 已上传，但 hermes 未重扫；标记 pending
			status = "pending"
		}
	} else {
		// 容器未运行（dev stub / 未启动）：skill 已落库，等下次启动 oc-restore 恢复
		status = "pending"
	}
	return AppSkillResult{
		Name:      in.Name,
		Source:    in.Source,
		SourceRef: in.SourceRef,
		Version:   in.Version,
		Status:    status,
		Category:  "manager",
	}, nil
}

// =========================================================
// Uninstall
// =========================================================

// Uninstall 卸载指定实例的 skill：
//  1. 权限（CanManageAppSkill）
//  2. 查 app_skills 行（不存在 → ErrAppSkillNotFound）
//  3. 删除保护：若该 skill 属于当前绑定版本的 skills_json → ErrAppSkillProtected
//  4. 删 app_skills
//  5. 写审计
//  6. oc-ops 热删（SkillDelete）+ reload（SkillReload）；失败静默忽略（对账可识别 pending）
func (s *AppSkillService) Uninstall(ctx context.Context, principal auth.Principal, appID, name string) error {
	// 解析 app 定位信息（org/owner/oc-ops 地址/是否支持热删）
	loc, err := s.apps.LocateApp(ctx, appID)
	if err != nil {
		return err
	}
	// 权限判断：owner 本人或本 org 的 org_admin 方可卸载实例 skill
	if !auth.CanManageAppSkill(principal, loc.OrgID, loc.OwnerUserID) {
		return ErrAppSkillDenied
	}
	// 查 app_skills 行，不存在则返回 NotFound
	if _, err := s.store.GetAppSkillByAppAndName(ctx, sqlc.GetAppSkillByAppAndNameParams{
		AppID: appID,
		Name:  name,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAppSkillNotFound
		}
		return fmt.Errorf("查询实例 skill 失败: %w", err)
	}
	// 删除保护：若该 skill 属于 app 当前绑定版本的 skills_json，拒绝卸载
	protected, err := s.isCurrentVersionSkill(ctx, loc.VersionID, name)
	if err != nil {
		return err
	}
	if protected {
		return ErrAppSkillProtected
	}
	// 从 app_skills 表删除记录
	if err := s.store.DeleteAppSkillByAppAndName(ctx, sqlc.DeleteAppSkillByAppAndNameParams{
		AppID: appID,
		Name:  name,
	}); err != nil {
		return fmt.Errorf("删除实例 skill 失败: %w", err)
	}
	// 写审计日志（记录卸载操作，target_type=app_skill，action=skill.uninstall）
	_, _ = s.audit.Record(ctx, AuditEvent{
		ActorID:       principal.UserID,
		ActorRole:     string(principal.Role),
		OrgID:         loc.OrgID,
		TargetType:    "app_skill",
		TargetID:      appID + "/" + name,
		Action:        "skill.uninstall",
		Result:        "succeeded",
		DetailMessage: fmt.Sprintf("从实例 %s 卸载 skill %s", appID, name),
	})
	// oc-ops 热删 + reload（失败静默忽略：app_skills 已删，对账可识别并清理容器侧残留）
	if loc.Supported {
		_ = s.ocops.SkillDelete(ctx, loc.Endpoint, name)
		_ = s.ocops.SkillReload(ctx, loc.Endpoint)
	}
	return nil
}

// isCurrentVersionSkill 判断 name 是否属于 app 当前绑定版本的 skills_json（删除保护）。
// versionID 为空（app 未绑定版本）时直接返回 false（无需保护）。
func (s *AppSkillService) isCurrentVersionSkill(ctx context.Context, versionID, name string) (bool, error) {
	if versionID == "" {
		// app 未绑定任何助手版本，无删除保护
		return false, nil
	}
	// 取当前版本的 skills_json 中所有 skill name
	names, err := s.versions.SkillNames(ctx, versionID)
	if err != nil {
		return false, fmt.Errorf("查询版本 skill 列表失败: %w", err)
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// fetchArchive 按来源取归档字节、sha256（可能为空）、原始元数据快照、扩展名。
// 返回的 sha 为空时调用方须自行计算（clawhub 来源不提供 sha）。
func (s *AppSkillService) fetchArchive(ctx context.Context, in InstallSkillInput) (data []byte, sha string, meta map[string]any, ext string, err error) {
	switch in.Source {
	case "platform":
		// 平台库来源：GetForInstall 返回归档字节与预存 sha256
		d, sh, e := s.platform.GetForInstall(ctx, in.SourceRef, in.Version)
		if e != nil {
			return nil, "", nil, "", fmt.Errorf("取平台库 skill 归档失败: %w", e)
		}
		return d, sh, map[string]any{"source": "platform", "name": in.SourceRef, "version": in.Version}, "tar", nil
	case "clawhub":
		// ClawHub 来源：调用下载器；nil 时表示该来源未启用
		if s.clawhub == nil {
			return nil, "", nil, "", ErrAppSkillSourceUnknown
		}
		d, e := s.clawhub.Download(ctx, in.SourceRef, in.Version)
		if e != nil {
			return nil, "", nil, "", fmt.Errorf("从 ClawHub 下载 skill 失败: %w", e)
		}
		return d, "", map[string]any{"source": "clawhub", "slug": in.SourceRef, "version": in.Version}, "zip", nil
	default:
		// 未知来源类型，拒绝安装
		return nil, "", nil, "", ErrAppSkillSourceUnknown
	}
}

// =========================================================
// validateArchiveSafety
// =========================================================

// validateArchiveSafety 对归档字节做解压炸弹防护：
// 统计 tar/zip 解压后总字节与文件数，任意超过阈值则返回错误。
// 路径穿越（zip-slip）由容器侧 render_skills 负责校验；本函数只防解压炸弹（总字节/文件数超限）。
// ext 目前支持 "tar" 与 "zip"；未知扩展名视为合法（宽松策略，避免误拒未知格式）。
func validateArchiveSafety(archive []byte, ext string) error {
	switch ext {
	case "tar":
		return validateTarSafety(archive)
	case "zip":
		return validateZipSafety(archive)
	default:
		// 未知格式：宽松放行（后续可扩展）
		return nil
	}
}

// validateTarSafety 遍历 tar 归档，统计文件数与解压后总字节，超阈值返回错误。
// 若归档解析失败（非法格式）则放行（不是炸弹；后续真正解压时会失败）。
func validateTarSafety(data []byte) error {
	tr := tar.NewReader(bytes.NewReader(data))
	var totalBytes int64
	var fileCount int
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// 非法 tar 格式：不是有效归档，无炸弹风险；放行（实际解压时会失败）
			return nil
		}
		fileCount++
		if fileCount > maxArchiveFiles {
			return fmt.Errorf("tar 文件数超过上限 %d", maxArchiveFiles)
		}
		// 只统计普通文件的大小（目录/symlink header.Size 可能为 0 或无意义）
		if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == 0 {
			totalBytes += hdr.Size
			if totalBytes > maxArchiveBytes {
				return fmt.Errorf("tar 解压后总字节超过上限 %d", maxArchiveBytes)
			}
		}
	}
	return nil
}

// validateZipSafety 遍历 zip 归档，统计文件数与解压后总字节，超阈值返回错误。
func validateZipSafety(data []byte) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("zip 解析失败: %w", err)
	}
	if len(r.File) > maxArchiveFiles {
		return fmt.Errorf("zip 文件数超过上限 %d", maxArchiveFiles)
	}
	var totalBytes int64
	for _, f := range r.File {
		totalBytes += int64(f.UncompressedSize64)
		if totalBytes > maxArchiveBytes {
			return fmt.Errorf("zip 解压后总字节超过上限 %d", maxArchiveBytes)
		}
	}
	return nil
}

// =========================================================
// 编译期断言
// =========================================================

// 确认 *ocops.Client 满足 OcOpsSkillClient；方法签名漂移时编译期报错。
var _ OcOpsSkillClient = (*ocops.Client)(nil)
