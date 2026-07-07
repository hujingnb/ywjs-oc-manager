# skill 上传支持 zip 压缩包 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让平台技能上传与定制技能交付两个入口都能直接上传 zip 压缩包，浏览器内解压、剥掉顶层目录、校验后复用现有扁平 tar 上传链路。

**Architecture:** zip 的解压与规范化全部放在前端（`web/src/domain/skillPackaging.ts`）：用 fflate 在浏览器解压 zip → 若整包套了单层顶层目录则剥掉 → 校验根级 `SKILL.md`、解析 frontmatter → 复用现有 `buildTar` 打成扁平 tar 上传。后端、oc-ops、OpenAPI 契约零改动，三种上传方式（粘贴 MD / 上传文件夹 / 上传 zip）在后端收到的都是同一种扁平 tar。

**Tech Stack:** Vue 3 + TypeScript + naive-ui + vue-i18n（前端）、vitest（单测）、fflate（zip 解码/编码）。

设计文档：`docs/superpowers/specs/2026-07-07-skill-zip-upload-design.md`。

---

## 文件结构

- `web/package.json` — 新增 `fflate` 依赖。
- `web/src/domain/skillPackaging.ts` — 新增 `packFromZip`；抽出共享收尾函数 `finalizePack`（供 `packFromFolder` 与 `packFromZip` 复用）。
- `web/src/domain/skillPackaging.test.ts` — 新增 `packFromZip` 单测。
- `web/src/pages/platform/PlatformSkillsPage.vue` — 上传方式增加「上传压缩包」选项。
- `web/src/components/ticket/DeliverCustomSkillModal.vue` — 交付定制技能增加「上传压缩包」选项。
- `web/src/i18n/locales/{zh,en}/platform.ts` — 平台上传页 zip 模式文案。
- `web/src/i18n/locales/{zh,en}/components.ts` — 交付弹框 zip 模式文案。

---

### Task 1: 引入 fflate 依赖

**Files:**
- Modify: `web/package.json`（dependencies 段新增 `fflate`）

- [ ] **Step 1: 安装 fflate**

Run:
```bash
cd web && npm install fflate@0.8.3 --save
```
Expected: `package.json` 的 `dependencies` 出现 `"fflate": "^0.8.3"`，`package-lock.json` 更新，安装无报错。

- [ ] **Step 2: 验证可导入**

Run:
```bash
cd web && node -e "const {unzipSync, zipSync, strToU8} = require('fflate'); const z = zipSync({'a.txt': strToU8('hi')}); const u = unzipSync(z); console.log(new TextDecoder().decode(u['a.txt']))"
```
Expected: 打印 `hi`（证明 fflate 解压/压缩可用）。

- [ ] **Step 3: 提交**

```bash
git add web/package.json web/package-lock.json
git commit -m "chore(web): 引入 fflate 用于浏览器内 zip 解压

skill 上传新增「上传压缩包」方式，需在浏览器内解压 zip 后重打包成扁平 tar。"
```

---

### Task 2: 实现 `packFromZip`（TDD）

抽出共享收尾函数 `finalizePack`（路径安全校验 + 根级 `SKILL.md` 校验 + 解析 frontmatter + `buildTar`），让 `packFromFolder` 与新的 `packFromZip` 都复用它，避免重复校验逻辑。

**Files:**
- Modify: `web/src/domain/skillPackaging.ts`
- Test: `web/src/domain/skillPackaging.test.ts`

- [ ] **Step 1: 写失败测试**

在 `web/src/domain/skillPackaging.test.ts` 顶部 import 追加 `packFromZip`，并从 fflate 引入 `zipSync` / `strToU8` 造测试 zip。import 块改为：

```ts
import { describe, expect, it } from 'vitest'
import { zipSync, strToU8 } from 'fflate'

import {
  buildTar,
  packFromFolder,
  packFromMarkdown,
  packFromZip,
  parseSkillFrontmatter,
  type TarEntry,
  type UploadedFile,
} from './skillPackaging'
```

在文件末尾追加：

