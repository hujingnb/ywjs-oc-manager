import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { RuntimeNode } from '@/api'
import type { InstanceResourceSample, NodeInstanceResourceRow, NodeResourceSample } from '@/api/hooks/useRuntimeNodes'
import RuntimeNodesPage from './RuntimeNodesPage.vue'

const routePath = ref('/runtime-nodes')
const nodesData = ref<RuntimeNode[]>([{
  id: 'node-1',
  name: 'node-1',
  status: 'active',
  max_apps: 3,
  heartbeat_interval_seconds: 30,
  has_agent_token: true,
}])
const nodeResourcesData = ref<NodeResourceSample[]>([])
const nodeInstancesData = ref<NodeInstanceResourceRow[]>([])
const instanceResourcesData = ref<InstanceResourceSample[]>([])

// 运行节点页测试复用可变 hook 数据，按用例覆盖列表、抽屉和实例展开资源展示。
vi.mock('@/api/hooks/useRuntimeNodes', () => ({
  useRuntimeNodesQuery: () => ({
    data: nodesData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useRuntimeNodeResourcesQuery: () => ({
    data: nodeResourcesData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useRuntimeNodeInstancesQuery: () => ({
    data: nodeInstancesData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useRuntimeNodeInstanceResourcesQuery: () => ({
    data: instanceResourcesData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useSetRuntimeNodeStatus: () => ({ mutate: vi.fn() }),
}))

vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
  useRoute: () => ({ path: routePath.value }),
  useRouter: () => ({ push: vi.fn() }),
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

describe('RuntimeNodesPage', () => {
  // 每个用例重置 hook 数据，避免抽屉和展开状态场景互相污染。
  beforeEach(() => {
    routePath.value = '/runtime-nodes'
    nodesData.value = [{
      id: 'node-1',
      name: 'node-1',
      status: 'active',
      max_apps: 3,
      heartbeat_interval_seconds: 30,
      has_agent_token: true,
    }]
    nodeResourcesData.value = []
    nodeInstancesData.value = []
    instanceResourcesData.value = []
  })

  it('只展示 agent 配置上报的最大实例数，不提供编辑入口', () => {
    const wrapper = mount(RuntimeNodesPage)

    expect(wrapper.text()).toContain('最大实例数')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).not.toContain('编辑')
  })

  it('shows current resource column for runtime nodes', () => {
    nodesData.value = [{
      id: 'node-1',
      name: 'node-1',
      status: 'active',
      max_apps: 3,
      heartbeat_interval_seconds: 30,
      has_agent_token: true,
      current_resource: {
        sampled_at: '2026-05-13T03:00:00Z',
        cpu_percent: 12.5,
        memory_used_bytes: 512 * 1024 * 1024,
        memory_total_bytes: 1024 * 1024 * 1024,
        disk_used_bytes: 10 * 1024 * 1024 * 1024,
        disk_total_bytes: 20 * 1024 * 1024 * 1024,
      },
    }]

    const wrapper = mount(RuntimeNodesPage)

    expect(wrapper.text()).toContain('CPU')
    expect(wrapper.text()).toContain('内存')
  })

  it('opens drawer without changing route when view is clicked', async () => {
    const wrapper = mount(RuntimeNodesPage)
    const viewButton = wrapper.findAll('button').find((button) => button.text() === '查看')

    expect(viewButton).toBeTruthy()
    await viewButton?.trigger('click')

    expect(wrapper.text()).toContain('节点资源 · node-1')
    expect(routePath.value).toBe('/runtime-nodes')
  })

  it('loads instance resources when an instance row is expanded', async () => {
    nodeInstancesData.value = [{
      app_id: 'app-1',
      org_id: 'org-1',
      owner_user_id: 'user-1',
      name: '实例一',
      status: 'running',
      runtime_node_id: 'node-1',
    }]
    instanceResourcesData.value = [{
      sampled_at: '2026-05-13T03:00:00Z',
      cpu_percent: 10,
      memory_used_bytes: 128 * 1024 * 1024,
    }]
    const wrapper = mount(RuntimeNodesPage)
    const viewButton = wrapper.findAll('button').find((button) => button.text() === '查看')

    expect(viewButton).toBeTruthy()
    await viewButton?.trigger('click')
    const resourceButton = wrapper.findAll('button').find((button) => button.text() === '资源')
    expect(resourceButton).toBeTruthy()
    await resourceButton?.trigger('click')

    expect(wrapper.text()).toContain('实例一 CPU')
  })
})
