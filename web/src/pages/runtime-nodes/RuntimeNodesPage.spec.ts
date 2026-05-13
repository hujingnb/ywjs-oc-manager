import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type Ref } from 'vue'
import { NConfigProvider, NDrawer } from 'naive-ui'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { RuntimeNode } from '@/api'
import type { InstanceResourceSample, NodeInstanceResourceRow, NodeResourceSample, ResourceRange } from '@/api/hooks/useRuntimeNodes'
import RuntimeNodesPage from './RuntimeNodesPage.vue'

const routePath = ref('/runtime-nodes')
const routerPush = vi.fn()
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
const nodeResourceHookCalls: Array<{ nodeId: Ref<string | undefined>; range: Ref<ResourceRange> }> = []
const nodeInstancesHookCalls: Array<{ nodeId: Ref<string | undefined> }> = []
const instanceResourceHookCalls: Array<{
  nodeId: Ref<string | undefined>
  appId: Ref<string | undefined>
  range: Ref<ResourceRange>
  enabled: Ref<boolean>
}> = []

// 运行节点页测试复用可变 hook 数据，按用例覆盖列表、抽屉和实例展开资源展示。
vi.mock('@/api/hooks/useRuntimeNodes', () => ({
  useRuntimeNodesQuery: () => ({
    data: nodesData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useRuntimeNodeResourcesQuery: (nodeId: Ref<string | undefined>, range: Ref<ResourceRange>) => {
    nodeResourceHookCalls.push({ nodeId, range })
    return {
      data: nodeResourcesData,
      isLoading: ref(false),
      error: ref(null),
    }
  },
  useRuntimeNodeInstancesQuery: (nodeId: Ref<string | undefined>) => {
    nodeInstancesHookCalls.push({ nodeId })
    return {
      data: nodeInstancesData,
      isLoading: ref(false),
      error: ref(null),
    }
  },
  useRuntimeNodeInstanceResourcesQuery: (
    nodeId: Ref<string | undefined>,
    appId: Ref<string | undefined>,
    range: Ref<ResourceRange>,
    enabled: Ref<boolean>,
  ) => {
    instanceResourceHookCalls.push({ nodeId, appId, range, enabled })
    return {
      data: instanceResourcesData,
      isLoading: ref(false),
      error: ref(null),
    }
  },
  useSetRuntimeNodeStatus: () => ({ mutate: vi.fn() }),
}))

vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
  useRoute: () => ({ path: routePath.value }),
  useRouter: () => ({ push: routerPush }),
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

function mountPage() {
  return mount(defineComponent({
    setup() {
      return () => h(NConfigProvider, null, {
        default: () => h(RuntimeNodesPage),
      })
    },
  }))
}

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
    nodeResourceHookCalls.length = 0
    nodeInstancesHookCalls.length = 0
    instanceResourceHookCalls.length = 0
    routerPush.mockClear()
  })

  it('只展示 agent 配置上报的最大实例数，不提供编辑入口', () => {
    const wrapper = mountPage()

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

    const wrapper = mountPage()

    expect(wrapper.text()).toContain('CPU')
    expect(wrapper.text()).toContain('内存')
  })

  it('opens drawer without changing route when view is clicked', async () => {
    const wrapper = mountPage()
    const viewButton = wrapper.findAll('button').find((button) => button.text() === '查看')

    expect(viewButton).toBeTruthy()
    await viewButton?.trigger('click')

    expect(wrapper.text()).toContain('节点资源 · node-1')
    expect(routePath.value).toBe('/runtime-nodes')
    expect(routerPush).not.toHaveBeenCalled()
  })

  it('closes drawer when selected node disappears after refetch', async () => {
    const wrapper = mountPage()
    const viewButton = wrapper.findAll('button').find((button) => button.text() === '查看')

    expect(viewButton).toBeTruthy()
    await viewButton?.trigger('click')
    expect(wrapper.text()).toContain('节点资源 · node-1')

    nodesData.value = []
    await nextTick()

    expect(wrapper.text()).not.toContain('节点资源 · node-1')
  })

  it('uses responsive drawer width', () => {
    const wrapper = mountPage()
    const drawer = wrapper.findComponent(NDrawer)

    expect(drawer.props('width')).toBe('min(960px, 100vw)')
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
    const wrapper = mountPage()
    const viewButton = wrapper.findAll('button').find((button) => button.text() === '查看')

    expect(viewButton).toBeTruthy()
    await viewButton?.trigger('click')
    expect(nodeResourceHookCalls[0].nodeId.value).toBe('node-1')
    expect(nodeResourceHookCalls[0].range.value).toBe('1h')
    expect(nodeInstancesHookCalls[0].nodeId.value).toBe('node-1')
    expect(instanceResourceHookCalls).toHaveLength(0)

    const resourceButton = wrapper.findAll('button').find((button) => button.text() === '资源')
    expect(resourceButton).toBeTruthy()
    await resourceButton?.trigger('click')

    expect(instanceResourceHookCalls).toHaveLength(1)
    expect(instanceResourceHookCalls[0].nodeId.value).toBe('node-1')
    expect(instanceResourceHookCalls[0].appId.value).toBe('app-1')
    expect(instanceResourceHookCalls[0].range.value).toBe('1h')
    expect(instanceResourceHookCalls[0].enabled.value).toBe(true)
    expect(wrapper.text()).toContain('实例一 CPU')
  })
})
