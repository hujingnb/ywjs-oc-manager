import { describe, expect, it } from 'vitest'

import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

type Tree = { [k: string]: string | Tree }

// flattenLeaves 把消息树压成 path->string 映射；遇到非字符串/对象抛错以暴露异常结构。
function flattenLeaves(tree: Tree, prefix = '', out = new Map<string, string>()): Map<string, string> {
  for (const [key, val] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof val === 'string') out.set(path, val)
    else if (val && typeof val === 'object') flattenLeaves(val as Tree, path, out)
    else throw new Error(`非法 message 节点类型 ${path}: ${typeof val}`)
  }
  return out
}

// nodeTypes 记录每个 path 的类型（leaf/branch），用于嵌套结构一致性比对。
function nodeTypes(tree: Tree, prefix = '', out = new Map<string, 'leaf' | 'branch'>()): Map<string, 'leaf' | 'branch'> {
  for (const [key, val] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof val === 'string') out.set(path, 'leaf')
    else if (val && typeof val === 'object') {
      out.set(path, 'branch')
      nodeTypes(val as Tree, path, out)
    }
  }
  return out
}

// namedTokens 抽取 vue-i18n 命名插值 {name}/{0}；ICU 的 {x, plural,...} 含逗号不匹配，天然排除。
function namedTokens(s: string): Set<string> {
  return new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))
}

// pipeBranches 按 vue-i18n 管道复数分隔符切分，返回分支数（无管道则为 1）。
function pipeBranches(s: string): number {
  return s.split(/\s*\|\s*/).length
}

// icuCategories 抽取 ICU plural/select 的分支类别集合（如 one/other/=0）；无则空集。
function icuCategories(s: string): Set<string> {
  const m = s.match(/\{\s*\w+\s*,\s*(?:plural|select)\s*,([\s\S]*)\}/)
  if (!m) return new Set()
  return new Set([...m[1].matchAll(/(=\d+|\w+)\s*\{/g)].map((x) => x[1]))
}

const enLeaves = flattenLeaves(en as Tree)
const zhLeaves = flattenLeaves(zh as Tree)
const enTypes = nodeTypes(en as Tree)
const zhTypes = nodeTypes(zh as Tree)
const sharedLeaves = [...enLeaves.keys()].filter((k) => zhLeaves.has(k))

describe('i18n 翻译完整性', () => {
  // 双向 key 对齐：列出仅在一侧出现的 leaf path，缺/多均失败。
  it('en 与 zh 的 key 完全对齐', () => {
    const onlyEn = [...enLeaves.keys()].filter((k) => !zhLeaves.has(k))
    const onlyZh = [...zhLeaves.keys()].filter((k) => !enLeaves.has(k))
    expect({ onlyEn, onlyZh }).toEqual({ onlyEn: [], onlyZh: [] })
  })

  // 空值检测：任一侧 leaf 为空/纯空白即失败。
  it('两侧均无空文案', () => {
    const empties: string[] = []
    for (const [k, v] of enLeaves) if (!v.trim()) empties.push(`en:${k}`)
    for (const [k, v] of zhLeaves) if (!v.trim()) empties.push(`zh:${k}`)
    expect(empties).toEqual([])
  })

  // 命名插值占位符集合一致：同一 key 两侧 {name} 名字集合必须相等。
  it('共享 key 的命名占位符集合一致', () => {
    const mismatches: string[] = []
    for (const k of sharedLeaves) {
      const a = [...namedTokens(enLeaves.get(k)!)].sort().join(',')
      const b = [...namedTokens(zhLeaves.get(k)!)].sort().join(',')
      if (a !== b) mismatches.push(`${k}: en{${a}} zh{${b}}`)
    }
    expect(mismatches).toEqual([])
  })

  // en 文案不得残留中日韩表意文字（漏译防御）。
  it('en 文案无中日韩表意文字', () => {
    const cjk = [...enLeaves].filter(([, v]) => /[一-鿿]/.test(v)).map(([k]) => k)
    expect(cjk).toEqual([])
  })

  // 嵌套结构一致：同一 path 两侧 leaf/branch 类型一致，无单侧多/缺子树。
  it('en 与 zh 嵌套结构一致', () => {
    const allPaths = new Set([...enTypes.keys(), ...zhTypes.keys()])
    const diffs: string[] = []
    for (const p of allPaths) {
      if (enTypes.get(p) !== zhTypes.get(p)) diffs.push(`${p}: en=${enTypes.get(p)} zh=${zhTypes.get(p)}`)
    }
    expect(diffs).toEqual([])
  })

  // 复数结构一致：管道分支数 + ICU 分支类别两侧相等（现状无复数，防御性兜底）。
  it('共享 key 的复数结构一致', () => {
    const diffs: string[] = []
    for (const k of sharedLeaves) {
      const ev = enLeaves.get(k)!
      const zv = zhLeaves.get(k)!
      if (pipeBranches(ev) !== pipeBranches(zv)) diffs.push(`${k}: 管道分支 en=${pipeBranches(ev)} zh=${pipeBranches(zv)}`)
      const ec = [...icuCategories(ev)].sort().join(',')
      const zc = [...icuCategories(zv)].sort().join(',')
      if (ec !== zc) diffs.push(`${k}: ICU 分支 en{${ec}} zh{${zc}}`)
    }
    expect(diffs).toEqual([])
  })
})
