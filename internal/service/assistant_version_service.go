package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// auxiliarySlots 是智能路由支持的 8 个 auxiliary 槽位，顺序固定，用于校验与渲染。
var auxiliarySlots = []string{
	"vision", "compression", "web_extract", "session_search",
	"title_generation", "approval", "skills_hub", "mcp",
}

// AssistantVersionStore 抽象版本 service 需要的数据访问能力。
type AssistantVersionStore interface {
	GetAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error)
	GetAssistantVersionByName(ctx context.Context, name string) (sqlc.AssistantVersion, error)
	ListAssistantVersions(ctx context.Context) ([]sqlc.AssistantVersion, error)
	CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) (sqlc.AssistantVersion, error)
	UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) (sqlc.AssistantVersion, error)
	UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) (sqlc.AssistantVersion, error)
	SoftDeleteAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error)
	CountAppsUsingVersion(ctx context.Context, id pgtype.UUID) (int64, error)
	CountOrgsUsingVersion(ctx context.Context, id string) (int64, error)
}

// AssistantVersionImageResolver 抽象「校验 image_id 是否存在于配置」的能力。
type AssistantVersionImageResolver interface {
	HasRuntimeImage(id string) bool
}

// AssistantVersionImageLister 抽象「列出全部可选镜像」的能力。
type AssistantVersionImageLister interface {
	ListRuntimeImages() []RuntimeImageOption
}

// assistantVersionImages 合并镜像校验与列举能力，由配置适配器统一实现。
type assistantVersionImages interface {
	AssistantVersionImageResolver
	AssistantVersionImageLister
}

// AssistantVersionModelValidator 抽象「校验模型名是否存在」的能力。
type AssistantVersionModelValidator interface {
	HasModel(id string) bool
}

// SkillBlobStore 抽象 skill tar 文件系统主副本的读写能力。
type SkillBlobStore interface {
	// PutSkill 写入一个 skill tar，返回相对数据根目录的存储路径。
	PutSkill(versionID, skillName string, data []byte) (relPath string, err error)
	// DeleteSkill 删除一个 skill tar。
	DeleteSkill(relPath string) error
}

// RuntimeImageOption 是暴露给前端镜像 select 的单个选项。
type RuntimeImageOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AssistantVersionService 维护助手版本目录。
type AssistantVersionService struct {
	store  AssistantVersionStore
	images assistantVersionImages
	models AssistantVersionModelValidator
	blobs  SkillBlobStore
	// maxSkillBytes 是单个 skill tar 的大小上限。
	maxSkillBytes int64
}

// NewAssistantVersionService 创建版本 service。maxSkillBytes<=0 时取默认 10 MiB。
func NewAssistantVersionService(
	store AssistantVersionStore,
	images assistantVersionImages,
	models AssistantVersionModelValidator,
	blobs SkillBlobStore,
	maxSkillBytes int64,
) *AssistantVersionService {
	if maxSkillBytes <= 0 {
		maxSkillBytes = 10 << 20
	}
	return &AssistantVersionService{store: store, images: images, models: models, blobs: blobs, maxSkillBytes: maxSkillBytes}
}

// AssistantVersionSkill 是 skills_json 内单个 skill 的元信息。
type AssistantVersionSkill struct {
	Name       string `json:"name"`
	FilePath   string `json:"file_path"`
	FileSize   int64  `json:"file_size"`
	FileSha256 string `json:"file_sha256"`
}

// AssistantVersionResult 是面向 handler/前端的版本视图。
type AssistantVersionResult struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	SystemPrompt string                  `json:"system_prompt"`
	ImageID      string                  `json:"image_id"`
	MainModel    string                  `json:"main_model"`
	Routing      map[string]string       `json:"routing"`
	Skills       []AssistantVersionSkill `json:"skills"`
	Revision     int32                   `json:"revision"`
}

// List 返回全部未删除版本，按创建时间倒序。
func (s *AssistantVersionService) List(ctx context.Context, principal auth.Principal) ([]AssistantVersionResult, error) {
	if !auth.CanViewAssistantVersion(principal) {
		return nil, ErrAssistantVersionDenied
	}
	rows, err := s.store.ListAssistantVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询版本列表失败: %w", err)
	}
	out := make([]AssistantVersionResult, 0, len(rows))
	for _, row := range rows {
		r, err := toAssistantVersionResult(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Get 返回单个版本。
func (s *AssistantVersionService) Get(ctx context.Context, principal auth.Principal, id string) (AssistantVersionResult, error) {
	if !auth.CanViewAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	return toAssistantVersionResult(row)
}

// loadVersion 按 id 取版本，未找到统一映射为 ErrAssistantVersionNotFound。
func (s *AssistantVersionService) loadVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return sqlc.AssistantVersion{}, ErrAssistantVersionNotFound
	}
	row, err := s.store.GetAssistantVersion(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.AssistantVersion{}, ErrAssistantVersionNotFound
	}
	if err != nil {
		return sqlc.AssistantVersion{}, fmt.Errorf("查询版本失败: %w", err)
	}
	return row, nil
}

// toAssistantVersionResult 把 sqlc 行转成对外视图。
func toAssistantVersionResult(row sqlc.AssistantVersion) (AssistantVersionResult, error) {
	routing := map[string]string{}
	if len(row.RoutingJson) > 0 {
		if err := json.Unmarshal(row.RoutingJson, &routing); err != nil {
			return AssistantVersionResult{}, fmt.Errorf("解析 routing_json 失败: %w", err)
		}
	}
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	return AssistantVersionResult{
		ID:           uuidToString(row.ID),
		Name:         row.Name,
		Description:  row.Description,
		SystemPrompt: row.SystemPrompt,
		ImageID:      row.ImageID,
		MainModel:    row.MainModel,
		Routing:      routing,
		Skills:       skills,
		Revision:     row.Revision,
	}, nil
}

// decodeSkills 把 skills_json 解为切片，供 Update/skill 操作复用。
func decodeSkills(raw []byte) ([]AssistantVersionSkill, error) {
	skills := []AssistantVersionSkill{}
	if len(raw) == 0 {
		return skills, nil
	}
	if err := json.Unmarshal(raw, &skills); err != nil {
		return nil, fmt.Errorf("解析 skills_json 失败: %w", err)
	}
	return skills, nil
}

// trimSpace 是 strings.TrimSpace 的本地别名，保持调用点简洁。
func trimSpace(s string) string { return strings.TrimSpace(s) }
