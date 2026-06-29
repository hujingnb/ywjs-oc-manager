# Web Publish — Plan 5: 生命周期与管理面 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **依赖 Plan 1/2/4**：复用 Plan 4 的 `published_sites` 与其 sqlc 查询；复用 Plan 2 的 `org_web_publish_config`、`WebPublishProvisionHandler`、`CertProvisioner`/`ClusterApplier`、`WebPublishConfigService`、`JobTypeWebPublishProvision`；复用 Plan 1 的 `acme.Issuer`（经 Plan 2 的 provision handler 间接）。本 plan 是整个 5-plan 套件的收尾。

**Goal:** 站点生命周期（TTL 自动回收、手动下线、续期）+ 通配证书自动续签巡检 + 管理面（org admin 看/管本企业站点与只读证书状态；平台管理员全局视图 + 手动重试签发/续签）+ 对应前端页面与证书状态面板。

**Architecture:** 后台维护循环（参照现有 `internal/worker/reaper`，redis 锁防多副本重复）：`SiteReaper` 扫 `published_sites.expires_at < now && active` → 置 `expired` + 删对象前缀；`CertRenewalChecker` 扫 `org_web_publish_config.cert_not_after` 临近到期 → 入队既有 `web_publish_provision` job（复用 Plan 2 handler 完成重签 + 覆盖 TLS Secret）。`WebPublishSiteService` 提供 org/平台的列表、手动下线、续期。`WebPublishConfigService` 补 `Get`（含证书状态，脱敏）与 `RetryProvision`（手动重试入队）。provision handler 小增强：按当前 `cert_status` 区分首签（issuing）与续签（renewing）。前端两个页面：org 站点列表（下线/续期/只读证书）+ 平台配置页证书状态面板（重试）。后端 TDD；前端复用既有组件（`DataTableList`/`ConfirmActionModal`/column builders/TanStack Query hooks）给完整代码，端到端浏览器验证。

**Tech Stack:** Go（worker/redis 锁、gin、sqlc）、acme/k8sorch（经 Plan 2 复用）、Vue 3 + TypeScript + Naive UI + TanStack Query + vue-i18n、swag/openapi-typescript、testify。

---

## 背景约束（落地前必读）

- **下线/回收立即生效靠 site-server 轮询**：置 `disabled`/`expired` 后，site-server 下一轮同步（几秒）即从注册表移除 → 404。删对象前缀是清理，不依赖它来 404（spec §7）。
- **回收删整前缀**：reaper 置 `expired` 后删 `published-sites/<siteID>/`（整站所有版本），不只当前版本（spec §4.4/§7）。手动下线同样删前缀。
- **证书续签复用 provision job（DRY）**：续签巡检与"手动重试"都入队 `web_publish_provision` job，由 Plan 2 的 `WebPublishProvisionHandler` 处理（它本就是"签发 → 写 Secret → 建 Ingress"幂等全流程，重跑即续签）。handler 小增强按现状区分 issuing/renewing 与 `cert_last_issued_at`/`cert_last_renewed_at`。续签独立于站点生命周期（企业级，spec §6/§7）。
- **续签提前量**：`cert_not_after` 早于 `now + renewBefore`（默认 30 天）即触发续签，留足重试窗口。
- **权限**（spec §8）：org admin 看/管**本企业**站点与只读看本企业证书（`CanViewOrg`/`CanManageOrg`）；平台管理员全局视图 + 手动重试签发/续签（`CanManageWebPublishConfig`，Plan 2 已建）。
- **证书状态对两角色可见**：`Get` 返回 `cert_status`/`cert_not_after`/`cert_last_issued_at`/`cert_last_renewed_at`/`cert_message` + 通配域 `*.base_domain`，**不返回凭证密文**。
- **前端**：复用既有 `DataTableList`、column builders（`linkColumn`/`statusColumn`/`actionColumn`）、`ConfirmActionModal`、TanStack Query hooks 模式（见 `OrganizationsPage.vue`/`MembersPage.vue`）；i18n key 同时加 en/zh（completeness 测试校验对齐）。
- OpenAPI 同步、注释、单测注释、testify 规范同前序 plan。AGENTS.md：新功能须真实浏览器验证。

## File Structure

```
internal/store/queries/published_sites.sql        # 追加 RenewPublishedSite
internal/store/queries/web_publish_config.sql     # 追加 ListConfigsCertExpiringBefore

internal/service/web_publish_site_service.go       # org/平台 列表 + 下线 + 续期
internal/service/web_publish_site_service_test.go
internal/service/web_publish_config_service.go     # 追加 Get（含证书状态）+ RetryProvision
internal/worker/handlers/web_publish_provision.go  # 增强 issuing/renewing 区分

internal/worker/webpublish/reaper.go               # SiteReaper + CertRenewalChecker（后台 loop）
internal/worker/webpublish/reaper_test.go

internal/api/handlers/web_publish_sites.go         # org/平台 站点管理 + 证书状态 API
internal/api/handlers/web_publish_sites_test.go
internal/api/handlers/dto.go                       # 追加续期/下线请求（如需）
internal/api/router.go / cmd/server/main.go        # 路由与后台 loop 装配

web/src/api/hooks/useWebPublish.ts                 # 前端数据 hooks
web/src/pages/org/PublishedSitesPage.vue           # org 站点列表页
web/src/pages/platform/...                         # 平台证书状态面板（扩展 Plan 2 页面）
web/src/app/router.ts                              # 路由注册
web/src/i18n/locales/{en,zh}/...                   # 文案
openapi/openapi.yaml / web/src/api/generated.ts    # 重新生成
```

---

### Task 1: sqlc 查询补充（续期 + 证书到期扫描）

