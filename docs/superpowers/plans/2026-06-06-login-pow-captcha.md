# 登录页工作量证明（PoW）验证码 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 manager 登录接口加入常驻、无感、可自托管的 Altcha 工作量证明验证码（纯 PoW + Redis 一次性消费防重放），主防公网多租户场景的大规模撞库/扫号。

**Architecture:** 后端在现有 Gin 进程内挂一个公开出题路由 `GET /api/v1/auth/altcha-challenge` 并在 `AuthService.Login` 前置校验 PoW；altcha 第三方库完全隔离在新包 `internal/auth/pow`，一次性消费用现有 `*goredis.Client` 做 `SETNX`；前端登录页嵌入 `<altcha-widget>` web component（Vite 打包进自身产物，不连任何第三方）。Redis 故障走 fail-open。验证码由 `captcha.enabled` 配置项总控，关闭时出题接口返回 204、登录跳过校验。

**Tech Stack:** Go 1.x + Gin + `github.com/altcha-org/altcha-lib-go` + `github.com/redis/go-redis/v9`；Vue 3 + TypeScript + Vite + `altcha` npm 包；测试 `stretchr/testify`（后端）、`vitest` + `@vue/test-utils`（前端）。

**已批准设计：** `docs/superpowers/specs/2026-06-06-login-pow-captcha-design.md`

---

## 关于 altcha 第三方 API 的隔离约定（实现前必读）

altcha-lib-go 采用经典 SHA-256 Hashcash 模型：服务端 `CreateChallenge` 下发 `{algorithm,challenge,salt,signature,maxNumber}`（HMAC 签名、`Expires` 编码进 salt 参数），客户端暴力找 `number` 使 `hex(sha256(salt+number))==challenge`，服务端 `VerifySolution(payloadBase64, hmacKey, checkExpires)` 验签+重算+查过期。

**所有 altcha 符号只出现在 `internal/auth/pow/pow.go` 一个文件里。** 不同库版本的选项结构体/函数名可能略有差异（例如 `ChallengeOptions` vs `CreateChallengeOptions`、`HMACKey` vs `HMACSignatureSecret`）。Task 1 的单测用「自己暴力解 + 走真实 altcha 出题/验签」round-trip，`go test ./internal/auth/pow` 会立即暴露任何符号漂移——只在这一个文件按编译/测试反馈调整这 ~4 处调用，其它任务全部与 altcha 解耦。

---

## File Structure

**新增文件：**
- `internal/auth/pow/pow.go` — altcha 封装：`Verifier`（出题 + 验解 + 取 signature）。唯一接触 altcha 的文件。
- `internal/auth/pow/pow_test.go` — round-trip 单测（出题→自解→验签）。
- `internal/auth/pow/replay.go` — `RedisReplayGuard`：`SETNX` 一次性消费。
- `internal/auth/pow/replay_integration_test.go` — 真实 Redis 集成测试（`//go:build integration`）。
- `internal/service/captcha.go` — `CaptchaService`（编排 pow + replay + fail-open）、`CaptchaVerifier` / `ReplayGuard` 接口、`ErrCaptcha*` 之外的装配。
- `internal/service/captcha_test.go` — CaptchaService 单测（required/invalid/replayed/fail-open）。
- `web/src/pages/login/LoginPage.spec.ts` — 登录页验证码交互单测。

**修改文件：**
- `internal/config/config.go` — 加 `CaptchaConfig` + `Config.Captcha`。
- `internal/config/loader.go` — `applyDefaults` 填默认、`Validate` 启用时必填 `hmac_secret`。
- `internal/config/loader_test.go` — 校验用例。
- `internal/service/errors.go` — `ErrCaptchaRequired/Invalid/Replayed`。
- `internal/service/auth_service.go` — `LoginInput.Captcha`、`AuthService.captcha`、`NewAuthService` 签名、`Login` 前置校验。
- `internal/service/auth_service_test.go` — 更新 `newTestAuthService` + 新增 Login 验证码用例。
- `internal/api/handlers/dto.go` — `LoginRequest.Captcha`。
- `internal/api/handlers/auth.go` — `Login` 传 captcha、`writeAuthError` 映射、`AltchaChallenge` handler、`NewAuthHandler` 签名、路由注册。
- `internal/api/handlers/auth_test.go` — handler 用例（若不存在则新建）。
- `internal/api/router.go` — `Dependencies.Captcha` + 装配。
- `cmd/server/main.go` — 验证码装配（typed-nil 处理）。
- `openapi/openapi.yaml` + `web/src/api/generated.ts` — 生成产物（`make openapi-gen` + `make web-types-gen`）。
- `web/src/main.ts` — `import 'altcha'`。
- `web/vite.config.ts` — `isCustomElement`。
- `web/src/stores/auth.ts` — `login()` 加 `captcha` 参数。
- `web/src/pages/login/LoginPage.vue` — 嵌入 widget + 门槛 + 失败重置 + 探测 204。
- `web/package.json` — `altcha` 依赖。
- `deploy/k8s/local/secret.yaml` — 内嵌 `captcha:` 段（本地开启）。
- `deploy/k8s/prod/secret.example.yaml` — 内嵌 `captcha:` 段（`__FILL_` 占位）。

---

## Task 1: pow 包 — altcha 封装与 round-trip 单测

**Files:**
- Create: `internal/auth/pow/pow.go`
- Test: `internal/auth/pow/pow_test.go`

- [ ] **Step 1: 加依赖**

Run:
```bash
cd /home/hujing/dir/software/ywjs/oc-manager
go get github.com/altcha-org/altcha-lib-go
```
Expected: `go.mod` / `go.sum` 出现 altcha-lib-go。

- [ ] **Step 2: 写 round-trip 失败测试**

Create `internal/auth/pow/pow_test.go`:
```go
package pow

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// solveChallenge 用 altcha 文档算法暴力找 number：hex(sha256(salt+number)) == challenge。
// 仅测试使用，保证不依赖库的 solver 符号。
func solveChallenge(t *testing.T, salt, challenge string, maxNumber int64) int64 {
	t.Helper()
	for n := int64(0); n <= maxNumber; n++ {
		sum := sha256.Sum256([]byte(salt + strconv.FormatInt(n, 10)))
		if hex.EncodeToString(sum[:]) == challenge {
			return n
		}
	}
	t.Fatalf("在 maxNumber=%d 内未找到解，疑似 altcha 摘要算法与测试不一致", maxNumber)
	return 0
}

// buildPayload 按 widget 提交格式拼 base64(JSON) payload。
func buildPayload(algorithm, challenge, salt, signature string, number int64) string {
	b, _ := json.Marshal(map[string]any{
		"algorithm": algorithm,
		"challenge": challenge,
		"number":    number,
		"salt":      salt,
		"signature": signature,
	})
	return base64.StdEncoding.EncodeToString(b)
}

// 正常路径：有效解通过校验，并返回该题 signature 作为一次性 key。
func TestVerifierAcceptsValidSolution(t *testing.T) {
	v := NewVerifier("test-secret", 5000, time.Minute)
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	n := solveChallenge(t, ch.Salt, ch.Challenge, ch.MaxNumber)
	payload := buildPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n)

	sig, err := v.Verify(payload)
	require.NoError(t, err)
	assert.Equal(t, ch.Signature, sig) // 返回的 signature 即出题时的 signature
}

// 异常路径：篡改 number 后重算不成立，校验失败。
func TestVerifierRejectsTamperedSolution(t *testing.T) {
	v := NewVerifier("test-secret", 5000, time.Minute)
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	n := solveChallenge(t, ch.Salt, ch.Challenge, ch.MaxNumber)
	payload := buildPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n+1) // 改坏 number

	_, err = v.Verify(payload)
	require.ErrorIs(t, err, ErrInvalidSolution)
}

// 边界路径：挑战已过期（ttl 取负把 Expires 推到过去）→ 校验失败。
func TestVerifierRejectsExpiredChallenge(t *testing.T) {
	v := NewVerifier("test-secret", 5000, -time.Minute)
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	n := solveChallenge(t, ch.Salt, ch.Challenge, ch.MaxNumber)
	payload := buildPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n)

	_, err = v.Verify(payload)
	require.ErrorIs(t, err, ErrInvalidSolution)
}

// 异常路径：非法 base64 直接判失败，不 panic。
func TestVerifierRejectsGarbage(t *testing.T) {
	v := NewVerifier("test-secret", 5000, time.Minute)
	_, err := v.Verify("!!!not-base64!!!")
	require.ErrorIs(t, err, ErrInvalidSolution)
}
```

