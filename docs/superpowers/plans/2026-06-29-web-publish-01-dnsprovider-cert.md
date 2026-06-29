# Web Publish — Plan 1: DNS Provider 适配层 + ACME 通配证书签发 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个自包含、可单测的证书/DNS 能力库：给定 DNS provider 名 + 加密凭证 + 基础域名 + ingress 公网 IP，能 (a) 写/删通配 A 记录 `*.base_domain → ingressIP`，(b) 通过 ACME DNS-01 签发 / 续签 `*.base_domain` 通配证书，(c) 把证书写成 `kubernetes.io/tls` Secret。

**Architecture:** 三个新包，依赖单向：`dnsprovider`（统一 Provider 接口 + 四家实现，封装 lego 原生 provider 与 vendored cmcccloud）→ `acme`（用 go-acme/lego 编排 DNS-01 签发/续签，产出 PEM）→ `k8sorch` 扩展（把 PEM 写成 TLS Secret）。核心编排逻辑用 fake provider / fake ACME client 全量单测；真实云厂商 SDK 调用藏在接口后，落地时用 `go doc` 验证 API 后实现。本 plan **不接 worker/不接 HTTP/不动 DB**——那是 Plan 2（provisioning 状态机）的事，本 plan 交付一个被调用的库。

**Tech Stack:** Go 1.25、`github.com/go-acme/lego/v4`（新增依赖）、各云厂商 DNS SDK（alidns/huaweicloud/tencentcloud 经 lego；cmcccloud vendor certimate）、`k8s.io/client-go`（已在用，fake clientset 单测）、testify。

---

## 背景约束（落地前必读）

- **lego 版本**：spec §2/§6 写"lego v5 / v5.2.2"，但 go-acme/lego 公开 module path 历史上是 `github.com/go-acme/lego/v4`。**Task 1 第一步先确认真实可用的 module path 与版本**，并据此统一全 plan 的 import 路径。下文代码按 `/v4` 书写；若 Task 1 确认存在 `/v5`，全局把 `lego/v4` 替换为 `lego/v5` 即可，API 形状一致。
- **lego 原生 provider 只做 DNS-01 TXT**：lego 的 `challenge.Provider`（`Present`/`CleanUp`）只写 `_acme-challenge` TXT 记录，**不管通配 A 记录**。通配 A 记录（`*.base_domain → ingressIP`）必须用各云厂商 DNS SDK 直接 CRUD。因此 `dnsprovider.Provider` 接口同时暴露「ACME 挑战 provider」与「A 记录 CRUD」两类能力（见 Task 2）。
- **cmcccloud（移动云）**：lego 无原生移动云 provider，需 vendor certimate 的 `pkg/core/certifier/challengers/dns01/cmcccloud` + 其 fork 的 ecloud SDK + go.mod replace（spec §2.1/§6）。本 plan 把 cmcccloud 的 DNS-01 与 A 记录实现都纳入 Task 7，并在该 Task 第一步用 `grep -ri ecloud` 二次确认 lego 确无原生移动云。
- 仓库无 `vendor/` 目录，go.mod 全是公网 module。vendored cmcccloud 代码落在 `internal/integrations/dnsprovider/cmcccloud/internal/ecloud/`（仓库内目录，不走 go module replace 网络拉取），见 Task 7。
- 所有新代码、结构体、字段、方法都要中文注释（AGENTS.md「注释」节）；单测每个子用例要相邻中文注释（AGENTS.md「单元测试」节）；断言用 testify `require`/`assert`。

## File Structure

```
internal/integrations/dnsprovider/
  provider.go        # Provider 接口、ProviderType 枚举、Credentials 结构、错误定义
  provider_test.go   # ProviderType 校验、Credentials JSON 编解码单测
  factory.go         # New(ctx, ProviderType, Credentials, baseDomain) (Provider, error) 工厂
  factory_test.go    # 工厂分发 + 未知 provider 报错单测
  fake.go            # FakeProvider（内存实现，供 acme 包与上层单测复用；非 _test.go 以便跨包 import）
  alidns/alidns.go         # 阿里云：lego alidns 做 DNS-01 + 阿里云 SDK 做 A 记录
  huaweicloud/huaweicloud.go
  tencentcloud/tencentcloud.go
  cmcccloud/cmcccloud.go   # 移动云：vendored certimate challenger + ecloud SDK
  cmcccloud/internal/ecloud/...  # vendored fork（Task 7 迁移清单）

internal/integrations/acme/
  issuer.go          # Issuer：编排 lego 注册 + DNS-01 + Obtain/Renew，产出 Certificate(PEM)
  issuer_test.go     # 用 fake acmeClient + FakeProvider 单测签发/续签/失败路径
  account.go         # ACME 账户私钥的生成与（内存）持有；user 实现 registration.User
  account_test.go

internal/integrations/k8sorch/
  tls_secret.go      # RenderTLSSecret + (a *KubernetesAdapter) ApplyTLSSecret / DeleteTLSSecret
  tls_secret_test.go # fake clientset 验证 create/update/delete 幂等
```

---

### Task 1: 引入 lego 依赖并确认 module path

**Files:**
- Modify: `go.mod`, `go.sum`（由 `go get` 自动写入）

- [ ] **Step 1: 确认 lego 真实 module path 与最新版本**

Run:
```bash
cd /home/user/ywjs-oc-manager
go list -m -versions github.com/go-acme/lego/v4 2>&1 | tail -1
go list -m -versions github.com/go-acme/lego/v5 2>&1 | tail -1
```
Expected: 至少一行返回版本列表。**记录实际可用的最高 path/版本**（记为 `<LEGO>`，例如 `github.com/go-acme/lego/v4`）。若两行都报错（网络/proxy），按本仓库根 `/root/.ccr/README.md` 处理 proxy 后重试；仍不行则在交付说明里写明阻塞原因，**不要**凭记忆瞎填版本号。

