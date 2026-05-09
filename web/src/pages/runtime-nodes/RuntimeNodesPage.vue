<template>
  <div style="display: grid; gap: 18px">
    <!-- 节点列表 -->
    <DataTableList
      title="运行节点"
      eyebrow="Platform · Runtime"
      :columns="columns"
      :data="nodes ?? []"
      :loading="isLoading"
      :error-message="error ? `查询失败：${error.message}` : undefined"
      :row-key="(row: RuntimeNode) => row.id"
    >
      <template #toolbar>
        <n-button type="primary" @click="openForm">
          <template #icon><Plus :size="16" /></template>
          注册节点
        </n-button>
      </template>
    </DataTableList>

    <!-- 注册表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">New</p>
            <h2 style="margin: 0">注册 runtime 节点</h2>
          </div>
          <n-button quaternary circle @click="closeForm">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="节点名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="心跳间隔 (秒)">
              <n-input-number v-model:value="form.heartbeat_interval_seconds" :min="5" style="width: 100%" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="节点数据根目录">
              <n-input v-model:value="form.node_data_root" placeholder="/var/lib/oc-agent" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="closeForm">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <!-- 调整最大应用数 -->
    <n-card v-if="editingNode" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">Capacity</p>
            <h2 style="margin: 0">调整最大应用数 · {{ editingNode.name }}</h2>
          </div>
          <n-button quaternary circle @click="cancelMaxAppsEdit">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form label-placement="top" @submit.prevent="saveMaxApps">
        <n-form-item label="最大应用数（清空表示不限；0 表示暂停接收新应用）">
          <n-input-number v-model:value="maxAppsInput" :min="0" placeholder="留空表示不限" style="width: 100%" />
        </n-form-item>
        <p class="state-text">
          {{ maxAppsInput == null ? '保存后该节点不限制应用数量。' : '保存后 OnboardingService 自动选节点时仅在剩余容量 > 0 时分配新应用到该节点。' }}
        </p>
        <n-space justify="end">
          <n-button @click="cancelMaxAppsEdit">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="updateMaxAppsMutation.isPending.value">保存</n-button>
        </n-space>
        <p v-if="maxAppsError" class="state-text danger">{{ maxAppsError }}</p>
      </n-form>
    </n-card>

    <!-- Bootstrap Token 展示 -->
    <n-card v-if="lastIssuedToken" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">Bootstrap Token</p>
            <h2 style="margin: 0">{{ lastIssuedNodeName }}</h2>
          </div>
          <n-button quaternary circle @click="dismissToken">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <p class="state-text">
        以下 token 仅展示一次，请立即配置到 agent 容器的 BOOTSTRAP_TOKEN 环境变量。
        过期时间：{{ lastIssuedExpiresAt }}
      </p>
      <pre class="token-block">{{ lastIssuedToken }}</pre>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { h, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NSpace, type DataTableColumns,
} from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { formatRuntimeNodeStatus } from '@/domain/status'
import { useFormModal } from '@/composables/useFormModal'
import {
  useCreateRuntimeNode, useRotateBootstrap, useRuntimeNodesQuery,
  useSetRuntimeNodeStatus, useUpdateRuntimeNodeMaxApps,
} from '@/api/hooks/useRuntimeNodes'
import type { RuntimeNode } from '@/api'

const { data: nodes, isLoading, error } = useRuntimeNodesQuery()
const createMutation = useCreateRuntimeNode()
const rotateMutation = useRotateBootstrap()
const statusMutation = useSetRuntimeNodeStatus()
const updateMaxAppsMutation = useUpdateRuntimeNodeMaxApps()

// bootstrap token 展示状态
const lastIssuedToken = ref<string | null>(null)
const lastIssuedNodeName = ref('')
const lastIssuedExpiresAt = ref('')

// showToken 仅在节点携带 bootstrap_token 时展示，只展示一次后由用户手动关闭
function showToken(node: RuntimeNode) {
  if (!node.bootstrap_token) return
  lastIssuedToken.value = node.bootstrap_token
  lastIssuedNodeName.value = node.name
  lastIssuedExpiresAt.value = node.bootstrap_token_expires_at ?? ''
}

