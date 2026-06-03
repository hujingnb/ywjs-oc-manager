# Hermes Skill 市场与实例级 skill 管理 — 设计文档

## 背景与目标

现状 skill 绑定在「助手版本（assistant_version）」上：平台管理员上传 tar，存 manager
对象存储，容器启动时由 `render_skills.py` 解压到 `data/skills/`（带 `.oc-managed`
标记）。同一版本的所有实例拿到完全相同的 skill，用户无法自选；hermes 运行时自己创建
的 skill（写在 `data/skills/` 无标记目录）manager 完全不感知，而 k8s 下 `data` 是
emptyDir、换镜像走 `Recreate`，这些自创 skill 会随 pod 重建丢失。

本设计把 skill 管理下沉到**实例（app）级**，引入**可扩展来源的 skill 库（市场）**，
并补齐 **hermes 自创 skill 的备份/恢复**，目标：

1. 企业成员能在左侧菜单「技能」页浏览自己实例**已安装的全部 skill**；平台/企业管理员
   能在**实例详情页「技能」tab** 代管某个实例的 skill。两入口共用一套组件，数据同源
   （per-app）。
2. 能浏览 **skill 市场**（平台库 + ClawHub 公共库）并一键安装。
3. hermes 自创的 skill 自动备份，换镜像/重建实例时恢复（市场拿不到，必须备份）。
4. 来源做成抽象，未来可接入更多公共库。

## 术语

