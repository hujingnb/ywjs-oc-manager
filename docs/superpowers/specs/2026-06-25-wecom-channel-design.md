# 企业微信渠道（智能机器人 AI Bot）设计

- 日期：2026-06-25 初版；**2026-06-27 修订**（对齐已落地的飞书渠道架构）
- 状态：设计已确认，待重写实现计划
- 范围：manager 后端 + 前端 + oc-ops 连通状态转发（两个 hermes variant）

> ## 修订说明（2026-06-27）
>
> 本 spec 初版写于飞书渠道实现**之前**。此后飞书渠道已完整落地（migration `000015`、
> `000016`），并把企业微信本来要自己铺的地基**提前铺好了**：多渠道并存约束、`env 注入 +
> Secret patch + RolloutRestart`、health 探测连通、`restarting` 过渡态、`ChannelOps`
> oc-ops 注册表。因此企业微信现在的本质是 **「飞书手填那一半」+ `WECOM_*` env**，比初版设
> 想的简单得多。
>
> **设计决策全部不变**（AI Bot 长连接、env 注入、DB 为 source of truth、渠道并存、health
> 探测）。本次修订只对齐**实现细节**：废弃过时的文件行号与「直接改 oc-ops `channel.py`
> if-chain」假设，改为复用飞书已验证的链路；并补上初版漏掉的 `restarting` 状态。
>
> **与飞书最大的差异（决定了哪些要新写）**：企业微信**没有扫码协议，只能纯手填**。而飞书
> 最终**砍掉了手填、只留扫码**（提交 9f8c1f9 等）。所以飞书删掉的那条「带配置体的手填 auth
> 入口」正是企业微信唯一要走的路，当前代码里已不存在，需要**重新引入**（可参考被删飞书手填
> 的 git 历史）。飞书有的「扫码 SSE / `internal/integrations/channel` adapter」企业微信
> **不需要**。

## 1. 背景与目标

为 app 实例新增「企业微信」渠道。前端此前已有 `work_wechat` 的 UI 预告（卡片 `supported:
false` 灰显）、品牌图标、`channelWorkWechat` 中英文案，后端零实现。

本设计落地企业微信渠道的完整链路：用户填凭证 → manager 安全注入 hermes 容器 → 重启生效 →
即时验证连通状态。

### 接入形态：智能机器人 AI Bot 长连接（Polling）（已查证锁定）

企业微信官方接入有多种形态，经查证后锁定为 **智能机器人 AI Bot 长连接（Polling）**，理由与
排除项：

- hermes gateway 引擎镜像内 `gateway/platforms/wecom.py` 已完整实现该形态：
  - 文档头：「WeCom (Enterprise WeChat) platform adapter. Uses the WeCom AI Bot WebSocket gateway」
  - 认证 `aibot_subscribe`、收消息 `aibot_msg_callback`、发消息 `aibot_send_msg`/`aibot_respond_msg`
  - 配置：`bot_id` + `secret`（+ 可选 `websocket_url`，默认 `wss://openws.work.weixin.qq.com`）
  - 主动 WebSocket 外连，**不需要公网回调 URL / 公网 IP / 域名**
- **排除「个人号扫码」**：引擎对企业微信无扫码协议（无 `qr_login`），企业微信不存在个人号扫码登录形态。
- **排除「扫码自动创建机器人」**：经查证企业微信**没有**「程序化创建智能机器人并下发 bot_id/secret」的开放 API。智能机器人只能在企业微信客户端手动创建（工作台→智能机器人→创建→API 模式→长连接配置→Bot Id 自动生成、Secret 随机获取→手动复制）。开放能力仅 `aibot_subscribe`/`aibot_msg_callback`/`aibot_respond_msg` 三个长连接指令。所有第三方方案（LangBot/OpenClaw/QClaw）均为手动复制凭证。
  - **这正是企业微信与飞书的根本区别**：飞书有 `qr_register()` 设备码流程可扫码自动建应用，企业微信没有对等能力，只能手动复制凭证。故企业微信**只有手填一条路**。
