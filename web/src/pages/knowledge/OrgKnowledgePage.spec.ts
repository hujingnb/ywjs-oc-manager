import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES } from '@/api/hooks/useKnowledge'
import OrgKnowledgePage from './OrgKnowledgePage.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
  canManage: vi.fn(() => true),
  downloadOrgKnowledgeFile: vi.fn(),
}))

type RenderableColumn = {
  key: string
  render?: (row: unknown) => VNodeChild
}

type RenderedChild = NonNullable<VNodeChild>

function renderCellChildren(column: RenderableColumn, row: unknown): RenderedChild[] {
  const child = column.render?.(row)
  return child == null ? [] : [child as RenderedChild]
}

// DataTableStub 渲染列 render 结果，确保单元测试能覆盖表格操作按钮。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () => h('div', props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row)))))
  },
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'admin-1', role: 'org_admin', org_id: 'org-1' } }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', () => ({
  canManageOrgKnowledge: mocks.canManage,
}))

vi.mock('@/composables/usePlatformOrgSelection', () => ({
  usePlatformOrgSelection: () => ({
    isPlatformAdmin: ref(false),
    selectedOrgId: ref('org-1'),
    effectiveOrgId: ref('org-1'),
    orgOptions: ref([]),
    organizationsLoading: ref(false),
  }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ warning: mocks.warning }),
  }
})

vi.mock('@/api/hooks/useKnowledge', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useKnowledge')>('@/api/hooks/useKnowledge')
  return {
    ...actual,
    useOrgKnowledgeQuery: () => ({
      data: ref({
        items: [{ id: 'doc-1', name: 'readme.md', size: 5, parse_status: 'completed', progress: 100, created_at: '2026-05-27T00:00:00Z' }],
        total: 1,
      }),
      isLoading: ref(false),
      error: ref(null),
    }),
    downloadOrgKnowledgeFile: mocks.downloadOrgKnowledgeFile,
    useUploadOrgKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteOrgKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
    useReparseOrgKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
  }
})

function mountPage() {
  return mount(OrgKnowledgePage, {
    global: {
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NSpace: { template: '<div><slot /></div>' },
        NSelect: { template: '<select />' },
        NDataTable: DataTableStub,
        NButton: { template: '<button><slot /></button>' },
        NTag: { template: '<span><slot /></span>' },
      },
    },
  })
}

function oversizedFile(): File {
  const file = new File(['x'], 'huge.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'size', { value: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })
  return file
}

describe('OrgKnowledgePage', () => {
  beforeEach(() => {
    mocks.canManage.mockReturnValue(true)
    mocks.downloadOrgKnowledgeFile.mockReset()
    mocks.run.mockReset()
    mocks.warning.mockReset()
    mocks.mutateAsync.mockReset()
  })

  // 覆盖组织知识库上传超限路径：前端提示 100MB 限制，并且不创建上传会话。
  it('拒绝超过 100MB 的组织知识库文件', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith('单文件最多支持 100MB')
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖组织成员只读场景：可下载组织知识库文件，且下载按 RAGFlow document ID 定位。
  it('组织成员可下载组织知识库文件但不可删除', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('下载')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('button').trigger('click')

    expect(mocks.downloadOrgKnowledgeFile).toHaveBeenCalledWith('org-1', 'doc-1', 'readme.md')
  })
})