```ts
describe('packFromZip', () => {
  // 带顶层目录的 zip（zip 一个文件夹的常见形态）：剥掉最外层 my-coffee/ 后 SKILL.md 落到归档根，
  // 附属文件同样落到根级，name/description 取自 frontmatter。
  it('剥掉单层顶层目录并打成扁平 tar', () => {
    const zip = zipSync({
      'my-coffee/SKILL.md': strToU8('---\nname: my-coffee\ndescription: 冲咖啡\n---\n正文'),
      'my-coffee/LICENSE': strToU8('MIT'),
      'my-coffee/scripts/run.sh': strToU8('echo hi'),
    })
    const res = packFromZip(zip)
    expect(res.name).toBe('my-coffee')
    expect(res.description).toBe('冲咖啡')
    const parsed = readTar(res.tar)
    // 顶层 my-coffee/ 被剥离：SKILL.md 落到归档根，子目录 scripts/ 保留。
    expect(parsed['SKILL.md']).toBe('---\nname: my-coffee\ndescription: 冲咖啡\n---\n正文')
    expect(parsed['LICENSE']).toBe('MIT')
    expect(parsed['scripts/run.sh']).toBe('echo hi')
    expect(parsed['my-coffee/SKILL.md']).toBeUndefined()
  })

  // zip 内已经是扁平结构（根级即 SKILL.md）：原样打包，不误剥。
  it('已扁平的 zip 原样打包', () => {
    const zip = zipSync({
      'SKILL.md': strToU8('---\nname: flat\n---\n正文'),
      'notes.md': strToU8('n'),
    })
    const res = packFromZip(zip)
    expect(res.name).toBe('flat')
    const parsed = readTar(res.tar)
    expect(parsed['SKILL.md']).toBe('---\nname: flat\n---\n正文')
    expect(parsed['notes.md']).toBe('n')
  })

  // 剥层后根级仍无 SKILL.md（嵌在更深一层）应抛错。
  it('找不到根级 SKILL.md 抛错', () => {
    const zip = zipSync({
      'pkg/inner/SKILL.md': strToU8('---\nname: x\n---\n'),
    })
    expect(() => packFromZip(zip)).toThrow(/根级 SKILL.md/)
  })

  // macOS 打包残留（__MACOSX/、.DS_Store）应被过滤，不进 tar。
  it('过滤 __MACOSX 与 .DS_Store', () => {
    const zip = zipSync({
      'my-coffee/SKILL.md': strToU8('---\nname: my-coffee\n---\n正文'),
      'my-coffee/.DS_Store': strToU8('junk'),
      '__MACOSX/my-coffee/._SKILL.md': strToU8('junk'),
    })
    const res = packFromZip(zip)
    const parsed = readTar(res.tar)
    expect(parsed['SKILL.md']).toBe('---\nname: my-coffee\n---\n正文')
    expect(parsed['.DS_Store']).toBeUndefined()
    expect(Object.keys(parsed)).toEqual(['SKILL.md'])
  })

  // 含越界路径条目（..）应被拒绝。
  it('越界路径抛错', () => {
    const zip = zipSync({
      'SKILL.md': strToU8('---\nname: x\n---\n'),
      '../evil.sh': strToU8('rm -rf /'),
    })
    expect(() => packFromZip(zip)).toThrow(/非法路径/)
  })

  // frontmatter 缺 name 应抛错（复用 parseSkillFrontmatter 的校验）。
  it('frontmatter 缺 name 抛错', () => {
    const zip = zipSync({
      'my-coffee/SKILL.md': strToU8('---\ndescription: 无名\n---\n正文'),
    })
    expect(() => packFromZip(zip)).toThrow(/name/)
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run:
```bash
cd web && npx vitest run src/domain/skillPackaging.test.ts
```
Expected: FAIL —— `packFromZip` 尚未导出（`packFromZip is not a function` 或导入报错）。

- [ ] **Step 3: 实现 `finalizePack`、`stripSingleTopDir`、`packFromZip`**

在 `web/src/domain/skillPackaging.ts` 顶部（现有注释块之后、第一个 `export` 之前）加入 fflate import：

```ts
import { unzipSync } from 'fflate'
```

在文件中 `packFromFolder` 定义之前，新增共享收尾函数：

```ts
// finalizePack 对「已剥离到归档根」的条目做统一收尾：路径安全校验 + 根级 SKILL.md 校验 +
// 解析 frontmatter + 打成扁平 tar。packFromFolder 与 packFromZip 共用，避免重复校验逻辑。
// 错误文案保留「非法路径」「根级 SKILL.md」关键词，供上层页面直接展示、也被单测断言。
function finalizePack(entries: TarEntry[]): PackResult {
  let skillMD: TarEntry | undefined
  for (const e of entries) {
    // 拒绝空路径、绝对路径、含 .. 的越界路径，防止解压时写出归档根之外。
    if (!e.path || e.path.startsWith('/') || e.path.split('/').includes('..')) {
      throw new Error(`归档含非法路径条目：${e.path || '(空路径)'}`)
    }
    if (e.path === 'SKILL.md') {
      skillMD = e
    }
  }
  if (!skillMD) {
    throw new Error('未找到根级 SKILL.md（其应直接位于归档根或单层目录内）')
  }
  const meta = parseSkillFrontmatter(new TextDecoder().decode(skillMD.data))
  const tar = buildTar(entries)
  return { name: meta.name, description: meta.description, tar }
}
```

把现有 `packFromFolder` 的尾部（从「路径安全 + 根级 SKILL.md 校验」到 `return`）替换为调用 `finalizePack`。改造后的 `packFromFolder` 为：

```ts
// packFromFolder 处理「上传文件夹」：剥掉所选目录的顶层目录名，使内容落到归档根（满足扁平契约），
// 再交给 finalizePack 校验根级 SKILL.md 并打成扁平 tar。
// 任何非法路径（越界 / 缺目录层级）或根级缺 SKILL.md 时抛出带中文说明的 Error。
export function packFromFolder(files: UploadedFile[]): PackResult {
  if (files.length === 0) {
    throw new Error('所选文件夹为空')
  }
  // 逐个剥离顶层目录前缀：webkitRelativePath 首段为所选文件夹名，去掉后内容即落到归档根。
  const stripped: TarEntry[] = files.map((f) => {
    const norm = f.relativePath.replace(/\\/g, '/')
    const idx = norm.indexOf('/')
    if (idx < 0) {
      // 没有目录段，说明不是通过文件夹选择得到的相对路径（异常输入）。
      throw new Error(`非法的文件路径（缺少目录层级）：${f.relativePath}`)
    }
    return { path: norm.slice(idx + 1), data: f.data }
  })
  return finalizePack(stripped)
}
```

在 `packFromFolder` 之后新增 zip 相关实现：

```ts
// stripSingleTopDir 处理 zip 的顶层目录：
// - 若根级已存在 SKILL.md，视为已扁平，原样返回；
// - 否则当所有文件都位于「同一个」顶层目录下（每条路径都含 '/' 且首段相同）时，剥掉该顶层段
//   （对应「zip 一个文件夹」得到 my-coffee/SKILL.md 的常见形态）；
// - 其余情况原样返回，交给 finalizePack 因根级缺 SKILL.md 报错。
function stripSingleTopDir(files: TarEntry[]): TarEntry[] {
  if (files.some((f) => f.path === 'SKILL.md')) {
    return files
  }
  const tops = new Set<string>()
  for (const f of files) {
    const idx = f.path.indexOf('/')
    // 有文件直接落在根级（且不是 SKILL.md），不属于「单层目录」形态，不剥。
    if (idx < 0) {
      return files
    }
    tops.add(f.path.slice(0, idx))
  }
  if (tops.size !== 1) {
    return files
  }
  const top = [...tops][0]
  return files.map((f) => ({ path: f.path.slice(top.length + 1), data: f.data }))
}