- **排除「服务商扫码授权」**：企业微信第三方服务商/代开发模式可做「扫码授权自动接入」，但 (1) 接的是自建应用（对应引擎更重的 `wecom_callback` 线，授权范围不含「管理机器人」）；(2) 需 oc-manager 注册成企业微信服务商 + 一整套服务商 OAuth 基建（suite_ticket 回调、permanent_code、token 刷新）；(3) 每实例需公网回调 URL，丢掉智能机器人「免公网」优势。属独立大型项目，本期不做。

凭证获取由企业管理员在企业微信后台手动完成，manager 端提供「清晰表单 + 即时验证连通」作为最
佳体验补偿。

### 与既有两条渠道的关系

| 维度 | 微信 wechat | 飞书 feishu（已实现） | 企业微信 work_wechat（本设计） |
|---|---|---|---|
| 取凭证 | 个人号扫码（hermes weixin `qr_login`） | 扫码自动创建（`qr_register`）SSE | **用户手填 bot_id+secret（无扫码）** |
| 凭证落点 | hermes 自落盘 `/opt/data/weixin/accounts/`（文件态） | manager 经手注入 env | **manager 经手注入 env** |
| `internal/integrations/channel` adapter | `WeChatAdapter`（SSE） | `FeishuAdapter`（SSE 消费 + TakeCredentials） | **不需要 adapter（无 SSE）** |
| oc-ops 角色 | `channel_login` 扫码 SSE + 文件态 status/unbind | `FeishuChannelOps`（扫码 SSE + status） | **`WorkWechatChannelOps`（仅 status 连通转发）** |
| 配置注入 | 文件态（render_env 读 accounts） | Deployment env `FEISHU_*`（manager 直注） | Deployment env `WECOM_*`（manager 直注） |
| 绑定判定 | oc-ops 查 accounts 文件 | hermes `/health/detailed` 报 `platforms.feishu` | hermes `/health/detailed` 报 `platforms.wecom` |

一句话：企业微信 = **取凭证纯手填（无扫码、无 SSE、无 adapter）+ 注入运行复用飞书 env 链路**。

## 2. 数据模型

- migration（新建 `000017_support_work_wechat_channel`，不改 baseline 也不改飞书的 `000015`）：
  - 放宽 `channel_bindings.channel_type` CHECK 约束：由现状 `CHECK (channel_type IN ('wechat', 'feishu'))` 改为 `CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat'))`（DOWN 还原为 `('wechat', 'feishu')`）。
  - 唯一约束 `uk_channel_bindings_app_active` **无需再改**：飞书 migration `000015` 已把它改为 `(app_active_key, channel_type)` 复合键，企业微信直接受益（同一 app 可同时绑定 wechat / feishu / work_wechat 各一条非 deleted 绑定）。
  - 所有表/字段 SQL COMMENT 齐全（项目规范）。
- 复用 `channel_bindings` 表，`channel_type='work_wechat'`。
- `metadata_json` 存渠道配置：`{"bot_id": "...", "secret_ciphertext": "...", "websocket_url": "...(可选)"}`。
  - `secret` 用现成 `auth.Cipher`(AES-GCM) 加密，**DB 只存密文**（沿用 `apps.newapi_key_ciphertext` 与飞书 `app_secret_ciphertext` 惯例）。
  - `bot_id` 不敏感，明文存（详情页回显）。
- **DB 是 source of truth；k8s Secret 是派生注入物**。

### 现状证据
- `internal/migrations/000015_support_feishu_channel.up.sql:5`：现状 `CHECK (channel_type IN ('wechat', 'feishu'))`；行 12-13：`uk_channel_bindings_app_active (app_active_key, channel_type)`（已含 channel_type）。
- `internal/store/queries/channel_bindings.sql`：**所有查询已按 `app_id = ? AND channel_type = ? AND status <> 'deleted'` 过滤**，并存场景查询层零改动。
- `internal/domain/enums.go:44-47`：现有 `ChannelTypeWeChat = "wechat"`、`ChannelTypeFeishu = "feishu"`，新增 `ChannelTypeWorkWeChat = "work_wechat"`。

## 3. 配置注入（per-app Secret + optional env，照搬飞书 `feishuOptionalEnv` 模式）

