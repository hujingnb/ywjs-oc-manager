import { shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref, type Ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import UsagePage from '../UsagePage.vue'
import { useMembersQuery } from '@/api/hooks/useMembers'
import { useAuthStore } from '@/stores/auth'

const usageRefs = vi.hoisted(() => ({
  orgRef: undefined as Ref<string | undefined> | undefined,
  memberOrgRef: undefined as Ref<string | undefined> | undefined,
  memberRef: undefined as Ref<string | undefined> | undefined,
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({ data: ref([]) }),
}))

vi.mock('@/api/hooks/useMembers', () => ({
  useMembersQuery: vi.fn(() => ({ data: ref([]) })),
}))

vi.mock('@/api/hooks/useApps', () => ({
  useAppsByOrgQuery: () => ({ data: ref([]) }),
  useAppUsageQuery: () => ({ data: ref(null), isLoading: ref(false), error: ref(null) }),
}))

vi.mock('@/api/hooks/useRecharge', () => ({
  useBillingStatusQuery: () => ({ data: ref(null) }),
}))

vi.mock('@/api/hooks/useUsage', () => ({
  useOrgUsageQuery: (orgRef: Ref<string | undefined>) => {
    usageRefs.orgRef = orgRef
    return { data: ref(null), isLoading: ref(false), error: ref(null) }
  },
  useMemberUsageQuery: (orgRef: Ref<string | undefined>, memberRef: Ref<string | undefined>) => {
    usageRefs.memberOrgRef = orgRef
    usageRefs.memberRef = memberRef
    return { data: ref(null), isLoading: ref(false), error: ref(null) }
  },
  usePlatformUsageQuery: () => ({ data: ref(null), isLoading: ref(false), error: ref(null) }),
}))

describe('UsagePage role query refs', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageRefs.orgRef = undefined
    usageRefs.memberOrgRef = undefined
    usageRefs.memberRef = undefined
  })

  it('企业成员查询我的用量时保留当前企业 ID', () => {
    const auth = useAuthStore()
    auth.user = {
      id: 'user-1',
      org_id: 'org-1',
      username: 'member',
      display_name: '成员',
      role: 'org_member',
      status: 'active',
    }

    shallowMount(UsagePage, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: true,
          UsageSummary: true,
        },
      },
    })

    // 普通成员不能查询组织统计，但成员用量接口仍必须携带 org_id 做权限边界。
    expect(usageRefs.orgRef?.value).toBeUndefined()
    expect(usageRefs.memberOrgRef?.value).toBe('org-1')
    expect(usageRefs.memberRef?.value).toBe('user-1')
  })

  it('企业管理员继续使用所属企业查询企业维度', () => {
    const auth = useAuthStore()
    auth.user = {
      id: 'admin-1',
      org_id: 'org-1',
      username: 'admin',
      display_name: '管理员',
      role: 'org_admin',
      status: 'active',
    }

    shallowMount(UsagePage, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: true,
          UsageSummary: true,
        },
      },
    })

    expect(usageRefs.orgRef?.value).toBe('org-1')
    expect(usageRefs.memberOrgRef?.value).toBe('org-1')
  })
})

describe('UsagePage effective ID 消除跨企业残留', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageRefs.orgRef = undefined
    usageRefs.memberOrgRef = undefined
    usageRefs.memberRef = undefined
  })

  // effectiveMemberId 把"成员 ID 必须落在当前 members 列表里"作为硬约束，
  // 避免切换组织瞬间 vue-query 还以旧 memberId + 新 orgId 发查询。
  // 验证三个阶段：初始 auto-select → 列表清空（切组织瞬间）回退 undefined →
  // 新列表到位后重新 auto-select。
  it('memberRef 在选中成员不在当前 members 列表时解析为 undefined', async () => {
    const auth = useAuthStore()
    auth.user = {
      id: 'admin-1', org_id: 'org-A',
      username: 'admin', display_name: '平台管理员',
      role: 'platform_admin', status: 'active',
    }

    const membersRef = ref<{ id: string; username: string; display_name: string }[]>([
      { id: 'mem-A', username: 'a', display_name: 'A' },
    ])
    vi.mocked(useMembersQuery).mockReturnValue({ data: membersRef } as any)

    shallowMount(UsagePage, {
      global: { plugins: [i18n], stubs: { RouterLink: true, UsageSummary: true } },
    })

    // 阶段 1：初始 auto-select 之后 memberRef 等于 mem-A
    await new Promise((r) => setTimeout(r, 0))
    expect(usageRefs.memberRef?.value).toBe('mem-A')

    // 阶段 2：切换组织瞬间，旧 members 列表被替换为空数组
    membersRef.value = []
    await new Promise((r) => setTimeout(r, 0))
    expect(usageRefs.memberRef?.value).toBeUndefined()

    // 阶段 3：新 org 的列表到位（[B]），watch(members) auto-select 接管
    membersRef.value = [{ id: 'mem-B', username: 'b', display_name: 'B' }]
    await new Promise((r) => setTimeout(r, 0))
    expect(usageRefs.memberRef?.value).toBe('mem-B')
  })
})
