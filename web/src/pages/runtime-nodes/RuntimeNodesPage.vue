<template>
  <div class="runtime-nodes-page">
    <n-alert type="info" :show-icon="true">
      runtime-agent 启动后会使用 enrollment secret 自动注册到 manager；后台仅负责查看和启停节点，最大实例数由 agent.max_apps 配置同步。
    </n-alert>

    <DataTableList
      title="运行节点"
      eyebrow="Platform · Runtime"
      :columns="columns"
      :data="nodes ?? []"
      :loading="isLoading"
      :error-message="error ? `查询失败：${error.message}` : undefined"
      :row-key="(row: RuntimeNode) => row.id"
    />

    <div ref="drawerTarget" class="drawer-teleport-target" />
    <n-drawer v-model:show="isDrawerVisible" width="min(960px, 100vw)" placement="right" :to="drawerTarget ?? 'body'">
      <n-drawer-content :title="selectedNode ? `节点资源 · ${selectedNode.name}` : '节点资源'" closable>
        <div v-if="selectedNode" class="node-drawer">
          <section class="drawer-section">
            <div class="drawer-section-heading">
              <div>
                <p class="section-kicker">Node Metrics</p>
                <h3>资源趋势</h3>
              </div>
              <n-radio-group v-model:value="resourceRange" size="small">
                <n-radio-button v-for="option in rangeOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </n-radio-button>
              </n-radio-group>
            </div>
            <div class="chart-grid">
              <ResourceTrendChart title="节点 CPU" :samples="nodeCpuSamples" unit="percent" />
              <ResourceTrendChart title="节点内存" :samples="nodeMemorySamples" unit="percent" />
              <ResourceTrendChart title="节点磁盘" :samples="nodeDiskSamples" unit="percent" />
              <ResourceTrendChart title="节点网络 RX/TX 累计" :samples="nodeNetworkSamples" unit="bytes" />
              <ResourceTrendChart title="实例数量" :samples="nodeInstanceCountSamples" unit="count" />
            </div>
          </section>

          <section class="drawer-section">
            <div class="drawer-section-heading">
              <div>
                <p class="section-kicker">Instances</p>
                <h3>关联实例</h3>
              </div>
              <span class="section-meta">{{ nodeInstances?.length ?? 0 }} 个实例</span>
            </div>
            <n-alert v-if="nodeInstancesError" type="error" :show-icon="false">
              实例查询失败：{{ nodeInstancesError.message }}
            </n-alert>
            <n-data-table
              :columns="instanceColumns"
              :data="nodeInstances ?? []"
              :loading="isNodeInstancesLoading"
              :row-key="(row: NodeInstanceResourceRow) => row.app_id"
              :bordered="false"
              size="small"
            />
            <div v-for="instance in expandedInstances" :key="instance.app_id" class="instance-resource-panel">
              <InstanceResourcePanel
                :node-id="selectedNode.id"
                :instance="instance"
                :range="resourceRange"
                :enabled="isInstanceExpanded(instance.app_id)"
              />
            </div>
          </section>
        </div>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, ref, toRef, watch, type PropType } from 'vue'
import { NAlert, NButton, NDataTable, NDrawer, NDrawerContent, NRadioButton, NRadioGroup, type DataTableColumns } from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import ResourceTrendChart from '@/components/ResourceTrendChart.vue'
import { actionColumn, statusColumn } from '@/components/columns'
import { formatRuntimeNodeStatus } from '@/domain/status'
import {
  useRuntimeNodeInstanceResourcesQuery,
  useRuntimeNodeInstancesQuery,
  useRuntimeNodeResourcesQuery,
  useRuntimeNodesQuery,
  useSetRuntimeNodeStatus,
  type InstanceResourceSample,
  type NodeInstanceResourceRow,
  type NodeResourceSample,
  type ResourceRange,
} from '@/api/hooks/useRuntimeNodes'
import type { RuntimeNode } from '@/api'