hermes `config.py` 的 `_apply_env_overrides` 读 `os.getenv("WECOM_BOT_ID")`/`WECOM_SECRET`（+可选 `WECOM_WEBSOCKET_URL`），非空即启用 `Platform.WECOM`；启用判定 `bool(cfg.extra.get("bot_id"))`。

- `RenderDeployment`（`internal/integrations/k8sorch/render.go:127`，与 `feishuOptionalEnv(spec.AppID)...` 并列）hermes 容器**永久**加 optional `SecretKeyRef` env，**新写一个 `workWechatOptionalEnv(appID)` 仿 `feishuOptionalEnv`（render.go:202）**：
  - `WECOM_BOT_ID` → `app-<id>-token` Secret 的 `wecom-bot-id` key，`optional: true`
  - `WECOM_SECRET` → 同 Secret 的 `wecom-secret` key，`optional: true`
  - （可选 `WECOM_WEBSOCKET_URL` → `wecom-websocket-url` key，optional；默认不注入用引擎默认 `wss://openws.work.weixin.qq.com`）
- `RenderSecret`（render.go:28，飞书在 32-42 填三把 key）渲染时**从 DB 带出已绑定的 wecom 配置**写入 `StringData`，保证 app 重建/镜像升级不丢配置。需 `AppSpec`（`internal/integrations/k8sorch/orchestrator.go:55` 飞书三字段处）**增 `WorkWeChatBotID`/`WorkWeChatSecret`（+可选 `WorkWeChatWebsocketURL`）字段**，`buildAppSpec`（`internal/worker/handlers/app_initialize.go:397`，飞书在 437 处填字段）**从 channel_bindings 查 `work_wechat` 绑定、解密 secret 带出**（仿飞书 404-439 查询+解密块）。
- 语义：未绑定 → Secret 无此 key → optional env 不注入 → `getenv` 为空 → wecom 平台不启用。Deployment template 永不因绑定而改。

### 绑定/解绑对 k8s 的操作（复用飞书已验证范式）
- 绑定：写 DB → `PatchSecretKeys()` 给 `app-<id>-token` Secret 增 `wecom-bot-id`/`wecom-secret` → `RolloutRestart`（`internal/integrations/k8sorch/adapter.go`）→ 置 app status `restarting`（见第 4 节）。
- 解绑：写 DB（status=unbound_by_user）→ `PatchSecretKeys()` 删这两把 key → `RolloutRestart` → 置 `restarting`。
- 注意：k8s 不会因 Secret 内容变化自动重启引用它的 pod，故必须显式 `RolloutRestart`。飞书解绑链路（`channel_service.go:368-393`：`PatchSecretKeys` + `RolloutRestart` + 置 `restarting`）可直接照搬。

## 4. 状态机

### 渠道绑定状态（复用现有 `ChannelStatus*` 枚举，无扫码）

```
unbound
  └─(用户提交 bot_id+secret：写 DB+Secret+RolloutRestart)→ pending_auth（配置已下发，等待连接）
        └─(连通探测 platforms.wecom.platform_state)
              ├─ connected → bound（在线）
              ├─ fatal     → failed（带 error_message，如凭证无效）
              └─ 超时未连上 → failed（连接超时）
unbound_by_user（用户主动解绑）
```

### 实例 `restarting` 过渡态（初版漏掉，本次补上）

飞书实现暴露的新需求：绑定/解绑都要 `RolloutRestart` 重建 pod 让 hermes 装/卸凭证，重启窗口
（~20s 停机）期间 oc-ops 不可达，必须有过渡态避免裸奔 502 与状态不一致。

- 枚举 `AppStatusRestarting`（`internal/domain/enums.go:36`），转移规则 `app_state_machine.go:78-80`：`running → restarting`、`restarting → running`、`restarting → error`。
- 发起守卫 `AppCanInitiateChannelAuth()`（`app_state_machine.go:136-142`）只允许 `running`/`binding_waiting`/`binding_failed` 发起，**明确拦截 `restarting`**——企业微信绑定/解绑发起前同样受此守卫保护。
- 收敛：reconciler 按 pod Ready 把 `restarting → running`。
- 企业微信绑定/解绑**必须**走这条：解绑后置 `restarting` → `RolloutRestart` → reconciler 收敛 `running`，否则凭证残留导致绑定态不一致。

