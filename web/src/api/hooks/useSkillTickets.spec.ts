// useSkillTickets API hooks 测试覆盖统一消息、动作按钮和可见范围更新的路径与缓存失效。
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { apiDownload, apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import {
  _adminKey,
  _badgeKey,
  _detailKey,
  _mineKey,
  useAdminSkillTicketsQuery,
  useDeliverCustomSkill,
  useMySkillTicketsQuery,
  useRejectSkillTicket,
  useReopenTicket,
  useSendTicketMessage,
  useSetSkillTicketQuote,
  useSkillTicketBadgeQuery,
  useSkillTicketDetailQuery,
  useStartTicket,
  useSubmitSkillTicket,
  useUpdateTicketTargets,
  useUploadTicketMessage,
  downloadTicketMessage,
  fetchTicketMessageBlobUrl,
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

// createTestQueryClient 每个测试独立创建 QueryClient,关闭重试避免失败路径产生额外异步请求。
function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

// mountWithQueryClient 挂载匿名组件并 expose hook 返回值,便于直接调用 mutateAsync。
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
    { global: { plugins: [[VueQueryPlugin, { queryClient }]] } },
  )
  return { queryClient, wrapper }
}

describe('useSkillTickets hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllTimers()
    vi.restoreAllMocks()
  })

  // 工单核心缓存键必须稳定,供各 mutation 精准失效。
  it('核心缓存键格式稳定', () => {
    expect(_mineKey()).toEqual(['skill-tickets', 'mine'])
    expect(_adminKey()).toEqual(['skill-tickets', 'admin'])
    expect(_badgeKey()).toEqual(['skill-tickets', 'badge'])
    expect(_detailKey('t-1')).toEqual(['skill-tickets', 'detail', 't-1'])
    expect(_detailKey(undefined)).toEqual(['skill-tickets', 'detail', undefined])
  })

  // 我的工单列表查询请求 /skill-tickets 并解包 tickets 字段。
  it('useMySkillTicketsQuery 请求我的工单列表', async () => {
    apiRequestMock.mockResolvedValueOnce({ tickets: [{ id: 't-1', status: 'pending', title: '新需求' }] })
    mountWithQueryClient(() => {
      useMySkillTicketsQuery()
    })
    await vi.waitFor(() => expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets'))
  })

  // 管理员队列查询请求 /admin/skill-tickets。
  it('useAdminSkillTicketsQuery 请求管理员工单队列', async () => {
    apiRequestMock.mockResolvedValueOnce({ tickets: [] })
    mountWithQueryClient(() => {
      useAdminSkillTicketsQuery()
    })
    await vi.waitFor(() => expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/admin/skill-tickets'))
  })

  // 待处理角标查询请求 badge 端点并解包 pending。
  it('useSkillTicketBadgeQuery 请求角标路径', async () => {
    apiRequestMock.mockResolvedValueOnce({ pending: 2 })
    mountWithQueryClient(() => {
      useSkillTicketBadgeQuery()
    })
    await vi.waitFor(() => expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/admin/skill-tickets/badge'))
  })

  // 角标端点仅平台管理员可访问；禁用时不能向 admin badge 端点发请求，避免普通成员页面产生 403。
  it('useSkillTicketBadgeQuery disabled 时不请求管理员角标', async () => {
    mountWithQueryClient(() => {
      useSkillTicketBadgeQuery(ref(false))
    })
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(apiRequestMock).not.toHaveBeenCalled()
  })

  // 工单详情有 id 时请求详情路径;id 为空时不请求。
  it('useSkillTicketDetailQuery 按 id 控制请求', async () => {
    apiRequestMock.mockResolvedValueOnce({ ticket: { id: 't-1', status: 'pending' } })
    const id = ref<string | undefined>('t-1')
    mountWithQueryClient(() => {
      useSkillTicketDetailQuery(id)
    })
    await vi.waitFor(() => expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1'))

    vi.clearAllMocks()
    const emptyID = ref<string | undefined>(undefined)
    mountWithQueryClient(() => {
      useSkillTicketDetailQuery(emptyID)
    })
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(apiRequestMock).not.toHaveBeenCalled()
  })

  // 提交工单成功后只需刷新“我的工单”列表。
  it('useSubmitSkillTicket POST 到提交路径并 invalidate 我的列表', async () => {
    apiRequestMock.mockResolvedValueOnce({ ticket: { id: 't-1', status: 'pending', title: '新需求' } })
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ submit: useSubmitSkillTicket() }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { submit: ReturnType<typeof useSubmitSkillTicket> }
    ).submit.mutateAsync({ title: '新需求', description: '请做一个技能' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets', {
      method: 'POST',
      body: { title: '新需求', description: '请做一个技能' },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _mineKey() })
  })

  // 发文本消息可能触发工单重开,因此成功后刷新详情、双方列表和角标。
  it('useSendTicketMessage invalidates detail/admin/mine/badge', async () => {
    apiRequestMock.mockResolvedValueOnce({ message: { id: 'm-1', kind: 'text', text: '补充' } })
    const id = ref<string | undefined>('t-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ send: useSendTicketMessage(id) }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { send: ReturnType<typeof useSendTicketMessage> }
    ).send.mutateAsync({ text: '补充' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/messages', {
      method: 'POST',
      body: { text: '补充' },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _mineKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // 上传图片/文件消息走 multipart,成功后刷新和文本消息相同的四类缓存。
  it('useUploadTicketMessage 走 xhrUpload 并刷新消息相关缓存', async () => {
    xhrUploadMock.mockResolvedValueOnce({ status: 201, body: { message: { id: 'm-2', kind: 'file', file_name: 'spec.pdf' } } })
    const id = ref<string | undefined>('t-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ upload: useUploadTicketMessage(id) }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const file = new File(['pdf'], 'spec.pdf', { type: 'application/pdf' })
    await (
      wrapper.vm as unknown as { upload: ReturnType<typeof useUploadTicketMessage> }
    ).upload.mutateAsync(file)

    expect(xhrUploadMock).toHaveBeenCalledWith(
      '/api/v1/skill-tickets/t-1/messages/upload',
      expect.objectContaining({ method: 'POST' }),
    )
    expect(xhrUploadMock.mock.calls[0][1].body).toBeInstanceOf(FormData)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _mineKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // 下载文件消息用鉴权下载端点,并按消息文件名兜底保存。
  it('downloadTicketMessage 调用消息下载路径并触发浏览器下载', async () => {
    const blob = new Blob(['pdf'], { type: 'application/pdf' })
    apiDownloadMock.mockResolvedValueOnce({ blob, filename: null })
    const createObjectURL = vi.fn().mockReturnValue('blob:mock')
    const revokeObjectURL = vi.fn()
    globalThis.URL.createObjectURL = createObjectURL
    globalThis.URL.revokeObjectURL = revokeObjectURL
    const clickSpy = vi.fn()
    const removeSpy = vi.fn()
    vi.spyOn(document, 'createElement').mockReturnValueOnce({ href: '', download: '', click: clickSpy, remove: removeSpy } as unknown as HTMLElement)
    vi.spyOn(document.body, 'appendChild').mockImplementationOnce(vi.fn())

    await downloadTicketMessage('t-1', { id: 'm-1', kind: 'file', file_name: 'spec.pdf' })

    expect(apiDownloadMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/messages/m-1/download')
    expect(clickSpy).toHaveBeenCalled()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock')
  })

  // 图片预览通过 apiDownload 转 objectURL,调用方负责在组件卸载时 revoke。
  it('fetchTicketMessageBlobUrl 返回鉴权下载后的 objectURL', async () => {
    const blob = new Blob(['png'], { type: 'image/png' })
    apiDownloadMock.mockResolvedValueOnce({ blob, filename: 'pic.png' })
    const createObjectURL = vi.fn().mockReturnValue('blob:image')
    globalThis.URL.createObjectURL = createObjectURL

    const url = await fetchTicketMessageBlobUrl('t-1', { id: 'm-img', kind: 'image' })

    expect(apiDownloadMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/messages/m-img/download')
    expect(url).toBe('blob:image')
  })

  // 开始制作成功后刷新详情、管理员列表和角标。
  it('useStartTicket invalidates detail/admin/badge', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ start: useStartTicket() }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { start: ReturnType<typeof useStartTicket> }
    ).start.mutateAsync({ id: 't-1' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/start', { method: 'POST' })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // 重新受理成功后刷新详情、管理员列表和角标。
  it('useReopenTicket invalidates detail/admin/badge', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ reopen: useReopenTicket() }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { reopen: ReturnType<typeof useReopenTicket> }
    ).reopen.mutateAsync({ id: 't-1' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/reopen', { method: 'POST' })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // 报价和拒绝仍走原端点,但由详情页动作触发。
  it('useSetSkillTicketQuote/useRejectSkillTicket invalidates admin caches', async () => {
    apiRequestMock.mockResolvedValue(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      quote: useSetSkillTicketQuote(),
      reject: useRejectSkillTicket(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (
      wrapper.vm as unknown as { quote: ReturnType<typeof useSetSkillTicketQuote> }
    ).quote.mutateAsync({ id: 't-1', quote_amount_cents: 50000 })
    await (
      wrapper.vm as unknown as { reject: ReturnType<typeof useRejectSkillTicket> }
    ).reject.mutateAsync({ id: 't-1', reason: '需求不清晰' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/quote', {
      method: 'PATCH',
      body: { quote_amount_cents: 50000 },
    })
    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/reject', {
      method: 'POST',
      body: { reason: '需求不清晰' },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
  })

  // 更新可见范围成功后刷新详情和市场缓存,让交付后可见性立即反映到市场。
  it('useUpdateTicketTargets invalidates detail + market', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ updateTargets: useUpdateTicketTargets() }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const targets = [{ org_id: 'org-1', audience: 'org_admins' }]

    await (
      wrapper.vm as unknown as { updateTargets: ReturnType<typeof useUpdateTicketTargets> }
    ).updateTargets.mutateAsync({ id: 't-1', targets })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-tickets/t-1/targets', {
      method: 'PATCH',
      body: { targets },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['skills', 'market'] })
  })

  // 交付成功后刷新工单缓存和市场缓存。
  it('useDeliverCustomSkill invalidates ticket + market caches', async () => {
    const skill = { id: 'cs-1', name: 'my-skill', version: '20260610120000', ticket_id: 't-1' }
    xhrUploadMock.mockResolvedValueOnce({ status: 201, body: { skill } })
    const { queryClient, wrapper } = mountWithQueryClient(() => ({ deliver: useDeliverCustomSkill() }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const file = new File(['tar'], 'my-skill.tar', { type: 'application/x-tar' })
    const targets = [{ org_id: 'org-1', audience: 'all_org' }]

    await (
      wrapper.vm as unknown as { deliver: ReturnType<typeof useDeliverCustomSkill> }
    ).deliver.mutateAsync({ ticketId: 't-1', description: '描述', targets, file })

    expect(xhrUploadMock).toHaveBeenCalledWith('/api/v1/custom-skills/deliver', expect.objectContaining({ method: 'POST' }))
    const formData = xhrUploadMock.mock.calls[0][1].body as FormData
    expect(formData.get('ticket_id')).toBe('t-1')
    expect(formData.get('targets')).toBe(JSON.stringify(targets))
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _detailKey('t-1') })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _adminKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _badgeKey() })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['skills', 'market'] })
  })
})
