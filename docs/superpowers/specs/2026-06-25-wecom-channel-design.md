# 企业微信渠道（智能机器人 AI Bot）设计

- 日期：2026-06-25
- 状态：设计已确认，待写实现计划
- 范围：manager 后端 + 前端 + oc-ops 连通状态转发（两个 hermes variant）

## 1. 背景与目标

为 app 实例新增「企业微信」渠道。前端此前已有 `work_wechat` 的 UI 预告（卡片禁用）、品牌图标、`channelWorkWechat` 中英文案，后端零实现。

本设计落地企业微信渠道的完整链路：用户填凭证 → manager 安全注入 hermes 容器 → 重启生效 → 即时验证连通状态。

### 接入形态：智能机器人 AI Bot 长连接（已查证锁定）

企业微信官方接入有多种形态，经查证后锁定为 **智能机器人 AI Bot 长连接（Polling）**，理由与排除项：

- hermes gateway 引擎镜像内 `gateway/platforms/wecom.py` 已完整实现该形态：
  - 文档头：「WeCom (Enterprise WeChat) platform adapter. Uses the WeCom AI Bot WebSocket gateway」
  - 认证 `aibot_subscribe`、收消息 `aibot_msg_callback`、发消息 `aibot_send_msg`/`aibot_respond_msg`
  - 配置：`bot_id` + `secret`（+ 可选 `websocket_url`，默认 `wss://openws.work.weixin.qq.com`）
  - 主动 WebSocket 外连，**不需要公网回调 URL / 公网 IP / 域名**
- **排除「个人号扫码」**：引擎对企业微信无扫码协议（无 `qr_login`），企业微信不存在个人号扫码登录形态。
- **排除「扫码自动创建机器人」**：经查证企业微信**没有**「程序化创建智能机器人并下发 bot_id/secret」的开放 API。智能机器人只能在企业微信客户端手动创建（工作台→智能机器人→创建→API 模式→长连接配置→Bot Id 自动生成、Secret 随机获取→手动复制）。开放能力仅 `aibot_subscribe`/`aibot_msg_callback`/`aibot_respond_msg` 三个长连接指令。所有第三方方案（LangBot/OpenClaw/QClaw）均为手动复制凭证。
- **排除「服务商扫码授权」**：企业微信第三方服务商/代开发模式可做「扫码授权自动接入」，但 (1) 接的是自建应用（对应引擎更重的 `wecom_callback` 线，授权范围不含「管理机器人」）；(2) 需 oc-manager 注册成企业微信服务商 + 一整套服务商 OAuth 基建（suite_ticket 回调、permanent_code、token 刷新）；(3) 每实例需公网回调 URL，丢掉智能机器人「免公网」优势。属独立大型项目，本期不做。

凭证获取由企业管理员在企业微信后台手动完成，manager 端提供「清晰表单 + 图文指引 + 填完即时验证连通」作为最佳体验补偿。

### 与微信渠道的本质差异

| 维度 | 微信 wechat | 企业微信 work_wechat |
|---|---|---|
| 接入 | 个人号扫码（hermes weixin plugin） | 智能机器人配置（bot_id+secret） |
| 凭证来源 | hermes 扫码后自管落盘 `/opt/data/weixin/accounts/` | 用户手动提供，manager 经手注入 |
| oc-ops 角色 | `channel_login` SSE 扫码流 + 文件态 status/unbind | 仅转发连通状态；不走扫码 SSE |
| 配置注入 | 文件态（render_env 读 accounts→`WEIXIN_*`） | Deployment env `WECOM_BOT_ID`/`WECOM_SECRET`（manager 直注） |
| 绑定判定 | oc-ops 查 accounts 文件存在 | hermes `/health/detailed` 报 `platforms.wecom.platform_state` |

## 2. 数据模型

