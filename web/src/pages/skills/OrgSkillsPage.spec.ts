/**
 * OrgSkillsPage.spec.ts
 *
 * 覆盖技能顶级页三种状态：加载中、无实例（仍渲染工单面板 + banner）、有实例正常渲染 SkillManager。
 * 此页现服务 org_member 与 org_admin 两类用户，统一通过 useOwnApp 取各自实例。
 */
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import OrgSkillsPage from './OrgSkillsPage.vue'

// mock useOwnApp composable，由各用例按需覆盖返回值。
const mockUseOwnApp = vi.fn()
vi.mock('@/composables/useOwnApp', () => ({
  useOwnApp: () => mockUseOwnApp(),
}))

// mock SkillManager 组件，确保测试聚焦在 OrgSkillsPage 的渲染逻辑上。
// vi.mock factory 内不能引用外部变量（hoisting 限制），故在 factory 内定义 stub。
vi.mock('@/components/SkillManager.vue', () => ({
  default: { template: '<div class="skill-manager-stub" />', props: ['appId'] },
}))

// mock SkillTicketPanel 组件：无实例态用它承载工单提交/跟踪，stub 出可断言的标记元素。
vi.mock('@/components/SkillTicketPanel.vue', () => ({
  default: { template: '<div class="skill-ticket-panel-stub" />', emits: ['goInstall'] },
}))

// mock naive-ui 的 useMessage：无实例「去安装」会调用 message.info，测试中只需提供占位实现。
vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return { ...actual, useMessage: () => ({ info: vi.fn() }) }
})

describe('OrgSkillsPage', () => {
  // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  // 覆盖加载态：useOwnApp 返回 isLoading=true 时应渲染加载提示，不渲染 SkillManager / 工单面板。
  it('isLoading 为 true 时展示加载提示', () => {
    mockUseOwnApp.mockReturnValue({
      appId: ref(undefined),
      hasApp: ref(false),
      isLoading: ref(true),
      app: ref(null),
    })

    const wrapper = mount(OrgSkillsPage, { global: { plugins: [i18n] } })

    expect(wrapper.text()).toContain('加载中')
    expect(wrapper.find('.skill-manager-stub').exists()).toBe(false)
    expect(wrapper.find('.skill-ticket-panel-stub').exists()).toBe(false)
  })

  // 覆盖无实例态：hasApp=false 且非加载中时，渲染提示 banner + 工单面板，但不渲染 SkillManager。
  it('hasApp 为 false 时展示提示 banner 并渲染工单面板', () => {
    mockUseOwnApp.mockReturnValue({
      appId: ref(undefined),
      hasApp: ref(false),
      isLoading: ref(false),
      app: ref(null),
    })

    const wrapper = mount(OrgSkillsPage, { global: { plugins: [i18n] } })

    // banner 文案提示可提交需求、交付后需有实例才能安装。
    expect(wrapper.text()).toContain('你还没有实例')
    // 工单面板仍渲染，保证无实例用户也能提交/跟踪定制技能需求。
    expect(wrapper.find('.skill-ticket-panel-stub').exists()).toBe(true)
    // 无实例不渲染 SkillManager（无 appId 可传）。
    expect(wrapper.find('.skill-manager-stub').exists()).toBe(false)
  })

  // 覆盖有实例态：useOwnApp 返回有效 appId 时应渲染 SkillManager，且不渲染无实例 banner / 工单面板。
  it('hasApp 为 true 时渲染 SkillManager 并传入 appId', () => {
    const testAppId = 'app-test-123'
    mockUseOwnApp.mockReturnValue({
      appId: ref(testAppId),
      hasApp: ref(true),
      isLoading: ref(false),
      app: ref({ id: testAppId }),
    })

    const wrapper = mount(OrgSkillsPage, { global: { plugins: [i18n] } })

    // SkillManager stub 渲染了说明条件渲染命中有实例态分支。
    const skillManagerEl = wrapper.find('.skill-manager-stub')
    expect(skillManagerEl.exists()).toBe(true)
    // 加载提示和无实例 banner 不应出现，工单面板也不在此态单独渲染。
    expect(wrapper.text()).not.toContain('加载中')
    expect(wrapper.text()).not.toContain('你还没有实例')
    expect(wrapper.find('.skill-ticket-panel-stub').exists()).toBe(false)
  })
})