// packFromZip 处理「上传压缩包」：在浏览器内解压 zip，过滤目录/垃圾条目，剥掉单层顶层目录后
// 交给 finalizePack 校验并打成扁平 tar（与粘贴 MD / 上传文件夹产出同一种扁平 tar）。
// zip 损坏、无文件、缺根级 SKILL.md、含越界路径或 frontmatter 缺 name 时抛出带中文说明的 Error。
export function packFromZip(zipBytes: Uint8Array): PackResult {
  let unzipped: Record<string, Uint8Array>
  try {
    unzipped = unzipSync(zipBytes)
  } catch {
    throw new Error('zip 解压失败：文件可能已损坏或不是有效的 zip')
  }
  const files: TarEntry[] = []
  for (const [rawPath, data] of Object.entries(unzipped)) {
    const path = rawPath.replace(/\\/g, '/')
    // 跳过目录条目（fflate 以 '/' 结尾的键表示目录）。
    if (path.endsWith('/')) {
      continue
    }
    // 跳过 macOS 打包残留：__MACOSX/ 资源分叉目录与任意层级的 .DS_Store。
    if (path.startsWith('__MACOSX/') || path.split('/').pop() === '.DS_Store') {
      continue
    }
    files.push({ path, data })
  }
  if (files.length === 0) {
    throw new Error('zip 内没有可用文件')
  }
  return finalizePack(stripSingleTopDir(files))
}
```

- [ ] **Step 4: 运行测试确认通过**

Run:
```bash
cd web && npx vitest run src/domain/skillPackaging.test.ts
```
Expected: PASS —— 新增 6 条 `packFromZip` 用例与原有 `packFromFolder` / `packFromMarkdown` / `buildTar` / `parseSkillFrontmatter` 用例全绿（原用例断言的 `/非法路径/`、`/根级 SKILL.md/` 关键词在 `finalizePack` 中保留，不受抽取影响）。

- [ ] **Step 5: 提交**

```bash
git add web/src/domain/skillPackaging.ts web/src/domain/skillPackaging.test.ts
git commit -m "feat(web): skillPackaging 新增 packFromZip 解析 zip 压缩包