- [ ] **Step 3: 跑测试，确认编译失败（未实现）**

Run: `go test ./internal/auth/pow/`
Expected: 编译失败，`undefined: NewVerifier` 等。

- [ ] **Step 4: 实现 pow.go**

Create `internal/auth/pow/pow.go`:
```go
// Package pow 封装 Altcha 工作量证明的出题与验解，是全仓库唯一接触
// altcha-lib-go 的位置。对外暴露与 altcha 解耦的稳定契约：出题返回可直接
// JSON 序列化给 widget 的挑战；验解返回该题 signature（供上层做一次性消费 key）。
package pow

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go"
)

// ErrInvalidSolution 表示 payload 验签失败、解不成立或已过期，统一由上层映射为 400。
var ErrInvalidSolution = errors.New("altcha 解校验失败")

// Verifier 持有出题/验签所需的 HMAC 密钥、难度上限与有效期。
type Verifier struct {
	hmacKey   string        // HMAC 签名密钥（captcha.hmac_secret）
	maxNumber int64         // 难度上限（captcha.difficulty），常驻取低值≈几百 ms
	ttl       time.Duration // 挑战有效期（captcha.ttl），也是一次性 key 的最长 TTL
}

// NewVerifier 构造 Verifier。
func NewVerifier(hmacKey string, maxNumber int64, ttl time.Duration) *Verifier {
	return &Verifier{hmacKey: hmacKey, maxNumber: maxNumber, ttl: ttl}
}

// TTL 返回挑战有效期，供一次性消费设置 Redis key 过期时间。
func (v *Verifier) TTL() time.Duration { return v.ttl }

// CreateChallenge 生成一道带 HMAC 签名与过期时间的挑战；服务端无需保存任何状态。
// 返回的 altcha.Challenge 带 json tag（algorithm/challenge/maxNumber/salt/signature），
// handler 直接 c.JSON 即为 widget 需要的形态。
func (v *Verifier) CreateChallenge() (altcha.Challenge, error) {
	expires := time.Now().Add(v.ttl)
	return altcha.CreateChallenge(altcha.ChallengeOptions{
		HMACKey:   v.hmacKey,
		MaxNumber: v.maxNumber,
		Expires:   &expires,
	})
}

// Verify 校验 base64 payload：验 HMAC 签名 + 重算解 + 未过期（checkExpires=true）。
// 成功返回该题 signature（一次性消费 key 的来源）；任何失败返回 ErrInvalidSolution。
// signature 自行从 payload 解析，避免依赖 altcha 内部 payload 表示。
func (v *Verifier) Verify(payloadB64 string) (string, error) {
	ok, err := altcha.VerifySolution(payloadB64, v.hmacKey, true)
	if err != nil || !ok {
		return "", ErrInvalidSolution
	}
	raw, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", ErrInvalidSolution
	}
	var p struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Signature == "" {
		return "", ErrInvalidSolution
	}
	return p.Signature, nil
}
```

> 若 `go build` 报 altcha 符号不存在（`ChallengeOptions`/`HMACKey`/`VerifySolution` 等），按本任务顶部「隔离约定」只在本文件调整这几处；round-trip 测试会确认调整正确。

- [ ] **Step 5: 跑测试，确认通过**

Run: `go test ./internal/auth/pow/`
Expected: PASS（4 个用例）。

- [ ] **Step 6: 提交**

```bash
git add go.mod go.sum internal/auth/pow/pow.go internal/auth/pow/pow_test.go
git commit -m "feat(auth): 新增 pow 包封装 Altcha 出题与验解

引入 altcha-lib-go，新包 internal/auth/pow 是全仓库唯一接触该库处。
Verifier 提供无状态出题（HMAC 签名 + 过期）与验解（验签 + 重算 + 取
signature 作一次性 key），round-trip 单测覆盖有效/篡改/过期/非法解。"
```

---

## Task 2: pow 包 — Redis 一次性消费（防重放）

**Files:**
- Create: `internal/auth/pow/replay.go`
- Test: `internal/auth/pow/replay_integration_test.go`

- [ ] **Step 1: 实现 replay.go**

Create `internal/auth/pow/replay.go`:
```go
package pow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisReplayGuard 用 Redis SETNX 保证同一挑战 signature 只被消费一次，实现防重放。
// 复用 manager 既有 *redis.Client（与 distLocker 共享物理实例）。
type RedisReplayGuard struct {
	client redis.Cmdable // 复用现有 go-redis 客户端
	prefix string        // Redis key 前缀（cfg.Redis.KeyPrefix），隔离共享 Redis 键空间
}

// NewRedisReplayGuard 构造一次性消费守卫。
func NewRedisReplayGuard(client redis.Cmdable, keyPrefix string) *RedisReplayGuard {
	return &RedisReplayGuard{client: client, prefix: keyPrefix}
}

// Consume 尝试消费 token：首次写入返回 true，已存在返回 false（即重放）。
// key 为 prefix+"altcha:used:"+sha256hex(token)，TTL 设为题目剩余有效期，
// 保证一道解最多撑到它本就该过期的时刻且只换一次登录尝试。
func (g *RedisReplayGuard) Consume(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	sum := sha256.Sum256([]byte(token))
	key := g.prefix + "altcha:used:" + hex.EncodeToString(sum[:])
	ok, err := g.client.SetNX(ctx, key, 1, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}
```

- [ ] **Step 2: 写集成测试（真实 Redis，缺地址时 skip）**

Create `internal/auth/pow/replay_integration_test.go`:
```go
//go:build integration

package pow

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedisReplayGuard_ConsumeOnce 用真实 Redis 验证一次性消费语义：
// 同一 token 首次 true、二次 false；不同 token 各自首次 true。
func TestRedisReplayGuard_ConsumeOnce(t *testing.T) {
	addr := os.Getenv("INTEGRATION_REDIS_ADDR")
	if addr == "" {
		t.Skip("缺 INTEGRATION_REDIS_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	guard := NewRedisReplayGuard(client, "ocm:test:")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token := "sig-" + time.Now().Format("150405.000")
	first, err := guard.Consume(ctx, token, time.Minute) // 首次消费
	require.NoError(t, err)
	assert.True(t, first)

	second, err := guard.Consume(ctx, token, time.Minute) // 重放
	require.NoError(t, err)
	assert.False(t, second)
}
```

- [ ] **Step 3: 跑普通单测确保编译通过（集成测试默认不跑）**

