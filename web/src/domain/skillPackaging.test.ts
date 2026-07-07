// skillPackaging.test.ts — 平台技能上传前打包/校验逻辑单测。
// 覆盖：frontmatter 解析（正常/缺 fence/缺 name/引号）、扁平 tar 打包 round-trip、
// 文件夹剥层与根级 SKILL.md 校验、Markdown 打包。
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

// readTar 是测试用的极简 USTAR 读回器：解析 buildTar 产物，返回 {path -> 内容字符串}。
// 仅识别普通文件条目，遇到全零块（EOF）停止。用于验证打包字节确实可被标准 tar 解析。
function readTar(bytes: Uint8Array): Record<string, string> {
  const dec = new TextDecoder()
  const out: Record<string, string> = {}
  let off = 0
  while (off + 512 <= bytes.length) {
    const header = bytes.subarray(off, off + 512)
    // 全零块表示归档结束。
    if (header.every((b) => b === 0)) {
      break
    }
    const name = dec.decode(header.subarray(0, 100)).replace(/\0.*$/, '')
    const sizeStr = dec.decode(header.subarray(124, 136)).replace(/\0.*$/, '').trim()
    const size = parseInt(sizeStr, 8)
    off += 512
    const data = bytes.subarray(off, off + size)
    out[name] = dec.decode(data)
    // 跳过数据区并按 512 对齐。
    off += Math.ceil(size / 512) * 512
  }
  return out
}

// enc 把字符串编码为 Uint8Array，简化构造测试输入。
function enc(s: string): Uint8Array {
  return new TextEncoder().encode(s)
}

describe('parseSkillFrontmatter', () => {
  // 正常路径：标准 frontmatter，取出 name 与 description。
  it('解析出 name 与 description', () => {
    const meta = parseSkillFrontmatter('---\nname: weather\ndescription: 查天气\n---\n# 天气\n正文')
    expect(meta.name).toBe('weather')
    expect(meta.description).toBe('查天气')
  })

  // 边界：description 带双引号，应去掉两端引号。
  it('去掉 description 两端引号', () => {
    const meta = parseSkillFrontmatter('---\nname: w\ndescription: "带引号的描述"\n---\n正文')
    expect(meta.description).toBe('带引号的描述')
  })

  // 边界：缺少 description 时返回空串、不报错。
  it('缺少 description 时 description 为空串', () => {
    const meta = parseSkillFrontmatter('---\nname: only-name\n---\n正文')
    expect(meta.name).toBe('only-name')
    expect(meta.description).toBe('')
  })

  // 异常：首行不是 ---（无 frontmatter）应抛错。
  it('无 frontmatter 抛错', () => {
    expect(() => parseSkillFrontmatter('# 没有 frontmatter\n正文')).toThrow(/frontmatter/)
  })

  // 异常：frontmatter 未闭合（缺结束 ---）应抛错。
  it('frontmatter 未闭合抛错', () => {
    expect(() => parseSkillFrontmatter('---\nname: x\n正文没有结束分隔')).toThrow(/闭合/)
  })

  // 异常：缺少 name 字段应抛错。
  it('缺少 name 抛错', () => {
    expect(() => parseSkillFrontmatter('---\ndescription: 只有描述\n---\n正文')).toThrow(/name/)
  })

  // 边界：CRLF 换行也能正确解析（先归一化为 LF）。
  it('兼容 CRLF 换行', () => {
    const meta = parseSkillFrontmatter('---\r\nname: crlf-skill\r\n---\r\n正文')
    expect(meta.name).toBe('crlf-skill')
  })
})

