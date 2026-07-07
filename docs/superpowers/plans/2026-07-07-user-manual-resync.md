# 用户手册全量重核对同步 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `docs/user-manual.md` 全量重核对重写，使其与当前 manager 前端（`web/src`）完全一致：删除已下线内容、补齐所有新页面与 tab、修正渠道与登录字段描述。

**Architecture:** 沿用「按角色分章」结构（登录 / 平台管理员 / 企业管理员 / 企业成员 / 技能体系公共节 / 通用约定 / 速查）。真相来源是阅读前端页面组件与 `web/src/app/router.ts` 路由表，不依赖运行环境。逐章节以 Edit 在现有文件上原地转换（删除失效章、改写变动章、插入新章），保证每次提交后手册仍是一篇连贯文档。

**Tech Stack:** Markdown 文档；Vue 3 + vue-router 前端源码作为核实依据；`grep` / 阅读组件核对。

**依据 spec:** `docs/superpowers/specs/2026-07-07-user-manual-resync-design.md`

---

## 权威路由 → 页面 → 角色映射（所有任务的核对基准）

来自 `web/src/app/router.ts`。角色缩写：P=platform_admin，A=org_admin，M=org_member。「全部」=所有已登录用户。

| 路径 | 组件 | 可访问角色 |
|---|---|---|
| `/login` | `pages/login/LoginHost.vue` | 公开 |
| `/`（首页） | `pages/dashboard/RoleAwareHome.vue` | 全部 |
| `/console` | `pages/platform/ConsolePage.vue` | P |
| `/platform/dashboard`、`/dashboard` | → 重定向到 `/console` | — |
| `/org-console` | `pages/org/OrgConsolePage.vue` | A |
| `/organizations` | `pages/platform/OrganizationsPage.vue` | P |
| `/assistant-versions` | `pages/platform/AssistantVersionsPage.vue` | P |
| `/platform/industry-knowledge` | `pages/platform/IndustryKnowledgePage.vue` | P |
| `/platform/skills` | `pages/platform/PlatformSkillsPage.vue` | P |
| `/platform/custom-skills` | `pages/platform/CustomSkillTicketsPage.vue` | P |
| `/platform/organizations/:orgId/recharge` | `pages/platform/RechargePage.vue` | P |
| `/platform/permissions` | `pages/platform/PermissionsPage.vue` | P |
| `/platform/web-publish-config` | `pages/platform/WebPublishConfigPage.vue` | P + A |
| `/members` | `pages/org/MembersPage.vue` | P + A |
| `/published-sites` | `pages/org/PublishedSitesPage.vue` | P + A |
| `/members/new` | `pages/org/CreateMemberPage.vue` | A |
| `/audit-logs` | `pages/audit/AuditLogsPage.vue` | P + A |
| `/knowledge` | `pages/knowledge/OrgKnowledgePage.vue` | 全部 |
| `/skills` | `pages/skills/OrgSkillsPage.vue` | 全部 |
| `/skill-tickets/:id` | `pages/skill-tickets/TicketDetailPage.vue` | 全部 |
| `/usage` | `pages/usage/UsagePage.vue` | 全部 |
| `/balance` | `pages/org/OrgBalancePage.vue` | A |
| `/apps` | `pages/apps/AppsPage.vue`（M 重定向到自己实例详情或 `/apps/empty`） | 全部 |
| `/apps/empty` | `pages/apps/AppEmptyPage.vue` | 全部 |
| `/apps/:appId` | `pages/apps/AppDetailPage.vue` | 全部 |
| `…/overview` | `pages/apps/AppOverviewTab.vue` | 全部 |
| `…/kanban` | `pages/apps/AppKanbanTab.vue` | 全部 |
| `…/cron` | `pages/apps/AppCronTab.vue` | 全部 |
| `…/runtime` | `pages/apps/AppRuntimeTab.vue` | **仅 P** |
| `…/channels` | `pages/apps/AppChannelsTab.vue` | 全部 |
| `…/knowledge` | `pages/apps/AppKnowledgeTab.vue` | 全部 |
| `…/skills` | `pages/apps/AppSkillsTab.vue` | 全部 |
| `…/workspace` | `pages/apps/AppWorkspaceTab.vue` | 全部 |
| `…/audit` | `pages/apps/AppAuditTab.vue` | 全部 |
| `…/conversations` | `pages/apps/AppConversationsTab.vue` | 全部 |

