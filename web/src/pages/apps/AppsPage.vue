<template>
  <DataTableList
    :title="'应用列表'"
    :eyebrow="auth.user?.role === 'platform_admin' ? 'Platform · Apps' : '组织 · Apps'"
    :columns="columns"
    :data="visibleApps"
    :loading="isLoading"
    :error-message="!effectiveOrgId ? '当前账号未关联组织' : undefined"
    :row-key="(row: AppDTO) => row.id"
  >
    <template #toolbar>
      <n-button v-if="!isOrgMember" type="primary" @click="router.push('/members/new')">创建成员并初始化</n-button>
    </template>
  </DataTableList>

  <ConfirmActionModal
    :visible="!!toDelete"
    title="确认删除应用"
    :message='toDelete ? `将提交删除任务，应用 "${toDelete.name}" 关联的容器和 API key 都会被回收。是否继续？` : ""'
    confirm-label="确认删除"
    :busy="deleting"
    :verify-value="toDelete?.name"
    :verify-hint='toDelete ? `输入应用名 "${toDelete.name}" 以确认删除` : ""'
    @confirm="onConfirmDelete"
    @cancel="toDelete = null"
  />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { NButton } from 'naive-ui'

import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableList from '@/components/DataTableList.vue'
import { linkColumn, statusColumn, actionColumn } from '@/components/columns'
import { formatAppStatus } from '@/domain/status'
import { apiRequest } from '@/api/client'
import { useAppsByOrgQuery, type AppDTO } from '@/api/hooks/useApps'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const router = useRouter()
const client = useQueryClient()

const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const isOrgMember = computed(() => auth.user?.role === 'org_member')
const { data: apps, isLoading } = useAppsByOrgQuery(effectiveOrgId)

// org_member 只能看到自己的应用
const visibleApps = computed(() => {
  if (!apps.value) return []
  if (auth.user?.role === 'org_member') {
    return apps.value.filter(app => app.owner_user_id === auth.user?.id)
  }
  return apps.value
})

const toDelete = ref<AppDTO | null>(null)
const deleting = ref(false)

const columns = [
  linkColumn<AppDTO>({
    title: '名称',
    text: r => r.name,
    onClick: r => router.push(`/apps/${r.id}/overview`),
  }),
  statusColumn<AppDTO>('状态', r => formatAppStatus(r.status)),
  { title: 'API key', key: 'api_key_status' },
  { title: '容器', key: 'container_id', render: (r: AppDTO) => r.container_id ?? '—' },
  actionColumn<AppDTO>([
    { label: '重启', onClick: r => trigger(r, 'restart') },
    { label: '停止', onClick: r => trigger(r, 'stop') },
    { label: '删除', type: 'error', onClick: r => confirmDelete(r) },
  ]),
]

function confirmDelete(app: AppDTO) { toDelete.value = app }

async function onConfirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  try { await trigger(toDelete.value, 'delete') }
  finally { deleting.value = false; toDelete.value = null }
}

async function trigger(app: AppDTO, op: 'start' | 'stop' | 'restart' | 'delete') {
  await apiRequest<{ runtime_operation: { job_id: string } }>(
    `/api/v1/apps/${app.id}/runtime/${op}`, { method: 'POST' },
  )
  await client.invalidateQueries({ queryKey: ['apps', 'org', effectiveOrgId.value] })
}
</script>