function dismissToken() {
  lastIssuedToken.value = null; lastIssuedNodeName.value = ''; lastIssuedExpiresAt.value = ''
}

// 注册表单状态聚合到 useFormModal；onSuccess 注入 showToken 业务后置
const { form, formVisible, creating, submitError, openForm, closeForm, submit } = useFormModal({
  initial: {
    name: '',
    heartbeat_interval_seconds: undefined as number | undefined,
    node_data_root: '',
  },
  mutation: createMutation,
  // toPayload 过滤可选字段：空值转 undefined，避免向后端传空字符串
  toPayload: (f) => ({
    name: f.name,
    heartbeat_interval_seconds: f.heartbeat_interval_seconds || undefined,
    node_data_root: f.node_data_root || undefined,
  }),
  onSuccess: (created) => showToken(created),
})

const columns: DataTableColumns<RuntimeNode> = [
  // 名称列：含 node_data_root 副标题
  {
    title: '名称', key: 'name',
    render: (row) => [
      h('strong', row.name),
      row.node_data_root ? h('small', { class: 'data-table-subtitle' }, row.node_data_root) : null,
    ],
  },
  statusColumn<RuntimeNode>('状态', (r) => formatRuntimeNodeStatus(r.status)),
  { title: 'Docker', key: 'agent_docker_endpoint', render: (row) => row.agent_docker_endpoint || '—' },
  { title: 'File', key: 'agent_file_endpoint', render: (row) => row.agent_file_endpoint || '—' },
  { title: 'Agent 版本', key: 'agent_version', render: (row) => row.agent_version || '—' },
  { title: '心跳', key: 'heartbeat_interval_seconds', render: (row) => `${row.heartbeat_interval_seconds}s` },
  // max_apps 列：含页面特有的内联编辑按钮，不归 actionColumn
  {
    title: '最大应用数', key: 'max_apps',
    render: (row) => h('span', [
      row.max_apps == null ? '不限' : String(row.max_apps),
      ' ',
      h(NButton, { text: true, size: 'small', class: 'data-table-link', onClick: () => openMaxAppsEdit(row) }, { default: () => '编辑' }),
    ]),
  },
  // 操作列：轮换 bootstrap + 启用/禁用 hidden 互斥
  actionColumn<RuntimeNode>([
    { label: '轮换 bootstrap', onClick: (r) => onRotate(r), disabled: (r) => r.status === 'active' },
    { label: '禁用', onClick: (r) => onToggle(r, 'disable'), hidden: (r) => r.status === 'disabled' },
    { label: '启用', type: 'primary', onClick: (r) => onToggle(r, 'enable'), hidden: (r) => r.status !== 'disabled' },
  ]),
]

async function onRotate(node: RuntimeNode) {
  try { showToken(await rotateMutation.mutateAsync(node.id)) }
  catch (err) { console.error('轮换 bootstrap token 失败', err) }
}

function onToggle(node: RuntimeNode, action: 'enable' | 'disable') {
  statusMutation.mutate({ nodeId: node.id, action })
}

// max_apps 内联编辑状态（页面特有，不归 useFormModal）
const editingNode = ref<RuntimeNode | null>(null)
const maxAppsInput = ref<number | null>(null)
const maxAppsError = ref<string | null>(null)

function openMaxAppsEdit(node: RuntimeNode) {
  editingNode.value = node; maxAppsInput.value = node.max_apps ?? null; maxAppsError.value = null
}

function cancelMaxAppsEdit() {
  editingNode.value = null; maxAppsInput.value = null; maxAppsError.value = null
}

async function saveMaxApps() {
  if (!editingNode.value) return
  maxAppsError.value = null
  try {
    await updateMaxAppsMutation.mutateAsync({ nodeId: editingNode.value.id, maxApps: maxAppsInput.value })
    cancelMaxAppsEdit()
  } catch (err) { maxAppsError.value = err instanceof Error ? err.message : '保存失败' }
}
</script>

<style scoped>
.token-block {
  margin: 12px 0 0;
  padding: 14px;
  border: 1px solid rgba(0, 240, 255, 0.2);
  border-radius: 8px;
  background: rgba(15, 21, 53, 0.8);
  color: #00F0FF;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
  white-space: pre-wrap;
}
</style>
