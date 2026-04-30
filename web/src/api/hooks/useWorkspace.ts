import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest, getStoredAccessToken } from '@/api/client'

export interface WorkspaceEntry {
  path: string
  name: string
  size: number
  is_dir: boolean
}

export interface WorkspaceListing {
  path: string
  entries: WorkspaceEntry[]
}

// useWorkspaceQuery 列出应用工作目录。
export function useWorkspaceQuery(appId: Ref<string | undefined>, relative: Ref<string>) {
  return useQuery<WorkspaceListing | null>({
    queryKey: ['workspace', appId, relative],
    enabled: () => Boolean(appId.value),
    queryFn: async () => {
      if (!appId.value) return null
      return apiRequest<WorkspaceListing>(`/api/v1/apps/${appId.value}/workspace`, {
        query: { path: relative.value },
      })
    },
  })
}

// downloadWorkspaceFile 触发浏览器下载工作目录中的文件。
export function downloadWorkspaceFile(appId: string, targetPath: string): string {
  const token = getStoredAccessToken()
  const params = new URLSearchParams({ path: targetPath })
  if (token) params.append('access_token', token)
  return `/api/v1/apps/${appId}/workspace/file?${params.toString()}`
}

// archiveWorkspace 返回打包下载的 URL。
export function archiveWorkspace(appId: string, targetPath: string): string {
  const params = new URLSearchParams({ path: targetPath })
  return `/api/v1/apps/${appId}/workspace/archive?${params.toString()}`
}
