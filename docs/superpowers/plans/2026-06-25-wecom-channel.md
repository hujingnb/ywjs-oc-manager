# 企业微信渠道（智能机器人 AI Bot）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 app 实例新增「企业微信」渠道（智能机器人 AI Bot 长连接），用户手动填 bot_id+secret，manager 加密落库、注入 hermes 容器 env、重启生效、即时验证连通；可与微信渠道并存。

**Architecture:** 复用 `channel_bindings` 表与 `status` 状态机，但企业微信不走微信的扫码 SSE adapter，而是「配置注入 + 健康探测」独立路径：service 加密 secret 写 DB → worker 解密后 patch per-app k8s Secret（`wecom-bot-id`/`wecom-secret` key）→ RolloutRestart → hermes 读 optional `WECOM_*` env 启用 `Platform.WECOM` → worker 经 oc-ops 转发 hermes `/health/detailed` 读 `platforms.wecom.platform_state` 判定 bound/failed。hermes 引擎零改动。

**Tech Stack:** Go（gin/sqlc/testify/client-go）、MySQL migration、Vue3+TS（vue-query）、Python（oc-ops starlette，两个 hermes variant）、AES-256-GCM（auth.Cipher）。

**设计依据：** `docs/superpowers/specs/2026-06-25-wecom-channel-design.md`

---

## 关键约定（先读）

- **渠道标识**：manager 侧 `channel_type = "work_wechat"`；hermes 平台键 `wecom`；env `WECOM_BOT_ID`/`WECOM_SECRET`/可选 `WECOM_WEBSOCKET_URL`。三者不要混用。
- **secret 流转**：明文只在「用户提交 HTTP 请求」与「worker patch k8s Secret」两个瞬间存在；DB 存 `auth.Cipher` 密文；job payload **不带**明文 secret（worker 从 DB 读密文解密）。
- **每个 Task 自带 TDD：先写失败测试 → 跑挂 → 最小实现 → 跑过 → 提交。** Go 测试用 testify（`require`/`assert`，expected 在前），每个测试方法/子测试/表驱动用例配中文场景注释。
- **生成产物**：改 handler 签名/请求体/响应/路由后必须 `make openapi-gen` + `make web-types-gen`（Task 17），不要手改 `openapi/openapi.yaml` 与 `web/src/api/generated.ts`。
- 所有 Go 测试运行：`go test ./internal/...`；单个包示例在各 Task 内给出。

---

## Task 1: 数据模型 migration（放宽约束 + 唯一键加 channel_type + 回填存量）

**Files:**
- Create: `internal/migrations/000002_wecom_channel.up.sql`
- Create: `internal/migrations/000002_wecom_channel.down.sql`

> 注意：不改 `000001_baseline.up.sql`（baseline 已应用，改它无效）。新建一对 up/down。
> 现状：`uk_channel_bindings_app_active (app_active_key)` 使一个 app 仅一条非 deleted 绑定；`app_active_key = CASE WHEN status<>'deleted' THEN app_id END`（生成列）。CHECK 仅允许 `'wechat'`。

- [ ] **Step 1: 写 up migration**

```sql
-- 000002_wecom_channel.up.sql
-- 企业微信渠道：放宽 channel_type CHECK、唯一键加 channel_type 支持渠道并存、回填存量 app 的 work_wechat unbound 记录。

-- 1) 放宽渠道类型 CHECK：新增 work_wechat。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check;
ALTER TABLE channel_bindings
    ADD CONSTRAINT channel_bindings_channel_type_check
    CHECK (channel_type IN ('wechat', 'work_wechat'));

-- 2) 唯一键由 (app_active_key) 改为 (app_active_key, channel_type)，
--    使同一 app 的 wechat 与 work_wechat 可各有一条非 deleted 绑定。
ALTER TABLE channel_bindings
    DROP INDEX uk_channel_bindings_app_active;
ALTER TABLE channel_bindings
    ADD UNIQUE KEY uk_channel_bindings_app_active (app_active_key, channel_type);

-- 3) 给所有未删除且尚无 work_wechat 绑定的 app 回填一条 unbound 记录，
--    使 PollAuth/BeginAuth 对 work_wechat 与微信完全对称（记录恒存在）。
--    UUID 用 MySQL UUID() 生成；时间用 UTC_TIMESTAMP(6)（app DB 会话固定 UTC，迁移命令走 SYSTEM 时区，必须显式 UTC）。
INSERT INTO channel_bindings (id, app_id, channel_type, status, created_at, updated_at)
SELECT UUID(), a.id, 'work_wechat', 'unbound', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)
FROM apps a
WHERE a.status <> 'deleted'
  AND NOT EXISTS (
      SELECT 1 FROM channel_bindings cb
      WHERE cb.app_id = a.id AND cb.channel_type = 'work_wechat' AND cb.status <> 'deleted'
  );
```

- [ ] **Step 2: 写 down migration**

```sql
-- 000002_wecom_channel.down.sql
-- 回滚：删 work_wechat 记录、还原唯一键与 CHECK。

DELETE FROM channel_bindings WHERE channel_type = 'work_wechat';

ALTER TABLE channel_bindings
    DROP INDEX uk_channel_bindings_app_active;
ALTER TABLE channel_bindings
    ADD UNIQUE KEY uk_channel_bindings_app_active (app_active_key);

ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check;
ALTER TABLE channel_bindings
    ADD CONSTRAINT channel_bindings_channel_type_check
    CHECK (channel_type IN ('wechat'));
```

- [ ] **Step 3: 本地应用 migration 验证**

Run: `make migrate-up`（或项目既有 migration 命令；若不确定查 `Makefile` 中 migrate 目标）
Expected: 无报错；`SELECT DISTINCT channel_type FROM channel_bindings;` 含 `work_wechat`。

- [ ] **Step 4: 验证唯一键允许并存、禁止重复**

Run（mysql 客户端，替换 `<APP>` 为已有 app id）:
```sql
-- 同 app 已有 wechat + work_wechat 各一条 → 正常；再插一条重复 work_wechat 应报 Duplicate。
INSERT INTO channel_bindings (id, app_id, channel_type, status) VALUES (UUID(), '<APP>', 'work_wechat', 'unbound');
```
Expected: 报 `Duplicate entry` for key `uk_channel_bindings_app_active`。

- [ ] **Step 5: Commit**

```bash
git add internal/migrations/000002_wecom_channel.up.sql internal/migrations/000002_wecom_channel.down.sql
git commit -m "feat(channel): 企业微信渠道 migration 放宽约束并回填 work_wechat 绑定

放宽 channel_bindings.channel_type CHECK 增加 work_wechat；唯一键加
channel_type 支持微信与企业微信并存；给存量未删除 app 回填 work_wechat
unbound 记录，使 PollAuth/BeginAuth 与微信对称。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: domain 枚举（渠道类型 + job 类型）

**Files:**
- Modify: `internal/domain/enums.go`
- Test: `internal/domain/enums_test.go`（若不存在则创建）

- [ ] **Step 1: 写失败测试**

```go
// internal/domain/enums_test.go
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 验证企业微信渠道类型常量值固定为 work_wechat（与前端/DB CHECK 对齐，不可随意改名）。
func TestChannelTypeWorkWeChat(t *testing.T) {
	assert.Equal(t, "work_wechat", ChannelTypeWorkWeChat)
}

