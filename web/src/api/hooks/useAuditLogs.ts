import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { AuditLog } from '@/api/types'

// useOrgAuditLogsQuery 按组织维度查询审计日志。
// 平台管理员可任意查询，组织角色仅能查询自己的组织。
export function useOrgAuditLogsQuery(orgId: Ref<string | undefined>) {
  return useQuery<AuditLog[]>({
    queryKey: ['audit-logs', 'org', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return []
      const response = await apiRequest<{ audit_logs: AuditLog[] }>(
        `/api/v1/organizations/${orgId.value}/audit-logs`,
        { query: { limit: 200 } },
      )
      return response.audit_logs
    },
  })
}

// useTargetAuditLogsQuery 按目标资源维度查询审计日志。
export function useTargetAuditLogsQuery(target: Ref<{ targetType: string; targetId: string } | undefined>) {
  return useQuery<AuditLog[]>({
    queryKey: ['audit-logs', 'target', target],
    enabled: () => Boolean(target.value),
    queryFn: async () => {
      if (!target.value) return []
      const response = await apiRequest<{ audit_logs: AuditLog[] }>('/api/v1/audit-logs', {
        query: {
          target_type: target.value.targetType,
          target_id: target.value.targetId,
          limit: 200,
        },
      })
      return response.audit_logs
    },
  })
}
