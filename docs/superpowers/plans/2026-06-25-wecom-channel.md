# 企业微信渠道（智能机器人 AI Bot）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 app 实例新增「企业微信」渠道：用户手填 `bot_id`+`secret` → manager 加密落库 + 同步注入 hermes 容器 env → 重启生效 → 经 oc-ops health 探测连通态，与微信/飞书并存。

**Architecture:** 企业微信 = **「飞书手填那半」+ `WECOM_*` env**。无扫码、无 SSE，凭证随 HTTP body 同步到达，故注入（`PatchSecretKeys`+`RolloutRestart`+置 `restarting`）在 `service.BeginWorkWechatAuth` **同步**完成。连通检查走 worker **通用 check 路径**（`channel_login.go:270-338`）：新注册的 `WorkWeChatAdapter.PollAuth` 查 oc-ops `ChannelStatus(work_wechat)` 读 `platforms.wecom`，映射 `AuthStatus`。oc-ops 新建 `WorkWechatChannelOps` 子类只覆写 `status()`。引擎零改动。

**Tech Stack:** Go（gin / sqlc / golang-migrate / testify）、k8s（client-go，per-app Secret + optional SecretKeyRef env）、Python（oc-ops `ChannelOps` 注册表，两 variant）、Vue 3 + TS（vue-query hooks）。

**权威 spec：** `docs/superpowers/specs/2026-06-25-wecom-channel-design.md`（2026-06-27 已对齐飞书架构）。

**贯穿全程的关键约束（务必先读）：**
- 飞书是最近、可直接照抄的先例。每个 Go 任务都给出「飞书对应代码:行号」，照其模式改 `WECOM_*`。
- **命名取舍（已定）**：service 侧 `feishuPatcher`/`feishuRestarter`/`SetFeishuUnbindDeps` 是渠道无关能力（`PatchSecretKeys`/`RestartApp`），企业微信**直接复用**，不做无关 rename（CLAUDE.md surgical 原则），仅在注释标注「现同时服务飞书与企业微信」。
- **YAGNI（已定）**：不暴露 `websocket_url`，用引擎默认 `wss://openws.work.weixin.qq.com`。只 `bot_id`+`secret` 两个字段、两把 Secret key（`wecom-bot-id`/`wecom-secret`）、两条 env（`WECOM_BOT_ID`/`WECOM_SECRET`）。
- **oc-ops 渠道名对齐（关键 gotcha）**：manager 调 `ChannelStatus(ep, "work_wechat")` 的 channel 实参 = oc-ops `WorkWechatChannelOps.channel` 注册键 = `"work_wechat"`；但其内部读引擎 `platforms.wecom`（引擎平台名是 `wecom`，非 `work_wechat`）。两层命名别混。
- 每个测试方法 / 子测试 / table-driven 用例都要中文场景注释（项目规范）。断言用 testify `require`/`assert`。
- 改 handler 签名 / 请求体 / 响应后必须 `make openapi-gen` + `make web-types-gen`（Task 12）。

---

## 文件结构（先决定边界，再拆任务）

| 文件 | 创建/修改 | 职责 |
|---|---|---|
| `internal/domain/enums.go` | 改 | 加 `ChannelTypeWorkWeChat = "work_wechat"` |
| `internal/worker/handlers/channel_login.go` | 改 | `channelLabelWorker` 加企业微信中文标签 |
| `internal/migrations/000017_support_work_wechat_channel.{up,down}.sql` | 建 | 放宽 `channel_type` CHECK 加 `work_wechat`（唯一约束飞书 000015 已含 channel_type，不动） |
| `internal/integrations/k8sorch/orchestrator.go` | 改 | `AppSpec` 加 `WorkWeChatBotID`/`WorkWeChatSecret` 字段 |
| `internal/integrations/k8sorch/render.go` | 改 | `workWechatOptionalEnv` + `RenderSecret` 写 `wecom-*` key + hermes 容器挂载 |
| `internal/integrations/k8sorch/render_test.go` | 改 | 注入/Secret 单测 |
| `internal/worker/handlers/app_initialize.go` | 改 | `buildAppSpec` 查 `work_wechat` 绑定 + 解密带出 |
| `internal/integrations/channel/work_wechat.go` | 建 | `WorkWeChatAdapter`：`Type`/`BeginAuth`(占位)/`PollAuth`(查 oc-ops health) |
| `internal/integrations/channel/work_wechat_test.go` | 建 | adapter 单测（fake ops/resolver） |
| `internal/service/channel_service.go` | 改 | `SetCipher` + `BeginWorkWechatAuth` + `Unbind` 加 `work_wechat` 分支 |
| `internal/service/channel_service_test.go` | 改 | service 单测 |
| `internal/api/handlers/dto.go` | 改 | `WorkWechatChannelAuthRequest` |
| `internal/api/handlers/channels.go` | 改 | `BeginAuth` 加 `work_wechat` 分流 + 接口加方法 |
| `internal/api/handlers/channels_test.go` | 改 | handler 单测 |
| `cmd/server/main.go` | 改 | 注册 `WorkWeChatAdapter` + `channelService.SetCipher(cipher)` |
| `runtime/hermes/hermes-v2026.6.5/ocops/channel.py` | 改 | `_wecom_status` + `WorkWechatChannelOps` + `register_channel` |
| `runtime/hermes/hermes-v2026.5.16/ocops/channel.py` | 改 | 同上（两 variant 必须一致） |
| `web/src/api/hooks/useChannel.ts` | 改 | `useBeginWorkWechatAuth` |
| `web/src/pages/apps/AppChannelsTab.vue` | 改 | `work_wechat` `supported:true` + 表单 + 状态 |
| `web/src/i18n/locales/{zh,en}/apps/root.ts` | 改 | 企业微信表单/状态/错误文案 |
| `openapi/openapi.yaml`、`web/src/api/generated.ts` | 生成 | `make openapi-gen` + `make web-types-gen` |

---

## Task 0：预检（引擎契约，无代码，gate）

确认引擎契约成立后再动工，避免做完才发现 env 名 / 平台名不符。

**Files:** 无（只读核查）

- [ ] **Step 1: 确认引擎 env 契约与平台名**

在本地 k3d 任一运行中的 hermes pod 内核查（参考 prod-cluster-ops / 本地 `make local-shell svc=...`，或读镜像内文件）：

```bash
# 引擎是否读 WECOM_BOT_ID / WECOM_SECRET，平台启用判定是否 bool(bot_id)
grep -n "WECOM_BOT_ID\|WECOM_SECRET\|Platform.WECOM\|bot_id" <hermes>/gateway/config.py
# /health/detailed 的 platforms 键名是否为 wecom，状态字段是否为 state(connected/fatal)
grep -n "wecom\|aibot\|_mark_connected\|_mark_fatal" <hermes>/gateway/platforms/wecom.py | head
```

Expected: `config.py` 读 `WECOM_BOT_ID`/`WECOM_SECRET`、`platforms.wecom` 存在、状态字段 `state` 取值含 `connected`/`fatal`（与飞书 `_feishu_status` 同形）。

- [ ] **Step 2: 确认 wecom 依赖已在镜像内（无飞书那种缺 SDK 问题）**

```bash
# 在 hermes 容器内
python -c "import gateway.platforms.wecom" && echo "wecom importable"
```

Expected: 可导入。若报缺 SDK，则需在两 variant Dockerfile 预装对应依赖（参考飞书 `lark-oapi` 预装），并把该改动补成一个额外任务。

- [ ] **Step 3: 记录结论**

把核查结果（env 名、平台名、状态字段、依赖是否齐全）记在 PR 描述。**若任一项与 spec 假设不符，停下来同步设计，不要硬写。**

---

## Task 1：domain 枚举 + worker 标签

**Files:**
- Modify: `internal/domain/enums.go:46-47`
- Modify: `internal/worker/handlers/channel_login.go:673-682`
- Test: `internal/worker/handlers/channel_login_test.go`（若不存在则新建最小测试文件）

- [ ] **Step 1: 写失败测试（企业微信中文标签）**

在 `internal/worker/handlers/channel_login_test.go` 加：

