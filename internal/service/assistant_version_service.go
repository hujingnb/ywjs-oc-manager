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

// AssistantVersionInput 是创建/更新版本的入参。
type AssistantVersionInput struct {
	Name         string
	Description  string
	SystemPrompt string
	ImageID      string
	MainModel    string
	// Routing 是 auxiliary 槽位到模型名的映射；key 必须是 auxiliarySlots 之一。
	Routing map[string]string
}

// validateInput 校验版本入参的业务规则（不含名称唯一性，由调用方单独查）。
func (s *AssistantVersionService) validateInput(in AssistantVersionInput) error {
	if trimSpace(in.Name) == "" {
		return fmt.Errorf("%w: 名称不能为空", ErrAssistantVersionInvalid)
	}
	if trimSpace(in.SystemPrompt) == "" {
		return fmt.Errorf("%w: 内置提示词不能为空", ErrAssistantVersionInvalid)
	}
	imageID := trimSpace(in.ImageID)
	if !s.images.HasRuntimeImage(imageID) {
		return fmt.Errorf("%w: 镜像 %s 不存在于配置", ErrAssistantVersionInvalid, imageID)
	}
	mainModel := trimSpace(in.MainModel)
	if mainModel == "" || !s.models.HasModel(mainModel) {
		return fmt.Errorf("%w: 主模型 %s 不可用", ErrAssistantVersionInvalid, mainModel)
	}
	valid := make(map[string]struct{}, len(auxiliarySlots))
	for _, slot := range auxiliarySlots {
		valid[slot] = struct{}{}
	}
	for slot, model := range in.Routing {
		if _, ok := valid[slot]; !ok {
			return fmt.Errorf("%w: 未知路由槽位 %s", ErrAssistantVersionInvalid, slot)
		}
		m := trimSpace(model)
		if m == "" {
			continue
		}
		if !s.models.HasModel(m) {
			return fmt.Errorf("%w: 路由槽位 %s 的模型 %s 不可用", ErrAssistantVersionInvalid, slot, m)
		}
	}
	return nil
}

// normalizeRouting 丢弃空值槽位，返回紧凑的 routing map。
// 调用前必须已经过 validateInput 校验（槽位名合法、模型存在）。
func normalizeRouting(in map[string]string) map[string]string {
	out := map[string]string{}
	for slot, model := range in {
		if trimSpace(model) != "" {
			out[slot] = trimSpace(model)
		}
	}
	return out
}

// Create 创建一个新版本，revision 初始为 1。
func (s *AssistantVersionService) Create(ctx context.Context, principal auth.Principal, in AssistantVersionInput) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	if err := s.validateInput(in); err != nil {
		return AssistantVersionResult{}, err
	}
	if _, err := s.store.GetAssistantVersionByName(ctx, trimSpace(in.Name)); err == nil {
		return AssistantVersionResult{}, ErrAssistantVersionNameTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return AssistantVersionResult{}, fmt.Errorf("查询版本名称失败: %w", err)
	}
	routingJSON, err := json.Marshal(normalizeRouting(in.Routing))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 routing 失败: %w", err)
	}
	creator, _ := optionalUUID(principal.UserID)
	row, err := s.store.CreateAssistantVersion(ctx, sqlc.CreateAssistantVersionParams{
		Name:         trimSpace(in.Name),
		Description:  trimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		ImageID:      trimSpace(in.ImageID),
		MainModel:    trimSpace(in.MainModel),
		RoutingJson:  routingJSON,
		SkillsJson:   []byte("[]"),
		CreatedBy:    creator,
	})
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("写入版本失败: %w", err)
	}
	return toAssistantVersionResult(row)
}
