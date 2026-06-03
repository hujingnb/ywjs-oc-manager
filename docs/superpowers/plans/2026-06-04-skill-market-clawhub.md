# Skill 市场：SkillSource 抽象与 ClawHub 接入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 引入可扩展的 `SkillSource` 抽象与「市场聚合」能力：平台库（PlatformSource，复用 Plan 1 的 platform_skills）+ ClawHub 公共库（ClawHubSource，调 ClawHub REST API + Redis 缓存、不落库），并暴露 `GET /api/v1/skill-market` 浏览/搜索接口。

**Architecture:** 新增 `internal/integrations/clawhub` HTTP 客户端（无 key 的纯 JSON REST，标准库 net/http）。`SkillSource` 接口由 `PlatformSource`（包 platform_skills 查询）与 `ClawHubSource`（包 ClawHub client + Redis 缓存）实现。`SkillLibraryService` 按 `source` 参数聚合，handler 暴露市场浏览。公共库元数据走 Redis TTL 缓存、不落库。

**Tech Stack:** Go 1.25 / Gin / go-redis v9 / 标准库 net/http / testify / sqlc（复用 Plan 1）。

这是「Hermes Skill 市场」功能（spec `docs/superpowers/specs/2026-06-03-skill市场-design.md`）的 Plan 2（共 6 个），依赖 **Plan 1 已合入**（platform_skills 表、PlatformSkillService、LibraryBlobStore）。

---

## File Structure

新建：
- `internal/integrations/clawhub/types.go` — ClawHub API 响应类型
- `internal/integrations/clawhub/client.go` — `ClawHubClient`（Search/GetSkill/ListVersions/Download）
- `internal/integrations/clawhub/client_test.go` — 用 httptest mock server 测试
- `internal/service/skill_source.go` — `SkillSource` 接口 + `SkillEntry`/`SkillPage` 类型 + `PlatformSource`
- `internal/service/skill_source_test.go`
- `internal/service/clawhub_source.go` — `ClawHubSource`（client + Redis 缓存）
- `internal/service/clawhub_source_test.go`
- `internal/service/skill_library_service.go` — `SkillLibraryService`（聚合）
- `internal/service/skill_library_service_test.go`
- `internal/api/handlers/skill_market.go` — `SkillMarketHandler` + `RegisterSkillMarketRoutes`
- `internal/api/handlers/skill_market_test.go`

修改：
- `internal/config/config.go` — 加 `ClawHubConfig`
- `internal/config/loader.go` — 加默认值
- `internal/service/errors.go` — 加市场哨兵错误
- `internal/api/router.go` — Dependencies 加字段 + 注册
- `cmd/server/main.go` — 构造 client/sources/service 接线
- `openapi/openapi.yaml`、`web/src/api/generated.ts` — 生成产物

> **执行时现场确认项**（plan 中已尽量精确，实现前对照确认）：① ClawHub 响应 JSON 的实际字段名（以 `<BaseURL>/api/v1/openapi.json` 为准，types.go 按实际调整）;② `httpclient.BaseHTTPClient` / `config.Duration` / `redis.Cmdable` 的实际签名（照 `internal/integrations/httpclient/client.go`、`internal/config/config.go`、`internal/redis/dist_locker.go`）;③ 模块路径 `oc-manager`。

---

## Task 1: ClawHub HTTP 客户端 + 响应类型

**Files:** Create `internal/integrations/clawhub/types.go`、`internal/integrations/clawhub/client.go`、`internal/integrations/clawhub/client_test.go`

- [ ] **Step 1: 写响应类型** — Create `internal/integrations/clawhub/types.go`（字段按 ClawHub `/api/v1/openapi.json` 实际调整；下面是基于已知 API 的合理定义）：

