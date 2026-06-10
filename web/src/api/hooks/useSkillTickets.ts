// useSkillTickets.ts — 定制技能工单/附件/交付相关 API hooks。
// 成员侧：提交工单、查看我的工单列表、工单详情、附件上传/下载、对话评论。
// 管理员侧：工单队列、状态变更、报价、拒绝、交付（multipart 上传技能 tar）。
// 查询走 apiRequest；multipart 上传走 xhrUpload 支持进度感知；附件带鉴权下载走 apiDownload。
import { computed, type Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'

import { apiDownload, apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import type {
  SkillTicket,
  SkillTicketDetail,
  SkillTicketComment,
  SkillTicketAttachment,
  CustomSkillResult,
} from '@/api'

// ===== 缓存键 =====
// mineKey 是当前用户提交的工单列表缓存键。
const mineKey = () => ['skill-tickets', 'mine'] as const
// adminKey 是管理员工单队列缓存键。
const adminKey = () => ['skill-tickets', 'admin'] as const
// badgeKey 是管理员菜单待处理角标计数缓存键（定期自动刷新）。
const badgeKey = () => ['skill-tickets', 'badge'] as const
// detailKey 是指定工单详情缓存键，含评论列表。
const detailKey = (id: string | undefined) => ['skill-tickets', 'detail', id] as const
// attachKey 是指定工单附件列表缓存键。
const attachKey = (id: string | undefined) => ['skill-tickets', 'attachments', id] as const

// 导出缓存键函数，供单测断言 invalidate 时复用，避免拼写分歧。
export const _mineKey = mineKey
export const _adminKey = adminKey
export const _badgeKey = badgeKey
export const _detailKey = detailKey
export const _attachKey = attachKey

// ===== 查询 =====

// useMySkillTicketsQuery 拉取当前登录用户提交的所有工单（GET /api/v1/skill-tickets）。
export function useMySkillTicketsQuery() {
  return useQuery<SkillTicket[]>({
    queryKey: mineKey(),
    queryFn: async () =>
      (await apiRequest<{ tickets: SkillTicket[] }>('/api/v1/skill-tickets')).tickets ?? [],
  })
}

// useAdminSkillTicketsQuery 拉取所有工单（仅平台管理员，GET /api/v1/admin/skill-tickets）。
export function useAdminSkillTicketsQuery() {
  return useQuery<SkillTicket[]>({
    queryKey: adminKey(),
    queryFn: async () =>
      (await apiRequest<{ tickets: SkillTicket[] }>('/api/v1/admin/skill-tickets')).tickets ?? [],
  })
}

// useSkillTicketBadgeQuery 拉取待处理工单计数，供管理员菜单角标展示（GET /api/v1/admin/skill-tickets/badge）。
// 每分钟自动刷新，确保角标数与实际 pending 工单数保持一致。
export function useSkillTicketBadgeQuery() {
  return useQuery<number>({
    queryKey: badgeKey(),
    refetchInterval: 60_000,
    queryFn: async () =>
      (await apiRequest<{ pending: number }>('/api/v1/admin/skill-tickets/badge')).pending ?? 0,
  })
}

// useSkillTicketDetailQuery 拉取指定工单详情含评论（GET /api/v1/skill-tickets/:id）。
// id 为 undefined 时不发请求（enabled 为 false）。
export function useSkillTicketDetailQuery(id: Ref<string | undefined>) {
  return useQuery<SkillTicketDetail>({
    queryKey: computed(() => detailKey(id.value)),
    enabled: () => Boolean(id.value),
    queryFn: async () =>
      (await apiRequest<{ ticket: SkillTicketDetail }>(`/api/v1/skill-tickets/${id.value}`)).ticket,
  })
}

// useSkillTicketAttachmentsQuery 拉取指定工单的附件列表（GET /api/v1/skill-tickets/:id/attachments）。
// id 为 undefined 时不发请求。
export function useSkillTicketAttachmentsQuery(id: Ref<string | undefined>) {
  return useQuery<SkillTicketAttachment[]>({
    queryKey: computed(() => attachKey(id.value)),
    enabled: () => Boolean(id.value),
    queryFn: async () =>
      (
        await apiRequest<{ attachments: SkillTicketAttachment[] }>(
          `/api/v1/skill-tickets/${id.value}/attachments`,
        )
      ).attachments ?? [],
  })
}

// ===== 提交 / 评论（成员侧）=====

// useSubmitSkillTicket 提交新工单（POST /api/v1/skill-tickets）。
// 成功后 invalidate 我的工单列表，使列表自动刷新。
export function useSubmitSkillTicket() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { title: string; description: string }) =>
      (
        await apiRequest<{ ticket: SkillTicket }>('/api/v1/skill-tickets', {
          method: 'POST',
          body: input,
        })
      ).ticket,
    onSuccess: () => void client.invalidateQueries({ queryKey: mineKey() }),
  })
}

// useAddSkillTicketComment 向指定工单追加评论（POST /api/v1/skill-tickets/:id/comments）。
// 成员在 delivered/rejected 状态下发评论会触发后端重开（back to pending）逻辑，
// 因此 onSuccess 同时 invalidate 详情、我的列表、管理员列表和角标，使状态变更即时反映到所有视图。
export function useAddSkillTicketComment(id: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { body: string }) =>
      (
        await apiRequest<{ comment: SkillTicketComment }>(
          `/api/v1/skill-tickets/${id.value}/comments`,
          { method: 'POST', body: input },
        )
      ).comment,
    onSuccess: () => {
      // 评论可能触发工单重开（状态 delivered/rejected → pending），
      // 需同时刷新详情（状态徽章）、我的列表、管理员列表和待处理角标计数。
      void client.invalidateQueries({ queryKey: detailKey(id.value) })
      void client.invalidateQueries({ queryKey: mineKey() })
      void client.invalidateQueries({ queryKey: adminKey() })
      void client.invalidateQueries({ queryKey: badgeKey() })
    },
  })
}

