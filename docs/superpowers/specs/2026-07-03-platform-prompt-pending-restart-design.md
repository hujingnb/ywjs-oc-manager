# 概览提示「平台提示词已更新，需重启生效」（设计）

- 日期：2026-07-03
- 状态：待实现
- 范围：实例概览页新增一个「平台 prompt 变更后需重启」提示，沿用既有 `applied_*` 快照模式

## 1. 背景与问题

平台层身份 prompt 已从配置迁移为代码常量 `config.DefaultSystemPromptTemplate`
（见 `2026-07-03-agent-brand-identity-aigowork-design.md` 的实现修订）。该常量只在
manager 重新部署时变化，而存量实例的 SOUL.md 平台层是**上次 bootstrap/restart 时**
写死在 input 里的——常量变了，运行中的实例并不会自动更新，必须走一次 manager 重启
重渲染才能生效。

现有的「需重启」提示只覆盖三件事，**都不含平台 prompt**：

| 信号 | 快照列 | 期望来源 | 前端 |
|---|---|---|---|
| `version_synced` | `applied_version_revision` / `applied_image_ref` | 绑定版本 revision/镜像 | AppDTO 直读，tag+按钮 |
| `web_publish_pending_restart` | `web_publish_applied` | 企业 web-publish 开通态 | AppDTO 直读，n-alert 横幅 |
| 语言 `needs_restart` | 无（实时查 oc-ops） | `apps.locale` | 独立 `/locale-status` 接口 |

结果：改平台身份文案并重新部署后，存量实例「悄悄过期」，概览无任何提示。本设计补上
这个提示，与 `web_publish_pending_restart` 1:1 同构。

## 2. 为何用 DB 快照（而非实时查实例）

语言那套要求「实时查实例侧、不读 DB 快照」，是因为语言能在 UI 里切、`apps.locale`
会在**不重启**的情况下变，DB 与运行态会漂。**平台 prompt 没有这种 live-propagation
路径**：它是代码常量，实例只能通过「走 manager 重启（AppInputRefresher 重渲染
platform-rules.md）」来更新它。因此「上次写 input 时的常量 hash」就等于「当前实例运行
的平台 prompt」，DB 快照对本场景是精确的，无需实时查实例、无需改引擎/oc-ops。

## 3. 核心机制

- 新增列 `apps.applied_platform_prompt_hash CHAR(64) NOT NULL DEFAULT ''`：
  记录**上次写 input 时**所用平台 prompt 文本的 sha256（hex）。
- 新增 `config.PlatformPromptHash() string` = `sha256hex(DefaultSystemPromptTemplate)`，
  作为「当前期望 hash」的单一来源，供 stamp 与 compute 共用。
- 判定：`platform_prompt_pending_restart = (applied_platform_prompt_hash != PlatformPromptHash())`。
- 存量实例默认 `''` ≠ 当前 hash → 部署后立即提示需重启（正确：它们确实尚未获得新身份）。

hash 的对象是常量本身而非渲染后文本：当前平台 prompt 无 `{var}` 占位符
（`RenderRuleText` 为恒等），二者相等，hash 常量最简单稳定。若将来引入占位符，需改为
hash 渲染后文本，见 §7 非目标。

## 4. 写入点（stamp）

平台 prompt 只在「写 platform-rules.md 到实例 input」时进入实例，故在这两处
stamp `hash(cfg.PlatformPrompt)`，与 `applied_version_revision` 的双写点一致：

1. **bootstrap**：`internal/service/bootstrap_service.go` 的 `Build`，紧挨现有
   `SetAppWebPublishApplied` 调用处写入。
2. **restart 刷新**：restart 走的 `RefreshAppInput` 实现路径（实测 restart 会重渲染
   平台层，故必须同 stamp，否则重启后仍误报 pending）。

新增 sqlc 查询 `SetAppAppliedPlatformPromptHash(app_id, hash)`，仿现有
`SetAppWebPublishApplied`。

## 5. 暴露与前端

- **service**：`internal/service/app_service.go` 的 `GetAppWithVersion`（单查）里计算
  `PlatformPromptPendingRestart`，与 `WebPublishPendingRestart` 同位置、同风格。
  **仅详情/概览计算，不进批量列表** `ListAppsByOrgWithVersion`——与 web_publish 一致，
  也匹配「概览」诉求。
- **DTO / OpenAPI**：`AppDTO` 增字段 `platform_prompt_pending_restart bool`
  （`json:"platform_prompt_pending_restart"`），handler `apps.go` 透传。改动后跑
  `make openapi-gen` + `make web-types-gen` 重生成 `web/src/api/generated.ts`。
- **前端** `web/src/pages/apps/AppOverviewTab.vue`：新增计算属性
  `platformPromptNeedsRestart`（读 `app.platform_prompt_pending_restart === true`），
  渲染 warning 徽标 + 「立即重启」按钮，复用 `useTriggerRuntimeOperation('restart')`；
  按钮沿用 `version_synced` 那套 `canRestart` 门槛（运行中才可点）。
- **i18n**：`web/src/i18n/locales/{zh,en}/apps/root.ts` 增 `apps.overview.prompt.*`
  文案（needsRestart 标签 + restart 按钮）。

## 6. 测试

- 后端 `config`：`PlatformPromptHash()` 稳定性单测（同输入同输出、非空、与常量绑定）。
- 后端 `app_service`：compute 单测——`applied_platform_prompt_hash` 等于/不等于当前
  hash 两条路径，断言 `PlatformPromptPendingRestart` 取值；覆盖空 hash（存量实例）判为
  pending。
- 后端 stamp：bootstrap 与 restart 路径写入的 hash 等于 `PlatformPromptHash()`
  （在既有 bootstrap_service / app_runtime_ops 测试里补断言）。
- 前端 `AppOverviewTab.spec.ts`：`platform_prompt_pending_restart` 为 true 展示徽标、
  为 false/缺省不展示；运行中展示重启按钮（仿现有 `version_synced` 用例）。
- migration：`internal/migrations/migrations_test.go` 若有 up/down 往返校验则自动覆盖
  新 000027。

## 7. 非目标 / 边界

- 不做 AppsPage 列表徽标——仅概览，和 web_publish 对齐；将来需要再加（届时补
  `ListAppsByOrgWithVersion` 批量计算）。
- hash 只覆盖常量本身；平台 prompt 引入 `{var}` 占位符时需改为 hash 渲染后文本。
- 不改语言 `/locale-status` 那套，两提示独立并存。
- 不新增引擎/oc-ops 改动，不重建镜像。

## 8. 生效与回滚

- 生效：随本改动部署 manager；概览即按 hash 比对显示提示；用户点「立即重启」触发
  现有 `runtime/restart`，重启后 stamp 更新、提示消失。
- 回滚：migration down 删列；还原 Go/前端改动。无破坏性数据变更。

## 9. 验证方案

真实浏览器验证：
1. 起一个存量实例，概览应显示「平台提示词已更新，需重启生效」徽标 + 重启按钮。
2. 点「立即重启」，等实例重启完成，概览提示消失（stamp 已更新为当前 hash）。
3. exec 进实例确认 SOUL.md 平台层为最新常量内容。
4. 三角色（平台管理员 / 组织管理员 / 组织成员）走查。