```go
// Package clawhub 是 ClawHub skill 市场（openclaw/clawhub）的只读 REST 客户端。
// 公开 API 无需鉴权；本包只做浏览/搜索/下载，缓存与聚合在 service 层。
package clawhub

// Skill 是 ClawHub 列表/搜索/详情返回的单个 skill 元数据。
// 字段名以 ClawHub openapi.json 为准，未知字段忽略（json 默认行为）。
type Skill struct {
	Slug        string `json:"slug"`        // 库内唯一标识，用作 source_ref
	Name        string `json:"name"`        // SKILL.md name
	Description string `json:"description"`
	Version     string `json:"version"`     // 最新版本（latest）
	Downloads   int64  `json:"downloads"`
}

// SearchResult 是 /api/v1/search 与 /api/v1/skills 的列表响应（含游标分页）。
type SearchResult struct {
	Skills     []Skill `json:"skills"`
	NextCursor string  `json:"next_cursor"`
}

// SkillVersion 是 /api/v1/skills/{slug}/versions 的单个版本项。
type SkillVersion struct {
	Version string `json:"version"`
}
```

- [ ] **Step 2: 写 client 失败测试** — Create `internal/integrations/clawhub/client_test.go`（用 httptest mock ClawHub）：

```go
package clawhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Search 调 /api/v1/search 并解析 skills 列表与游标。
func TestClawHubClient_Search(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/search", r.URL.Path)
		assert.Equal(t, "weather", r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[{"slug":"weather","name":"weather","description":"天气","version":"1.2","downloads":100}],"next_cursor":"c2"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	res, err := c.Search(context.Background(), "weather", "")
	require.NoError(t, err)
	require.Len(t, res.Skills, 1)
	assert.Equal(t, "weather", res.Skills[0].Slug)
	assert.Equal(t, "c2", res.NextCursor)
}

// Download 调 /api/v1/download 返回归档原始字节与扩展名 zip。
func TestClawHubClient_Download(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/download", r.URL.Path)
		assert.Equal(t, "weather", r.URL.Query().Get("slug"))
		assert.Equal(t, "1.2", r.URL.Query().Get("version"))
		_, _ = w.Write([]byte("PK\x03\x04zip-bytes"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	data, err := c.Download(context.Background(), "weather", "1.2")
	require.NoError(t, err)
	assert.Equal(t, []byte("PK\x03\x04zip-bytes"), data)
}
```

- [ ] **Step 3: 运行确认失败** — `go test ./internal/integrations/clawhub/ -v`，Expected: FAIL（NewClient 未定义）。

- [ ] **Step 4: 实现 client** — Create `internal/integrations/clawhub/client.go`（先读 `internal/integrations/ragflow/client.go` 确认自持 http.Client + doJSON 风格，照其写）：

```go
package clawhub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ClawHubClient 是 ClawHub 公开 API 的只读客户端（无鉴权）。
type ClawHubClient struct {
	baseURL string
	http    *http.Client
}

// NewClient 构造客户端；timeout<=0 时用 10s 默认。
func NewClient(baseURL string, timeout time.Duration) *ClawHubClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ClawHubClient{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

// getJSON 发 GET 请求并把 JSON 响应解码进 out。
func (c *ClawHubClient) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("构造 ClawHub 请求失败: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求 ClawHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ClawHub 返回非 2xx 状态: %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析 ClawHub 响应失败: %w", err)
	}
	return nil
}

// Search 搜索 skill（q 为空时等价列出热门）。cursor 为分页游标。
func (c *ClawHubClient) Search(ctx context.Context, q, cursor string) (SearchResult, error) {
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var out SearchResult
	// q 为空走 /api/v1/skills 列表；有 q 走 /api/v1/search。
	path := "/api/v1/skills"
	if q != "" {
		path = "/api/v1/search"
	}
	if err := c.getJSON(ctx, path, query, &out); err != nil {
		return SearchResult{}, err
	}
	return out, nil
}

// GetSkill 取单个 skill 元数据。
func (c *ClawHubClient) GetSkill(ctx context.Context, slug string) (Skill, error) {
	var out Skill
	if err := c.getJSON(ctx, "/api/v1/skills/"+url.PathEscape(slug), nil, &out); err != nil {
		return Skill{}, err
	}
	return out, nil
}

// ListVersions 列出某 skill 的全部版本。
func (c *ClawHubClient) ListVersions(ctx context.Context, slug string) ([]SkillVersion, error) {
	var out []SkillVersion
	if err := c.getJSON(ctx, "/api/v1/skills/"+url.PathEscape(slug)+"/versions", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Download 下载指定版本的归档原始字节（ClawHub 返回 zip）。
func (c *ClawHubClient) Download(ctx context.Context, slug, version string) ([]byte, error) {
	query := url.Values{"slug": {slug}, "version": {version}}
	endpoint := c.baseURL + "/api/v1/download?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 ClawHub 下载请求失败: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ClawHub 下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ClawHub 下载返回非 2xx: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 5: 运行确认通过** — `go test ./internal/integrations/clawhub/ -v`，Expected: PASS。

- [ ] **Step 6: 提交**：
```bash
git add internal/integrations/clawhub/
git commit -m "feat(skill): 增加 ClawHub 市场 REST 只读客户端

