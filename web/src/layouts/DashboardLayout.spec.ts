import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NLayoutContent } from 'naive-ui'

import DashboardLayout from './DashboardLayout.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/runtime-nodes' }))
const logout = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' },
  isPlatformAdmin: true,
  isOrgAdmin: false,
  isOrgMember: false,
  logout,
}))
const memberAppState = vi.hoisted(() => ({
  appId: { value: undefined as string | undefined },
  hasApp: { value: false },
  isLoading: { value: false },
}))

vi.mock('vue-router', () => ({
  RouterView: { template: '<section class="route-page">页面内容</section>' },
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush, replace: routerReplace }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/composables/useMemberApp', () => ({
  useMemberApp: () => memberAppState,
}))

// HelpDrawerStub 暴露 show/role 到 DOM，便于测试父布局点击入口后是否正确打开手册抽屉。
const HelpDrawerStub = {
  props: ['show', 'role'],
  template: '<aside data-test="help-drawer" :data-show="String(show)" :data-role="role" />',
}

const MenuStub = {
  props: ['options', 'value'],
  emits: ['update:value'],
  template: `
    <nav data-test="menu" :data-value="value">
      <button
        v-for="option in options"
        :key="option.key"
        data-test="menu-item"
        :data-key="option.key"
        @click="$emit('update:value', option.key)"
      >
        {{ option.label }}
      </button>
    </nav>
  `,
}

function mountLayout() {
  return mount(DashboardLayout, {
    global: {
      stubs: {
        RouterView: { template: '<section class="route-page">页面内容</section>' },
        HelpDrawer: HelpDrawerStub,
        NMenu: MenuStub,
        Menu: MenuStub,
        'n-menu': MenuStub,
      },
    },
  })
}

function menuLabels(wrapper: ReturnType<typeof mountLayout>) {
  return wrapper.findAll('[data-test="menu-item"]').map(item => item.text())
}

function menuKeys(wrapper: ReturnType<typeof mountLayout>) {
  return wrapper.findAll('[data-test="menu-item"]').map(item => item.attributes('data-key'))
}

describe('DashboardLayout', () => {
  beforeEach(() => {
    routeState.path = '/runtime-nodes'
    routerPush.mockClear()
    routerReplace.mockClear()
    logout.mockClear()
    authState.user = { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = true
    authState.isOrgAdmin = false
    authState.isOrgMember = false
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false
    memberAppState.isLoading.value = false
  })

  // 覆盖后台整体骨架：内容区必须给子页面提供可撑满的剩余高度。
  it('wraps routed pages in a fill-height content frame', () => {
    const wrapper = mountLayout()
    const content = wrapper.findComponent(NLayoutContent)

    expect(content.props('contentStyle')).toContain('height: calc(100vh - 64px)')
    expect(content.props('contentStyle')).toContain('display: flex')
    expect(wrapper.find('.dashboard-page-frame').exists()).toBe(true)
  })

  // 覆盖右上角使用手册入口：必须显示明确文案，点击后仍打开按角色渲染的手册抽屉。
  it('renders the help manual entry as text and opens the drawer', async () => {
    const wrapper = mountLayout()
    const helpButton = wrapper.findAll('button').find(button => button.text().trim() === '使用手册')

    expect(helpButton).toBeTruthy()

    await helpButton!.trigger('click')

    expect(wrapper.find('[data-test="help-drawer"]').attributes('data-show')).toBe('true')
    expect(wrapper.find('[data-test="help-drawer"]').attributes('data-role')).toBe('platform_admin')
  })

  // 覆盖组织成员菜单：唯一实例的各个业务 tab 被拉平到左侧菜单。
  it('renders flattened app entries for org_member', () => {
    routeState.path = '/apps/app-1/overview'
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).toEqual(['总览', '渠道', '工作目录', '个人知识库', '企业知识库', '任务', '定时任务', '用量'])
    expect(menuKeys(wrapper)).toEqual([
      '/apps/app-1/overview',
      '/apps/app-1/channels',
      '/apps/app-1/workspace',
      '/apps/app-1/knowledge',
      '/knowledge',
      '/apps/app-1/kanban',
      '/apps/app-1/cron',
      '/usage',
    ])
  })

  // 覆盖组织成员当前路由高亮：任务页应选中左侧「任务」而不是旧的「实例」入口。
  it('selects the matching flattened member app entry', () => {
    routeState.path = '/apps/app-1/kanban'
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    memberAppState.appId.value = 'app-1'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()

    expect(wrapper.find('[data-test="menu"]').attributes('data-value')).toBe('/apps/app-1/kanban')
  })

  // 覆盖成员无实例边界：实例能力入口统一落到空状态页，避免生成缺少 appId 的坏路由。
  it('points member app entries to empty state when member has no app', async () => {
    routeState.path = '/apps/empty'
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false

    const wrapper = mountLayout()
    const appItems = wrapper.findAll('[data-test="menu-item"]')
      .filter(item => item.attributes('data-key')?.startsWith('member-empty-'))
    const appKeys = appItems.map(item => item.attributes('data-key'))

    expect(new Set(appKeys).size).toBe(6)
    expect(wrapper.find('[data-test="menu"]').attributes('data-value')).toBe('member-empty-overview')

    for (const item of appItems) {
      await item.trigger('click')
    }
    expect(routerPush.mock.calls.slice(-6).map(([path]) => path)).toEqual(['/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty'])
  })

  // 覆盖非成员菜单文案：组织级知识库统一叫「企业知识库」，但管理员仍保留「实例」入口。
  it('renames organization knowledge entry for non-member menus', () => {
    routeState.path = '/knowledge'
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).toContain('实例')
    expect(menuLabels(wrapper)).toContain('企业知识库')
    expect(menuLabels(wrapper)).not.toContain('知识库')
  })
})