浏览器内用 fflate 解压 zip，过滤目录/__MACOSX/.DS_Store 条目，自动剥掉
单层顶层目录后校验根级 SKILL.md 并打成扁平 tar。抽出共享收尾函数
finalizePack 供 packFromFolder 与 packFromZip 复用。"
```

---

### Task 3: 平台技能上传页接入 zip 模式

**Files:**
- Modify: `web/src/pages/platform/PlatformSkillsPage.vue`
- Modify: `web/src/i18n/locales/zh/platform.ts`
- Modify: `web/src/i18n/locales/en/platform.ts`

- [ ] **Step 1: 补 i18n 文案（zh）**

在 `web/src/i18n/locales/zh/platform.ts` 的 `skills.uploadMode` 内新增 `zip` 项，并在 `folderMode` 块之后新增 `zipMode` 块：

```ts
    uploadMode: {
      label: '上传方式',
      markdown: '粘贴 Markdown',
      folder: '上传文件夹',
      zip: '上传压缩包',
    },
```

```ts
    zipMode: {
      label: 'Skill 压缩包 *',
      selectButton: '选择 zip 文件',
      selectedInfo: '{name}',
      noZip: '未选择压缩包',
      hint: '选择一个 zip 压缩包，其中包含 skill 的全部文件（须含 SKILL.md）。若压缩包套了一层目录（如 my-coffee/SKILL.md），上传时会自动剥掉最外层目录。',
    },
```

- [ ] **Step 2: 补 i18n 文案（en）**

在 `web/src/i18n/locales/en/platform.ts` 对应位置新增：

```ts
    uploadMode: {
      label: 'Upload method',
      markdown: 'Paste Markdown',
      folder: 'Upload folder',
      zip: 'Upload zip',
    },
```

```ts
    zipMode: {
      label: 'Skill zip archive *',
      selectButton: 'Select zip file',
      selectedInfo: '{name}',
      noZip: 'No archive selected',
      hint: 'Select a zip archive containing all skill files (must include SKILL.md). If everything is wrapped in a single top-level directory (e.g. my-coffee/SKILL.md), it is stripped automatically on upload.',
    },