Run: `go test ./internal/auth/pow/`
Expected: PASS（集成测试因无 `integration` tag 不参与编译；如需跑：`go test -tags integration -run RedisReplayGuard ./internal/auth/pow/` 并设 `INTEGRATION_REDIS_ADDR`）。

- [ ] **Step 4: 提交**

```bash
git add internal/auth/pow/replay.go internal/auth/pow/replay_integration_test.go
git commit -m "feat(auth): pow 包加 Redis 一次性消费防重放

RedisReplayGuard 用 SETNX(prefix+altcha:used:sha256(sig)) 保证每个
挑战解只能换一次登录尝试，TTL 取题目剩余有效期。集成测试在真实
Redis 上验证首次 true/重放 false。"
```

---

## Task 3: 配置 — CaptchaConfig 与校验

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Test: `internal/config/loader_test.go`

- [ ] **Step 1: 在 config.go 加 CaptchaConfig 并挂到 Config**

在 `internal/config/config.go` 的 `Config` 结构体里，`ClawHub` 字段后新增：
```go
	// Captcha 是登录页工作量证明验证码配置；enabled 为 false 时整段可缺省。
	Captcha CaptchaConfig `yaml:"captcha"`
```
并在文件内（如 `ClawHubConfig` 定义之后）新增类型：
```go
// CaptchaConfig 描述登录 PoW 验证码（Altcha）配置。
// enabled 为 false 时其余字段可缺省；启用时 hmac_secret 必填（见 loader 校验）。
type CaptchaConfig struct {
	// Enabled 是验证码总开关；false 时出题接口返回 204、登录跳过 PoW 校验。
	Enabled bool `yaml:"enabled"`
	// HMACSecret 是 Altcha 出题/验签的 HMAC 密钥，按密钥管理（不入 git）。
	HMACSecret string `yaml:"hmac_secret"`
	// Difficulty 是 Altcha maxNumber 难度上限；常驻取低值≈几百 ms，缺省 50000。
	Difficulty int64 `yaml:"difficulty"`
	// TTL 是挑战有效期，也是一次性消费 key 的最长 TTL，缺省 5m。
	TTL Duration `yaml:"ttl"`
}
```
> 因 `decoder.KnownFields(true)`，新增 `captcha:` 必须有对应字段；现有不带 `captcha:` 的 YAML 仍能加载（缺省即 `Enabled=false`），向后兼容。

- [ ] **Step 2: 写失败测试**

在 `internal/config/loader_test.go` 末尾追加（若已有同名 helper 请复用，不要重复定义）：
```go
// validBaseConfig 返回一份通过 Validate 的最小配置，供验证码用例在其上改字段。
func validBaseConfig() Config {
	c := Config{}
	c.App.HTTPAddr = ":8080"
	c.App.DataRoot = "/data"
	c.Database.URL = "mysql://u:p@tcp(127.0.0.1:3306)/ocm"
	c.Redis.Addr = "127.0.0.1:6379"
	c.Auth.AccessTokenTTL = Duration{Duration: time.Hour}
	c.Auth.RefreshTokenTTL = Duration{Duration: 24 * time.Hour}
	c.Auth.JWTAccessSecret = "a"
	c.Auth.JWTRefreshSecret = "b"
	c.Auth.CSRFSecret = "c"
	c.Security.MasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEE=" // 32 字节 base64
	c.Hermes.SystemPromptTemplate = "tmpl"
	return c
}

// 启用验证码但缺 hmac_secret 应校验失败。
func TestValidateCaptchaEnabledRequiresSecret(t *testing.T) {
	c := validBaseConfig()
	c.Captcha.Enabled = true // 开启但不给 hmac_secret
	err := c.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "captcha.hmac_secret")
}

// 启用验证码且给了 hmac_secret 应通过，且 applyDefaults 填好难度与 TTL 默认值。
func TestCaptchaEnabledAppliesDefaults(t *testing.T) {
	c := validBaseConfig()
	c.Captcha.Enabled = true
	c.Captcha.HMACSecret = "secret" // 满足必填
	c.applyDefaults()
	require.NoError(t, c.Validate())
	assert.Equal(t, int64(50000), c.Captcha.Difficulty)        // 默认难度
	assert.Equal(t, 5*time.Minute, c.Captcha.TTL.Duration)     // 默认有效期
}

// 关闭验证码时缺省全部字段也应通过（向后兼容）。
func TestCaptchaDisabledNeedsNothing(t *testing.T) {
	c := validBaseConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())
}
```
> 若文件顶部缺 `time`/`assert`/`require` import 请补上。

- [ ] **Step 3: 跑测试，确认失败**

Run: `go test ./internal/config/ -run Captcha`
Expected: FAIL（默认值未填 / 校验缺失）。

- [ ] **Step 4: 在 loader.go 实现默认值与校验**

在 `internal/config/loader.go` 的 `applyDefaults()` 末尾（return/闭括号前）加入：
```go
	// 验证码启用时填难度与有效期默认（关闭时不使用这两个值）。
	if c.Captcha.Enabled {
		if c.Captcha.Difficulty == 0 {
			c.Captcha.Difficulty = 50000
		}
		if c.Captcha.TTL.Duration == 0 {
			c.Captcha.TTL.Duration = 5 * time.Minute
		}
	}
```
在 `Validate()` 的 `missing` 收集逻辑里（如 `security.master_key` 检查之后）加入：
```go
	if c.Captcha.Enabled && strings.TrimSpace(c.Captcha.HMACSecret) == "" {
		missing = append(missing, "captcha.hmac_secret")
	}
```

- [ ] **Step 5: 跑测试，确认通过**

Run: `go test ./internal/config/`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): 新增 captcha 验证码配置段

加 CaptchaConfig(enabled/hmac_secret/difficulty/ttl)，启用时
hmac_secret 必填、difficulty 默认 50000、ttl 默认 5m；关闭时全段
可缺省，向后兼容现有不带 captcha 的配置。"
```

---

## Task 4: service — ErrCaptcha* 与 CaptchaService

**Files:**
- Modify: `internal/service/errors.go`
- Create: `internal/service/captcha.go`
- Test: `internal/service/captcha_test.go`

- [ ] **Step 1: 加 sentinel error**

在 `internal/service/errors.go` 的「认证」段（`ErrInvalidToken` 之后）加入：
```go
// 验证码（登录 PoW）---------------------------------------------------

// ErrCaptchaRequired 表示开启了验证码但请求未携带 payload，handler 映射为 400。
var ErrCaptchaRequired = errors.New("需要完成人机验证")

// ErrCaptchaInvalid 表示 Altcha 解验签失败、不成立或已过期，handler 映射为 400。
var ErrCaptchaInvalid = errors.New("人机验证失败")

// ErrCaptchaReplayed 表示该 Altcha 解已被消费（重放），handler 映射为 400。
var ErrCaptchaReplayed = errors.New("人机验证已被使用")
```

- [ ] **Step 2: 写 CaptchaService 失败测试**

Create `internal/service/captcha_test.go`:
```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth/pow"
)

// fakeReplay 是 ReplayGuard 的可控桩：firstUse 决定首次/重放，err 模拟 Redis 故障。
type fakeReplay struct {
	firstUse bool
	err      error
	calls    int
}

func (f *fakeReplay) Consume(_ context.Context, _ string, _ time.Duration) (bool, error) {
	f.calls++
	return f.firstUse, f.err
}

