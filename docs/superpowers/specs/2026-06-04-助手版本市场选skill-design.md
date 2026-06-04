# 助手版本从「市场」选 skill（平台库 + ClawHub）设计

- 日期：2026-06-04
- 状态：设计完成，待写实现计划
- 关联：[[2026-06-03-skill市场-design]]（skill 市场与实例级 skill 管理基础设计）

## 1. 背景与目标

当前助手版本（assistant version）配置 skill 时**只能选平台库（platform）skill**——
`AssistantVersionService.AddSkillFromLibrary` 硬编码只接受 `source==platform`，这是
skill 市场 v1 的有意取舍（注释写明「clawhub 留 per-app 安装」）。

诉求：**助手版本可选的 skill，要与实例侧技能市场看到的是同一份内容**——即平台库 +
ClawHub 聚合市场，带搜索、来源筛选、详情与版本列表，而不是一个平铺下拉。平台管理员把
市场上的 skill（含 ClawHub）选进助手版本、锁定一个具体版本，作为该版本所有实例的种子。

成功标准：
- 平台管理员在助手版本详情页能像实例市场一样浏览/搜索/筛选平台库 + ClawHub skill，
  把任意一个（锁定具体版本）加入版本。
- 加入的 ClawHub skill 在版本快照中自包含、抗上游下架（加入时即下载缓存到本项目 S3）。
- 切到该版本的实例，通过既有种子链路自动继承这些 skill 并在 hermes 中生效。
- 平台库 skill 的加入体验统一并入同一市场浏览器（不再单独维护平台库下拉）。

## 2. 现状链路调研结论

下游交付链路**已完全来源无关、格式无关**，无需改动：

| 环节 | 文件 | 现状 |
|---|---|---|
| 版本快照 | `assistant_versions.skills_json`（`internal/migrations/000001_baseline.up.sql`） | 自包含快照数组，`source`/`source_ref`/`cached_path`/`file_size`/`file_sha256` 字段来源无关 |
| 种子注入 | `internal/worker/handlers/seed_version_skills.go` | 解析 skills_json，按 name 并集写 `app_skills`，`CachedTarPath` 直接透传版本快照的 `cached_path`（不重新下载） |
| bootstrap 交付 | `internal/service/bootstrap_service.go:306` `presignSkills` | 从 `app_skills` 取，`RelPath = resources/skills/<app_skills.name><ext>`，`ext` 取自 `CachedTarPath`（兼容 `.tar`/`.zip`） |
| pod 解包 | `runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py:109` `_extract_version_skills` | 按归档 suffix `.zip`→`_extract_zip` / 其余→`_extract_tar`，解到 `skills_root/<name>/` |

关键结论：**版本 skill 始终经 `app_skills` 下发**（注释「来源已从 version.SkillsJson 切换为
app_skills（P4-T3）」），且 `app_skills.name` 决定交付文件名、cached 路径扩展名决定解包方式。
ClawHub 的 `.zip` 全程已被支持。

ClawHub 缓存能力**已存在**于 `AppSkillService.Install`（`internal/service/app_skill_service.go`）：
下载 zip（`ClawHubDownloader.Download(slug, version)`）→ `blobs.PutLibrarySkill(source, ref,
version, "zip", archive)` 缓存到 `library/clawhub/<slug>/<version>.zip` → 本地算 sha256 →
写 `app_skills.cached_tar_path`。版本流程可直接复刻这段。

`AssistantVersionService` 当前依赖：`store / images / models / platformSkills`
（`assistant_version_service.go:74-89`）。platform 路径（`AddSkillFromLibrary` 行 446-453）
直接引用 `ps.TarPath`（平台库 blob 由 `PlatformSkillService.Upload` 持久化，不下载），故
AVS 现在没有 blob 写入器与 clawhub 下载器。

## 3. 设计

### 3.1 架构 / 组件边界（前端）

从 `web/src/components/SkillManager.vue` 的「技能市场」tab 提取出独立、可复用的
**`SkillMarketBrowser.vue`** 组件，承载：卡片网格、关键词搜索、来源筛选（全部/平台库/
ClawHub）、滚动加载（IntersectionObserver）、详情抽屉（作者/统计/许可/关键词/完整描述 +
版本列表含 changelog）。组件自持市场查询（`useSkillMarketQuery`）与详情查询
（`useSkillDetailQuery`）。

对外接口：

- **props**
  - `existingNames: Set<string>` —— 已存在的 skill 名（实例侧=已安装、版本侧=已配置），
    命中则展示「已存在」标记、隐藏操作按钮。
  - `actionLabel: string` —— 主操作文案（实例=`安装`、版本=`添加`）。
  - `actionPending: boolean` —— 主操作 loading。
  - `canAction: boolean` —— 是否有权限展示操作按钮。
  - `allowVersionPick: boolean` —— 为 true（版本场景）时，详情抽屉每个版本行额外渲染
    「添加此版本」按钮，允许锁定旧版；为 false（实例场景）时只用卡片操作装最新版。
