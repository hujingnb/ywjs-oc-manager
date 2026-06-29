# Web Publish — Plan 2: 企业能力开通 + Provisioning 状态机 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **依赖 Plan 1**：本 plan 消费 `2026-06-29-web-publish-01-dnsprovider-cert.md` 交付的接口：`dnsprovider.New(...)`、`acme.NewIssuer(...).Issue(...)`、`(*k8sorch.KubernetesAdapter).ApplyTLSSecret/DeleteTLSSecret`。执行本 plan 前 Plan 1 必须已合并、`go build ./...` 通过。

**Goal:** 平台管理员可按企业开通"网站发布"能力并配置基础域名 / DNS provider / 加密凭证；开通触发一次性异步 provisioning（写通配 A 解析 → ACME DNS-01 签通配证书 → 写 TLS Secret → 建一条通配 Ingress → site-server Service），状态机可观测、失败可自动重试，证书状态落库供页面展示。

**Architecture:** 新表 `org_web_publish_config`（单表存 per-org 配置 + provisioning/证书状态，凭证用 `auth.Cipher` 加密）。平台管理员经 HTTP handler → `WebPublishConfigService`（写表 + 入队 `web_publish_provision` job）。worker 的 `web_publish_provision` handler 是状态机：解密凭证 → `dnsprovider.New` → `acme.Issuer.Issue`（Plan 1）→ `k8sorch.ApplyTLSSecret` → 新增的 `k8sorch.ApplyWildcardIngress` → 置 `ready`；任一步失败置 `failed` 并由 worker 既有 backoff 重试。证书签发/Ingress 等外部副作用全抽成接口注入，状态机流转用 fake 全量单测。

**Tech Stack:** Go 1.25、MySQL（golang-migrate，新建 migration `000021`）、sqlc、`auth.Cipher`(AES-256-GCM)、worker/registry 既有异步框架、`k8s.io/api/networking/v1`（通配 Ingress）、gin + swag、testify。

---

## 背景约束（落地前必读）

- **依赖倒置（site-server 在 Plan 3）**：本 plan 建的通配 Ingress 的 backend 指向 site-server 的 Service（名/端口/namespace 来自 config，默认 `site-server:80`）。**Plan 2 阶段该 Service 尚不存在**——k8s 允许 Ingress 引用不存在的 backend（公网访问会 503 直到 Service 出现），Plan 3 部署 site-server 后即通。这是有意的解耦，不是缺陷；provisioning 不因 backend 缺失而失败。
- **命名空间一致性**：通配 Ingress、它引用的 TLS Secret、site-server Service 必须同命名空间。三者都用 manager `KubernetesConfig.Namespace`（默认 `oc-apps`，见 config.go:262）。`ApplyTLSSecret`/`ApplyWildcardIngress` 都用 adapter 内部 namespace，handler 不再传。
- **平台级 vs 企业级配置切分**：基础域名 / provider / 凭证是 per-org，入 `org_web_publish_config` 表；ingress 控制器公网 IP、ingressClassName、ACME email/CA、site-server service 名都是平台级、入全局 config（spec §4.1 前置约束）。
- **凭证不落明文 / 不进日志**：DNS provider 凭证 JSON → `auth.Cipher.Encrypt` → `dns_credentials_ciphertext`；handler 解密后只在内存用，禁止写日志（参照 `organizations` 凭证密文做法，agent 已确认 `auth.Cipher` 在 `internal/auth/crypto.go`）。
- **权限**：开通 / 改配置 / 重试均为 `platform_admin`，谓词放 `internal/auth/authorizer.go`（spec §4.1 / §8），不在 handler/service 内联 `if role==`。本 plan 复用既有 `auth.Principal` 与 `principalFromCtx`。
- **OpenAPI 同步**：改 handler 后必须 `make openapi-gen` + `make web-types-gen`，把 `openapi/openapi.yaml`、`web/src/api/generated.ts` 连同代码提交（AGENTS.md）。
- 注释 / 单测注释 / testify 断言规范同 Plan 1。

## File Structure

```
internal/migrations/
  000021_org_web_publish_config.up.sql     # 建表（带 COMMENT）
  000021_org_web_publish_config.down.sql   # drop 表

internal/store/queries/web_publish_config.sql  # sqlc 查询（生成到 internal/store/sqlc/）

internal/domain/enums.go                   # 追加 job type + provisioning/cert 状态枚举与校验

internal/config/config.go                  # 追加 WebPublishConfig（平台级）
internal/config/loader.go                  # defaults + validate

internal/integrations/k8sorch/
  ingress.go        # RenderWildcardIngress + ApplyWildcardIngress + DeleteWildcardIngress
  ingress_test.go

internal/service/
  web_publish_config_service.go   # 平台管理员配置 + 入队 provisioning
  web_publish_config_service_test.go

internal/worker/handlers/
  web_publish_provision.go        # provisioning 状态机 handler
  web_publish_provision_test.go

internal/api/handlers/
  web_publish_config.go           # 平台管理员 HTTP handler + 路由注册
  web_publish_config_test.go
  dto.go                          # 追加请求 DTO

cmd/server/main.go                # 装配 service / 注册 handler / 注册路由
config/manager.yaml（及本地示例）  # 追加 web_publish 段
```

---

### Task 1: domain 枚举（job type + provisioning/cert 状态）

**Files:**
- Modify: `internal/domain/enums.go`
- Test: `internal/domain/enums_test.go`（若不存在则新建；沿用既有 `IsJobType` 等测试风格）

- [ ] **Step 1: Write the failing test**

追加到 `internal/domain/enums_test.go`：
```go
// TestWebPublishProvisioningStatus 覆盖：四个 provisioning 状态合法，未知值非法，
// 保证写库前状态机取值受控。
func TestWebPublishProvisioningStatus(t *testing.T) {
	for _, s := range []string{ProvisioningDisabled, ProvisioningInProgress, ProvisioningReady, ProvisioningFailed} {
		assert.Truef(t, IsProvisioningStatus(s), "%s 应合法", s)
	}
	assert.False(t, IsProvisioningStatus("done"))
}

// TestWebPublishCertStatus 覆盖：五个证书状态合法，未知值非法（页面展示与巡检依赖）。
func TestWebPublishCertStatus(t *testing.T) {
	for _, s := range []string{CertStatusNone, CertStatusIssuing, CertStatusIssued, CertStatusRenewing, CertStatusFailed} {
		assert.Truef(t, IsCertStatus(s), "%s 应合法", s)
	}
	assert.False(t, IsCertStatus("expired"))
}

// TestWebPublishJobTypeRegistered 覆盖：新增的 provisioning job type 已登记，
// 否则 worker dispatch 时会报未注册类型。
func TestWebPublishJobTypeRegistered(t *testing.T) {
	assert.True(t, IsJobType(JobTypeWebPublishProvision))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/domain/ -run 'TestWebPublish' -v`
Expected: 编译失败 `undefined: ProvisioningDisabled / IsProvisioningStatus / ...`

- [ ] **Step 3: Write minimal implementation**

在 `internal/domain/enums.go` 第一个 const 块尾部追加（紧邻 JobStatus* 之后）：
```go
	// ProvisioningStatus* 描述企业 web-publish 能力开通的一次性 provisioning 进度
	// （写通配解析 → 签证书 → 建 Ingress）。落 org_web_publish_config.provisioning_status。
	ProvisioningDisabled   = "disabled"    // 未开通（初始态 / 已停用）
	ProvisioningInProgress = "provisioning" // 开通中，provisioning job 处理中
	ProvisioningReady      = "ready"       // 已就绪，可发布站点
	ProvisioningFailed     = "failed"      // provisioning 失败，可重试

	// CertStatus* 描述通配证书托管状态，落 org_web_publish_config.cert_status，供页面展示。
	CertStatusNone     = "none"     // 尚未签发
	CertStatusIssuing  = "issuing"  // 首次签发中
	CertStatusIssued   = "issued"   // 已签发可用
	CertStatusRenewing = "renewing" // 续签中（Plan 5 续期巡检写）
	CertStatusFailed   = "failed"   // 签发/续签失败
```

在第二个 const 块（JobType*）尾部追加：
```go
	// JobTypeWebPublishProvision 一次性开通企业 web-publish：通配解析 + 通配证书 + 通配 Ingress。
	JobTypeWebPublishProvision = "web_publish_provision"
```