## 5. 绑定流程（端到端，纯手填）

1. 前端表单提交 → `POST /api/v1/apps/:appId/channels/work_wechat/auth`，请求体携带 `bot_id`/`secret`/可选 `websocket_url`（新增 DTO，放 `internal/api/handlers/dto.go` 导出大写命名）。
   - handler（`internal/api/handlers/channels.go`）按 `channel_type` 分流：`work_wechat` 路由到新的**专有入口**（仿飞书 `BeginFeishuAuth` 的分流方式，但请求体是「直接给配置」而非「发起扫码」）。
2. **新增 `ChannelService.BeginWorkWechatAuth`**（`internal/service/channel_service.go`，仿 `BeginFeishuAuth:216` 但**省去 SSE/扫码**）：
   - 权限校验 `CanManageApp`（`internal/auth/authorizer.go`，不在 service 内联角色判断）。
   - 实例状态守卫 `AppCanInitiateChannelAuth`。
   - 用 `auth.Cipher` 加密 secret。
   - create-on-demand：`UpsertChannelBindingUnbound` → 写凭证（**新增 `SetWorkWechatCredentials` sqlc query**，仿 `SetFeishuCredentials:43`，metadata 写 `bot_id`/`secret_ciphertext`/可选 `websocket_url`，status=pending_auth）。
   - `PatchSecretKeys` 写 Secret → 入队连通探测 job → `RolloutRestart` → 置 `restarting`。
   - **可选即时校验**：若引擎/oc-ops 提供 wecom 凭证 probe（待实现期查证 `wecom.py` 有无类似飞书 `probe_bot` 的同步校验点），可在落库前先验一次给「凭证格式/无效」的即时反馈；无则纯靠下游 health 探测。
3. worker 连通探测 handler（`internal/worker/handlers/channel_login.go` 的 `ChannelCheckBindingHandler`，加 `work_wechat` 分支或复用飞书分支）：经 oc-ops 转发 hermes `GET /health/detailed` 读 `platforms.wecom.platform_state`，带退避轮询：`connected`→bound；`fatal`→failed(error_message)；超时→failed；bound 后写审计 `channel_bound:succeeded`（沿用既有渠道审计惯例）。
4. 前端轮询进度（复用 `GET /api/v1/apps/:appId/channels/:channelType/auth` 的 `PollAuth`）展示「验证中 → 已连接 / 失败原因」。

> 注：企业微信**不需要** `internal/integrations/channel` 下的 adapter（那是给 wechat/feishu
> 消费扫码 SSE 用的）。手填没有挑战流，配置直接进 service。

### 路由

```
POST   /api/v1/apps/:appId/channels/work_wechat/auth     → BeginWorkWechatAuth（手填提交配置）
GET    /api/v1/apps/:appId/channels/work_wechat/auth     → PollAuth（连通态进度）
POST   /api/v1/apps/:appId/channels/work_wechat/unbind   → Unbind
```

## 6. 连通性验证（已查证可达，复用飞书 health 探测）

- hermes api_server `GET /health/detailed` 返回 `{"platforms": runtime.get("platforms", {})}`，含各平台连接状态（`gateway/platforms/api_server.py`）。
- 平台基类 `gateway/platforms/base.py` 统一写状态：`_mark_connected()`→`platform_state="connected"`；`_mark_disconnected()`→`"disconnected"`；`_mark_fatal(code, message)`→`"fatal"` 带 `error_code`/`error_message`；`is_connected()` property。
- wecom adapter 继承该机制：连上调 `_mark_connected()`，凭证错走 fatal 路径并带原因 → manager 可精确区分「在线 / 凭证错误（带原因）/ 连接中」。
- **oc-ops 改动（两个 variant：hermes-v2026.6.5、hermes-v2026.5.16）**：飞书已把 oc-ops 渠道运维重构为 `ChannelOps` 基类 + `_CHANNEL_OPS` 注册表（`ocops/channel.py:123` 基类、行 148 注册表、行 151 `register_channel()`；飞书在 375-376 `register_channel(FeishuChannelOps())`）。企业微信**新建 `WorkWechatChannelOps(ChannelOps)` 子类、只覆写 `status()`**（转发 `/health/detailed` 抽 `platforms.wecom`），末尾 `register_channel(WorkWechatChannelOps())`。**不需要 `auth_stream()`**（无扫码）。**不再改 if-chain / server.py**。
- manager 侧 oc-ops 客户端（`internal/integrations/ocops/`）+ service 接口（`internal/service/ocops.go` 的 `channelOps`）：连通状态查询复用飞书已有方法（若按 channel_type 参数化则零新增；否则加 wecom 方法）。

