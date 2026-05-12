import { mount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppOverviewTab from './AppOverviewTab.vue'

const organizationName = ref<string | undefined>('测试组织')

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_admin',
    },
  }),
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => ({
    data: computed(() => organizationName.value
      ? { id: '00000000-0000-0000-0000-000000000101', name: organizationName.value, status: 'active' }
      : null),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/api/hooks/useApps', () => ({
  useInitializeAppMutation: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
  useJobQuery: () => ({
    data: ref(null),
  }),
  useToggleAppAPIKey: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
}))

const appRef = ref({
  id: '00000000-0000-0000-0000-000000000001',
  org_id: '00000000-0000-0000-0000-000000000101',
  owner_user_id: '00000000-0000-0000-0000-000000000201',
  name: '测试应用',
  status: 'running',
  persona_mode: 'org_inherited',
  api_key_status: 'active',
})

function mountOverview() {
  return mount(AppOverviewTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: appRef },
      stubs: {
        AppStatusTag: { template: '<span />' },
        ConfirmActionModal: true,
        JobProgressPanel: true,
        NButton: { template: '<button><slot /></button>' },
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NDescriptions: { template: '<dl><slot /></dl>' },
        NDescriptionsItem: { props: ['label'], template: '<div><dt>{{ label }}</dt><dd><slot /></dd></div>' },
        NSpace: { template: '<span><slot /></span>' },
        NTag: { template: '<span><slot /></span>' },
      },
    },
  })
}

describe('AppOverviewTab', () => {
  it('所属组织展示组织名称而不是组织 UUID', () => {
    organizationName.value = '测试组织'

    const wrapper = mountOverview()

    expect(wrapper.text()).toContain('测试组织')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000101')
  })

  it('组织名称缺失时展示友好兜底文案', () => {
    organizationName.value = undefined

    const wrapper = mountOverview()

    expect(wrapper.text()).toContain('未知组织')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000101')
  })
})