```

- [ ] **Step 3: 页面接入 zip 模式**

在 `web/src/pages/platform/PlatformSkillsPage.vue` 中做以下改动。

(a) import 追加 `packFromZip`：

```ts
import {
  packFromFolder,
  packFromMarkdown,
  packFromZip,
  parseSkillFrontmatter,
  type SkillMeta,
  type UploadedFile,
} from '@/domain/skillPackaging'
```

(b) `mode` 类型扩为三态，并新增 zip 相关 ref（放在 `folderName` 定义之后）：

```ts
const mode = ref<'markdown' | 'folder' | 'zip'>('markdown')
```
```ts
// 上传压缩包模式：已读入的 zip 字节与文件名（仅用于展示）。
const zipBytes = ref<Uint8Array | null>(null)
const zipName = ref('')
```

(c) 在 `folderInputRef` 定义之后新增隐藏 zip input 的 ref：

```ts
// 隐藏的 zip 文件选择 input 引用。
const zipInputRef = ref<HTMLInputElement | null>(null)
```

(d) `parsed` computed 增加 zip 分支（放在 markdown 分支之后、folder 分支之前）：

```ts
    if (mode.value === 'zip') {
      if (!zipBytes.value) return { meta: null, error: '' }
      const r = packFromZip(zipBytes.value)
      return { meta: { name: r.name, description: r.description }, error: '' }
    }
```

(e) 新增 `onZipChange`（放在 `onFolderChange` 之后）：

```ts
// onZipChange 读入所选 zip 文件的字节，供后续 packFromZip 解析/打包。
async function onZipChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) {
    zipBytes.value = null
    zipName.value = ''
    return
  }
  zipBytes.value = new Uint8Array(await file.arrayBuffer())
  zipName.value = file.name
  // 重置后允许再次选择同一文件。
  input.value = ''
  uploadFeedback.value = ''
  uploadFeedbackError.value = false
}
```

(f) `onUpload` 内打包语句改为三分支：

```ts
    const result =
      mode.value === 'markdown'
        ? packFromMarkdown(mdText.value)
        : mode.value === 'zip'
          ? packFromZip(zipBytes.value!)
          : packFromFolder(folderFiles.value)
```

(g) `onUpload` 成功后的表单重置追加清空 zip 状态：

```ts
    zipBytes.value = null
    zipName.value = ''
```

(h) 模板：在 radio 组新增 zip 选项（`folder` radio 之后）：

```html
            <n-radio-button value="zip">{{ t('platform.skills.uploadMode.zip') }}</n-radio-button>
```

(i) 模板：在「上传文件夹使用说明」`<ul>` 之后、「解析预览」`<p v-if="parsed.error">` 之前新增 zip 上传区块：

```html
        <!-- 上传压缩包：选择一个 zip，内部含 skill 全部文件（须含 SKILL.md） -->
        <n-form-item v-if="mode === 'zip'" :label="t('platform.skills.zipMode.label')">
          <input ref="zipInputRef" type="file" accept=".zip" style="display: none" @change="onZipChange" />
          <div style="display: flex; align-items: center; gap: 12px">
            <n-button @click="zipInputRef?.click()">{{ t('platform.skills.zipMode.selectButton') }}</n-button>
            <span v-if="zipName" class="state-text" style="margin: 0">{{ t('platform.skills.zipMode.selectedInfo', { name: zipName }) }}</span>
            <span v-else class="state-text" style="margin: 0">{{ t('platform.skills.zipMode.noZip') }}</span>
          </div>
          <p class="upload-hint" style="margin: 8px 0 0">{{ t('platform.skills.zipMode.hint') }}</p>
        </n-form-item>
```

- [ ] **Step 4: 类型检查 + 单测**

Run:
```bash
cd web && npx vue-tsc --noEmit && npx vitest run
```
Expected: 类型检查通过、全部单测 PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/platform/PlatformSkillsPage.vue web/src/i18n/locales/zh/platform.ts web/src/i18n/locales/en/platform.ts
git commit -m "feat(web): 平台技能上传新增「上传压缩包」方式

上传方式增加 zip 选项：选中 zip 后前端 packFromZip 解析预览技能名，
提交时复用扁平 tar 上传链路。补中英文案。"
```

---

### Task 4: 定制技能交付弹框接入 zip 模式

