// SkillTicketPanel.spec.ts — 成员定制技能工单面板单元测试。
// 覆盖：四类状态徽章渲染、报价格式化、delivered 行「去安装」并 emit goInstall、
// 「+ 提交需求」按钮打开提交弹窗、rejected 抽屉显示拒绝原因与重新提交提示。
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import SkillTicketPanel from './SkillTicketPanel.vue'

// ======================== 可变 reactive 状态 ========================
// ticketsState 控制 useMySkillTicketsQuery 返回的工单列表。
const ticketsState = {
  data: ref<Record<string, unknown>[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// detailState 控制 useSkillTicketDetailQuery 返回的工单详情（含 comments）。
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

// authState 控制 useAuthStore：user.id 用于对话气泡本人/对方左右区分。
const authState = {
  user: { id: 'me-1', role: 'org_member' } as { id: string; role: string },
}

// mocks 在 vi.mock 提升前创建，承载各 mutation 与 message 桩，供断言。
const mocks = vi.hoisted(() => ({
  submitMutateAsync: vi.fn(),
  commentMutateAsync: vi.fn(),
  uploadMutateAsync: vi.fn(),
  downloadAttachment: vi.fn(),
  messageSuccess: vi.fn(),
  messageError: vi.fn(),
}))

// ======================== vi.mock ========================
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  // 工单列表 / 详情 / 附件 query 由可变 state 控制。
  useMySkillTicketsQuery: () => ticketsState,
  useSkillTicketDetailQuery: () => detailState,
  useSkillTicketAttachmentsQuery: () => attachmentsState,
  // 提交 / 评论 / 上传 mutation 桩，断言调用参数。
  useSubmitSkillTicket: () => ({ mutateAsync: mocks.submitMutateAsync, isPending: ref(false) }),
  useAddSkillTicketComment: () => ({ mutateAsync: mocks.commentMutateAsync, isPending: ref(false) }),
  useUploadSkillTicketAttachment: () => ({ mutateAsync: mocks.uploadMutateAsync, isPending: ref(false) }),
  // 附件下载桩。
  downloadSkillTicketAttachment: mocks.downloadAttachment,
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  const vue = await import('vue')
  const { defineComponent: dc, h: _h } = vue

  // Col 是列定义的最小接口，用于 InlineDataTableStub 内部类型。
  interface Col { key: string; title?: string; render?: (row: unknown) => unknown }

  // InlineDataTableStub 内联在 factory 中渲染表头 + 每行每列单元格，便于按 cell-<key> 断言。
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
    useMessage: () => ({ success: mocks.messageSuccess, error: mocks.messageError }),
    NDataTable: InlineDataTableStub,
    // NButton stub 渲染为 button，透传 $attrs（含 disabled）。
    NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
    // NTag stub 渲染为 span。
    NTag: { template: '<span class="n-tag" v-bind="$attrs"><slot /></span>' },
    // NModal stub：show=true 时渲染内容与 footer 插槽，供提交弹窗断言。
    NModal: {
      props: ['show', 'title'],
      template: '<div v-if="show" class="n-modal"><slot /><slot name="footer" /></div>',
    },
    // NDrawer/NDrawerContent stub：show=true 时渲染内容，供详情抽屉断言。
    NDrawer: { props: ['show'], template: '<div v-if="show" class="n-drawer"><slot /></div>' },
    NDrawerContent: { props: ['title'], template: '<div class="n-drawer-content">{{ title }}<slot /></div>' },
    // NForm/NFormItem stub 渲染 slot 即可。
    NForm: { template: '<div class="n-form"><slot /></div>' },
    NFormItem: { props: ['label'], template: '<div class="n-form-item">{{ label }}<slot /></div>' },
    // NInput stub 渲染 input。
    NInput: { template: '<div class="n-input"><input /></div>' },
  }
})

// ======================== 挂载辅助 ========================
function mountPanel() {
  return mount(SkillTicketPanel)
}

