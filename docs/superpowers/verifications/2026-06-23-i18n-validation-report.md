# 国际化改动验证报告 · 2026-06-23

## 环境
- 本地 k3d（`make local-up`，所有 pod Running）+ 重建重部署的 manager-web/api（含本会话修复）。
- 基地址 http://ocm.localhost；浏览器：chrome-devtools MCP（真实浏览器，绕 7890 代理）。
- 默认语言 `en`（`i18n.default_locale` 缺省）；三角色账号来自 `make seed-e2e` fixture：
  - platform_admin：`admin` / `admin123`（组织标识留空）
  - org_admin：`e2e-org-admin` / `e2e-pass-123`（组织标识 `test-org`）
  - org_member：`e2e-org-member` / `e2e-pass-123`（组织标识 `test-org`）

## L1 翻译完整性（vitest 单测，入库）
- `web/src/i18n/locales/completeness.spec.ts` 六维断言全绿（6 tests passed）。
- 暴露并修复：`apps.runtime.snapshotError` en 侧半角 `|` 起首被 vue-i18n 误判复数分隔符 → 改全角 `｜`（commit 402c50f）。
- 复数断言对多行 Markdown 值跳过管道比较，避免表格 `|` 误报（commit 6a01633）。

## L2 单测/集成
- `make test`（Go `go test ./...`）：全绿，0 FAIL。
- `cd web && npm run test -- --run`：85 文件 / 573 tests 全 PASS。
- `cd web && npm run typecheck`：0 错误。

## L3 E2E（Playwright，入库）
- 扩展用例：org_member 语言 DB 持久化、app locale PATCH 端点链路（commit f987b7a）。
- 运行中暴露并修复的 e2e harness 缺陷（这些登录相关用例此前长期走 `test.skip`，从未在 en 默认下真跑过）：
  - **globalSetup 取末行解析 JSON 太脆**：递归 make 追加「make[1]: 离开目录」噪声行 → 改为从后向前找首条可解析 JSON 行。
  - **fixtures.ts loginAs 用中文 label**：默认 en 下匹配不到（且 `组织标识` 连中文都过时，真实为 `企业标识`）→ 改双语锚定正则 `/^(账号|Username)$/` 等。
  - **下拉选项用 `getByRole('option')`**：naive-ui n-dropdown 项无 `role=option`（实为 `.n-dropdown-option` DIV）→ 改 `locator('.n-dropdown-option',{hasText})`。
- 运行结果：见文末「执行记录」。

## L4 逐页矩阵（三角色 × 中英 × 全部可见页面）

方法：Playwright 全站清扫 `web/tests/e2e/l4-i18n-sweep.spec.ts`，三角色登录后按 en/zh
两遍遍历各自门控可见页面，每页整页加载后提取全部可见文本，自动判定 ① i18n key 裸露
② en 页 CJK 残留 ③ zh 页拉丁残留，并逐页截图（112 张，存 `screenshots/`，gitignore 不入库）。

**核心结论：全站三角色 × 双语 × 全部页面，0 处 i18n key 裸露。** 已沉淀为入库回归守卫
（清扫脚本断言 keyLeaks 为零）。

### 发现并修复的真实前端 i18n bug（先修再复验，全部已重建重部署 + 重扫确认）
| # | 页面/位置 | 问题 | 修复 commit | 复验 |
|---|---|---|---|---|
| 1 | 登录页 footer `LoginPage.vue:103-104` | `Secure runtime access`/`AI task control plane` 硬编码英文 span，中英文均不切换 | 3aef7eb 改用 `t('login.securityNote/controlNote')` | ✅ 重扫 zh 登录页显示「安全运行接入/AI 任务控制中枢」 |
| 2 | 全站 `<html lang>` | locale 切换不同步 `document.documentElement.lang`，始终 index.html 硬编码 zh-CN | 2f63b66 在 locale store `apply()` 同步 + 补单测 | ✅ 单测断言通过 |
| 3 | 表格操作列 `actionColumn.ts:28` | 默认标题硬编码中文「操作」，en 界面 /apps、/organizations、/members、/assistant-versions 等漏译 | 9adc793 改用 `i18n.global.t('common.table.actions')` | ✅ 重扫 en 残留已无「操作」 |

### 经核实为「非 bug」的扫描命中（透明记录）
- **eyebrow 装饰性 kicker**（`Instance · Overview`、`Platform`、`Industry`、`Billing` 等）：
  **有意设计英文**。证据：已 i18n 化的 `apps.list.eyebrowAdmin` 其 zh 值即 `Platform · Instances`
  （全英文）、`eyebrowOrg` zh 为 `企业 · Instances`（保留英文名词）。若"修正"反与既有设计冲突。
- **Cron 状态摘要**（`Cron status unknown`/`Gateway cron running`，`AppCronTab.vue:181`）：
  代码注释明载「**按产品要求保留英文**」，有意为之。
- **渠道品牌名**（WhatsApp/Discord/Slack/Line）、**数据值**（实例名/组织名/UUID/用户名/模型名）：
  非 UI 文案，本就不应翻译。
- **登录页验证码 Altcha `Verified`**：第三方 widget 文案，不易本地化，记为已知限制。