```go
// TestChannelLabelWorker_WorkWeChat 覆盖企业微信渠道的中文标签映射，
// 保证审计 detail_message 与其它渠道一致地有可读中文。
func TestChannelLabelWorker_WorkWeChat(t *testing.T) {
	assert.Equal(t, "企业微信", channelLabelWorker(domain.ChannelTypeWorkWeChat))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestChannelLabelWorker_WorkWeChat -v`
Expected: 编译失败（`ChannelTypeWorkWeChat` 未定义）。

- [ ] **Step 3: 加枚举常量**

`internal/domain/enums.go`，在 `ChannelTypeFeishu` 下加：

```go
	// ChannelTypeFeishu 是飞书 / Lark 渠道（扫码自动创建 + 手填兜底，WebSocket 长连接）。
	ChannelTypeFeishu = "feishu"
	// ChannelTypeWorkWeChat 是企业微信渠道（智能机器人 AI Bot 长连接，手填 bot_id+secret）。
	ChannelTypeWorkWeChat = "work_wechat"
```

- [ ] **Step 4: 加 worker 标签分支**

`internal/worker/handlers/channel_login.go` 的 `channelLabelWorker`：

```go
	case domain.ChannelTypeFeishu:
		return "飞书"
	case domain.ChannelTypeWorkWeChat:
		return "企业微信"
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/worker/handlers/ -run TestChannelLabelWorker_WorkWeChat -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/domain/enums.go internal/worker/handlers/channel_login.go internal/worker/handlers/channel_login_test.go
git commit -m "feat(channel): 新增企业微信渠道类型枚举与 worker 中文标签"
```

---

## Task 2：migration 000017（放宽 CHECK 约束）

**Files:**
- Create: `internal/migrations/000017_support_work_wechat_channel.up.sql`
- Create: `internal/migrations/000017_support_work_wechat_channel.down.sql`
- Test: `internal/migrations/migrations_test.go`（已有 `TestFS_ContainsUpAndDownPairs` 自动校验配对）

参考：飞书 `internal/migrations/000015_support_feishu_channel.up.sql`。**唯一约束已由 000015 改为 `(app_active_key, channel_type)`，本任务只动 CHECK 约束。**

- [ ] **Step 1: 写 up 迁移**

`internal/migrations/000017_support_work_wechat_channel.up.sql`：

```sql
-- 企业微信渠道：在 wechat+feishu 基础上放宽 channel_type CHECK 至再加 work_wechat。
-- 唯一约束 uk_channel_bindings_app_active 已由 000015 改为 (app_active_key, channel_type)，
-- 企业微信直接受益（同一 app 可 wechat/feishu/work_wechat 各一条非 deleted 绑定），此处不动。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat'));
```

- [ ] **Step 2: 写 down 迁移**

`internal/migrations/000017_support_work_wechat_channel.down.sql`：

```sql
-- 还原 CHECK 约束至 wechat+feishu（如已有 work_wechat 数据行，回滚会因约束冲突失败，属预期）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu'));
```

- [ ] **Step 3: 跑 FS 配对测试确认通过**

Run: `go test ./internal/migrations/ -run TestFS_ContainsUpAndDownPairs -v`
Expected: PASS（新版本 000017 的 up/down 已配对）。

- [ ] **Step 4: 本地应用迁移并验证 CHECK 放宽**

Run:
```bash
make local-migrate
# 进 DB 验证（参考 AGENTS.md 本地账号；或 make local-shell svc=manager-api 内连 DB）
# 期望：INSERT 一条 channel_type='work_wechat' 不再被 CHECK 拒绝
```
Expected: `channel_type='work_wechat'` 的写入被约束接受。

- [ ] **Step 5: 提交**

```bash
git add internal/migrations/000017_support_work_wechat_channel.up.sql internal/migrations/000017_support_work_wechat_channel.down.sql
git commit -m "feat(channel): migration 放宽 channel_type CHECK 支持企业微信"
```

---

## Task 3：AppSpec 字段 + 注入 env / Secret

**Files:**
- Modify: `internal/integrations/k8sorch/orchestrator.go:55-60`（飞书三字段处）
- Modify: `internal/integrations/k8sorch/render.go:28-42`（RenderSecret）、`:127`（hermes 容器 env 挂载）、`:199-216`（新增 `workWechatOptionalEnv`）
- Test: `internal/integrations/k8sorch/render_test.go`

参考：飞书 `feishuOptionalEnv`（render.go:202）、`RenderSecret` 飞书块（render.go:32-37）。

- [ ] **Step 1: 写失败测试（render 注入企业微信 env + Secret key）**

`internal/integrations/k8sorch/render_test.go` 加：

```go
// TestRenderSecret_WorkWeChatKeys 覆盖已绑定企业微信时 Secret 带出 wecom-bot-id/wecom-secret；
// 未绑定（字段空）时不写这两把 key，保证 optional env 注入语义。
func TestRenderSecret_WorkWeChatKeys(t *testing.T) {
	// 已绑定：两字段非空 → Secret 含两把 key。
	bound := RenderSecret(AppSpec{AppID: "a1", ControlToken: "t", WorkWeChatBotID: "bot-1", WorkWeChatSecret: "sec-1"}, "ns")
	assert.Equal(t, "bot-1", bound.StringData["wecom-bot-id"])
	assert.Equal(t, "sec-1", bound.StringData["wecom-secret"])
	// 未绑定：字段空 → 不写 key（避免空值 env 误启用平台）。
	unbound := RenderSecret(AppSpec{AppID: "a1", ControlToken: "t"}, "ns")
	_, hasBot := unbound.StringData["wecom-bot-id"]
	assert.False(t, hasBot)
}

// TestWorkWechatOptionalEnv 覆盖 hermes 容器永久挂载两条 optional SecretKeyRef env，
// optional=true 保证未绑定时不注入（引擎 getenv 为空→平台不启用）。
func TestWorkWechatOptionalEnv(t *testing.T) {
	envs := workWechatOptionalEnv("a1")
	assert.Len(t, envs, 2)
	assert.Equal(t, "WECOM_BOT_ID", envs[0].Name)
	assert.Equal(t, "WECOM_SECRET", envs[1].Name)
	// optional=true：Secret 缺 key 时 k8s 不报错、不注入该 env。
	assert.True(t, *envs[0].ValueFrom.SecretKeyRef.Optional)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run 'TestRenderSecret_WorkWeChatKeys|TestWorkWechatOptionalEnv' -v`
Expected: 编译失败（字段/函数未定义）。

- [ ] **Step 3: AppSpec 加字段**

`internal/integrations/k8sorch/orchestrator.go`，飞书字段后：

```go
	// FeishuDomain 是飞书 domain：feishu（国内）/ lark（国际），未绑定为空。
	FeishuDomain string
	// WorkWeChatBotID 是企业微信智能机器人 Bot ID（明文，未绑定为空）。
	WorkWeChatBotID string
	// WorkWeChatSecret 是企业微信机器人 Secret 明文（buildAppSpec 从 DB 密文解密后填入，引擎需明文；未绑定为空）。
	WorkWeChatSecret string
```

- [ ] **Step 4: RenderSecret 写 key**

`internal/integrations/k8sorch/render.go`，`RenderSecret` 飞书块之后：

```go
	if spec.FeishuAppID != "" && spec.FeishuAppSecret != "" {
		data["feishu-app-id"] = spec.FeishuAppID
		data["feishu-app-secret"] = spec.FeishuAppSecret
		data["feishu-domain"] = spec.FeishuDomain
	}
	// 已绑定企业微信：带出 bot_id + secret 明文，保证重建/升级不丢配置（DB 是 source of truth）。
	if spec.WorkWeChatBotID != "" && spec.WorkWeChatSecret != "" {
		data["wecom-bot-id"] = spec.WorkWeChatBotID
		data["wecom-secret"] = spec.WorkWeChatSecret
	}
```

- [ ] **Step 5: hermes 容器挂载 env**

`internal/integrations/k8sorch/render.go:127`，原行是 `append(append([]corev1.EnvVar{...}, feishuOptionalEnv(spec.AppID)...), proxyEnv...)`，再包一层 `workWechatOptionalEnv`。最终形如：

```go
Env: append(append(append([]corev1.EnvVar{
	{Name: "HERMES_HOME", Value: "/opt/data"},
	{Name: "API_SERVER_ENABLED", Value: "true"},
	{Name: "API_SERVER_KEY", ValueFrom: ctrlTokenEnv.ValueFrom},
}, feishuOptionalEnv(spec.AppID)...), workWechatOptionalEnv(spec.AppID)...), proxyEnv...),
```

