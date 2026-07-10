// AICC 前端领域 helper 测试覆盖公开入口渠道归一化，避免挂件流量被误记为独立链接。
import { describe, expect, it } from 'vitest'

import { normalizeAICCPublicChannel } from './aicc'

describe('normalizeAICCPublicChannel', () => {
  // 场景：网页挂件 iframe 会带 aicc_channel=web_widget，创建会话时应按挂件渠道记录。
  it('keeps the widget channel from the public chat query', () => {
    expect(normalizeAICCPublicChannel('web_widget')).toBe('web_widget')
  })

  // 场景：公开链接或异常 query 不应扩展出未知渠道，避免后端 CHECK 约束拒绝请求。
  it('falls back to web_link for missing or unknown channels', () => {
    expect(normalizeAICCPublicChannel(undefined)).toBe('web_link')
    expect(normalizeAICCPublicChannel('voice')).toBe('web_link')
    expect(normalizeAICCPublicChannel(['web_widget'])).toBe('web_link')
  })
})