> 注：tab 组件本身对全部登录用户开放（除 runtime 仅 P），实际写/读能力由页面内 helper 按「实例 owner / 管理员」再裁剪——每个 tab 小节须核实并写明这层边界。

## 每个页面小节的写作规范（DRY，全任务通用）

除非该页面特殊，每个页面小节按现有手册风格覆盖以下四点：

1. **页面入口与角色边界**：路由路径 + 哪些角色能进（对照上表）。
2. **列表 / 展示内容**：页面主要展示的字段或区块。
3. **可执行操作与确认方式**：按钮/操作，以及是否弹 `ConfirmActionModal`（需输入对象名）或浏览器原生 `confirm`。
4. **权限边界**：页面内对写/读操作的进一步角色裁剪（如「实例 owner 才能删」）。

「核实」一律指：打开对应组件文件读取模板与 script，确认上述四点的真实行为，不凭记忆或旧手册下笔。

## 通用「核实 + 提交」步骤模板

除非某任务另行说明，每个任务按此节奏：读组件 → 依写作规范改写/新增小节（用 Edit 原地转换现有文件）→ 核对 → 提交。核对指：

- 该小节出现的每个路由路径都能在权威映射表 / `router.ts` 中找到。
- 不引入已下线术语（见 Task 9 的黑名单）。

---

## Task 1: 登录章 + 顶部说明

**Files:**
- Modify: `docs/user-manual.md`（顶部 `> 平台管理员 / …` 说明段 + 「## 登录」整节）
- 核实来源: `web/src/pages/login/LoginHost.vue`，语言切换组件（在 `web/src` 内 grep `language` / `locale` / `切换语言` 定位，如 `DashboardLayout.vue` 或独立 LanguageSwitcher）

- [ ] **Step 1: 读组件核实**

读 `LoginHost.vue`，确认登录表单实际字段与命名（近期 i18n 提交把「用户名/显示名」统一为「账号/姓名」——确认登录页用词）。grep 语言切换：
```bash
cd web/src && grep -rn "locale\|语言\|zh-CN\|en-US" layouts/ pages/login/ i18n/ 2>/dev/null | head -30
```
确认：界面语言可否在登录页/顶栏切换、支持哪些语言、默认语言。

- [ ] **Step 2: 改写登录节**

用 Edit 更新「## 登录」节：字段命名与组件一致；补一段「界面语言切换（中/英）」说明；登录后跳转写为「平台管理员 → `/console`；企业角色 → 角色首页（`RoleAwareHome`）」。保留「重置密码不自助」说明（核实仍成立）。

- [ ] **Step 3: 核对**

```bash
grep -nE "runtime-nodes|运行节点|Runtime Node|/org/persona" docs/user-manual.md | head
```
本任务改动区不应引入这些术语。

