import { describe, expect, it, vi } from 'vitest'
import { actionColumn } from '../../columns/actionColumn'

// actionColumn 测试确保行操作列保持默认 key，并能处理隐藏、禁用和动态文案。
describe('actionColumn', () => {
  // 默认标题取全局 i18n common.table.actions；测试环境 i18n 默认 locale 为 en，故为 'Actions'。
  it('default title 取 i18n common.table.actions and key=actions', () => {
    const col = actionColumn<{ id: string }>([])
    expect(col.title).toBe('Actions')
    expect(col.key).toBe('actions')
  })

  it('honors custom title/key', () => {
    const col = actionColumn<{ id: string }>([], { title: '动作', key: 'ops' })
    expect(col.title).toBe('动作')
    expect(col.key).toBe('ops')
  })

  it('render returns a vnode (NSpace) when actions provided', () => {
    const col = actionColumn<{ id: string }>([
      { label: 'A', onClick: vi.fn() },
    ])
    const vnode = (col.render as any)({ id: '1' })
    expect(vnode).toBeTruthy()
  })

  it('render does not throw with hidden / disabled / function-label actions', () => {
    const col = actionColumn<{ id: string; show: boolean }>([
      { label: 'A', onClick: vi.fn() },
      { label: 'B', onClick: vi.fn(), hidden: (r) => !r.show },
      { label: (r) => `编辑-${r.id}`, onClick: vi.fn(), disabled: () => true },
    ])
    expect(() => (col.render as any)({ id: '1', show: false })).not.toThrow()
  })
})
