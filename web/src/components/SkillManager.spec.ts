// SkillManager.spec.ts — SkillManager 复用组件单元测试。
// 覆盖：四类 status 徽章渲染、protected 隐藏卸载、市场安装按钮、无权限时操作隐藏。
import { flushPromises, mount } from '@vue/test-utils'
import { computed, defineComponent, h, nextTick, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import SkillManager from './SkillManager.vue'
import type { AppSkill, SkillEntry } from '@/api'

// ======================== hoisted mocks ========================
// vi.hoisted 内只能使用 vi.fn()，不可用 ref()（hoisting 早于模块初始化）。
const mocks = vi.hoisted(() => ({
  // mutation 执行函数。
  installMutateAsync: vi.fn(),
  uninstallMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn(),
  reinstallMutateAsync: vi.fn(),

  // 权限控制。
  canManage: vi.fn(() => true),

  // 消息提示。
  messageSuccess: vi.fn(),
  messageError: vi.fn(),

  // 对话框。
  dialogWarning: vi.fn(),
}))

// ======================== column 渲染辅助 ========================
type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}

// DataTableStub 渲染 columns.render 结果，使测试可断言徽章/按钮内容。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () =>
      h('div', [
        h(
          'div',
          { class: 'headers' },
          props.columns.map((col) => h('span', { class: `header-${col.key}` }, col.title)),
        ),
        ...props.data.flatMap((row) =>
          props.columns.map((col) =>
            h('div', { class: `cell-${col.key}` }, col.render ? [col.render(row) as VNodeChild] : []),
          ),
        ),
      ])
  },
})

// ======================== 可变 reactive 状态（在 vi.mock 外部定义） ========================
// appSkillsState 用于控制 useAppSkillsQuery 的返回值。
const appSkillsState = {
  data: ref<AppSkill[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// marketState 用于控制 useSkillMarketQuery 的返回值。
// data 仍以扁平 { entries } 存放（各用例直接赋值），mock 工厂再包装成 useInfiniteQuery
// 的 { pages } 形状；hasNextPage/isFetchingNextPage/fetchNextPage 覆盖「加载更多」行为。
const marketState = {
  data: ref<{ entries: SkillEntry[] }>({ entries: [] }),
  isLoading: ref(false),
  error: ref<Error | null>(null),
  hasNextPage: ref(false),
  isFetchingNextPage: ref(false),
  fetchNextPage: vi.fn(),
}

// mutation pending 状态。
const mutationState = {
  installPending: ref(false),
  uninstallPending: ref(false),
  updatePending: ref(false),
  reinstallPending: ref(false),
}

// ======================== IntersectionObserver mock ========================
// jsdom 无 IntersectionObserver，组件的滚动加载哨兵建立观察时会报错，需 mock。
// lastIntersectionCallback 捕获最近一次构造时传入的回调，测试可手动触发模拟「哨兵进入视口」。
let lastIntersectionCallback: ((entries: { isIntersecting: boolean }[]) => void) | null = null
const ioObserve = vi.fn()
const ioDisconnect = vi.fn()
class MockIntersectionObserver {
  constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
    lastIntersectionCallback = cb
  }
  observe = ioObserve
  disconnect = ioDisconnect
  unobserve = vi.fn()
  takeRecords = vi.fn(() => [])
}
vi.stubGlobal('IntersectionObserver', MockIntersectionObserver)

// ======================== vi.mock ========================
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'user-1', role: 'org_member', org_id: 'org-1' } }),
}))

vi.mock('@/domain/permissions', async () => {
  const actual = await vi.importActual<typeof import('@/domain/permissions')>('@/domain/permissions')
  return { ...actual, canManageAppSkill: mocks.canManage }
})

