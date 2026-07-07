# skill 上传支持 zip 压缩包 设计文档

- 日期：2026-07-07
- 状态：设计已确认，待实现

## 背景

平台技能上传当前支持两种方式，均在浏览器内校验并打包成**扁平 tar**（`SKILL.md`
及其它文件直接位于归档根级）后走 multipart 上传，后端用
`hermes.InspectFlatSkillArchive` 校验、oc-ops 热装：

- **粘贴 Markdown**：粘贴内容即单个 `SKILL.md`；
- **上传文件夹**：浏览器 `webkitdirectory` 选目录，剥掉顶层目录名后打包。

打包逻辑集中在 `web/src/domain/skillPackaging.ts`（`packFromMarkdown` /
`packFromFolder` / `buildTar` / `parseSkillFrontmatter`），被两个入口复用：

- 平台技能上传：`web/src/pages/platform/PlatformSkillsPage.vue`；
- 定制技能交付：`web/src/components/ticket/DeliverCustomSkillModal.vue`。

用户常见的 skill 分发形态是一个 zip 压缩包（Windows 右键即可打包、别处下载到的
skill 包也多为 zip）。目前无法直接上传 zip，只能解压后再选文件夹，体验割裂。本次
新增第三种上传方式「上传压缩包（zip）」。

## 目标与非目标

**目标**：平台技能上传与定制技能交付两个入口都支持直接上传 zip 压缩包，自动完成
解压、顶层目录规范化、校验，并沿用现有扁平 tar 上传/校验/热装链路。

**非目标**：

- 不支持 tar / tar.gz / 7z / rar 等其它格式（仅 zip）。
- 不改动后端与 oc-ops：后端收到的仍是扁平 tar，契约不变。
- 不改动「粘贴 Markdown」「上传文件夹」两种既有方式的行为。

## 设计

### 架构决策

zip 的解压与规范化放在**前端**：浏览器内解压 zip → 剥掉顶层目录（若有）→ 校验根级
`SKILL.md`、解析 frontmatter → 复用现有 `buildTar` 重新打成**扁平 tar** 上传。

这样后端零改动，三种上传方式（粘贴 MD / 上传文件夹 / 上传 zip）在后端收到的都是
同一种扁平 tar，与现有「浏览器打扁平 tar」的架构完全一致。相较「后端接收原始 zip」
方案，避免后端同时存在 zip 与 tar 两套归档处理路径（`InspectFlatSkillArchive`
是 tar 的），改动面最小。

引入 **fflate**（~8KB，浏览器 zip 编解码标准库）作为 `web` 依赖。

### 核心：`skillPackaging.ts` 新增 `packFromZip`

```ts
export function packFromZip(zipBytes: Uint8Array): PackResult
```

返回与 `packFromMarkdown` / `packFromFolder` 一致的 `PackResult`
（`{ name, description, tar }`，`tar` 为扁平 tar）。处理流程：

1. `fflate.unzipSync(zipBytes)` 解出 `路径 → 字节` 映射。
2. **过滤垃圾条目**：目录条目（路径以 `/` 结尾、0 字节）、macOS 打包残留
   （`__MACOSX/` 前缀的条目、任意层级的 `.DS_Store`）。
3. **顶层目录规范化**：
   - 若根级已存在 `SKILL.md` → 视为已扁平，条目原样使用；
   - 否则若剩余全部文件共享**同一个**顶层目录段、且剥掉该段后根级出现
     `SKILL.md`（对应测试包 `my-coffee/SKILL.md` 结构）→ 剥掉这一层目录前缀；
   - 否则抛出中文错误「未在根级或单层目录内找到 SKILL.md」。
4. **路径安全校验**：拒绝含 `..` 段、绝对路径（以 `/` 开头）、空路径的条目
   （复用 `packFromFolder` 同款校验规则）。
5. 解析根级 `SKILL.md` 的 YAML frontmatter，取 `name`（必填）/ `description`
   （可选），复用现有 `parseSkillFrontmatter`。
6. `buildTar(entries)` 打成扁平 tar（复用现有函数，保留子目录结构）；`buildTar`
   现有的 USTAR name 字段 100 字节上限约束沿用不变。

规范化逻辑与 `packFromFolder` 相近，差别仅在：文件夹模式顶层目录一定存在且固定剥掉，
zip 模式顶层目录不一定存在，需按上述第 3 步判定。

### 两个上传入口各加「上传 zip」选项

`PlatformSkillsPage.vue` 与 `DeliverCustomSkillModal.vue` 做对称改动：

- `mode` 类型由 `'markdown' | 'folder'` 扩为 `'markdown' | 'folder' | 'zip'`，
  radio 组增加一项「上传压缩包」；
- 新增隐藏的 `<input type="file" accept=".zip">`，`onZipChange` 读所选文件的
  `ArrayBuffer` 转 `Uint8Array` 暂存；
- `onUpload` 的分支增加 `zip` 情况：调用 `packFromZip`；
- 补充 zh / en i18n key（zip 模式的 radio 文案、字段 label、上传提示）。

## 测试与验证

**单元测试**（`web/src/domain/skillPackaging.test.ts` 补 `packFromZip`）：

- 带顶层目录的 zip（用 fflate `zipSync` 造 `my-coffee/SKILL.md` 结构）→ 剥层后
  正确解析 name/description，产出扁平 tar；
- 已扁平的 zip（根级即 `SKILL.md`）→ 原样打包；
- 缺 `SKILL.md` 的 zip → 报错；
- 含 `__MACOSX/` 与 `.DS_Store` 的 zip → 被过滤、不进 tar；
- 含 `..` 越界路径的 zip → 报错；
- frontmatter 缺 name → 报错。

**真机浏览器验证**（本地 k3d，遵循全角色端到端要求）：

- 用 `/home/hujing/下载/my-coffee-skill.zip`（结构 `my-coffee/SKILL.md` 等 5 文件）
  走平台技能上传 → 落平台库；
- 实例安装该技能 → oc-ops 热装 → 对账 status 变 active；
- 定制技能交付入口同样用 zip 走一遍交付 → 安装链路。

## 影响范围

- 新增前端依赖 `fflate`。
- 改动文件：`web/src/domain/skillPackaging.ts`（新增函数）、
  `web/src/pages/platform/PlatformSkillsPage.vue`、
  `web/src/components/ticket/DeliverCustomSkillModal.vue`、对应 i18n locale 文件、
  `web/src/domain/skillPackaging.test.ts`。
- 后端、oc-ops、OpenAPI 契约：无改动。
