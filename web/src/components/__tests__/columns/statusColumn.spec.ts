import { describe, expect, it } from 'vitest'
import { statusColumn } from '../../columns/statusColumn'
import type { StatusView } from '@/domain/status'

// statusColumn 测试只覆盖列工厂契约，具体 tone 到组件库类型的映射由 StatusBadge 测试承担。
describe('statusColumn', () => {
  it('returns column with title/key/render', () => {
    const col = statusColumn<{ status: string }>('状态', (row) => ({ label: row.status, tone: 'success' as StatusView['tone'] }))
    expect(col.title).toBe('状态')
    expect(col.key).toBe('status')
    expect(typeof col.render).toBe('function')
  })

  it('honors custom key option', () => {
    const col = statusColumn<{ s: string }>('状态', () => ({ label: '', tone: 'neutral' }), { key: 's' })
    expect(col.key).toBe('s')
  })

  it('render produces a vnode', () => {
    const col = statusColumn<{ status: string }>('状态', () => ({ label: 'X', tone: 'danger' }))
    const vnode = (col.render as any)({ status: 'X' })
    expect(vnode).toBeTruthy()
    // 渲染细节由 StatusBadge.spec.ts 覆盖；这里仅确认 render 调 formatter 并返回 vnode
  })
})
