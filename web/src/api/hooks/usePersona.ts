import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

export interface PersonaDTO {
  org_id: string
  system_prompt: string
  conversation_rules?: string
  forbidden_rules?: string
  reply_style?: string
  allow_member_override: boolean
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
