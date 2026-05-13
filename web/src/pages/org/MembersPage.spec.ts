import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType } from 'vue'
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

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref([{ id: 'org-1', name: '测试组织', status: 'active' }]),
    isLoading: ref(false),
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
        NSelect: true,
        NSpace: defineComponent({
          setup(_, { slots }) {
            return () => h('div', slots.default?.())
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

  // 平台管理员可在成员行看到创建新实例入口，用于已删除实例后的复建。
  it('平台管理员可看到创建新实例入口', () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }

    const wrapper = mountPage()

    expect(wrapper.findAll('button').some(button => button.text() === '创建新实例')).toBe(true)
  })

  // 组织管理员仍通过原开户入口创建成员，不显示平台复建实例入口。
  it('组织管理员看不到平台复建实例入口', () => {
    authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

    const wrapper = mountPage()

    expect(wrapper.findAll('button').some(button => button.text() === '创建新实例')).toBe(false)
  })

  // 平台管理员提交实例表单后展示新实例与初始化任务结果。
  it('平台管理员提交创建新实例后展示结果', async () => {
    authUser.current = { id: 'admin-1', role: 'platform_admin' }
    createMemberAppMock.mutateAsync.mockClear()
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text() === '创建新实例')!.trigger('click')
    await wrapper.find('input').setValue('新实例')
    await wrapper.findAll('button').find(button => button.text() === '提交创建')!.trigger('click')

    expect(createMemberAppMock.mutateAsync).toHaveBeenCalledWith({ userId: 'member-1', payload: expect.objectContaining({ app_name: '新实例' }) })
    expect(wrapper.text()).toContain('已创建实例 新实例')
    expect(wrapper.text()).toContain('job-1')
  })
})
