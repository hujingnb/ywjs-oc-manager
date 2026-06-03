package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/guregu/null/v5"

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
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
	GetAssistantVersionByName(ctx context.Context, name string) (sqlc.AssistantVersion, error)
	ListAssistantVersions(ctx context.Context) ([]sqlc.AssistantVersion, error)
	// CreateAssistantVersion 创建版本（:exec），service 调用前需自行生成 ID 并在写入后 GetAssistantVersion 读回。
	CreateAssistantVersion(ctx context.Context, arg sqlc.CreateAssistantVersionParams) error
	// UpdateAssistantVersion 更新版本（:exec），service 写入后按 ID 重新读回。
	UpdateAssistantVersion(ctx context.Context, arg sqlc.UpdateAssistantVersionParams) error
	// UpdateAssistantVersionSkills 更新版本 skill 列表（:exec），service 写入后按 ID 重新读回。
	UpdateAssistantVersionSkills(ctx context.Context, arg sqlc.UpdateAssistantVersionSkillsParams) error
	SoftDeleteAssistantVersion(ctx context.Context, id string) error
	CountAppsUsingVersion(ctx context.Context, versionID null.String) (int64, error)
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

// PlatformSkillLibrary 抽象助手版本 service 从平台库查取 skill 元数据的能力。
// 是 PlatformSkillStore 的子集，仅需查询操作，不含上传/删除。
type PlatformSkillLibrary interface {
	// GetPlatformSkillByNameVersion 按名称 + 版本号精确查找平台库 skill，未找到返回 sql.ErrNoRows。
	GetPlatformSkillByNameVersion(ctx context.Context, arg sqlc.GetPlatformSkillByNameVersionParams) (sqlc.PlatformSkill, error)
}

// RuntimeImageOption 是暴露给前端镜像 select 的单个选项。
type RuntimeImageOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AssistantVersionService 维护助手版本目录。
type AssistantVersionService struct {
	store         AssistantVersionStore
	images        assistantVersionImages
	models        AssistantVersionModelValidator
	platformSkills PlatformSkillLibrary
}

// NewAssistantVersionService 创建版本 service。
func NewAssistantVersionService(
	store AssistantVersionStore,
	images assistantVersionImages,
	models AssistantVersionModelValidator,
	platformSkills PlatformSkillLibrary,
) *AssistantVersionService {
	return &AssistantVersionService{store: store, images: images, models: models, platformSkills: platformSkills}
}

