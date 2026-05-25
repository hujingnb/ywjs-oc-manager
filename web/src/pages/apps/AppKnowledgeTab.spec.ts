import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES } from '@/api/hooks/useKnowledge'
import AppKnowledgeTab from './AppKnowledgeTab.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'user-1', role: 'org_member', org_id: 'org-1' } }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', () => ({
  canManageApp: () => true,
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
    useAppKnowledgeQuery: () => ({
      data: ref({ path: '', entries: [] }),
      isLoading: ref(false),
      error: ref(null),
    }),
    useUploadAppKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteAppKnowledge: () => ({
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
        }),
      },
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NDataTable: { template: '<table />' },
        NButton: { template: '<button><slot /></button>' },
      },
    },
  })
}

function oversizedFile(): File {
  const file = new File(['x'], 'huge.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'size', { value: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })
  return file
}

describe('AppKnowledgeTab', () => {
  // 覆盖实例知识库上传超限路径：前端提示 100MB 限制，并且不创建上传会话。
  it('拒绝超过 100MB 的实例知识库文件', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith('单文件最多支持 100MB')
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })
})
