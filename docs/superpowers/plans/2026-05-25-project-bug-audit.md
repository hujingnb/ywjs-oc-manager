# Project Bug Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 对当前 `oc-manager` 项目做一次全链路 bug 排查，产出有证据支撑的分级报告，不直接修复业务代码。

**Architecture:** 采用只读审计流水线：先记录环境和工作区基线，再跑后端、前端、契约检查，随后审计近期高风险代码路径，最后用真实浏览器验证核心流程。执行过程中只写排查报告，不修改业务代码、测试或生成产物；若某个检查会生成文件，改用临时输出路径或在确认基线干净后只记录差异。

**Tech Stack:** Go 1.25、Gin、sqlc、PostgreSQL、Redis、Vue 3、Vite、TanStack Query、Playwright、Docker Compose、swag/OpenAPI。

**Spec:** `docs/superpowers/specs/2026-05-25-project-bug-audit-design.md`

---

## Scope Check

本 spec 覆盖多个检查维度，但只有一个交付物：项目 bug 排查报告。后端、前端、契约、代码审计和浏览器验证都服务于同一个结论集合，不需要拆成多个独立计划。

## File Structure

| 文件 | 责任 | 操作 |
|---|---|---|
| `docs/reports/2026-05-25-project-bug-audit.md` | 本次排查的证据、命令结果、浏览器验证记录和最终 bug 分级报告 | 创建 |
| 业务代码、测试、生成产物 | 被检查对象 | 不修改 |

执行约束：

- 所有 shell 命令使用 `rtk` 前缀，符合仓库根 `AGENTS.md` 引入的 RTK 要求。
- 不运行会删除本地数据的命令，除非用户在执行阶段明确确认。`make seed-e2e` 会 TRUNCATE 业务表，因此默认不跑。
- `make openapi-check` 会覆盖 `openapi/openapi.yaml`；本计划使用等价的临时目录生成方式检查契约漂移，避免污染工作区。
- 浏览器验证必须使用真实浏览器；curl 或直接 API 请求只能作为补充证据。

---

## Task 1: 建立排查报告和环境基线

**Files:**
- Create: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: none

- [ ] **Step 1: 确认工作区基线**

Run:

```bash
rtk git status --short --branch
```

Expected: 第一行显示当前分支状态，后续没有业务代码、测试或生成产物的未提交改动。若只存在已经批准的 plan/report 文档改动，记录到报告的“工作区基线”。

- [ ] **Step 2: 创建报告文件**

Apply this patch:

```patch
*** Begin Patch
*** Add File: docs/reports/2026-05-25-project-bug-audit.md
+# 项目全量 Bug 排查报告
+
+**日期：** 2026-05-25
+**范围：** 自动化基线、代码审计、真实浏览器验证
+**结论：** 排查完成后填写
+
+## 工作区基线
+
+- 分支状态：
+- 最近提交：
+- 本次排查创建的文件：
+
+## 自动化检查
+
+| 检查项 | 命令 | 结果 | 证据摘要 |
+|---|---|---|---|
+| Go 单元测试 | `rtk go test ./...` | 未运行 | 待执行 |
+| Go vet | `rtk go vet ./...` | 未运行 | 待执行 |
+| 前端 typecheck | `rtk npm run typecheck`（`web/`） | 未运行 | 待执行 |
+| 前端单测 | `rtk npm test -- --run`（`web/`） | 未运行 | 待执行 |
+| 前端构建 | `rtk npm run build`（`web/`） | 未运行 | 待执行 |
+| OpenAPI 契约 | 临时目录生成并 diff | 未运行 | 待执行 |
+| 前端类型契约 | 临时文件生成并 diff | 未运行 | 待执行 |
+
+## 代码审计记录
+
+- 权限与资源隔离：
+- job/worker 状态流：
+- Runtime 操作与 agent 交互：
+- new-api 余额与用量实时查询：
+- `users.deleted_at` 语义：
+
+## 浏览器验证记录
+
+- 服务启动状态：
+- 平台管理员流程：
+- 组织管理员流程：
+- 组织成员流程：
+- 实例详情和 job 进度：
+- 余额与用量页面：
+
+## 已确认 Bug
+
+当前无已确认 bug。
+
+## 疑似风险
+
+当前无疑似风险。
+
+## 无法验证项
+
+当前无无法验证项。
+
+## 建议下一步
+
+排查完成后填写。
*** End Patch
```

