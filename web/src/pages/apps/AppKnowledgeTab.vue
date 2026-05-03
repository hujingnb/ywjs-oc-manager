<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Knowledge</p>
        <h2>应用知识库</h2>
      </div>
      <div class="topbar-actions">
        <label class="secondary-button file-picker" :class="{ disabled: !knowledgeContext || uploading }">
          上传文件
          <input type="file" :disabled="!knowledgeContext || uploading" @change="onUploadFile" />
        </label>
      </div>
    </div>

    <p v-if="!app" class="state-text">尚未加载应用信息</p>
    <p v-else-if="!knowledgeContext" class="state-text">无法构造知识库查询上下文（缺 org_id / owner_user_id）</p>
    <p v-else-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
    <p v-else-if="listing.isLoading.value" class="state-text">加载中…</p>
    <p v-else-if="listing.error.value" class="state-text danger">查询失败：{{ listing.error.value?.message }}</p>
    <table v-else>
      <thead>
        <tr>
          <th>名称</th>
          <th>大小</th>
          <th>类型</th>
          <th class="actions-column">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="entry in listing.data.value?.entries ?? []" :key="entry.path">
          <td>
            <strong>{{ entry.name }}{{ entry.is_dir ? '/' : '' }}</strong>
          </td>
          <td>{{ entry.is_dir ? '—' : formatBytes(entry.size) }}</td>
          <td>{{ entry.is_dir ? '目录' : '文件' }}</td>
          <td class="actions-column">
            <button class="secondary-button danger" type="button" :disabled="deleting" @click="deleteEntry(entryRelativePath(entry.path))">
              删除
            </button>
          </td>
        </tr>
        <tr v-if="!listing.data.value?.entries?.length">
          <td colspan="4" class="state-text">应用知识库暂无文件</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'

import type { AppDTO } from '@/api/hooks/useApps'
import { useAppKnowledgeQuery, useDeleteAppKnowledge, useUploadAppKnowledge } from '@/api/hooks/useKnowledge'

const props = defineProps<{ appId: string }>()
const appIdRef = computed<string | undefined>(() => props.appId)

// 通过 provide 注入的 app；Phase A 不强求 ownerUserId 路径，提供 sentinel 即可。
const app = inject<Ref<AppDTO | null>>('app')

// 应用级知识库 API 需要 org_id + owner_user_id；这两项来自 app DTO。
const knowledgeContext = computed(() => {
  if (!app?.value) return undefined
  return {
    orgId: app.value.org_id,
    ownerUserId: app.value.owner_user_id,
    path: '',
  }
})

const listing = useAppKnowledgeQuery(appIdRef, knowledgeContext)
const uploadMutation = useUploadAppKnowledge(appIdRef, knowledgeContext)
const deleteMutation = useDeleteAppKnowledge(appIdRef, knowledgeContext)
const errorMessage = ref<string>('')

const uploading = computed(() => uploadMutation.isPending.value)
const deleting = computed(() => deleteMutation.isPending.value)

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

function entryRelativePath(entryPath: string) {
  const root = listing.data.value?.path
  if (!root) return entryPath
  const prefix = `${root}/`
  return entryPath.startsWith(prefix) ? entryPath.slice(prefix.length) : entryPath
}

async function onUploadFile(event: Event) {
  errorMessage.value = ''
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    await uploadMutation.mutateAsync({ path: file.name, file })
    input.value = ''
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '上传失败'
  }
}

async function deleteEntry(targetPath: string) {
  errorMessage.value = ''
  try {
    await deleteMutation.mutateAsync(targetPath)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '删除失败'
  }
}
</script>

<style scoped>
.file-picker {
  position: relative;
  overflow: hidden;
}

.file-picker input {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
}

.file-picker.disabled {
  opacity: 0.6;
  pointer-events: none;
}
</style>