// AssistantVersionSkill 是 skills_json 内单个 skill 的自包含快照。
// 首版仅 platform 来源可配进版本；clawhub 留 per-app 安装。
type AssistantVersionSkill struct {
	// Source 是 skill 来源类型，当前仅 "platform"。
	Source string `json:"source"`
	// SourceRef 是来源内精准标识；platform 来源时等于 skill name。
	SourceRef string `json:"source_ref"`
	// Name 是 skill 在版本内的唯一目录名。
	Name string `json:"name"`
	// Version 是 skill 版本号，由来源方定义。
	Version string `json:"version"`
	// CachedPath 是对象存储中归档的相对路径，如 library/platform/<name>/<version>.tar。
	CachedPath string `json:"cached_path"`
	// FileSize 是归档字节大小，供下载前预估流量。
	FileSize int64 `json:"file_size"`
	// FileSha256 是归档内容的 sha256，供完整性校验。
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
	// id 直接作为字符串传入，不再需要解析为 UUID 类型；不存在时 store 返回 sql.ErrNoRows。
	row, err := s.store.GetAssistantVersion(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
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
		ID:           row.ID,
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

// Update 编辑版本。仅当「影响容器」的字段变更时才把 revision +1：
// system_prompt / image_id / main_model / routing。name / description 变更不 bump。
func (s *AssistantVersionService) Update(ctx context.Context, principal auth.Principal, id string, in AssistantVersionInput) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	if err := s.validateInput(in); err != nil {
		return AssistantVersionResult{}, err
	}
	current, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	// 改名时确认新名称未被「其它」版本占用。
	newName := trimSpace(in.Name)
	if newName != current.Name {
		if existing, err := s.store.GetAssistantVersionByName(ctx, newName); err == nil {
			if existing.ID != current.ID {
				return AssistantVersionResult{}, ErrAssistantVersionNameTaken
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return AssistantVersionResult{}, fmt.Errorf("查询版本名称失败: %w", err)
		}
	}
	routingJSON, err := json.Marshal(normalizeRouting(in.Routing))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 routing 失败: %w", err)
	}
	revision := current.Revision
	if containerAffectingChanged(current, in, routingJSON) {
		revision++
	}
	// UpdateAssistantVersion 为 :exec，不返回行；写入后重新按 ID 读取最新数据。
	if err := s.store.UpdateAssistantVersion(ctx, sqlc.UpdateAssistantVersionParams{
		ID:           current.ID,
		Name:         newName,
		Description:  trimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		ImageID:      trimSpace(in.ImageID),
		MainModel:    trimSpace(in.MainModel),
		RoutingJson:  routingJSON,
		SkillsJson:   current.SkillsJson,
		Revision:     revision,
	}); err != nil {
		return AssistantVersionResult{}, fmt.Errorf("更新版本失败: %w", err)
	}
	row, err := s.store.GetAssistantVersion(ctx, current.ID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("重新查询更新后版本失败: %w", err)
	}
	return toAssistantVersionResult(row)
}

// containerAffectingChanged 判断本次更新是否动了「影响容器」的字段。
// routingJSON 是已归一化序列化后的新 routing，与库中现值做语义比较。
func containerAffectingChanged(current sqlc.AssistantVersion, in AssistantVersionInput, routingJSON []byte) bool {
	if current.SystemPrompt != in.SystemPrompt {
		return true
	}
	if current.ImageID != trimSpace(in.ImageID) {
		return true
	}
	if current.MainModel != trimSpace(in.MainModel) {
		return true
	}
	return !jsonEqual(current.RoutingJson, routingJSON)
}

// jsonEqual 比较两段 routing jsonb 字节在语义上是否相等（忽略 key 顺序与空白）。
func jsonEqual(a, b []byte) bool {
	var ma, mb map[string]string
	if err := json.Unmarshal(normalizeEmptyJSON(a), &ma); err != nil {
		return false
	}
	if err := json.Unmarshal(normalizeEmptyJSON(b), &mb); err != nil {
		return false
	}
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}

// normalizeEmptyJSON 把空字节视作空对象，避免 json.Unmarshal 报错。
func normalizeEmptyJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// Delete 软删除一个版本。严格保护：被任何未删除组织 allowlist 或未删除实例
// 引用时拒绝删除，调用方需先迁移/删除引用方。
func (s *AssistantVersionService) Delete(ctx context.Context, principal auth.Principal, id string) error {
	if !auth.CanManageAssistantVersion(principal) {
		return ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return err
	}
	// CountAppsUsingVersion 参数为 null.String（version_id 可空列用于 JSON 搜索）。
	appCount, err := s.store.CountAppsUsingVersion(ctx, null.StringFrom(row.ID))
	if err != nil {
		return fmt.Errorf("统计引用实例失败: %w", err)
	}
	if appCount > 0 {
		return fmt.Errorf("%w: 仍有 %d 个实例使用", ErrAssistantVersionInUse, appCount)
	}
	// CountOrgsUsingVersion 的参数是裸字符串 id（JSON 搜索）。
	orgCount, err := s.store.CountOrgsUsingVersion(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("统计引用企业失败: %w", err)
	}
	if orgCount > 0 {
		return fmt.Errorf("%w: 仍有 %d 个企业 allowlist 包含", ErrAssistantVersionInUse, orgCount)
	}
	if err := s.store.SoftDeleteAssistantVersion(ctx, row.ID); err != nil {
		return fmt.Errorf("删除版本失败: %w", err)
	}
	return nil
}

// AddSkillFromLibraryInput 是从平台库选 skill 配进版本的入参。
type AddSkillFromLibraryInput struct {
	// Source 是 skill 来源类型，首版仅接受 "platform"。
	Source string
	// SourceRef 是来源内精准标识；platform 来源时等于 skill name。
	SourceRef string
	// Version 是目标版本号，必须与平台库中已发布的版本对应。
	Version string
}

// AddSkillFromLibrary 从平台库选一个 skill 配进版本快照：
//  1. 权限校验（CanManageAssistantVersion）
//  2. 查版本
//  3. 查平台库取 skill 元数据（TarPath/FileSize/FileSha256）
//  4. 同 name 冲突检查（ErrAssistantVersionSkillNameTaken）
//  5. 追加自包含快照并 revision +1
//
// 首版仅 platform 来源；clawhub 来源留 per-app 安装路径。
func (s *AssistantVersionService) AddSkillFromLibrary(ctx context.Context, principal auth.Principal, id string, in AddSkillFromLibraryInput) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	// 查平台库取 skill 元数据；未找到时映射为 ErrPlatformSkillNotFound。
	ps, err := s.platformSkills.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{
		Name: in.SourceRef, Version: in.Version,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AssistantVersionResult{}, ErrPlatformSkillNotFound
		}
		return AssistantVersionResult{}, fmt.Errorf("查询平台库 skill 失败: %w", err)
	}
	// 同 name 冲突：同一版本内 skill 名称必须唯一。
	for _, sk := range skills {
		if sk.Name == ps.Name {
			return AssistantVersionResult{}, ErrAssistantVersionSkillNameTaken
		}
	}
	skills = append(skills, AssistantVersionSkill{
		Source:     "platform",
		SourceRef:  ps.Name,
		Name:       ps.Name,
		Version:    ps.Version,
		CachedPath: ps.TarPath,
		FileSize:   ps.FileSize,
		FileSha256: ps.FileSha256,
	})
	return s.persistSkills(ctx, row, skills)
}

