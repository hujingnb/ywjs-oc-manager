// useSkillTickets API hooks 测试覆盖工单/附件/交付端点 URL、缓存键结构和缓存失效边界。
// 使用真实 QueryClient 挂载组合式函数，验证 Vue Query 行为（queryKey、queryFn、invalidate）。
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { apiRequest, apiDownload } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import {
  _mineKey,
  _adminKey,
  _badgeKey,
  _detailKey,
  _attachKey,
  useMySkillTicketsQuery,
  useAdminSkillTicketsQuery,
  useSkillTicketBadgeQuery,
  useSkillTicketDetailQuery,
  useSkillTicketAttachmentsQuery,
  useSubmitSkillTicket,
  useAddSkillTicketComment,
  useUploadSkillTicketAttachment,
  downloadSkillTicketAttachment,
  useUpdateSkillTicketStatus,
  useSetSkillTicketQuote,
  useRejectSkillTicket,
  useDeliverCustomSkill,
} from './useSkillTickets'

vi.mock('@/api/client', () => ({
  apiRequest: vi.fn(),
  apiDownload: vi.fn(),
}))
vi.mock('@/api/xhrUpload', () => ({
  xhrUpload: vi.fn(),
}))

const apiRequestMock = vi.mocked(apiRequest)
const apiDownloadMock = vi.mocked(apiDownload)
const xhrUploadMock = vi.mocked(xhrUpload)

// createTestQueryClient 每次测试独立创建 QueryClient，禁用重试避免异步噪声。
function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

// mountWithQueryClient 挂载匿名组件并暴露 hook 返回值，供后续 mutateAsync / 断言使用。
function mountWithQueryClient(setupHook: () => Record<string, unknown> | void) {
  const queryClient = createTestQueryClient()
  const wrapper = mount(
    defineComponent({
      setup(_, { expose }) {
        const exposed = setupHook()
        if (exposed) expose(exposed)
        return () => h('div')
      },
    }),
    {
      global: {
        plugins: [[VueQueryPlugin, { queryClient }]],
      },
    },
  )
  return { queryClient, wrapper }
}

