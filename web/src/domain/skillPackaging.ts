// skillPackaging.ts — 平台技能「粘贴 Markdown / 上传文件夹」上传前的浏览器内校验与打包。
//
// 全系统 skill tar 契约为「扁平」：SKILL.md 及其它文件直接位于归档顶层（与后端
// oc-ops install_skill / renderer render_skills 的扁平契约一致），安装时整体解压进
// SKILLS_DIR/<技能名>/。因此打包时绝不能再套一层 <技能名>/ 目录，否则解压后 SKILL.md
// 落不到技能目录根，实例对账永远 pending。后端 hermes.InspectFlatSkillArchive 会再校验一次。

// SkillMeta 是从 SKILL.md frontmatter 解析出的元信息。
export interface SkillMeta {
  // name：技能规范名，取自 frontmatter 的 name 字段（必填）；同时作为平台库 name。
  name: string
  // description：技能描述，取自 frontmatter 的 description 字段（可选，缺失为空串）。
  description: string
}

// parseSkillFrontmatter 解析 SKILL.md 的 YAML frontmatter，取 name（必填）与 description（可选）。
// 约定与后端 parseSkillMDName 一致：必须以 `---` 行开头、再以 `---` 行结束。
// frontmatter 非法或缺少 name 时抛出带中文说明的 Error，供页面直接展示给用户。
export function parseSkillFrontmatter(md: string): SkillMeta {
  const body = md.replace(/\r\n/g, '\n')
  // frontmatter 必须以首行 "---" 开始，否则视为没有元数据。
  if (!body.startsWith('---\n')) {
    throw new Error('SKILL.md 必须以 YAML frontmatter 开头（首行为 ---）')
  }
  const rest = body.slice(4)
  const end = rest.indexOf('\n---')
  if (end < 0) {
    throw new Error('SKILL.md frontmatter 未正确闭合（缺少结束的 --- 行）')
  }
  const fm = rest.slice(0, end)
  let name = ''
  let description = ''
  for (const raw of fm.split('\n')) {
    const line = raw.trim()
    if (line.startsWith('name:')) {
      name = stripQuotes(line.slice('name:'.length).trim())
    } else if (line.startsWith('description:')) {
      description = stripQuotes(line.slice('description:'.length).trim())
    }
  }
  if (!name) {
    throw new Error('SKILL.md frontmatter 缺少 name 字段')
  }
  return { name, description }
}

// stripQuotes 去掉值两端配对的单/双引号（YAML 标量常见写法）；无引号时原样返回。
function stripQuotes(s: string): string {
  if (s.length >= 2 && (s[0] === '"' || s[0] === "'") && s[s.length - 1] === s[0]) {
    return s.slice(1, -1)
  }
  return s
}

// TarEntry 是 buildTar 的单个文件条目；path 为归档内相对路径，data 为文件字节。
export interface TarEntry {
  path: string
  data: Uint8Array
}

// buildTar 把若干文件打成一个 USTAR 格式 tar（无压缩）。
// 仅写普通文件条目、不写目录条目：解压方（Go archive/tar、Python tarfile）会按需创建父目录。
// 路径字节数超过 USTAR name 字段上限（100 字节）时抛错（skill 文件名通常远小于此）。
// mtime 固定为 0，保证同样输入产出同样字节（便于测试与去重）。
export function buildTar(entries: TarEntry[]): Uint8Array {
  const blocks: Uint8Array[] = []
  for (const e of entries) {
    blocks.push(buildHeader(e.path, e.data.length))
    blocks.push(e.data)
    // 数据区按 512 字节对齐补零。
    const pad = (512 - (e.data.length % 512)) % 512
    if (pad > 0) {
      blocks.push(new Uint8Array(pad))
    }
  }
  // 归档结尾固定两个全零块（1024 字节）表示 EOF。
  blocks.push(new Uint8Array(1024))
  return concat(blocks)
}

