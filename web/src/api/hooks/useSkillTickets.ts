// useSkillTickets.ts — 定制技能工单、统一消息、状态动作与交付相关 API hooks。
// 成员侧：提交工单、查看我的工单、发送 text/image/file 消息。
// 管理员侧：队列、开始制作、拒绝、重新受理、报价、交付、编辑可见范围。
import { computed, type Ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { apiDownload, apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import type {
  CustomSkillResult,
  SkillTicket,
  SkillTicketDetail,
  SkillTicketMessage,
} from '@/api'

// ===== 缓存键 =====
const mineKey = () => ['skill-tickets', 'mine'] as const
const adminKey = () => ['skill-tickets', 'admin'] as const
const badgeKey = () => ['skill-tickets', 'badge'] as const
const detailKey = (id: string | undefined) => ['skill-tickets', 'detail', id] as const

// 导出缓存键函数供单测和相邻模块复用,避免字符串拼写分歧。
export const _mineKey = mineKey
export const _adminKey = adminKey
export const _badgeKey = badgeKey
export const _detailKey = detailKey

// invalidateAdmin 刷新管理员动作影响的详情、队列和角标。
function invalidateAdmin(client: ReturnType<typeof useQueryClient>, id: string) {
  void client.invalidateQueries({ queryKey: detailKey(id) })
  void client.invalidateQueries({ queryKey: adminKey() })
  void client.invalidateQueries({ queryKey: badgeKey() })
}

// invalidateAll 刷新消息可能影响的双方列表;需求方关闭态补充消息会触发自动重开。
function invalidateAll(client: ReturnType<typeof useQueryClient>, id: string) {
  invalidateAdmin(client, id)
  void client.invalidateQueries({ queryKey: mineKey() })
}

// ===== 查询 =====

// useMySkillTicketsQuery 拉取当前用户提交的工单列表。
export function useMySkillTicketsQuery() {
  return useQuery<SkillTicket[]>({
    queryKey: mineKey(),
    queryFn: async () =>
      (await apiRequest<{ tickets: SkillTicket[] }>('/api/v1/skill-tickets')).tickets ?? [],
  })
}

// useAdminSkillTicketsQuery 拉取平台管理员工单队列。
export function useAdminSkillTicketsQuery() {
  return useQuery<SkillTicket[]>({
    queryKey: adminKey(),
    queryFn: async () =>
      (await apiRequest<{ tickets: SkillTicket[] }>('/api/v1/admin/skill-tickets')).tickets ?? [],
  })
}

// useSkillTicketBadgeQuery 拉取 pending 工单角标,供管理员菜单展示。
export function useSkillTicketBadgeQuery(enabled: Ref<boolean> | boolean = true) {
  return useQuery<number>({
    queryKey: badgeKey(),
    // 角标端点是 admin-only；非平台管理员布局会挂载本 hook,但必须保持禁用以避免 403。
    enabled: () => typeof enabled === 'boolean' ? enabled : enabled.value,
    refetchInterval: 60_000,
    queryFn: async () =>
      (await apiRequest<{ pending: number }>('/api/v1/admin/skill-tickets/badge')).pending ?? 0,
  })
}

// useSkillTicketDetailQuery 拉取工单详情,返回中包含 messages 判别联合。
export function useSkillTicketDetailQuery(id: Ref<string | undefined>) {
  return useQuery<SkillTicketDetail>({
    queryKey: computed(() => detailKey(id.value)),
    enabled: () => Boolean(id.value),
    queryFn: async () =>
      (await apiRequest<{ ticket: SkillTicketDetail }>(`/api/v1/skill-tickets/${id.value}`)).ticket,
  })
}

// ===== 提交 / 消息 =====

// useSubmitSkillTicket 提交新工单,成功后刷新“我的工单”列表。
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

// useSendTicketMessage 发送文本消息;成功后刷新双方列表和详情。
export function useSendTicketMessage(id: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { text: string }) =>
      (
        await apiRequest<{ message: SkillTicketMessage }>(
          `/api/v1/skill-tickets/${id.value}/messages`,
          { method: 'POST', body: input },
        )
      ).message,
    onSuccess: () => {
      if (id.value) invalidateAll(client, id.value)
    },
  })
}