Expected: 只新增 `docs/reports/2026-05-25-project-bug-audit.md`。

- [ ] **Step 3: 记录最近提交和工具版本**

Run:

```bash
rtk git log --oneline -n 12
rtk go version
rtk node --version
rtk npm --version
rtk docker compose version
```

Expected: 每个命令 exit 0。把版本号和最近提交摘要写入报告的“工作区基线”。

---

## Task 2: 后端自动化基线

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 跑 Go 单元测试**

Run from repository root:

```bash
rtk go test ./...
```

Expected: exit 0，所有 package PASS。若失败，把失败 package、测试名、错误摘要写入“自动化检查”；稳定失败且由当前代码行为导致的项按 `P1` 或 `P2` 记入“已确认 Bug”。

- [ ] **Step 2: 跑 Go vet**

Run from repository root:

```bash
rtk go vet ./...
```

Expected: exit 0。若失败，把 vet 输出的文件、行号和检查项写入“自动化检查”；运行期崩溃风险或格式化错误按影响级别归类。

- [ ] **Step 3: 后端失败项复核**

Run only for each failed package found in Step 1:

```bash
rtk go test ./path/to/package -run 'FailedTestName' -count=1 -v
```

Expected: 复跑结果与 Step 1 一致。把稳定复现的失败保留为 bug；只出现一次且复跑通过的失败放入“疑似风险”，标明可能是环境或时序问题。

---

## Task 3: 前端自动化基线

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 跑类型检查**

Run from `web/`:

```bash
rtk npm run typecheck
```

Expected: exit 0。若失败，把 TypeScript 文件、行号和错误码写入报告；接口类型漂移或组件 prop 不匹配按 `P1` 或 `P2` 归类。

- [ ] **Step 2: 跑 Vitest 单测**

Run from `web/`:

```bash
rtk npm test -- --run
```

Expected: exit 0。若失败，记录 spec 文件、测试名和断言差异；稳定失败按影响归类。

- [ ] **Step 3: 跑前端生产构建**

Run from `web/`:

```bash
rtk npm run build
```

Expected: exit 0，Vite build 成功。若失败，记录构建错误；影响所有用户访问时归为 `P1`。

---

## Task 4: 契约同步检查（不污染工作区）

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no generated artifacts

- [ ] **Step 1: 生成 OpenAPI 到临时目录**

Run from repository root:

```bash
rtk mkdir -p /tmp/oc-manager-openapi-audit
rtk go run github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc5 init --generalInfo main.go --dir cmd/server,internal/api/handlers,internal/service,internal/domain --output /tmp/oc-manager-openapi-audit --outputTypes yaml --v3.1
```

Expected: exit 0，生成 `/tmp/oc-manager-openapi-audit/swagger.yaml`。

- [ ] **Step 2: 对比 OpenAPI 契约**

Run from repository root:

```bash
rtk diff -u openapi/openapi.yaml /tmp/oc-manager-openapi-audit/swagger.yaml
```

Expected: exit 0 且无 diff。若出现 diff，把首个差异段和涉及 path/schema 写入报告；这说明 `openapi/openapi.yaml` 未同步，按影响归为 `P2` 或 `P3`。

- [ ] **Step 3: 生成前端 API 类型到临时文件**

Run from `web/`:

```bash
rtk npx openapi-typescript@7.13.0 ../openapi/openapi.yaml -o /tmp/oc-manager-generated-api.ts
```

Expected: exit 0，生成 `/tmp/oc-manager-generated-api.ts`。

- [ ] **Step 4: 对比前端生成类型**

Run from repository root:

```bash
rtk diff -u web/src/api/generated.ts /tmp/oc-manager-generated-api.ts
```

Expected: exit 0 且无 diff。若出现 diff，把首个差异段和涉及类型写入报告；这说明 `web/src/api/generated.ts` 未同步，按影响归为 `P2` 或 `P3`。

---

## Task 5: 近期高风险代码审计

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 审计最近提交涉及的业务面**

Run:

```bash
rtk git show --stat --oneline HEAD~8..HEAD
rtk git log --oneline -n 8
```

