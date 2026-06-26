# 飞书 / Lark 渠道实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 app 实例新增飞书 / Lark 消息渠道，支持「扫码自动创建」为主、「手填凭证」为辅两种取凭证方式，凭证经 manager 注入 hermes 容器走 WebSocket 长连接，并经 `/health/detailed` 探测连通态完成绑定。

**Architecture:** 飞书是「取凭证像微信（扫码 SSE）、注入运行像企业微信（env 态长连接 + health 探测）」的混血渠道。复用 `channel_bindings` 表、`ChannelAdapter` 接口、worker `channel_start_login`/`channel_check_binding` 两段式 job、per-app Secret optional env 注入与 `RolloutRestart`。与微信的本质差异：oc-ops 扫码 SSE 回传的是**凭证**（app_id/app_secret/domain），manager 必须捕获→加密落库→patch Secret→重启→再 health 探测确认连接；secret 全程不得经 `PollAuth` 泄露到前端。

**Tech Stack:** Go（gin / sqlc / client-go / testify）、golang-migrate（MySQL）、Python（Starlette/asyncio，hermes 两个 variant 的 oc-ops 层）、Vue 3 + TypeScript（Pinia/vue-query）、引擎只读依赖 `gateway/platforms/feishu.py`（`qr_register`/`_begin_registration`/`_poll_registration`/`probe_bot`，env `FEISHU_APP_ID`/`FEISHU_APP_SECRET`/`FEISHU_DOMAIN`）。

**Spec:** `docs/superpowers/specs/2026-06-26-feishu-channel-design.md`

---

## 关键约定（所有任务共用，先读）

- **渠道类型常量**：`domain.ChannelTypeFeishu = "feishu"`。
- **凭证存储**：`channel_bindings.metadata_json` 存
  `{"app_id","app_secret_ciphertext","domain","acquired_by","bot_name","bot_open_id","injected"}`。
  `app_secret` 用 `auth.Cipher`(AES-GCM) 加密只存密文；`app_id`/`domain`/`acquired_by`(qr|manual)/`bot_name`/`bot_open_id`/`injected`(注入并重启后置 "true") 明文。
- **DB 是 source of truth**；k8s Secret（`app-<id>-token`）的 `feishu-app-id`/`feishu-app-secret`/`feishu-domain` 三个 key 是派生注入物。
- **敏感字段过滤**：`PollAuth` 透传 `metadata_json` 给前端，必须剔除 `app_secret_ciphertext` 等 `*_ciphertext`/含 `secret` 的 key（见 Task 14）。
- **两个 hermes variant** 都改：`runtime/hermes/hermes-v2026.6.5/`、`runtime/hermes/hermes-v2026.5.16/`。
- **凭证 handoff（核心）**：扫码 SSE 的 `credentials` 事件由 `FeishuAdapter` 后台 goroutine 捕获，存进 adapter 私有字段，worker 经飞书专用方法 `TakeCredentials(appID)` 取出；**secret 不进 `AuthProgress`**。
- **运行测试**：Go 用 `go test ./internal/...`；oc-ops Python 测试见各 variant `runtime/hermes/.../tests/`（若无 pytest 环境，按 Task 说明手测）；前端 `cd web && npm run test`、`npm run build`。
- **每个 Go 测试方法/子测试/表驱动用例必须有相邻中文注释**（项目规范）。

---

## Phase 1 · DB 地基与枚举

### Task 1: 迁移——放宽 channel_type 约束并支持渠道并存

**Files:**
- Create: `internal/migrations/000015_support_feishu_channel.up.sql`
- Create: `internal/migrations/000015_support_feishu_channel.down.sql`
- Test: `internal/migrations/migrations_test.go`（已存在，跑全量迁移校验）

- [ ] **Step 1: 写 up 迁移**

`internal/migrations/000015_support_feishu_channel.up.sql`：
```sql
-- 飞书渠道：放宽 channel_type CHECK 至 wechat+feishu，并把唯一约束加上 channel_type，
-- 让同一 app 的 wechat 与 feishu 各保留一条非 deleted 绑定（渠道并存）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu'));

-- 唯一约束由 (app_active_key) 改为 (app_active_key, channel_type)：同一 app 多渠道并存。
ALTER TABLE channel_bindings
    DROP INDEX uk_channel_bindings_app_active,
    ADD UNIQUE KEY uk_channel_bindings_app_active (app_active_key, channel_type);
```

- [ ] **Step 2: 写 down 迁移**

`internal/migrations/000015_support_feishu_channel.down.sql`：
```sql
-- 还原唯一约束到单列（注意：若已有 feishu 绑定行，回滚会因约束冲突失败，属预期）。
ALTER TABLE channel_bindings
    DROP INDEX uk_channel_bindings_app_active,
    ADD UNIQUE KEY uk_channel_bindings_app_active (app_active_key);

ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat'));
```

- [ ] **Step 3: 跑迁移测试**

Run: `go test ./internal/migrations/ -run TestMigrations -v`
Expected: PASS（migrations_test.go 跑全量 up/down，验证 000015 语法与可逆性）。
若该测试需真实 MySQL：改跑本地 `make migrate-up` 后 `make migrate-down DOWN=1`，确认无报错。

- [ ] **Step 4: 提交**
```bash
git add internal/migrations/000015_support_feishu_channel.up.sql internal/migrations/000015_support_feishu_channel.down.sql
git commit -m "feat(channel): 迁移放宽 channel_type 约束并支持渠道并存

为飞书渠道放宽 channel_bindings.channel_type CHECK 至 wechat+feishu，
唯一约束 uk_channel_bindings_app_active 加上 channel_type，
使同一 app 的微信与飞书绑定可并存。"
```

---

### Task 2: 枚举与 worker 标签加飞书

**Files:**
- Modify: `internal/domain/enums.go:40-50`
- Modify: `internal/worker/handlers/channel_login.go`（`channelLabelWorker`）
- Test: `internal/domain/enums_test.go`（若不存在则跳过，常量改动由编译保证）

- [ ] **Step 1: 加飞书渠道类型常量**

`internal/domain/enums.go`，在 `ChannelTypeWeChat` 下新增：
```go
	// ChannelTypeWeChat 是当前唯一落地的渠道类型。
	ChannelTypeWeChat = "wechat"
	// ChannelTypeFeishu 是飞书 / Lark 渠道（扫码自动创建 + 手填兜底，WebSocket 长连接）。
	ChannelTypeFeishu = "feishu"
```

- [ ] **Step 2: worker 标签加飞书**

`internal/worker/handlers/channel_login.go` 的 `channelLabelWorker`：
```go
func channelLabelWorker(channelType string) string {
	switch channelType {
	case domain.ChannelTypeWeChat:
		return "微信"
	case domain.ChannelTypeFeishu:
		return "飞书"
	default:
		return channelType
	}
}
```

- [ ] **Step 3: 编译**

Run: `go build ./...`
Expected: 通过。

- [ ] **Step 4: 提交**
```bash
git add internal/domain/enums.go internal/worker/handlers/channel_login.go
git commit -m "feat(channel): 新增 feishu 渠道类型枚举与 worker 标签"
```

---

## Phase 2 · k8s 注入（FEISHU_* env + Secret 按 key patch）

### Task 3: AppSpec 加飞书字段，RenderSecret/RenderDeployment 注入

**Files:**
- Modify: `internal/integrations/k8sorch/orchestrator.go:34-55`（AppSpec）
- Modify: `internal/integrations/k8sorch/render.go`（RenderSecret + RenderDeployment）
- Test: `internal/integrations/k8sorch/render_test.go`

- [ ] **Step 1: 写失败测试**

`internal/integrations/k8sorch/render_test.go` 新增。注意 `FeishuAppSecret` 存**明文**（引擎 `FEISHU_APP_SECRET` 需明文，buildAppSpec 已解密，见 Task 4）：
```go
// TestRenderSecretIncludesFeishuKeys 验证 AppSpec 带飞书配置时 Secret 写入三个飞书 key。
func TestRenderSecretIncludesFeishuKeys(t *testing.T) {
	spec := AppSpec{
		AppID:           "app-1",
		ControlToken:    "tok",
		FeishuAppID:     "cli_abc",
		FeishuAppSecret: "plain-secret",
		FeishuDomain:    "feishu",
	}
	sec := RenderSecret(spec, "oc-apps")
	require.Equal(t, "cli_abc", sec.StringData["feishu-app-id"])
	require.Equal(t, "plain-secret", sec.StringData["feishu-app-secret"])
	require.Equal(t, "feishu", sec.StringData["feishu-domain"])
}

// TestRenderSecretOmitsFeishuKeysWhenUnset 验证未绑定飞书时不写飞书 key（optional env 不注入）。
func TestRenderSecretOmitsFeishuKeysWhenUnset(t *testing.T) {
	sec := RenderSecret(AppSpec{AppID: "app-1", ControlToken: "tok"}, "oc-apps")
	_, ok := sec.StringData["feishu-app-id"]
	require.False(t, ok)
}

// TestRenderDeploymentInjectsFeishuOptionalEnv 验证 hermes 容器永久带三条 optional 飞书 env。
func TestRenderDeploymentInjectsFeishuOptionalEnv(t *testing.T) {
	dep := RenderDeployment(AppSpec{AppID: "app-1", ControlToken: "tok"}, "oc-apps")
	envs := dep.Spec.Template.Spec.Containers[0].Env
	want := map[string]string{
		"FEISHU_APP_ID":     "feishu-app-id",
		"FEISHU_APP_SECRET": "feishu-app-secret",
		"FEISHU_DOMAIN":     "feishu-domain",
	}
	found := map[string]string{}
	for _, e := range envs {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			found[e.Name] = e.ValueFrom.SecretKeyRef.Key
		}
	}
	for name, key := range want {
		require.Equal(t, key, found[name], "env %s 应来自 secret key %s", name, key)
	}
}
```
> 注：测试里 `dep.Spec.Template.Spec.Containers[0]` 取 hermes 容器；若实际容器顺序不同，按 render.go 现状调整索引。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run TestRenderSecret -v`
Expected: FAIL（AppSpec 无 Feishu* 字段，编译错误）。

- [ ] **Step 3: AppSpec 加字段**

`internal/integrations/k8sorch/orchestrator.go` 的 `AppSpec` 末尾追加。设计取舍：引擎 `FEISHU_APP_SECRET` 需明文，故 Secret 的 `feishu-app-secret` 写明文；DB 里仍只存密文，buildAppSpec（Task 4）解密后再填 `FeishuAppSecret`：
```go
	// FeishuAppID 是飞书应用 App ID（明文，未绑定为空）。
	FeishuAppID string
	// FeishuAppSecret 是飞书 App Secret 明文（buildAppSpec 从 DB 密文解密后填入，引擎需明文；未绑定为空）。
	FeishuAppSecret string
	// FeishuDomain 是飞书 domain：feishu（国内）/ lark（国际），未绑定为空。
	FeishuDomain string
```

- [ ] **Step 4: RenderSecret 写飞书 key**

`internal/integrations/k8sorch/render.go` 的 `RenderSecret`：
```go
// RenderSecret 渲染 per-app 控制 token Secret（control-token 键）；已绑定飞书时附带飞书凭证 key。
func RenderSecret(spec AppSpec, namespace string) *corev1.Secret {
	data := map[string]string{"control-token": spec.ControlToken}
	// 已绑定飞书：把凭证带入 Secret，保证 app 重建/镜像升级不丢配置（DB 是 source of truth）。
	if spec.FeishuAppID != "" && spec.FeishuAppSecret != "" {
		data["feishu-app-id"] = spec.FeishuAppID
		data["feishu-app-secret"] = spec.FeishuAppSecret
		data["feishu-domain"] = spec.FeishuDomain
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
}
```

- [ ] **Step 5: RenderDeployment 加 optional env**

`internal/integrations/k8sorch/render.go` 的 hermes 容器 `Env: append([]corev1.EnvVar{...}, proxyEnv...)` 处，在现有 env 列表里追加三条 optional SecretKeyRef（`Optional` 为 `*bool` true）：
```go
	// feishuOptional 三条 env 永久注入；未绑定时 Secret 无对应 key，optional=true 使 env 不注入，
	// 引擎 getenv 为空 → 飞书平台不启用。Deployment 模板永不因绑定变化。
	optionalTrue := true
	feishuEnv := []corev1.EnvVar{
		{Name: "FEISHU_APP_ID", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)}, Key: "feishu-app-id", Optional: &optionalTrue}}},
		{Name: "FEISHU_APP_SECRET", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)}, Key: "feishu-app-secret", Optional: &optionalTrue}}},
		{Name: "FEISHU_DOMAIN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)}, Key: "feishu-domain", Optional: &optionalTrue}}},
	}
