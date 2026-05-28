# 组织成员实例菜单拉平设计

## 背景

组织成员只有一个归属实例。当前成员需要先进入「实例」，再在实例详情页顶部 tab 中切换「概览」「任务」「定时任务」「渠道」「实例知识库」「工作目录」。这层实例入口对成员来说信息层级偏深，也让「知识库」和「实例知识库」的关系不够直观。

本次设计将组织成员唯一实例的核心 tab 拉到左侧菜单，减少一次导航层级，并统一组织级知识库的命名。

## 目标

- `org_member` 左侧菜单直接展示唯一实例的主要能力入口。
- 原实例详情「概览」合并到成员「总览」入口。
- 原实例详情「实例知识库」在成员左侧菜单中命名为「个人知识库」。
- 原左侧「知识库」统一改名为「企业知识库」，适用于组织成员和组织管理员视角。
- 管理员视角保持现有实例列表和详情 tab 工作流。

## 非目标

- 不新增成员专用一级 URL，如 `/tasks`、`/cron`、`/channels`。
- 不改变后端 API、权限模型或数据结构。
- 不调整平台管理员和组织管理员的实例详情信息架构。
- 不重构实例 tab 内部页面的业务逻辑。

## 已确认方案

采用角色感知菜单改造方案。

`org_member` 的左侧菜单改为：

1. `总览`
2. `任务`
3. `定时任务`
4. `渠道`
5. `个人知识库`
6. `工作目录`
7. `企业知识库`
8. `用量`

实例相关入口继续复用现有动态路由：

- `总览` -> `/apps/:appId/overview`
- `任务` -> `/apps/:appId/kanban`
- `定时任务` -> `/apps/:appId/cron`
- `渠道` -> `/apps/:appId/channels`
- `个人知识库` -> `/apps/:appId/knowledge`
- `工作目录` -> `/apps/:appId/workspace`

如果成员没有实例，实例相关入口统一指向 `/apps/empty`。

`企业知识库` 继续使用 `/knowledge`，`用量` 继续使用 `/usage`。

## 角色行为

### 组织成员

- 访问 `/` 时直接进入唯一实例的 overview 页面；无实例时进入 `/apps/empty`。
- 左侧菜单不再显示「实例」父入口。
- 进入 `/apps/:appId/...` 页面时，实例详情页顶部 tab 隐藏，避免与左侧菜单重复。
- 左侧菜单高亮根据当前 `/apps/:appId/<tab>` 的末段决定：
  - `overview` 高亮「总览」
  - `kanban` 高亮「任务」
  - `cron` 高亮「定时任务」
  - `channels` 高亮「渠道」
  - `knowledge` 高亮「个人知识库」
  - `workspace` 高亮「工作目录」

### 组织管理员

- 保持「实例」入口和实例详情顶部 tab。
- 左侧「知识库」改名为「企业知识库」。
- 首页快捷卡片中的组织知识库入口文案同步为「企业知识库」。

### 平台管理员

- 保持现有平台控制台和实例详情行为。
- 不新增成员专属菜单项。

## 组件设计

### `DashboardLayout.vue`

`DashboardLayout.vue` 继续作为左侧菜单的唯一生成位置。

- 复用 `useMemberApp()` 获取成员唯一实例 ID。
- 当 `auth.isOrgMember` 为 true 时，生成成员专属菜单项。
- 成员实例菜单项的 key 使用现有 `/apps/:appId/...` 路径；无实例时使用 `/apps/empty`。
- `activeKey` 对成员按当前路由末段映射到对应实例菜单项，而不是把所有 `/apps` 路由都归到「实例」。
- 非成员继续使用现有菜单结构，仅将「知识库」文案改为「企业知识库」。

### `AppDetailPage.vue`

`AppDetailPage.vue` 继续负责加载实例基础信息并向子页面 provide `app`。

- 对 `org_member` 隐藏顶部 tab 导航。
- 对 `org_admin` 和 `platform_admin` 保留顶部 tab 导航。
- 管理员视角中 `knowledge` tab 仍显示「实例知识库」。
- 成员视角的「个人知识库」只体现在左侧菜单文案上，避免改动页面内部业务语义。

### `RoleAwareHome.vue`

`RoleAwareHome.vue` 继续负责角色首页跳转。

- `platform_admin` 保持跳转 `/console`。
- `org_admin` 保持跳转 `/org-console`。
- `org_member` 改为跳转唯一实例 overview；无实例时跳转 `/apps/empty`。
- 组织管理员首页卡片文案同步使用「企业知识库」。

### `router.ts`

路由表保持现有结构。

- `/apps` 对 `org_member` 的兼容重定向保留。
- `/apps/:appId/overview|kanban|cron|channels|knowledge|workspace` 继续承载成员实例页面。
- 不新增成员专用别名路由，避免重复维护权限和空实例处理。

## 测试计划

### 单元测试

- `DashboardLayout.spec.ts`
  - 覆盖 `org_member` 菜单显示拉平后的入口。
  - 覆盖 `org_member` 在 `/apps/:appId/kanban` 等路径下高亮对应菜单。
  - 覆盖菜单文案使用「企业知识库」。

- `AppDetailPage.spec.ts`
  - 覆盖 `org_member` 不显示顶部 tab。
  - 覆盖非成员仍显示顶部 tab。

- `RoleAwareHome` 测试
  - 覆盖 `org_member` 首页跳转唯一实例 overview。
  - 覆盖 `org_member` 无实例时跳转 `/apps/empty`。
  - 覆盖组织管理员首页卡片展示「企业知识库」。

建议运行：

```bash
npm --prefix web test -- DashboardLayout AppDetailPage RoleAwareHome
npm --prefix web run typecheck
```

### 浏览器验收

使用真实浏览器以组织成员账号登录后验证：

- 左侧菜单显示 `总览 / 任务 / 定时任务 / 渠道 / 个人知识库 / 工作目录 / 企业知识库 / 用量`。
- `总览` 直接显示唯一实例概览内容。
- `任务 / 定时任务 / 渠道 / 个人知识库 / 工作目录` 能进入对应现有实例页面。
- 实例详情页顶部不再显示重复 tab。
- `企业知识库` 命名正确，并仍进入组织级知识库页面。

## 风险与边界

- `useMemberApp()` 依赖组织实例列表查询。若成员实例尚未初始化或查询失败，菜单会落到 `/apps/empty`，与现有成员 `/apps` 兼容入口一致。
- 成员菜单显示「个人知识库」，但页面内部仍使用应用级知识库 API 和组件；这是命名层面的角色化呈现，不改变数据边界。
- 管理员继续看到「实例知识库」tab，以保留企业多实例管理语义。
