# 飞书 / Lark 渠道（扫码自动创建 + 手填兜底）设计

- 日期：2026-06-26
- 状态：设计已确认，待写实现计划
- 范围：manager 后端 + 前端 + oc-ops 扫码注册与连通转发（两个 hermes variant）

## 1. 背景与目标

为 app 实例新增「飞书 / Lark」渠道。前端此前已有 `feishu` 的 UI 预告（卡片禁用 `supported: false`），后端零实现。当前仓库仅微信（`wechat`）渠道落地，企业微信（`work_wechat`）与飞书均为占位，故飞书是**第一个**在微信之上新增的渠道，须顺带落地「多渠道并存」的 DB 基础。

本设计落地飞书渠道完整链路：用户取得凭证（扫码自动创建为主、手填为辅）→ manager 安全注入 hermes 容器 → 重启生效 → 即时验证连通状态。

### 接入形态：WebSocket 长连接 + 扫码自动创建（已查证锁定）

经查证引擎镜像 `hermes-runtime:v2026.6.5-dev` 内 `gateway/platforms/feishu.py`（215KB 完整适配器）锁定形态：

- **运行态：WebSocket 长连接**。引擎 `config.py:_apply_env_overrides` 读 `FEISHU_APP_ID`+`FEISHU_APP_SECRET`（二者齐全即 `Platform.FEISHU.enabled=True`，启用判定 `bool(cfg.extra.get("app_id"))`），`FEISHU_CONNECTION_MODE` 默认 `websocket`，走官方 `lark_oapi` 的 `FeishuWSClient` 主动外连，**不需要公网回调 URL / 公网 IP / 域名**。
- **取凭证态：扫码自动创建（device-code flow）**。`feishu.py` 内 `qr_register()` 走飞书 `/oauth/v1/app/registration` 设备码流程（`archetype=PersonalAgent`、`auth_method=client_secret`）：
  - `_begin_registration` 返回 `device_code` + `qr_url`（`verification_uri_complete`）；
  - 用户用飞书 / Lark 手机 App 扫码授权 → 引擎自动创建一个配置好的 bot 应用；
  - `_poll_registration` 轮询至扫码成功，返回 `app_id`(client_id) + `app_secret`(client_secret) + `domain` + `open_id`；
  - **自动识别 domain**：轮询响应 `user_info.tenant_brand == "lark"` 时切到 Lark 国际，否则飞书国内，用户无需手选；
  - 成功后 `probe_bot` 调 `/open-apis/bot/v3/info` 取 `bot_name`/`bot_open_id`（best-effort）。
- **手填兜底**：引擎 `hermes_cli/gateway.py:_setup_feishu()` 自身即「scan-to-create or manual credentials」两路设计——手填 `app_id`+`app_secret`+`domain`，同样 `probe_bot` 校验。
- **排除 webhook 回调模式**：`feishu.py` 支持 webhook（需 `FEISHU_ENCRYPT_KEY`/`FEISHU_VERIFICATION_TOKEN` + 可达 HTTP 端点），但需每实例公网回调，丢掉长连接「免公网」优势，本期不做。
- **排除飞书子功能模块**：`feishu_comment.py` / `feishu_meeting_invite.py` 等是评论 / 会议邀请等子能力，超出「消息渠道接入」范围，不涉及。

### 与既有两条渠道的关系：飞书是「混血」

| 维度 | 微信 wechat | 企业微信 work_wechat（已设计未实现） | 飞书 feishu（本设计） |
|---|---|---|---|
| 取凭证 | 个人号扫码（hermes weixin `qr_login`） | 用户手填 bot_id+secret | **扫码自动创建（`qr_register`）为主 + 手填为辅** |
| 凭证落点 | hermes 自落盘 `/opt/data/weixin/accounts/`（文件态） | manager 经手注入 env | **manager 经手注入 env（取凭证可经 SSE 扫码）** |
| oc-ops 角色 | `channel_login` 扫码 SSE + 文件态 status/unbind | 仅转发连通状态 | **扫码注册 SSE（回传凭证）+ 连通状态转发** |
| 配置注入 | 文件态（render_env 读 accounts） | Deployment env `WECOM_*` | Deployment env `FEISHU_*`（manager 直注） |
| 绑定判定 | oc-ops 查 accounts 文件 | hermes `/health/detailed` 报 `platforms.wecom` | hermes `/health/detailed` 报 `platforms.feishu` |

