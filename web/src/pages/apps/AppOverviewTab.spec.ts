import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppOverviewTab from './AppOverviewTab.vue'

const organizationName = ref<string | undefined>('测试组织')
const updateModelMock = vi.hoisted(() => vi.fn(async () => ({
  app: {
    id: '00000000-0000-0000-0000-000000000001',
    org_id: '00000000-0000-0000-0000-000000000101',
    owner_user_id: '00000000-0000-0000-0000-000000000201',
    name: '测试实例',
    status: 'running',
    persona_mode: 'org_inherited',
    api_key_status: 'active',
    model_id: 'deepseek-r1:14b',
    container_id: 'container-1',
  },
  requires_restart: true,
  restart_job_id: 'job-restart-1',
})))

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
      ? {
          id: '00000000-0000-0000-0000-000000000101',
          name: organizationName.value,
          status: 'active',
          enabled_models: ['qwen2.5:7b', 'deepseek-r1:14b'],
        }
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
  useUpdateAppModel: () => ({
    isPending: ref(false),
    mutateAsync: updateModelMock,
  }),
}))

const appRef = ref({
  id: '00000000-0000-0000-0000-000000000001',
  org_id: '00000000-0000-0000-0000-000000000101',
  owner_user_id: '00000000-0000-0000-0000-000000000201',
  name: '测试实例',
  status: 'running',
  persona_mode: 'org_inherited',
  api_key_status: 'active',
  model_id: 'qwen2.5:7b',
  container_id: 'container-1',
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
        NButton: defineComponent({
          props: ['disabled'],
          emits: ['click'],
          setup(props, { slots, emit }) {
            return () => h('button', {
              disabled: props.disabled,
              onClick: () => emit('click'),
            }, slots.default?.())
          },
        }),
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NDescriptions: { template: '<dl><slot /></dl>' },
        NDescriptionsItem: { props: ['label'], template: '<div><dt>{{ label }}</dt><dd><slot /></dd></div>' },
        NFormItem: { props: ['label'], template: '<label><span>{{ label }}</span><slot /></label>' },
        'n-select': defineComponent({
          props: ['value', 'options'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('select', {
              value: props.value,
              onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value),
            }, (props.options ?? []).map((option: { label: string; value: string }) =>
              h('option', { value: option.value }, option.label),
            ))
          },
        }),
        Select: defineComponent({
          props: ['value', 'options'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('select', {
              value: props.value,
              onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value),
            }, (props.options ?? []).map((option: { label: string; value: string }) =>
              h('option', { value: option.value }, option.label),
            ))
          },
        }),
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

  it('修改模型后展示重启任务提示', async () => {
    organizationName.value = '测试组织'
    updateModelMock.mockClear()
    const wrapper = mountOverview()

    await wrapper.find('select').setValue('deepseek-r1:14b')
    await wrapper.findAll('button').find(button => button.text() === '保存并重启实例')!.trigger('click')

    expect(updateModelMock).toHaveBeenCalledWith('deepseek-r1:14b')
    expect(wrapper.text()).toContain('已提交模型生效重启任务：job-restart-1')
  })
})
