// Package service - WebPublishService 负责 app runtime token 发布静态站点的核心逻辑。
// 入口为 Publish：验证 token → 校验企业开通状态 → slug 归属与配额检查 →
// 解包 tar.gz 到新版本前缀（防 zip-slip + 大小上限）→ 原子切版本指针 + 重置 TTL → 删旧版本前缀。
// 首发与更新对调用方一致返回 {URL, ExpiresAt}。
package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	mlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// ErrWebPublishNotProvisioned 表示企业 web-publish 能力未开通或尚未就绪。
// handler 层可据此返回 403/422。
var ErrWebPublishNotProvisioned = errors.New("企业未开通 web-publish 能力")

// WebPublishStore 是 WebPublishService 需要的最小存储能力抽象。
// 仅包含本服务实际调用的方法，避免强依赖具体 Queries 类型。
type WebPublishStore interface {
	// GetAppByRuntimeTokenHash 按 runtime token hash（null.String）查找活跃 App；不存在返回 sql.ErrNoRows。
	GetAppByRuntimeTokenHash(ctx context.Context, hash null.String) (sqlc.App, error)
	// GetWebPublishConfig 按企业 ID 查 web-publish 配置；不存在返回 sql.ErrNoRows。
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	// GetPublishedSiteByHost 按完整 host 查站点记录；不存在返回 sql.ErrNoRows。
	GetPublishedSiteByHost(ctx context.Context, host string) (sqlc.PublishedSite, error)
	// CountActiveSitesByOrg 统计企业下 status='active' 的站点数，用于配额校验。
	CountActiveSitesByOrg(ctx context.Context, orgID string) (int64, error)
	// CreatePublishedSite 插入新站点记录（首次发布）。
	CreatePublishedSite(ctx context.Context, arg sqlc.CreatePublishedSiteParams) error
	// UpdatePublishedSiteVersion 原子更新版本指针、前缀、大小、过期时间（原地更新）。
	UpdatePublishedSiteVersion(ctx context.Context, arg sqlc.UpdatePublishedSiteVersionParams) error
}

// publishObjectStore 是 WebPublishService 需要的对象存储能力子集。
// 单独抽象而非复用完整 ObjectStore，保持依赖最小化、测试易注入。
type publishObjectStore interface {
	// PutObject 上传单个对象；size < 0 表示未知大小，实现可缓冲处理。
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	// DeletePrefix 删除指定前缀下所有对象（旧版本清理）。
	DeletePrefix(ctx context.Context, prefix string) error
}

// WebPublishServiceConfig 允许调用方注入可替换的行为函数，方便测试时固定随机性与时钟。
// 所有字段均有合理默认值，生产代码无需显式设置。
type WebPublishServiceConfig struct {
	// SlugGen 生成随机 slug（DNS label 格式）；默认使用 UUID 前 8 位去横线。
	// 测试时注入固定函数以保证结果可重复。
	SlugGen func() string
	// Now 返回当前时间；默认为 time.Now。
	// 测试时注入固定时间以保证 ExpiresAt 可断言。
	Now func() time.Time
	// MaxUploadSize 限制单次上传解压的最大字节数（所有文件之和）；默认 200MB。
	MaxUploadSize int64
}

// PublishResult 是 Publish 方法的成功返回值，对首发和更新场景统一。
type PublishResult struct {
	// URL 是站点的完整访问地址，格式为 https://<slug>.<base_domain>。
	URL string `json:"url"`
	// ExpiresAt 是本次发布后的过期时间（now + site_ttl_days）。
	ExpiresAt time.Time `json:"expires_at"`
}

// WebPublishService 提供 app runtime token 驱动的静态站点发布能力。
// 设计约束：
//   - Publish 是唯一的业务入口，首发/更新对外接口完全一致；
//   - 版本指针（CurrentVersion/S3Prefix）在 DB 更新后才删旧前缀，顺序保证原子性；
//   - 解包过程防 zip-slip（路径 Clean 后拒绝 ../ 越界），并有全局大小上限。
type WebPublishService struct {
	store  WebPublishStore
	obj    publishObjectStore
	slugFn func() string
	nowFn  func() time.Time
	maxSize int64
}