// useUploadTicketMessage 上传图片或文件消息;后端按 content_type 判定 image/file kind。
export function useUploadTicketMessage(id: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (file: File) => {
      const form = new FormData()
      form.append('file', file)
      const resp = await xhrUpload(`/api/v1/skill-tickets/${id.value}/messages/upload`, {
        method: 'POST',
        body: form,
      })
      return (resp.body as { message: SkillTicketMessage }).message
    },
    onSuccess: () => {
      if (id.value) invalidateAll(client, id.value)
    },
  })
}

// downloadTicketMessage 通过鉴权端点下载 image/file 消息,并触发浏览器保存。
export async function downloadTicketMessage(
  ticketId: string,
  msg: SkillTicketMessage,
): Promise<void> {
  const { blob, filename } = await apiDownload(
    `/api/v1/skill-tickets/${ticketId}/messages/${msg.id}/download`,
  )
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename ?? msg.file_name ?? 'download'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// fetchTicketMessageBlobUrl 下载图片消息并转为 objectURL;调用方负责在组件卸载时 revoke。
export async function fetchTicketMessageBlobUrl(
  ticketId: string,
  msg: SkillTicketMessage,
): Promise<string> {
  const { blob } = await apiDownload(`/api/v1/skill-tickets/${ticketId}/messages/${msg.id}/download`)
  return URL.createObjectURL(blob)
}

// ===== 管理员动作 =====

// useStartTicket 开始制作:pending → processing。
export function useStartTicket() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string }) =>
      apiRequest<void>(`/api/v1/skill-tickets/${input.id}/start`, { method: 'POST' }),
    onSuccess: (_d, v) => invalidateAdmin(client, v.id),
  })
}

// useReopenTicket 重新受理:rejected → processing。
export function useReopenTicket() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string }) =>
      apiRequest<void>(`/api/v1/skill-tickets/${input.id}/reopen`, { method: 'POST' }),
    onSuccess: (_d, v) => invalidateAdmin(client, v.id),
  })
}

// useSetSkillTicketQuote 设置报价(分);后端仅允许 pending/processing。
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

// useRejectSkillTicket 拒绝 pending/processing 工单并记录原因。
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

// DeliverTarget 描述单条可见范围:目标企业 ID + 受众范围。
export interface DeliverTarget {
  // org_id 是目标企业 UUID。
  org_id: string
  // audience 是可见范围:all_org / org_admins / requester_only。
  audience: string
}

// useUpdateTicketTargets 编辑已交付定制技能的可见范围。
export function useUpdateTicketTargets() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { id: string; targets: DeliverTarget[] }) =>
      apiRequest<void>(`/api/v1/skill-tickets/${input.id}/targets`, {
        method: 'PATCH',
        body: { targets: input.targets },
      }),
    onSuccess: (_d, v) => {
      void client.invalidateQueries({ queryKey: detailKey(v.id) })
      void client.invalidateQueries({ queryKey: ['skills', 'market'] })
    },
  })
}

// useDeliverCustomSkill 交付定制技能归档;成功后刷新工单详情/队列/角标与市场缓存。
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
      form.append('targets', JSON.stringify(input.targets))
      form.append('file', input.file)
      const resp = await xhrUpload('/api/v1/custom-skills/deliver', {
        method: 'POST',
        body: form,
      })
      return (resp.body as { skill: CustomSkillResult }).skill
    },
    onSuccess: (_d, v) => {
      invalidateAdmin(client, v.ticketId)
      void client.invalidateQueries({ queryKey: ['skills', 'market'] })
    },
  })
}
