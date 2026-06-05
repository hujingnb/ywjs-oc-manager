# 登录页工作量证明（PoW）验证码设计

> 日期：2026-06-06
> 目标：在登录页加入**常驻、无感、可自托管**的工作量证明验证码（Altcha），
> 主防公网多租户场景下的**大规模撞库 / 扫号**。

## 1. 背景与目标

### 1.1 现状

登录链路（`internal/api/handlers/auth.go` → `internal/service/auth_service.go`）
当前**防暴力破解的层一个都没有**，只有密码卫生做得不错：

| 已有（不动） | 缺失 |
|---|---|
| Argon2id 密码哈希（抗离线爆破） | 无验证码 / 人机校验 |
| 恒定时间比较（防时序） | 无失败次数限制 / 账号锁定 |
| 统一错误消息（防用户枚举） | 无 IP / 频率限流 |
| JWT + refresh 轮换、CSRF 双 cookie | 中间件里 rate-limit 仅「预留接口形态」，未实现 |

### 1.2 威胁模型

部署形态确定为 **C：公网可达 + 多租户**，主要担心**大规模撞库 / 扫号**。
撞库的攻击形态决定了防护选型：

- 攻击者持有泄露的 `(账号, 密码)` 对，**每个账号往往只试 1 次**；
- 流量**分散在海量 IP（僵尸网络）**上；
- 因此「按账号锁定」基本不触发、「按 IP 节流」被分布式绕过；
- 唯一对撞库真正生效的是**让每一次尝试都付出固定算力成本**——这正是常驻 PoW 的价值：百万条凭证就得算百万次 PoW。

### 1.3 关于 PoW 验证码的能力边界（必须明确）

PoW 验证码本质是**「按算力收费」，不是「人机识别」**：

- 它能抬高**规模化撞库 / 扫号**的总成本（本设计的目标）；
- 它**不是图灵测试**，headless 自动化天然能算 PoW；
- 它**挡不住盯着单账号慢慢试**的定向爆破——难度调到对真人「无感」就只能很低，而攻击者用 GPU / 原生代码跑哈希，远快于受害者浏览器 JS，这个「无感」难度对他几乎免费。

> 因此本设计的定位是**反规模化滥用**，不承诺防定向爆破。定向爆破的对症手段是「按账号失败计数 + 锁定」，本期**有意不做**（见 §9 范围外）。

## 2. 关键决策与取舍

| 决策点 | 选择 | 理由 |
|---|---|---|
| 威胁场景 | C 公网多租户，防撞库 | 用户确认 |
| 验证码触发方式 | **常驻**（每次登录都先验 PoW） | 撞库「每账号试 1 次」会绕过「失败 N 次才升级」，常驻才能让每条凭证都付算力 |
| 实现选型 | **Altcha 嵌入式** | MIT 许可、自托管/隐私友好（契合企业 + 国内部署）、Go 库 + web component 与现有栈无缝、自带 Web Worker 不卡 UI、**不新增任何对外服务** |
| 防护范围 | **纯 PoW + Redis 一次性消费防重放** | 撞库场景下 IP 节流 / 账号锁定收益有限，按 YAGNI 砍掉；但防重放不可砍，否则 PoW 唯一卖点失效 |
| 常驻难度 | `maxnumber=50000`（约几百 ms） | 常驻起点，可配置 |
| 提交门槛 | widget `verified` 前**禁用登录按钮**，并显示「人机校验中…」状态 | 杜绝「没带 payload 就提交」；显式状态避免用户以为卡 bug |
| Redis 故障降级 | **fail-open** | 验签层（HMAC+过期）在 Redis 挂时仍有效，只丢「过期窗口内重放」；Redis 本就还被进度总线/队列用着，挂时全站已降级，优先保登录可用；密码层兜底 |

### 2.1 为什么 Altcha 不增加公网暴露面