// solveFor 复用 pow 包同款暴力解，给 CaptchaService 造一个有效 payload。
// （与 pow_test 的 solveChallenge 等价，这里内联避免跨包导出测试 helper。）
func solveFor(t *testing.T, v *pow.Verifier) string {
	t.Helper()
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	for n := int64(0); n <= 5000; n++ {
		sum := sha256OfSaltNumber(ch.Salt, n)
		if sum == ch.Challenge {
			return buildAltchaPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n)
		}
	}
	t.Fatal("未找到解")
	return ""
}

// 缺 payload → ErrCaptchaRequired。
func TestCaptchaServiceRequiresPayload(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: true})
	err := svc.Verify(context.Background(), "")
	require.ErrorIs(t, err, ErrCaptchaRequired)
}

// 无效 payload → ErrCaptchaInvalid。
func TestCaptchaServiceRejectsInvalid(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: true})
	err := svc.Verify(context.Background(), "garbage")
	require.ErrorIs(t, err, ErrCaptchaInvalid)
}

// 有效 payload 但已被消费 → ErrCaptchaReplayed。
func TestCaptchaServiceRejectsReplay(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: false}) // 模拟已消费
	err := svc.Verify(context.Background(), solveFor(t, v))
	require.ErrorIs(t, err, ErrCaptchaReplayed)
}

// 有效 payload + Redis 故障 → fail-open 放行（返回 nil）。
func TestCaptchaServiceFailOpenOnReplayError(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{err: errors.New("redis down")})
	err := svc.Verify(context.Background(), solveFor(t, v))
	require.NoError(t, err)
}

// 有效 payload + 首次消费 → 通过。
func TestCaptchaServiceAcceptsFirstUse(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: true})
	err := svc.Verify(context.Background(), solveFor(t, v))
	require.NoError(t, err)
}
```
并在同文件加测试用小工具：
```go
// 测试用：复刻 altcha SHA-256 摘要与 payload 拼装，避免依赖 pow 内部。
func sha256OfSaltNumber(salt string, n int64) string {
	sum := sha256sum([]byte(salt + itoa(n)))
	return hexEncode(sum)
}
```
> 为减少样板，本步直接把 `crypto/sha256`、`encoding/hex`、`strconv` 内联用即可。**实现时把 `sha256OfSaltNumber`/`buildAltchaPayload`/`sha256sum`/`itoa`/`hexEncode` 用标准库一行写法替换**（参考 Task 1 的 `solveChallenge`/`buildPayload`），保持与 pow_test 同款逻辑。建议直接复制 Task 1 的 `solveChallenge` 与 `buildPayload` 两个函数到本文件并改名内联，避免引入未定义符号。

- [ ] **Step 3: 跑测试，确认失败**

Run: `go test ./internal/service/ -run Captcha`
Expected: FAIL（`undefined: NewCaptchaService`）。

- [ ] **Step 4: 实现 captcha.go**

Create `internal/service/captcha.go`:
```go
package service

import (
	"context"
	"log/slog"
	"time"

	"oc-manager/internal/auth/pow"
)

// ReplayGuard 抽象一次性消费能力，便于 CaptchaService 单测注入桩。
// 由 internal/auth/pow.RedisReplayGuard 结构化实现。
type ReplayGuard interface {
	// Consume 首次使用返回 true；已使用返回 false；底层故障返回 err。
	Consume(ctx context.Context, token string, ttl time.Duration) (bool, error)
}

// CaptchaVerifier 抽象登录前置的验证码校验，供 AuthService 注入（nil 表示验证码关闭）。
type CaptchaVerifier interface {
	Verify(ctx context.Context, payload string) error
}

// CaptchaService 编排 PoW 验解与一次性消费，是 CaptchaVerifier 的生产实现，
// 同时为出题接口提供 Challenge。
type CaptchaService struct {
	pow    *pow.Verifier
	replay ReplayGuard
}

// NewCaptchaService 构造验证码服务。
func NewCaptchaService(p *pow.Verifier, r ReplayGuard) *CaptchaService {
	return &CaptchaService{pow: p, replay: r}
}

// Challenge 生成一道挑战，返回值可直接 JSON 序列化给 widget。
func (s *CaptchaService) Challenge() (any, error) {
	return s.pow.CreateChallenge()
}

// Verify 执行登录前置校验：空 payload→Required；验签失败→Invalid；
// 重放→Replayed；一次性消费存储故障→fail-open 放行（仅 Warn 日志）。
func (s *CaptchaService) Verify(ctx context.Context, payload string) error {
	if payload == "" {
		return ErrCaptchaRequired
	}
	sig, err := s.pow.Verify(payload)
	if err != nil {
		return ErrCaptchaInvalid
	}
	firstUse, err := s.replay.Consume(ctx, sig, s.pow.TTL())
	if err != nil {
		// fail-open：Redis 故障时仅保留验签、跳过一次性消费，保登录可用。
		slog.WarnContext(ctx, "验证码一次性消费不可用，fail-open 放行", "error", err)
		return nil
	}
	if !firstUse {
		return ErrCaptchaReplayed
	}
	return nil
}
```

- [ ] **Step 5: 跑测试，确认通过**

Run: `go test ./internal/service/ -run Captcha`
Expected: PASS（5 个用例）。

- [ ] **Step 6: 提交**

```bash
git add internal/service/errors.go internal/service/captcha.go internal/service/captcha_test.go
git commit -m "feat(auth): 新增 CaptchaService 编排 PoW 与一次性消费

加 ErrCaptchaRequired/Invalid/Replayed 三个 sentinel；CaptchaService
串联 pow 验解与 ReplayGuard 一次性消费，Redis 故障 fail-open 放行。
单测覆盖缺 payload/无效/重放/fail-open/首次通过五条路径。"
```

---

## Task 5: AuthService 集成验证码前置校验

**Files:**
- Modify: `internal/service/auth_service.go`
- Modify: `internal/service/auth_service_test.go`

- [ ] **Step 1: 给 LoginInput 加 Captcha 字段**

`internal/service/auth_service.go` 的 `LoginInput`：
```go
type LoginInput struct {
	OrgCode  string
	Username string
	Password string
	// Captcha 是 Altcha payload（base64）；验证码开启时由 Login 前置校验，关闭时忽略。
	Captcha string
}
```

- [ ] **Step 2: 给 AuthService 加 captcha 字段并改 NewAuthService 签名**

`AuthService` 结构体加字段：
```go
	// captcha 为验证码前置校验器；nil 表示验证码关闭，Login 直接跳过。
	captcha CaptchaVerifier
```
`NewAuthService` 改为：
```go
// NewAuthService 创建认证服务。captcha 为 nil 时不启用登录验证码校验。
func NewAuthService(store AuthStore, tokens *auth.TokenManager, captcha CaptchaVerifier) *AuthService {
	return &AuthService{
		store:          store,
		tokens:         tokens,
		captcha:        captcha,
		verifyPassword: auth.VerifyPassword,
		hashPassword: func(password string) (string, error) {
			return auth.HashPassword(password, auth.DefaultPasswordParams)
		},
		now: time.Now,
	}
}
```

- [ ] **Step 3: 在 Login 最前面插入校验**

`Login` 方法体开头（`input.OrgCode = strings.ToLower(...)` 之前）插入：
```go
	// 验证码前置校验：开启时（captcha != nil）必须先过 PoW + 一次性消费，
	// 失败直接返回，连密码校验（Argon2id，开销大）都不触发。
	if s.captcha != nil {
		if err := s.captcha.Verify(ctx, input.Captcha); err != nil {
			return LoginResult{}, err
		}
	}