- [ ] **Step 6: 新增 workWechatOptionalEnv**

`internal/integrations/k8sorch/render.go`，`feishuOptionalEnv` 函数之后：

```go
// workWechatOptionalEnv 返回企业微信两条 optional SecretKeyRef env（WECOM_BOT_ID / WECOM_SECRET），
// 供 hermes 容器永久挂载。Optional=true：未绑定时 Secret 无对应 key，k8s 不注入该 env
// （引擎 getenv 为空 → 企业微信平台不启用），Deployment 模板无需随绑定状态变化。
func workWechatOptionalEnv(appID string) []corev1.EnvVar {
	optionalTrue := true
	ref := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(appID)},
			Key:                  key,
			Optional:             &optionalTrue,
		}}
	}
	return []corev1.EnvVar{
		{Name: "WECOM_BOT_ID", ValueFrom: ref("wecom-bot-id")},
		{Name: "WECOM_SECRET", ValueFrom: ref("wecom-secret")},
	}
}
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -run 'TestRenderSecret_WorkWeChatKeys|TestWorkWechatOptionalEnv' -v`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/render.go internal/integrations/k8sorch/render_test.go
git commit -m "feat(channel): 渲染企业微信 WECOM_* optional env 与 Secret key"
```

---

## Task 4：buildAppSpec 带出企业微信凭证（重建不丢）

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go:397-441`（飞书块）
- Test: `internal/worker/handlers/app_initialize_test.go`（仿现有飞书 buildAppSpec 测试）

参考：飞书 buildAppSpec 块（app_initialize.go:404-439）。

- [ ] **Step 1: 写失败测试（已绑定企业微信时解密带出）**

在 `internal/worker/handlers/app_initialize_test.go`（仿飞书 buildAppSpec 测试，搜 `FeishuAppID` 在 `_test.go` 的断言）加：构造一个 `work_wechat` 且 `status=bound` 的绑定行，metadata 含 `bot_id` + `secret_ciphertext`（用同一 cipher 加密的明文），断言 `buildAppSpec` 返回的 `AppSpec.WorkWeChatBotID`/`WorkWeChatSecret` 为解密后的明文。

```go
// TestBuildAppSpec_WorkWeChatBound 覆盖已绑定企业微信时 buildAppSpec 从 metadata 解密带出
// bot_id/secret，保证 app 重建/镜像升级时 RenderSecret 不丢企业微信配置。
func TestBuildAppSpec_WorkWeChatBound(t *testing.T) {
	// 按现有飞书 buildAppSpec 测试同构：fake store 返回 work_wechat bound 绑定行，
	// metadata_json = {"bot_id":"bot-1","secret_ciphertext": <cipher.Encrypt("sec-1")>}。
	// 断言：
	//   assert.Equal(t, "bot-1", spec.WorkWeChatBotID)
	//   assert.Equal(t, "sec-1", spec.WorkWeChatSecret)
}
```

> 实现期：若仓库尚无 buildAppSpec 的直接单测脚手架，照飞书测试建最小 fake store + cipher，复用同构造。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestBuildAppSpec_WorkWeChatBound -v`
Expected: FAIL（字段恒为空）。

- [ ] **Step 3: buildAppSpec 加企业微信解密块**

`internal/worker/handlers/app_initialize.go`，飞书解密块之后、`return k8sorch.AppSpec{...}` 之前：

```go
	// 已绑定企业微信：解密带出 bot_id+secret，使 RenderSecret 在重建/升级时不丢配置。
	// 查询失败 / 无行 / 非 bound / 解密失败均静默降级为空——未绑定的 app 不应因此报错。
	var wecomBotID, wecomSecret string
	if binding, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID: app.ID, ChannelType: domain.ChannelTypeWorkWeChat,
	}); err == nil && binding.Status == domain.ChannelStatusBound && len(binding.MetadataJson) > 0 {
		var m struct {
			BotID            string `json:"bot_id"`
			SecretCiphertext string `json:"secret_ciphertext"`
		}
		if json.Unmarshal(binding.MetadataJson, &m) == nil && m.SecretCiphertext != "" && h.cfg.Cipher != nil {
			if plain, derr := h.cfg.Cipher.Decrypt(m.SecretCiphertext); derr == nil {
				wecomBotID, wecomSecret = m.BotID, string(plain)
			}
		}
	}
```

并在 `return k8sorch.AppSpec{...}` 里补两字段：

```go
		FeishuAppID:     feishuAppID,
		FeishuAppSecret: feishuSecret,
		FeishuDomain:    feishuDomain,
		WorkWeChatBotID:  wecomBotID,
		WorkWeChatSecret: wecomSecret,
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/worker/handlers/ -run TestBuildAppSpec_WorkWeChatBound -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -m "feat(channel): buildAppSpec 解密带出企业微信凭证防重建丢失"
```

---

## Task 5：WorkWeChatAdapter（新建，承载连通检查）

**Files:**
- Create: `internal/integrations/channel/work_wechat.go`
- Create: `internal/integrations/channel/work_wechat_test.go`

参考：`internal/integrations/channel/wechat_identity.go`（`OcOpsLocationResolver`、`channelStatusClient` 接口）、`feishu_runner.go`（窄接口构造模式）。

设计：`PollAuth` 解析 oc-ops 坐标 → 查 `ChannelStatus(work_wechat)` → 映射 `connected→Bound`/`fatal→Failed`/其余→`Pending`。**坐标解析失败 / oc-ops 不可达（重启窗口）/ dev stub → 返回 `Pending`**（吞瞬时错误，让 worker 通用分支 re-enqueue，不把 job 判失败）。`BeginAuth` 是占位（企业微信无扫码发起）。

- [ ] **Step 1: 写失败测试**

`internal/integrations/channel/work_wechat_test.go`：

```go
package channel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// fakeWWLocation 实现 OcOpsLocationResolver：按需返回 supported / 错误。
type fakeWWLocation struct {
	supported bool
	err       error
}

func (f fakeWWLocation) Resolve(_ context.Context, _ string) (ocops.Endpoint, bool, error) {
	return ocops.Endpoint{}, f.supported, f.err
}

// fakeWWStatus 实现 channelStatusClient：返回预置 ChannelStatus / 错误。
type fakeWWStatus struct {
	st  ocops.ChannelStatus
	err error
}

func (f fakeWWStatus) ChannelStatus(_ context.Context, _ ocops.Endpoint, _ string) (ocops.ChannelStatus, error) {
	return f.st, f.err
}

// TestWorkWeChatAdapter_PollAuth 覆盖企业微信连通态映射的五种场景。
func TestWorkWeChatAdapter_PollAuth(t *testing.T) {
	cases := []struct {
		name   string              // 场景名
		loc    fakeWWLocation      // 坐标解析行为
		st     ocops.ChannelStatus // oc-ops 返回的连通态
		stErr  error               // oc-ops 查询错误
		expect AuthStatus          // 期望对外状态
	}{
		// platform_state=connected → 已连上企业微信开放平台 → Bound
		{"connected→bound", fakeWWLocation{supported: true}, ocops.ChannelStatus{PlatformState: "connected"}, nil, AuthStatusBound},
		// platform_state=fatal → 凭证无效等致命错误 → Failed
		{"fatal→failed", fakeWWLocation{supported: true}, ocops.ChannelStatus{PlatformState: "fatal", ErrorMessage: "invalid secret"}, nil, AuthStatusFailed},
		// 连接中（connecting/空）→ Pending，继续等
		{"connecting→pending", fakeWWLocation{supported: true}, ocops.ChannelStatus{PlatformState: "connecting"}, nil, AuthStatusPending},
		// oc-ops 不可达（重启窗口）→ 吞错返回 Pending，不判失败
		{"oc-ops 错误→pending", fakeWWLocation{supported: true}, ocops.ChannelStatus{}, errors.New("connection refused"), AuthStatusPending},
		// dev stub（supported=false）→ Pending
		{"dev stub→pending", fakeWWLocation{supported: false}, ocops.ChannelStatus{}, nil, AuthStatusPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewWorkWeChatAdapter(fakeWWStatus{st: c.st, err: c.stErr}, c.loc)
			got, err := a.PollAuth(context.Background(), AuthInput{AppID: "a1"})
			require.NoError(t, err)
			assert.Equal(t, c.expect, got.Status)
			if c.expect == AuthStatusFailed {
				assert.Equal(t, "invalid secret", got.ErrorMessage)
			}
		})
	}
}