- migration（新建一个 up/down 对，不改 baseline）：
  - 放宽 `channel_bindings.channel_type` CHECK 约束：`CHECK (channel_type IN ('wechat', 'work_wechat'))`。
  - 唯一约束 `uk_channel_bindings_app_active` 由 `(app_active_key)` 改为 `(app_active_key, channel_type)`，支持同一 app 的 wechat 与 work_wechat 各一条非 deleted 绑定（即渠道并存）。
- 复用 `channel_bindings` 表，`channel_type='work_wechat'`。
- `metadata_json` 存渠道配置：`{"bot_id": "...", "secret_ciphertext": "...", "websocket_url": "...(可选)"}`。
  - `secret` 用现成 `auth.Cipher`(AES-GCM) 加密，**DB 只存密文**（沿用 `apps.newapi_key_ciphertext` 惯例）。
  - `bot_id` 不敏感，明文存（用于详情页回显）。
- **DB 是 source of truth；k8s Secret 是派生注入物**。

### 现状证据
- `internal/migrations/000001_baseline.up.sql`：`channel_bindings` 表定义在 166-185 行；约束 182（CHECK channel_type）、184（uk_channel_bindings_app_active）、179（app_active_key 生成列）。
- `internal/store/queries/channel_bindings.sql`：**所有查询已按 `app_id = ? AND channel_type = ? AND status <> 'deleted'` 过滤**，并存场景查询层零改动。
- `internal/domain/enums.go`：现有 `ChannelTypeWeChat = "wechat"`，新增 `ChannelTypeWorkWeChat = "work_wechat"`。

## 3. 配置注入（方案 1：per-app Secret + optional env）

hermes `config.py` 的 `_apply_env_overrides` 读 `os.getenv("WECOM_BOT_ID")`/`WECOM_SECRET`（+可选 `WECOM_WEBSOCKET_URL`），非空即启用 `Platform.WECOM`；启用判定 `bool(cfg.extra.get("bot_id"))`。

- `RenderDeployment`（`internal/integrations/k8sorch/render.go`）的 hermes 容器（113-117 行 env append 处）**永久**加两条 optional `SecretKeyRef` env：
  - `WECOM_BOT_ID` → `app-<id>-token` Secret 的 `wecom-bot-id` key，`optional: true`
  - `WECOM_SECRET` → 同 Secret 的 `wecom-secret` key，`optional: true`
  - （可选 `WECOM_WEBSOCKET_URL`：若放开自定义，同样走 optional env；默认不注入用引擎默认值）
- `RenderSecret`（render.go:28-34）渲染时**从 DB 带出已绑定的 wecom 配置**写入 `StringData`，保证 app 重建/镜像升级不丢失配置（解决 RenderSecret 重建覆盖问题）。这要求 `AppSpec` 增加 wecom 字段，`buildAppSpec`（`internal/worker/handlers/app_initialize.go:390`）从 channel_bindings 解密带出。
- 语义：未绑定 → Secret 无此 key → optional env 不注入 → `getenv` 为空 → wecom 平台不启用。Deployment template 永不因绑定而改。

### 绑定/解绑对 k8s 的操作
- 绑定：写 DB → patch `app-<id>-token` Secret 增加 `wecom-bot-id`/`wecom-secret` → `RolloutRestart`。
- 解绑：写 DB（status=unbound_by_user）→ patch Secret 删除这两个 key → `RolloutRestart`。
- 注意：k8s 不会因 Secret 内容变化自动重启引用它的 pod，故必须显式 `RolloutRestart`（与微信绑定后重启一致，`RolloutRestart` 见 `internal/integrations/k8sorch/adapter.go:121`）。

## 4. 状态机（无扫码，复用现有 status 枚举）

复用 `internal/domain/enums.go` 现有 `ChannelStatus*`，语义映射：

```
unbound
  └─(用户提交 bot_id+secret：写 DB+Secret+RolloutRestart)→ pending_auth（配置已下发，等待连接）
        └─(连通探测)
              ├─ connected → bound（在线）
              ├─ fatal     → failed（带 error_message，如凭证无效）
              └─ 超时未连上 → failed（连接超时）
unbound_by_user（用户主动解绑）
```