```

- [ ] **Step 4: 更新测试 helper（编译先过）**

在 `internal/service/auth_service_test.go` 找到 `newTestAuthService` 定义，把对 `NewAuthService(...)` 的调用补第三个参数 `nil`（默认这些既有用例不启用验证码）。例如：
```go
	// 既有认证用例默认不启用验证码，captcha 传 nil。
	svc := NewAuthService(store, tokens, nil)
```
> 若 `newTestAuthService` 用结构体字面量或其它方式构造，按同样思路把 captcha 设为 nil。

- [ ] **Step 5: 跑既有用例，确认仍通过**

Run: `go test ./internal/service/ -run AuthService`
Expected: PASS（既有用例不受影响）。

- [ ] **Step 6: 写 Login 验证码用例**

在 `internal/service/auth_service_test.go` 末尾追加：
```go
// loginFakeCaptcha 是 CaptchaVerifier 的测试桩，按预置 err 返回。
type loginFakeCaptcha struct{ err error }

func (f loginFakeCaptcha) Verify(_ context.Context, _ string) error { return f.err }

// 验证码校验失败时，Login 直接返回该错误且不进入密码校验。
func TestAuthServiceLoginRejectsBadCaptcha(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.captcha = loginFakeCaptcha{err: ErrCaptchaInvalid} // 注入失败桩

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
		Captcha:  "whatever",
	})
	require.ErrorIs(t, err, ErrCaptchaInvalid)
	require.False(t, store.loggedIn) // 未走到 MarkUserLoggedIn，证明前置拦截
}

// 验证码校验通过时，Login 正常签发 token。
func TestAuthServiceLoginPassesWithGoodCaptcha(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.captcha = loginFakeCaptcha{err: nil} // 注入通过桩

	result, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
		Captcha:  "valid",
	})
	require.NoError(t, err)
	require.Equal(t, "admin", result.User.Username)
}
```

- [ ] **Step 7: 跑测试，确认通过**

Run: `go test ./internal/service/`
Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add internal/service/auth_service.go internal/service/auth_service_test.go
git commit -m "feat(auth): Login 前置 PoW 验证码校验

LoginInput 加 Captcha 字段；AuthService 注入 CaptchaVerifier(nil 即
关闭)，Login 在密码校验前先过验证码，失败即返回不触发 Argon2id。
更新测试 helper 并补验证码通过/拒绝两条用例。"
```

---

## Task 6: handler 与路由 — 登录传参、错误映射、出题接口

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/router.go`
- Test: `internal/api/handlers/auth_test.go`（不存在则新建）

- [ ] **Step 1: dto 加 Captcha 字段**

`internal/api/handlers/dto.go` 的 `LoginRequest`：
```go
	// Captcha 是 Altcha payload（base64）；验证码开启时必填，是否必填由后端按
	// captcha.enabled 在 service 层判断，故此处不加 binding:"required"。
	Captcha string `json:"captcha"`
```

- [ ] **Step 2: Login handler 透传 captcha**

`internal/api/handlers/auth.go` 的 `Login` 里构造 `service.LoginInput` 处加 `Captcha`：
```go
	result, err := h.service.Login(c.Request.Context(), service.LoginInput{
		OrgCode:  req.OrgCode,
		Username: req.Username,
		Password: req.Password,
		Captcha:  req.Captcha,
	})
```

- [ ] **Step 3: writeAuthError 加验证码映射**

在 `writeAuthError` 的 switch 里（`ErrMemberCreateInvalid` 分支之后、`default` 之前）加：
```go
	case errors.Is(err, service.ErrCaptchaRequired):
		c.JSON(http.StatusBadRequest, apierror.New("CAPTCHA_REQUIRED", "请先完成人机验证"))
	case errors.Is(err, service.ErrCaptchaInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("CAPTCHA_INVALID", "人机验证已失效，请重试"))
	case errors.Is(err, service.ErrCaptchaReplayed):
		c.JSON(http.StatusBadRequest, apierror.New("CAPTCHA_REPLAYED", "人机验证已失效，请重试"))
```

- [ ] **Step 4: 加 AltchaChallenge handler + 改 NewAuthHandler 签名 + 注册路由**

`AuthHandler` 结构体加字段：
```go
	// captcha 为出题器；nil 表示验证码关闭，出题接口返回 204。
	// 用具体类型而非接口，规避 Go typed-nil 接口陷阱（nil 指针装箱成非 nil 接口）。
	captcha *service.CaptchaService
```
`NewAuthHandler` 改签名：
```go
// NewAuthHandler 创建认证 handler。captcha 为 nil 时出题接口返回 204、登录不校验验证码。
func NewAuthHandler(service AuthService, captcha *service.CaptchaService) *AuthHandler {
	return &AuthHandler{service: service, captcha: captcha}
}
```
`RegisterPublicAuthRoutes` 加一条公开路由：
```go
	group.GET("/altcha-challenge", handler.AltchaChallenge)
```
新增 handler 方法：
```go
// AltchaChallenge 下发一道 Altcha 挑战；验证码关闭时返回 204。
//
// @Summary      Altcha 挑战
// @Description  返回登录页验证码挑战；验证码未启用时返回 204
// @Tags         auth
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Altcha 挑战 JSON"
// @Success      204  "验证码未启用"
// @Failure      500  {object}  ErrorResponse
// @Router       /auth/altcha-challenge [get]
func (h *AuthHandler) AltchaChallenge(c *gin.Context) {
	if h.captcha == nil {
		c.Status(http.StatusNoContent)
		return
	}
	challenge, err := h.captcha.Challenge()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apierror.New("CAPTCHA_CHALLENGE_FAILED", "生成人机验证失败"))
		return
	}
	c.JSON(http.StatusOK, challenge)
}
```

- [ ] **Step 5: router.go 装配 captcha**

`internal/api/router.go` 的 `Dependencies` 结构体加字段（`AuthService` 附近）：
```go
	// Captcha 是登录 PoW 验证码服务；nil 表示验证码关闭。
	Captcha *service.CaptchaService
```
把第 123 行 `authHandler := handlers.NewAuthHandler(dep.AuthService)` 改为：
```go
		authHandler := handlers.NewAuthHandler(dep.AuthService, dep.Captcha)
```

- [ ] **Step 6: 写 handler 测试**

Create（或在既有 `internal/api/handlers/auth_test.go` 追加）`internal/api/handlers/auth_test.go`：
```go
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth/pow"
	"oc-manager/internal/service"
)

// 验证码关闭（captcha=nil）时出题接口返回 204。
func TestAltchaChallengeDisabledReturns204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler(nil, nil) // service 在本路由用不到，captcha=nil
	r := gin.New()
	r.GET("/api/v1/auth/altcha-challenge", h.AltchaChallenge)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/altcha-challenge", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// 验证码开启时出题接口返回 200 且响应体含 challenge 字段。