- [ ] **Step 4: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 重写登录章并补界面语言切换"
```

---

## Task 2: 平台管理员 — 总览 / 企业管理 / 权限管理（1.1–1.3），删除运行节点章

**Files:**
- Modify: `docs/user-manual.md`（§1.1 平台总览、§1.2 企业管理，新增 §1.3 权限管理，删除旧 §1.3 运行节点整章）
- 核实来源: `pages/platform/ConsolePage.vue`、`pages/platform/OrganizationsPage.vue`、`pages/platform/RechargePage.vue`、`pages/platform/PermissionsPage.vue`

- [ ] **Step 1: 读组件核实**

- `ConsolePage.vue`：确认平台总览实际展示的指标项集合（节点概念已删——核实是否还有「运行中/异常」这类实例聚合，以及总余额来源）。
- `OrganizationsPage.vue`：确认企业列表列、建企业表单当前字段（是否仍有可用模型多选、是否在此设置人设/预警阈值）、启用/禁用、复制信息、充值入口是否跳转到独立充值页。
- `RechargePage.vue`：确认独立充值页 `/platform/organizations/:orgId/recharge` 的展示（当前余额直查、充值金额/备注、确认方式）。
- `PermissionsPage.vue`：确认权限管理页做什么（查看/配置角色-资源权限？可操作项与边界）。

- [ ] **Step 2: 改写 §1.1、§1.2**

按核实结果用 Edit 更新 §1.1 平台总览（路径改 `/console`，指标表按实际）、§1.2 企业管理（充值改为「跳转独立充值页」并描述充值页）。

- [ ] **Step 3: 新增 §1.3 权限管理**

在 §1.2 后插入 §1.3 权限管理小节（按写作规范四点）。

- [ ] **Step 4: 删除旧「运行节点」章**

删除旧 §1.3「运行节点」整章（从 `### 1.3 运行节点` 到下一 `### 1.4` 之前的全部内容）。后续小节编号顺延在 Task 5 统一校对（本任务内先保证不遗留节点内容）。

- [ ] **Step 5: 核对**

```bash
grep -nE "运行节点|runtime-nodes|Runtime Node|心跳|enrollment" docs/user-manual.md
```
预期：无输出（§2.3 概览里的 Runtime Node 引用在 Task 4 处理；若此处仍有命中且属概览章，记录待 Task 4）。

- [ ] **Step 6: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 重写平台总览/企业管理/充值，新增权限管理，删除运行节点章"
```

---

## Task 3: 平台管理员 — 助手版本 / 行业知识库 / 平台技能库 / 定制技能工单 / web 发布（1.4–1.8）

**Files:**
- Modify: `docs/user-manual.md`（对应各节）
- 核实来源: `pages/platform/AssistantVersionsPage.vue`、`pages/platform/IndustryKnowledgePage.vue`、`pages/platform/PlatformSkillsPage.vue`、`pages/platform/CustomSkillTicketsPage.vue`、`pages/platform/WebPublishConfigPage.vue`、`pages/org/PublishedSitesPage.vue`

- [ ] **Step 1: 读组件核实**

- `AssistantVersionsPage.vue`：助手版本创建/编辑字段；**是否承载企业人设编辑**（spec 待核实点）；行业知识库关联（旧 §1.9 内容并入此处）。
- `IndustryKnowledgePage.vue`：行业库增删/上传/文件管理/外部上传接口文档弹框——核实旧 §1.8 文案是否仍准确（接口地址、Header、字段、返回码）。
- `PlatformSkillsPage.vue`：平台技能库——技能来源（平台库 / 外部源 ClawHub 等）、上传方式（粘贴 MD / 传文件夹打包）、版本号校验（近期提交要求 x.x.x 或 x.x）、列表与操作。
- `CustomSkillTicketsPage.vue`：定制技能工单——工单列表、状态流转、定向交付。
- `WebPublishConfigPage.vue`：平台侧开通/停用/跨企业配置。
- `PublishedSitesPage.vue`：已发布站点列表 + 证书状态 + 平台可跨企业查看/重试证书。

- [ ] **Step 2: 改写/新增各节**

- 更新 §1.4 助手版本（吸收旧 §1.9 行业库关联；若承载人设编辑则在此写明，并在 Task 6/7 的人设落点引用）。
- 更新 §1.5 行业知识库（保留并按组件校正外部上传接口文档）。
- 新增 §1.6 平台技能库、§1.7 定制技能工单。
- 新增 §1.8 web 发布（`/platform/web-publish-config` + `/published-sites`）。

- [ ] **Step 3: 核对**

```bash
grep -nE "/platform/skills|/platform/custom-skills|/platform/web-publish-config|/published-sites|/assistant-versions|/platform/industry-knowledge" docs/user-manual.md
```
预期：以上路径均出现。

- [ ] **Step 4: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 重写助手版本/行业库，新增平台技能库/定制工单/web发布"
```

