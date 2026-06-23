# 国际化改动 · 全面测试验证设计

> 日期：2026-06-23 · 状态：已评审待执行
> 范围：本次国际化（i18n）改动的端到端测试验证，覆盖三个国际化面 × 中英双语 × 三角色。

## 1. 背景

本次国际化改动涉及 261 个文件（约 14000 行），分为三个相互独立的国际化面：

1. **Manager 后台 UI 中英切换（前端）**
   - `web/src/stores/locale.ts`：locale store，集中管理解析优先级（localStorage → 平台默认 → 兜底 `en`）、应用到 vue-i18n、持久化。
   - `LocaleSwitcher` 组件 + vue-i18n 单例（`web/src/i18n/index.ts`，`legacy:false`，默认 / 兜底均为 `en`）。
   - `web/src/i18n/locales/{en,zh}` 两套 locale 文件树（按命名空间：common/login/audit/platform/org/apps/knowledge/skills/usage/dashboard/layout/components/domain/tickets/locale）。
   - 平台默认语言由后端 `/api/v1/config` 下发（`i18n.default_locale`，缺省 `en`，见 `internal/config/loader.go:108`）。

2. **用户语言偏好持久化（后端）**
   - 迁移 `000013_users_locale`：`users.locale VARCHAR(10) NULL`，CHECK 约束 `IN ('en','zh')`，NULL=未选择回退平台默认。
   - `PATCH /api/v1/auth/me/locale` 端点；登录后 `applyFromUser` 用 DB 值覆盖前端语言。

3. **App 语言 → hermes 渲染（后端 + hermes）**
   - 迁移 `000014_apps_locale`：`apps.locale VARCHAR(10) NULL`，CHECK 约束同上；创建实例时快照 owner 的 locale。
   - `PATCH /api/v1/apps/:appId/locale` 端点；manifest 新增 `app.language` 字段（`internal/integrations/hermes/`）。
   - hermes runtime renderer 读取 manifest `app.language`，bot 用对应语言对终端用户说话（语言不再硬编码）。

## 2. 验证目标

证明该改动在「三面 × 中英双语 × 三角色」下：文案完整、切换正确、持久化可靠、hermes 端到端语言正确；并将关键校验**沉淀为入库自动化测试以防回归**。

角色三层（与 `internal/auth/authorizer.go` 一致）：`platform_admin` / `org_admin` / `org_member`。

## 3. 五层验证策略

下层兜底上层抽样不到的角落；从机器自动化到人工浏览器逐级收敛。

| 层 | 内容 | 形态 | 归宿 |
|---|---|---|---|
| **L1 翻译完整性** | zh/en 树形对齐、空值、占位符、嵌套结构、复数结构（详见 §4） | 净新增 vitest 单测，遍历 locale 树 | **入库** |
| **L2 单测/集成** | 后端 `service`/`handler`（locale 业务、PATCH 端点、manifest Language、快照 owner locale）+ 前端已有 `*.spec.ts` | `make test` + `npm run test` 全绿 | 已有，补缺口 |
| **L3 E2E** | Playwright 主链路：登录页切换→localStorage、登录后切换→后端持久化、跨会话保持、app locale 设置链路 | 扩展 `web/tests/e2e/locale.spec.ts` | **入库** |
| **L4 浏览器逐页走查** | 三角色 × 中英 × 全部可见页面逐页截图 | 真实浏览器 + 截图证据矩阵 | 验证报告 |
| **L5 hermes 端到端** | app.locale=zh/en → 部署 → 实际对话验证语言 | 本地 k3d + 浏览器对话 | 验证报告 |

**环境**：本地 k3d（`make local-up`）+ `npm run dev`。默认语言 `en`，需额外验证把 `i18n.default_locale` 配为 `zh` 时，登录页 / 未登录用户的回退。

**关键约束**：L4 一律真实浏览器（curl 不能替代前端验证）；逐页矩阵带截图证据；三角色账号本地齐备（platform_admin = `admin`/组织标识留空、org_admin、org_member）。

## 4. L1 翻译完整性校验维度（入库 vitest 单测）

遍历 `web/src/i18n/locales/{en,zh}` 两棵 message 树，逐项断言：