func TestAltchaChallengeEnabledReturnsChallenge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	captcha := service.NewCaptchaService(pow.NewVerifier("test-secret", 1000, time.Minute), nil)
	h := NewAuthHandler(nil, captcha)
	r := gin.New()
	r.GET("/api/v1/auth/altcha-challenge", h.AltchaChallenge)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/altcha-challenge", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "challenge")
	assert.Contains(t, w.Body.String(), "signature")
}
```
> 出题路径不触碰 `replay`，故 `NewCaptchaService(..., nil)` 安全。

- [ ] **Step 7: 跑测试，确认通过**

Run: `go test ./internal/api/handlers/ ./internal/api/`
Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/auth.go internal/api/router.go internal/api/handlers/auth_test.go
git commit -m "feat(auth): 登录接口接入验证码与 Altcha 出题路由

LoginRequest 加 captcha 字段并透传 service；writeAuthError 把三个
验证码错误映射为 400；新增公开 GET /auth/altcha-challenge(关闭返回
204)，router Dependencies 注入 Captcha 服务。handler 测试覆盖 204
与出题两条路径。"
```

---

## Task 7: main.go 装配（typed-nil 处理 + 复用 Redis）

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 加 pow import**

`cmd/server/main.go` 的 import 块（`oc-manager/internal/auth` 附近）加：
```go
	"oc-manager/internal/auth/pow"
```

- [ ] **Step 2: 删除原 authService 构造行，改到 imagecoordRedis 之后装配**

删除第 136 行：
```go
	authService := service.NewAuthService(dbStore.Queries, tokenManager)
```
在 `distLocker := redis.NewRedisDistLocker(imagecoordRedis)`（约第 173 行）之后插入：
```go
	// 验证码（登录 PoW）装配：仅 cfg.Captcha.Enabled 时构造，复用 imagecoordRedis
	// 这个已存在的 go-redis 客户端做一次性消费。
	var captchaService *service.CaptchaService
	if cfg.Captcha.Enabled {
		powVerifier := pow.NewVerifier(cfg.Captcha.HMACSecret, cfg.Captcha.Difficulty, cfg.Captcha.TTL.Duration)
		replayGuard := pow.NewRedisReplayGuard(imagecoordRedis, cfg.Redis.KeyPrefix)
		captchaService = service.NewCaptchaService(powVerifier, replayGuard)
	}
	// 注意 Go typed-nil 接口陷阱：把具体 *CaptchaService(可能为 nil) 直接赋给
	// CaptchaVerifier 接口，会得到非 nil 接口，导致 AuthService.captcha != nil 误判 panic。
	// 故关闭时显式保持 nil 接口。
	var captchaVerifier service.CaptchaVerifier
	if captchaService != nil {
		captchaVerifier = captchaService
	}
	authService := service.NewAuthService(dbStore.Queries, tokenManager, captchaVerifier)
```

- [ ] **Step 3: 在 Dependencies 字面量加 Captcha 字段**

`api.NewRouter(api.Dependencies{` 字面量里（`AuthService: authService,` 附近）加：
```go
			Captcha: captchaService,
```
> 这里传具体 `*service.CaptchaService`（nil 安全），handler 用具体类型判 nil。

- [ ] **Step 4: 编译 + 全量后端测试**

Run: `go build ./... && go test ./...`
Expected: 编译通过，全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add cmd/server/main.go
git commit -m "feat(auth): main 装配登录验证码服务

cfg.Captcha.Enabled 时用 pow.Verifier + 复用 imagecoordRedis 的
RedisReplayGuard 构造 CaptchaService，注入 AuthService 与 router；
显式规避 typed-nil 接口陷阱，关闭时全链路 nil。"
```

---

## Task 8: OpenAPI 同步

**Files:**
- Modify: `openapi/openapi.yaml`、`web/src/api/generated.ts`（生成产物）

- [ ] **Step 1: 重新生成 openapi + 前端类型**

Run:
```bash
cd /home/hujing/dir/software/ywjs/oc-manager
make openapi-gen
make web-types-gen
```
Expected: `openapi/openapi.yaml` 出现 `/auth/altcha-challenge` 路由、`handlers.LoginRequest` 多出 `captcha` 字段；`web/src/api/generated.ts` 同步更新。

- [ ] **Step 2: 校验同步**

Run: `make openapi-check`
Expected: 跑完工作区干净（无未提交 diff）。

- [ ] **Step 3: 提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步登录验证码契约

LoginRequest 增加 captcha 字段、新增 GET /auth/altcha-challenge，
由 make openapi-gen + web-types-gen 生成。"
```

---

## Task 9: 前端依赖与 web component 注册

**Files:**
- Modify: `web/package.json`、`web/src/main.ts`、`web/vite.config.ts`

- [ ] **Step 1: 装 altcha**

Run:
```bash
cd /home/hujing/dir/software/ywjs/oc-manager/web
npm install altcha
```
Expected: `package.json` dependencies 出现 `altcha`。

- [ ] **Step 2: main.ts 注册 web component**

`web/src/main.ts` 顶部 import 区加：
```ts
import 'altcha'
```
> 该副作用 import 把 `<altcha-widget>` 注册为自定义元素，资源随 Vite 打包进自身产物，不连第三方。

- [ ] **Step 3: vite.config.ts 声明自定义元素**

`web/vite.config.ts` 把 `vue()` 改为：
```ts
    vue({
      template: {
        compilerOptions: {
          // 告诉编译器 altcha-* 是自定义元素，避免被当未知 Vue 组件报警。
          isCustomElement: (tag) => tag.startsWith('altcha-'),
        },
      },
    }),
```

- [ ] **Step 4: 构建确认**

Run: `npm run build`
Expected: 构建通过（type-check 与打包无报错）。

- [ ] **Step 5: 提交**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git add web/package.json web/package-lock.json web/src/main.ts web/vite.config.ts
git commit -m "feat(web): 引入 altcha web component 并注册自定义元素

装 altcha 依赖、main.ts 副作用 import 注册 <altcha-widget>、
vite 配 isCustomElement(altcha-*) 避免编译告警。"
```

---

## Task 10: 登录页集成验证码

**Files:**
- Modify: `web/src/stores/auth.ts`、`web/src/pages/login/LoginPage.vue`

- [ ] **Step 1: auth store login 加 captcha 参数**

`web/src/stores/auth.ts` 的 `login` 改为：
```ts
  // login 成功后先持久化 token 再写入 user，保证随后的路由跳转能带 Authorization。
  // captcha 为登录页 Altcha payload；验证码关闭时传 undefined，请求体自动省略。
  async function login(
    username: string,
    password: string,
    orgCode = '',
    captcha?: string,
  ): Promise<LoginResult> {
    loading.value = true
    error.value = null
    try {
      const result = await apiRequest<LoginResult>('/api/v1/auth/login', {
        method: 'POST',
        body: { org_code: orgCode.trim() || undefined, username, password, captcha },
        withAuth: false,
      })
      setStoredTokens({
        accessToken: result.tokens.access_token,
        refreshToken: result.tokens.refresh_token,
      })
      user.value = result.user
      return result
    } catch (err) {
      error.value = err instanceof Error ? err.message : '登录失败'
      throw err
    } finally {
      loading.value = false
    }
  }
```

- [ ] **Step 2: LoginPage.vue template 加 widget 与门槛**

`web/src/pages/login/LoginPage.vue` 在 `<p v-if="errorMessage" ...>` 之后、提交按钮之前插入：
```html
    <!-- 验证码：常驻、auto=onload 加载即自动取题+Web Worker 解，无需点击。
         captchaActive 由挂载时探测出题接口是否 204 决定（关闭则不渲染、不挡按钮）。 -->
    <div v-if="captchaActive" class="login-captcha">
      <altcha-widget
        ref="captchaRef"
        challengeurl="/api/v1/auth/altcha-challenge"
        auto="onload"
        hidefooter
        @statechange="onCaptchaState"
      />
      <p v-if="!captchaVerified" class="login-captcha-hint">🔄 人机校验中…</p>
    </div>