// buildHeader 构造一个 512 字节的 USTAR 文件头并填好校验和。
function buildHeader(name: string, size: number): Uint8Array {
  const nameBytes = new TextEncoder().encode(name)
  if (nameBytes.length > 100) {
    throw new Error(`文件路径过长（超过 100 字节）：${name}`)
  }
  const h = new Uint8Array(512)
  h.set(nameBytes, 0) // name [0,100)
  writeStr(h, 100, '0000644\0') // mode [100,108)
  writeStr(h, 108, '0000000\0') // uid  [108,116)
  writeStr(h, 116, '0000000\0') // gid  [116,124)
  writeStr(h, 124, octal(size, 11) + '\0') // size  [124,136) 11 位八进制 + NUL
  writeStr(h, 136, octal(0, 11) + '\0') // mtime [136,148) 固定 0
  // chksum [148,156) 计算前先填 8 个空格。
  for (let i = 148; i < 156; i++) {
    h[i] = 0x20
  }
  h[156] = 0x30 // typeflag '0'：普通文件
  writeStr(h, 257, 'ustar\0') // magic   [257,263)
  writeStr(h, 263, '00') // version [263,265)
  // 校验和 = 头部 512 字节之和（chksum 字段按 8 个空格参与计算）。
  let sum = 0
  for (let i = 0; i < 512; i++) {
    sum += h[i]
  }
  // USTAR 约定：6 位八进制 + NUL + 空格。
  writeStr(h, 148, octal(sum, 6) + '\0 ')
  return h
}

// writeStr 把 ASCII 字符串写入 buf 的指定偏移处。
function writeStr(buf: Uint8Array, off: number, s: string): void {
  buf.set(new TextEncoder().encode(s), off)
}

// octal 把数字转成定宽（左侧补 0）的八进制字符串。
function octal(n: number, width: number): string {
  return n.toString(8).padStart(width, '0')
}

// concat 顺序拼接多个 Uint8Array。
function concat(parts: Uint8Array[]): Uint8Array {
  let total = 0
  for (const p of parts) {
    total += p.length
  }
  const out = new Uint8Array(total)
  let off = 0
  for (const p of parts) {
    out.set(p, off)
    off += p.length
  }
  return out
}

// PackResult 是一次打包的产物：解析出的 name/description + 扁平 tar 字节。
export interface PackResult {
  name: string
  description: string
  tar: Uint8Array
}

// packFromMarkdown 处理「粘贴 Markdown」：粘贴内容即单个 SKILL.md，
// 校验 frontmatter 后打成只含根级 SKILL.md 的扁平 tar。
export function packFromMarkdown(md: string): PackResult {
  const meta = parseSkillFrontmatter(md)
  const tar = buildTar([{ path: 'SKILL.md', data: new TextEncoder().encode(md) }])
  return { name: meta.name, description: meta.description, tar }
}

// UploadedFile 是「上传文件夹」选中的单个文件；relativePath 取自浏览器 webkitRelativePath，
// 形如 "weather/SKILL.md" 或 "weather/scripts/run.sh"（首段为所选文件夹名）。
export interface UploadedFile {
  relativePath: string
  data: Uint8Array
}

// packFromFolder 处理「上传文件夹」：剥掉所选目录的顶层目录名，使内容落到归档根（满足扁平契约），
// 校验根级存在 SKILL.md 后按扁平契约打包（保留子目录结构）。
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
  // 路径安全 + 根级 SKILL.md 校验。
  let hasRootSkillMD = false
  for (const e of stripped) {
    if (!e.path || e.path.startsWith('/') || e.path.split('/').includes('..')) {
      throw new Error(`文件夹含非法路径条目：${e.path || '(空路径)'}`)
    }
    if (e.path === 'SKILL.md') {
      hasRootSkillMD = true
    }
  }
  if (!hasRootSkillMD) {
    throw new Error('未找到根级 SKILL.md，请选择 skill 自身的文件夹（其中应直接包含 SKILL.md）')
  }
  // 解析根级 SKILL.md 的 frontmatter 得到 name/description。
  const skillMD = stripped.find((e) => e.path === 'SKILL.md')!
  const meta = parseSkillFrontmatter(new TextDecoder().decode(skillMD.data))
  const tar = buildTar(stripped)
  return { name: meta.name, description: meta.description, tar }
}