vi.mock('@/api/hooks/useSkills', () => ({
  useAppSkillsQuery: () => appSkillsState,
  // 把扁平 marketState.data（{ entries }）包装成 useInfiniteQuery 的 { pages } 形状，
  // 这样各用例仍可直接给 marketState.data.value 赋 { entries }，无需改动。
  useSkillMarketQuery: () => ({
    data: computed(() => ({ pages: [{ entries: marketState.data.value.entries ?? [] }] })),
    isLoading: marketState.isLoading,
    error: marketState.error,
    hasNextPage: marketState.hasNextPage,
    isFetchingNextPage: marketState.isFetchingNextPage,
    fetchNextPage: marketState.fetchNextPage,
  }),
  useInstallAppSkill: () => ({
    mutateAsync: mocks.installMutateAsync,
    isPending: mutationState.installPending,
  }),
  useUninstallAppSkill: () => ({
    mutateAsync: mocks.uninstallMutateAsync,
    isPending: mutationState.uninstallPending,
  }),
  useUpdateAppSkill: () => ({
    mutateAsync: mocks.updateMutateAsync,
    isPending: mutationState.updatePending,
  }),
  useReinstallAppSkill: () => ({
    mutateAsync: mocks.reinstallMutateAsync,
    isPending: mutationState.reinstallPending,
  }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  const vue = await import('vue')
  const { defineComponent: dc, h: _h } = vue

  // Col 是列定义的最小接口，用于 InlineDataTableStub 内部类型。
  interface Col { key: string; title?: string; render?: (row: unknown) => unknown }

  // DataTableStub 内联在 vi.mock factory 中，避免 hoisting 导致 ReferenceError。
  const InlineDataTableStub = dc({
    props: {
      columns: { type: Array, default: () => [] },
      data: { type: Array, default: () => [] },
    },
    setup(props: { columns: Col[]; data: unknown[] }) {
      return () =>
        _h('div', [
          _h(
            'div',
            { class: 'headers' },
            props.columns.map((col) => _h('span', { class: `header-${col.key}` }, col.title)),
          ),
          ...props.data.flatMap((row: unknown) =>
            props.columns.map((col: Col) =>
              _h('div', { class: `cell-${col.key}` }, col.render ? [col.render(row) as import('vue').VNodeChild] : []),
            ),
          ),
        ])
    },
  })

  return {
    ...actual,
    useMessage: () => ({
      success: mocks.messageSuccess,
      error: mocks.messageError,
    }),
    useDialog: () => ({
      warning: mocks.dialogWarning,
    }),
    // NDataTable 替换为自定义 stub，避免 NaiveUI 实际组件依赖 n-config-provider 报 warning。
    NDataTable: InlineDataTableStub,
    // NTabs/NTabPane stub 直接渲染 slot，确保内容对测试可见。
    NTabs: { template: '<div class="n-tabs"><slot /></div>' },
    NTabPane: { template: '<div class="n-tab-pane"><slot /></div>' },
    // NCard stub 渲染 slot 内容即可。
    NCard: { template: '<div class="n-card"><slot /></div>' },
    // NButton stub 渲染为 button。
    NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
    // NTag stub 渲染为 span。
    NTag: { template: '<span class="n-tag" v-bind="$attrs"><slot /></span>' },
    // NInput stub：接受所有 props 避免 size/placeholder 报 DOMException warning。
    NInput: { template: '<div class="n-input"><input /></div>' },
  }
})

// ======================== 挂载辅助 ========================
// 标准 app provide：org_id / owner_user_id 用于 canManageApp 权限计算。
const defaultApp = {
  id: 'app-1',
  org_id: 'org-1',
  owner_user_id: 'user-1',
  name: '测试实例',
  status: 'running',
  api_key_status: 'active',
  knowledge_quota_bytes: 0,
}

function mountManager() {
  return mount(SkillManager, {
    props: { appId: 'app-1' },
    global: {
      provide: { app: ref(defaultApp) },
    },
  })
}

// ======================== 测试套件 ========================
describe('SkillManager', () => {
  beforeEach(() => {
    // 重置每个 mock 和状态为默认值。
    appSkillsState.data.value = []
    appSkillsState.isLoading.value = false
    appSkillsState.error.value = null
    marketState.data.value = { entries: [] }
    marketState.isLoading.value = false
    marketState.error.value = null
    marketState.hasNextPage.value = false
    marketState.isFetchingNextPage.value = false
    marketState.fetchNextPage.mockReset()
    lastIntersectionCallback = null
    ioObserve.mockReset()
    ioDisconnect.mockReset()
    mutationState.installPending.value = false
    mutationState.uninstallPending.value = false
    mutationState.updatePending.value = false
    mocks.installMutateAsync.mockReset()
    mocks.uninstallMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.messageSuccess.mockReset()
    mocks.messageError.mockReset()
    mocks.dialogWarning.mockReset()
    mocks.canManage.mockReturnValue(true)
  })

  // ======== 已安装：status 徽章渲染 ========

  it('已安装列表渲染 active 状态徽章为「已生效」', () => {
    // 覆盖 active 状态 skill 的正常运行场景。
    appSkillsState.data.value = [
      { name: 'skill-active', status: 'active', source: 'platform', version: '1.0.0' },
    ]
    const wrapper = mountManager()
    // status 列单元格应包含「已生效」文案。
    expect(wrapper.find('.cell-status').text()).toContain('已生效')
  })

  it('已安装列表渲染 pending 状态徽章为「待生效·重新安装」', () => {
    // 覆盖 pending 状态 skill（安装后未重启生效）的提示文案。
    appSkillsState.data.value = [
      { name: 'skill-pending', status: 'pending', source: 'platform', version: '1.0.0' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.cell-status').text()).toContain('待生效·重新安装')
  })

  it('已安装列表渲染 builtin 状态徽章为「内置」', () => {
    // 覆盖镜像内置 skill（只读展示，不可卸载）的状态文案。
    appSkillsState.data.value = [
      { name: 'skill-builtin', status: 'builtin', source: undefined, version: '1.0.0' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.cell-status').text()).toContain('内置')
  })

  it('已安装列表渲染 self_created 状态徽章为「自创」', () => {
    // 覆盖用户在助手中自定义创建的 skill 的状态文案。
    appSkillsState.data.value = [
      { name: 'skill-self', status: 'self_created', source: undefined, version: '1.0.0' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.cell-status').text()).toContain('自创')
  })

  // ======== 已安装：protected 隐藏卸载 ========

  it('protected skill 不显示卸载按钮，显示锁标记', () => {
    // 覆盖当前版本必需 skill：protected=true 时隐藏卸载入口，避免用户误操作。
    appSkillsState.data.value = [
      { name: 'skill-protected', status: 'active', source: 'platform', version: '1.0.0', protected: true },
    ]
    const wrapper = mountManager()
    const actionsCell = wrapper.find('.cell-actions')
    // 卸载按钮文案不可见。
    expect(actionsCell.text()).not.toContain('卸载')
    // 锁标记应渲染。
    expect(actionsCell.find('.protected-lock').exists()).toBe(true)
  })

  it('非 protected skill 有写权限时显示卸载按钮', () => {
    // 覆盖普通 active skill 有管理权限时卸载入口可见。
    appSkillsState.data.value = [
      { name: 'skill-normal', status: 'active', source: 'platform', version: '1.0.0', protected: false },
    ]
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    expect(wrapper.find('.cell-actions').text()).toContain('卸载')
  })

  it('pending skill 显示「重新安装」按钮并可点击触发重装', async () => {
    // 覆盖 pending 状态：首次热装/reload 未成功时给「重新安装」重试入口，点击调 reinstall mutation。
    appSkillsState.data.value = [
      { name: 'skill-pending', status: 'pending', source: 'platform', version: '1.0.0', protected: false },
    ]
    mocks.canManage.mockReturnValue(true)
    mocks.reinstallMutateAsync.mockResolvedValue({ name: 'skill-pending', status: 'active' })
    const wrapper = mountManager()
    const actionsCell = wrapper.find('.cell-actions')
    // pending 同时给出「重新安装」与「卸载」两个入口。
    expect(actionsCell.text()).toContain('重新安装')
    expect(actionsCell.text()).toContain('卸载')
    // 点击「重新安装」触发 reinstall mutation，参数为 skill 名。
    const reinstallBtn = actionsCell.findAll('button').find((b) => b.text().includes('重新安装'))
    await reinstallBtn?.trigger('click')
    expect(mocks.reinstallMutateAsync).toHaveBeenCalledWith('skill-pending')
  })

  it('builtin skill 操作列显示「内置只读」而非卸载按钮', () => {
    // 覆盖镜像内置 skill 只读展示，即使有权限也不允许卸载。
    appSkillsState.data.value = [
      { name: 'skill-builtin2', status: 'builtin', source: undefined, version: '1.0.0' },
    ]
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    const actionsCell = wrapper.find('.cell-actions')
    expect(actionsCell.text()).toContain('内置只读')
    expect(actionsCell.text()).not.toContain('卸载')
  })

  // ======== 已安装：无权限隐藏卸载按钮 ========

  it('无写权限时已安装列表不显示卸载按钮', () => {
    // 覆盖只读用户（如其他组织成员）无操作入口的场景。
    appSkillsState.data.value = [
      { name: 'skill-readonly', status: 'active', source: 'platform', version: '1.0.0', protected: false },
    ]
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountManager()
    expect(wrapper.find('.cell-actions').text()).not.toContain('卸载')
  })

  it('platform_admin 有写权限时显示卸载和安装按钮', () => {
    // 覆盖 platform_admin 可管理任意实例 skill 的场景：
    // canManageAppSkill 对 platform_admin 返回 true，操作按钮应可见。
    appSkillsState.data.value = [
      { name: 'skill-platform-admin', status: 'active', source: 'platform', version: '1.0.0', protected: false },
    ]
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'new-skill', name: 'new-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    // 模拟 canManageAppSkill 对 platform_admin 返回 true。
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    // 已安装列表应显示卸载按钮。
    expect(wrapper.find('.cell-actions').text()).toContain('卸载')
    // 市场安装按钮应显示。
    expect(wrapper.find('.n-card').text()).toContain('安装')
  })

  // ======== 已安装：更新按钮 ========

  it('latest_version 大于 version 时显示更新按钮', () => {
    // 覆盖有新版本可用时「更新」入口展示。
    appSkillsState.data.value = [
      { name: 'skill-upgradable', status: 'active', source: 'platform', version: '1.0.0', latest_version: '1.1.0' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.cell-update').text()).toContain('更新至 1.1.0')
  })

  it('latest_version 与 version 相同时不显示更新按钮', () => {
    // 覆盖版本已是最新、不应展示更新按钮的场景。
    appSkillsState.data.value = [
      { name: 'skill-latest', status: 'active', source: 'platform', version: '1.0.0', latest_version: '1.0.0' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.cell-update').text()).toBe('—')
  })

  // ======== 市场：安装按钮 ========

  it('技能市场展示条目并显示安装按钮', () => {
    // 覆盖市场条目正常加载、有权限时可点击安装的场景。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'my-skill', name: 'my-skill', version: '2.0.0', downloads: 42 },
      ],
    }
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    // 市场卡片区域应渲染安装按钮。
    const card = wrapper.find('.n-card')
    expect(card.exists()).toBe(true)
    expect(card.text()).toContain('my-skill')
    expect(card.text()).toContain('安装')
  })

  it('市场中已安装的 skill 显示「已安装」标记而非安装按钮', () => {
    // 覆盖市场展示与已安装列表交叉对比去重：同名 skill 禁止重复安装。
    appSkillsState.data.value = [
      { name: 'existing-skill', status: 'active', source: 'platform', version: '1.0.0' },
    ]
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'existing-skill', name: 'existing-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    const card = wrapper.find('.n-card')
    // 已安装标记（n-tag 渲染的 span）应存在。
    expect(card.text()).toContain('已安装')
    // 安装按钮（button 元素）不应存在——注意与「已安装」文案区分，
    // 这里检查 button 元素而非文本，避免「已安装」文案中含「安装」子串的误判。
    expect(card.find('button').exists()).toBe(false)
  })

  it('无写权限时市场不显示安装按钮', () => {
    // 覆盖只读角色浏览市场时没有安装入口的场景。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'remote-skill', name: 'remote-skill', version: '3.0.0', downloads: 100 },
      ],
    }
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountManager()
    expect(wrapper.find('.n-card').text()).not.toContain('安装')
  })

  // ======== 市场：分页滚动加载 ========

  it('clawhub 有下一页时哨兵进入视口自动拉取下一页', async () => {
    // 覆盖滚动加载：hasNextPage=true 时底部哨兵被 observe，进入视口触发 fetchNextPage。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c1', name: 'c1', version: '1.0.0', downloads: 5 },
      ],
    }
    marketState.hasNextPage.value = true
    mountManager()
    await flushPromises()
    await nextTick()
    // 哨兵元素应已被 IntersectionObserver 观察。
    expect(ioObserve).toHaveBeenCalled()
    // 模拟哨兵进入视口 → 自动拉取下一页。
    lastIntersectionCallback?.([{ isIntersecting: true }])
    expect(marketState.fetchNextPage).toHaveBeenCalledTimes(1)
  })

  it('正在拉取下一页时哨兵再次进入视口不重复触发', async () => {
    // 防抖：isFetchingNextPage=true 时再次相交不应重复调用 fetchNextPage。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c1', name: 'c1', version: '1.0.0', downloads: 5 },
      ],
    }
    marketState.hasNextPage.value = true
    marketState.isFetchingNextPage.value = true
    mountManager()
    await flushPromises()
    await nextTick()
    lastIntersectionCallback?.([{ isIntersecting: true }])
    expect(marketState.fetchNextPage).not.toHaveBeenCalled()
  })

  it('没有下一页时不渲染哨兵、不建立观察', async () => {
    // 边界：hasNextPage=false（如 platform 来源无游标）时哨兵不渲染，IntersectionObserver 不 observe。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'p1', name: 'p1', version: '1.0.0', downloads: 0 },
      ],
    }
    marketState.hasNextPage.value = false
    mountManager()
    await flushPromises()
    await nextTick()
    expect(ioObserve).not.toHaveBeenCalled()
  })

  // ======== 市场：来源徽章 ========

  it('平台库条目来源徽章显示「平台库」', () => {
    // 覆盖 source=platform 时来源徽章文案正确。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'p-skill', name: 'p-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountManager()
    expect(wrapper.find('.n-card').text()).toContain('平台库')
  })

  it('ClawHub 条目来源徽章显示「ClawHub」', () => {
    // 覆盖 source=clawhub 时来源徽章文案正确。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c-skill', name: 'c-skill', version: '2.0.0', downloads: 10 },
      ],
    }
    const wrapper = mountManager()
    expect(wrapper.find('.n-card').text()).toContain('ClawHub')
  })

  // ======== 已安装：卸载点击触发 dialog ========

  it('点击卸载按钮触发确认对话框', async () => {
    // 覆盖卸载操作走二次确认弹窗，避免误操作。
    appSkillsState.data.value = [
      { name: 'skill-to-remove', status: 'active', source: 'platform', version: '1.0.0', protected: false },
    ]
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    const uninstallBtn = wrapper.findAll('button').find((b) => b.text() === '卸载')
    expect(uninstallBtn).toBeDefined()
    await uninstallBtn!.trigger('click')
    // useDialog.warning 应被调用。
    expect(mocks.dialogWarning).toHaveBeenCalledTimes(1)
  })

  // ======== 已安装：已安装列表首列标题 ========

  it('已安装列表首列标题为「名称」', () => {
    // 覆盖表格列头文案正确，便于用户识别各列含义。
    appSkillsState.data.value = [
      { name: 'skill-x', status: 'active', source: 'platform', version: '1.0.0' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.header-name').text()).toBe('名称')
  })
})