在 var 块新增校验集合，并追加两个 Is* 函数：
```go
	validProvisioningStatuses = set(ProvisioningDisabled, ProvisioningInProgress, ProvisioningReady, ProvisioningFailed)
	validCertStatuses         = set(CertStatusNone, CertStatusIssuing, CertStatusIssued, CertStatusRenewing, CertStatusFailed)
```
把 `JobTypeWebPublishProvision` 加进 `validJobTypes` 集合。然后：
```go
// IsProvisioningStatus 校验 web-publish 能力开通状态取值是否合法。
func IsProvisioningStatus(value string) bool {
	_, ok := validProvisioningStatuses[value]
	return ok
}

// IsCertStatus 校验通配证书状态取值是否合法。
func IsCertStatus(value string) bool {
	_, ok := validCertStatuses[value]
	return ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/domain/ -run 'TestWebPublish' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/enums.go internal/domain/enums_test.go
git commit -m "feat(domain): 增加 web-publish provisioning 与证书状态枚举及 job 类型

新增 ProvisioningStatus*（disabled/provisioning/ready/failed）、CertStatus*
（none/issuing/issued/renewing/failed）与 JobTypeWebPublishProvision，并补校验函数，
供 org_web_publish_config 状态机与页面展示在写库前做受控校验。"
```

---

### Task 2: migration —— org_web_publish_config 表

**Files:**
- Create: `internal/migrations/000021_org_web_publish_config.up.sql`
- Create: `internal/migrations/000021_org_web_publish_config.down.sql`

- [ ] **Step 1: Write the up migration**

`internal/migrations/000021_org_web_publish_config.up.sql`：
```sql
-- org_web_publish_config 存每个企业的"网站发布"能力配置与一次性 provisioning / 证书托管状态。
-- 单独建表（不塞进 organizations）以隔离 provider 凭证密文与证书状态字段。
CREATE TABLE org_web_publish_config (
    org_id                     CHAR(36)     NOT NULL COMMENT '所属企业 ID（主键即一企业一行）',
    enabled                    TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '能力总开关：1 开通、0 停用',
    base_domain                VARCHAR(255) NOT NULL DEFAULT '' COMMENT '企业基础域名，站点为 <slug>.<base_domain>',
    dns_provider               VARCHAR(32)  NOT NULL DEFAULT '' COMMENT 'DNS provider：alidns/huaweicloud/tencentcloud/cmcccloud',
    dns_credentials_ciphertext TEXT         NULL COMMENT 'provider 凭证 JSON 的 auth.Cipher 密文（不落明文/不进日志）',
    site_ttl_days              INT          NOT NULL DEFAULT 7  COMMENT '站点默认存活天数（发布/续期用）',
    max_sites                  INT          NOT NULL DEFAULT 20 COMMENT '该企业最多同时存在的已发布站点数',
    provisioning_status        VARCHAR(20)  NOT NULL DEFAULT 'disabled' COMMENT '开通进度：disabled/provisioning/ready/failed',
    provisioning_message       TEXT         NULL COMMENT 'provisioning 失败原因 / 最近一次结果摘要',
    cert_secret_name           VARCHAR(253) NOT NULL DEFAULT '' COMMENT '通配证书 k8s TLS Secret 名（通配 Ingress 引用）',
    cert_status                VARCHAR(20)  NOT NULL DEFAULT 'none' COMMENT '证书状态：none/issuing/issued/renewing/failed',
    cert_not_after             DATETIME(6)  NULL COMMENT '证书到期时间（续期巡检依据）',
    cert_last_issued_at        DATETIME(6)  NULL COMMENT '最近一次签发成功时间',
    cert_last_renewed_at       DATETIME(6)  NULL COMMENT '最近一次续签成功时间',
    cert_message               TEXT         NULL COMMENT '证书失败原因 / 最近一次结果摘要',
    created_at                 DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at                 DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    PRIMARY KEY (org_id),
    CONSTRAINT fk_owpc_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT owpc_provisioning_status_check CHECK (provisioning_status IN ('disabled','provisioning','ready','failed')),
    CONSTRAINT owpc_cert_status_check CHECK (cert_status IN ('none','issuing','issued','renewing','failed')),
    CONSTRAINT owpc_dns_provider_check CHECK (dns_provider IN ('','alidns','huaweicloud','tencentcloud','cmcccloud'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='企业网站发布能力配置与证书托管状态';
```

`internal/migrations/000021_org_web_publish_config.down.sql`：
```sql
DROP TABLE IF EXISTS org_web_publish_config;
```

- [ ] **Step 2: 把新 schema 文件登记进 sqlc.yaml**

编辑 `sqlc.yaml` 的 schema 列表（agent 确认在 lines 5-23，按 migration 顺序列出 `*.up.sql`），在 `000020_*.up.sql` 之后追加：
```yaml
      - internal/migrations/000021_org_web_publish_config.up.sql
```

- [ ] **Step 3: 跑迁移测试验证 up/down 可执行**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/migrations/ -v`
Expected: PASS（`migrations_test.go` 会校验所有 up/down 成对且可加载；若该测试连真实 DB 需先 `make local-up`，按其现有约定执行）

- [ ] **Step 4: Commit**

```bash
git add internal/migrations/000021_org_web_publish_config.up.sql internal/migrations/000021_org_web_publish_config.down.sql sqlc.yaml
git commit -m "feat(db): 新增 org_web_publish_config 表存企业发布能力与证书状态

单表存 per-org 基础域名 / DNS provider / 凭证密文 / 站点配额，以及一次性
provisioning 进度与通配证书托管状态（cert_status/not_after/issued_at 等），
带完整中文 COMMENT 与状态 CHECK 约束；并登记进 sqlc.yaml schema。"
```

---

### Task 3: sqlc 查询

**Files:**
- Create: `internal/store/queries/web_publish_config.sql`
- Generate: `internal/store/sqlc/web_publish_config.sql.go`（由 sqlc 生成，勿手改）

- [ ] **Step 1: Write the queries**

`internal/store/queries/web_publish_config.sql`：
```sql
-- name: GetWebPublishConfig :one
-- 按企业取发布能力配置；不存在返回 sql.ErrNoRows（视为未开通）。
SELECT * FROM org_web_publish_config WHERE org_id = ?;

-- name: ListWebPublishConfigs :many
-- 平台管理员全局视图：列出所有企业的发布能力配置（Plan 5 用）。
SELECT * FROM org_web_publish_config ORDER BY updated_at DESC;

