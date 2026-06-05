import { describe, expect, it } from 'vitest'

import { AUXILIARY_SLOTS, emptyRouting } from './useAssistantVersions'
import type { AssistantVersionDTO, AssistantVersionFormPayload } from './useAssistantVersions'

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

  // 验证助手版本 DTO 与提交体包含行业知识库关联字段，供后续 UI 表单直接读写。
  it('助手版本类型包含行业知识库关联字段', () => {
    const version = {
      id: 'ver-1',
      name: '标准版',
      description: '默认版本',
      system_prompt: '你是助手',
      image_id: 'v2026.5.16',
      main_model: 'qwen',
      routing: {},
      skills: [],
      revision: 1,
      industry_knowledge_bases: [{ id: 'kb-risk', name: '金融风控' }],
    } satisfies AssistantVersionDTO
    const payload = {
      name: '标准版',
      description: '默认版本',
      system_prompt: '你是助手',
      image_id: 'v2026.5.16',
      main_model: 'qwen',
      routing: emptyRouting(),
      industry_knowledge_base_ids: ['kb-risk'],
    } satisfies AssistantVersionFormPayload

    expect(version.industry_knowledge_bases?.[0].name).toBe('金融风控')
    expect(payload.industry_knowledge_base_ids).toEqual(['kb-risk'])
  })
})
