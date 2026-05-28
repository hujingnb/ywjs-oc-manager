// useMemberApp composable 单元测试
// 验证 org_member 能从组织实例列表中找到自己拥有的实例 ID。
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import { useMemberApp } from '../useMemberApp'

// 模拟 auth store：当前用户为 org_member，属于 org-1。
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 'user-1', org_id: 'org-1', role: 'org_member' },
    isOrgMember: true,
  }),
}))

// 模拟 useAppsByOrgQuery：返回一个属于 user-1 的活跃实例。
vi.mock('@/api/hooks/useApps', () => ({
  useAppsByOrgQuery: () => ({
    data: ref([
      { id: 'app-1', org_id: 'org-1', owner_user_id: 'user-1', name: '我的实例', status: 'running' },
    ]),
    isLoading: ref(false),
  }),
}))

describe('useMemberApp', () => {
  // org_member 有实例时应返回 appId、hasApp 为 true、isLoading 为 false
  it('返回 org_member 拥有的实例 ID', () => {
    const { appId, hasApp, isLoading } = useMemberApp()
    expect(appId.value).toBe('app-1')
    expect(hasApp.value).toBe(true)
    expect(isLoading.value).toBe(false)
  })
})
