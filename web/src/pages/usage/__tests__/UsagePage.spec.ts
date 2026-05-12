import { shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref, type Ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UsagePage from '../UsagePage.vue'
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
  useMembersQuery: () => ({ data: ref([]) }),
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

  it('组织成员查询我的用量时保留当前组织 ID', () => {
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

  it('组织管理员继续使用所属组织查询组织维度', () => {
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