**Files:**
- Modify: `web/src/components/ticket/DeliverCustomSkillModal.vue`
- Modify: `web/src/i18n/locales/zh/components.ts`
- Modify: `web/src/i18n/locales/en/components.ts`

- [ ] **Step 1: 补 i18n 文案（zh）**

在 `web/src/i18n/locales/zh/components.ts` 的 `deliverCustomSkillModal` 块内，`modeFolder` 之后新增 `modeZip`，并新增 zip 字段（放在 `selectFolderBtn` 附近）：

```ts
    modeZip: '上传压缩包',
    fieldSkillZip: 'Skill 压缩包',
    selectZipBtn: '选择 zip 文件',
    zipFileName: '{name}',
```

- [ ] **Step 2: 补 i18n 文案（en）**

在 `web/src/i18n/locales/en/components.ts` 对应位置新增：

```ts
    modeZip: 'Upload zip',
    fieldSkillZip: 'Skill zip archive',
    selectZipBtn: 'Select zip file',
    zipFileName: '{name}',
```

- [ ] **Step 3: 弹框接入 zip 模式**

在 `web/src/components/ticket/DeliverCustomSkillModal.vue` 中做以下改动。

(a) import 追加 `packFromZip`：

```ts
import { packFromFolder, packFromMarkdown, packFromZip, type UploadedFile } from '@/domain/skillPackaging'
```

(b) `mode` 类型扩为三态，并在 `folderFiles` 之后新增 zip ref：

```ts
const mode = ref<'markdown' | 'folder' | 'zip'>('markdown')
```
```ts
const zipInput = ref<HTMLInputElement | null>(null)
const zipBytes = ref<Uint8Array | null>(null)
const zipName = ref('')
```

(c) `watch` 内的重置逻辑追加清空 zip 状态（与 `folderFiles.value = []` 相邻）：

```ts
    zipBytes.value = null
    zipName.value = ''
```

(d) 新增 `onZipChange`（放在 `onFolderChange` 之后）：

```ts
// onZipChange 读入所选 zip 文件的字节，供交付时 packFromZip 解析/打包。
async function onZipChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  zipBytes.value = file ? new Uint8Array(await file.arrayBuffer()) : null
  zipName.value = file?.name ?? ''
}
```

(e) `onDeliver` 内打包语句改为三分支：

```ts
    const pack =
      mode.value === 'markdown'
        ? packFromMarkdown(mdText.value)
        : mode.value === 'zip'
          ? packFromZip(zipBytes.value!)
          : packFromFolder(folderFiles.value)
```

(f) 模板：radio 组新增 zip 选项（`folder` radio 之后）：

```html
          <n-radio-button value="zip">{{ t('components.deliverCustomSkillModal.modeZip') }}</n-radio-button>
```

(g) 模板：现有 folder 的 `<n-form-item v-else ...>` 改为 `v-else-if="mode === 'folder'"`，并在其后新增 zip 区块。改造后这两块为：

```html
      <n-form-item v-else-if="mode === 'folder'" :label="t('components.deliverCustomSkillModal.fieldSkillFolder')">
        <input ref="folderInput" type="file" multiple class="folder-input" @change="onFolderChange" />
        <n-button @click="folderInput?.click()">{{ t('components.deliverCustomSkillModal.selectFolderBtn') }}</n-button>
        <span v-if="folderFiles.length" class="folder-count">{{ t('components.deliverCustomSkillModal.folderFileCount', { count: folderFiles.length }) }}</span>
      </n-form-item>
      <n-form-item v-else :label="t('components.deliverCustomSkillModal.fieldSkillZip')">
        <input ref="zipInput" type="file" accept=".zip" class="folder-input" @change="onZipChange" />
        <n-button @click="zipInput?.click()">{{ t('components.deliverCustomSkillModal.selectZipBtn') }}</n-button>
        <span v-if="zipName" class="folder-count">{{ t('components.deliverCustomSkillModal.zipFileName', { name: zipName }) }}</span>
      </n-form-item>
```

- [ ] **Step 4: 类型检查 + 单测 + 构建**