新增 clawhub 包：Search/GetSkill/ListVersions/Download，标准库 net/http，
无鉴权，httptest mock 覆盖搜索与下载。"
```

---

## Task 2: ClawHub 配置

**Files:** Modify `internal/config/config.go`、`internal/config/loader.go`

- [ ] **Step 1: 加配置结构** — 先读 `internal/config/config.go` 的 `RAGFlowConfig` 与 `Config` struct、`Duration` 类型确认风格，然后在 `Config` struct 加字段 `ClawHub ClawHubConfig `yaml:"clawhub"``，并定义：

```go
// ClawHubConfig 描述 ClawHub skill 市场 API 接入配置。
// BaseURL 为空时 ClawHubSource 降级（市场只显示平台库），不影响其它功能。
type ClawHubConfig struct {
	// BaseURL 是 ClawHub API 地址，例如 https://clawhubcn.com（国内站）。
	BaseURL string `yaml:"base_url"`
	// RequestTimeout 是单次请求超时，缺省 10s。
	RequestTimeout Duration `yaml:"request_timeout"`
	// CacheTTL 是搜索/列表结果的 Redis 缓存时长，缺省 5m。
	CacheTTL Duration `yaml:"cache_ttl"`
}
```

- [ ] **Step 2: 加默认值** — 在 `internal/config/loader.go` 的 `applyDefaults`（或等价函数，先读确认函数名）补：
```go
	if c.ClawHub.RequestTimeout.Duration == 0 {
		c.ClawHub.RequestTimeout.Duration = 10 * time.Second
	}
	if c.ClawHub.CacheTTL.Duration == 0 {
		c.ClawHub.CacheTTL.Duration = 5 * time.Minute
	}
```

- [ ] **Step 3: 编译 + 现有配置测试** — `go build ./internal/config/... && go test ./internal/config/...`，Expected: PASS（KnownFields 严格模式下新字段不破坏现有 yaml）。

- [ ] **Step 4: 提交**：
```bash
git add internal/config/config.go internal/config/loader.go
git commit -m "feat(skill): 增加 ClawHub 市场接入配置

新增 ClawHubConfig（base_url/request_timeout/cache_ttl）与默认值；BaseURL
为空时市场降级为仅平台库。"
```

---

## Task 3: SkillSource 抽象 + SkillEntry/SkillPage + PlatformSource

**Files:** Create `internal/service/skill_source.go`、`internal/service/skill_source_test.go`；Modify `internal/service/errors.go`

- [ ] **Step 1: 加哨兵错误** — `internal/service/errors.go` 末尾追加：
```go
// ===== skill 市场 =====

