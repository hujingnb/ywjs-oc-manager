<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">App · Knowledge</p>
        <h2 style="margin: 0">应用知识库</h2>
      </div>
    </template>
    <template #header-extra>
      <label v-if="canManage" class="secondary-button file-picker" :class="{ disabled: !knowledgeContext || uploading }">
        上传文件
        <input type="file" :disabled="!knowledgeContext || uploading" @change="onUploadFile" />
      </label>
    </template>

    <p v-if="!app" class="state-text">尚未加载应用信息</p>
    <p v-else-if="!knowledgeContext" class="state-text">无法构造知识库查询上下文（缺 org_id / owner_user_id）</p>
    <p v-else-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
    <div v-else-if="listing.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="listing.error.value" class="state-text danger">查询失败：{{ listing.error.value?.message }}</p>
    <n-data-table
      v-else
      :columns="columns"
      :data="listing.data.value?.entries ?? []"
      size="small"
      :bordered="false"
      :row-key="(row) => row.path"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed, h, inject, ref, type Ref } from 'vue'
import { NButton, NCard, NDataTable, type DataTableColumns } from 'naive-ui'

import type { AppDTO } from '@/api/hooks/useApps'
import { useAppKnowledgeQuery, useDeleteAppKnowledge, useUploadAppKnowledge } from '@/api/hooks/useKnowledge'
import type { KnowledgeEntry } from '@/api/hooks/useKnowledge'
import { canManageApp } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppKnowledgeTab 管理单个应用的知识库文件，权限和路径上下文来自应用详情注入。
const props = defineProps<{ appId: string }>()
const appIdRef = computed<string | undefined>(() => props.appId)
const auth = useAuthStore()

const app = inject<Ref<AppDTO | null>>('app')

// knowledgeContext 将应用归属转换为知识库 API 需要的组织、所有者和相对路径。
// app 未加载完成时返回 undefined；页面通过 UI guard 避免常规用户操作提前触发，hook 被无上下文调用时仍会防御性抛错。
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

// canManage 控制上传和删除入口，后端仍会基于应用归属做最终权限校验。
const canManage = computed(() => canManageApp(auth.user, app?.value))
const uploading = computed(() => uploadMutation.isPending.value)
const deleting = computed(() => deleteMutation.isPending.value)

// formatBytes 仅用于文件大小展示，目录大小在列渲染中统一降级为占位符。
function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

// entryRelativePath 去掉当前目录前缀，确保删除接口收到应用知识库内的相对路径。
function entryRelativePath(entryPath: string) {
  const root = listing.data.value?.path
  if (!root) return entryPath
  const prefix = `${root}/`
  return entryPath.startsWith(prefix) ? entryPath.slice(prefix.length) : entryPath
}

// onUploadFile 处理原生 file input 事件；成功后清空 input 以便再次选择同名文件。
async function onUploadFile(event: Event) {
  errorMessage.value = ''
  if (!canManage.value) return
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

// deleteEntry 删除知识库条目并把 mutation 错误转为页面内反馈文案。
async function deleteEntry(targetPath: string) {
  errorMessage.value = ''
  if (!canManage.value) return
  try {
    await deleteMutation.mutateAsync(targetPath)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '删除失败'
  }
}

// columns 展示文件名、大小、类型和删除操作；删除按钮只在可管理时出现。
const columns: DataTableColumns<KnowledgeEntry> = [
  { title: '名称', key: 'name', render: (row) => h('strong', `${row.name}${row.is_dir ? '/' : ''}`) },
  { title: '大小', key: 'size', render: (row) => row.is_dir ? '—' : formatBytes(row.size) },
  { title: '类型', key: 'is_dir', render: (row) => row.is_dir ? '目录' : '文件' },
  {
    title: '操作', key: 'actions',
    render: (row) => canManage.value ? h(NButton, {
      size: 'small',
      type: 'error',
      disabled: deleting.value,
      onClick: () => deleteEntry(entryRelativePath(row.path)),
    }, { default: () => '删除' }) : null,
  },
]
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
