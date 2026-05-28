// 审计日志 API hooks 负责企业维度和目标资源维度的只读查询。
// 本文件不做权限判断；路由和页面根据角色决定是否启用对应 query。
import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { AuditLog } from '@/api'

// useOrgAuditLogsQuery 按企业维度查询审计日志。
// 平台管理员可任意查询，企业角色仅能查询自己的企业。
// orgId 为空时暂停，缓存键带 orgId 以隔离平台管理员切换企业的结果。
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
// target 缺失时暂停；targetType/targetId 由页面保证对应后端审计 target。
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