## 5. 绑定流程（端到端）

1. 前端表单提交 → `POST /api/v1/apps/:appId/channels/work_wechat/auth`，请求体携带 `bot_id`/`secret`/可选 `websocket_url`（新增 DTO，放 `internal/api/handlers/dto.go` 导出大写命名）。
   - 注意：微信的 `BeginAuth` 无请求体；企业微信需要带配置体。两条渠道在 handler/service 内按 `channel_type` 分流。
2. `ChannelService`（`internal/service/channel_service.go`）：权限校验（`CanManageApp`）→ 用 `auth.Cipher` 加密 secret → upsert `channel_bindings`（status=pending_auth，metadata_json 写配置）→ patch k8s Secret → 创建连通探测 job → `RolloutRestart`。
3. worker 连通探测 handler（仿 `internal/worker/handlers/channel_login.go` 的 `ChannelCheckBindingHandler`，新增 work_wechat 分支或新 handler）：
   - 经 oc-ops 转发 hermes `GET /health/detailed`，读 `platforms.wecom.platform_state`。
   - 轮询若干次（带退避，参考微信 5s 延迟重试）：`connected`→bound；`fatal`→failed(error_message)；超时→failed。
   - bound 后写审计日志 `channel_bound:succeeded`（沿用微信审计惯例）。
4. 前端轮询 progress（复用 `GET /api/v1/apps/:appId/channels/:channelType/auth` 的 `PollAuth`，`web/src/api/hooks/useChannel.ts`）展示「验证中 → 已连接 / 失败原因」。

## 6. 连通性验证（已查证可达）

- hermes api_server `GET /health/detailed` 返回 `{"platforms": runtime.get("platforms", {})}`，含各平台连接状态（`gateway/platforms/api_server.py` 的 `_handle_health_detailed`）。
- 平台基类 `gateway/platforms/base.py` 统一写状态：`_mark_connected()`→`platform_state="connected"`；`_mark_disconnected()`→`"disconnected"`；`_mark_fatal(code, message)`→`"fatal"` 带 `error_code`/`error_message`；`is_connected()` property。
- wecom adapter 继承该机制：连上调 `_mark_connected()`，凭证错走 fatal 路径并带原因 → manager 可精确区分「在线 / 凭证错误（带原因）/ 连接中」。
- **oc-ops 改动（两个 variant：hermes-v2026.6.5、hermes-v2026.5.16）**：新增一条渠道连通状态路由，转发 hermes `/health/detailed` 并抽取 `platforms.wecom`（或扩展现有 `ocops/channel.py` 的 `channel_status` 支持 `work_wechat`，查 health 而非文件态）。manager→oc-ops→hermes api_server 转发模式参考现有会话转发层（`ocops/conversation.py`）。
- manager 侧 oc-ops 客户端（`internal/integrations/ocops/`）新增对应方法 + service 接口（`internal/service/ocops.go` 的 `channelOps`）。

## 7. 解绑流程

`POST /api/v1/apps/:appId/channels/work_wechat/unbind` → service 写 DB（status=unbound_by_user）+ patch Secret 删 key → `RolloutRestart`。无需 oc-ops 文件态 unbind（那是微信 accounts 删除路径）。

## 8. 前端

- `web/src/pages/apps/AppChannelsTab.vue`：`work_wechat` 的 `supported` 改 `true`。
- 渠道交互按 `channel_type` 分流：微信走现有二维码组件；企业微信走**新表单组件**。
- 企业微信表单：`bot_id` + `secret`(password 输入) + 可选 `websocket_url` + 提交按钮。
- 折叠**图文指引**：如何在企业微信后台建机器人（工作台 → 智能机器人 → 创建 → API 模式 → 长连接配置 → 复制 Bot ID + Secret）。
- 状态展示：验证中 / 已连接（在线）/ 失败（展示具体 error_message）。
- 已绑定详情：`bot_id` 展示 + secret 脱敏（不回显明文）+ 重新配置 + 解绑。
- i18n 中英补全（i18n 三件套，参考既有渠道文案）：`web/src/i18n/locales/{zh,en}/apps/root.ts`，`channelWorkWechat` 已有，补表单/指引/状态/错误文案。
- OpenAPI 同步：新增/改动 handler 后跑 `make openapi-gen` + `make web-types-gen`，连同代码提交。

