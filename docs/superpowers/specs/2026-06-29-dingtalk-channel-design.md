# 钉钉 / DingTalk 渠道（手填 AppKey/AppSecret + Stream 长连接）设计

- 日期：2026-06-29
- 状态：设计已确认，待写实现计划
- 范围：manager 后端 + 前端 + oc-ops 连通状态转发与 SDK 预装（两个 hermes variant）

## 1. 背景与目标

为 app 实例新增「钉钉 / DingTalk」渠道。前端此前已有 `dingtalk` 的 UI 预告（卡片 `supported: false` 灰显，标记「暂不支持」）。本设计落地钉钉渠道完整链路：用户填凭证 → manager 安全注入 hermes 容器 → 重启生效 → 即时验证连通状态。

钉钉是继微信（`wechat`）、飞书（`feishu`）、企业微信（`work_wechat`）之后的**第四个**渠道。多渠道并存基础（`channel_bindings` 复合唯一约束、env 注入 + Secret patch + RolloutRestart、health 探测连通、`restarting` 过渡态、`ChannelOps` oc-ops 注册表）已由飞书 / 企业微信完整铺好，钉钉直接复用。

### 关键去风险结论：引擎已原生支持钉钉，无需升级版本（已查证锁定）

上游对话引擎是公开仓库 `github.com/NousResearch/hermes-agent`，容器镜像通过 `install.sh --branch ${HERMES_REF}` 从该仓库对应 git tag 安装。逐版本核对（blobless 克隆 v2026.6.5 / v2026.5.16 / v2026.6.19 tag）确认：

- **当前在用的两个 variant（v2026.6.5 + v2026.5.16）上游 tag 本身就自带 `gateway/platforms/dingtalk.py`**，且 `gateway/config.py` 已把 `Platform.DINGTALK`（v2026.6.5 config.py:119）与启用判定（config.py:451，`client_id`/`client_secret` 二者齐全即启用）全接好。线上镜像内引擎已具备钉钉适配器，**无需升级引擎版本**（更新的 v2026.6.19 同样有，但钉钉用不到它）。
- 引擎层完全就绪，故钉钉本质是 **manager 侧管道活 + 引擎侧两处轻量补全**（SDK 预装 + oc-ops status 转发），而非「逆向引擎写新适配器」。

> 历史误判记录：早期一次仓库探查曾报「引擎不支持钉钉」，原因是只能看到仓库内的 Dockerfile / ocops，看不进容器镜像内的引擎 Python 源码，从「Dockerfile 未预装 dingtalk SDK + ocops 未注册 DingtalkChannelOps」错误反推。实际引擎层早已支持，本设计据上游 git tag 实证修正该结论。

### 接入形态：dingtalk-stream WebSocket 长连接 + 纯手填凭证（已查证锁定）

经查证 `gateway/platforms/dingtalk.py`（v2026.6.5 / v2026.5.16 / v2026.6.19 形态一致）：

