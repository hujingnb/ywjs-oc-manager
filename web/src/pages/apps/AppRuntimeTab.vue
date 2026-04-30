<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Runtime</p>
        <h2>运行时</h2>
      </div>
      <div class="topbar-actions">
        <button class="secondary-button" type="button" :disabled="!canStart || mutation.isPending.value" @click="onAction('start')">启动</button>
        <button class="secondary-button" type="button" :disabled="!canStop || mutation.isPending.value" @click="onAction('stop')">停止</button>
        <button class="secondary-button" type="button" :disabled="!canStop || mutation.isPending.value" @click="onAction('restart')">重启</button>
        <button class="secondary-button danger" type="button" :disabled="!canDelete || mutation.isPending.value" @click="onAction('delete')">删除</button>
      </div>
    </div>

    <p v-if="runtimeQuery.isLoading.value" class="state-text">加载中…</p>
    <p v-else-if="runtimeQuery.error.value" class="state-text danger">查询失败：{{ runtimeQuery.error.value?.message }}</p>
    <div v-else>
      <p class="state-text">
        当前状态：
        <strong>{{ runtimeStatusLabel }}</strong>
        <span v-if="runtime?.container">｜容器：<code>{{ runtime.container.id }}</code></span>
      </p>
      <p v-if="runtime?.container?.image" class="state-text">
        镜像：<code>{{ runtime.container.image }}</code>
      </p>
    </div>

    <p v-if="actionFeedback" class="state-text" :class="{ danger: actionError }">{{ actionFeedback }}</p>

    <JobProgressPanel
      v-if="trackingJobId"
      :title="'最近运行操作'"
      :subtitle="trackingJobId"
      :job="trackedJob ?? undefined"
    />

    <ConfirmActionModal
      :visible="confirmDelete"
      title="确认删除应用"
      message="将提交删除任务，应用容器、API key 和工作目录都会被回收。该操作不可撤销。"
      confirm-label="确认删除"
      :busy="mutation.isPending.value"
      @confirm="onConfirmDelete"
      @cancel="confirmDelete = false"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'

import {
  useAppRuntimeQuery,
  useJobQuery,
  useTriggerRuntimeOperation,
  type AppDTO,
} from '@/api/hooks/useApps'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'

const props = defineProps<{ appId: string }>()
const appId = computed<string | undefined>(() => props.appId)

const app = inject<Ref<AppDTO | null>>('app')

const runtimeQuery = useAppRuntimeQuery(appId)
const runtime = computed(() => runtimeQuery.data.value ?? null)

const mutation = useTriggerRuntimeOperation(appId)
const trackingJobId = ref<string | undefined>()
const jobIdRef = computed<string | undefined>(() => trackingJobId.value)
const jobQuery = useJobQuery(jobIdRef)
const trackedJob = computed(() => jobQuery.data.value ?? null)

const actionFeedback = ref('')
const actionError = ref(false)
const confirmDelete = ref(false)

const runtimeStatusLabel = computed(() => {
  const status = runtime.value?.status
  if (!status) return '—'
  if (status === 'no_container') return '尚未创建容器'
  return status
})

const canStart = computed(() => app?.value?.status === 'stopped')
const canStop = computed(() => {
  const status = app?.value?.status
  return status === 'running' || status === 'binding_waiting'
})
const canDelete = computed(() => {
  const status = app?.value?.status
  return status !== 'deleted'
})

async function onAction(op: 'start' | 'stop' | 'restart' | 'delete') {
  if (op === 'delete') {
    // 删除走二次确认；ConfirmActionModal 的 confirm 事件触发实际请求。
    confirmDelete.value = true
    return
  }
  await runMutation(op)
}

async function onConfirmDelete() {
  confirmDelete.value = false
  await runMutation('delete')
}

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

<style scoped>
.topbar-actions {
  display: flex;
  gap: 8px;
}

.danger {
  color: rgba(220, 38, 38, 1);
  border-color: rgba(220, 38, 38, 0.4);
}
</style>