// RuntimeNodesPage 展示 runtime-agent 自动注册的节点，并提供平台侧启停操作。
const { data: nodes, isLoading, error } = useRuntimeNodesQuery()
const statusMutation = useSetRuntimeNodeStatus()
const selectedNodeId = ref<string | null>(null)
const resourceRange = ref<ResourceRange>('7d')
const expandedInstanceIds = ref<string[]>([])
const drawerTarget = ref<HTMLElement | null>(null)

// selectedNode 从最新列表数据派生，避免列表刷新后抽屉继续持有旧行对象。
const selectedNode = computed(() => (nodes.value ?? []).find((node) => node.id === selectedNodeId.value) ?? null)
const selectedNodeIdForQuery = computed(() => selectedNodeId.value ?? undefined)

// 抽屉显隐完全由 selectedNodeId 派生，关闭时清理实例展开状态，避免切换节点沿用旧行。
const isDrawerVisible = computed({
  get: () => selectedNodeId.value !== null,
  set: (visible: boolean) => {
    if (!visible) {
      selectedNodeId.value = null
      expandedInstanceIds.value = []
    }
  },
})
const { data: nodeResources } = useRuntimeNodeResourcesQuery(selectedNodeIdForQuery, resourceRange)
const {
  data: nodeInstances,
  isLoading: isNodeInstancesLoading,
  error: nodeInstancesError,
} = useRuntimeNodeInstancesQuery(selectedNodeIdForQuery)

// 列表刷新后如果节点已不存在，关闭抽屉，避免展示过期的资源和实例关系。
watch([nodes, selectedNodeId], ([currentNodes, currentNodeId]) => {
  if (!currentNodeId || !currentNodes) return
  if (!currentNodes.some((node) => node.id === currentNodeId)) {
    selectedNodeId.value = null
    expandedInstanceIds.value = []
  }
}, { flush: 'sync' })

const rangeOptions: Array<{ label: string; value: ResourceRange }> = [
  { label: '1 小时', value: '1h' },
  { label: '24 小时', value: '24h' },
  { label: '7 天', value: '7d' },
  { label: '30 天', value: '30d' },
]

const nodeCpuSamples = computed(() => mapNodeSamples((sample) => sample.cpu_percent))
const nodeMemorySamples = computed(() => mapNodeSamples((sample) => percent(sample.memory_used_bytes, sample.memory_total_bytes)))
const nodeDiskSamples = computed(() => mapNodeSamples((sample) => percent(sample.disk_used_bytes, sample.disk_total_bytes)))
const nodeNetworkSamples = computed(() => (nodeResources.value ?? []).map((sample) => ({
  sampled_at: sample.sampled_at,
  value: sample.network_rx_bytes,
  secondary: sample.network_tx_bytes,
})))
const nodeInstanceCountSamples = computed(() => mapNodeSamples((sample) => sample.instance_count))
const expandedInstances = computed(() => (nodeInstances.value ?? []).filter((instance) => isInstanceExpanded(instance.app_id)))

// columns 展示 agent 上报配置和探测结果；最大应用数只读，不在前端提供编辑入口。
const columns: DataTableColumns<RuntimeNode> = [
  {
    title: '名称', key: 'name',
    render: (row) => [
      h('strong', row.name),
      row.agent_id ? h('small', { class: 'data-table-subtitle' }, `Agent ${row.agent_id}`) : null,
    ],
  },
  statusColumn<RuntimeNode>('状态', (r) => formatRuntimeNodeStatus(r.status)),
  { title: 'Docker', key: 'agent_docker_endpoint', render: (row) => row.agent_docker_endpoint || '—' },
  { title: 'File', key: 'agent_file_endpoint', render: (row) => row.agent_file_endpoint || '—' },
  { title: 'Agent 版本', key: 'agent_version', render: (row) => row.agent_version || '—' },
  { title: '心跳', key: 'heartbeat_interval_seconds', render: (row) => `${row.heartbeat_interval_seconds}s` },
  {
    title: '探测',
    key: 'probe',
    render: (row) => [
      h('span', row.last_probe_ok_at ? `最近成功 ${formatDateTime(row.last_probe_ok_at)}` : '尚无成功记录'),
      row.last_probe_error ? h('small', { class: 'data-table-subtitle' }, row.last_probe_error) : null,
    ],
  },
  {
    title: '最大实例数', key: 'max_apps',
    render: (row) => h('span', row.max_apps == null ? '不限' : String(row.max_apps)),
  },
  {
    title: '当前资源',
    key: 'current_resource',
    render: (row) => h('span', formatCurrentResource(row)),
  },
  {
    title: '最近采样',
    key: 'sampled_at',
    render: (row) => row.current_resource?.sampled_at ? formatDateTime(row.current_resource.sampled_at) : '—',
  },
  actionColumn<RuntimeNode>([
    { label: '查看', onClick: (r) => onView(r) },
    { label: '禁用', onClick: (r) => onToggle(r, 'disable'), hidden: (r) => r.status === 'disabled' },
    { label: '启用', type: 'primary', onClick: (r) => onToggle(r, 'enable'), hidden: (r) => r.status !== 'disabled' },
  ]),
]