-- name: UpsertWebPublishConfig :exec
-- 平台管理员配置/改配置：写基础域名 / provider / 凭证密文 / 配额。
-- 不触碰 provisioning_status 与 cert_* 状态（那由状态机维护），首插时取列默认值。
INSERT INTO org_web_publish_config (
    org_id, base_domain, dns_provider, dns_credentials_ciphertext, site_ttl_days, max_sites
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    base_domain                = VALUES(base_domain),
    dns_provider               = VALUES(dns_provider),
    dns_credentials_ciphertext = VALUES(dns_credentials_ciphertext),
    site_ttl_days              = VALUES(site_ttl_days),
    max_sites                  = VALUES(max_sites),
    updated_at                 = now();

-- name: SetWebPublishEnabled :exec
-- 开通/停用：置 enabled 与 provisioning_status（开通时由 service 传 'provisioning'）。
UPDATE org_web_publish_config
SET enabled = ?, provisioning_status = ?, updated_at = now()
WHERE org_id = ?;

-- name: SetWebPublishProvisioning :exec
-- 状态机更新 provisioning 结果：状态 + 摘要 + 证书 Secret 名。
UPDATE org_web_publish_config
SET provisioning_status = ?, provisioning_message = ?, cert_secret_name = ?, updated_at = now()
WHERE org_id = ?;

-- name: SetWebPublishCertStatus :exec
-- 状态机/巡检更新证书状态：状态 + 到期 + 最近签发时间 + 摘要。
UPDATE org_web_publish_config
SET cert_status = ?, cert_not_after = ?, cert_last_issued_at = ?, cert_message = ?, updated_at = now()
WHERE org_id = ?;
```

- [ ] **Step 2: 生成 sqlc 代码**

Run: `cd /home/user/ywjs-oc-manager && make sqlc-gen 2>/dev/null || go run github.com/sqlc-dev/sqlc/cmd/sqlc generate`
（用仓库既有 sqlc 生成 make 目标；若 Makefile 目标名不同，`grep -n sqlc Makefile` 确认后用之。）
Expected: 生成 `internal/store/sqlc/web_publish_config.sql.go`，含 `OrgWebPublishConfig` 结构体与 `GetWebPublishConfig`/`UpsertWebPublishConfigParams` 等。

- [ ] **Step 3: 编译验证**

Run: `cd /home/user/ywjs-oc-manager && go build ./internal/store/...`
Expected: 无报错

- [ ] **Step 4: Commit**

```bash
git add internal/store/queries/web_publish_config.sql internal/store/sqlc/
git commit -m "feat(store): 增加 org_web_publish_config 的 sqlc 查询

覆盖 Get/List、配置 Upsert（不动状态机字段）、开通开关、provisioning 结果与
证书状态更新；生成对应 sqlc 代码供 service 与 provisioning 状态机使用。"
```

---

### Task 4: 平台级配置（config + loader）

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Test: `internal/config/loader_test.go`（沿用既有测试风格）

- [ ] **Step 1: Write the failing test**

追加到 `internal/config/loader_test.go`：
```go
// TestWebPublishDefaults 覆盖：web_publish 段缺省时填默认（CA staging、site-server:80、
// ACME 邮箱空但不报错——仅 enabled 校验），避免最小配置启动失败。
func TestWebPublishDefaults(t *testing.T) {
	var c Config
	applyDefaults(&c)
	assert.Equal(t, "site-server", c.WebPublish.SiteServerService)
	assert.Equal(t, int32(80), c.WebPublish.SiteServerPort)
	assert.NotEmpty(t, c.WebPublish.ACMEDirectoryURL) // 默认 staging 目录
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/config/ -run TestWebPublish -v`
Expected: 编译失败 `c.WebPublish undefined`

- [ ] **Step 3: Write minimal implementation**

在 `config.go` 顶层 `Config` struct 增加字段（与 `Kubernetes`/`Storage` 同级）：
```go
	// WebPublish 是企业网站发布能力的平台级配置（基础域名/provider/凭证为 per-org，入库）。
	WebPublish WebPublishConfig `yaml:"web_publish"`
```

新增结构（紧邻 `KubernetesConfig` 之后）：
```go
// WebPublishConfig 是 web-publish 能力的平台级配置。
// 企业级配置（基础域名 / DNS provider / 凭证）落 org_web_publish_config 表，不在此。
type WebPublishConfig struct {
	// IngressPublicIP 是平台 ingress 控制器的公网 IP；通配 A 记录 *.base_domain 指向它。
	IngressPublicIP string `yaml:"ingress_public_ip"`
	// IngressClassName 是通配 Ingress 的 ingressClassName，跟随环境（本地 traefik / 线上 controller）。
	IngressClassName string `yaml:"ingress_class_name"`
	// ACMEEmail 是 ACME 账户注册邮箱（证书到期通知等）。
	ACMEEmail string `yaml:"acme_email"`
	// ACMEDirectoryURL 是 ACME 目录 URL；缺省用 Let's Encrypt staging，生产需显式配生产目录。
	ACMEDirectoryURL string `yaml:"acme_directory_url"`
	// SiteServerService 是通配 Ingress 的 backend Service 名（Plan 3 部署），缺省 "site-server"。
	SiteServerService string `yaml:"site_server_service"`
	// SiteServerPort 是 backend Service 端口，缺省 80。
	SiteServerPort int32 `yaml:"site_server_port"`
}
```

在 `loader.go` 的 `applyDefaults` 追加：
```go
	// web-publish 默认值：site-server backend 名/端口 + ACME staging 目录，
	// 让最小配置可启动；生产须显式配 ingress 公网 IP / class / 生产 ACME 目录。
	if c.WebPublish.SiteServerService == "" {
		c.WebPublish.SiteServerService = "site-server"
	}
	if c.WebPublish.SiteServerPort == 0 {
		c.WebPublish.SiteServerPort = 80
	}
	if c.WebPublish.ACMEDirectoryURL == "" {
		// staging 目录避免本地/测试触发 Let's Encrypt 生产速率限制；生产环境必须覆盖。
		c.WebPublish.ACMEDirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/config/ -run TestWebPublish -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): 增加 web-publish 平台级配置

新增 WebPublishConfig（ingress 公网 IP / class、ACME 邮箱与目录、site-server
backend 名与端口），并设默认值（site-server:80 + ACME staging 目录）让最小
配置可启动；生产须显式覆盖 ingress 公网 IP 与生产 ACME 目录。"
```

---

### Task 5: k8sorch —— 通配 Ingress 渲染/apply/delete

**Files:**
- Create: `internal/integrations/k8sorch/ingress.go`
- Test: `internal/integrations/k8sorch/ingress_test.go`

- [ ] **Step 1: Write the failing test**

```go
package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestRenderWildcardIngress 覆盖：渲染出的 Ingress host 为 *.base、TLS 引用给定 Secret、
// backend 指向给定 service/port、ingressClassName 正确。
func TestRenderWildcardIngress(t *testing.T) {
	ing := RenderWildcardIngress(WildcardIngressSpec{
		Name: "wc-apps", Namespace: "oc-apps", BaseDomain: "apps.example.com",
		TLSSecretName: "wildcard-apps", IngressClassName: "traefik",
		BackendService: "site-server", BackendPort: 80,
	})
	assert.Equal(t, "traefik", *ing.Spec.IngressClassName)
	require.Len(t, ing.Spec.Rules, 1)
	assert.Equal(t, "*.apps.example.com", ing.Spec.Rules[0].Host)
	require.Len(t, ing.Spec.TLS, 1)
	assert.Equal(t, []string{"*.apps.example.com"}, ing.Spec.TLS[0].Hosts)
	assert.Equal(t, "wildcard-apps", ing.Spec.TLS[0].SecretName)
	b := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	assert.Equal(t, "site-server", b.Name)
	assert.Equal(t, int32(80), b.Port.Number)
}

// TestApplyWildcardIngressCreateThenUpdate 覆盖：首次 Apply 创建，二次 Apply 同名走更新分支不报错。
func TestApplyWildcardIngressCreateThenUpdate(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "oc-apps")
	spec := WildcardIngressSpec{Name: "wc", Namespace: "oc-apps", BaseDomain: "apps.example.com",
		TLSSecretName: "s", IngressClassName: "traefik", BackendService: "site-server", BackendPort: 80}

	require.NoError(t, a.ApplyWildcardIngress(context.Background(), spec))
	_, err := client.NetworkingV1().Ingresses("oc-apps").Get(context.Background(), "wc", metav1.GetOptions{})
	require.NoError(t, err)

	// 二次 apply 同名应更新而非 AlreadyExists 报错
	require.NoError(t, a.ApplyWildcardIngress(context.Background(), spec))
}

// TestDeleteWildcardIngressIdempotent 覆盖：删除不存在的 Ingress 不报错（回收幂等）。
func TestDeleteWildcardIngressIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "oc-apps")
	require.NoError(t, a.DeleteWildcardIngress(context.Background(), "missing"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/k8sorch/ -run 'WildcardIngress' -v`
Expected: 编译失败 `undefined: RenderWildcardIngress / WildcardIngressSpec / ApplyWildcardIngress`

- [ ] **Step 3: Write minimal implementation**

```go
package k8sorch

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WildcardIngressSpec 描述一条 *.base_domain → site-server 的通配 Ingress。
type WildcardIngressSpec struct {
	Name             string // Ingress 名（按企业基础域名确定性生成）
	Namespace        string // 命名空间（与 TLS Secret、site-server Service 同）
	BaseDomain       string // 企业基础域名（不含通配前缀）
	TLSSecretName    string // 通配证书 TLS Secret 名
	IngressClassName string // ingressClassName，跟随环境
	BackendService   string // backend Service 名（site-server）
	BackendPort      int32  // backend Service 端口
}

// RenderWildcardIngress 渲染一条把 *.base_domain 全部 path 转发给 site-server、
// 用通配证书做 TLS 的 Ingress。backend Service 可能此刻尚未存在（Plan 3 部署），
// k8s 允许，公网访问 503 直到 Service 出现。
func RenderWildcardIngress(s WildcardIngressSpec) *networkingv1.Ingress {
	wildcard := "*." + s.BaseDomain
	pathType := networkingv1.PathTypePrefix
	className := s.IngressClassName
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/part-of": "oc-manager", "app.kubernetes.io/component": "web-publish-ingress"},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{{
				Hosts:      []string{wildcard},
				SecretName: s.TLSSecretName,
			}},
			Rules: []networkingv1.IngressRule{{
				Host: wildcard,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: s.BackendService,
									Port: networkingv1.ServiceBackendPort{Number: s.BackendPort},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

// ApplyWildcardIngress 幂等 apply 通配 Ingress（首建创建、改配置更新）。
// spec.Namespace 若为空则用 adapter 命名空间，保持与 TLS Secret/Service 一致。
func (a *KubernetesAdapter) ApplyWildcardIngress(ctx context.Context, spec WildcardIngressSpec) error {
	if spec.Namespace == "" {
		spec.Namespace = a.namespace
	}
	ing := RenderWildcardIngress(spec)
	api := a.client.NetworkingV1().Ingresses(spec.Namespace)
	existing, err := api.Get(ctx, ing.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, ing, metav1.CreateOptions{})
		return wrapK8s("创建通配 Ingress", cerr)
	}
	if err != nil {
		return wrapK8s("查询通配 Ingress", err)
	}
	ing.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, ing, metav1.UpdateOptions{})
	return wrapK8s("更新通配 Ingress", uerr)
}

// DeleteWildcardIngress 删除通配 Ingress（NotFound 视为成功，幂等）。
func (a *KubernetesAdapter) DeleteWildcardIngress(ctx context.Context, name string) error {
	err := a.client.NetworkingV1().Ingresses(a.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除通配 Ingress", err)
	}
	return nil
}

// 占位避免未用 import（corev1 供后续可能的 backend 扩展；若编译报未用则删除该行与 import）。
var _ = corev1.ProtocolTCP
```

> 注：若 `corev1` 未被使用导致编译报错，删除最后两行与 `corev1` import（保留与否以 `go build` 为准）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/k8sorch/ -run 'WildcardIngress' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/k8sorch/ingress.go internal/integrations/k8sorch/ingress_test.go
git commit -m "feat(k8sorch): 增加通配 Ingress 的渲染与幂等 apply/delete

RenderWildcardIngress 产出 *.base_domain → site-server 的 TLS Ingress（引用
通配证书 Secret），ApplyWildcardIngress 复用 get-or-create-then-update 幂等模式；
backend service 允许此刻不存在（Plan 3 部署 site-server 后即通）。"
```

---

### Task 6: WebPublishConfigService（配置 + 入队 provisioning）

**Files:**
- Create: `internal/service/web_publish_config_service.go`
- Test: `internal/service/web_publish_config_service_test.go`

- [ ] **Step 1: Write the failing test**

```go
package service

import (
	"context"
	"encoding/json"
	"testing"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWPStore 是内存 WebPublishConfigStore：记录 upsert/enabled 调用与入队 job。
type fakeWPStore struct {
	upserted  *sqlc.UpsertWebPublishConfigParams
	enabled   *sqlc.SetWebPublishEnabledParams
	createdJob *sqlc.CreateJobParams
}

func (f *fakeWPStore) UpsertWebPublishConfig(_ context.Context, p sqlc.UpsertWebPublishConfigParams) error { f.upserted = &p; return nil }
func (f *fakeWPStore) SetWebPublishEnabled(_ context.Context, p sqlc.SetWebPublishEnabledParams) error { f.enabled = &p; return nil }
func (f *fakeWPStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) { return sqlc.OrgWebPublishConfig{}, nil }
func (f *fakeWPStore) CreateJob(_ context.Context, p sqlc.CreateJobParams) error { f.createdJob = &p; return nil }
func (f *fakeWPStore) CreateAuditLog(_ context.Context, _ sqlc.CreateAuditLogParams) error { return nil }

type fakeNotifier struct{ enqueued []string }
func (f *fakeNotifier) Enqueue(_ context.Context, id string) error { f.enqueued = append(f.enqueued, id); return nil }

// TestConfigureEncryptsCredentials 覆盖：平台管理员配置时凭证被加密落库、
// 明文不出现在 upsert 参数里（凭证安全）。
func TestConfigureEncryptsCredentials(t *testing.T) {
	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	st := &fakeWPStore{}
	svc := NewWebPublishConfigService(st, &fakeNotifier{}, cipher)
	admin := auth.Principal{Role: domain.UserRolePlatformAdmin}

	err = svc.Configure(context.Background(), admin, WebPublishConfigInput{
		OrgID: "org-1", BaseDomain: "apps.example.com", DNSProvider: "alidns",
		Credentials: map[string]string{"access_key_id": "AK", "access_key_secret": "SK"},
		SiteTTLDays: 7, MaxSites: 20,
	})
	require.NoError(t, err)
	require.NotNil(t, st.upserted)
	// 密文非空且不含明文
	require.True(t, st.upserted.DnsCredentialsCiphertext.Valid)
	assert.NotContains(t, st.upserted.DnsCredentialsCiphertext.String, "SK")
	// 解密可还原
	raw, derr := cipher.Decrypt(st.upserted.DnsCredentialsCiphertext.String)
	require.NoError(t, derr)
	var creds map[string]string
	require.NoError(t, json.Unmarshal(raw, &creds))
	assert.Equal(t, "AK", creds["access_key_id"])
}

// TestEnableEnqueuesProvisioning 覆盖：开通会置 enabled+provisioning 并入队 provisioning job。
func TestEnableEnqueuesProvisioning(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPStore{}
	nt := &fakeNotifier{}
	svc := NewWebPublishConfigService(st, nt, cipher)
	admin := auth.Principal{Role: domain.UserRolePlatformAdmin}

	require.NoError(t, svc.Enable(context.Background(), admin, "org-1"))
	require.NotNil(t, st.enabled)
	assert.Equal(t, domain.ProvisioningInProgress, st.enabled.ProvisioningStatus)
	require.NotNil(t, st.createdJob)
	assert.Equal(t, domain.JobTypeWebPublishProvision, st.createdJob.Type)
	assert.Len(t, nt.enqueued, 1)
}

// TestConfigureDeniedForNonPlatformAdmin 覆盖：非平台管理员配置被拒（权限谓词）。
func TestConfigureDeniedForNonPlatformAdmin(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	svc := NewWebPublishConfigService(&fakeWPStore{}, &fakeNotifier{}, cipher)
	member := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}
	err := svc.Configure(context.Background(), member, WebPublishConfigInput{OrgID: "org-1", DNSProvider: "alidns"})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run 'TestConfigure|TestEnable' -v`
Expected: 编译失败 `undefined: NewWebPublishConfigService / WebPublishConfigInput`

- [ ] **Step 3: 先加权限谓词**

在 `internal/auth/authorizer.go` 追加（若已有等价 `CanManagePlatform` 则复用，不重复造）：
```go
// CanManageWebPublishConfig 报告是否可开通/配置企业 web-publish 能力——仅平台管理员。
func CanManageWebPublishConfig(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}
```

- [ ] **Step 4: Write minimal implementation**

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// WebPublishConfigStore 是 service 所需的最小数据访问能力。
type WebPublishConfigStore interface {
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	UpsertWebPublishConfig(ctx context.Context, arg sqlc.UpsertWebPublishConfigParams) error
	SetWebPublishEnabled(ctx context.Context, arg sqlc.SetWebPublishEnabledParams) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// JobNotifier 复用既有定义（runtime_operation_service.go）：即时把 jobID 推入 Redis 队列。
// 此处不重复声明，直接引用同包内的 JobNotifier 接口。

// WebPublishConfigInput 是平台管理员配置一个企业发布能力的入参。
type WebPublishConfigInput struct {
	OrgID       string
	BaseDomain  string
	DNSProvider string
	Credentials map[string]string // 明文凭证；service 内加密后落库，绝不直接持久化/记日志
	SiteTTLDays int32
	MaxSites    int32
}

// WebPublishConfigService 负责平台管理员侧的发布能力配置与开通编排。
type WebPublishConfigService struct {
	store    WebPublishConfigStore
	notifier JobNotifier
	cipher   *auth.Cipher
}

// NewWebPublishConfigService 构造 service。
func NewWebPublishConfigService(store WebPublishConfigStore, notifier JobNotifier, cipher *auth.Cipher) *WebPublishConfigService {
	return &WebPublishConfigService{store: store, notifier: notifier, cipher: cipher}
}

// Configure 写入/更新企业发布能力配置：校验权限与 provider、加密凭证后 upsert。
// 不触发 provisioning（开通用 Enable）。
func (s *WebPublishConfigService) Configure(ctx context.Context, p auth.Principal, in WebPublishConfigInput) error {
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden // 复用 service 包既有 ErrForbidden（若名称不同，按实际错误常量替换）
	}
	if !domain.IsDNSProvider(in.DNSProvider) { // 见下方说明：domain 层薄校验或直接用 dnsprovider.ProviderType.Valid
		return fmt.Errorf("不支持的 DNS provider: %q", in.DNSProvider)
	}
	// 凭证 JSON → 加密 → 密文
	var cipherText null.String
	if len(in.Credentials) > 0 {
		raw, err := json.Marshal(in.Credentials)
		if err != nil {
			return fmt.Errorf("序列化凭证失败: %w", err)
		}
		enc, err := s.cipher.Encrypt(raw)
		if err != nil {
			return fmt.Errorf("加密凭证失败: %w", err)
		}
		cipherText = null.StringFrom(enc)
	}
	ttl := in.SiteTTLDays
	if ttl <= 0 {
		ttl = 7
	}
	maxSites := in.MaxSites
	if maxSites <= 0 {
		maxSites = 20
	}
	return s.store.UpsertWebPublishConfig(ctx, sqlc.UpsertWebPublishConfigParams{
		OrgID:                    in.OrgID,
		BaseDomain:               in.BaseDomain,
		DnsProvider:              in.DNSProvider,
		DnsCredentialsCiphertext: cipherText,
		SiteTtlDays:              ttl,
		MaxSites:                 maxSites,
	})
}

// Enable 开通企业发布能力：置 enabled + provisioning_status=provisioning，入队一次性 provisioning job。
func (s *WebPublishConfigService) Enable(ctx context.Context, p auth.Principal, orgID string) error {
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden
	}
	if err := s.store.SetWebPublishEnabled(ctx, sqlc.SetWebPublishEnabledParams{
		Enabled:            true,
		ProvisioningStatus: domain.ProvisioningInProgress,
		OrgID:              orgID,
	}); err != nil {
		return fmt.Errorf("置开通状态失败: %w", err)
	}
	jobID := uuid.NewString()
	payload, err := json.Marshal(map[string]string{"org_id": orgID})
	if err != nil {
		return err
	}
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeWebPublishProvision,
		Priority:    100,
		RunAfter:    time.Now(),
		MaxAttempts: 5,
		PayloadJson: payload,
	}); err != nil {
		return fmt.Errorf("创建 provisioning 任务失败: %w", err)
	}
	// 即时入队让 worker 毫秒级拿到；scheduler 周期扫库兜底（入队失败不阻断，job 仍会被扫到）。
	if err := s.notifier.Enqueue(ctx, jobID); err != nil {
		// 仅记不阻断：scheduler 兜底
		_ = err
	}
	return nil
}