```
把 `feishuEnv...` 并入 hermes 容器的 `Env` append 列表（与 `proxyEnv...` 同级 append）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -run 'TestRenderSecret|TestRenderDeploymentInjectsFeishu' -v`
Expected: PASS。

- [ ] **Step 7: 提交**
```bash
git add internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/render.go internal/integrations/k8sorch/render_test.go
git commit -m "feat(channel): k8s 渲染注入飞书 optional env 与 Secret key"
```

---

### Task 4: buildAppSpec 从 channel_bindings 解密带出飞书配置

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go:390-413`（buildAppSpec）
- Test: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1: 写失败测试**

在 `app_initialize_test.go` 新增（用已绑定飞书的 channel_bindings stub 行）：
```go
// TestBuildAppSpecCarriesFeishuCredentials 验证已绑定飞书时 buildAppSpec 解密带出明文凭证。
func TestBuildAppSpecCarriesFeishuCredentials(t *testing.T) {
	// 构造 metadata_json：app_id 明文 + secret 密文（用测试 cipher 加密 "s3cret"）。
	cipher := newTestCipher(t)
	enc, err := cipher.Encrypt([]byte("s3cret"))
	require.NoError(t, err)
	meta, _ := json.Marshal(map[string]any{
		"app_id": "cli_abc", "app_secret_ciphertext": enc, "domain": "feishu", "injected": "true",
	})
	h := newAppInitHandlerWithCipher(t, cipher) // 测试 helper：注入含 cipher 的 handler
	h.feishuBinding = sqlc.ChannelBinding{ // 测试 helper 让 buildAppSpec 能读到该绑定
		AppID: "app-1", ChannelType: domain.ChannelTypeFeishu,
		Status: domain.ChannelStatusBound, MetadataJson: meta,
	}
	spec := h.buildAppSpec(sqlc.App{ID: "app-1"}, "hermes:img", "tok")
	require.Equal(t, "cli_abc", spec.FeishuAppID)
	require.Equal(t, "s3cret", spec.FeishuAppSecret)
	require.Equal(t, "feishu", spec.FeishuDomain)
}
```
> 说明：`buildAppSpec` 现签名为 `(app, hermesImage, controlToken)`，需新增「读取该 app 的飞书绑定并解密」逻辑。测试 helper（`newAppInitHandlerWithCipher`/`feishuBinding`）按现有 `app_initialize_test.go` 的 stub 风格补充；若现有测试用真实 store mock，则在 mock 的 `GetChannelBindingByAppAndType` 返回该行。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestBuildAppSpecCarriesFeishu -v`
Expected: FAIL。

- [ ] **Step 3: buildAppSpec 带出飞书配置**

