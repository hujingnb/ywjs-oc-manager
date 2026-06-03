# Skill 技能页前端 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 skill 三处前端界面：① 平台管理员的平台库管理页（上传/列出/删除）;② 企业成员左侧菜单「技能」顶级页（已安装列表 + 技能市场）;③ 平台/企业管理员在实例详情页的「技能」tab。两个 per-app 入口复用同一套 `SkillManager` 组件;助手版本编辑页改「从库选」。

**Architecture:** Vue 3 + Pinia + Naive UI + TanStack Query。新建 `useSkills.ts` hooks（apiRequest + useQuery/useMutation），`SkillManager.vue` 共享组件（props `appId`），由成员 `/skills` 顶级页（`useMemberApp` 取自身 appId）与管理员 `AppSkillsTab`（props.appId + inject app）复用。平台库页与助手版本编辑独立。权限走 `domain/permissions.ts` 的 `canManageApp`。

**Tech Stack:** Vue 3 / Vite / Pinia / Naive UI / TanStack Query / vitest。

「Hermes Skill 市场」功能 Plan 6（共 6 个，最后一个）。**依赖 Plan 1/2/3 的后端接口已合入并跑过 `make openapi-gen`+`make web-types-gen`**，使 `web/src/api/generated.ts` 含 `/api/v1/apps/{appId}/skills`、`/api/v1/skill-market`、`/platform-skills` 路由与 `AppSkillResult`/`SkillPage`/`SkillEntry`/`PlatformSkillResult` 类型。**平台库页（Task 2）只依赖 Plan 1 的 `/platform-skills`，可先开工。**

> **现场确认项**：generated.ts 实际的 skill 路由请求/响应字段（hook 里 body/字段按生成结果对齐）;`useMemberApp`/`canManageApp`/`apiRequest`/auth store 实际签名（`web/src/composables/`、`web/src/domain/permissions.ts`、`web/src/api/client.ts`、`web/src/stores/auth.ts`）;DashboardLayout menuOptions 与 activeKey 行号。

校验命令：`make web-test`（vitest）、`cd web && npm run typecheck`（vue-tsc）。最终需**真实浏览器三角色验证**（CLAUDE.md 硬性要求，本地账号见 AGENTS.md）。

---

## File Structure

新建：
- `web/src/api/hooks/useSkills.ts`（+ `useSkills.spec.ts`）— platform/market/app skill 的 query/mutation
- `web/src/pages/platform/PlatformSkillsPage.vue` — 平台库管理页
- `web/src/components/SkillManager.vue`（+ `SkillManager.spec.ts`）— 已安装列表 + 市场，props appId
- `web/src/pages/skills/OrgSkillsPage.vue` — 成员顶级页（useMemberApp + SkillManager）
- `web/src/pages/apps/AppSkillsTab.vue` — 管理员 app tab（props.appId + inject app + SkillManager）
修改：
- `web/src/api/index.ts`（类型 alias）、`web/src/app/router.ts`（3 路由）、`web/src/layouts/DashboardLayout.vue`（成员菜单 + activeKey）、`web/src/pages/apps/AppDetailPage.vue`（skills tab）
- `web/src/pages/platform/AssistantVersionsPage.vue`（助手版本 skill 改「从库选」）

---

## Task 1: 类型 alias + useSkills hooks

**Files:** Modify `web/src/api/index.ts`；Create `web/src/api/hooks/useSkills.ts`、`useSkills.spec.ts`

- [ ] **Step 1: 加类型 alias** — `web/src/api/index.ts`（参照现有 WithRequired 收紧）：
```ts
export type PlatformSkill = WithRequired<Schemas['service.PlatformSkillResult'], 'id' | 'name' | 'version'>
export type AppSkill = WithRequired<Schemas['service.AppSkillResult'], 'name' | 'status'>
export type SkillEntry = WithRequired<Schemas['service.SkillEntry'], 'source' | 'source_ref' | 'name'>
```
（字段名按 generated.ts 实际，先 `grep -n 'AppSkillResult\|SkillEntry\|PlatformSkillResult' web/src/api/generated.ts` 确认。）

