import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppsPage from './AppsPage.vue'

// 平台管理员没有 auth.user.org_id，页面需要先从企业列表选择一个企业再拉实例列表。
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
    data: ref([{ id: 'org-1', name: '测试企业', status: 'active' }]),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

// apps mock 数据：包含 version_synced=false 和 version_synced=true（或缺省）两条实例，
// 用于验证状态列「需重启」标签的条件渲染逻辑。
vi.mock('@/api/hooks/useApps', () => ({
  useAppsByOrgQuery: (orgId: { value: string | undefined }) => ({
    data: computed(() => orgId.value === 'org-1'
      ? [
          {
            id: 'app-1',
            org_id: 'org-1',
            owner_user_id: 'member-1',
            name: '企业实例',
            status: 'running',
            api_key_status: 'active',
          },
          {
            id: 'app-2',
            org_id: 'org-1',
            owner_user_id: 'member-1',
            name: '版本未同步实例',
            status: 'running',
            api_key_status: 'active',
            // version_synced=false 表示绑定的助手版本已被编辑，实例需重启才能生效。
            version_synced: false,
          },
        ]
      : []),
    isLoading: ref(false),
  }),
}))

// DataTableList stub：接收 columns 和 data 两个 prop，对每行调用每列的 render(row)
// （若列无 render 则回退到 row[column.key]），从而让状态列等自定义渲染得到实际执行。
const DataTableListStub = defineComponent({
  props: ['data', 'columns', 'errorMessage'],
  render() {
    const rows = (this.data ?? []) as Record<string, unknown>[]
    const columns = (this.columns ?? []) as Array<{ key?: string; render?: (r: Record<string, unknown>) => unknown }>
    return h('section', [
      // 渲染 errorMessage，供"未关联企业"等断言使用。
      h('p', String(this.errorMessage ?? '')),
      ...rows.map(row =>
        h('div', { key: row.id as string }, [
          // 逐列调用 render 或读取 key 值，确保状态列等自定义渲染在测试中被执行。
          ...columns.map(col =>
            h('span', {}, [col.render ? (col.render(row) as ReturnType<typeof h>) : String(row[col.key ?? ''] ?? '')]),
          ),
        ]),
      ),
    ])
  },
})

// 全局 stubs 注册 naive-ui 组件和内部业务组件，令其透传 slot 内容为文本，
// 避免 NTooltip / NTag / StatusBadge 缺失而抛出渲染错误。
const globalStubs = {
  DataTableList: DataTableListStub,
  ConfirmActionModal: true,
  NButton: true,
  NSelect: true,
  NTag: {
    props: ['type', 'size', 'bordered'],
    template: '<span><slot /></span>',
  },
  NTooltip: {
    template: '<span><slot name="trigger" /><slot /></span>',
  },
  StatusBadge: {
    props: ['view'],
    template: '<span>{{ view?.label }}</span>',
  },
}

describe('AppsPage', () => {
  // 验证平台管理员在不传 orgId prop 时，页面默认使用企业列表中第一个企业加载实例列表。
  it('平台管理员默认使用第一个企业加载实例列表', () => {
    const wrapper = mount(AppsPage, {
      global: { stubs: globalStubs },
    })

    expect(wrapper.text()).toContain('企业实例')
    expect(wrapper.text()).not.toContain('当前账号未关联企业')
  })

  // 验证 version_synced=false 的实例行，状态列渲染「需重启」警告标签。
  it('version_synced=false 的实例显示「需重启」标签', () => {
    const wrapper = mount(AppsPage, {
      global: { stubs: globalStubs },
    })

    expect(wrapper.text()).toContain('需重启')
  })

  // 验证 version_synced 为 true 或字段缺省的实例行，状态列不渲染「需重启」标签。
  // 通过检查「企业实例」所在行不含「需重启」来保证条件是严格判断 false，而非 falsy。
  it('version_synced 非 false 的实例不显示「需重启」标签', () => {
    const wrapper = mount(AppsPage, {
      global: { stubs: globalStubs },
    })

    // 找到「企业实例」行，该行 version_synced 字段缺省，不应包含「需重启」。
    const rows = wrapper.findAll('section > div')
    const syncedRow = rows.find(row => row.text().includes('企业实例'))
    expect(syncedRow).toBeDefined()
    expect(syncedRow!.text()).not.toContain('需重启')

    // 「版本未同步实例」行应包含「需重启」，确认两行各自独立渲染。
    const unsyncedRow = rows.find(row => row.text().includes('版本未同步实例'))
    expect(unsyncedRow).toBeDefined()
    expect(unsyncedRow!.text()).toContain('需重启')
  })
})