// 验证企业微信两个 job 类型已登记到 validJobTypes，否则调度系统会拒绝入队。
func TestWeComJobTypesRegistered(t *testing.T) {
	assert.True(t, IsJobType(JobTypeChannelConfigureWeCom)) // 配置注入 job 已登记
	assert.True(t, IsJobType(JobTypeChannelCheckWeCom))     // 连通探测 job 已登记
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/domain/ -run 'WorkWeChat|WeComJob' -v`
Expected: 编译失败（未定义 `ChannelTypeWorkWeChat` 等）。

- [ ] **Step 3: 实现**

在 `enums.go` 渠道常量块（`ChannelTypeWeChat` 行下）加：
```go
	// ChannelTypeWorkWeChat 是企业微信渠道（智能机器人 AI Bot 长连接）。
	ChannelTypeWorkWeChat = "work_wechat"
```

在 job 类型常量块（`JobTypeChannelCheckBinding` 行下）加：
```go
	// JobTypeChannelConfigureWeCom 把用户提交的企业微信 bot_id/secret 注入 hermes 容器并重启。
	JobTypeChannelConfigureWeCom = "channel_configure_wecom"
	// JobTypeChannelCheckWeCom 经 oc-ops 探测 hermes 企业微信平台连通状态，推进 bound/failed。
	JobTypeChannelCheckWeCom = "channel_check_wecom"
```

在 `validJobTypes = set(...)` 列表内加这两个常量：
```go
		JobTypeChannelConfigureWeCom,
		JobTypeChannelCheckWeCom,
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/domain/ -run 'WorkWeChat|WeComJob' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/enums.go internal/domain/enums_test.go
git commit -m "feat(channel): 新增企业微信渠道类型与配置/探测 job 枚举

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: sqlc 查询（读 metadata + upsert 兜底）

**Files:**
- Modify: `internal/store/queries/channel_bindings.sql`
- 生成：运行 sqlc 后 `internal/store/sqlc/*.go` 更新

> 现有 query 已覆盖 `GetChannelBindingByAppAndType`/`SetChannelBindingChallenge`/`MarkChannelBindingBound`/`SetChannelBindingStatus`。企业微信复用它们写 metadata 与状态。仅需补一条「确保记录存在」的 upsert，兜底回填 migration 之后才创建的 app（onboarding 已预置，见 Task 4，但 upsert 保证幂等安全）。

- [ ] **Step 1: 追加 query**

在 `internal/store/queries/channel_bindings.sql` 末尾加：
```sql
-- name: EnsureChannelBinding :exec
-- 确保指定 app+channel 存在一条非 deleted 绑定（幂等）。
-- 企业微信配置入口调用，兜底 onboarding 预置缺失（如存量 app 边界）。
INSERT INTO channel_bindings (id, app_id, channel_type, status)
VALUES (?, ?, ?, 'unbound')
ON DUPLICATE KEY UPDATE id = id;
```

> 说明：`ON DUPLICATE KEY UPDATE id = id` 是 no-op，仅借唯一键 `(app_active_key, channel_type)` 实现「存在即跳过」。

- [ ] **Step 2: 重新生成 sqlc**

Run: `make sqlc-gen`（或 `sqlc generate`；查 Makefile 确认目标名）
Expected: `internal/store/sqlc/` 出现 `EnsureChannelBinding` 方法，无报错。

- [ ] **Step 3: 验证生成产物编译**

Run: `go build ./internal/store/...`
Expected: 编译通过。

- [ ] **Step 4: Commit**

```bash
git add internal/store/queries/channel_bindings.sql internal/store/sqlc/
git commit -m "feat(channel): 新增 EnsureChannelBinding 幂等 upsert query

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: onboarding 预置 work_wechat 绑定记录

**Files:**
- Modify: `internal/service/onboarding_service.go`（两处 `CreateChannelBinding` 调用点：约 190、378 行）
- Test: `internal/service/onboarding_service_test.go`（在既有测试文件追加用例）

> 现状：onboarding 为新 app 创建一条 wechat unbound 记录。企业微信并存需同时创建 work_wechat unbound 记录。

- [ ] **Step 1: 写失败测试**

在 `onboarding_service_test.go` 追加（沿用文件内既有 onboarding 测试的 store mock 与构造方式；断言创建后两种渠道记录都存在）：
```go
// 验证 onboarding 为新 app 同时预置 wechat 与 work_wechat 两条 unbound 渠道绑定，
// 保证企业微信渠道的 PollAuth/BeginAuth 与微信对称（记录恒存在）。
func TestOnboardingCreatesBothChannelBindings(t *testing.T) {
	// 用文件内既有的 onboarding 测试夹具（fake store / 输入）完成一次 onboarding，
	// 然后断言 fake store 收到两次 CreateChannelBinding，channel_type 覆盖
	// {wechat, work_wechat}，status 均为 unbound。
	// （具体夹具沿用同文件其他用例的 newOnboardingTestStore() 等 helper。）
	gotTypes := capturedChannelTypes // 由 fake store 记录每次 CreateChannelBinding 的 channel_type
	assert.ElementsMatch(t, []string{"wechat", "work_wechat"}, gotTypes)
}
```

> 实现者注意：按同文件既有 fake store 的写法，让其 `CreateChannelBinding` 累积 `arg.ChannelType` 到切片再断言。若文件无现成 fake，复用同文件其它 service 测试的 mock 模式。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestOnboardingCreatesBothChannelBindings -v`
Expected: FAIL（只创建了 wechat 一条）。

- [ ] **Step 3: 实现**

在两处 `CreateChannelBinding(wechat unbound)` 调用之后，各追加一条 work_wechat：
```go
			// 企业微信渠道与微信对称预置 unbound 记录，支持两渠道并存。
			if err := store.CreateChannelBinding(ctx, sqlc.CreateChannelBindingParams{
				ID:          newUUID(),
				AppID:       appID,
				ChannelType: domain.ChannelTypeWorkWeChat,
				Status:      domain.ChannelStatusUnbound,
			}); err != nil {
				return fmt.Errorf("创建企业微信渠道绑定失败: %w", err)
			}
```

> 第二处（378 行附近）用该上下文的 app id 变量名（确认局部变量名后替换 `appID`）。`newUUID()` 用 service 包内既有 helper（与现有 `channelBindingID` 生成方式一致）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -run TestOnboardingCreatesBothChannelBindings -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/onboarding_service.go internal/service/onboarding_service_test.go
git commit -m "feat(channel): onboarding 为新 app 预置企业微信 unbound 绑定

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: k8s 渲染 —— AppSpec 加 WeCom 字段 + optional env + RenderSecret 带出

**Files:**
- Modify: `internal/integrations/k8sorch/orchestrator.go`（AppSpec 加字段）
- Modify: `internal/integrations/k8sorch/render.go`（RenderSecret 带 wecom key、hermes 容器加 optional env）
- Test: `internal/integrations/k8sorch/render_test.go`（若不存在则创建）

- [ ] **Step 1: 写失败测试**

```go
// internal/integrations/k8sorch/render_test.go
package k8sorch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// hermes 容器必须始终带 WECOM_BOT_ID/WECOM_SECRET 两条 optional SecretKeyRef env，
// 未绑定时 Secret 无对应 key、env 不注入，hermes 不启用企业微信平台。
func TestRenderDeploymentHasOptionalWeComEnv(t *testing.T) {
	dep := RenderDeployment(AppSpec{AppID: "app1", HermesImage: "img", OpsImage: "ops", ControlToken: "t",
		Resources: ResourceLimits{RequestsCPU: "100m", RequestsMemory: "128Mi", LimitsCPU: "500m", LimitsMemory: "512Mi"}}, "ns")
	var hermes corev1.Container
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == "hermes" {
			hermes = c
		}
	}
	envByName := map[string]corev1.EnvVar{}
	for _, e := range hermes.Env {
		envByName[e.Name] = e
	}
	bot, ok := envByName["WECOM_BOT_ID"]
	require.True(t, ok, "应注入 WECOM_BOT_ID env")
	require.NotNil(t, bot.ValueFrom)
	require.NotNil(t, bot.ValueFrom.SecretKeyRef)
	assert.Equal(t, "wecom-bot-id", bot.ValueFrom.SecretKeyRef.Key)             // 引用 Secret 的 wecom-bot-id key
	require.NotNil(t, bot.ValueFrom.SecretKeyRef.Optional)
	assert.True(t, *bot.ValueFrom.SecretKeyRef.Optional)                        // optional=true：缺 key 时不注入
	assert.Equal(t, "app-app1-token", bot.ValueFrom.SecretKeyRef.Name)         // 复用 per-app token Secret
}

// 已绑定企业微信时，RenderSecret 把 bot_id/secret 写进 Secret，保证 app 重建不丢配置。
func TestRenderSecretCarriesWeCom(t *testing.T) {
	sec := RenderSecret(AppSpec{AppID: "app1", ControlToken: "t", WeComBotID: "bid", WeComSecret: "sec"}, "ns")
	assert.Equal(t, "bid", sec.StringData["wecom-bot-id"]) // 带出 bot_id
	assert.Equal(t, "sec", sec.StringData["wecom-secret"]) // 带出 secret
}

// 未绑定（WeCom 字段为空）时，RenderSecret 不写 wecom key，避免注入空凭证误启用平台。
func TestRenderSecretOmitsEmptyWeCom(t *testing.T) {
	sec := RenderSecret(AppSpec{AppID: "app1", ControlToken: "t"}, "ns")
	_, hasBot := sec.StringData["wecom-bot-id"]
	_, hasSec := sec.StringData["wecom-secret"]
	assert.False(t, hasBot) // 空配置不写 key
	assert.False(t, hasSec)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run 'WeCom' -v`
Expected: 编译失败（AppSpec 无 WeComBotID 字段）。

- [ ] **Step 3: 实现**

`orchestrator.go` 的 `AppSpec` struct 末尾加字段：
```go
	// WeComBotID / WeComSecret 是企业微信智能机器人凭证；非空时 RenderSecret 写入
	// per-app Secret 的 wecom-bot-id / wecom-secret key，供 hermes 容器 optional env 注入。
	// 留空表示未绑定企业微信，Secret 不写对应 key、hermes 不启用 Platform.WECOM。
	WeComBotID string
	WeComSecret string
```

`render.go` 的 `RenderSecret` 改为按需带出 wecom：
```go
func RenderSecret(spec AppSpec, namespace string) *corev1.Secret {
	data := map[string]string{"control-token": spec.ControlToken}
	// 已绑定企业微信时带出凭证，保证 EnsureApp 重建 Secret（镜像升级等）不丢配置。
	if spec.WeComBotID != "" && spec.WeComSecret != "" {
		data["wecom-bot-id"] = spec.WeComBotID
		data["wecom-secret"] = spec.WeComSecret
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
}
```

`render.go` 的 `RenderDeployment` 中 hermes 容器 env（113-117 行 append 块）加两条 optional env。先在函数内定义 helper（紧邻 `ctrlTokenEnv` 定义处）：
```go
	// wecomEnv 注入企业微信凭证：引用 per-app Secret 的 wecom-* key，optional=true 表示
	// 未绑定（Secret 无此 key）时 env 不存在，hermes config 读不到 WECOM_BOT_ID 即不启用平台。
	optTrue := true
	wecomBotEnv := corev1.EnvVar{Name: "WECOM_BOT_ID", ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)},
			Key:                  "wecom-bot-id",
			Optional:             &optTrue,
		},
	}}
	wecomSecretEnv := corev1.EnvVar{Name: "WECOM_SECRET", ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)},
			Key:                  "wecom-secret",
			Optional:             &optTrue,
		},
	}}
```

把 hermes 容器 Env append 改为包含这两条（在现有 `proxyEnv...` 之后）：
```go
							Env: append([]corev1.EnvVar{
								{Name: "HERMES_HOME", Value: "/opt/data"},
								{Name: "API_SERVER_ENABLED", Value: "true"},
								{Name: "API_SERVER_KEY", ValueFrom: ctrlTokenEnv.ValueFrom},
								wecomBotEnv,
								wecomSecretEnv,
							}, proxyEnv...),
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -run 'WeCom' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/render.go internal/integrations/k8sorch/render_test.go
git commit -m "feat(channel): hermes 容器注入企业微信 optional env 并由 Secret 带出

AppSpec 增加 WeCom 凭证字段；RenderSecret 非空时写 wecom-bot-id/wecom-secret
key；hermes 容器永久挂 WECOM_BOT_ID/WECOM_SECRET 两条 optional SecretKeyRef
env，未绑定时不注入、不启用企业微信平台。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Orchestrator 接口 + k8s 实现 —— patch/清除 WeCom Secret

**Files:**
- Modify: `internal/integrations/k8sorch/orchestrator.go`（接口加方法）
- Modify: `internal/integrations/k8sorch/adapter.go`（KubernetesAdapter 实现）
- Test: `internal/integrations/k8sorch/adapter_test.go`（用 `k8s.io/client-go/kubernetes/fake`，沿用包内既有 fake client 测试模式；若无则创建）

- [ ] **Step 1: 写失败测试**

```go
// internal/integrations/k8sorch/adapter_wecom_test.go
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

// SetWeComSecret 把 bot_id/secret patch 进既有 per-app Secret，不动 control-token。
func TestSetWeComSecret(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-app1-token", Namespace: "ns"},
		Data:       map[string][]byte{"control-token": []byte("t")},
	})
	a := &KubernetesAdapter{client: cs, namespace: "ns"} // 字段名以 adapter.go 实际为准
	require.NoError(t, a.SetWeComSecret(context.Background(), "app1", "bid", "sec"))
	got, err := cs.CoreV1().Secrets("ns").Get(context.Background(), "app-app1-token", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "bid", string(got.Data["wecom-bot-id"]))
	assert.Equal(t, "sec", string(got.Data["wecom-secret"]))
	assert.Equal(t, "t", string(got.Data["control-token"])) // control-token 不受影响
}

