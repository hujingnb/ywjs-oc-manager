# Web Publish — Plan 4: 发布链路 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **依赖 Plan 2 / Plan 3**：复用 Plan 2 的 `org_web_publish_config`（判断开通 + ttl/max_sites）；实现 Plan 3 定死的内部同步端点契约（`GET /internal/web-publish/sites`）。执行前 Plan 2 已合并；Plan 3 可并行但同步端点以 Plan 3 契约为准。

**Goal:** 把 hermes、manager、site-server 串起来：hermes 在对话中产出静态目录后调 `oc-publish ./<dir> [--slug xxx]`，经 manager runtime 发布端点（per-app token 鉴权）校验开通+配额、分配 `<slug>.<base_domain>`、解包上传对象存储、写/更新 `published_sites` 行（原子换版、TTL 重置、归属校验），返回 `{url, expires_at}`；site-server 通过 manager 内部同步端点拿到活跃站点路由。能力的"有/无"= `oc-publish` skill 的"注入/不注入"，由 manifest `web_publish` 段条件控制。

**Architecture:** 新表 `published_sites`（一 slug 一行，update-in-place）。发布逻辑集中在 `WebPublishService`：token→app→org→校验 `org_web_publish_config`（enabled+ready）→ 配额/归属校验 → 解包 tar 到新版本前缀 → 原子切 DB 版本指针 + 重置 TTL → 删旧版本前缀。runtime 发布端点（multipart，`X-OC-App-Token`）与内部同步端点（`X-OC-Site-Sync-Token`）两个 HTTP 入口。Go 侧 manifest 加 `web_publish` 段并在 bootstrap 装配时按 org 开通条件注入；Python 侧 `oc-publish` skill 双 variant + 渲染器条件注入 + entrypoint env。外部副作用（对象存储）用既有 `storage.ObjectStore` 接口，发布核心逻辑（slug/版本/归属/配额/解包）用 fake 全量单测。

**Tech Stack:** Go 1.25、MySQL（migration `000022`）、sqlc、`archive/tar`+`compress/gzip`（解包）、gin、`storage.ObjectStore`、testify；Python 3（`oc-publish.py`，仅标准库，与 `oc-kb.py` 同构）。

---

## 背景约束（落地前必读）

- **打包方式**：`oc-publish` 把目录打成单个 `tar.gz` 上传（multipart `file` 字段，与 `oc-kb` 手写 multipart 同款）。manager 服务端解包逐文件 `PutObject`。大目录分片上传是 spec §10 计划阶段细化点——**本 plan 先单 tar.gz（设大小上限，如 200MB）**，超限报错提示，分片留后续。
- **身份 = slug，企业基础域名内稳定**（spec §4.5）：同 `app_id` + 同 slug → update-in-place（同 host/同 URL/同行，只换内容）；不同 app 占用已存在 slug → `slug 已占用` 错误，不允许跨 app 覆盖。host = `<slug>.<base_domain>` 全局唯一（= slug 在该 org base_domain 内唯一）。
- **原子换版**（spec §4.5）：先把新内容全部 `PutObject` 到 `published-sites/<siteID>/<version>/`，**整目录传完后**再单行 DB 更新切 `current_version`/`s3_prefix`/`expires_at`；site-server 下次轮询（几秒）才切到新版本。切换后删旧版本前缀（保留几个历史版本是 §10 细化点，本 plan 切换后删上一版，简单可回收）。
- **TTL 重置**（spec §4.5）：每次发布把 `expires_at = now + site_ttl_days`，迭代中的站点不中途过期。
- **鉴权即定位**：runtime 端点不接受请求方传 app/org，`X-OC-App-Token` hash 反查 app（`GetAppByRuntimeTokenHash`），org/配额/host 全在 manager 侧定（spec §4.2）。
- **能力条件注入**（spec §4.2）：企业未开通时 manifest 无 `web_publish` 段，`oc-publish` 不渲染进 hermes，hermes 对话里压根不知道有发布能力。开通判断 = `org_web_publish_config.enabled && provisioning_status='ready'`。
- **双 variant**：`oc-publish` skill 与渲染器改动须同时落 `hermes-v2026.6.5` 与 `hermes-v2026.5.16`（spec §4.2）。
- **内部同步端点**：新建 `/internal/*` 路由组（当前不存在，见 2026-05-29 spec 记录），不走用户 JWT，用独立 `X-OC-Site-Sync-Token`（manager config）。
- OpenAPI 同步、注释、单测注释、testify 规范同前序 plan。

## File Structure

```
internal/migrations/000022_published_sites.{up,down}.sql
internal/store/queries/published_sites.sql

internal/domain/enums.go                       # 追加 SiteStatus* 枚举

internal/service/web_publish_service.go        # 发布核心 + 内部同步列表
internal/service/web_publish_service_test.go

internal/api/handlers/runtime_web_publish.go   # POST runtime 发布端点（X-OC-App-Token）
internal/api/handlers/runtime_web_publish_test.go
internal/api/handlers/internal_web_publish.go  # GET 内部同步端点（X-OC-Site-Sync-Token）
internal/api/handlers/internal_web_publish_test.go
internal/api/router.go                         # 注册 runtime 端点 + 新建 internal 路由组

internal/integrations/hermes/
  manifest.go        # 追加 ManifestWebPublish + Manifest.WebPublish
  app_input.go       # 追加 WebPublish* 字段
  build_manifest.go  # 条件写 web_publish 段
  build_manifest_test.go

internal/service/bootstrap_service.go          # 按 org 开通条件注入 web_publish

internal/config/config.go / loader.go          # 追加 SiteSyncToken

runtime/hermes/hermes-v2026.6.5/               # 双 variant 同步改动
  oc-publish.py
  renderer/render_skills.py
  oc-entrypoint.py
  lib/manifest.py
runtime/hermes/hermes-v2026.5.16/  (同上)

cmd/server/main.go                             # 装配 service / handler / 路由 / config
```

---

### Task 1: published_sites migration

**Files:**
- Create: `internal/migrations/000022_published_sites.up.sql` / `.down.sql`
- Modify: `sqlc.yaml`（schema 列表追加 000022）

- [ ] **Step 1: Write migrations**

`000022_published_sites.up.sql`：
```sql
-- published_sites 存每个已发布静态站点（一 slug 一行，反复修改 update-in-place）。
CREATE TABLE published_sites (
    id              CHAR(36)     NOT NULL COMMENT '站点 ID（siteID，UUID）',
    org_id          CHAR(36)     NOT NULL COMMENT '所属企业 ID',
    app_id          CHAR(36)     NOT NULL COMMENT '创建并归属该站点的实例 ID（update-in-place 归属校验）',
    host            VARCHAR(255) NOT NULL COMMENT '完整访问域名 <slug>.<base_domain>（全局唯一=slug 在企业域内唯一）',
    slug            VARCHAR(63)  NOT NULL COMMENT '子域 slug',
    current_version VARCHAR(32)  NOT NULL COMMENT '当前版本标识（如 v1/v2，原子换版指针）',
    s3_prefix       VARCHAR(512) NOT NULL COMMENT '当前版本对象前缀 published-sites/<id>/<version>/（末尾带 /）',
    status          VARCHAR(20)  NOT NULL DEFAULT 'active' COMMENT '状态：active/disabled/expired',
    size_bytes      BIGINT       NOT NULL DEFAULT 0 COMMENT '当前版本总字节数',
    created_at      DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    expires_at      DATETIME(6)  NOT NULL COMMENT '过期时间（每次发布重置为 now + site_ttl_days）',
    updated_at      DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    PRIMARY KEY (id),
    UNIQUE KEY uq_published_sites_host (host),
    KEY idx_published_sites_org_status (org_id, status),
    KEY idx_published_sites_expires (expires_at),
    CONSTRAINT fk_published_sites_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_published_sites_app FOREIGN KEY (app_id) REFERENCES apps(id),
    CONSTRAINT published_sites_status_check CHECK (status IN ('active','disabled','expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='已发布静态站点';
```

`000022_published_sites.down.sql`：
```sql
DROP TABLE IF EXISTS published_sites;
```

