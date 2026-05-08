<template>
  <div style="display: grid; gap: 18px">
    <!-- 文件列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">{{ eyebrow }}</p>
          <h2 style="margin: 0">组织知识库</h2>
        </div>
      </template>
      <template #header-extra>
        <label class="primary-button" :class="{ disabled: !canManage }">
          <input class="hidden-input" type="file" :disabled="!canManage" @change="onUpload" />
          上传文件
        </label>
      </template>

      <n-space align="center" style="margin-bottom: 12px">
        <span class="state-text" style="margin: 0">当前路径：<code>{{ relativePath || '/' }}</code></span>
        <n-button v-if="relativePath" size="small" @click="goUp">返回上级</n-button>
      </n-space>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织</div>
      <div v-else-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <n-data-table
        v-else
        :columns="fileColumns"
        :data="listing?.entries ?? []"
        size="small"
        :bordered="false"
        :row-key="(row) => row.path"
      />
    </n-card>

    <!-- 节点同步状态 -->
    <n-card v-if="canManage && effectiveOrgId" :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Sync · 节点同步状态</p>
          <h2 style="margin: 0">各节点同步状态</h2>
        </div>
      </template>

      <div v-if="syncStatusLoading" class="state-text">加载中…</div>
      <n-data-table
        v-else
        :columns="syncColumns"
        :data="syncStatuses ?? []"
        size="small"
        :bordered="false"
        :row-key="(row) => row.node_id"
      />
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { NButton, NCard, NDataTable, NSpace, NTag, type DataTableColumns } from 'naive-ui'

import {
  useDeleteOrgKnowledge,
  useOrgKnowledgeQuery,
  useOrgKnowledgeSyncStatusQuery,
  useRetryOrgKnowledgeSync,
  useUploadOrgKnowledge,
  type KnowledgeEntry,
  type OrgSyncStatusEntry,
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

function syncTagType(s: string): 'success' | 'warning' | 'error' | 'default' {
  return s === 'synced' ? 'success' : s === 'pending' ? 'warning' : s === 'failed' ? 'error' : 'default'
}

function syncStatusLabel(s: string): string {
  return s === 'synced' ? '已同步' : s === 'pending' ? '同步中' : s === 'failed' ? '失败' : s
}

function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

function formatSize(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}

function enter(entry: KnowledgeEntry) {
  if (entry.is_dir) relativePath.value = entry.path
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

async function onRetry(nodeId: string) {
  await retryMutation.mutateAsync(nodeId)
}

const fileColumns: DataTableColumns<KnowledgeEntry> = [
  {
    title: '名称', key: 'name',
    render: (row) => row.is_dir
      ? h('strong', { style: 'cursor: pointer; color: #00F0FF; text-decoration: underline dotted', onClick: () => enter(row) }, `${row.name}/`)
      : row.name,
  },
  { title: '大小', key: 'size', render: (row) => row.is_dir ? '—' : formatSize(row.size) },
  {
    title: '操作', key: 'actions',
    render: (row) => canManage.value && !row.is_dir
      ? h(NButton, { size: 'small', onClick: () => onDelete(row) }, { default: () => '删除' })
      : null,
  },
]

const syncColumns: DataTableColumns<OrgSyncStatusEntry> = [
  { title: '节点 ID', key: 'node_id', render: (row) => h('code', row.node_id.slice(0, 12)) },
  {
    title: '状态', key: 'status',
    render: (row) => h(NTag, { type: syncTagType(row.status), size: 'small', bordered: false }, { default: () => syncStatusLabel(row.status) }),
  },
  { title: '最近成功', key: 'last_success_at', render: (row) => formatTime(row.last_success_at) },
  {
    title: '最近错误', key: 'last_error',
    render: (row) => row.last_error
      ? h('span', { style: 'color: #FF3B5C; font-size: 12px' }, row.last_error)
      : '—',
  },
  {
    title: '操作', key: 'actions',
    render: (row) => h(NButton, {
      size: 'small',
      disabled: retryMutation.isPending.value,
      onClick: () => onRetry(row.node_id),
    }, { default: () => retryMutation.isPending.value ? '入队中…' : '重试同步' }),
  },
]
</script>

<style scoped>
.hidden-input {
  display: none;
}

.primary-button.disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
