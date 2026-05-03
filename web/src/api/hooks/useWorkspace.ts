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

async function downloadBlob(url: string, fileName: string) {
  const headers: Record<string, string> = {}
  const token = getStoredAccessToken()
  if (token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(url, { headers })
  if (!response.ok) {
    const text = await response.text().catch(() => '')
    throw new Error(text || '下载失败')
  }
  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = fileName
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectUrl)
}

// downloadWorkspaceFile 触发浏览器下载工作目录中的文件。
export function downloadWorkspaceFile(appId: string, targetPath: string, fileName: string): Promise<void> {
  const params = new URLSearchParams({ path: targetPath })
  return downloadBlob(`/api/v1/apps/${appId}/workspace/file?${params.toString()}`, fileName)
}

// archiveWorkspace 下载工作目录 zip 归档。
export function archiveWorkspace(appId: string, targetPath: string): Promise<void> {
  const params = new URLSearchParams({ path: targetPath })
  return downloadBlob(`/api/v1/apps/${appId}/workspace/archive?${params.toString()}`, 'workspace.zip')
}