// ErrSkillMarketSourceUnknown 表示请求了未知的 skill 来源。
var ErrSkillMarketSourceUnknown = errors.New("未知的 skill 来源")
```

- [ ] **Step 2: 写 PlatformSource 失败测试** — Create `internal/service/skill_source_test.go`：

```go
package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PlatformSource.Search 按 name 聚合平台库（每个 name 取最新版本），q 子串过滤。
func TestPlatformSource_Search(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: []byte("a")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "2.0", Data: []byte("b")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "translate", Version: "1.0", Data: []byte("c")})
	require.NoError(t, err)

	src := NewPlatformSource(svc)
	page, err := src.Search(context.Background(), psvcPlatformPrincipal(), "weather", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)                    // 只匹配 weather，且聚合成一行
	assert.Equal(t, "weather", page.Entries[0].Name)
	assert.Equal(t, "platform", page.Entries[0].Source)
	assert.Equal(t, "weather", page.Entries[0].SourceRef) // platform 的 source_ref = name
	assert.Equal(t, "2.0", page.Entries[0].Version)       // 取最新版本
}
```

- [ ] **Step 3: 运行确认失败** — `go test ./internal/service/ -run TestPlatformSource -v`，Expected: FAIL。

- [ ] **Step 4: 实现 SkillSource + 类型 + PlatformSource** — Create `internal/service/skill_source.go`：

```go
package service

import (
	"context"
	"sort"
	"strings"

	"oc-manager/internal/auth"
)

// SkillEntry 是市场里一个 skill 的统一展示条目（跨来源）。
type SkillEntry struct {
	Source      string `json:"source"`       // platform | clawhub
	SourceRef   string `json:"source_ref"`   // 回源标识：platform=name、clawhub=slug
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`      // 最新版本
	Downloads   int64  `json:"downloads"`    // 仅 clawhub 有意义，platform 为 0
}

// SkillPage 是一页市场结果（含下一页游标，platform 无游标留空）。
type SkillPage struct {
	Entries    []SkillEntry `json:"entries"`
	NextCursor string       `json:"next_cursor"`
}

// SkillSource 是单个 skill 来源的浏览/搜索能力（platform / clawhub 各实现一个）。
type SkillSource interface {
	// Kind 返回来源标识（platform | clawhub）。
	Kind() string
	// Search 按关键词 q（空=列出）与游标 cursor 返回一页条目。
	Search(ctx context.Context, principal auth.Principal, q, cursor string) (SkillPage, error)
}

// PlatformSource 把平台库（platform_skills）适配为 SkillSource。
type PlatformSource struct {
	svc *PlatformSkillService
}

// NewPlatformSource 构造平台库来源。
func NewPlatformSource(svc *PlatformSkillService) *PlatformSource {
	return &PlatformSource{svc: svc}
}

// Kind 实现 SkillSource。
func (s *PlatformSource) Kind() string { return "platform" }

// Search 列出平台库 skill，按 name 聚合（每个 name 取最新版本），按 q 子串过滤。
// platform 无游标分页，NextCursor 恒为空。
func (s *PlatformSource) Search(ctx context.Context, principal auth.Principal, q, _ string) (SkillPage, error) {
	rows, err := s.svc.List(ctx, principal)
	if err != nil {
		return SkillPage{}, err
	}
	// 按 name 聚合，保留遍历到的第一条（List 已按 name, created_at DESC 排序，故首条即最新）。
	seen := map[string]SkillEntry{}
	order := []string{}
	for _, r := range rows {
		if q != "" && !strings.Contains(r.Name, q) && !strings.Contains(r.Description, q) {
			continue
		}
		if _, ok := seen[r.Name]; ok {
			continue
		}
		seen[r.Name] = SkillEntry{
			Source: "platform", SourceRef: r.Name, Name: r.Name,
			Description: r.Description, Version: r.Version, Downloads: 0,
		}
		order = append(order, r.Name)
	}
	sort.Strings(order)
	entries := make([]SkillEntry, 0, len(order))
	for _, n := range order {
		entries = append(entries, seen[n])
	}
	return SkillPage{Entries: entries, NextCursor: ""}, nil
}

var _ SkillSource = (*PlatformSource)(nil)
```

> 注：依赖 Plan 1 的 `ListPlatformSkills` 排序为 `name ASC, created_at DESC, id DESC`，故同 name 首条即最新版本。若排序不同，改为显式版本比较。

- [ ] **Step 5: 运行确认通过** — `go test ./internal/service/ -run TestPlatformSource -v`，Expected: PASS。

- [ ] **Step 6: 提交**：
```bash
git add internal/service/skill_source.go internal/service/skill_source_test.go internal/service/errors.go
git commit -m "feat(skill): 增加 SkillSource 抽象与平台库来源适配

定义 SkillEntry/SkillPage 统一条目与 SkillSource 接口；PlatformSource
把 platform_skills 按 name 聚合（取最新版本）适配为来源。"
```

---

## Task 4: ClawHubSource（Redis 缓存包装）

**Files:** Create `internal/service/clawhub_source.go`、`internal/service/clawhub_source_test.go`

- [ ] **Step 1: 写失败测试** — Create `internal/service/clawhub_source_test.go`（用 fake clawhub client + fake redis Cmdable）：

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/clawhub"
)