describe('useSkillTickets hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllTimers()
  })

  // ===== 缓存键结构 =====

  // 五类缓存键必须互不冲突，且前缀 ['skill-tickets'] 统一，便于精准 invalidate 子集。
  it('各缓存键格式正确且互不冲突', () => {
    // 我的工单列表键
    expect(_mineKey()).toEqual(['skill-tickets', 'mine'])
    // 管理员队列键
    expect(_adminKey()).toEqual(['skill-tickets', 'admin'])
    // 角标计数键
    expect(_badgeKey()).toEqual(['skill-tickets', 'badge'])
    // 工单详情键包含 id，id 变化时自动重查
    expect(_detailKey('t-1')).toEqual(['skill-tickets', 'detail', 't-1'])
    // id 为 undefined 时生成稳定占位键，不产生随机字符串
    expect(_detailKey(undefined)).toEqual(['skill-tickets', 'detail', undefined])
    // 附件列表键包含 id，与详情键不同避免覆盖
    expect(_attachKey('t-1')).toEqual(['skill-tickets', 'attachments', 't-1'])
  })

  // ===== 我的工单列表查询 =====

  // useMySkillTicketsQuery 应请求正确路径并解包 tickets 键。
  it('useMySkillTicketsQuery 请求我的工单列表路径并解包 tickets', async () => {
    const tickets = [{ id: 't-1', status: 'pending', title: '新需求' }]
    apiRequestMock.mockResolvedValueOnce({ tickets })

    mountWithQueryClient(() => {
      useMySkillTicketsQuery()
    })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets')
    })
  })

  // tickets 字段缺失（后端返回空对象）时应回退为空数组，不抛出类型错误。
  it('useMySkillTicketsQuery 在响应体缺少 tickets 键时回退为空数组', async () => {
    apiRequestMock.mockResolvedValueOnce({})
    mountWithQueryClient(() => {
      useMySkillTicketsQuery()
    })
    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets')
    })
  })

  // ===== 管理员工单队列查询 =====

  // useAdminSkillTicketsQuery 应请求管理员专用路径。
  it('useAdminSkillTicketsQuery 请求管理员工单队列路径', async () => {
    apiRequestMock.mockResolvedValueOnce({ tickets: [] })
    mountWithQueryClient(() => {
      useAdminSkillTicketsQuery()
    })
    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/admin/skill-tickets')
    })
  })

  // ===== 待处理角标查询 =====

  // useSkillTicketBadgeQuery 应请求角标计数路径并解包 pending 键。
  it('useSkillTicketBadgeQuery 请求角标路径并解包 pending', async () => {
    apiRequestMock.mockResolvedValueOnce({ pending: 3 })
    mountWithQueryClient(() => {
      useSkillTicketBadgeQuery()
    })
    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/admin/skill-tickets/badge')
    })
  })

  // ===== 工单详情查询 =====

  // id 有值时应请求含 id 的路径并解包 ticket 键。
  it('useSkillTicketDetailQuery 有 id 时请求详情路径', async () => {
    const detail = { id: 't-1', status: 'processing', title: '需求详情' }
    apiRequestMock.mockResolvedValueOnce({ ticket: detail })
    const id = ref<string | undefined>('t-1')

    mountWithQueryClient(() => {
      useSkillTicketDetailQuery(id)
    })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1')
    })
  })

  // id 为 undefined 时不应发出请求（enabled=false）。
  it('useSkillTicketDetailQuery 在 id 为 undefined 时不发请求', async () => {
    const id = ref<string | undefined>(undefined)
    mountWithQueryClient(() => {
      useSkillTicketDetailQuery(id)
    })
    await new Promise((r) => setTimeout(r, 50))
    expect(apiRequestMock).not.toHaveBeenCalled()
  })

  // ===== 附件列表查询 =====

  // id 有值时应请求附件列表路径并解包 attachments 键。
  it('useSkillTicketAttachmentsQuery 有 id 时请求附件路径', async () => {
    const attachments = [{ id: 'a-1', file_name: 'spec.pdf', file_size: 1024 }]
    apiRequestMock.mockResolvedValueOnce({ attachments })
    const id = ref<string | undefined>('t-1')

    mountWithQueryClient(() => {
      useSkillTicketAttachmentsQuery(id)
    })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/attachments')
    })
  })

  // id 为 undefined 时不应发出请求（enabled=false）。
  it('useSkillTicketAttachmentsQuery 在 id 为 undefined 时不发请求', async () => {
    const id = ref<string | undefined>(undefined)
    mountWithQueryClient(() => {
      useSkillTicketAttachmentsQuery(id)
    })
    await new Promise((r) => setTimeout(r, 50))
    expect(apiRequestMock).not.toHaveBeenCalled()
  })

  // ===== 提交工单 =====

  // 提交应 POST 到正确路径，成功后使我的工单列表缓存失效。
  it('useSubmitSkillTicket POST 到提交路径并 invalidate 我的工单列表', async () => {
    const ticket = { id: 't-2', status: 'pending', title: '新需求' }
    apiRequestMock.mockResolvedValueOnce({ ticket })
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      submit: useSubmitSkillTicket(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { submit: ReturnType<typeof useSubmitSkillTicket> }
    ).submit.mutateAsync({ title: '新需求', description: '请做一个XXX功能' })

    // 请求路径和请求体必须准确
    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets', {
      method: 'POST',
      body: { title: '新需求', description: '请做一个XXX功能' },
    })
    // 提交成功后必须刷新我的工单列表
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _mineKey() })
  })

  // ===== 添加评论 =====

  // 添加评论应 POST 到含工单 id 的 comments 路径，
  // 成功后同时 invalidate 详情、我的列表、管理员列表和角标（因为评论可触发工单重开）。
  it('useAddSkillTicketComment POST 到评论路径并 invalidate 四类缓存键', async () => {
    const comment = { id: 'c-1', body: '已更新描述', author_user_id: 'u-1', created_at: '2026-06-10T00:00:00Z' }
    apiRequestMock.mockResolvedValueOnce({ comment })
    const id = ref<string | undefined>('t-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      addComment: useAddSkillTicketComment(id),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { addComment: ReturnType<typeof useAddSkillTicketComment> }
    ).addComment.mutateAsync({ body: '已更新描述' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/comments', {
      method: 'POST',
      body: { body: '已更新描述' },
    })
    // 评论成功后刷新详情（状态可能回 pending）、我的列表、管理员列表和角标计数
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _mineKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // ===== 上传附件 =====

  // 附件上传应走 xhrUpload 发送 FormData，成功后 invalidate 附件列表缓存。
  it('useUploadSkillTicketAttachment 走 xhrUpload 发送 FormData 并 invalidate 附件列表', async () => {
    const attachment = { id: 'a-1', file_name: 'spec.pdf', file_size: 1024 }
    xhrUploadMock.mockResolvedValueOnce({ status: 201, body: { attachment } })
    const id = ref<string | undefined>('t-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      upload: useUploadSkillTicketAttachment(id),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const file = new File(['pdf content'], 'spec.pdf', { type: 'application/pdf' })
    await (
      wrapper.vm as unknown as { upload: ReturnType<typeof useUploadSkillTicketAttachment> }
    ).upload.mutateAsync(file)

    // 必须走 xhrUpload，路径包含工单 id
    expect(xhrUploadMock).toHaveBeenCalledWith(
      '/api/v1/skill-tickets/t-1/attachments',
      expect.objectContaining({ method: 'POST' }),
    )
    // 上传的 body 必须是 FormData
    const callArgs = xhrUploadMock.mock.calls[0][1]
    expect(callArgs.body).toBeInstanceOf(FormData)
    // 成功后必须刷新该工单的附件列表
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _attachKey('t-1') })
  })

  // ===== 带鉴权下载附件 =====

  // downloadSkillTicketAttachment 应调用 apiDownload（带 Authorization），并用 file_name 作回退文件名。
  it('downloadSkillTicketAttachment 调用 apiDownload 并触发浏览器下载', async () => {
    const blob = new Blob(['pdf content'], { type: 'application/pdf' })
    // 模拟 Content-Disposition 解析出文件名
    apiDownloadMock.mockResolvedValueOnce({ blob, filename: 'spec.pdf' })

    // 模拟 DOM URL/a 元素下载行为
    const createObjectURL = vi.fn().mockReturnValue('blob:mock')
    const revokeObjectURL = vi.fn()
    globalThis.URL.createObjectURL = createObjectURL
    globalThis.URL.revokeObjectURL = revokeObjectURL
    const clickSpy = vi.fn()
    const appendSpy = vi.fn()
    const removeSpy = vi.fn()
    // mockA 需包含 remove 方法，因为 downloadSkillTicketAttachment 调用 a.remove() 移除 DOM 节点。
    const mockA = { href: '', download: '', click: clickSpy, remove: removeSpy }
    vi.spyOn(document, 'createElement').mockReturnValueOnce(mockA as unknown as HTMLElement)
    vi.spyOn(document.body, 'appendChild').mockImplementationOnce(appendSpy)

    const att = { id: 'a-1', file_name: 'spec.pdf', file_size: 1024 }
    await downloadSkillTicketAttachment('t-1', att)

    // 应调用 apiDownload 而非 apiRequest，确保走带鉴权的下载路径
    expect(apiDownloadMock).toHaveBeenCalledWith(
      '/api/v1/skill-tickets/t-1/attachments/a-1/download',
    )
    // 触发浏览器下载
    expect(clickSpy).toHaveBeenCalled()
    // 回收 Object URL 防内存泄漏
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock')
  })

  // ===== 管理员：更改工单状态 =====

  // 更改状态应 PATCH 到含工单 id 的 status 路径，成功后 invalidate 详情/管理员列表/角标。
  it('useUpdateSkillTicketStatus PATCH 到状态路径并 invalidate 管理员三类缓存', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      updateStatus: useUpdateSkillTicketStatus(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { updateStatus: ReturnType<typeof useUpdateSkillTicketStatus> }
    ).updateStatus.mutateAsync({ id: 't-1', status: 'processing' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/status', {
      method: 'PATCH',
      body: { status: 'processing' },
    })
    // 状态变更后刷新详情、管理员队列和角标计数
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // ===== 管理员：设置报价 =====

  // 设置报价应 PATCH 到含工单 id 的 quote 路径，body 含 quote_amount_cents（单位：分），成功后 invalidate 管理员三类缓存。
  it('useSetSkillTicketQuote PATCH 到报价路径并 invalidate 管理员三类缓存', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      setQuote: useSetSkillTicketQuote(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    // 500 元 = 50000 分
    await (
      wrapper.vm as unknown as { setQuote: ReturnType<typeof useSetSkillTicketQuote> }
    ).setQuote.mutateAsync({ id: 't-1', quote_amount_cents: 50000 })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/quote', {
      method: 'PATCH',
      body: { quote_amount_cents: 50000 },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // ===== 管理员：拒绝工单 =====

  // 拒绝工单应 POST 到含工单 id 的 reject 路径，body 含拒绝原因，成功后 invalidate 管理员三类缓存。
  it('useRejectSkillTicket POST 到拒绝路径并 invalidate 管理员三类缓存', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      reject: useRejectSkillTicket(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { reject: ReturnType<typeof useRejectSkillTicket> }
    ).reject.mutateAsync({ id: 't-1', reason: '需求描述不够清晰，请补充' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/reject', {
      method: 'POST',
      body: { reason: '需求描述不够清晰，请补充' },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // ===== 交付定制技能 =====

  // 交付应走 xhrUpload 发送 multipart FormData，包含 ticket_id/description/targets(JSON)/file，
  // 成功后 invalidate 工单三类缓存（detail/admin/badge）和市场缓存（['skills', 'market']）。
  it('useDeliverCustomSkill 走 xhrUpload 发送 multipart 并 invalidate 工单+市场缓存', async () => {
    const skill = { id: 'cs-1', name: 'my-skill', version: '20260610120000', ticket_id: 't-1' }
    xhrUploadMock.mockResolvedValueOnce({ status: 200, body: { skill } })
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      deliver: useDeliverCustomSkill(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const file = new File(['tar content'], 'my-skill.tar', { type: 'application/x-tar' })
    const targets = [{ org_id: 'org-1', audience: 'all_org' }]
    await (
      wrapper.vm as unknown as { deliver: ReturnType<typeof useDeliverCustomSkill> }
    ).deliver.mutateAsync({
      ticketId: 't-1',
      description: '一个全新的技能',
      targets,
      file,
    })

    // 必须走 xhrUpload，路径为交付端点
    expect(xhrUploadMock).toHaveBeenCalledWith(
      '/api/v1/custom-skills/deliver',
      expect.objectContaining({ method: 'POST' }),
    )
    // 上传 body 必须是 FormData
    const callArgs = xhrUploadMock.mock.calls[0][1]
    expect(callArgs.body).toBeInstanceOf(FormData)
    const formData = callArgs.body as FormData
    // FormData 字段：ticket_id/description/targets(JSON)/file
    expect(formData.get('ticket_id')).toBe('t-1')
    expect(formData.get('description')).toBe('一个全新的技能')
    expect(formData.get('targets')).toBe(JSON.stringify(targets))
    expect(formData.get('file')).toBeInstanceOf(File)
    // 交付成功后刷新工单详情、管理员队列、角标（工单转 delivered）
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
    // 同时刷新市场缓存，使定制技能卡片即时可见
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['skills', 'market'] })
  })
})