在 `sqlc.yaml` schema 列表追加 `internal/migrations/000022_published_sites.up.sql`。

- [ ] **Step 2: Run migration test**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/migrations/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/migrations/000022_published_sites.up.sql internal/migrations/000022_published_sites.down.sql sqlc.yaml
git commit -m "feat(db): 新增 published_sites 表存已发布静态站点

一 slug 一行（update-in-place）：host 全局唯一、current_version/s3_prefix 原子换版
指针、status/size/expires_at；含 (org,status) 与 expires_at 索引供列表与回收，
带完整中文 COMMENT；登记进 sqlc.yaml。"
```

---

### Task 2: domain SiteStatus 枚举

**Files:**
- Modify: `internal/domain/enums.go`
- Test: `internal/domain/enums_test.go`

- [ ] **Step 1: Write the failing test**

```go
// TestSiteStatus 覆盖：三个站点状态合法、未知值非法（写库与回收前受控校验）。
func TestSiteStatus(t *testing.T) {
	for _, s := range []string{SiteStatusActive, SiteStatusDisabled, SiteStatusExpired} {
		assert.Truef(t, IsSiteStatus(s), "%s 应合法", s)
	}
	assert.False(t, IsSiteStatus("deleted"))
}
```

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/domain/ -run TestSiteStatus -v`
Expected: 编译失败 `undefined: SiteStatusActive`

- [ ] **Step 3: Implement**

在 `enums.go` const 块追加：
```go
	// SiteStatus* 描述已发布站点生命周期，落 published_sites.status。
	SiteStatusActive   = "active"   // 在线可访问
	SiteStatusDisabled = "disabled" // 手动下线（site-server 立即 404）
	SiteStatusExpired  = "expired"  // TTL 到期被 reaper 回收
```
var 块追加 `validSiteStatuses = set(SiteStatusActive, SiteStatusDisabled, SiteStatusExpired)`，并加：
```go
// IsSiteStatus 校验已发布站点状态取值是否合法。
func IsSiteStatus(value string) bool {
	_, ok := validSiteStatuses[value]
	return ok
}
```

- [ ] **Step 4: Run → pass; Commit**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/domain/ -run TestSiteStatus -v`
```bash
git add internal/domain/enums.go internal/domain/enums_test.go
git commit -m "feat(domain): 增加 published_sites 站点状态枚举 SiteStatus*"
```

---

### Task 3: sqlc 查询

**Files:**
- Create: `internal/store/queries/published_sites.sql`
- Generate: `internal/store/sqlc/published_sites.sql.go`

- [ ] **Step 1: Write queries**

```sql
-- name: GetPublishedSiteByHost :one
SELECT * FROM published_sites WHERE host = ?;

-- name: GetPublishedSiteByID :one
SELECT * FROM published_sites WHERE id = ?;

-- name: CountActiveSitesByOrg :one
-- 配额校验：统计企业当前 active 站点数。
SELECT COUNT(*) FROM published_sites WHERE org_id = ? AND status = 'active';

-- name: ListActiveSites :many
-- site-server 内部同步端点：列出所有 active 站点的路由信息。
SELECT host, id, s3_prefix, status FROM published_sites WHERE status = 'active';

-- name: ListSitesByOrg :many
-- org admin 列表（Plan 5）：本企业全部站点（任意状态）。
SELECT * FROM published_sites WHERE org_id = ? ORDER BY updated_at DESC;

-- name: CreatePublishedSite :exec
INSERT INTO published_sites (
    id, org_id, app_id, host, slug, current_version, s3_prefix, status, size_bytes, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?);

-- name: UpdatePublishedSiteVersion :exec
-- 原子换版 + TTL 重置：切当前版本指针、刷大小与过期时间、置回 active。
UPDATE published_sites
SET current_version = ?, s3_prefix = ?, size_bytes = ?, status = 'active', expires_at = ?, updated_at = now()
WHERE id = ?;

-- name: SetPublishedSiteStatus :exec
-- 手动下线 / reaper 置过期（Plan 5）。
UPDATE published_sites SET status = ?, updated_at = now() WHERE id = ?;

-- name: ListExpiredActiveSites :many
-- reaper（Plan 5）：扫出已过期但仍 active 的站点。
SELECT * FROM published_sites WHERE status = 'active' AND expires_at < now();
```

- [ ] **Step 2: Generate + build**

Run: `cd /home/user/ywjs-oc-manager && go run github.com/sqlc-dev/sqlc/cmd/sqlc generate && go build ./internal/store/...`
Expected: 生成 `published_sites.sql.go`，编译通过

- [ ] **Step 3: Commit**

```bash
git add internal/store/queries/published_sites.sql internal/store/sqlc/
git commit -m "feat(store): 增加 published_sites 的 sqlc 查询

覆盖按 host/id 查、配额计数、active 列表（site-server 同步）、org 列表、创建、
原子换版更新、状态置换与过期扫描（供 Plan 5 reaper）。"
```

---

### Task 4: WebPublishService —— 发布核心（update-in-place）

**Files:**
- Create: `internal/service/web_publish_service.go`
- Test: `internal/service/web_publish_service_test.go`

> 这是本 plan 的核心，状态最复杂。外部依赖（store / objStore / slug 生成 / 时钟）全注入，便于单测覆盖首发、update-in-place、跨 app 拒绝、配额、未开通。

- [ ] **Step 1: Write the failing test**

```go
package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"testing"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	null "github.com/guregu/null/v5"
)

// --- 测试替身 ---

type fakeWPubStore struct {
	app      sqlc.App
	appErr   error
	cfg      sqlc.OrgWebPublishConfig
	byHost   map[string]sqlc.PublishedSite
	activeN  int64
	created  *sqlc.CreatePublishedSiteParams
	updated  *sqlc.UpdatePublishedSiteVersionParams
}

func (f *fakeWPubStore) GetAppByRuntimeTokenHash(_ context.Context, _ string) (sqlc.App, error) { return f.app, f.appErr }
func (f *fakeWPubStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) { return f.cfg, nil }
func (f *fakeWPubStore) GetPublishedSiteByHost(_ context.Context, host string) (sqlc.PublishedSite, error) {
	s, ok := f.byHost[host]
	if !ok { return sqlc.PublishedSite{}, sql.ErrNoRows } // import database/sql
	return s, nil
}
func (f *fakeWPubStore) CountActiveSitesByOrg(_ context.Context, _ string) (int64, error) { return f.activeN, nil }
func (f *fakeWPubStore) CreatePublishedSite(_ context.Context, p sqlc.CreatePublishedSiteParams) error { f.created = &p; return nil }
func (f *fakeWPubStore) UpdatePublishedSiteVersion(_ context.Context, p sqlc.UpdatePublishedSiteVersionParams) error { f.updated = &p; return nil }

// fakeObjStore 实现 storage.ObjectStore 的子集（其余方法 panic，本测试不触）。
type fakeObjStore struct {
	put     map[string][]byte
	deleted []string
}
func newFakeObjStore() *fakeObjStore { return &fakeObjStore{put: map[string][]byte{}} }
func (f *fakeObjStore) PutObject(_ context.Context, key string, r io.Reader, _ int64) error {
	b, _ := io.ReadAll(r); f.put[key] = b; return nil
}
func (f *fakeObjStore) DeletePrefix(_ context.Context, prefix string) error { f.deleted = append(f.deleted, prefix); return nil }
func (f *fakeObjStore) PresignGet(context.Context, string, time.Duration) (string, error) { panic("unused") }
func (f *fakeObjStore) ObjectExists(context.Context, string) (bool, error) { panic("unused") }
func (f *fakeObjStore) ListObjects(context.Context, string) ([]storage.ObjectInfo, error) { panic("unused") }
func (f *fakeObjStore) MovePrefix(context.Context, string, string) error { panic("unused") }

// makeTarGz 把 files（相对路径→内容）打成 tar.gz，模拟 oc-publish 上传体。
func makeTarGz(files map[string]string) *bytes.Reader {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close(); _ = gz.Close()
	return bytes.NewReader(buf.Bytes())
}