// ClearWeComSecret 删除 wecom-* key，保留 control-token（解绑用）。
func TestClearWeComSecret(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-app1-token", Namespace: "ns"},
		Data: map[string][]byte{"control-token": []byte("t"), "wecom-bot-id": []byte("b"), "wecom-secret": []byte("s")},
	})
	a := &KubernetesAdapter{client: cs, namespace: "ns"}
	require.NoError(t, a.ClearWeComSecret(context.Background(), "app1"))
	got, _ := cs.CoreV1().Secrets("ns").Get(context.Background(), "app-app1-token", metav1.GetOptions{})
	_, hasBot := got.Data["wecom-bot-id"]
	assert.False(t, hasBot)                              // wecom key 已删
	assert.Equal(t, "t", string(got.Data["control-token"])) // control-token 保留
}
```

> 实现者：先看 `adapter.go` 中 `KubernetesAdapter` 的实际字段名（client / clientset、namespace）并对齐测试。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run 'WeComSecret' -v`
Expected: 编译失败（无 `SetWeComSecret`）。

- [ ] **Step 3: 实现**

`orchestrator.go` 的 `Orchestrator` 接口加：
```go
	// SetWeComSecret patch per-app Secret 写入 wecom-bot-id/wecom-secret key（不动 control-token）。
	SetWeComSecret(ctx context.Context, appID, botID, secret string) error
	// ClearWeComSecret 删除 per-app Secret 的 wecom-* key（解绑用，保留 control-token）。
	ClearWeComSecret(ctx context.Context, appID string) error
```

`adapter.go` 加实现（沿用文件内既有 `retry.RetryOnConflict` + `secretName` 模式；`secretName` 在 render.go 已定义）：
```go
// SetWeComSecret 把企业微信凭证写入 per-app Secret。
func (a *KubernetesAdapter) SetWeComSecret(ctx context.Context, appID, botID, secret string) error {
	api := a.client.CoreV1().Secrets(a.namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		s, err := api.Get(ctx, secretName(appID), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if s.Data == nil {
			s.Data = map[string][]byte{}
		}
		s.Data["wecom-bot-id"] = []byte(botID)
		s.Data["wecom-secret"] = []byte(secret)
		_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
		return uerr
	})
	return wrapK8s("写入企业微信 Secret", err)
}

// ClearWeComSecret 删除 per-app Secret 的企业微信凭证 key。
func (a *KubernetesAdapter) ClearWeComSecret(ctx context.Context, appID string) error {
	api := a.client.CoreV1().Secrets(a.namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		s, err := api.Get(ctx, secretName(appID), metav1.GetOptions{})
		if err != nil {
			return err
		}
		delete(s.Data, "wecom-bot-id")
		delete(s.Data, "wecom-secret")
		_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
		return uerr
	})
	return wrapK8s("清除企业微信 Secret", err)
}
```

> `wrapK8s` 是 adapter.go 内既有错误包装 helper（见 RolloutRestart）。`metav1`/`retry` import 已在 adapter.go 存在。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -run 'WeComSecret' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/adapter.go internal/integrations/k8sorch/adapter_wecom_test.go
git commit -m "feat(channel): Orchestrator 增加企业微信 Secret patch/清除能力

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: app_initialize 重建时从 DB 带出 WeCom 凭证

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`（`buildAppSpec`）
- Test: `internal/worker/handlers/app_initialize_test.go`（追加用例）

> 目的：EnsureApp 重建 Secret（镜像升级/重渲染）时不丢企业微信配置。`buildAppSpec` 需查 work_wechat 绑定、解密 secret、填进 AppSpec.WeCom*。

- [ ] **Step 1: 写失败测试**

```go
// 已绑定企业微信的 app，buildAppSpec 应从 channel_bindings 解密带出 WeCom 凭证，
// 使 EnsureApp 重建 Secret 时不丢配置。
func TestBuildAppSpecCarriesWeCom(t *testing.T) {
	// 构造 fake store：GetChannelBindingByAppAndType(work_wechat) 返回 status=bound、
	// metadata_json = {"bot_id":"bid","secret_ciphertext":"<cipher>"}；handler 注入 cipher。
	// 断言 spec.WeComBotID=="bid" 且 spec.WeComSecret 解密后=="sec"。
	// （沿用同文件既有 AppInitializeHandler 测试夹具与 cipher 构造方式。）
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestBuildAppSpecCarriesWeCom -v`
Expected: FAIL（spec.WeComBotID 为空）。

- [ ] **Step 3: 实现**

确认 `AppInitializeHandler` 是否已有 cipher 与 channel store 依赖；若无，给 handler 加 `cipher *auth.Cipher` 字段与 `GetChannelBindingByAppAndType` 访问（store 接口加该方法）。`buildAppSpec` 改为方法内查 work_wechat 绑定并解密：
```go
	spec := k8sorch.AppSpec{ /* 现有字段保持不变 */ }
	// 已绑定企业微信时带出凭证，保证 EnsureApp 重建 Secret 不丢配置。
	if b, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID: app.ID, ChannelType: domain.ChannelTypeWorkWeChat,
	}); err == nil && b.Status == domain.ChannelStatusBound {
		if botID, secret, derr := decodeWeComCredentials(h.cipher, b.MetadataJson); derr == nil {
			spec.WeComBotID = botID
			spec.WeComSecret = secret
		}
	}
	return spec
