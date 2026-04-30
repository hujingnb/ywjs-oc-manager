<template>
  <main class="dashboard-main">
    <section class="panel">
      <DataTableToolbar
        :title="'应用列表'"
        :eyebrow="auth.user?.role === 'platform_admin' ? 'Platform · Apps' : '组织 · Apps'"
        :subtitle="effectiveOrgId ? '展示当前组织全部应用，含状态和 API key' : ''"
      >
        <template #actions>
          <RouterLink class="primary-button" to="/members/new">创建成员并初始化</RouterLink>
        </template>
      </DataTableToolbar>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织</div>
      <div v-else-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <table v-else>
        <thead>
          <tr>
            <th>名称</th>
            <th>状态</th>
            <th>API key</th>
            <th>容器</th>
            <th class="actions-column">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="app in apps ?? []" :key="app.id">
            <td>
              <RouterLink :to="{ path: `/apps/${app.id}/overview` }">
                <strong>{{ app.name }}</strong>
              </RouterLink>
              <small v-if="app.description">{{ app.description }}</small>
            </td>
            <td><AppStatusTag :status="app.status" /></td>
            <td>{{ app.api_key_status }}</td>
            <td>{{ app.container_id || '—' }}</td>
            <td class="actions-column">
              <button class="secondary-button" type="button" @click="trigger(app, 'restart')">重启</button>
              <button class="secondary-button" type="button" @click="trigger(app, 'stop')">停止</button>
              <button class="secondary-button" type="button" @click="confirmDelete(app)">删除</button>
            </td>
          </tr>
          <tr v-if="!apps?.length">
            <td colspan="5" class="state-text">尚未创建应用</td>
          </tr>
        </tbody>
      </table>
    </section>

    <ConfirmActionModal
      :visible="!!toDelete"
      title="确认删除应用"
      :message="toDelete ? '将提交删除任务，应用关联的容器和 API key 都会被回收。是否继续？' : ''"
      confirm-label="确认删除"
      :busy="deleting"
      @confirm="onConfirmDelete"
      @cancel="toDelete = null"
    />
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'

import AppStatusTag from '@/components/AppStatusTag.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableToolbar from '@/components/DataTableToolbar.vue'
import { apiRequest } from '@/api/client'
import { useAppsByOrgQuery, type AppDTO } from '@/api/hooks/useApps'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const client = useQueryClient()

const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const { data: apps, isLoading, error } = useAppsByOrgQuery(effectiveOrgId)

const toDelete = ref<AppDTO | null>(null)
const deleting = ref(false)

function confirmDelete(app: AppDTO) {
  toDelete.value = app
}

async function onConfirmDelete() {
  if (!toDelete.value) return
  deleting.value = true
  try {
    await trigger(toDelete.value, 'delete')
  } finally {
    deleting.value = false
    toDelete.value = null
  }
}

async function trigger(app: AppDTO, op: 'start' | 'stop' | 'restart' | 'delete') {
  await apiRequest<{ runtime_operation: { job_id: string } }>(
    `/api/v1/apps/${app.id}/runtime/${op}`,
    { method: 'POST' },
  )
  await client.invalidateQueries({ queryKey: ['apps', 'org', effectiveOrgId.value] })
}
</script>
