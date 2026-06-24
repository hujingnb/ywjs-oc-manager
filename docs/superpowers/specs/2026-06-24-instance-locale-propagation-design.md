# Spec C：实例 locale 赋值 + 传播 + 实时展示

- 日期：2026-06-24
- 状态：设计已评审，待实现
- 所属拆分：国际化整改三件套之 **C**（A=hermes 运行时文案 / B=后端错误 i18n / C=本篇）

## 背景

实例语言（`apps.locale`，即 hermes 对终端用户说话的语言、注入 `display.language`）目前在创建时一次性快照，之后与所属成员的界面语言脱节：

- `OnboardMember`（创建新成员同时建实例）：新成员 `users.locale` **留空**，新实例 `apps.locale` 直接用平台默认 `s.defaultLocale`，**不随创建者**。
- `CreateAppForMember`（为已有成员重建实例）：已优先用成员 `user.Locale`，缺省回落平台默认（保持）。
- `AuthService.UpdateLocale`（用户改界面语言，PATCH `/api/v1/auth/me/locale`）：**只改 `users.locale`，不传播**到名下实例。
- 实例详情页（`AppOverviewTab.vue`）**无语言字段**；且即便加，也不应读 `apps.locale` 快照——用户铁律：**实例当前使用的语言必须实时从实例接口获取**，防 DB 快照与实例真实运行态漂移（同「余额实时查 new-api」哲学）。
- oc-ops sidecar（`runtime/hermes/.../ocops/server.py`）**无** `/oc/config` 这类返回当前运行配置的端点。

## 目标

实例语言跟随其所属成员的界面语言；实例详情页**实时**展示实例真正在用的语言；语言变更以**非破坏（不自动重启）+ 手动重启生效**方式传播。

## 关键决策（brainstorm 已定）

1. **新成员 locale = 创建者 locale**：`OnboardMember` 读操作 principal（创建者）的 `users.locale`（DB 读，缺省回落平台 default），设为新成员 `users.locale`；新实例 `apps.locale` = 该新成员 locale。
2. **UI 语言变更传播**：`UpdateLocale` 改 `users.locale` 后，把**仅该用户自己拥有**的实例 `apps.locale` 更新为新值，**不自动重启**（不打断运行中实例）。
3. **详情页"当前语言"实时查实例**：从 oc-ops 实时取，不读 DB 快照。实例停止/不可达 → 显示"实例未运行/未知"。
4. **"需重启生效"提示**：当 live 当前语言 ≠ 期望（`apps.locale`）且实例运行中 → 详情页显示提示 + 「重启」按钮，触发现有 `app_restart_container`，重启后 bootstrap 按新 `apps.locale` 重渲染 `config.yaml`，live 收敛到期望。

> 注：`current`（实际语言）严格来自 oc-ops；`desired`（期望/配置语言）合法来自 `apps.locale`（它本就是配置值，非"当前实际")。铁律针对的是"当前实际"展示不得用 DB，二者区分清晰。

## 现状基线（已核对）

- `internal/service/onboarding_service.go`：`OnboardMember`（~L105 入参 `principal auth.Principal`；~L155-177 建 user 不设 locale、建 app 用 `s.defaultLocale`）；`CreateAppForMember`（~L349-363 用成员 `user.Locale` 或回落默认）。`s.defaultLocale` 来自 `cmd/server/main.go:143` 注入的 `cfg.I18n.DefaultLocale`。
- `internal/auth/token.go`：`Principal{UserID, OrgID, Role}` **不含 locale** → 取创建者 locale 需 `store.GetUser(ctx, principal.UserID)`。
- `internal/service/auth_service.go:348-360`：`UpdateLocale` 仅 `store.UpdateUserLocale`，无传播。
- `internal/service/app_service.go:304-381`：`UpdateAppLocale` 改 `apps.locale` + 入队 `domain.JobTypeAppRestartContainer`（本特性传播路径**不**复用其重启部分，只用「改列」）。
- `internal/store`：有 `UpdateAppLocale`（仅改列）、`UpdateUserLocale`；需新增「按 owner 列出 app id」查询（若无）。
- oc-ops（`ocops/server.py`）：有 `/oc/info`、`/oc/doctor`、`/oc/channels/*`、`/oc/cron/*`、`/oc/kanban/*`、`/oc/skills/*`、`/oc/conversations/*`，**无 `/oc/config`**。`config.yaml` 在 `/opt/data/config.yaml`，oc-ops 可读。
- `internal/integrations/ocops/client.go`：基址 `http://app-<id>-ocops.<ns>.svc:8080`，per-app Bearer token；`client_*.go` 各业务方法（Info/Doctor 等）。
- `web/src/pages/apps/AppOverviewTab.vue`：展示 Status/APIKey/Version/Org/Desc/RuntimeImage，**无语言字段**；数据来自 `/api/v1/apps/:appId` 的 AppDTO。
- `web/src/stores/locale.ts:49-54`：`setLocale(persist)` 调 PATCH `/api/v1/auth/me/locale`，无实例同步。

## 架构与组件

### (a) oc-ops `/oc/config` 端点（新增）
`runtime/hermes/hermes-v2026.6.5/ocops/server.py`：新增 `GET /oc/config`，读 `/opt/data/config.yaml` 的 `display.language`，返回 `{"display_language": "<zh|en>"}`（解析失败/缺字段返回合理默认或 404，由 client 侧归一）。我们自有代码、**无需构建期补丁**；契约文件（ocops-contract）若有则同步；新 variant 需带走（同其它 ocops 端点）。oc-ops 测试（`runtime/hermes/.../tests/`）覆盖：给定 config.yaml 返回正确 language。