```

> `buildAppSpec` 需要 `ctx`；若当前签名无 ctx，改为 `buildAppSpec(ctx, app, ...)` 并更新调用点（364 行）。`decodeWeComCredentials` 见 Task 8（放共享位置，如 `internal/service` 或 `internal/integrations/channel` 的 wecom 包；若 worker 不便依赖 service，复制一份小 helper 到 worker 包并加注释说明同源）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/worker/handlers/ -run TestBuildAppSpecCarriesWeCom -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -m "feat(channel): app 重建时从 DB 解密带出企业微信凭证

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: WeCom 凭证编解码 helper + DTO

**Files:**
- Create: `internal/integrations/channel/wecom.go`（凭证 metadata 编解码，纯函数，无 k8s/store 依赖）
- Modify: `internal/api/handlers/dto.go`（请求体）
- Test: `internal/integrations/channel/wecom_test.go`

> `metadata_json` 形如 `{"bot_id":"...","secret_ciphertext":"...","websocket_url":"..."}`。提供 encode/decode helper，service 与 worker 共用。

- [ ] **Step 1: 写失败测试**

```go
// internal/integrations/channel/wecom_test.go
package channel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
)

// 加密编码后再解码应还原 bot_id/secret/websocket_url，secret 在 JSON 中以密文存储。
func TestWeComCredentialRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	cipher, err := auth.NewCipher(key)
	require.NoError(t, err)
	raw, err := EncodeWeComCredentials(cipher, "bid", "sec", "wss://x")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "sec") // 明文 secret 不出现在 metadata
	botID, secret, wsURL, err := DecodeWeComCredentials(cipher, raw)
	require.NoError(t, err)
	assert.Equal(t, "bid", botID)
	assert.Equal(t, "sec", secret)
	assert.Equal(t, "wss://x", wsURL)
}