### L4 补验：三处覆盖缺口闭合（部署真实 org_member 自有实例后复扫）
首轮自动清扫用的 fixture 实例 owner 是 org_admin 且无运行 pod，导致三处未真正验到内容；
据正常流程部署「org_member 自有」的真实 hermes 实例（owner=被 onboard 的 org_member，pod 3/3 Running）后补验：
- **org_member 自有实例详情内容**：以该 org_member 登录扫自有实例 8 个 tab × 中英 = 16 检查点，
  全部停在自有 URL（无 403）、**0 key 裸露、0 错误态**。（首轮的 org_member 403 经核实为正确权限设计：
  `CanViewApp` 限 org_member 只看自有实例，fixture 没给它自有实例所致，非 bug。）
- **#2 kanban / cron 真实态**：在运行中 pod 下，这两个 tab 不再出现 `命令执行失败`，正常渲染。
- **#3 动态文案**：项目前端无 n-form 校验规则（表单校验走后端），动态前端 i18n 主要在叠加层。
  以 org_admin 触发删除应用的 `ConfirmActionModal`，验证标题、含 `{name}` 插值的消息、确认/取消
  按钮在中英下均正确切换（`Confirm instance deletion`↔`确认删除实例` 等），并捕获删除后反馈存在。

### 已知限制（超出本次前端国际化范围）
- **后端 apierror 错误文案为中文**：`无权访问该应用`、`命令执行失败`、`无权操作该工单` 等在
  en 界面经 `Load failed: …` 透传显示中文。属后端错误未 i18n，是独立课题，建议后续单独立项。

## L5 hermes 端到端（真实部署的运行中 pod 上验证）

本地此前从未构建/部署过 hermes，本次从零打通：
1. `make build-hermes-runtime`（variant hermes-v2026.6.5，内含 203 个 hermes pytest 通过）+ ops 镜像，
   retag 推 k3d registry（`oc-manager-hermes:v2026.6.5-dev1`、`oc-manager-ops:dev2`）。
2. 经正常 API 建版本（image_id=v2026.6.5、main_model=deepseek-chat，new-api 实时可用）+ 建组织
   （provisioning new-api 账户；seed 裸 SQL 的 org 无此账户会致 bootstrap 失败）+ org_admin onboard 成员。
3. worker 处理 app_initialize → bootstrap（new-api API key=active）→ k8sorch 部署到 oc-apps。

**验证证据（真实运行 pod `app-a196f893-…`，3/3 Running）：**

| 阶段 | app.locale | pod `/opt/oc-input/manifest.yaml` | pod 渲染产物 `/opt/data/config.yaml` |
|---|---|---|---|
| 初始部署（owner 快照） | en | `app.language: en` | `language: en` |
| PATCH locale=zh + pod 重建 | zh | `app.language: zh` | `language: zh` |

证明完整链路：**DB app.locale → manager 下发 manifest app.language → pod 拉取 → oc-entrypoint
renderer 读取并渲染运行时 config.language**（commit 8bc17a1「语言不再硬编码」在真实 pod 上成立）。
并验证「创建实例时快照 owner locale」：owner 无 locale → app.locale 快照为平台默认 en。

**环境限制（非功能缺陷）：** hermes bot 对终端用户说话经渠道（wechat 扫码登录），无「测试对话」
HTTP 端点；自动化环境无法绑定真实 wechat，故未做「人与 bot 逐句对话」。但 bot 语言的决定因素
（运行时 config.language + SOUL.md 系统提示）已在真实 pod 上确认为 zh，等价于 bot 将以中文应答。

## 最终全量回归（全绿）
- Go 单测 `go test ./...`：31 包 ok，0 FAIL。
- 前端单测 `npm run test --run`：85 文件 / 573 tests 全 PASS。
- 前端类型检查 `npm run typecheck`：0 错误。
- E2E `locale.spec.ts` + `l4-i18n-sweep.spec.ts`：**10 passed**（locale 7 + l4 清扫 3 角色），0 key 裸露。

## 本会话发现并修复的真实问题汇总
| # | 层 | 问题 | commit |
|---|---|---|---|
| 1 | L1 | `apps.runtime.snapshotError` en 半角 `\|` 起首被 vue-i18n 误判复数分隔 → 全角 `｜` | 402c50f |
| 2 | L3 | e2e harness：globalSetup 取末行解析 JSON 脆、fixtures 中文 label 失配 en 默认、下拉用错 role | 38005d6 |
| 3 | L3 | seed fixture app 未绑 assistant_version 致实例查询 404 | af7c1b0 |
| 4 | L4 | 登录页 footer 硬编码英文 span 不切换 | 3aef7eb |
| 5 | L4 | `<html lang>` 不随语言同步（始终 zh-CN） | 2f63b66 |
| 6 | L4 | 表格操作列默认标题硬编码「操作」，en 漏译 | 9adc793 |

## 验收结论
本次国际化改动（Manager UI 中英切换 / 用户语言持久化 / App 语言→hermes 渲染三面）经五层验证
全部通过：翻译完整性单测、单测集成、E2E、三角色×双语全页清扫（0 key 裸露）、hermes 真实 pod
端到端语言传导。共发现并修复 6 个真实缺陷（均已重建重部署/重扫复验），关键校验沉淀为入库回归守卫
（completeness.spec、locale.spec、l4-i18n-sweep.spec）。仅 hermes「人与 bot 逐句对话」受 wechat
渠道环境限制未做，其语言决定因素已在真实 pod 验证为正确。