// fakeClawHubAPI 是 ClawHubSearcher 的内存实现，记录调用次数验证缓存命中。
type fakeClawHubAPI struct {
	result clawhub.SearchResult
	calls  int
}

func (f *fakeClawHubAPI) Search(_ context.Context, _, _ string) (clawhub.SearchResult, error) {
	f.calls++
	return f.result, nil
}

// 首次 Search 回源并写缓存；第二次相同入参命中缓存、不再回源。
func TestClawHubSource_SearchCaches(t *testing.T) {
	api := &fakeClawHubAPI{result: clawhub.SearchResult{
		Skills:     []clawhub.Skill{{Slug: "weather", Name: "weather", Description: "天气", Version: "1.2", Downloads: 9}},
		NextCursor: "n1",
	}}
	rdb := newFakeRedis()
	src := NewClawHubSource(api, rdb, time.Minute)

	p1, err := src.Search(context.Background(), auth.Principal{}, "weather", "")
	require.NoError(t, err)
	require.Len(t, p1.Entries, 1)
	assert.Equal(t, "clawhub", p1.Entries[0].Source)
	assert.Equal(t, "weather", p1.Entries[0].SourceRef) // clawhub 的 source_ref = slug
	assert.EqualValues(t, 9, p1.Entries[0].Downloads)
	assert.Equal(t, "n1", p1.NextCursor)
	assert.Equal(t, 1, api.calls)

	p2, err := src.Search(context.Background(), auth.Principal{}, "weather", "")
	require.NoError(t, err)
	assert.Equal(t, p1, p2)
	assert.Equal(t, 1, api.calls) // 命中缓存，回源次数不变
}
```

并在同文件加一个最小 fake redis（实现 `redis.Cmdable` 中本 source 用到的 Get/Set）。**实现前先读 `internal/redis/dist_locker.go` 确认注入的接口类型**（是 `redis.Cmdable` 还是 `*redis.Client`），fake 按需实现：

```go
// newFakeRedis 返回一个仅实现 Get/Set 的内存 redis 替身（满足 ClawHubSource 所需最小集）。
// 实现细节见 clawhub_source.go 对 redis 接口的依赖声明（RedisCache）。
```

> 设计取舍：为可测，ClawHubSource 依赖一个**最小缓存接口** `RedisCache`（Get/Set），而非整个 `redis.Cmdable`，由 `*redis.Client` 自然满足。这样 fake 只实现两个方法。

- [ ] **Step 2: 运行确认失败** — `go test ./internal/service/ -run TestClawHubSource -v`，Expected: FAIL。

- [ ] **Step 3: 实现 ClawHubSource** — Create `internal/service/clawhub_source.go`：

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/clawhub"
)

// ClawHubSearcher 是 ClawHubSource 依赖的最小搜索能力（由 clawhub.ClawHubClient 满足）。
type ClawHubSearcher interface {
	Search(ctx context.Context, q, cursor string) (clawhub.SearchResult, error)
}

// RedisCache 是 ClawHubSource 依赖的最小缓存能力（由 *redis.Client 满足）。
type RedisCache interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd
}

// ClawHubSource 把 ClawHub 公共库适配为 SkillSource，搜索结果走 Redis TTL 缓存、不落库。
type ClawHubSource struct {
	api ClawHubSearcher
	rdb RedisCache
	ttl time.Duration
}

// NewClawHubSource 构造公共库来源。
func NewClawHubSource(api ClawHubSearcher, rdb RedisCache, ttl time.Duration) *ClawHubSource {
	return &ClawHubSource{api: api, rdb: rdb, ttl: ttl}
}

// Kind 实现 SkillSource。
func (s *ClawHubSource) Kind() string { return "clawhub" }

// Search 先查 Redis 缓存，未命中再回源 ClawHub 并写缓存。结果转成统一 SkillEntry。
func (s *ClawHubSource) Search(ctx context.Context, _ auth.Principal, q, cursor string) (SkillPage, error) {
	key := "skill-market:clawhub:" + q + ":" + cursor
	if val, err := s.rdb.Get(ctx, key).Result(); err == nil {
		var page SkillPage
		if json.Unmarshal([]byte(val), &page) == nil {
			return page, nil
		}
	} else if !errors.Is(err, redis.Nil) {
		// 缓存读异常不致命，继续回源。
		_ = err
	}
	res, err := s.api.Search(ctx, q, cursor)
	if err != nil {
		return SkillPage{}, err
	}
	page := SkillPage{NextCursor: res.NextCursor, Entries: make([]SkillEntry, 0, len(res.Skills))}
	for _, sk := range res.Skills {
		page.Entries = append(page.Entries, SkillEntry{
			Source: "clawhub", SourceRef: sk.Slug, Name: sk.Name,
			Description: sk.Description, Version: sk.Version, Downloads: sk.Downloads,
		})
	}
	if raw, err := json.Marshal(page); err == nil {
		_ = s.rdb.Set(ctx, key, raw, s.ttl).Err()
	}
	return page, nil
}

var _ SkillSource = (*ClawHubSource)(nil)
```