## 7. 解绑流程

`POST /api/v1/apps/:appId/channels/work_wechat/unbind` → service 写 DB（status=unbound_by_user）+ `PatchSecretKeys` 删 `wecom-bot-id`/`wecom-secret` key → `RolloutRestart` → 置 `restarting`。无需 oc-ops 文件态 unbind（那是微信 accounts 删除路径）。直接照搬飞书解绑链路（`channel_service.go:368-393`）。

## 8. 前端（复用飞书已就位的通用件）

- `web/src/pages/apps/AppChannelsTab.vue:182-192`：把 `work_wechat` 的 `supported` 由 `false` 改 `true`（现为灰显占位，紧挨已 `true` 的 wechat/feishu）。
- 渠道交互按 `channel_type` 分流；企业微信走**新表单组件**（无扫码、无模式选择）：`bot_id` + `secret`(password 输入) + 可选 `websocket_url` + 提交按钮。
- **复用飞书通用 hook**：`useChannelProgressQuery()`（`useChannel.ts:95`，4s 轮询）、`useUnbindChannel()`（行 159）；新增 `useBeginWorkWechatAuth`（仿 `useBeginFeishuAuth:139`，带配置 body）。
- 状态展示复用飞书逻辑（调整 i18n key）：验证中 / 已连接（在线）/ 失败（展示具体 `error_message`）；`restarting` 期间「发起/解绑」按钮按 `instanceReady` 闸门 disabled 并提示「实例重启中，稍后再试」（飞书 `AppChannelsTab.vue:51,72,84,123` 已有该闸门）。
- 操作按钮位置遵循现行约定：统一在标题右上（提交 030d088）。
- 已绑定详情：`bot_id` 展示 + secret 脱敏（不回显明文）+ 重新配置 + 解绑。
- i18n 中英补全（三件套，参考既有渠道文案）：`web/src/i18n/locales/{zh,en}/apps/root.ts`，`channelWorkWechat` 已有，补表单/状态/错误文案。
- **图文指引（待定，见第 11 节）**：飞书最终**移除**了图文指引折叠块（提交 3bb575a）。企业微信因「手动复制凭证是唯一路径」，是否保留一段**轻量内联指引**（如何在企业微信后台建机器人取 Bot Id + Secret）实现期定；倾向保留精简版而非重型折叠块，以与现行渠道 UI 一致。
- OpenAPI 同步：新增/改动 handler 后跑 `make openapi-gen` + `make web-types-gen`，连同代码提交（不手改 `openapi.yaml` / `generated.ts`）。

## 9. 渠道并存（已由飞书落地，零新增基础设施）

支持同一实例同时绑定微信、飞书、企业微信：

- schema：唯一约束 `uk_channel_bindings_app_active (app_active_key, channel_type)` 飞书 `000015` 已建；企业微信仅需放宽 CHECK 约束（第 2 节）。
- 查询：现有 sqlc 已按 `(app_id, channel_type)` 过滤，零改动。
- 引擎：`config.platforms` 为 dict，weixin（文件态）、feishu、wecom（env 态）可同时 enabled，互不干扰。
- 前端：`AppChannelsTab` 本就是渠道列表、每渠道独立绑定/解绑，天然支持。

## 10. 测试