```
把提交按钮改为：
```html
    <button
      type="submit"
      class="login-submit"
      :disabled="auth.loading || (captchaActive && !captchaVerified)"
    >
      {{ auth.loading ? '登录中…' : '登录' }}
    </button>
```

- [ ] **Step 3: LoginPage.vue script 加状态与逻辑**

把 `<script setup lang="ts">` 整段替换为：
```ts
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

// LoginPage 负责本地账号登录，并在登录成功后回跳原始受保护路径。
const auth = useAuthStore()
const router = useRouter()

const orgCode = ref('')
const username = ref('')
const password = ref('')
// showPassword 控制密码框明文显示，仅前端交互不影响提交逻辑。
const showPassword = ref(false)
// errorMessage 只保存本次登录失败原因，下一次提交前会清空。
const errorMessage = ref<string | null>(null)

// captchaActive：是否启用验证码（挂载时探测出题接口决定）；初值 true 以默认禁用按钮（安全侧）。
const captchaActive = ref(true)
// captchaVerified：widget 是否已算出有效解。
const captchaVerified = ref(false)
// captchaPayload：已验证的 Altcha payload，提交时带上。
const captchaPayload = ref('')
// captchaRef：widget 元素引用，失败后 reset() 触发重新出题。
const captchaRef = ref<(HTMLElement & { reset?: () => void }) | null>(null)

// 挂载时探测出题接口：204 表示后端关闭验证码 → 不渲染 widget、不挡按钮；
// 其它（200 或网络错误）按开启处理，渲染 widget，由其自身展示进度/错误。
onMounted(async () => {
  try {
    const res = await fetch('/api/v1/auth/altcha-challenge')
    captchaActive.value = res.status !== 204
  } catch {
    captchaActive.value = true
  }
})

// onCaptchaState 监听 widget 状态：verified 时存 payload 并放行按钮，其它状态清空。
function onCaptchaState(e: Event) {
  const detail = (e as CustomEvent).detail as { state?: string; payload?: string } | undefined
  if (detail?.state === 'verified' && detail.payload) {
    captchaPayload.value = detail.payload
    captchaVerified.value = true
  } else {
    captchaVerified.value = false
    captchaPayload.value = ''
  }
}

// onSubmit 调用 auth store 登录；redirect 查询参数由全局 401 处理器写入。
async function onSubmit() {
  errorMessage.value = null
  try {
    await auth.login(
      username.value,
      password.value,
      orgCode.value,
      captchaActive.value ? captchaPayload.value : undefined,
    )
    const target = (router.currentRoute.value.query.redirect as string | undefined) ?? '/'
    await router.replace(target)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '登录失败'
    // payload 一次性：无论密码错(401)还是验证码错(400)，本次 payload 已消费，
    // 重置 widget 触发重新出题+重算，让用户可再试。
    if (captchaActive.value) {
      captchaVerified.value = false
      captchaPayload.value = ''
      captchaRef.value?.reset?.()
    }
  }
}
```

- [ ] **Step 4: 加 widget 提示样式**

在 `<style scoped>` 内（`.login-error` 之后）加：
```css
.login-captcha {
  margin-bottom: 14px;
}

.login-captcha-hint {
  margin: 8px 0 0;
  color: #7a8597;
  font-size: 12px;
}
```

- [ ] **Step 5: 构建确认**

Run: `cd web && npm run build`
Expected: type-check + 打包通过。

- [ ] **Step 6: 提交**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git add web/src/stores/auth.ts web/src/pages/login/LoginPage.vue
git commit -m "feat(web): 登录页嵌入 Altcha 常驻验证码

挂载探测出题接口(204=关闭)；开启时渲染 <altcha-widget>、按钮在
verified 前禁用并显示「校验中」；登录失败后 reset 重新出题。auth
store login 加 captcha 参数透传请求体。"
```

---

## Task 11: 登录页验证码交互单测

**Files:**
- Create: `web/src/pages/login/LoginPage.spec.ts`

- [ ] **Step 1: 写测试**

Create `web/src/pages/login/LoginPage.spec.ts`:
```ts
// LoginPage.spec.ts — 登录页验证码交互单测。
// 覆盖：开启时未 verified 按钮禁用、verified 后可提交且带 captcha、
// 失败后重置 widget、关闭(204)时按钮直接可用。
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import LoginPage from './LoginPage.vue'

// ======================== mocks ========================
const loginMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ loading: false, login: loginMock }),
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ currentRoute: { value: { query: {} } }, replace: replaceMock }),
}))

// 把出题探测 fetch 固定为指定状态码。
function stubChallenge(status: number) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status }))
}

// stubs：altcha-widget 是自定义元素，挂一个带 reset() 的 stub 以便断言重置。
const resetSpy = vi.fn()
function mountPage() {
  return mount(LoginPage, {
    global: {
      stubs: {
        'altcha-widget': {
          template: '<div class="altcha-stub" />',
          methods: { reset: resetSpy },
        },
      },
    },
  })
}

describe('LoginPage 验证码交互', () => {
  beforeEach(() => {
    loginMock.mockReset()
    replaceMock.mockReset()
    resetSpy.mockReset()
  })

  // 开启验证码(200)时，未 verified 前提交按钮禁用。
  it('未 verified 时按钮禁用', async () => {
    stubChallenge(200)
    const wrapper = mountPage()
    await flushPromises()
    const btn = wrapper.find('button.login-submit')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(wrapper.find('.login-captcha-hint').exists()).toBe(true)
  })

  // verified 后按钮可用，提交把 payload 传给 auth.login。
  it('verified 后提交带 captcha payload', async () => {
    stubChallenge(200)
    loginMock.mockResolvedValue({})
    const wrapper = mountPage()
    await flushPromises()
    // 模拟 widget 触发 verified 状态事件。
    wrapper.find('.altcha-stub').trigger('statechange')
    // 直接派发带 detail 的 CustomEvent（trigger 不带 detail）。
    wrapper.find('.altcha-stub').element.dispatchEvent(
      new CustomEvent('statechange', { detail: { state: 'verified', payload: 'PAYLOAD123' } }),
    )
    await flushPromises()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(loginMock).toHaveBeenCalledWith('', '', '', 'PAYLOAD123')
  })

  // 登录失败后重置 widget 并清空已验证状态。
  it('登录失败后重置 widget', async () => {
    stubChallenge(200)
    loginMock.mockRejectedValue(new Error('账号或密码错误'))
    const wrapper = mountPage()
    await flushPromises()
    wrapper.find('.altcha-stub').element.dispatchEvent(
      new CustomEvent('statechange', { detail: { state: 'verified', payload: 'P' } }),
    )
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(resetSpy).toHaveBeenCalled()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeDefined()
  })

  // 关闭验证码(204)时不渲染 widget、按钮直接可用。
  it('204 时按钮直接可用且无 widget', async () => {
    stubChallenge(204)
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.altcha-stub').exists()).toBe(false)
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()
  })
})
```
> 若 stub 的 `reset()` 因 `@vue/test-utils` stub 方法暴露方式不生效，改为给 stub 一个 `setup` 暴露 `reset` 的写法；核心断言是「失败后按钮重新禁用」，`resetSpy` 为辅助。

- [ ] **Step 2: 跑测试**

Run: `cd web && npx vitest run src/pages/login/LoginPage.spec.ts`
Expected: PASS（4 个用例）。如个别断言因 stub 细节偏差，按上注调整，保证「禁用/启用/失败重置/204」四个行为被覆盖。