---

## Task 4: 企业管理员 — 企业控制台 / 成员管理 / 实例列表 / 实例详情全部 tab（2.1–2.4）

**Files:**
- Modify: `docs/user-manual.md`（§2 开头到实例详情各 tab）
- 核实来源: `pages/org/OrgConsolePage.vue`、`pages/org/MembersPage.vue`、`pages/org/CreateMemberPage.vue`、`pages/apps/AppsPage.vue`、`pages/apps/AppDetailPage.vue` 及 10 个 tab 组件

- [ ] **Step 1: 读组件核实（控制台/成员/列表）**

- `OrgConsolePage.vue`：企业控制台展示与入口（企业维度概览？余额/用量/成员/实例入口？）。
- `MembersPage.vue` + `CreateMemberPage.vue`：成员列表列、建成员表单字段（命名统一后的账号/姓名）、创建并初始化实例、仅建账号、禁用/启用、重置密码、删除的确认方式；平台管理员在此页的只读 + 创建新实例能力。
- `AppsPage.vue`：实例列表列、工具栏入口、行内操作（重启/停止/删除）确认方式；M 的重定向行为（见路由 `beforeEnter`）。

- [ ] **Step 2: 读组件核实（实例详情 tab）**

依次读并记录每个 tab 的真实行为与角色边界：
- `AppDetailPage.vue`：tab 顺序与整体布局。
- `AppOverviewTab.vue`：基础信息字段（**移除 Runtime Node**）、API key 禁用/恢复、切换模型、重新初始化、**「平台提示词已更新需重启」横幅**、**restarting/runtime_phase 状态**表现。
- `AppKanbanTab.vue`（看板，342 行）、`AppCronTab.vue`（定时任务，333 行）：核实各自功能与可见性/编辑权限。
- `AppRuntimeTab.vue`：**仅平台管理员可见**——写明企业管理员/成员在此看不到该 tab；内容为资源趋势 + 启动/停止/重启/删除。
- `AppChannelsTab.vue`：**4 个渠道**（微信/企业微信/飞书/钉钉）各自的绑定方式（微信/飞书扫码；企业微信/钉钉填凭证 env）、即时验证、解绑。
- `AppKnowledgeTab.vue`：实例私有库上传/列表/下载/删除/重解析；移除已下线的容量编辑描述（近期提交 `76aa5d64`）。
- `AppSkillsTab.vue`（26 行）：实例技能管理（安装/移除，来源指向技能体系公共节 Task 6）。
- `AppWorkspaceTab.vue`：工作目录只读浏览/下载/归档。
- `AppAuditTab.vue`：实例审计。
- `AppConversationsTab.vue`：对话（会话列表 + 流式续聊）；**不写语音输入**（已注释禁用）。

- [ ] **Step 3: 改写 §2.1–§2.4**

按核实结果用 Edit 更新：新增 §2.1 企业控制台；§2.2 成员管理、§2.3 实例列表按组件校正；§2.4 实例详情把 tab 列表改为「概览、看板、定时任务、渠道、实例知识库、技能、工作目录、审计、对话」（说明运行时 tab 仅平台管理员可见），逐 tab 写小节。

- [ ] **Step 4: 核对**

```bash
grep -nE "Runtime Node|运行节点" docs/user-manual.md   # 预期无输出
grep -nE "看板|定时任务|对话|企业微信|飞书|钉钉|平台提示词" docs/user-manual.md | head
```