`buildAppSpec` 内，在 return AppSpec 前查询并解密飞书绑定（store 需有 `GetChannelBindingByAppAndType`）：
```go
	// 已绑定飞书：解密带出凭证，使 RenderSecret 在重建/升级时不丢配置。
	var feishuAppID, feishuSecret, feishuDomain string
	if binding, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID: app.ID, ChannelType: domain.ChannelTypeFeishu,
	}); err == nil && binding.Status == domain.ChannelStatusBound && len(binding.MetadataJson) > 0 {
		var m struct {
			AppID            string `json:"app_id"`
			SecretCiphertext string `json:"app_secret_ciphertext"`
			Domain           string `json:"domain"`
		}
		if json.Unmarshal(binding.MetadataJson, &m) == nil && m.SecretCiphertext != "" && h.cfg.Cipher != nil {
			if plain, derr := h.cfg.Cipher.Decrypt(m.SecretCiphertext); derr == nil {
				feishuAppID, feishuSecret, feishuDomain = m.AppID, string(plain), m.Domain
			}
		}
	}
```
然后在返回的 `k8sorch.AppSpec{...}` 里补：
```go
		FeishuAppID:     feishuAppID,
		FeishuAppSecret: feishuSecret,
		FeishuDomain:    feishuDomain,
```
> 注意：`buildAppSpec` 当前可能不接收 `ctx`，需把 `ctx` 透传进来（改签名为 `buildAppSpec(ctx, app, hermesImage, controlToken)` 并更新调用点），且 handler 需持有 `store`（含 `GetChannelBindingByAppAndType`）。若 `AppInitializeHandler.store` 接口未含该方法，给其接口补上。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/worker/handlers/ -run TestBuildAppSpecCarriesFeishu -v`
Expected: PASS。

- [ ] **Step 5: 全包回归**

Run: `go test ./internal/worker/handlers/ -v`
Expected: PASS（确认改签名未破坏既有测试）。

- [ ] **Step 6: 提交**
```bash
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -m "feat(channel): app 初始化时解密带出飞书凭证写入 AppSpec"
```

---

### Task 5: KubernetesAdapter 按 key patch Secret

**Files:**
- Modify: `internal/integrations/k8sorch/adapter.go`（新增 `PatchSecretKeys`）
- Test: `internal/integrations/k8sorch/adapter_test.go`

- [ ] **Step 1: 写失败测试（用 fake clientset）**

`internal/integrations/k8sorch/adapter_test.go` 新增（用 `k8s.io/client-go/kubernetes/fake`）：
```go
// TestPatchSecretKeysSetAndDelete 验证按 key 增删 Secret 不影响其他 key（control-token 保留）。
func TestPatchSecretKeysSetAndDelete(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName("app-1"), Namespace: "oc-apps"},
		Data:       map[string][]byte{"control-token": []byte("tok")},
	})
	a := &KubernetesAdapter{client: client, namespace: "oc-apps"}
	// 增三个飞书 key
	err := a.PatchSecretKeys(context.Background(), "app-1",
		map[string]string{"feishu-app-id": "cli_x", "feishu-app-secret": "sec", "feishu-domain": "feishu"}, nil)
	require.NoError(t, err)
	got, _ := client.CoreV1().Secrets("oc-apps").Get(context.Background(), secretName("app-1"), metav1.GetOptions{})
	require.Equal(t, "cli_x", string(got.Data["feishu-app-id"]))
	require.Equal(t, "tok", string(got.Data["control-token"]), "control-token 不应被动")
	// 删三个飞书 key（解绑）
	require.NoError(t, a.PatchSecretKeys(context.Background(), "app-1", nil,
		[]string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}))
	got2, _ := client.CoreV1().Secrets("oc-apps").Get(context.Background(), secretName("app-1"), metav1.GetOptions{})
	_, ok := got2.Data["feishu-app-id"]
	require.False(t, ok)
	require.Equal(t, "tok", string(got2.Data["control-token"]))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/k8sorch/ -run TestPatchSecretKeys -v`
Expected: FAIL（无 PatchSecretKeys）。

- [ ] **Step 3: 实现 PatchSecretKeys**

`internal/integrations/k8sorch/adapter.go`（参考现有 `applySecret` 的 Get→Update 乐观锁风格，用 `retry.RetryOnConflict`）：
```go
// PatchSecretKeys 对 app-<id>-token Secret 增删指定 key，不动其他 key。
// set 写入/覆盖；del 删除。用于渠道绑定/解绑时增删 feishu-* 凭证 key。
// 用 retry.RetryOnConflict 处理 Get→Update 间的乐观锁冲突。
func (a *KubernetesAdapter) PatchSecretKeys(ctx context.Context, appID string, set map[string]string, del []string) error {
	api := a.client.CoreV1().Secrets(a.namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		s, err := api.Get(ctx, secretName(appID), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if s.Data == nil {
			s.Data = map[string][]byte{}
		}
		for k, v := range set {
			s.Data[k] = []byte(v)
		}
		for _, k := range del {
			delete(s.Data, k)
		}
		_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
		return uerr
	})
	return wrapK8s("patch Secret keys", err)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/k8sorch/ -run TestPatchSecretKeys -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/integrations/k8sorch/adapter.go internal/integrations/k8sorch/adapter_test.go
git commit -m "feat(channel): KubernetesAdapter 支持按 key patch Secret"
```

---

## Phase 3 · oc-ops 引擎侧（Python，两 variant 同改）

> 以下 Task 6-8 的代码需**同时**写入两个 variant：
> `runtime/hermes/hermes-v2026.6.5/ocops/` 与 `runtime/hermes/hermes-v2026.5.16/ocops/`
> （两 variant 的 `ocops/channel.py`、`ocops/server.py` 当前内容一致，可同文件复制）。

### Task 6: oc-ops 飞书扫码注册 SSE 事件流

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`
- Test: `runtime/hermes/hermes-v2026.6.5/tests/`（若有 ocops 测试目录，加 `test_feishu_channel.py`；否则按 Step 5 手测）

- [ ] **Step 1: 实现 feishu_register async generator**

在两个 `ocops/channel.py` 末尾新增（驱动引擎私有 `_begin_registration`/`_poll_registration`，结构化返回，优于解析 stdout）：
```python
async def feishu_register(domain: str = "feishu"):
    """飞书扫码自动创建的 async 事件流：先 yield qrcode，最后 yield credentials/failed。

    驱动 hermes 引擎 gateway.platforms.feishu 的设备码注册函数：
      _begin_registration(domain) -> {device_code, qr_url, interval, expire_in}
      _poll_registration(device_code, interval, expire_in, domain) -> {app_id, app_secret, domain, open_id} | None

    事件序列：
      - 引擎 SDK 不可用 → {"event":"failed","reason":...}
      - 正常 → {"event":"qrcode","url":...} 然后
               {"event":"credentials","app_id":...,"app_secret":...,"domain":...,
                "bot_name":...,"bot_open_id":...}
      - 扫码超时/拒绝 → {"event":"failed","reason":"registration timeout or denied"}

    刻意不抛异常：所有失败降级为 failed 事件，让上层 SSE 端点优雅收尾。
    凭证（含 app_secret）经此 SSE 在 oc-ops↔manager 内网鉴权通道回传，由 manager 落库即加密。
    """
    try:
        from gateway.platforms.feishu import (
            _begin_registration,
            _poll_registration,
            probe_bot,
        )
    except ImportError as e:
        yield {"event": "failed", "reason": f"hermes feishu SDK not available: {e}"}
        return

    loop = asyncio.get_event_loop()
    try:
        begin = await loop.run_in_executor(None, _begin_registration, domain)
    except Exception as e:  # noqa: BLE001 - 注册启动失败降级为 failed
        yield {"event": "failed", "reason": f"begin registration failed: {e}"}
        return

    # 先把二维码 URL 发给前端展示。
    yield {"event": "qrcode", "url": begin.get("qr_url", "")}

    # 阻塞轮询（在线程池里跑，避免堵事件循环），直到扫码成功/超时。
    def _poll():
        return _poll_registration(
            device_code=begin["device_code"],
            interval=begin.get("interval", 5),
            expire_in=begin.get("expire_in", 600),
            domain=domain,
        )

    try:
        result = await loop.run_in_executor(None, _poll)
    except Exception as e:  # noqa: BLE001
        yield {"event": "failed", "reason": f"poll registration failed: {e}"}
        return

    if not result or not result.get("app_id") or not result.get("app_secret"):
        yield {"event": "failed", "reason": "registration timeout or denied"}
        return

    # best-effort 探测 bot 名/open_id（失败不影响凭证回传）。
    bot_name, bot_open_id = None, None
    try:
        info = await loop.run_in_executor(
            None, probe_bot, result["app_id"], result["app_secret"], result.get("domain", domain)
        )
        if info:
            bot_name = info.get("bot_name")
            bot_open_id = info.get("bot_open_id")
    except Exception:  # noqa: BLE001 - 探测失败忽略
        pass

    yield {
        "event": "credentials",
        "app_id": result["app_id"],
        "app_secret": result["app_secret"],
        "domain": result.get("domain", domain),
        "bot_name": bot_name,
        "bot_open_id": bot_open_id,
    }
```
> ⚠️ `_begin_registration`/`_poll_registration` 是引擎 `_` 前缀私有函数。**实现期先验证**：
> ```bash
> docker run --rm --entrypoint sh hermes-runtime:v2026.6.5-dev -c \
>   'python -c "from gateway.platforms.feishu import _begin_registration,_poll_registration,probe_bot; print(\"ok\")"'
> ```
> 对 v2026.5.16 镜像重复一次。若任一 variant import 失败（私有 API 漂移），改用引擎公有 `qr_register()` + 捕获其 stdout 中以 `http` 开头的二维码 URL 行（仿 `_QRLineWriter` 模式），但 `qr_register` 把 begin+poll 合并，需用 stdout 抓 URL、用返回值取凭证。

- [ ] **Step 2: 确保 `import asyncio` 已在文件顶部**（channel.py 已 import asyncio，无需重复）。

- [ ] **Step 3（若有 pytest 环境）: 写 mock 测试**

`tests/test_feishu_channel.py`（mock 引擎三函数，断言事件序列 qrcode→credentials；poll 返回 None 时 failed）。若无引擎依赖的 mock 框架，跳到 Step 4 手测。

- [ ] **Step 4: 本地真机手测（Step 5 路由就绪后做，见 Task 8 Step 5）。**

- [ ] **Step 5: 提交**
```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py
git commit -m "feat(channel): oc-ops 新增飞书扫码注册 SSE 事件流（两 variant）"
```

---

### Task 7: oc-ops 飞书手填校验与 health 态状态查询

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`

- [ ] **Step 1: feishu_probe（手填凭证即时校验）**

两个 `ocops/channel.py` 新增（同步函数，driver 引擎 `probe_bot`）：
```python
def feishu_probe(app_id: str, app_secret: str, domain: str = "feishu") -> dict:
    """手填模式即时校验飞书凭证：返回 {"ok": bool, "bot_name": str|None, "bot_open_id": str|None}。

    驱动引擎 gateway.platforms.feishu.probe_bot（内部走 /open-apis/bot/v3/info）。
    SDK 不可用或凭证无效返回 ok=False，不抛异常。
    """
    try:
        from gateway.platforms.feishu import probe_bot
    except ImportError:
        return {"ok": False, "bot_name": None, "bot_open_id": None}
    try:
        info = probe_bot(app_id, app_secret, domain)
    except Exception:  # noqa: BLE001
        info = None
    if not info:
        return {"ok": False, "bot_name": None, "bot_open_id": None}
    return {"ok": True, "bot_name": info.get("bot_name"), "bot_open_id": info.get("bot_open_id")}
```

- [ ] **Step 2: channel_status 支持 feishu（走 health 态）**

修改两个 `ocops/channel.py` 的 `channel_status`，在 `weixin` 文件态分支外加 feishu 分支（查 api_server `/health/detailed` 的 `platforms.feishu`）：
```python
def channel_status(channel: str, data_root: Path) -> dict:
    """查询渠道绑定态：weixin 走 accounts 文件态；feishu 走 api_server /health/detailed。"""
    if channel == "feishu":
        return _feishu_status()
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", f"unknown channel: {channel}")
    # ... 原有 weixin 文件态逻辑不变 ...


def _feishu_status() -> dict:
    """读 hermes api_server /health/detailed 的 platforms.feishu.platform_state，
    映射为渠道绑定态：connected→bound；fatal→failed(带原因)；其他→pending。"""
    import json as _json
    import urllib.request as _u
    api_base = "http://127.0.0.1:8642"  # 与 conversation._API_BASE 一致
    req = _u.Request(api_base + "/health/detailed", method="GET")
    key = _api_server_key()  # 复用 conversation.py 同款取 key；若不可跨模块引用则内联同逻辑
    if key:
        req.add_header("Authorization", "Bearer " + key)
    try:
        with _u.urlopen(req, timeout=10) as resp:
            data = _json.loads(resp.read().decode("utf-8"))
    except Exception as e:  # noqa: BLE001
        raise OpsError("INTERNAL", f"查询 /health/detailed 失败: {e}")
    fe = (data.get("platforms") or {}).get("feishu") or {}
    state = fe.get("platform_state", "")
    if state == "connected":
        return {"channel": "feishu", "bound": True,
                "platform_state": state,
                "bot_open_id": fe.get("bot_open_id", "")}
    if state == "fatal":
        return {"channel": "feishu", "bound": False, "platform_state": state,
                "error_code": fe.get("error_code", ""), "error_message": fe.get("error_message", "")}
    return {"channel": "feishu", "bound": False, "platform_state": state or "connecting"}
```
> `_api_server_key` 若定义在 `conversation.py`，在 `channel.py` 顶部 `from ocops.conversation import _api_server_key`，或把取 key 逻辑内联（读 `OC_CONTROL_TOKEN`/api_server key env，与 conversation 一致）。实现期对照 `conversation.py` 确认 key 来源。

- [ ] **Step 3: 编译/import 校验**

Run（两 variant 各一次）:
```bash
docker run --rm --entrypoint sh hermes-runtime:v2026.6.5-dev -c \
  'cd /usr/local/lib && python -c "import ocops.channel" 2>&1 | head'
```
> 注：宿主机 `runtime/hermes/.../ocops/channel.py` 是构建期 COPY 进镜像的源；本步骤需在「改完源 + 重建镜像」后做，或先用 `python -m py_compile runtime/hermes/hermes-v2026.6.5/ocops/channel.py` 做语法校验。
Run: `python -m py_compile runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py`
Expected: 无语法错误。

- [ ] **Step 4: 提交**
```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py
git commit -m "feat(channel): oc-ops 飞书手填校验与 health 态状态查询（两 variant）"
```

---

### Task 8: oc-ops 注册飞书 register SSE 路由

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/server.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/server.py`

- [ ] **Step 1: 加 SSE handler**

两个 `ocops/server.py` 在 `channel_login` handler 旁新增（仿其 StreamingResponse 写法）：
```python
async def feishu_register(request):
    """POST /oc/channels/feishu/register：把 channel.feishu_register async generator 转 SSE。

    query/body 可带 domain（feishu|lark），默认 feishu。鉴权由 AuthMiddleware 统一处理。
    """
    domain = request.query_params.get("domain", "feishu")

    async def gen():
        async for ev in channel.feishu_register(domain):
            yield f"data: {json.dumps(ev, ensure_ascii=False)}\n\n"

    return StreamingResponse(gen(), media_type="text/event-stream")
```

- [ ] **Step 2: 加手填校验 handler**

```python
async def feishu_probe(request):
    """POST /oc/channels/feishu/probe：手填模式即时校验凭证，返回 {ok, bot_name, bot_open_id}。"""
    body = await request.json()
    res = channel.feishu_probe(body.get("app_id", ""), body.get("app_secret", ""), body.get("domain", "feishu"))
    return JSONResponse(res)
```

- [ ] **Step 3: 注册路由**

两个 `ocops/server.py` 的 `routes` 列表，在 `Route("/oc/channels/{channel}/login", ...)` 旁加：
```python
    Route("/oc/channels/feishu/register", feishu_register, methods=["POST"]),
    Route("/oc/channels/feishu/probe", feishu_probe, methods=["POST"]),
```
> `/oc/channels/{channel}/status` 与 `/unbind` 已存在且 `{channel}` 通配，feishu 复用（status 已在 Task 7 支持 feishu；unbind 对 feishu 是 no-op 文件态，manager 侧 unbind 真正动作是删 Secret key + 重启，见 Task 17）。

- [ ] **Step 4: 语法校验**

Run: `python -m py_compile runtime/hermes/hermes-v2026.6.5/ocops/server.py runtime/hermes/hermes-v2026.5.16/ocops/server.py`
Expected: 无报错。

- [ ] **Step 5: 提交**
```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/server.py runtime/hermes/hermes-v2026.5.16/ocops/server.py
git commit -m "feat(channel): oc-ops 注册飞书 register/probe 路由（两 variant）"
```

---

### Task 9: Dockerfile 预装飞书 SDK 依赖（两 variant）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/Dockerfile`
- Modify: `runtime/hermes/hermes-v2026.5.16/Dockerfile`

- [ ] **Step 1: 预装 lark-oapi + websockets**

两个 Dockerfile 在「显式预装 weixin platform 必需依赖」那条 `uv pip install ... aiohttp cryptography qrcode` 之后新增：
```dockerfile
# 显式预装 feishu / lark platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
# 引擎 gateway/platforms/feishu.py 长连接走 lark_oapi + websockets；扫码注册用 urllib 不依赖 SDK，
# 但实际 WebSocket 运行必须有这两个包，否则飞书平台起不来（线上 pod 出网受限时 lazy 装会失败）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      lark-oapi==1.5.3 websockets
```
> 版本依据：引擎 `tools/lazy_deps.py` 的 `"platform.feishu": ("lark-oapi==1.5.3", ...)`。实现期复核两 variant 的 lazy_deps 是否同版本：
> ```bash
> docker run --rm --entrypoint sh hermes-runtime:v2026.5.16-dev -c \
>   'grep -A2 platform.feishu /usr/local/lib/hermes-agent/tools/lazy_deps*.py'
> ```
> 若 5.16 引擎无 `feishu.py` 或版本不同，按其实际值调整本步骤并在 spec/计划记录。

- [ ] **Step 2: 重建镜像验证依赖装入**

Run（本地 k3d 构建链，参考 Makefile hermes 构建 target）:
```bash
docker build -t hermes-runtime:v2026.6.5-feishu-test runtime/hermes/hermes-v2026.6.5/
docker run --rm --entrypoint sh hermes-runtime:v2026.6.5-feishu-test -c \
  'python -c "import lark_oapi, websockets; print(\"lark\", lark_oapi.__version__, \"ws ok\")"'
```
Expected: 输出 `lark 1.5.3 ws ok`（不再 ImportError）。

- [ ] **Step 3: 提交**
```bash
git add runtime/hermes/hermes-v2026.6.5/Dockerfile runtime/hermes/hermes-v2026.5.16/Dockerfile
git commit -m "feat(channel): hermes 两 variant 预装 lark-oapi 与 websockets"
```

---

## Phase 4 · manager oc-ops 客户端

### Task 10: ocops 客户端飞书方法（register SSE / probe / health 状态）

**Files:**
- Create: `internal/integrations/ocops/client_feishu.go`
- Modify: `internal/integrations/ocops/types_channel.go`（加飞书 DTO）
- Test: `internal/integrations/ocops/client_feishu_test.go`

- [ ] **Step 1: 写失败测试（用 httptest SSE server）**

`client_feishu_test.go`：
```go
// TestFeishuRegisterParsesEvents 验证 SSE 客户端把 qrcode/credentials 事件解析为 channel。
func TestFeishuRegisterParsesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"event\":\"qrcode\",\"url\":\"https://open.feishu.cn/qr/x\"}\n\n")
		fmt.Fprint(w, "data: {\"event\":\"credentials\",\"app_id\":\"cli_x\",\"app_secret\":\"sec\",\"domain\":\"feishu\",\"bot_name\":\"Bot\"}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()
	c := NewClient(srv.Client()) // 按现有 Client 构造方式
	ep := Endpoint{BaseURL: srv.URL, Token: "t"}
	events, err := c.FeishuRegister(context.Background(), ep, "feishu")
	require.NoError(t, err)
	var got []FeishuRegisterEvent
	for ev := range events {
		got = append(got, ev)
	}
	require.Equal(t, "qrcode", got[0].Event)
	require.Equal(t, "https://open.feishu.cn/qr/x", got[0].URL)
	require.Equal(t, "credentials", got[1].Event)
	require.Equal(t, "cli_x", got[1].AppID)
	require.Equal(t, "sec", got[1].AppSecret)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/ocops/ -run TestFeishuRegister -v`
Expected: FAIL。

- [ ] **Step 3: 加 DTO**

`internal/integrations/ocops/types_channel.go` 追加：
```go
// FeishuRegisterEvent 是 oc-ops /oc/channels/feishu/register SSE 的一条事件。
type FeishuRegisterEvent struct {
	Event     string `json:"event"`               // qrcode | credentials | failed
	URL       string `json:"url,omitempty"`       // qrcode 事件的二维码 URL
	AppID     string `json:"app_id,omitempty"`    // credentials 事件
	AppSecret string `json:"app_secret,omitempty"`
	Domain    string `json:"domain,omitempty"`
	BotName   string `json:"bot_name,omitempty"`
	BotOpenID string `json:"bot_open_id,omitempty"`
	Reason    string `json:"reason,omitempty"`    // failed 事件原因
}

// FeishuProbeResult 是手填校验返回。
type FeishuProbeResult struct {
	OK        bool   `json:"ok"`
	BotName   string `json:"bot_name"`
	BotOpenID string `json:"bot_open_id"`
}
```

- [ ] **Step 4: 实现客户端方法**

`internal/integrations/ocops/client_feishu.go`（SSE 消费仿现有 weixin 登录 SSE 消费；HTTP probe 仿现有同步请求）：
```go
package ocops

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// FeishuRegister 发起 oc-ops 飞书扫码注册 SSE，把事件逐条投递到返回 channel；流结束即关闭 channel。
func (c *Client) FeishuRegister(ctx context.Context, ep Endpoint, domain string) (<-chan FeishuRegisterEvent, error) {
	url := strings.TrimRight(ep.BaseURL, "/") + "/oc/channels/feishu/register?domain=" + domain
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发起飞书注册 SSE 失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("飞书注册 SSE 状态码 %d", resp.StatusCode)
	}
	out := make(chan FeishuRegisterEvent, 4)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(line[len("data:"):])
			var ev FeishuRegisterEvent
			if json.Unmarshal(payload, &ev) == nil {
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// FeishuProbe 手填模式即时校验飞书凭证。
func (c *Client) FeishuProbe(ctx context.Context, ep Endpoint, appID, appSecret, domain string) (FeishuProbeResult, error) {
	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret, "domain": domain})
	url := strings.TrimRight(ep.BaseURL, "/") + "/oc/channels/feishu/probe"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FeishuProbeResult{}, fmt.Errorf("飞书凭证校验失败: %w", err)
	}
	defer resp.Body.Close()
	var res FeishuProbeResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return FeishuProbeResult{}, fmt.Errorf("解析飞书校验响应失败: %w", err)
	}
	return res, nil
}
```
> 字段名 `c.httpClient`、`Client` 构造、`Endpoint{BaseURL,Token}` 按现有 `internal/integrations/ocops/` 实际定义对齐（实现期先读 `client.go` 确认）。飞书 health 态状态查询复用现有 `ChannelStatus(ctx, ep, "feishu")`（oc-ops 端 Task 7 已让 `/oc/channels/feishu/status` 走 health；client `ChannelStatus` 解析需能读 `platform_state`/`bound`，若现有 `ChannelStatus` DTO 无这些字段则在 `types_channel.go` 的 `ChannelStatus` 补 `PlatformState`/`ErrorMessage`）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/integrations/ocops/ -run 'TestFeishuRegister|TestFeishuProbe' -v`
Expected: PASS。

- [ ] **Step 6: 提交**
```bash
git add internal/integrations/ocops/client_feishu.go internal/integrations/ocops/types_channel.go internal/integrations/ocops/client_feishu_test.go
git commit -m "feat(channel): manager oc-ops 客户端新增飞书注册 SSE 与校验方法"
```

---

## Phase 5 · manager FeishuAdapter（双模式 + 凭证 handoff）

### Task 11: FeishuAdapter 扫码模式 + 凭证 handoff

**Files:**
- Create: `internal/integrations/channel/feishu.go`
- Test: `internal/integrations/channel/feishu_test.go`

设计：`FeishuAdapter` 实现 `ChannelAdapter`。内部按 appID 维护私有状态 `{status, qrURL, creds, errMsg}`。扫码 `BeginAuth` 起 SSE，读到 qrcode 即返回 challenge，并 `go consume` 后台读到 `credentials` 存进私有 `creds`、status 置 `AuthStatusPending`（仍"验证中"，**secret 不进 AuthProgress**）；`failed` 置 failed。新增飞书专用方法 `TakeCredentials(appID)` 供 worker 取凭证（取后清空）。

- [ ] **Step 1: 写失败测试**

`internal/integrations/channel/feishu_test.go`：
```go
// fakeFeishuRunner 是 FeishuRegisterRunner 的测试实现。
type fakeFeishuRunner struct{ events []ocops.FeishuRegisterEvent }

func (r *fakeFeishuRunner) StreamFeishuRegister(_ context.Context, _ AuthInput, _ string) (<-chan ocops.FeishuRegisterEvent, error) {
	ch := make(chan ocops.FeishuRegisterEvent, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// TestFeishuAdapterScanReturnsQRThenCredentials 验证扫码模式：BeginAuth 返回二维码，
// 后台消费 credentials 事件后 TakeCredentials 可取出凭证，且 PollAuth 不泄露 secret。
func TestFeishuAdapterScanReturnsQRThenCredentials(t *testing.T) {
	runner := &fakeFeishuRunner{events: []ocops.FeishuRegisterEvent{
		{Event: "qrcode", URL: "https://open.feishu.cn/qr/x"},
		{Event: "credentials", AppID: "cli_x", AppSecret: "sec", Domain: "feishu", BotName: "Bot"},
	}}
	a := NewFeishuAdapter(runner)
	ch, err := a.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.NoError(t, err)
	require.Equal(t, "qrcode", ch.Type)
	require.Equal(t, "https://open.feishu.cn/qr/x", ch.QRCode)

	// 等后台消费 credentials。
	var creds *FeishuCredentials
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c, ok := a.TakeCredentials("app-1"); ok {
			creds = &c
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, creds)
	require.Equal(t, "cli_x", creds.AppID)
	require.Equal(t, "sec", creds.AppSecret)
	require.Equal(t, "Bot", creds.BotName)

	// PollAuth 不得含 secret。
	p, _ := a.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
	for _, v := range p.Metadata {
		require.NotEqual(t, "sec", v, "PollAuth 不得泄露 app_secret")
	}
}

// TestFeishuAdapterScanFailed 验证扫码失败事件→PollAuth 报 failed。
func TestFeishuAdapterScanFailed(t *testing.T) {
	runner := &fakeFeishuRunner{events: []ocops.FeishuRegisterEvent{
		{Event: "qrcode", URL: "u"},
		{Event: "failed", Reason: "registration timeout or denied"},
	}}
	a := NewFeishuAdapter(runner)
	_, err := a.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.NoError(t, err)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		p, _ := a.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
		if p.Status == AuthStatusFailed {
			require.Contains(t, p.ErrorMessage, "timeout")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("未达到 failed 状态")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/channel/ -run TestFeishuAdapterScan -v`
Expected: FAIL（无 FeishuAdapter）。

- [ ] **Step 3: 实现 FeishuAdapter（扫码部分）**

`internal/integrations/channel/feishu.go`：
```go
package channel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// FeishuRegisterRunner 抽象通过 oc-ops 触发飞书扫码注册 SSE 的能力。
type FeishuRegisterRunner interface {
	StreamFeishuRegister(ctx context.Context, input AuthInput, domain string) (<-chan ocops.FeishuRegisterEvent, error)
}

// FeishuCredentials 是扫码/手填取得的飞书凭证（仅在 manager 内部流转，secret 明文）。
type FeishuCredentials struct {
	AppID     string
	AppSecret string
	Domain    string
	BotName   string
	BotOpenID string
}

// feishuState 是单个 app 的飞书绑定内部状态（含敏感凭证，不对外序列化）。
type feishuState struct {
	status  AuthStatus
	qrURL   string
	errMsg  string
	creds   *FeishuCredentials
	updated time.Time
}

// FeishuAdapter 实现 ChannelAdapter：扫码模式经 oc-ops SSE 取凭证；凭证经 TakeCredentials 交给 worker。
type FeishuAdapter struct {
	runner FeishuRegisterRunner
	mu     sync.Mutex
	states map[string]*feishuState
}

// NewFeishuAdapter 创建飞书 adapter。
func NewFeishuAdapter(runner FeishuRegisterRunner) *FeishuAdapter {
	return &FeishuAdapter{runner: runner, states: map[string]*feishuState{}}
}

// Type 返回 feishu。
func (a *FeishuAdapter) Type() string { return domain.ChannelTypeFeishu }

// BeginAuth 启动扫码注册：读到二维码即返回 challenge，后台消费 credentials/failed。
// input.ChannelName 复用为 domain（feishu|lark）传递；为空默认 feishu。
func (a *FeishuAdapter) BeginAuth(ctx context.Context, input AuthInput) (AuthChallenge, error) {
	if a.runner == nil {
		return AuthChallenge{}, errors.New("feishu adapter 未配置 FeishuRegisterRunner")
	}
	feishuDomain := input.ChannelName
	if feishuDomain == "" {
		feishuDomain = "feishu"
	}
	events, err := a.runner.StreamFeishuRegister(ctx, input, feishuDomain)
	if err != nil {
		return AuthChallenge{}, fmt.Errorf("启动飞书扫码注册失败: %w", err)
	}
	for ev := range events {
		switch ev.Event {
		case "qrcode":
			if ev.URL == "" {
				a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: "二维码事件缺少 URL", updated: time.Now()})
				return AuthChallenge{}, errors.New("二维码事件缺少 URL")
			}
			a.set(input.AppID, feishuState{status: AuthStatusPending, qrURL: ev.URL, updated: time.Now()})
			go a.consume(input.AppID, events)
			return AuthChallenge{Type: "qrcode", QRCode: ev.URL, ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
		case "failed":
			a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: ev.Reason, updated: time.Now()})
			return AuthChallenge{}, fmt.Errorf("飞书扫码注册失败: %s", ev.Reason)
		}
	}
	a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: "未收到二维码事件", updated: time.Now()})
	return AuthChallenge{}, errors.New("飞书扫码注册未输出二维码")
}

