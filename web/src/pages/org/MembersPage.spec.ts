import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import MembersPage from './MembersPage.vue'
import type { Member } from '@/api'

const authUser = vi.hoisted(() => ({
  current: { id: 'admin-1', role: 'org_admin', org_id: 'org-1' } as { id: string; role: string; org_id?: string } | null,
}))

const createMemberAppMock = vi.hoisted(() => ({
  mutateAsync: vi.fn(async () => ({
    app: { id: 'app-1', name: '新实例', status: 'draft', persona_mode: 'org_inherited', api_key_status: 'pending' },
    job_id: 'job-1',
  })),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get user() {
      return authUser.current
    },
  }),
}))

vi.mock('vue-router', async () => {
  const actual = await import('vue-router')
  return {
    ...actual,
    useRouter: () => ({ push: vi.fn() }),
    RouterLink: defineComponent({
      props: ['to'],
      setup(props, { slots }) {
        return () => h('a', { href: typeof props.to === 'string' ? props.to : props.to?.path ?? '' }, slots.default?.())
      },
    }),
  }
})

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref([
      { id: 'org-1', name: '测试组织', status: 'active', enabled_models: ['qwen2.5:7b'] },
      { id: 'org-2', name: '第二组织', status: 'active', enabled_models: ['deepseek-r1:14b'] },
    ]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useOrganizationQuery: (orgId: { value?: string }) => ({
    data: computed(() => {
      if (orgId.value === 'org-2') {
        return { id: 'org-2', name: '第二组织', status: 'active', enabled_models: ['deepseek-r1:14b'] }
      }
      return { id: 'org-1', name: '测试组织', status: 'active', enabled_models: ['qwen2.5:7b'] }
    }),
    isLoading: ref(false),
    isError: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/api/hooks/useMembers', () => ({
  useMembersQuery: () => ({
    data: ref<Member[]>([
      {
        id: 'admin-1',
        org_id: 'org-1',
        username: 'org-admin',
        display_name: '组织管理员',
        role: 'org_admin',
        status: 'active',
        active_app_id: 'app-admin-1',
        active_app_name: '管理员的实例',
      },
      {
        id: 'member-1',
        org_id: 'org-1',
        username: 'member',
        display_name: '组织成员',
        role: 'org_member',
        status: 'active',
      },
    ]),
    isLoading: ref(false),
  }),
  useCreateMember: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useCreateMemberApp: () => ({ mutateAsync: createMemberAppMock.mutateAsync, isPending: ref(false) }),
  useDeleteMember: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useResetMemberPassword: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useSetMemberStatus: () => ({ mutate: vi.fn(), isPending: ref(false) }),
}))

describe('MembersPage', () => {
  const mountPage = () => mount(MembersPage, {
    global: {
      stubs: {
        ConfirmActionModal: true,
        DataTableList: defineComponent({
          props: {
            columns: { type: Array as PropType<DataTableColumn<Member>[]>, required: true },
            data: { type: Array as PropType<Member[]>, required: true },
          },
          setup(props, { slots }) {
            return () => h('section', [
              slots.toolbar?.(),
              h('table', props.data.map(row => h('tr', { key: row.id }, props.columns.map((column) => {
                if ('render' in column && column.render) {
                  return h('td', [column.render(row, 0)])
                }
                const key = 'key' in column ? column.key : undefined
                return h('td', key ? String(row[key as keyof Member] ?? '') : '')
              })))),
            ])
          },
        }),
        NButton: defineComponent({
          props: ['type', 'disabled'],
          emits: ['click'],
          setup(_, { slots, emit }) {
            return () => h('button', { onClick: () => emit('click') }, slots.default?.())
          },
        }),
        NCard: defineComponent({
          setup(_, { slots }) {
            return () => h('section', [slots.header?.(), slots.default?.()])
          },
        }),
        NForm: true,
        NFormItem: true,
        NGrid: true,
        NGridItem: true,
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
        NSpace: defineComponent({
          setup(_, { slots }) {
            return () => h('div', slots.default?.())
          },
        }),
        NTag: defineComponent({
          setup(_, { slots }) {
            return () => h('span', slots.default?.())
          },
        }),
      },
    },
  })

  // 组织管理员管理本组织成员时，自己的行不能出现删除入口，避免误删当前登录账号。
  it('组织管理员不可删除自身', () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountPage()

    const deleteButtons = wrapper.findAll('button').filter(button => button.text() === '删除')
    expect(deleteButtons).toHaveLength(1)
    expect(wrapper.text()).toContain('member')
  })

  // 平台管理员在成员页只有观察权限，即使列表中出现同 ID 用户也不显示删除入口。
  it('平台管理员不可删除自身', () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }

    const wrapper = mountPage()

    const deleteButtons = wrapper.findAll('button').filter(button => button.text() === '删除')
    expect(deleteButtons).toHaveLength(0)
  })

  // 平台管理员仅在没有活跃实例的成员行看到补建入口；与新版 hidden 条件保持一致。
  it('平台管理员只在无实例成员行看到补建入口', () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }

    const wrapper = mountPage()

    const createAppButtons = wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')
    expect(createAppButtons).toHaveLength(1)
  })

  // 组织管理员可以看到「为该成员创建实例」入口，但只对没有活跃实例的成员行显示。
  it('组织管理员可见无实例成员的补建入口', () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountPage()

    const buttons = wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')
    expect(buttons).toHaveLength(1)
  })

  // 平台管理员提交实例表单后展示新实例与初始化任务结果。
  it('平台管理员提交创建新实例时带上默认模型并展示结果', async () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }
    createMemberAppMock.mutateAsync.mockClear()
    const wrapper = mountPage()

    await wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')[0].trigger('click')
    // 默认 app_name 预填为「{显示名} 的实例」，测试覆盖默认值后再改名走表单提交。
    const appNameInput = wrapper.find('input')
    expect((appNameInput.element as HTMLInputElement).value).toBe('组织成员 的实例')
    await appNameInput.setValue('新实例')
    await wrapper.findAll('button').find(button => button.text() === '提交创建')!.trigger('click')

    expect(createMemberAppMock.mutateAsync).toHaveBeenCalledWith({
      userId: 'member-1',
      payload: {
        app_name: '新实例',
        persona_mode: 'org_inherited',
        channel_type: 'wechat',
        model_id: 'qwen2.5:7b',
      },
    })
    expect(wrapper.text()).toContain('已创建实例 新实例')
    expect(wrapper.text()).toContain('job-1')
  })

  // 平台管理员切换组织时关闭已打开的复建实例表单，避免旧成员和新组织模型混用。
  it('平台管理员切换组织时关闭创建新实例表单', async () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }
    createMemberAppMock.mutateAsync.mockClear()
    const wrapper = mountPage()

    await wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')[0].trigger('click')
    expect(wrapper.text()).toContain('提交创建')

    await wrapper.find('select').setValue('org-2')
    await nextTick()

    expect(wrapper.text()).not.toContain('提交创建')
    expect(createMemberAppMock.mutateAsync).not.toHaveBeenCalled()
  })

  // 列表「实例」列在有活跃实例时渲染可点击链接，跳转到 /apps/:appId/overview。
  it('已绑定实例的成员行展示可点击实例链接', () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountPage()

    const link = wrapper.find('a[href="/apps/app-admin-1/overview"]')
    expect(link.exists()).toBe(true)
    expect(link.text()).toBe('管理员的实例')
  })

  // 列表「实例」列在无活跃实例时展示「无实例」警告 tag。
  it('无实例的成员行展示无实例 tag', () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountPage()

    expect(wrapper.text()).toContain('无实例')
  })
})