- **服务端**：Altcha 的 Go 库直接跑在现有 Gin 进程里，出题/验签只是在现有 API server 上**多挂一个路由**，走原有域名与 Ingress，**不新增部署单元、不新增对外端口**。
- **前端**：widget 通过 npm 装入项目，由 Vite 打包进现有前端产物，从**自己的源**加载，**不向任何第三方 CDN / 外部服务发请求**。

与「reCAPTCHA 连 Google」「mCaptcha 连独立 Rust 服务」是两回事——Altcha 全程只和自己的后端说话，公网暴露面与现在完全一致。

## 3. 整体架构

```
                   登录请求 POST /api/v1/auth/login
                              │
   ┌──────────────────────────▼──────────────────────────┐
   │  Altcha PoW 校验（常驻，CAPTCHA_ENABLED 时执行）       │
   │   a. pow.VerifySolution(payload)：验 HMAC 签名 +      │
   │      解确实成立 + 未过期  → 失败 400                  │
   │   b. ReplayGuard.Consume：Redis SETNX 一次性消费      │
   │      已用过 → 400 重放拒绝                            │
   └──────────────────────────▼──────────────────────────┘
                    既有 Argon2id 密码校验（一行不改）
                    失败 → 401 / 成功 → 签发 token
```

**校验顺序**：PoW 验签最便宜且无状态，放最前面当门槛——攻击者连一次密码校验（Argon2id 很贵）都触发不了，顺带保护后端 CPU。

**模块落点**（沿用现有结构）：

- 校验编排在 `internal/service/auth_service.go` 的 `Login()` 里；
- PoW 出题/验签封装到新包 `internal/auth/pow`（薄封装 altcha-lib-go，**不依赖 Redis**，可独立单测）；
- 一次性消费（防重放）用一个薄接口 `ReplayGuard`，由现有 redis client 的 `SETNX` 实现；
- 权限无关，不碰 `internal/auth/authorizer.go`。

## 4. 数据流

```
浏览器                              Gin 后端                        Redis
  │ ①打开登录页，widget 自动                                         │
  │   GET /api/v1/auth/altcha-challenge                              │
  │ ─────────────────────────────────▶│                             │
  │                              pow.CreateChallenge()               │
  │                              随机 salt + 难度 maxnumber          │
  │                              + ExpiresAt(5min) + HMAC 签名       │
  │  {algorithm,challenge,salt,        │  ← 服务端不存任何东西        │
  │   signature,maxnumber}             │    （无状态，验签即可）      │
  │ ◀─────────────────────────────────│                             │
  │ ②Web Worker 暴力找 number（~几百 ms，UI 不卡）                   │
  │   widget 打勾，payload 写入隐藏字段                              │
  │ ③用户输账号密码，点登录                                         │
  │   POST /login {org_code,username,password, captcha:<payload>}    │
  │ ─────────────────────────────────▶│                             │
  │                              a. pow.VerifySolution(payload)      │
  │                                 验 HMAC + 解成立 + 未过期 → 否则 400 │
  │                              b. SETNX altcha:used:<hash(sig)>    │
  │                                 EX=剩余有效期 ───────────────────▶│
  │                                 已存在(=0) → 400 重放拒绝         │
  │                              c. 既有 Argon2id 校验（不动）        │
  │                                 失败 → 401 / 成功 → 签发 token    │
  │ ◀─────────────────────────────────│                             │
```

### 4.1 三个关键设计点

1. **无状态出题**：challenge 用 HMAC 密钥签名下发，校验靠验签 + 重算，**服务端不存挑战**。故 `/altcha-challenge` 极廉价、不会被刷爆存储，天然抗 DoS；只有「已消费的解」才写一个会自动过期的小 key 进 Redis。

2. **一次性消费 = 防重放核心**：key 取 challenge 的 `signature`（每题随机 salt → 唯一）哈希而成；`SETNX` 成功才算本次尝试有效；TTL = 到 `ExpiresAt` 的剩余时间，保证一道解最多撑到它本就该过期的时刻，且**只能换一次登录尝试**。这一步必须在密码校验**之前**执行，且**密码失败也不回滚消费**——否则攻击者一次解可卡住一个账号反复猜密码。

