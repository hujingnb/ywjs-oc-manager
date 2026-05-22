import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'

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

// mock 组织查询，返回包含测试版本 id 的 allowlist。
vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => ({
    data: ref({
      id: 'org-1',
      name: '测试组织',
      code: 'test',
      status: 'active',
      assistant_version_ids: ['version-1'],
    }),
    isLoading: ref(false),
  }),
}))

// mock 助手版本目录，仅包含一个与 allowlist 匹配的版本。
vi.mock('@/api/hooks/useAssistantVersions', () => ({
  useAssistantVersionsQuery: () => ({
    data: ref([
      { id: 'version-1', name: '测试版本 A' },
      { id: 'version-2', name: '测试版本 B（不在 allowlist）' },
    ]),
    isLoading: ref(false),
  }),
}))

function mountPage() {
  return mount(CreateMemberPage, {
    global: {
      // 注入 QueryClient，解决 useQuery 调用报 "No 'queryClient' found" 的问题。
      plugins: [[VueQueryPlugin, { queryClient: new QueryClient() }]],
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
        // naive-ui 内部以 'n-select' 和 'Select' 注册组件，VTU stub 需覆盖三个 key
        // 才能可靠拦截模板中的 <n-select> 元素，与 MembersPage.spec.ts 保持一致。
        'n-select': defineComponent({
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
        Select: defineComponent({
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
  // 选择助手版本后提交，mutation 应携带 version_id，不包含 model_id。
  it('提交创建成员并初始化实例时包含 version_id 且不包含 model_id', async () => {
    onboardMock.mockClear()
    const wrapper = mountPage()
    // 等待 computed 初始化（versionOptions 依赖 mock 数据）。
    await nextTick()

    // 依次填写用户名、显示名、密码、实例名。
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('member')
    await inputs[1].setValue('成员')
    await inputs[2].setValue('member-pass-123')
    await inputs[3].setValue('测试实例')

    // 表单有两个 <select>：index 0 = role，index 1 = version_id。
    // 通过 DOM setValue 模拟用户选择助手版本，与 MembersPage.spec.ts 保持一致。
    const selects = wrapper.findAll('select')
    await selects[1].setValue('version-1')
    await nextTick()

    await wrapper.find('form').trigger('submit')

    expect(onboardMock).toHaveBeenCalledWith(expect.objectContaining({
      username: 'member',
      display_name: '成员',
      password: 'member-pass-123',
      app_name: '测试实例',
      version_id: 'version-1',
    }))
    // 模型由组织统一配置，前端不再传 model_id
    expect(onboardMock).toHaveBeenCalledWith(expect.not.objectContaining({
      model_id: expect.anything(),
    }))
    expect(wrapper.text()).toContain('Job ID：job-1')
  })

  // version_id 为空时，提交应被阻断并显示提示信息，mutation 不被调用。
  it('未选择助手版本时提交被阻断并显示错误提示', async () => {
    onboardMock.mockClear()
    const wrapper = mountPage()

    // 填写必填账号字段，但不选择助手版本。
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('member2')
    await inputs[1].setValue('成员2')
    await inputs[2].setValue('pass-123456')
    await inputs[3].setValue('测试实例2')

    // version_id 保持空，直接提交。
    await wrapper.find('form').trigger('submit')

    // mutation 不应被调用。
    expect(onboardMock).not.toHaveBeenCalled()
    // 页面应显示版本未选择的错误提示。
    expect(wrapper.text()).toContain('请选择助手版本')
  })
})