var _ = errors.New // 占位：若未用到 errors 则删除该行与 import
```

> **关于 `domain.IsDNSProvider`**：可在 domain 层加一个薄校验集合（与 Task 1 同模式），或直接 `dnsprovider.ProviderType(in.DNSProvider).Valid()`。二选一，推荐后者避免枚举重复定义——若选后者，import `oc-manager/internal/integrations/dnsprovider` 并把校验改为 `if !dnsprovider.ProviderType(in.DNSProvider).Valid()`。本 plan 后续按"直接用 dnsprovider.ProviderType.Valid()"实现，删去 `domain.IsDNSProvider` 引用。
>
> **关于 `ErrForbidden`**：用 service 包既有的禁止错误常量（`grep -rn "ErrForbidden\|ErrUnauthorized" internal/service` 确认实际名称后替换）。

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/service/ -run 'TestConfigure|TestEnable' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/service/web_publish_config_service.go internal/service/web_publish_config_service_test.go internal/auth/authorizer.go
git commit -m "feat(service): 增加 WebPublishConfigService 配置与开通编排

Configure 校验平台管理员权限与 provider、加密 DNS 凭证后 upsert（明文不落库/不进
日志）；Enable 置开通态并入队 web_publish_provision job。权限谓词
CanManageWebPublishConfig 集中在 authorizer.go。"
```