// consume 后台消费剩余事件，落地 credentials/failed。
func (a *FeishuAdapter) consume(appID string, events <-chan ocops.FeishuRegisterEvent) {
	for ev := range events {
		switch ev.Event {
		case "credentials":
			a.set(appID, feishuState{
				status: AuthStatusPending, // 凭证已取，但连接未确认，仍 pending
				creds: &FeishuCredentials{
					AppID: ev.AppID, AppSecret: ev.AppSecret, Domain: ev.Domain,
					BotName: ev.BotName, BotOpenID: ev.BotOpenID,
				},
				updated: time.Now(),
			})
			return
		case "failed":
			a.set(appID, feishuState{status: AuthStatusFailed, errMsg: ev.Reason, updated: time.Now()})
			return
		}
	}
}

// PollAuth 返回不含 secret 的进度视图（供 HTTP 与 worker 通用读取状态）。
func (a *FeishuAdapter) PollAuth(_ context.Context, input AuthInput) (AuthProgress, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.states[input.AppID]
	if !ok {
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()}, nil
	}
	return AuthProgress{Status: st.status, ErrorMessage: st.errMsg, UpdatedAt: st.updated}, nil
}

// TakeCredentials 取出并清空某 app 的飞书凭证（worker 专用；secret 经此交接，不走 PollAuth）。
func (a *FeishuAdapter) TakeCredentials(appID string) (FeishuCredentials, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.states[appID]
	if !ok || st.creds == nil {
		return FeishuCredentials{}, false
	}
	c := *st.creds
	st.creds = nil // 取后清空，避免重复注入
	return c, true
}

