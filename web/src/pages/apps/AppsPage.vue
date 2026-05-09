<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">{{ auth.user?.role === 'platform_admin' ? 'Platform · Apps' : '组织 · Apps' }}</p>
        <h2 style="margin: 0">应用列表</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button v-if="!isOrgMember" type="primary" @click="router.push('/members/new')">创建成员并初始化</n-button>
    </template>

    <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织</div>
    <n-data-table
      v-else
      :columns="columns"
      :data="visibleApps"
      :loading="isLoading"
      size="small"
      :bordered="false"
      :row-key="(row) => row.id"
    />

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
  </n-card>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { NButton, NCard, NDataTable, NSpace, type DataTableColumns } from 'naive-ui'

import AppStatusTag from '@/components/AppStatusTag.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
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

const columns: DataTableColumns<AppDTO> = [
  {
    title: '名称', key: 'name',
    render: (row) => h('a', {
      onClick: () => router.push(`/apps/${row.id}/overview`),
      style: 'cursor:pointer; color: #00F0FF',
    }, h('strong', row.name)),
  },
  { title: '状态', key: 'status', render: (row) => h(AppStatusTag, { status: row.status }) },
  { title: 'API key', key: 'api_key_status' },
  { title: '容器', key: 'container_id', render: (row) => row.container_id ?? '—' },
  {
    title: '操作', key: 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => [
        h(NButton, { size: 'small', onClick: () => trigger(row, 'restart') }, { default: () => '重启' }),
        h(NButton, { size: 'small', onClick: () => trigger(row, 'stop') }, { default: () => '停止' }),
        h(NButton, { size: 'small', type: 'error', onClick: () => confirmDelete(row) }, { default: () => '删除' }),
      ]
    }),
  },
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