- [ ] **Step 5: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 重写企业管理员实例详情章（新增看板/定时/技能/对话tab、4渠道、移除节点引用）"
```

---

## Task 5: 企业管理员 — 知识库 / 企业余额 / web 发布企业侧 / 用量 / 审计 / 人设落点（2.5–2.8），并统一 §1 编号

**Files:**
- Modify: `docs/user-manual.md`
- 核实来源: `pages/knowledge/OrgKnowledgePage.vue`、`pages/org/OrgBalancePage.vue`、`pages/platform/WebPublishConfigPage.vue`（企业视角分支）、`pages/usage/UsagePage.vue`、`pages/audit/AuditLogsPage.vue`

- [ ] **Step 1: 读组件核实**

- `OrgKnowledgePage.vue`：企业级知识库上传/列表/下载/删除/重解析与角色边界。
- `OrgBalancePage.vue`（107 行）：企业余额页展示（余额直查 new-api、预警阈值？），`/balance` 仅 org_admin。
- `WebPublishConfigPage.vue`：企业管理员视角能配置「本企业且平台已开通」的 web 发布（开通/停用仍仅平台）。
- `UsagePage.vue`：企业管理员看到的 tab（企业/成员/实例）。
- `AuditLogsPage.vue`：企业视角审计（无跨企业下拉）。
- 人设落点：结合 Task 3 对 `AssistantVersionsPage`/`OrganizationsPage` 的核实，确认企业管理员如何编辑/查看人设，改写旧 §2.4「企业 AI 人设」为指向真实落点的小节。

- [ ] **Step 2: 改写/新增各节**

新增 §2.6 企业余额、§2.7 web 发布（企业侧）；更新企业知识库、用量、审计各节；改写人设落点小节。

- [ ] **Step 3: 统一 §1 与 §2 小节编号**

通读 §1、§2，修正因删章/插章导致的编号断裂，保证连续。

- [ ] **Step 4: 核对**

```bash
grep -nE "/balance|/knowledge|/usage|/audit-logs" docs/user-manual.md | head
grep -nE "### 1\.|### 2\." docs/user-manual.md   # 人工确认编号连续
```

- [ ] **Step 5: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 补企业余额/web发布企业侧/人设落点，校正章节编号"
```

---

## Task 6: 企业成员章（§3）+ 技能体系公共节（§4）

**Files:**
- Modify: `docs/user-manual.md`（§3 企业成员，新增 §4 技能体系）
- 核实来源: `pages/skills/OrgSkillsPage.vue`、`pages/skill-tickets/TicketDetailPage.vue`、`pages/apps/AppSkillsTab.vue`、`pages/platform/PlatformSkillsPage.vue`、`pages/platform/CustomSkillTicketsPage.vue`、`pages/dashboard/RoleAwareHome.vue`

- [ ] **Step 1: 读组件核实**

- `RoleAwareHome.vue`：成员登录后落点。
- `OrgSkillsPage.vue`（56 行）+ `TicketDetailPage.vue`：成员技能页能做什么（浏览/安装技能、发起定制工单、查看工单详情）。
- 成员视角实例详情：仅自己实例、无运行时 tab、人设只读、用量「我的」。
- 汇总技能体系四方关系：平台技能库（P 维护）→ 实例技能 tab（装到实例）→ 定制工单（M 发起、P 定制交付）→ 成员技能页（M 浏览/安装）。

- [ ] **Step 2: 改写 §3 企业成员**

按成员权限范围改写：实例（仅自己）+ 各 tab（去掉运行时 tab；渠道/知识库/工作目录/审计/对话/看板/定时按可见性）、技能页、知识库只读、用量「我的」、人设只读落点。

- [ ] **Step 3: 新增 §4 技能体系（跨角色公共说明）**

新增 §4，用一段说明串起四方关系，供 §1.6/§1.7、§2.4 技能 tab、§3 技能页引用（在这些小节加「详见 §4」指引，避免重复）。