// instanceColumns 保持实例表紧凑展示；资源按钮用于测试和键盘访问，同时触发展开区加载趋势。
const instanceColumns: DataTableColumns<NodeInstanceResourceRow> = [
  {
    title: '实例',
    key: 'name',
    render: (row) => [
      h('strong', row.name),
      h('small', { class: 'data-table-subtitle' }, row.app_id),
    ],
  },
  { title: '状态', key: 'status', render: (row) => row.status || '—' },
  { title: '容器', key: 'container_id', render: (row) => row.container_id || '—' },
  {
    title: '当前资源',
    key: 'current_resource',
    render: (row) => formatInstanceCurrentResource(row.current_resource),
  },
  {
    title: '操作',
    key: 'actions',
    render: (row) => h(NButton, {
      size: 'tiny',
      type: isInstanceExpanded(row.app_id) ? 'primary' : 'default',
      onClick: () => toggleInstance(row.app_id),
    }, { default: () => '资源' }),
  },
]

// InstanceResourcePanel 在实例展开时独立绑定资源 hook，避免未展开行提前请求明细趋势。
const InstanceResourcePanel = defineComponent({
  name: 'InstanceResourcePanel',
  props: {
    nodeId: { type: String, required: true },
    instance: { type: Object as PropType<NodeInstanceResourceRow>, required: true },
    range: { type: String as PropType<ResourceRange>, required: true },
    enabled: { type: Boolean, required: true },
  },
  setup(props) {
    const nodeId = toRef(props, 'nodeId')
    const appId = computed(() => props.instance.app_id)
    const range = toRef(props, 'range')
    const enabled = toRef(props, 'enabled')
    const { data: samples } = useRuntimeNodeInstanceResourcesQuery(nodeId, appId, range, enabled)
    const cpuSamples = computed(() => mapInstanceSamples(samples.value, (sample) => sample.cpu_percent))
    const memorySamples = computed(() => mapInstanceSamples(samples.value, (sample) => sample.memory_used_bytes))
    const diskSamples = computed(() => (samples.value ?? []).map((sample) => ({
      sampled_at: sample.sampled_at,
      value: sample.disk_read_bytes,
      secondary: sample.disk_write_bytes,
    })))
    const networkSamples = computed(() => (samples.value ?? []).map((sample) => ({
      sampled_at: sample.sampled_at,
      value: sample.network_rx_bytes,
      secondary: sample.network_tx_bytes,
    })))

    return () => h('section', { class: 'instance-resource-content' }, [
      h('header', { class: 'instance-resource-heading' }, [
        h('h4', `${props.instance.name} 资源趋势`),
        h('span', props.instance.app_id),
      ]),
      h('div', { class: 'chart-grid' }, [
        h(ResourceTrendChart, { title: `${props.instance.name} CPU`, samples: cpuSamples.value, unit: 'percent' }),
        h(ResourceTrendChart, { title: `${props.instance.name} 内存`, samples: memorySamples.value, unit: 'bytes' }),
        h(ResourceTrendChart, { title: `${props.instance.name} 磁盘读写累计`, samples: diskSamples.value, unit: 'bytes' }),
        h(ResourceTrendChart, { title: `${props.instance.name} 网络 RX/TX 累计`, samples: networkSamples.value, unit: 'bytes' }),
      ]),
    ])
  },
})

