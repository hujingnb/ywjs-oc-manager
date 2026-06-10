import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NLayoutContent } from 'naive-ui'

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

// 角标 query 桩：返回固定 ref，避免在测试环境实例化真实 useQuery（需 QueryClient 注入）。
vi.mock('@/api/hooks/useSkillTickets', () => ({
  useSkillTicketBadgeQuery: () => ({ data: { value: 0 } }),
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

function mountLayout() {
  return mount(DashboardLayout, {
    global: {
      stubs: {
        RouterView: { template: '<section class="route-page">页面内容</section>' },
        HelpDrawer: HelpDrawerStub,
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

    // 技能市场功能为组织成员增加了「技能」顶级菜单（/skills），位于「企业知识库」之后。
    expect(menuLabels(wrapper)).toEqual(['总览', '渠道', '工作目录', '个人知识库', '企业知识库', '技能', '任务', '定时任务', '用量'])
    expect(menuKeys(wrapper)).toEqual([
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
})
