<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">App · Workspace</p>
        <h2 style="margin: 0">工作目录</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button v-if="appId" :disabled="downloading" @click="downloadArchive">下载归档</n-button>
    </template>

    <n-space align="center" style="margin-bottom: 12px">
      <span class="state-text" style="margin: 0">当前路径：<code>{{ relativePath || '/' }}</code></span>
      <n-button v-if="relativePath" size="small" @click="goUp">返回上级</n-button>
    </n-space>

    <div v-if="!appId" class="state-text">请选择目标应用</div>
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
import { computed, h, ref, toRef } from 'vue'
import { NButton, NCard, NDataTable, NSpace, type DataTableColumns } from 'naive-ui'

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
const { data: listing, isLoading, error } = useWorkspaceQuery(appId, relativeRef)
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

// entryRelativePath 将后端返回的绝对/带根路径条目转换成下载接口需要的相对路径。
function entryRelativePath(entryPath: string): string {
  const root = listing.value?.path
  if (!root || root === '/') return entryPath.replace(/^\/+/, '')
  const normalizedRoot = root.replace(/^\/+|\/+$/g, '')
  const normalizedEntry = entryPath.replace(/^\/+/, '')
  const prefix = `${normalizedRoot}/`
  return normalizedEntry.startsWith(prefix) ? normalizedEntry.slice(prefix.length) : normalizedEntry
}

// columns 提供目录进入和文件下载操作，目录不展示下载按钮。
const columns: DataTableColumns<WorkspaceEntry> = [
  {
    title: '名称', key: 'name',
    render: (row) => row.is_dir
      ? h('strong', { style: 'cursor: pointer; color: #00F0FF; text-decoration: underline dotted', onClick: () => enter(row) }, `${row.name}/`)
      : row.name,
  },
  { title: '大小', key: 'size', render: (row) => row.is_dir ? '—' : formatSize(row.size) },
  {
    title: '操作', key: 'actions',
    render: (row) => !row.is_dir && appId.value
      ? h(NButton, { size: 'small', disabled: downloading.value, onClick: () => downloadEntry(row) }, { default: () => '下载' })
      : null,
  },
]
</script>