- **运行态：dingtalk-stream WebSocket 长连接**。引擎读 `DINGTALK_CLIENT_ID`（= 钉钉应用 AppKey）+ `DINGTALK_CLIENT_SECRET`（= AppSecret），二者齐全即 `Platform.DINGTALK.enabled=True`（`config.py` 启用判定 `bool(cfg.extra.get("client_id") or getenv("DINGTALK_CLIENT_ID")) and (... client_secret ...)`）。底层用官方 `dingtalk-stream>=0.20` SDK 的 `DingTalkStreamClient` 主动外连，**不需要公网回调 URL / 公网 IP / 域名**。
- **取凭证态：纯手填，无扫码**。钉钉没有「扫码自动创建机器人并下发凭证」的开放 API；`dingtalk.py` 内无 `qr_register` / `_begin_registration` / `device_code` 等扫码注册函数。凭证由企业管理员在钉钉开放平台手动创建企业内部应用（机器人 → Stream 推送模式 → 复制 AppKey + AppSecret）后手填。
- **连通态上报**：`dingtalk.py` 继承平台基类 `gateway/platforms/base.py`，连上调 `_mark_connected()`（dingtalk.py:294）→ `platform_state="connected"`；断开调 `_mark_disconnected()`（dingtalk.py:326）→ `platform_state="disconnected"`。
- **失败态差异（关键，决定失败 UX）**：`dingtalk.py` **从不调 `_mark_fatal()`**（base.py:2160 提供该方法，但钉钉适配器不使用）。后果是凭证填错时，引擎**不会上报「凭证无效（带 error_code/error_message）」的 fatal 态**，只会一直停在 `disconnected` / 未达 `connected`。故 manager **无法像飞书 / 企业微信那样精确区分「凭证错误 vs 连接中」**，凭证错统一归入「连接超时」失败（见第 5 节）。
- **无即时校验 probe**：`dingtalk.py` 仅有 `get_chat_info(chat_id)`（需已知 chat_id，不能用于凭证预校验），无飞书 `probe_bot` 式同步凭证校验点。故落库前不做即时校验，统一靠下游 health 探测（见第 6 节）。
- **依赖未预装**：`dingtalk-stream>=0.20` + `httpx` 未预装进镜像，引擎靠运行时 `tools.lazy_deps.ensure("platform.dingtalk")` 懒装（需联网 pip）。本设计在两个 variant 的 Dockerfile 预装，避免线上 pod 出网受限时装不上、起不来（与 feishu / discord 预装同因，见第 4 节）。

### 与既有三条渠道的关系：钉钉 = 企业微信模式

| 维度 | 微信 wechat | 飞书 feishu | 企业微信 work_wechat | 钉钉 dingtalk（本设计） |
|---|---|---|---|---|
| 取凭证 | 个人号扫码（`qr_login`） | 扫码自动创建（`qr_register`）SSE | 用户手填 bot_id+secret | **用户手填 Client ID+Client Secret（即 AppKey/AppSecret，无扫码）** |
| 凭证落点 | hermes 自落盘文件态 | manager 经手注入 env | manager 经手注入 env | **manager 经手注入 env** |
| `internal/integrations/channel` adapter | `WeChatAdapter`（SSE） | `FeishuAdapter`（SSE+TakeCredentials） | `WorkWeChatAdapter`（无 SSE；`PollAuth` 查 health） | **`DingTalkAdapter`（无 SSE；`PollAuth` 查 health）** |
| worker check 路径 | 通用路径 | 飞书特判两阶段 | 通用路径 | **通用路径（复用企业微信那条）** |
| oc-ops 角色 | 扫码 SSE + 文件态 | `FeishuChannelOps`（扫码 SSE + status） | `WorkWechatChannelOps`（仅 status 转发） | **`DingtalkChannelOps`（仅 status 转发）** |
| 配置注入 | 文件态 | env `FEISHU_*` | env `WECOM_*` | **env `DINGTALK_*`（manager 直注）** |
| 绑定判定 | oc-ops 查 accounts 文件 | health `platforms.feishu` | health `platforms.wecom` | **health `platforms.dingtalk`** |
| 失败态精度 | — | fatal 带原因 | fatal 带原因 | **无 fatal，仅超时（精度弱一档）** |

一句话：钉钉 = **企业微信渠道（已上线）的近乎 1:1 克隆**，`s/work_wechat/dingtalk/`、`s/wecom/dingtalk/`、`s/bot_id+secret/client_id+client_secret/`，外加引擎侧两处轻改（SDK 预装 + ocops status 转发），唯一行为差异是失败态无 fatal（见第 5 节）。

## 2. 数据模型

