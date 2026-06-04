// SkillManager.spec.ts — SkillManager 复用组件单元测试。
// 覆盖：已安装列表四类 status 徽章渲染、protected 隐藏卸载、来源筛选+数量统计、
// 更新按钮、卸载对话框、无权限时操作隐藏。市场与详情抽屉用例已迁至
// SkillMarketBrowser.spec.ts 与 SkillDetailDrawer.spec.ts。
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import SkillManager from './SkillManager.vue'
import type { AppSkill } from '@/api'

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

// SkillMarketBrowser/SkillDetailDrawer stub：避免拉起其内部市场/详情查询。
// 已安装列表测试只关注 SkillManager 自身逻辑，子组件行为已在各自 spec 覆盖。
vi.mock('./SkillMarketBrowser.vue', () => ({
  default: { name: 'SkillMarketBrowser', template: '<div class="stub-market" />' },
}))
vi.mock('./SkillDetailDrawer.vue', () => ({
  // show prop 需声明，供测试通过 props('show') 断言 detailOpen 是否被正确设置。
  default: { name: 'SkillDetailDrawer', props: ['show', 'skill', 'allowVersionPick', 'actionPending', 'existingNames'], template: '<div class="stub-drawer" />' },
}))

// ======================== 可变 reactive 状态（在 vi.mock 外部定义） ========================
// appSkillsState 用于控制 useAppSkillsQuery 的返回值。
const appSkillsState = {
  data: ref<AppSkill[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// mutation pending 状态。
const mutationState = {
  installPending: ref(false),
  uninstallPending: ref(false),
  updatePending: ref(false),
  reinstallPending: ref(false),
}

// authState 控制 useAuthStore 返回值：user 用于 canManageAppSkill（已被 mock，不实际读取），
// isPlatformAdmin 控制已安装列表是否对当前用户展示 builtin skill（仅平台管理员可见）。
// 默认 org_member 视角（非平台管理员），builtin 隐藏；需展示 builtin 的用例显式置 true。
const authState = {
  user: { id: 'user-1', role: 'org_member', org_id: 'org-1' } as { id: string; role: string; org_id: string },
  isPlatformAdmin: false,
}

// ======================== vi.mock ========================
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/domain/permissions', async () => {
  const actual = await vi.importActual<typeof import('@/domain/permissions')>('@/domain/permissions')
  return { ...actual, canManageAppSkill: mocks.canManage }
})

