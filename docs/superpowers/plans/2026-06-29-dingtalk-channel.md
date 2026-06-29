# 钉钉（DingTalk）渠道 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 app 实例新增「钉钉」渠道：用户手填 Client ID / Client Secret（AppKey/AppSecret）→ manager 注入 hermes 容器 env → 重启生效 → health 探测连通，与微信/飞书/企业微信并存。

**Architecture:** 钉钉是已上线的企业微信（work_wechat）渠道的近乎 1:1 克隆。引擎 `gateway/platforms/dingtalk.py` 在 v2026.6.5 / v2026.5.16 已自带（无需升级引擎），只需引擎侧两处轻改（Dockerfile 预装 `dingtalk-stream` SDK + ocops 加 `DingtalkChannelOps`）；manager 侧照搬企业微信链路（enums / migration / service `BeginDingtalkAuth` / `DingtalkAdapter` / render env 注入 / 解绑 / 前端手填表单 / i18n / OpenAPI）。唯一行为差异：钉钉引擎不上报 `fatal`，凭证错统一归「连接超时」。

**Tech Stack:** Go 1.22（gin / sqlc / testify）、MySQL migration、k8s（client-go）、Python（hermes ocops，starlette/pytest）、Vue 3 + TypeScript（naive-ui / vue-query / vitest）、swag OpenAPI。

**参考设计：** `docs/superpowers/specs/2026-06-29-dingtalk-channel-design.md`（commit 8a0b856）。

---

## 命名约定（全栈统一，照抄即可）

| 维度 | 值 |
|---|---|
| domain 枚举 | `ChannelTypeDingTalk = "dingtalk"` |
| 引擎平台名（read path） | `platforms.dingtalk`（引擎内部即 `dingtalk`） |
| 引擎 env | `DINGTALK_CLIENT_ID`、`DINGTALK_CLIENT_SECRET` |
| k8s Secret key | `dingtalk-client-id`、`dingtalk-client-secret` |
| DB metadata key | `client_id`（明文）、`client_secret_ciphertext`（密文） |
| DTO / service input | `DingtalkChannelAuthRequest{ClientID, ClientSecret}` / `DingtalkAuthInput{ClientID, ClientSecret}`（json `client_id` / `client_secret`） |
| service 方法 | `BeginDingtalkAuth` |
| adapter | `DingtalkAdapter` / `NewDingtalkAdapter` |
| AppSpec 字段 | `DingtalkClientID`、`DingtalkClientSecret` |
| render 函数 | `dingtalkOptionalEnv` |
| 引擎 ocops 类 | `DingtalkChannelOps`、`_dingtalk_status()`（channel=`dingtalk`） |
| 前端 hook / body | `useBeginDingtalkAuth` / `DingtalkAuthBody{client_id, client_secret}` |
| 前端 i18n key 前缀 | `channelDingtalk*`（渠道名已存在）+ 新增 `dingtalk*` 表单 key |

**关键复用（无需新建）：** sqlc query 复用通用 `SetChannelBindingChallenge` + `UpsertChannelBindingUnbound`（企业微信即如此，无专属 `SetXxxCredentials`）；worker 走通用 check 路径（`adapter.PollAuth`），无特判分支；前端 `ChannelLogo.vue` 已有钉钉 logo 与 brandColor（零改动）；i18n `channelDingtalk`/`channelDingtalkDesc` 已存在。

**已知既有破损（非本任务引入）：** `web/src/pages/apps/AppChannelsTab.spec.ts` 当前在 master 上即失败——组件 setup 调 `useBeginWorkWechatAuth()`→`useQueryClient()`，但测试 mount 未装 `VueQueryPlugin`，三个用例全在 mount 阶段抛 `No 'queryClient' found`。Task 15 在触碰该文件时一并修复 harness。

---

## 文件结构

**引擎侧（两 variant 各一份，内容除路径外完全一致）：**
- 改 `runtime/hermes/hermes-v2026.6.5/Dockerfile`、`runtime/hermes/hermes-v2026.5.16/Dockerfile`：预装 `dingtalk-stream>=0.20 httpx`。
- 改 `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`、`runtime/hermes/hermes-v2026.5.16/ocops/channel.py`：加 `_dingtalk_status()` + `DingtalkChannelOps` + `register_channel`。

**后端 Go：**
- 改 `internal/domain/enums.go`：加枚举常量。
- 新建 `internal/migrations/000020_support_dingtalk_channel.{up,down}.sql`。
- 改 `internal/integrations/k8sorch/orchestrator.go`：AppSpec 加两字段。
- 改 `internal/integrations/k8sorch/render.go`：`dingtalkOptionalEnv` + RenderSecret + RenderDeployment 挂载。
- 改 `internal/worker/handlers/app_initialize.go`：buildAppSpec 解密带出。
- 新建 `internal/integrations/channel/dingtalk.go`：`DingtalkAdapter`。
- 改 `internal/service/channel_service.go`：`DingtalkAuthInput` + `BeginDingtalkAuth` + `unbindSecretKeys` 加分支 + `ChannelStore` 无需改。
- 改 `internal/api/handlers/dto.go`、`internal/api/handlers/channels.go`：DTO + 分流 + 接口。
- 改 `internal/worker/handlers/channel_login.go`：`channelLabelWorker` 加分支。
- 改 `cmd/server/main.go`：注册 adapter。
- 生成物：`openapi/openapi.yaml`、`web/src/api/generated.ts`（跑 make 生成）。

**前端：**
- 改 `web/src/api/hooks/useChannel.ts`：`DingtalkAuthBody` + `useBeginDingtalkAuth`。
- 改 `web/src/pages/apps/AppChannelsTab.vue`：`supported:true` + 表单模板 + script。
- 改 `web/src/i18n/locales/zh/apps/root.ts`、`web/src/i18n/locales/en/apps/root.ts`：表单文案。
- 改 `web/src/pages/apps/AppChannelsTab.spec.ts`：修 harness + 更新断言。

**测试：** `internal/integrations/channel/dingtalk_test.go`（新）、`internal/service/channel_service_test.go`（补）、`internal/api/handlers/channels_test.go`（补）、`internal/integrations/k8sorch/render_test.go`（补）。

---

## Task 1：引擎 Dockerfile 预装 dingtalk-stream（两 variant）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/Dockerfile`（飞书预装块在 74-78 行后插入）
- Modify: `runtime/hermes/hermes-v2026.5.16/Dockerfile`（飞书预装块在 65-69 行后插入）

- [ ] **Step 1：在 6.5 Dockerfile 飞书预装块之后插入钉钉预装**

在 `runtime/hermes/hermes-v2026.6.5/Dockerfile` 中，找到飞书预装块（结尾是 `      lark-oapi==1.5.3 websockets`），在其后、discord 预装块之前插入：

```dockerfile

# 显式预装 dingtalk platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
# 引擎 gateway/platforms/dingtalk.py 走官方 dingtalk-stream WebSocket 长连接（DINGTALK_CLIENT_ID/
# DINGTALK_CLIENT_SECRET 启用），依赖 dingtalk-stream(>=0.20)+httpx；未预装会在首次启用时
# lazy_deps.ensure("platform.dingtalk") 同步阻塞拉取，线上 pod 出网受限时装不上、起不来
# （与 weixin/feishu/discord 预装同因）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      "dingtalk-stream>=0.20" httpx
```

- [ ] **Step 2：在 5.16 Dockerfile 飞书预装块之后插入同样内容**

在 `runtime/hermes/hermes-v2026.5.16/Dockerfile` 飞书预装块（结尾 `      lark-oapi==1.5.3 websockets`）之后插入与 Step 1 完全相同的钉钉预装块。

- [ ] **Step 3：校验两个 Dockerfile 语法**

Run: `grep -n "dingtalk-stream" runtime/hermes/hermes-v2026.6.5/Dockerfile runtime/hermes/hermes-v2026.5.16/Dockerfile`
Expected: 各输出一行含 `"dingtalk-stream>=0.20" httpx`。

> 注：镜像实际构建走 `make` 流水线、耗时长且需联网，不在本 task 内构建；构建验证留到 Task 16 / 用户侧。

- [ ] **Step 4：Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/Dockerfile runtime/hermes/hermes-v2026.5.16/Dockerfile
git commit -m "feat(runtime): 两 variant 预装 dingtalk-stream SDK

钉钉渠道引擎适配器 gateway/platforms/dingtalk.py 走 dingtalk-stream WebSocket
长连接，预装 dingtalk-stream>=0.20+httpx 避免线上 pod 出网受限时运行时 lazy
install 阻塞/失败（与 weixin/feishu/discord 预装同因）。"
```

---

## Task 2：引擎 ocops 加 DingtalkChannelOps（两 variant）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`（`_wecom_status` 在 88-117；`WorkWechatChannelOps` 在 410-425；注册在 429-431）
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/channel.py`（同结构，行号可能略偏，按符号定位）
- Test: 两 variant 各自的 `tests/`（构建期 pytest 自检；本地用下方命令跑 channel 测试）

- [ ] **Step 1：在 6.5 channel.py 的 `_wecom_status` 函数之后新增 `_dingtalk_status`**

在 `_wecom_status()` 函数（以 `return {"channel": "work_wechat", "bound": False, "platform_state": state or "connecting"}` 结尾）之后、`class _QRLineWriter` 之前插入：

```python