飞书 = **取凭证像微信（扫码 SSE）+ 注入运行像企业微信（env 态长连接 + health 探测）**。

## 2. 数据模型

- migration（新建一对 up/down，不改 baseline）——飞书作为第一个新增渠道，须落地多渠道并存基础：
  - 放宽 `channel_bindings.channel_type` CHECK 约束：`CHECK (channel_type IN ('wechat', 'feishu'))`（DOWN 还原为 `('wechat')`）。
  - 唯一约束 `uk_channel_bindings_app_active` 由 `(app_active_key)` 改为 `(app_active_key, channel_type)`，支持同一 app 的 wechat 与 feishu 各一条非 deleted 绑定（渠道并存）。
- 复用 `channel_bindings` 表，`channel_type='feishu'`。
- `metadata_json` 存渠道配置：
  ```json
  {
    "app_id": "cli_xxx",
    "app_secret_ciphertext": "...",
    "domain": "feishu",
    "acquired_by": "qr",
    "bot_name": "...",
    "bot_open_id": "..."
  }
  ```
  - `app_secret` 用现成 `auth.Cipher`(AES-GCM) 加密，**DB 只存密文**（沿用 `apps.newapi_key_ciphertext` 惯例）。
  - `app_id`、`domain`（feishu/lark）、`acquired_by`（qr/manual）、`bot_name`、`bot_open_id` 不敏感，明文存（详情页回显）。
- `bound_identity` / `channel_name` 存 `bot_name` + `bot_open_id`（`probe_bot` 返回）。
- **DB 是 source of truth；k8s Secret 是派生注入物。**

### 现状证据
- `internal/migrations/000001_baseline.up.sql`：`channel_bindings` 表（CHECK channel_type、`uk_channel_bindings_app_active`、`app_active_key` 生成列）。当前只有 `wechat`。
- `internal/store/queries/channel_bindings.sql`：所有查询已按 `app_id = ? AND channel_type = ? AND status <> 'deleted'` 过滤，并存查询层零改动。
- `internal/domain/enums.go`：现有 `ChannelTypeWeChat = "wechat"`，新增 `ChannelTypeFeishu = "feishu"`。
- `cmd/server/main.go:262`：现仅 `channelRegistry.Register(channel.NewWeChatAdapter(...))`，新增飞书 adapter 注册。

## 3. 配置注入（per-app Secret + optional env）+ 引擎依赖预装

引擎 `config.py:_apply_env_overrides` 读：`FEISHU_APP_ID`、`FEISHU_APP_SECRET`（二者齐全即启用）、`FEISHU_DOMAIN`（默认 `feishu`，国际版传 `lark`）。`FEISHU_CONNECTION_MODE` 我们不注入，用引擎默认 `websocket`。

- `RenderDeployment`（`internal/integrations/k8sorch/render.go`）hermes 容器**永久**加三条 optional `SecretKeyRef` env：
  - `FEISHU_APP_ID` → `app-<id>-token` Secret 的 `feishu-app-id` key，`optional: true`
  - `FEISHU_APP_SECRET` → 同 Secret 的 `feishu-app-secret` key，`optional: true`
  - `FEISHU_DOMAIN` → 同 Secret 的 `feishu-domain` key，`optional: true`
  - 未绑定 → Secret 无此 key → env 不注入 → `getenv` 为空 → 飞书平台不启用。Deployment 模板永不因绑定而变。
- `RenderSecret`（render.go:28-34）渲染时**从 DB 带出已绑定飞书配置**写入 `StringData`，保证 app 重建 / 镜像升级不丢配置。需 `AppSpec`（`internal/integrations/k8sorch/orchestrator.go:34-55`）增飞书字段，`buildAppSpec`（`internal/worker/handlers/app_initialize.go:390`）从 channel_bindings 解密带出。
- 绑定：写 DB → patch `app-<id>-token` Secret 增 `feishu-app-id`/`feishu-app-secret`/`feishu-domain` → `RolloutRestart`。
- 解绑：写 DB（status=unbound_by_user）→ patch Secret 删这三个 key → `RolloutRestart`。
- k8s 不会因 Secret 内容变化自动重启 pod，故必须显式 `RolloutRestart`（`internal/integrations/k8sorch/adapter.go:121`）。

