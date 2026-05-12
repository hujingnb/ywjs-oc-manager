import { mount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppsPage from './AppsPage.vue'

// 平台管理员没有 auth.user.org_id，页面需要先从组织列表选择一个组织再拉实例列表。
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 'admin-1', role: 'platform_admin' },
  }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref([{ id: 'org-1', name: '测试组织', status: 'active' }]),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/api/hooks/useApps', () => ({
  useAppsByOrgQuery: (orgId: { value: string | undefined }) => ({
    data: computed(() => orgId.value === 'org-1'
      ? [{
          id: 'app-1',
          org_id: 'org-1',
          owner_user_id: 'member-1',
          name: '组织实例',
          status: 'running',
          persona_mode: 'org_inherited',
          api_key_status: 'active',
        }]
      : []),
    isLoading: ref(false),
  }),
}))

describe('AppsPage', () => {
  it('平台管理员默认使用第一个组织加载实例列表', () => {
    const wrapper = mount(AppsPage, {
      global: {
        stubs: {
          DataTableList: {
            props: ['data', 'errorMessage'],
            template: '<section><slot name="toolbar" /><p>{{ errorMessage }}</p><div v-for="row in data" :key="row.id">{{ row.name }}</div></section>',
          },
          ConfirmActionModal: true,
          NButton: true,
        },
      },
    })

    expect(wrapper.text()).toContain('组织实例')
    expect(wrapper.text()).not.toContain('当前账号未关联组织')
  })
})
