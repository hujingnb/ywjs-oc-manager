import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import { i18n } from '@/i18n'
import IndustryKnowledgePage from './IndustryKnowledgePage.vue'
import type { IndustryKnowledgeBase } from '@/api/hooks/useIndustryKnowledge'
import type { KnowledgeDocument } from '@/api/hooks/useKnowledge'

const createBase = vi.hoisted(() => vi.fn())
const renameBase = vi.hoisted(() => vi.fn())
const deleteBase = vi.hoisted(() => vi.fn())
const uploadFile = vi.hoisted(() => vi.fn())
const deleteFile = vi.hoisted(() => vi.fn())
const clearFiles = vi.hoisted(() => vi.fn())
const reparseFile = vi.hoisted(() => vi.fn())
const messageWarning = vi.hoisted(() => vi.fn())
const messageSuccess = vi.hoisted(() => vi.fn())
const messageError = vi.hoisted(() => vi.fn())
const writeText = vi.hoisted(() => vi.fn())
const fileQueryCalls = vi.hoisted(() => [] as Array<{ industryId: unknown; options: Record<string, { value: unknown }> }>)
const authUser = vi.hoisted(() => ({ current: { role: 'platform_admin' } as { role: string; org_id?: string } }))

const baseItems = vi.hoisted<IndustryKnowledgeBase[]>(() => [
  { id: 'industry-1', name: '保险', document_count: 1, created_at: '2026-06-05T00:00:00Z', updated_at: '2026-06-05T00:00:00Z' },
])

const fileItems = vi.hoisted<KnowledgeDocument[]>(() => [
  {
    id: 'doc-1',
    name: 'policy.pdf',
    size: 1024,
    parse_status: 'completed',
    progress: 100,
    created_at: '2026-06-05T00:00:00Z',
  },
])