- [ ] **Step 4: 核对**

```bash
grep -nE "/skills|skill-tickets|技能体系" docs/user-manual.md | head
```

- [ ] **Step 5: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 重写企业成员章并新增技能体系公共节"
```

---

## Task 7: 通用约定（§5）+ 常见任务速查（§6）

**Files:**
- Modify: `docs/user-manual.md`（旧 §4 通用约定 → §5，旧 §5 速查 → §6）
- 核实来源: 已读过的各页面确认（高风险确认清单、状态提示）；`AppOverviewTab.vue`（restarting/需重启横幅）

- [ ] **Step 1: 更新通用约定**

- 高风险操作确认清单：按最新页面核实（停止/删除实例、删除成员、重置密码、禁用 API key 等；技能/web 发布等新写操作是否有确认）。
- 补「界面语言切换」「restarting / runtime_phase 状态」「平台提示词需重启横幅」到状态/约定说明。
- 异常状态提示表：移除节点相关行（如「节点 unreachable」），按现状校正。

- [ ] **Step 2: 重建速查表**

按新页面重写「常见任务速查」表：移除运行节点/旧人设页入口，新增技能、对话、web 发布、企业余额、看板、定时任务等入口，路径与角色对照权威映射表。

- [ ] **Step 3: 核对**

```bash
grep -nE "节点|runtime-nodes|/org/persona|/platform/dashboard" docs/user-manual.md   # 预期无残留（重定向别名不写入手册）
```

- [ ] **Step 4: 提交**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 更新通用约定与常见任务速查表"
```

---

## Task 8: 全篇一致性核对（收尾）

**Files:**
- Modify: `docs/user-manual.md`（仅修核对中发现的缺口）

- [ ] **Step 1: 死术语黑名单扫描**

```bash
grep -nE "运行节点|runtime-nodes|Runtime Node|心跳|enrollment secret|/org/persona|语音输入|VoiceInput" docs/user-manual.md
```
预期：无输出。有命中则修正（重定向别名 `/platform/dashboard`、`/dashboard` 不应作为页面入口出现在手册正文）。

- [ ] **Step 2: 路径闭环核对**

提取手册中所有 `/xxx` 路由路径，逐个确认存在于权威映射表 / `router.ts`：
```bash
grep -oE "\`/[a-zA-Z0-9:/_-]+\`" docs/user-manual.md | sort -u
```
人工比对：无「手册有但路由无」的路径；反向抽查权威映射表每个用户可达页面在手册中都有落点（工具类重定向除外）。

- [ ] **Step 3: 通读连贯性**

从头读一遍：章节编号连续、tab 列表与 §2.4 一致、技能体系引用（§4）在各处链接正确、角色边界前后不矛盾。

- [ ] **Step 4: 提交（如有修正）**

```bash
git add docs/user-manual.md && git commit -m "docs(manual): 全篇一致性核对与收尾修正"
```

---

## Self-Review（计划作者自查记录）

- **Spec 覆盖**：spec「结构差异总览」的每一项——删运行节点（Task 2/8）、删 persona 独立页/改落点（Task 5/6）、`/console` 等新平台页（Task 2/3）、org-console/balance（Task 4/5）、skills/工单（Task 3/6）、10 tab（Task 4）、4 渠道（Task 4）、登录字段/语言/跳转（Task 1）、需重启横幅/restarting（Task 4/7）——均有对应任务。spec「须核实清单」10 项分布在 Task 2–5 的 Step 1。
- **无占位符**：内容型步骤均给出「读哪个组件 + 核实哪几点 + 写哪节」的具体清单；因真相来源是代码、最终手册措辞须据组件而定，故写作步骤以「须覆盖的事实点」而非预写成稿呈现（doc-resync 的正确粒度），非 TODO 占位。
- **一致性**：各任务路径统一引用顶部权威映射表；死术语黑名单在 Task 8 统一兜底。