- migration（新建 `000020_support_dingtalk_channel.{up,down}.sql`，不改 baseline / 飞书 000015 / 企业微信 000017）：
  - 放宽 `channel_bindings.channel_type` CHECK 约束：由现状 `CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat'))` 改为 `CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat', 'dingtalk'))`（DOWN 还原为不含 `dingtalk`）。
  - 唯一约束 `uk_channel_bindings_app_active (app_active_key, channel_type)` **无需再改**：飞书 000015 已含 channel_type，钉钉直接受益（同一 app 可同时绑定 wechat / feishu / work_wechat / dingtalk 各一条非 deleted 绑定）。
  - 所有表 / 字段 SQL COMMENT 齐全（项目规范）。
- 复用 `channel_bindings` 表，`channel_type='dingtalk'`。
- `metadata_json` 存渠道配置：`{"client_id": "...", "client_secret_ciphertext": "..."}`。
  - `client_secret` 用现成 `auth.Cipher`(AES-GCM) 加密，**DB 只存密文**（沿用 `apps.newapi_key_ciphertext`、飞书 `app_secret_ciphertext`、企业微信 `secret_ciphertext` 惯例）。
  - `client_id` 不敏感，明文存（详情页回显）。
- **DB 是 source of truth；k8s Secret 是派生注入物。**
- `internal/domain/enums.go`：现有 `ChannelTypeWeChat` / `ChannelTypeFeishu` / `ChannelTypeWorkWeChat`，新增 `ChannelTypeDingTalk = "dingtalk"`。

### 命名约定（全栈统一 client_id / client_secret，无层间翻译）

栈里唯一被强制的命名是引擎环境变量 `DINGTALK_CLIENT_ID` / `DINGTALK_CLIENT_SECRET`（引擎定，改不了）。其余我们控制的层一律对齐它，避免「UI 一套、后端一套」的翻译瑕疵：

- DB metadata / DTO / Secret key / 前端字段 **全部用 `client_id` / `client_secret`**，与 env 一一对应、零层间映射。
- **前端 UI 主标签也用「Client ID」「Client Secret」**——这正是钉钉开放平台「凭证与基础信息」页当前显示的主名（钉钉已把旧 AppKey/AppSecret 升级为 Client ID/Client Secret，控制台成对标注如「Client Secret（AppSecret）」）。
- i18n 文案在主标签后括注「（即 AppKey / AppSecret）」，兼容仍看到旧版控制台、或习惯旧叫法的用户，确保对着控制台能正确复制。

### 现状证据

- `internal/migrations/000017_support_work_wechat_channel.up.sql`：现状 CHECK 含 `work_wechat`；唯一约束已含 channel_type（飞书 000015 建）。
- `internal/store/queries/channel_bindings.sql`：所有查询已按 `app_id = ? AND channel_type = ? AND status <> 'deleted'` 过滤，并存查询层零改动。
- `internal/domain/enums.go`：渠道类型枚举处新增 `ChannelTypeDingTalk`。

## 3. 配置注入（per-app Secret + optional env，照搬 `workWechatOptionalEnv` 模式）

hermes `config.py` 的启用判定读 `DINGTALK_CLIENT_ID` / `DINGTALK_CLIENT_SECRET`，二者非空即启用 `Platform.DINGTALK`。

- `RenderDeployment`（`internal/integrations/k8sorch/render.go`，与 `feishuOptionalEnv` / `workWechatOptionalEnv` 并列）hermes 容器**永久**加 optional `SecretKeyRef` env，**新写 `dingtalkOptionalEnv(appID)` 仿 `workWechatOptionalEnv`**：
  - `DINGTALK_CLIENT_ID` → `app-<id>-token` Secret 的 `dingtalk-client-id` key，`optional: true`
  - `DINGTALK_CLIENT_SECRET` → 同 Secret 的 `dingtalk-client-secret` key，`optional: true`