- [ ] **Step 2: 写 hooks 测试 + 实现** — Create `useSkills.ts`（抄 `useKnowledge.ts` 的 query/mutation 骨架）：
```ts
// 已安装列表（实时对账，含 status）
export function useAppSkillsQuery(appId: Ref<string | undefined>) { /* GET /api/v1/apps/${appId}/skills → r.skills */ }
// 市场浏览/搜索
export function useSkillMarketQuery(params: Ref<{ source?: string; q?: string }>) { /* GET /api/v1/skill-market */ }
// 安装 / 卸载 / 更新（mutation，onSuccess invalidate appSkillKey）
export function useInstallAppSkill(appId): /* POST /api/v1/apps/${appId}/skills body {source,source_ref,name,version} */
export function useUninstallAppSkill(appId): /* DELETE /api/v1/apps/${appId}/skills/${name} */
export function useUpdateAppSkill(appId): /* POST /api/v1/apps/${appId}/skills/${name}/update body {version} */
// 平台库
export function usePlatformSkillsQuery() { /* GET /platform-skills */ }
export function useUploadPlatformSkill() { /* POST /platform-skills multipart */ }
export function useDeletePlatformSkill() { /* DELETE /platform-skills/${id} */ }
```
`useSkills.spec.ts`（vitest，抄 `useKnowledge.spec.ts`）：断言 queryKey、queryFn 调对 path、mutation invalidate 缓存。

- [ ] **Step 3: 校验 + 提交** — `cd web && npm run typecheck && npm test -- useSkills`。提交：`feat(web): 增加 skill hooks 与类型 alias`。

---

## Task 2: 平台库管理页（可先开工，仅依赖 Plan 1）

**Files:** Create `web/src/pages/platform/PlatformSkillsPage.vue`；Modify `router.ts`、`DashboardLayout.vue`

- [ ] **Step 1: 页面组件** — `PlatformSkillsPage.vue`（参照 `AssistantVersionsPage.vue` 多区块 + `OrgKnowledgePage.vue` 表格）：`n-card` 标题 + 上传区（原生 file input + name/version/description 表单 → `useUploadPlatformSkill`）+ `n-data-table`（列：name、version、file_size、操作=删除按钮 `useDialog().warning` 确认 → `useDeletePlatformSkill`）。加载/错误态用 `.state-text`/`.danger`，提示用 `useMessage`。

- [ ] **Step 2: 路由 + 菜单** — `router.ts` 加 `{ path: 'platform/skills', component: PlatformSkillsPage, meta: { allowedRoles: PLATFORM_ONLY } }`（紧邻 `/assistant-versions`）+ import。`DashboardLayout.vue` 管理员分支 menuOptions 加平台库菜单项（key `/platform/skills`，lucide 图标如 `Package`），activeKey prefixes 加 `/platform/skills`。

- [ ] **Step 3: 测试 + 提交** — `PlatformSkillsPage.spec.ts`（vitest，抄 AppKnowledgeTab.spec.ts：mock hooks、stub Naive、断言列表渲染与删除按钮）。`npm run typecheck && npm test -- PlatformSkills`。提交：`feat(web): 增加平台库管理页`。

---

## Task 3: SkillManager 复用组件（已安装列表 + 市场）

**Files:** Create `web/src/components/SkillManager.vue`、`SkillManager.spec.ts`

- [ ] **Step 1: 组件骨架** — `SkillManager.vue` props `{ appId: string }`，两视图（`n-tabs` type=line：已安装 / 技能市场）：
  - **已安装视图**：`useAppSkillsQuery(appId)` → `n-data-table`，列：name、来源徽章（`n-tag`：platform 蓝 / clawhub 橙 / hermes 内置灰 / hermes 自创紫，按 `row.category`/`row.source`）、version、status（`n-tag`：active 成功 / pending 警告「待生效·重新安装」/ builtin 默认 / self_created 默认）、更新（`latest_version > version` 显示「更新」按钮 → `useUpdateAppSkill`）、操作（**当前版本必需 `row.protected` 隐藏卸载、显示锁标记**;builtin 只读;其余「卸载」按钮 `useDialog` 确认 → `useUninstallAppSkill`）。pending 长时间未 active 显示「重新安装」（再次 install）。
  - **技能市场视图**：来源筛选（`n-tag` 可点：全部/平台库/ClawHub）+ 搜索 `n-input`（debounce）→ `useSkillMarketQuery({source,q})` → 卡片网格（`n-card` 或 grid）：name、来源徽章、描述、version/downloads、安装状态（已装置灰、同名冲突禁装、ClawHub metadata 标「⚠ 需依赖」）→「安装」按钮 `useInstallAppSkill`。
  - 安装/卸载按钮显隐用 `canManageApp(auth.user, app)`（app 从 props 或 inject）。**镜像内置 skill 不在已安装列表手动管（只读展示）。**

- [ ] **Step 2: 测试** — `SkillManager.spec.ts`（vitest，抄 AppKnowledgeTab.spec.ts）：mock useSkills hooks、stub Naive（DataTableStub 渲染 columns.render 以断言徽章/按钮）、断言：四类 status 徽章渲染、protected 隐藏卸载、市场安装按钮、无权限时按钮隐藏。每个 it 带中文注释。

- [ ] **Step 3: 校验 + 提交** — `npm run typecheck && npm test -- SkillManager`。提交：`feat(web): 增加 SkillManager 复用组件（已安装+市场）`。