// ===== 附件（成员侧/管理员侧）=====

// useUploadSkillTicketAttachment 向指定工单上传附件（POST /api/v1/skill-tickets/:id/attachments，multipart）。
// 走 xhrUpload 发送 FormData；成功后 invalidate 附件列表。
export function useUploadSkillTicketAttachment(id: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (file: File) => {
      const form = new FormData()
      form.append('file', file)
      const resp = await xhrUpload(`/api/v1/skill-tickets/${id.value}/attachments`, {
        method: 'POST',
        body: form,
      })
      return (resp.body as { attachment: SkillTicketAttachment }).attachment
    },
    onSuccess: () => void client.invalidateQueries({ queryKey: attachKey(id.value) }),
  })
}

// downloadSkillTicketAttachment 带鉴权下载附件，触发浏览器保存（GET /api/v1/skill-tickets/:ticketId/attachments/:id/download）。
// 下载端点需 Bearer token，不能用裸 <a href>；复用 apiDownload 处理 Authorization header、401 跳登录和 blob 转换。
// file_name 作为保存文件名（来自 SkillTicketAttachment.file_name，由 WithRequired 保证非 undefined）。
export async function downloadSkillTicketAttachment(
  ticketId: string,
  att: SkillTicketAttachment,
): Promise<void> {
  const { blob, filename } = await apiDownload(
    `/api/v1/skill-tickets/${ticketId}/attachments/${att.id}/download`,
  )
  // 优先使用 Content-Disposition 解析出的文件名，回退到附件记录中的 file_name。
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename ?? att.file_name
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// ===== 管理员处理 =====

// invalidateAdmin 是管理员操作（状态变更/报价/拒绝/交付）后统一 invalidate 的缓存键组合：
// 详情（含评论）、管理员列表、待处理角标计数。
function invalidateAdmin(client: ReturnType<typeof useQueryClient>, id: string) {
  void client.invalidateQueries({ queryKey: detailKey(id) })
  void client.invalidateQueries({ queryKey: adminKey() })
  void client.invalidateQueries({ queryKey: badgeKey() })
}

// useUpdateSkillTicketStatus 更改工单状态（PATCH /api/v1/skill-tickets/:id/status）。
// 支持 pending → processing 等状态迁移；成功后刷新详情/列表/角标。
export function useUpdateSkillTicketStatus() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; status: string }) =>
      apiRequest<void>(`/api/v1/skill-tickets/${input.id}/status`, {
        method: 'PATCH',
        body: { status: input.status },
      }),
    onSuccess: (_d, v) => invalidateAdmin(client, v.id),
  })
}

// useSetSkillTicketQuote 设置工单报价（PATCH /api/v1/skill-tickets/:id/quote）。
// quote_amount_cents 单位为分，前端输入元后 *100 取整传入。
export function useSetSkillTicketQuote() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; quote_amount_cents: number }) =>
      apiRequest<void>(`/api/v1/skill-tickets/${input.id}/quote`, {
        method: 'PATCH',
        body: { quote_amount_cents: input.quote_amount_cents },
      }),
    onSuccess: (_d, v) => invalidateAdmin(client, v.id),
  })
}

// useRejectSkillTicket 拒绝工单并附原因（POST /api/v1/skill-tickets/:id/reject）。
// 成员在详情抽屉可看到 reject_reason，发评论可触发重开。
export function useRejectSkillTicket() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; reason: string }) =>
      apiRequest<void>(`/api/v1/skill-tickets/${input.id}/reject`, {
        method: 'POST',
        body: { reason: input.reason },
      }),
    onSuccess: (_d, v) => invalidateAdmin(client, v.id),
  })
}

// ===== 交付（管理员，multipart：ticket_id/description/targets(JSON)/file）=====

// DeliverTarget 描述单条交付目标：目标企业 ID + 受众范围。
export interface DeliverTarget {
  // org_id 是目标企业 UUID。
  org_id: string
  // audience 是可见范围：all_org（整企业）/ org_admins（仅管理员）/ requester_only（仅申请人）。
  audience: string
}

// useDeliverCustomSkill 交付定制技能（POST /api/v1/custom-skills/deliver，multipart）。
// 走 xhrUpload 发送 FormData，包含工单 ID、描述、目标范围 JSON 和 tar 包文件。
// 成功后刷新 detail/admin/badge（工单转 delivered）+ 市场缓存（使定制筛选卡片即时可见）。
export function useDeliverCustomSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      ticketId: string
      description: string
      targets: DeliverTarget[]
      file: File
    }) => {
      const form = new FormData()
      form.append('ticket_id', input.ticketId)
      form.append('description', input.description)
      // targets 是 JSON 数组，后端解析为 []DeliverTarget。
      form.append('targets', JSON.stringify(input.targets))
      form.append('file', input.file)
      const resp = await xhrUpload('/api/v1/custom-skills/deliver', {
        method: 'POST',
        body: form,
      })
      return (resp.body as { skill: CustomSkillResult }).skill
    },
    onSuccess: (_d, v) => {
      // 工单本身的详情/队列/角标需要刷新（工单状态变为 delivered）。
      invalidateAdmin(client, v.ticketId)
      // 市场缓存刷新，使定制筛选（source=custom）卡片即时出现。
      void client.invalidateQueries({ queryKey: ['skills', 'market'] })
    },
  })
}