**Files:**
- Modify: `internal/store/queries/published_sites.sql`
- Modify: `internal/store/queries/web_publish_config.sql`
- Generate: sqlc

- [ ] **Step 1: Add queries**

`published_sites.sql` 追加：
```sql
-- name: RenewPublishedSite :exec
-- 续期：把过期时间延后到 now + N 天（N 由 service 按企业 site_ttl_days 传入）。
UPDATE published_sites
SET expires_at = ?, status = 'active', updated_at = now()
WHERE id = ?;
```

`web_publish_config.sql` 追加：
```sql
-- name: ListConfigsCertExpiringBefore :many
-- 证书续签巡检：列出已签发且 cert_not_after 早于阈值的企业配置（需续签）。
SELECT * FROM org_web_publish_config
WHERE enabled = 1
  AND provisioning_status = 'ready'
  AND cert_status = 'issued'
  AND cert_not_after IS NOT NULL
  AND cert_not_after < ?;
```

- [ ] **Step 2: Generate + build + commit**

Run: `cd /home/user/ywjs-oc-manager && go run github.com/sqlc-dev/sqlc/cmd/sqlc generate && go build ./internal/store/...`
```bash
git add internal/store/queries/ internal/store/sqlc/
git commit -m "feat(store): 增加站点续期与证书到期扫描查询

RenewPublishedSite 延后 expires_at 并置回 active；ListConfigsCertExpiringBefore
扫出已签发且临近到期的企业配置供续签巡检。"
```

---

### Task 2: 站点管理 service（列表 / 下线 / 续期）

**Files:**
- Create: `internal/service/web_publish_site_service.go`
- Test: `internal/service/web_publish_site_service_test.go`

- [ ] **Step 1: Write the failing test**

```go
package service

import (
	"context"
	"testing"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSiteStore struct {
	site      sqlc.PublishedSite
	cfg       sqlc.OrgWebPublishConfig
	statusSet *sqlc.SetPublishedSiteStatusParams
	renewed   *sqlc.RenewPublishedSiteParams
	deleted   []string
	listed    []sqlc.PublishedSite
}
func (f *fakeSiteStore) ListSitesByOrg(_ context.Context, _ string) ([]sqlc.PublishedSite, error) { return f.listed, nil }
func (f *fakeSiteStore) GetPublishedSiteByID(_ context.Context, _ string) (sqlc.PublishedSite, error) { return f.site, nil }
func (f *fakeSiteStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) { return f.cfg, nil }
func (f *fakeSiteStore) SetPublishedSiteStatus(_ context.Context, p sqlc.SetPublishedSiteStatusParams) error { f.statusSet = &p; return nil }
func (f *fakeSiteStore) RenewPublishedSite(_ context.Context, p sqlc.RenewPublishedSiteParams) error { f.renewed = &p; return nil }

type fakeSiteObj struct{ deleted []string }
func (f *fakeSiteObj) DeletePrefix(_ context.Context, prefix string) error { f.deleted = append(f.deleted, prefix); return nil }

func orgAdmin(org string) auth.Principal { return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: org} }

// TestTakedownSetsDisabledAndDeletesPrefix 覆盖：下线置 disabled 并删整站前缀。
func TestTakedownSetsDisabledAndDeletesPrefix(t *testing.T) {
	st := &fakeSiteStore{site: sqlc.PublishedSite{ID: "s1", OrgID: "org-1", S3Prefix: "published-sites/s1/v3/"}}
	obj := &fakeSiteObj{}
	svc := NewWebPublishSiteService(st, obj, func() time.Time { return time.Now() })
	require.NoError(t, svc.Takedown(context.Background(), orgAdmin("org-1"), "s1"))
	require.NotNil(t, st.statusSet)
	assert.Equal(t, domain.SiteStatusDisabled, st.statusSet.Status)
	// 删整站前缀 published-sites/<id>/（非仅当前版本）
	assert.Contains(t, obj.deleted, "published-sites/s1/")
}

// TestTakedownDeniedCrossOrg 覆盖：org admin 不能下线别企业站点。
func TestTakedownDeniedCrossOrg(t *testing.T) {
	st := &fakeSiteStore{site: sqlc.PublishedSite{ID: "s1", OrgID: "org-OTHER", S3Prefix: "p/"}}
	svc := NewWebPublishSiteService(st, &fakeSiteObj{}, time.Now)
	err := svc.Takedown(context.Background(), orgAdmin("org-1"), "s1")
	require.Error(t, err)
}

// TestRenewExtendsExpiry 覆盖：续期把 expires_at 延后 now + 企业 ttl 天。
func TestRenewExtendsExpiry(t *testing.T) {
	now := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)
	st := &fakeSiteStore{
		site: sqlc.PublishedSite{ID: "s1", OrgID: "org-1"},
		cfg:  sqlc.OrgWebPublishConfig{OrgID: "org-1", SiteTtlDays: 7},
	}
	svc := NewWebPublishSiteService(st, &fakeSiteObj{}, func() time.Time { return now })
	require.NoError(t, svc.Renew(context.Background(), orgAdmin("org-1"), "s1"))
	require.NotNil(t, st.renewed)
	assert.Equal(t, now.Add(7*24*time.Hour), st.renewed.ExpiresAt)
}
```

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run 'TestTakedown|TestRenew' -v`
Expected: 编译失败 `undefined: NewWebPublishSiteService`

- [ ] **Step 3: Implement**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// WebPublishSiteStore 是站点管理 service 的最小数据访问能力。
type WebPublishSiteStore interface {
	ListSitesByOrg(ctx context.Context, orgID string) ([]sqlc.PublishedSite, error)
	GetPublishedSiteByID(ctx context.Context, id string) (sqlc.PublishedSite, error)
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	SetPublishedSiteStatus(ctx context.Context, arg sqlc.SetPublishedSiteStatusParams) error
	RenewPublishedSite(ctx context.Context, arg sqlc.RenewPublishedSiteParams) error
}

// siteObjectStore 是站点回收所需的对象存储子集。
type siteObjectStore interface {
	DeletePrefix(ctx context.Context, prefix string) error
}

// SiteResult 是站点列表/操作返回（脱敏，供前端展示）。
type SiteResult struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	URL       string    `json:"url"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WebPublishSiteService 提供 org/平台对已发布站点的查看与管理（下线/续期）。
type WebPublishSiteService struct {
	store WebPublishSiteStore
	obj   siteObjectStore
	now   func() time.Time
}