- service 单测：配置加密写入（`SetWorkWechatCredentials`）、状态流转（pending_auth→bound/failed）、权限校验（org_admin/org_member/platform_admin 三角色）、`restarting` 守卫拦截、并存（同 app wechat+feishu+work_wechat 各一条）。
- worker 单测：连通探测 job 的 `connected`→bound、`fatal`→failed(原因)、超时→failed。
- 加密往返：secret 密文存取（`auth.Cipher`）。
- render 单测：`workWechatOptionalEnv` 注入三/两条 optional env、`RenderSecret` 按字段非空填 key、`buildAppSpec` 从绑定解密带出（仿飞书 render/orchestrator 现有单测）。
- 注释：每个测试方法/子测试/表驱动用例配中文场景注释（项目规范）。
- **浏览器端到端**（CLAUDE.md 硬性要求，用户可提供真实 bot_id/secret）：三角色（platform_admin/org_admin/org_member）真实填凭证 → 验证连通在线 → 故意填错 secret 验证 fatal 报错路径 → 解绑（验 `restarting`→`running` 收敛）→ 与微信/飞书并存。

## 11. 不做（YAGNI）

- 不做服务商扫码授权 / `wecom_callback` 自建应用回调线。
- 不做 `internal/integrations/channel` 下的 wecom adapter（无扫码 SSE，配置直进 service）。
- 不做多机器人 / 多账号。
- 不暴露 `dm_policy`/`group_policy`/`allow_from` 等引擎高级会话策略（用引擎默认）；`websocket_url` 默认隐藏走引擎默认，是否暴露为高级选项实现时定。

## 关键文件索引（2026-06-27 已校准至飞书落地后现状）

| 层 | 文件 | 说明 |
|---|---|---|
| migration | `internal/migrations/000017_support_work_wechat_channel.{up,down}.sql`（新建） | CHECK 约束加 `work_wechat`；唯一约束已由 `000015` 含 channel_type，无需再改 |
| 枚举 | `internal/domain/enums.go:44-47` | 加 `ChannelTypeWorkWeChat` |
| 查询 | `internal/store/queries/channel_bindings.sql` | 已按 channel_type 过滤；新增 `SetWorkWechatCredentials`（仿 `SetFeishuCredentials`） |
| 注入-Secret/env | `internal/integrations/k8sorch/render.go:28,127,202` | `RenderSecret` 填 wecom key；`workWechatOptionalEnv` 仿 `feishuOptionalEnv` |
| 编排-AppSpec | `internal/integrations/k8sorch/orchestrator.go:55` | 加 `WorkWeChatBotID`/`WorkWeChatSecret`(+可选 URL) 字段 |
| 重启 | `internal/integrations/k8sorch/adapter.go` | `RolloutRestart`（飞书已用） |
| 初始化 | `internal/worker/handlers/app_initialize.go:397` | `buildAppSpec` 查 `work_wechat` 绑定 + 解密带出（仿飞书 404-439） |
| 状态机 | `internal/domain/enums.go:36`、`app_state_machine.go:78-80,136-142` | `restarting` 过渡态 + 发起守卫（飞书已建，复用） |
| service | `internal/service/channel_service.go:216,368-393` | 新增 `BeginWorkWechatAuth`（仿 `BeginFeishuAuth` 去 SSE）；解绑复用 |
| worker | `internal/worker/handlers/channel_login.go` | 连通探测 handler 加 work_wechat 分支 |
| handler/DTO | `internal/api/handlers/channels.go`、`dto.go` | 配置请求体 + channel_type 分流 |
| oc-ops 客户端 | `internal/integrations/ocops/`、`service/ocops.go` | 连通状态查询（参数化则复用飞书） |
| 前端 | `web/src/pages/apps/AppChannelsTab.vue:182-192`、`api/hooks/useChannel.ts:95,139,159` | `supported:true` + 表单 + `useBeginWorkWechatAuth` + 复用进度/解绑 hook |
| i18n | `web/src/i18n/locales/{zh,en}/apps/root.ts` | 表单/状态/错误文案 |
| 引擎（只读参考，零改动） | 容器内 `gateway/platforms/wecom.py`、`config.py`、`base.py`、`api_server.py` | wecom 适配器/env/health |
| oc-ops（两 variant 改） | `runtime/hermes/hermes-v2026.6.5/ocops/channel.py:123,148,151`、`hermes-v2026.5.16/ocops/channel.py` | 新建 `WorkWechatChannelOps(ChannelOps)` 只覆写 `status()` + `register_channel()`；不改 if-chain |