- [ ] **Step 3: 提交**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git add web/src/pages/login/LoginPage.spec.ts
git commit -m "test(web): 登录页验证码交互单测

覆盖未 verified 按钮禁用、verified 后提交带 payload、失败后重置
widget、204 关闭时按钮直接可用。"
```

---

## Task 12: 配置文件同步（本地 + prod 模板）

**Files:**
- Modify: `deploy/k8s/local/secret.yaml`
- Modify: `deploy/k8s/prod/secret.example.yaml`

- [ ] **Step 1: 本地 secret 加 captcha 段（本地开启、低难度便于联调）**

`deploy/k8s/local/secret.yaml` 内嵌 `manager.yaml` 里，在 `security:` 段（`master_key` 那行）之后、下一个顶级段之前，插入（4 空格缩进，与 `auth:`/`security:` 同级）：
```yaml
    captcha:
      enabled: true
      hmac_secret: "local-dev-altcha-hmac-secret-change-me"
      difficulty: 2000
      ttl: "5m"
```
> 本地 dev 密钥可入库（仅本地 k3d）；`difficulty: 2000` 让解几乎瞬时，便于联调。

- [ ] **Step 2: prod 模板加 captcha 段（占位、默认关闭）**

`deploy/k8s/prod/secret.example.yaml` 内嵌 `manager.yaml` 里，`security:` 段之后插入：
```yaml
    captcha:
      enabled: false
      hmac_secret: "__FILL_CAPTCHA_HMAC_SECRET__"
      difficulty: 50000
      ttl: "5m"
```
> 占位符遵循该文件既有 `__FILL_*__` 约定；默认 `enabled: false`，上线先合代码、灰度时再开。

- [ ] **Step 3: 本地起服务自检配置可加载**

Run（如本地 k3d 在跑）：
```bash
cd /home/hujing/dir/software/ywjs/oc-manager
make local-up
kubectl -n ocm rollout status deploy/manager-api --timeout=120s
```
Expected: manager-api 正常 Ready（配置含 captcha 段能被 `KnownFields(true)` 接受、Validate 通过）。若本地环境不便起，至少跑 `go test ./internal/config/` 确保解析逻辑覆盖。

- [ ] **Step 4: 提交**

```bash
git add deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.example.yaml
git commit -m "chore(deploy): 配置同步登录验证码 captcha 段

本地 secret 开启验证码(低难度便于联调)，prod 模板加占位
__FILL_CAPTCHA_HMAC_SECRET__ 默认关闭。prod 真实 secret.yaml(被
gitignore)由运维填强随机密钥并 make update-config 生效。"
```

---

## Task 13: 交付验证（真实浏览器 + 线上配置）

**Files:** 无代码改动；执行验证与线上配置。

- [ ] **Step 1: openapi 与全量测试干净**

Run:
```bash
cd /home/hujing/dir/software/ywjs/oc-manager
make openapi-check
go test ./...
cd web && npx vitest run
```
Expected: 全部干净 / PASS。

- [ ] **Step 2: 真实浏览器全流程（本地 k3d，CLAUDE.md 硬性，不可用 curl 替代）**

在浏览器打开 `http://ocm.localhost`，逐项确认：
- [ ] 打开登录页 → widget 自动转圈 → 几百 ms 后打勾、按钮由禁用变可用；
- [ ] 用 `admin` / `admin123`（组织标识留空）登录 → 成功跳转控制台；
- [ ] 故意输错密码 → 提示「账号或密码错误」+ widget 自动重算后可再试；
- [ ] 浏览器开发者工具复制一次成功的 `captcha` payload，构造第二次相同 payload 的登录请求 → 后端返回 400（`CAPTCHA_REPLAYED`），验证防重放；
- [ ] 临时把本地 `captcha.enabled` 改为 `false` 并 `make update-config`（或本地 `kubectl apply` 本地 secret 后 `rollout restart`）→ 登录页无 widget、按钮直接可用、登录正常（验 kill-switch）。验证完改回。

- [ ] **Step 3: 线上配置同步（prod，用户明确要求）**

> prod 真实 `deploy/k8s/prod/secret.yaml` 被 `.gitignore`，不提交。

- [ ] 生成强随机密钥：`openssl rand -base64 32`；
- [ ] 在 prod `secret.yaml` 内嵌 `manager.yaml` 加 `captcha:` 段，`hmac_secret` 填上一步的强随机值，`enabled: false`（先关），`difficulty: 50000`、`ttl: "5m"`；
- [ ] `make update-config`（apply secret + rollout restart manager-api）→ 确认 manager-api Ready；
- [ ] 灰度：观察无误后把 prod `secret.yaml` 的 `captcha.enabled` 改 `true`，再 `make update-config`，浏览器复验登录页验证码生效。

- [ ] **Step 4: 收尾确认**

- [ ] 工作区无无关改动、无密钥/调试代码混入（`git status` 干净，`grep -rn '__FILL_' deploy/k8s/prod/secret.yaml` 应为空，即真实值已填全且该文件未被 git 跟踪）。

---

## Self-Review（计划作者自检）

**1. Spec coverage（逐节对照设计）：**
- 三层架构→纯 PoW 范围：Task 1/2/4/5（PoW + 一次性消费，未做 IP 节流/账号锁定，符合范围 A）。✓
- 无状态出题 + 一次性消费 key（signature）：Task 1（CreateChallenge 无状态、Verify 返回 signature）+ Task 2（SETNX）。✓
- 失败必须重新出题：Task 10 Step 3 `onSubmit` catch 里 `reset()`。✓
- 后端改动（pow 包/ReplayGuard/Login 串接/路由/dto/errors/config/openapi）：Task 1–8。✓
- 前端改动（依赖/main.ts/vite/LoginPage/store/失败重置/校验中状态）：Task 9–11。✓
- 配置 YAML 化 + prod 同步：Task 3（结构体/校验）+ Task 12（本地/ prod 模板）+ Task 13 Step 3（prod 真实 secret + make update-config）。✓
- fail-open：Task 4（CaptchaService.Verify 故障放行）+ 单测。✓
- kill-switch 单一真相源(204)：Task 6（AltchaChallenge 204）+ Task 10（前端探测 204）。✓
- 难度默认 50000 / TTL 5m：Task 3 applyDefaults。✓
- 按钮 verified 前禁用 + 校验中状态：Task 10 Step 2/3。✓

**2. Placeholder scan：** 无 TBD/TODO；Task 4 Step 2 的内联工具函数已明确指示「复制 Task 1 的 solveChallenge/buildPayload 改名内联」，给出具体来源而非占位。✓

**3. Type consistency：** `pow.NewVerifier(string,int64,time.Duration)`、`Verify(string)(string,error)`、`TTL()`、`RedisReplayGuard.Consume(ctx,string,Duration)(bool,error)`、`service.ReplayGuard`/`CaptchaVerifier`、`NewCaptchaService(*pow.Verifier,ReplayGuard)`、`NewAuthService(store,tokens,CaptchaVerifier)`、`NewAuthHandler(AuthService,*service.CaptchaService)`、`Dependencies.Captcha *service.CaptchaService`、`CaptchaConfig{Enabled,HMACSecret,Difficulty int64,TTL Duration}` 在各任务间一致。✓

**4. Ambiguity：** altcha 符号不确定性已在「隔离约定」集中说明并用 round-trip 测试兜底，限定在单文件 ~4 处。✓