describe('buildTar', () => {
  // 单文件打包后能被标准 tar 解析器读回，内容一致。
  it('单文件 round-trip 内容一致', () => {
    const entries: TarEntry[] = [{ path: 'SKILL.md', data: enc('hello skill') }]
    const parsed = readTar(buildTar(entries))
    expect(parsed['SKILL.md']).toBe('hello skill')
  })

  // 多文件 + 子目录路径，结构与内容均保留。
  it('多文件含子目录 round-trip', () => {
    const entries: TarEntry[] = [
      { path: 'SKILL.md', data: enc('main') },
      { path: 'scripts/run.sh', data: enc('echo hi') },
    ]
    const parsed = readTar(buildTar(entries))
    expect(parsed['SKILL.md']).toBe('main')
    expect(parsed['scripts/run.sh']).toBe('echo hi')
  })

  // 归档总长度应是 512 的整数倍（头部 + 数据补齐 + 结尾零块都对齐）。
  it('归档字节按 512 对齐', () => {
    const tar = buildTar([{ path: 'SKILL.md', data: enc('x') }])
    expect(tar.length % 512).toBe(0)
  })

  // 异常：路径超过 100 字节抛错。
  it('路径过长抛错', () => {
    const longPath = 'a/'.repeat(60) + 'SKILL.md' // 远超 100 字节
    expect(() => buildTar([{ path: longPath, data: enc('x') }])).toThrow(/过长/)
  })
})

describe('packFromMarkdown', () => {
  // 粘贴 MD 打包：name/description 来自 frontmatter，tar 内含根级 SKILL.md 且内容为原文。
  it('打包出根级 SKILL.md 且 meta 取自 frontmatter', () => {
    const md = '---\nname: greet\ndescription: 打招呼\n---\n# greet\n正文'
    const res = packFromMarkdown(md)
    expect(res.name).toBe('greet')
    expect(res.description).toBe('打招呼')
    const parsed = readTar(res.tar)
    expect(parsed['SKILL.md']).toBe(md)
    // 不应套一层目录：只能有根级 SKILL.md。
    expect(Object.keys(parsed)).toEqual(['SKILL.md'])
  })

  // 异常：缺 name 的 MD 直接抛错（不产出 tar）。
  it('frontmatter 缺 name 抛错', () => {
    expect(() => packFromMarkdown('---\ndescription: x\n---\n正文')).toThrow(/name/)
  })
})

describe('packFromFolder', () => {
  // 文件夹打包：剥掉顶层目录名后内容落到归档根，SKILL.md 在根级，子目录结构保留。
  it('剥掉顶层目录并保留子目录结构', () => {
    const files: UploadedFile[] = [
      { relativePath: 'weather/SKILL.md', data: enc('---\nname: weather\n---\n正文') },
      { relativePath: 'weather/scripts/run.sh', data: enc('echo hi') },
    ]
    const res = packFromFolder(files)
    expect(res.name).toBe('weather')
    const parsed = readTar(res.tar)
    // 顶层 weather/ 被剥离：SKILL.md 落到归档根，子目录 scripts/ 保留。
    expect(parsed['SKILL.md']).toBe('---\nname: weather\n---\n正文')
    expect(parsed['scripts/run.sh']).toBe('echo hi')
    expect(parsed['weather/SKILL.md']).toBeUndefined()
  })

  // 异常：剥层后根级没有 SKILL.md（SKILL.md 嵌在更深一层）应抛错。
  it('根级缺 SKILL.md 抛错', () => {
    const files: UploadedFile[] = [
      { relativePath: 'parent/inner/SKILL.md', data: enc('---\nname: x\n---\n') },
    ]
    expect(() => packFromFolder(files)).toThrow(/根级 SKILL.md/)
  })

  // 异常：空文件列表抛错。
  it('空文件夹抛错', () => {
    expect(() => packFromFolder([])).toThrow(/为空/)
  })

  // 异常：剥层后路径含 .. 越界条目应抛错。
  it('越界路径抛错', () => {
    const files: UploadedFile[] = [
      { relativePath: 'skill/SKILL.md', data: enc('---\nname: x\n---\n') },
      { relativePath: 'skill/../escape.txt', data: enc('evil') },
    ]
    expect(() => packFromFolder(files)).toThrow(/非法路径/)
  })
})

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

  // 传入非 zip 的随机字节：unzipSync 失败应抛「zip 解压失败」提示。
  it('损坏/非 zip 字节抛解压失败', () => {
    const garbage = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8])
    expect(() => packFromZip(garbage)).toThrow(/解压失败/)
  })

  // 空 zip（无任何条目）：过滤后没有可用文件应抛「zip 内没有可用文件」。
  it('空 zip 抛无可用文件', () => {
    const zip = zipSync({})
    expect(() => packFromZip(zip)).toThrow(/没有可用文件/)
  })
})
