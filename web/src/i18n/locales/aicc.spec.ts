import { describe, expect, it } from 'vitest'

import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

describe('AICC i18n namespace', () => {
  // 覆盖 AICC 前端用户可见文案接入统一 i18n 命名空间。
  it('provides zh and en AICC messages', () => {
    expect(zh.aicc.manager.title).toBe('AICC 接待台')
    expect(en.aicc.manager.title).toBe('AICC Desk')
  })
})