---

### Task 7: provisioning 状态机 handler

**Files:**
- Create: `internal/worker/handlers/web_publish_provision.go`
- Test: `internal/worker/handlers/web_publish_provision_test.go`

> 状态机消费 Plan 1 接口。把"签证书"与"建 Ingress/写 Secret"抽成接口注入，用 fake 单测流转与失败置 failed。

- [ ] **Step 1: Write the failing test**

```go
package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/acme"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	null "github.com/guregu/null/v5"
)

// --- 测试替身 ---

type fakeWPProvStore struct {
	cfg          sqlc.OrgWebPublishConfig
	provUpdates  []sqlc.SetWebPublishProvisioningParams
	certUpdates  []sqlc.SetWebPublishCertStatusParams
}
func (f *fakeWPProvStore) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) { return f.cfg, nil }
func (f *fakeWPProvStore) SetWebPublishProvisioning(_ context.Context, p sqlc.SetWebPublishProvisioningParams) error { f.provUpdates = append(f.provUpdates, p); return nil }
func (f *fakeWPProvStore) SetWebPublishCertStatus(_ context.Context, p sqlc.SetWebPublishCertStatusParams) error { f.certUpdates = append(f.certUpdates, p); return nil }

type fakeProvisioner struct {
	ret acme.Certificate
	err error
	gotBaseDomain, gotIP string
}
func (f *fakeProvisioner) Provision(_ context.Context, in CertProvisionInput) (acme.Certificate, error) {
	f.gotBaseDomain, f.gotIP = in.BaseDomain, in.IngressIP
	return f.ret, f.err
}

type fakeClusterApplier struct {
	tlsApplied bool
	ingApplied bool
	tlsErr, ingErr error
}
func (f *fakeClusterApplier) ApplyTLSSecret(_ context.Context, _ string, _, _ []byte) error { f.tlsApplied = true; return f.tlsErr }
func (f *fakeClusterApplier) ApplyWildcardIngress(_ context.Context, _ WildcardIngressParams) error { f.ingApplied = true; return f.ingErr }

func newCfg(cipher *auth.Cipher) sqlc.OrgWebPublishConfig {
	raw, _ := json.Marshal(map[string]string{"access_key_id": "AK", "access_key_secret": "SK"})
	enc, _ := cipher.Encrypt(raw)
	return sqlc.OrgWebPublishConfig{
		OrgID: "org-1", BaseDomain: "apps.example.com", DnsProvider: "alidns",
		DnsCredentialsCiphertext: null.StringFrom(enc), CertSecretName: "wildcard-apps",
	}
}

func provJob() sqlc.Job {
	p, _ := json.Marshal(map[string]string{"org_id": "org-1"})
	return sqlc.Job{Type: domain.JobTypeWebPublishProvision, PayloadJson: p}
}

// TestProvisionHappyPath 覆盖：签证书成功 → 写 TLS Secret → 建通配 Ingress →
// provisioning_status=ready、cert_status=issued。验证完整状态流转与外部调用顺序产物。
func TestProvisionHappyPath(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPProvStore{cfg: newCfg(cipher)}
	prov := &fakeProvisioner{ret: acme.Certificate{CertPEM: []byte("C"), KeyPEM: []byte("K"), NotAfter: 1893456000}}
	cl := &fakeClusterApplier{}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	require.NoError(t, h.Handle(context.Background(), provJob()))

	assert.Equal(t, "apps.example.com", prov.gotBaseDomain)
	assert.Equal(t, "1.2.3.4", prov.gotIP)
	assert.True(t, cl.tlsApplied)
	assert.True(t, cl.ingApplied)
	// 最终 provisioning=ready
	require.NotEmpty(t, st.provUpdates)
	assert.Equal(t, domain.ProvisioningReady, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)
	// 最终 cert=issued
	require.NotEmpty(t, st.certUpdates)
	assert.Equal(t, domain.CertStatusIssued, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}

// TestProvisionCertFails 覆盖：签证书失败 → 返回错误（worker 据此重试）、
// provisioning_status=failed、cert_status=failed，且不建 Ingress。
func TestProvisionCertFails(t *testing.T) {
	cipher, _ := auth.NewCipher(make([]byte, 32))
	st := &fakeWPProvStore{cfg: newCfg(cipher)}
	prov := &fakeProvisioner{err: assert.AnError}
	cl := &fakeClusterApplier{}
	h := NewWebPublishProvisionHandler(st, prov, cl, cipher, WebPublishProvisionConfig{IngressPublicIP: "1.2.3.4"})

	err := h.Handle(context.Background(), provJob())
	require.Error(t, err)
	assert.False(t, cl.ingApplied, "签证书失败不应建 Ingress")
	assert.Equal(t, domain.ProvisioningFailed, st.provUpdates[len(st.provUpdates)-1].ProvisioningStatus)
	assert.Equal(t, domain.CertStatusFailed, st.certUpdates[len(st.certUpdates)-1].CertStatus)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/worker/handlers/ -run 'TestProvision' -v`