// ======================== 测试套件 ========================
describe('SkillTicketPanel', () => {
  beforeEach(() => {
    // 每个用例前重置列表/详情/附件状态与各桩。
    ticketsState.data.value = []
    ticketsState.isLoading.value = false
    ticketsState.error.value = null
    detailState.data.value = null
    detailState.isLoading.value = false
    detailState.error.value = null
    attachmentsState.data.value = []
    attachmentsState.isLoading.value = false
    attachmentsState.error.value = null
    authState.user = { id: 'me-1', role: 'org_member' }
    mocks.submitMutateAsync.mockReset()
    mocks.commentMutateAsync.mockReset()
    mocks.uploadMutateAsync.mockReset()
    mocks.downloadAttachment.mockReset()
    mocks.messageSuccess.mockReset()
    mocks.messageError.mockReset()
  })

  // ======== 状态徽章 ========

  it('工单列表渲染四类状态徽章中文文案', () => {
    // 覆盖：pending→待处理 / processing→制作中 / delivered→已交付 / rejected→已拒绝。
    ticketsState.data.value = [
      { id: 't1', title: '需求1', status: 'pending' }, // pending → 待处理
      { id: 't2', title: '需求2', status: 'processing' }, // processing → 制作中
      { id: 't3', title: '需求3', status: 'delivered', custom_skill_name: 'sk' }, // delivered → 已交付
      { id: 't4', title: '需求4', status: 'rejected' }, // rejected → 已拒绝
    ]
    const wrapper = mountPanel()
    const statusText = wrapper.findAll('.cell-status').map((c) => c.text()).join(' ')
    expect(statusText).toContain('待处理')
    expect(statusText).toContain('制作中')
    expect(statusText).toContain('已交付')
    expect(statusText).toContain('已拒绝')
  })

  // ======== 报价格式化 ========

  it('报价列：有金额格式化为「¥x.xx」，无金额显示「—」', () => {
    // 覆盖：12345 分 → ¥123.45；null → —。
    ticketsState.data.value = [
      { id: 't1', title: '需求1', status: 'processing', quote_amount_cents: 12345 }, // 有报价
      { id: 't2', title: '需求2', status: 'pending', quote_amount_cents: null }, // 未报价
    ]
    const wrapper = mountPanel()
    const quoteText = wrapper.findAll('.cell-quote').map((c) => c.text()).join(' ')
    expect(quoteText).toContain('¥123.45')
    expect(quoteText).toContain('—')
  })

  // ======== delivered 行「去安装」并 emit ========

  it('delivered 行有「去安装」按钮，点击 emit goInstall 携带 custom_skill_name', async () => {
    // 覆盖：仅 delivered 工单出现「去安装」，点击上抛技能名给父组件跳市场定制筛选。
    ticketsState.data.value = [
      { id: 't1', title: '需求1', status: 'delivered', custom_skill_name: 'my-skill' },
    ]
    const wrapper = mountPanel()
    const installBtn = wrapper.findAll('button').find((b) => b.text().includes('去安装'))
    expect(installBtn).toBeTruthy()
    await installBtn!.trigger('click')
    // emit goInstall 携带 custom_skill_name。
    expect(wrapper.emitted('goInstall')?.[0]).toEqual(['my-skill'])
  })

  it('非 delivered 行不出现「去安装」按钮', () => {
    // 边界：pending/processing/rejected 工单只有「查看」，无「去安装」。
    ticketsState.data.value = [{ id: 't1', title: '需求1', status: 'processing' }]
    const wrapper = mountPanel()
    const installBtn = wrapper.findAll('button').find((b) => b.text().includes('去安装'))
    expect(installBtn).toBeUndefined()
  })

  // ======== 提交需求弹窗 ========

  it('点击「+ 提交需求」打开提交弹窗', async () => {
    // 覆盖：默认弹窗关闭（不渲染），点击工具栏按钮后 NModal show=true 渲染表单。
    const wrapper = mountPanel()
    // 初始无弹窗。
    expect(wrapper.find('.n-modal').exists()).toBe(false)
    const submitBtn = wrapper.findAll('button').find((b) => b.text().includes('提交需求'))
    expect(submitBtn).toBeTruthy()
    await submitBtn!.trigger('click')
    await nextTick()
    // 弹窗出现并含标题文案。
    const modal = wrapper.find('.n-modal')
    expect(modal.exists()).toBe(true)
    expect(modal.text()).toContain('标题')
    expect(modal.text()).toContain('描述')
  })

  // ======== rejected 抽屉显示拒绝原因 ========

  it('rejected 工单抽屉显示拒绝原因与「补充说明后将重新提交」提示', async () => {
    // 覆盖：打开 rejected 工单详情，抽屉头部显示 reject_reason 与重新提交提示文案。
    ticketsState.data.value = [{ id: 't1', title: '被拒需求', status: 'rejected' }]
    detailState.data.value = {
      id: 't1',
      title: '被拒需求',
      status: 'rejected',
      reject_reason: '描述不够清晰',
      comments: [],
    }
    const wrapper = mountPanel()
    // 点「查看」打开抽屉。
    const viewBtn = wrapper.findAll('button').find((b) => b.text().trim() === '查看')
    expect(viewBtn).toBeTruthy()
    await viewBtn!.trigger('click')
    await nextTick()
    const drawer = wrapper.find('.n-drawer')
    expect(drawer.exists()).toBe(true)
    // 拒绝原因与重新提交提示均展示。
    expect(drawer.text()).toContain('描述不够清晰')
    expect(drawer.text()).toContain('补充说明后将重新提交')
  })

  // ======== 对话流本人/对方区分 ========

  it('对话流按 author_user_id 区分本人（mine）与对方（theirs）气泡', async () => {
    // 覆盖：author_user_id === 当前用户 id 的评论标记 mine（靠右），否则 theirs（靠左）。
    ticketsState.data.value = [{ id: 't1', title: '需求1', status: 'processing' }]
    detailState.data.value = {
      id: 't1',
      title: '需求1',
      status: 'processing',
      comments: [
        { id: 'c1', body: '我的留言', author_user_id: 'me-1' }, // 本人 → mine
        { id: 'c2', body: '管理员回复', author_user_id: 'admin-9' }, // 他人 → theirs
      ],
    }
    const wrapper = mountPanel()
    const viewBtn = wrapper.findAll('button').find((b) => b.text().trim() === '查看')
    await viewBtn!.trigger('click')
    await nextTick()
    // 本人气泡 mine，对方气泡 theirs。
    expect(wrapper.find('.ticket-comment.mine').text()).toContain('我的留言')
    expect(wrapper.find('.ticket-comment.theirs').text()).toContain('管理员回复')
  })

  // ======== delivered 抽屉「去安装」 ========

  it('delivered 工单抽屉显「去安装」并 emit goInstall', async () => {
    // 覆盖：delivered 详情抽屉底部「去安装」点击 emit goInstall 携带 custom_skill_name。
    ticketsState.data.value = [{ id: 't1', title: '需求1', status: 'delivered', custom_skill_name: 'sk-x' }]
    detailState.data.value = {
      id: 't1',
      title: '需求1',
      status: 'delivered',
      custom_skill_name: 'sk-x',
      comments: [],
    }
    const wrapper = mountPanel()
    // 列表里 delivered 行有「去安装」，先打开抽屉再断言抽屉内的「去安装」。
    const viewBtn = wrapper.findAll('button').find((b) => b.text().trim() === '查看')
    await viewBtn!.trigger('click')
    await nextTick()
    const drawer = wrapper.find('.n-drawer')
    const installBtn = drawer.findAll('button').find((b) => b.text().includes('去安装'))
    expect(installBtn).toBeTruthy()
    await installBtn!.trigger('click')
    expect(wrapper.emitted('goInstall')?.at(-1)).toEqual(['sk-x'])
  })
})
