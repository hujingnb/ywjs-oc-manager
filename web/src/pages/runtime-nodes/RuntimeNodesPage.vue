<template>
  <div style="display: grid; gap: 18px">
    <!-- 节点列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform · Runtime</p>
          <h2 style="margin: 0">运行节点</h2>
        </div>
      </template>
      <template #header-extra>
        <n-button type="primary" @click="openForm">
          <template #icon><Plus :size="16" /></template>
          注册节点
        </n-button>
      </template>

      <div v-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <n-data-table
        v-else
        :columns="columns"
        :data="nodes ?? []"
        size="small"
        :bordered="false"
        :row-key="(row) => row.id"
      />
    </n-card>

    <!-- 注册表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">New</p>
            <h2 style="margin: 0">注册 runtime 节点</h2>
          </div>
          <n-button quaternary circle @click="formVisible = false">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="onSubmit">
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
              <n-button @click="formVisible = false">取消</n-button>
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
import { h, reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NSpace, NTag, type DataTableColumns,
} from 'naive-ui'

import RuntimeStatusTag from '@/components/RuntimeStatusTag.vue'
import {
  useCreateRuntimeNode, useRotateBootstrap, useRuntimeNodesQuery,
  useSetRuntimeNodeStatus, useUpdateRuntimeNodeMaxApps,
  type RuntimeNodeFormPayload,
} from '@/api/hooks/useRuntimeNodes'
import type { RuntimeNode } from '@/api/types'

const { data: nodes, isLoading, error } = useRuntimeNodesQuery()
const createMutation = useCreateRuntimeNode()
const rotateMutation = useRotateBootstrap()
const statusMutation = useSetRuntimeNodeStatus()
const updateMaxAppsMutation = useUpdateRuntimeNodeMaxApps()

const formVisible = ref(false)
const creating = ref(false)
const submitError = ref<string | null>(null)
const lastIssuedToken = ref<string | null>(null)
const lastIssuedNodeName = ref('')
const lastIssuedExpiresAt = ref('')
const form = reactive<RuntimeNodeFormPayload>({ name: '' })

const columns: DataTableColumns<RuntimeNode> = [
  {
    title: '名称', key: 'name',
    render: (row) => [
      h('strong', row.name),
      row.node_data_root ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.node_data_root) : null,
    ],
  },
  { title: '状态', key: 'status', render: (row) => h(RuntimeStatusTag, { status: row.status }) },
  { title: 'Docker', key: 'agent_docker_endpoint', render: (row) => row.agent_docker_endpoint || '—' },
  { title: 'File', key: 'agent_file_endpoint', render: (row) => row.agent_file_endpoint || '—' },
  { title: 'Agent 版本', key: 'agent_version', render: (row) => row.agent_version || '—' },
  { title: '心跳', key: 'heartbeat_interval_seconds', render: (row) => `${row.heartbeat_interval_seconds}s` },
  {
    title: '最大应用数', key: 'max_apps',
    render: (row) => h('span', [
      row.max_apps == null ? '不限' : String(row.max_apps),
      ' ',
      h(NButton, { text: true, size: 'small', style: 'color:#00F0FF', onClick: () => openMaxAppsEdit(row) }, { default: () => '编辑' }),
    ]),
  },
  {
    title: '操作', key: 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => [
        h(NButton, { size: 'small', disabled: row.status === 'active', onClick: () => onRotate(row) }, { default: () => '轮换 bootstrap' }),
        row.status !== 'disabled'
          ? h(NButton, { size: 'small', onClick: () => onToggle(row, 'disable') }, { default: () => '禁用' })
          : h(NButton, { size: 'small', type: 'primary', onClick: () => onToggle(row, 'enable') }, { default: () => '启用' }),
      ]
    }),
  },
]

function openForm() {
  formVisible.value = true; submitError.value = null
  form.name = ''; form.heartbeat_interval_seconds = undefined; form.node_data_root = ''
}

async function onSubmit() {
  creating.value = true; submitError.value = null
  try {
    const created = await createMutation.mutateAsync({
      name: form.name,
      heartbeat_interval_seconds: form.heartbeat_interval_seconds || undefined,
      node_data_root: form.node_data_root || undefined,
    })
    formVisible.value = false; showToken(created)
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '注册节点失败'
  } finally { creating.value = false }
}

async function onRotate(node: RuntimeNode) {
  try { showToken(await rotateMutation.mutateAsync(node.id)) }
  catch (err) { submitError.value = err instanceof Error ? err.message : '轮换 bootstrap token 失败' }
}

function onToggle(node: RuntimeNode, action: 'enable' | 'disable') {
  statusMutation.mutate({ nodeId: node.id, action })
}

function showToken(node: RuntimeNode) {
  if (!node.bootstrap_token) return
  lastIssuedToken.value = node.bootstrap_token
  lastIssuedNodeName.value = node.name
  lastIssuedExpiresAt.value = node.bootstrap_token_expires_at ?? ''
}

function dismissToken() {
  lastIssuedToken.value = null; lastIssuedNodeName.value = ''; lastIssuedExpiresAt.value = ''
}

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
