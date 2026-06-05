# 设计文档：第三方市场 skill 归档 S3 缓存 + 上游失败错误处理

**日期：** 2026-06-06
**状态：** 待用户复核

## 背景

实例 skill 管理页（`/apps/<id>/skills`）的「技能市场」聚合了平台库（`platform`）与 ClawHub 公共库（`clawhub`）两个来源。用户报告：在详情抽屉点「下载」时报「服务器内部错误」。

经浏览器复现 + 上游探测取证，根因已确定：

- 浏览器：`GET /api/v1/skill-market/download?source=clawhub&ref=self-improving&version=1.2.16` → **500** `{"code":"INTERNAL","message":"服务器内部错误"}`。
- 直接探测上游：`GET https://clawhubcn.com/api/v1/download?slug=self-improving&version=1.2.16` → **502 "Download failed"**（15 字节，clawhub 自己的 nginx 网关错误）。
- 对照组：skill-vetter / weather / github / humanizer / stock-analysis 等下载均为 **200 + 合法 zip**（`PK` 开头）。

即：**上游 clawhub 对个别 skill 的下载接口返回 502**，而 manager 的 `clawhub.Download` 把这个上游错误包成普通 `error`，一路冒泡到 `writeSkillMarketError` 的 default 分支，被显示成泛化的 500「服务器内部错误」——把上游故障误报成了 manager 自身的内部错误。

**该错误同样影响安装**（已浏览器实测）：点 self-improving「安装」→ `POST /api/v1/apps/<id>/skills` → **500** `{"code":"INTERNAL_ERROR","message":"服务器内部错误"}`，因为安装与下载走的是同一个 `clawhub.Download`。

与此同时提出一个增强需求：下载 / 安装 skill 时把归档保存到 S3，后续再次读取直接从 S3 取、不再回源；并且这应是一个 source 无关的通用能力，未来所有第三方市场都复用。

## 目标

1. **修复错误处理**：上游市场下载失败（非 2xx / 网络错误）时，下载与安装都返回**明确**的 502 错误（文案「上游技能市场暂时不可用，请稍后重试」），不再误报为泛化 500「服务器内部错误」。
2. **第三方市场归档 S3 缓存**：下载 / 安装 / 助手版本加 skill 成功取到归档后写入对象存储；后续再次读取同一 `(source, ref, version)` 时直接读缓存、不回源。
3. **通用化**：缓存层对来源抽象（按 `(source, ref, version)` 寻址），未来新增第三方市场只需贡献一个「回源闭包」即可自动获得缓存与统一错误语义。

## 非目标

- **不**修复 self-improving 这个具体 skill 的下载——它是 clawhub 上游的故障，manager 无法修。缓存对它无效（从未成功拉取过 → S3 无副本 → 首次仍回源仍撞 502），唯一改进是把错误显示清楚。
- **不**对市场列表做「下载可用性」预探测 / 置灰（按决策②，过度设计）。
- **不**改 `platform` 来源的取数路径——平台库归档本就持久化在 `library/platform/...`、不回源，天然等价于已缓存。
- **不**引入缓存 TTL / 失效刷新（按决策①，按版本永久缓存）。
- **不**改数据库结构。

## 决策（已与用户确认）

- **缓存新鲜度：按版本永久缓存。** 某 `(source, ref, version)` 一旦成功缓存，后续永远读 S3、不回源。取舍：clawhub 作者偶尔用同一版本号 re-upload 新内容时不会自动更新（需换版本号或手动清缓存）。skill 归档按版本号本应不可变，此取舍可接受。
- **坏上游行为：返回明确错误即可。** 不做置灰 / 预探测。

## 推荐方案

新增一个按 `(source, ref, version, ext)` 通用键、套在现有 `LibraryBlobStore` 之上的 read-through 缓存助手 `SkillArchiveCache`，注入到三个回源点；每个回源点传入自己的「回源闭包」。

考虑过的替代：

- **`CachingSkillSource` 装饰器 + 让安装走 `SkillSource.Download`**：取数路径更统一，但安装 / 版本现在依赖 `ClawHubDownloader` / `PlatformInstaller` 接口而非 `SkillSource`，要把 source 注册表穿进两个 service，改动面与测试量都大得多。否决。
- **缓存塞进 `clawhub.ClawHubClient.Download`**：clawhub 包是纯 stdlib、无存储依赖，且只对 clawhub 生效，违背「所有未来市场通用」。否决。

推荐方案改动最小、完全复用现有 `library/` key 方案，且天然 source 无关。

## 现有基础（复用，不新建）