### 飞书特有：引擎依赖预装（关键，企业微信无此问题）
- 已查证：`lark-oapi==1.5.3` 与 `websockets` **未预装进镜像**，引擎靠运行时 `lazy_deps.ensure("platform.feishu")` 懒装（需联网 pip）。注册流程（`_begin/_poll_registration`）用 `urllib` 不依赖 SDK，但**实际长连接运行需 `lark_oapi`+`websockets`**。
- 故在**两个 hermes variant**（v2026.6.5、v2026.5.16）的 Dockerfile 预装 `lark-oapi==1.5.3` + `websockets`，与企业微信预装 weixin 依赖一致；避免线上 pod 出网受限时飞书装不上、起不来。
- ⚠️ 实现期须对 **v2026.5.16 variant** 镜像重复一次本节查证：确认其 `gateway/platforms/feishu.py` 存在、env 契约（`FEISHU_APP_ID/SECRET/DOMAIN`）与 `qr_register`/`_begin_registration`/`_poll_registration`/`probe_bot` 函数签名一致（v2026.6.5 已确认）。

## 4. 状态机（复用现有 status 枚举）

复用 `internal/domain/enums.go` 现有 `ChannelStatus*`，扫码「等待扫码」与「等待连接」共用 `pending_auth`：

```
unbound
  └─(扫码：出二维码 / 手填：提交配置 → 取得凭证 → 写 DB+Secret+RolloutRestart)→ pending_auth
        └─(连通探测 platforms.feishu.platform_state)
              ├─ connected → bound（在线）
              ├─ fatal     → failed（带 error_message，如凭证无效 / 应用未发布）
              └─ 超时未连上 → failed（连接超时）
unbound_by_user（用户主动解绑）
```

## 5. 绑定流程（端到端，双模式）

两模式只在「怎么取凭证」不同，取得 `app_id`/`app_secret`/`domain` 后**汇流到完全一致的持久化 + 注入 + 探测**。

### 模式 A — 扫码自动创建（主，克隆微信扫码 SSE 骨架）

1. 前端发起扫码 → manager `FeishuAdapter.BeginAuth`（新 adapter，仿 `internal/integrations/channel/wechat.go` 的 `WeChatAdapter`）→ 经 oc-ops 启动**飞书注册 SSE**。
2. **oc-ops 改动（两 variant：v2026.6.5、v2026.5.16）**：在 `ocops/channel.py` 现有 `channel_login` async 事件流基础上增飞书分支：
   - `from gateway.platforms.feishu import _begin_registration, _poll_registration`（结构化返回，优于解析 stdout）。
   - 调 `_begin_registration(domain)` → yield `{"event":"qrcode","url": qr_url}`。
   - 调 `_poll_registration(device_code, interval, expire_in, domain)` 阻塞至扫码 / 超时 → 成功 yield `{"event":"credentials","app_id":...,"app_secret":...,"domain":...,"bot_name":...,"bot_open_id":...}`；失败 yield `{"event":"failed","reason":...}`。
   - **与微信终态本质差异**：微信 yield `bound`（引擎自落盘 pod 内文件态）；飞书必须把**凭证回传 manager**（manager 持有持久化 + env 注入权）。`app_secret` 经 oc-ops Bearer 鉴权 SSE 内网回传，与 `control-token` 同信任边界；manager 落库即用 `auth.Cipher` 加密。
   - ⚠️ `_begin_registration` / `_poll_registration` 为 `_` 前缀「私有」函数，实现期须在两 variant 确认存在且签名一致（私有 API 漂移风险）；若不稳定，退回「驱动公有 `qr_register()` + 捕获其 stdout 中二维码 URL」的微信同款 stdout 法。
3. manager 收到 `credentials` 事件 → `auth.Cipher` 加密 `app_secret` → upsert `channel_bindings`(status=pending_auth, `acquired_by=qr`, 写 `bot_name`/`bot_open_id`) → patch Secret → 创建连通探测 job → `RolloutRestart`。

### 模式 B — 手填兜底（仿企业微信表单）

1. 前端表单 `app_id`+`app_secret`+`domain`（飞书国内 / Lark 国际）→ `POST /api/v1/apps/:appId/channels/feishu/auth`，请求体携带配置（新增 DTO 放 `internal/api/handlers/dto.go` 导出大写命名）。微信 `BeginAuth` 无请求体，飞书手填需带配置体；两模式在 handler/service 内按入参分流。
2. `ChannelService`（`internal/service/channel_service.go`）：权限校验 `CanManageApp` →（可选）经 oc-ops 调引擎 `probe_bot` 即时验证凭证 → 加密 `app_secret` → upsert `channel_bindings`(status=pending_auth, `acquired_by=manual`) → patch Secret → 创建探测 job → `RolloutRestart`。