vi.mock('@/api/hooks/useIndustryKnowledge', () => ({
  useIndustryKnowledgeUploadTokenQuery: () => ({
    data: ref({ upload_token: 'secret-token' }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useIndustryKnowledgeBasesQuery: () => ({
    data: ref({ items: baseItems, total: baseItems.length }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useIndustryKnowledgeFilesQuery: (industryId: unknown, options: Record<string, { value: unknown }> = {}) => {
    fileQueryCalls.push({ industryId, options })
    return {
    data: ref({ items: fileItems, total: fileItems.length }),
    isLoading: ref(false),
    error: ref(null),
    }
  },
  useCreateIndustryKnowledgeBase: () => ({ mutateAsync: createBase, isPending: ref(false) }),
  useRenameIndustryKnowledgeBase: () => ({ mutateAsync: renameBase, isPending: ref(false) }),
  useDeleteIndustryKnowledgeBase: () => ({ mutateAsync: deleteBase, isPending: ref(false) }),
  useUploadIndustryKnowledgeFile: () => ({ mutateAsync: uploadFile, isPending: ref(false) }),
  useDeleteIndustryKnowledgeFile: () => ({ mutateAsync: deleteFile, isPending: ref(false) }),
  useClearIndustryKnowledgeFiles: () => ({ mutateAsync: clearFiles, isPending: ref(false) }),
  useReparseIndustryKnowledgeFile: () => ({ mutateAsync: reparseFile, isPending: ref(false) }),
  downloadIndustryKnowledgeFile: vi.fn(),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({
    run: vi.fn(async () => undefined),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: authUser.current }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  const vue = await vi.importActual<typeof import('vue')>('vue')
  const StubModal = vue.defineComponent({
    props: ['show'],
    setup(p, { slots }) {
      return () => p.show ? vue.h('section', { class: 'modal' }, slots.default?.()) : null
    },
  })
  return {
    ...actual,
    NModal: StubModal,
    useMessage: () => ({ warning: messageWarning, success: messageSuccess, error: messageError }),
  }
})

function mountPage() {
  return mount(IndustryKnowledgePage, {
    global: {
      // 注入 i18n 插件，IndustryKnowledgePage 使用 useI18n() 需要。
      plugins: [i18n],
      stubs: {
        NButton: defineComponent({
          props: ['loading', 'disabled'],
          emits: ['click'],
          setup(p, { slots, emit }) {
            return () => h('button', { disabled: p.disabled, onClick: () => emit('click') }, slots.default?.())
          },
        }),
        NCard: defineComponent({
          setup(_, { slots }) {
            return () => h('section', [slots.header?.(), slots['header-extra']?.(), slots.default?.(), slots.footer?.()])
          },
        }),
        NAlert: defineComponent({ setup(_, { slots }) { return () => h('div', { class: 'alert' }, slots.default?.()) } }),
        NInput: defineComponent({
          props: ['value', 'placeholder'],
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('input', {
              value: p.value,
              placeholder: p.placeholder,
              onInput: (e: Event) => emit('update:value', (e.target as HTMLInputElement).value),
            })
          },
        }),
        NDatePicker: datePickerStub(),
        'n-date-picker': datePickerStub(),
        DatePicker: datePickerStub(),
        NTag: defineComponent({ setup(_, { slots }) { return () => h('span', slots.default?.()) } }),
        NModal: modalStub(),
        'n-modal': modalStub(),
        ConfirmActionModal: defineComponent({
          props: ['visible', 'title', 'message', 'verifyValue', 'verifyHint'],
          emits: ['confirm', 'cancel'],
          setup(props, { emit }) {
            return () => props.visible
              ? h('div', { class: 'confirm-modal' }, [
                  h('strong', props.title),
                  h('p', props.message),
                  h('span', { class: 'verify-value' }, props.verifyValue),
                  h('span', { class: 'verify-hint' }, props.verifyHint),
                  h('button', { class: 'confirm-clear', onClick: () => emit('confirm') }, '确认清空'),
                ])
              : null
          },
        }),
        RAGFlowDatasetInfoDialog: defineComponent({
          props: ['visible', 'scope', 'targetId', 'targetName'],
          setup(props) {
            return () => props.visible
              ? h('div', { class: 'ragflow-dialog' }, `${props.scope}:${props.targetId}:${props.targetName}`)
              : null
          },
        }),
        NDataTable: tableStub(),
        'n-data-table': tableStub(),
        DataTable: tableStub(),
        DataTableList: defineComponent({
          props: {
            columns: { type: Array as PropType<DataTableColumn<IndustryKnowledgeBase>[]>, required: true },
            data: { type: Array as PropType<IndustryKnowledgeBase[]>, required: true },
          },
          setup(p, { slots }) {
            return () => h('section', [
              slots.toolbar?.(),
              h('table', [h('tbody', p.data.map(row =>
                h('tr', { key: row.id }, p.columns.map((col) => {
                  if ('render' in col && col.render) return h('td', [col.render(row, 0)])
                  return h('td', '')
                })),
              ))]),
            ])
          },
        }),
      },
    },
  })
}

function modalStub() {
  return defineComponent({
    props: ['show'],
    setup(p, { slots }) {
      return () => p.show ? h('section', { class: 'modal' }, slots.default?.()) : null
    },
  })
}

function tableStub() {
  return defineComponent({
    props: {
      columns: { type: Array as PropType<DataTableColumn<Record<string, unknown>>[]>, required: true },
      data: { type: Array as PropType<Record<string, unknown>[]>, required: true },
      pagination: { type: [Object, Boolean] as PropType<{
        page: number
        pageSize: number
        itemCount?: number
        onUpdatePage?: (page: number) => void
        onUpdatePageSize?: (pageSize: number) => void
      } | false>, default: false },
    },
    setup(p) {
      return () => h('div', [
        h('table', [h('tbody', p.data.map((row, index) =>
          h('tr', { key: String(row.id ?? index) }, p.columns.map((col) => {
            if ('render' in col && col.render) return h('td', [col.render(row, index)])
            return h('td', '')
          })),
        ))]),
        p.pagination
          ? h('div', { class: 'pagination-summary' }, [
              `page=${p.pagination.page};pageSize=${p.pagination.pageSize};total=${p.pagination.itemCount ?? 0}`,
              h('button', { class: 'page-two', onClick: () => p.pagination && p.pagination.onUpdatePage?.(2) }, '第 2 页'),
              h('button', { class: 'page-size-ten', onClick: () => p.pagination && p.pagination.onUpdatePageSize?.(10) }, '每页 10 条'),
            ])
          : null,
      ])
    },
  })
}

function datePickerStub() {
  return defineComponent({
    props: ['value'],
    emits: ['update:value'],
    setup(_, { emit }) {
      return () => h('button', {
        class: 'date-range',
        onClick: () => emit('update:value', [
          new Date(2026, 5, 1).getTime(),
          new Date(2026, 5, 5).getTime(),
        ]),
      }, '选择创建日期')
    },
  })
}

describe('IndustryKnowledgePage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authUser.current = { role: 'platform_admin' }
    fileQueryCalls.splice(0, fileQueryCalls.length)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    // 测试断言中文文案，设置 zh 语言以匹配 t() 返回值。
    i18n.global.locale.value = 'zh'
  })

  // 展示行业库列表、选中行业库文件和同名覆盖提示。
  it('展示行业库和文件列表', async () => {
    const wrapper = mountPage()
    await nextTick()

    expect(wrapper.text()).toContain('保险')
    expect(wrapper.text()).toContain('policy.pdf')
    expect(wrapper.text()).toContain('同名文件会覆盖当前行业库内的旧文件')
  })

  // 平台管理员需要通过入口查看远端 dataset 名称并调整 embedding 模型。
  it('platform_admin 可以看到并打开行业知识库 RAGFlow 信息入口', async () => {
    const wrapper = mountPage()
    await nextTick()

    await wrapper.findAll('button').find(button => button.text().includes('RAGFlow 信息'))!.trigger('click')

    expect(wrapper.find('.ragflow-dialog').text()).toBe('industry:industry-1:保险')
  })

  // 企业管理员仍可管理文件或容量，但不能触发 RAGFlow dataset 运维弹框。
  it('org_admin 看不到行业知识库 RAGFlow 信息入口', async () => {
    authUser.current = { role: 'org_admin', org_id: 'org-1' }
    const wrapper = mountPage()
    await nextTick()

    expect(wrapper.text()).not.toContain('RAGFlow 信息')
  })

  // 点击新建行业库后打开弹框，并在弹框确认时提交去除首尾空白后的行业名称。
  it('通过新建弹框提交行业名称', async () => {
    createBase.mockResolvedValue({ ...baseItems[0], id: 'industry-2', name: '医疗' })
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('新建行业库'))!.trigger('click')
    await nextTick()
    await wrapper.find('input[placeholder="请输入行业名称"]').setValue('  医疗  ')
    await wrapper.findAll('button').find(button => button.text().includes('确认创建'))!.trigger('click')

    expect(createBase).toHaveBeenCalledWith('医疗')
  })

  // 清空行业库文件内容需要二次确认，确认后只调用清空文件 mutation，不删除行业库记录。
  it('行业知识库支持二次确认后清空整库文件内容', async () => {
    clearFiles.mockResolvedValue(undefined)
    const wrapper = mountPage()
    await nextTick()

    await wrapper.findAll('button').find(button => button.text().includes('清空文件'))!.trigger('click')

    expect(wrapper.find('.confirm-modal').text()).toContain('确认清空行业知识库文件')
    expect(wrapper.find('.confirm-modal').text()).toContain('保险')

    await wrapper.find('.confirm-clear').trigger('click')

    expect(clearFiles).toHaveBeenCalledTimes(1)
    expect(deleteBase).not.toHaveBeenCalled()
    expect(messageSuccess).toHaveBeenCalledWith('已清空行业库「保险」文件')
  })

  // 覆盖行业库文件列表查询条件：文件名、创建日期和远程分页必须通过响应式参数传给 hook。
  it('行业库文件列表支持按文件名、创建日期搜索和远程分页', async () => {
    const wrapper = mountPage()
    const queryCall = fileQueryCalls[0]

    expect(wrapper.find('input[placeholder="搜索文件名称"]').exists()).toBe(true)
    expect(queryCall.options.page.value).toBe(1)
    expect(queryCall.options.pageSize.value).toBe(50)
    expect(queryCall.options.keyword.value).toBe('')
    expect(queryCall.options.createdFrom.value).toBeUndefined()
    expect(queryCall.options.createdTo.value).toBeUndefined()
    expect(wrapper.find('.pagination-summary').text()).toContain('total=1')

    await wrapper.find('.page-two').trigger('click')
    expect(queryCall.options.page.value).toBe(2)

    await wrapper.find('.page-size-ten').trigger('click')
    expect(queryCall.options.page.value).toBe(1)
    expect(queryCall.options.pageSize.value).toBe(10)

    await wrapper.find('input[placeholder="搜索文件名称"]').setValue(' policy ')
    expect(queryCall.options.keyword.value).toBe('policy')
    expect(queryCall.options.page.value).toBe(1)

    await wrapper.find('.date-range').trigger('click')
    expect(queryCall.options.createdFrom.value).toBe('2026-06-01')
    expect(queryCall.options.createdTo.value).toBe('2026-06-05')
    expect(queryCall.options.page.value).toBe(1)
  })

  // 弹框中行业名称为空时不提交创建，并提示平台管理员补充名称。
  it('弹框中行业名称为空时确认创建显示输入提示', async () => {
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('新建行业库'))!.trigger('click')
    await nextTick()
    await wrapper.findAll('button').find(button => button.text().includes('确认创建'))!.trigger('click')

    expect(createBase).not.toHaveBeenCalled()
    expect(messageWarning).toHaveBeenCalledWith('请输入行业名称')
  })

  // 接口文档弹框说明外部上传地址、鉴权 header、表单字段和 curl 调用方式。
  it('展示外部上传接口文档', async () => {
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('接口文档'))!.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('POST /api/v1/external/industry-knowledge/files')
    expect(wrapper.text()).toContain('X-OC-Industry-Knowledge-Token')
    expect(wrapper.text()).toContain('industry_name')
    expect(wrapper.text()).toContain('secret-token')
    expect(wrapper.text()).not.toContain('wrong-token')
    expect(wrapper.text()).toContain('curl')
  })

  // 复制 Markdown 会把完整接口文档写入剪贴板，方便交付给外部商业知识库服务方。
  it('复制外部上传接口 Markdown 文档', async () => {
    writeText.mockResolvedValue(undefined)
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('接口文档'))!.trigger('click')
    await nextTick()
    await wrapper.findAll('button').find(button => button.text().includes('复制 Markdown'))!.trigger('click')

    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText.mock.calls[0][0]).toContain('# 行业知识库外部上传接口')
    expect(writeText.mock.calls[0][0]).toContain('X-OC-Industry-Knowledge-Token: secret-token')
    expect(writeText.mock.calls[0][0]).not.toContain('wrong-token')
    expect(messageSuccess).toHaveBeenCalledWith('已复制 Markdown 文档')
  })
})
