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
        <div v-if="canManage" class="upload-actions">
          <span class="upload-limit">{{ KNOWLEDGE_UPLOAD_MAX_MESSAGE }}</span>
          <label class="primary-button">
            <input class="hidden-input" type="file" :disabled="!canManage" @change="onUpload" />
            上传文件
          </label>
        </div>
      </template>

      <n-space align="center" style="margin-bottom: 12px">
        <n-select
          v-if="isPlatformAdmin"
          v-model:value="selectedOrgId"
          :options="orgOptions"
          style="width: 220px"
          placeholder="选择组织"
        />
        <span class="state-text" style="margin: 0">当前路径：<code>{{ relativePath || '/' }}</code></span>
        <n-button v-if="relativePath" size="small" @click="goUp">返回上级</n-button>
      </n-space>

      <div v-if="!effectiveOrgId" class="state-text">{{ emptyOrgMessage }}</div>
      <div v-else-if="isLoading || organizationsLoading" class="state-text">加载中…</div>
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
import { NButton, NCard, NDataTable, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'

import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  downloadOrgKnowledgeFile,
  isKnowledgeUploadTooLarge,
  useDeleteOrgKnowledge,
  useOrgKnowledgeQuery,
  useOrgKnowledgeSyncStatusQuery,
  useRetryOrgKnowledgeSync,
  useUploadOrgKnowledge,
  type KnowledgeEntry,
  type OrgSyncStatusEntry,
} from '@/api/hooks/useKnowledge'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { canManageOrgKnowledge } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import { useUploadProgressStore } from '@/stores/uploadProgress'

// OrgKnowledgePage 管理组织级共享知识库，并展示各 runtime node 的同步状态。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const uploadProgress = useUploadProgressStore()
const message = useMessage()
// 平台管理员通过组织选择器查看组织知识库，组织用户默认使用自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))

// relativePath 是当前浏览目录的组织内相对路径，空字符串表示知识库根目录。
const relativePath = ref('')
const relativeRef = computed(() => relativePath.value)
const eyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 知识库' : '组织 · 知识库'))
// canManage 决定上传、删除和同步重试入口是否可见，接口层仍执行最终权限校验。
const canManage = computed(() => canManageOrgKnowledge(auth.user, effectiveOrgId.value))
const emptyOrgMessage = computed(() => isPlatformAdmin.value ? '暂无可查看组织' : '当前账号未关联组织')

const { data: listing, isLoading, error } = useOrgKnowledgeQuery(effectiveOrgId, relativeRef)
const uploadMutation = useUploadOrgKnowledge(effectiveOrgId, relativeRef)
const deleteMutation = useDeleteOrgKnowledge(effectiveOrgId, relativeRef)
const { data: syncStatuses, isLoading: syncStatusLoading } = useOrgKnowledgeSyncStatusQuery(effectiveOrgId, canManage)
const retryMutation = useRetryOrgKnowledgeSync(effectiveOrgId)
// downloading 标记当前页面正在触发浏览器下载，防止同一页面重复点击下载按钮。
const downloading = ref(false)

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

// onUpload 将文件保存到当前目录；上传进度统一由全局 UploadProgressModal 展示。
// 互斥规则：会话进行中 store.run 抛错，业务侧用 n-message 提示用户。
async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 不论是否真正发起上传，都清空 input.value 以便用户重新选择同名文件。
  input.value = ''
  if (!file) return
  // 前端先拦截超过知识库业务上限的文件，避免创建进度会话后再被网关或后端拒绝。
  if (isKnowledgeUploadTooLarge(file)) {
    message.warning(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    return
  }
  const target = relativePath.value ? `${relativePath.value}/${file.name}` : file.name
  try {
    await uploadProgress.run([{ file, label: file.name }], async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        path: target,
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    // 唯一会被抛出的错误是「会话互斥」：仅此一种情况下提示用户。
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}

// onDelete 使用浏览器确认框拦截误删，删除后由 mutation hook 负责刷新列表缓存。
async function onDelete(entry: KnowledgeEntry) {
  if (!confirm(`确认删除 ${entry.name} ？`)) return
  await deleteMutation.mutateAsync(entry.path)
}

// onDownload 下载组织知识库中的单个文件；目录行不调用此函数。
async function onDownload(entry: KnowledgeEntry) {
  if (!effectiveOrgId.value) return
  downloading.value = true
  try {
    await downloadOrgKnowledgeFile(effectiveOrgId.value, entry.path, entry.name)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  } finally {
    downloading.value = false
  }
}

// onRetry 针对单个 runtime node 重新入队同步任务。
async function onRetry(nodeId: string) {
  await retryMutation.mutateAsync(nodeId)
}

// fileColumns 展示知识库文件；文件行始终可下载，删除入口仅对可管理用户开放。
const fileColumns: DataTableColumns<KnowledgeEntry> = [
  {
    title: '名称', key: 'name',
    render: (row) => row.is_dir
      ? h('strong', { style: 'cursor: pointer; color: var(--color-info-text); text-decoration: underline dotted', onClick: () => enter(row) }, `${row.name}/`)
      : row.name,
  },
  { title: '大小', key: 'size', render: (row) => row.is_dir ? '—' : formatSize(row.size) },
  {
    title: '操作', key: 'actions',
    render: (row) => {
      if (row.is_dir) return null
      const actions = [
        h(NButton, {
          size: 'small',
          disabled: downloading.value,
          onClick: () => onDownload(row),
        }, { default: () => downloading.value ? '下载中…' : '下载' }),
      ]
      if (canManage.value) {
        actions.push(h(NButton, { size: 'small', onClick: () => onDelete(row) }, { default: () => '删除' }))
      }
      return h('div', { style: 'display: flex; gap: 8px; flex-wrap: wrap' }, actions)
    },
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
      ? h('span', { style: 'color: var(--color-danger); font-size: 12px' }, row.last_error)
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
.upload-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

.upload-limit {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.hidden-input {
  display: none;
}

.primary-button.disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