- **emit**
  - `action({ source, source_ref, name, version })` —— 卡片操作带最新版；版本场景下
    详情抽屉「添加此版本」带该行的具体版本号。

两个消费方：

- `SkillManager.vue`（实例）：`actionLabel="安装"`、`allowVersionPick=false`、
  `existingNames=installedNames`、`@action=onInstall`。行为与现状一致。
- `AssistantVersionsPage.vue`（版本）：`actionLabel="添加"`、`allowVersionPick=true`、
  `existingNames=` 本版本已配 skill 名集合、`@action=onAddToVersion`。

> SkillManager.vue 的「已安装」tab（含上一提交新增的来源筛选与数量统计）保持不变，
> 只把「技能市场」tab 的内容替换为 `<SkillMarketBrowser>`。

### 3.2 后端

`AddSkillFromLibrary`（`internal/service/assistant_version_service.go:418`）解除 platform-only：

- `source==platform`：维持现状——`GetPlatformSkillByNameVersion(name, version)` →
  快照引用已存在的 `library/platform/<name>/<version>.tar`，不下载。
- `source==clawhub`：复刻 `AppSkillService.Install` 的归档获取与缓存——
  `ClawHubDownloader.Download(slug, version)` → `blobs.PutLibrarySkill(clawhub, slug,
  version, "zip", archive)` 缓存到 `library/clawhub/<slug>/<version>.zip` → 本地算 sha256 →
  追加 `AssistantVersionSkill{Source:"clawhub", SourceRef:slug, Name:displayName, Version,
  CachedPath:relPath, FileSize, FileSha256}`。
- `source` 非 `{platform, clawhub}`：返回明确的「不支持的来源」错误。
- 同 name 冲突检查、revision+1、persistSkills 不变。
- ClawHub 下载失败（上游下架/抖动，如已知坏包返回 15 字节「Download failed」）→ 返回明确
  错误，**不把该 skill 写进版本**（保持版本快照只含可成功获取的 skill）。

**`name` 取值**：与 per-app clawhub 安装一致，用市场条目的 `name`（ClawHub 为 displayName，
可含空格，如 "Skill Vetter"）。bootstrap 按 `app_skills.name` 生成 `resources/skills/<name>.zip`，
render_skills 据此命名目录，hermes 仍按 SKILL.md frontmatter 名注册——此机制在 per-app
clawhub 安装已实测可用。

**依赖新增**：`AssistantVersionService` 结构体加 `clawhub ClawHubDownloader` 与
`blobs LibraryBlobStore`（提供 `PutLibrarySkill`）两个字段；`NewAssistantVersionService`
增对应参数；组装根复用 `AppSkillService` 所用的同两个实例（同一 `ClawHubDownloader` /
`LibraryBlobStore`）。

**Handler / DTO**：`internal/api/handlers/assistant_versions.go` + `dto.go` 的
`AddSkillFromLibrary` 请求体 `source` 校验白名单由「仅 platform」放宽为 `{platform, clawhub}`；
clawhub 下载失败的 HTTP 状态码**对齐 per-app 安装 clawhub 下载失败的现有映射**
（`AppSkillService.Install` 的 handler），保持两处一致而非另定。

**数据库**：零改动。skills_json 自包含、字段全通用。

**OpenAPI**：请求/响应类型结构未变（`source` 早已是 string 字段），预计 `openapi.yaml`/
`generated.ts` 不变；实现后仍跑 `make openapi-gen` + `make web-types-gen` + `make openapi-check`
确认工作区干净。

### 3.3 版本锁定 UX

每个加入版本的 skill 必须锁定一个具体版本（快照可复现，与平台库一致）：

- 市场卡片「添加」=加入该 skill 的**最新版**（卡片展示的 version）。
- 点卡片进详情抽屉，版本列表每行（来自 ClawHub `Versions()` / 平台库多版本）有
  「添加此版本」按钮，可锁定**旧版**。
- 平台库 skill 统一走该市场浏览器，不再单独维护平台库专属下拉。

### 3.4 数据流（端到端）

```
平台管理员 在助手版本详情页 SkillMarketBrowser 选 skill + 版本
  → POST /api/v1/assistant-versions/:id/skills { source, source_ref, version }
  → AddSkillFromLibrary:
       platform → 引用 library/platform/<name>/<ver>.tar
       clawhub  → 下载 zip → PutLibrarySkill → library/clawhub/<slug>/<ver>.zip + sha
  → 追加 AssistantVersionSkill 到 skills_json，revision+1
（实例切到该版本时）
  → seedVersionSkills 并集注入 app_skills（CachedTarPath = 快照 cached_path）
  → bootstrap.presignSkills 预签名 → resources/skills/<name>.<ext>
  → render_skills 按 .tar/.zip 解包到 skills_root/<name>/
  → hermes reload 生效
```