- `service.LibraryBlobStore` 接口（`PutLibrarySkill` / `OpenLibrarySkill` / `DeleteLibrarySkill`），FS（本地）与 S3（生产，`NewS3LibraryBlobStore`）两实现，已在 main.go 装配为 `libraryBlobs`。
- `storage.LibrarySkillKey(source, sourceRef, version, ext)` → `library/<source>/<sourceRef>/<version>.<ext>`。
- 安装路径已「下载后 `PutLibrarySkill` 写缓存」，`Reinstall` 已「按 `cached_tar_path` 读缓存」。缺口是**读取时不先查缓存**（每次都回源），以及**市场下载按钮既不读也不写缓存**。

## 组件设计

### 1. `SkillArchiveCache`（新增 `internal/service/skill_archive_cache.go`）

通用 read-through 归档缓存，按 `(source, ref, version, ext)` 寻址，底层走 `LibraryBlobStore`。

```go
type SkillArchiveCache struct {
    blobs LibraryBlobStore
}

func NewSkillArchiveCache(blobs LibraryBlobStore) *SkillArchiveCache

// Fetch 取 (source, ref, version) 的归档：
//   1. 读缓存 OpenLibrarySkill(LibrarySkillKey(source, ref, version, ext))；命中 → 返回缓存字节与其相对路径（不回源）。
//   2. 读缓存出错或对象不存在 → 视为未命中（缓存是优化、非硬依赖），继续回源。
//   3. 调 fetch(ctx) 回源；成功 → PutLibrarySkill 写回 → 返回 (data, relPath, nil)。
//   4. fetch 失败 → 原样返回该错误（由调用方分类映射），不写缓存。
func (c *SkillArchiveCache) Fetch(
    ctx context.Context,
    source, ref, version, ext string,
    fetch func(ctx context.Context) ([]byte, error),
) (data []byte, relPath string, err error)
```

要点：

- **永久缓存**：无 TTL；blob 存在即命中。
- **缓存是优化而非硬依赖**：读缓存出错（S3 抖动、预签名失败、404）一律降级为未命中、回源，绝不因缓存问题让请求失败。
- **不写不安全归档**：zip 炸弹校验放进调用方传入的 `fetch` 闭包内（见下）——校验失败 → `fetch` 返回错误 → 不写缓存。
- **写回幂等**：`PutLibrarySkill` 覆盖写，重复安装同版本不产生副作用。

### 2. 错误处理：新增哨兵 + 502 映射

- `internal/service/errors.go` 新增：
  ```go
  // ErrSkillMarketUpstreamUnavailable 表示上游第三方市场归档下载失败（非 2xx / 网络错误），
  // 且本地缓存未命中、无法降级。映射为 502 Bad Gateway，与「manager 自身 500」区分开。
  var ErrSkillMarketUpstreamUnavailable = errors.New("上游技能市场暂时不可用")
  ```
- 三个回源点的「回源闭包」把上游 `Download` 的非 2xx / 网络错误包成 `ErrSkillMarketUpstreamUnavailable`（用 `%w` 包住原因便于日志）。
- `internal/api/handlers/skill_market.go` 的 `writeSkillMarketError`：新增分支
  ```go
  case errors.Is(err, service.ErrSkillMarketUpstreamUnavailable):
      c.JSON(http.StatusBadGateway, apierror.New("UPSTREAM_UNAVAILABLE", "上游技能市场暂时不可用，请稍后重试"))
  ```
- `internal/api/handlers/app_skills.go` 的 `writeAppSkillError`：新增同样的 502 分支。

### 3. 三个回源点接线

| 回源点 | 文件 | 现状 | 改造 |
|---|---|---|---|
| 市场下载按钮 | `ClawHubSource.Download` | 直连上游、不读不写缓存 | 经 `cache.Fetch("clawhub", ref, version, "zip", 下载闭包)`；闭包把上游错误包成 `ErrSkillMarketUpstreamUnavailable` |
| 安装 / 更新 | `AppSkillService.fetchArchive`（clawhub 分支） | 下载后 `PutLibrarySkill` 写缓存，但不读 | 经 `cache.Fetch`；闭包内含 `validateArchiveSafety`（不安全 → 不缓存、返回 `ErrAppSkillArchiveTooDangerous`）与上游错误包装。删除 `Install`/`Update` 里原本显式的 `PutLibrarySkill`，改用 `Fetch` 返回的 `relPath`（保持单次写入） |
| 助手版本加 skill | `AssistantVersionService.resolveLibrarySkill`（clawhub 分支） | 下载后 `PutLibrarySkill`，不读 | 经 `cache.Fetch`；同上 |

- `platform` 来源（`PlatformSource.Download` / `fetchArchive` platform 分支 / `resolveLibrarySkill` platform 分支）保持不变：本就读 `library/platform/...` 持久副本、无上游。
- `SkillArchiveCache` 在 `cmd/server/main.go` 用 `service.NewSkillArchiveCache(libraryBlobs)` 构造一次，注入 `ClawHubSource`、`AppSkillService`、`AssistantVersionService`（仅 clawhub 启用时这些来源才生效，nil 守卫沿用现有模式）。