func readyCfg() sqlc.OrgWebPublishConfig {
	return sqlc.OrgWebPublishConfig{OrgID: "org-1", Enabled: true,
		ProvisioningStatus: domain.ProvisioningReady, BaseDomain: "apps.example.com",
		SiteTtlDays: 7, MaxSites: 20}
}

func newSvc(st *fakeWPubStore, obj *fakeObjStore) *WebPublishService {
	// 注入确定性 slug 生成与固定时钟，便于断言
	return NewWebPublishService(st, obj,
		WebPublishServiceConfig{
			SlugGen: func() string { return "rand123" },
			Now:     func() time.Time { return time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC) },
		})
}

// TestPublishFirstTime 覆盖：首发（无 slug）→ 生成随机 slug、host=<slug>.<base>、
// 创建行、上传到 v1 前缀、返回 url 与 expires_at（now+7d）。
func TestPublishFirstTime(t *testing.T) {
	st := &fakeWPubStore{app: sqlc.App{ID: "app-1", OrgID: "org-1"}, cfg: readyCfg(), byHost: map[string]sqlc.PublishedSite{}}
	obj := newFakeObjStore()
	res, err := newSvc(st, obj).Publish(context.Background(), "token", "", makeTarGz(map[string]string{
		"index.html": "<h1>hi</h1>", "css/app.css": "body{}",
	}))
	require.NoError(t, err)
	assert.Equal(t, "https://rand123.apps.example.com", res.URL)
	assert.Equal(t, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), res.ExpiresAt) // now+7d
	require.NotNil(t, st.created)
	assert.Equal(t, "rand123.apps.example.com", st.created.Host)
	// 文件传到 v1 前缀
	assert.Contains(t, obj.put, "published-sites/"+st.created.ID+"/v1/index.html")
	assert.Contains(t, obj.put, "published-sites/"+st.created.ID+"/v1/css/app.css")
}

// TestPublishUpdateInPlace 覆盖：同 app 同 slug 再发 → 不新建行，切到 v2 前缀、
// 重置 TTL、删旧 v1 前缀（原子换版 + TTL 重置）。
func TestPublishUpdateInPlace(t *testing.T) {
	existing := sqlc.PublishedSite{ID: "site-1", OrgID: "org-1", AppID: "app-1",
		Host: "blog.apps.example.com", Slug: "blog", CurrentVersion: "v1",
		S3Prefix: "published-sites/site-1/v1/"}
	st := &fakeWPubStore{app: sqlc.App{ID: "app-1", OrgID: "org-1"}, cfg: readyCfg(),
		byHost: map[string]sqlc.PublishedSite{"blog.apps.example.com": existing}}
	obj := newFakeObjStore()
	res, err := newSvc(st, obj).Publish(context.Background(), "token", "blog", makeTarGz(map[string]string{"index.html": "v2"}))
	require.NoError(t, err)
	assert.Equal(t, "https://blog.apps.example.com", res.URL)
	assert.Nil(t, st.created, "update-in-place 不应新建行")
	require.NotNil(t, st.updated)
	assert.Equal(t, "v2", st.updated.CurrentVersion)
	assert.Equal(t, "published-sites/site-1/v2/", st.updated.S3Prefix)
	assert.Contains(t, obj.put, "published-sites/site-1/v2/index.html")
	assert.Contains(t, obj.deleted, "published-sites/site-1/v1/", "切换后删旧版本前缀")
}

