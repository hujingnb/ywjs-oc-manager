// 人设 API hooks 负责组织级 AI 人设读取与版本化写入。
// 应用是否允许覆盖由 persona 字段表达，页面层根据权限决定是否展示编辑入口。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

// PersonaDTO 是组织 AI 人设的前端视图。
export interface PersonaDTO {
  // 归属组织 ID。
  org_id: string
  // 系统提示词，是后端构造会话上下文的核心字段。
  system_prompt: string
  // 对话规则补充说明。
  conversation_rules?: string
  // 禁止行为或安全规则。
  forbidden_rules?: string
  // 回复风格描述。
  reply_style?: string
  // 是否允许成员应用覆盖组织默认人设。
  allow_member_override: boolean
  // 乐观展示用版本号，写入成功后后端递增。
  version: number
}

const personaKey = (orgId: string | undefined) => ['persona', orgId] as const

// usePersonaQuery 查询当前组织的 AI 人设。
// 后端在 ErrPersonaNotFound 时返回 404，这里把 null 视作"尚未配置"。
export function usePersonaQuery(orgId: Ref<string | undefined>) {
  return useQuery<PersonaDTO | null>({
    queryKey: ['persona', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return null
      try {
        const response = await apiRequest<{ persona: PersonaDTO }>(`/api/v1/orgs/${orgId.value}/persona`)
        return response.persona
      } catch (err: unknown) {
        if (err instanceof Error && err.message.includes('尚未配置')) return null
        throw err
      }
    },
  })
}

// usePersonaMutation 写入新版本人设。
// 成功后失效当前组织 persona 缓存，确保版本号和覆盖开关同步。
export function usePersonaMutation(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: Omit<PersonaDTO, 'org_id' | 'version'>) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const response = await apiRequest<{ persona: PersonaDTO }>(
        `/api/v1/orgs/${orgId.value}/persona`,
        { method: 'PUT', body: input },
      )
      return response.persona
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: personaKey(orgId.value) })
    },
  })
}