Expected: 输出最近 8 个提交及变更文件。把涉及权限、job、worker、Runtime、前端实例详情页的文件列入报告“代码审计记录”。

- [ ] **Step 2: 搜索 service/handler 内联角色判断**

Run:

```bash
rtk rg -n "principal\\.Role|Role ==|Role !=|platform_admin|org_admin|org_member" internal --glob '!internal/auth/authorizer.go' --glob '!**/*_test.go'
```

Expected: 只出现 DTO、常量映射、错误响应或已明确允许的展示逻辑。若在 handler/service 发现新的资源权限判断，记录文件行号；违反 `AGENTS.md` 权限集中约束时归为 `P1` 或 `P2`。

- [ ] **Step 3: 审计 job 查询、轮询和 worker 状态流**

Run:

```bash
rtk rg -n "GetJob|payload_json|app_id|CanViewApp|CanTriggerRuntimeOperation|useJobQuery|JobProgressPanel|refetchInterval|retry" internal web/src
```

Expected: job 查询权限、payload app 关联、前端轮询和 worker 状态推进能形成一致闭环。发现“能触发但不能查”“终态错误仍无限轮询”“worker 成功但 app 状态未推进”等路径时，记录为已确认 bug 或疑似风险。

- [ ] **Step 4: 审计 new-api 余额和用量实时查询约束**

Run:

```bash
rtk rg -n "quota|usage|token logs|tokenLogs|RemainQuota|recharge_records|newapi|NewAPI" internal web/src
```

Expected: 余额和用量展示通过 new-api 查询，manager 本地只保留充值操作日志。若发现把 new-api 余额/用量快照写入本地业务表或前端展示本地缓存，按 `P1` 或 `P2` 归类。

- [ ] **Step 5: 审计 `users.deleted_at` 语义**

Run:

```bash
rtk rg -n "deleted_at|SoftDeleteUser|EnableUser|DisableUser|users_active_idx|status = 'disabled'|status='disabled'" internal
```

Expected: 用户下线场景只把 `deleted_at` 当作禁用时间戳，活跃用户查询使用 `deleted_at IS NULL`，不混用组织真删除语义。若发现把 `users.deleted_at` 当不可恢复删除使用，按影响归类。

---

## Task 6: 本地服务与浏览器验证准备

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 查看 Compose 服务状态**

Run:

```bash
rtk docker compose ps
```

Expected: 能读取 compose 状态。若 Docker 不可用，把错误写入“无法验证项”，并跳到 Task 8 总结。

- [ ] **Step 2: 启动本地开发环境**

Run:

```bash
rtk make dev-up
```

Expected: exit 0，`manager-postgres`、`manager-redis`、`manager-api`、`manager-web` 至少启动。若启动失败，记录失败服务和日志摘要。

- [ ] **Step 3: 应用数据库迁移**

Run:

```bash
rtk make migrate-up
```

Expected: exit 0。若迁移失败，记录版本号和错误；迁移失败会阻塞浏览器验证。

- [ ] **Step 4: 确认平台管理员账号**

Run:

```bash
rtk docker compose run --rm --no-deps manager-api go run ./cmd/seed-admin admin admin123 admin
```

Expected: exit 0，输出“已创建 platform_admin”或“用户名 admin 已存在”。这是幂等写入，不会清空业务表。

---

## Task 7: 真实浏览器验证核心流程

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 打开登录页**

Use a real browser through DevTools:

```text
URL: http://localhost:5173/login
```

Expected: 登录页可见，控制台没有红色错误。把页面是否加载、首个控制台错误和网络 5xx 记录到报告。

- [ ] **Step 2: 平台管理员登录**

Browser actions:

```text
组织标识：留空
账号：admin
密码：admin123
点击：登录
```

Expected: 登录后离开 `/login`，进入平台首页或角色首页。若停留登录页，记录页面错误提示和 `/api/v1/auth/login` 响应状态。

- [ ] **Step 3: 验证平台管理员关键页面**

Navigate in browser:

```text
/console
/organizations
/runtime-nodes
/audit-logs
```

Expected: 每个页面可加载，无全屏错误、无权限误拒绝、无控制台红色错误。发现页面白屏、接口 5xx、明显空数据误判时，记录路径和截图说明。