// TestPublishSlugTakenByOtherApp 覆盖：slug 被别的 app 占用 → 拒绝，不覆盖。
func TestPublishSlugTakenByOtherApp(t *testing.T) {
	existing := sqlc.PublishedSite{ID: "site-1", AppID: "app-OTHER", Host: "blog.apps.example.com", Slug: "blog"}
	st := &fakeWPubStore{app: sqlc.App{ID: "app-1", OrgID: "org-1"}, cfg: readyCfg(),
		byHost: map[string]sqlc.PublishedSite{"blog.apps.example.com": existing}}
	_, err := newSvc(st, newFakeObjStore()).Publish(context.Background(), "token", "blog", makeTarGz(map[string]string{"index.html": "x"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已占用")
}

// TestPublishNotProvisioned 覆盖：企业未开通/未就绪 → 拒绝发布。
func TestPublishNotProvisioned(t *testing.T) {
	cfg := readyCfg(); cfg.ProvisioningStatus = domain.ProvisioningInProgress
	st := &fakeWPubStore{app: sqlc.App{ID: "app-1", OrgID: "org-1"}, cfg: cfg, byHost: map[string]sqlc.PublishedSite{}}
	_, err := newSvc(st, newFakeObjStore()).Publish(context.Background(), "token", "", makeTarGz(map[string]string{"index.html": "x"}))
	require.Error(t, err)
}

// TestPublishQuotaExceeded 覆盖：新建会超 max_sites → 拒绝（update-in-place 不受配额限制）。
func TestPublishQuotaExceeded(t *testing.T) {
	cfg := readyCfg(); cfg.MaxSites = 1
	st := &fakeWPubStore{app: sqlc.App{ID: "app-1", OrgID: "org-1"}, cfg: cfg, byHost: map[string]sqlc.PublishedSite{}, activeN: 1}
	_, err := newSvc(st, newFakeObjStore()).Publish(context.Background(), "token", "newsite", makeTarGz(map[string]string{"index.html": "x"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "配额")
}
```

> 测试 import 需补 `database/sql`、`io`。

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run TestPublish -v`
Expected: 编译失败 `undefined: NewWebPublishService / WebPublishServiceConfig`

- [ ] **Step 3: Implement**

```go
package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

// WebPublishStore 是发布服务所需的最小数据访问能力。
type WebPublishStore interface {
	GetAppByRuntimeTokenHash(ctx context.Context, hash string) (sqlc.App, error)
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	GetPublishedSiteByHost(ctx context.Context, host string) (sqlc.PublishedSite, error)
	CountActiveSitesByOrg(ctx context.Context, orgID string) (int64, error)
	CreatePublishedSite(ctx context.Context, arg sqlc.CreatePublishedSiteParams) error
	UpdatePublishedSiteVersion(ctx context.Context, arg sqlc.UpdatePublishedSiteVersionParams) error
}

// publishObjectStore 是发布服务所需的对象存储子集（*storage.S3ObjectStore 满足）。
type publishObjectStore interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	DeletePrefix(ctx context.Context, prefix string) error
}

// WebPublishServiceConfig 注入可替换依赖（便于单测确定性）。
type WebPublishServiceConfig struct {
	SlugGen      func() string    // 随机 slug 生成；nil 时用内置随机
	Now          func() time.Time // 时钟；nil 时用 time.Now
	MaxUploadSize int64           // 单次 tar.gz 解包累计上限，<=0 用默认 200MB
}

// PublishResult 是发布返回（透传给 hermes 回显给用户）。
type PublishResult struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WebPublishService 负责把 hermes 上传的静态目录发布为带域名的公网站点。
type WebPublishService struct {
	store  WebPublishStore
	obj    publishObjectStore
	slug   func() string
	now    func() time.Time
	maxSz  int64
}

// NewWebPublishService 构造服务。
func NewWebPublishService(store WebPublishStore, obj publishObjectStore, cfg WebPublishServiceConfig) *WebPublishService {
	s := &WebPublishService{store: store, obj: obj, slug: cfg.SlugGen, now: cfg.Now, maxSz: cfg.MaxUploadSize}
	if s.slug == nil {
		s.slug = randomSlug
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.maxSz <= 0 {
		s.maxSz = 200 << 20 // 200MB
	}
	return s
}

// Publish 执行发布/更新：token→app→org 开通校验 → slug/归属/配额 → 解包到新版本前缀 →
// 原子切版本指针 + 重置 TTL → 删旧版本前缀。返回 {url, expires_at}。
func (s *WebPublishService) Publish(ctx context.Context, appToken, slug string, body io.Reader) (PublishResult, error) {
	// 1. 鉴权即定位：token hash 反查 app
	app, err := s.store.GetAppByRuntimeTokenHash(ctx, hashRuntimeToken(appToken))
	if err != nil {
		return PublishResult{}, fmt.Errorf("无效的 app token: %w", err)
	}
	// 2. org 开通校验
	cfg, err := s.store.GetWebPublishConfig(ctx, app.OrgID)
	if err != nil || !cfg.Enabled || cfg.ProvisioningStatus != domain.ProvisioningReady {
		return PublishResult{}, errors.New("企业未开通网站发布能力或尚未就绪")
	}
	// 3. slug 与 host
	if slug == "" {
		slug = s.slug()
	}
	if err := validateSlug(slug); err != nil {
		return PublishResult{}, err
	}
	host := slug + "." + cfg.BaseDomain

	// 4. 归属/配额校验，决定创建还是更新
	existing, err := s.store.GetPublishedSiteByHost(ctx, host)
	isUpdate := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PublishResult{}, fmt.Errorf("查询站点失败: %w", err)
	}
	if isUpdate && existing.AppID != app.ID {
		return PublishResult{}, fmt.Errorf("slug %q 已占用", slug)
	}
	if !isUpdate {
		n, err := s.store.CountActiveSitesByOrg(ctx, app.OrgID)
		if err != nil {
			return PublishResult{}, err
		}
		if n >= int64(cfg.MaxSites) {
			return PublishResult{}, fmt.Errorf("已达站点配额上限（%d）", cfg.MaxSites)
		}
	}

	// 5. 计算 siteID 与新版本前缀
	siteID := existing.ID
	if !isUpdate {
		siteID = uuid.NewString()
	}
	nextVer := "v1"
	if isUpdate {
		nextVer = bumpVersion(existing.CurrentVersion)
	}
	newPrefix := fmt.Sprintf("published-sites/%s/%s/", siteID, nextVer)

	// 6. 解包 tar.gz 到新前缀（整目录传完才切版本）
	size, err := s.unpackToPrefix(ctx, body, newPrefix)
	if err != nil {
		return PublishResult{}, err
	}

	// 7. 原子切版本 + 重置 TTL
	expiresAt := s.now().Add(time.Duration(cfg.SiteTtlDays) * 24 * time.Hour)
	if isUpdate {
		if err := s.store.UpdatePublishedSiteVersion(ctx, sqlc.UpdatePublishedSiteVersionParams{
			CurrentVersion: nextVer, S3Prefix: newPrefix, SizeBytes: size, ExpiresAt: expiresAt, ID: siteID,
		}); err != nil {
			return PublishResult{}, err
		}
		// 切换后删旧版本前缀（保留多版本是 §10 细化点）
		_ = s.obj.DeletePrefix(ctx, existing.S3Prefix)
	} else {
		if err := s.store.CreatePublishedSite(ctx, sqlc.CreatePublishedSiteParams{
			ID: siteID, OrgID: app.OrgID, AppID: app.ID, Host: host, Slug: slug,
			CurrentVersion: nextVer, S3Prefix: newPrefix, SizeBytes: size, ExpiresAt: expiresAt,
		}); err != nil {
			return PublishResult{}, err
		}
	}
	return PublishResult{URL: "https://" + host, ExpiresAt: expiresAt}, nil
}

// unpackToPrefix 解包 tar.gz 并逐文件上传到 prefix 下，返回累计字节数。
// 防 zip-slip：清理 entry 名，拒绝越出 prefix。超大小上限报错。
func (s *WebPublishService) unpackToPrefix(ctx context.Context, body io.Reader, prefix string) (int64, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return 0, fmt.Errorf("解压失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("解包失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // 跳过目录/符号链接等，仅取常规文件
		}
		// 清理路径，消解 .. 与前导 /，杜绝越出前缀
		clean := path.Clean("/" + hdr.Name)
		rel := strings.TrimPrefix(clean, "/")
		if rel == "" || strings.HasPrefix(rel, "../") {
			continue
		}
		total += hdr.Size
		if total > s.maxSz {
			return 0, fmt.Errorf("发布内容超过上限 %d 字节", s.maxSz)
		}
		if err := s.obj.PutObject(ctx, prefix+rel, tr, hdr.Size); err != nil {
			return 0, fmt.Errorf("上传 %s 失败: %w", rel, err)
		}
	}
	return total, nil
}

// bumpVersion 把 "vN" 递增为 "v(N+1)"；解析失败回退 v1+时间无关的安全值。
func bumpVersion(cur string) string {
	n, err := strconv.Atoi(strings.TrimPrefix(cur, "v"))
	if err != nil {
		return "v2" // 历史脏值兜底：至少与 v1 不同
	}
	return "v" + strconv.Itoa(n+1)
}

// validateSlug 校验 slug 合法（DNS label：小写字母数字与连字符，1-63 字符，不以连字符开头结尾）。
func validateSlug(slug string) error {
	if len(slug) == 0 || len(slug) > 63 {
		return fmt.Errorf("slug 长度需为 1-63")
	}
	for i, r := range slug {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			return fmt.Errorf("slug 只能含小写字母数字与连字符")
		}
		if r == '-' && (i == 0 || i == len(slug)-1) {
			return fmt.Errorf("slug 不能以连字符开头或结尾")
		}
	}
	return nil
}

// randomSlug 生成默认随机短 slug（8 字符）。用 uuid 去连字符取前 8 位，避开时间依赖。
func randomSlug() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
}
```

> **`hashRuntimeToken`**：复用 `internal/service/app_runtime_token.go` 既有的 `HashAppRuntimeToken`（agent 确认存在）。把上面 `hashRuntimeToken(appToken)` 替换为该实际函数名（`grep -n "func Hash" internal/service/app_runtime_token.go` 确认签名）。
>
> **`ListActiveSitesForSync`**：内部同步端点用，加到本 service（Task 5），返回 `[]sqlc.ListActiveSitesRow` 或映射为 DTO。

- [ ] **Step 4: Run → pass**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run TestPublish -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/web_publish_service.go internal/service/web_publish_service_test.go
git commit -m "feat(service): 增加 WebPublishService 发布核心（update-in-place）

token→app→org 开通校验 → slug/归属/配额校验 → 解包 tar.gz 到新版本前缀（防
zip-slip + 大小上限）→ 原子切版本指针 + 重置 TTL → 删旧版本前缀；首发/更新对
调用方一致返回 {url, expires_at}。注入 slug 生成与时钟，核心分支全量单测。"
```

---

### Task 5: 内部同步端点（site-server 拉活跃站点）

**Files:**
- Modify: `internal/service/web_publish_service.go`（加 `ListActiveSitesForSync`）
- Create: `internal/api/handlers/internal_web_publish.go`
- Modify: `internal/api/router.go`（新建 `/internal` 路由组）
- Modify: `internal/config/config.go` / `loader.go`（加 `SiteSyncToken`）
- Test: `internal/api/handlers/internal_web_publish_test.go`

- [ ] **Step 1: service 加同步列表方法**

在 `web_publish_service.go` 追加（store 接口加 `ListActiveSites`）：
```go
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
	out := make([]SiteSyncRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, SiteSyncRecord{Host: r.Host, SiteID: r.ID, S3Prefix: r.S3Prefix, Status: r.Status})
	}
	return out, nil
}
```
（`WebPublishStore` 接口加 `ListActiveSites(ctx) ([]sqlc.ListActiveSitesRow, error)`；字段名 `ID/Host/S3Prefix/Status` 以 sqlc 生成的 row 结构为准。）

- [ ] **Step 2: Write the failing test（handler 鉴权）**

```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"oc-manager/internal/service"
)

type stubSyncService struct{ recs []service.SiteSyncRecord }
func (s *stubSyncService) ListActiveSitesForSync(context.Context) ([]service.SiteSyncRecord, error) { return s.recs, nil }

// TestSyncEndpointRejectsBadToken 覆盖：缺/错 token → 401，防止公网拉取站点清单。
func TestSyncEndpointRejectsBadToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterInternalWebPublishRoutes(r, NewInternalWebPublishHandler(&stubSyncService{}, "secret"))
	req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
	req.Header.Set("X-OC-Site-Sync-Token", "wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestSyncEndpointReturnsSites 覆盖：带正确 token → 200 + sites 数组（契约对齐 Plan 3）。
func TestSyncEndpointReturnsSites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := &stubSyncService{recs: []service.SiteSyncRecord{{Host: "blog.apps.example.com", SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"}}}
	RegisterInternalWebPublishRoutes(r, NewInternalWebPublishHandler(svc, "secret"))
	req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
	req.Header.Set("X-OC-Site-Sync-Token", "secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "blog.apps.example.com")
	assert.Contains(t, w.Body.String(), `"sites"`)
}
```

- [ ] **Step 3: Implement handler + routes + config**

`internal/api/handlers/internal_web_publish.go`：
```go
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

type syncService interface {
	ListActiveSitesForSync(ctx context.Context) ([]service.SiteSyncRecord, error)
}

// InternalWebPublishHandler 暴露 site-server 轮询用的内部同步端点。
type InternalWebPublishHandler struct {
	service syncService
	token   string // 与 site-server MANAGER_SYNC_TOKEN 共享的内部鉴权 token
}

// NewInternalWebPublishHandler 构造 handler。
func NewInternalWebPublishHandler(svc syncService, token string) *InternalWebPublishHandler {
	return &InternalWebPublishHandler{service: svc, token: token}
}

// RegisterInternalWebPublishRoutes 注册内部路由（集群内可达，独立 token，不走用户 JWT）。
func RegisterInternalWebPublishRoutes(router gin.IRouter, h *InternalWebPublishHandler) {
	g := router.Group("/internal/web-publish")
	g.GET("/sites", h.ListSites)
}

// ListSites 返回所有 active 站点路由，契约：{"sites":[{host,site_id,s3_prefix,status}]}。
func (h *InternalWebPublishHandler) ListSites(c *gin.Context) {
	if c.GetHeader("X-OC-Site-Sync-Token") != h.token || h.token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	recs, err := h.service.ListActiveSitesForSync(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sites": recs})
}
```

config：`WebPublishConfig` 加 `SiteSyncToken string \`yaml:"site_sync_token"\``（与 Plan 2 的 WebPublishConfig 同结构体；若该结构体在 Plan 2 已建，这里追加字段）。router 在 router.go 顶层（非 user 组）注册：
```go
	if dep.WebPublishService != nil {
		handlers.RegisterInternalWebPublishRoutes(engine, handlers.NewInternalWebPublishHandler(dep.WebPublishService, dep.SiteSyncToken))
	}
```
（`engine` 为顶层 gin engine；internal 组不挂用户鉴权中间件。）

- [ ] **Step 4: Run → pass; build**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/api/handlers/ -run TestSync -v && go build ./...`
Expected: PASS / 编译通过

- [ ] **Step 5: Commit**

```bash
git add internal/service/web_publish_service.go internal/api/handlers/internal_web_publish.go internal/api/router.go internal/config/
git commit -m "feat(api): 增加 site-server 内部同步端点

新建 /internal 路由组暴露 GET /internal/web-publish/sites，独立
X-OC-Site-Sync-Token 鉴权（不走用户 JWT），返回 active 站点路由契约
{\"sites\":[...]}（对齐 Plan 3 site-server）；token 入 config。"
```

---

### Task 6: runtime 发布端点（oc-publish 调用）

**Files:**
- Create: `internal/api/handlers/runtime_web_publish.go`
- Modify: `internal/api/router.go`
- Test: `internal/api/handlers/runtime_web_publish_test.go`

> 参照 `runtime_knowledge.go`（X-OC-App-Token、multipart `file`）。

- [ ] **Step 1: Write the failing test**

```go
package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/service"
)

type stubPublishService struct {
	gotToken, gotSlug string
	res               service.PublishResult
	err               error
}
func (s *stubPublishService) Publish(_ context.Context, token, slug string, _ io.Reader) (service.PublishResult, error) {
	s.gotToken, s.gotSlug = token, slug
	return s.res, s.err
}

func multipartTar(t *testing.T, slug string) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if slug != "" {
		_ = w.WriteField("slug", slug)
	}
	fw, _ := w.CreateFormFile("file", "site.tar.gz")
	_, _ = fw.Write([]byte("\x1f\x8b\x08\x00")) // gzip 魔数占位（service 被 stub，不真正解包）
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

// TestRuntimePublishHappy 覆盖：带 X-OC-App-Token + multipart file + slug → 调 service，
// 返回 {url, expires_at}。
func TestRuntimePublishHappy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubPublishService{res: service.PublishResult{URL: "https://blog.apps.example.com", ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}}
	r := gin.New()
	RegisterRuntimeWebPublishRoutes(r, NewRuntimeWebPublishHandler(svc))
	body, ct := multipartTar(t, "blog")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/web-publish", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-OC-App-Token", "app-token-xyz")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "app-token-xyz", svc.gotToken)
	assert.Equal(t, "blog", svc.gotSlug)
	assert.Contains(t, w.Body.String(), "blog.apps.example.com")
}

// TestRuntimePublishMissingToken 覆盖：缺 X-OC-App-Token → 401。
func TestRuntimePublishMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRuntimeWebPublishRoutes(r, NewRuntimeWebPublishHandler(&stubPublishService{}))
	body, ct := multipartTar(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/web-publish", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
```

> import 补 `io`。

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/api/handlers/ -run TestRuntimePublish -v`
Expected: 编译失败 `undefined: NewRuntimeWebPublishHandler`

- [ ] **Step 3: Implement**

```go
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

const appTokenHeader = "X-OC-App-Token" // 与 runtime_knowledge 一致

type publishService interface {
	Publish(ctx context.Context, appToken, slug string, body io.Reader) (service.PublishResult, error)
}

// RuntimeWebPublishHandler 暴露 hermes oc-publish 调用的发布端点。
type RuntimeWebPublishHandler struct {
	service publishService
}

// NewRuntimeWebPublishHandler 构造 handler。
func NewRuntimeWebPublishHandler(svc publishService) *RuntimeWebPublishHandler {
	return &RuntimeWebPublishHandler{service: svc}
}

// RegisterRuntimeWebPublishRoutes 注册 runtime 发布路由（鉴权用 X-OC-App-Token，handler 内取）。
func RegisterRuntimeWebPublishRoutes(router gin.IRouter, h *RuntimeWebPublishHandler) {
	router.POST("/api/v1/runtime/web-publish", h.Publish)
}

// Publish 接收 oc-publish 上传的 tar.gz（multipart file 字段）+ 可选 slug，转交 service。
//
// @Summary      发布静态站点
// @Tags         runtime-web-publish
// @Accept       multipart/form-data
// @Produce      json
// @Param        X-OC-App-Token  header  string  true  "per-app runtime token"
// @Param        slug            formData string false "站点 slug（缺省随机分配）"
// @Param        file            formData file   true  "站点目录 tar.gz"
// @Success      200  {object}  service.PublishResult
// @Failure      401  {object}  ErrorResponse
// @Failure      400  {object}  ErrorResponse
// @Router       /runtime/web-publish [post]
func (h *RuntimeWebPublishHandler) Publish(c *gin.Context) {
	token := c.GetHeader(appTokenHeader)
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing app token"})
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read file"})
		return
	}
	defer f.Close()
	res, err := h.service.Publish(c.Request.Context(), token, c.PostForm("slug"), f)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
```
（import 补 `io`；router.go 在 runtime 组注册 `RegisterRuntimeWebPublishRoutes`，与 `RegisterRuntimeKnowledgeRoutes` 并列。）

- [ ] **Step 4: Run → pass; build**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/api/handlers/ -run TestRuntimePublish -v && go build ./...`
Expected: PASS / 编译通过

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/runtime_web_publish.go internal/api/handlers/runtime_web_publish_test.go internal/api/router.go
git commit -m "feat(api): 增加 runtime 发布端点 POST /api/v1/runtime/web-publish

X-OC-App-Token 鉴权 + multipart file(tar.gz) + 可选 slug，转交 WebPublishService，
返回 {url, expires_at}；参照 runtime_knowledge 的 token/multipart 模式。"
```

---

### Task 7: manifest web_publish 段（Go 侧）

**Files:**
- Modify: `internal/integrations/hermes/manifest.go`
- Modify: `internal/integrations/hermes/app_input.go`
- Modify: `internal/integrations/hermes/build_manifest.go`
- Test: `internal/integrations/hermes/build_manifest_test.go`

- [ ] **Step 1: Write the failing test**

追加到 `build_manifest_test.go`：
```go
// TestBuildManifestWebPublish 验证 web_publish 三字段齐全时写入、不全时省略（条件注入）。
func TestBuildManifestWebPublish(t *testing.T) {
	// 齐全：写入
	m := BuildManifest(AppInputData{AppID: "a1",
		WebPublishRuntimeBaseURL: "http://manager/runtime", WebPublishAppToken: "tok", WebPublishBaseDomain: "apps.example.com"})
	assert.Equal(t, "http://manager/runtime", m.WebPublish.RuntimeBaseURL)
	assert.Equal(t, "tok", m.WebPublish.AppToken)
	assert.Equal(t, "apps.example.com", m.WebPublish.BaseDomain)

	// 缺 token：整段省略（omitempty）
	m2 := BuildManifest(AppInputData{AppID: "a1", WebPublishRuntimeBaseURL: "http://x", WebPublishBaseDomain: "apps.example.com"})
	assert.Empty(t, m2.WebPublish.AppToken)
	assert.Empty(t, m2.WebPublish.RuntimeBaseURL)
}
```

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/hermes/ -run TestBuildManifestWebPublish -v`
Expected: 编译失败 `WebPublish undefined`

- [ ] **Step 3: Implement**

`manifest.go`：
```go
// ManifestWebPublish 是 oc-publish skill 的运行时配置（条件注入；企业未开通时整段省略）。
type ManifestWebPublish struct {
	RuntimeBaseURL string `yaml:"runtime_base_url"`
	AppToken       string `yaml:"app_token"`
	BaseDomain     string `yaml:"base_domain"`
}
```
`Manifest` struct 加：
```go
	WebPublish ManifestWebPublish `yaml:"web_publish,omitempty"`
```
`app_input.go` 的 `AppInputData` 加：
```go
	// WebPublish* 在企业开通发布能力时注入，触发 oc-publish skill 条件渲染。
	WebPublishRuntimeBaseURL string
	WebPublishAppToken       string
	WebPublishBaseDomain     string
```
`build_manifest.go` 在 knowledge 注入后并列加：
```go
	// web_publish 仅在三字段齐全时写入（与 knowledge 同款条件注入）。
	if in.WebPublishRuntimeBaseURL != "" && in.WebPublishAppToken != "" && in.WebPublishBaseDomain != "" {
		m.WebPublish = ManifestWebPublish{
			RuntimeBaseURL: in.WebPublishRuntimeBaseURL,
			AppToken:       in.WebPublishAppToken,
			BaseDomain:     in.WebPublishBaseDomain,
		}
	}
```

- [ ] **Step 4: Run → pass; Commit**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/hermes/ -run TestBuildManifest -v`
```bash
git add internal/integrations/hermes/manifest.go internal/integrations/hermes/app_input.go internal/integrations/hermes/build_manifest.go internal/integrations/hermes/build_manifest_test.go
git commit -m "feat(hermes): manifest 增加 web_publish 段（条件注入）

ManifestWebPublish(runtime_base_url/app_token/base_domain) 与 AppInputData 对应
字段，三者齐全才写入 manifest（omitempty 省略），作为 oc-publish skill 条件渲染
的开关；与 knowledge 同款。"
```

---

### Task 8: bootstrap 装配时按 org 开通注入 web_publish

**Files:**
- Modify: `internal/service/bootstrap_service.go`
- Test: `internal/service/bootstrap_service_test.go`（沿用既有测试风格）

- [ ] **Step 1: Write the failing test**

参照 `bootstrap_service_test.go` 既有用例，新增：企业 `org_web_publish_config.enabled && provisioning_status='ready'` 时，构造的 `AppInputData` 含 `WebPublishBaseDomain`/`AppToken`/`RuntimeBaseURL`；未开通时三字段为空。（断言落在 `buildAppInput`/对应内部函数返回的 `AppInputData` 上；测试需给 store stub 返回相应 `OrgWebPublishConfig`。）

```go
// TestBuildAppInputInjectsWebPublishWhenReady 覆盖：org 已开通且 ready 时注入 web_publish 字段。
func TestBuildAppInputInjectsWebPublishWhenReady(t *testing.T) {
	// 构造 store stub：GetWebPublishConfig 返回 enabled+ready+base_domain
	// 调用 bootstrap 构造 AppInputData 的入口，断言三字段已填、AppToken==controlToken
	// （具体桩与入口函数名以 bootstrap_service.go 既有结构为准）
}

// TestBuildAppInputOmitsWebPublishWhenDisabled 覆盖：未开通时三字段为空，oc-publish 不会注入。
func TestBuildAppInputOmitsWebPublishWhenDisabled(t *testing.T) {
	// GetWebPublishConfig 返回 enabled=false（或 sql.ErrNoRows），断言三字段为空
}
```

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run TestBuildAppInput -v`
Expected: FAIL（字段未注入）

- [ ] **Step 3: Implement**

bootstrap_service 的 store 接口加 `GetWebPublishConfig(ctx, orgID) (sqlc.OrgWebPublishConfig, error)`。在 `bootstrap_service.go:253` 构造 `AppInputData` 处，knowledge 注入之后并列加：
```go
	// web_publish：企业开通且 provisioning ready 时注入，触发 oc-publish skill 条件渲染。
	// app_token 复用 per-app controlToken（与 knowledge 同），runtime base 同 knowledge base。
	if wp, werr := s.store.GetWebPublishConfig(ctx, app.OrgID); werr == nil &&
		wp.Enabled && wp.ProvisioningStatus == domain.ProvisioningReady {
		in.WebPublishRuntimeBaseURL = s.cfg.KnowledgeBaseURL
		in.WebPublishAppToken = string(controlToken)
		in.WebPublishBaseDomain = wp.BaseDomain
	}
```
（`in` 为构造中的 `AppInputData`；`controlToken`、`app`、`s.cfg.KnowledgeBaseURL` 用该函数既有变量名。`domain` 已 import 则直接用。）

- [ ] **Step 4: Run → pass; build**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run TestBuildAppInput -v && go build ./...`
Expected: PASS / 编译通过

- [ ] **Step 5: Commit**

```bash
git add internal/service/bootstrap_service.go internal/service/bootstrap_service_test.go
git commit -m "feat(service): bootstrap 按企业开通状态注入 web_publish manifest 段

企业 org_web_publish_config 开通且 ready 时，把 base_domain、runtime base 与
per-app controlToken 注入 AppInputData，触发 oc-publish skill 渲染；未开通时
留空，hermes 不会获得发布能力（条件注入落地“不对所有人开放”）。"
```

---

### Task 9: oc-publish skill 脚本（双 variant）

**Files:**
- Create: `runtime/hermes/hermes-v2026.6.5/oc-publish.py`
- Create: `runtime/hermes/hermes-v2026.5.16/oc-publish.py`（内容一致）

> 与 `oc-kb.py` 同构：仅标准库、手写 multipart、env 读取 `OC_PUBLISH_RUNTIME_BASE_URL`/`OC_PUBLISH_APP_TOKEN`。

- [ ] **Step 1: Write oc-publish.py**

```python
#!/usr/bin/env python3
"""oc-publish：把本地静态目录发布为带域名的公网站点。

用法：oc-publish ./<dir> [--slug <slug>]

把目录打成 tar.gz，经 manager runtime 发布端点（X-OC-App-Token 鉴权）上传，
manager 解包上传对象存储、分配 <slug>.<base_domain>、返回 {url, expires_at}。
配置（OC_PUBLISH_RUNTIME_BASE_URL / OC_PUBLISH_APP_TOKEN）由 oc-entrypoint
从 manifest.web_publish 注入进程环境（见 _configure_web_publish_env）。
"""
import io
import json
import os
import sys
import tarfile
import urllib.request
import uuid

# 单次发布 tar.gz 大小上限（与 manager 服务端 MaxUploadSize 同量级）。
MAX_UPLOAD_BYTES = 200 * 1024 * 1024


def _config() -> tuple[str, str]:
    """读取 manager runtime API 连接配置；任一缺失立即失败而不是默默拼错请求。"""
    base_url = os.environ.get("OC_PUBLISH_RUNTIME_BASE_URL", "").rstrip("/")
    token = os.environ.get("OC_PUBLISH_APP_TOKEN", "")
    if not base_url or not token:
        raise RuntimeError("oc-publish 未配置：缺 OC_PUBLISH_RUNTIME_BASE_URL 或 OC_PUBLISH_APP_TOKEN")
    return base_url, token


def _make_targz(src_dir: str) -> bytes:
    """把 src_dir 下内容打成 tar.gz（仅常规文件，路径相对 src_dir）。"""
    if not os.path.isdir(src_dir):
        raise RuntimeError(f"目录不存在：{src_dir}")
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for root, _dirs, files in os.walk(src_dir):
            for name in files:
                full = os.path.join(root, name)
                rel = os.path.relpath(full, src_dir)
                tar.add(full, arcname=rel)
    data = buf.getvalue()
    if len(data) > MAX_UPLOAD_BYTES:
        raise RuntimeError(f"发布内容超过上限 {MAX_UPLOAD_BYTES} 字节")
    return data


def _publish(src_dir: str, slug: str) -> dict:
    """上传 tar.gz 到 manager 发布端点并返回解析后的 JSON。"""
    base_url, token = _config()
    payload = _make_targz(src_dir)
    boundary = "----oc-publish-" + uuid.uuid4().hex
    parts = []
    if slug:
        parts += [
            f"--{boundary}\r\n".encode(),
            b'Content-Disposition: form-data; name="slug"\r\n\r\n',
            slug.encode(), b"\r\n",
        ]
    parts += [
        f"--{boundary}\r\n".encode(),
        b'Content-Disposition: form-data; name="file"; filename="site.tar.gz"\r\n',
        b"Content-Type: application/gzip\r\n\r\n",
        payload, b"\r\n",
        f"--{boundary}--\r\n".encode(),
    ]
    body = b"".join(parts)
    req = urllib.request.Request(base_url + "/api/v1/runtime/web-publish", data=body, method="POST")
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    req.add_header("Accept", "application/json")
    req.add_header("X-OC-App-Token", token)
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode())


def main(argv: list[str]) -> int:
    """解析参数并执行发布，把结果用户可读地打到 stdout。"""
    args = argv[1:]
    slug = ""
    positional = []
    i = 0
    while i < len(args):
        if args[i] == "--slug" and i + 1 < len(args):
            slug = args[i + 1]
            i += 2
            continue
        positional.append(args[i])
        i += 1
    if not positional:
        print("用法：oc-publish ./<dir> [--slug <slug>]", file=sys.stderr)
        return 2
    try:
        result = _publish(positional[0], slug)
    except Exception as e:  # noqa: BLE001 — CLI 顶层统一兜底，错误回显给 hermes
        print(f"发布失败：{e}", file=sys.stderr)
        return 1
    url = result.get("url", "")
    expires = result.get("expires_at", "")
    print(f"已发布：{url}（到期：{expires}）")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
```

把同一文件复制到 `hermes-v2026.5.16/oc-publish.py`。

- [ ] **Step 2: 语法自检**

Run: `cd /home/user/ywjs-oc-manager && python3 -m py_compile runtime/hermes/hermes-v2026.6.5/oc-publish.py runtime/hermes/hermes-v2026.5.16/oc-publish.py`
Expected: 无输出（编译通过）

- [ ] **Step 3: Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/oc-publish.py runtime/hermes/hermes-v2026.5.16/oc-publish.py
git commit -m "feat(hermes): 增加 oc-publish skill 脚本（双 variant）

与 oc-kb 同构的瘦客户端：tar.gz 打包目录 → 手写 multipart 经 X-OC-App-Token
上传 manager 发布端点 → 回显 {url, expires_at}；仅标准库，配置从环境读取。"
```

---

### Task 10: hermes 渲染器/entrypoint/manifest 条件注入（双 variant）

**Files:**（`hermes-v2026.6.5` 与 `hermes-v2026.5.16` 各一份，内容一致）
- Modify: `renderer/render_skills.py`
- Modify: `oc-entrypoint.py`
- Modify: `lib/manifest.py`

- [ ] **Step 1: manifest 解析加 web_publish 字段**

`lib/manifest.py` 的 `Manifest` dataclass 加（参照 knowledge 字段）：
```python
    # web_publish：manager runtime 发布 API 配置；企业未开通时缺省，oc-publish 不渲染。
    web_publish_runtime_base_url: str = ""
    web_publish_app_token: str = ""
    web_publish_base_domain: str = ""
```
在 `load()` 解析处加（参照 `knowledge.runtime_base_url`）：
```python
    wp = data.get("web_publish") or {}
    m.web_publish_runtime_base_url = wp.get("runtime_base_url", "")
    m.web_publish_app_token = wp.get("app_token", "")
    m.web_publish_base_domain = wp.get("base_domain", "")
```

- [ ] **Step 2: render_skills 条件渲染 oc-publish**

`renderer/render_skills.py` 加 SKILL.md 常量与渲染函数：
```python
_OC_PUBLISH_SKILL_MD = """---
name: oc-publish
description: Publish a static site directory to a public HTTPS URL via manager. Use when the user asks to publish/deploy a website you built. Run `oc-publish ./<dir> [--slug <slug>]`; re-run with the same --slug to update the same site in place.
---

# oc-publish

把当前 app 工作区里的一个静态站点目录发布到公网（带域名 HTTPS，N 天后自动删除）。

用法：`oc-publish ./<dir> [--slug <slug>]`
- `<dir>`：要发布的静态目录（含 index.html）。
- `--slug`：站点子域；省略则随机分配。对同一 slug 再次运行即原地更新（URL 不变）。
发布成功后会输出公网 URL 与到期时间，请把它告诉用户。
"""

def _render_web_publish_skill(m, skills_root):
    """manifest 含 web_publish 配置时渲染 oc-publish skill；token 只进环境变量，不写入 SKILL.md。"""
    if not (m.web_publish_runtime_base_url and m.web_publish_app_token):
        return []
    skill_dir = skills_root / "oc-publish"
    skill_dir.mkdir(parents=True, exist_ok=True)
    write_text(skill_dir / "SKILL.md", _OC_PUBLISH_SKILL_MD)
    _write_marker(skill_dir, "runtime-web-publish")
    return ["skills/oc-publish/SKILL.md"]
```
在 `render()` 主流程把它接进去（紧随 `_render_runtime_knowledge_skill` 之后）：
```python
    outputs.extend(_render_web_publish_skill(m, skills_root))
```

- [ ] **Step 3: entrypoint 注入 env**

`oc-entrypoint.py` 加（参照 `_configure_knowledge_env`）并在 `main()` 调用：
```python
def _configure_web_publish_env(manifest) -> None:
    """把 manifest web_publish 配置注入 Hermes 进程环境，供 oc-publish 子命令使用。"""
    if manifest.web_publish_runtime_base_url and manifest.web_publish_app_token:
        os.environ["OC_PUBLISH_RUNTIME_BASE_URL"] = manifest.web_publish_runtime_base_url
        os.environ["OC_PUBLISH_APP_TOKEN"] = manifest.web_publish_app_token
        return
    os.environ.pop("OC_PUBLISH_RUNTIME_BASE_URL", None)
    os.environ.pop("OC_PUBLISH_APP_TOKEN", None)
```
（在 `main()` 里 `_configure_knowledge_env(manifest)` 旁边加 `_configure_web_publish_env(manifest)`。）

- [ ] **Step 4: 两个 variant 同步 + 语法自检**

确保上述三处改动 `hermes-v2026.6.5` 与 `hermes-v2026.5.16` 完全一致。
Run:
```bash
cd /home/user/ywjs-oc-manager && python3 -m py_compile \
  runtime/hermes/hermes-v2026.6.5/renderer/render_skills.py \
  runtime/hermes/hermes-v2026.6.5/oc-entrypoint.py \
  runtime/hermes/hermes-v2026.6.5/lib/manifest.py \
  runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py \
  runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py \
  runtime/hermes/hermes-v2026.5.16/lib/manifest.py
```
Expected: 无输出
（若仓库 hermes 有 pytest 套件，跑 `renderer` 相关测试；否则语法自检 + Task 11 端到端浏览器验证兜底。）

- [ ] **Step 5: Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/renderer/render_skills.py runtime/hermes/hermes-v2026.6.5/oc-entrypoint.py runtime/hermes/hermes-v2026.6.5/lib/manifest.py runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py runtime/hermes/hermes-v2026.5.16/oc-entrypoint.py runtime/hermes/hermes-v2026.5.16/lib/manifest.py
git commit -m "feat(hermes): 渲染器按 web_publish 段条件注入 oc-publish skill（双 variant）

manifest 解析 web_publish；render_skills 在配置存在时渲染 oc-publish SKILL.md；
entrypoint 把 runtime base/token 注入 OC_PUBLISH_* 环境变量。两个 hermes variant
同步落地，企业未开通时整体不渲染（hermes 无从知晓发布能力）。"
```

---

### Task 11: 装配 + OpenAPI + 端到端验证

**Files:**
- Modify: `cmd/server/main.go`
- Regenerate: `openapi/openapi.yaml`, `web/src/api/generated.ts`

- [ ] **Step 1: 装配 WebPublishService + 两个 handler + 路由**

在 `main.go`：构造 `service.NewWebPublishService(dbStore.Queries, objStore, service.WebPublishServiceConfig{})`（`objStore` 用既有 S3 store 变量）；放进 router `dep`（`WebPublishService` + `SiteSyncToken: cfg.WebPublish.SiteSyncToken`）；runtime 发布路由在 runtime 组注册，internal 同步路由在顶层 engine 注册（见 Task 5/6）。

- [ ] **Step 2: 生成 OpenAPI 与前端类型**

Run: `cd /home/user/ywjs-oc-manager && make openapi-gen && make web-types-gen && make openapi-check`
Expected: yaml/types 更新；openapi-check 干净

- [ ] **Step 3: 全量编译与测试**

Run: `cd /home/user/ywjs-oc-manager && go build ./... && go test ./internal/... -count=1`
Expected: 编译通过；新增单测全绿

- [ ] **Step 4: 端到端浏览器验证（AGENTS.md 交付前检查要求）**

本地 k3d 起全栈后，按真实链路验证（curl 不能替代——AGENTS.md 明确要求真实浏览器验证前端逻辑；此处发布链路虽无前端页面，但需真实 hermes pod + site-server + DNS 才能验证端到端）：
1. 平台管理员（Plan 2/5 页面或直接 API）给某企业开通 web-publish，等 provisioning ready。
2. 该企业某 app 的 hermes 对话里确认 `oc-publish` skill 已注入（manifest 有 web_publish 段）。
3. 在 pod 工作区造一个 `./site/index.html`，跑 `oc-publish ./site --slug demo`，确认输出 `https://demo.<base_domain>` 与到期时间。
4. 浏览器访问该 URL，确认返回 index.html（site-server 经通配 Ingress + 通配证书 + S3）。
5. 改 index.html 内容，`oc-publish ./site --slug demo` 再发，几秒后浏览器刷新确认内容更新、URL 不变。
6. 用另一个 app 试 `--slug demo`，确认被拒（slug 已占用）。

> 若本地环境受限（无公网 DNS / staging 证书浏览器不信任），至少验证到 site-server 直接按 Host 头返回正确文件（`curl -H "Host: demo.<base>" http://<site-server-svc>/`）+ manager 发布端点返回正确 JSON；并在交付说明写明哪些环节用真实浏览器验证、哪些受环境限制降级验证及原因。

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(server): 装配发布链路（WebPublishService + runtime/internal 端点）

构造 WebPublishService 并注册 runtime 发布端点与 site-server 内部同步端点，
同步 openapi.yaml/前端类型；完成端到端发布→访问→更新→归属校验验证。"
```

---

## Self-Review

**1. Spec coverage（对应 §4.2/§4.4/§4.5/§5/§11.4）：**
- `published_sites` 表（一 slug 一行、host 唯一、版本指针、索引）→ Task 1 ✓
- runtime 发布端点（校验开通+配额、分配 host、解包上传 S3、插/更新行、返回 {url,expires_at}）→ Task 4 + Task 6 ✓
- update-in-place（同 app+slug 更新、原子换版、TTL 重置、跨 app 拒绝）→ Task 4（全单测覆盖）✓
- 内部同步端点（host→{siteID,s3_prefix,status}，集群内带鉴权）→ Task 5（对齐 Plan 3 契约）✓
- oc-publish skill 双 variant（SKILL.md + 脚本）→ Task 9 ✓
- manifest web_publish 段条件注入（render_skills + app_input + entrypoint）→ Task 7/8/10 ✓
- per-app token 鉴权（X-OC-App-Token hash 反查）→ Task 4/6 ✓
- 能力"有/无"=skill"注入/不注入"，按 org 开通条件 → Task 8/10 ✓
- 首发 vs 更新对 hermes 透明（同调用形态、同返回）→ Task 4 ✓
- OpenAPI 同步 → Task 11 ✓

**2. Placeholder scan：** Task 8 的 bootstrap 单测与 Task 10 的 Python 渲染器测试以"参照既有测试结构补全"标注——因要对齐仓库未在本 plan 引用的桩/入口函数名与（可能缺失的）Python 测试基建，非代码占位；落地者参照 `bootstrap_service_test.go` 与 `oc-kb` 现有验证方式。真实对象存储/真实 hermes pod 路径靠 Task 11 端到端验证。其余 Go 逻辑均完整代码 + TDD。

**3. Type consistency：**
- `service.PublishResult{URL,ExpiresAt}` 在 Task 4 定义、Task 6 handler 返回、oc-publish.py 解析（`url`/`expires_at` JSON tag）一致 ✓
- `service.SiteSyncRecord{Host,SiteID,S3Prefix,Status}` JSON tag（Task 5）与 Plan 3 `siteserver.SiteRecord` 及其端点契约逐字段一致 ✓
- manifest `web_publish.{runtime_base_url,app_token,base_domain}`（Go Task 7 yaml tag）与 Python `lib/manifest.py` 解析 key（Task 10）一致；env `OC_PUBLISH_RUNTIME_BASE_URL`/`OC_PUBLISH_APP_TOKEN` 在 entrypoint（Task 10）与 oc-publish.py（Task 9）一致 ✓
- `domain.ProvisioningReady`（Plan 2）在 Task 4/8 一致用作开通判据；`org_web_publish_config` 字段（`Enabled`/`ProvisioningStatus`/`BaseDomain`/`SiteTtlDays`/`MaxSites`）在 Task 4/8 与 Plan 2 sqlc 生成一致 ✓
- runtime 端点路径 `/api/v1/runtime/web-publish` 在 handler（Task 6）与 oc-publish.py（Task 9）一致 ✓

**给 Plan 5 的契约：**
- `published_sites` 的 `SetPublishedSiteStatus`、`ListSitesByOrg`、`ListExpiredActiveSites`、`GetPublishedSiteByID` 已就绪，供 Plan 5 的 reaper（置 expired + 删前缀）、手动下线、续期、org/平台列表复用。
- 回收时删 `published-sites/<siteID>/` 整前缀（spec §4.4）；下线置 `disabled` 后 site-server 下轮同步即 404。

**落地者需确认的仓库既有名：** `app_runtime_token.go` 的 hash 函数名（`HashAppRuntimeToken`）；`bootstrap_service.go` 构造 `AppInputData` 的函数与 `controlToken`/`s.cfg.KnowledgeBaseURL` 变量名；router `dep` 结构与 runtime 组变量名；sqlc 生成的 `ListActiveSitesRow` 字段名；`writeServiceError`/`ErrorResponse` 既有定义；`lib/manifest.py` 的 `load()` 解析风格与 `write_text`/`_write_marker` helper（render_skills.py 既有）。
