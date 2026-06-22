<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Workspace</p>
        <h2 style="margin: 0">工作目录</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button v-if="appId" :disabled="downloading" @click="downloadArchive">下载归档</n-button>
    </template>

    <n-space align="center" style="margin-bottom: 12px">
      <n-input
        v-model:value="searchInput"
        clearable
        size="small"
        placeholder="搜索文件（递归整个工作目录）"
        style="max-width: 260px"
      />
      <template v-if="!searching">
        <span class="state-text" style="margin: 0">当前路径：<code>{{ relativePath || '/' }}</code></span>
        <n-button v-if="relativePath" size="small" @click="goUp">返回上级</n-button>
      </template>
      <span v-else class="state-text" style="margin: 0">
        搜索「{{ keyword }}」：{{ listing?.entries?.length ?? 0 }} 个结果
      </span>
    </n-space>

    <div v-if="!appId" class="state-text">请选择目标实例</div>
    <div v-else-if="isLoading" class="state-text">加载中…</div>
    <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
    <n-data-table
      v-else
      :columns="columns"
      :data="listing?.entries ?? []"
      size="small"
      :bordered="false"
      :row-key="(row) => row.path"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed, h, ref, toRef, watch } from 'vue'
import { NButton, NCard, NDataTable, NInput, NSpace, type DataTableColumns } from 'naive-ui'

import {
  archiveWorkspace,
  downloadWorkspaceFile,
  useWorkspaceQuery,
  type WorkspaceEntry,
} from '@/api/hooks/useWorkspace'

// AppWorkspaceTab 浏览和下载应用工作目录文件，路径始终以应用工作目录为根。
const props = defineProps<{ appId?: string }>()
const appId = toRef(props, 'appId')
// relativePath 保存当前目录相对路径，空字符串表示工作目录根。
const relativePath = ref('')
const relativeRef = computed(() => relativePath.value)
// searchInput 绑定输入框；keyword 是防抖后真正生效的搜索关键字，避免每次按键都递归列举整个工作目录。
const searchInput = ref('')
const keyword = ref('')
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(searchInput, (value) => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    keyword.value = value.trim()
  }, 300)
})
// searching 为真时进入搜索视图：后端忽略当前路径，返回整棵树的匹配文件（完整相对路径）。
const searching = computed(() => keyword.value !== '')
const { data: listing, isLoading, error } = useWorkspaceQuery(appId, relativeRef, keyword)
const downloading = ref(false)

// enter 只允许目录项改变当前路径，文件点击不会触发导航。
function enter(entry: WorkspaceEntry) {
  if (entry.is_dir) relativePath.value = entryRelativePath(entry.path)
}

// goUp 去掉最后一级路径片段，已在根目录时仍保持空路径。
function goUp() {
  const segments = relativePath.value.split('/').filter(Boolean)
  segments.pop()
  relativePath.value = segments.join('/')
}

// downloadEntry 下载单个文件；缺少 appId 时直接返回，避免构造无效下载地址。
async function downloadEntry(entry: WorkspaceEntry) {
  if (!props.appId) return
  downloading.value = true
  try {
    await downloadWorkspaceFile(props.appId, entryRelativePath(entry.path), entry.name)
  } finally {
    downloading.value = false
  }
}

// downloadArchive 下载当前目录归档，下载中的全局按钮态避免重复触发。
async function downloadArchive() {
  if (!props.appId) return
  downloading.value = true
  try {
    await archiveWorkspace(props.appId, relativePath.value)
  } finally {
    downloading.value = false
  }
}

// formatSize 只处理文件大小展示，目录由列渲染为占位符。
function formatSize(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}

// formatTime 把后端 RFC3339 时间转成本地可读格式；目录的零值时间由列渲染为占位符，不会进入此处。
function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// isZeroTime 判断后端是否返回零值时间（目录或缺失），用于决定是否展示创建时间。
function isZeroTime(value: string): boolean {
  if (!value) return true
  const t = new Date(value).getTime()
  return Number.isNaN(t) || t <= 0
}

// entryRelativePath 返回条目相对工作目录根的完整路径，供下载 / 归档接口与目录导航共用。
// 后端这些接口一律以工作目录根为基准；普通列举（joinSlash(relPath, name)）与递归搜索
// （S3 相对 key）返回的 path 本就是 root 相对完整路径，因此只需去掉可能的前导斜杠。
// 不能再剥掉当前目录前缀：否则子目录文件下载会丢失层级（如把 logs/app.log 砍成 app.log
// 导致 404），多级目录导航也会因 relativePath 丢级而查不到目录。
function entryRelativePath(entryPath: string): string {
  return entryPath.replace(/^\/+/, '')
}

// columns 提供目录进入和文件下载操作，目录不展示下载按钮。
const columns: DataTableColumns<WorkspaceEntry> = [
  {
    title: '文件名称', key: 'name',
    render: (row) => row.is_dir
      ? h('strong', { style: 'cursor: pointer; color: var(--color-info-text); text-decoration: underline dotted', onClick: () => enter(row) }, `${row.name}/`)
      : (searching.value ? row.path : row.name),
  },
  { title: '大小', key: 'size', render: (row) => row.is_dir ? '—' : formatSize(row.size) },
  {
    title: '创建时间', key: 'mod_time',
    render: (row) => (row.is_dir || isZeroTime(row.mod_time)) ? '—' : formatTime(row.mod_time),
  },
  {
    title: '操作', key: 'actions',
    render: (row) => !row.is_dir && appId.value
      ? h(NButton, { size: 'small', disabled: downloading.value, onClick: () => downloadEntry(row) }, { default: () => '下载' })
      : null,
  },
]
</script>