3. **失败后必须重新出题（固有 UX 代价）**：每个 payload 一次性，**不管登录成功失败该 PoW 都被用掉**。用户密码输错想重试，前端要让 widget 自动重取题 + 重算（又几百 ms）。这是「常驻 + 防重放」的固有代价，不是 bug。

## 5. 后端改动

### 5.1 新依赖

```
go get github.com/altcha-org/altcha-lib-go   // MIT，纯库
```

### 5.2 新包 `internal/auth/pow`（纯逻辑，不碰 Redis）

```go
type Verifier struct {
    hmacKey   []byte        // CAPTCHA_HMAC_SECRET
    maxNumber int64         // CAPTCHA_DIFFICULTY
    ttl       time.Duration // CAPTCHA_TTL
}

// 出题：封装 altcha CreateChallenge，带 HMAC 签名 + ExpiresAt=now+ttl
func (v *Verifier) CreateChallenge() (Challenge, error)

// 验解：base64 解码 → altcha VerifySolution（验签 + 重算 + 未过期）
// 成功返回该题 signature（供上层做一次性 key），失败返回 ErrInvalidSolution / ErrExpired
func (v *Verifier) VerifySolution(payloadB64 string) (signature string, err error)
```

### 5.3 一次性消费接口（防重放）

```go
type ReplayGuard interface {
    // 首次使用返回 true；已用过返回 false；Redis 故障返回 err
    Consume(ctx context.Context, token string, ttl time.Duration) (firstUse bool, err error)
}
// Redis 实现：SETNX  key="altcha:used:"+sha256hex(signature)  value=1  EX=ttl
```

### 5.4 `internal/service/auth_service.go` — `Login()` 开头串接

既有 user 查询 + Argon2id 校验 + 签发 token **一行不改**，仅在最前面加：

```go
if s.captchaEnabled {
    if req.Captcha == "" {
        return ErrCaptchaRequired
    }
    sig, err := s.pow.VerifySolution(req.Captcha)
    if err != nil {
        return ErrCaptchaInvalid
    }
    firstUse, err := s.replay.Consume(ctx, sig, s.captchaTTL)
    if err != nil {
        // fail-open：Redis 故障时仅保留验签，跳过一次性消费
        log.Warn("captcha replay guard unavailable, fail-open", "err", err)
    } else if !firstUse {
        return ErrCaptchaReplayed
    }
}
// ↓↓↓ 既有逻辑
```

### 5.5 `internal/api/handlers/auth.go` — 加 1 个公开路由 + handler

```go
group.GET("/altcha-challenge", handler.AltchaChallenge) // 公开
```

`AltchaChallenge`：

- `CAPTCHA_ENABLED=false` → 返回 **204 No Content**（前端据此不渲染 widget、不禁用按钮，kill-switch 单一真相源在后端）；
- 启用 → 返回 `pow.CreateChallenge()` 的 JSON。

### 5.6 `internal/api/handlers/dto.go` — `LoginRequest` 加字段

```go
Captcha string `json:"captcha"` // Altcha payload（base64）；启用验证码时必填
```

> **故意不加 `binding:"required"`**——是否必填由 `CAPTCHA_ENABLED` 在 `Login()` 里判，
> 以保证 feature flag 能真正关掉验证码，不被 gin 强制校验顶死。

### 5.7 `internal/service/errors.go` + handler 错误映射

| sentinel | HTTP | 含义 |
|---|---|---|
| `ErrCaptchaRequired` | 400 | 开了验证码但没带 payload |
| `ErrCaptchaInvalid` | 400 | 验签失败 / 解不成立 / 已过期 |
| `ErrCaptchaReplayed` | 400 | payload 重放（已消费过） |