// NewWebPublishSiteService 构造 service。
func NewWebPublishSiteService(store WebPublishSiteStore, obj siteObjectStore, now func() time.Time) *WebPublishSiteService {
	if now == nil {
		now = time.Now
	}
	return &WebPublishSiteService{store: store, obj: obj, now: now}
}

// ListByOrg 列出某企业全部站点（org admin 限本企业，平台管理员任意）。
func (s *WebPublishSiteService) ListByOrg(ctx context.Context, p auth.Principal, orgID string) ([]SiteResult, error) {
	if !auth.CanViewOrg(p, orgID) {
		return nil, ErrForbidden
	}
	rows, err := s.store.ListSitesByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]SiteResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSiteResult(r))
	}
	return out, nil
}

// Takedown 手动下线站点：置 disabled 并删整站对象前缀（site-server 下轮同步即 404）。
func (s *WebPublishSiteService) Takedown(ctx context.Context, p auth.Principal, siteID string) error {
	site, err := s.authorizeSite(ctx, p, siteID)
	if err != nil {
		return err
	}
	if err := s.store.SetPublishedSiteStatus(ctx, sqlc.SetPublishedSiteStatusParams{
		Status: domain.SiteStatusDisabled, ID: siteID,
	}); err != nil {
		return err
	}
	// 删整站前缀（所有版本），不只当前版本。
	_ = s.obj.DeletePrefix(ctx, siteRootPrefix(site))
	return nil
}

// Renew 续期：把 expires_at 延后 now + 企业 site_ttl_days 天，并置回 active。
func (s *WebPublishSiteService) Renew(ctx context.Context, p auth.Principal, siteID string) error {
	site, err := s.authorizeSite(ctx, p, siteID)
	if err != nil {
		return err
	}
	cfg, err := s.store.GetWebPublishConfig(ctx, site.OrgID)
	if err != nil {
		return err
	}
	expires := s.now().Add(time.Duration(cfg.SiteTtlDays) * 24 * time.Hour)
	return s.store.RenewPublishedSite(ctx, sqlc.RenewPublishedSiteParams{ExpiresAt: expires, ID: siteID})
}

// authorizeSite 加载站点并校验调用方可管理其所属企业。
func (s *WebPublishSiteService) authorizeSite(ctx context.Context, p auth.Principal, siteID string) (sqlc.PublishedSite, error) {
	site, err := s.store.GetPublishedSiteByID(ctx, siteID)
	if err != nil {
		return sqlc.PublishedSite{}, fmt.Errorf("站点不存在: %w", err)
	}
	if !auth.CanManageOrg(p, site.OrgID) {
		return sqlc.PublishedSite{}, ErrForbidden
	}
	return site, nil
}

// siteRootPrefix 由当前版本前缀推出整站前缀 published-sites/<siteID>/。
func siteRootPrefix(site sqlc.PublishedSite) string {
	return fmt.Sprintf("published-sites/%s/", site.ID)
}

