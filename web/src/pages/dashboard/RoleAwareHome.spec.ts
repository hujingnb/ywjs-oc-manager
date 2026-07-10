import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AuthUser } from '@/api'
import { i18n } from '@/i18n'
import RoleAwareHome from './RoleAwareHome.vue'

const routerReplace = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: makeAuthUser({ id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }),
}))

// makeAuthUser 生成完整 AuthUser 测试对象；status 是登录接口必返字段，避免 mock 与真实用户结构漂移。
function makeAuthUser(overrides: Partial<AuthUser>): AuthUser {
  return {
    id: 'user-1',
    username: 'user',
    display_name: '用户',
    role: 'org_member',
    status: 'enabled',
    ...overrides,
  }
}
const memberAppState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')

  return {
    appId: ref('app-1' as string | undefined),
    hasApp: ref(true),
    isLoading: ref(false),
  }
})
const organizationState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')

  return {
    data: ref({
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }),
  }
})

vi.mock('vue-router', () => ({
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  useRouter: () => ({ replace: routerReplace }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/composables/useMemberApp', () => ({
  useMemberApp: () => memberAppState,
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => organizationState,
}))

const mountedWrappers: { unmount: () => void }[] = []

// mountHome 注入 i18n 插件，RoleAwareHome 通过 useI18n() 渲染文案。
// locale 固定为 zh 使文案断言与中文原文对齐，与其他已迁移页面规范一致。
function mountHome() {
  i18n.global.locale.value = 'zh'
  const wrapper = mount(RoleAwareHome, { global: { plugins: [i18n] } })
  mountedWrappers.push(wrapper)
  return wrapper
}

describe('RoleAwareHome', () => {
  beforeEach(() => {
    routerReplace.mockClear()
    authState.user = makeAuthUser({ id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' })
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true
    memberAppState.isLoading.value = false
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }
  })

  afterEach(() => {
    mountedWrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  })

  // 覆盖平台管理员默认首页：AICC 企业概览入口调整后仍必须直接进入平台控制台。
  it('redirects platform_admin home to platform console', async () => {
    authState.user = makeAuthUser({ id: 'platform-1', username: 'platform', display_name: '平台管理员', role: 'platform_admin', org_id: undefined })

    mountHome()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/console')
  })

  // 覆盖组织成员默认首页：有唯一实例时直接进入该实例的 overview。
  it('redirects org_member home to their app overview', async () => {
    mountHome()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/apps/app-1/overview')
  })

  // 覆盖组织成员无实例边界：不能拼接缺失 appId 的路由，应进入空状态页。
  it('redirects org_member home to empty state when no app exists', async () => {
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false

    mountHome()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/apps/empty')
  })

  // 覆盖成员实例查询加载中边界：等待 useMemberApp 完成后再跳转，避免先跳空状态再闪回。
  it('redirects org_member after member app query finishes loading', async () => {
    memberAppState.isLoading.value = true

    mountHome()
    await nextTick()

    expect(routerReplace).not.toHaveBeenCalled()

    memberAppState.isLoading.value = false
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/apps/app-1/overview')
  })

  // 覆盖组织管理员首页文案：组织级知识库入口统一使用「企业知识库」。
  it('shows enterprise knowledge copy for org_admin quick card', () => {
    authState.user = makeAuthUser({ id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' })

    const wrapper = mountHome()

    expect(wrapper.text()).toContain('企业知识库')
    expect(wrapper.text()).not.toContain('知识库上传共享文件')
  })

  // 覆盖企业管理员默认落点：org_admin 不再被首页自动替换到 /org-console，而是在概览页看到子系统入口。
  it('keeps org_admin on enterprise overview and shows enabled AICC subsystem card', async () => {
    authState.user = makeAuthUser({ id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' })
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }

    const wrapper = mountHome()
    await nextTick()

    expect(routerReplace).not.toHaveBeenCalledWith('/org-console')
    expect(wrapper.text()).toContain('子系统入口')
    expect(wrapper.text()).toContain('AICC 客服')
    expect(wrapper.find('a[href="/aicc-console"]').exists()).toBe(true)
  })

  // 覆盖未开通企业边界：未开通 AICC 时概览页不能暴露客服子系统入口。
  it('hides AICC subsystem card for org_admin when AICC is disabled', () => {
    authState.user = makeAuthUser({ id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' })
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: false,
    }

    const wrapper = mountHome()

    expect(wrapper.text()).not.toContain('AICC 客服')
    expect(wrapper.find('a[href="/aicc-console"]').exists()).toBe(false)
  })
})