并在测试文件实现 `newFakeRedis`（满足 `RedisCache`，用 map 存）：返回 `*redis.StringCmd`/`*redis.StatusCmd` 可用 `redis.NewStringResult(val, err)` / `redis.NewStatusResult("OK", nil)` 构造。

- [ ] **Step 4: 运行确认通过** — `go test ./internal/service/ -run TestClawHubSource -v`，Expected: PASS（含缓存命中断言）。

- [ ] **Step 5: 提交**：
```bash
git add internal/service/clawhub_source.go internal/service/clawhub_source_test.go
git commit -m "feat(skill): 增加 ClawHubSource（Redis 缓存的公共库来源）

搜索先查 Redis、未命中回源 ClawHub 并写 TTL 缓存，转统一 SkillEntry；
依赖最小 ClawHubSearcher/RedisCache 接口便于测试。"
```

---

## Task 5: SkillLibraryService 聚合 + SkillMarketHandler

**Files:** Create `internal/service/skill_library_service.go`、`internal/service/skill_library_service_test.go`、`internal/api/handlers/skill_market.go`、`internal/api/handlers/skill_market_test.go`

- [ ] **Step 1: 写 service 失败测试** — Create `internal/service/skill_library_service_test.go`：

```go
package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
)

// stubSource 是 SkillSource 的可控替身。
type stubSource struct {
	kind string
	page SkillPage
}

func (s *stubSource) Kind() string { return s.kind }
func (s *stubSource) Search(context.Context, auth.Principal, string, string) (SkillPage, error) {
	return s.page, nil
}

// source=platform 只走平台来源；source=clawhub 只走公共来源；空则聚合两者。
func TestSkillLibraryService_List(t *testing.T) {
	plat := &stubSource{kind: "platform", page: SkillPage{Entries: []SkillEntry{{Source: "platform", Name: "p1"}}}}
	claw := &stubSource{kind: "clawhub", page: SkillPage{Entries: []SkillEntry{{Source: "clawhub", Name: "c1"}}}}
	svc := NewSkillLibraryService(plat, claw)

	// 指定 platform
	page, err := svc.List(context.Background(), auth.Principal{}, "platform", "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "platform", page.Entries[0].Source)

	// 指定 clawhub
	page, err = svc.List(context.Background(), auth.Principal{}, "clawhub", "", "")
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "clawhub", page.Entries[0].Source)

	// 空 source 聚合两者
	page, err = svc.List(context.Background(), auth.Principal{}, "", "", "")
	require.NoError(t, err)
	assert.Len(t, page.Entries, 2)

	// 未知 source 报错
	_, err = svc.List(context.Background(), auth.Principal{}, "github", "", "")
	require.ErrorIs(t, err, ErrSkillMarketSourceUnknown)
}
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/service/ -run TestSkillLibraryService -v`，Expected: FAIL。