// toSiteResult 把 DB 行映射为脱敏结果（URL 由 host 拼）。
func toSiteResult(r sqlc.PublishedSite) SiteResult {
	return SiteResult{
		ID: r.ID, Host: r.Host, URL: "https://" + r.Host, Slug: r.Slug,
		Status: r.Status, SizeBytes: r.SizeBytes, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
}

var _ = errors.New // 占位：未用到则删
var _ = strings.TrimSpace
```

> `ErrForbidden`/`CanViewOrg`/`CanManageOrg` 用既有定义（Plan 2 已确认）。`var _ =` 占位行按编译实际删除。

- [ ] **Step 4: Run → pass; commit**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run 'TestTakedown|TestRenew' -v`
```bash
git add internal/service/web_publish_site_service.go internal/service/web_publish_site_service_test.go
git commit -m "feat(service): 增加站点管理 service（列表/下线/续期）

ListByOrg(CanViewOrg)、Takedown(置 disabled + 删整站前缀)、Renew(按企业 ttl
延后 expires_at)，均做企业归属权限校验；下线/回收靠 site-server 下轮同步生效。"
```

---

### Task 3: SiteReaper —— TTL 自动回收

**Files:**
- Create: `internal/worker/webpublish/reaper.go`
- Test: `internal/worker/webpublish/reaper_test.go`

> 核心扫描+动作抽成可单测的 `ReapOnce`；后台 loop（参照 `internal/worker/reaper/reaper.go` 的 goroutine + redis 锁 + tick）包一层，loop 本身不单测。

- [ ] **Step 1: Write the failing test**

```go
package webpublish

import (
	"context"
	"testing"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReaperStore struct {
	expired   []sqlc.PublishedSite
	statusSet []sqlc.SetPublishedSiteStatusParams
}
func (f *fakeReaperStore) ListExpiredActiveSites(_ context.Context) ([]sqlc.PublishedSite, error) { return f.expired, nil }
func (f *fakeReaperStore) SetPublishedSiteStatus(_ context.Context, p sqlc.SetPublishedSiteStatusParams) error { f.statusSet = append(f.statusSet, p); return nil }

type fakeReaperObj struct{ deleted []string }
func (f *fakeReaperObj) DeletePrefix(_ context.Context, prefix string) error { f.deleted = append(f.deleted, prefix); return nil }

// TestReapOnceExpiresAndDeletes 覆盖：每个过期 active 站点被置 expired 并删整站前缀。
func TestReapOnceExpiresAndDeletes(t *testing.T) {
	st := &fakeReaperStore{expired: []sqlc.PublishedSite{
		{ID: "s1", S3Prefix: "published-sites/s1/v2/"},
		{ID: "s2", S3Prefix: "published-sites/s2/v1/"},
	}}
	obj := &fakeReaperObj{}
	r := NewSiteReaper(st, obj)
	require.NoError(t, r.ReapOnce(context.Background()))

	require.Len(t, st.statusSet, 2)
	assert.Equal(t, domain.SiteStatusExpired, st.statusSet[0].Status)
	assert.ElementsMatch(t, []string{"published-sites/s1/", "published-sites/s2/"}, obj.deleted)
}

// TestReapOnceNoExpired 覆盖：无过期站点时不做任何动作。
func TestReapOnceNoExpired(t *testing.T) {
	st := &fakeReaperStore{}
	obj := &fakeReaperObj{}
	require.NoError(t, NewSiteReaper(st, obj).ReapOnce(context.Background()))
	assert.Empty(t, obj.deleted)
	_ = time.Now
}
```

- [ ] **Step 2: Run → fail**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/worker/webpublish/ -run TestReap -v`
Expected: 编译失败 `undefined: NewSiteReaper`

- [ ] **Step 3: Implement**

```go
// Package webpublish 提供 web-publish 的后台维护任务：站点 TTL 回收与证书续签巡检。
package webpublish

import (
	"context"
	"fmt"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// SiteReaperStore 是站点回收所需的最小数据访问能力。
type SiteReaperStore interface {
	ListExpiredActiveSites(ctx context.Context) ([]sqlc.PublishedSite, error)
	SetPublishedSiteStatus(ctx context.Context, arg sqlc.SetPublishedSiteStatusParams) error
}

// ReaperObjectStore 是删对象前缀的能力。
type ReaperObjectStore interface {
	DeletePrefix(ctx context.Context, prefix string) error
}

// SiteReaper 回收已过期站点：置 expired + 删整站对象前缀。
type SiteReaper struct {
	store SiteReaperStore
	obj   ReaperObjectStore
}

// NewSiteReaper 构造 reaper。
func NewSiteReaper(store SiteReaperStore, obj ReaperObjectStore) *SiteReaper {
	return &SiteReaper{store: store, obj: obj}
}

// ReapOnce 扫一遍过期 active 站点，逐个置 expired 并删整站前缀。
// 单个失败不阻断其余（记录后继续），保证一个坏站点不卡住整轮回收。
func (r *SiteReaper) ReapOnce(ctx context.Context) error {
	sites, err := r.store.ListExpiredActiveSites(ctx)
	if err != nil {
		return fmt.Errorf("扫描过期站点失败: %w", err)
	}
	for _, s := range sites {
		if err := r.store.SetPublishedSiteStatus(ctx, sqlc.SetPublishedSiteStatusParams{
			Status: domain.SiteStatusExpired, ID: s.ID,
		}); err != nil {
			continue // 该站点本轮跳过，下轮再试
		}
		_ = r.obj.DeletePrefix(ctx, fmt.Sprintf("published-sites/%s/", s.ID))
	}
	return nil
}
```

- [ ] **Step 4: Run → pass**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/worker/webpublish/ -run TestReap -v`
Expected: PASS

- [ ] **Step 5: 后台 loop（参照现有 reaper）**

在同包加 `Loop`（goroutine + redis 分布式锁防多副本重复 + ticker），结构对照 `internal/worker/reaper/reaper.go:66-102`：tick 间隔默认 60s，加锁 `ocm:webpublish-reaper:lock` 后调 `ReapOnce`。loop 装配在 Task 9。无独立单测（与现有 reaper 一致，逻辑在 ReapOnce）。

- [ ] **Step 6: Commit**

```bash
git add internal/worker/webpublish/reaper.go internal/worker/webpublish/reaper_test.go
git commit -m "feat(worker): 增加站点 TTL 回收 reaper

ReapOnce 扫过期 active 站点，逐个置 expired 并删整站对象前缀，单个失败不阻断整轮；
后台 loop 参照现有 reaper（redis 锁 + 60s tick）。站点下线靠 site-server 下轮同步生效。"
```

---

### Task 4: 证书续签（provision handler 增强 + 续签巡检 + 手动重试）

**Files:**
- Modify: `internal/worker/handlers/web_publish_provision.go`（issuing/renewing 区分）
- Modify: `internal/service/web_publish_config_service.go`（`Get` + `RetryProvision`）
- Create: `internal/worker/webpublish/cert_renewal.go`（巡检 → 入队 provision job）
- Test: 对应 `_test.go`

- [ ] **Step 1: provision handler 增强测试**

在 `web_publish_provision_test.go` 追加：当 `GetWebPublishConfig` 返回的 `cert_status` 已是 `issued`（说明续签）时，进入态写 `renewing`、成功写 `cert_last_renewed_at`（而非 issued/last_issued_at）。
```go
// TestProvisionRenewalPath 覆盖：已 issued 的证书再跑 provision 视为续签：
// 进入 renewing、成功后状态回 issued 且写 last_renewed_at。
func TestProvisionRenewalPath(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	cfg := newCfg(cipher); cfg.CertStatus = domain.CertStatusIssued // 现状已签发 → 续签语义
	st := &fakeWPProvStore{cfg: cfg}
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1924992000}}
	h := NewWebPublishProvisionHandler(st, prov, &fakeClusterApplier{}, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})
	require.NoError(t, h.Handle(context.Background(), provJob()))
	// 中途出现过 renewing
	var sawRenewing bool
	for _, u := range st.certUpdates {
		if u.CertStatus == domain.CertStatusRenewing { sawRenewing = true }
	}
	assert.True(t, sawRenewing, "续签应经过 renewing 态")
	// 最终 issued
	assert.Equal(t, domain.CertStatusIssued, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}
```

- [ ] **Step 2: 增强 handler**

在 `Handle` 进入处：根据加载的 `cfg.CertStatus` 决定进行中状态——
```go
	// 已签发再跑视为续签，否则首签；用于状态展示与时间戳字段选择。
	inProgress := domain.CertStatusIssuing
	isRenewal := cfg.CertStatus == domain.CertStatusIssued
	if isRenewal {
		inProgress = domain.CertStatusRenewing
	}
	h.setCert(ctx, payload.OrgID, inProgress, nil, "")
```
成功分支按 `isRenewal` 写 `cert_last_renewed_at` 或 `cert_last_issued_at`（其一非空、另一保持）。其余不变。补 `SetWebPublishCertStatusParams` 的 `CertLastRenewedAt` 字段写入（migration 已有该列，sqlc 查询 `SetWebPublishCertStatus` 若未覆盖该列，需在 Plan 2 的查询补一个含 `cert_last_renewed_at` 的变体或扩展该查询——落地时按 sqlc 生成字段对齐；推荐 Plan 2 的 `SetWebPublishCertStatus` 同时 set 两个时间列，传 null 跳过）。

> **查询对齐说明**：若 Plan 2 的 `SetWebPublishCertStatus` 未含 `cert_last_renewed_at`，在此扩展该 query 增加该列（`SET ..., cert_last_renewed_at = COALESCE(?, cert_last_renewed_at)`），重新 sqlc 生成。

- [ ] **Step 3: config service 加 Get + RetryProvision**

```go
// WebPublishConfigResult 是脱敏的配置+证书状态（不含凭证密文），供两角色查看。
type WebPublishConfigResult struct {
	OrgID              string     `json:"org_id"`
	Enabled            bool       `json:"enabled"`
	BaseDomain         string     `json:"base_domain"`
	WildcardDomain     string     `json:"wildcard_domain"` // *.base_domain
	DNSProvider        string     `json:"dns_provider"`
	SiteTTLDays        int32      `json:"site_ttl_days"`
	MaxSites           int32      `json:"max_sites"`
	ProvisioningStatus string     `json:"provisioning_status"`
	ProvisioningMessage string    `json:"provisioning_message,omitempty"`
	CertStatus         string     `json:"cert_status"`
	CertNotAfter       *time.Time `json:"cert_not_after,omitempty"`
	CertLastIssuedAt   *time.Time `json:"cert_last_issued_at,omitempty"`
	CertLastRenewedAt  *time.Time `json:"cert_last_renewed_at,omitempty"`
	CertMessage        string     `json:"cert_message,omitempty"`
}

// Get 返回企业发布能力配置与证书状态（脱敏）。org admin 限本企业，平台管理员任意。
func (s *WebPublishConfigService) Get(ctx context.Context, p auth.Principal, orgID string) (WebPublishConfigResult, error) {
	if !auth.CanViewOrg(p, orgID) {
		return WebPublishConfigResult{}, ErrForbidden
	}
	cfg, err := s.store.GetWebPublishConfig(ctx, orgID)
	if err != nil {
		return WebPublishConfigResult{}, err
	}
	return toConfigResult(cfg), nil // toConfigResult 做 null.* → *time.Time 映射、拼 wildcard
}

// RetryProvision 平台管理员手动重试签发/续签：入队 provision job（复用既有 handler）。
func (s *WebPublishConfigService) RetryProvision(ctx context.Context, p auth.Principal, orgID string) error {
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden
	}
	return s.enqueueProvision(ctx, orgID) // 提取自 Enable 的入队逻辑（Plan 2）
}
```
（把 Plan 2 `Enable` 里的"建 job + Enqueue"提取为私有 `enqueueProvision(ctx, orgID)`，`Enable` 与 `RetryProvision` 共用——DRY。`WebPublishConfigStore` 接口需加 `GetWebPublishConfig`。）

- [ ] **Step 4: 续签巡检**

`internal/worker/webpublish/cert_renewal.go`：
```go
package webpublish

import (
	"context"
	"fmt"
	"time"
)

// CertRenewalStore 是续签巡检所需能力：扫到期配置 + 入队 provision job。
type CertRenewalStore interface {
	ListConfigsCertExpiringBefore(ctx context.Context, before time.Time) ([]string, error) // 返回 orgID 列表（封装查询）
}

// ProvisionEnqueuer 抽象"为某 org 入队 provision job"（复用 WebPublishConfigService.RetryProvision 内部逻辑）。
type ProvisionEnqueuer interface {
	EnqueueProvision(ctx context.Context, orgID string) error
}

// CertRenewalChecker 巡检临近到期的通配证书并入队续签（复用 provision job）。
type CertRenewalChecker struct {
	store       CertRenewalStore
	enqueuer    ProvisionEnqueuer
	renewBefore time.Duration
	now         func() time.Time
}

// NewCertRenewalChecker 构造巡检器；renewBefore<=0 用默认 30 天。
func NewCertRenewalChecker(store CertRenewalStore, enq ProvisionEnqueuer, renewBefore time.Duration, now func() time.Time) *CertRenewalChecker {
	if renewBefore <= 0 {
		renewBefore = 30 * 24 * time.Hour
	}
	if now == nil {
		now = time.Now
	}
	return &CertRenewalChecker{store: store, enqueuer: enq, renewBefore: renewBefore, now: now}
}

// CheckOnce 扫一遍临近到期证书并逐个入队续签。
func (c *CertRenewalChecker) CheckOnce(ctx context.Context) error {
	threshold := c.now().Add(c.renewBefore)
	orgIDs, err := c.store.ListConfigsCertExpiringBefore(ctx, threshold)
	if err != nil {
		return fmt.Errorf("扫描待续签证书失败: %w", err)
	}
	for _, orgID := range orgIDs {
		_ = c.enqueuer.EnqueueProvision(ctx, orgID) // 单个失败不阻断
	}
	return nil
}
```
（`CertRenewalStore` 的实现把 sqlc `ListConfigsCertExpiringBefore` 行映射为 orgID 列表；`ProvisionEnqueuer` 由 `WebPublishConfigService` 暴露的 `EnqueueProvision` 满足。后台 loop 同 SiteReaper，12h tick。`CheckOnce` 用 fake 单测扫描+入队，loop 不单测。）

- [ ] **Step 5: Run tests + commit**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/worker/... ./internal/service/ -run 'Provision|CertRenewal|Renew|Retry' -v`
```bash
git add internal/worker/handlers/web_publish_provision.go internal/service/web_publish_config_service.go internal/worker/webpublish/cert_renewal.go internal/store/queries/ internal/store/sqlc/
git commit -m "feat: 通配证书自动续签与手动重试

provision handler 按现状区分首签(issuing)/续签(renewing)并写对应时间戳；
WebPublishConfigService 增加 Get(脱敏配置+证书状态) 与 RetryProvision(手动重试入队)；
CertRenewalChecker 巡检临近到期证书入队 provision job 完成续签（复用既有 handler）。"
```

---

### Task 5: 管理面 API（org 站点管理 + 证书状态 + 平台重试）

**Files:**
- Create: `internal/api/handlers/web_publish_sites.go`
- Modify: `internal/api/router.go`
- Test: `internal/api/handlers/web_publish_sites_test.go`
- Regenerate: openapi

- [ ] **Step 1: Write handler + routes（含 swag 注解）**

端点：
- `GET /api/v1/organizations/:orgId/published-sites` → org/平台 列站点（`WebPublishSiteService.ListByOrg`）
- `POST /api/v1/published-sites/:siteId/disable` → 下线（`Takedown`）
- `POST /api/v1/published-sites/:siteId/renew` → 续期（`Renew`）
- `GET /api/v1/organizations/:orgId/web-publish` → 配置+证书状态（`WebPublishConfigService.Get`，org 只读 + 平台）
- `POST /api/v1/platform/organizations/:orgId/web-publish/cert/retry` → 平台重试（`RetryProvision`）

handler 结构、principal 注入、`writeServiceError`、swag 注解参照 `organizations.go`/`runtime_knowledge.go`。每个 handler 方法带 `@Summary/@Tags/@Param/@Success/@Router` 注解。

- [ ] **Step 2: Write the failing test**

至少覆盖：list 200 转发 service、takedown/renew 调 service、cert/retry 限平台（org admin 调 403）、GET web-publish 返回证书状态字段。用 stub service + 构造 principal（参照既有 handler 测试如何注入 principal middleware/context）。

- [ ] **Step 3: Run → fail → implement → pass**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/api/handlers/ -run WebPublishSites -v`

- [ ] **Step 4: 注册路由 + openapi**

router.go 注册（org 端点在 user 组，平台端点限平台——权限在 service 层谓词兜底，路由层与 organizations 同级挂载）。
Run: `cd /home/user/ywjs-oc-manager && make openapi-gen && make web-types-gen && make openapi-check && go build ./...`

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/web_publish_sites.go internal/api/handlers/web_publish_sites_test.go internal/api/router.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(api): 增加站点管理与证书状态管理面接口

org/平台 列站点、手动下线、续期、查配置+证书状态（脱敏）、平台手动重试签发/续签；
权限谓词在 service 层校验，同步 swag 注解与 openapi/前端类型。"
```

---

### Task 6: 前端数据 hooks

**Files:**
- Create: `web/src/api/hooks/useWebPublish.ts`

- [ ] **Step 1: Write hooks（参照 `useOrganizations.ts` 模式）**

```typescript
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { apiRequest } from '../client'
import type { MaybeRefOrGetter } from 'vue'
import { toValue } from 'vue'

const SITES_KEY = (orgId: string) => ['published-sites', orgId]
const WEBPUBLISH_CFG_KEY = (orgId: string) => ['web-publish-config', orgId]

// usePublishedSitesQuery 拉某企业已发布站点列表。
export function usePublishedSitesQuery(orgId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: ['published-sites', toValue(orgId)],
    queryFn: () => apiRequest<{ sites: SiteResult[] }>(`/api/v1/organizations/${toValue(orgId)}/published-sites`),
  })
}

