// workspaceFiles.spec.ts —— 工作目录根层文件名提取纯逻辑单测。
// 覆盖：只取文件不取目录、空列表、回填 basename（script 不允许带路径）。
import { describe, expect, it } from 'vitest'

import { workspaceFileNames } from './workspaceFiles'
import type { WorkspaceListing } from '@/api/hooks/useWorkspace'

describe('workspaceFileNames', () => {
  // 只保留 is_dir=false 的条目，目录被过滤。
  it('过滤掉目录只留文件', () => {
    const listing: WorkspaceListing = {
      path: '/',
      entries: [
        { path: 'daily.py', name: 'daily.py', size: 10, is_dir: false, mod_time: '' },
        { path: 'logs', name: 'logs', size: 0, is_dir: true, mod_time: '' },
      ],
    }
    expect(workspaceFileNames(listing)).toEqual(['daily.py'])
  })

  // null / 空列表返回空数组。
  it('空列表返回空', () => {
    expect(workspaceFileNames(null)).toEqual([])
  })
})