Expected: 编译失败 `undefined: NewWebPublishProvisionHandler / CertProvisionInput / WildcardIngressParams / ...`

- [ ] **Step 3: Write minimal implementation**

```go
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/acme"
	"oc-manager/internal/store/sqlc"
)

// WebPublishProvisionStore 是 provisioning 状态机所需的最小数据访问能力。
type WebPublishProvisionStore interface {
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	SetWebPublishProvisioning(ctx context.Context, arg sqlc.SetWebPublishProvisioningParams) error
	SetWebPublishCertStatus(ctx context.Context, arg sqlc.SetWebPublishCertStatusParams) error
}

// CertProvisionInput 是签发一张通配证书的入参（凭证已解密）。
type CertProvisionInput struct {
	ProviderType string
	Credentials  map[string]string
	BaseDomain   string
	IngressIP    string
}

// CertProvisioner 封装"用某 provider 凭证签发通配证书"的能力（内部 dnsprovider.New + acme.Issuer.Issue）。
// 抽成接口便于状态机单测注入 fake。
type CertProvisioner interface {
	Provision(ctx context.Context, in CertProvisionInput) (acme.Certificate, error)
}

// WildcardIngressParams 是建通配 Ingress 的入参（避免 handler 包反向依赖 k8sorch 的 spec 类型细节）。
type WildcardIngressParams struct {
	Name, BaseDomain, TLSSecretName, IngressClassName, BackendService string
	BackendPort int32
}

// ClusterApplier 封装把证书写 TLS Secret + 建通配 Ingress 的集群副作用，便于单测注入 fake。
type ClusterApplier interface {
	ApplyTLSSecret(ctx context.Context, name string, certPEM, keyPEM []byte) error
	ApplyWildcardIngress(ctx context.Context, p WildcardIngressParams) error
}

// WebPublishProvisionConfig 是 handler 的平台级配置（来自 config.WebPublishConfig）。
type WebPublishProvisionConfig struct {
	IngressPublicIP  string
	IngressClassName string
	BackendService   string
	BackendPort      int32
}

// WebPublishProvisionHandler 是 web_publish_provision job 的状态机处理器。
type WebPublishProvisionHandler struct {
	store  WebPublishProvisionStore
	prov   CertProvisioner
	applier ClusterApplier
	cipher *auth.Cipher
	cfg    WebPublishProvisionConfig
}

// NewWebPublishProvisionHandler 构造 handler。
func NewWebPublishProvisionHandler(store WebPublishProvisionStore, prov CertProvisioner, applier ClusterApplier, cipher *auth.Cipher, cfg WebPublishProvisionConfig) *WebPublishProvisionHandler {
	return &WebPublishProvisionHandler{store: store, prov: prov, applier: applier, cipher: cipher, cfg: cfg}
}

// Handle 执行一次性 provisioning：解密凭证 → 签通配证书 → 写 TLS Secret → 建通配 Ingress。
// 任一步失败：置 provisioning/cert=failed 并返回错误（worker 按 backoff 重试，最终成功置 ready）。
func (h *WebPublishProvisionHandler) Handle(ctx context.Context, job sqlc.Job) error {
	var payload struct {
		OrgID string `json:"org_id"`
	}
	if err := json.Unmarshal(job.PayloadJson, &payload); err != nil {
		return fmt.Errorf("解析 provisioning payload 失败: %w", err)
	}
	cfg, err := h.store.GetWebPublishConfig(ctx, payload.OrgID)
	if err != nil {
		return fmt.Errorf("加载发布配置失败: %w", err)
	}
	// 进入签发中
	h.setCert(ctx, payload.OrgID, domain.CertStatusIssuing, nil, "")

	// 解密凭证
	creds, err := h.decryptCredentials(cfg)
	if err != nil {
		return h.fail(ctx, payload.OrgID, "解密凭证失败", err)
	}
	// 签发通配证书（先写 A 记录后签发，由 acme.Issuer 内部完成）
	cert, err := h.prov.Provision(ctx, CertProvisionInput{
		ProviderType: cfg.DnsProvider, Credentials: creds,
		BaseDomain: cfg.BaseDomain, IngressIP: h.cfg.IngressPublicIP,
	})
	if err != nil {
		return h.fail(ctx, payload.OrgID, "签发证书失败", err)
	}
	// 写 TLS Secret
	if err := h.applier.ApplyTLSSecret(ctx, cfg.CertSecretName, cert.CertPEM, cert.KeyPEM); err != nil {
		return h.fail(ctx, payload.OrgID, "写 TLS Secret 失败", err)
	}
	// 建通配 Ingress
	if err := h.applier.ApplyWildcardIngress(ctx, WildcardIngressParams{
		Name: cfg.CertSecretName, BaseDomain: cfg.BaseDomain, TLSSecretName: cfg.CertSecretName,
		IngressClassName: h.cfg.IngressClassName, BackendService: h.cfg.BackendService, BackendPort: h.cfg.BackendPort,
	}); err != nil {
		return h.fail(ctx, payload.OrgID, "建通配 Ingress 失败", err)
	}
	// 成功：证书 issued + provisioning ready
	notAfter := time.Unix(cert.NotAfter, 0).UTC()
	h.store.SetWebPublishCertStatus(ctx, sqlc.SetWebPublishCertStatusParams{
		CertStatus: domain.CertStatusIssued, CertNotAfter: null.TimeFrom(notAfter),
		CertLastIssuedAt: null.TimeFrom(time.Now().UTC()), CertMessage: null.StringFrom("签发成功"), OrgID: payload.OrgID,
	})
	return h.store.SetWebPublishProvisioning(ctx, sqlc.SetWebPublishProvisioningParams{
		ProvisioningStatus: domain.ProvisioningReady, ProvisioningMessage: null.StringFrom("开通成功"),
		CertSecretName: cfg.CertSecretName, OrgID: payload.OrgID,
	})
}

// decryptCredentials 解密 provider 凭证密文为 map（密文为空返回空 map）。
func (h *WebPublishProvisionHandler) decryptCredentials(cfg sqlc.OrgWebPublishConfig) (map[string]string, error) {
	if !cfg.DnsCredentialsCiphertext.Valid || cfg.DnsCredentialsCiphertext.String == "" {
		return map[string]string{}, nil
	}
	raw, err := h.cipher.Decrypt(cfg.DnsCredentialsCiphertext.String)
	if err != nil {
		return nil, err
	}
	var creds map[string]string
	if err := json.Unmarshal(raw, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// fail 把 provisioning 与 cert 状态都置 failed 并返回带摘要的错误（worker 据此重试）。
func (h *WebPublishProvisionHandler) fail(ctx context.Context, orgID, stage string, cause error) error {
	msg := fmt.Sprintf("%s: %v", stage, cause)
	h.setCert(ctx, orgID, domain.CertStatusFailed, nil, msg)
	h.store.SetWebPublishProvisioning(ctx, sqlc.SetWebPublishProvisioningParams{
		ProvisioningStatus: domain.ProvisioningFailed, ProvisioningMessage: null.StringFrom(msg), OrgID: orgID,
	})
	return fmt.Errorf("provisioning %s", msg)
}

// setCert 是 cert 状态更新的小封装（只动状态与摘要，不动到期时间时传 nil）。
func (h *WebPublishProvisionHandler) setCert(ctx context.Context, orgID, status string, notAfter *time.Time, msg string) {
	p := sqlc.SetWebPublishCertStatusParams{CertStatus: status, OrgID: orgID}
	if notAfter != nil {
		p.CertNotAfter = null.TimeFrom(*notAfter)
	}
	if msg != "" {
		p.CertMessage = null.StringFrom(msg)
	}
	h.store.SetWebPublishCertStatus(ctx, p)
}

// 编译期断言：Handle 满足 worker 的 HandlerFunc 形态。
var _ HandlerFunc = (*WebPublishProvisionHandler)(nil).Handle
```