- `RenderSecret` 渲染时**从 DB 带出已绑定钉钉配置**写入 `StringData`，保证 app 重建 / 镜像升级不丢配置。需 `AppSpec`（`internal/integrations/k8sorch/orchestrator.go`，飞书 / 企业微信字段处）**增 `DingTalkClientID` / `DingTalkClientSecret` 字段**，`buildAppSpec`（`internal/worker/handlers/app_initialize.go`）**从 channel_bindings 查 `dingtalk` 绑定、解密 client_secret 带出**（仿企业微信查询 + 解密块）。
- 语义：未绑定 → Secret 无此 key → optional env 不注入 → `getenv` 为空 → 钉钉平台不启用。Deployment template 永不因绑定而改。

### 绑定 / 解绑对 k8s 的操作（复用企业微信已验证范式）
- 绑定：写 DB → `PatchSecretKeys()` 给 `app-<id>-token` Secret 增 `dingtalk-client-id` / `dingtalk-client-secret` → `RolloutRestart` → 置 `runtime_phase=restarting`（见第 5 节）。
- 解绑：写 DB（status=unbound_by_user）→ `PatchSecretKeys()` 删这两把 key → `RolloutRestart` → 置 `restarting`。
- k8s 不会因 Secret 内容变化自动重启引用它的 pod，故必须显式 `RolloutRestart`。企业微信解绑链路（`channel_service.go` 的 `PatchSecretKeys` + `RolloutRestart` + 置 `restarting`）可直接照搬；`unbindSecretKeys()` 加 `dingtalk` 分支返回 `["dingtalk-client-id", "dingtalk-client-secret"]`。

## 4. 引擎侧改动（两个 variant，唯一的引擎触点）

引擎 `gateway/platforms/dingtalk.py` 与 `config.py` 的 `Platform.DINGTALK` 接线**已就绪、零改动**。只需补两处运维侧支撑：

- **Dockerfile（两 variant：hermes-v2026.6.5、hermes-v2026.5.16）预装钉钉 SDK**：
  ```dockerfile
  # 显式预装 dingtalk platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
  RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
        "dingtalk-stream>=0.20" httpx
  ```
  与 weixin / feishu / discord 预装同因：避免线上 pod 出网受限时 `lazy_deps.ensure("platform.dingtalk")` 同步阻塞 / 失败，导致渠道长时间卡在「验证连接中」。`httpx` 钉钉适配器主动调用也需要（部分 variant 可能已随其他平台装过，实现期确认幂等）。
- **`ocops/channel.py`（两 variant）新建 `DingtalkChannelOps(ChannelOps)`**：仿 `WorkWechatChannelOps`，**只覆写 `status()`**——转发 hermes `/health/detailed`、抽 `platforms.dingtalk` 的 `state` / `error_code` / `error_message`，映射为 `ChannelStatus`。末尾 `register_channel(DingtalkChannelOps())`。**不需要 `auth_stream()`**（无扫码），**不需要文件态 `unbind()`**（env 注入态，解绑由 manager 删 Secret key 完成，oc-ops 侧返回幂等成功即可，照搬企业微信）。**不改 if-chain / server.py**。

> 实现期须对两个 variant 各自确认 `gateway/platforms/dingtalk.py` 存在、env 契约（`DINGTALK_CLIENT_ID` / `DINGTALK_CLIENT_SECRET`）与 `_mark_connected` / `_mark_disconnected` 行为一致（本设计已据 v2026.6.5 / v2026.5.16 git tag 确认）。

## 5. 状态机

### 渠道绑定状态（复用现有 `ChannelStatus*` 枚举，无扫码）

```
unbound
  └─(用户提交 client_id+secret：写 DB+Secret+RolloutRestart+restarting)→ pending_auth（配置已下发，等待连接）
        └─(连通探测 platforms.dingtalk.state)
              ├─ connected → bound（在线）
              └─ 超时未 connected → failed（连接超时）
unbound_by_user（用户主动解绑）
```

### 失败态：无 fatal 分支（与飞书 / 企业微信的差异）

