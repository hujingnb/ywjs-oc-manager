import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import CreateMemberPage from './CreateMemberPage.vue'

const onboardMock = vi.hoisted(() => vi.fn(async () => ({
  member: {
    id: 'member-1',
    org_id: 'org-1',
    username: 'member',
    display_name: '成员',
    role: 'org_member',
    status: 'active',
  },
  app: {
    id: 'app-1',
    name: '测试实例',
    status: 'draft',
    persona_mode: 'org_inherited',
    api_key_status: 'pending',
  },
  job_id: 'job-1',
})))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 'admin-1', role: 'org_admin', org_id: 'org-1' },
  }),
}))

vi.mock('@/api/hooks/useMembers', () => ({
  useOnboardMember: () => ({ mutateAsync: onboardMock, isPending: ref(false) }),
}))

function mountPage() {
  return mount(CreateMemberPage, {
    global: {
      stubs: {
        RouterLink: defineComponent({
          setup(_, { slots }) {
            return () => h('a', slots.default?.())
          },
        }),
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
        NCard: { template: '<section><slot name="header" /><slot /></section>' },
        NForm: { template: '<form><slot /></form>' },
        NFormItem: { props: ['label'], template: '<label><span>{{ label }}</span><slot /></label>' },
        NGrid: { template: '<div><slot /></div>' },
        NGridItem: { template: '<div><slot /></div>' },
        NInput: defineComponent({
          props: ['value'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              value: props.value,
              onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
            })
          },
        }),
        NSelect: defineComponent({
          props: ['value', 'options', 'disabled'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('select', {
              disabled: props.disabled,
              value: props.value,
              onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value),
            }, (props.options ?? []).map((option: { label: string; value: string }) =>
              h('option', { value: option.value }, option.label),
            ))
          },
        }),
        NSpace: { template: '<div><slot /></div>' },
        NTag: { template: '<span><slot /></span>' },
      },
    },
  })
}

describe('CreateMemberPage', () => {
  // 一键开户提交时不再需要选择模型，模型由组织统一配置。
  it('提交创建成员并初始化实例时不包含 model_id', async () => {
    onboardMock.mockClear()
    const wrapper = mountPage()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('member')
    await inputs[1].setValue('成员')
    await inputs[2].setValue('member-pass-123')
    await inputs[3].setValue('测试实例')
    await wrapper.find('form').trigger('submit')

    expect(onboardMock).toHaveBeenCalledWith(expect.objectContaining({
      username: 'member',
      display_name: '成员',
      password: 'member-pass-123',
      app_name: '测试实例',
    }))
    // 模型由组织统一配置，前端不再传 model_id
    expect(onboardMock).toHaveBeenCalledWith(expect.not.objectContaining({
      model_id: expect.anything(),
    }))
    expect(wrapper.text()).toContain('Job ID：job-1')
  })
})