### (b) manager ocops client 方法（新增）
`internal/integrations/ocops/client_*.go`：新增 `Config(ctx, appID) (OcConfig, error)`，`DoJSON` 调 `/oc/config`，复用寻址 + token。返回结构含 `DisplayLanguage string`。

### (c) manager API：实例语言状态端点（新增）
`GET /api/v1/apps/:appId/locale-status` → service `AppLocaleStatus(ctx, principal, appID)`：
- `current_language`：调 ocops `Config` 实时取；实例未运行/不可达/超时 → `null`（短超时，避免详情页卡顿）。
- `desired_language`：`apps.locale`。
- `needs_restart`：`current_language != nil && current_language != desired_language`。
- 响应 DTO（`internal/api/handlers/dto.go` 导出大写）：`{current_language *string, desired_language string, needs_restart bool}`。
- 权限：沿用现有 app 访问鉴权（owner / org_admin / platform_admin）。
- 错误文案走 Spec B 的 msgKey（apierror.JSON）。

### (d) onboarding locale 赋值（改造）
`OnboardMember`：在建 user 前 `creator, _ := store.GetUser(ctx, principal.UserID)`；`memberLocale := creator.Locale`（缺省/空 → `s.defaultLocale`）；建 user 时 `locale = memberLocale`；建 app 时 `apps.locale = memberLocale`。`CreateAppForMember` 保持（已随成员 locale）。

### (e) UI 语言变更传播（改造）
`AuthService.UpdateLocale(ctx, userID, locale)`：更新 `users.locale` 成功后，`appIDs := store.ListAppIDsByOwner(ctx, userID)`，逐个 `store.UpdateAppLocale(ctx, {ID, Locale})`（**仅改列、不入队重启**）。`ListAppIDsByOwner` 若无则新增 sqlc 查询（按 owner_user_id 且 deleted_at 语义筛活跃实例）。传播失败不阻断 users.locale 更新（best-effort + 记日志）；或事务内一起改（按现有事务习惯，倾向同事务保证一致）。

### (f) 前端实例详情（改造）
`web/src/pages/apps/AppOverviewTab.vue`：加"实例语言"行，调 `/api/v1/apps/:appId/locale-status`（生成的 typed client）：
- `current_language` 有值 → 显示语言（zh→"中文" / en→"English"，走 vue-i18n label）。
- `current_language` 为 null → 显示"实例未运行"。
- `needs_restart` → 显示徽标"切换语言后需重启生效为 {desired}" + 「重启」按钮（复用现有重启交互，触发 app_restart_container；重启后刷新该状态）。

### (g) 前端 setLocale（轻量改造）
`web/src/stores/locale.ts`：保持 PATCH `/auth/me/locale`（传播在后端做）。切换成功后可加轻量 toast 提示"名下实例语言已更新，进入实例详情可重启应用"（可选，文案走 vue-i18n）。

## 数据流

UI 切语言 → PATCH `/auth/me/locale` → 后端改 `users.locale` + 名下 `apps.locale`（不重启）→ 用户进实例详情 → `/locale-status` 实时查 oc-ops：current(旧) ≠ desired(新) → 点「重启」→ `app_restart_container` → bootstrap 按新 `apps.locale` 重渲染 `config.yaml` 的 `display.language` → oc-ops live 变新 → 详情页 current=desired、提示消失。

## 测试

- **Go 单元**：`OnboardMember` 取创建者 locale 并赋值 user+app（mock store GetUser）；`UpdateLocale` 传播（mock store 验各 owned app locale 更新、**无 restart job 入队**）；`AppLocaleStatus`（ocops 可达返回 current、不可达/超时返回 null、needs_restart 计算）。
- **ocops client**：`Config` 解析 `/oc/config`。
- **oc-ops server.py**：`/oc/config` 读 config.yaml 返回 display_language（tests/ 下）。
- **前端**：AppOverviewTab 三态（运行中同语言 / 运行中需重启 / 未运行）渲染。
- **openapi**：新增端点 → `make openapi-gen` + `make web-types-gen` 同步入 git（本特性**有** API 变更）。
- **真机三角色**：新成员随创建者 locale；成员切界面语言→名下实例 apps.locale 更新；详情页实时语言 + needs_restart 提示 + 重启生效；实例未运行显示"未运行"。

## 风险与缓解

- **locale-status 实时往返**：详情页加载多一次 oc-ops 调用；实例未运行须短超时快速返回 null，避免卡顿。
- **期望与实际暂不一致**：传播不重启的设计本意，靠详情页 needs_restart 提示 + 手动重启弥合。
- **oc-ops 新端点跨 variant**：进 6.5（及按需 5.16），新 variant 复制时带走（见 [[project-hermes-i18n-zh]] 的 variant 带走清单）。
- **创建者无 locale / 非企业管理员创建**：回落平台 default；用创建者自身 locale 不区分角色。
- **传播事务一致性**：users.locale 与 owned apps.locale 尽量同事务；失败回滚或 best-effort 记日志（按现有 store 事务习惯定，实现期明确）。

## 交付物清单

- 新增 `ocops/server.py` 的 `/oc/config` + oc-ops 测试 + 契约同步（如有）。
- 新增 ocops client `Config` 方法。
- 新增 `GET /api/v1/apps/:appId/locale-status`（handler + service `AppLocaleStatus` + DTO）+ openapi/web-types 同步。
- 改造 `OnboardMember`（创建者 locale）、`UpdateLocale`（传播）；新增 `ListAppIDsByOwner` 查询。
- 改造 `AppOverviewTab.vue`（语言行 + needs_restart + 重启）；`locale.ts` 可选 toast；新增 vue-i18n 文案。
- 单元测试 + 真机三角色验证报告。
