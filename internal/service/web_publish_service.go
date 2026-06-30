// Package service - WebPublishService 负责 app runtime token 发布静态站点的核心逻辑。
// 入口为 Publish：验证 token → 校验企业开通状态 → slug 归属与配额检查 →
// 解包 tar.gz 到新版本前缀（防 zip-slip + 大小上限）→ 原子切版本指针 + 重置 TTL → 删旧版本前缀。
// 首发与更新对调用方一致返回 {URL, ExpiresAt}。
package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/domain"
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
	// CreatePublishedSite 插入新站点记录（每次发布都是新站点）。
	CreatePublishedSite(ctx context.Context, arg sqlc.CreatePublishedSiteParams) error
	// ListActiveSites 返回所有 status='active' 的站点路由摘要，供 site-server 同步。
	ListActiveSites(ctx context.Context) ([]sqlc.ListActiveSitesRow, error)
}

// SiteSyncRecord 是同步端点返回的单条记录（字段与 site-server SiteRecord JSON 对齐）。
type SiteSyncRecord struct {
	Host     string `json:"host"`
	SiteID   string `json:"site_id"`
	S3Prefix string `json:"s3_prefix"`
	Status   string `json:"status"`
}

// ListActiveSitesForSync 返回所有 active 站点路由信息，供 site-server 轮询同步。
func (s *WebPublishService) ListActiveSitesForSync(ctx context.Context) ([]SiteSyncRecord, error) {
	rows, err := s.store.ListActiveSites(ctx)
	if err != nil {
		return nil, err
	}
	// 将 DB 行映射到同步契约结构体（字段顺序与 site-server SiteRecord json tag 对齐）。
	out := make([]SiteSyncRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, SiteSyncRecord{
			Host:     r.Host,
			SiteID:   r.ID,
			S3Prefix: r.S3Prefix,
			Status:   r.Status,
		})
	}
	return out, nil
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

// Publish 解析 app runtime token，校验企业开通状态，分配随机唯一子域并校验配额，
// 将 body（tar.gz）解包上传到对象存储，插入站点记录，返回站点 URL 与过期时间。
// 命名策略：每次发布都创建全新随机站点，不接受调用方指定名字、不做原地更新——
// 反复发布永不撞名、URL 不可猜测；要改内容就再发布一个新地址（旧站点按 TTL 自动回收）。
func (s *WebPublishService) Publish(ctx context.Context, appToken string, body io.Reader) (PublishResult, error) {
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

	// ── 步骤3：分配随机唯一子域 + 配额检查 ───────────────────────────────────
	// 每次发布都创建全新随机站点，不接受调用方指定名字、不做原地更新：保证反复发布
	// 永不撞名、URL 不可猜测（曾出现模型用产品名派生固定子域导致撞名/静默覆盖的问题）。
	slug, host, err := s.allocateRandomHost(ctx, cfg.BaseDomain)
	if err != nil {
		return PublishResult{}, err
	}
	count, err := s.store.CountActiveSitesByOrg(ctx, app.OrgID)
	if err != nil {
		return PublishResult{}, fmt.Errorf("查询站点配额失败: %w", err)
	}
	if count >= int64(cfg.MaxSites) {
		// 配额上限是业务拒绝，用 ErrConflict 包装映射 409 + 清晰文案（避免误报 500）。
		return PublishResult{}, fmt.Errorf("%w: 已达站点配额上限（%d），请先删除旧站点", ErrConflict, cfg.MaxSites)
	}

	// ── 步骤4：确定站点 ID 与对象存储前缀 ────────────────────────────────────
	// 每个站点都是新建，版本固定 v1；前缀格式：published-sites/<siteID>/v1/（末尾带 /）。
	siteID := newUUID()
	const version = "v1"
	newPrefix := fmt.Sprintf("published-sites/%s/%s/", siteID, version)

	// ── 步骤5：解包 tar.gz 并上传到前缀 ──────────────────────────────────────
	// 防 zip-slip：对每个 tar entry 的 name 做 path.Clean，拒绝 ../ 越界。
	// 全局大小上限：所有文件之和不超过 MaxUploadSize。
	totalSize, err := s.unpackToPrefix(ctx, body, newPrefix)
	if err != nil {
		return PublishResult{}, fmt.Errorf("解包上传失败: %w", err)
	}

	// ── 步骤6：插入新站点行，设置 TTL ────────────────────────────────────────
	expiresAt := s.nowFn().Add(time.Duration(cfg.SiteTtlDays) * 24 * time.Hour)
	if err := s.store.CreatePublishedSite(ctx, sqlc.CreatePublishedSiteParams{
		ID:             siteID,
		OrgID:          app.OrgID,
		AppID:          app.ID,
		Host:           host,
		Slug:           slug,
		CurrentVersion: version,
		S3Prefix:       newPrefix,
		SizeBytes:      totalSize,
		ExpiresAt:      expiresAt,
	}); err != nil {
		return PublishResult{}, fmt.Errorf("创建站点记录失败: %w", err)
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

		// AWS S3 SDK v2 默认对 PutObject 做 payload 签名，需要 body 可 seek 以计算 SHA256；
		// tar entry 流不可 seek，直接传会报 "request stream is not seekable" 导致上传失败。
		// 静态站点单文件体积小，这里把内容读入内存（用 LimitReader 限制在 maxSize 剩余额度 +1 字节，
		// 超出即判超限）后用可 seek 的 bytes.Reader 上传。同时以实际读取字节数累加 totalSize，
		// 不再信任可能被伪造的 hdr.Size。
		remaining := s.maxSize - totalSize
		data, err := io.ReadAll(io.LimitReader(tr, remaining+1))
		if err != nil {
			return 0, fmt.Errorf("读取归档文件 %q 失败: %w", cleanName, err)
		}
		if int64(len(data)) > remaining {
			return 0, fmt.Errorf("归档解压后总大小超过上限 %d 字节", s.maxSize)
		}
		if err := s.obj.PutObject(ctx, objKey, bytes.NewReader(data), int64(len(data))); err != nil {
			return 0, fmt.Errorf("上传文件 %q 失败: %w", cleanName, err)
		}
		totalSize += int64(len(data))
	}

	return totalSize, nil
}

// allocateRandomHost 在 baseDomain 下分配一个未被占用的随机子域，返回 (slug, host)。
// 重试至多 5 次规避随机碰撞（slug 空间足够大，碰撞概率极低）；连续命中已存在视为异常返回错误。
func (s *WebPublishService) allocateRandomHost(ctx context.Context, baseDomain string) (string, string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		cand := s.slugFn()
		host := cand + "." + baseDomain
		_, err := s.store.GetPublishedSiteByHost(ctx, host)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return cand, host, nil // 未占用，可用
		case err != nil:
			return "", "", fmt.Errorf("查询站点记录失败: %w", err)
		}
		// host 已存在（碰撞），换一个继续尝试。
	}
	return "", "", fmt.Errorf("分配随机站点名失败，请重试")
}

