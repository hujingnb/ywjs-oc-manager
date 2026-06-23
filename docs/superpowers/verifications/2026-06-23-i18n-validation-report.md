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

### 已知限制（超出本次前端国际化范围）
- **后端 apierror 错误文案为中文**：`无权访问该应用`、`命令执行失败`、`无权操作该工单` 等在
  en 界面经 `Load failed: …` 透传显示中文。属后端错误未 i18n，是独立课题，建议后续单独立项。
  （本次清扫平台管理员访问 e2e 组织实例触发的 403 即此类。）

## L5 hermes 端到端
_(待执行)_

## 执行记录
- L3 e2e 最终运行结果：**7 passed**（登录页切换×3、登录后持久化×2 含 org_member、app locale 端点链路×1）。
- L4 清扫：3 角色 × 双语全页通过，0 key 裸露。