钉钉引擎不调 `_mark_fatal`，**无「凭证无效（带原因）」独立终态**。凭证填错、应用未启用 Stream 模式、网络不通等，统一表现为「探测超时未连上」→ `failed`。

- worker 连通探测在退避重试上限内若始终未 `connected`，置 `failed`，`last_error` 写**统一超时文案**：「连接超时，请检查 Client ID / Client Secret 是否正确、机器人是否已在钉钉开放平台启用 Stream 推送模式」。
- 不新增「凭证无效」专用错误分支（飞书 / 企业微信有，钉钉做不到）。该文案在前端失败态展示，引导用户自查最可能的两个原因。

### 实例 `restarting` 过渡态（复用飞书 / 企业微信已建）

- 枚举 `RuntimePhaseRestarting`，绑定 / 解绑都 `RolloutRestart` 重建 pod 让 hermes 装 / 卸凭证，重启窗口期间 oc-ops 不可达，置 `runtime_phase=restarting` 避免裸奔 502。
- 发起守卫 `AppCanInitiateChannelAuth(status, runtime_phase)`（双轴：status allowlist + `runtime_phase==ready`）拦截 `restarting` 期发起——钉钉绑定 / 解绑发起前同样受此守卫保护。
- 收敛：reconciler 按 pod Ready 把 `runtime_phase` 收敛回 `ready`。

## 6. 绑定流程（端到端，纯手填 + 同步注入 + 通用 check）

凭证随 HTTP body 同步到达，故注入在 service 发起时同步完成，worker 只负责「连通状态检查」走通用 check 路径——与企业微信完全一致。

1. 前端表单提交 → `POST /api/v1/apps/:appId/channels/dingtalk/auth`，请求体携带 `client_id` / `client_secret`（新增 DTO `DingTalkChannelAuthRequest`，放 `internal/api/handlers/dto.go` 导出大写命名；字段名全栈统一，不沿用企业微信 DTO 的 `secret` 简写）。
   - handler（`internal/api/handlers/channels.go`）按 `channel_type` 分流：`dingtalk` 路由到 `BeginDingTalkAuth`（仿企业微信分流）。
2. **新增 `ChannelService.BeginDingTalkAuth`**（`internal/service/channel_service.go`，克隆 `BeginWorkWechatAuth`）：
   - 权限校验 `CanManageApp`（`internal/auth/authorizer.go`，不在 service 内联角色判断）。
   - 实例状态守卫 `AppCanInitiateChannelAuth`（拦截 `restarting` 等）。
   - `registry.Lookup(dingtalk)` 渠道可用性守卫（要求 adapter 已注册）。
   - bound 短路（已绑定再次发起直接返回 bound）。
   - 用 `auth.Cipher` 加密 `client_secret`。
   - create-on-demand：`UpsertChannelBindingUnbound` → `SetDingtalkCredentials`（**新增 sqlc query**，仿 `SetWorkwechatCredentials`，metadata 写 `client_id` / `client_secret_ciphertext`，status=pending_auth）。
   - **同步注入**：`PatchSecretKeys` 给 `app-<id>-token` Secret 写 `dingtalk-client-id` / `dingtalk-client-secret` → `RolloutRestart` → `SetAppRuntimePhase(restarting)`（复用企业微信已注入的 patcher + restarter 依赖）。
   - 入队 `channel_check_binding` job（不入 `channel_start_login`——无扫码挑战可发起）。
   - **无即时校验**（钉钉无可用 probe，统一靠下游 health 探测）。
