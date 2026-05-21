import { describe, expect, it } from 'vitest'

import { AUXILIARY_SLOTS, emptyRouting } from './useAssistantVersions'

describe('useAssistantVersions 辅助导出', () => {
  // 验证 8 个 auxiliary 槽位齐全且 key 与后端约定一致。
  it('AUXILIARY_SLOTS 含全部 8 个槽位', () => {
    const keys = AUXILIARY_SLOTS.map(s => s.key)
    expect(keys).toEqual([
      'vision', 'compression', 'web_extract', 'session_search',
      'title_generation', 'approval', 'skills_hub', 'mcp',
    ])
  })

  // 验证 emptyRouting 返回 8 个槽位且全为空字符串。
  it('emptyRouting 返回全空的 8 槽位对象', () => {
    const r = emptyRouting()
    expect(Object.keys(r)).toHaveLength(8)
    expect(Object.values(r).every(v => v === '')).toBe(true)
  })
})