def _dingtalk_status() -> dict:
    """读 hermes api_server /health/detailed 的 platforms.dingtalk 运行态，映射为渠道绑定态。

    与 _wecom_status 同形：引擎平台名即 dingtalk，字段为 state（connected/disconnected/…）。
    注意：钉钉适配器只 _mark_connected/_mark_disconnected、不写 fatal，故 fatal 分支实际不触发，
    保留只为与其它渠道同构（凭证错表现为长期非 connected → manager 侧按超时判失败）。
      - state == "connected" → bound=True
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
    dt = (data.get("platforms") or {}).get("dingtalk") or {}
    state = dt.get("state", "")
    if state == "connected":
        return {"channel": "dingtalk", "bound": True, "platform_state": state}
    if state == "fatal":
        return {"channel": "dingtalk", "bound": False, "platform_state": state,
                "error_code": dt.get("error_code", "") or "",
                "error_message": dt.get("error_message", "") or ""}
    return {"channel": "dingtalk", "bound": False, "platform_state": state or "connecting"}
```

- [ ] **Step 2：在 6.5 channel.py 的 `WorkWechatChannelOps` 类之后新增 `DingtalkChannelOps`**

在 `WorkWechatChannelOps`（以 `return {"channel": "work_wechat", "status": "unbound"}` 结尾）之后、`register_channel(WeixinChannelOps())` 注册块之前插入：

```python


# ============================================================================
# dingtalk：env 注入 + health 运行态（无扫码授权流）
# ============================================================================

class DingtalkChannelOps(ChannelOps):
    """钉钉渠道：机器人凭证经环境变量注入引擎（manager 直注 DINGTALK_CLIENT_ID/SECRET），运行态走 health。

    无扫码授权流（auth_stream 用基类默认 failed 终态即可）；channel 标识 dingtalk 与
    manager 侧枚举一致，内部读引擎 platforms.dingtalk。"""

    channel = "dingtalk"

    def status(self, data_root: Path) -> dict:
        # 钉钉无本地账号文件（凭证经 DINGTALK_* env 注入），绑定态以引擎运行态为准。
        return _dingtalk_status()

    def unbind(self, data_root: Path) -> dict:
        # env 注入型渠道：oc-ops 侧无本地文件态可删（真正清理由 manager 删 dingtalk-* key + RolloutRestart），
        # 此处返回幂等成功即可。
        return {"channel": "dingtalk", "status": "unbound"}
```

- [ ] **Step 3：在 6.5 channel.py 注册块追加钉钉注册**

把：
```python
register_channel(WeixinChannelOps())
register_channel(FeishuChannelOps())
register_channel(WorkWechatChannelOps())
```
改为追加一行：
```python
register_channel(WeixinChannelOps())
register_channel(FeishuChannelOps())
register_channel(WorkWechatChannelOps())
register_channel(DingtalkChannelOps())
```

- [ ] **Step 4：对 5.16 channel.py 重复 Step 1-3（内容完全相同）**

按 `_wecom_status` / `WorkWechatChannelOps` / `register_channel(WorkWechatChannelOps())` 符号定位插入点，插入与 6.5 相同的三段。

- [ ] **Step 5：跑两 variant 的 ocops channel 单测验证未破坏**

Run:
```bash
cd /home/hujing/dir/software/ywjs/oc-manager/runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=. python -m pytest tests/ -k channel -q
cd /home/hujing/dir/software/ywjs/oc-manager/runtime/hermes/hermes-v2026.5.16 && PYTHONPATH=. python -m pytest tests/ -k channel -q
```
Expected: PASS（或 collected 0 时回退 `python -m pytest tests/ -q` 跑全量，确认绿；若本机缺 starlette 等依赖致 ImportError，记录后留待 Task 16 镜像构建期自检兜底）。

- [ ] **Step 6：语法自检**

Run: `python3 -c "import ast; [ast.parse(open(p).read()) for p in ['runtime/hermes/hermes-v2026.6.5/ocops/channel.py','runtime/hermes/hermes-v2026.5.16/ocops/channel.py']]; print('ok')"`
Expected: `ok`

- [ ] **Step 7：Commit**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/channel.py runtime/hermes/hermes-v2026.5.16/ocops/channel.py
git commit -m "feat(runtime): ocops 注册 DingtalkChannelOps 转发 platforms.dingtalk 连通态

两 variant 加 _dingtalk_status() 读 /health/detailed 的 platforms.dingtalk + 
DingtalkChannelOps(只覆写 status/unbind，无扫码 auth_stream) + register_channel。
钉钉引擎不写 fatal，fatal 分支保留只为同构。"
```

---

## Task 3：domain 枚举加 ChannelTypeDingTalk

**Files:**
- Modify: `internal/domain/enums.go`（`ChannelTypeWorkWeChat` 在第 58 行附近）

- [ ] **Step 1：加枚举常量**

在 `ChannelTypeWorkWeChat = "work_wechat"` 那一行之后新增：
```go
	// ChannelTypeDingTalk 是钉钉渠道（手填 Client ID/Client Secret，dingtalk-stream 长连接）。
	ChannelTypeDingTalk = "dingtalk"
```

- [ ] **Step 2：编译验证**

Run: `go build ./internal/domain/`
Expected: 无输出（成功）。

- [ ] **Step 3：Commit**

```bash
git add internal/domain/enums.go
git commit -m "feat(domain): 加 ChannelTypeDingTalk 渠道枚举"
```

---

## Task 4：migration 000020 放宽 channel_type CHECK

**Files:**
- Create: `internal/migrations/000020_support_dingtalk_channel.up.sql`
- Create: `internal/migrations/000020_support_dingtalk_channel.down.sql`

- [ ] **Step 1：写 up migration**

`internal/migrations/000020_support_dingtalk_channel.up.sql`：
```sql
-- 放宽 channel_bindings.channel_type CHECK 约束，新增 'dingtalk'。
-- 唯一约束 uk_channel_bindings_app_active (app_active_key, channel_type) 由飞书 000015 已建，
-- 含 channel_type，钉钉直接受益（同一 app 可同时绑定 wechat/feishu/work_wechat/dingtalk 各一条非 deleted）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat', 'dingtalk'));
```

- [ ] **Step 2：写 down migration**

`internal/migrations/000020_support_dingtalk_channel.down.sql`：
```sql
-- 回滚：移除 'dingtalk'，还原为飞书+企业微信三值约束。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat'));
```

- [ ] **Step 3：确认 migration 编号唯一且文件可被嵌入**

Run: `ls internal/migrations/ | grep 000020`
Expected: 两个文件 `000020_support_dingtalk_channel.up.sql` / `.down.sql`。

Run: `go build ./internal/migrations/... 2>&1 | head` 或 `go build ./...`（若 migration 经 embed.FS 加载，构建即校验存在）
Expected: 无错误。

- [ ] **Step 4：Commit**

```bash
git add internal/migrations/000020_support_dingtalk_channel.up.sql internal/migrations/000020_support_dingtalk_channel.down.sql
git commit -m "feat(migration): 000020 放宽 channel_type CHECK 支持 dingtalk

唯一约束已由飞书 000015 含 channel_type，钉钉仅需放宽 CHECK 即支持四渠道并存。"
```

---

## Task 5：k8s render 注入 DINGTALK_* env

**Files:**
- Modify: `internal/integrations/k8sorch/orchestrator.go`（AppSpec 在 35-65，`WorkWeChatSecret` 字段在 63-64）
- Modify: `internal/integrations/k8sorch/render.go`（RenderSecret 在 28-47；RenderDeployment Env 在 128-132；`workWechatOptionalEnv` 在 235-251）
- Test: `internal/integrations/k8sorch/render_test.go`（`TestWorkWechatOptionalEnv` 在 220 附近）

- [ ] **Step 1：写失败测试 `TestDingtalkOptionalEnv`**

在 `internal/integrations/k8sorch/render_test.go` 的 `TestWorkWechatOptionalEnv` 之后新增（仿其结构）：
```go
// TestDingtalkOptionalEnv 验证钉钉两条 optional SecretKeyRef env 名/key/optional 标记正确。
// 覆盖：未绑定时 Secret 无对应 key 也不报错（optional=true），引擎 getenv 为空 → 钉钉平台不启用。
func TestDingtalkOptionalEnv(t *testing.T) {
	envs := dingtalkOptionalEnv("a1")
	require.Len(t, envs, 2)                               // 钉钉注入两条 env
	assert.Equal(t, "DINGTALK_CLIENT_ID", envs[0].Name)  // 第一条对应 AppKey
	assert.Equal(t, "dingtalk-client-id", envs[0].ValueFrom.SecretKeyRef.Key)
	assert.True(t, *envs[0].ValueFrom.SecretKeyRef.Optional)
	assert.Equal(t, "DINGTALK_CLIENT_SECRET", envs[1].Name) // 第二条对应 AppSecret
	assert.Equal(t, "dingtalk-client-secret", envs[1].ValueFrom.SecretKeyRef.Key)
	assert.True(t, *envs[1].ValueFrom.SecretKeyRef.Optional)
}
```

- [ ] **Step 2：跑测试验证失败**

Run: `go test ./internal/integrations/k8sorch/ -run TestDingtalkOptionalEnv -v`
Expected: FAIL（`undefined: dingtalkOptionalEnv`）。

- [ ] **Step 3：AppSpec 加两字段**

在 `internal/integrations/k8sorch/orchestrator.go` 的 `WorkWeChatSecret string` 字段（含其注释）之后、AppSpec 结构体 `}` 之前插入：
```go
	// DingtalkClientID 是钉钉应用 Client ID（即 AppKey，明文，未绑定为空）。
	DingtalkClientID string
	// DingtalkClientSecret 是钉钉 Client Secret 明文（即 AppSecret，buildAppSpec 从 DB 密文解密后填入，引擎需明文；未绑定为空）。
	DingtalkClientSecret string
```

- [ ] **Step 4：实现 `dingtalkOptionalEnv` + RenderSecret 带出 + RenderDeployment 挂载**

在 `internal/integrations/k8sorch/render.go` 的 `workWechatOptionalEnv` 函数之后新增：
```go

// dingtalkOptionalEnv 返回钉钉两条 optional SecretKeyRef env（DINGTALK_CLIENT_ID / DINGTALK_CLIENT_SECRET），
// 供 hermes 容器永久挂载。Optional=true：未绑定时 Secret 无对应 key，k8s 不注入该 env
// （引擎 getenv 为空 → 钉钉平台不启用），Deployment 模板无需随绑定状态变化。
func dingtalkOptionalEnv(appID string) []corev1.EnvVar {
	optionalTrue := true
	ref := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(appID)},
			Key:                  key,
			Optional:             &optionalTrue,
		}}
	}
	return []corev1.EnvVar{
		{Name: "DINGTALK_CLIENT_ID", ValueFrom: ref("dingtalk-client-id")},
		{Name: "DINGTALK_CLIENT_SECRET", ValueFrom: ref("dingtalk-client-secret")},
	}
}
```

在 `RenderSecret`（render.go）的企业微信带出块之后（`data["wecom-secret"] = spec.WorkWeChatSecret` 的 `}` 之后）插入：
```go
	// 已绑定钉钉：带出 client_id + client_secret 明文，保证重建/升级不丢配置（DB 是 source of truth）。
	if spec.DingtalkClientID != "" && spec.DingtalkClientSecret != "" {
		data["dingtalk-client-id"] = spec.DingtalkClientID
		data["dingtalk-client-secret"] = spec.DingtalkClientSecret
	}
```

在 `RenderDeployment` 的容器 `Env:` 链式 append 中，把：
```go
								}, feishuOptionalEnv(spec.AppID)...), workWechatOptionalEnv(spec.AppID)...), proxyEnv...),
```
改为再套一层 dingtalk：
```go
								}, feishuOptionalEnv(spec.AppID)...), workWechatOptionalEnv(spec.AppID)...), dingtalkOptionalEnv(spec.AppID)...), proxyEnv...),
```
并把同语句开头的 `append(append(append([]corev1.EnvVar{` 增加一层 `append(`，即变为 `append(append(append(append([]corev1.EnvVar{`（四个值组：base / feishu / wecom / dingtalk，再加 proxy 共五个 append，注意 append 嵌套层数 = 值组数）。

> 注意：当前是 `append(append(append(BASE, feishu...), wecom...), proxy...)`（3 层 append 串 4 个切片）。新增 dingtalk 后是 4 层 append 串 5 个切片：`append(append(append(append(BASE, feishu...), wecom...), dingtalk...), proxy...)`。务必让左侧 `append(` 数量 = 右侧 `...)` 追加组数。

- [ ] **Step 5：扩展 RenderSecret 测试覆盖钉钉带出（若 render_test 有 RenderSecret 用例则补一条断言；无则新增最小用例）**

在 `render_test.go` 新增：
```go
// TestRenderSecret_Dingtalk 验证已绑定钉钉时 client_id/client_secret 明文写入 Secret StringData。
func TestRenderSecret_Dingtalk(t *testing.T) {
	sec := RenderSecret(AppSpec{
		AppID:                "a1",
		ControlToken:         "tok",
		DingtalkClientID:     "ding-key",
		DingtalkClientSecret: "ding-secret",
	}, "ns")
	assert.Equal(t, "ding-key", sec.StringData["dingtalk-client-id"])       // Client ID 明文带出
	assert.Equal(t, "ding-secret", sec.StringData["dingtalk-client-secret"]) // Client Secret 明文带出
}
```

- [ ] **Step 6：跑测试验证通过 + 全包编译**

Run: `go test ./internal/integrations/k8sorch/ -run 'TestDingtalkOptionalEnv|TestRenderSecret_Dingtalk' -v && go build ./...`
Expected: PASS + 编译成功。

- [ ] **Step 7：Commit**

```bash
git add internal/integrations/k8sorch/orchestrator.go internal/integrations/k8sorch/render.go internal/integrations/k8sorch/render_test.go
git commit -m "feat(k8sorch): 渲染钉钉 DINGTALK_CLIENT_ID/SECRET optional env + Secret 带出

AppSpec 加 DingtalkClientID/DingtalkClientSecret；dingtalkOptionalEnv 仿
workWechatOptionalEnv 注入两条 optional SecretKeyRef；RenderSecret 从 DB 带出
明文保证重建/升级不丢配置。"
```

---

## Task 6：buildAppSpec 解密带出钉钉绑定

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`（企业微信解密块在 436-451；AppSpec 返回在 453-476）

- [ ] **Step 1：在企业微信解密块之后新增钉钉解密块**

在 `app_initialize.go` 的企业微信解密块（`wecomBotID, wecomSecret string` 那段，以其 `}` 结尾）之后插入：
```go

	// 已绑定钉钉：解密带出 client_id+client_secret，使 RenderSecret 在重建/升级时不丢配置。
	// 查询失败 / 无行 / 非 bound / 解密失败均静默降级为空——未绑定的 app 不应因此报错。
	var dingtalkClientID, dingtalkClientSecret string
	if binding, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID: app.ID, ChannelType: domain.ChannelTypeDingTalk,
	}); err == nil && binding.Status == domain.ChannelStatusBound && len(binding.MetadataJson) > 0 {
		var m struct {
			ClientID         string `json:"client_id"`
			SecretCiphertext string `json:"client_secret_ciphertext"`
		}
		if json.Unmarshal(binding.MetadataJson, &m) == nil && m.SecretCiphertext != "" && h.cfg.Cipher != nil {
			if plain, derr := h.cfg.Cipher.Decrypt(m.SecretCiphertext); derr == nil {
				dingtalkClientID, dingtalkClientSecret = m.ClientID, string(plain)
			}
		}
	}
```

- [ ] **Step 2：在返回的 AppSpec 字面量中填两字段**

把返回的 `k8sorch.AppSpec{...}` 中：
```go
		WorkWeChatBotID:  wecomBotID,
		WorkWeChatSecret: wecomSecret,
	}
```
改为：
```go
		WorkWeChatBotID:      wecomBotID,
		WorkWeChatSecret:     wecomSecret,
		DingtalkClientID:     dingtalkClientID,
		DingtalkClientSecret: dingtalkClientSecret,
	}
```

- [ ] **Step 3：编译验证**

Run: `go build ./internal/worker/...`
Expected: 无错误。

- [ ] **Step 4：Commit**

```bash
git add internal/worker/handlers/app_initialize.go
git commit -m "feat(worker): buildAppSpec 解密带出钉钉 client_id/client_secret

仿企业微信：已绑定 dingtalk 时从 metadata 解密 client_secret_ciphertext，
填入 AppSpec 供 RenderSecret 在 pod 重建/镜像升级时不丢配置。"
```

---

## Task 7：DingtalkAdapter（连通态查询）+ 注册

**Files:**
- Create: `internal/integrations/channel/dingtalk.go`
- Create: `internal/integrations/channel/dingtalk_test.go`
- Modify: `cmd/server/main.go`（企业微信注册在 284-289）
- 参考：`internal/integrations/channel/work_wechat.go`（全文）、`work_wechat_test.go`

- [ ] **Step 1：写 adapter 测试 `dingtalk_test.go`**

`internal/integrations/channel/dingtalk_test.go`（仿 `work_wechat_test.go`；如该文件存在请打开比对 mock 名称，下面用与 work_wechat 同构的内联 fake）：
```go
package channel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// fakeDingtalkStatusClient 按预置返回桩 ChannelStatus / 错误，驱动 PollAuth 状态映射断言。
type fakeDingtalkStatusClient struct {
	st  ocops.ChannelStatus
	err error
}

func (f fakeDingtalkStatusClient) ChannelStatus(_ context.Context, _ ocops.Endpoint, _ string) (ocops.ChannelStatus, error) {
	return f.st, f.err
}

// fakeDingtalkResolver 控制坐标解析结果（supported=false 模拟 dev stub）。
type fakeDingtalkResolver struct {
	supported bool
	err       error
}

func (f fakeDingtalkResolver) Resolve(_ context.Context, _ string) (ocops.Endpoint, bool, error) {
	return ocops.Endpoint{}, f.supported, f.err
}

// TestDingtalkAdapter_PollAuth 覆盖连通态映射：connected→Bound、fatal→Failed、其余→Pending，
// 以及坐标解析失败/oc-ops 错误一律吞错返回 Pending（解绑重启窗口不误判失败）。
func TestDingtalkAdapter_PollAuth(t *testing.T) {
	cases := []struct {
		name      string                 // 场景
		resolver  fakeDingtalkResolver    // 坐标解析桩
		client    fakeDingtalkStatusClient // 连通态桩
		wantState string                 // 期望 AuthStatus
	}{
		{"connected→Bound", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: "connected"}}, AuthStatusBound},               // 引擎已连上 → 绑定成功
		{"fatal→Failed", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: "fatal", ErrorMessage: "boom"}}, AuthStatusFailed}, // 引擎报致命（钉钉实际不触发，但映射保留）
		{"connecting→Pending", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: "connecting"}}, AuthStatusPending},           // 连接中 → 继续等
		{"empty→Pending", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: ""}}, AuthStatusPending},                          // 空态 → 继续等（钉钉凭证错的典型表现）
		{"resolve-unsupported→Pending", fakeDingtalkResolver{supported: false}, fakeDingtalkStatusClient{}, AuthStatusPending},                                                     // dev stub → 等下次
		{"ocops-error→Pending", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{err: errors.New("unreachable")}, AuthStatusPending},                               // oc-ops 不可达（重启窗口）→ 吞错等
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewDingtalkAdapter(c.client, c.resolver)
			pr, err := a.PollAuth(context.Background(), AuthInput{AppID: "a1"})
			require.NoError(t, err)
			assert.Equal(t, c.wantState, pr.Status)
		})
	}
}

// TestDingtalkAdapter_Type_Begin 校验 Type 标识与 BeginAuth 占位（钉钉无扫码发起，凭证经表单提交）。
func TestDingtalkAdapter_Type_Begin(t *testing.T) {
	a := NewDingtalkAdapter(fakeDingtalkStatusClient{}, fakeDingtalkResolver{})
	assert.Equal(t, domain.ChannelTypeDingTalk, a.Type()) // Type 返回 dingtalk
	_, err := a.BeginAuth(context.Background(), AuthInput{})
	require.Error(t, err)                                 // BeginAuth 占位必报错（不入 channel_start_login）
}
```

> 实现期校验：打开 `work_wechat_test.go` 确认 `channelStatusClient` / `OcOpsLocationResolver` 接口的 fake 写法是否已有可复用类型；若有同名 fake 导致重复声明，改用已有 fake 并删去本文件重复定义。

- [ ] **Step 2：跑测试验证失败**

Run: `go test ./internal/integrations/channel/ -run TestDingtalkAdapter -v`
Expected: FAIL（`undefined: NewDingtalkAdapter`）。

- [ ] **Step 3：实现 `dingtalk.go`（克隆 work_wechat.go）**

`internal/integrations/channel/dingtalk.go`：
```go
package channel

import (
	"context"
	"errors"
	"time"

	"oc-manager/internal/domain"
)

// DingtalkAdapter 实现 ChannelAdapter：钉钉无扫码、无 SSE，凭证经 manager 表单同步注入。
// 它只承载「连通状态检查」——PollAuth 经 oc-ops ChannelStatus(dingtalk) 读 platforms.dingtalk，
// 插进 worker 通用 check 路径（channel_login.go 非飞书分支），无需飞书式两阶段特判。
// BeginAuth 为占位：钉钉不入 channel_start_login，凭证经 POST /channels/dingtalk/auth 提交。
type DingtalkAdapter struct {
	// ops 查 oc-ops 渠道连通态（platform_state）。
	ops channelStatusClient
	// resolver 把 appID 解析为 oc-ops 调用坐标及 dev stub 标志。
	resolver OcOpsLocationResolver
}

// NewDingtalkAdapter 构造钉钉 adapter；ops 与 resolver 均不得为 nil。
func NewDingtalkAdapter(ops channelStatusClient, resolver OcOpsLocationResolver) *DingtalkAdapter {
	return &DingtalkAdapter{ops: ops, resolver: resolver}
}

// Type 返回 dingtalk（供 Registry 路由；与 oc-ops DingtalkChannelOps.channel 注册键一致）。
func (a *DingtalkAdapter) Type() string { return domain.ChannelTypeDingTalk }

// BeginAuth 占位：钉钉无扫码发起，凭证经表单提交，故不应被 worker 调用（不入 channel_start_login）。
func (a *DingtalkAdapter) BeginAuth(_ context.Context, _ AuthInput) (AuthChallenge, error) {
	return AuthChallenge{}, errors.New("钉钉不支持扫码发起，凭证经 POST /channels/dingtalk/auth 表单提交")
}

// PollAuth 查 oc-ops 钉钉连通态并映射为统一 AuthStatus。
//
// 关键容错：坐标解析失败 / oc-ops 不可达（解绑重启窗口）/ dev stub 一律返回 Pending，
// 吞瞬时错误让 worker 通用分支按退避 re-enqueue，不把 check job 判失败。
// 钉钉引擎只 connected/disconnected、不写 fatal：凭证错表现为长期非 connected，
// 由 worker 退避达上限后判超时失败（见设计第 5 节），此处仅 connected 给终态。
func (a *DingtalkAdapter) PollAuth(ctx context.Context, input AuthInput) (AuthProgress, error) {
	now := time.Now()
	ep, supported, err := a.resolver.Resolve(ctx, input.AppID)
	if err != nil || !supported {
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
	st, err := a.ops.ChannelStatus(ctx, ep, domain.ChannelTypeDingTalk)
	if err != nil {
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
	switch st.PlatformState {
	case "connected":
		return AuthProgress{Status: AuthStatusBound, UpdatedAt: now}, nil
	case "fatal":
		return AuthProgress{Status: AuthStatusFailed, ErrorMessage: st.ErrorMessage, UpdatedAt: now}, nil
	default:
		// connecting / disconnected / 空：连接中或凭证错，继续等（worker 退避达上限判超时）。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
}

// 确保实现 ChannelAdapter 接口（编译期校验）。
var _ ChannelAdapter = (*DingtalkAdapter)(nil)
```

- [ ] **Step 4：跑测试验证通过**

Run: `go test ./internal/integrations/channel/ -run TestDingtalkAdapter -v`
Expected: PASS。

- [ ] **Step 5：cmd/server 注册 adapter**

在 `cmd/server/main.go` 企业微信注册块（`channelRegistry.Register(workWechatAdapter)` 的 `}` 之后）插入：
```go

	// 钉钉渠道：无扫码、凭证经表单同步注入；adapter 只承载连通检查
	// （PollAuth 查 oc-ops ChannelStatus(dingtalk)/platforms.dingtalk，走 worker 通用 check 路径）。
	dingtalkAdapter := channel.NewDingtalkAdapter(ocopsClient, ocopsBindingLocationResolver{inner: ocopsResolver})
	if err := channelRegistry.Register(dingtalkAdapter); err != nil {
		return fmt.Errorf("注册钉钉渠道失败: %w", err)
	}
```

- [ ] **Step 6：编译验证**

Run: `go build ./cmd/server/ && go vet ./internal/integrations/channel/`
Expected: 无错误。

- [ ] **Step 7：Commit**

```bash
git add internal/integrations/channel/dingtalk.go internal/integrations/channel/dingtalk_test.go cmd/server/main.go
git commit -m "feat(channel): DingtalkAdapter 查 platforms.dingtalk 连通态 + 注册

克隆 WorkWeChatAdapter：PollAuth 经 oc-ops ChannelStatus(dingtalk) 映射
connected→Bound、其余→Pending，走 worker 通用 check 路径；BeginAuth 占位
（钉钉凭证经表单提交，不入 channel_start_login）。cmd/server 注册进 Registry。"
```

---

## Task 8：service BeginDingtalkAuth + 解绑

**Files:**
- Modify: `internal/service/channel_service.go`（`WorkWechatAuthInput` 在 312-318；`BeginWorkWechatAuth` 在 320-427；`unbindSecretKeys` 在 521-533）
- Test: `internal/service/channel_service_test.go`（`TestBeginWorkWechatAuth_*` 在 392 附近；`TestUnbind_WorkWeChat` 在 292 附近）

- [ ] **Step 1：写失败测试（克隆企业微信用例）**

打开 `internal/service/channel_service_test.go`，定位 `TestBeginWorkWechatAuth_Succeeds`，在其后新增钉钉版（沿用同一套 fake store / patcher / cipher helper，把 channel 改 dingtalk、key 改 client_id/client_secret）：
```go
// TestBeginDingtalkAuth_Succeeds 验证钉钉手填发起：加密落库 + 同步注入 dingtalk-* Secret +
// 置 runtime_phase=restarting + 入队 channel_check_binding，且 metadata 写 client_id+密文。
func TestBeginDingtalkAuth_Succeeds(t *testing.T) {
	// —— 复用 TestBeginWorkWechatAuth_Succeeds 的 fake 搭建（store/patcher/restarter/cipher/notifier），
	//     仅把渠道换成 dingtalk、凭证字段换成 ClientID/ClientSecret。下面按该测试的现有 helper 改写。
	store := newFakeChannelStore(t)                         // 复用现有 helper（若名不同，按文件内实际命名调整）
	store.app = readyAppFixture()                           // 业务态 running + runtime_phase ready
	patcher := &fakeSecretPatcher{}
	restarter := &fakeRestarter{}
	cipher := newTestCipher(t)
	svc := NewChannelService(store, testRegistryWithDingtalk(t), &fakeNotifier{})
	svc.SetFeishuUnbindDeps(patcher, restarter)
	svc.SetCipher(cipher)

	res, err := svc.BeginDingtalkAuth(context.Background(), adminPrincipal(), store.app.ID, DingtalkAuthInput{
		ClientID:     "ding-key",
		ClientSecret: "ding-secret",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ChannelStatusPendingAuth, res.Status) // 返回 pending_auth
	// 注入了 dingtalk-client-id / dingtalk-client-secret 两把明文 key。
	assert.Equal(t, "ding-key", patcher.lastSet["dingtalk-client-id"])
	assert.Equal(t, "ding-secret", patcher.lastSet["dingtalk-client-secret"])
	// metadata 落库：client_id 明文 + client_secret_ciphertext 密文（非明文）。
	var meta map[string]string
	require.NoError(t, json.Unmarshal(store.lastChallengeMetadata, &meta))
	assert.Equal(t, "ding-key", meta["client_id"])
	assert.NotEmpty(t, meta["client_secret_ciphertext"])
	assert.NotEqual(t, "ding-secret", meta["client_secret_ciphertext"]) // 已加密
	// 置 restarting + 重启 + 入队探测。
	assert.Equal(t, domain.RuntimePhaseRestarting, store.lastRuntimePhase)
	assert.True(t, restarter.called)
	assert.Equal(t, domain.JobTypeChannelCheckBinding, store.lastJobType)
}

// TestBeginDingtalkAuth_InstanceNotReady 验证实例未就绪（runtime_phase!=ready）时发起被拦截，不加密不写库。
func TestBeginDingtalkAuth_InstanceNotReady(t *testing.T) {
	store := newFakeChannelStore(t)
	store.app = restartingAppFixture()                    // runtime_phase=restarting → 闸门关闭
	svc := NewChannelService(store, testRegistryWithDingtalk(t), &fakeNotifier{})
	svc.SetCipher(newTestCipher(t))
	_, err := svc.BeginDingtalkAuth(context.Background(), adminPrincipal(), store.app.ID, DingtalkAuthInput{ClientID: "k", ClientSecret: "s"})
	require.ErrorIs(t, err, ErrInstanceNotReady)
}

// TestBeginDingtalkAuth_Forbidden 验证无管理权限主体发起被拒（org_member 对非自管 app）。
func TestBeginDingtalkAuth_Forbidden(t *testing.T) {
	store := newFakeChannelStore(t)
	store.app = readyAppFixture()
	svc := NewChannelService(store, testRegistryWithDingtalk(t), &fakeNotifier{})
	svc.SetCipher(newTestCipher(t))
	_, err := svc.BeginDingtalkAuth(context.Background(), memberPrincipalOfOther(), store.app.ID, DingtalkAuthInput{ClientID: "k", ClientSecret: "s"})
	require.Error(t, err) // 权限错误（具体哨兵按 loadManageableApp 现有返回断言）
}
```
并在 `TestUnbind_WorkWeChat` 之后新增：
```go
// TestUnbind_Dingtalk 验证钉钉解绑：置 unbound_by_user + 删 dingtalk-* 两把 key + 置 restarting + 重启。
func TestUnbind_Dingtalk(t *testing.T) {
	store := newFakeChannelStore(t)
	store.app = readyAppFixture()
	store.binding = boundBindingFixture(domain.ChannelTypeDingTalk) // 已绑定钉钉
	patcher := &fakeSecretPatcher{}
	restarter := &fakeRestarter{}
	svc := NewChannelService(store, testRegistryWithDingtalk(t), &fakeNotifier{})
	svc.SetFeishuUnbindDeps(patcher, restarter)
	require.NoError(t, svc.Unbind(context.Background(), adminPrincipal(), store.app.ID, domain.ChannelTypeDingTalk))
	assert.Equal(t, domain.ChannelStatusUnboundByUser, store.lastStatus)
	assert.ElementsMatch(t, []string{"dingtalk-client-id", "dingtalk-client-secret"}, patcher.lastDel) // 删两把 key
	assert.Equal(t, domain.RuntimePhaseRestarting, store.lastRuntimePhase)
	assert.True(t, restarter.called)
}
```

> 实现期对齐：`channel_service_test.go` 的实际 fake 命名/构造（`newFakeChannelStore`、`testRegistryWithDingtalk`、`readyAppFixture` 等）以现有 `TestBeginWorkWechatAuth_Succeeds` / `TestUnbind_WorkWeChat` 为准照搬——若现有用例用的是不同 helper（如直接内联 struct），按其风格改写本段，保持与企业微信用例同构。`testRegistryWithDingtalk` 需注册一个 `DingtalkAdapter`（用 Task 7 的 fake ops/resolver 构造）以通过 `registry.Lookup(dingtalk)` 守卫。

- [ ] **Step 2：跑测试验证失败**

Run: `go test ./internal/service/ -run 'TestBeginDingtalkAuth|TestUnbind_Dingtalk' -v`
Expected: FAIL（`undefined: BeginDingtalkAuth` / `DingtalkAuthInput`）。

- [ ] **Step 3：实现 `DingtalkAuthInput` + `BeginDingtalkAuth`**

在 `channel_service.go` 的 `BeginWorkWechatAuth` 方法之后插入（结构与之 1:1，把 work_wechat→dingtalk、bot_id/secret→client_id/client_secret，metadata key 改名）：
```go

// DingtalkAuthInput 是钉钉手填发起的 service 入参（与 handler 的 DingtalkChannelAuthRequest 对应）。
type DingtalkAuthInput struct {
	// ClientID 是钉钉应用 Client ID（即 AppKey，明文）。
	ClientID string
	// ClientSecret 是钉钉 Client Secret 明文（即 AppSecret，service 内加密后落 metadata，明文注入 k8s Secret）。
	ClientSecret string
}

// BeginDingtalkAuth 是钉钉手填发起入口（与企业微信 BeginWorkWechatAuth 同构，handler 按渠道分流）。
// 钉钉无扫码：凭证随请求体同步到达，故此处一次性完成「加密落库 + 同步注入 Secret + 重启 + 置 restarting + 入队连通探测」。
// 注入/重启复用飞书/企业微信同款 patcher/restarter（PatchSecretKeys/RestartApp 渠道无关）。
func (s *ChannelService) BeginDingtalkAuth(ctx context.Context, principal auth.Principal, appID string, in DingtalkAuthInput) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	if !domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase) {
		return ChallengeResult{}, ErrInstanceNotReady
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(domain.ChannelTypeDingTalk); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, domain.ChannelTypeDingTalk)
	}
	// cipher 必需：钉钉 client_secret 必须加密后落 metadata；未注入直接报错而非明文落库。
	if s.cipher == nil {
		return ChallengeResult{}, fmt.Errorf("钉钉发起缺少 cipher，无法加密 client_secret")
	}
	// bound 短路：已绑定的钉钉 app 再次发起直接返回 bound，不重跑 upsert / 写 metadata / 注入 / 入队。
	existing, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: domain.ChannelTypeDingTalk})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChallengeResult{}, fmt.Errorf("查询钉钉绑定失败: %w", err)
	}
	if err == nil && existing.Status == domain.ChannelStatusBound {
		return ChallengeResult{Status: domain.ChannelStatusBound, ChannelType: domain.ChannelTypeDingTalk}, nil
	}
	// create-on-demand：钉钉绑定行不在实例创建时预建，发起时按需建立（已存在 no-op）。
	if err := s.store.UpsertChannelBindingUnbound(ctx, sqlc.UpsertChannelBindingUnboundParams{
		ID:          newUUID(),
		AppID:       app.ID,
		ChannelType: domain.ChannelTypeDingTalk,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建钉钉绑定行失败: %w", err)
	}
	enc, err := s.cipher.Encrypt([]byte(in.ClientSecret))
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("加密钉钉 client_secret 失败: %w", err)
	}
	metaJSON, err := json.Marshal(map[string]any{
		"client_id":                in.ClientID,
		"client_secret_ciphertext": enc,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化钉钉 metadata 失败: %w", err)
	}
	if err := s.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
		MetadataJson: metaJSON,
		AppID:        app.ID,
		ChannelType:  domain.ChannelTypeDingTalk,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("写入钉钉凭证失败: %w", err)
	}
	// 同步注入 k8s Secret：明文 client_id/client_secret 写入 app Secret，引擎重启后装载钉钉平台。
	if s.feishuPatcher != nil {
		if err := s.feishuPatcher.PatchSecretKeys(ctx, app.ID, map[string]string{
			"dingtalk-client-id":     in.ClientID,
			"dingtalk-client-secret": in.ClientSecret,
		}, nil); err != nil {
			return ChallengeResult{}, fmt.Errorf("注入钉钉 Secret 失败: %w", err)
		}
	}
	// 双轴模型：置 runtime_phase=restarting 标记运行时不就绪（发起闸门据此关闭），业务态 status 不动。
	if err := s.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
		RuntimePhase: domain.RuntimePhaseRestarting,
		ID:           app.ID,
	}); err != nil {
		slog.ErrorContext(ctx, "钉钉发起置 runtime_phase=restarting 失败", "app_id", app.ID, redactlog.Err(err))
	}
	if s.feishuRestarter != nil {
		if err := s.feishuRestarter.RestartApp(ctx, app.ID); err != nil {
			slog.ErrorContext(ctx, "钉钉注入后重启失败", "app_id", app.ID, redactlog.Err(err))
		}
	}
	// 入队 channel_check_binding：worker 经 oc-ops 探测 platforms.dingtalk 连通态并写回绑定结果。
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"channel_type": domain.ChannelTypeDingTalk,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化钉钉探测任务失败: %w", err)
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
		return ChallengeResult{}, fmt.Errorf("创建钉钉探测任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{Status: domain.ChannelStatusPendingAuth, ChannelType: domain.ChannelTypeDingTalk, JobID: jobID}, nil
}
```

- [ ] **Step 4：`unbindSecretKeys` 加钉钉分支**

把 `unbindSecretKeys` 的 switch：
```go
	case domain.ChannelTypeWorkWeChat:
		return []string{"wecom-bot-id", "wecom-secret"}
```
之后插入：
```go
	case domain.ChannelTypeDingTalk:
		return []string{"dingtalk-client-id", "dingtalk-client-secret"}
```

- [ ] **Step 5：跑测试验证通过**

Run: `go test ./internal/service/ -run 'TestBeginDingtalkAuth|TestUnbind_Dingtalk' -v`
Expected: PASS。

- [ ] **Step 6：Commit**

```bash
git add internal/service/channel_service.go internal/service/channel_service_test.go
git commit -m "feat(service): BeginDingtalkAuth 手填同步注入 + 解绑删 dingtalk-* key

克隆 BeginWorkWechatAuth：加密 client_secret 落 metadata、同步 PatchSecretKeys
注入 dingtalk-client-id/secret、置 runtime_phase=restarting+RolloutRestart、
入队 channel_check_binding；unbindSecretKeys 加 dingtalk 分支。"
```

---

## Task 9：handler DTO + 分流 + swag

**Files:**
- Modify: `internal/api/handlers/dto.go`（`WorkWechatChannelAuthRequest` 在 156-162）
- Modify: `internal/api/handlers/channels.go`（接口在 21-29；分流在 87-104；swag @Description 在 47）
- Test: `internal/api/handlers/channels_test.go`（`TestBeginAuth_WorkWeChat*` 在 227 附近）

- [ ] **Step 1：写失败测试（克隆企业微信 handler 用例）**

在 `internal/api/handlers/channels_test.go` 的 `TestBeginAuth_WorkWeChat_BadBody` 之后新增：
```go
// TestBeginAuth_Dingtalk 验证 dingtalk 渠道分流到 BeginDingtalkAuth，正确解析 client_id/client_secret。
func TestBeginAuth_Dingtalk(t *testing.T) {
	svc := &fakeChannelService{challenge: service.ChallengeResult{Status: "pending_auth", ChannelType: "dingtalk"}}
	r := setupChannelRouter(svc)                          // 复用本文件现有路由搭建 helper
	body := `{"client_id":"ding-key","client_secret":"ding-secret"}`
	w := doPOST(r, "/api/v1/apps/app-1/channels/dingtalk/auth", body) // 复用现有 helper
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ding-key", svc.lastDingtalkInput.ClientID)       // 分流入参正确
	assert.Equal(t, "ding-secret", svc.lastDingtalkInput.ClientSecret)
}

// TestBeginAuth_Dingtalk_BadBody 验证缺必填字段返回 400（binding:"required" 校验）。
func TestBeginAuth_Dingtalk_BadBody(t *testing.T) {
	svc := &fakeChannelService{}
	r := setupChannelRouter(svc)
	w := doPOST(r, "/api/v1/apps/app-1/channels/dingtalk/auth", `{"client_id":"ding-key"}`) // 缺 client_secret
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
```
并在该文件的 `fakeChannelService`（mock）上新增 `BeginDingtalkAuth` 实现与 `lastDingtalkInput` 字段：
```go
// BeginDingtalkAuth 记录入参并返回预置挑战，供 TestBeginAuth_Dingtalk 断言分流。
func (f *fakeChannelService) BeginDingtalkAuth(_ context.Context, _ auth.Principal, _ string, in service.DingtalkAuthInput) (service.ChallengeResult, error) {
	f.lastDingtalkInput = in
	return f.challenge, f.err
}
```
（在 `fakeChannelService` struct 加字段 `lastDingtalkInput service.DingtalkAuthInput`；具体 mock 名/helper 名以该测试文件现有 `BeginWorkWechatAuth` mock 为准照搬。）

- [ ] **Step 2：跑测试验证失败**

Run: `go test ./internal/api/handlers/ -run TestBeginAuth_Dingtalk -v`
Expected: FAIL（编译错误：`channelService` 接口缺 `BeginDingtalkAuth` / DTO 缺失）。

- [ ] **Step 3：DTO 加 `DingtalkChannelAuthRequest`**

在 `internal/api/handlers/dto.go` 的 `WorkWechatChannelAuthRequest` 之后插入：
```go

// DingtalkChannelAuthRequest 是钉钉渠道发起请求体（手填机器人凭证，字段名全栈统一为 client_id/client_secret）。
type DingtalkChannelAuthRequest struct {
	// ClientID 是钉钉应用 Client ID（即 AppKey，必填）。
	ClientID string `json:"client_id" binding:"required"`
	// ClientSecret 是钉钉 Client Secret（即 AppSecret，必填，仅入库密文与注入 Secret，不回显）。
	ClientSecret string `json:"client_secret" binding:"required"`
}
```

- [ ] **Step 4：`channelService` 接口加方法 + 分流 + swag**

在 `channels.go` 的 `channelService` 接口中，`BeginWorkWechatAuth` 那行之后插入：
```go
	// BeginDingtalkAuth 是钉钉专用发起入口（手填 client_id+client_secret，同步注入）。
	BeginDingtalkAuth(ctx context.Context, principal auth.Principal, appID string, in service.DingtalkAuthInput) (service.ChallengeResult, error)
```

在 `BeginAuth` 方法的企业微信分流块（`if channelType == domain.ChannelTypeWorkWeChat { ... }` 的 `}` 之后、`// 其他渠道` 之前）插入：
```go
	// 钉钉走专用入口（读请求体 client_id+client_secret，手填同步注入），与微信/飞书/企业微信分流。
	if channelType == domain.ChannelTypeDingTalk {
		var req DingtalkChannelAuthRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgChannelInvalidRequest)
			return
		}
		result, err := h.service.BeginDingtalkAuth(c.Request.Context(), principal, appID, service.DingtalkAuthInput{
			ClientID:     req.ClientID,
			ClientSecret: req.ClientSecret,
		})
		if err != nil {
			writeChannelError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": result})
		return
	}
```

把 `BeginAuth` 的 swag `@Description` 那行：
```go
// @Description  为指定应用和渠道类型发起登录授权流程，返回挑战信息（如二维码 URL）。feishu / work_wechat 渠道需传请求体，其他渠道（如 wechat）无需请求体。
```
改为追加 dingtalk：
```go
// @Description  为指定应用和渠道类型发起登录授权流程，返回挑战信息（如二维码 URL）。feishu / work_wechat / dingtalk 渠道需传请求体，其他渠道（如 wechat）无需请求体。
```

- [ ] **Step 5：跑测试验证通过 + 编译**

Run: `go test ./internal/api/handlers/ -run TestBeginAuth_Dingtalk -v && go build ./...`
Expected: PASS + 编译成功。

- [ ] **Step 6：Commit**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/channels.go internal/api/handlers/channels_test.go
git commit -m "feat(api): 钉钉渠道 DTO + BeginAuth 分流到 BeginDingtalkAuth

DingtalkChannelAuthRequest{client_id, client_secret} required 校验；handler
按 channelType=dingtalk 分流；channelService 接口加 BeginDingtalkAuth。"
```

---

## Task 10：worker channelLabelWorker 加钉钉

**Files:**
- Modify: `internal/worker/handlers/channel_login.go`（`channelLabelWorker` 在 687-698）

- [ ] **Step 1：加 case**

把 `channelLabelWorker` 的：
```go
	case domain.ChannelTypeWorkWeChat:
		return "企业微信"
```
之后插入：
```go
	case domain.ChannelTypeDingTalk:
		return "钉钉"
```

- [ ] **Step 2：编译 + 全量 worker 测试**

Run: `go test ./internal/worker/... 2>&1 | tail -5`
Expected: ok（无新增失败）。

- [ ] **Step 3：Commit**

```bash
git add internal/worker/handlers/channel_login.go
git commit -m "feat(worker): channelLabelWorker 加钉钉中文标签

finalizeChannelBound 写审计 detail 时把 dingtalk 映射为「钉钉」。"
```

---

## Task 11：OpenAPI + 前端类型生成

**Files:**
- 生成物：`openapi/openapi.yaml`、`web/src/api/generated.ts`
- 参考：`Makefile`（`openapi-gen` 509-517、`web-types-gen` 519-521、`openapi-check` 523-527）

- [ ] **Step 1：跑 openapi 生成**

Run: `make openapi-gen`
Expected: 命令成功；`git status` 显示 `openapi/openapi.yaml` 有改动（至少 BeginAuth 的 description 文本更新为含 dingtalk）。

- [ ] **Step 2：跑前端类型生成**

Run: `make web-types-gen`
Expected: 命令成功；`web/src/api/generated.ts` 若有变更则一并纳入。

- [ ] **Step 3：校验同步**

Run: `make openapi-check`
Expected: 通过（跑完 openapi-gen 后工作区对该文件无残余 diff）。

- [ ] **Step 4：Commit**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步钉钉渠道 API 契约与前端类型

make openapi-gen + web-types-gen 生成产物随 handler 改动同步。"
```

---

## Task 12：前端 useBeginDingtalkAuth hook

**Files:**
- Modify: `web/src/api/hooks/useChannel.ts`（`WorkWechatAuthBody` 在 157-163；`useBeginWorkWechatAuth` 在 165-184）

- [ ] **Step 1：加 `DingtalkAuthBody` + `useBeginDingtalkAuth`**

在 `useBeginWorkWechatAuth`（以其 `}` 结尾）之后插入：
```ts

// DingtalkAuthBody 描述钉钉发起绑定的请求体（手填机器人凭证，字段名全栈统一 client_id/client_secret）。
export interface DingtalkAuthBody {
  // 钉钉应用 Client ID（即 AppKey）。
  client_id: string
  // 钉钉 Client Secret（即 AppSecret，仅提交，不回显）。
  client_secret: string
}

// useBeginDingtalkAuth 触发钉钉手填绑定，发起需携带 client_id+client_secret body。
// 复用通用进度轮询（GET /channels/dingtalk/auth）与解绑接口，仅发起入口不同。
// 成功后失效钉钉进度缓存，让轮询尽快拉到连通态。
export function useBeginDingtalkAuth(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (body: DingtalkAuthBody) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/dingtalk/auth`,
        { method: 'POST', body },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, 'dingtalk') })
    },
  })
}
```

- [ ] **Step 2：类型检查**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | grep -i useChannel | head` （或全量 `npm run type-check`）
Expected: 无 useChannel 相关报错。

- [ ] **Step 3：Commit**

```bash
git add web/src/api/hooks/useChannel.ts
git commit -m "feat(web): useBeginDingtalkAuth hook + DingtalkAuthBody 类型

克隆 useBeginWorkWechatAuth：POST /channels/dingtalk/auth 带 client_id/client_secret，
成功后失效钉钉进度缓存。"
```

---

## Task 13：前端 AppChannelsTab 钉钉表单

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.vue`（channels 列表在 229-239；操作区在 79-90；详情模板在 147-176；import 在 196-206；script 在 343-379）

- [ ] **Step 1：渠道列表 dingtalk 改 supported:true**

把 channels 列表第 233 行：
```ts
  { type: 'dingtalk', name: t('apps.channels.channelDingtalk'), description: t('apps.channels.channelDingtalkDesc'), supported: false, statusLabel: t('apps.channels.unsupported') },
```
改为：
```ts
  { type: 'dingtalk', name: t('apps.channels.channelDingtalk'), description: t('apps.channels.channelDingtalkDesc'), supported: true, statusLabel: t('apps.channels.supported') },
```

- [ ] **Step 2：import 加 `useBeginDingtalkAuth`**

把 import 块中：
```ts
  useBeginWorkWechatAuth,
```
之后加：
```ts
  useBeginDingtalkAuth,
```

- [ ] **Step 3：操作区加钉钉提交/解绑按钮**

在企业微信操作区 `<n-space v-else-if="selectedChannelType === 'work_wechat'" :size="8"> ... </n-space>`（含解绑按钮的 `</n-space>`）之后插入：
```html
          <!-- 钉钉操作区：手填凭证齐备且实例就绪才可提交，已连接时仅留解绑入口。 -->
          <n-space v-else-if="selectedChannelType === 'dingtalk'" :size="8">
            <n-button
              v-if="!dingtalkBound"
              type="primary"
              :disabled="!appId || !canManage || !instanceReady || !dingtalkClientId || !dingtalkSecret"
              :loading="dingtalkBeginning"
              @click="submitDingtalk"
            >
              {{ t('apps.channels.dingtalkSubmit') }}
            </n-button>
            <n-button v-if="dingtalkCanUnbind" @click="unbindDingtalk">{{ t('apps.channels.unbind') }}</n-button>
          </n-space>
```

- [ ] **Step 4：详情区加钉钉面板模板**

在企业微信详情 `<template v-else-if="selectedChannelType === 'work_wechat'"> ... </template>`（以其 `</template>` 结尾，紧接 `</section>` 之前）之后插入：
```html

        <!-- 钉钉渠道详情：已连接给出提示，未连接展示 client_id + client_secret 手填表单与精简内联指引（无扫码、无二维码）。 -->
        <template v-else-if="selectedChannelType === 'dingtalk'">
          <div class="wecom-panel">
            <template v-if="dingtalkBound">
              <div class="wecom-bound">
                <p class="state-text">{{ t('apps.channels.dingtalkBoundHint') }}</p>
              </div>
            </template>
            <template v-else>
              <p v-if="canManage && !instanceReady" class="state-text">{{ instanceNotReadyHint }}</p>
              <div class="wecom-controls">
                <label class="wecom-field">
                  <span class="wecom-field-label">{{ t('apps.channels.dingtalkClientIdLabel') }}</span>
                  <n-input v-model:value="dingtalkClientId" :disabled="!canManage || !instanceReady" :placeholder="t('apps.channels.dingtalkClientIdPlaceholder')" />
                </label>
                <label class="wecom-field">
                  <span class="wecom-field-label">{{ t('apps.channels.dingtalkSecretLabel') }}</span>
                  <n-input v-model:value="dingtalkSecret" type="password" show-password-on="click" :disabled="!canManage || !instanceReady" :placeholder="t('apps.channels.dingtalkSecretPlaceholder')" />
                </label>
              </div>
              <p class="wecom-guide">
                {{ t('apps.channels.dingtalkGuide') }}
                <a class="wecom-guide-link" :href="DINGTALK_DOC_URL" target="_blank" rel="noopener noreferrer">{{ t('apps.channels.dingtalkGuideLink') }}</a>
              </p>
            </template>
            <p v-if="dingtalkError" class="state-text danger">{{ t('apps.channels.errorMsg') }}{{ dingtalkError }}</p>
          </div>
        </template>
```

- [ ] **Step 5：script 加钉钉状态与方法**

在企业微信 script 段（`unbindWorkWechat` 函数的 `}` 之后）插入：
```ts

// ---- 钉钉渠道（手填机器人凭证）----
// 与企业微信同构：无模式选择、无二维码，仅 client_id + client_secret 手填表单 + 提交。
// DINGTALK_DOC_URL 指向钉钉机器人接入指引（开放平台建企业内部应用 → 启用 Stream 模式 → 复制 Client ID/Secret）。
const DINGTALK_DOC_URL = 'https://open.dingtalk.com/document/orgapp/the-creation-and-installation-of-the-application-robot-in-the'
// 钉钉手填表单输入（仅提交时使用，不回显已绑定 secret）。
const dingtalkClientId = ref('')
const dingtalkSecret = ref('')
const beginDingtalk = useBeginDingtalkAuth(appId)
const dingtalkBeginning = computed(() => beginDingtalk.isPending.value)
// dingtalkProgressType 仅在选中钉钉时返回 'dingtalk'，否则 undefined 关闭轮询。
const dingtalkProgressType = computed<string | undefined>(() => (selectedChannelType.value === 'dingtalk' ? 'dingtalk' : undefined))
const dingtalkChannelRef = computed<string | undefined>(() => 'dingtalk')
const { data: dingtalkProgress } = useChannelProgressQuery(appId, dingtalkProgressType)
const unbindDingtalkMutation = useUnbindChannel(appId, dingtalkChannelRef)
// dingtalkBound 表示钉钉已连接，用于切换已连接提示与解绑按钮。
const dingtalkBound = computed(() => dingtalkProgress.value?.status === 'bound')
// dingtalkError 展示最近一次绑定失败原因（钉钉无 fatal，通常为超时文案）。
const dingtalkError = computed(() => dingtalkProgress.value?.error_message ?? '')
// dingtalkCanUnbind 受管理权限与非未绑定态共同约束。
const dingtalkCanUnbind = computed(() => canManage.value && Boolean(dingtalkProgress.value && dingtalkProgress.value.status !== 'unbound'))

// submitDingtalk 提交手填凭证：调发起接口，成功后清空 secret 输入（不滞留明文）。
async function submitDingtalk() {
  if (!canManage.value) return
  if (!dingtalkClientId.value || !dingtalkSecret.value) return
  await beginDingtalk.mutateAsync({ client_id: dingtalkClientId.value, client_secret: dingtalkSecret.value })
  dingtalkSecret.value = ''
}

// unbindDingtalk 解绑钉钉，等待进度缓存失效后回到未绑定表单展示。
async function unbindDingtalk() {
  if (!canManage.value) return
  await unbindDingtalkMutation.mutateAsync()
}
```

- [ ] **Step 6：statusLabel 加钉钉 pending 文案覆盖**

把 `statusLabel` computed 中企业微信分支：
```ts
  if (selectedChannelType.value === 'work_wechat') {
    if (wecomProgress.value?.status === 'pending_auth') return t('apps.channels.workWechatConnecting')
    return formatChannelStatus(wecomProgress.value?.status)
  }
```
之后插入：
```ts
  if (selectedChannelType.value === 'dingtalk') {
    // 钉钉无扫码：pending_auth = 「凭证已提交、正在验证连接」，复用专属文案。
    if (dingtalkProgress.value?.status === 'pending_auth') return t('apps.channels.dingtalkConnecting')
    return formatChannelStatus(dingtalkProgress.value?.status)
  }
```

- [ ] **Step 7：类型检查**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | grep -iE 'AppChannelsTab|dingtalk' | head`
Expected: 无报错（i18n key 缺失不报 TS 错，Task 14 补）。

- [ ] **Step 8：Commit**

```bash
git add web/src/pages/apps/AppChannelsTab.vue
git commit -m "feat(web): AppChannelsTab 钉钉手填表单 + 提交/解绑

dingtalk 渠道 supported:true；克隆企业微信面板：Client ID/Client Secret 手填、
就绪闸门禁用、pending_auth 显「验证连接中」、复用进度轮询与解绑 hook。"
```

---

## Task 14：前端 i18n 钉钉表单文案（zh + en）

**Files:**
- Modify: `web/src/i18n/locales/zh/apps/root.ts`（企业微信文案在 128-163 区）
- Modify: `web/src/i18n/locales/en/apps/root.ts`（企业微信文案在 128-163 区）

- [ ] **Step 1：zh 补钉钉表单文案**

在 `web/src/i18n/locales/zh/apps/root.ts` 的 `channelDingtalkDesc: '企业通讯与审批场景',` 那行之后（或紧邻 workWechat 文案块）插入：
```ts
    dingtalkSubmit: '提交并连接',
    dingtalkClientIdLabel: 'Client ID',
    dingtalkClientIdPlaceholder: '钉钉开放平台「凭证与基础信息」中的 Client ID（即 AppKey）',
    dingtalkSecretLabel: 'Client Secret',
    dingtalkSecretPlaceholder: 'Client Secret（即 AppSecret，仅保存，不回显）',
    dingtalkGuide: '在钉钉开放平台「创建企业内部应用 → 添加机器人 → 消息接收模式选 Stream 模式」后，于「凭证与基础信息」复制 Client ID 与 Client Secret 填入。',
    dingtalkGuideLink: '查看官方文档',
    dingtalkBoundHint: '钉钉已连接，机器人正在接收消息。',
    dingtalkConnecting: '验证连接中',
```

- [ ] **Step 2：en 补钉钉表单文案**

在 `web/src/i18n/locales/en/apps/root.ts` 对应位置插入：
```ts
    dingtalkSubmit: 'Submit & connect',
    dingtalkClientIdLabel: 'Client ID',
    dingtalkClientIdPlaceholder: 'Client ID (AppKey) from DingTalk console',
    dingtalkSecretLabel: 'Client Secret',
    dingtalkSecretPlaceholder: 'Client Secret (AppSecret, stored only, never shown)',
    dingtalkGuide: 'In DingTalk Open Platform: create an internal app → add a Bot → set message mode to Stream, then copy the Client ID and Client Secret from "Credentials & Basic Info".',
    dingtalkGuideLink: 'View official guide',
    dingtalkBoundHint: 'DingTalk connected; the bot is receiving messages.',
    dingtalkConnecting: 'Verifying connection',
```

- [ ] **Step 3：类型检查 + i18n 键齐全自检**

Run: `cd web && npx vue-tsc --noEmit 2>&1 | grep -iE 'dingtalk|apps/root' | head`
Expected: 无报错（zh/en 两侧 key 齐全）。

- [ ] **Step 4：Commit**

```bash
git add web/src/i18n/locales/zh/apps/root.ts web/src/i18n/locales/en/apps/root.ts
git commit -m "feat(web): 钉钉渠道表单中英文案

dingtalk 表单 label/placeholder/指引/状态/超时文案；术语用 Client ID/Client Secret
主名并括注 AppKey/AppSecret 兼容旧版控制台。"
```

---

## Task 15：前端 AppChannelsTab.spec 修复 + 钉钉断言

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.spec.ts`（mount helper、渠道断言在 105-144）

- [ ] **Step 1：复现既有失败**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts 2>&1 | tail -15`
Expected: 3 个用例全失败，错误 `No 'queryClient' found in Vue context`（既有破损，非本任务引入）。

- [ ] **Step 2：给 mount helper 注入 VueQueryPlugin/QueryClient**

打开 `AppChannelsTab.spec.ts`，定位 `mountChannelsTab`（或 `mount(...)` 调用处）。在 `global.plugins` 中加入 VueQueryPlugin。顶部 import：
```ts
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
```
在 mount 选项的 `global.plugins` 数组里追加（若无 plugins 字段则新增）：
```ts
        [VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }) }],
```
> 若该 spec 已 mock `useChannel.ts` 全模块（检查文件顶部有无 `vi.mock('@/api/hooks/useChannel'...)`），则 `useQueryClient` 不会真正被调用，本步可跳过——此时失败另有原因，按实际报错处理。先以实际 `vitest run` 报错为准。

- [ ] **Step 3：更新渠道断言（钉钉转 supported）**

把 `列出全部渠道` 用例中：
```ts
    // 当前已支持渠道为微信 + 飞书共 2 个；两者均展示「已支持」且可点击进入详情。
    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(2)
```
改为（实际 supported = 微信 + 企业微信 + 飞书 + 钉钉 = 4）：
```ts
    // 已支持渠道为微信 + 企业微信 + 飞书 + 钉钉共 4 个；均展示「已支持」且可点击进入详情。
    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(4)
```
把：
```ts
    expect(supportedText).toContain('微信')
    expect(supportedText).toContain('飞书')
```
改为补全：
```ts
    expect(supportedText).toContain('微信')
    expect(supportedText).toContain('企业微信')
    expect(supportedText).toContain('飞书')
    expect(supportedText).toContain('钉钉')
```
把 unsupported 相关断言由 7 改为 5：
```ts
    const unsupported = wrapper.findAll('.channel-list-item.unsupported')
    expect(unsupported).toHaveLength(5)
```
以及：
```ts
    expect(wrapper.findAll('.channel-logo.muted')).toHaveLength(5)
```
（若用例上方注释写「飞书转为已支持后其余 7 个」，同步改为「钉钉转为已支持后其余 5 个」。）

> 说明：此处把 supported 从 2 修正为 4，是因为 work_wechat 早已是 supported:true（断言此前即与代码不符，因 mount 崩溃未暴露），叠加本次钉钉转 supported。

- [ ] **Step 4：跑测试验证通过**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts 2>&1 | tail -15`
Expected: 3 个用例全 PASS。

- [ ] **Step 5：Commit**

```bash
git add web/src/pages/apps/AppChannelsTab.spec.ts
git commit -m "test(web): 修 AppChannelsTab.spec mount 缺 QueryClient + 钉钉断言

补 VueQueryPlugin 修既有 mount 崩溃；渠道断言更新为 supported=4(含企业微信/钉钉)、
unsupported=5，与渠道列表现状一致。"
```

---

## Task 16：全栈构建、测试与浏览器验证

**Files:** 无（验证 task）

- [ ] **Step 1：后端全量构建 + 测试**

Run: `go build ./... && go test ./... 2>&1 | tail -20`
Expected: 编译成功；测试全绿（重点关注 service / channel / k8sorch / handlers / worker 包）。

- [ ] **Step 2：前端构建 + 单测 + 类型检查**

Run: `cd web && npm run type-check && npx vitest run 2>&1 | tail -20 && npm run build 2>&1 | tail -5`
Expected: 类型检查通过、单测全绿、build 成功。

- [ ] **Step 3：OpenAPI 同步校验**

Run: `make openapi-check`
Expected: 工作区干净（生成物已在 Task 11 提交）。

- [ ] **Step 4：构建钉钉镜像（本地 k3d）验证引擎预装与 ocops 注册**

> 引擎改动（Task 1/2）只有在镜像构建 + 实例运行时才能真正验证。按本地 k3d 流程构建两 variant 镜像（参考 `docs/local-development.md` / Makefile 的镜像构建 target），确认：
> - Dockerfile 构建期 `uv pip install dingtalk-stream` 成功（不被旧缓存层短路，必要时 `NO_CACHE=1`）；
> - 镜像构建期 pytest 自检通过（含 ocops channel 测试）；
> - 起一个实例，`kubectl exec` 进 oc-ops 容器 `curl 127.0.0.1:8642/health/detailed` 能见 platforms（或经 manager 调 `/oc/channels/dingtalk/status` 返回结构正常）。
>
> 用 `rtk proxy kubectl`（rtk 会误压缩 kubectl 输出，见项目约定）。

- [ ] **Step 5：三角色真实浏览器端到端验证（CLAUDE.md 硬性要求，需用户提供真实钉钉 AppKey/AppSecret）**

用真实浏览器（非 curl），分别以 platform_admin / org_admin / org_member 三角色：
1. 进入某 running + ready 实例的「渠道」tab，钉钉卡片显示「已支持」可点击。
2. 填入真实 Client ID / Client Secret → 提交 → 观察实例进入 restarting（按钮禁用 + 提示）→ pod 重启完成 → 状态变「已连接（在线）」。
3. 在钉钉机器人发消息，确认 bot 正常收发（引擎真实连通）。
4. 故意填错 Client Secret 重绑 → 观察退避后变「连接超时」失败 + 统一文案（验证无 fatal 路径的超时归并）。
5. 解绑 → 观察 restarting → running 收敛，钉钉回未绑定表单。
6. 验证与微信 / 飞书 / 企业微信并存（同一实例多渠道各自独立）。
7. org_member 对非自管实例：发起被拒（权限）。

> 发现问题先修再验，直到全部通过。

- [ ] **Step 6：最终交付说明**

汇总：逐文件改动矩阵、各包测试结果、三角色浏览器验证证据（截图/录屏）、未跑项及原因（如真实钉钉账号由用户提供的部分）。

---

## 自检记录（plan vs spec 覆盖）

- spec §2 数据模型 → Task 3（枚举）+ Task 4（migration）✓
- spec §3 配置注入 → Task 5（render env/Secret）+ Task 6（buildAppSpec 带出）✓
- spec §4 引擎侧 → Task 1（Dockerfile 预装）+ Task 2（ocops DingtalkChannelOps）✓
- spec §5 状态机/失败态无 fatal → Task 7（adapter 仅 connected 给终态）+ Task 8（service）+ 超时文案 Task 14 ✓
- spec §6 绑定流程 → Task 8（service）+ Task 9（handler 分流）+ Task 7（worker 通用 check 走 adapter.PollAuth）✓
- spec §7 连通验证 → Task 2（ocops status）+ Task 7（PollAuth）✓
- spec §8 解绑 → Task 8（unbindSecretKeys + Unbind 复用）✓
- spec §9 前端 → Task 12（hook）+ Task 13（表单）+ Task 14（i18n）；ChannelLogo 已含钉钉 logo（零改动）✓
- spec §10 并存 → migration 复合唯一约束已具备；Task 16 e2e 验证并存 ✓
- spec §11 测试 → Task 5/7/8/9 单测 + Task 15 前端 spec + Task 16 e2e ✓
- spec §12 不做（高级策略/webhook/fatal/probe/多机器人/不升级引擎）→ 计划未引入，符合 ✓
- 命名一致性：全程 `dingtalk` / `DINGTALK_CLIENT_ID(_SECRET)` / `dingtalk-client-id(-secret)` / `client_id` / `client_secret` / `Dingtalk*` Go 标识，前后一致 ✓
