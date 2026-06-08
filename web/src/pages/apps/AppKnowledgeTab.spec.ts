import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES, KNOWLEDGE_UPLOAD_MAX_MESSAGE } from '@/api/hooks/useKnowledge'
import AppKnowledgeTab from './AppKnowledgeTab.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
  updateQuotaMutateAsync: vi.fn(),
  canManage: vi.fn(() => true),
  authUser: { id: 'user-1', role: 'org_member', org_id: 'org-1' } as { id: string; role: string; org_id?: string },
  downloadAppKnowledgeFile: vi.fn(),
  appKnowledgeQueryCalls: [] as Array<{ appId: unknown; options: Record<string, { value: unknown }> }>,
}))

type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}

interface PaginationConfig {
  page: number
  pageSize: number
  itemCount?: number
  onUpdatePage?: (page: number) => void
  onUpdatePageSize?: (pageSize: number) => void
}

type UploadRunItem = { file: File; label: string }
type UploadRunContext = { onProgress: (percent: number) => void; signal: AbortSignal }

const uploadRunContexts: UploadRunContext[] = []

type RenderedChild = NonNullable<VNodeChild>

function renderCellChildren(column: RenderableColumn, row: unknown): RenderedChild[] {
  const child = column.render?.(row)
  return child == null ? [] : [child as RenderedChild]
}

// DataTableStub 渲染列 render 结果，确保单元测试能点击表格操作按钮。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
    pagination: { type: Object as PropType<PaginationConfig | false>, default: false },
  },
  setup(props) {
    return () => h('div', [
      h('div', { class: 'headers' }, props.columns.map((column) => h('span', { class: `header-${column.key}` }, column.title))),
      ...props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row)))),
      props.pagination
        ? h('div', { class: 'pagination-summary' }, [
            `page=${props.pagination.page};pageSize=${props.pagination.pageSize};total=${props.pagination.itemCount ?? 0}`,
            h('button', { class: 'page-two', onClick: () => props.pagination && props.pagination.onUpdatePage?.(2) }, '第 2 页'),
            h('button', { class: 'page-size-ten', onClick: () => props.pagination && props.pagination.onUpdatePageSize?.(10) }, '每页 10 条'),
          ])
        : null,
    ])
  },
})

// CardStub 承接 n-card 根节点上的 class 和拖拽监听，便于验证 drop zone 行为。
const CardStub = { template: '<section v-bind="$attrs"><slot name="header" /><slot name="header-extra" /><slot /></section>' }

// InputStub 模拟 naive-ui 的 v-model:value 协议，让搜索框测试聚焦查询状态变化。
const InputStub = defineComponent({
  props: {
    value: { type: String, default: '' },
    placeholder: { type: String, default: '' },
  },
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('input', {
      value: props.value,
      placeholder: props.placeholder,
      onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
    })
  },
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: mocks.authUser }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', async () => {
  const actual = await vi.importActual<typeof import('@/domain/permissions')>('@/domain/permissions')
  return {
    ...actual,
    canManageApp: mocks.canManage,
  }
})

vi.mock('@/api/hooks/useApps', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useApps')>('@/api/hooks/useApps')
  return {
    ...actual,
    useUpdateAppKnowledgeQuota: () => ({
      mutateAsync: mocks.updateQuotaMutateAsync,
      isPending: ref(false),
    }),
  }
})

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ error: mocks.error, warning: mocks.warning }),
  }
})

vi.mock('@/api/hooks/useKnowledge', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useKnowledge')>('@/api/hooks/useKnowledge')
  return {
    ...actual,
    useAppKnowledgeQuery: (appId: unknown, options: Record<string, { value: unknown }> = {}) => {
      mocks.appKnowledgeQueryCalls.push({ appId, options })
      return {
        data: ref({
          items: [{ id: 'doc-app-1', name: 'readme.md', size: 5, parse_status: 'completed', progress: 100, created_at: '2026-05-27T00:00:00Z' }],
          total: 1,
          used_bytes: 5,
          quota_bytes: 100,
          remaining_bytes: 95,
        }),
        isLoading: ref(false),
        error: ref(null),
      }
    },
    downloadAppKnowledgeFile: mocks.downloadAppKnowledgeFile,
    useUploadAppKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteAppKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
    useReparseAppKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
  }
})

