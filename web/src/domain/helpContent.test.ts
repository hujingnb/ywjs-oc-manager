// helpContent 测试覆盖使用手册按角色取文案的映射与未知角色降级逻辑。
// 手册是纯静态展示数据，这里重点验证角色解析不会出现空白或越权内容。
import { describe, expect, it } from 'vitest'

import { getHelpManual } from './helpContent'

describe('getHelpManual', () => {
  // 平台管理员：应取到平台手册，包含「企业」专属入口，且操作指引含「创建一个新企业」教程。
  it('returns the platform admin manual for platform_admin', () => {
    const manual = getHelpManual('platform_admin')
    expect(manual.roleLabel).toBe('平台管理员')
    expect(manual.sections.some(section => section.title === '企业')).toBe(true)
    expect(manual.guides.some(guide => guide.title === '创建一个新企业')).toBe(true)
  })

  // 企业管理员：应取到企业管理员手册，包含「账户余额」入口（仅该角色有此菜单）。
  it('returns the org admin manual for org_admin', () => {
    const manual = getHelpManual('org_admin')
    expect(manual.roleLabel).toBe('企业管理员')
    expect(manual.sections.some(section => section.title === '账户余额')).toBe(true)
  })

  // 企业成员：应取到成员手册，定位文案点明只管理自己的实例。
  it('returns the org member manual for org_member', () => {
    const manual = getHelpManual('org_member')
    expect(manual.roleLabel).toBe('企业成员')
    expect(manual.summary).toContain('分配给自己的实例')
  })

  // 未知 / 缺失角色：统一降级到权限最小的成员手册，避免抽屉空白或暴露管理内容。
  it('falls back to the org member manual for unknown or missing roles', () => {
    expect(getHelpManual('some_new_role').roleLabel).toBe('企业成员')
    expect(getHelpManual(undefined).roleLabel).toBe('企业成员')
    expect(getHelpManual(null).roleLabel).toBe('企业成员')
  })

  // 每个角色都必须既有功能介绍又有操作指引，且步骤非空，保证抽屉两大块都不为空。
  it('provides non-empty sections and step-by-step guides for every role', () => {
    for (const role of ['platform_admin', 'org_admin', 'org_member']) {
      const manual = getHelpManual(role)
      expect(manual.sections.length).toBeGreaterThan(0)
      expect(manual.guides.length).toBeGreaterThan(0)
      // 每条操作指引都必须有具体步骤，避免出现只有标题的空教程。
      expect(manual.guides.every(guide => guide.steps.length > 0)).toBe(true)
    }
  })
})