---

## Task 4: 成员 /skills 页 + 管理员 AppSkillsTab + 路由/菜单

**Files:** Create `web/src/pages/skills/OrgSkillsPage.vue`、`web/src/pages/apps/AppSkillsTab.vue`；Modify `router.ts`、`DashboardLayout.vue`、`AppDetailPage.vue`

- [ ] **Step 1: 成员顶级页** — `OrgSkillsPage.vue`：`const { appId, hasApp, isLoading } = useMemberApp()`;loading 显示加载态、`!hasApp` 显示空态（参照 `/apps/empty` 语义）、否则 `<SkillManager :app-id="appId" />`。

- [ ] **Step 2: 管理员 app tab** — `AppSkillsTab.vue`：`defineProps<{ appId: string }>()` + `const app = inject<Ref<AppDTO|null>>('app')`，`<SkillManager :app-id="appId" />`（SkillManager 内部用 app 做权限，或经 props 传 app）。

- [ ] **Step 3: 路由** — `router.ts`：DashboardLayout children 加 `{ path: 'skills', component: OrgSkillsPage }`（无 allowedRoles，成员可见，紧邻 `/knowledge`）;apps/:appId children 加 `{ path: 'skills', component: AppSkillsTab, props: true }`（紧邻 knowledge tab）+ import 两组件。

- [ ] **Step 4: 菜单 + tab 注册** — `DashboardLayout.vue` 成员分支 menuOptions 加「技能」项（key `/skills`，lucide 图标如 `Puzzle`）+ activeKey prefixes 加 `/skills`。`AppDetailPage.vue` allTabs 加 `{ path: 'skills', label: '技能' }`（对管理员可见，无需角色过滤）。

- [ ] **Step 5: 测试 + 提交** — `OrgSkillsPage.spec.ts`（mock useMemberApp：有 app/无 app/loading 三态）。`npm run typecheck && npm test`。提交：`feat(web): 增加成员技能页与实例技能 tab（复用 SkillManager）`。

---

## Task 5: 助手版本编辑页改「从库选」

**Files:** Modify `web/src/pages/platform/AssistantVersionsPage.vue`

- [ ] 版本编辑表单里 skill 配置区，从「上传 tar」改为「从平台库选」：`usePlatformSkillsQuery` 列出平台库 skill（name+version）→ `n-select` 选一个 → 调改造后的 `POST /assistant-versions/:id/skills` body `{source:'platform', source_ref, version}`（hook `useAddVersionSkill`）。已配 skill 列表展示 name/version + 删除。各 skill 显示「有更新」提示（对比平台库最新版本，可选）。更新对应 spec、测试、提交：`feat(web): 助手版本 skill 配置改为从库选`。

---

## Task 6: 整体校验 + 三角色浏览器验证

- [ ] **Step 1: 前端全量** — `cd web && npm run typecheck && npm test`，全过。
- [ ] **Step 2: 构建** — `cd web && npm run build`（vite），无错误。
- [ ] **Step 3: 真实浏览器三角色验证**（CLAUDE.md 硬性要求，本地 k3d + 账号见 AGENTS.md）：
  - **平台管理员**（admin/admin123）：平台库页上传/删除 skill;助手版本编辑「从库选」;实例详情「技能」tab 代管某成员实例（装/卸/更新/对账 status）。
  - **企业管理员**：实例详情「技能」tab 代管本 org 成员实例。
  - **企业成员**：左侧「技能」页（已安装列表四类 status、市场浏览筛选/搜索、安装一个 skill 后新对话验证生效、卸载、当前版本必需 skill 无卸载按钮）。
  - 发现问题先修再验，直到三角色全部正常。
- [ ] **Step 4: 提交**（若浏览器验证修了 bug）。

---

## Self-Review 备注

- **Spec 覆盖**：平台库管理页;成员左侧「技能」页（已安装四类 status 徽章 + 市场浏览安装）;管理员实例详情「技能」tab;双入口复用 SkillManager;当前版本必需 skill 隐藏卸载按钮;pending 重新安装;助手版本「从库选」。
- **复用改进**：knowledge 双入口未抽组件导致重复;skill 抽 `SkillManager.vue` 共享，成员/管理员两入口复用（对现有模式的合理改进）。
- **前置依赖**：per-app/市场页需后端 Plan 1/2/3 的 openapi 同步后 generated.ts 才有类型;平台库页仅需 Plan 1，可先开工。
- **现场确认项**：generated.ts 实际字段;DashboardLayout/router 行号;useMemberApp/canManageApp 签名;助手版本 skill 改造后端接口（Plan 4）需先合入再做 Task 5。
- **验证**：vitest 单测 + 真实浏览器三角色（不可用 curl 替代，CLAUDE.md 硬性要求）。
