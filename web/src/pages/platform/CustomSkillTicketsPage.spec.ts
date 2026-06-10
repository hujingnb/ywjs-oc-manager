// CustomSkillTicketsPage.spec.ts — 平台后台定制技能工单页单元测试。
// 覆盖：队列渲染（标题/提交者/状态/报价）、状态筛选、抽屉操作按钮存在、
// 交付弹窗默认目标范围推导（member→all_org、admin→org_admins）、交付提交构造的 targets/file 正确。
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import CustomSkillTicketsPage from './CustomSkillTicketsPage.vue'

// ======================== 可变 reactive 状态 ========================
// ticketsState 控制 useAdminSkillTicketsQuery 返回的工单队列。
const ticketsState = {
  data: ref<Record<string, unknown>[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// detailState 控制 useSkillTicketDetailQuery 返回的工单详情（含 comments / requester_role / org_id）。
const detailState = {
  data: ref<Record<string, unknown> | null>(null),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// attachmentsState 控制 useSkillTicketAttachmentsQuery 返回的附件列表。
const attachmentsState = {
  data: ref<Record<string, unknown>[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// orgsState 控制 useOrganizationsQuery 返回的组织列表（用于「加组织」下拉与目标名称展示）。
const orgsState = {
  data: ref<Record<string, unknown>[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// authState 控制 useAuthStore：user.id 用于对话气泡本人/对方区分。
const authState = {
  user: { id: 'admin-1', role: 'platform_admin' } as { id: string; role: string },
}

// mocks 在 vi.mock 提升前创建，承载各 mutation 与 message 桩，供断言。
const mocks = vi.hoisted(() => ({
  statusMutateAsync: vi.fn(),
  quoteMutateAsync: vi.fn(),
  rejectMutateAsync: vi.fn(),
  commentMutateAsync: vi.fn(),
  deliverMutateAsync: vi.fn(),
  downloadAttachment: vi.fn(),
  messageSuccess: vi.fn(),
  messageError: vi.fn(),
}))

// ======================== vi.mock ========================
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  // 队列 / 详情 / 附件 query 由可变 state 控制。
  useAdminSkillTicketsQuery: () => ticketsState,
  useSkillTicketDetailQuery: () => detailState,
  useSkillTicketAttachmentsQuery: () => attachmentsState,
  // 各管理员 mutation 桩，断言调用参数。
  useUpdateSkillTicketStatus: () => ({ mutateAsync: mocks.statusMutateAsync, isPending: ref(false) }),
  useSetSkillTicketQuote: () => ({ mutateAsync: mocks.quoteMutateAsync, isPending: ref(false) }),
  useRejectSkillTicket: () => ({ mutateAsync: mocks.rejectMutateAsync, isPending: ref(false) }),
  useAddSkillTicketComment: () => ({ mutateAsync: mocks.commentMutateAsync, isPending: ref(false) }),
  useDeliverCustomSkill: () => ({ mutateAsync: mocks.deliverMutateAsync, isPending: ref(false) }),
  // 附件下载桩。
  downloadSkillTicketAttachment: mocks.downloadAttachment,
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => orgsState,
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  const vue = await import('vue')
  const { defineComponent: dc, h: _h } = vue

  // Col 是列定义的最小接口，用于 InlineDataTableStub 内部类型。
  interface Col { key: string; title?: string; render?: (row: unknown) => unknown }

  // InlineDataTableStub 内联渲染表头 + 每行每列单元格，便于按 cell-<key> 断言。
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

  // NSelectStub：渲染当前 value，并暴露 set-<value> 按钮以模拟选择某项（供状态筛选/受众/加组织断言）。
  const NSelectStub = dc({
    props: {
      value: { type: [String, Number, null] as unknown as () => string | number | null, default: null },
      options: { type: Array, default: () => [] },
      size: { type: String, default: '' },
      clearable: { type: Boolean, default: false },
      placeholder: { type: String, default: '' },
    },
    emits: ['update:value'],
    setup(
      props: { value: string | number | null; options: { label?: string; value: string | number }[] },
      { emit }: { emit: (e: 'update:value', v: string | number) => void },
    ) {
      return () =>
        _h('div', { class: 'n-select' }, [
          _h('span', { class: 'select-value' }, String(props.value)),
          ...props.options.map((o) =>
            _h(
              'button',
              { class: `select-set-${o.value}`, onClick: () => emit('update:value', o.value) },
              o.label ?? String(o.value),
            ),
          ),
        ])
    },
  })

  return {
    ...actual,
    useMessage: () => ({ success: mocks.messageSuccess, error: mocks.messageError }),
    NDataTable: InlineDataTableStub,
    NSelect: NSelectStub,
    // NButton stub 渲染为 button，透传 $attrs（含 disabled）。
    NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
    NCard: { template: '<div class="n-card"><slot name="header" /><slot /></div>' },
    NTag: { template: '<span class="n-tag" v-bind="$attrs"><slot /></span>' },
    // NModal stub：show=true 时渲染内容与 footer 插槽。
    NModal: {
      props: ['show', 'title'],
      template: '<div v-if="show" class="n-modal"><slot /><slot name="footer" /></div>',
    },
    // NDrawer/NDrawerContent stub：show=true 时渲染内容。
    NDrawer: { props: ['show'], template: '<div v-if="show" class="n-drawer"><slot /></div>' },
    NDrawerContent: { props: ['title'], template: '<div class="n-drawer-content">{{ title }}<slot /></div>' },
    NForm: { template: '<div class="n-form"><slot /></div>' },
    NFormItem: { props: ['label'], template: '<div class="n-form-item">{{ label }}<slot /></div>' },
    // NInput stub：渲染为 textarea 以保留多行内容（SKILL.md frontmatter 依赖换行），
    // v-model:value 经原生 input 事件双向绑定；声明 size 等 prop 以免透传到原生元素触发告警。
    NInput: {
      props: ['value', 'size', 'type', 'rows', 'clearable', 'placeholder', 'autosize'],
      emits: ['update:value'],
      template: '<textarea class="n-input" :value="value" @input="$emit(\'update:value\', $event.target.value)"></textarea>',
    },
    // NInputNumber stub：以 number 解析输入值；声明 size 等 prop 以免透传告警。
    NInputNumber: {
      props: ['value', 'size', 'min', 'precision', 'placeholder'],
      emits: ['update:value'],
      template: '<input class="n-input-number" :value="value" @input="$emit(\'update:value\', Number($event.target.value))" />',
    },
    NRadioGroup: { template: '<div class="n-radio-group"><slot /></div>' },
    NRadioButton: { props: ['value'], template: '<span class="n-radio-button"><slot /></span>' },
  }
})

// ======================== 挂载辅助 ========================
function mountPage() {
  return mount(CustomSkillTicketsPage)
}

// 打开第一行工单的「处理」抽屉（队列每行操作列有一个「处理」按钮）。
async function openFirstDetail(wrapper: ReturnType<typeof mountPage>) {
  const btn = wrapper.findAll('button').find((b) => b.text().trim() === '处理')
  await btn!.trigger('click')
  await nextTick()
}

// ======================== 测试套件 ========================
describe('CustomSkillTicketsPage', () => {
  beforeEach(() => {
    // 每个用例前重置队列/详情/附件/组织状态与各桩。
    ticketsState.data.value = []
    ticketsState.isLoading.value = false
    ticketsState.error.value = null
    detailState.data.value = null
    detailState.isLoading.value = false
    detailState.error.value = null
    attachmentsState.data.value = []
    attachmentsState.isLoading.value = false
    attachmentsState.error.value = null
    orgsState.data.value = []
    authState.user = { id: 'admin-1', role: 'platform_admin' }
    mocks.statusMutateAsync.mockReset()
    mocks.quoteMutateAsync.mockReset()
    mocks.rejectMutateAsync.mockReset()
    mocks.commentMutateAsync.mockReset()
    mocks.deliverMutateAsync.mockReset()
    mocks.downloadAttachment.mockReset()
    mocks.messageSuccess.mockReset()
    mocks.messageError.mockReset()
  })

  // ======== 队列渲染 ========

  it('队列渲染标题、提交者（成员/管理员）、状态徽章、报价', () => {
    // 覆盖：requester_role org_member→成员、org_admin→管理员；报价分→元；状态中文徽章。
    ticketsState.data.value = [
      { id: 't1', title: '需求A', status: 'pending', requester_role: 'org_member', quote_amount_cents: 9900 }, // 成员 + ¥99.00
      { id: 't2', title: '需求B', status: 'delivered', requester_role: 'org_admin', quote_amount_cents: null }, // 管理员 + 未报价
    ]
    const wrapper = mountPage()
    const titles = wrapper.findAll('.cell-title').map((c) => c.text()).join(' ')
    expect(titles).toContain('需求A')
    expect(titles).toContain('需求B')
    const requesters = wrapper.findAll('.cell-requester').map((c) => c.text()).join(' ')
    expect(requesters).toContain('成员')
    expect(requesters).toContain('管理员')
    const statuses = wrapper.findAll('.cell-status').map((c) => c.text()).join(' ')
    expect(statuses).toContain('待处理')
    expect(statuses).toContain('已交付')
    const quotes = wrapper.findAll('.cell-quote').map((c) => c.text()).join(' ')
    expect(quotes).toContain('¥99.00')
    expect(quotes).toContain('—')
  })

  // ======== 状态筛选 ========

  it('状态筛选：选「制作中」后只保留 processing 工单', async () => {
    // 覆盖：顶部状态下拉切到 processing，computed 过滤掉 pending/delivered 行。
    ticketsState.data.value = [
      { id: 't1', title: '待处理需求', status: 'pending', requester_role: 'org_member' }, // 应被过滤
      { id: 't2', title: '制作中需求', status: 'processing', requester_role: 'org_member' }, // 保留
    ]
    const wrapper = mountPage()
    // 第一个 NSelect 是状态筛选；点其 processing 选项。
    const filterSelect = wrapper.find('.n-select')
    await filterSelect.find('.select-set-processing').trigger('click')
    await nextTick()
    const titles = wrapper.findAll('.cell-title').map((c) => c.text()).join(' ')
    expect(titles).toContain('制作中需求')
    expect(titles).not.toContain('待处理需求')
  })

  // ======== 抽屉操作按钮 ========

  it('打开抽屉后存在「拒绝」「交付」「发送」「保存状态」「保存报价」操作按钮', async () => {
    // 覆盖：点「处理」打开宽抽屉，右侧操作区与回复区按钮均渲染。
    ticketsState.data.value = [{ id: 't1', title: '需求A', status: 'pending', requester_role: 'org_member' }]
    detailState.data.value = {
      id: 't1',
      title: '需求A',
      status: 'pending',
      requester_role: 'org_member',
      org_id: 'org-1',
      comments: [],
    }
    const wrapper = mountPage()
    await openFirstDetail(wrapper)
    const drawer = wrapper.find('.n-drawer')
    expect(drawer.exists()).toBe(true)
    const labels = drawer.findAll('button').map((b) => b.text())
    expect(labels).toContain('拒绝')
    expect(labels).toContain('交付')
    expect(labels).toContain('发送')
    expect(labels).toContain('保存状态')
    expect(labels).toContain('保存报价')
  })

  // ======== 交付弹窗默认目标范围推导 ========

  it('交付弹窗：成员工单默认目标受众为 all_org', async () => {
    // 覆盖：requester_role=org_member → defaultTargets audience=all_org，受众下拉选中值为 all_org。
    ticketsState.data.value = [{ id: 't1', title: '需求A', status: 'pending', requester_role: 'org_member' }]
    detailState.data.value = {
      id: 't1',
      title: '需求A',
      status: 'pending',
      requester_role: 'org_member',
      org_id: 'org-1',
      comments: [],
    }
    const wrapper = mountPage()
    await openFirstDetail(wrapper)
    // 点抽屉里的「交付」打开交付弹窗。
    const deliverBtn = wrapper.find('.n-drawer').findAll('button').find((b) => b.text().trim() === '交付')
    await deliverBtn!.trigger('click')
    await nextTick()
    const modal = wrapper.find('.n-modal')
    expect(modal.exists()).toBe(true)
    // 受众下拉显示当前 value=all_org（select-value）。
    const selectValues = modal.findAll('.select-value').map((s) => s.text())
    expect(selectValues).toContain('all_org')
  })

  it('交付弹窗：管理员工单默认目标受众为 org_admins', async () => {
    // 覆盖：requester_role=org_admin → defaultTargets audience=org_admins。
    ticketsState.data.value = [{ id: 't2', title: '需求B', status: 'pending', requester_role: 'org_admin' }]
    detailState.data.value = {
      id: 't2',
      title: '需求B',
      status: 'pending',
      requester_role: 'org_admin',
      org_id: 'org-9',
      comments: [],
    }
    const wrapper = mountPage()
    await openFirstDetail(wrapper)
    const deliverBtn = wrapper.find('.n-drawer').findAll('button').find((b) => b.text().trim() === '交付')
    await deliverBtn!.trigger('click')
    await nextTick()
    const selectValues = wrapper.find('.n-modal').findAll('.select-value').map((s) => s.text())
    expect(selectValues).toContain('org_admins')
  })

  // ======== 交付提交构造 targets/file ========

  it('交付提交：粘贴 Markdown 后构造 ticketId/targets/file 调用 deliver', async () => {
    // 覆盖：填入合法 SKILL.md → 点「确认交付」→ deliver mutation 收到正确 ticketId、targets（org_id+audience）、tar File。
    ticketsState.data.value = [{ id: 't1', title: '需求A', status: 'processing', requester_role: 'org_member' }]
    detailState.data.value = {
      id: 't1',
      title: '需求A',
      status: 'processing',
      requester_role: 'org_member',
      org_id: 'org-1',
      comments: [],
    }
    mocks.deliverMutateAsync.mockResolvedValue({ id: 's1', name: 'demo-skill', version: '1' })
    const wrapper = mountPage()
    await openFirstDetail(wrapper)
    const deliverBtn = wrapper.find('.n-drawer').findAll('button').find((b) => b.text().trim() === '交付')
    await deliverBtn!.trigger('click')
    await nextTick()
    // 在交付弹窗内填入合法 SKILL.md（markdown 模式下第一个 n-input 即 SKILL.md 文本域）。
    const md = '---\nname: demo-skill\ndescription: 一个演示技能\n---\n\n# Demo\n正文'
    const modal = wrapper.find('.n-modal')
    await modal.find('textarea.n-input').setValue(md)
    await nextTick()
    // 点「确认交付」。
    const confirmBtn = modal.findAll('button').find((b) => b.text().trim() === '确认交付')
    expect(confirmBtn).toBeTruthy()
    await confirmBtn!.trigger('click')
    await nextTick()
    // deliver mutation 被调用一次，参数结构正确。
    expect(mocks.deliverMutateAsync).toHaveBeenCalledTimes(1)
    const arg = mocks.deliverMutateAsync.mock.calls[0][0]
    expect(arg.ticketId).toBe('t1')
    expect(arg.targets).toEqual([{ org_id: 'org-1', audience: 'all_org' }])
    expect(arg.file).toBeInstanceOf(File)
    expect(arg.file.name).toBe('demo-skill.tar')
    expect(arg.file.type).toBe('application/x-tar')
  })

  // ======== 改名冲突 409 ========

  it('交付改名冲突（409）提示「迭代必须沿用同一技能名」', async () => {
    // 覆盖：deliver mutation 抛出带 status=409 的错误 → message.error 展示迭代须同名文案。
    ticketsState.data.value = [{ id: 't1', title: '需求A', status: 'processing', requester_role: 'org_member' }]
    detailState.data.value = {
      id: 't1',
      title: '需求A',
      status: 'processing',
      requester_role: 'org_member',
      org_id: 'org-1',
      comments: [],
    }
    mocks.deliverMutateAsync.mockRejectedValue(Object.assign(new Error('conflict'), { status: 409 }))
    const wrapper = mountPage()
    await openFirstDetail(wrapper)
    const deliverBtn = wrapper.find('.n-drawer').findAll('button').find((b) => b.text().trim() === '交付')
    await deliverBtn!.trigger('click')
    await nextTick()
    const modal = wrapper.find('.n-modal')
    await modal.find('textarea.n-input').setValue('---\nname: renamed-skill\n---\n\n# X')
    await nextTick()
    const confirmBtn = modal.findAll('button').find((b) => b.text().trim() === '确认交付')
    await confirmBtn!.trigger('click')
    await nextTick()
    // 等待 mutateAsync rejection 被捕获。
    await new Promise((r) => setTimeout(r, 0))
    expect(mocks.messageError).toHaveBeenCalledWith('迭代必须沿用同一技能名')
  })
})