1. **双向 key 对齐**：en 有而 zh 缺、zh 有而 en 缺，均报错（列出完整 key 路径）。
2. **空值检测**：任一侧叶子为空字符串 / 空白即报错。
3. **命名插值占位符集合一致**：同一 key，两侧 `{name}` 形式占位符的名字集合必须完全相等（vue-i18n 命名插值，现状大量使用 `{count}`/`{n}`/`{name}`）。
4. **en 不残留中文**：en 叶子值匹配到中日韩统一表意文字即报错（防漏译）；对应 zh 叶子需非空。
5. **嵌套结构一致**：同一 key 路径两侧节点类型一致——不能一边是叶子字符串、一边是子对象；无单侧多 / 缺子树。
6. **复数结构一致**：
   - 若 en 用 vue-i18n 管道复数 `a | b`，则 zh 同 key 分支数（`|` 分隔段数）必须相等。
   - 若出现标准 ICU `{x, plural, ...}` / `{x, select, ...}`，则两侧分支类别（one/other/zero 等）一致。
   - 现状未使用复数语法，本检测属防御性：将来引入即自动生效。

> 注意：现有 `*.spec.ts` 中提及「漏译 / 缺失」字样者均为业务页面测试，并无专门 key 对齐测试——L1 为净新增。

## 5. L4 逐页走查矩阵（角色 × 语言 × 页面）

页面清单按 `web/src/app/router.ts` 路由门控枚举；每页走 `en` + `zh` 两遍，逐页截图。

### 5.1 页面清单

**全角色可见（org_member / org_admin / platform_admin 都走）**
- 登录页（未登录，含默认语言回退验证）
- 首页 `RoleAwareHome`（`/`）、知识库 `knowledge`、技能 `skills`、技能工单详情 `skill-tickets/:id`、用量 `usage`
- Apps 列表 `apps`、空态 `apps/empty`
- App 详情 8 个 tab：`overview` / `kanban` / `cron` / `channels` / `knowledge` / `skills` / `workspace` / `audit`

**org_admin 以上（+platform_admin）**：成员 `members`、审计日志 `audit-logs`

**org_admin 独有**：企业控制台 `org-console`、新建成员 `members/new`、余额 `balance`

**platform_admin 独有**：平台控制台 `console`、组织 `organizations`、助手版本 `assistant-versions`、行业知识库 `platform/industry-knowledge`、平台技能 `platform/skills`、定制技能工单 `platform/custom-skills`、充值 `platform/organizations/:orgId/recharge`、权限 `platform/permissions`、App `runtime` tab（PLATFORM_ONLY）

> 每个角色只走其门控可见页面。粗算独立 (角色, 页面) 组合 ≈ 50 项 × 2 语言 ≈ **100 个截图检查点**。

### 5.2 每页检查项

1. 无未翻译残留：en 页无中文裸串、zh 页无英文裸串。
2. 无 i18n key 裸露（如 `common.save`、`apps.xxx` 直接显示在界面）。
3. 切换语言即时生效、无刷新空窗。
4. 布局无溢出 / 截断 / 错位（长英文文案易撑破按中文设计的窗口）。
5. 动态文案同步切换：toast、表单校验提示、空态、分页「共 N 条」等。

## 6. L5 hermes 端到端

1. 本地建 2 个实例。
2. 分别 `PATCH /api/v1/apps/:appId/locale` 设 `zh` / `en`。
3. 部署，浏览器实际对话。
4. 验证：bot 回应语言正确；manifest `app.language` 正确；「创建实例时快照 owner locale」生效。

## 7. 交付物

1. **入库代码**：L1 vitest 单测、L3 扩展 E2E 用例。
2. **验证报告**（`docs/` 下）：
   - L4 逐页矩阵表（角色 × 语言 × 页面 × 结论 × 截图链接）。
   - L1 / L2 / L3 运行结果。
   - L5 对话证据。
   - 发现的 bug 及修复回归记录。
3. 发现问题 → 先修再复验，直到全绿才交付。

## 8. 执行顺序

L1（最快暴露最多问题）→ L2 → L3 → 起本地 k3d + dev → L4 逐页 → L5 hermes → 汇总报告。