Run:
```bash
cd web && npx vue-tsc --noEmit && npx vitest run && npm run build
```
Expected: 类型检查通过、单测全绿、构建成功。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/ticket/DeliverCustomSkillModal.vue web/src/i18n/locales/zh/components.ts web/src/i18n/locales/en/components.ts
git commit -m "feat(web): 定制技能交付新增「上传压缩包」方式

交付弹框上传方式增加 zip 选项，选中 zip 后复用 packFromZip 打成扁平 tar
交付。补中英文案。"
```

---

### Task 5: 真机浏览器端到端验证

> 遵循 AGENTS.md：新功能必须用真实浏览器（非 curl）验证；本地 k3d 环境见 memory `local-k3d-env`，前端改动纯静态资源，可 `make web-build` 后部署或直接本地 dev server 联调线上后端。三角色账号见 memory `feedback_verification-rigor`。

**Files:**
- 无代码改动（验证任务）；如发现 bug 则回到对应 Task 修复并补测。

- [ ] **Step 1: 起前端**

Run（二选一）：本地 dev server（`cd web && npm run dev`，浏览器开 http://localhost:5173，或按项目既有 dev 联调方式）；或 `make local-build` 部署到本地 k3d 后开 http://ocm.localhost。

- [ ] **Step 2: 平台技能上传（zip）**

以平台管理员（`admin` / 组织标识留空 / `admin123`）登录 → 进「平台技能」页 → 上传方式选「上传压缩包」→ 选 `/home/hujing/下载/my-coffee-skill.zip`（结构 `my-coffee/SKILL.md` 等 5 文件）。
Expected: 解析预览显示「识别到技能：my-coffee」；填版本号后上传成功；列表出现 `my-coffee`。

- [ ] **Step 3: 实例安装并对账**

用一个组织成员账号在其实例的技能页从平台库安装 `my-coffee` → 触发 oc-ops 热装。
Expected: 已安装列表中 `my-coffee` status 变为 `active`（证明扁平 tar 落地到 `/opt/data/skills/my-coffee/SKILL.md` 且 hermes reload 成功）。

- [ ] **Step 4: 定制技能交付（zip）**

以成员提交一条定制技能工单 → 平台管理员在「定制技能」队列打开交付弹框 → 上传方式选「上传压缩包」→ 用同一个 zip 交付。
Expected: 交付成功；成员在市场「定制」筛选看到该技能并可安装。

- [ ] **Step 5: 记录验证证据**

汇总逐项结果（每步截图 / DB 佐证 / 会话 jsonl 若涉及对话生效），按 AGENTS.md 的逐项验证要求交付。若任一环节失败，回到对应 Task 修复并重跑本任务。

---

## Self-Review

**1. Spec coverage：**
- 仅 zip、前端解压重打包成扁平 tar、后端零改动 → Task 1（fflate）+ Task 2（packFromZip 产扁平 tar）。
- 自动剥单层顶层目录 → `stripSingleTopDir`（Task 2 Step 3 + 测试用例 1/2）。
- 过滤 macOS 残留 + 路径安全 + 缺 SKILL.md/缺 name 报错 → Task 2 测试用例 3/4/5/6 + `finalizePack`。
- 两个入口对称接入 → Task 3（平台上传）+ Task 4（定制交付）。
- zh/en i18n → Task 3 Step 1-2、Task 4 Step 1-2。
- 单测 + 真机三角色验证 → Task 2 测试块 + Task 5。
覆盖完整，无遗漏 spec 要求。

**2. Placeholder scan：** 所有步骤含实际代码/命令/预期输出，无 TBD/TODO/「类似 Task N」等占位。

**3. Type consistency：** `packFromZip(zipBytes: Uint8Array): PackResult`、`finalizePack(entries: TarEntry[]): PackResult`、`stripSingleTopDir(files: TarEntry[]): TarEntry[]` 在 Task 2 定义，Task 3/4 调用签名一致（`packFromZip(zipBytes.value!)`）；`PackResult` / `TarEntry` / `SkillMeta` 沿用现有导出类型；ref 名 `zipBytes` / `zipName` / `zipInputRef`(页) / `zipInput`(弹框) 在各自文件内自洽。