### 汇流（两模式一致）

3. worker 连通探测 handler（仿 `internal/worker/handlers/channel_login.go` 的 `ChannelCheckBindingHandler`，加 feishu 分支）：经 oc-ops 转发 hermes `GET /health/detailed` 读 `platforms.feishu.platform_state`，带退避轮询：`connected`→bound；`fatal`→failed(error_message)；超时→failed；bound 后写审计 `channel_bound:succeeded`。
4. 前端轮询 `PollAuth`（`GET /api/v1/apps/:appId/channels/feishu/auth`，`web/src/api/hooks/useChannel.ts`）展示「扫码中 / 验证中 → 已连接 / 失败原因」。

### 路由

```
POST   /api/v1/apps/:appId/channels/feishu/auth     → BeginAuth（扫码发起 / 手填提交，按入参分流）
GET    /api/v1/apps/:appId/channels/feishu/auth     → PollAuth（进度：扫码事件 / 连通态）
POST   /api/v1/apps/:appId/channels/feishu/unbind   → Unbind
```

## 6. 连通性验证（已查证可达）

- `feishu.py` 适配器继承平台基类 `gateway/platforms/base.py`：连上调 `_mark_connected()`（`feishu.py:1681`）→ `platform_state="connected"`；断开 `_mark_disconnected()`；凭证错走 fatal 带 `error_code`/`error_message`。
- hermes api_server `GET /health/detailed` 返回 `{"platforms": runtime.get("platforms", {})}`，含 `platforms.feishu` 连接状态。
- manager 可精确区分「在线 / 凭证错误（带原因）/ 连接中」。
- manager 侧 oc-ops 客户端（`internal/integrations/ocops/`）+ service 接口（`internal/service/ocops.go` 的 `channelOps`）新增：飞书注册 SSE 消费、（手填）`probe_bot` 转发、连通状态查询。转发模式参考现有会话转发层（`ocops/conversation.py`）。

## 7. 解绑流程

`POST /api/v1/apps/:appId/channels/feishu/unbind` → service 写 DB（status=unbound_by_user）+ patch Secret 删三个 key → `RolloutRestart`。无需 oc-ops 文件态 unbind（那是微信 accounts 删除路径）。

> 备注：扫码自动创建的飞书应用在飞书侧仍存在（解绑只停本侧长连接、清本侧凭证，不调飞书删除应用 API），符合最小动作原则。

## 8. 前端

- `web/src/pages/apps/AppChannelsTab.vue`：`feishu` 的 `supported` 改 `true`（现为灰色禁用占位）。
- 渠道交互按 `channel_type` 分流；飞书内先出**模式选择**（扫码创建 / 手动填写），与引擎 `_setup_feishu` 一致。
- 扫码模式：复用微信二维码组件（展示 `qrcode` 事件 URL）+ 轮询进度（扫码中 → 已连接 / 失败）。
- 手填模式：`app_id` + `app_secret`(password 输入) + `domain` 下拉（飞书国内 / Lark 国际）+ 提交。
- 折叠**图文指引**：扫码授权步骤（用飞书 App 扫码、确认创建）/ 手动建应用步骤（开放平台建应用 → 开启 Bot 能力 → 复制 App ID + Secret）。
- 状态展示：扫码中 / 验证中 / 已连接（在线）/ 失败（展示具体 error_message）。
- 已绑定详情：`bot_name` + `app_id` + `domain` 展示，secret 脱敏（不回显明文），重新配置，解绑。
- i18n 中英补全（三件套，参考既有渠道文案）：`web/src/i18n/locales/{zh,en}/apps/root.ts`，补飞书模式选择 / 表单 / 指引 / 状态 / 错误文案（`channelFeishu` 文案现状实现期确认有无预留）。
- OpenAPI 同步：handler 改动后跑 `make openapi-gen` + `make web-types-gen`，连同代码提交。
- 复用 `useChannel.ts` 的 `useBeginChannelAuth`/`useChannelProgressQuery`/`useUnbindChannel`（已按 `channelType` 参数化）。

## 9. 渠道并存（已确认）

支持同一实例同时绑定微信与飞书：