### 4. 安全校验位置（避免缓存 zip 炸弹）

安装 / 版本的回源闭包结构为「下载 → `validateArchiveSafety` → 返回字节」：校验失败时闭包返回 `ErrAppSkillArchiveTooDangerous`，`Fetch` 不写缓存。市场下载（平台管理员取原始字节直接下到浏览器）不解压、无需校验，闭包仅「下载 → 返回」。这样缓存里不会留下未经校验的不安全归档。

## 前端

- 详情抽屉下载失败：`SkillDetailDrawer.onDownload` 已 `message.error(e.message)`，`downloadSkillArchive` → `apiDownload` 在非 2xx 时应抛出携带服务端 `message` 的 Error。需确认 `apiDownload` 对二进制端点的 JSON 错误体解析正确，使 502 文案能展示在 toast（如不解析则补一处错误体解析）。
- 安装失败：安装 mutation 的错误已透传服务端 `message`，502 文案自动展示。
- 无新增页面 / 组件 / 类型；如 handler 签名 / 响应未变则无需 `make openapi-gen` / `web-types-gen`（新增的是错误分支，不改契约）。

## 数据与迁移

- **零 DB 变更**。完全复用 `library/<source>/<ref>/<version>.<ext>` 对象布局与 `app_skills.cached_tar_path` 列语义。

## 错误处理小结

| 场景 | 行为 |
|---|---|
| 缓存命中 | 读 S3 返回，不回源 |
| 缓存未命中 + 上游成功 | 回源 → 写 S3 → 返回 |
| 缓存未命中 + 上游 502 / 网络错误 | 返回 `ErrSkillMarketUpstreamUnavailable` → handler 502「上游技能市场暂时不可用，请稍后重试」 |
| 缓存读取出错（S3 抖动 / 404 / 预签名失败） | 降级为未命中、回源（缓存不是硬依赖） |
| 归档不安全（zip 炸弹，仅安装 / 版本） | 闭包返回 `ErrAppSkillArchiveTooDangerous` → 不缓存 → handler 400 |

## 测试

### 后端单元测试（testify assert/require）

- `SkillArchiveCache.Fetch`：
  - 缓存命中 → 返回缓存字节、**不调用** fetch 闭包（用计数桩断言回源 0 次）。
  - 缓存未命中 → 调 fetch 一次 → 断言写回（fake blob store 收到 `PutLibrarySkill`）。
  - fetch 失败 → **不写**缓存、原样返回错误。
  - 读缓存出错（fake `OpenLibrarySkill` 报错）→ 降级回源成功。
- `ClawHubSource.Download`：命中走缓存；未命中回源；上游非 2xx → 包成 `ErrSkillMarketUpstreamUnavailable`。
- `AppSkillService.Install` / `Update`：
  - 缓存命中 → 跳过 `clawhub.Download`（桩断言下载 0 次），仍落 `app_skills` 且 `cached_tar_path` 为缓存键。
  - 上游失败 → 返回 `ErrSkillMarketUpstreamUnavailable`。
  - 不安全归档 → `ErrAppSkillArchiveTooDangerous`、不缓存。
- `AssistantVersionService.resolveLibrarySkill`（clawhub）：命中 / 未命中 / 上游失败三路。
- handler 映射：`writeSkillMarketError` 与 `writeAppSkillError` 对 `ErrSkillMarketUpstreamUnavailable` → 502。

### 前端单元测试（vitest）

- `apiDownload` 对 502 JSON 错误体抛出含 `message` 的 Error；`SkillDetailDrawer` 下载失败 toast 展示该 message（沿用现有 mock 模式）。

### 浏览器手工验证（真实 k3d，平台管理员 + org 角色）

- **错误处理**：self-improving 下载 → toast「上游技能市场暂时不可用…」（不再「服务器内部错误」）；self-improving 安装 → 同样明确 toast。
- **缓存命中**：weather 等正常 skill 首次下载（预热缓存）→ 去 MinIO 确认 `library/clawhub/weather/1.0.0.zip` 存在 → 第二次下载 / 安装时确认走缓存（manager 日志无上游请求，或临时断网 clawhub 仍能装成功）。
- **安装端到端**：装一个正常 clawhub skill → hermes reload → 对账 active（沿用既有验证口径）。

## 交付影响

- 新增文件：`internal/service/skill_archive_cache.go` + 其单测。
- 改动文件：`clawhub_source.go`、`app_skill_service.go`、`assistant_version_service.go`、`service/errors.go`、`handlers/skill_market.go`、`handlers/app_skills.go`、`cmd/server/main.go`，及对应单测；可能 `web/src/api/client.ts`（错误体解析，按需）。
- 无 DB 迁移、无 OpenAPI 契约变更。
- 风险：改了三个回源点的取数路径，需保证缓存未命中时行为与现状完全一致（回源 + 写回 + 落库），靠单测与浏览器验证兜住。
