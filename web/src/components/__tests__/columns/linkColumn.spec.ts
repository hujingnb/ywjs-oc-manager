import { describe, expect, it, vi } from 'vitest'
import { linkColumn } from '../../columns/linkColumn'

// linkColumn 测试确保主链接列按业务回调渲染，副标题为空时不返回数组占位。
describe('linkColumn', () => {
  it('returns column with title/key/render', () => {
    const col = linkColumn<{ id: string; name: string }>({
      title: '名称',
      text: (r) => r.name,
      onClick: vi.fn(),
    })
    expect(col.title).toBe('名称')
    expect(col.key).toBe('link')
    expect(typeof col.render).toBe('function')
  })

  it('subtitle 存在时返回 vnode 数组（含 small）', () => {
    const col = linkColumn<{ name: string; sub: string | null }>({
      title: '名称',
      text: (r) => r.name,
      onClick: vi.fn(),
      subtitle: (r) => r.sub,
    })
    const withSub = (col.render as any)({ name: 'A', sub: '辅助文字' })
    expect(Array.isArray(withSub)).toBe(true)
    const noSub = (col.render as any)({ name: 'A', sub: null })
    expect(Array.isArray(noSub)).toBe(false)
  })
})