3. worker `ChannelCheckBindingHandler`：`dingtalk` **走通用（非飞书）分支**——`adapter.PollAuth` 查 oc-ops health：
   - **`DingTalkAdapter.PollAuth`** 经 endpoint resolver 解析 app → oc-ops 坐标，调 `ChannelStatus(ctx, ep, "dingtalk")` 读 `platforms.dingtalk.state`，映射 `connected`→`AuthStatusBound`、其余（`disconnected` / 空 / connecting）→`AuthStatusPending`（**无 fatal→failed 映射**，因引擎不报 fatal）。
   - 通用分支据此：`Bound`→`finalizeChannelBound`（写 bound + `binding_waiting`→`running` + 审计 `channel_bound:succeeded`）；`Pending`→按退避 re-enqueue 继续等连接；**退避重试达上限仍未 Bound → 置 `failed` + 统一超时文案**（见第 5 节）。
   - `finalizeChannelBound` 对钉钉**无需在此重启**（绑定发起时已 `RolloutRestart`），保持现状。
4. 前端轮询进度（`GET /api/v1/apps/:appId/channels/:channelType/auth` 的通用 `PollAuth`，读 DB binding，渠道无关）展示「验证中 → 已连接 / 失败（超时文案）」；`restarting` 窗口由 `instanceReady` 闸门提示。

### 路由

```
POST   /api/v1/apps/:appId/channels/dingtalk/auth     → BeginDingTalkAuth（手填提交配置 + 同步注入）
GET    /api/v1/apps/:appId/channels/dingtalk/auth     → PollAuth（连通态进度，读 DB）
POST   /api/v1/apps/:appId/channels/dingtalk/unbind   → Unbind
```

## 7. 连通性验证（已查证可达，复用企业微信 health 探测）

- hermes api_server `GET /health/detailed` 返回 `{"platforms": runtime.get("platforms", {})}`，含 `platforms.dingtalk` 连接状态。
- 平台基类 `gateway/platforms/base.py` 统一写状态：`_mark_connected()`→`platform_state="connected"`；`_mark_disconnected()`→`"disconnected"`。钉钉**不写 fatal**（见第 1、5 节）。
- manager 可区分「在线（connected）/ 未连上（disconnected / 空）」，但**不能精确报凭证错误原因**。
- manager 侧 oc-ops 客户端（`internal/integrations/ocops/`）+ service 接口（`internal/service/ocops.go` 的 `channelOps`）：`ChannelStatus(ctx, ep, "dingtalk")` 复用已按 channel 参数化的 `ChannelStatus` 方法（零新增）。
- **`DingTalkAdapter` 持有 endpoint resolver + oc-ops `ChannelStatus` 客户端**（构造期注入，结构仿企业微信 `WorkWeChatAdapter`），其 `PollAuth` 即「解析坐标 → 查 health → 映射 AuthStatus」。`BeginAuth` 对钉钉不参与流程（无 `channel_start_login`），实现为返回错误 / 空挑战的占位即可；`Type()` 返回 `dingtalk` 供 registry 路由。`cmd/server` 注册进 Registry（仿企业微信）。

## 8. 解绑流程

`POST /api/v1/apps/:appId/channels/dingtalk/unbind` → service 写 DB（status=unbound_by_user）+ `PatchSecretKeys` 删 `dingtalk-client-id` / `dingtalk-client-secret` key → `RolloutRestart` → 置 `restarting`。无需 oc-ops 文件态 unbind（那是微信 accounts 删除路径）。直接照搬企业微信解绑链路。

> 备注：解绑只停本侧长连接、清本侧凭证，不调钉钉删除应用 API（钉钉侧应用仍存在），符合最小动作原则。

## 9. 前端（复用企业微信已就位的通用件）