// 解码非法/空 metadata 应返回错误，避免静默注入空凭证。
func TestDecodeWeComCredentialsRejectsEmpty(t *testing.T) {
	key := make([]byte, 32)
	cipher, _ := auth.NewCipher(key)
	_, _, _, err := DecodeWeComCredentials(cipher, []byte(`{}`))
	assert.Error(t, err)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/channel/ -run WeComCredential -v`
Expected: 编译失败（无 `EncodeWeComCredentials`）。

- [ ] **Step 3: 实现**

```go
// internal/integrations/channel/wecom.go
// Package channel：企业微信渠道凭证在 channel_bindings.metadata_json 中的编解码。
// secret 用 auth.Cipher 加密后存密文；bot_id/websocket_url 明文（非敏感）。
// service（写入）与 worker（注入容器）共用，保证两侧格式一致。
package channel

import (
	"encoding/json"
	"errors"
	"fmt"

	"oc-manager/internal/auth"
)

// wecomCredentials 是 metadata_json 中企业微信配置的内部形状。
type wecomCredentials struct {
	BotID            string `json:"bot_id"`
	SecretCiphertext string `json:"secret_ciphertext"`
	WebsocketURL     string `json:"websocket_url,omitempty"`
}

// EncodeWeComCredentials 把 bot_id/secret/websocket_url 编码为 metadata_json 字节，
// secret 经 cipher 加密为密文，明文绝不落库。
func EncodeWeComCredentials(cipher *auth.Cipher, botID, secret, websocketURL string) ([]byte, error) {
	if botID == "" || secret == "" {
		return nil, errors.New("bot_id 与 secret 不能为空")
	}
	ct, err := cipher.Encrypt([]byte(secret))
	if err != nil {
		return nil, fmt.Errorf("加密企业微信 secret 失败: %w", err)
	}
	return json.Marshal(wecomCredentials{BotID: botID, SecretCiphertext: ct, WebsocketURL: websocketURL})
}

// DecodeWeComCredentials 从 metadata_json 解出 bot_id/secret/websocket_url，secret 经解密还原。
func DecodeWeComCredentials(cipher *auth.Cipher, raw []byte) (botID, secret, websocketURL string, err error) {
	var c wecomCredentials
	if uerr := json.Unmarshal(raw, &c); uerr != nil {
		return "", "", "", fmt.Errorf("解析企业微信 metadata 失败: %w", uerr)
	}
	if c.BotID == "" || c.SecretCiphertext == "" {
		return "", "", "", errors.New("企业微信 metadata 缺少 bot_id 或 secret")
	}
	plain, derr := cipher.Decrypt(c.SecretCiphertext)
	if derr != nil {
		return "", "", "", fmt.Errorf("解密企业微信 secret 失败: %w", derr)
	}
	return c.BotID, string(plain), c.WebsocketURL, nil
}
```

> Task 7 引用的 `decodeWeComCredentials(cipher, raw)` 即此 `channel.DecodeWeComCredentials`（worker 直接调 channel 包；channel 包已被 worker import）。

`internal/api/handlers/dto.go` 加请求体：
```go
// WeComConfigRequest 是企业微信渠道配置请求体（POST .../channels/work_wechat/auth）。
type WeComConfigRequest struct {
	// BotID 是企业微信智能机器人 Bot ID（管理后台 API 模式生成）。
	BotID string `json:"bot_id" binding:"required"`
	// Secret 是机器人校验 secret；仅在请求中出现，落库前加密。
	Secret string `json:"secret" binding:"required"`
	// WebsocketURL 可选，留空走引擎默认 wss://openws.work.weixin.qq.com。
	WebsocketURL string `json:"websocket_url"`
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/channel/ -run WeComCredential -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/channel/wecom.go internal/integrations/channel/wecom_test.go internal/api/handlers/dto.go
git commit -m "feat(channel): 企业微信凭证 metadata 编解码与配置请求体

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: ChannelService.ConfigureWeCom（加密写库 + 入队配置 job）

**Files:**
- Modify: `internal/service/channel_service.go`（加 cipher 依赖 + ConfigureWeCom 方法 + store 接口加 EnsureChannelBinding）
- Test: `internal/service/channel_service_test.go`（追加用例；沿用文件内既有 fake store）

- [ ] **Step 1: 写失败测试**

```go
// ConfigureWeCom 应：校验权限 → 确保绑定存在 → 写加密 metadata + status=pending_auth →
// 入队 channel_configure_wecom job。secret 必须以密文写入 metadata。
func TestConfigureWeComEnqueuesConfigureJob(t *testing.T) {
	// 用文件内既有 fakeChannelStore（捕获 SetChannelBindingChallenge 的 MetadataJson、
	// CreateJob 的 Type）。principal 为可管理该 app 的 org_admin。
	// 断言：
	//   - 捕获到的 metadata 不含明文 "sec"（含 secret_ciphertext）
	//   - 入队 job.Type == domain.JobTypeChannelConfigureWeCom
	//   - 返回 ChallengeResult.Status == pending_auth
}

// 非法输入（bot_id 为空）应返回校验错误，不写库不入队。
func TestConfigureWeComRejectsEmptyBotID(t *testing.T) {
	// 断言返回 error，且 fake store 未收到 CreateJob。
}

// 无管理权限的主体调用应返回 ErrForbidden。
func TestConfigureWeComForbidden(t *testing.T) {
	// org_member 对他人 app 调用 → ErrForbidden。
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run ConfigureWeCom -v`
Expected: 编译失败（无 ConfigureWeCom）。

- [ ] **Step 3: 实现**

`ChannelStore` 接口加：
```go
	EnsureChannelBinding(ctx context.Context, arg sqlc.EnsureChannelBindingParams) error
	SetChannelBindingChallenge(ctx context.Context, arg sqlc.SetChannelBindingChallengeParams) error
```

`ChannelService` struct 加 `cipher *auth.Cipher` 字段，并更新 `NewChannelService` 签名接收 cipher（更新装配点 Task 12）。新增方法：
```go
// ConfigureWeCom 接收用户提交的企业微信智能机器人配置，加密 secret 写入 channel_bindings，
// 入队 channel_configure_wecom job 由 worker 注入容器并重启。不在请求线程内碰 k8s。
func (s *ChannelService) ConfigureWeCom(ctx context.Context, principal auth.Principal, appID, botID, secret, websocketURL string) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	if botID == "" || secret == "" {
		return ChallengeResult{}, fmt.Errorf("%w: bot_id 与 secret 必填", ErrInvalidInput)
	}
	if s.cipher == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	// 确保绑定记录存在（onboarding 已预置，此处兜底存量/边界）。
	if err := s.store.EnsureChannelBinding(ctx, sqlc.EnsureChannelBindingParams{
		ID: newUUID(), AppID: app.ID, ChannelType: domain.ChannelTypeWorkWeChat,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("确保企业微信绑定失败: %w", err)
	}
	metadata, err := channel.EncodeWeComCredentials(s.cipher, botID, secret, websocketURL)
	if err != nil {
		return ChallengeResult{}, err
	}
	// SetChannelBindingChallenge 把 status 置 pending_auth 并写 metadata_json。
	if err := s.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
		AppID: app.ID, ChannelType: domain.ChannelTypeWorkWeChat, MetadataJson: metadata,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("写入企业微信配置失败: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"app_id": app.ID, "channel_type": domain.ChannelTypeWorkWeChat, "requested_by": principal.UserID})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化企业微信配置任务失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID: jobID, Type: domain.JobTypeChannelConfigureWeCom, Priority: 90,
		RunAfter: time.Now(), MaxAttempts: 3, PayloadJson: payload,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建企业微信配置任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{Status: domain.ChannelStatusPendingAuth, ChannelType: domain.ChannelTypeWorkWeChat, JobID: jobID}, nil
}
```

> 若 `ErrInvalidInput` 不存在，在 service 包错误定义处新增 `var ErrInvalidInput = errors.New("输入参数非法")`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -run ConfigureWeCom -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/channel_service.go internal/service/channel_service_test.go
git commit -m "feat(channel): ChannelService 增加企业微信配置入口

加密 secret 写 channel_bindings.metadata_json，置 pending_auth，入队
channel_configure_wecom job；不在请求线程内触碰 k8s。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: handler 分流 work_wechat → ConfigureWeCom

**Files:**
- Modify: `internal/api/handlers/channels.go`（BeginAuth 分流 + channelService 接口加方法）
- Test: `internal/api/handlers/channels_test.go`（追加用例；沿用既有 handler 测试 fake）

- [ ] **Step 1: 写失败测试**

```go
// POST .../channels/work_wechat/auth 带配置体时，handler 应调 ConfigureWeCom 并回 200+challenge。
func TestBeginAuthRoutesWorkWeChatToConfigure(t *testing.T) {
	// fake channelService 记录 ConfigureWeCom 是否被调用、入参 botID/secret。
	// 发 POST body {"bot_id":"b","secret":"s"}，断言 ConfigureWeCom 被调用且响应含 challenge.status。
}

// work_wechat 缺 bot_id 时 handler 返回 400。
func TestBeginAuthWorkWeChatMissingBotID(t *testing.T) {
	// 发空 body，断言 400。
}

// wechat 渠道仍走原 BeginAuth（不解析 body），保证微信路径不回归。
func TestBeginAuthWeChatUnchanged(t *testing.T) {
	// channelType=wechat，断言调用的是 BeginAuth 而非 ConfigureWeCom。
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/handlers/ -run BeginAuth -v`
Expected: FAIL（work_wechat 仍走旧 BeginAuth）。

- [ ] **Step 3: 实现**

`channelService` 接口加：
```go
	ConfigureWeCom(ctx context.Context, principal auth.Principal, appID, botID, secret, websocketURL string) (service.ChallengeResult, error)
```

`BeginAuth` handler 分流：
```go
func (h *ChannelsHandler) BeginAuth(c *gin.Context) {
	principal := principalFromCtx(c)
	channelType := c.Param("channelType")
	// 企业微信走配置注入而非扫码：解析配置体并转 ConfigureWeCom。
	if channelType == domain.ChannelTypeWorkWeChat {
		var req WeComConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apierror.JSON(c, http.StatusBadRequest, "INVALID_INPUT", apierror.MsgChannelInvalidConfig)
			return
		}
		result, err := h.service.ConfigureWeCom(c.Request.Context(), principal, c.Param("appId"), req.BotID, req.Secret, req.WebsocketURL)
		if err != nil {
			writeChannelError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": result})
		return
	}
	result, err := h.service.BeginAuth(c.Request.Context(), principal, c.Param("appId"), channelType)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge": result})
}
```

> import `oc-manager/internal/domain`。在 `writeChannelError` 的 switch 加 `service.ErrInvalidInput → 400 INVALID_INPUT`。新增 `apierror.MsgChannelInvalidConfig`（Task 似 messages_channel.go，双语 catalog；参考既有 MsgChannel* 写法）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/handlers/ -run BeginAuth -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/channels.go internal/api/handlers/channels_test.go internal/api/apierror/messages_channel.go
git commit -m "feat(channel): handler 将企业微信配置请求分流到 ConfigureWeCom

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: oc-ops 客户端 —— 转发 hermes /health/detailed 读 wecom 状态

**Files:**
- Create: `internal/integrations/ocops/client_health.go`
- Modify: `internal/service/ocops.go`（`channelOps` 接口加方法 + resolver 包装）
- Test: `internal/integrations/ocops/client_health_test.go`（用 httptest 起假 oc-ops）

> hermes api_server `/health/detailed` 返回 `{"platforms": {"wecom": {"platform_state": "connected|disconnected|fatal", "error_message": "..."}}}`。oc-ops 侧新增转发路由（Task 13）路径定为 `/oc/channels/{channel}/status`，对 work_wechat 返回 `{"channel":"work_wechat","state":"connected","error_message":""}`。

- [ ] **Step 1: 写失败测试**

```go
// internal/integrations/ocops/client_health_test.go
package ocops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ChannelState 解析 oc-ops /oc/channels/work_wechat/status 的连通状态。
func TestChannelState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oc/channels/work_wechat/status", r.URL.Path)
		_, _ = w.Write([]byte(`{"channel":"work_wechat","state":"connected","error_message":""}`))
	}))
	defer srv.Close()
	c := NewClient(http.DefaultClient)
	st, err := c.ChannelState(context.Background(), Endpoint{BaseURL: srv.URL}, "work_wechat")
	require.NoError(t, err)
	assert.Equal(t, "connected", st.State)
}
```

> `Endpoint` 字段名以 `client.go:19` 实际为准（如 `BaseURL`/`Token`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/ocops/ -run ChannelState -v`
Expected: 编译失败（无 ChannelState）。

- [ ] **Step 3: 实现**

```go
// internal/integrations/ocops/client_health.go
// client_health.go — 渠道连通状态查询（企业微信经 oc-ops 转发 hermes /health/detailed）。
package ocops

import (
	"context"
	"fmt"
	"net/http"
)

// ChannelStateResult 是 /oc/channels/{channel}/status 响应：渠道在 hermes 内的连通状态。
type ChannelStateResult struct {
	// Channel 是渠道键，如 work_wechat。
	Channel string `json:"channel"`
	// State 取值 connected / disconnected / fatal / unknown。
	State string `json:"state"`
	// ErrorMessage 是 fatal 时的具体原因（如 secret 无效）。
	ErrorMessage string `json:"error_message"`
}

// ChannelState 查询指定渠道在实例内的连通状态。
func (c *Client) ChannelState(ctx context.Context, ep Endpoint, channel string) (ChannelStateResult, error) {
	var out ChannelStateResult
	err := c.DoJSON(ctx, ep, http.MethodGet, fmt.Sprintf("/oc/channels/%s/status", channel), nil, &out)
	return out, err
}
```

`internal/service/ocops.go` 的 `channelOps` 接口加 `ChannelState(...)`，并在 resolver 包装侧透出（沿用既有 `configOps`/`channelOps` 装配方式）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/ocops/ -run ChannelState -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/ocops/client_health.go internal/service/ocops.go
git commit -m "feat(channel): oc-ops 客户端查询企业微信连通状态

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: worker —— WeCom 配置 handler + 连通探测 handler

**Files:**
- Create: `internal/worker/handlers/channel_wecom.go`
- Test: `internal/worker/handlers/channel_wecom_test.go`

> 两个 handler：
> - `WeComConfigureHandler`（`channel_configure_wecom`）：读 binding metadata → 解密 → `orch.SetWeComSecret` → `orch.RolloutRestart` → 入队 `channel_check_wecom`(delay 8s)。
> - `WeComCheckHandler`（`channel_check_wecom`）：`ocops.ChannelState` → connected→`MarkChannelBindingBound`(+app running) / fatal→`SetChannelBindingStatus(failed, errMsg)` / 其它→重试到上限后 failed。

- [ ] **Step 1: 写失败测试**

```go
// 配置 handler：解密 metadata 后 patch Secret 并重启，入队探测 job。
func TestWeComConfigureHandler(t *testing.T) {
	// fake store 返回 work_wechat binding（metadata 为 EncodeWeComCredentials 产物）；
	// fake orch 记录 SetWeComSecret(botID,secret) 与 RestartApp 调用；
	// 断言 secret 解密正确传入、入队了 channel_check_wecom。
}

// 探测 handler：connected → 标记 bound 且 app 推进 running。
func TestWeComCheckHandlerConnected(t *testing.T) {
	// fake ocops.ChannelState 返回 state=connected；
	// 断言 MarkChannelBindingBound 被调用、app status→running。
}

// 探测 handler：fatal → failed 并写 error_message。
func TestWeComCheckHandlerFatal(t *testing.T) {
	// state=fatal, error_message="invalid secret"；
	// 断言 SetChannelBindingStatus(failed) 且 last_error 含安全错误文本。
}

// 探测 handler：disconnected 未到上限 → 重新入队探测（不终结）。
func TestWeComCheckHandlerRetry(t *testing.T) {
	// state=disconnected；断言入队了新的 channel_check_wecom job。
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run WeCom -v`
Expected: 编译失败。

- [ ] **Step 3: 实现**

```go
// internal/worker/handlers/channel_wecom.go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/ocops"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// WeComOrchestrator 是企业微信配置注入与解绑清理所需的最小编排能力。
type WeComOrchestrator interface {
	SetWeComSecret(ctx context.Context, appID, botID, secret string) error
	ClearWeComSecret(ctx context.Context, appID string) error
	RolloutRestart(ctx context.Context, appID string) error
}

// WeComStateProber 经 oc-ops 查询企业微信连通状态。
type WeComStateProber interface {
	ChannelState(ctx context.Context, appID, channel string) (ocops.ChannelStateResult, error)
}

// WeComConfigureHandler 执行 channel_configure_wecom job。
type WeComConfigureHandler struct {
	store  ChannelLoginStore
	cipher *auth.Cipher
	orch   WeComOrchestrator
}

func NewWeComConfigureHandler(store ChannelLoginStore, cipher *auth.Cipher, orch WeComOrchestrator) *WeComConfigureHandler {
	return &WeComConfigureHandler{store: store, cipher: cipher, orch: orch}
}

// Handle 解密企业微信凭证 → patch k8s Secret → 重启容器 → 入队连通探测。
func (h *WeComConfigureHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeChannelConfigureWeCom {
		return fmt.Errorf("非 channel_configure_wecom 任务: %s", job.Type)
	}
	payload, err := decodeChannelLoginPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	binding, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID: payload.AppID, ChannelType: domain.ChannelTypeWorkWeChat,
	})
	if err != nil {
		return fmt.Errorf("查询企业微信绑定失败: %w", err)
	}
	// 解绑路径：service.Unbind 已把 status 置 unbound_by_user 并入队本 job；
	// 这里清除容器 Secret 的 wecom-* key 并重启，使 hermes 不再启用企业微信平台。
	if binding.Status == domain.ChannelStatusUnboundByUser {
		if err := h.orch.ClearWeComSecret(ctx, payload.AppID); err != nil {
			return fmt.Errorf("清除企业微信 Secret 失败: %w", err)
		}
		if err := h.orch.RolloutRestart(ctx, payload.AppID); err != nil {
			slog.ErrorContext(ctx, "企业微信解绑后重启容器失败", "app_id", payload.AppID, redactlog.Err(err))
		}
		return nil
	}
	botID, secret, _, err := channel.DecodeWeComCredentials(h.cipher, binding.MetadataJson)
	if err != nil {
		_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID: payload.AppID, ChannelType: domain.ChannelTypeWorkWeChat,
			Status: domain.ChannelStatusFailed, LastError: null.StringFrom("企业微信配置解析失败"),
		})
		return fmt.Errorf("解析企业微信凭证失败: %w", err)
	}
	if err := h.orch.SetWeComSecret(ctx, payload.AppID, botID, secret); err != nil {
		return fmt.Errorf("写入企业微信 Secret 失败: %w", err)
	}
	if err := h.orch.RolloutRestart(ctx, payload.AppID); err != nil {
		slog.ErrorContext(ctx, "企业微信配置后重启容器失败", "app_id", payload.AppID, redactlog.Err(err))
	}
	// 给容器留启动+订阅时间，再开始探测连通。
	return enqueueWeComCheck(ctx, h.store, payload.AppID, 8*time.Second, 0)
}