- [ ] **Step 2: 拉取依赖**

Run（把 `<LEGO>` 替换为 Step 1 确认的 path）:
```bash
cd /home/user/ywjs-oc-manager
go get <LEGO>@latest
go mod tidy
```
Expected: `go.mod` require 段新增 `<LEGO> vX.Y.Z`，无报错。

- [ ] **Step 3: 验证 lego 关键 API 形状（写代码前必做，避免按错误记忆写）**

Run:
```bash
cd /home/user/ywjs-oc-manager
go doc <LEGO>/lego Client
go doc <LEGO>/lego NewClient
go doc <LEGO>/registration User
go doc <LEGO>/certificate ObtainRequest
go doc <LEGO>/challenge Provider
go doc <LEGO>/providers/dns/alidns NewDNSProvider
```
Expected: 打印各类型/函数签名。**若签名与下文 Task 3/Task 5 代码不一致，以 `go doc` 为准调整代码**（lego 跨大版本签名稳定，但以实测为准）。

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): 引入 go-acme/lego 用于 ACME DNS-01 通配证书签发

为 web-publish 通配证书能力引入 lego 作为 ACME 客户端主流程依赖。
后续 dnsprovider 适配层（alidns/huaweicloud/tencentcloud）复用其原生
DNS-01 provider，移动云另行 vendor certimate 实现。"
```

---

### Task 2: 定义 dnsprovider.Provider 接口与基础类型

**Files:**
- Create: `internal/integrations/dnsprovider/provider.go`
- Test: `internal/integrations/dnsprovider/provider_test.go`

- [ ] **Step 1: Write the failing test**

```go
package dnsprovider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProviderTypeValid 覆盖：四个受支持的 provider 类型判定为合法，未知值判定为非法。
func TestProviderTypeValid(t *testing.T) {
	// 受支持的四家云厂商均应合法
	for _, pt := range []ProviderType{ProviderAlidns, ProviderHuaweicloud, ProviderTencentcloud, ProviderCmcccloud} {
		assert.Truef(t, pt.Valid(), "%s 应为合法 provider", pt)
	}
	// 空值与拼写错误应非法，避免脏数据进入签发流程
	assert.False(t, ProviderType("").Valid())
	assert.False(t, ProviderType("aws").Valid())
}