- `web/src/pages/apps/AppChannelsTab.vue`：把 `dingtalk` 的 `supported` 由 `false` 改 `true`（现为灰显占位）。
- 渠道交互按 `channel_type` 分流；钉钉走**新表单组件**（无扫码、无模式选择）：**Client ID**（即 AppKey）+ **Client Secret**（即 AppSecret，password 输入）+ 提交按钮。字段名直接 `client_id` / `client_secret`，与后端 / env 全栈一致。
- **复用企业微信通用 hook**：`useChannelProgressQuery()`（4s 轮询）、`useUnbindChannel()`；新增 `useBeginDingTalkAuth`（仿 `useBeginWorkWechatAuth`，带配置 body）。
- 状态展示复用企业微信逻辑（调整 i18n key）：验证中 / 已连接（在线）/ 失败（展示**统一超时文案**，引导自查 Client ID / Client Secret 与 Stream 模式）；`restarting` 期间「发起 / 解绑」按钮按 `instanceReady` 闸门 disabled 并提示「实例重启中，稍后再试」。
- 操作按钮位置遵循现行约定（统一在标题右上）。
- 已绑定详情：`client_id`（Client ID）展示 + Client Secret 脱敏（不回显明文）+ 重新配置 + 解绑。
- 轻量内联指引（与企业微信一致风格）：如何在钉钉开放平台创建企业内部应用 → 启用机器人 Stream 推送模式 → 复制 AppKey + AppSecret。倾向精简内联而非重型折叠块。
- `web/src/pages/apps/ChannelLogo.vue`：新增钉钉 logo / 图标。
- i18n 中英补全（三件套）：`web/src/i18n/locales/{zh,en}/apps/root.ts`，补 `channelDingTalk` 表单 / 状态 / 超时文案 / 指引。
- OpenAPI 同步：新增 / 改动 handler 后跑 `make openapi-gen` + `make web-types-gen`，连同代码提交（不手改 `openapi.yaml` / `generated.ts`）。

## 10. 渠道并存（已由飞书 / 企业微信落地，零新增基础设施）

支持同一实例同时绑定微信、飞书、企业微信、钉钉：

- schema：唯一约束 `(app_active_key, channel_type)` 飞书 000015 已建；钉钉仅需放宽 CHECK 约束（第 2 节）。
- 查询：现有 sqlc 已按 `(app_id, channel_type)` 过滤，零改动。
- 引擎：`config.platforms` 为 dict，weixin（文件态）、feishu / wecom / dingtalk（env 态）可同时 enabled，互不干扰。
- 前端：`AppChannelsTab` 本就是渠道列表、每渠道独立绑定 / 解绑，天然支持。

## 11. 测试

- service 单测：配置加密写入（`SetDingtalkCredentials`）、状态流转（pending_auth→bound、pending_auth→超时 failed）、权限校验（platform_admin / org_admin / org_member 三角色）、`restarting` 守卫拦截、bound 短路、并存（同 app wechat+feishu+work_wechat+dingtalk 各一条）。每条测试 / 子测试 / 表驱动用例配中文场景注释（项目规范）。
- worker 单测：连通探测 `connected`→bound、退避未连上达上限→failed（超时文案）；**无 fatal 路径**（区别于企业微信用例）。
- 加密往返：`client_secret` 密文存取（`auth.Cipher`）。
- render 单测：`dingtalkOptionalEnv` 注入两条 optional env、`RenderSecret` 按字段非空填 key、`buildAppSpec` 从绑定解密带出（仿企业微信 render / orchestrator 现有单测）。
- **浏览器端到端**（CLAUDE.md 硬性要求，用户提供真实钉钉 AppKey/AppSecret）：三角色分别真实填凭证 → 验证连通在线 → 故意填错 secret 验证「超时失败 + 统一文案」路径 → 解绑（验 `restarting`→`running` 收敛）→ 与微信 / 飞书 / 企业微信并存。

## 12. 不做（YAGNI）

- 不暴露 `DINGTALK_REQUIRE_MENTION` / `DINGTALK_ALLOWED_USERS` / `DINGTALK_ALLOWED_CHATS` / `DINGTALK_MENTION_PATTERNS` / `DINGTALK_FREE_RESPONSE_CHATS` 等引擎高级会话策略（用引擎默认）。
- 不做 webhook 回调模式（Stream 长连接免公网，无需回调）。
- 不做「凭证无效」精确报错（引擎不上报 fatal，统一归超时）。
- 不做即时凭证预校验（钉钉无可用 probe）。
- 不调钉钉删除应用 API（解绑只清本侧）。
- 不做多机器人 / 多账号。
- 不升级引擎版本（v2026.6.5 / v2026.5.16 已自带钉钉适配器）。