// wecomCheckPayload 携带探测重试计数。
type wecomCheckPayload struct {
	AppID    string `json:"app_id"`
	Attempt  int    `json:"attempt"`
}

const wecomCheckMaxAttempts = 12 // 约 12*8s≈96s 内未连上判失败

// WeComCheckHandler 执行 channel_check_wecom job。
type WeComCheckHandler struct {
	store  ChannelLoginStore
	prober WeComStateProber
}

func NewWeComCheckHandler(store ChannelLoginStore, prober WeComStateProber) *WeComCheckHandler {
	return &WeComCheckHandler{store: store, prober: prober}
}

// Handle 探测企业微信连通状态并推进绑定终态。
func (h *WeComCheckHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeChannelCheckWeCom {
		return fmt.Errorf("非 channel_check_wecom 任务: %s", job.Type)
	}
	var p wecomCheckPayload
	if err := json.Unmarshal(job.PayloadJson, &p); err != nil || p.AppID == "" {
		return fmt.Errorf("解析 channel_check_wecom payload 失败")
	}
	binding, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID: p.AppID, ChannelType: domain.ChannelTypeWorkWeChat,
	})
	if err != nil {
		return fmt.Errorf("查询企业微信绑定失败: %w", err)
	}
	if binding.Status == domain.ChannelStatusBound {
		return nil
	}
	app, err := h.store.GetApp(ctx, p.AppID)
	if err != nil {
		return fmt.Errorf("查询应用失败: %w", err)
	}
	st, err := h.prober.ChannelState(ctx, p.AppID, "work_wechat")
	if err != nil {
		// 探测自身失败（容器还没起好等）：重试。
		return h.retryOrFail(ctx, p, "实例不可达")
	}
	switch st.State {
	case "connected":
		if err := h.store.MarkChannelBindingBound(ctx, sqlc.MarkChannelBindingBoundParams{
			AppID: p.AppID, ChannelType: domain.ChannelTypeWorkWeChat,
			BoundIdentity: null.NewString(st.Channel, st.Channel != ""),
			ChannelName:   null.String{}, MetadataJson: binding.MetadataJson,
		}); err != nil {
			return fmt.Errorf("标记企业微信绑定成功失败: %w", err)
		}
		if app.Status == domain.AppStatusBindingWaiting {
			_ = h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: p.AppID, Status: domain.AppStatusRunning})
		}
		return recordChannelAppAudit(ctx, h.store, app, "channel_bound", "succeeded", "",
			"渠道 企业微信", map[string]any{"channel_type": domain.ChannelTypeWorkWeChat})
	case "fatal":
		safe := "企业微信连接失败"
		if st.ErrorMessage != "" {
			safe = redactlog.SafeErrorMessage(errors.New(st.ErrorMessage))
		}
		_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID: p.AppID, ChannelType: domain.ChannelTypeWorkWeChat,
			Status: domain.ChannelStatusFailed, LastError: null.StringFrom(safe),
		})
		return recordChannelAppAudit(ctx, h.store, app, "channel_bound", "failed", safe,
			"渠道 企业微信", map[string]any{"channel_type": domain.ChannelTypeWorkWeChat})
	default: // disconnected / unknown：未到上限则重试。
		return h.retryOrFail(ctx, p, "企业微信连接超时")
	}
}

func (h *WeComCheckHandler) retryOrFail(ctx context.Context, p wecomCheckPayload, failMsg string) error {
	if p.Attempt+1 >= wecomCheckMaxAttempts {
		return h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID: p.AppID, ChannelType: domain.ChannelTypeWorkWeChat,
			Status: domain.ChannelStatusFailed, LastError: null.StringFrom(failMsg),
		})
	}
	return enqueueWeComCheck(ctx, h.store, p.AppID, 8*time.Second, p.Attempt+1)
}

func enqueueWeComCheck(ctx context.Context, store ChannelLoginStore, appID string, delay time.Duration, attempt int) error {
	raw, err := json.Marshal(wecomCheckPayload{AppID: appID, Attempt: attempt})
	if err != nil {
		return fmt.Errorf("序列化 channel_check_wecom payload 失败: %w", err)
	}
	if err := store.CreateJob(ctx, sqlc.CreateJobParams{
		ID: uuid.NewString(), Type: domain.JobTypeChannelCheckWeCom, Priority: 80,
		RunAfter: time.Now().Add(delay), MaxAttempts: 3, PayloadJson: raw,
	}); err != nil {
		return fmt.Errorf("创建 channel_check_wecom 任务失败: %w", err)
	}
	return nil
}
```

> `ChannelLoginStore` 已含 `GetApp/GetChannelBindingByAppAndType/SetChannelBindingStatus/MarkChannelBindingBound/SetAppStatus/CreateJob/CreateAuditLog`（见 channel_login.go），无需扩接口。`WeComStateProber` 由装配侧用闭包适配 `service.OcOpsResolverFromStore` + `ocops.Client.ChannelState`（先 ResolveEndpoint 再 ChannelState）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/worker/handlers/ -run WeCom -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handlers/channel_wecom.go internal/worker/handlers/channel_wecom_test.go
git commit -m "feat(channel): worker 企业微信配置注入与连通探测 handler

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: 装配（main.go）

**Files:**
- Modify: `cmd/server/main.go`

> 把新 service/handler/worker 接进启动流程。无独立单测；靠编译 + 后续端到端验证。

- [ ] **Step 1: 实现装配**

1. `NewChannelService` 传入 cipher（253 行附近）：
```go
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry, cipher, redisQueue)
```
（确认 `cipher` 在 main 作用域的变量名——app_service 已用同一 cipher 加密 newapi key，复用之。`NewChannelService` 签名相应调整：`cipher` 在 `registry` 后、`notifier` 前。）

2. 注册两个 worker handler（在 channel handler 注册块附近，483-488 行）：
```go
	// 企业微信配置注入 worker：解密凭证 patch Secret 并重启。
	if err := registry.Register(domain.JobTypeChannelConfigureWeCom,
		handlers.NewWeComConfigureHandler(dbStore.Queries, cipher, orch).Handle); err != nil {
		return err // 沿用本文件既有 Register 错误处理风格
	}
	// 企业微信连通探测 worker：经 oc-ops 查 hermes /health/detailed。
	wecomProber := wecomProberFromResolver{resolver: ocopsResolver, client: ocopsClient} // 见下方适配器
	if err := registry.Register(domain.JobTypeChannelCheckWeCom,
		handlers.NewWeComCheckHandler(dbStore.Queries, wecomProber).Handle); err != nil {
		return err
	}
