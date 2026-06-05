package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	// GetIndustryKnowledgeBase 按 ID 校验行业知识库存在，供版本关联写入前使用。
	GetIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error)
	// ReplaceAssistantVersionIndustryKnowledgeBases 清空版本旧行业库关联，由 service 先校验再重建。
	ReplaceAssistantVersionIndustryKnowledgeBases(ctx context.Context, versionID string) error
	// AddAssistantVersionIndustryKnowledgeBase 为版本追加单个行业库关联。
	AddAssistantVersionIndustryKnowledgeBase(ctx context.Context, arg sqlc.AddAssistantVersionIndustryKnowledgeBaseParams) error
	// ListIndustryKnowledgeBasesByAssistantVersion 读取版本运行时额外检索的行业库。
	ListIndustryKnowledgeBasesByAssistantVersion(ctx context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error)
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

// AssistantVersionTxRunner 抽象助手版本写操作事务。
// 版本主表与行业库关联表必须一起提交或一起回滚，避免运行时检索范围与版本字段不同步。
type AssistantVersionTxRunner interface {
	WithAssistantVersionTx(ctx context.Context, fn func(AssistantVersionStore) error) error
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
	store          AssistantVersionStore
	tx             AssistantVersionTxRunner
	images         assistantVersionImages
	models         AssistantVersionModelValidator
	platformSkills PlatformSkillLibrary
	// clawhub 下载 ClawHub skill 归档；可为 nil（未配置 ClawHub BaseURL 时禁用 clawhub 来源）。
	clawhub ClawHubDownloader
	// blobs 把 clawhub 下载的归档缓存到对象存储（platform 来源不用，引用平台库已存档案）。
	blobs LibraryBlobStore
}

// NewAssistantVersionService 创建版本 service。
// clawhub 可为 nil（未配 ClawHub），此时 clawhub 来源的 AddSkillFromLibrary 返回 ErrAppSkillSourceUnknown。
func NewAssistantVersionService(
	store AssistantVersionStore,
	images assistantVersionImages,
	models AssistantVersionModelValidator,
	platformSkills PlatformSkillLibrary,
	clawhub ClawHubDownloader,
	blobs LibraryBlobStore,
) *AssistantVersionService {
	return &AssistantVersionService{
		store: store, images: images, models: models,
		platformSkills: platformSkills, clawhub: clawhub, blobs: blobs,
	}
}

// SetClawHubDownloader 注入 ClawHub 下载器。用 setter 而非构造参数回填，规避 nil *Client
// 直接传接口参数产生「非 nil interface 包装 nil 指针」的陷阱（与 AppSkillService 同处理）。
func (s *AssistantVersionService) SetClawHubDownloader(d ClawHubDownloader) { s.clawhub = d }

// SetTxRunner 注入助手版本事务 runner，生产环境用于保证版本主表与行业库关联原子提交。
func (s *AssistantVersionService) SetTxRunner(tx AssistantVersionTxRunner) { s.tx = tx }