## 关键文件索引

| 层 | 文件 | 说明 |
|---|---|---|
| migration | `internal/migrations/000020_support_dingtalk_channel.{up,down}.sql`（新建） | CHECK 约束加 `dingtalk`；唯一约束已含 channel_type，无需再改 |
| 枚举 | `internal/domain/enums.go` | 加 `ChannelTypeDingTalk = "dingtalk"` |
| adapter | `internal/integrations/channel/dingtalk.go`（新） | `DingTalkAdapter`：`Type/BeginAuth(占位)/PollAuth(查 health)`；持 resolver+ChannelStatus 客户端；`cmd/server` 注册进 Registry（仿企业微信） |
| 查询 | `internal/store/queries/channel_bindings.sql` | 已按 channel_type 过滤；新增 `SetDingtalkCredentials`（仿 `SetWorkwechatCredentials`） |
| 注入-Secret/env | `internal/integrations/k8sorch/render.go` | `RenderSecret` 填 dingtalk key；`dingtalkOptionalEnv` 仿 `workWechatOptionalEnv` |
| 编排-AppSpec | `internal/integrations/k8sorch/orchestrator.go` | 加 `DingTalkClientID` / `DingTalkClientSecret` 字段 |
| 重启 | `internal/integrations/k8sorch/adapter.go` | `RolloutRestart`（已用） |
| 初始化 | `internal/worker/handlers/app_initialize.go` | `buildAppSpec` 查 `dingtalk` 绑定 + 解密带出（仿企业微信） |
| 状态机 | `internal/domain/enums.go`、`app_state_machine.go` | `restarting` 过渡态 + 双轴发起守卫（已建，复用） |
| service | `internal/service/channel_service.go` | 新增 `BeginDingTalkAuth`（克隆 `BeginWorkWechatAuth`）；解绑复用企业微信链路 + `unbindSecretKeys` 加 dingtalk 分支 |
| worker | `internal/worker/handlers/channel_login.go` | 复用通用 check 分支（adapter.PollAuth 判 bound），不加 dingtalk 特判；不入 `channel_start_login` |
| handler/DTO | `internal/api/handlers/channels.go`、`dto.go` | `DingTalkChannelAuthRequest{client_id, client_secret}` + channel_type 分流 |
| oc-ops 客户端 | `internal/integrations/ocops/`、`service/ocops.go` | 连通状态查询（已参数化，复用） |
| 前端 | `web/src/pages/apps/AppChannelsTab.vue`、`api/hooks/useChannel.ts` | `supported:true` + Client ID/Client Secret 表单（字段 `client_id`/`client_secret`）+ `useBeginDingTalkAuth` + 复用进度/解绑 hook |
| 前端 logo | `web/src/pages/apps/ChannelLogo.vue` | 钉钉图标 |
| i18n | `web/src/i18n/locales/{zh,en}/apps/root.ts` | `channelDingTalk` 表单/状态/超时/指引文案 |
| 引擎（只读参考，零改动） | 容器内 `gateway/platforms/dingtalk.py`、`config.py`、`base.py`、`api_server.py` | dingtalk 适配器 / env / health（v2026.6.5 / v2026.5.16 已自带） |
| 引擎（两 variant 改） | `runtime/hermes/hermes-v2026.6.5/Dockerfile`、`hermes-v2026.5.16/Dockerfile` | 预装 `dingtalk-stream>=0.20` + httpx |
| oc-ops（两 variant 改） | `runtime/hermes/hermes-v2026.6.5/ocops/channel.py`、`hermes-v2026.5.16/ocops/channel.py` | 新建 `DingtalkChannelOps(ChannelOps)` 只覆写 `status()` + `register_channel()`；不改 if-chain |
