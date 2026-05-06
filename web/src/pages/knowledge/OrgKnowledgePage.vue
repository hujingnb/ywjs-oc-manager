<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">{{ eyebrow }}</p>
          <h2>组织知识库</h2>
        </div>
        <label class="primary-button" :class="{ disabled: !canManage }">
          <input class="hidden-input" type="file" :disabled="!canManage" @change="onUpload" />
          上传文件
        </label>
      </div>

      <p class="state-text">
        当前路径：<code>{{ relativePath || '/' }}</code>
        <button v-if="relativePath" class="secondary-button" type="button" @click="goUp">返回上级</button>
      </p>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织</div>
      <div v-else-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <table v-else>
        <thead>
          <tr>
            <th>名称</th>
            <th>大小</th>
            <th class="actions-column">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in listing?.entries ?? []" :key="entry.path">
            <td>
              <strong v-if="entry.is_dir" class="folder" @click="enter(entry)">{{ entry.name }}/</strong>
              <span v-else>{{ entry.name }}</span>
            </td>
            <td>{{ entry.is_dir ? '—' : formatSize(entry.size) }}</td>
            <td class="actions-column">
              <button v-if="canManage && !entry.is_dir" class="secondary-button" type="button" @click="onDelete(entry)">
                删除
              </button>
            </td>
          </tr>
          <tr v-if="!listing?.entries?.length">
            <td colspan="3" class="state-text">当前目录为空</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="canManage && effectiveOrgId" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Sync · 节点同步状态</p>
          <h2>各节点同步状态</h2>
        </div>
      </div>
      <div v-if="syncStatusLoading" class="state-text">加载中…</div>
      <table v-else>
        <thead>
          <tr>
            <th>节点 ID</th>
            <th>状态</th>
            <th>最近成功</th>
            <th>最近错误</th>
            <th class="actions-column">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in syncStatuses ?? []" :key="entry.node_id">
            <td><code>{{ entry.node_id.slice(0, 12) }}</code></td>
            <td>
              <span :class="['sync-badge', `sync-${entry.status}`]">{{ syncStatusLabel(entry.status) }}</span>
            </td>
            <td>{{ formatTime(entry.last_success_at) }}</td>
            <td>
              <span v-if="entry.last_error" class="state-text danger">{{ entry.last_error }}</span>
              <span v-else>—</span>
            </td>
            <td class="actions-column">
              <button
                class="secondary-button"
                type="button"
                :disabled="retryMutation.isPending.value"
                @click="onRetry(entry.node_id)"
              >
                {{ retryMutation.isPending.value ? '入队中…' : '重试同步' }}
              </button>
            </td>
          </tr>
          <tr v-if="!syncStatuses?.length">
            <td colspan="5" class="state-text">暂无节点同步记录（上传组织级文件后会自动出现）</td>
          </tr>
        </tbody>
      </table>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

import {
  useDeleteOrgKnowledge,
  useOrgKnowledgeQuery,
  useOrgKnowledgeSyncStatusQuery,
  useRetryOrgKnowledgeSync,
  useUploadOrgKnowledge,
  type KnowledgeEntry,
} from '@/api/hooks/useKnowledge'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)

const relativePath = ref('')
const relativeRef = computed(() => relativePath.value)
const eyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 知识库' : '组织 · 知识库'))
const canManage = computed(() => auth.user?.role === 'platform_admin' || auth.user?.role === 'org_admin')

const { data: listing, isLoading, error } = useOrgKnowledgeQuery(effectiveOrgId, relativeRef)
const uploadMutation = useUploadOrgKnowledge(effectiveOrgId, relativeRef)
const deleteMutation = useDeleteOrgKnowledge(effectiveOrgId, relativeRef)
const { data: syncStatuses, isLoading: syncStatusLoading } = useOrgKnowledgeSyncStatusQuery(effectiveOrgId)
const retryMutation = useRetryOrgKnowledgeSync(effectiveOrgId)

function syncStatusLabel(s: string): string {
  return s === 'synced' ? '已同步' : s === 'pending' ? '同步中' : s === 'failed' ? '失败' : s
}

function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

async function onRetry(nodeId: string) {
  await retryMutation.mutateAsync(nodeId)
}

function enter(entry: KnowledgeEntry) {
  if (entry.is_dir) {
    relativePath.value = entry.path
  }
}

function goUp() {
  const segments = relativePath.value.split('/').filter(Boolean)
  segments.pop()
  relativePath.value = segments.join('/')
}

async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const target = relativePath.value ? `${relativePath.value}/${file.name}` : file.name
  await uploadMutation.mutateAsync({ path: target, file })
  input.value = ''
}

async function onDelete(entry: KnowledgeEntry) {
  if (!confirm(`确认删除 ${entry.name} ？`)) return
  await deleteMutation.mutateAsync(entry.path)
}

function formatSize(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}
</script>

<style scoped>
.folder {
  cursor: pointer;
  color: #276d5c;
  text-decoration: underline dotted;
}

.hidden-input {
  display: none;
}

.primary-button.disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.sync-badge {
  display: inline-block;
  padding: 2px 10px;
  border-radius: 10px;
  font-size: 12px;
  font-weight: 500;
}

.sync-pending {
  background: #fff7e6;
  color: #ad6800;
}

.sync-synced {
  background: #e6f7e0;
  color: #2c7a2c;
}

.sync-failed {
  background: #ffe1e1;
  color: #b51d1d;
}
</style>
