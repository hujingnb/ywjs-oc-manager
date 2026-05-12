<template>
  <div style="display: grid; gap: 18px">
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
  </div>
</template>

<script setup lang="ts">
import { h, ref } from 'vue'
import { NAlert, NButton, type DataTableColumns } from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import { actionColumn, statusColumn } from '@/components/columns'
import { formatRuntimeNodeStatus } from '@/domain/status'
import { useRuntimeNodesQuery, useSetRuntimeNodeStatus } from '@/api/hooks/useRuntimeNodes'
import type { RuntimeNode } from '@/api'

// RuntimeNodesPage 展示 runtime-agent 自动注册的节点，并提供平台侧启停操作。
const { data: nodes, isLoading, error } = useRuntimeNodesQuery()
const statusMutation = useSetRuntimeNodeStatus()

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
  actionColumn<RuntimeNode>([
    { label: '禁用', onClick: (r) => onToggle(r, 'disable'), hidden: (r) => r.status === 'disabled' },
    { label: '启用', type: 'primary', onClick: (r) => onToggle(r, 'enable'), hidden: (r) => r.status !== 'disabled' },
  ]),
]

// onToggle 调用节点状态切换接口，列表刷新由 mutation hook 的缓存失效策略处理。
function onToggle(node: RuntimeNode, action: 'enable' | 'disable') {
  statusMutation.mutate({ nodeId: node.id, action })
}

// formatDateTime 用于探测时间展示，保留浏览器本地时区。
function formatDateTime(value: string) {
  return new Date(value).toLocaleString()
}

</script>
