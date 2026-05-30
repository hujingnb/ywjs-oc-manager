<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Runtime</p>
        <h2 style="margin: 0">运行时</h2>
      </div>
    </template>
    <template #header-extra>
      <n-space :size="8">
        <n-button size="small" :disabled="!canStart || mutation.isPending.value" @click="onAction('start')">启动</n-button>
        <n-button size="small" :disabled="!canStop || mutation.isPending.value" @click="onAction('stop')">停止</n-button>
        <n-button size="small" :disabled="!canStop || mutation.isPending.value" @click="onAction('restart')">重启</n-button>
        <n-button v-if="canDelete" size="small" type="error" :disabled="mutation.isPending.value" @click="onAction('delete')">删除</n-button>
      </n-space>
    </template>

    <p v-if="runtimeQuery.isLoading.value" class="state-text">加载中…</p>
    <p v-else-if="runtimeQuery.error.value" class="state-text danger">查询失败：{{ runtimeQuery.error.value?.message }}</p>
    <div v-else>
      <!-- 运行时状态：no_container 转为业务文案，其余状态展示原值 -->
      <p class="state-text" style="margin-bottom: 12px">
        当前状态：
        <strong>{{ runtimeStatusLabel }}</strong>
      </p>
      <!-- 最近一次快照：展示采集时间与采集错误，首次采集前提示等待 -->
      <p class="state-text" style="margin-top: 12px">
        <template v-if="runtime?.snapshot">
          最新采样：{{ formatTime(runtime.snapshot.collected_at) }}
          <span v-if="runtime.snapshot.last_error" class="danger"> ｜ 采样错误：{{ runtime.snapshot.last_error }}</span>
        </template>
        <template v-else>资源指标尚未采集（首次采集需 30s 内完成）。</template>
      </p>
    </div>

    <p v-if="actionFeedback" class="state-text" :class="{ danger: actionError }" style="margin-top: 8px">{{ actionFeedback }}</p>

    <JobProgressPanel
      v-if="trackingJobId"
      :title="'最近运行操作'"
      :subtitle="trackingJobId"
      :job="trackedJob ?? undefined"
      style="margin-top: 12px"
    />

    <ConfirmActionModal
      :visible="confirmDelete"
      title="确认删除实例"
      message="将提交删除任务，实例容器、API key 和工作目录都会被回收。该操作不可撤销。"
      confirm-label="确认删除"
      :busy="mutation.isPending.value"
      :verify-value="app?.name ?? ''"
      :verify-hint='app ? `输入实例名 "${app.name}" 以确认删除` : ""'
      @confirm="onConfirmDelete"
      @cancel="confirmDelete = false"
    />

    <ConfirmActionModal
      :visible="confirmStop"
      title="确认停止容器"
      message="停止后 Hermes 容器对话立即中断；可在恢复时重新启动。"
      confirm-label="确认停止"
      :busy="mutation.isPending.value"
      :verify-value="app?.name ?? ''"
      :verify-hint='app ? `输入实例名 "${app.name}" 以确认停止运行` : ""'
      @confirm="onConfirmStop"
      @cancel="confirmStop = false"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'
import { NButton, NCard, NSpace } from 'naive-ui'

import {
  useAppRuntimeQuery,
  useJobQuery,
  useTriggerRuntimeOperation,
  type AppDTO,
} from '@/api/hooks/useApps'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'
import { canManageApp, canTriggerRuntimeOperation } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppRuntimeTab 展示应用 k8s 运行时状态，并触发 start/stop/restart/delete 操作。
// spec-A2b：删除节点信息、ResourceTrendChart 资源趋势图与 container 详情展示；
// 保留启停/重启/删除触发按钮（useTriggerRuntimeOperation）与 k8s 运行状态/快照时间展示。
const props = defineProps<{ appId: string }>()
const appId = computed<string | undefined>(() => props.appId)

const app = inject<Ref<AppDTO | null>>('app')
const auth = useAuthStore()

const runtimeQuery = useAppRuntimeQuery(appId)
const runtime = computed(() => runtimeQuery.data.value ?? null)

const mutation = useTriggerRuntimeOperation(appId)
// trackingJobId 保存最近一次运行时操作的任务 ID，用于轮询异步执行进度。
const trackingJobId = ref<string | undefined>()
const jobIdRef = computed<string | undefined>(() => trackingJobId.value)
const jobQuery = useJobQuery(jobIdRef)
const trackedJob = computed(() => jobQuery.data.value ?? null)

const actionFeedback = ref('')
const actionError = ref(false)
const confirmDelete = ref(false)
const confirmStop = ref(false)

// runtimeStatusLabel 负责把 no_container 转成业务文案，其他未知状态保留原值。
const runtimeStatusLabel = computed(() => {
  const status = runtime.value?.status
  if (!status) return '—'
  if (status === 'no_container') return '尚未创建容器'
  return status
})

// canStart/canStop/canDelete 控制按钮可见性，真实权限和状态转换仍以后端校验为准。
// canManage：运行时启停/重启需平台管理员运维介入能力，使用 canTriggerRuntimeOperation。
const canManage = computed(() => canTriggerRuntimeOperation(auth.user, app?.value))
const canStart = computed(() => canManage.value && app?.value?.status === 'stopped')
const canStop = computed(() => {
  const status = app?.value?.status
  return canManage.value && (status === 'running' || status === 'binding_waiting')
})
// canDelete 仅限应用管理者（不含平台管理员），删除操作不可逆，不向跨组织角色开放。
const canDelete = computed(() => canManageApp(auth.user, app?.value) && app?.value?.status !== 'deleted')

// onAction 对 stop/delete 先弹二次确认，其他操作直接提交运行时任务。
async function onAction(op: 'start' | 'stop' | 'restart' | 'delete') {
  if (op === 'delete') { confirmDelete.value = true; return }
  if (op === 'stop') { confirmStop.value = true; return }
  await runMutation(op)
}

async function onConfirmDelete() { confirmDelete.value = false; await runMutation('delete') }
async function onConfirmStop() { confirmStop.value = false; await runMutation('stop') }

// formatTime 使用中文本地化格式展示后端 ISO 时间。
function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

// runMutation 提交运行时操作并记录 job_id；失败时只更新本页反馈，不做乐观状态切换。
async function runMutation(op: 'start' | 'stop' | 'restart' | 'delete') {
  actionFeedback.value = ''
  actionError.value = false
  try {
    const result = await mutation.mutateAsync(op)
    trackingJobId.value = result.job_id
    actionFeedback.value = `已提交 ${op}：${result.job_id}`
  } catch (err: unknown) {
    actionError.value = true
    actionFeedback.value = err instanceof Error ? err.message : `${op} 操作失败`
  }
}
</script>
