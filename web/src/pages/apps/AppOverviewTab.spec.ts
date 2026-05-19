import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, ref } from 'vue'
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
    isPlatformAdmin: false,
  }),
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => ({
    data: computed(() => organizationName.value
      ? {
          id: '00000000-0000-0000-0000-000000000101',
          name: organizationName.value,
          status: 'active',
          model_id: 'qwen2.5:7b',
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
        JobProgressPanel: { props: ['title'], template: '<section>{{ title }}</section>' },
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
        NSpace: { template: '<span><slot /></span>' },
        NTag: { template: '<span><slot /></span>' },
        // NProgress 仅作占位,断言关心父节点 .init-progress 是否渲染,而不是进度条本身。
        NProgress: { props: ['percentage', 'processing'], template: '<div class="progress-stub" />' },
      },
    },
  })
}

// mountWithApp 复用上面 mountOverview 的 stubs 配置,但允许覆盖 provide 的 app 数据,
// 便于 init / error 等状态的进度条断言。原 mountOverview 不动以保持既有用例的语义。
function mountWithApp(appOverride: Record<string, unknown>) {
  const customApp = ref({ ...appRef.value, ...appOverride })
  return mount(AppOverviewTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: customApp },
      stubs: {
        AppStatusTag: { template: '<span />' },
        ConfirmActionModal: true,
        JobProgressPanel: { props: ['title'], template: '<section>{{ title }}</section>' },
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
        NSpace: { template: '<span><slot /></span>' },
        NTag: { template: '<span><slot /></span>' },
        NProgress: { props: ['percentage', 'processing'], template: '<div class="progress-stub" />' },
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

// AppOverviewTab progress 覆盖 init 子状态的进度条与失败阶段提示三条分支:
// 1) total=0 时走不定进度,不展示字节文案;
// 2) total>0 时按字节渲染 current/total;
// 3) status=error + last_error_status 显示对应中文阶段。
describe('AppOverviewTab progress', () => {
  // pulling_runtime_image 阶段且 total 未知时只渲染不定进度条,不展示字节文案
  it('init 阶段且 total=0 时展示不定进度', () => {
    const wrapper = mountWithApp({
      status: 'pulling_runtime_image',
      progress_current: 0,
      progress_total: 0,
    })
    expect(wrapper.find('.init-progress').exists()).toBe(true)
    expect(wrapper.find('.init-progress-bytes').exists()).toBe(false)
  })

  // pulling_runtime_image 阶段且 total>0 时按 1.0 KB / 4.0 KB 渲染字节文案
  it('init 阶段且 total>0 时展示字节进度', () => {
    const wrapper = mountWithApp({
      status: 'pulling_runtime_image',
      progress_current: 1024,
      progress_total: 4096,
    })
    const bytes = wrapper.find('.init-progress-bytes')
    expect(bytes.exists()).toBe(true)
    expect(bytes.text()).toContain('1.0 KB')
    expect(bytes.text()).toContain('4.0 KB')
  })

  // error + last_error_status=pulling_runtime_image 时按 status.ts 映射展示「拉取运行时镜像」中文
  it('error 时展示失败阶段', () => {
    const wrapper = mountWithApp({
      status: 'error',
      last_error_status: 'pulling_runtime_image',
    })
    expect(wrapper.find('.init-failure').text()).toContain('拉取运行时镜像')
  })
})