// TestCredentialsJSONRoundTrip 覆盖：凭证结构 JSON 编解码可逆，
// 这是落库前序列化（再交给 auth.Cipher 加密）的基础。
func TestCredentialsJSONRoundTrip(t *testing.T) {
	in := Credentials{"access_key_id": "AK", "access_key_secret": "SK"}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out Credentials
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/dnsprovider/ -run 'TestProviderType|TestCredentials' -v`
Expected: 编译失败 `undefined: ProviderType / Credentials / ProviderAlidns ...`

- [ ] **Step 3: Write minimal implementation**

```go
// Package dnsprovider 定义统一的 DNS provider 适配层，供 web-publish 能力使用。
//
// 该层有两类能力，对应 spec §6：
//  1. ACME DNS-01 挑战：写/删 _acme-challenge TXT 记录，供 lego 签发回调。
//     由 ChallengeProvider() 返回一个 lego challenge.Provider。
//  2. 通配解析记录：写/删 *.base_domain → ingressIP 的 A 记录。
//     lego 原生 provider 不覆盖此能力，故各实现用云厂商 DNS SDK 直接 CRUD。
//
// 四家实现：alidns / huaweicloud / tencentcloud 复用 lego 原生 DNS-01 provider；
// cmcccloud（移动云）vendor certimate 的实现（lego 无原生移动云）。
package dnsprovider

import (
	"context"

	"github.com/go-acme/lego/v4/challenge"
)

// ProviderType 是受支持的 DNS provider 枚举（与 org_web_publish_config.dns_provider 取值一致）。
type ProviderType string

const (
	// ProviderAlidns 阿里云 DNS（lego 原生 provider: providers/dns/alidns）。
	ProviderAlidns ProviderType = "alidns"
	// ProviderHuaweicloud 华为云 DNS（lego 原生 provider: providers/dns/huaweicloud）。
	ProviderHuaweicloud ProviderType = "huaweicloud"
	// ProviderTencentcloud 腾讯云 DNS（lego 原生 provider: providers/dns/tencentcloud）。
	ProviderTencentcloud ProviderType = "tencentcloud"
	// ProviderCmcccloud 中国移动云 DNS（lego 无原生，vendor certimate 实现）。
	ProviderCmcccloud ProviderType = "cmcccloud"
)

// Valid 报告 pt 是否为受支持的 provider，用于落库前与签发前校验，挡住脏数据。
func (pt ProviderType) Valid() bool {
	switch pt {
	case ProviderAlidns, ProviderHuaweicloud, ProviderTencentcloud, ProviderCmcccloud:
		return true
	default:
		return false
	}
}

// Credentials 是 provider 凭证的中立载体（key→value）。
// 不同 provider 需要的 key 不同（如阿里云需要 access_key_id/access_key_secret，
// 华为云需要 access_key_id/secret_access_key/region 等）；各实现的 New 自行取用并校验。
// 该结构 JSON 序列化后由 auth.Cipher 加密落库（见 Plan 2），本层只负责取值。
type Credentials map[string]string

// Provider 是统一的 DNS provider 适配接口。一个实例绑定一个基础域名与一组凭证。
type Provider interface {
	// ChallengeProvider 返回供 lego 做 DNS-01 挑战的 provider（写/删 _acme-challenge TXT）。
	// acme.Issuer 在签发时把它 SetDNS01Provider 进 lego client。
	ChallengeProvider() challenge.Provider

	// EnsureWildcardA 幂等确保存在一条 *.baseDomain → ip 的 A 记录（已存在且值相同则不动）。
	// baseDomain 不含通配前缀（如 "apps.example.com"），由实现自行拼 "*" 子域。
	EnsureWildcardA(ctx context.Context, baseDomain, ip string) error

	// DeleteWildcardA 删除 *.baseDomain 的通配 A 记录（不存在视为成功，幂等）。
	DeleteWildcardA(ctx context.Context, baseDomain string) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/dnsprovider/ -run 'TestProviderType|TestCredentials' -v`
Expected: PASS（注意此步会编译 lego import，确认 Task 1 的 path 正确）

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/dnsprovider/provider.go internal/integrations/dnsprovider/provider_test.go
git commit -m "feat(dnsprovider): 定义统一 DNS provider 适配接口与基础类型

Provider 接口同时覆盖 ACME DNS-01 挑战（TXT）与通配 A 记录 CRUD 两类能力，
对应 spec §6；ProviderType 枚举与凭证中立载体 Credentials 供四家实现复用。"
```

---

### Task 3: FakeProvider（内存实现，供跨包单测复用）

**Files:**
- Create: `internal/integrations/dnsprovider/fake.go`
- Test: `internal/integrations/dnsprovider/fake_test.go`

> 放在非 `_test.go` 文件，因为 `acme` 包的单测（Task 5）也要 import 它做依赖注入。

- [ ] **Step 1: Write the failing test**

```go
package dnsprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFakeProviderWildcardA 覆盖：EnsureWildcardA 写入后可查到，重复写同值幂等，
// DeleteWildcardA 删除后查不到且对不存在记录不报错。
func TestFakeProviderWildcardA(t *testing.T) {
	p := NewFakeProvider()
	ctx := context.Background()

	// 首次写入通配 A 记录
	require.NoError(t, p.EnsureWildcardA(ctx, "apps.example.com", "1.2.3.4"))
	assert.Equal(t, "1.2.3.4", p.ARecords["*.apps.example.com"])

	// 重复写入相同值应幂等，不报错
	require.NoError(t, p.EnsureWildcardA(ctx, "apps.example.com", "1.2.3.4"))

	// 删除已存在记录
	require.NoError(t, p.DeleteWildcardA(ctx, "apps.example.com"))
	_, ok := p.ARecords["*.apps.example.com"]
	assert.False(t, ok)

	// 删除不存在记录应幂等
	require.NoError(t, p.DeleteWildcardA(ctx, "apps.example.com"))
}

// TestFakeProviderInjectedError 覆盖：注入错误后 EnsureWildcardA 返回该错误，
// 供上层（acme.Issuer / provisioning 状态机）单测失败路径。
func TestFakeProviderInjectedError(t *testing.T) {
	p := NewFakeProvider()
	p.EnsureErr = assert.AnError
	assert.ErrorIs(t, p.EnsureWildcardA(context.Background(), "apps.example.com", "1.2.3.4"), assert.AnError)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/dnsprovider/ -run TestFakeProvider -v`
Expected: 编译失败 `undefined: NewFakeProvider`

- [ ] **Step 3: Write minimal implementation**

```go
package dnsprovider

import (
	"context"

	"github.com/go-acme/lego/v4/challenge"
)

// FakeProvider 是 Provider 的内存实现，仅供单测：A 记录存 map，
// TXT 挑战记录交给内嵌的 fakeChallenge。可注入错误模拟失败路径。
type FakeProvider struct {
	ARecords  map[string]string // key 为完整通配域名 "*.<baseDomain>"，value 为 IP
	TXTed     map[string]string // 记录 lego DNS-01 写入的 fqdn→value，供断言
	EnsureErr error             // 非 nil 时 EnsureWildcardA 直接返回它
	DeleteErr error             // 非 nil 时 DeleteWildcardA 直接返回它
}

// NewFakeProvider 构造一个空的内存 provider。
func NewFakeProvider() *FakeProvider {
	return &FakeProvider{ARecords: map[string]string{}, TXTed: map[string]string{}}
}

// ChallengeProvider 返回写入 FakeProvider.TXTed 的内存挑战 provider。
func (f *FakeProvider) ChallengeProvider() challenge.Provider { return &fakeChallenge{f: f} }

// EnsureWildcardA 写入/覆盖 *.baseDomain 的 A 记录；EnsureErr 非 nil 时返回它。
func (f *FakeProvider) EnsureWildcardA(_ context.Context, baseDomain, ip string) error {
	if f.EnsureErr != nil {
		return f.EnsureErr
	}
	f.ARecords["*."+baseDomain] = ip
	return nil
}

// DeleteWildcardA 删除 *.baseDomain 的 A 记录；DeleteErr 非 nil 时返回它。
func (f *FakeProvider) DeleteWildcardA(_ context.Context, baseDomain string) error {
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	delete(f.ARecords, "*."+baseDomain)
	return nil
}

// fakeChallenge 实现 lego challenge.Provider，把挑战记录写进父 FakeProvider.TXTed。
type fakeChallenge struct{ f *FakeProvider }

// Present 记录 DNS-01 挑战 TXT（lego 在签发时调用）。
func (c *fakeChallenge) Present(domain, token, keyAuth string) error {
	c.f.TXTed[domain] = keyAuth
	return nil
}

// CleanUp 清理挑战 TXT（lego 在签发结束后调用）。
func (c *fakeChallenge) CleanUp(domain, token, keyAuth string) error {
	delete(c.f.TXTed, domain)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/dnsprovider/ -run TestFakeProvider -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/dnsprovider/fake.go internal/integrations/dnsprovider/fake_test.go
git commit -m "test(dnsprovider): 增加内存 FakeProvider 供跨包单测复用

FakeProvider 用 map 模拟 A 记录与 DNS-01 TXT 记录、可注入错误，
供 acme.Issuer 与上层 provisioning 状态机在不触网的情况下单测成功/失败路径。"
```

---

### Task 4: ACME 账户（registration.User 实现 + 私钥生成）

**Files:**
- Create: `internal/integrations/acme/account.go`
- Test: `internal/integrations/acme/account_test.go`

- [ ] **Step 1: Write the failing test**

```go
package acme

import (
	"crypto"
	"testing"

	"github.com/go-acme/lego/v4/registration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAccountImplementsUser 覆盖：account 满足 lego registration.User 接口，
// 且暴露的 email / 私钥与构造入参一致——lego.NewClient 需要一个 User。
func TestNewAccountImplementsUser(t *testing.T) {
	acc, err := newAccount("ops@example.com")
	require.NoError(t, err)

	// 编译期+运行期确认实现 registration.User 接口
	var _ registration.User = acc
	assert.Equal(t, "ops@example.com", acc.GetEmail())
	assert.NotNil(t, acc.GetPrivateKey())

	// 同一账户的私钥应稳定（多次取用是同一把），保证 lego 注册一致
	var k crypto.PrivateKey = acc.GetPrivateKey()
	assert.Equal(t, k, acc.GetPrivateKey())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/acme/ -run TestNewAccount -v`
Expected: 编译失败 `undefined: newAccount`

- [ ] **Step 3: Write minimal implementation**

```go
// Package acme 用 go-acme/lego 编排 ACME DNS-01 通配证书的签发与续签。
//
// 设计要点（spec §6）：
//   - 每企业一张 *.base_domain 通配证书，manager 全权托管、自动续期。
//   - 通配证书必须走 DNS-01 挑战，挑战 provider 由 dnsprovider 适配层提供。
//   - 本包只产出 PEM（证书链 + 私钥），写 k8s TLS Secret 的事交给 k8sorch。
//
// 账户私钥的持久化策略：本期账户私钥进程内生成、不落库（每次进程/签发用新账户也能成功
// 注册并签发，ACME 账户无状态副作用）。若未来要稳定账户身份，再扩展为从 Secret 读取。
package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"

	"github.com/go-acme/lego/v4/registration"
)

// account 实现 lego 的 registration.User：携带邮箱、ACME 注册资源与账户私钥。
type account struct {
	email        string
	registration *registration.Resource
	privateKey   crypto.PrivateKey
}

// newAccount 生成一个带新 P-256 私钥的 ACME 账户（尚未向 CA 注册）。
func newAccount(email string) (*account, error) {
	// ACME 账户私钥用 P-256 ECDSA，足够且签发快；与证书私钥无关。
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &account{email: email, privateKey: key}, nil
}

// GetEmail 返回账户邮箱（registration.User 接口）。
func (a *account) GetEmail() string { return a.email }

// GetRegistration 返回 ACME 注册资源（registration.User 接口）；注册前为 nil。
func (a *account) GetRegistration() *registration.Resource { return a.registration }

// GetPrivateKey 返回账户私钥（registration.User 接口）。
func (a *account) GetPrivateKey() crypto.PrivateKey { return a.privateKey }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/acme/ -run TestNewAccount -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/acme/account.go internal/integrations/acme/account_test.go
git commit -m "feat(acme): 增加 ACME 账户类型实现 lego registration.User

account 携带邮箱、P-256 账户私钥与注册资源，供 lego.NewClient 注册使用；
账户私钥进程内生成、本期不落库。"
```

---

### Task 5: Issuer —— 编排签发/续签（核心逻辑，全量单测）

**Files:**
- Create: `internal/integrations/acme/issuer.go`
- Test: `internal/integrations/acme/issuer_test.go`

> 关键设计：把"真正调 lego 网络签发"的动作抽成一个 `obtainer` 接口（单方法），生产实现用 lego，单测用 fake。Issuer 自身只负责编排：确保 A 记录 → 调 obtainer 拿 PEM → 计算 NotAfter。这样核心编排逻辑可不触网单测。

- [ ] **Step 1: Write the failing test**

```go
package acme

import (
	"context"
	"testing"

	"oc-manager/internal/integrations/dnsprovider"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeObtainer 是 obtainer 的测试替身：记录被请求的域名，返回预置 PEM 或预置错误。
type fakeObtainer struct {
	gotDomains []string
	ret        Certificate
	err        error
}

func (f *fakeObtainer) Obtain(_ context.Context, domains []string) (Certificate, error) {
	f.gotDomains = domains
	if f.err != nil {
		return Certificate{}, f.err
	}
	return f.ret, nil
}

// TestIssuerIssueHappyPath 覆盖：正常签发会先写通配 A 记录、再用通配域名请求证书、
// 返回 obtainer 给出的 PEM。验证「先解析后签发」的编排顺序与产物透传。
func TestIssuerIssueHappyPath(t *testing.T) {
	fp := dnsprovider.NewFakeProvider()
	ob := &fakeObtainer{ret: Certificate{CertPEM: []byte("CERT"), KeyPEM: []byte("KEY")}}
	iss := newIssuerWithObtainer(fp, ob)

	cert, err := iss.Issue(context.Background(), "apps.example.com", "1.2.3.4")
	require.NoError(t, err)

	// 通配 A 记录应已写入
	assert.Equal(t, "1.2.3.4", fp.ARecords["*.apps.example.com"])
	// 证书请求的域名应是通配域名
	assert.Equal(t, []string{"*.apps.example.com"}, ob.gotDomains)
	// PEM 应原样透传
	assert.Equal(t, []byte("CERT"), cert.CertPEM)
	assert.Equal(t, []byte("KEY"), cert.KeyPEM)
}

// TestIssuerIssueDNSFailsSkipsObtain 覆盖：写 A 记录失败时直接返回错误，
// 不应继续请求证书（避免在 DNS 未就绪时浪费 ACME 配额）。
func TestIssuerIssueDNSFailsSkipsObtain(t *testing.T) {
	fp := dnsprovider.NewFakeProvider()
	fp.EnsureErr = assert.AnError
	ob := &fakeObtainer{}
	iss := newIssuerWithObtainer(fp, ob)

	_, err := iss.Issue(context.Background(), "apps.example.com", "1.2.3.4")
	require.Error(t, err)
	assert.Nil(t, ob.gotDomains, "DNS 失败时不应请求证书")
}

// TestIssuerIssueObtainFails 覆盖：证书签发失败时把错误透传给调用方（provisioning 据此置 failed）。
func TestIssuerIssueObtainFails(t *testing.T) {
	fp := dnsprovider.NewFakeProvider()
	ob := &fakeObtainer{err: assert.AnError}
	iss := newIssuerWithObtainer(fp, ob)
	_, err := iss.Issue(context.Background(), "apps.example.com", "1.2.3.4")
	assert.ErrorIs(t, err, assert.AnError)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/acme/ -run TestIssuer -v`
Expected: 编译失败 `undefined: Certificate / newIssuerWithObtainer`

- [ ] **Step 3: Write minimal implementation**

```go
package acme

import (
	"context"
	"fmt"

	"oc-manager/internal/integrations/dnsprovider"
)

// Certificate 是一次签发的产物：PEM 编码的证书链与私钥，外加从证书解析出的到期时间。
type Certificate struct {
	CertPEM  []byte // tls.crt：证书链 PEM
	KeyPEM   []byte // tls.key：私钥 PEM
	NotAfter int64  // 证书到期 Unix 秒（供 cert_not_after 落库与续期巡检）；fake/未解析时为 0
}

// obtainer 抽象「真正向 ACME CA 请求一张证书」的动作，便于单测注入 fake。
// 生产实现见 legoObtainer（Step 6），用 lego 完成 DNS-01 挑战与 Obtain。
type obtainer interface {
	// Obtain 为 domains 请求一张证书并返回 PEM；domains[0] 通常是通配域 "*.base"。
	Obtain(ctx context.Context, domains []string) (Certificate, error)
}

// Issuer 编排通配证书签发：先确保通配 A 记录解析，再请求证书。
type Issuer struct {
	provider dnsprovider.Provider
	obtainer obtainer
}

// newIssuerWithObtainer 用显式 obtainer 构造 Issuer（供单测注入 fake）。
func newIssuerWithObtainer(p dnsprovider.Provider, ob obtainer) *Issuer {
	return &Issuer{provider: p, obtainer: ob}
}

// Issue 签发 *.baseDomain 通配证书：
//  1. 幂等写通配 A 记录 *.baseDomain → ip（DNS 未就绪时直接失败，不浪费 ACME 配额）；
//  2. 用通配域名请求证书并返回 PEM。
func (i *Issuer) Issue(ctx context.Context, baseDomain, ip string) (Certificate, error) {
	if err := i.provider.EnsureWildcardA(ctx, baseDomain, ip); err != nil {
		return Certificate{}, fmt.Errorf("acme: 写通配 A 记录失败: %w", err)
	}
	wildcard := "*." + baseDomain
	cert, err := i.obtainer.Obtain(ctx, []string{wildcard})
	if err != nil {
		return Certificate{}, fmt.Errorf("acme: 签发 %s 失败: %w", wildcard, err)
	}
	return cert, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/acme/ -run TestIssuer -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/acme/issuer.go internal/integrations/acme/issuer_test.go
git commit -m "feat(acme): 增加 Issuer 编排通配证书签发（先解析后签发）

Issuer 先幂等写通配 A 记录再请求证书，DNS 失败时跳过签发避免浪费 ACME 配额；
真正的 ACME 调用抽成 obtainer 接口，核心编排逻辑用 fake 全量单测。"
```

- [ ] **Step 6: 实现生产用 legoObtainer（lego 真实签发，含 NotAfter 解析）**

新增到 `issuer.go`（无独立单测——真实 ACME 触网，留待 Plan 2 集成/本地联调验证；本步只要 `go build` 通过且 `go doc` 已确认 API）：

```go
import (
	"crypto/x509"
	"encoding/pem"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// IssuerConfig 是构造生产 Issuer 的参数。
type IssuerConfig struct {
	Email      string // ACME 账户邮箱
	CADirURL   string // ACME 目录 URL（如 Let's Encrypt 生产/staging；本地联调建议先用 staging）
}

// legoObtainer 用 lego 完成 DNS-01 挑战与证书签发。
type legoObtainer struct {
	provider dnsprovider.Provider
	cfg      IssuerConfig
}

// NewIssuer 构造生产用 Issuer：DNS-01 挑战 provider 来自适配层，CA/邮箱来自 cfg。
func NewIssuer(p dnsprovider.Provider, cfg IssuerConfig) *Issuer {
	return newIssuerWithObtainer(p, &legoObtainer{provider: p, cfg: cfg})
}

// Obtain 用 lego 注册账户、设置 DNS-01 provider、请求证书，并解析 NotAfter。
// 注意：以 go doc 实测的 lego API 为准微调下列调用。
func (o *legoObtainer) Obtain(_ context.Context, domains []string) (Certificate, error) {
	acc, err := newAccount(o.cfg.Email)
	if err != nil {
		return Certificate{}, err
	}
	legoCfg := lego.NewConfig(acc)
	if o.cfg.CADirURL != "" {
		legoCfg.CADirURL = o.cfg.CADirURL
	}
	client, err := lego.NewClient(legoCfg)
	if err != nil {
		return Certificate{}, err
	}
	if err := client.Challenge.SetDNS01Provider(o.provider.ChallengeProvider()); err != nil {
		return Certificate{}, err
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return Certificate{}, err
	}
	acc.registration = reg
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: domains, Bundle: true})
	if err != nil {
		return Certificate{}, err
	}
	cert := Certificate{CertPEM: res.Certificate, KeyPEM: res.PrivateKey}
	// 解析叶子证书 NotAfter 供续期巡检；解析失败不阻断签发（NotAfter 留 0，由巡检兜底）。
	if block, _ := pem.Decode(res.Certificate); block != nil {
		if leaf, perr := x509.ParseCertificate(block.Bytes); perr == nil {
			cert.NotAfter = leaf.NotAfter.Unix()
		}
	}
	_ = time.Now // time import 占位：若上面未用到 time 可删除该 import
	return cert, nil
}
```

- [ ] **Step 7: Build verify + commit**

Run: `cd /home/user/ywjs-oc-manager && go build ./internal/integrations/acme/ && go vet ./internal/integrations/acme/`
Expected: 无报错（若 lego API 签名不符，按 `go doc` 调整后再过）

```bash
git add internal/integrations/acme/issuer.go
git commit -m "feat(acme): 增加 lego 生产签发实现 legoObtainer

用 lego 注册 ACME 账户、设置 DNS-01 provider、请求通配证书并解析 NotAfter；
真实 ACME 触网路径留待 Plan 2 集成与本地 staging 联调验证。"
```

---

### Task 6: k8sorch 扩展 —— 把证书写成 kubernetes.io/tls Secret

**Files:**
- Create: `internal/integrations/k8sorch/tls_secret.go`
- Test: `internal/integrations/k8sorch/tls_secret_test.go`

> 复用既有 `applySecret` 的 get-or-create-then-update 幂等模式（见 `adapter.go:73`），但 Secret 类型为 `corev1.SecretTypeTLS`、用 `Data`（[]byte）而非 `StringData`。

- [ ] **Step 1: Write the failing test**

```go
package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestRenderTLSSecret 覆盖：渲染出的 Secret 类型为 TLS、含 tls.crt/tls.key 两个 Data key、
// 名称与命名空间正确。
func TestRenderTLSSecret(t *testing.T) {
	s := RenderTLSSecret("wildcard-apps-example-com", "ocm", []byte("CERT"), []byte("KEY"))
	assert.Equal(t, corev1.SecretTypeTLS, s.Type)
	assert.Equal(t, []byte("CERT"), s.Data[corev1.TLSCertKey])
	assert.Equal(t, []byte("KEY"), s.Data[corev1.TLSPrivateKeyKey])
	assert.Equal(t, "wildcard-apps-example-com", s.Name)
	assert.Equal(t, "ocm", s.Namespace)
}

// TestApplyTLSSecretCreateThenUpdate 覆盖：首次 Apply 创建 Secret，
// 第二次 Apply 同名 Secret 走更新分支（不报 AlreadyExists），且内容被刷新。
func TestApplyTLSSecretCreateThenUpdate(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "ocm")
	ctx := context.Background()

	// 首次创建
	require.NoError(t, a.ApplyTLSSecret(ctx, "wc", []byte("C1"), []byte("K1")))
	got, err := client.CoreV1().Secrets("ocm").Get(ctx, "wc", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("C1"), got.Data[corev1.TLSCertKey])

	// 再次 Apply 同名：应更新而非报错，内容刷新为新证书（续期场景）
	require.NoError(t, a.ApplyTLSSecret(ctx, "wc", []byte("C2"), []byte("K2")))
	got, err = client.CoreV1().Secrets("ocm").Get(ctx, "wc", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("C2"), got.Data[corev1.TLSCertKey])
}

// TestDeleteTLSSecretIdempotent 覆盖：删除不存在的 Secret 不报错（幂等回收）。
func TestDeleteTLSSecretIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "ocm")
	require.NoError(t, a.DeleteTLSSecret(context.Background(), "missing"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/k8sorch/ -run 'TLSSecret' -v`
Expected: 编译失败 `undefined: RenderTLSSecret / ApplyTLSSecret / DeleteTLSSecret`

- [ ] **Step 3: Write minimal implementation**

```go
package k8sorch

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RenderTLSSecret 渲染一个 kubernetes.io/tls 类型的 Secret，供通配 Ingress 引用。
// name 由调用方按企业基础域名确定性生成（如 wildcard-<sanitized base domain>），
// namespace 跟随通配 Ingress 所在命名空间。
func RenderTLSSecret(name, namespace string, certPEM, keyPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app.kubernetes.io/part-of": "oc-manager", "app.kubernetes.io/component": "web-publish-cert"},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM, // tls.crt
			corev1.TLSPrivateKeyKey: keyPEM,  // tls.key
		},
	}
}

// ApplyTLSSecret 幂等 apply 通配证书 Secret（首签创建、续期更新），
// 复用与 applySecret 一致的 get-or-create-then-update 模式。
func (a *KubernetesAdapter) ApplyTLSSecret(ctx context.Context, name string, certPEM, keyPEM []byte) error {
	s := RenderTLSSecret(name, a.namespace, certPEM, keyPEM)
	api := a.client.CoreV1().Secrets(a.namespace)
	existing, err := api.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
		return wrapK8s("创建 TLS Secret", cerr)
	}
	if err != nil {
		return wrapK8s("查询 TLS Secret", err)
	}
	s.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
	return wrapK8s("更新 TLS Secret", uerr)
}

// DeleteTLSSecret 删除通配证书 Secret（NotFound 视为成功，幂等）。
func (a *KubernetesAdapter) DeleteTLSSecret(ctx context.Context, name string) error {
	err := a.client.CoreV1().Secrets(a.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 TLS Secret", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/k8sorch/ -run 'TLSSecret' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/k8sorch/tls_secret.go internal/integrations/k8sorch/tls_secret_test.go
git commit -m "feat(k8sorch): 增加通配证书 TLS Secret 的渲染与幂等 apply/delete

RenderTLSSecret 产出 kubernetes.io/tls 类型 Secret（tls.crt/tls.key），
ApplyTLSSecret 复用 get-or-create-then-update 幂等模式支持首签创建与续期更新，
供 Plan 2 provisioning 把 acme.Certificate 写入集群、给通配 Ingress 引用。"
```

---

### Task 7: 四家 provider 实现 + 工厂（含 cmcccloud vendoring）

> 本 Task 的 alidns/huaweicloud/tencentcloud 的 **DNS-01 挑战**直接复用 lego 原生 provider；**通配 A 记录 CRUD** 需各云厂商 DNS SDK，落地时按 `go doc` 确认 SDK API 后实现。cmcccloud 两类能力都要 vendor。鉴于真实 SDK 调用无法离线单测，本 Task 的单测聚焦**工厂分发**与**接口装配**，真实 SDK 路径留 Plan 2 本地联调验证（spec §10 已列为计划阶段细化点）。

- [ ] **Step 1: 二次确认 lego 是否原生支持移动云（spec §6 要求）**

Run:
```bash
cd /home/user/ywjs-oc-manager
ls $(go env GOMODCACHE)/$(go list -m -f '{{.Path}}@{{.Version}}' <LEGO>)/providers/dns/ | grep -i 'ecloud\|cmcc\|chinamobile' || echo "lego 无原生移动云，需 vendor certimate"
```
Expected: 大概率打印 "lego 无原生移动云，需 vendor certimate"，确认 cmcccloud 必须 vendor。

- [ ] **Step 2: Write the failing test（工厂分发）**

`internal/integrations/dnsprovider/factory_test.go`:
```go
package dnsprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewUnknownProvider 覆盖：未知 provider 类型返回错误，挡住脏配置。
func TestNewUnknownProvider(t *testing.T) {
	_, err := New(context.Background(), ProviderType("aws"), Credentials{}, "apps.example.com")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedProvider)
}

// TestNewAlidnsMissingCreds 覆盖：阿里云缺少必填凭证 key 时报错，
// 保证签发前就发现配置问题而非签发时才失败。
func TestNewAlidnsMissingCreds(t *testing.T) {
	_, err := New(context.Background(), ProviderAlidns, Credentials{}, "apps.example.com")
	require.Error(t, err)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/dnsprovider/ -run TestNew -v`
Expected: 编译失败 `undefined: New / ErrUnsupportedProvider`

- [ ] **Step 4: Write factory + alidns reference impl**

`internal/integrations/dnsprovider/factory.go`:
```go
package dnsprovider

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnsupportedProvider 表示传入了不受支持的 provider 类型。
var ErrUnsupportedProvider = errors.New("dnsprovider: 不支持的 provider 类型")

// New 按 ProviderType 构造对应 Provider 实例：校验凭证、装配 lego DNS-01 provider
// 与 A 记录客户端。baseDomain 仅用于校验/日志，实际域名在每次调用时再传。
func New(ctx context.Context, pt ProviderType, creds Credentials, baseDomain string) (Provider, error) {
	switch pt {
	case ProviderAlidns:
		return newAlidns(creds)
	case ProviderHuaweicloud:
		return newHuaweicloud(creds)
	case ProviderTencentcloud:
		return newTencentcloud(creds)
	case ProviderCmcccloud:
		return newCmcccloud(creds)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProvider, pt)
	}
}
```

`internal/integrations/dnsprovider/alidns/alidns.go`（参考实现；huaweicloud/tencentcloud 同构）:
```go
// Package alidns 适配阿里云 DNS：DNS-01 挑战复用 lego 原生 provider，
// 通配 A 记录用阿里云 alidns SDK 直接 CRUD。
package alidns

// 实现说明（落地时按 go doc 确认 SDK API 后补全）：
//  1. DNS-01：import lego "github.com/go-acme/lego/v4/providers/dns/alidns"，
//     用 alidns.NewDNSProviderConfig(cfg) 构造，cfg.APIKey/SecretKey 取自 Credentials。
//  2. A 记录：import 阿里云 alidns SDK（github.com/aliyun/alibaba-cloud-sdk-go/services/alidns
//     或 alidns20150109），用 AddDomainRecord / DescribeDomainRecords / DeleteDomainRecord
//     管理 RR="*" 的 A 记录。
//  3. 把上述两者组装成实现 dnsprovider.Provider 的结构体。
```

> 为保持 plan 可执行且不引入未经验证的 SDK 调用，alidns/huaweicloud/tencentcloud/cmcccloud 的 `newXxx` 在 `factory.go` 中先以「校验必填凭证 key → 暂返回 `fmt.Errorf("alidns provider 待实现：A 记录 SDK 装配")`」占位实现，使工厂单测（Step 2）通过；真实 SDK 装配在本 Task 后续步骤逐个 provider 补全并本地联调。**这是 plan 显式标注的边界，不是占位符遗漏。**

- [ ] **Step 5: Run factory test + commit**

Run: `cd /home/user/ywjs-oc-manager && go test ./internal/integrations/dnsprovider/ -v`
Expected: PASS（含工厂分发与凭证校验）

```bash
git add internal/integrations/dnsprovider/factory.go internal/integrations/dnsprovider/factory_test.go internal/integrations/dnsprovider/alidns/
git commit -m "feat(dnsprovider): 增加 provider 工厂与凭证校验骨架

New 按 ProviderType 分发到四家实现并在签发前校验必填凭证；
各家 DNS-01 复用 lego 原生 provider、通配 A 记录用云厂商 SDK，
真实 SDK 装配按 provider 逐个补全并本地联调（spec §10）。"
```

- [ ] **Step 6: cmcccloud vendoring（迁移清单）**

按 spec §2.1/§6/§10 把 certimate 的移动云实现搬入仓库（go module replace 对下游 import 方不生效，故必须把源码搬入仓库目录）：

1. 确定 certimate 版本与许可证（MIT），在 `internal/integrations/dnsprovider/cmcccloud/internal/ecloud/` 顶部保留原始 LICENSE/版权头。
2. 复制 `certimate-go/certimate` 的 `pkg/core/certifier/challengers/dns01/cmcccloud` 到 `internal/integrations/dnsprovider/cmcccloud/`，改包名/import 路径为本仓库 `oc-manager/internal/integrations/dnsprovider/cmcccloud/...`。
3. 复制其 fork 的 ecloud SDK（certimate go.mod 里 `replace` 指向的那个本地/fork 版本）到 `internal/integrations/dnsprovider/cmcccloud/internal/ecloud/`，同样改 import 路径，去掉对 replace 的依赖。
4. `go mod tidy`，确认无残留对 certimate / 原 ecloud module 的 require。
5. 在 `cmcccloud.go` 里把 vendored challenger 装配成实现 `dnsprovider.Provider` 的结构体（DNS-01 用 vendored challenger，A 记录用 vendored ecloud SDK 的记录 CRUD）。
6. 用 `go build ./...` 确认整体编译通过。

> 该步无法 TDD（外部 SDK、触网），交付时在说明里列出实际迁移的文件清单与 import 改写范围；真实签发用本地/staging 联调验证（Plan 2）。

- [ ] **Step 7: Build verify + commit**

Run: `cd /home/user/ywjs-oc-manager && go build ./... && go mod tidy && git diff --stat go.mod go.sum`
Expected: 编译通过；go.mod 无对 certimate 上游/原 ecloud 的 require（已 vendor）

```bash
git add internal/integrations/dnsprovider/cmcccloud/ go.mod go.sum
git commit -m "feat(dnsprovider): vendor certimate 移动云 DNS-01 实现与 ecloud SDK

lego 无原生移动云 provider，按 spec §2.1 搬入 certimate 的 cmcccloud challenger
及其 fork 的 ecloud SDK（go module replace 对下游不生效，必须搬源码入仓），
改写 import 路径并保留原始 MIT 许可证头。"
```

---

## Self-Review

**1. Spec coverage（对应 §6 / §11.1）：**
- DNS-01 写/删 TXT → `Provider.ChallengeProvider()` + 各实现（Task 2/7）✓
- 写/删通配 A 记录 → `Provider.EnsureWildcardA/DeleteWildcardA`（Task 2/7）✓
- 四家 provider（ali/huawei/tencent 用 lego 原生 + cmcccloud vendor）→ Task 7 ✓
- lego 自建签发主流程 → `acme.Issuer` + `legoObtainer`（Task 5）✓
- 续期：本 plan 产出可重复调用的 `Issue`（续期=再签发后 `ApplyTLSSecret` 覆盖同名 Secret）；定时巡检触发在 Plan 5（reaper/续期 job）。`Certificate.NotAfter` 已为巡检预留 ✓
- 证书写 `kubernetes.io/tls` Secret → `k8sorch.ApplyTLSSecret`（Task 6）✓

**2. Placeholder scan：** alidns/cmcccloud 的真实 SDK 装配被显式标注为「逐个补全 + 本地联调」边界（外部 SDK 无法离线 TDD），并非遗漏的 TODO——已在 Task 7 文字与本 plan 背景约束中说明原因与验证方式。其余步骤均含完整可运行代码。

**3. Type consistency：** `Certificate{CertPEM,KeyPEM,NotAfter}` 在 Task 5 定义、Task 6 的 `ApplyTLSSecret(certPEM,keyPEM)` 消费一致；`dnsprovider.Provider` 接口（Task 2）被 `FakeProvider`（Task 3）、`acme.Issuer`（Task 5）、工厂（Task 7）一致实现/消费；`<LEGO>` import 路径由 Task 1 统一确定后全 plan 替换。

**遗留给 Plan 2 的接口契约（供后续 plan 对齐）：**
- `acme.NewIssuer(provider dnsprovider.Provider, cfg acme.IssuerConfig) *Issuer`，`Issue(ctx, baseDomain, ip) (Certificate, error)`
- `dnsprovider.New(ctx, ProviderType, Credentials, baseDomain) (Provider, error)`
- `(*k8sorch.KubernetesAdapter).ApplyTLSSecret(ctx, name, certPEM, keyPEM) error` / `DeleteTLSSecret(ctx, name) error`
- TLS Secret 命名约定：`wildcard-<sanitized base domain>`（Plan 2 落 `cert_secret_name`）