// NewWebPublishService 创建 WebPublishService。
// cfg 中未设置的函数字段将使用默认实现（生产安全）。
func NewWebPublishService(store WebPublishStore, obj publishObjectStore, cfg WebPublishServiceConfig) *WebPublishService {
	s := &WebPublishService{
		store:   store,
		obj:     obj,
		slugFn:  cfg.SlugGen,
		nowFn:   cfg.Now,
		maxSize: cfg.MaxUploadSize,
	}
	// 补充默认值：SlugGen 使用 UUID 前 8 位去横线，保证生成结果是合法 DNS label。
	if s.slugFn == nil {
		s.slugFn = func() string {
			id := newUUID()                         // e.g. "550e8400-e29b-41d4-a716-446655440000"
			clean := strings.ReplaceAll(id, "-", "") // 去除横线
			if len(clean) > 8 {
				return clean[:8]
			}
			return clean
		}
	}
	// 默认时钟使用系统时间。
	if s.nowFn == nil {
		s.nowFn = time.Now
	}
	// 默认最大上传大小 200MB。
	if s.maxSize <= 0 {
		s.maxSize = 200 * 1024 * 1024
	}
	return s
}

// Publish 解析 app runtime token，校验企业开通状态，执行 slug/配额检查，
// 将 body（tar.gz）解包上传到新版本前缀，原子更新版本指针，删旧前缀。
// 返回站点 URL 与新的过期时间，首发与更新对调用方接口完全一致。
func (s *WebPublishService) Publish(ctx context.Context, appToken, slug string, body io.Reader) (PublishResult, error) {
	// ── 步骤1：token → app ────────────────────────────────────────────────────
	// 对 plain token 做 hash 后查库，避免明文 token 落日志或被 SQL 层比较明文。
	if strings.TrimSpace(appToken) == "" {
		return PublishResult{}, fmt.Errorf("无效的 app token")
	}
	hash := HashAppRuntimeToken(appToken)
	app, err := s.store.GetAppByRuntimeTokenHash(ctx, null.StringFrom(hash))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishResult{}, fmt.Errorf("无效的 app token")
	}
	if err != nil {
		return PublishResult{}, fmt.Errorf("验证 app token 失败: %w", err)
	}

	// ── 步骤2：校验企业 web-publish 能力已就绪 ───────────────────────────────
	// 只有 sql.ErrNoRows 才视为「未开通」（企业尚未配置）；其他错误（DB 超时/死锁等）
	// 原样透传，让上层映射 500，避免把底层故障伪装成 403/未开通。
	cfg, err := s.store.GetWebPublishConfig(ctx, app.OrgID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, fmt.Errorf("查询 web-publish 配置失败: %w", err)
		}
		return PublishResult{}, ErrWebPublishNotProvisioned
	}
	if !cfg.Enabled || cfg.ProvisioningStatus != domain.ProvisioningReady {
		return PublishResult{}, ErrWebPublishNotProvisioned
	}

	// ── 步骤3：确定 slug 并校验格式，构造 host ───────────────────────────────
	if slug == "" {
		// 未指定 slug 时随机生成一个合法的 DNS label。
		slug = s.slugFn()
	}
	if err := validateSlug(slug); err != nil {
		return PublishResult{}, err
	}
	host := slug + "." + cfg.BaseDomain

	// ── 步骤4：查 host 归属，检查配额 ────────────────────────────────────────
	existing, lookupErr := s.store.GetPublishedSiteByHost(ctx, host)
	isUpdate := lookupErr == nil
	if !isUpdate && !errors.Is(lookupErr, sql.ErrNoRows) {
		// 非"不存在"的其他查询错误，属于意外故障。
		return PublishResult{}, fmt.Errorf("查询站点记录失败: %w", lookupErr)
	}
	if isUpdate && existing.AppID != app.ID {
		// host 已被其他 app 占用，不允许越权发布。
		return PublishResult{}, fmt.Errorf("slug %q 已占用，该域名已被其他实例持有", slug)
	}
	if !isUpdate {
		// 新建站点前检查配额上限，避免无限制创建。
		count, err := s.store.CountActiveSitesByOrg(ctx, app.OrgID)
		if err != nil {
			return PublishResult{}, fmt.Errorf("查询站点配额失败: %w", err)
		}
		if count >= int64(cfg.MaxSites) {
			return PublishResult{}, fmt.Errorf("已达站点配额上限（%d），请先删除旧站点", cfg.MaxSites)
		}
	}

	// ── 步骤5：确定站点 ID、新版本号、新前缀 ─────────────────────────────────
	var siteID string
	var nextVer string
	if isUpdate {
		// 原地更新：复用已有 siteID，版本号递增。
		siteID = existing.ID
		nextVer = bumpVersion(existing.CurrentVersion)
	} else {
		// 首次发布：生成新 UUID 作为 siteID，从 v1 开始。
		siteID = newUUID()
		nextVer = "v1"
	}
	// 对象存储前缀格式：published-sites/<siteID>/<version>/（末尾带 /）。
	newPrefix := fmt.Sprintf("published-sites/%s/%s/", siteID, nextVer)

	// ── 步骤6：解包 tar.gz 并上传到新版本前缀 ────────────────────────────────
	// 防 zip-slip：对每个 tar entry 的 name 做 path.Clean，拒绝 ../ 越界。
	// 全局大小上限：所有文件之和不超过 MaxUploadSize。
	totalSize, err := s.unpackToPrefix(ctx, body, newPrefix)
	if err != nil {
		return PublishResult{}, fmt.Errorf("解包上传失败: %w", err)
	}

	// ── 步骤7：原子切换版本指针，重置 TTL，删旧前缀 ─────────────────────────
	expiresAt := s.nowFn().Add(time.Duration(cfg.SiteTtlDays) * 24 * time.Hour)
	if isUpdate {
		// 先更新 DB 中的版本指针，确保新流量路由到新前缀后再删除旧对象。
		if err := s.store.UpdatePublishedSiteVersion(ctx, sqlc.UpdatePublishedSiteVersionParams{
			CurrentVersion: nextVer,
			S3Prefix:       newPrefix,
			SizeBytes:      totalSize,
			ExpiresAt:      expiresAt,
			ID:             siteID,
		}); err != nil {
			return PublishResult{}, fmt.Errorf("更新站点版本指针失败: %w", err)
		}
		// 删除旧版本前缀下所有对象（版本指针已切换，旧对象不再被引用）。
		if err := s.obj.DeletePrefix(ctx, existing.S3Prefix); err != nil {
			// 删除失败不阻断响应（DB 指针已切到新前缀，发布已生效），但必须记一条 warning，
			// 否则运维无法发现遗留的孤儿前缀。旧对象会在下次发布或巡检时被清理。
			slog.WarnContext(ctx, "清理旧版本前缀失败", "prefix", existing.S3Prefix, mlog.Err(err))
		}
	} else {
		// 首次发布：插入新站点行。
		if err := s.store.CreatePublishedSite(ctx, sqlc.CreatePublishedSiteParams{
			ID:             siteID,
			OrgID:          app.OrgID,
			AppID:          app.ID,
			Host:           host,
			Slug:           slug,
			CurrentVersion: nextVer,
			S3Prefix:       newPrefix,
			SizeBytes:      totalSize,
			ExpiresAt:      expiresAt,
		}); err != nil {
			return PublishResult{}, fmt.Errorf("创建站点记录失败: %w", err)
		}
	}

	return PublishResult{
		URL:       "https://" + host,
		ExpiresAt: expiresAt,
	}, nil
}