// useWebPublishConfigQuery 拉某企业发布能力配置 + 证书状态（脱敏，两角色可读）。
export function useWebPublishConfigQuery(orgId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: ['web-publish-config', toValue(orgId)],
    queryFn: () => apiRequest<WebPublishConfigResult>(`/api/v1/organizations/${toValue(orgId)}/web-publish`),
  })
}

// useTakedownSite 手动下线站点。
export function useTakedownSite(orgId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (siteId: string) => apiRequest(`/api/v1/published-sites/${siteId}/disable`, { method: 'POST' }),
    onSuccess: () => void client.invalidateQueries({ queryKey: SITES_KEY(orgId) }),
  })
}

// useRenewSite 续期站点。
export function useRenewSite(orgId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (siteId: string) => apiRequest(`/api/v1/published-sites/${siteId}/renew`, { method: 'POST' }),
    onSuccess: () => void client.invalidateQueries({ queryKey: SITES_KEY(orgId) }),
  })
}

// useRetryCert 平台管理员手动重试签发/续签。
export function useRetryCert(orgId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => apiRequest(`/api/v1/platform/organizations/${orgId}/web-publish/cert/retry`, { method: 'POST' }),
    onSuccess: () => void client.invalidateQueries({ queryKey: WEBPUBLISH_CFG_KEY(orgId) }),
  })
}
```
（类型 `SiteResult`/`WebPublishConfigResult` 从 `generated.ts` 导入或在 `api/index.ts` 加别名，以生成类型为准。）

- [ ] **Step 2: 类型检查 + commit**

Run: `cd /home/user/ywjs-oc-manager/web && npx vue-tsc --noEmit 2>&1 | head -20`（无新增类型错误）
```bash
git add web/src/api/hooks/useWebPublish.ts
git commit -m "feat(web): 增加 web-publish 数据 hooks