// SetCredentials 直接写入凭证（手填模式无 SSE，由 service/worker 注入；见 Task 12）。
func (a *FeishuAdapter) SetCredentials(appID string, c FeishuCredentials) {
	a.set(appID, feishuState{status: AuthStatusPending, creds: &c, updated: time.Now()})
}

func (a *FeishuAdapter) set(appID string, st feishuState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.states[appID] = &st
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/channel/ -run TestFeishuAdapterScan -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/integrations/channel/feishu.go internal/integrations/channel/feishu_test.go
git commit -m "feat(channel): FeishuAdapter 扫码模式与凭证 handoff"
```

---

### Task 12: FeishuAdapter 手填模式（probe 校验 + 直接置凭证）+ 注册

**Files:**
- Modify: `internal/integrations/channel/feishu.go`
- Modify: `cmd/server/main.go`
- Test: `internal/integrations/channel/feishu_test.go`

手填模式不走 SSE：service 拿到 body 里的 app_id/secret/domain 后，经 oc-ops `FeishuProbe` 校验（可选），再 `SetCredentials`，worker 注入。这里给 adapter 增一个手填入口便于单测；真正落库注入在 worker（Task 17）。

- [ ] **Step 1: 写失败测试（手填 probe 通过/失败）**

```go
// fakeFeishuProber 模拟 oc-ops 手填校验。
type fakeFeishuProber struct{ ok bool; botName string }

func (p *fakeFeishuProber) ProbeFeishu(_ context.Context, _ AuthInput, _, _, _ string) (bool, string, string, error) {
	return p.ok, p.botName, "", nil
}

// TestFeishuAdapterManualProbeOK 验证手填校验通过后置凭证 + bot_name。
func TestFeishuAdapterManualProbeOK(t *testing.T) {
	a := NewFeishuAdapter(nil)
	a.SetProber(&fakeFeishuProber{ok: true, botName: "Bot"})
	ch, err := a.BeginManual(context.Background(), AuthInput{AppID: "app-1"},
		FeishuCredentials{AppID: "cli_x", AppSecret: "sec", Domain: "feishu"})
	require.NoError(t, err)
	require.Equal(t, "feishu_manual", ch.Type)
	c, ok := a.TakeCredentials("app-1")
	require.True(t, ok)
	require.Equal(t, "Bot", c.BotName)
}

// TestFeishuAdapterManualProbeFail 验证校验失败返回错误且不置凭证。
func TestFeishuAdapterManualProbeFail(t *testing.T) {
	a := NewFeishuAdapter(nil)
	a.SetProber(&fakeFeishuProber{ok: false})
	_, err := a.BeginManual(context.Background(), AuthInput{AppID: "app-1"},
		FeishuCredentials{AppID: "cli_x", AppSecret: "bad", Domain: "feishu"})
	require.Error(t, err)
	_, ok := a.TakeCredentials("app-1")
	require.False(t, ok)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/channel/ -run TestFeishuAdapterManual -v`
Expected: FAIL。

- [ ] **Step 3: 实现手填入口与 prober**

`feishu.go` 追加：
```go
// FeishuProber 抽象手填模式经 oc-ops 即时校验飞书凭证的能力。
type FeishuProber interface {
	ProbeFeishu(ctx context.Context, input AuthInput, appID, appSecret, domain string) (ok bool, botName, botOpenID string, err error)
}

// SetProber 注入手填校验器。
func (a *FeishuAdapter) SetProber(p FeishuProber) { a.prober = p }

// BeginManual 手填模式：可选 probe 校验，通过则置凭证（带回 bot_name/open_id）。
func (a *FeishuAdapter) BeginManual(ctx context.Context, input AuthInput, creds FeishuCredentials) (AuthChallenge, error) {
	if a.prober != nil {
		ok, botName, botOpenID, err := a.prober.ProbeFeishu(ctx, input, creds.AppID, creds.AppSecret, creds.Domain)
		if err != nil {
			return AuthChallenge{}, fmt.Errorf("飞书凭证校验失败: %w", err)
		}
		if !ok {
			a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: "飞书凭证无效", updated: time.Now()})
			return AuthChallenge{}, errors.New("飞书凭证无效")
		}
		creds.BotName, creds.BotOpenID = botName, botOpenID
	}
	a.SetCredentials(input.AppID, creds)
	return AuthChallenge{Type: "feishu_manual"}, nil
}
```
并在 `FeishuAdapter` struct 加字段 `prober FeishuProber`。

- [ ] **Step 4: main.go 注册 FeishuAdapter**

`cmd/server/main.go` 在微信注册旁加（runner/prober 用 ocopsClient 适配；endpoint 解析复用 worker 注入 AuthInput.Endpoint 的现状——飞书 runner 的 `StreamFeishuRegister` 内部按 `input.Endpoint` 调 ocops.Client.FeishuRegister）：
```go
	// 飞书渠道：扫码注册 SSE + 手填 probe 都经 oc-ops；runner/prober 适配 ocopsClient。
	feishuAdapter := channel.NewFeishuAdapter(channel.NewOcOpsFeishuRunner(ocopsClient))
	feishuAdapter.SetProber(channel.NewOcOpsFeishuProber(ocopsClient))
	if err := channelRegistry.Register(feishuAdapter); err != nil {
		return fmt.Errorf("注册飞书渠道失败: %w", err)
	}
```
新建 `internal/integrations/channel/feishu_runner.go`：
```go
package channel

import (
	"context"
	"oc-manager/internal/integrations/ocops"
)

// ocopsFeishuClient 窄接口：飞书注册 SSE 与手填校验。
type ocopsFeishuClient interface {
	FeishuRegister(ctx context.Context, ep ocops.Endpoint, domain string) (<-chan ocops.FeishuRegisterEvent, error)
	FeishuProbe(ctx context.Context, ep ocops.Endpoint, appID, appSecret, domain string) (ocops.FeishuProbeResult, error)
}

// OcOpsFeishuRunner 用 input.Endpoint 把注册 SSE 路由到目标 app 实例。
type OcOpsFeishuRunner struct{ ops ocopsFeishuClient }

func NewOcOpsFeishuRunner(ops ocopsFeishuClient) *OcOpsFeishuRunner { return &OcOpsFeishuRunner{ops: ops} }

func (r *OcOpsFeishuRunner) StreamFeishuRegister(ctx context.Context, input AuthInput, domain string) (<-chan ocops.FeishuRegisterEvent, error) {
	return r.ops.FeishuRegister(ctx, input.Endpoint, domain)
}

// OcOpsFeishuProber 经 oc-ops 手填校验。
type OcOpsFeishuProber struct{ ops ocopsFeishuClient }

func NewOcOpsFeishuProber(ops ocopsFeishuClient) *OcOpsFeishuProber { return &OcOpsFeishuProber{ops: ops} }

func (p *OcOpsFeishuProber) ProbeFeishu(ctx context.Context, input AuthInput, appID, appSecret, domain string) (bool, string, string, error) {
	res, err := p.ops.FeishuProbe(ctx, input.Endpoint, appID, appSecret, domain)
	if err != nil {
		return false, "", "", err
	}
	return res.OK, res.BotName, res.BotOpenID, nil
}
```

- [ ] **Step 5: 跑测试 + 编译**

Run: `go test ./internal/integrations/channel/ -run TestFeishuAdapterManual -v && go build ./...`
Expected: PASS + 编译通过。

- [ ] **Step 6: 提交**
```bash
git add internal/integrations/channel/feishu.go internal/integrations/channel/feishu_runner.go internal/integrations/channel/feishu_test.go cmd/server/main.go
git commit -m "feat(channel): FeishuAdapter 手填模式与 oc-ops runner/prober 注册"
```

---

## Phase 6 · service / handler / DTO 双模式分流

### Task 13: 新增 channel_bindings 飞书凭证写入查询

**Files:**
- Modify: `internal/store/queries/channel_bindings.sql`
- Test: 由 sqlc 生成 + 后续 service 测试覆盖

- [ ] **Step 1: 加 upsert 与凭证写入查询**

`internal/store/queries/channel_bindings.sql` 追加：
```sql
-- name: UpsertChannelBindingUnbound :exec
-- 飞书无预建绑定行，BeginAuth 时 create-on-demand（已存在则忽略）。
INSERT INTO channel_bindings (id, app_id, channel_type, status)
VALUES (?, ?, ?, 'unbound')
ON DUPLICATE KEY UPDATE id = id;

-- name: SetFeishuCredentials :exec
-- 写入飞书凭证 metadata（app_id 明文 + secret 密文 + domain + bot 信息 + injected 标记）并置状态。
UPDATE channel_bindings
SET metadata_json = ?, status = ?, last_error = NULL, updated_at = now()
WHERE app_id = ? AND channel_type = 'feishu' AND status <> 'deleted';
```
> 注意：`UpsertChannelBindingUnbound` 依赖唯一约束 `(app_active_key, channel_type)`（Task 1 已建），`ON DUPLICATE KEY` 才能命中。

- [ ] **Step 2: 重新生成 sqlc**

Run: `make sqlc-gen`（或项目实际的 sqlc 生成命令；查 Makefile）
Expected: `internal/store/sqlc/channel_bindings.sql.go` 出现 `UpsertChannelBindingUnbound`、`SetFeishuCredentials` 方法。

- [ ] **Step 3: 编译**

Run: `go build ./...`
Expected: 通过。

- [ ] **Step 4: 提交**
```bash
git add internal/store/queries/channel_bindings.sql internal/store/sqlc/
git commit -m "feat(channel): 新增飞书绑定 upsert 与凭证写入查询"
```

---

### Task 14: service 双模式分流 + PollAuth 敏感字段过滤

**Files:**
- Modify: `internal/service/channel_service.go`
- Modify: `internal/api/handlers/dto.go`
- Test: `internal/service/channel_service_test.go`

- [ ] **Step 1: 写失败测试（手填分流 + create-on-demand + secret 过滤）**

`channel_service_test.go` 新增（扩展 `channelStub` 加 `UpsertChannelBindingUnbound`/`SetFeishuCredentials` 记录字段）：
```go
// TestChannelServiceBeginAuthFeishuManualCreatesBindingAndEncrypts
// 验证飞书手填：无绑定行时 create-on-demand，secret 加密写 metadata，入队 job。
func TestChannelServiceBeginAuthFeishuManualCreatesBinding(t *testing.T) {
	store := newChannelStub(t)
	store.bindingMissing = true // GetChannelBindingByAppAndType 首次返回 ErrNoRows
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	svc := NewChannelService(store, registry, withCipher(newTestCipher(t))) // service 需持 cipher
	req := FeishuAuthInput{Mode: "manual", AppID: "cli_x", AppSecret: "sec", Domain: "feishu"}

	res, err := svc.BeginFeishuAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, req)
	require.NoError(t, err)
	require.NotEmpty(t, res.JobID)
	require.True(t, store.upsertCalled, "应 create-on-demand 绑定行")
	require.NotEmpty(t, store.feishuMeta, "应写入飞书 metadata")
	require.NotContains(t, string(store.feishuMeta), "\"sec\"", "secret 必须加密，不得明文出现")
}

