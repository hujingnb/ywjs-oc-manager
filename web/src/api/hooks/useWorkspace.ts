// 工作区 API hook 和下载工具负责读取应用运行目录以及触发浏览器下载。
// 文件下载不是 JSON API，因此通过带 Authorization 的 fetch 获取 Blob。
import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest, getStoredAccessToken } from '@/api/client'

// WorkspaceEntry 是应用工作目录中的文件或目录。
export interface WorkspaceEntry {
  // 相对工作目录根的完整路径。
  path: string
  // 当前层级展示名。
  name: string
  // 文件大小字节数；目录可为 0。
  size: number
  // true 表示目录，false 表示普通文件。
  is_dir: boolean
}

// WorkspaceListing 是某个工作目录路径下的列表响应。
export interface WorkspaceListing {
  // 当前目录相对路径。
  path: string
  // 当前目录的直接子项。
  entries: WorkspaceEntry[]
}

// useWorkspaceQuery 列出应用工作目录。
// appId 缺失时暂停；relative 由调用方维护并直接传给后端 path 参数。
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

// downloadBlob 负责把受保护接口返回的 Blob 转成浏览器下载。
// 调用方必须保证运行在浏览器环境；该工具会临时创建并移除 a 标签。
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
// targetPath 必须是后端认可的工作区相对路径，fileName 仅影响浏览器保存名。
export function downloadWorkspaceFile(appId: string, targetPath: string, fileName: string): Promise<void> {
  const params = new URLSearchParams({ path: targetPath })
  return downloadBlob(`/api/v1/apps/${appId}/workspace/file?${params.toString()}`, fileName)
}

// archiveWorkspace 下载工作目录 zip 归档。
// targetPath 可指向目录或文件，后端负责打包边界和路径校验。
export function archiveWorkspace(appId: string, targetPath: string): Promise<void> {
  const params = new URLSearchParams({ path: targetPath })
  return downloadBlob(`/api/v1/apps/${appId}/workspace/archive?${params.toString()}`, 'workspace.zip')
}