function mountTab() {
  return mount(AppKnowledgeTab, {
    props: { appId: 'app-1' },
    global: {
      provide: {
        app: ref({
          id: 'app-1',
          org_id: 'org-1',
          owner_user_id: 'user-1',
          name: '测试实例',
          status: 'running',
          api_key_status: 'active',
          knowledge_quota_bytes: 100,
        }),
      },
      stubs: {
        NCard: CardStub,
        'n-card': CardStub,
        NInput: InputStub,
        DataTable: DataTableStub,
        NDataTable: DataTableStub,
        NButton: { template: '<button><slot /></button>' },
        NTag: { template: '<span><slot /></span>' },
        RAGFlowDatasetInfoDialog: defineComponent({
          props: ['visible', 'scope', 'targetId', 'targetName'],
          setup(props) {
            return () => props.visible
              ? h('div', { class: 'ragflow-dialog' }, `${props.scope}:${props.targetId}:${props.targetName}`)
              : null
          },
        }),
      },
    },
  })
}

function oversizedFile(): File {
  const file = new File(['x'], 'huge.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'size', { value: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })
  return file
}

function fileDragTransfer(dropEffect = 'none') {
  return {
    items: [{ kind: 'file' }],
    files: [],
    dropEffect,
  }
}

describe('AppKnowledgeTab', () => {
  beforeEach(() => {
    mocks.authUser = { id: 'user-1', role: 'org_member', org_id: 'org-1' }
    mocks.canManage.mockReturnValue(true)
    mocks.downloadAppKnowledgeFile.mockReset()
    mocks.error.mockReset()
    mocks.warning.mockReset()
    mocks.run.mockReset()
    uploadRunContexts.splice(0, uploadRunContexts.length)
    mocks.appKnowledgeQueryCalls.splice(0, mocks.appKnowledgeQueryCalls.length)
    mocks.run.mockImplementation(async (items: UploadRunItem[], runner: (item: UploadRunItem, file: File, ctx: UploadRunContext) => Promise<void>) => {
      for (const item of items) {
        const ctx = { onProgress: vi.fn(), signal: new AbortController().signal }
        uploadRunContexts.push(ctx)
        await runner(item, item.file, ctx)
      }
    })
    mocks.mutateAsync.mockReset()
    mocks.updateQuotaMutateAsync.mockReset()
  })

  // 覆盖实例知识库文件列表列头文案：文件名列必须明确显示为「文件名称」。
  it('实例知识库文件列表首列展示文件名称', () => {
    const wrapper = mountTab()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })

  // 覆盖实例知识库容量编辑入口：企业管理员可看到编辑空间按钮。
  it('企业管理员可看到实例知识库空间编辑入口', () => {
    mocks.authUser = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }
    const wrapper = mountTab()

    expect(wrapper.text()).toContain('编辑空间')
  })

  // 平台管理员需要通过入口查看实例知识库远端 dataset 名称并调整 embedding 模型。
  it('platform_admin 可以看到实例知识库 RAGFlow 信息入口', async () => {
    mocks.authUser = { id: 'platform-1', role: 'platform_admin', org_id: undefined }
    const wrapper = mountTab()

    await wrapper.findAll('button').find(button => button.text().includes('RAGFlow 信息'))!.trigger('click')

    expect(wrapper.find('.ragflow-dialog').text()).toBe('app:app-1:测试实例')
  })

  // 企业管理员仍可管理文件或容量，但不能触发 RAGFlow dataset 运维弹框。
  it('org_admin 看不到实例知识库 RAGFlow 信息入口', () => {
    mocks.authUser = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }
    const wrapper = mountTab()

    expect(wrapper.text()).not.toContain('RAGFlow 信息')
  })

  // 覆盖实例知识库列表查询条件：搜索关键词、页码和页大小必须通过响应式参数传给 hook。
  it('实例知识库列表支持按文件名搜索和远程分页', async () => {
    const wrapper = mountTab()
    const queryCall = mocks.appKnowledgeQueryCalls[0]

    expect(wrapper.find('input[placeholder="搜索文件名称"]').exists()).toBe(true)
    expect(queryCall.options.page.value).toBe(1)
    expect(queryCall.options.pageSize.value).toBe(50)
    expect(queryCall.options.keyword.value).toBe('')
    expect(wrapper.find('.pagination-summary').text()).toContain('total=1')

    await wrapper.find('.page-two').trigger('click')
    expect(queryCall.options.page.value).toBe(2)

    await wrapper.find('.page-size-ten').trigger('click')
    expect(queryCall.options.page.value).toBe(1)
    expect(queryCall.options.pageSize.value).toBe(10)

    await wrapper.find('input[placeholder="搜索文件名称"]').setValue(' readme ')
    expect(queryCall.options.keyword.value).toBe('readme')
    expect(queryCall.options.page.value).toBe(1)
  })

  // 覆盖实例知识库上传入口：文件选择框必须允许一次选择多个文件。
  it('实例知识库文件选择框允许多选', () => {
    const wrapper = mountTab()

    expect(wrapper.find('input[type="file"]').attributes('multiple')).toBeDefined()
  })

  // 覆盖实例知识库上传超限路径：前端提示上限并且不创建上传会话。
  it('拒绝超过上限的实例知识库文件', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖实例知识库容量动态失败：超过 remaining_bytes 的文件仍进入队列，由后端逐个返回失败。
  it('超过实例知识库剩余空间的文件仍交给上传队列', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')
    const file = new File(['x'], 'too-large.md')
    Object.defineProperty(file, 'size', { value: 96 })

    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).not.toHaveBeenCalled()
    expect(mocks.run).toHaveBeenCalledTimes(1)
    expect(mocks.run.mock.calls[0][0]).toEqual([{ file, label: 'too-large.md' }])
  })

  // 覆盖实例知识库多选上传：多个文件应按选择顺序交给全局上传队列。
  it('支持一次选择多个实例知识库文件上传', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    Object.defineProperty(input.element, 'files', { value: [first, second], configurable: true })
    await input.trigger('change')

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(1, {
      file: first,
      onProgress: uploadRunContexts[0].onProgress,
      signal: uploadRunContexts[0].signal,
    })
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(2, {
      file: second,
      onProgress: uploadRunContexts[1].onProgress,
      signal: uploadRunContexts[1].signal,
    })
  })

  // 覆盖实例知识库拖拽上传：拖入多个文件时复用同一批量上传流程。
  it('支持拖拽多个实例知识库文件上传', async () => {
    const wrapper = mountTab()
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    await wrapper.find('.knowledge-drop-zone').trigger('drop', {
      dataTransfer: {
        items: [],
        files: [first, second],
      },
    })

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(1, {
      file: first,
      onProgress: uploadRunContexts[0].onProgress,
      signal: uploadRunContexts[0].signal,
    })
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(2, {
      file: second,
      onProgress: uploadRunContexts[1].onProgress,
      signal: uploadRunContexts[1].signal,
    })
  })

  // 覆盖实例知识库拖拽态：文件拖入时显示视觉态，内部移动不清空，真正离开卡片才清空。
  it('实例知识库文件拖拽态仅在离开卡片时清空', async () => {
    const wrapper = mountTab()
    const section = wrapper.find('.knowledge-drop-zone')
    const inner = section.find('.headers')
    const dataTransfer = fileDragTransfer()

    await section.trigger('dragenter', { dataTransfer })
    expect(section.classes()).toContain('drag-active')

    await section.trigger('dragover', { dataTransfer })
    expect(dataTransfer.dropEffect).toBe('copy')

    await section.trigger('dragleave', { relatedTarget: inner.element })
    expect(section.classes()).toContain('drag-active')

    await section.trigger('dragleave', { relatedTarget: document.body })
    expect(section.classes()).not.toContain('drag-active')
  })

  // 覆盖实例知识库只读拖拽：无写权限时拖拽和 drop 都不会创建上传会话。
  it('只读实例知识库拖拽文件不会上传', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountTab()
    const first = new File(['a'], 'a.md')
    const section = wrapper.find('.knowledge-drop-zone')

    await section.trigger('dragenter', { dataTransfer: fileDragTransfer() })
    await section.trigger('drop', {
      dataTransfer: {
        items: [],
        files: [first],
      },
    })

    expect(section.classes()).not.toContain('drag-active')
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖实例知识库只读场景：可读用户可以下载文件，且下载按 RAGFlow document ID 定位。
  it('只读用户可下载实例知识库文件但不可删除', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountTab()

    expect(wrapper.text()).toContain('下载')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('button').trigger('click')

    expect(mocks.downloadAppKnowledgeFile).toHaveBeenCalledWith('app-1', 'doc-app-1', 'readme.md')
  })
})