> 既有 `ErrInvalidCredentials`→401 不变，且**与验证码错误分开**，前端可据此决定是否重置 widget。
> fail-open 下 Redis 故障不向用户暴露错误（仅日志告警），故不需要面向用户的 503。

### 5.8 Config（新增 4 个键）

| key | 默认 | 说明 |
|---|---|---|
| `CAPTCHA_ENABLED` | `false` | 总开关；先合代码后灰度开启 |
| `CAPTCHA_HMAC_SECRET` | （启用时必填） | **当作密钥管理，走 env/配置不入 git** |
| `CAPTCHA_DIFFICULTY` | `50000` | maxnumber；常驻取低值 ≈ 几百 ms |
| `CAPTCHA_TTL` | `5m` | 挑战有效期 = 一次性 key 的最长 TTL |

### 5.9 OpenAPI 同步（CLAUDE.md 硬性）

新增 `/altcha-challenge` 路由 + `LoginRequest.Captcha` 字段 → 必须跑
`make openapi-gen` + `make web-types-gen`，把 `openapi/openapi.yaml` 与
`web/src/api/generated.ts` 一起提交。

## 6. 前端改动

栈：Vue 3 + TS + Naive UI + Vite。

### 6.1 新依赖 + 注册自定义元素

```
npm i altcha   // web component，Vite 打包进自身产物，不连第三方
```

- `main.ts`：`import 'altcha'` 注册 `<altcha-widget>`；
- `vite.config.ts` 的 vue 插件：`isCustomElement: tag => tag.startsWith('altcha-')`，避免 Vue 把它当未知组件报警。

### 6.2 `web/src/pages/login/LoginPage.vue` 集成（常驻，自动出题）

```vue
<altcha-widget
  ref="captchaRef"
  challengeurl="/api/v1/auth/altcha-challenge"
  auto="onload"          <!-- 常驻：加载即自动取题 + Web Worker 算，无需点击 -->
  hidefooter hidelogo
  @statechange="onCaptchaState" />
```

- `auto="onload"` 对应「常驻无感」；用户还在输账号，题通常已算完；
- `@statechange` 拿到 `verified` 时把 `detail.payload` 存进 `captchaPayload` ref；
- 纯 GET 读接口、无状态、不涉 CSRF，与现有 CSRF 双 cookie 不冲突。

### 6.3 提交门槛 + 校验中状态

- 登录按钮在 widget 状态 ≠ `verified` 时**禁用**，旁边显示「🔄 人机校验中…」，`verified` 后变正常态并启用。常驻模式下几乎瞬时完成，用户基本无感；显式状态避免误以为卡 bug。
- `CAPTCHA_ENABLED=false`（challenge 返回 204）时**不渲染 widget、不禁用按钮**。

### 6.4 失败后自动重置

```ts
async function onSubmit() {
  try {
    await auth.login(username.value, password.value, orgCode.value, captchaPayload.value)
    // ...跳转
  } catch (err) {
    errorMessage.value = humanize(err)
    captchaRef.value?.reset()   // payload 已被消费 → 强制重新出题 + 重算
    captchaPayload.value = ''
  }
}
```

不管密码错（401）还是验证码错（400），payload 都已一次性用掉，catch 里统一 `reset()`，widget 自动重算。

### 6.5 `web/src/stores/auth.ts` — `login()` 多带一个参数

```ts
login(username, password, orgCode, captcha)   // 把 captcha 塞进 /login 请求体
```

> 请求体类型由 `make web-types-gen` 重新生成的 `generated.ts` 提供，手动不改类型文件。

### 6.6 错误展示映射（`humanize`）

| 后端 | 用户看到 | widget |
|---|---|---|
| 401 凭证错 | 「账号或密码错误」 | 静默重置 |
| 400 验证码错/重放/缺失 | 「人机验证已失效，请重试」 | 静默重置 |

> 实际上用户绝大多数只会看到「账号或密码错误」，验证码重算是透明的。

## 7. 降级策略

### 7.1 Redis 故障（fail-open）