- schema：唯一约束 `uk_channel_bindings_app_active` 加 `channel_type`（见第 2 节）。
- 查询：现有 sqlc 已按 `(app_id, channel_type)` 过滤，零改动。
- 引擎：`config.platforms` 为 dict，weixin（文件态）与 feishu（env 态）可同时 enabled，互不干扰。
- 前端：`AppChannelsTab` 本就是渠道列表、每渠道独立绑定 / 解绑，天然支持。
- 为后续企业微信预留：CHECK 约束与并存基础已通用，企业微信后续仅追加 `work_wechat` 分支即可。

## 10. 测试

- service 单测：扫码 `credentials` 事件解析 → 落库加密、手填路径、状态流转（pending_auth→bound/failed）、权限校验（platform_admin/org_admin/org_member 三角色）、并存（同 app wechat+feishu 各一条）、domain 双值（feishu/lark）、`acquired_by` 取值。
- worker 单测：连通探测 `connected`→bound、`fatal`→failed(原因)、超时→failed。
- 加密往返：`app_secret` 密文存取（`auth.Cipher`）。
- 注释：每个测试方法 / 子测试 / 表驱动用例配中文场景注释（项目规范）。
- **浏览器端到端**（CLAUDE.md 硬性要求，需提供真实飞书账号）：三角色分别走「扫码自动创建」与「手填凭证」两条路 → 验证连通在线 → 故意填错 secret 验 fatal 报错路径 → 解绑 → 与微信并存。

## 11. 不做（YAGNI）

- 不做 webhook 回调模式（`FEISHU_ENCRYPT_KEY`/`FEISHU_VERIFICATION_TOKEN` 不暴露）。
- 不暴露 `FEISHU_CONNECTION_MODE`（固定走默认 websocket）。
- 不暴露引擎高级会话策略（`FEISHU_ALLOW_ALL_USERS`/`FEISHU_ALLOWED_USERS`/`FEISHU_GROUP_POLICY`/`FEISHU_HOME_CHANNEL` 等用引擎默认）。
- 不调飞书删除应用 API（解绑只清本侧）。
- 不做飞书子功能模块（评论 / 会议邀请等）。
- 不做多应用 / 多机器人。

## 关键文件索引

| 层 | 文件 | 说明 |
|---|---|---|
| migration | `internal/migrations/`（新建 up/down） | 放宽 channel_type CHECK + 唯一约束加 channel_type |
| 查询 | `internal/store/queries/channel_bindings.sql` | 已按 channel_type 过滤，零改动 |
| 枚举 | `internal/domain/enums.go` | 加 `ChannelTypeFeishu` |
| adapter | `internal/integrations/channel/feishu.go`（新） | 仿 `wechat.go`：扫码 SSE 消费 + 手填配置；注册进 `cmd/server/main.go:262` |
| 注入 | `internal/integrations/k8sorch/render.go:28-117` | RenderSecret 带出 + hermes 容器 FEISHU_* env |
| 编排 | `internal/integrations/k8sorch/orchestrator.go:34-55` | AppSpec 加 feishu 字段 |
| 重启 | `internal/integrations/k8sorch/adapter.go:121` | RolloutRestart |
| 初始化 | `internal/worker/handlers/app_initialize.go:390` | buildAppSpec 带出 feishu |
| service | `internal/service/channel_service.go` | BeginAuth/PollAuth 双模式分流 |
| worker | `internal/worker/handlers/channel_login.go` | 连通探测 handler 加 feishu 分支 |
| handler/DTO | `internal/api/handlers/channels.go`、`dto.go` | 手填配置请求体 |
| oc-ops 客户端 | `internal/integrations/ocops/`、`service/ocops.go` | 扫码 SSE 消费 + probe + 连通状态 |
| 前端 | `web/src/pages/apps/AppChannelsTab.vue`、`api/hooks/useChannel.ts` | 模式选择 + 二维码 / 表单 + 状态 |
| i18n | `web/src/i18n/locales/{zh,en}/apps/root.ts` | 文案 |
| 引擎（只读参考，零改动） | 容器内 `gateway/platforms/feishu.py`、`config.py`、`base.py`、`api_server.py`、`hermes_cli/gateway.py:_setup_feishu` | feishu 适配器 / env / health / 扫码注册 |
| oc-ops（两 variant 改） | `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`、`hermes-v2026.5.16/ocops/channel.py` | 扫码注册 SSE + 连通状态转发 |
| Dockerfile（两 variant 改） | `runtime/hermes/hermes-v2026.6.5/Dockerfile`、`hermes-v2026.5.16/Dockerfile` | 预装 `lark-oapi==1.5.3`+`websockets` |