> **生产 CertProvisioner / ClusterApplier 实现**（无独立单测，触网/触集群，留 Plan 2 装配 + 本地联调）：
> - `certProvisionerImpl.Provision`：`p, err := dnsprovider.New(ctx, dnsprovider.ProviderType(in.ProviderType), in.Credentials, in.BaseDomain)` → `acme.NewIssuer(p, acme.IssuerConfig{Email, CADirURL}).Issue(ctx, in.BaseDomain, in.IngressIP)`。
> - `clusterApplierImpl`：包一个 `*k8sorch.KubernetesAdapter`，`ApplyTLSSecret` 直转，`ApplyWildcardIngress` 把 `WildcardIngressParams` 转成 `k8sorch.WildcardIngressSpec` 后调 `adapter.ApplyWildcardIngress`。
> 这两个实现放在 `cmd/server` 装配处或一个薄 adapter 文件，Task 9 装配时落地。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/worker/handlers/ -run 'TestProvision' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handlers/web_publish_provision.go internal/worker/handlers/web_publish_provision_test.go
git commit -m "feat(worker): 增加 web-publish provisioning 状态机 handler

解密凭证 → 签通配证书 → 写 TLS Secret → 建通配 Ingress，成功置 ready/issued、
任一步失败置 failed 并返回错误供 worker backoff 重试；签证书与集群副作用抽成
CertProvisioner/ClusterApplier 接口，状态流转用 fake 全量单测。"
```

---

### Task 8: HTTP handler + DTO + 路由 + OpenAPI

**Files:**
- Create: `internal/api/handlers/web_publish_config.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/router.go`
- Test: `internal/api/handlers/web_publish_config_test.go`
- Regenerate: `openapi/openapi.yaml`, `web/src/api/generated.ts`

- [ ] **Step 1: Write request DTO**

追加到 `internal/api/handlers/dto.go`：
```go
// ConfigureWebPublishRequest 是平台管理员配置企业发布能力的请求体。
type ConfigureWebPublishRequest struct {
	BaseDomain  string            `json:"base_domain" binding:"required"`
	DNSProvider string            `json:"dns_provider" binding:"required"`
	Credentials map[string]string `json:"credentials"`          // provider 凭证（明文上送，服务端加密落库）
	SiteTTLDays int32             `json:"site_ttl_days"`
	MaxSites    int32             `json:"max_sites"`
}
```

- [ ] **Step 2: Write the failing test**

```go
package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubWPConfigService 实现 handler 依赖接口，记录是否被调用。
type stubWPConfigService struct{ configured, enabled bool; err error }
func (s *stubWPConfigService) Configure(_ gin.Context, _ ConfigureWebPublishRequest) {} // 见下：实际接口以 service 方法签名为准

// TestConfigureWebPublishBadRequest 覆盖：缺 base_domain 的请求返回 400（绑定校验）。
func TestConfigureWebPublishBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造仅校验绑定的最小 handler 测试：用空 service，请求体缺 base_domain
	r := gin.New()
	// 路由注册见实现；此处直接打到 handler.Configure
	// ...（按实现注册）
	req := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/org-1/web-publish", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NotNil(t, w.Body)
}
```

> 注：handler 单测以仓库既有 handler 测试风格为准（参考 `runtime_knowledge_test.go` / `organizations` 的 handler 测试如何注入 stub service、构造 principal 中间件）。上面是骨架，落地时按既有模式补全 stub service 接口方法签名与路由注册，确保至少覆盖：400 绑定失败、200 正常调用转发到 service、Enable 入口。

- [ ] **Step 3: Write handler + routes**

```go
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// webPublishConfigService 是 handler 依赖的 service 能力（便于单测注入 stub）。
type webPublishConfigService interface {
	Configure(ctx context.Context, p auth.Principal, in service.WebPublishConfigInput) error
	Enable(ctx context.Context, p auth.Principal, orgID string) error
	Disable(ctx context.Context, p auth.Principal, orgID string) error
}

// WebPublishConfigHandler 暴露平台管理员配置/开通企业发布能力的 HTTP 接口。
type WebPublishConfigHandler struct {
	service webPublishConfigService
}

// NewWebPublishConfigHandler 构造 handler。
func NewWebPublishConfigHandler(svc webPublishConfigService) *WebPublishConfigHandler {
	return &WebPublishConfigHandler{service: svc}
}

// RegisterWebPublishConfigRoutes 注册平台管理员路由（鉴权中间件在父 router 已挂）。
func RegisterWebPublishConfigRoutes(router gin.IRouter, h *WebPublishConfigHandler) {
	g := router.Group("/api/v1/platform/organizations/:orgId/web-publish")
	g.PUT("", h.Configure)       // 配置/改配置
	g.POST("/enable", h.Enable)  // 开通（触发 provisioning）
	g.POST("/disable", h.Disable)
}

