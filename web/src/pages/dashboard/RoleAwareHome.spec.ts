import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import RoleAwareHome from './RoleAwareHome.vue'

const routerReplace = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' },
}))
const memberAppState = vi.hoisted(() => ({
  appId: { value: 'app-1' as string | undefined },
  hasApp: { value: true },
  isLoading: { value: false },
}))

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

function mountHome() {
  return mount(RoleAwareHome)
}

describe('RoleAwareHome', () => {
  beforeEach(() => {
    routerReplace.mockClear()
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true
    memberAppState.isLoading.value = false
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

  // 覆盖成员实例查询加载中边界：等待 useMemberApp 完成，避免先跳空状态再闪回。
  it('does not redirect org_member while member app query is loading', async () => {
    memberAppState.isLoading.value = true

    mountHome()
    await nextTick()

    expect(routerReplace).not.toHaveBeenCalled()
  })

  // 覆盖组织管理员首页文案：组织级知识库入口统一使用「企业知识库」。
  it('shows enterprise knowledge copy for org_admin quick card', () => {
    authState.user = { id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountHome()

    expect(wrapper.text()).toContain('企业知识库')
    expect(wrapper.text()).not.toContain('知识库上传共享文件')
  })
})