### 3.5 错误处理

- ClawHub 下载失败：service 返回错误，handler 映射 4xx，前端 toast 明确文案
  （「该 skill 在 ClawHub 暂不可下载」），版本不变。
- source 非法：400/422。
- name 冲突：沿用现有「版本内 skill 名唯一」错误。
- 平台库 skill 未找到：沿用 `ErrPlatformSkillNotFound` → 404。

### 3.6 权限

助手版本管理为平台管理员专属（现状 handler 已 gate）。市场浏览
（`SkillLibraryService.List`）要求 `CanViewPlatformSkillMarket`（所有登录用户可浏览），
平台管理员满足。无新增权限谓词。

## 4. 测试策略

- **后端单测**（testify，`assert`/`require`，每条用例带中文场景注释）：
  - `AddSkillFromLibrary` clawhub 成功：下载→缓存→快照字段（source/source_ref/name/
    version/cached_path/file_sha256）正确，revision+1。
  - clawhub 下载失败 → 返回错误且 skills_json 不变。
  - source 非法 → 返回不支持错误。
  - name 冲突 → 返回冲突错误。
  - 维持现有 platform 用例通过。
- **前端单测**（vitest）：
  - 新增 `SkillMarketBrowser.spec.ts`，承接原 `SkillManager.spec.ts` 的市场相关用例
    （卡片渲染、来源徽章、来源筛选、滚动加载、详情抽屉版本列表、已存在标记、操作 emit）。
  - 新增覆盖：`allowVersionPick=true` 时详情抽屉版本行「添加此版本」emit 带具体版本。
  - `SkillManager.spec.ts` 精简为已安装列表用例 + stub `<SkillMarketBrowser>`。
  - 新增 `AssistantVersionsPage` 市场添加流程用例（选 skill→调 useAddVersionSkill）。
- **真实浏览器验证**（本地 k3d，三角色）：平台管理员在助手版本配 platform + clawhub
  skill（含锁旧版）→ 新建/切换实例继承种子 → 实例已安装列表对账 active → hermes 中可用。

## 5. 非目标

- 不支持「自定义上传 skill 包直接进版本」（用户确认只要「与实例市场同款内容」）。
- 不引入新 skill 来源、不改 `SkillSource` 抽象。
- 不改种子（`seedVersionSkills`）、bootstrap、render_skills（已全通用）。
- 不改 `app_skills` / 数据库 schema。
- 不改 SkillManager「已安装」tab（含来源筛选/数量统计）。

## 6. 触碰文件清单

| 层 | 文件 | 改动 |
|---|---|---|
| 前端组件 | `web/src/components/SkillMarketBrowser.vue` | **新增**：从 SkillManager 市场 tab 提取 |
| 前端组件 | `web/src/components/SkillManager.vue` | 市场 tab 改用 `<SkillMarketBrowser>`；已安装 tab 不动 |
| 前端页面 | `web/src/pages/platform/AssistantVersionsPage.vue` | 平台库下拉换成 `<SkillMarketBrowser>`（添加+锁版） |
| 前端 hooks | `web/src/api/hooks/useAssistantVersions.ts` | 大概率不变（`AddVersionSkillInput` 已含 source/source_ref/version），仅调用方传 `source:"clawhub"`；如类型缺 source 则补 |
| 前端测试 | `web/src/components/SkillMarketBrowser.spec.ts` | **新增**：承接市场用例 |
| 前端测试 | `web/src/components/SkillManager.spec.ts` | 精简为已安装用例 + stub 子组件 |
| 后端 service | `internal/service/assistant_version_service.go` | `AddSkillFromLibrary` 支持 clawhub；AVS 加 2 依赖 |
| 后端组装 | service 组装根（`NewAssistantVersionService` 调用处） | 注入 ClawHubDownloader + LibraryBlobStore |
| 后端 handler | `internal/api/handlers/assistant_versions.go` / `dto.go` | source 白名单放宽 + 错误映射 |
| 后端测试 | `internal/service/assistant_version_service_test.go` | clawhub 分支用例 |

## 7. 风险与缓解

- **风险**：ClawHub 部分坏包下载失败（已知 metadata=null 的 skill，如 polymarket，
  下载返回 15 字节）。
  **缓解**：加入时即下载，失败直接报错不入版本——把问题暴露在配置期而非实例运行期。
- **风险**：市场 UI 提取后两处行为漂移。
  **缓解**：单一 `SkillMarketBrowser` 组件 + 共享单测，实例/版本只注入 props，无逻辑分叉。
- **风险**：clawhub displayName 含空格作为目录名。
  **缓解**：与 per-app clawhub 安装同机制，已实测可用，不另行处理。
