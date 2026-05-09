import { describe, expect, it } from 'vitest'
import { timeColumn } from '../../columns/timeColumn'

describe('timeColumn', () => {
  const col = timeColumn<{ t: string | null | undefined }>('时间', (row) => row.t)

  it('formats valid ISO string', () => {
    const out = (col.render as any)({ t: '2026-05-09T10:00:00Z' })
    expect(typeof out).toBe('string')
    expect(out).not.toBe('—')
  })

  it.each([null, undefined, ''])('returns placeholder for empty value (%s)', (v) => {
    const out = (col.render as any)({ t: v })
    expect(out).toBe('—')
  })

  it('honors custom placeholder', () => {
    const c = timeColumn<{ t: string | null }>('时间', (r) => r.t, { placeholder: 'N/A' })
    expect((c.render as any)({ t: null })).toBe('N/A')
  })
})