- [ ] **Step 3: 实现 SkillLibraryService** — Create `internal/service/skill_library_service.go`：

```go
package service

import (
	"context"

	"oc-manager/internal/auth"
)

// SkillLibraryService 聚合多个 SkillSource，提供市场浏览/搜索。
type SkillLibraryService struct {
	platform SkillSource
	clawhub  SkillSource // 可为 nil（未配置 ClawHub）
}

// NewSkillLibraryService 构造聚合 service。clawhub 可为 nil。
func NewSkillLibraryService(platform, clawhub SkillSource) *SkillLibraryService {
	return &SkillLibraryService{platform: platform, clawhub: clawhub}
}

// List 按 source 返回市场条目：
// "platform" 只查平台库；"clawhub" 只查公共库；"" 聚合（platform 在前）。
// 未知 source 返回 ErrSkillMarketSourceUnknown。
func (s *SkillLibraryService) List(ctx context.Context, principal auth.Principal, source, q, cursor string) (SkillPage, error) {
	switch source {
	case "platform":
		return s.platform.Search(ctx, principal, q, cursor)
	case "clawhub":
		if s.clawhub == nil {
			return SkillPage{Entries: []SkillEntry{}}, nil
		}
		return s.clawhub.Search(ctx, principal, q, cursor)
	case "":
		page, err := s.platform.Search(ctx, principal, q, "")
		if err != nil {
			return SkillPage{}, err
		}
		if s.clawhub != nil {
			cp, err := s.clawhub.Search(ctx, principal, q, cursor)
			if err == nil { // 公共库失败不阻断平台库展示（降级）
				page.Entries = append(page.Entries, cp.Entries...)
				page.NextCursor = cp.NextCursor
			}
		}
		return page, nil
	default:
		return SkillPage{}, ErrSkillMarketSourceUnknown
	}
}
```

- [ ] **Step 4: 运行确认通过** — `go test ./internal/service/ -run TestSkillLibraryService -v`，Expected: PASS。

- [ ] **Step 5: 写 handler 测试 + 实现** — Create `internal/api/handlers/skill_market.go`（参照 Plan 1 的 `platform_skills.go` handler 模式；service 接口、principalFromCtx、gin.H 包装、writeXxxError）与 `skill_market_test.go`：

handler 要点：
```go
// skillMarketService 是 handler 依赖的市场能力。
type skillMarketService interface {
	List(ctx context.Context, principal auth.Principal, source, q, cursor string) (service.SkillPage, error)
}
// SkillMarketHandler.List：读 c.Query("source"/"q"/"cursor") → service.List → gin.H{"page": page}
// 错误：ErrSkillMarketSourceUnknown → 400；其它 → 500（写 writeSkillMarketError）。
// RegisterSkillMarketRoutes：GET /api/v1/skill-market。
// swag 注解齐全（@Tags skill-market，@Failure 400/500）。
```
测试用 stub service：① 正常返回 page + 200；② source=github → 400。

- [ ] **Step 6: 编译 + 测试** — `go test ./internal/service/... ./internal/api/handlers/... -run 'SkillLibrary|SkillMarket' -v && go build ./...`，Expected: PASS。

- [ ] **Step 7: 提交**：
```bash
git add internal/service/skill_library_service.go internal/service/skill_library_service_test.go internal/api/handlers/skill_market.go internal/api/handlers/skill_market_test.go
git commit -m "feat(skill): 增加市场聚合 service 与 GET /skill-market 接口

SkillLibraryService 按 source 聚合平台库/公共库（公共库失败降级不阻断）；
SkillMarketHandler 暴露浏览/搜索，未知来源返回 400。"
```