// unpackToPrefix 将 body（gzip 压缩的 tar 归档）中的所有普通文件解包并上传到 prefix 下。
// 安全约束：
//   - 每个 entry 名经 path.Clean 后若以 ../ 开头则拒绝（zip-slip 防御）；
//   - 非普通文件（目录/符号链接等）跳过；
//   - 所有文件字节总和不得超过 s.maxSize（防止 bomb 耗尽内存/带宽）。
//
// 返回实际上传的总字节数。
func (s *WebPublishService) unpackToPrefix(ctx context.Context, body io.Reader, prefix string) (int64, error) {
	// 解 gzip 外层压缩。
	gr, err := gzip.NewReader(body)
	if err != nil {
		return 0, fmt.Errorf("解析 gzip 头失败，归档格式不合法: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var totalSize int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// 归档读取完毕，正常退出。
			break
		}
		if err != nil {
			return 0, fmt.Errorf("读取 tar entry 失败: %w", err)
		}

		// 仅处理普通文件，跳过目录、符号链接等特殊类型。
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}

		// zip-slip 防御：对 entry 名做 path.Clean（去除冗余 / 和 .），
		// 拒绝清理后仍以 ../ 开头的路径，防止写出归档根目录之外。
		cleanName := path.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") {
			return 0, fmt.Errorf("非法的归档路径 %q（路径越界）", hdr.Name)
		}
		// 拒绝绝对路径（如 /etc/passwd）：拼到 prefix 后会产生双斜杠脏 key，
		// 且语义上绝对路径不应出现在站点静态资源归档里，直接跳过。
		if path.IsAbs(cleanName) {
			continue
		}

		// 大小上限预检：tar header 中的 Size 字段可能伪造，以实际读取量为准；
		// 此处作为早期拒绝手段，减少无效上传。
		if totalSize+hdr.Size > s.maxSize {
			return 0, fmt.Errorf("归档解压后总大小超过上限 %d 字节", s.maxSize)
		}

		// 构造目标对象 key：prefix + cleanName（prefix 末尾已带 /）。
		objKey := prefix + cleanName

		// 使用 io.LimitReader 防止 tar entry 声明 Size 与实际内容不符时超量读取。
		lr := io.LimitReader(tr, s.maxSize-totalSize+1)
		if err := s.obj.PutObject(ctx, objKey, lr, hdr.Size); err != nil {
			return 0, fmt.Errorf("上传文件 %q 失败: %w", cleanName, err)
		}
		totalSize += hdr.Size
	}

	return totalSize, nil
}

// bumpVersion 将 "vN" 格式的版本号递增为 "v(N+1)"。
// 若 cur 格式非法或解析失败，返回 "v2" 作为保守默认值。
func bumpVersion(cur string) string {
	if !strings.HasPrefix(cur, "v") {
		return "v2"
	}
	n, err := strconv.Atoi(cur[1:])
	if err != nil || n < 1 {
		// 格式不符合 vN，保守返回 v2。
		return "v2"
	}
	return fmt.Sprintf("v%d", n+1)
}

// validateSlug 校验 slug 是否符合 DNS label 规范：
//   - 仅含小写字母、数字和连字符；
//   - 长度 1~63；
//   - 不以连字符开头或结尾。
func validateSlug(slug string) error {
	if len(slug) == 0 || len(slug) > 63 {
		return fmt.Errorf("slug 长度必须在 1~63 之间，当前长度 %d", len(slug))
	}
	if slug[0] == '-' || slug[len(slug)-1] == '-' {
		return fmt.Errorf("slug 不能以连字符开头或结尾: %q", slug)
	}
	for _, c := range slug {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return fmt.Errorf("slug 只允许小写字母、数字和连字符，非法字符 %q: %q", c, slug)
		}
	}
	return nil
}
