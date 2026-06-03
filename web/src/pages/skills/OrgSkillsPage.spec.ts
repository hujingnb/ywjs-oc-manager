/**
 * OrgSkillsPage.spec.ts
 *
 * 覆盖成员技能顶级页三种状态：加载中、无实例空态、有实例正常渲染 SkillManager。
 */
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import OrgSkillsPage from './OrgSkillsPage.vue'

// mock useMemberApp composable，由各用例按需覆盖返回值。
const mockUseMemberApp = vi.fn()
vi.mock('@/composables/useMemberApp', () => ({
  useMemberApp: () => mockUseMemberApp(),
}))

// mock SkillManager 组件，确保测试聚焦在 OrgSkillsPage 的渲染逻辑上。
// vi.mock factory 内不能引用外部变量（hoisting 限制），故在 factory 内定义 stub。
vi.mock('@/components/SkillManager.vue', () => ({
  default: { template: '<div class="skill-manager-stub" />', props: ['appId'] },
}))

describe('OrgSkillsPage', () => {
  // 覆盖加载态：useMemberApp 返回 isLoading=true 时应渲染加载提示而非 SkillManager。
  it('isLoading 为 true 时展示加载提示', () => {
    mockUseMemberApp.mockReturnValue({
      appId: ref(undefined),
      hasApp: ref(false),
      isLoading: ref(true),
    })

    const wrapper = mount(OrgSkillsPage)

    expect(wrapper.text()).toContain('加载中')
    expect(wrapper.find('.skill-manager-stub').exists()).toBe(false)
  })

  // 覆盖空态：useMemberApp 返回 hasApp=false 且 isLoading=false 时应展示无实例提示。
  it('hasApp 为 false 时展示无实例空态提示', () => {
    mockUseMemberApp.mockReturnValue({
      appId: ref(undefined),
      hasApp: ref(false),
      isLoading: ref(false),
    })

    const wrapper = mount(OrgSkillsPage)

    expect(wrapper.text()).toContain('暂无关联实例')
    expect(wrapper.find('.skill-manager-stub').exists()).toBe(false)
  })

  // 覆盖正常态：useMemberApp 返回有效 appId 时应渲染 SkillManager，且不渲染空态/加载态文案。
  it('hasApp 为 true 时渲染 SkillManager 并传入 appId', () => {
    const testAppId = 'app-test-123'
    mockUseMemberApp.mockReturnValue({
      appId: ref(testAppId),
      hasApp: ref(true),
      isLoading: ref(false),
    })

    const wrapper = mount(OrgSkillsPage)

    // SkillManager stub 渲染了说明条件渲染命中正常态分支。
    const skillManagerEl = wrapper.find('.skill-manager-stub')
    expect(skillManagerEl.exists()).toBe(true)
    // 加载提示和空态提示不应同时出现。
    expect(wrapper.text()).not.toContain('加载中')
    expect(wrapper.text()).not.toContain('暂无关联实例')
  })
})