// TestWorkWeChatAdapter_Type_Begin 覆盖 Type 标识与 BeginAuth 占位（企业微信无扫码发起）。
func TestWorkWeChatAdapter_Type_Begin(t *testing.T) {
	a := NewWorkWeChatAdapter(fakeWWStatus{}, fakeWWLocation{supported: true})
	assert.Equal(t, "work_wechat", a.Type())
	_, err := a.BeginAuth(context.Background(), AuthInput{AppID: "a1"})
	require.Error(t, err) // 占位：企业微信凭证经表单提交，不走 adapter 发起
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/channel/ -run TestWorkWeChatAdapter -v`
Expected: 编译失败（`NewWorkWeChatAdapter` 未定义）。

- [ ] **Step 3: 实现 adapter**

`internal/integrations/channel/work_wechat.go`：

```go
package channel

import (
	"context"
	"errors"
	"time"

	"oc-manager/internal/domain"
)

// WorkWeChatAdapter 实现 ChannelAdapter：企业微信无扫码、无 SSE，凭证经 manager 表单同步注入。
// 它只承载「连通状态检查」——PollAuth 经 oc-ops ChannelStatus(work_wechat) 读 platforms.wecom，
// 插进 worker 通用 check 路径（channel_login.go:270-338），无需飞书式两阶段特判。
// BeginAuth 为占位：企业微信不入 channel_start_login，凭证经 POST /channels/work_wechat/auth 提交。
type WorkWeChatAdapter struct {
	// ops 查 oc-ops 渠道连通态（platform_state）。
	ops channelStatusClient
	// resolver 把 appID 解析为 oc-ops 调用坐标及 dev stub 标志。
	resolver OcOpsLocationResolver
}

// NewWorkWeChatAdapter 构造企业微信 adapter；ops 与 resolver 均不得为 nil。
func NewWorkWeChatAdapter(ops channelStatusClient, resolver OcOpsLocationResolver) *WorkWeChatAdapter {
	return &WorkWeChatAdapter{ops: ops, resolver: resolver}
}

// Type 返回 work_wechat（供 Registry 路由；与 oc-ops WorkWechatChannelOps.channel 注册键一致）。
func (a *WorkWeChatAdapter) Type() string { return domain.ChannelTypeWorkWeChat }

// BeginAuth 占位：企业微信无扫码发起，凭证经表单提交，故不应被 worker 调用（不入 channel_start_login）。
func (a *WorkWeChatAdapter) BeginAuth(_ context.Context, _ AuthInput) (AuthChallenge, error) {
	return AuthChallenge{}, errors.New("企业微信不支持扫码发起，凭证经 POST /channels/work_wechat/auth 表单提交")
}

// PollAuth 查 oc-ops 企业微信连通态并映射为统一 AuthStatus。
// 关键容错：坐标解析失败 / oc-ops 不可达（解绑重启窗口）/ dev stub 一律返回 Pending，
// 吞瞬时错误让 worker 通用分支按退避 re-enqueue，不把 check job 判失败。
// 仅 platform_state 明确为 connected/fatal 时给终态（Bound/Failed）。
func (a *WorkWeChatAdapter) PollAuth(ctx context.Context, input AuthInput) (AuthProgress, error) {
	now := time.Now()
	ep, supported, err := a.resolver.Resolve(ctx, input.AppID)
	if err != nil || !supported {
		// 解析失败（基础设施抖动）或 dev stub（无真实 hermes）：等下次 poll。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
	st, err := a.ops.ChannelStatus(ctx, ep, domain.ChannelTypeWorkWeChat)
	if err != nil {
		// oc-ops 不可达（pod 重启窗口）：吞错继续等，不判失败。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
	switch st.PlatformState {
	case "connected":
		return AuthProgress{Status: AuthStatusBound, UpdatedAt: now}, nil
	case "fatal":
		return AuthProgress{Status: AuthStatusFailed, ErrorMessage: st.ErrorMessage, UpdatedAt: now}, nil
	default:
		// connecting / retrying / disconnected / 空：连接中，继续等。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
}

// 确保实现 ChannelAdapter 接口（编译期校验）。
var _ ChannelAdapter = (*WorkWeChatAdapter)(nil)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/channel/ -run TestWorkWeChatAdapter -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/integrations/channel/work_wechat.go internal/integrations/channel/work_wechat_test.go
git commit -m "feat(channel): 新增 WorkWeChatAdapter 查 oc-ops health 承载连通检查"
```

---

## Task 6：注册 WorkWeChatAdapter 到 Registry

**Files:**
- Modify: `cmd/server/main.go:277-280`（飞书注册之后）

参考：`cmd/server/main.go:271`（微信注册）、`:277`（飞书注册）。复用微信已构造的 `ocopsBindingLocationResolver{inner: ocopsResolver}`（适配 `service.OcOpsResolver → channel.OcOpsLocationResolver`）与 `ocopsClient`（满足 `channelStatusClient`）。

- [ ] **Step 1: 注册 adapter**

`cmd/server/main.go`，飞书 `channelRegistry.Register(feishuAdapter)` 之后：

```go
	// 企业微信渠道：无扫码、凭证经表单同步注入；adapter 只承载连通检查
	// （PollAuth 查 oc-ops ChannelStatus(work_wechat)/platforms.wecom，走 worker 通用 check 路径）。
	workWechatAdapter := channel.NewWorkWeChatAdapter(ocopsClient, ocopsBindingLocationResolver{inner: ocopsResolver})
	if err := channelRegistry.Register(workWechatAdapter); err != nil {
		return fmt.Errorf("注册企业微信渠道失败: %w", err)
	}
```

- [ ] **Step 2: 编译确认通过**

Run: `go build ./cmd/server/`
Expected: 编译通过。

> 若 `ocopsClient` 未实现 `channel.channelStatusClient`（窄接口 `ChannelStatus`），核对方法签名；微信 `NewOcOpsBindingResolver(ocopsClient, ...)` 已用同一窄接口，应直接满足。

- [ ] **Step 3: 提交**

```bash
git add cmd/server/main.go
git commit -m "feat(channel): 装配并注册企业微信 adapter 到渠道注册表"
```

---

## Task 7：ChannelService.BeginWorkWechatAuth（同步注入）

**Files:**
- Modify: `internal/service/channel_service.go`（加 `cipher` 字段 + `SetCipher` + `BeginWorkWechatAuth`）
- Test: `internal/service/channel_service_test.go`

参考：`BeginFeishuAuth`（channel_service.go:216）、`BeginAuth`（:119 的审计/入队骨架）、`Unbind` 飞书块（:368-393，patch+restart+restarting）。

**复用既有能力**：加密用新增 `s.cipher`（`SetCipher` 注入）；注入/重启复用 `s.feishuPatcher`/`s.feishuRestarter`（渠道无关）；落库复用 `SetChannelBindingChallenge`（已按 `(app_id, channel_type)` 参数化，置 `pending_auth`+写 metadata，**无需新 sqlc query**）；create-on-demand 复用 `UpsertChannelBindingUnbound`。

- [ ] **Step 1: 写失败测试**

`internal/service/channel_service_test.go` 加（仿现有飞书 service 测试的 fake store；fake store 需实现 `SetChannelBindingChallenge`/`UpsertChannelBindingUnbound`/`GetChannelBindingByAppAndType`/`CreateJob`/`CreateAuditLog`/`SetAppStatus`，多数已在飞书测试 fake 内）：

```go
// TestBeginWorkWechatAuth_Succeeds 覆盖企业微信手填发起：加密落库 + 同步 patch Secret + 重启 +
// 置 restarting + 入队 check job，返回 pending_auth。
func TestBeginWorkWechatAuth_Succeeds(t *testing.T) {
	// 装配：fake store + 真实 auth.Cipher（测试 key）+ fake patcher/restarter（记录调用）+ 注册了 work_wechat adapter 的 registry。
	// 调 BeginWorkWechatAuth(principal=org_admin, appID, {BotID:"bot-1", Secret:"sec-1"})。
	// 断言：
	//   require.NoError(err); assert.Equal(domain.ChannelStatusPendingAuth, res.Status)
	//   patcher 收到 set={"wecom-bot-id":"bot-1","wecom-secret":"sec-1"}
	//   restarter.RestartApp 被调用；SetAppStatus(restarting) 被调用
	//   绑定 metadata_json 含 secret_ciphertext（非明文 "sec-1"）
	//   入队了 channel_check_binding job
}

// TestBeginWorkWechatAuth_InstanceNotReady 覆盖 restarting 等不可发起态被守卫拦截。
func TestBeginWorkWechatAuth_InstanceNotReady(t *testing.T) {
	// app.Status=restarting → require.ErrorIs(err, ErrInstanceNotReady)；不写库不入队。
}

// TestBeginWorkWechatAuth_Forbidden 覆盖 org_member 无管理权限被拒。
func TestBeginWorkWechatAuth_Forbidden(t *testing.T) {
	// principal=org_member（非 owner）→ require.ErrorIs(err, ErrForbidden)。
}
```

> 实现期：照搬飞书 service 测试（搜 `BeginFeishuAuth` 在 `_test.go`）的 fake store 与装配，改请求体/断言。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestBeginWorkWechatAuth -v`
Expected: 编译失败（方法未定义）。

- [ ] **Step 3: 加 cipher 字段 + SetCipher**

`internal/service/channel_service.go`，`ChannelService` 结构体加字段（并把 `feishuPatcher`/`feishuRestarter` 注释补一句「现同时服务飞书与企业微信」）：

```go
	feishuPatcher   FeishuSecretPatcher
	feishuRestarter ChannelRestarter
	// cipher 用于企业微信手填发起时加密 secret 落 metadata（飞书加密在 worker check，企业微信在 service）。
	cipher *auth.Cipher
```

加 setter（在 `SetFeishuUnbindDeps` 旁）：

```go
// SetCipher 注入加密器，供企业微信手填发起时加密 secret 落库。未注入时 BeginWorkWechatAuth 报错。
func (s *ChannelService) SetCipher(c *auth.Cipher) { s.cipher = c }
```

> 确认文件已 import `oc-manager/internal/auth`（已有，`auth.Principal` 在用）。

- [ ] **Step 4: 实现 BeginWorkWechatAuth**

`internal/service/channel_service.go`，`BeginFeishuAuth` 之后。先加入参类型，再加方法：

```go
// WorkWechatAuthInput 是企业微信手填发起的 service 入参（与 handler 的 WorkWechatChannelAuthRequest 对应）。
type WorkWechatAuthInput struct {
	// BotID 是企业微信智能机器人 Bot ID（明文）。
	BotID string
	// Secret 是机器人 Secret 明文（service 内加密后落 metadata，明文注入 k8s Secret）。
	Secret string
}

// BeginWorkWechatAuth 是企业微信手填发起入口（与微信 BeginAuth / 飞书 BeginFeishuAuth 并列，handler 按渠道分流）。
// 企业微信无扫码：凭证随请求体同步到达，故此处一次性完成「加密落库 + 同步注入 Secret + 重启 + 置 restarting + 入队连通探测」。
// 注入/重启复用飞书解绑同款 patcher/restarter（PatchSecretKeys/RestartApp 渠道无关）。
func (s *ChannelService) BeginWorkWechatAuth(ctx context.Context, principal auth.Principal, appID string, in WorkWechatAuthInput) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	// 实例就绪守卫（与微信/飞书同口径）：restarting / 升级 / stopped 等不可发起。
	if !domain.AppCanInitiateChannelAuth(app.Status) {
		return ChallengeResult{}, ErrInstanceNotReady
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(domain.ChannelTypeWorkWeChat); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, domain.ChannelTypeWorkWeChat)
	}
	if s.cipher == nil {
		return ChallengeResult{}, fmt.Errorf("企业微信发起缺少 cipher，无法加密 secret")
	}
	// bound 短路：已绑定再次发起直接返回 bound，不重复注入。
	existing, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: domain.ChannelTypeWorkWeChat})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChallengeResult{}, fmt.Errorf("查询企业微信绑定失败: %w", err)
	}
	if err == nil && existing.Status == domain.ChannelStatusBound {
		return ChallengeResult{Status: domain.ChannelStatusBound, ChannelType: domain.ChannelTypeWorkWeChat}, nil
	}
	// create-on-demand（已存在则 no-op）。
	if err := s.store.UpsertChannelBindingUnbound(ctx, sqlc.UpsertChannelBindingUnboundParams{
		ID:          newUUID(),
		AppID:       app.ID,
		ChannelType: domain.ChannelTypeWorkWeChat,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建企业微信绑定行失败: %w", err)
	}
	// 加密 secret，metadata 只存密文（DB 是 source of truth；明文仅注入 k8s Secret）。
	enc, err := s.cipher.Encrypt([]byte(in.Secret))
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("加密企业微信 secret 失败: %w", err)
	}
	metaJSON, err := json.Marshal(map[string]any{
		"bot_id":            in.BotID,
		"secret_ciphertext": enc,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化企业微信 metadata 失败: %w", err)
	}
	// SetChannelBindingChallenge 已按 (app_id, channel_type) 参数化：置 pending_auth + 写 metadata_json + 清 last_error。
	if err := s.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
		MetadataJson: metaJSON,
		AppID:        app.ID,
		ChannelType:  domain.ChannelTypeWorkWeChat,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("写入企业微信凭证失败: %w", err)
	}
	// 同步注入：明文写 app Secret（引擎 WECOM_* 需明文）。patcher 为 nil（未启用 k8s）时跳过。
	if s.feishuPatcher != nil {
		if err := s.feishuPatcher.PatchSecretKeys(ctx, app.ID, map[string]string{
			"wecom-bot-id": in.BotID,
			"wecom-secret": in.Secret,
		}, nil); err != nil {
			return ChallengeResult{}, fmt.Errorf("注入企业微信 Secret 失败: %w", err)
		}
	}
	// RolloutRestart 前置 restarting：重建 pod 窗口 oc-ops 不可用，标记过渡态让前端禁用发起、
	// reconciler 在 pod 重新 Ready 后收敛回 running。守卫不过（非 running）则跳过置位只记日志。
	if err := domain.EnsureAppTransition(app.Status, domain.AppStatusRestarting); err != nil {
		slog.InfoContext(ctx, "企业微信发起跳过置 restarting：当前状态不允许", "app_id", app.ID, "status", app.Status)
	} else if err := s.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{Status: domain.AppStatusRestarting, ID: app.ID}); err != nil {
		slog.ErrorContext(ctx, "企业微信发起置 restarting 失败", "app_id", app.ID, redactlog.Err(err))
	}
	if s.feishuRestarter != nil {
		if err := s.feishuRestarter.RestartApp(ctx, app.ID); err != nil {
			slog.ErrorContext(ctx, "企业微信注入后重启失败", "app_id", app.ID, redactlog.Err(err))
		}
	}
	// 入队 channel_check_binding（不入 channel_start_login：无扫码挑战可发起）。
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"channel_type": domain.ChannelTypeWorkWeChat,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化企业微信探测任务失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeChannelCheckBinding,
		Priority:    80,
		RunAfter:    time.Now().Add(5 * time.Second),
		MaxAttempts: 20,
		PayloadJson: payload,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建企业微信探测任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{Status: domain.ChannelStatusPendingAuth, ChannelType: domain.ChannelTypeWorkWeChat, JobID: jobID}, nil
}
```

> 注：`domain.JobTypeChannelCheckBinding`（飞书 worker 用）、`newUUID`/`json`/`sql`/`time`/`slog`/`redactlog` 均已在该文件 import。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/service/ -run TestBeginWorkWechatAuth -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/service/channel_service.go internal/service/channel_service_test.go
git commit -m "feat(channel): 企业微信手填发起入口同步加密落库+注入+重启"
```

---

## Task 8：Unbind 支持 work_wechat（删 Secret key + 重启）

**Files:**
- Modify: `internal/service/channel_service.go:367-393`（飞书解绑块）
- Test: `internal/service/channel_service_test.go`

把现有「仅 feishu」的即时清理分支泛化到也覆盖 `work_wechat`，按渠道选 key 列表。

- [ ] **Step 1: 写失败测试**

```go
// TestUnbind_WorkWeChat 覆盖企业微信解绑：置 unbound_by_user + 删 wecom-* Secret key + 置 restarting + 重启。
func TestUnbind_WorkWeChat(t *testing.T) {
	// 装配同 Task 7。已绑定 work_wechat 的 app（status=running）。
	// 调 Unbind(org_admin, appID, "work_wechat")。
	// 断言：binding.status=unbound_by_user；patcher 收到 del=["wecom-bot-id","wecom-secret"]；
	//       SetAppStatus(restarting) + RestartApp 被调用。
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestUnbind_WorkWeChat -v`
Expected: FAIL（未删 wecom key / 未重启）。

- [ ] **Step 3: 泛化 Unbind 即时清理分支**

`internal/service/channel_service.go`，把 `if channelType == domain.ChannelTypeFeishu && s.feishuPatcher != nil {` 整块改为按渠道选 key：

```go
	// 飞书 / 企业微信解绑是用户即时动作（不走 worker）：删 app Secret 对应 key 并重启，
	// 使引擎下次重启不再启用该平台。删 key / 重启失败只记日志不阻断——
	// DB status=unbound_by_user 已是 source of truth，凭证残留也不会被引擎装载。
	if delKeys := unbindSecretKeys(channelType); delKeys != nil && s.feishuPatcher != nil {
		if err := s.feishuPatcher.PatchSecretKeys(ctx, app.ID, nil, delKeys); err != nil {
			slog.ErrorContext(ctx, "解绑删渠道 Secret key 失败", "app_id", app.ID, "channel", channelType, redactlog.Err(err))
		}
		if err := domain.EnsureAppTransition(app.Status, domain.AppStatusRestarting); err != nil {
			slog.InfoContext(ctx, "解绑跳过置 restarting：当前状态不允许", "app_id", app.ID, "status", app.Status)
		} else if err := s.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
			Status: domain.AppStatusRestarting,
			ID:     app.ID,
		}); err != nil {
			slog.ErrorContext(ctx, "解绑置 restarting 失败", "app_id", app.ID, redactlog.Err(err))
		}
		if s.feishuRestarter != nil {
			if err := s.feishuRestarter.RestartApp(ctx, app.ID); err != nil {
				slog.ErrorContext(ctx, "解绑后重启失败", "app_id", app.ID, redactlog.Err(err))
			}
		}
	}
```

并在文件内加 helper：

```go
// unbindSecretKeys 返回某渠道解绑时需从 app Secret 删除的 key 列表；非 env 注入型渠道（如微信文件态）返回 nil。
func unbindSecretKeys(channelType string) []string {
	switch channelType {
	case domain.ChannelTypeFeishu:
		return []string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}
	case domain.ChannelTypeWorkWeChat:
		return []string{"wecom-bot-id", "wecom-secret"}
	default:
		return nil
	}
}
```

- [ ] **Step 4: 跑测试确认通过（含飞书回归）**

Run: `go test ./internal/service/ -run 'TestUnbind' -v`
Expected: PASS（企业微信新用例 + 飞书既有用例都过）。

- [ ] **Step 5: 提交**

```bash
git add internal/service/channel_service.go internal/service/channel_service_test.go
git commit -m "feat(channel): 解绑即时清理泛化覆盖企业微信 wecom-* key"
```

---

## Task 9：handler DTO + 分流

**Files:**
- Modify: `internal/api/handlers/dto.go:149-154`（飞书请求体旁）
- Modify: `internal/api/handlers/channels.go:18-25`（接口）、`:66-92`（BeginAuth 分流）
- Test: `internal/api/handlers/channels_test.go`

参考：飞书 `FeishuChannelAuthRequest`（dto.go:150）、handler 飞书分流（channels.go:72-90）。

- [ ] **Step 1: 写失败测试**

`internal/api/handlers/channels_test.go`（仿飞书 handler 测试，搜 `feishu/auth`）：

```go
// TestBeginAuth_WorkWeChat 覆盖企业微信手填发起：handler 读 bot_id/secret body → 调 BeginWorkWechatAuth。
func TestBeginAuth_WorkWeChat(t *testing.T) {
	// fake channelService 记录 BeginWorkWechatAuth 入参。
	// POST /api/v1/apps/app-1/channels/work_wechat/auth body={"bot_id":"bot-1","secret":"sec-1"}
	// 断言：200；fake 收到 in.BotID=="bot-1" && in.Secret=="sec-1"。
}

// TestBeginAuth_WorkWeChat_BadBody 覆盖缺字段返回 400。
func TestBeginAuth_WorkWeChat_BadBody(t *testing.T) {
	// body={} → 400 BAD_REQUEST。
}
```

> 实现期：fake `channelService` 需补 `BeginWorkWechatAuth` 方法（接口新增后所有 fake 都要补，否则编译不过）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/handlers/ -run TestBeginAuth_WorkWeChat -v`
Expected: 编译失败（接口缺方法 / DTO 未定义）。

- [ ] **Step 3: 加 DTO**

`internal/api/handlers/dto.go`，飞书请求体之后：

```go
// WorkWechatChannelAuthRequest 是企业微信渠道发起请求体（手填智能机器人凭证）。
type WorkWechatChannelAuthRequest struct {
	// BotID 是企业微信智能机器人 Bot ID（必填）。
	BotID string `json:"bot_id" binding:"required"`
	// Secret 是机器人 Secret（必填，仅入库密文与注入 Secret，不回显）。
	Secret string `json:"secret" binding:"required"`
}
```

- [ ] **Step 4: 接口加方法 + handler 分流**

`internal/api/handlers/channels.go`，`channelService` 接口加：

```go
	BeginFeishuAuth(ctx context.Context, principal auth.Principal, appID string, in service.FeishuAuthInput) (service.ChallengeResult, error)
	// BeginWorkWechatAuth 是企业微信专用发起入口（手填 bot_id+secret，同步注入）。
	BeginWorkWechatAuth(ctx context.Context, principal auth.Principal, appID string, in service.WorkWechatAuthInput) (service.ChallengeResult, error)
```

`BeginAuth` handler，在飞书分流块之后、通用路径之前加：

```go
	// 企业微信走专用入口（读请求体 bot_id+secret，手填同步注入），与微信/飞书分流。
	if channelType == domain.ChannelTypeWorkWeChat {
		var req WorkWechatChannelAuthRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgChannelInvalidRequest)
			return
		}
		result, err := h.service.BeginWorkWechatAuth(c.Request.Context(), principal, appID, service.WorkWechatAuthInput{
			BotID:  req.BotID,
			Secret: req.Secret,
		})
		if err != nil {
			writeChannelError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": result})
		return
	}
```

并把 `BeginAuth` swag 注解 `@Description` 改为「feishu / work_wechat 渠道需传请求体，其他渠道（如 wechat）无需请求体」。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/api/handlers/ -run TestBeginAuth_WorkWeChat -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/channels.go internal/api/handlers/channels_test.go
git commit -m "feat(channel): handler 分流企业微信手填请求体到专用入口"
```

---

## Task 10：main.go 装配 cipher + 全量回归

**Files:**
- Modify: `cmd/server/main.go:262`（`SetFeishuUnbindDeps` 旁）

- [ ] **Step 1: 注入 cipher**

`cmd/server/main.go`，`channelService.SetFeishuUnbindDeps(...)` 之后：

```go
	// 企业微信手填发起需在 service 内加密 secret 落 metadata（飞书在 worker 加密，企业微信在 service）。
	channelService.SetCipher(cipher)
```

> `cipher` 在 main.go 已构造（同处用于 `channelCheckHandler.SetFeishuDeps(feishuPatcher, cipher, ...)`，行 514）。

- [ ] **Step 2: 编译并跑全量后端测试**

Run: `go build ./cmd/server/ && go test ./internal/...`
Expected: 编译通过，全绿。

- [ ] **Step 3: 提交**

```bash
git add cmd/server/main.go
git commit -m "feat(channel): 为企业微信发起注入 service cipher"
```

---

## Task 11：oc-ops WorkWechatChannelOps（两 variant）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`

参考：`_feishu_status`（channel.py:44-86）、`FeishuChannelOps`（:282）、注册（:375-376）。

> ⚠️ **两 variant 必须同步改、内容一致**。先在 6.5 改完测通，再原样移植 5.16，并核对 5.16 的 `_feishu_status`/`ChannelOps`/`register_channel` 结构一致（飞书已确认两 variant 同构）。

- [ ] **Step 1: 加 _wecom_status helper（6.5）**

`runtime/hermes/hermes-v2026.6.5/ocops/channel.py`，`_feishu_status` 之后：

```python
def _wecom_status() -> dict:
    """读 hermes api_server /health/detailed 的 platforms.wecom 运行态，映射为渠道绑定态。

    与 _feishu_status 同形：引擎平台名是 wecom（非 work_wechat），字段为 state
    （connected/fatal/…）。对外仍以 platform_state 暴露给 manager 稳定渠道契约。
      - state == "connected" → bound=True
      - state == "fatal"     → bound=False，带 error_code/error_message
      - 其他 → bound=False，pending 态
    """
    import json as _json
    import urllib.request as _u

    req = _u.Request(_API_BASE + "/health/detailed", method="GET")
    key = _api_server_key()
    if key:
        req.add_header("Authorization", "Bearer " + key)
    try:
        with _u.urlopen(req, timeout=10) as resp:
            data = _json.loads(resp.read().decode("utf-8"))
    except Exception as e:  # noqa: BLE001 - 网络/解析失败统一映射为 INTERNAL
        raise OpsError("INTERNAL", f"查询 /health/detailed 失败: {e}")
    we = (data.get("platforms") or {}).get("wecom") or {}
    state = we.get("state", "")
    if state == "connected":
        return {"channel": "work_wechat", "bound": True, "platform_state": state}
    if state == "fatal":
        return {"channel": "work_wechat", "bound": False, "platform_state": state,
                "error_code": we.get("error_code", "") or "",
                "error_message": we.get("error_message", "") or ""}
    return {"channel": "work_wechat", "bound": False, "platform_state": state or "connecting"}
```

- [ ] **Step 2: 加 WorkWechatChannelOps + 注册（6.5）**

`FeishuChannelOps` 类之后：

```python
class WorkWechatChannelOps(ChannelOps):
    """企业微信渠道：智能机器人凭证经环境变量注入引擎（manager 直注），运行态走 health。

    无扫码授权流（auth_stream 用基类默认 failed 终态即可）；channel 标识 work_wechat
    与 manager 侧枚举一致，但内部读引擎 platforms.wecom（引擎平台名是 wecom）。"""

    channel = "work_wechat"

    def status(self, data_root: Path) -> dict:
        # 企业微信无本地账号文件（凭证经 WECOM_* env 注入），绑定态以引擎运行态为准。
        return _wecom_status()

    def unbind(self, data_root: Path) -> dict:
        # env 注入型渠道：oc-ops 侧无本地文件态可删（真正清理由 manager 删 wecom-* key + RolloutRestart），
        # 此处返回幂等成功即可。
        return {"channel": "work_wechat", "status": "unbound"}
```

注册块（`register_channel(FeishuChannelOps())` 之后）：

```python
register_channel(WeixinChannelOps())
register_channel(FeishuChannelOps())
register_channel(WorkWechatChannelOps())
```

- [ ] **Step 3: 本地验证（6.5）**

构建/部署该 variant 后（或在已运行 6.5 pod 内导入模块）确认无语法错误、`channel_status("work_wechat", ...)` 可派发：

```bash
make local-shell svc=<app-oc-ops 容器>   # 或对应本地实例
python -c "from ocops.channel import channel_status; print('ok')"
```
Expected: `ok`。未连接时调 status 返回 `platform_state` pending 态，已连通返回 connected。

- [ ] **Step 4: 原样移植到 5.16 + 核对同构**

把 Step 1-2 两段原样加入 `runtime/hermes/hermes-v2026.5.16/ocops/channel.py` 对应位置，核对 5.16 的 `_API_BASE`/`_api_server_key`/`OpsError`/`ChannelOps`/`register_channel` 与 6.5 同名同义。

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py
git commit -m "feat(channel): oc-ops 两 variant 新增企业微信连通状态 ChannelOps"
```

---

## Task 12：OpenAPI + 前端类型同步

**Files:**
- Generate: `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 重生成**

Run:
```bash
make openapi-gen
make web-types-gen
```

- [ ] **Step 2: 校验同步**

Run: `make openapi-check`
Expected: `make openapi-gen` 后 git 工作区干净（yaml 已随 handler 变更更新）。

- [ ] **Step 3: 提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(channel): 同步企业微信 handler 的 OpenAPI 与前端类型"
```

---

## Task 13：前端 useBeginWorkWechatAuth hook

**Files:**
- Modify: `web/src/api/hooks/useChannel.ts`（`useBeginFeishuAuth` 之后）

参考：`useBeginFeishuAuth`（useChannel.ts，带 body 的 mutation）。

- [ ] **Step 1: 加 body 类型 + hook**

`web/src/api/hooks/useChannel.ts`，`useBeginFeishuAuth` 之后：

```ts
// WorkWechatAuthBody 描述企业微信发起绑定的请求体（手填智能机器人凭证）。
export interface WorkWechatAuthBody {
  // 企业微信智能机器人 Bot ID。
  bot_id: string
  // 机器人 Secret（仅提交，不回显）。
  secret: string
}

// useBeginWorkWechatAuth 触发企业微信手填绑定，发起需携带 bot_id+secret body。
// 复用通用进度轮询（GET /channels/work_wechat/auth）与解绑接口，仅发起入口不同。
// 成功后失效企业微信进度缓存，让轮询尽快拉到连通态。
export function useBeginWorkWechatAuth(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (body: WorkWechatAuthBody) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/work_wechat/auth`,
        { method: 'POST', body },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, 'work_wechat') })
    },
  })
}
```

- [ ] **Step 2: 类型检查通过**

Run: `cd web && npm run type-check`（查 `web/package.json` scripts 确认命令名）
Expected: 无类型错误。

- [ ] **Step 3: 提交**

```bash
git add web/src/api/hooks/useChannel.ts
git commit -m "feat(channel): 前端新增企业微信手填发起 hook"
```

---

## Task 14：前端 AppChannelsTab 表单 + 状态 + i18n

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.vue`（渠道列表 `supported`、操作区、详情面板、`<script setup>` 状态）
- Modify: `web/src/i18n/locales/zh/apps/root.ts`、`web/src/i18n/locales/en/apps/root.ts`

参考：飞书在 AppChannelsTab.vue 的整段实现（`selectedChannelType === 'feishu'` 的操作区 67-84、详情面板 96-…、`<script setup>` 里 `feishuBound`/`feishuBeginning`/`beginFeishuScan`/`unbindFeishu`/`feishuCanUnbind` 等）。企业微信比飞书更简单：**无模式选择、无二维码、无 domain 下拉**，只有「bot_id + secret 表单 + 提交」。

- [ ] **Step 1: 渠道列表放开 supported**

找到渠道列表里 `{ type: 'work_wechat', supported: false }`（约 AppChannelsTab.vue:182-192），改为 `supported: true`。

- [ ] **Step 2: `<script setup>` 加企业微信状态与动作**

仿飞书 computed/方法新增（用 `useBeginWorkWechatAuth`、`useChannelProgressQuery`、`useUnbindChannel`）：

```ts
// 企业微信手填表单输入（仅提交时使用，不回显已绑定 secret）。
const wecomBotId = ref('')
const wecomSecret = ref('')
const beginWorkWechat = useBeginWorkWechatAuth(appId)
const wecomBeginning = computed(() => beginWorkWechat.isPending.value)
// 企业微信进度查询（复用通用轮询，仅当前选中企业微信时启用）。
const wecomProgress = useChannelProgressQuery(appId, computed(() => selectedChannelType.value === 'work_wechat' ? 'work_wechat' : undefined))
const wecomBound = computed(() => wecomProgress.data.value?.status === 'bound')
const wecomError = computed(() => wecomProgress.data.value?.error_message ?? '')
const wecomCanUnbind = computed(() => Boolean(wecomProgress.data.value && wecomProgress.data.value.status !== 'unbound'))

// 提交手填凭证：调发起接口，成功后清空 secret 输入（不滞留明文）。
async function submitWorkWechat() {
  if (!wecomBotId.value || !wecomSecret.value) return
  await beginWorkWechat.mutateAsync({ bot_id: wecomBotId.value, secret: wecomSecret.value })
  wecomSecret.value = ''
}
const unbindWorkWechatMutation = useUnbindChannel(appId, computed(() => 'work_wechat'))
async function unbindWorkWechat() { await unbindWorkWechatMutation.mutateAsync() }
```

> 实现期：变量命名、`instanceReady`/`canManage` 闸门、错误提示，全部对齐飞书既有写法（同文件可见）。

- [ ] **Step 3: 操作区（`selectedChannelType === 'work_wechat'`）**

仿飞书操作区（67-84），加一段（按钮位置遵循现行约定——标题右上，提交 030d088 的布局）：

```vue
<n-space v-else-if="selectedChannelType === 'work_wechat'" :size="8">
  <n-button
    v-if="!wecomBound"
    type="primary"
    :disabled="!appId || !canManage || !instanceReady || !wecomBotId || !wecomSecret"
    :loading="wecomBeginning"
    @click="submitWorkWechat"
  >{{ t('apps.channels.workWechatSubmit') }}</n-button>
  <n-button v-if="wecomCanUnbind" @click="unbindWorkWechat">{{ t('apps.channels.unbind') }}</n-button>
</n-space>
```

并复用飞书同款的「实例重启中」提示（`v-if="canManage && !instanceReady"`，第 84 行那条 `state-text`）。

- [ ] **Step 4: 详情面板（`selectedChannelType === 'work_wechat'`）**

仿飞书详情面板，加表单 + 状态：

```vue
<template v-else-if="selectedChannelType === 'work_wechat'">
  <div class="wecom-panel">
    <!-- 已绑定：展示在线态，secret 脱敏不回显 -->
    <template v-if="wecomBound">
      <div class="wecom-bound">
        <p>{{ t('apps.channels.workWechatBoundHint') }}</p>
      </div>
    </template>
    <!-- 未绑定：手填表单（bot_id + secret） -->
    <template v-else>
      <div class="wecom-controls">
        <label class="wecom-field">
          <span class="wecom-field-label">{{ t('apps.channels.workWechatBotIdLabel') }}</span>
          <n-input v-model:value="wecomBotId" :disabled="!canManage" :placeholder="t('apps.channels.workWechatBotIdPlaceholder')" />
        </label>
        <label class="wecom-field">
          <span class="wecom-field-label">{{ t('apps.channels.workWechatSecretLabel') }}</span>
          <n-input v-model:value="wecomSecret" type="password" show-password-on="click" :disabled="!canManage" :placeholder="t('apps.channels.workWechatSecretPlaceholder')" />
        </label>
      </div>
      <!-- 精简内联指引（不用重型折叠块，与现行渠道 UI 一致）：如何在企业微信后台取 Bot Id + Secret -->
      <p class="wecom-guide">{{ t('apps.channels.workWechatGuide') }}</p>
    </template>
    <!-- 失败原因 -->
    <p v-if="wecomError" class="state-text error">{{ wecomError }}</p>
  </div>
</template>
```

> 样式 class（`wecom-panel`/`wecom-field` 等）复用或仿飞书 `feishu-*` 样式块，照抄改名。

- [ ] **Step 5: i18n 文案（中英）**

`web/src/i18n/locales/zh/apps/root.ts` 的 `channels` 节点补：

```ts
        workWechatSubmit: '提交并连接',
        workWechatBotIdLabel: 'Bot ID',
        workWechatBotIdPlaceholder: '企业微信后台机器人 Bot ID',
        workWechatSecretLabel: 'Secret',
        workWechatSecretPlaceholder: '机器人 Secret（仅保存，不回显）',
        workWechatGuide: '在企业微信「管理后台 → 工作台 → 智能机器人 → 创建 → API 接收消息（长连接）」中获取 Bot ID 与 Secret 后填入。',
        workWechatBoundHint: '企业微信已连接，机器人正在接收消息。',
```

`web/src/i18n/locales/en/apps/root.ts` 对应英文：

```ts
        workWechatSubmit: 'Submit & connect',
        workWechatBotIdLabel: 'Bot ID',
        workWechatBotIdPlaceholder: 'Bot ID from WeCom admin console',
        workWechatSecretLabel: 'Secret',
        workWechatSecretPlaceholder: 'Bot Secret (stored only, never shown)',
        workWechatGuide: 'In WeCom admin console: Workbench → AI Bot → Create → API mode (long connection), then copy the Bot ID and Secret here.',
        workWechatBoundHint: 'WeCom connected; the bot is receiving messages.',
```

> `channelWorkWechat`（渠道卡片名）中英文案 spec 称已存在；若缺则一并补。

- [ ] **Step 6: 前端构建/类型检查**

Run: `cd web && npm run build`（或 `npm run type-check` + lint）
Expected: 构建通过，无类型/模板错误。

- [ ] **Step 7: 提交**

```bash
git add web/src/pages/apps/AppChannelsTab.vue web/src/i18n/locales/zh/apps/root.ts web/src/i18n/locales/en/apps/root.ts
git commit -m "feat(channel): 前端企业微信手填表单与连通状态展示"
```

---

## Task 15：浏览器端到端验证（CLAUDE.md 硬性要求）

**Files:** 无（真实环境验证）

凭 `make local-build` 部署最新 manager + 两 variant hermes 镜像后，用真实浏览器（非 curl）逐项验证。需用户提供真实企业微信 `bot_id`/`secret`。

- [ ] **Step 1: 三角色权限**

platform_admin / org_admin / org_member 分别进入某 app 渠道页：org_member（非 owner）发起应被拒（前端禁用或 403）；org_admin / owner 可发起。

- [ ] **Step 2: 正常绑定连通**

org_admin 填真实 `bot_id`+`secret` → 提交 → 实例进入 `restarting`（发起按钮禁用 + 「重启中」提示）→ pod 重启起来后轮询连通 → 状态变「已连接」。在企业微信里给机器人发消息验证有回复。

- [ ] **Step 3: 凭证错误 fatal 路径**

故意填错 `secret` → 提交 → 轮询应展示失败原因（来自 `platforms.wecom` 的 `error_message`），不是一直转圈。

- [ ] **Step 4: 解绑**

解绑 → 实例 `restarting` → 收敛回 `running` → 企业微信机器人不再收到回复（`wecom-*` key 已删、引擎不再装载）。

- [ ] **Step 5: 与微信/飞书并存**

同一 app 同时绑定微信（或飞书）+ 企业微信，两渠道各自在线、互不干扰。

- [ ] **Step 6: 记录验证矩阵**

按「逐文件/逐场景矩阵 + 证据（截图/日志）」在交付说明里列出结果。发现问题先修复再重验，直至全绿。

---

## 自检（写完计划后回看 spec）

- **Spec 覆盖**：spec 第 2 节（migration/枚举）→ Task 1-2；第 3 节（env 注入）→ Task 3-4；第 4 节（restarting）→ Task 7-8；第 5 节（绑定流程/adapter/通用 check）→ Task 5-9；第 6 节（oc-ops health）→ Task 5、11；第 7 节（解绑）→ Task 8；第 8 节（前端）→ Task 13-14；第 9 节（并存）→ Task 2 + Task 15 Step 5；第 10 节（测试）→ 各任务 TDD + Task 15；第 11 节（YAGNI：不暴露 websocket_url、不做扫码 adapter SSE）→ 已落到设计取舍。✓
- **类型一致**：`WorkWeChatBotID`/`WorkWeChatSecret`（AppSpec）、`wecom-bot-id`/`wecom-secret`（Secret key）、`WECOM_BOT_ID`/`WECOM_SECRET`（env）、`work_wechat`（manager 枚举 + oc-ops 注册键）、`platforms.wecom`（引擎平台名）——全计划一致。`BeginWorkWechatAuth`/`WorkWechatAuthInput`/`WorkWechatChannelAuthRequest`/`useBeginWorkWechatAuth` 命名贯穿一致。✓
- **无 placeholder**：每个 Go 任务给出可编译代码；前端大组件给出净增片段 + 飞书锚点（活模板）。Task 4/7/8/9 的 `_test.go` 装配以「仿飞书测试 fake」描述（仓库有活模板），非空泛 TODO。✓
- **Task 0 gate**：引擎契约不符时停手，避免按错误假设全量实现。✓
