import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import IndustryKnowledgePage from './IndustryKnowledgePage.vue'
import type { IndustryKnowledgeBase } from '@/api/hooks/useIndustryKnowledge'
import type { KnowledgeDocument } from '@/api/hooks/useKnowledge'

const createBase = vi.hoisted(() => vi.fn())
const renameBase = vi.hoisted(() => vi.fn())
const deleteBase = vi.hoisted(() => vi.fn())
const uploadFile = vi.hoisted(() => vi.fn())
const deleteFile = vi.hoisted(() => vi.fn())
const reparseFile = vi.hoisted(() => vi.fn())
const messageWarning = vi.hoisted(() => vi.fn())
const messageSuccess = vi.hoisted(() => vi.fn())
const messageError = vi.hoisted(() => vi.fn())
const writeText = vi.hoisted(() => vi.fn())

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
  useIndustryKnowledgeFilesQuery: () => ({
    data: ref({ items: fileItems, total: fileItems.length }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useCreateIndustryKnowledgeBase: () => ({ mutateAsync: createBase, isPending: ref(false) }),
  useRenameIndustryKnowledgeBase: () => ({ mutateAsync: renameBase, isPending: ref(false) }),
  useDeleteIndustryKnowledgeBase: () => ({ mutateAsync: deleteBase, isPending: ref(false) }),
  useUploadIndustryKnowledgeFile: () => ({ mutateAsync: uploadFile, isPending: ref(false) }),
  useDeleteIndustryKnowledgeFile: () => ({ mutateAsync: deleteFile, isPending: ref(false) }),
  useReparseIndustryKnowledgeFile: () => ({ mutateAsync: reparseFile, isPending: ref(false) }),
  downloadIndustryKnowledgeFile: vi.fn(),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({
    run: vi.fn(async () => undefined),
  }),
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
          props: ['value'],
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('input', {
              value: p.value,
              onInput: (e: Event) => emit('update:value', (e.target as HTMLInputElement).value),
            })
          },
        }),
        NTag: defineComponent({ setup(_, { slots }) { return () => h('span', slots.default?.()) } }),
        NModal: modalStub(),
        'n-modal': modalStub(),
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
    },
    setup(p) {
      return () => h('table', [h('tbody', p.data.map((row, index) =>
        h('tr', { key: String(row.id ?? index) }, p.columns.map((col) => {
          if ('render' in col && col.render) return h('td', [col.render(row, index)])
          return h('td', '')
        })),
      ))])
    },
  })
}

describe('IndustryKnowledgePage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
  })

  // 展示行业库列表、选中行业库文件和同名覆盖提示。
  it('展示行业库和文件列表', async () => {
    const wrapper = mountPage()
    await nextTick()

    expect(wrapper.text()).toContain('保险')
    expect(wrapper.text()).toContain('policy.pdf')
    expect(wrapper.text()).toContain('同名文件会覆盖当前行业库内的旧文件')
  })

  // 点击新建行业库后打开弹框，并在弹框确认时提交去除首尾空白后的行业名称。
  it('通过新建弹框提交行业名称', async () => {
    createBase.mockResolvedValue({ ...baseItems[0], id: 'industry-2', name: '医疗' })
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('新建行业库'))!.trigger('click')
    await nextTick()
    const textInputs = wrapper.findAll('input').filter(input => input.attributes('type') !== 'file')
    await textInputs[1].setValue('  医疗  ')
    await wrapper.findAll('button').find(button => button.text().includes('确认创建'))!.trigger('click')

    expect(createBase).toHaveBeenCalledWith('医疗')
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