## 9. 渠道并存（已确认）

支持同一实例同时绑定微信与企业微信：

- schema：唯一约束 `uk_channel_bindings_app_active` 加 `channel_type`（见第 2 节）。
- 查询：现有 sqlc 已按 `(app_id, channel_type)` 过滤，零改动。
- 引擎：`config.platforms` 为 dict，weixin（文件态）与 wecom（env 态）可同时 enabled，互不干扰。
- 前端：`AppChannelsTab` 本就是渠道列表、每渠道独立绑定/解绑，天然支持。

## 10. 测试

- service 单测：配置加密写入、状态流转（pending_auth→bound/failed）、权限校验（org_admin/org_member/platform_admin）、并存（同 app wechat+work_wechat 各一条）。
- worker 单测：连通探测 job 的 `connected`→bound、`fatal`→failed(原因)、超时→failed。
- 加密往返：secret 密文存取（auth.Cipher）。
- 注释：每个测试方法/子测试/表驱动用例配中文场景注释（项目规范）。
- **浏览器端到端**（CLAUDE.md 硬性要求，用户可提供真实 bot_id/secret）：三角色（platform_admin/org_admin/org_member）真实填凭证 → 验证连通在线 → 故意填错 secret 验证 fatal 报错路径 → 解绑 → 与微信并存。

## 11. 不做（YAGNI）

- 不做服务商扫码授权 / `wecom_callback` 自建应用回调线。
- 不做多机器人 / 多账号。
- 不暴露 `dm_policy`/`group_policy`/`allow_from` 等引擎高级会话策略（用引擎默认）；`websocket_url` 默认隐藏走引擎默认，是否暴露为高级选项实现时定。

## 关键文件索引

| 层 | 文件 | 说明 |
|---|---|---|
| migration | `internal/migrations/000001_baseline.up.sql:166-185` | channel_bindings 表（新建 up/down 改约束） |
| 查询 | `internal/store/queries/channel_bindings.sql` | 已按 channel_type 过滤 |
| 枚举 | `internal/domain/enums.go` | 加 ChannelTypeWorkWeChat |
| 注入 | `internal/integrations/k8sorch/render.go:28-117` | RenderSecret + hermes 容器 env |
| 编排 | `internal/integrations/k8sorch/orchestrator.go:34-55` | AppSpec 加 wecom 字段 |
| 重启 | `internal/integrations/k8sorch/adapter.go:121` | RolloutRestart |
| 初始化 | `internal/worker/handlers/app_initialize.go:390` | buildAppSpec 带出 wecom |
| service | `internal/service/channel_service.go` | BeginAuth/PollAuth 分流 |
| worker | `internal/worker/handlers/channel_login.go` | 连通探测 handler |
| handler/DTO | `internal/api/handlers/channels.go`、`dto.go` | 配置请求体 |
| oc-ops 客户端 | `internal/integrations/ocops/`、`service/ocops.go` | 连通状态查询 |
| 前端 | `web/src/pages/apps/AppChannelsTab.vue`、`api/hooks/useChannel.ts` | 表单+状态 |
| i18n | `web/src/i18n/locales/{zh,en}/apps/root.ts` | 文案 |
| 引擎（只读参考，零改动） | 容器内 `gateway/platforms/wecom.py`、`config.py`、`base.py`、`api_server.py` | wecom 适配器/env/health |
| oc-ops（两 variant 改） | `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`、`hermes-v2026.5.16/ocops/channel.py` | 连通状态转发 |