// Configure 配置企业发布能力。
//
// @Summary      配置企业网站发布能力
// @Tags         web-publish
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Param        body   body  ConfigureWebPublishRequest  true  "配置请求"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /platform/organizations/{orgId}/web-publish [put]
func (h *WebPublishConfigHandler) Configure(c *gin.Context) {
	principal := principalFromCtx(c)
	var req ConfigureWebPublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	in := service.WebPublishConfigInput{
		OrgID: c.Param("orgId"), BaseDomain: req.BaseDomain, DNSProvider: req.DNSProvider,
		Credentials: req.Credentials, SiteTTLDays: req.SiteTTLDays, MaxSites: req.MaxSites,
	}
	if err := h.service.Configure(c.Request.Context(), principal, in); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Enable 开通企业发布能力（触发 provisioning）。
//
// @Summary      开通企业网站发布能力
// @Tags         web-publish
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Success      204
// @Failure      403  {object}  ErrorResponse
// @Router       /platform/organizations/{orgId}/web-publish/enable [post]
func (h *WebPublishConfigHandler) Enable(c *gin.Context) {
	if err := h.service.Enable(c.Request.Context(), principalFromCtx(c), c.Param("orgId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Disable 停用企业发布能力。
//
// @Summary      停用企业网站发布能力
// @Tags         web-publish
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Success      204
// @Router       /platform/organizations/{orgId}/web-publish/disable [post]
func (h *WebPublishConfigHandler) Disable(c *gin.Context) {
	if err := h.service.Disable(c.Request.Context(), principalFromCtx(c), c.Param("orgId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
```

> `Disable` 的 service 方法（置 `enabled=false`、`provisioning_status=disabled`，并可选回收 Ingress/Secret——回收建议放 Plan 5 与站点回收一起）在 Task 6 的 service 补一个对称方法即可（`SetWebPublishEnabled(false, ProvisioningDisabled)`）。把它加进 Task 6 实现与测试。

- [ ] **Step 4: 注册路由**

在 `internal/api/router.go` 仿照 `OrganizationService` 的注册（agent 确认在 router.go:170），在 router 依赖结构 `dep` 加 `WebPublishConfigService` 字段，并：
```go
	if dep.WebPublishConfigService != nil {
		handlers.RegisterWebPublishConfigRoutes(user, handlers.NewWebPublishConfigHandler(dep.WebPublishConfigService))
	}
```
（`user` 为已挂 `RequireUserAuth` 的路由组，与 organizations 同级。）

- [ ] **Step 5: Run handler test + build**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/api/handlers/ -run 'WebPublish' -v && go build ./...`
Expected: PASS / 编译通过

- [ ] **Step 6: 生成 OpenAPI 与前端类型**

Run: `cd /home/user/ywjs-oc-manager && make openapi-gen && make web-types-gen && make openapi-check`
Expected: `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 更新；`openapi-check` 工作区干净（生成稳定）

- [ ] **Step 7: Commit**

```bash
git add internal/api/handlers/web_publish_config.go internal/api/handlers/web_publish_config_test.go internal/api/handlers/dto.go internal/api/router.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(api): 增加平台管理员配置/开通企业发布能力的接口

PUT 配置（凭证服务端加密）、POST enable 触发 provisioning、POST disable 停用，
均限 platform_admin；同步 swag 注解与 openapi.yaml / 前端生成类型。"
```

---

### Task 9: cmd/server 装配 + 本地配置

**Files:**
- Modify: `cmd/server/main.go`
- Create/Modify: `cmd/server/wiring.go`（若装配 adapter 习惯放此）
- Modify: `config/manager.yaml`（本地示例）

- [ ] **Step 1: 装配 CertProvisioner / ClusterApplier 生产实现**

新增薄 adapter（放 `cmd/server/wiring.go` 或 `internal/worker/handlers` 同包的 `*_impl.go`，按仓库习惯）：
```go
// certProvisionerImpl 用 dnsprovider + acme 实现 handlers.CertProvisioner。
type certProvisionerImpl struct {
	email, caDirURL string
}
func (c certProvisionerImpl) Provision(ctx context.Context, in handlers.CertProvisionInput) (acme.Certificate, error) {
	p, err := dnsprovider.New(ctx, dnsprovider.ProviderType(in.ProviderType), in.Credentials, in.BaseDomain)
	if err != nil {
		return acme.Certificate{}, err
	}
	return acme.NewIssuer(p, acme.IssuerConfig{Email: c.email, CADirURL: c.caDirURL}).Issue(ctx, in.BaseDomain, in.IngressIP)
}

// clusterApplierImpl 把 handlers.ClusterApplier 转调 k8sorch adapter。
type clusterApplierImpl struct{ adapter *k8sorch.KubernetesAdapter }
func (a clusterApplierImpl) ApplyTLSSecret(ctx context.Context, name string, cert, key []byte) error {
	return a.adapter.ApplyTLSSecret(ctx, name, cert, key)
}
func (a clusterApplierImpl) ApplyWildcardIngress(ctx context.Context, p handlers.WildcardIngressParams) error {
	return a.adapter.ApplyWildcardIngress(ctx, k8sorch.WildcardIngressSpec{
		Name: p.Name, BaseDomain: p.BaseDomain, TLSSecretName: p.TLSSecretName,
		IngressClassName: p.IngressClassName, BackendService: p.BackendService, BackendPort: p.BackendPort,
	})
}
```

- [ ] **Step 2: 注册 service、handler、路由**

在 `main.go` service 构造区构造 `WebPublishConfigService` 并放进 router `dep`；在 registry 注册区（main.go:449-540 附近）追加：
```go
	if err := registry.Register(domain.JobTypeWebPublishProvision,
		handlers.NewWebPublishProvisionHandler(
			dbStore.Queries,
			certProvisionerImpl{email: cfg.WebPublish.ACMEEmail, caDirURL: cfg.WebPublish.ACMEDirectoryURL},
			clusterApplierImpl{adapter: k8sAdapter}, // 复用已构造的 KubernetesAdapter
			cipher,
			handlers.WebPublishProvisionConfig{
				IngressPublicIP:  cfg.WebPublish.IngressPublicIP,
				IngressClassName: cfg.WebPublish.IngressClassName,
				BackendService:   cfg.WebPublish.SiteServerService,
				BackendPort:      cfg.WebPublish.SiteServerPort,
			},
		).Handle); err != nil {
		return fmt.Errorf("注册 web_publish_provision handler 失败: %w", err)
	}
```
（`k8sAdapter`/`cipher`/`dbStore` 用 main.go 既有变量名，按实际命名对齐。`dbStore.Queries` 满足 `WebPublishProvisionStore` 与 service 的 `WebPublishConfigStore`——因 sqlc Queries 已实现全部方法。）

- [ ] **Step 3: 本地配置示例**

在 `config/manager.yaml`（及本地 dev 配置）追加：
```yaml
web_publish:
  ingress_public_ip: "127.0.0.1"        # 本地 k3d traefik；生产填真实公网 IP
  ingress_class_name: "traefik"
  acme_email: "ops@example.com"
  acme_directory_url: "https://acme-staging-v02.api.letsencrypt.org/directory"
  site_server_service: "site-server"
  site_server_port: 80
```

- [ ] **Step 4: 全量编译与测试**

Run: `cd /home/user/ywjs-oc-manager && go build ./... && go test ./internal/... -count=1`
Expected: 编译通过；新增单测全绿（触网/触集群部分未在单测覆盖，已在 Plan 1/2 注明）

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go cmd/server/wiring.go config/manager.yaml
git commit -m "feat(server): 装配 web-publish 配置服务与 provisioning handler

构造 WebPublishConfigService 并注册平台管理员路由；注册 web_publish_provision
worker handler，注入 dnsprovider+acme 的 CertProvisioner 与 k8sorch 的
ClusterApplier；补本地 web_publish 配置示例（ACME staging + traefik）。"
```

---

## Self-Review

**1. Spec coverage（对应 §4.1 / §11.2）：**
- 新表 `org_web_publish_config`（含证书状态字段）→ Task 2 ✓
- 平台管理员开关 + 配置（base_domain/provider/凭证密文/ttl/max_sites）→ Task 6 service + Task 8 API ✓
- 开通触发一次性 provisioning（异步、状态机、失败可重试）→ Task 7 handler + worker 既有 backoff ✓
- provisioning：通配 A 解析 + DNS-01 通配证书 + TLS Secret + 一条通配 Ingress → Task 5 + Task 7（A 记录与签发在 Plan 1 的 Issuer 内）✓
- 证书状态字段落库并可被读取（cert_status/not_after/issued_at/message）→ Task 2 列 + Task 7 写入 ✓（页面展示在 Plan 5）
- 权限 platform_admin 集中在 authorizer.go → Task 6 `CanManageWebPublishConfig` ✓
- 凭证加密（auth.Cipher）→ Task 6 ✓
- OpenAPI 同步 → Task 8 Step 6 ✓
- 前置约束（ingress 公网 IP / ingressClassName 跟随环境）→ Task 4 config ✓

**2. Placeholder scan：** 生产 `CertProvisioner`/`ClusterApplier` 实现在 Task 9 给出完整代码（非占位）；触网/触集群路径不做单测是显式取舍并说明验证方式（本地 staging 联调）。Task 8 Step 2 的 handler 单测标注"按既有 handler 测试风格补全 stub/路由"——这是因为要对齐仓库未在本 plan 引用的测试基建（principal 中间件注入方式），不是代码占位；落地者参照 `runtime_knowledge_test.go`。

**3. Type consistency：**
- `domain` 常量（ProvisioningInProgress/ProvisioningReady/ProvisioningFailed、CertStatusIssuing/Issued/Failed、JobTypeWebPublishProvision）在 Task 1 定义，Task 6/7 一致引用 ✓
- sqlc 生成的 `OrgWebPublishConfig`、`UpsertWebPublishConfigParams`、`SetWebPublishEnabledParams`、`SetWebPublishProvisioningParams`、`SetWebPublishCertStatusParams`、`CreateJobParams` 在 Task 6/7 一致消费（字段名以 sqlc 实际生成为准：列名 `dns_credentials_ciphertext` → `DnsCredentialsCiphertext`，`site_ttl_days` → `SiteTtlDays`，落地时若大小写不符以生成代码为准）✓
- Plan 1 接口签名 `acme.NewIssuer(provider, acme.IssuerConfig)`、`Issue(ctx, baseDomain, ip)`、`acme.Certificate{CertPEM,KeyPEM,NotAfter}`、`ApplyTLSSecret(ctx,name,cert,key)` 在 Task 7/9 一致消费 ✓
- `JobNotifier`（service 包既有）、`HandlerFunc`（handlers 包既有）复用，不重复定义 ✓

**给 Plan 3 / Plan 5 的契约：**
- 通配 Ingress backend 默认 `site-server:80`（Plan 3 须以此名/端口部署 Service）。
- `cert_secret_name` 同时用作 TLS Secret 名与通配 Ingress 名（确定性、便于回收）。
- `org_web_publish_config` 的 `ListWebPublishConfigs`、`SetWebPublishCertStatus` 已就绪，供 Plan 5 全局视图与续期巡检复用。
- 证书续期巡检（`cert_not_after` 到期前重签 + `ApplyTLSSecret` 覆盖 + `cert_status=renewing/issued`）放 Plan 5。

**落地者需自行确认的仓库既有名（本 plan 已标注）：** service 包禁止错误常量名（`ErrForbidden` 或其他）、sqlc 生成 make 目标名、router `dep` 结构字段挂载方式、main.go 中 `KubernetesAdapter`/`cipher` 的实际变量名。
