<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Platform · Runtime</p>
          <h2>运行节点</h2>
        </div>
        <button class="primary-button" type="button" @click="openForm">
          <Plus :size="16" />
          <span>注册节点</span>
        </button>
      </div>

      <div v-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <table v-else>
        <thead>
          <tr>
            <th>名称</th>
            <th>状态</th>
            <th>Docker</th>
            <th>File</th>
            <th>Agent 版本</th>
            <th>心跳</th>
            <th>最大应用数</th>
            <th class="actions-column">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="node in nodes" :key="node.id">
            <td>
              <strong>{{ node.name }}</strong>
              <small v-if="node.node_data_root">{{ node.node_data_root }}</small>
            </td>
            <td><RuntimeStatusTag :status="node.status" /></td>
            <td>{{ node.agent_docker_endpoint || '—' }}</td>
            <td>{{ node.agent_file_endpoint || '—' }}</td>
            <td>{{ node.agent_version || '—' }}</td>
            <td>{{ node.heartbeat_interval_seconds }}s</td>
            <td>
              <span v-if="node.max_apps == null">不限</span>
              <span v-else>{{ node.max_apps }}</span>
              <button class="link-button" type="button" @click="openMaxAppsEdit(node)">编辑</button>
            </td>
            <td class="actions-column">
              <button class="secondary-button" type="button" :disabled="node.status === 'active'" @click="onRotate(node)">
                轮换 bootstrap
              </button>
              <button v-if="node.status !== 'disabled'" class="secondary-button" type="button" @click="onToggle(node, 'disable')">
                禁用
              </button>
              <button v-else class="secondary-button" type="button" @click="onToggle(node, 'enable')">
                启用
              </button>
            </td>
          </tr>
          <tr v-if="!nodes?.length">
            <td colspan="8" class="state-text">尚未注册节点</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="formVisible" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">New</p>
          <h2>注册 runtime 节点</h2>
        </div>
        <button class="icon-button" type="button" aria-label="关闭" @click="formVisible = false">
          <X :size="18" />
        </button>
      </div>
      <form class="form-grid" @submit.prevent="onSubmit">
        <label>
          <span>名称 *</span>
          <input v-model.trim="form.name" required type="text" />
        </label>
        <label>
          <span>心跳间隔 (秒)</span>
          <input v-model.number="form.heartbeat_interval_seconds" type="number" min="5" />
        </label>
        <label class="form-grid-full">
          <span>节点数据根目录</span>
          <input v-model.trim="form.node_data_root" placeholder="/var/lib/oc-agent" type="text" />
        </label>
        <div class="form-actions">
          <button class="secondary-button" type="button" @click="formVisible = false">取消</button>
          <button class="primary-button" type="submit" :disabled="creating">
            {{ creating ? '提交中…' : '保存' }}
          </button>
        </div>
        <p v-if="submitError" class="state-text danger form-grid-full">{{ submitError }}</p>
      </form>
    </section>

    <section v-if="editingNode" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Capacity</p>
          <h2>调整最大应用数 · {{ editingNode.name }}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="关闭" @click="cancelMaxAppsEdit">
          <X :size="18" />
        </button>
      </div>
      <form class="form-grid" @submit.prevent="saveMaxApps">
        <label class="form-grid-full">
          <span>最大应用数（清空表示不限；0 表示暂停接收新应用）</span>
          <input v-model="maxAppsInput" type="number" min="0" placeholder="留空表示不限" />
        </label>
        <p class="state-text">
          {{
            maxAppsInput === ''
              ? '保存后该节点不限制应用数量。'
              : `保存后 OnboardingService 自动选节点时仅在剩余容量 > 0 时分配新应用到该节点。`
          }}
        </p>
        <div class="form-actions">
          <button class="secondary-button" type="button" @click="cancelMaxAppsEdit">取消</button>
          <button class="primary-button" type="submit" :disabled="updateMaxAppsMutation.isPending.value">
            {{ updateMaxAppsMutation.isPending.value ? '保存中…' : '保存' }}
          </button>
        </div>
        <p v-if="maxAppsError" class="state-text danger form-grid-full">{{ maxAppsError }}</p>
      </form>
    </section>

    <section v-if="lastIssuedToken" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Bootstrap Token</p>
          <h2>{{ lastIssuedNodeName }}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="关闭" @click="dismissToken">
          <X :size="18" />
        </button>
      </div>
      <p class="state-text">
        以下 token 仅展示一次，请立即配置到 agent 容器的 BOOTSTRAP_TOKEN 环境变量。
        过期时间：{{ lastIssuedExpiresAt }}
      </p>
      <pre class="token-block">{{ lastIssuedToken }}</pre>
    </section>
  </main>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'

import RuntimeStatusTag from '@/components/RuntimeStatusTag.vue'
import {
  useCreateRuntimeNode,
  useRotateBootstrap,
  useRuntimeNodesQuery,
  useSetRuntimeNodeStatus,
  useUpdateRuntimeNodeMaxApps,
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

function openForm() {
  formVisible.value = true
  submitError.value = null
  form.name = ''
  form.heartbeat_interval_seconds = undefined
  form.node_data_root = ''
}

async function onSubmit() {
  creating.value = true
  submitError.value = null
  try {
    const created = await createMutation.mutateAsync({
      name: form.name,
      heartbeat_interval_seconds: form.heartbeat_interval_seconds || undefined,
      node_data_root: form.node_data_root || undefined,
    })
    formVisible.value = false
    showToken(created)
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '注册节点失败'
  } finally {
    creating.value = false
  }
}

async function onRotate(node: RuntimeNode) {
  try {
    const updated = await rotateMutation.mutateAsync(node.id)
    showToken(updated)
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '轮换 bootstrap token 失败'
  }
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
  lastIssuedToken.value = null
  lastIssuedNodeName.value = ''
  lastIssuedExpiresAt.value = ''
}

const editingNode = ref<RuntimeNode | null>(null)
const maxAppsInput = ref<number | ''>('')
const maxAppsError = ref<string | null>(null)

function openMaxAppsEdit(node: RuntimeNode) {
  editingNode.value = node
  maxAppsInput.value = node.max_apps ?? ''
  maxAppsError.value = null
}

function cancelMaxAppsEdit() {
  editingNode.value = null
  maxAppsInput.value = ''
  maxAppsError.value = null
}

async function saveMaxApps() {
  if (!editingNode.value) return
  maxAppsError.value = null
  const raw = maxAppsInput.value
  const value: number | null = raw === '' ? null : Number(raw)
  if (value !== null && (Number.isNaN(value) || value < 0)) {
    maxAppsError.value = '最大应用数必须是非负整数或留空'
    return
  }
  try {
    await updateMaxAppsMutation.mutateAsync({ nodeId: editingNode.value.id, maxApps: value })
    cancelMaxAppsEdit()
  } catch (err) {
    maxAppsError.value = err instanceof Error ? err.message : '保存失败'
  }
}
</script>

<style scoped>
.token-block {
  margin: 12px 0 0;
  padding: 14px;
  border: 1px solid #d8e0ea;
  border-radius: 8px;
  background: #f8fafc;
  color: #172033;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
  white-space: pre-wrap;
}

.link-button {
  margin-left: 6px;
  padding: 0;
  border: 0;
  background: transparent;
  color: #2c5db5;
  font-size: 12px;
  cursor: pointer;
}

.link-button:hover {
  text-decoration: underline;
}
</style>