- [ ] **Step 4: 验证组织和应用入口**

Navigate in browser:

```text
/organizations
/apps
/usage
```

Expected: 页面要么显示真实数据，要么显示清晰空态或错误态；不得出现无限加载、重复 toast、缓存余额/用量假象。把异常归类到报告。

- [ ] **Step 5: 验证实例详情和 job 进度**

When an app row is available in `/apps`, open its detail page:

```text
/apps/<appId>/overview
```

Expected: 概览页加载实例名、状态、Runtime 操作区和 job 进度区域。若页面提供“立即重启”等操作，不执行破坏性或会重启容器的操作；只记录按钮可见性、权限表现和当前 job 展示状态。

- [ ] **Step 6: 验证非平台角色权限边界**

Only if an existing organization admin/member account is available from local data:

```text
组织标识：对应组织 code
账号：组织管理员或成员账号
密码：对应密码
```

Expected: 组织管理员不能访问平台-only 页面，组织成员只能看到自己允许访问的资源。没有可用账号时，把该项写入“无法验证项”，不运行 `make seed-e2e`。

---

## Task 8: 可选 Playwright e2e 验证（需要用户确认本地数据可重置）

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 请求确认是否允许重置本地业务表**

Ask the user this exact question before running e2e:

```text
Playwright globalSetup 会执行 `make seed-e2e`，该命令会 TRUNCATE 本地业务表并重建测试数据。是否允许在当前本地数据库上运行？
```

Expected: 用户明确回答允许后继续；用户未确认或拒绝时，跳过 Task 8 后续步骤，并把 Playwright e2e 写入“无法验证项”。

- [ ] **Step 2: 安装 Playwright Chromium**

Run from `web/` only after Step 1 is allowed:

```bash
rtk npx playwright install chromium
```

Expected: exit 0。

- [ ] **Step 3: 跑 e2e 测试**

Run from `web/` only after Step 1 is allowed:

```bash
rtk npm run test:e2e
```

Expected: exit 0。若失败，记录 spec 文件、测试名、trace/screenshot 路径和浏览器可见错误；稳定失败按影响归类。

---

## Task 9: 汇总分级报告并交付

**Files:**
- Modify: `docs/reports/2026-05-25-project-bug-audit.md`
- Modify: no business code

- [ ] **Step 1: 整理 bug 分级**

Update report sections using this severity mapping:

```text
P0：数据泄露、权限绕过、破坏性数据写入、核心流程完全不可用。
P1：主要业务流程失败、状态严重不一致、用户可见错误。
P2：边界条件错误、错误提示或重试策略不合理、局部功能异常。
P3：低风险一致性问题、测试缺口、文档与行为轻微不一致。
```

Expected: 每条问题都有标题、证据来源、影响范围和建议下一步；没有证据的项只能放在“疑似风险”或“无法验证项”。

- [ ] **Step 2: 检查工作区没有业务代码改动**

Run:

```bash
rtk git status --short
```

Expected: 只允许出现 `docs/reports/2026-05-25-project-bug-audit.md` 的改动和本计划文档改动。若出现业务代码、测试或生成产物改动，先确认来源；检查工具造成的临时生成差异不得混入最终交付。

- [ ] **Step 3: 最终交付摘要**

Final response must include:

```text
- 自动化检查运行结果。
- 浏览器验证是否完成；未完成时说明阻塞原因。
- 已确认 bug 列表，按 P0/P1/P2/P3 排序。
- 疑似风险和无法验证项。
- 下一步建议：进入哪个具体 bug 的修复设计。
```

Expected: 用户无需打开报告文件也能理解结论；报告文件保留完整证据。

---

## Self-Review

- Spec coverage: Task 2 覆盖后端测试和 vet；Task 3 覆盖前端 typecheck/test/build；Task 4 覆盖 OpenAPI 与前端类型同步；Task 5 覆盖权限、job/worker、Runtime、new-api、`users.deleted_at`；Task 6-8 覆盖真实浏览器和 e2e 验证；Task 9 覆盖分级报告。
- Placeholder scan: 本计划没有未填充内容、未定义任务或跳过细节的引用。
- Scope check: 计划只创建报告，不安排业务代码修复；发现 bug 后另走修复设计与计划。