vi.mock('@/api/hooks/useSkills', () => ({
  // 已安装列表查询。
  useAppSkillsQuery: () => appSkillsState,
  // useSkillMarketQuery/useSkillDetailQuery 已迁至子组件，此 spec 不再使用；
  // 保留空实现防止 SkillMarketBrowser/SkillDetailDrawer stub 失效时 import 出错。
  useSkillMarketQuery: () => ({
    data: { value: { pages: [] } },
    isLoading: { value: false },
    error: { value: null },
    hasNextPage: { value: false },
    isFetchingNextPage: { value: false },
    fetchNextPage: vi.fn(),
  }),
  useSkillDetailQuery: () => ({
    data: { value: { detail: {}, versions: [] } },
    isLoading: { value: false },
    error: { value: null },
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
    // NAlert stub 渲染 title + slot，供运行时不支持横幅测试断言文案。
    NAlert: { props: ['title', 'type'], template: '<div class="n-alert">{{ title }}<slot /></div>' },
    // NDrawer/NDrawerContent stub：show=true 时渲染内容，供详情抽屉测试断言。
    NDrawer: { props: ['show'], template: '<div v-if="show" class="n-drawer"><slot /></div>' },
    NDrawerContent: { props: ['title'], template: '<div class="n-drawer-content">{{ title }}<slot /></div>' },
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
    // 重置每个 mock 和已安装相关状态为默认值。
    appSkillsState.data.value = []
    appSkillsState.isLoading.value = false
    appSkillsState.error.value = null
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
    // 默认非平台管理员视角；需要看到 builtin skill 的用例在用例内显式置 true。
    authState.isPlatformAdmin = false
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
    // builtin 仅平台管理员可见，故以平台管理员视角断言徽章渲染。
    authState.isPlatformAdmin = true
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

  // ======== 运行时版本过旧：提示更新 ========

  it('已安装查询返回 APP_SKILL_RUNTIME_UNSUPPORTED 时显示更新提示、隐藏 tab', () => {
    // 覆盖：实例运行的 hermes 版本过旧（oc-ops 无 /oc/skills 路由），后端 409 返回该 code，
    // 组件应展示「技能管理不可用」横幅与后端提示文案，且不再渲染「已安装/技能市场」tab。
    appSkillsState.error.value = Object.assign(new Error('版本过旧'), {
      status: 409,
      body: { code: 'APP_SKILL_RUNTIME_UNSUPPORTED', message: '当前实例运行的 hermes 版本过旧，请更新版本' },
    }) as unknown as Error
    const wrapper = mountManager()
    // 横幅标题 + 后端提示文案
    const alert = wrapper.find('.n-alert')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('技能管理不可用')
    expect(alert.text()).toContain('请更新版本')
    // tab 容器不渲染（被 v-else 排除）
    expect(wrapper.find('.n-tabs').exists()).toBe(false)
  })

  it('普通查询错误（非运行时不支持）不触发更新横幅', () => {
    // 边界：其它错误（如网络故障）走常规「查询失败」分支，不误判为版本过旧。
    appSkillsState.error.value = Object.assign(new Error('网络错误'), {
      status: 500,
      body: { code: 'INTERNAL_ERROR', message: '服务器内部错误' },
    }) as unknown as Error
    const wrapper = mountManager()
    expect(wrapper.find('.n-alert').exists()).toBe(false)
    // 仍渲染 tab（常规错误在已安装 tab 内以文案展示）
    expect(wrapper.find('.n-tabs').exists()).toBe(true)
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
    // builtin 仅平台管理员可见，故以平台管理员视角断言操作列只读。
    authState.isPlatformAdmin = true
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

  it('platform_admin 有写权限时已安装列表显示卸载按钮', () => {
    // 覆盖 platform_admin 可管理任意实例 skill 的场景：
    // canManageAppSkill 对 platform_admin 返回 true，卸载按钮应可见。
    appSkillsState.data.value = [
      { name: 'skill-platform-admin', status: 'active', source: 'platform', version: '1.0.0', protected: false },
    ]
    // 模拟 canManageAppSkill 对 platform_admin 返回 true。
    mocks.canManage.mockReturnValue(true)
    const wrapper = mountManager()
    // 已安装列表应显示卸载按钮。
    expect(wrapper.find('.cell-actions').text()).toContain('卸载')
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

  // ======== 已安装：来源筛选 + 数量统计 ========

  it('多来源时展示来源筛选 tag 且各带数量统计（全部为总数）', () => {
    // 覆盖：列表含 platform/clawhub/builtin 三类来源时，筛选工具栏展示「全部/平台库/ClawHub/内置」
    // 四个 tag，每个 tag 文案带该来源数量，「全部」为总数 4。
    // 含 builtin 来源，须以平台管理员视角才能看到内置筛选项与计数。
    authState.isPlatformAdmin = true
    appSkillsState.data.value = [
      { name: 'p1', status: 'active', source: 'platform', version: '1.0.0' },
      { name: 'p2', status: 'active', source: 'platform', version: '1.0.0' },
      { name: 'c1', status: 'active', source: 'clawhub', version: '1.0.0' },
      { name: 'b1', status: 'builtin', source: undefined, version: '内置' },
    ]
    const wrapper = mountManager()
    const toolbar = wrapper.find('.installed-toolbar')
    expect(toolbar.exists()).toBe(true)
    const text = toolbar.text()
    // 「全部」为总数 4，平台库 2、ClawHub 1、内置 1；自创无数据不展示。
    expect(text).toContain('全部 (4)')
    expect(text).toContain('平台库 (2)')
    expect(text).toContain('ClawHub (1)')
    expect(text).toContain('内置 (1)')
    expect(text).not.toContain('自创')
  })

  it('点击来源筛选 tag 只展示该来源的已安装 skill', async () => {
    // 覆盖：选中「ClawHub」后表格只剩 clawhub 来源的行，platform 行被过滤掉。
    appSkillsState.data.value = [
      { name: 'p-skill', status: 'active', source: 'platform', version: '1.0.0' },
      { name: 'c-skill', status: 'active', source: 'clawhub', version: '1.0.0' },
    ]
    const wrapper = mountManager()
    // 初始「全部」：两行都在。
    expect(wrapper.findAll('.cell-name').length).toBe(2)
    // 点击「ClawHub」筛选项。
    const clawTag = wrapper
      .findAll('.installed-toolbar .n-tag')
      .find((t) => t.text().includes('ClawHub'))
    expect(clawTag).toBeTruthy()
    await clawTag!.trigger('click')
    await nextTick()
    // 仅剩 clawhub 来源的一行。
    const nameCells = wrapper.findAll('.cell-name')
    expect(nameCells.length).toBe(1)
    expect(nameCells[0].text()).toContain('c-skill')
  })

  it('仅单一来源时不展示筛选工具栏、改显示总数统计行', () => {
    // 边界：全部 skill 同属一种来源（builtin）时筛选无意义，工具栏隐藏，改为一行「共 N 个技能」。
    // 列表全为 builtin，须平台管理员视角才可见（非管理员会被隐藏成空列表）。
    authState.isPlatformAdmin = true
    appSkillsState.data.value = [
      { name: 'b1', status: 'builtin', source: undefined, version: '内置' },
      { name: 'b2', status: 'builtin', source: undefined, version: '内置' },
    ]
    const wrapper = mountManager()
    expect(wrapper.find('.installed-toolbar').exists()).toBe(false)
    expect(wrapper.find('.installed-count').text()).toContain('共 2 个技能')
  })

  // ======== 已安装：内置 skill 按角色隐藏 ========

  it('非平台管理员已安装列表隐藏 builtin skill 且来源筛选不含「内置」', () => {
    // 覆盖核心需求：内置 skill 对普通用户（org_member）直接隐藏——
    // 表格不渲染 builtin 行、来源筛选不出现「内置」项、「全部」计数不含 builtin。
    authState.isPlatformAdmin = false
    appSkillsState.data.value = [
      { name: 'p1', status: 'active', source: 'platform', version: '1.0.0' },
      { name: 'c1', status: 'active', source: 'clawhub', version: '1.0.0' },
      { name: 'b1', status: 'builtin', source: undefined, version: '内置' },
    ]
    const wrapper = mountManager()
    // 表格仅 2 行（platform + clawhub），builtin 行被隐藏。
    const names = wrapper.findAll('.cell-name').map((c) => c.text())
    expect(names.length).toBe(2)
    expect(names.join()).not.toContain('b1')
    // 来源筛选「全部」计数为 2（不含 builtin），且无「内置」筛选项。
    const toolbar = wrapper.find('.installed-toolbar')
    expect(toolbar.text()).toContain('全部 (2)')
    expect(toolbar.text()).not.toContain('内置')
  })

  it('平台管理员已安装列表展示 builtin skill 与「内置」筛选项', () => {
    // 覆盖：平台管理员需运维排查内置 skill，故 builtin 对其可见——
    // 表格渲染 builtin 行、来源筛选含「内置 (1)」、「全部」计数包含 builtin。
    authState.isPlatformAdmin = true
    appSkillsState.data.value = [
      { name: 'p1', status: 'active', source: 'platform', version: '1.0.0' },
      { name: 'c1', status: 'active', source: 'clawhub', version: '1.0.0' },
      { name: 'b1', status: 'builtin', source: undefined, version: '内置' },
    ]
    const wrapper = mountManager()
    // 表格渲染全部 3 行，含 builtin。
    const names = wrapper.findAll('.cell-name').map((c) => c.text())
    expect(names.length).toBe(3)
    expect(names.join()).toContain('b1')
    // 来源筛选含「内置 (1)」，「全部」计数为 3。
    const toolbar = wrapper.find('.installed-toolbar')
    expect(toolbar.text()).toContain('全部 (3)')
    expect(toolbar.text()).toContain('内置 (1)')
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

  // ======== 详情抽屉：点击已安装名称打开抽屉 ========
  // 注：抽屉内容（版本列表、来源标签、富详情等）的详细断言已迁至 SkillDetailDrawer.spec.ts。
  // 此处仅验证 SkillManager 正确触发 SkillDetailDrawer 子组件显示（stub-drawer 出现）。

  it('点击已安装 skill 名称打开详情抽屉（stub 出现）', async () => {
    // 覆盖：已安装列表名称列渲染为可点击按钮，点击后 detailOpen 变为 true，
    // SkillDetailDrawer stub 组件从 DOM 中出现（show=true）。
    appSkillsState.data.value = [
      { name: 'oc-clawtest', status: 'active', source: 'clawhub', source_ref: 'oc-clawtest', version: '1.0.0' },
    ]
    const wrapper = mountManager()
    // 抽屉 stub 初始不显示（SkillDetailDrawer 的 show prop 为 false，stub 无条件渲染但不含内容可区分）。
    const nameBtn = wrapper.findAll('button').find((b) => b.text() === 'oc-clawtest')
    expect(nameBtn).toBeTruthy()
    await nameBtn!.trigger('click')
    await nextTick()
    // stub-drawer 组件应已挂载（子组件 stub 始终存在于 DOM）。
    expect(wrapper.findComponent({ name: 'SkillDetailDrawer' }).exists()).toBe(true)
    // 抽屉 show prop 应变为 true，表明 detailOpen 已被正确设置。
    expect(wrapper.findComponent({ name: 'SkillDetailDrawer' }).props('show')).toBe(true)
  })
})