Redis 挂时**跳过一次性消费，仅保留验签（HMAC + 过期）**：登录仍可用；代价是故障窗口内丧失防重放（攻击者可在 `CAPTCHA_TTL` 内重放同一个解）。验签层仍要求每个挑战真算 PoW，且 Argon2id 密码层兜底。仅记 `Warn` 日志，不向用户暴露错误。

### 7.2 kill-switch 一致性

`CAPTCHA_ENABLED` 为后端单一真相源：关闭时 `/altcha-challenge` 返回 204，前端据此不渲染 widget、不禁用按钮——后端热关验证码即可同步前端，无需重新发版前端。挑战接口临时报错时 widget 显示重试、按钮保持禁用，靠 kill-switch 快速止血。

### 7.3 灰度

`CAPTCHA_ENABLED` 默认 `false` 先合代码，再在单一环境开启验证，通过后才到生产。

## 8. 测试方案

测试断言统一用 testify；每个测试方法 / 子测试 / table 用例都带相邻中文场景注释。

### 8.1 后端单测

- `internal/auth/pow`（无 Redis）：正常解通过、篡改 payload（改 number/salt/signature）→ `ErrInvalidSolution`、过期 → `ErrExpired`、坏 base64 → err。
- `ReplayGuard`（miniredis）：首次 `Consume`→true、同 token 再次→false、TTL 过期后可再用。
- `Login()` table-driven：
  - 关闭验证码 → 跳过校验 = 现状行为；
  - 缺 payload → `ErrCaptchaRequired`；
  - 无效 payload → `ErrCaptchaInvalid`；
  - 重放 payload → `ErrCaptchaReplayed`；
  - 有效 payload + 正确密码 → 签发 token；
  - **有效 payload + 错误密码 → 401，且 payload 已消费（再用同一个 → 重放拒绝）** ← 安全核心用例；
  - Redis 故障 → fail-open 放行（验签通过即继续到密码校验）。
- handler：400 / 401 错误码映射；`/altcha-challenge` 在启用/关闭下分别返回挑战 / 204。

### 8.2 前端单测（vitest，stub 掉 web component）

- 未 `verified` 时按钮禁用且显示「校验中」；
- `verified` 后按钮可用，提交带上 captcha；
- 登录失败后调用 `widget.reset()` 并清空 payload；
- `CAPTCHA_ENABLED=false`（challenge 204）时不渲染 widget、按钮直接可用。

## 9. 范围外（本期不做）

- **按 IP 滑窗节流**、**按账号失败计数 + 锁定**：撞库场景收益有限，按 YAGNI 砍掉；
- **动态难度**（mCaptcha 式按负载调难度）：常驻固定难度已满足，未来需要再引入；
- **定向单账号爆破防护**：PoW 不承诺，需要时应另加账号锁定。

## 10. 交付前检查（CLAUDE.md 硬性）

- `make openapi-check` 工作区干净；
- 跑相关后端 / 前端单测；
- **真实浏览器**全流程验证（不能用 curl 替代）：
  - 打开登录页 → widget 自动转圈 → 几百 ms 后打勾、按钮启用；
  - 正确凭证 → 登录成功跳转；
  - 错误密码 → 提示「账号或密码错误」+ widget 自动重算，可再试；
  - 关掉 `CAPTCHA_ENABLED` → 登录页无 widget、按钮直接可用、登录正常（验 kill-switch）；
  - 抓包确认同一 payload 二次提交被 400 拒（验防重放）；
- 不提交 HMAC secret / 调试代码。

## 11. 落地前需复核的外部事实

- 复核 `altcha-lib-go` 当前版本的 `CreateChallenge` / `VerifySolution` 选项结构体字段名（v2 协议参数：salt、signature、maxnumber / cost、expires 等）与本文示例对齐；
- 确认 `altcha` web component 暴露 payload 的事件名与 `reset()` API（`statechange` / `verified` 事件、`auto` 属性取值）与集成代码一致。
