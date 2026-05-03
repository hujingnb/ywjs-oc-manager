<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Workspace</p>
        <h2>工作目录</h2>
      </div>
      <button v-if="appId" class="secondary-button" type="button" :disabled="downloading" @click="downloadArchive">
        下载归档
      </button>
    </div>

    <p class="state-text">
      当前路径：<code>{{ relativePath || '/' }}</code>
      <button v-if="relativePath" class="secondary-button" type="button" @click="goUp">返回上级</button>
    </p>

    <div v-if="!appId" class="state-text">请选择目标应用</div>
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
            <button
              v-if="!entry.is_dir && appId"
              class="secondary-button"
              type="button"
              :disabled="downloading"
              @click="downloadEntry(entry)"
            >
              下载
            </button>
          </td>
        </tr>
        <tr v-if="!listing?.entries?.length">
          <td colspan="3" class="state-text">当前目录为空</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, toRef } from 'vue'

import {
  archiveWorkspace,
  downloadWorkspaceFile,
  useWorkspaceQuery,
  type WorkspaceEntry,
} from '@/api/hooks/useWorkspace'

const props = defineProps<{ appId?: string }>()
const appId = toRef(props, 'appId')
const relativePath = ref('')
const relativeRef = computed(() => relativePath.value)
const { data: listing, isLoading, error } = useWorkspaceQuery(appId, relativeRef)
const downloading = ref(false)

function enter(entry: WorkspaceEntry) {
  if (entry.is_dir) relativePath.value = entryRelativePath(entry.path)
}

function goUp() {
  const segments = relativePath.value.split('/').filter(Boolean)
  segments.pop()
  relativePath.value = segments.join('/')
}

async function downloadEntry(entry: WorkspaceEntry) {
  if (!props.appId) return
  downloading.value = true
  try {
    await downloadWorkspaceFile(props.appId, entryRelativePath(entry.path), entry.name)
  } finally {
    downloading.value = false
  }
}

async function downloadArchive() {
  if (!props.appId) return
  downloading.value = true
  try {
    await archiveWorkspace(props.appId, relativePath.value)
  } finally {
    downloading.value = false
  }
}

function formatSize(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}

function entryRelativePath(entryPath: string): string {
  const root = listing.value?.path
  if (!root || root === '/') return entryPath.replace(/^\/+/, '')
  const normalizedRoot = root.replace(/^\/+|\/+$/g, '')
  const normalizedEntry = entryPath.replace(/^\/+/, '')
  const prefix = `${normalizedRoot}/`
  return normalizedEntry.startsWith(prefix) ? normalizedEntry.slice(prefix.length) : normalizedEntry
}
</script>

<style scoped>
.folder {
  cursor: pointer;
  color: #276d5c;
  text-decoration: underline dotted;
}
</style>