// DeleteSkill 从版本中移除一个 skill 快照：只更新 skills_json 并 revision +1。
// skill 来自平台库（快照引用 CachedPath），不持有独立副本，无需删除对象存储文件。
func (s *AssistantVersionService) DeleteSkill(ctx context.Context, principal auth.Principal, id, skillName string) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	kept := make([]AssistantVersionSkill, 0, len(skills))
	found := false
	for i := range skills {
		if skills[i].Name == skillName {
			found = true
			continue
		}
		kept = append(kept, skills[i])
	}
	if !found {
		return AssistantVersionResult{}, fmt.Errorf("%w: skill %s 不存在", ErrAssistantVersionInvalid, skillName)
	}
	return s.persistSkills(ctx, row, kept)
}

// persistSkills 把更新后的 skill 列表写库并把 revision +1（skill 变更属于容器相关变更）。
func (s *AssistantVersionService) persistSkills(ctx context.Context, row sqlc.AssistantVersion, skills []AssistantVersionSkill) (AssistantVersionResult, error) {
	skillsJSON, err := json.Marshal(skills)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 skills 失败: %w", err)
	}
	// UpdateAssistantVersionSkills 为 :exec，不返回行；写入后重新读取最新数据。
	if err := s.store.UpdateAssistantVersionSkills(ctx, sqlc.UpdateAssistantVersionSkillsParams{
		ID:         row.ID,
		SkillsJson: skillsJSON,
		Revision:   row.Revision + 1,
	}); err != nil {
		return AssistantVersionResult{}, fmt.Errorf("更新版本 skill 失败: %w", err)
	}
	updated, err := s.store.GetAssistantVersion(ctx, row.ID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("重新查询版本 skill 更新后数据失败: %w", err)
	}
	return toAssistantVersionResult(updated)
}

// ListRuntimeImages 返回全部可选镜像，供前端版本编辑表单的镜像 select 使用。
// 镜像目录属于平台运维数据，使用 CanManageAssistantVersion（仅 platform_admin）保护。
func (s *AssistantVersionService) ListRuntimeImages(_ context.Context, principal auth.Principal) ([]RuntimeImageOption, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return nil, ErrAssistantVersionDenied
	}
	return s.images.ListRuntimeImages(), nil
}

// ValidateAssistantVersionIDs 校验一组版本 id 全部存在且未删除，返回去重后的列表。
// 供组织 allowlist 写入前校验；空列表合法（组织可暂不配置任何版本）。
func (s *AssistantVersionService) ValidateAssistantVersionIDs(ctx context.Context, ids []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := trimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if _, err := s.loadVersion(ctx, id); err != nil {
			if errors.Is(err, ErrAssistantVersionNotFound) {
				return nil, fmt.Errorf("%w: 版本 %s 不存在", ErrAssistantVersionInvalid, id)
			}
			return nil, err
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
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
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AssistantVersionResult{}, fmt.Errorf("查询版本名称失败: %w", err)
	}
	routingJSON, err := json.Marshal(normalizeRouting(in.Routing))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 routing 失败: %w", err)
	}
	// CreateAssistantVersion 为 :exec，预先生成 ID 后写入，再读回获取完整行。
	// CreatedBy 为可空 null.String；principal.UserID 非空时填充，否则留 NULL。
	newID := newUUID()
	createdBy := null.String{}
	if principal.UserID != "" {
		createdBy = null.StringFrom(principal.UserID)
	}
	if err := s.store.CreateAssistantVersion(ctx, sqlc.CreateAssistantVersionParams{
		ID:           newID,
		Name:         trimSpace(in.Name),
		Description:  trimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		ImageID:      trimSpace(in.ImageID),
		MainModel:    trimSpace(in.MainModel),
		RoutingJson:  routingJSON,
		SkillsJson:   []byte("[]"),
		CreatedBy:    createdBy,
	}); err != nil {
		return AssistantVersionResult{}, fmt.Errorf("写入版本失败: %w", err)
	}
	row, err := s.store.GetAssistantVersion(ctx, newID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("读取新建版本失败: %w", err)
	}
	return toAssistantVersionResult(row)
}
