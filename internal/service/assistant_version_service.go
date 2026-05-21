package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/hermes"
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
			if uuidToString(existing.ID) != uuidToString(current.ID) {
				return AssistantVersionResult{}, ErrAssistantVersionNameTaken
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
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
	row, err := s.store.UpdateAssistantVersion(ctx, sqlc.UpdateAssistantVersionParams{
		ID:           current.ID,
		Name:         newName,
		Description:  trimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		ImageID:      trimSpace(in.ImageID),
		MainModel:    trimSpace(in.MainModel),
		RoutingJson:  routingJSON,
		SkillsJson:   current.SkillsJson,
		Revision:     revision,
	})
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("更新版本失败: %w", err)
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
	appCount, err := s.store.CountAppsUsingVersion(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("统计引用实例失败: %w", err)
	}
	if appCount > 0 {
		return fmt.Errorf("%w: 仍有 %d 个实例使用", ErrAssistantVersionInUse, appCount)
	}
	// jsonb_exists 的参数是裸字符串 id。
	orgCount, err := s.store.CountOrgsUsingVersion(ctx, uuidToString(row.ID))
	if err != nil {
		return fmt.Errorf("统计引用组织失败: %w", err)
	}
	if orgCount > 0 {
		return fmt.Errorf("%w: 仍有 %d 个组织 allowlist 包含", ErrAssistantVersionInUse, orgCount)
	}
	if _, err := s.store.SoftDeleteAssistantVersion(ctx, row.ID); err != nil {
		return fmt.Errorf("删除版本失败: %w", err)
	}
	return nil
}

// UploadSkill 上传一个 skill tar：校验大小、合法性、推导名称，写文件系统主副本，
// 把元信息追加进 skills_json 并把 revision +1。
func (s *AssistantVersionService) UploadSkill(ctx context.Context, principal auth.Principal, id string, data []byte) (AssistantVersionResult, error) {
	if !auth.CanManageAssistantVersion(principal) {
		return AssistantVersionResult{}, ErrAssistantVersionDenied
	}
	if int64(len(data)) > s.maxSkillBytes {
		return AssistantVersionResult{}, fmt.Errorf("%w: 上限 %d 字节", ErrSkillTooLarge, s.maxSkillBytes)
	}
	row, err := s.loadVersion(ctx, id)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	info, err := hermes.InspectSkillArchive(bytes.NewReader(data))
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("%w: %w", ErrAssistantVersionInvalid, err)
	}
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	for _, sk := range skills {
		if sk.Name == info.Name {
			return AssistantVersionResult{}, fmt.Errorf("%w: skill %s 已存在", ErrAssistantVersionInvalid, info.Name)
		}
	}
	// 先写 blob 再写库：persistSkills 失败时最多留下一个无引用的 tar（可由清理任务回收），不会出现 DB 指向缺失文件。
	relPath, err := s.blobs.PutSkill(uuidToString(row.ID), info.Name, data)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("写入 skill tar 失败: %w", err)
	}
	sum := sha256.Sum256(data)
	skills = append(skills, AssistantVersionSkill{
		Name: info.Name, FilePath: relPath, FileSize: int64(len(data)), FileSha256: hex.EncodeToString(sum[:]),
	})
	return s.persistSkills(ctx, row, skills)
}

// DeleteSkill 从版本中删除一个 skill：删文件系统主副本、从 skills_json 移除、revision +1。
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
	var removed *AssistantVersionSkill
	for i := range skills {
		if skills[i].Name == skillName {
			removed = &skills[i]
			continue
		}
		kept = append(kept, skills[i])
	}
	if removed == nil {
		return AssistantVersionResult{}, fmt.Errorf("%w: skill %s 不存在", ErrAssistantVersionInvalid, skillName)
	}
	// 先删 blob 再更新库；与 UploadSkill 的顺序相反，保持「DB 不指向已不存在文件」由 persistSkills 收尾。
	if err := s.blobs.DeleteSkill(removed.FilePath); err != nil {
		return AssistantVersionResult{}, fmt.Errorf("删除 skill tar 失败: %w", err)
	}
	return s.persistSkills(ctx, row, kept)
}

// persistSkills 把更新后的 skill 列表写库并把 revision +1（skill 变更属于容器相关变更）。
func (s *AssistantVersionService) persistSkills(ctx context.Context, row sqlc.AssistantVersion, skills []AssistantVersionSkill) (AssistantVersionResult, error) {
	skillsJSON, err := json.Marshal(skills)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("序列化 skills 失败: %w", err)
	}
	updated, err := s.store.UpdateAssistantVersionSkills(ctx, sqlc.UpdateAssistantVersionSkillsParams{
		ID:         row.ID,
		SkillsJson: skillsJSON,
		Revision:   row.Revision + 1,
	})
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("更新版本 skill 失败: %w", err)
	}
	return toAssistantVersionResult(updated)
}

// ListRuntimeImages 返回全部可选镜像，供前端版本编辑表单的镜像 select 使用。
func (s *AssistantVersionService) ListRuntimeImages(_ context.Context, principal auth.Principal) ([]RuntimeImageOption, error) {
	if !auth.CanViewAssistantVersion(principal) {
		return nil, ErrAssistantVersionDenied
	}
	return s.images.ListRuntimeImages(), nil
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
