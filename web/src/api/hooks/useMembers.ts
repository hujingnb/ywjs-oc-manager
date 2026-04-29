import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { Member } from '@/api/types'

const memberListKey = (orgId: string | undefined) => ['members', orgId] as const

export interface MemberFormPayload {
  username: string
  display_name: string
  password: string
  role?: 'org_admin' | 'org_member'
}

// useMembersQuery 列出指定组织的成员。
// orgId 为响应式引用，便于在组织详情/切换场景下自动重查。
export function useMembersQuery(orgId: Ref<string | undefined>) {
  return useQuery<Member[]>({
    queryKey: ['members', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return []
      const response = await apiRequest<{ members: Member[] }>(
        `/api/v1/organizations/${orgId.value}/members`,
        { query: { limit: 200 } },
      )
      return response.members
    },
  })
}

// useCreateMember 创建组织成员。
export function useCreateMember(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: MemberFormPayload) => {
      if (!orgId.value) {
        throw new Error('缺少组织 ID')
      }
      const response = await apiRequest<{ member: Member }>(
        `/api/v1/organizations/${orgId.value}/members`,
        { method: 'POST', body: payload },
      )
      return response.member
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: memberListKey(orgId.value) })
    },
  })
}

// useSetMemberStatus 启用或禁用成员。
export function useSetMemberStatus(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ userId, action }: { userId: string; action: 'enable' | 'disable' }) => {
      const response = await apiRequest<{ member: Member }>(`/api/v1/members/${userId}/${action}`, {
        method: 'POST',
      })
      return response.member
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: memberListKey(orgId.value) })
    },
  })
}

// useResetMemberPassword 由管理员强制重置成员密码。
export function useResetMemberPassword() {
  return useMutation({
    mutationFn: async ({ userId, password }: { userId: string; password: string }) => {
      await apiRequest<void>(`/api/v1/members/${userId}/password`, {
        method: 'POST',
        body: { password },
      })
    },
  })
}
