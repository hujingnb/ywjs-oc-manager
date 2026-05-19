<template>
  <DataTableList
    :title="'实例列表'"
    :eyebrow="auth.user?.role === 'platform_admin' ? 'Platform · Instances' : '组织 · Instances'"
    :columns="columns"
    :data="visibleApps"
    :loading="isLoading || organizationsLoading"
    :error-message="errorMessage"
    :row-key="(row: AppDTO) => row.id"
  >
    <template #toolbar>
      <n-select
        v-if="isPlatformAdmin"
        v-model:value="selectedOrgId"
        :options="orgOptions"
        style="width: 220px"
        placeholder="选择组织"
      />
      <n-button v-if="canCreateApp" type="primary" @click="router.push('/members/new')">创建成员并初始化</n-button>
    </template>
  </DataTableList>

  <ConfirmActionModal
    :visible="!!toDelete"
    title="确认删除实例"
    :message='toDelete ? `将提交删除任务，实例 "${toDelete.name}" 关联的容器和 API key 都会被回收。是否继续？` : ""'
    confirm-label="确认删除"
    :busy="deleting"
    :verify-value="toDelete?.name"
    :verify-hint='toDelete ? `输入实例名 "${toDelete.name}" 以确认删除` : ""'
    @confirm="onConfirmDelete"
    @cancel="toDelete = null"
  />
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { NButton, NSelect, NTag } from 'naive-ui'

import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableList from '@/components/DataTableList.vue'
import { linkColumn, actionColumn } from '@/components/columns'
import StatusBadge from '@/components/StatusBadge.vue'
import { formatAppStatus } from '@/domain/status'
import { apiRequest } from '@/api/client'
import { useAppsByOrgQuery, type AppDTO } from '@/api/hooks/useApps'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { canCreateAppForOrg, canManageApp } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppsPage 展示当前组织的应用列表，并提供运行时快捷操作和删除确认。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const router = useRouter()
const client = useQueryClient()

// 平台管理员通过组织选择器切换观察范围，组织用户则落到当前登录组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))
// canCreateApp 控制创建入口，后端仍按组织边界校验创建权限。
const canCreateApp = computed(() => canCreateAppForOrg(auth.user, effectiveOrgId.value))
const { data: apps, isLoading } = useAppsByOrgQuery(effectiveOrgId)

// org_member 只能看到自己的应用
const visibleApps = computed(() => {
  if (!apps.value) return []
  if (auth.user?.role === 'org_member') {
    return apps.value.filter(app => app.owner_user_id === auth.user?.id)
  }
  return apps.value
})

// errorMessage 区分平台管理员未选组织和组织用户无归属，避免平台读页面误报“未关联组织”。
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value) return isPlatformAdmin.value ? '暂无可查看组织' : '当前账号未关联组织'
  return undefined
})

const toDelete = ref<AppDTO | null>(null)
const deleting = ref(false)

// columns 组合链接、状态和操作列；运行时操作按钮按行权限隐藏。
const columns = [
  linkColumn<AppDTO>({
    title: '名称',
    text: r => r.name,
    onClick: r => router.push(`/apps/${r.id}/overview`),
  }),
  // 状态列：model_synced=false 时附加"需重启"警告标签，提示管理员模型变更尚未生效。
  {
    title: '状态',
    key: 'status',
    render: (r: AppDTO) => {
      const badge = h(StatusBadge, { view: formatAppStatus(r.status) })
      if (r.model_synced === false) {
        return h('span', { style: 'display:inline-flex;align-items:center;gap:6px' }, [
          badge,
          h(NTag, { type: 'warning', size: 'small', bordered: false }, () => '需重启'),
        ])
      }
      return badge
    },
  },
  { title: 'API key', key: 'api_key_status' },
  { title: '容器', key: 'container_id', render: (r: AppDTO) => r.container_id ?? '—' },
  actionColumn<AppDTO>([
    { label: '重启', hidden: r => !canManageApp(auth.user, r), onClick: r => trigger(r, 'restart') },
    { label: '停止', hidden: r => !canManageApp(auth.user, r), onClick: r => trigger(r, 'stop') },
    { label: '删除', type: 'error', hidden: r => !canManageApp(auth.user, r), onClick: r => confirmDelete(r) },
  ]),
]

// confirmDelete 只记录待删除应用，真正删除在二次确认后提交。
function confirmDelete(app: AppDTO) { toDelete.value = app }

// onConfirmDelete 复用 runtime/delete 接口，完成后由 trigger 失效应用列表缓存。
async function onConfirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  try { await trigger(toDelete.value, 'delete') }
  finally { deleting.value = false; toDelete.value = null }
}

// trigger 调用运行时操作接口；成功后失效当前组织应用列表，不做前端乐观改状态。
async function trigger(app: AppDTO, op: 'start' | 'stop' | 'restart' | 'delete') {
  await apiRequest<{ runtime_operation: { job_id: string } }>(
    `/api/v1/apps/${app.id}/runtime/${op}`, { method: 'POST' },
  )
  await client.invalidateQueries({ queryKey: ['apps', 'org', effectiveOrgId.value] })
}
</script>
