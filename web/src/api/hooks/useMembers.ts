// 成员 API hooks 负责组织成员列表、创建、删除、状态变更、密码重置和一键开户。
// 缓存边界以组织成员列表为主，涉及应用初始化的副作用由后端 job 和页面跳转处理。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { Member } from '@/api'

// OnboardMemberPayload 是“一键创建成员和应用”表单提交体。
export interface OnboardMemberPayload {
  // 登录用户名，后端会校验组织内唯一性。
  username: string
  // 页面展示名。
  display_name: string
  // 初始密码，仅提交给后端，不在前端持久化。
  password: string
  // 新成员角色；缺省由后端按组织成员处理。
  role?: 'org_admin' | 'org_member'
  // 同步创建的应用名称。
  app_name: string
  // 应用级提示词，persona_mode=app_override 时生效。
  app_prompt?: string
  // 人设继承模式，缺省时后端使用组织默认规则。
  persona_mode?: 'org_inherited' | 'app_override'
  // 首次绑定的渠道类型，目前仅支持 wechat。
  channel_type?: 'wechat'
  // 指定 runtime 节点；为空时后端按调度策略选择。
  runtime_node_id?: string
}

// OnboardMemberResult 是一键开户的业务结果。
export interface OnboardMemberResult {
  // 新创建的成员。
  member: Member
  // 同步创建的应用摘要。
  app: {
    // 应用 ID，用于跳转详情页。
    id: string
    // 应用名称。
    name: string
    // 初始化后的应用状态。
    status: string
    // 应用人设模式。
    persona_mode: string
    // new-api token 绑定状态。
    api_key_status: string
  }
  // 初始化 job ID，页面可用它提示后台进度。
  job_id: string
}

const memberListKey = (orgId: string | undefined) => ['members', orgId] as const

// MemberFormPayload 是普通创建成员表单提交体。
export interface MemberFormPayload {
  // 登录用户名。
  username: string
  // 展示名。
  display_name: string
  // 初始密码。
  password: string
  // 成员角色，组织管理员页面可选择。
  role?: 'org_admin' | 'org_member'
}

// useMembersQuery 列出指定组织的成员。
// orgId 为响应式引用，便于在组织详情/切换场景下自动重查。
// orgId 为空时暂停请求，避免成员页初始化时打到无效组织路径。
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
// 成功后只失效当前组织成员列表；新成员详情没有单独缓存。
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

// useDeleteMember 删除成员并联动其名下应用。
// 后端会软删 user.status=disabled、入队 app_delete 任务，前端只需 invalidate 列表。
export function useDeleteMember(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (userId: string) => {
      await apiRequest<void>(`/api/v1/members/${userId}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: memberListKey(orgId.value) })
    },
  })
}

// useSetMemberStatus 启用或禁用成员。
// 后端会维护 users.deleted_at 的下线语义，前端只刷新成员列表。
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

// useOnboardMember 在事务里创建成员、应用、渠道绑定与初始化任务。
// 成功后刷新成员列表；应用列表刷新由跳转后的应用页自行加载。
export function useOnboardMember(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: OnboardMemberPayload) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const response = await apiRequest<{ onboarding: { member: Member; app: OnboardMemberResult['app']; job_id: string } }>(
        `/api/v1/organizations/${orgId.value}/members/onboard`,
        { method: 'POST', body: payload },
      )
      return response.onboarding
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: memberListKey(orgId.value) })
    },
  })
}

// useResetMemberPassword 由管理员强制重置成员密码。
// 密码重置不改变成员列表展示字段，因此不主动失效缓存。
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
