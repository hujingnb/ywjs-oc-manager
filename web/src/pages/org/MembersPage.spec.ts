import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import MembersPage from './MembersPage.vue'
import type { Member } from '@/api'

const authUser = vi.hoisted(() => ({
  current: { id: 'admin-1', role: 'org_admin', org_id: 'org-1' } as { id: string; role: string; org_id?: string } | null,
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
        NInput: true,
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
})
