import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NLayoutContent } from 'naive-ui'

import { i18n } from '@/i18n'
import DashboardLayout from './DashboardLayout.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/runtime-nodes' }))
const logout = vi.hoisted(() => vi.fn())
const changePassword = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' },
  isPlatformAdmin: true,
  isOrgAdmin: false,
  isOrgMember: false,
  logout,
  changePassword,
}))
const memberAppState = vi.hoisted(() => ({
  appId: { value: undefined as string | undefined },
  hasApp: { value: false },
  isLoading: { value: false },
}))
const setPerspective = vi.hoisted(() => vi.fn())
const resetPerspective = vi.hoisted(() => vi.fn())
const adminPerspectiveState = vi.hoisted(() => ({
  perspective: { value: 'manage' as 'manage' | 'instance' },
  setPerspective,
  resetPerspective,
}))
const organizationState = vi.hoisted(() => ({
  data: {
    value: {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    } as Record<string, unknown> | null,
  },
}))

vi.mock('vue-router', () => ({
  RouterView: { template: '<section class="route-page">页面内容</section>' },
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush, replace: routerReplace }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/composables/useOwnApp', () => ({
  useOwnApp: () => memberAppState,
}))

vi.mock('@/composables/useAdminPerspective', () => ({
  useAdminPerspective: () => adminPerspectiveState,
}))

// 角标 query 桩：返回固定 ref，避免在测试环境实例化真实 useQuery（需 QueryClient 注入）。
vi.mock('@/api/hooks/useSkillTickets', () => ({
  useSkillTicketBadgeQuery: () => ({ data: { value: 0 } }),
}))

// 企业 web-publish 配置 query 桩：布局测试只关心菜单结构，不需要真实 Vue Query client。
vi.mock('@/api/hooks/useWebPublish', () => ({
  useWebPublishConfigQuery: () => ({ data: { value: null } }),
}))

// 当前企业详情 query 桩：org_admin 菜单需要读取 aicc_enabled 决定是否展示 AICC 入口。
vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => organizationState,
}))

// HelpDrawerStub 暴露 show/role 到 DOM，便于测试父布局点击入口后是否正确打开手册抽屉。
const HelpDrawerStub = {
  props: ['show', 'role'],
  template: '<aside data-test="help-drawer" :data-show="String(show)" :data-role="role" />',
}