// AssistantVersionSkill 是 skills_json 内单个 skill 的自包含快照。
// 支持 platform 与 clawhub 两种来源。
type AssistantVersionSkill struct {
	// Source 是 skill 来源类型，支持 "platform" 与 "clawhub"。
	Source string `json:"source"`
	// SourceRef 是来源内精准标识；platform 来源时等于 skill name，clawhub 来源时为 slug。
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
	// IndustryKnowledgeBases 是该版本运行时额外检索的行业库来源引用，仅暴露排障所需 id/name。
	IndustryKnowledgeBases []IndustryKnowledgeBaseRef `json:"industry_knowledge_bases"`
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
		r, err := s.toAssistantVersionResult(ctx, row)
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
	return s.toAssistantVersionResult(ctx, row)
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

// assistantVersionBaseResult 把 sqlc 行转成对外视图的基础字段，不访问其它表。
func assistantVersionBaseResult(row sqlc.AssistantVersion) (AssistantVersionResult, error) {
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

// toAssistantVersionResult 补齐版本关联的行业库 id/name，供 handler 和前端识别检索来源。
func (s *AssistantVersionService) toAssistantVersionResult(ctx context.Context, row sqlc.AssistantVersion) (AssistantVersionResult, error) {
	result, err := assistantVersionBaseResult(row)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	result.IndustryKnowledgeBases = []IndustryKnowledgeBaseRef{}
	bases, err := s.store.ListIndustryKnowledgeBasesByAssistantVersion(ctx, row.ID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("查询版本行业知识库失败: %w", err)
	}
	for _, base := range bases {
		result.IndustryKnowledgeBases = append(result.IndustryKnowledgeBases, IndustryKnowledgeBaseRef{
			ID:   base.ID,
			Name: base.Name,
		})
	}
	return result, nil
}

// withAssistantVersionTx 在事务中执行版本写操作；单测未注入 runner 时退化为直接使用 store。
func (s *AssistantVersionService) withAssistantVersionTx(ctx context.Context, fn func(AssistantVersionStore) error) error {
	if s.tx != nil {
		return s.tx.WithAssistantVersionTx(ctx, fn)
	}
	return fn(s.store)
}

// decodeSkills 把 skills_json 解为切片，供 Update/skill 操作复用。
func decodeSkills(raw []byte) ([]AssistantVersionSkill, error) {
	return DecodeVersionSkills(raw)
}

// DecodeVersionSkills 是 decodeSkills 的导出版本，供 worker 包等跨包场景复用，
// 避免在多处重复 JSON 解码逻辑。
func DecodeVersionSkills(raw []byte) ([]AssistantVersionSkill, error) {
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
	// IndustryKnowledgeBaseIDs 是该版本运行时额外检索的行业库 ID 列表，保存后立即生效。
	IndustryKnowledgeBaseIDs []string
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
	industryIDs, err := s.normalizeIndustryKnowledgeBaseIDs(ctx, in.IndustryKnowledgeBaseIDs)
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
	// 版本字段与行业库关联必须同事务提交，否则关联插入失败会造成版本已改但检索范围部分更新。
	if err := s.withAssistantVersionTx(ctx, func(store AssistantVersionStore) error {
		// UpdateAssistantVersion 为 :exec，不返回行；事务提交后重新按 ID 读取最新数据。
		if err := store.UpdateAssistantVersion(ctx, sqlc.UpdateAssistantVersionParams{
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
			return fmt.Errorf("更新版本失败: %w", err)
		}
		return s.replaceNormalizedIndustryKnowledgeBases(ctx, store, current.ID, industryIDs)
	}); err != nil {
		return AssistantVersionResult{}, err
	}
	row, err := s.store.GetAssistantVersion(ctx, current.ID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("重新查询更新后版本失败: %w", err)
	}
	return s.toAssistantVersionResult(ctx, row)
}

// normalizeIndustryKnowledgeBaseIDs 去除空白、过滤空值并校验行业库存在。
// 该步骤必须在删除旧关联前完成，避免输入含无效 ID 时清空原运行时检索范围。
func (s *AssistantVersionService) normalizeIndustryKnowledgeBaseIDs(ctx context.Context, ids []string) ([]string, error) {
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
		if _, err := s.store.GetIndustryKnowledgeBase(ctx, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrIndustryKnowledgeNotFound, id)
			}
			return nil, fmt.Errorf("查询行业知识库失败: %w", err)
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// replaceIndustryKnowledgeBases 替换版本行业库关联；直接调用时也会先校验全部 ID。
func (s *AssistantVersionService) replaceIndustryKnowledgeBases(ctx context.Context, versionID string, ids []string) error {
	normalized, err := s.normalizeIndustryKnowledgeBaseIDs(ctx, ids)
	if err != nil {
		return err
	}
	return s.withAssistantVersionTx(ctx, func(store AssistantVersionStore) error {
		return s.replaceNormalizedIndustryKnowledgeBases(ctx, store, versionID, normalized)
	})
}

// replaceNormalizedIndustryKnowledgeBases 用已校验、去重后的 ID 重建关联表。
func (s *AssistantVersionService) replaceNormalizedIndustryKnowledgeBases(ctx context.Context, store AssistantVersionStore, versionID string, ids []string) error {
	if err := store.ReplaceAssistantVersionIndustryKnowledgeBases(ctx, versionID); err != nil {
		return fmt.Errorf("清空版本行业知识库关联失败: %w", err)
	}
	for _, id := range ids {
		if err := store.AddAssistantVersionIndustryKnowledgeBase(ctx, sqlc.AddAssistantVersionIndustryKnowledgeBaseParams{
			VersionID:               versionID,
			IndustryKnowledgeBaseID: id,
		}); err != nil {
			return fmt.Errorf("保存版本行业知识库关联失败: %w", err)
		}
	}
	return nil
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

// AddSkillFromLibraryInput 是从市场选 skill 配进版本的入参。
type AddSkillFromLibraryInput struct {
	// Source 是 skill 来源类型，接受 "platform" 或 "clawhub"。
	Source string
	// SourceRef 是来源内精准标识；platform=skill name，clawhub=slug。
	SourceRef string
	// Name 是 skill 在版本内的唯一目录名；clawhub 来源必填（用 displayName，与 per-app 安装一致），
	// platform 来源可空（以平台库 DB 的 name 为准）。
	Name string
	// Version 是目标版本号。
	Version string
}

// AddSkillFromLibrary 从市场（平台库 / ClawHub）选一个 skill 配进版本快照：
//  1. 权限校验（CanManageAssistantVersion）
//  2. 查版本
//  3. 按来源解析为自包含快照（platform 引用平台库归档；clawhub 下载并缓存）
//  4. 同 name 冲突检查（ErrAssistantVersionSkillNameTaken）
//  5. 追加快照并 revision +1
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
	snap, err := s.resolveLibrarySkill(ctx, in)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	// 同 name 冲突：同一版本内 skill 名称必须唯一。
	for _, sk := range skills {
		if sk.Name == snap.Name {
			return AssistantVersionResult{}, ErrAssistantVersionSkillNameTaken
		}
	}
	skills = append(skills, snap)
	return s.persistSkills(ctx, row, skills)
}

// resolveLibrarySkill 按来源把入参解析为一条自包含 AssistantVersionSkill 快照。
//   - platform：查平台库引用其已持久化的归档（library/platform/<name>/<ver>.tar），不下载。
//   - clawhub：下载 zip → 缓存到对象存储（library/clawhub/<slug>/<ver>.zip）→ 本地算 sha256。
func (s *AssistantVersionService) resolveLibrarySkill(ctx context.Context, in AddSkillFromLibraryInput) (AssistantVersionSkill, error) {
	switch in.Source {
	case "platform":
		// 查平台库取 skill 元数据；未找到时映射为 ErrPlatformSkillNotFound。
		ps, err := s.platformSkills.GetPlatformSkillByNameVersion(ctx, sqlc.GetPlatformSkillByNameVersionParams{
			Name: in.SourceRef, Version: in.Version,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return AssistantVersionSkill{}, ErrPlatformSkillNotFound
			}
			return AssistantVersionSkill{}, fmt.Errorf("查询平台库 skill 失败: %w", err)
		}
		return AssistantVersionSkill{
			Source: "platform", SourceRef: ps.Name, Name: ps.Name, Version: ps.Version,
			CachedPath: ps.TarPath, FileSize: ps.FileSize, FileSha256: ps.FileSha256,
		}, nil
	case "clawhub":
		// 未配 ClawHub（downloader/blobs 任一为 nil）时拒绝，前端市场此时也不会展示 clawhub 条目。
		if s.clawhub == nil || s.blobs == nil {
			return AssistantVersionSkill{}, ErrAppSkillSourceUnknown
		}
		if in.Name == "" {
			return AssistantVersionSkill{}, fmt.Errorf("%w: clawhub 来源缺少 name", ErrAssistantVersionInvalid)
		}
		archive, err := s.clawhub.Download(ctx, in.SourceRef, in.Version)
		if err != nil {
			return AssistantVersionSkill{}, fmt.Errorf("从 ClawHub 下载 skill 失败: %w", err)
		}
		relPath, err := s.blobs.PutLibrarySkill("clawhub", in.SourceRef, in.Version, "zip", archive)
		if err != nil {
			return AssistantVersionSkill{}, fmt.Errorf("缓存 ClawHub skill 归档失败: %w", err)
		}
		sum := sha256.Sum256(archive)
		return AssistantVersionSkill{
			Source: "clawhub", SourceRef: in.SourceRef, Name: in.Name, Version: in.Version,
			CachedPath: relPath, FileSize: int64(len(archive)), FileSha256: hex.EncodeToString(sum[:]),
		}, nil
	default:
		return AssistantVersionSkill{}, ErrAppSkillSourceUnknown
	}
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
	return s.toAssistantVersionResult(ctx, updated)
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
	industryIDs, err := s.normalizeIndustryKnowledgeBaseIDs(ctx, in.IndustryKnowledgeBaseIDs)
	if err != nil {
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
	// 新版本和行业库关联同事务创建，避免关联失败后留下无法重试的同名孤儿版本。
	if err := s.withAssistantVersionTx(ctx, func(store AssistantVersionStore) error {
		if err := store.CreateAssistantVersion(ctx, sqlc.CreateAssistantVersionParams{
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
			return fmt.Errorf("写入版本失败: %w", err)
		}
		return s.replaceNormalizedIndustryKnowledgeBases(ctx, store, newID, industryIDs)
	}); err != nil {
		return AssistantVersionResult{}, err
	}
	row, err := s.store.GetAssistantVersion(ctx, newID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("读取新建版本失败: %w", err)
	}
	return s.toAssistantVersionResult(ctx, row)
}