// onToggle 调用节点状态切换接口，列表刷新由 mutation hook 的缓存失效策略处理。
function onToggle(node: RuntimeNode, action: 'enable' | 'disable') {
  statusMutation.mutate({ nodeId: node.id, action })
}

// onView 只更新本地抽屉状态，不写入路由，保证节点详情在当前列表页内完成。
function onView(node: RuntimeNode) {
  selectedNodeId.value = node.id
  expandedInstanceIds.value = []
}

// formatCurrentResource 把列表最近采样压缩成一行，缺少采样时明确展示未采集。
function formatCurrentResource(node: RuntimeNode): string {
  const resource = node.current_resource
  if (!resource) return '未采集'
  return [
    `CPU ${formatPercent(resource.cpu_percent)}`,
    `内存 ${formatBytesPair(resource.memory_used_bytes, resource.memory_total_bytes)}`,
    `磁盘 ${formatBytesPair(resource.disk_used_bytes, resource.disk_total_bytes)}`,
  ].join(' · ')
}

function formatInstanceCurrentResource(resource: InstanceResourceSample | undefined): string {
  if (!resource) return '未采集'
  return [
    `CPU ${formatPercent(resource.cpu_percent)}`,
    `内存 ${formatBytes(resource.memory_used_bytes)}`,
  ].join(' · ')
}

function toggleInstance(appId: string) {
  expandedInstanceIds.value = isInstanceExpanded(appId)
    ? expandedInstanceIds.value.filter((id) => id !== appId)
    : [...expandedInstanceIds.value, appId]
}

function isInstanceExpanded(appId: string): boolean {
  return expandedInstanceIds.value.includes(appId)
}

function mapNodeSamples(resolveValue: (sample: NodeResourceSample) => number | undefined) {
  return (nodeResources.value ?? []).map((sample) => ({
    sampled_at: sample.sampled_at,
    value: resolveValue(sample),
  }))
}

function mapInstanceSamples(samples: InstanceResourceSample[] | undefined, resolveValue: (sample: InstanceResourceSample) => number | undefined) {
  return (samples ?? []).map((sample) => ({
    sampled_at: sample.sampled_at,
    value: resolveValue(sample),
  }))
}

function percent(used: number | undefined, total: number | undefined): number | undefined {
  if (used == null || !total) return undefined
  return (used / total) * 100
}

function formatPercent(value: number | undefined): string {
  return value == null ? '—' : `${formatNumber(value, 1)}%`
}

function formatBytesPair(used: number | undefined, total: number | undefined): string {
  if (used == null && total == null) return '—'
  return `${formatBytes(used)} / ${formatBytes(total)}`
}

function formatBytes(value: number | undefined): string {
  if (value == null) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unitIndex = 0
  while (Math.abs(size) >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  return `${formatNumber(size, unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`
}

function formatNumber(value: number, maximumFractionDigits: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits }).format(value)
}

// formatDateTime 用于探测时间展示，保留浏览器本地时区。
function formatDateTime(value: string) {
  return new Date(value).toLocaleString()
}

</script>

<style scoped>
.node-drawer {
  display: grid;
  gap: 20px;
}

.runtime-nodes-page {
  display: grid;
  gap: 18px;
}

.drawer-teleport-target {
  display: contents;
}

.drawer-section {
  display: grid;
  gap: 12px;
}

.drawer-section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.drawer-section-heading h3,
.instance-resource-heading h4 {
  margin: 0;
  color: var(--color-text-primary, #1f2433);
  font-size: 16px;
  line-height: 24px;
}

.section-kicker {
  margin: 0 0 2px;
  color: var(--color-text-secondary, #8a94c6);
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.section-meta,
.instance-resource-heading span {
  color: var(--color-text-secondary, #66758a);
  font-size: 12px;
}

.chart-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}

.instance-resource-panel {
  border-top: 1px solid var(--color-border, #d9ddea);
  padding-top: 14px;
}

.instance-resource-content {
  display: grid;
  gap: 12px;
}

.instance-resource-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

@media (max-width: 760px) {
  .chart-grid {
    grid-template-columns: 1fr;
  }

  .drawer-section-heading {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