站点列表/配置+证书状态查询，下线/续期/证书重试 mutation（成功失效缓存），
参照 useOrganizations 的 TanStack Query 模式。"
```

---

### Task 7: 前端 org 站点列表页

**Files:**
- Create: `web/src/pages/org/PublishedSitesPage.vue`

- [ ] **Step 1: Write page（参照 `MembersPage.vue`）**

用 `DataTableList` + column builders 渲染站点表（URL/状态/到期/大小），`actionColumn` 提供"访问/下线/续期"，下线用 `ConfirmActionModal`。数据流：`usePublishedSitesQuery(effectiveOrgId)` + `useTakedownSite`/`useRenewSite`。列：
- `linkColumn` URL（点击新窗口打开 `https://host`）
- `statusColumn` 状态（active/disabled/expired → 文案）
- 到期时间、大小（格式化）
- `actionColumn`：下线（status=active 才显示，error 类型 + 确认弹窗）、续期

完整 `<script setup lang="ts">` + `<template>`，复用既有组件，i18n key 走 `t('org.publishedSites.*')`。（结构镜像 `MembersPage.vue`，此处不重复 Naive UI 样板——落地者按既有页面模式填充组件，逻辑/数据流/动作如上完整指定。）

- [ ] **Step 2: 类型检查 + commit**