// TestChannelServicePollAuthRedactsSecret 验证 PollAuth 不把 *_ciphertext 透传前端。
func TestChannelServicePollAuthRedactsSecret(t *testing.T) {
	store := newChannelStub(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	store.binding.MetadataJson = []byte(`{"app_id":"cli_x","app_secret_ciphertext":"ENC","domain":"feishu"}`)
	svc := NewChannelService(store, channel.NewRegistry())
	p, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeFeishu)
	require.NoError(t, err)
	require.Equal(t, "cli_x", p.Metadata["app_id"])
	_, leaked := p.Metadata["app_secret_ciphertext"]
	require.False(t, leaked, "secret 密文不得透传前端")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestChannelServiceBeginAuthFeishuManual|TestChannelServicePollAuthRedacts' -v`
Expected: FAIL。

- [ ] **Step 3: DTO + service 实现**

`internal/api/handlers/dto.go` 加：
```go
// FeishuChannelAuthRequest 是飞书渠道发起请求体（扫码 mode=scan 无需凭证；手填 mode=manual 带凭证）。
type FeishuChannelAuthRequest struct {
	Mode      string `json:"mode" binding:"required,oneof=scan manual"` // scan | manual
	Domain    string `json:"domain"`                                    // feishu | lark，默认 feishu
	AppID     string `json:"app_id"`                                    // manual 必填
	AppSecret string `json:"app_secret"`                                // manual 必填
}
```
`internal/service/channel_service.go` 加 service 入参类型与方法（与现有 `BeginAuth` 并列；扫码模式仍走原 `BeginAuth` 的 job 入队，手填模式先写凭证 metadata）：
```go
// FeishuAuthInput 是飞书发起的 service 入参。
type FeishuAuthInput struct {
	Mode      string
	Domain    string
	AppID     string
	AppSecret string
}

// BeginFeishuAuth 飞书双模式发起：create-on-demand 绑定行；manual 先加密写凭证 metadata；
// 两模式都入队 channel_start_login job 推进绑定（worker 按 metadata 区分阶段）。
func (s *ChannelService) BeginFeishuAuth(ctx context.Context, principal auth.Principal, appID string, in FeishuAuthInput) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	// create-on-demand：飞书无预建绑定行。
	if err := s.store.UpsertChannelBindingUnbound(ctx, sqlc.UpsertChannelBindingUnboundParams{
		ID: newUUID(), AppID: app.ID, ChannelType: domain.ChannelTypeFeishu,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建飞书绑定行失败: %w", err)
	}
	feishuDomain := in.Domain
	if feishuDomain == "" {
		feishuDomain = "feishu"
	}
	if in.Mode == "manual" {
		if in.AppID == "" || in.AppSecret == "" {
			return ChallengeResult{}, ErrInvalidArgument // 现有 service 错误；无则用 fmt.Errorf 包装
		}
		enc, err := s.cipher.Encrypt([]byte(in.AppSecret))
		if err != nil {
			return ChallengeResult{}, fmt.Errorf("加密飞书 secret 失败: %w", err)
		}
		meta, _ := json.Marshal(map[string]any{
			"app_id": in.AppID, "app_secret_ciphertext": enc, "domain": feishuDomain,
			"acquired_by": "manual", "injected": "false",
		})
		if err := s.store.SetFeishuCredentials(ctx, sqlc.SetFeishuCredentialsParams{
			MetadataJson: meta, Status: domain.ChannelStatusPendingAuth, AppID: app.ID,
		}); err != nil {
			return ChallengeResult{}, fmt.Errorf("写入飞书凭证失败: %w", err)
		}
	} else {
		// scan：仅置 pending_auth + 把 domain 暂存 metadata（worker 经 adapter 取二维码/凭证）。
		meta, _ := json.Marshal(map[string]any{"domain": feishuDomain, "acquired_by": "qr", "injected": "false"})
		if err := s.store.SetFeishuCredentials(ctx, sqlc.SetFeishuCredentialsParams{
			MetadataJson: meta, Status: domain.ChannelStatusPendingAuth, AppID: app.ID,
		}); err != nil {
			return ChallengeResult{}, fmt.Errorf("初始化飞书绑定失败: %w", err)
		}
	}
	// 入队 channel_start_login（payload 带 mode/domain，worker 分流）。
	jobID := newUUID()
	payload, _ := json.Marshal(map[string]any{
		"app_id": app.ID, "channel_type": domain.ChannelTypeFeishu,
		"mode": in.Mode, "domain": feishuDomain, "requested_by": principal.UserID,
	})
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID: jobID, Type: domain.JobTypeChannelStartLogin, Priority: 90,
		RunAfter: time.Now(), MaxAttempts: 3, PayloadJson: payload,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建飞书登录任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{Status: domain.ChannelStatusPendingAuth, ChannelType: domain.ChannelTypeFeishu, JobID: jobID}, nil
}
```
> `s.cipher`：给 `ChannelService` 加 `cipher *auth.Cipher` 字段与构造注入（`NewChannelService` 增可选参数或新构造）。`ChannelStore` 接口加 `UpsertChannelBindingUnbound`、`SetFeishuCredentials`。

`PollAuth` 敏感字段过滤——修改 `channelBindingMetadata`（或在 PollAuth 调用后过滤）：
```go
// channelBindingMetadata 归一化为 string map，并剔除敏感凭证 key（不得透传前端）。
func channelBindingMetadata(raw []byte) map[string]string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return map[string]string{}
	}
	metadata := make(map[string]string, len(data))
	for key, value := range data {
		// 过滤密文/secret 类敏感字段。
		if strings.Contains(key, "ciphertext") || strings.Contains(strings.ToLower(key), "secret") {
			continue
		}
		switch v := value.(type) {
		case string:
			metadata[key] = v
		case map[string]any:
			for hintKey, hintValue := range v {
				if hint, ok := hintValue.(string); ok {
					metadata[hintKey] = hint
				}
			}
		}
	}
	return metadata
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -run 'TestChannelServiceBeginAuthFeishuManual|TestChannelServicePollAuthRedacts' -v`
Expected: PASS。

- [ ] **Step 5: 回归**

Run: `go test ./internal/service/ -v`
Expected: PASS（确认现有微信测试未破坏）。

- [ ] **Step 6: 提交**
```bash
git add internal/service/channel_service.go internal/api/handlers/dto.go internal/service/channel_service_test.go
git commit -m "feat(channel): service 飞书双模式发起与 PollAuth 敏感字段过滤"
```

---

### Task 15: handler 飞书发起路由（读 body 分流）

**Files:**
- Modify: `internal/api/handlers/channels.go`
- Test: `internal/api/handlers/channels_test.go`

- [ ] **Step 1: 写失败测试**

`channels_test.go`（仿现有 handler 测试，用 gin test context + mock channelService）：
```go
// TestChannelsHandlerBeginFeishuAuthScan 验证飞书 scan 请求体被正确解析并调 service。
func TestChannelsHandlerBeginFeishuAuthScan(t *testing.T) {
	svc := &mockChannelService{challenge: service.ChallengeResult{Status: "pending_auth", ChannelType: "feishu", JobID: "j1"}}
	h := NewChannelsHandler(svc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "appId", Value: "app-1"}, {Key: "channelType", Value: "feishu"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"mode":"scan","domain":"feishu"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setPrincipal(c, channelOrgAdminPrincipal()) // 测试 helper
	h.BeginAuth(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, svc.beganFeishu)
	require.Equal(t, "scan", svc.lastFeishuMode)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/handlers/ -run TestChannelsHandlerBeginFeishuAuth -v`
Expected: FAIL。

- [ ] **Step 3: handler 分流**

`channels.go` 的 `channelService` 接口加 `BeginFeishuAuth`；`BeginAuth` handler 按 channelType 分流：
```go
type channelService interface {
	BeginAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (service.ChallengeResult, error)
	BeginFeishuAuth(ctx context.Context, principal auth.Principal, appID string, in service.FeishuAuthInput) (service.ChallengeResult, error)
	PollAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (service.ProgressResult, error)
	Unbind(ctx context.Context, principal auth.Principal, appID, channelType string) error
}

func (h *ChannelsHandler) BeginAuth(c *gin.Context) {
	principal := principalFromCtx(c)
	appID, channelType := c.Param("appId"), c.Param("channelType")
	// 飞书走双模式专用入口（读请求体 mode/domain/凭证）。
	if channelType == domain.ChannelTypeFeishu {
		var req FeishuChannelAuthRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgChannelInvalidRequest)
			return
		}
		result, err := h.service.BeginFeishuAuth(c.Request.Context(), principal, appID, service.FeishuAuthInput{
			Mode: req.Mode, Domain: req.Domain, AppID: req.AppID, AppSecret: req.AppSecret,
		})
		if err != nil {
			writeChannelError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": result})
		return
	}
	result, err := h.service.BeginAuth(c.Request.Context(), principal, appID, channelType)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge": result})
}
```
> `apierror.MsgChannelInvalidRequest` 若不存在则新增（messages_*.go 中英 catalog，见 i18n 规范）。

- [ ] **Step 4: 跑测试确认通过 + 回归**

Run: `go test ./internal/api/handlers/ -run TestChannelsHandler -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/api/handlers/channels.go internal/api/handlers/channels_test.go internal/api/apierror/
git commit -m "feat(channel): handler 飞书发起读请求体双模式分流"
```

---

## Phase 7 · worker 两阶段绑定推进

### Task 16: ChannelStartLoginHandler 飞书分支（出码 / 手填就绪）

**Files:**
- Modify: `internal/worker/handlers/channel_login.go`
- Test: `internal/worker/handlers/channel_login_test.go`

设计：飞书 payload 带 `mode`/`domain`。`ChannelStartLoginHandler.Handle` 飞书分支：
- scan：调 `adapter.BeginAuth`（input.ChannelName=domain）→ 拿二维码 challenge → `SetChannelBindingChallenge`（写 qrcode metadata 供前端）→ 入队 check（5s）。
- manual：service 已写好凭证 metadata（status=pending_auth）→ 直接入队 check（worker check 阶段读 metadata 注入）。

- [ ] **Step 1: 写失败测试**

```go
// TestChannelStartLoginFeishuScanSavesQR 验证飞书扫码：保存二维码 metadata 并入队 check。
func TestChannelStartLoginFeishuScanSavesQR(t *testing.T) {
	store := newWorkerChannelStub(t) // 复用/仿 worker 既有 stub
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(&fakeFeishuRunner{events: []ocops.FeishuRegisterEvent{
		{Event: "qrcode", URL: "https://open.feishu.cn/qr/x"},
	}}))
	h := NewChannelStartLoginHandler(store, registry, stubResolver{})
	job := feishuJob(t, "scan", "feishu") // payload helper
	require.NoError(t, h.Handle(context.Background(), job))
	require.Contains(t, string(store.lastChallengeMeta), "open.feishu.cn/qr/x")
	require.True(t, store.checkEnqueued)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestChannelStartLoginFeishu -v`
Expected: FAIL。

- [ ] **Step 3: payload 加字段 + Handle 飞书分支**

`channelLoginPayload` 加 `Mode`/`Domain`：
```go
type channelLoginPayload struct {
	AppID       string `json:"app_id"`
	ChannelType string `json:"channel_type"`
	Mode        string `json:"mode,omitempty"`   // feishu: scan|manual
	Domain      string `json:"domain,omitempty"` // feishu: feishu|lark
}
```
`ChannelStartLoginHandler.Handle` 在调用 `adapter.BeginAuth` 处，对飞书分流：
```go
	if payload.ChannelType == domain.ChannelTypeFeishu {
		if payload.Mode == "manual" {
			// 手填：凭证已由 service 写入 metadata，直接进入 check（注入阶段）。
			return h.enqueueCheck(ctx, payload, 1*time.Second)
		}
		// 扫码：起 SSE 取二维码，domain 经 AuthInput.ChannelName 传给 adapter。
		challenge, err := adapter.BeginAuth(ctx, channel.AuthInput{
			AppID: payload.AppID, OwnerUserID: app.OwnerUserID, ChannelName: payload.Domain, Endpoint: endpoint,
		})
		if err != nil {
			_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
				AppID: binding.AppID, ChannelType: binding.ChannelType,
				Status: domain.ChannelStatusFailed, LastError: null.StringFrom(redactlog.SafeErrorMessage(err)),
			})
			return fmt.Errorf("发起飞书扫码失败: %w", err)
		}
		meta, _ := json.Marshal(map[string]any{
			"type": challenge.Type, "qrcode": challenge.QRCode, "expires_at": challenge.ExpiresAt,
			"acquired_by": "qr", "domain": payload.Domain,
		})
		if err := h.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
			AppID: binding.AppID, ChannelType: binding.ChannelType, MetadataJson: meta,
		}); err != nil {
			return fmt.Errorf("保存飞书二维码失败: %w", err)
		}
		return h.enqueueCheck(ctx, payload, 5*time.Second)
	}
	// ...原微信逻辑不变...
```
> 注意：`SetChannelBindingChallenge` 会覆盖 metadata（含 domain），check 阶段注入时再 merge。为简化，scan 的 challenge metadata 保留 `domain`/`acquired_by`，注入阶段（Task 17）写凭证时合并保留这些字段。`enqueueCheck` 现为私有方法已存在。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/worker/handlers/ -run TestChannelStartLoginFeishu -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/worker/handlers/channel_login.go internal/worker/handlers/channel_login_test.go
git commit -m "feat(channel): worker 飞书扫码出码与手填就绪分支"
```

---

### Task 17: ChannelCheckBindingHandler 飞书两阶段（注入 → health 探测）

**Files:**
- Modify: `internal/worker/handlers/channel_login.go`
- Test: `internal/worker/handlers/channel_login_test.go`

设计：飞书 check 两阶段，靠 metadata `injected` 区分：
- **阶段 1（injected != "true"）**：取凭证（scan 经 `adapter.(*FeishuAdapter).TakeCredentials`；manual 从 metadata 解密已有凭证，或 service 已写好直接注入）→ 加密写 metadata（`injected="true"`）→ `PatchSecretKeys` 注入 feishu-* → `RolloutRestart` → 状态保持 pending_auth → 入队 check（接入连接探测）。若 scan 凭证未就绪且 adapter 状态非 failed → 继续等（re-enqueue）；failed → 置 failed。
- **阶段 2（injected == "true"）**：经 oc-ops `ChannelStatus(ep,"feishu")` 查 health → connected→`MarkChannelBindingBound`(bot_open_id 作 identity, bot_name 作 channel_name)；fatal→failed(error_message)；其他→re-enqueue。

worker 需新增依赖：`ChannelLoginStore` 加 `SetFeishuCredentials`；handler 加 k8s patch 能力（`FeishuSecretPatcher` 接口：`PatchFeishuSecret(ctx, appID, set)`、复用 `ChannelRestarter`）、`channelStatusClient`（查 health）、`cipher`。

- [ ] **Step 1: 写失败测试（阶段1 注入 + 阶段2 bound）**

```go
// TestFeishuCheckPhase1InjectsAndRestarts 验证扫码凭证就绪→加密落库+patch Secret+重启+标 injected。
func TestFeishuCheckPhase1InjectsAndRestarts(t *testing.T) {
	store := newWorkerChannelStub(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	store.binding.Status = domain.ChannelStatusPendingAuth
	store.binding.MetadataJson = []byte(`{"acquired_by":"qr","domain":"feishu","injected":"false"}`)
	fa := channel.NewFeishuAdapter(nil)
	fa.SetCredentials("app-1", channel.FeishuCredentials{AppID: "cli_x", AppSecret: "sec", Domain: "feishu", BotName: "Bot", BotOpenID: "ou_1"})
	registry := channel.NewRegistry()
	registry.MustRegister(fa)
	patcher := &fakeFeishuPatcher{}
	restarter := &fakeRestarter{}
	h := NewChannelCheckBindingHandler(store, registry, nil)
	h.SetRestarter(restarter)
	h.SetFeishuDeps(patcher, newTestCipher(t), &fakeHealthClient{}) // 新注入点
	require.NoError(t, h.Handle(context.Background(), feishuCheckJob(t)))
	require.True(t, patcher.patched, "应 patch feishu-* key")
	require.Equal(t, "cli_x", patcher.set["feishu-app-id"])
	require.Equal(t, "sec", patcher.set["feishu-app-secret"])
	require.True(t, restarter.restarted)
	require.Contains(t, string(store.feishuMeta), "\"injected\":\"true\"")
	require.NotContains(t, string(store.feishuMeta), "\"sec\"", "secret 落库必须密文")
}

// TestFeishuCheckPhase2HealthConnectedBinds 验证已注入→health connected→MarkBound。
func TestFeishuCheckPhase2HealthConnectedBinds(t *testing.T) {
	store := newWorkerChannelStub(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	store.binding.MetadataJson = []byte(`{"app_id":"cli_x","domain":"feishu","bot_name":"Bot","bot_open_id":"ou_1","injected":"true"}`)
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	h := NewChannelCheckBindingHandler(store, registry, nil)
	h.SetFeishuDeps(&fakeFeishuPatcher{}, newTestCipher(t), &fakeHealthClient{state: "connected", botOpenID: "ou_1"})
	require.NoError(t, h.Handle(context.Background(), feishuCheckJob(t)))
	require.True(t, store.boundCalled)
	require.Equal(t, "ou_1", store.boundIdentity)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/worker/handlers/ -run TestFeishuCheck -v`
Expected: FAIL。

- [ ] **Step 3: 实现飞书两阶段 check**

`ChannelCheckBindingHandler` 加字段与注入：
```go
// FeishuSecretPatcher 抽象给 app Secret 增删飞书 key 的能力（k8sorch.KubernetesAdapter 实现）。
type FeishuSecretPatcher interface {
	PatchSecretKeys(ctx context.Context, appID string, set map[string]string, del []string) error
}

// FeishuHealthClient 查飞书 health 态状态（oc-ops ChannelStatus(feishu)）。
type FeishuHealthClient interface {
	FeishuStatus(ctx context.Context, appID string) (state, botOpenID, errMessage string, err error)
}

// SetFeishuDeps 注入飞书 check 两阶段所需依赖。
func (h *ChannelCheckBindingHandler) SetFeishuDeps(p FeishuSecretPatcher, cipher *auth.Cipher, hc FeishuHealthClient) {
	h.feishuPatcher, h.cipher, h.feishuHealth = p, cipher, hc
}
```
`Handle` 在 `payload.ChannelType == domain.ChannelTypeFeishu` 时走专用函数 `handleFeishuCheck`：
```go
func (h *ChannelCheckBindingHandler) handleFeishuCheck(ctx context.Context, app sqlc.App, binding sqlc.ChannelBinding, payload channelLoginPayload, adapter channel.ChannelAdapter) error {
	var meta map[string]any
	_ = json.Unmarshal(binding.MetadataJson, &meta)
	injected, _ := meta["injected"].(string)

	if injected != "true" {
		// ── 阶段 1：取凭证 → 注入 → 重启 ──
		creds, ok := h.takeFeishuCredentials(adapter, payload, meta)
		if !ok {
			// 凭证未就绪：检查 adapter 是否已 failed，否则继续等。
			if p, _ := adapter.PollAuth(ctx, channel.AuthInput{AppID: payload.AppID}); p.Status == channel.AuthStatusFailed {
				return h.failFeishu(ctx, app, binding, payload, p.ErrorMessage)
			}
			return enqueueChannelCheck(ctx, h.store, payload, 3*time.Second)
		}
		enc, err := h.cipher.Encrypt([]byte(creds.AppSecret))
		if err != nil {
			return fmt.Errorf("加密飞书 secret 失败: %w", err)
		}
		newMeta, _ := json.Marshal(map[string]any{
			"app_id": creds.AppID, "app_secret_ciphertext": enc, "domain": creds.Domain,
			"acquired_by": orDefault(meta["acquired_by"], "qr"),
			"bot_name": creds.BotName, "bot_open_id": creds.BotOpenID, "injected": "true",
		})
		if err := h.store.SetFeishuCredentials(ctx, sqlc.SetFeishuCredentialsParams{
			MetadataJson: newMeta, Status: domain.ChannelStatusPendingAuth, AppID: app.ID,
		}); err != nil {
			return fmt.Errorf("写入飞书凭证失败: %w", err)
		}
		if h.feishuPatcher != nil {
			if err := h.feishuPatcher.PatchSecretKeys(ctx, app.ID, map[string]string{
				"feishu-app-id": creds.AppID, "feishu-app-secret": creds.AppSecret, "feishu-domain": creds.Domain,
			}, nil); err != nil {
				return fmt.Errorf("patch 飞书 Secret 失败: %w", err)
			}
		}
		if h.restarter != nil {
			if err := h.restarter.RestartApp(ctx, app.ID); err != nil {
				slog.ErrorContext(ctx, "飞书注入后重启失败", "app_id", app.ID, redactlog.Err(err))
			}
		}
		return enqueueChannelCheck(ctx, h.store, payload, 8*time.Second) // 等重启 + 连接
	}

	// ── 阶段 2：health 探测连通 ──
	if h.feishuHealth == nil {
		return enqueueChannelCheck(ctx, h.store, payload, 5*time.Second)
	}
	state, botOpenID, errMsg, err := h.feishuHealth.FeishuStatus(ctx, app.ID)
	if err != nil {
		return enqueueChannelCheck(ctx, h.store, payload, 5*time.Second)
	}
	switch state {
	case "connected":
		identity := botOpenID
		if identity == "" {
			identity, _ = meta["bot_open_id"].(string)
		}
		channelName, _ := meta["bot_name"].(string)
		return h.finalizeChannelBound(ctx, app, binding, payload, identity, channelName, binding.MetadataJson)
	case "fatal":
		return h.failFeishu(ctx, app, binding, payload, errMsg)
	default:
		return enqueueChannelCheck(ctx, h.store, payload, 5*time.Second)
	}
}

// takeFeishuCredentials：scan 经 adapter.TakeCredentials；manual 从 metadata 解密。
func (h *ChannelCheckBindingHandler) takeFeishuCredentials(adapter channel.ChannelAdapter, payload channelLoginPayload, meta map[string]any) (channel.FeishuCredentials, bool) {
	if fa, ok := adapter.(*channel.FeishuAdapter); ok {
		if c, ok := fa.TakeCredentials(payload.AppID); ok {
			return c, true
		}
	}
	// manual：service 已把密文写进 metadata（acquired_by=manual），解密取出。
	if ab, _ := meta["acquired_by"].(string); ab == "manual" {
		enc, _ := meta["app_secret_ciphertext"].(string)
		appID, _ := meta["app_id"].(string)
		dom, _ := meta["domain"].(string)
		if enc != "" && appID != "" && h.cipher != nil {
			if plain, err := h.cipher.Decrypt(enc); err == nil {
				return channel.FeishuCredentials{AppID: appID, AppSecret: string(plain), Domain: dom}, true
			}
		}
	}
	return channel.FeishuCredentials{}, false
}
```
加 `failFeishu`（置 failed + 审计，仿现有 failed 分支）与 `orDefault` 小工具。`finalizeChannelBound` 复用现有；但现有版本 `if h.restarter != nil && payload.ChannelType == domain.ChannelTypeWeChat` 只对微信重启——飞书在阶段1已重启，阶段2 bound 不需再重启，保持原条件即可（飞书不进入该重启分支）。

manual 模式同样进 `handleFeishuCheck`：阶段1 `TakeCredentials` 走 metadata 解密分支（adapter 无 scan 凭证），注入+重启；之后阶段2 health 探测。

- [ ] **Step 4: main.go 注入飞书 check 依赖**

`cmd/server/main.go` 构造 `ChannelCheckBindingHandler` 后调 `SetFeishuDeps(k8sAdapter, cipher, ocopsFeishuHealth)`。`ocopsFeishuHealth` 适配 `ocopsClient.ChannelStatus(ep,"feishu")`→`FeishuStatus`（新建小适配器，经 endpoint resolver 解析 ep）。

- [ ] **Step 5: 跑测试 + 编译**

Run: `go test ./internal/worker/handlers/ -run TestFeishuCheck -v && go build ./...`
Expected: PASS + 编译通过。

- [ ] **Step 6: 提交**
```bash
git add internal/worker/handlers/channel_login.go internal/worker/handlers/channel_login_test.go cmd/server/main.go
git commit -m "feat(channel): worker 飞书两阶段 check（注入凭证→health 探测绑定）"
```

---

### Task 18: 解绑删飞书 Secret key + 重启

**Files:**
- Modify: `internal/service/channel_service.go`（`Unbind` 飞书分支）
- Test: `internal/service/channel_service_test.go`

- [ ] **Step 1: 写失败测试**

```go
// TestChannelServiceUnbindFeishuDeletesSecretKeys 验证飞书解绑删 Secret key + 重启 + 置 unbound_by_user。
func TestChannelServiceUnbindFeishuDeletesSecretKeys(t *testing.T) {
	store := newChannelStub(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	patcher := &fakeFeishuPatcher{}
	restarter := &fakeRestarter{}
	svc := NewChannelService(store, channel.NewRegistry())
	svc.SetFeishuUnbindDeps(patcher, restarter) // 新注入
	require.NoError(t, svc.Unbind(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, domain.ChannelTypeFeishu))
	require.Equal(t, domain.ChannelStatusUnboundByUser, store.lastStatus)
	require.ElementsMatch(t, []string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}, patcher.deleted)
	require.True(t, restarter.restarted)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestChannelServiceUnbindFeishu -v`
Expected: FAIL。

- [ ] **Step 3: Unbind 飞书分支**

`channel_service.go` 的 `Unbind`，在置 `unbound_by_user` 后加飞书清理（注入依赖经 `SetFeishuUnbindDeps`；endpoint 解析复用现状）：
```go
	if channelType == domain.ChannelTypeFeishu && s.feishuPatcher != nil {
		// 删 Secret 飞书 key + 重启，使引擎下次重启不再启用飞书平台。
		if err := s.feishuPatcher.PatchSecretKeys(ctx, app.ID, nil,
			[]string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}); err != nil {
			slog.ErrorContext(ctx, "解绑删飞书 Secret key 失败", "app_id", app.ID, redactlog.Err(err))
		}
		if s.feishuRestarter != nil {
			if err := s.feishuRestarter.RestartApp(ctx, app.ID); err != nil {
				slog.ErrorContext(ctx, "解绑后重启失败", "app_id", app.ID, redactlog.Err(err))
			}
		}
	}
```
> 给 `ChannelService` 加 `feishuPatcher`/`feishuRestarter` 字段 + `SetFeishuUnbindDeps` + main.go 注入。解绑同步删 key（不走 worker），因为是用户即时动作。

- [ ] **Step 4: 跑测试 + 编译**

Run: `go test ./internal/service/ -run TestChannelServiceUnbindFeishu -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/service/channel_service.go internal/service/channel_service_test.go cmd/server/main.go
git commit -m "feat(channel): 飞书解绑删 Secret key 并重启"
```

---

## Phase 8 · 前端（模式选择 + 二维码/表单双 UI + i18n）

### Task 19: AppChannelsTab 启用飞书并加双模式 UI

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
- Modify: `web/src/api/hooks/useChannel.ts`（加飞书发起方法）
- Test: 前端组件测试（若有 `*.spec.ts`）或随端到端覆盖

- [ ] **Step 1: 飞书 supported=true**

`AppChannelsTab.vue` 的 `channels` computed，把 feishu 行 `supported: false` 改 `true`、`statusLabel` 改 `t('apps.channels.supported')`：
```typescript
  { type: 'feishu', name: t('apps.channels.channelFeishu'), description: t('apps.channels.channelFeishuDesc'), supported: true, statusLabel: t('apps.channels.supported') },
```

- [ ] **Step 2: 加飞书发起 hook**

`useChannel.ts` 新增（扫码复用现有 GET 轮询 + 二维码渲染；发起改为带 body）：
```typescript
// 飞书发起（双模式）：scan 仅传 mode/domain；manual 传 app_id/app_secret/domain。
export function useBeginFeishuAuth(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (body: { mode: 'scan' | 'manual'; domain: string; app_id?: string; app_secret?: string }) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/feishu/auth`,
        { method: 'POST', body: JSON.stringify(body), headers: { 'Content-Type': 'application/json' } },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['channel-progress', appId.value, 'feishu'] })
    },
  })
}
```

- [ ] **Step 3: 飞书面板 UI（模式选择 + 二维码/表单）**

`AppChannelsTab.vue` 渠道详情区按 `selectedChannel.type === 'feishu'` 渲染飞书专用面板：
- 模式单选：`扫码自动创建`（默认）/ `手动填写`（`t('apps.channels.feishuModeScan')` / `feishuModeManual`）。
- domain 下拉：`飞书国内`(feishu) / `Lark 国际`(lark)。
- 扫码模式：点「发起」→ `useBeginFeishuAuth({mode:'scan',domain})` → 轮询 `useChannelProgressQuery(appId, ref('feishu'))` → 用 `AuthChallengeRenderer`（progress.metadata.qrcode → challenge）展示二维码；状态走 `formatChannelStatus`。
- 手填模式：app_id + app_secret(password) 输入 → 「提交」→ `useBeginFeishuAuth({mode:'manual',domain,app_id,app_secret})` → 轮询状态。
- 折叠图文指引：扫码授权步骤 / 开放平台建应用步骤（文案走 i18n）。
- 已绑定详情：`bound_identity`(bot_open_id)、`channel_name`(bot_name)、domain 展示；解绑按钮调 `useUnbindChannel(appId, ref('feishu'))`。

> 复用既有 `AuthChallengeRenderer.vue`（已支持 `challenge_type==='qrcode'` 渲染 URL→PNG）。飞书二维码 URL 来自 progress.metadata.qrcode（worker 写入），与微信一致：构造 `ChannelChallenge{challenge_type:'qrcode', qrcode: progress.metadata.qrcode, expires_at: progress.metadata.expires_at}` 传给 renderer。

- [ ] **Step 4: 构建校验**

Run: `cd web && npm run build`
Expected: 构建通过，无 TS 报错。

- [ ] **Step 5: 提交**
```bash
git add web/src/pages/apps/AppChannelsTab.vue web/src/api/hooks/useChannel.ts
git commit -m "feat(channel): 前端启用飞书渠道并加扫码/手填双模式 UI"
```

---

### Task 20: i18n 飞书文案补全

**Files:**
- Modify: `web/src/i18n/locales/zh/apps/root.ts`
- Modify: `web/src/i18n/locales/en/apps/root.ts`

- [ ] **Step 1: 补中英文案**

`channels` 对象内新增（zh）：
```typescript
  feishuModeScan: '扫码自动创建',
  feishuModeManual: '手动填写凭证',
  feishuDomainFeishu: '飞书（国内）',
  feishuDomainLark: 'Lark（国际）',
  feishuAppId: 'App ID',
  feishuAppSecret: 'App Secret',
  feishuScanGuide: '用飞书/Lark App 扫码并确认创建机器人',
  feishuManualGuide: '前往开放平台创建应用、开启机器人能力，复制 App ID 与 App Secret',
  feishuSubmit: '提交',
```
en 对应：
```typescript
  feishuModeScan: 'Scan to create automatically',
  feishuModeManual: 'Enter credentials manually',
  feishuDomainFeishu: 'Feishu (China)',
  feishuDomainLark: 'Lark (International)',
  feishuAppId: 'App ID',
  feishuAppSecret: 'App Secret',
  feishuScanGuide: 'Scan with the Feishu / Lark app and confirm bot creation',
  feishuManualGuide: 'Create an app on the open platform, enable bot capability, copy App ID and App Secret',
  feishuSubmit: 'Submit',
```
> `channelFeishu`/`channelFeishuDesc`/`status.*` 已存在，无需重复。`status.pending_auth` 现为「等待扫码授权」，飞书手填阶段语义也兼容（验证中），保留。

- [ ] **Step 2: 构建校验（i18n 一致性守卫）**

Run: `cd web && npm run build`
Expected: 通过（项目 i18n 校验中英 key 对齐）。

- [ ] **Step 3: 提交**
```bash
git add web/src/i18n/locales/zh/apps/root.ts web/src/i18n/locales/en/apps/root.ts
git commit -m "feat(channel): 补全飞书渠道中英文案"
```

---

## Phase 9 · OpenAPI 同步与端到端验证

### Task 21: OpenAPI 与前端类型同步

**Files:**
- Modify: `openapi/openapi.yaml`（生成产物）
- Modify: `web/src/api/generated.ts`（生成产物）

- [ ] **Step 1: 重新生成**

Run:
```bash
make openapi-gen
make web-types-gen
```
Expected: 生成 `FeishuChannelAuthRequest`、飞书发起响应等；`openapi/openapi.yaml` 与 `web/src/api/generated.ts` 更新。

- [ ] **Step 2: 校验工作区干净（生成同步）**

Run: `make openapi-check`
Expected: 跑完 `make openapi-gen` 后 git 工作区干净（yaml 已随代码更新）。

- [ ] **Step 3: 提交**
```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(channel): 同步飞书渠道 OpenAPI 与前端类型"
```

---

### Task 22: 本地 k3d 真机集成 + 三角色浏览器端到端验证

**Files:** 无（验证任务，按 CLAUDE.md 硬性要求用真实浏览器）

- [ ] **Step 1: 重建并加载 hermes 两 variant 镜像到本地 k3d**

Run（参考 Makefile hermes 构建/导入 target 与 docs/local-development.md）:
```bash
# 构建含飞书依赖 + oc-ops 飞书路由的镜像，导入 k3d registry，滚动重启实例
make local-hermes-build   # 或项目实际 target
```
验证镜像内：`python -c "import lark_oapi, websockets"` 不报错；`/usr/local/lib/ocops/server.py` 含 `/oc/channels/feishu/register`。

- [ ] **Step 2: 跑全量后端测试**

Run: `go test ./internal/...`
Expected: 全 PASS。

- [ ] **Step 3: 扫码自动创建端到端（需真实飞书账号 + 管理权限）**

浏览器登录本地 manager（http://ocm.localhost），进某实例「渠道绑定」→ 飞书 → 扫码模式 → 发起 → 用飞书 App 扫码确认 → 观察：二维码展示 → 扫码后状态「验证中」→ pod 重启 → 「已连接（在线）」→ 详情显示 bot 名。给飞书机器人发消息验证助手回复。

- [ ] **Step 4: 手填凭证端到端**

另一实例 → 飞书 → 手填模式 → 填真实 app_id/app_secret + 选 domain → 提交 → 验证连通在线。故意填错 secret → 验证状态「失败」并展示原因。

- [ ] **Step 5: 解绑 + 渠道并存 + 三角色**

解绑飞书 → 验证 Secret key 删除、pod 重启后飞书离线。同一实例同时绑定微信 + 飞书，验证并存互不干扰。分别用 platform_admin / org_admin / org_member 三角色重复关键路径，验证权限（org_member 无管理权应被拒）。

- [ ] **Step 6: 记录验证矩阵**

按项目规范在交付说明里给出逐项验证矩阵（模式 × 角色 × 结果 + 证据截图）。

- [ ] **Step 7（如有问题）:** 先修复并重新验证，直到全部通过再交付。

---

## 自检清单（实现完成后逐项确认）

- [ ] 迁移 up/down 可逆，CHECK 与唯一约束如期（Task 1）。
- [ ] 未绑定飞书时 Deployment 不注入飞书 env、Secret 无飞书 key（Task 3）。
- [ ] app 重建/升级后已绑定飞书配置不丢（Task 4 RenderSecret 带出）。
- [ ] secret 全链路不泄露：DB 只存密文、PollAuth 过滤 `*_ciphertext`/secret、AuthProgress 不含 secret（Task 11/14）。
- [ ] 扫码与手填两条路都能 bound；fatal 带原因（Task 17）。
- [ ] 两个 hermes variant 的 oc-ops/Dockerfile 都改且 5.16 引擎契约已复核（Task 6-9）。
- [ ] OpenAPI/前端类型同步、`make openapi-check` 干净（Task 21）。
- [ ] 三角色浏览器端到端通过，含并存与错误路径（Task 22）。