- **skill 库 / 市场**：可浏览、可安装的 skill 集合，由多个**来源（source）**聚合。
- **来源 source**：`platform`（平台库，管理员上传维护）、`clawhub`（ClawHub 公共
  registry，开源 [openclaw/clawhub](https://github.com/openclaw/clawhub)，国内站
  `clawhubcn.com`，公开 REST API 无需 key），未来可扩展。
- **实例 skill（app_skills）**：安装到某个 app 的 skill，是运行时唯一的 skill 来源。
- **版本 skill 种子**：助手版本配置的 skill，仅在**实例创建/换版本**时注入成实例 skill。
- **自创 skill**：hermes 运行时自己在 `data/skills/` 下创建、无 `.oc-managed` 标记、
  也不在镜像内置清单中的 skill。
- **`name`**：skill 解压目录名 `data/skills/<name>/`，实例内**物理唯一**。
- **`source_ref`**：来源内精准标识，`platform→name`、`clawhub→slug`，用于回源查更新。

## 已确认决策

1. **归属**：skill 下沉到 per-app；成员自助（左侧菜单）+ 管理员代管（实例详情 tab），
   权限分层、数据同源。
2. **skill 库为中心枢纽**，来源可扩展抽象，每个 skill 标注来源。
3. **助手版本 skill = 实例创建/换版本/重启时的「种子」**：注入成实例级 `app_skills`；
   改版本配置不自动推送到已建实例，而复用现有 `apps.version_synced`「需重启」机制——版本
   skill 变更使实例 `version_synced=false` 显示「需重启」，用户点「重启」时把版本新增的
   skill 并集注入。无自动批量重启。
4. **换助手版本**：版本里实例未安装的 skill 自动安装；已安装的不删、不覆盖；旧版本残留
   不删（**并集**语义）。去重按 `name`；**当前版本必需的 skill 禁止删除**。
5. **换镜像/重建**：`app_skills`（实例级，统一）从对象存储缓存恢复；**自创 skill** 由
   `oc-sync` 备份、`oc-restore` 恢复；镜像内置由新镜像自带。
6. **识别自创**：镜像构建期生成 `/opt/skills-builtin.json` 内置清单；`oc-sync` 只备份
   「不在内置清单 且 无 `.oc-managed`」的目录。
7. **公共库**：首批接 ClawHub 适配器 + 平台库并存；公共库浏览数据 **Redis 缓存、不落库**。
8. **安装管控**：成员可自由浏览安装（含公共库）+ 全程审计。
9. **打包格式**：容器 `render_skills.py` 改为**同时支持解 tar 与 zip**（ClawHub 下载为
   zip，原样落地不转换）；zip 必须补齐路径穿越（zip slip）安全校验。
10. **版本管理**：安装即锁定具体版本，换镜像恢复确定；升级显式（提示后手动触发）；
    每个安装版本的内容缓存在对象存储，**抗上游下架**；平台库上传新版本为新增不覆盖。
11. **数据模型**：平台库落库；ClawHub 不落库（Redis 缓存）；`app_skills` 自包含快照，
    不引用库 id。

## 整体架构

```
                          ┌─────────────────────────────────────┐
   平台管理员 ── 上传 ──▶  │ skill 库（中心枢纽）                 │
                          │  SkillSource 抽象                    │
                          │   • PlatformSource（platform_skills │
                          │     落库 + 对象存储 tar）            │
                          │   • ClawHubSource（REST 适配，Redis │
                          │     缓存，不落库）                   │
                          │   • 未来：GitHub marketplace.json…  │
                          └───────┬───────────────┬─────────────┘
                  从库选 skill     │               │  从市场装 skill
            ┌────────────────────┘               └───────────────────┐
            ▼                                                          ▼
   助手版本 skills_json（自包含快照）              app_skills（实例级，自包含快照）
            │ 实例创建/换版本注入                              ▲   ▲
            └─────────────────────────────────────────────────┘   │ 成员/管理员安装
                                                                   │
   容器运行时 data/skills/ ＝ app_skills（统一） + 镜像内置 + hermes 自创
                                            └ oc-sync 备份 / oc-restore 恢复（自创）
```

三个消费方：①平台管理员配助手版本（从库选）②成员/管理员 per-app 安装（从市场选）
③自创 skill 备份恢复（不走库）。

## skill 来源抽象（SkillSource）

```go
// SkillSource 抽象一个 skill 来源，使市场聚合与未来扩展与具体来源解耦。
type SkillSource interface {
    Kind() string // "platform" | "clawhub"
    // List/Search 返回市场条目（含 name/description/version/source_ref/metadata）。
    List(ctx context.Context, q SkillQuery) ([]SkillEntry, error)
    // Get 取单个 skill 指定版本的元数据。
    Get(ctx context.Context, ref string, version string) (SkillEntry, error)
    // LatestVersion 回源查最高版本，供更新检测。
    LatestVersion(ctx context.Context, ref string) (string, error)
    // FetchArchive 下载指定版本的归档（tar 或 zip 原样字节）+ 媒体类型。
    FetchArchive(ctx context.Context, ref, version string) (data []byte, ext string, err error)
}
```

- **PlatformSource**：基于 `platform_skills` 表 + 对象存储 tar。
- **ClawHubSource**：调 ClawHub v1 REST（`GET /api/v1/search`、`/api/v1/skills`、
  `/api/v1/skills/{slug}`、`/api/v1/skills/{slug}/versions`、
  `/api/v1/download?slug=&version=`），List/Get 结果按 query 维度 **Redis 缓存
  TTL（约 10 min）**；`FetchArchive` 返回 zip 原样字节。出网走 pod 既有 proxyEnv。

## 数据模型

> 建表规范：所有新表遵循项目 MySQL 约定（`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
> COLLATE=utf8mb4_0900_ai_ci`），**每个字段与表本身都必须带 SQL `COMMENT`**。

### platform_skills（新增，落库）

平台库 skill，平台管理员维护，可浏览、可被助手版本/实例引用。

```sql
CREATE TABLE platform_skills (
    id            CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    name          VARCHAR(128) NOT NULL                 COMMENT 'skill 名，等于容器内解压目录名',
    description   TEXT         NOT NULL                 COMMENT 'skill 描述，市场展示用',
    version       VARCHAR(64)  NOT NULL                 COMMENT '语义版本号',
    tar_path      VARCHAR(512) NOT NULL                 COMMENT '对象存储相对路径 library/platform/<name>/<version>.tar',
    file_size     BIGINT       NOT NULL                 COMMENT 'tar 字节大小',
    file_sha256   CHAR(64)     NOT NULL                 COMMENT 'tar 内容 SHA256，完整性校验',
    metadata_json JSON         NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '附加元数据（作者、标签等）',
    uploaded_by   CHAR(36)     NOT NULL                 COMMENT '上传者 user id（平台管理员）',
    created_at    TIMESTAMP    NOT NULL DEFAULT NOW()   COMMENT '创建时间',
    PRIMARY KEY (id),
    UNIQUE KEY platform_skills_name_version (name, version)  -- 多版本共存，新增不覆盖
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='平台库 skill，平台管理员维护，多版本共存';
```

### app_skills（新增，自包含快照）

实例级 skill 安装清单，运行时唯一来源。**不引用库 id**，所有信息自包含，保证来源下架后
仍可恢复与展示。

```sql
CREATE TABLE app_skills (
    id              CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    app_id          CHAR(36)     NOT NULL                 COMMENT '所属实例 app id',
    name            VARCHAR(128) NOT NULL                 COMMENT '解压目录名，实例内唯一，去重键',
    source          VARCHAR(32)  NOT NULL                 COMMENT '来源：platform | clawhub',
    source_ref      VARCHAR(256) NOT NULL                 COMMENT '来源内精准标识：platform=name、clawhub=slug，回源查更新用',
    version         VARCHAR(64)  NOT NULL                 COMMENT '锁定的当前安装版本',
    latest_version  VARCHAR(64)  DEFAULT NULL             COMMENT '定时任务回源所得最高版本，大于 version 即有更新',
    cached_tar_path VARCHAR(512) NOT NULL                 COMMENT '对象存储缓存路径，恢复走它（确定性 + 抗下架）',
    source_metadata JSON         NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '安装时来源完整元数据快照，后台展示用（抗下架）',
    file_size       BIGINT       NOT NULL                 COMMENT '归档字节大小',
    file_sha256     CHAR(64)     NOT NULL                 COMMENT '归档内容 SHA256',
    installed_by    CHAR(36)     NOT NULL                 COMMENT '安装者 user id',
    installed_at    TIMESTAMP    NOT NULL DEFAULT NOW()   COMMENT '安装时间',
    last_checked_at TIMESTAMP    DEFAULT NULL             COMMENT '定时任务上次回源检查时间',
    PRIMARY KEY (id),
    UNIQUE KEY app_skills_app_name (app_id, name),  -- name 物理唯一 = 去重键
    KEY app_skills_app (app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='实例级 skill 安装清单，自包含快照，运行时唯一来源';
```

去重键是 `name`（容器目录物理唯一）。`(source, source_ref)` 仅用于回源查更新，不参与去重；
两个来源不同但 name 相同的 skill 视为冲突，后到者被拒并提示「已有同名 skill」。

### ClawHub 公共库（不落库）

浏览/搜索/元数据实时调 ClawHub API，结果写 **Redis 缓存 TTL**，无业务表。被安装时其内容
缓存进对象存储、并在 `app_skills` 落一条自包含快照。

### assistant_versions.skills_json 改造

从「裸 tar 引用」`[{name,file_path,file_size,file_sha256}]` 改为**自包含快照**（与
app_skills 一致，作为实例创建时的种子）：

```json
[{"source":"clawhub","source_ref":"weather","name":"weather","version":"1.5",
  "cached_tar_path":"library/clawhub/weather/1.5.zip","file_sha256":"abc...","source_metadata":{}}]
```

平台管理员编辑版本页时对每个 skill **实时回源查最高版本**显示更新提示（版本量小，不上定时
任务）。`revision` 维持现有「容器相关变更 +1」语义。

## 版本 skill = 实例种子模型

版本 skill 通过统一的「并集注入」`syncVersionSeed(app)` 应用到实例，在**三个时机**触发：
**实例创建**、**换助手版本**、**实例重启**。注入逻辑一致——遍历实例当前绑定版本
`skills_json`，按 `name` 判定实例是否已有：

- 未有 → 安装（`FetchArchive` → 缓存 → 落 `app_skills`）；
- 已有 → 保留实例当前版本，**不覆盖**；
- 版本不再包含、但实例已有的 → **不删**。

即并集语义。注入后与版本解耦，运行时只看 `app_skills`。

- **创建（onboarding / app_initialize）**：首次注入当前版本全部 skill。
- **换助手版本（A→B）**：对 B 做并集注入；A 注入但 B 没有的保留不删。
- **改版本配置的传播**：复用现有 `apps.version_synced` 机制。版本 skill 变更使
  `revision +1`，已绑定实例 `version_synced=false`，前端实例列表/详情显示「需重启」。
  用户/管理员点「重启」时，restart 链路在应用新 manifest 的同时执行 `syncVersionSeed`，
  把版本新增 skill 加入实例并生效，随后写回 applied revision。**无自动批量重启**。
- **删除保护**：删除某 `app_skills` 时，实时取 `app.version_id` 对应版本 `skills_json` 的
  name 集合，命中则拒绝（当前版本必需）。用户自装/旧版本残留的可删。
- **种子重装是预期行为**：种子 skill 仅当它已不属于当前版本（未受删除保护）时才可被卸载；
  卸载后若换到一个也包含它的版本，并集注入会按「实例没有就装」把它装回——这是**预期行为**
  （助手版本需要就装回），**不**维护「用户主动卸载」清单。注意单纯重启当前版本不会重装：
  当前版本的 skill 受删除保护无法卸载，非当前版本的 skill 重启不会注入。

## 安装 / 卸载 / 更新 / 版本管理

- **安装**：选定 `source + source_ref + version`（默认 latest）→ `FetchArchive` 取归档
  → 校验 sha256 / 解压防炸弹（解压后总字节 + 文件数上限）/（zip 时）zip slip 安全
  → 缓存到对象存储共享前缀
  `library/<source>/<source_ref>/<version>.{tar,zip}`（同一 skill 多 app 只存一份）
  → 落 `app_skills` 自包含快照 → **oc-ops 热装**：解压进容器 `/opt/data/skills/<name>`
  （带 `.oc-managed` 标记）→ **触发 hermes `reload-skills`** → 当前对话下一轮生效 → 写审计。
  失败回滚已落记录、状态置 `pending`。
- **卸载（manager 管理类）**：删除保护校验 → 删 `app_skills` 行（共享缓存 tar 不随之删，
  可由独立 GC）→ **oc-ops 热删** `/opt/data/skills/<name>` → 触发 `reload-skills`
  （自动 `removed`）→ 写审计。
- **卸载（hermes 自创类）**：删 S3 `apps/<id>/skills/<name>` 备份 → oc-ops 热删容器目录
  `/opt/data/skills/<name>` → 触发 `reload-skills` → 写审计。hermes 内置类不可卸载。
- **更新检测（定时任务，复用现有 scheduler）**：按 `(source, source_ref)` 去重批量回源
  查最高版本，写回 `app_skills.latest_version` + `last_checked_at`。前端比较
  `version < latest_version` 显示更新。
- **更新执行**：用户/管理员显式触发 → 取目标版本归档覆盖缓存引用与 `app_skills.version`
  → oc-ops 热替换容器目录 → 触发 `reload-skills` → 审计。
- **抗下架**：每个版本内容缓存自持，上游删除不影响已装实例的恢复与展示。

## 已安装列表：实时对账与状态

「已安装」列表**实时通过 oc-ops「列 skills」端点枚举容器内实际 skill**，与 manager 的
`app_skills` 表（期望）对账，每条带 **`status`**，暴露「期望 vs 实际」不一致：

- oc-ops 列 `/opt/data/skills/` 实际目录（name + 是否带 `.oc-managed` + SKILL.md 元数据）。
- 与 `app_skills` 表 + 镜像内置清单（按 `image_id`，注册时登记进 manager）对账算 `status`：

| status | 条件（app_skills 期望 × 容器实际） | 类别 / UI |
|---|---|---|
| `active` 已生效 | 有 × 有 | manager 管理，正常（可卸/更新，受删除保护） |
| `pending` 待生效 | 有 × 无 | manager 管理，已记录但容器还没有（热装/reload 失败、恢复中）→ **「重新安装」按钮** |
| `builtin` 内置 | 无 × 有 + 在内置清单/oc-kb | 「hermes 内置」，只读 |
| `self_created` 自创 | 无 × 有 + 非内置无 `.oc-managed` | 「hermes 自创」，可卸载 |

- **重试**：`pending` 长时间未转 `active`（热装/reload 没成功），UI 给「重新安装」按钮，
  重新执行热装 + reload。
- **容器停机 fallback**：oc-ops 不可达时退化为按 `app_skills` 表展示、状态标 `unknown`
  （实例未运行），hermes 内置/自创枚举不出并提示。

## 备份 / 恢复机制

### 镜像构建期

生成 `/opt/skills-builtin.json`，列出镜像内置 skill 名单，作为「识别自创」的基线；同时在
镜像注册/构建时**登记进 manager**，供「已安装列表」展示 hermes 内置类。

### oc-sync（sidecar，持续，改造）

在现有 `workspace/sessions/weixin/state.db` 同步之外，新增：扫描 `data/skills/` 下
**「不在 `skills-builtin.json` 且无 `.oc-managed`」** 的目录（= 自创 skill），增量同步到
S3 `apps/<id>/skills/`。`oc-presync`（preStop）做最终一次，防最后时刻丢失。

### oc-restore（initContainer，改造）

- 读 **app_skills 清单**（由 bootstrap 下发，替代旧 version.skills），按 `cached_tar_path`
  预签名 URL 下载归档到 `input/resources/skills/`（文件名扩展名 `.tar`/`.zip`）。
- **恢复自创 skill**：从 S3 `apps/<id>/skills/` 拉回 `data/skills/`。
- workspace/sessions/weixin/state.db 维持现状。

### render_skills.py（entrypoint，改造）

- `_wipe_managed_skills` 清掉带 `.oc-managed` 的目录（上次安装），再解压 app_skills 归档
  到 `skills/<name>/` 并打标记；**按扩展名/magic 分流 tarfile 与 zipfile**，zip 路径
  逐条做穿越/绝对路径/symlink 校验（补 `filter="data"` 缺失的防护）。
- 镜像内置（无标记）与已恢复的自创（无标记）一律不碰。

### bootstrap 契约改造

manager 生成 app bootstrap 时，skill 列表来源由「version.skills」改为「app_skills」，
下发各条的预签名下载 URL + name + 媒体类型。

## 缓存布局

`app_skills.cached_tar_path` 与 `platform_skills.tar_path` 共用对象存储**共享前缀**
`library/<source>/<source_ref>/<version>.{tar,zip}`，同一 skill 同版本被多个 app 安装
只存一份，恢复用预签名 URL（复用现有 version skill 的预签名机制）。

## 权限设计

集中在 `internal/auth/authorizer.go`，延续 platform_admin / org_admin / org_member 三层：

| 操作 | 谓词 | 允许角色 |
|---|---|---|
| 平台库上传/编辑/删（platform_skills） | `CanManagePlatformSkill` | platform_admin |
| 助手版本配 skill（从库选） | 现有 `CanManageAssistantVersion` | platform_admin |
| per-app 浏览/安装/卸载/更新 | `CanManageAppSkill(appID)` | app owner（成员本人）+ 本 org 的 org_admin + platform_admin（复用 `CanWriteAppKnowledge` 同款规则） |
| 删除当前版本必需的 skill | —（service 内实时判定） | 任何角色均不可 |

## 接口设计

请求体类型放 `internal/api/handlers/dto.go` 并导出；响应用 `service.XxxResult`。

### 平台库（platform_admin）

- `GET    /api/v1/platform-skills` — 列表（按 name 聚合 + 各版本）
- `POST   /api/v1/platform-skills` — 上传 tar（multipart，落表 + 对象存储）
- `DELETE /api/v1/platform-skills/:id` — 删除某版本

### skill 市场（聚合浏览）

- `GET /api/v1/skill-market?source=&q=&cursor=` — 聚合 platform + clawhub，ClawHub 走
  Redis 缓存；返回 name/description/version/source/source_ref/metadata + 安装状态/同名冲突。
- `GET /api/v1/skill-market/:source/:ref` — 详情。

### per-app skill

- `GET    /api/v1/apps/:appId/skills` — 已安装列表：**实时对账**返回每条带 `status`
  （active/pending/builtin/self_created）+ 类别/来源/版本/更新/禁删标记
- `POST   /api/v1/apps/:appId/skills` — 安装（body：source/source_ref/version）
- `DELETE /api/v1/apps/:appId/skills/:name` — 卸载：manager 管理类走删除保护 + 删
  app_skills；hermes 自创类删 S3 备份 + 容器目录；hermes 内置类拒绝
- `POST   /api/v1/apps/:appId/skills/:name/update` — 更新到目标版本（仅 manager 管理类）

成员一人一 app，前端用自身 appId 调用；管理员从实例详情页用目标 appId 调用。

### 助手版本配 skill（改造）

- `POST   /api/v1/assistant-versions/:id/skills` — 从「上传 tar」改为「从库选」（body：
  source/source_ref/version）
- `DELETE /api/v1/assistant-versions/:id/skills/:name` — 维持

### runtime（oc-ops sidecar，容器内，新增）

manager 经 oc-ops 控制 hermes 容器内 skill；oc-ops 与 hermes gateway 同容器、共享
`/opt/data`。

- `GET    /skills` — 列 `/opt/data/skills/` 实际目录（name + `.oc-managed` + SKILL.md
  元数据），供已安装列表对账。
- `POST   /skills` — 热装：把归档解压进 `/opt/data/skills/<name>`（带 `.oc-managed`）。
- `DELETE /skills/:name` — 热删容器目录。
- `POST   /skills/reload` — 触发 hermes `reload-skills`（程序化触发途径见末尾「关键技术
  验证」，具体接口待实现确定）。

## Service 设计

- **SkillLibraryService**：平台库 CRUD；市场聚合（多 SkillSource）；ClawHub 适配 + Redis
  缓存；安装时 `FetchArchive` → 校验 → 缓存到对象存储。
- **AppSkillService**：安装/卸载/更新（**oc-ops 热装/热删 + reload，不重启**）；name 去重；
  删除保护（对照当前版本 skills_json）；种子注入与换版本并集；**已安装列表实时对账**
  （oc-ops 列 skills × app_skills × 内置清单 → status）；自创卸载（删 S3 + 热删）；审计。
- **SkillUpdateChecker**（scheduler 定时任务）：批量回源更新 `latest_version`。
- 改造 **AssistantVersionService**：skill 配置改为引用库快照。
- 改造 **bootstrap**：skill 列表来源改为 app_skills。

## 错误处理

- ClawHub API 不可用 → 市场浏览降级（Redis 缓存兜底 + 提示「公共库暂不可用」），不影响
  平台库与已安装管理。
- zip 安全校验失败（防 zip slip 路径穿越：拒绝含 `..`、绝对路径、越界 symlink 的条目）→
  拒绝安装，提示来源不可信。
- 同名冲突 → 前端禁用安装；后端 `UNIQUE(app_id,name)` 兜底，返回明确错误。
- sha256 不符 / 触发解压防炸弹上限（解压后总字节或文件数超阈值，防 zip bomb 撑爆容器磁盘）
  → 拒绝。**不设 skill 业务大小上限**（平台库可信、ClawHub 自身限 50MB，仅留解压安全底线）。
- 安装中断（已缓存但 app_skills 未落 / 热装或 reload 失败）→ 回滚或状态置 `pending`，UI 给
  「重新安装」重试；缓存可保留。
- oc-ops 不可达（实例未运行）→ 已安装列表 fallback 到 app_skills 表 + 状态 `unknown`。
- 删除当前版本必需 skill → 返回禁止删除错误。

## OpenAPI 与生成类型

新增/改造 handler 后跑 `make openapi-gen` + `make web-types-gen`，保持
`openapi/openapi.yaml` 与 `web/src/api/generated.ts` 与代码同步，连同代码一起提交。

## 前端设计

成员左侧菜单顶级路由 `/skills`（`SkillsPage`，作用于本人 app）与实例详情页 tab
`/apps/:appId/skills`（`AppSkillsTab`）复用同一套组件，参照现有 knowledge 双入口模式。

- **已安装视图**：**实时对账**展示该实例容器内**全部 skill**，每条带 `status`：
  - **manager 管理**（app_skills）：来源徽章 + 版本 + 更新提示与「更新」按钮；当前版本必需
    的 **隐藏「卸载」按钮**、只显示禁删/必需标记（后端拒绝删除作兜底），其余显示「卸载」；
    `status=pending` 显示「待生效 / 重新安装」；点名看详情（source_metadata 快照，下架也能展示）。
  - **hermes 内置**（镜像内置 + oc-kb）：标「hermes 内置」，**只读**。
  - **hermes 自创**（运行时生成）：标「hermes 自创」，**可卸载**。
  - 安装/卸载**即时生效**（reload 后下一轮对话），无重启提示。
- **技能市场视图**：来源筛选（全部/平台库/ClawHub）+ 搜索 + skill 卡片（来源徽章、描述、
  版本/下载数）+「安装」。状态：已安装置灰、`metadata.openclaw` 声明 runtime 依赖时标注
  「⚠ 需 X 依赖」、同名冲突禁装。
- **平台库管理页**（platform_admin）：上传 tar、多版本、编辑、删，入口在平台控制台。
- **助手版本编辑页**：skill 配置改为「从库选」选择器 + 各 skill 更新提示。

## 测试计划

遵循项目规范：testify `assert`/`require`、每用例相邻中文注释、真实浏览器三角色验证。

### 后端单元测试

- SkillLibraryService：市场聚合、ClawHub 适配 + Redis 缓存命中/失效、安装缓存、解压防炸弹
  （解压后总字节/文件数）+ sha256 校验。
- AppSkillService：安装/卸载/更新；**name 去重**（同名不同源拒绝）；**删除保护**（当前
  版本必需禁删、自装可删）；**`syncVersionSeed` 种子并集注入**（创建/换版本/重启三时机：
  未装装入、已装不覆盖、残留不删）。
- SkillUpdateChecker：回源查更新写回 latest_version。
- 权限谓词：CanManagePlatformSkill / CanManageAppSkill 三角色矩阵。

### 容器侧测试

- `render_skills.py`：解 tar 与 **zip**；**zip slip**（越界/绝对路径/symlink 条目）拒绝。
- `oc-sync`：仅备份「不在内置清单 + 无 .oc-managed」目录；`oc-restore`：恢复 app_skills
  与自创。
- oc-ops skill 端点：列/热装/热删 + 触发 `reload-skills`（hermes `reload_skills()` 已实测
  热装即识别，见末尾「关键技术验证」）。

### 前端测试

- 已安装列表（三类标记：manager 管理/hermes 内置只读/hermes 自创可卸载，徽章/更新/禁删/
  卸载渲染）、市场（筛选/搜索/状态）、平台库管理。

### 浏览器验证（真实浏览器，三角色）

成员自助浏览/安装/卸载/更新、换镜像后自创 skill 恢复；管理员实例详情页代管；平台管理员
平台库上传与版本管理。

## 影响范围

- 新增表：`platform_skills`、`app_skills`；改造 `assistant_versions.skills_json` 语义。
- 新增 Service/Source/定时任务；改造 AssistantVersionService、bootstrap、onboarding/
  app_initialize 与 restart 链路（`syncVersionSeed` 种子并集注入，重启时补齐版本新增 skill）。
- 容器侧：`render_skills.py`（解 zip + 安全）、`oc-sync`/`oc-restore`（自创备份恢复 +
  app_skills 恢复）、镜像构建/注册（`skills-builtin.json` 生成 + 登记进 manager）、
  **oc-ops 新增 skill 端点**（列/热装/热删 + 触发 `reload-skills`，必需）。
- 前端：新增技能页/实例 tab/平台库管理页，改造助手版本编辑页。
- 依赖：ClawHub REST（出网走 pod proxyEnv）、Redis（已有）。

## 不做

- 不接 GitHub `marketplace.json` 等其它公共库（抽象已留，作为未来新 SkillSource 实现）。
- 不做公共 skill 的自动安全沙箱/静态扫描（仅 zip bomb/zip slip 安全 + 依赖标注 + 审计）。
- 不做助手版本 skill 更新的「主动推送到在用实例」（种子模型已使其无必要）。
- 不做共享缓存 tar 的自动 GC（先保留，后续按引用计数清理）。

## 关键技术验证（已实测）

**结论：hermes 原生支持运行时 `reload-skills`，热装 skill 后无需重启进程/会话/pod 即生效。**

依据（读 hermes-agent 源码 + 真实容器实测，镜像 `hermes-runtime:v2026.5.16`）：

- hermes gateway 注册 `/reload-skills` slash command（`gateway/run.py` 的
  `_handle_reload_skills_command` → `agent.skill_commands.reload_skills`），rescan
  `$HERMES_HOME/skills/`（= 容器 `/opt/data/skills/`），返回 `added/removed/unchanged`，
  **新 skill 当前对话下一轮（next turn）生效**。
- 实测：容器内放 `SKILL.md` 到 `$HERMES_HOME/skills/my-test-skill/`，调真实 `reload_skills()`
  → 返回 `{'added': [{'name': 'my-test-skill', ...}], 'total': 1, 'commands': 1}`，热装即识别，
  全程不重启。

**待实现确定的工程接口**：manager/oc-ops 如何**程序化触发** `reload-skills`（它是 gateway
slash command，经 MessageEvent 触发）。oc-ops 与 gateway 同容器，候选方案：给 gateway 加本地
控制端点 / 文件信号 / oc-ops 经内部渠道注入 `/reload-skills` 消息——可行性已由实测保证，具体
接口在实现 plan 阶段调研定。
