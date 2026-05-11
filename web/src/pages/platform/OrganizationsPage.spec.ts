import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import OrganizationsPage from './OrganizationsPage.vue'

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref([{
      id: 'org-1',
      name: '测试组织',
      status: 'active',
      credit_warning_threshold: 20,
    }]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useCreateOrganization: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useUpdateOrganizationStatus: () => ({ mutate: vi.fn() }),
}))

vi.mock('@/api/hooks/useRecharge', () => ({
  useOrgBalanceQuery: () => ({
    data: ref({ newapi_user_id: 4, remain_quota: 0, used_quota: 0 }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useRechargeMutation: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
}))

describe('OrganizationsPage', () => {
  it('在组织列表中提供弹框充值入口', () => {
    const wrapper = mount(OrganizationsPage)

    expect(wrapper.text()).toContain('充值')
    expect(wrapper.text()).not.toContain('返回组织列表')
  })
})