```

3. 加 prober 适配器（在 main.go 或同包新文件 `cmd/server/wecom_prober.go`）：
```go
// wecomProberFromResolver 把 appID 解析为 oc-ops Endpoint 后查询渠道连通状态，
// 适配 handlers.WeComStateProber，避免 worker 直接依赖 service 包。
type wecomProberFromResolver struct {
	resolver *service.OcOpsResolverFromStore
	client   *ocops.Client
}

func (p wecomProberFromResolver) ChannelState(ctx context.Context, appID, ch string) (ocops.ChannelStateResult, error) {
	loc, err := p.resolver.Resolve(ctx, appID)
	if err != nil {
		return ocops.ChannelStateResult{}, err
	}
	return p.client.ChannelState(ctx, loc.Endpoint, ch)
}
```
（`ocopsResolver`/`ocopsClient` 变量名以 main.go 现有装配为准；`Resolve` 返回类型含 `Endpoint` 字段，见 service/ocops.go。）

- [ ] **Step 2: 编译验证**

Run: `go build ./...`
Expected: 通过。

- [ ] **Step 3: 全量单测**

Run: `go test ./internal/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go cmd/server/wecom_prober.go
git commit -m "feat(channel): 装配企业微信 service/worker/探测器

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 14: oc-ops 渠道状态路由（两个 hermes variant）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`
- （`ocops/server.py` 路由 `/oc/channels/{channel}/status` 已存在且参数化，无需改）

> 现状：`channel_status(channel, data_root)` 对非 weixin 抛 BAD_REQUEST。改为：work_wechat 时转发 hermes 同 pod api_server `GET /health/detailed`，读 `platforms.wecom.platform_state` 映射为 `{channel, state, error_message}`。

- [ ] **Step 1: 实现（两个 variant 内容相同）**

在 `channel.py` 顶部已有 import 基础上，把 `channel_status` 改为分发：
```python
import os
import json
import urllib.request

_API_SERVER_BASE = "http://127.0.0.1:8642"  # hermes api_server loopback（与 conversation 转发同源）

def _wecom_status(data_root):
    """转发 hermes /health/detailed，读企业微信平台连通状态。

    返回 {channel, state, error_message}；api_server 不可达或无 wecom 平台时
    state='unknown'，由 manager 侧按重试处理。
    """
    key = os.environ.get("API_SERVER_KEY", "")
    req = urllib.request.Request(_API_SERVER_BASE + "/health/detailed")
    if key:
        req.add_header("Authorization", "Bearer " + key)
    try:
        with urllib.request.urlopen(req, timeout=3) as resp:
            data = json.loads(resp.read().decode("utf-8"))
    except Exception:
        return {"channel": "work_wechat", "state": "unknown", "error_message": ""}
    platforms = (data or {}).get("platforms", {}) or {}
    wecom = platforms.get("wecom", {}) or {}
    state = wecom.get("platform_state") or "unknown"
    return {"channel": "work_wechat", "state": state, "error_message": wecom.get("error_message") or ""}


def channel_status(channel: str, data_root):
    # 企业微信走 api_server 健康转发；微信保持原文件态判定。
    if channel == "work_wechat":
        return _wecom_status(data_root)
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", f"unknown channel: {channel}")
    # ……（保留原 weixin 文件态实现，不改动）……
```

> `API_SERVER_KEY` env 两容器同源（control-token，见 render.go 注释），api_server 要求 Bearer 鉴权。`_API_SERVER_BASE` 端口以该 variant 实际 api_server 端口为准（render.go 注释提到 127.0.0.1:8642）。`channel_unbind`/`channel_login` 对 work_wechat 不需要支持（企业微信不走文件态登录/解绑）。

- [ ] **Step 2: 语法自检**

Run: `python -m py_compile runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py`
Expected: 无输出（编译通过）。

- [ ] **Step 3: Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py
git commit -m "feat(channel): oc-ops 转发 hermes 健康状态供企业微信连通探测

两个 hermes variant 的 channel_status 对 work_wechat 转发 api_server
/health/detailed，读 platforms.wecom.platform_state。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 15: 前端 hook（配置 mutation + 状态映射）

**Files:**
- Modify: `web/src/api/hooks/useChannel.ts`

- [ ] **Step 1: 实现配置 mutation**

加企业微信配置 mutation（带 body，区别于微信无 body 的 `useBeginChannelAuth`）：
```typescript
// WeComConfig 是企业微信智能机器人配置入参。
export interface WeComConfig {
  bot_id: string
  secret: string
  websocket_url?: string
}

// useConfigureWeCom 提交企业微信配置，触发后端注入容器并重启。
// 成功后失效该渠道进度缓存，由轮询展示「验证中→已连接/失败」。
export function useConfigureWeCom(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (config: WeComConfig) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/work_wechat/auth`,
        { method: 'POST', body: JSON.stringify(config) },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['channel-progress', appId.value, 'work_wechat'] })
    },
  })
}
```

> `apiRequest` 的 body 传法以 `@/api/client` 既有约定为准（若它自动 JSON.stringify，则直接传对象）。

- [ ] **Step 2: 类型检查**

Run: `cd web && npm run type-check`（或项目既有 `vue-tsc` 目标）
Expected: 无类型错误。

- [ ] **Step 3: Commit**

```bash
git add web/src/api/hooks/useChannel.ts
git commit -m "feat(channel): 前端企业微信配置提交 hook

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 16: 前端企业微信表单组件 + 渠道页接入

**Files:**
- Create: `web/src/pages/apps/WeComChannelForm.vue`
- Modify: `web/src/pages/apps/AppChannelsTab.vue`（`work_wechat` supported→true，按 channel_type 渲染表单 vs 二维码）

- [ ] **Step 1: 实现表单组件**

```vue
<!-- web/src/pages/apps/WeComChannelForm.vue -->
<!-- 企业微信智能机器人配置表单：填 bot_id+secret，提交后轮询连通状态；含后台建机器人图文指引。 -->
<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useConfigureWeCom, useChannelProgressQuery, useUnbindChannel, formatChannelStatus } from '@/api/hooks/useChannel'

const props = defineProps<{ appId: string }>()
const { t } = useI18n()
const channelType = computed(() => 'work_wechat')

const botId = ref('')
const secret = ref('')
const showGuide = ref(false)

const configure = useConfigureWeCom(computed(() => props.appId))
const { data: progress } = useChannelProgressQuery(computed(() => props.appId), channelType)
const unbind = useUnbindChannel(computed(() => props.appId), channelType)

const isBound = computed(() => progress.value?.status === 'bound')
const statusText = computed(() => formatChannelStatus(progress.value?.status))
const errorMessage = computed(() => progress.value?.error_message ?? '')

async function submit() {
  await configure.mutateAsync({ bot_id: botId.value.trim(), secret: secret.value.trim() })
  secret.value = '' // 提交后清空明文 secret 输入
}
</script>

