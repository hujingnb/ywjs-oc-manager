import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type Ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { rangeQuery, type InstanceResourceSample, type ResourceRange } from '@/api/hooks/useRuntimeNodes'
import AppRuntimeTab from './AppRuntimeTab.vue'

const appRef = ref({
  id: '00000000-0000-0000-0000-000000000001',
  org_id: '00000000-0000-0000-0000-000000000101',
  owner_user_id: '00000000-0000-0000-0000-000000000201',
  name: '测试实例',
  status: 'running',
  persona_mode: 'org_inherited',
  api_key_status: 'active',
})
const runtimeData = ref({
  status: 'running',
  container: {
    id: 'container-1',
    name: 'oc-app-1',
    image: 'openclaw:test',
    status: 'running',
  },
})
const resourceSamples = ref<InstanceResourceSample[]>([])
const resourceHookCalls: Array<{ appId: Ref<string | undefined>; range: Ref<ResourceRange> }> = []

// 运行时页测试通过 hook mock 控制容器状态、资源采样和操作权限，不依赖真实后端。
vi.mock('@/api/hooks/useApps', () => ({
  useAppRuntimeQuery: () => ({
    data: runtimeData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useAppResourcesQuery: (appId: Ref<string | undefined>, range: Ref<ResourceRange>) => {
    resourceHookCalls.push({ appId, range })
    return {
      data: resourceSamples,
      isLoading: ref(false),
      error: ref(null),
    }
  },
  useJobQuery: () => ({
    data: ref(null),
  }),
  useTriggerRuntimeOperation: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_admin',
    },
  }),
}))

vi.mock('@/components/ResourceTrendChart.vue', () => ({
  default: defineComponent({
    name: 'ResourceTrendChart',
    props: {
      title: { type: String, required: true },
      samples: { type: Array, required: true },
      unit: { type: String, required: true },
      emptyText: { type: String, required: false },
    },
    setup(props) {
      return () => h('section', { class: 'resource-trend-chart' }, props.title)
    },
  }),
}))

function mountRuntimeTab() {
  return mount(AppRuntimeTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: appRef },
      stubs: {
        ConfirmActionModal: true,
        JobProgressPanel: true,
        NButton: { template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>', props: ['disabled'] },
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NGrid: { template: '<div><slot /></div>' },
        NGridItem: { template: '<div><slot /></div>' },
        NSpace: { template: '<div><slot /></div>' },
      },
    },
  })
}

describe('AppRuntimeTab', () => {
  beforeEach(() => {
    appRef.value = {
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试实例',
      status: 'running',
      persona_mode: 'org_inherited',
      api_key_status: 'active',
    }
    runtimeData.value = {
      status: 'running',
      container: {
        id: 'container-1',
        name: 'oc-app-1',
        image: 'openclaw:test',
        status: 'running',
      },
    }
    resourceSamples.value = []
    resourceHookCalls.length = 0
  })

  // 覆盖资源采样存在时运行时页从最新快照卡片切换为实例资源趋势图。
  it('renders resource trend charts in runtime tab', () => {
    resourceSamples.value = [{
      sampled_at: '2026-05-13T03:00:00Z',
      container_status: 'running',
      cpu_percent: 12.5,
      memory_used_bytes: 256 * 1024 * 1024,
      memory_limit_bytes: 1024 * 1024 * 1024,
      disk_read_bytes: 10 * 1024 * 1024,
      disk_write_bytes: 20 * 1024 * 1024,
      network_rx_bytes: 30 * 1024,
      network_tx_bytes: 40 * 1024,
    }]

    const wrapper = mountRuntimeTab()

    expect(wrapper.text()).toContain('实例 CPU')
    expect(wrapper.text()).toContain('实例网络 RX/TX')
  })

  // 覆盖资源采样为空时仍保留运行中实例可执行的停止、重启和删除操作入口。
  it('keeps runtime operation buttons visible when no samples exist', () => {
    resourceSamples.value = []

    const wrapper = mountRuntimeTab()

    expect(wrapper.findAll('button').map((button) => button.text())).toEqual(
      expect.arrayContaining(['停止', '重启', '删除']),
    )
  })

  // 覆盖时间范围切换会更新资源 hook 的 range，并验证 30d 查询会按后端约定聚合到 1h bucket。
  it('changes resource query when range changes', async () => {
    const wrapper = mountRuntimeTab()

    expect(resourceHookCalls[0].range.value).toBe('1h')

    const thirtyDayButton = wrapper.findAll('button').find((button) => button.text() === '30d')
    expect(thirtyDayButton).toBeTruthy()
    await thirtyDayButton?.trigger('click')
    await nextTick()

    expect(resourceHookCalls[0].range.value).toBe('30d')
    expect(rangeQuery(resourceHookCalls[0].range.value).bucket).toBe('1h')
  })
})