// LocaleSwitcherStub：语言选择器占位桩，避免挂载真实组件时因缺少 i18n/Pinia 插件而抛错。
const LocaleSwitcherStub = {
  props: ['persist'],
  template: '<div data-test="locale-switcher" />',
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

const ButtonStub = defineComponent({
  props: ['disabled', 'loading'],
  emits: ['click'],
  setup(props, { slots, emit }) {
    return () => h('button', {
      disabled: props.disabled || props.loading,
      onClick: () => emit('click'),
    }, [slots.icon?.(), slots.default?.()])
  },
})

const ModalStub = defineComponent({
  inheritAttrs: false,
  props: ['show'],
  setup(props, { attrs, slots }) {
    return () => props.show
      ? h('section', { ...attrs, 'data-show': String(props.show) }, slots.default?.())
      : null
  },
})

const FormStub = defineComponent({
  inheritAttrs: false,
  emits: ['submit'],
  setup(_, { attrs, slots, emit }) {
    return () => h('form', {
      ...attrs,
      onSubmit: (event: Event) => {
        event.preventDefault()
        emit('submit', event)
      },
    }, slots.default?.())
  },
})

const FormItemStub = defineComponent({
  props: ['label'],
  setup(props, { slots }) {
    return () => h('label', [h('span', props.label), slots.default?.()])
  },
})

const InputStub = defineComponent({
  props: ['value', 'type'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('input', {
      type: props.type ?? 'text',
      value: props.value,
      onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
    })
  },
})

const SpaceStub = defineComponent({
  setup(_, { slots }) {
    return () => h('div', slots.default?.())
  },
})

const AlertStub = defineComponent({
  setup(_, { slots }) {
    return () => h('p', { role: 'alert' }, slots.default?.())
  },
})

// mountLayout：挂载 DashboardLayout，注入 i18n 插件（布局使用 useI18n() 渲染导航、顶栏与弹窗文案）。
// i18n locale 设置为 zh，保持各测试用例中的中文文案断言不变。
function mountLayout() {
  i18n.global.locale.value = 'zh'
  return mount(DashboardLayout, {
    global: {
      plugins: [i18n],
      stubs: {
        RouterView: { template: '<section class="route-page">页面内容</section>' },
        HelpDrawer: HelpDrawerStub,
        LocaleSwitcher: LocaleSwitcherStub,
        NAlert: AlertStub,
        NButton: ButtonStub,
        NForm: FormStub,
        NFormItem: FormItemStub,
        NInput: InputStub,
        NModal: ModalStub,
        NMenu: MenuStub,
        NSpace: SpaceStub,
        Alert: AlertStub,
        Button: ButtonStub,
        Form: FormStub,
        FormItem: FormItemStub,
        Input: InputStub,
        Modal: ModalStub,
        'n-alert': AlertStub,
        'n-button': ButtonStub,
        'n-form': FormStub,
        'n-form-item': FormItemStub,
        'n-input': InputStub,
        'n-modal': ModalStub,
        Menu: MenuStub,
        'n-menu': MenuStub,
        Space: SpaceStub,
        'n-space': SpaceStub,
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
    changePassword.mockClear()
    changePassword.mockResolvedValue(undefined)
    authState.user = { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = true
    authState.isOrgAdmin = false
    authState.isOrgMember = false
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false
    memberAppState.isLoading.value = false
    adminPerspectiveState.perspective.value = 'manage'
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }
    setPerspective.mockReset()
    resetPerspective.mockReset()
    // setPerspective 桩实现:更新视角 ref,使菜单在切换后重新计算
    setPerspective.mockImplementation((next: 'manage' | 'instance') => {
      adminPerspectiveState.perspective.value = next
    })
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

    // 「对话」置于成员菜单首位（最常用）；「技能」顶级菜单（/skills）位于「企业知识库」之后。
    expect(menuLabels(wrapper)).toEqual(['对话', '总览', '渠道', '工作目录', '个人知识库', '企业知识库', '技能', '任务', '定时任务', '用量'])
    expect(menuKeys(wrapper)).toEqual([
      '/apps/app-1/conversations',
      '/apps/app-1/overview',
      '/apps/app-1/channels',
      '/apps/app-1/workspace',
      '/apps/app-1/knowledge',
      '/knowledge',
      '/skills',
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

    // 成员实例能力入口共 7 个（含「对话」）：conversations/overview/channels/workspace/knowledge/kanban/cron。
    expect(new Set(appKeys).size).toBe(7)
    expect(wrapper.find('[data-test="menu"]').attributes('data-value')).toBe('member-empty-overview')

    for (const item of appItems) {
      await item.trigger('click')
    }
    expect(routerPush.mock.calls.slice(-7).map(([path]) => path)).toEqual(['/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty', '/apps/empty'])
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

  // 覆盖 org_admin 企业管理视角菜单:管理项齐全,且「技能」已迁出(不在此视角)。
  it('renders management menu without skills for org_admin manage perspective', () => {
    routeState.path = '/'
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    adminPerspectiveState.perspective.value = 'manage'

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).toEqual(['总览', '成员', 'AICC 客服', '已发布站点', '实例', '企业知识库', '账户余额', '审计', '用量'])
    expect(menuLabels(wrapper)).not.toContain('技能')
  })

  // 覆盖企业未开通 AICC 的菜单裁剪：org_admin 不应看到不可用的 AICC 客服入口。
  it('hides AICC menu for org_admin when organization has not enabled AICC', () => {
    routeState.path = '/'
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: false,
    }

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).not.toContain('AICC 客服')
  })

  // 覆盖 org_admin 我的实例视角菜单:与组织成员同款(含「技能」),由自有实例 appId 驱动。
  it('renders member-style menu for org_admin instance perspective', () => {
    routeState.path = '/apps/admin-app/overview'
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    adminPerspectiveState.perspective.value = 'instance'
    memberAppState.appId.value = 'admin-app'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()

    expect(menuLabels(wrapper)).toEqual(['对话', '总览', '渠道', '工作目录', '个人知识库', '企业知识库', '技能', '任务', '定时任务', '用量'])
    expect(menuKeys(wrapper)).toEqual([
      '/apps/admin-app/conversations',
      '/apps/admin-app/overview',
      '/apps/admin-app/channels',
      '/apps/admin-app/workspace',
      '/apps/admin-app/knowledge',
      '/knowledge',
      '/skills',
      '/apps/admin-app/kanban',
      '/apps/admin-app/cron',
      '/usage',
    ])
  })

  // 覆盖 org_admin 实例视角当前高亮:在 /apps/:id/:tab 上应高亮对应实例 tab(复用成员高亮逻辑)。
  it('highlights instance tab for org_admin instance perspective', () => {
    routeState.path = '/apps/admin-app/channels'
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    adminPerspectiveState.perspective.value = 'instance'
    memberAppState.appId.value = 'admin-app'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()

    expect(wrapper.find('[data-test="menu"]').attributes('data-value')).toBe('/apps/admin-app/channels')
  })

  // 覆盖侧边栏用户区改密入口：已登录用户点击「修改密码」后应打开弹窗表单。
  it('opens the password modal from sidebar footer', async () => {
    const wrapper = mountLayout()
    const passwordButton = wrapper.findAll('button').find(button => button.text().includes('修改密码'))

    expect(passwordButton).toBeTruthy()

    await passwordButton!.trigger('click')

    expect(wrapper.find('[data-test="password-modal"]').exists()).toBe(true)
  })

  // 覆盖客户端确认密码校验：两次新密码不一致时不应调用后端改密接口。
  it('rejects mismatched confirmation before submitting', async () => {
    const wrapper = mountLayout()
    const passwordButton = wrapper.findAll('button').find(button => button.text().includes('修改密码'))

    await passwordButton!.trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('old-password')
    await inputs[1].setValue('new-password-123')
    await inputs[2].setValue('different-password')
    await wrapper.find('[data-test="password-form"]').trigger('submit')

    expect(changePassword).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('两次输入的新密码不一致')
  })

  // 覆盖改密请求进行中重复提交边界：挂起中的请求只能被提交一次，防止双击或回车重复调用后端。
  it('ignores duplicate password submissions while request is pending', async () => {
    let resolvePasswordChange!: () => void
    const pendingPasswordChange = new Promise<void>((resolve) => {
      resolvePasswordChange = resolve
    })
    changePassword.mockReturnValue(pendingPasswordChange)
    const wrapper = mountLayout()
    const passwordButton = wrapper.findAll('button').find(button => button.text().includes('修改密码'))

    await passwordButton!.trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('old-password')
    await inputs[1].setValue('new-password-123')
    await inputs[2].setValue('new-password-123')
    const form = wrapper.find('[data-test="password-form"]')
    await form.trigger('submit')
    await form.trigger('submit')

    expect(changePassword).toHaveBeenCalledTimes(1)

    resolvePasswordChange()
    await flushPromises()
  })

  // 覆盖改密成功流程：提交匹配的新密码后调用 auth store，并跳转登录页重新登录。
  it('submits password change and redirects to login on success', async () => {
    const wrapper = mountLayout()
    const passwordButton = wrapper.findAll('button').find(button => button.text().includes('修改密码'))

    await passwordButton!.trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('old-password')
    await inputs[1].setValue('new-password-123')
    await inputs[2].setValue('new-password-123')
    await wrapper.find('[data-test="password-form"]').trigger('submit')
    await flushPromises()

    expect(changePassword).toHaveBeenCalledWith('old-password', 'new-password-123')
    expect(routerReplace).toHaveBeenCalledWith('/login')
  })

  // 覆盖切换器可见性:仅 org_admin 渲染视角切换;platform_admin 不渲染。
  it('shows perspective switch only for org_admin', () => {
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false

    const adminWrapper = mountLayout()
    const adminButtons = adminWrapper.findAll('button').map(b => b.text().trim())
    expect(adminButtons).toContain('企业管理')
    expect(adminButtons).toContain('我的实例')

    authState.isOrgAdmin = false
    authState.isPlatformAdmin = true
    authState.user = { id: 'admin-1', username: 'admin', display_name: 'admin', role: 'platform_admin', org_id: 'org-1' }
    const platformWrapper = mountLayout()
    const platformButtons = platformWrapper.findAll('button').map(b => b.text().trim())
    expect(platformButtons).not.toContain('我的实例')

    authState.isPlatformAdmin = false
    authState.isOrgAdmin = false
    authState.isOrgMember = true
    authState.user = { id: 'member-1', username: 'member', display_name: '成员', role: 'org_member', org_id: 'org-1' }
    const memberWrapper = mountLayout()
    const memberButtons = memberWrapper.findAll('button').map(b => b.text().trim())
    expect(memberButtons).not.toContain('企业管理')
    expect(memberButtons).not.toContain('我的实例')
  })

  // 覆盖切到我的实例视角(无自有实例):应持久化视角并导航到空状态页。
  it('switches to instance perspective and navigates to empty state when no own app', async () => {
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    memberAppState.appId.value = undefined
    memberAppState.hasApp.value = false

    const wrapper = mountLayout()
    const instanceButton = wrapper.findAll('button').find(b => b.text().trim() === '我的实例')
    await instanceButton!.trigger('click')

    expect(setPerspective).toHaveBeenCalledWith('instance')
    expect(routerPush).toHaveBeenCalledWith('/apps/empty')
  })

  // 覆盖切到我的实例视角(有自有实例):应导航到自有实例总览。
  it('switches to instance perspective and navigates to own app overview', async () => {
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    memberAppState.appId.value = 'admin-app'
    memberAppState.hasApp.value = true

    const wrapper = mountLayout()
    const instanceButton = wrapper.findAll('button').find(b => b.text().trim() === '我的实例')
    await instanceButton!.trigger('click')

    expect(setPerspective).toHaveBeenCalledWith('instance')
    expect(routerPush).toHaveBeenCalledWith('/apps/admin-app/overview')
  })

  // 覆盖切回企业管理视角:应持久化并导航到管理总览根路由。
  it('switches back to manage perspective and navigates to root', async () => {
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    adminPerspectiveState.perspective.value = 'instance'

    const wrapper = mountLayout()
    const manageButton = wrapper.findAll('button').find(b => b.text().trim() === '企业管理')
    await manageButton!.trigger('click')

    expect(setPerspective).toHaveBeenCalledWith('manage')
    expect(routerPush).toHaveBeenCalledWith('/')
  })

  // 覆盖退出登录:除清登录态外还需清除视角持久化,避免换账号串视角。
  it('resets perspective on logout', async () => {
    authState.user = { id: 'org-admin-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    authState.isPlatformAdmin = false
    authState.isOrgAdmin = true
    authState.isOrgMember = false
    logout.mockResolvedValue(undefined)

    const wrapper = mountLayout()
    const logoutButton = wrapper.findAll('button').find(b => b.text().includes('退出'))
    await logoutButton!.trigger('click')
    await flushPromises()

    expect(resetPerspective).toHaveBeenCalled()
  })
})