---

## Task 6: 依赖接线（main + router）

**Files:** Modify `internal/api/router.go`、`cmd/server/main.go`

- [ ] **Step 1: router 加字段 + 注册** — `internal/api/router.go`：`Dependencies` 加 `SkillLibraryService *service.SkillLibraryService`；`NewRouter` user 组加 nil 守卫注册 `RegisterSkillMarketRoutes`。

- [ ] **Step 2: main 接线** — `cmd/server/main.go`：在 Plan 1 构造 `platformSkillService` 之后加：
```go
	// 平台库来源（复用 platformSkillService）。
	platformSource := service.NewPlatformSource(platformSkillService)
	// 公共库来源（ClawHub）：BaseURL 配置为空则不接入，市场降级为仅平台库。
	var clawhubSource service.SkillSource
	if cfg.ClawHub.BaseURL != "" {
		clawhubClient := clawhub.NewClient(cfg.ClawHub.BaseURL, cfg.ClawHub.RequestTimeout.Duration)
		clawhubSource = service.NewClawHubSource(clawhubClient, <redis.Client>, cfg.ClawHub.CacheTTL.Duration)
	}
	skillLibraryService := service.NewSkillLibraryService(platformSource, clawhubSource)
```
其中 `<redis.Client>` 用现有已构造的 `*redis.Client`（与 dist locker/queue 同源；先读 main 确认现有 redis client 变量名，复用它，避免新建连接）。填入 `Dependencies.SkillLibraryService`。

> 注意 `NewSkillLibraryService(platformSource, clawhubSource)` 第一个参数是 `SkillSource` 接口，`*PlatformSource` 满足；clawhubSource 为 `service.SkillSource`（nil 合法）。

- [ ] **Step 3: 编译 + 全量测试** — `go build ./... && go test ./internal/... && go vet ./...`，Expected: 全过。

- [ ] **Step 4: 提交**：
```bash
git add internal/api/router.go cmd/server/main.go
git commit -m "feat(skill): 接线市场聚合 service（平台库 + 可选 ClawHub）

main 构造 PlatformSource 与（配置非空时）ClawHubSource 复用现有 redis
client；router 注册 GET /skill-market。"
```

---

## Task 7: OpenAPI / 前端类型同步

**Files:** Generated `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 生成** — `make openapi-gen && make web-types-gen`，确认 `grep skill-market openapi/openapi.yaml` 出现路由与 `SkillPage`/`SkillEntry` schema。
- [ ] **Step 2: 校验** — `make openapi-check`，Expected: 退出 0。
- [ ] **Step 3: 全量** — `make test && make vet`，Expected: PASS。
- [ ] **Step 4: 提交**：
```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(skill): 同步市场接口的 OpenAPI 契约与前端类型"
```

---

## Self-Review 备注

- **Spec 覆盖**：本 plan 实现 spec 的「SkillSource 抽象（platform/clawhub 可扩展）」「ClawHub 适配 + Redis 缓存不落库」「市场聚合浏览 `GET /skill-market`」「公共库失败降级」。skill 详情（GetSkill）、安装（Download→缓存→app_skills）属 Plan 3。
- **类型一致**：`SkillEntry`/`SkillPage`/`SkillSource` 在 source、service、handler 间一致；`clawhub.Skill` 字段以 openapi.json 为准（现场确认项已标注）。
- **降级**：ClawHub BaseURL 空 → clawhubSource nil → 市场仅平台库；ClawHub 调用失败 → 聚合时不阻断平台库（spec 要求）。
- **不做**：详情/安装/下载缓存到对象存储（Plan 3）；GitHub marketplace.json 适配器（未来）。
- **现场确认项**（执行时务必先核对）：ClawHub 响应 JSON 字段、`config.Duration` 类型、main 里现有 `*redis.Client` 变量名、`httpclient`/handler helper 签名、模块路径 `oc-manager`。