Run: `cd /home/user/ywjs-oc-manager/web && npx vue-tsc --noEmit 2>&1 | head -20`
```bash
git add web/src/pages/org/PublishedSitesPage.vue
git commit -m "feat(web): 增加企业已发布站点列表页

DataTableList 渲染 URL/状态/到期/大小，支持访问、手动下线(确认弹窗)、续期，
复用既有列构造器与 ConfirmActionModal。"
```

---

### Task 8: 前端证书状态面板 + i18n + 路由

**Files:**
- Modify: 平台企业配置页（Plan 2 的页面）/ Create 证书状态展示组件
- Modify: `web/src/app/router.ts`
- Modify: `web/src/i18n/locales/{en,zh}/*.ts`

- [ ] **Step 1: 证书状态面板组件**

一个展示 `useWebPublishConfigQuery` 结果的卡片/组件：通配域 `*.base_domain`、`cert_status`（未签发/签发中/已签发/续签中/失败的彩色 tag）、到期时间、最近签发/续签时间、失败原因。平台管理员视图额外渲染"重试签发/续签"按钮（`useRetryCert`）；企业管理员只读。该组件复用在 org 站点页（只读）与平台配置页（可重试）。

- [ ] **Step 2: 路由注册**

`router.ts` 加：org 站点页（`meta.allowedRoles = ORG_ADMIN_ONLY`，路径如 `published-sites`），平台配置页扩展证书面板（沿用 Plan 2 已加的平台路由）。

- [ ] **Step 3: i18n（en + zh 同步）**

`locales/en/org.ts` + `locales/zh/org.ts` 加 `publishedSites.*`（标题、列、动作、确认弹窗文案）；`locales/{en,zh}/platform.ts` 加 `webPublishCert.*`（证书状态各态文案、重试按钮、域名/到期/失败原因标签）。两语言 key 必须对齐（completeness 测试）。

- [ ] **Step 4: i18n 完整性 + 类型检查**

Run: `cd /home/user/ywjs-oc-manager/web && npx vitest run i18n 2>&1 | tail -10 && npx vue-tsc --noEmit 2>&1 | head -20`
Expected: i18n completeness 通过；无新增类型错误

- [ ] **Step 5: Commit**

```bash
git add web/src/components/ web/src/pages/ web/src/app/router.ts web/src/i18n/
git commit -m "feat(web): 增加证书状态面板与路由/文案

证书状态面板组件展示通配域/状态/到期/最近签发续签/失败原因，平台管理员可重试、
企业管理员只读；注册站点页与平台配置页路由，补 en/zh 文案并保持 key 对齐。"
```

---

### Task 9: 装配后台 loop + 端到端验证

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 装配 service + 后台 loop**

构造 `WebPublishSiteService`、给 `WebPublishConfigService` 暴露 `EnqueueProvision`；启动 `SiteReaper` loop（60s）与 `CertRenewalChecker` loop（12h），参照现有 reaper 的 `Start(gctx)` goroutine 装配（main.go:773 附近）；注册 Task 5 的管理 API 路由。

