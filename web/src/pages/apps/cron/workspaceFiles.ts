// workspaceFiles.ts —— 从工作目录列表提取可选脚本文件名。
// 后端要求 script 是无路径单文件名，故只取根目录直接子项中的文件（is_dir=false）的 name。
import type { WorkspaceListing } from '@/api/hooks/useWorkspace'

// workspaceFileNames 返回根层文件名列表；空响应安全返回空数组。
export function workspaceFileNames(listing: WorkspaceListing | null | undefined): string[] {
  if (!listing) return []
  return listing.entries.filter((e) => !e.is_dir).map((e) => e.name)
}