<template>
  <div class="wecom-form">
    <!-- 已绑定：展示状态 + bot_id + 解绑 -->
    <div v-if="isBound">
      <p>{{ t('apps.channels.wecom.boundStatus') }}：{{ statusText }}</p>
      <p>{{ t('apps.channels.wecom.botIdLabel') }}：{{ botId || progress?.bound_identity }}</p>
      <button @click="unbind.mutate()">{{ t('apps.channels.wecom.unbind') }}</button>
    </div>
    <!-- 未绑定/失败：表单 -->
    <form v-else @submit.prevent="submit">
      <label>{{ t('apps.channels.wecom.botIdLabel') }}
        <input v-model="botId" required :placeholder="t('apps.channels.wecom.botIdPlaceholder')" />
      </label>
      <label>{{ t('apps.channels.wecom.secretLabel') }}
        <input v-model="secret" type="password" required :placeholder="t('apps.channels.wecom.secretPlaceholder')" />
      </label>
      <p v-if="errorMessage" class="error">{{ t('apps.channels.wecom.failed') }}：{{ errorMessage }}</p>
      <p v-else-if="progress?.status === 'pending_auth'">{{ t('apps.channels.wecom.verifying') }}</p>
      <button type="submit" :disabled="configure.isPending.value">{{ t('apps.channels.wecom.submit') }}</button>
    </form>
    <!-- 图文指引 -->
    <button class="link" @click="showGuide = !showGuide">{{ t('apps.channels.wecom.guideToggle') }}</button>
    <ol v-if="showGuide">
      <li>{{ t('apps.channels.wecom.guideStep1') }}</li>
      <li>{{ t('apps.channels.wecom.guideStep2') }}</li>
      <li>{{ t('apps.channels.wecom.guideStep3') }}</li>
    </ol>
  </div>
</template>
```

> 样式/类名沿用 AppChannelsTab 既有设计语言；上面用最小结构，实现者按页面现有组件库（按钮/输入）替换原生标签。

`AppChannelsTab.vue`：把 `work_wechat` 的 `supported: false` 改为 `true`；在渠道详情渲染处按 type 分流：`work_wechat` 渲染 `<WeComChannelForm :app-id="appId" />`，其余保持现有二维码逻辑。

- [ ] **Step 2: 类型检查 + 构建**

Run: `cd web && npm run type-check && npm run build`
Expected: 通过。

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/apps/WeComChannelForm.vue web/src/pages/apps/AppChannelsTab.vue
git commit -m "feat(channel): 前端企业微信配置表单与渠道页接入

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 17: i18n 文案（中英）+ 生成产物同步

**Files:**
- Modify: `web/src/i18n/locales/zh/apps/root.ts`
- Modify: `web/src/i18n/locales/en/apps/root.ts`
- 生成：`openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 加中文文案**

在 `zh/apps/root.ts` 的 `channels` 段（与 `channelWorkWechat` 同层）补 `wecom` 子段：
```ts
    wecom: {
      botIdLabel: 'Bot ID',
      botIdPlaceholder: '企业微信后台智能机器人的 Bot ID',
      secretLabel: 'Secret',
      secretPlaceholder: '机器人 Secret（保存后不再回显）',
      submit: '保存并连接',
      verifying: '正在连接企业微信，请稍候…',
      failed: '连接失败',
      boundStatus: '绑定状态',
      botIdLabelShort: 'Bot ID',
      unbind: '解绑',
      guideToggle: '如何获取 Bot ID 与 Secret？',
      guideStep1: '登录企业微信管理后台 → 安全与管理 → 管理工具 → 智能机器人。',
      guideStep2: '创建机器人 → 选择「API 模式」→「长连接（Polling）」配置。',
      guideStep3: '复制生成的 Bot ID 与 Secret，粘贴到上方表单并保存。',
    },
```
并把 `channelWorkWechatDesc` 更新为「企业微信智能机器人（长连接接入）」，`supported` 相关文案沿用现有。

- [ ] **Step 2: 加英文文案**

在 `en/apps/root.ts` 对应位置加同 key 英文：
```ts
    wecom: {
      botIdLabel: 'Bot ID',
      botIdPlaceholder: 'Bot ID from WeCom admin console',
      secretLabel: 'Secret',
      secretPlaceholder: 'Bot secret (hidden after saving)',
      submit: 'Save & Connect',
      verifying: 'Connecting to WeCom, please wait…',
      failed: 'Connection failed',
      boundStatus: 'Binding status',
      botIdLabelShort: 'Bot ID',
      unbind: 'Unbind',
      guideToggle: 'How to get Bot ID and Secret?',
      guideStep1: 'Sign in to WeCom admin console → Security → Management Tools → Smart Robot.',
      guideStep2: 'Create a robot → choose "API mode" → "Long connection (Polling)".',
      guideStep3: 'Copy the generated Bot ID and Secret, paste into the form above and save.',
    },
```

- [ ] **Step 3: 生成 OpenAPI 与前端类型**

Run: `make openapi-gen && make web-types-gen`
Then: `make openapi-check`
Expected: `openapi-check` 后 git 工作区干净（yaml 已跟随 handler 更新）。

- [ ] **Step 4: 类型检查**

Run: `cd web && npm run type-check`
Expected: 无错误。

- [ ] **Step 5: Commit**

```bash
git add web/src/i18n/locales/zh/apps/root.ts web/src/i18n/locales/en/apps/root.ts openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(channel): 企业微信渠道中英文案与生成产物同步

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 18: 全量回归

- [ ] **Step 1: 后端全量测试**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 2: 前端构建**

Run: `cd web && npm run type-check && npm run build`
Expected: 通过。

- [ ] **Step 3: OpenAPI 一致性**

Run: `make openapi-check`
Expected: 工作区干净。

---

## Task 19: 真实浏览器端到端验证（CLAUDE.md 硬性要求）

> 本地 k3d 环境（`make local-up`，*.localhost 访问需绕 7890 代理）。用户提供真实企业微信机器人 bot_id/secret。用真实浏览器（非 curl）逐项验证；发现问题先修再验。

- [ ] **Step 1: 重新构建并部署 hermes/oc-ops 镜像到本地 k3d**

> oc-ops channel.py 改动需进镜像。按本地 hermes 构建 runbook（见 docs 与 memory）重建并让现有 app pod 滚动到新镜像。

- [ ] **Step 2: 平台管理员视角**

用 `admin`（组织标识空）登录 `http://ocm.localhost`：进入某 app 渠道页 → 企业微信卡片可用（非禁用）→ 填真实 bot_id/secret 保存 → 观察「验证中→已连接」→ 渠道状态 bound。

- [ ] **Step 3: 失败路径**

故意填错 secret → 观察「连接失败」并展示具体原因（fatal error_message）。

- [ ] **Step 4: org_admin / org_member 视角**

用本地 org 账号分别验证：有管理权限者可配置/解绑；仅查看权限者只读。

- [ ] **Step 5: 与微信并存**

同一 app 同时绑定微信（扫码）与企业微信（配置）→ 两渠道状态各自独立、互不影响。

- [ ] **Step 6: 解绑**

企业微信解绑 → 容器重启 → 状态回未绑定 → hermes 不再启用企业微信平台（可经 oc-ops /health/detailed 确认 platforms 无 wecom 或 disconnected）。

- [ ] **Step 7: 记录验证结果**

按 [[feedback_verification-rigor]] 要求，交付时给逐项/全角色验证矩阵与证据（截图/状态值）。

---

## 自检对照（spec 覆盖）

- spec §2 数据模型 → Task 1（migration）、Task 3（query）、Task 4（onboarding 预置）
- spec §3 配置注入方案 1 → Task 5（env+RenderSecret）、Task 6（Secret patch）、Task 7（重建带出）
- spec §4 状态机 / §5 绑定流程 → Task 8（凭证编码）、Task 9（service）、Task 10（handler）、Task 12（worker 配置+探测）
- spec §6 连通验证 → Task 11（oc-ops 客户端）、Task 14（oc-ops 路由两 variant）
- spec §7 解绑 → Task 9 Unbind 对 work_wechat 入队 + Task 12 配置 handler 解绑分支（调 ClearWeComSecret+RolloutRestart）
- spec §8 前端 → Task 15（hook）、Task 16（表单+接入）、Task 17（i18n）
- spec §9 并存 → Task 1（唯一键）、Task 4（预置）
- spec §10 测试 → 各 Task 单测 + Task 18 回归 + Task 19 浏览器验证

> **补充（解绑注入清理）已落实到任务**：现有 `ChannelService.Unbind` 仅置 status=unbound_by_user，不清 k8s Secret，企业微信解绑后重启仍会启用平台。最终方案（已写入 Task 12 配置 handler 的解绑分支）：
> 1. **Task 9** 在 `ChannelService.Unbind` 内对 `channelType == work_wechat` 分支，置完 unbound_by_user 后**额外入队一个 `channel_configure_wecom` job**（payload 仅 app_id/channel_type）。
> 2. **Task 12** 的 `WeComConfigureHandler.Handle` 已加分支：读到 `binding.Status == unbound_by_user` 时调 `ClearWeComSecret` + `RolloutRestart` 并返回（不解密、不注入）。
> 3. **Task 12 测试**追加 `TestWeComConfigureHandlerUnbind`：binding.status=unbound_by_user 时断言调用 `ClearWeComSecret`、未调用 `SetWeComSecret`。
> 实现 Task 9 时记得在 Unbind 的 work_wechat 分支入队该 job，否则解绑只改 DB、容器仍连着企业微信。
