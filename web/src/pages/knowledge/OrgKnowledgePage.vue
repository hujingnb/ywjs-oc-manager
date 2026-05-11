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
        <label v-if="canManage" class="primary-button">
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
import { canManageOrgKnowledge } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// OrgKnowledgePage 管理组织级共享知识库，并展示各 runtime node 的同步状态。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
// effectiveOrgId 支持平台管理员从组织详情进入，也支持组织用户默认使用自身组织。
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)

// relativePath 是当前浏览目录的组织内相对路径，空字符串表示知识库根目录。
const relativePath = ref('')
const relativeRef = computed(() => relativePath.value)
const eyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 知识库' : '组织 · 知识库'))
// canManage 决定上传、删除和同步重试入口是否可见，接口层仍执行最终权限校验。
const canManage = computed(() => canManageOrgKnowledge(auth.user, effectiveOrgId.value))

const { data: listing, isLoading, error } = useOrgKnowledgeQuery(effectiveOrgId, relativeRef)
const uploadMutation = useUploadOrgKnowledge(effectiveOrgId, relativeRef)
const deleteMutation = useDeleteOrgKnowledge(effectiveOrgId, relativeRef)
const { data: syncStatuses, isLoading: syncStatusLoading } = useOrgKnowledgeSyncStatusQuery(effectiveOrgId, canManage)
const retryMutation = useRetryOrgKnowledgeSync(effectiveOrgId)

// syncTagType 将同步状态映射为标签颜色，未知状态保留默认色便于兼容后端新增状态。
function syncTagType(s: string): 'success' | 'warning' | 'error' | 'default' {
  return s === 'synced' ? 'success' : s === 'pending' ? 'warning' : s === 'failed' ? 'error' : 'default'
}

// syncStatusLabel 将同步状态映射为中文文案，未知状态保留原始值。
function syncStatusLabel(s: string): string {
  return s === 'synced' ? '已同步' : s === 'pending' ? '同步中' : s === 'failed' ? '失败' : s
}

// formatTime 对可选同步时间做本地化展示，缺失时统一显示占位符。
function formatTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

// formatSize 用于文件大小展示，目录大小由表格列降级为占位符。
function formatSize(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}

// enter 只允许目录项进入下一级，文件项点击不改变当前路径。
function enter(entry: KnowledgeEntry) {
  if (entry.is_dir) relativePath.value = entry.path
}

// goUp 返回上一级目录，根目录继续保持空路径。
function goUp() {
  const segments = relativePath.value.split('/').filter(Boolean)
  segments.pop()
  relativePath.value = segments.join('/')
}

// onUpload 将文件保存到当前目录；成功后清空 input，允许重复上传同名文件。
async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const target = relativePath.value ? `${relativePath.value}/${file.name}` : file.name
  await uploadMutation.mutateAsync({ path: target, file })
  input.value = ''
}

// onDelete 使用浏览器确认框拦截误删，删除后由 mutation hook 负责刷新列表缓存。
async function onDelete(entry: KnowledgeEntry) {
  if (!confirm(`确认删除 ${entry.name} ？`)) return
  await deleteMutation.mutateAsync(entry.path)
}

// onRetry 针对单个 runtime node 重新入队同步任务。
async function onRetry(nodeId: string) {
  await retryMutation.mutateAsync(nodeId)
}

// fileColumns 展示知识库文件，并只在可管理且非目录行提供删除操作。
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

// syncColumns 展示各节点同步状态和错误，便于组织管理员定位分发问题。
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
