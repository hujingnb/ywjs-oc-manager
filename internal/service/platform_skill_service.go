package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// PlatformSkillStore 是 PlatformSkillService 所需的最小数据访问能力。
type PlatformSkillStore interface {
	CreatePlatformSkill(ctx context.Context, arg sqlc.CreatePlatformSkillParams) error
	GetPlatformSkill(ctx context.Context, id string) (sqlc.PlatformSkill, error)
	GetPlatformSkillByNameVersion(ctx context.Context, arg sqlc.GetPlatformSkillByNameVersionParams) (sqlc.PlatformSkill, error)
	ListPlatformSkills(ctx context.Context) ([]sqlc.PlatformSkill, error)
	DeletePlatformSkill(ctx context.Context, id string) error
}

// PlatformSkillService 管理平台库 skill（平台管理员上传/列出/删除）。
type PlatformSkillService struct {
	store PlatformSkillStore
	blobs LibraryBlobStore
}

// NewPlatformSkillService 构造平台库 service。
func NewPlatformSkillService(store PlatformSkillStore, blobs LibraryBlobStore) *PlatformSkillService {
	return &PlatformSkillService{store: store, blobs: blobs}
}

// PlatformSkillUploadInput 是上传平台库 skill 的入参（归档原始字节）。
type PlatformSkillUploadInput struct {
	Name        string
	Version     string
	Description string
	Data        []byte
}

// PlatformSkillResult 是平台库 skill 的对外视图。
type PlatformSkillResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	FileSize    int64  `json:"file_size"`
	FileSha256  string `json:"file_sha256"`
}

// toPlatformSkillResult 将数据库行转为对外视图，隐藏存储路径等内部字段。
func toPlatformSkillResult(row sqlc.PlatformSkill) PlatformSkillResult {
	return PlatformSkillResult{
		ID: row.ID, Name: row.Name, Description: row.Description, Version: row.Version,
		FileSize: row.FileSize, FileSha256: row.FileSha256,
	}
}

// List 返回全部平台库 skill，仅平台管理员可调用（用于管理页面）。
func (s *PlatformSkillService) List(ctx context.Context, principal auth.Principal) ([]PlatformSkillResult, error) {
	if !auth.CanManagePlatformSkill(principal) {
		return nil, ErrPlatformSkillDenied
	}
	return s.listAll(ctx)
}

// ListForMarket 返回全部平台库 skill，供市场聚合层调用。
// 市场是只读展示入口，所有已登录用户均可浏览，权限校验使用 CanViewPlatformSkillMarket。
func (s *PlatformSkillService) ListForMarket(ctx context.Context, principal auth.Principal) ([]PlatformSkillResult, error) {
	if !auth.CanViewPlatformSkillMarket(principal) {
		return nil, ErrPlatformSkillDenied
	}
	return s.listAll(ctx)
}

// listAll 是内部公共查询逻辑，不做权限校验（由调用方保证）。
func (s *PlatformSkillService) listAll(ctx context.Context) ([]PlatformSkillResult, error) {
	rows, err := s.store.ListPlatformSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	out := make([]PlatformSkillResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPlatformSkillResult(r))
	}
	return out, nil
}

// Upload 上传一个平台库 skill 归档：
//  1. 校验权限与入参（name/version/data 不能为空）
//  2. 查重（同名同版本已存在则拒绝）
//  3. 计算 sha256
//  4. 写对象存储（source=platform，路径：library/platform/<name>/<version>.tar）
//  5. 落库；若落库失败则回滚归档
//  6. 读回数据库行返回结果
func (s *PlatformSkillService) Upload(ctx context.Context, principal auth.Principal, in PlatformSkillUploadInput) (PlatformSkillResult, error) {
	if !auth.CanManagePlatformSkill(principal) {
		return PlatformSkillResult{}, ErrPlatformSkillDenied
	}
	name := strings.TrimSpace(in.Name)
	version := strings.TrimSpace(in.Version)
	if name == "" || version == "" || len(in.Data) == 0 {
		return PlatformSkillResult{}, fmt.Errorf("%w: name/version/内容不能为空", ErrPlatformSkillInvalid)
	}
	// 查重：同名同版本已存在则返回 NameVersionTaken。
	if _, err := s.store.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{Name: name, Version: version}); err == nil {
		return PlatformSkillResult{}, ErrPlatformSkillNameVersionTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PlatformSkillResult{}, fmt.Errorf("查询同名版本失败: %w", err)
	}
	// 计算归档内容的 sha256 摘要，用于完整性校验。
	sum := sha256.Sum256(in.Data)
	sha := hex.EncodeToString(sum[:])
	relPath, err := s.blobs.PutLibrarySkill("platform", name, version, "tar", in.Data)
	if err != nil {
		return PlatformSkillResult{}, err
	}
	id := newUUID()
	if err := s.store.CreatePlatformSkill(ctx, sqlc.CreatePlatformSkillParams{
		ID: id, Name: name, Description: strings.TrimSpace(in.Description), Version: version,
		TarPath: relPath, FileSize: int64(len(in.Data)), FileSha256: sha,
		MetadataJson: json.RawMessage("{}"), UploadedBy: null.StringFrom(principal.UserID),
	}); err != nil {
		// 落库失败时回滚已写入的归档，避免孤立对象残留。
		_ = s.blobs.DeleteLibrarySkill(relPath)
		return PlatformSkillResult{}, fmt.Errorf("写入平台库 skill 失败: %w", err)
	}
	row, err := s.store.GetPlatformSkill(ctx, id)
	if err != nil {
		return PlatformSkillResult{}, fmt.Errorf("读回平台库 skill 失败: %w", err)
	}
	return toPlatformSkillResult(row), nil
}

// Delete 删除一个平台库 skill：先确认存在，再删行与对象存储中的归档。
// 顺序为先删库行、后删归档；归档删除失败记录错误但不影响数据库一致性（对象孤立）。
func (s *PlatformSkillService) Delete(ctx context.Context, principal auth.Principal, id string) error {
	if !auth.CanManagePlatformSkill(principal) {
		return ErrPlatformSkillDenied
	}
	row, err := s.store.GetPlatformSkill(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPlatformSkillNotFound
		}
		return fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	if err := s.store.DeletePlatformSkill(ctx, id); err != nil {
		return fmt.Errorf("删除平台库 skill 失败: %w", err)
	}
	if err := s.blobs.DeleteLibrarySkill(row.TarPath); err != nil {
		return fmt.Errorf("删除平台库 skill 归档失败: %w", err)
	}
	return nil
}

// GetForInstall 取平台库 skill 指定版本的归档字节与 sha256，供安装到实例使用。
// name/version 不存在时返回 ErrPlatformSkillNotFound；归档读取失败时透传底层错误。
func (s *PlatformSkillService) GetForInstall(ctx context.Context, name, version string) (archive []byte, sha string, err error) {
	row, err := s.store.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{Name: name, Version: version})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrPlatformSkillNotFound
		}
		return nil, "", fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	rc, err := s.blobs.OpenLibrarySkill(row.TarPath)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, "", fmt.Errorf("读取平台库归档失败: %w", err)
	}
	return data, row.FileSha256, nil
}