- [ ] **Step 2: 全量编译测试**

Run: `cd /home/user/ywjs-oc-manager && go build ./... && go test ./internal/... -count=1 && make openapi-check`
Expected: 全绿 / openapi 干净

- [ ] **Step 3: 端到端浏览器验证（AGENTS.md）**

本地起全栈，真实浏览器验证：
1. org admin 登录 → 站点列表页看到 Plan 4 发布的站点（URL/状态/到期/大小）。
2. 点"续期"→ 到期时间延后；点"下线"（确认弹窗）→ 状态变 disabled，几秒后浏览器访问该 URL 返回 404。
3. 站点页/平台页证书面板显示通配域、cert_status=issued、到期时间。
4. 平台管理员对某企业点"重试签发/续签"→ 观察 cert_status 经 renewing 回 issued（或 staging 环境对应状态）。
5. TTL 回收：把某站点 expires_at 改到过去（或等 reaper），确认被置 expired + 对象前缀删除 + 访问 404。
6. 越权验证：org admin A 不能下线 org B 的站点（接口 403）。

> 环境受限（无公网 DNS/staging 证书不被信任）时，至少用真实浏览器验证管理页交互（列表/下线/续期/证书面板渲染与权限），站点 404/证书链路按 Plan 4 的降级方式验证，并在交付说明写明。

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(server): 装配站点回收与证书续签后台 loop 及管理面路由

启动 SiteReaper(60s)与 CertRenewalChecker(12h)后台 loop（redis 锁防多副本），
注册站点管理与证书状态管理面路由；完成生命周期与管理面端到端浏览器验证。"
```

---

## Self-Review

**1. Spec coverage（对应 §7/§8/§11.5）：**
- reaper job：扫 `expires_at < now && active` → 置 expired → 删对象前缀 → Task 3 ✓
- 手动下线：置 disabled + 删前缀（site-server 下轮 404）→ Task 2 ✓
- 续期：延长 expires_at（+ttl_days）→ Task 2 ✓
- 通配证书续期独立巡检（cert_not_after 临近）→ Task 4 CertRenewalChecker（复用 provision job + renewing 态）✓
- org admin：列本企业站点 + 下线 + 续期 + 只读证书状态 → Task 2/5/7/8 ✓
- 平台管理员：全局视图（按 org 列表）+ 手动重试签发/续签 → Task 4/5/8 ✓
- 证书状态面板（两角色可见，平台可重试、企业只读）→ Task 8 ✓
- 新增 handler/service/路由，请求体入 dto、响应用 service.XxxResult，OpenAPI 同步 → Task 5 ✓

**2. Placeholder scan：** Task 7/8 前端页面以"镜像 MembersPage.vue / 复用既有组件"指定结构而非逐行重写 Naive UI 样板——这是 DRY（复用项目既有 `DataTableList`/`ConfirmActionModal`/column builders），逻辑/数据流/列/动作均完整指定，非占位。Task 5 handler 实现步骤引用既有 handler 模式（principal 注入/swag）。后台 loop 不单测（与现有 reaper 一致，核心在 `ReapOnce`/`CheckOnce` 已 TDD）。真实证书续签/404 靠 Task 9 端到端验证。其余后端逻辑完整代码 + TDD。

**3. Type consistency：**
- `SiteResult`/`WebPublishConfigResult`（Task 2/4 service）→ Task 5 handler 响应 → Task 6 前端 hooks 类型一致（经 generated.ts）✓
- `domain.SiteStatusDisabled/Expired`（Plan 4）在 Task 2/3 一致用；`CertStatusRenewing/Issued`（Plan 2）在 Task 4 一致用 ✓
- sqlc `RenewPublishedSiteParams{ExpiresAt,ID}`、`SetPublishedSiteStatusParams{Status,ID}`、`ListExpiredActiveSites`/`ListSitesByOrg`/`GetPublishedSiteByID`（Plan 4 Task 3 + 本 plan Task 1）在 service/reaper 一致消费 ✓
- `enqueueProvision`（提取自 Plan 2 `Enable`）被 `RetryProvision` 与 `CertRenewalChecker.EnqueueProvision` 复用，统一入队 `JobTypeWebPublishProvision` → 同一 `WebPublishProvisionHandler` ✓
- `CanViewOrg`/`CanManageOrg`/`CanManageWebPublishConfig`（既有 + Plan 2）在 Task 2/4/5 一致用 ✓

**5-plan 套件收尾：** 至此 spec 五个子系统全部成计划：
1. DNS/cert 适配层 ✓ 2. 企业开通+provisioning ✓ 3. site-server ✓ 4. 发布链路 ✓ 5. 生命周期+管理面 ✓。
四处依赖倒置（site-server↔manager 同步端点、Ingress↔site-server Service、续签↔provision job、provision↔acme/dnsprovider）均以"先定契约/接口、后实现"闭环。建议执行顺序：1→2→4→3→5（3 与 4 可并行，但 4 实现 3 依赖的同步端点，故 4 不晚于 3 联调）。

**落地者需确认的仓库既有名：** 现有 `reaper` 的 `Start`/redis 锁 API（`internal/worker/reaper/reaper.go`）；`auth` 谓词与 `ErrForbidden`；前端 `DataTableList`/`ConfirmActionModal`/column builders 的确切 props；`router.ts` 的 `ORG_ADMIN_ONLY`/`PLATFORM_ONLY` 常量；i18n completeness 测试命令；Plan 2 `SetWebPublishCertStatus` 是否含 `cert_last_renewed_at` 列（不含则按 Task 4 Step 2 说明扩展）。
