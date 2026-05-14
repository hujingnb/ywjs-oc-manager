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
        <n-button size="small" type="error" :disabled="!canDelete || mutation.isPending.value" @click="onAction('delete')">删除</n-button>
      </n-space>
    </template>

    <p v-if="runtimeQuery.isLoading.value" class="state-text">加载中…</p>
    <p v-else-if="runtimeQuery.error.value" class="state-text danger">查询失败：{{ runtimeQuery.error.value?.message }}</p>
    <div v-else>
      <p class="state-text" style="margin-bottom: 12px">
        当前状态：
        <strong>{{ runtimeStatusLabel }}</strong>
        <span v-if="runtime?.container">｜容器：<code>{{ runtime.container.id }}</code></span>
      </p>
      <p v-if="runtime?.container?.image" class="state-text">
        镜像：<code>{{ runtime.container.image }}</code>
      </p>

      <n-space :size="8" style="margin-top: 12px">
        <n-button
          v-for="option in rangeOptions"
          :key="option"
          size="small"
          :type="resourceRange === option ? 'primary' : 'default'"
          @click="resourceRange = option"
        >
          {{ option }}
        </n-button>
      </n-space>

      <p v-if="resourcesQuery.isLoading.value" class="state-text" style="margin-top: 12px">资源趋势加载中…</p>
      <p v-else-if="resourcesQuery.error.value" class="state-text danger" style="margin-top: 12px">
        资源趋势查询失败：{{ resourcesQuery.error.value?.message }}
      </p>
      <n-grid v-else :cols="2" :x-gap="12" :y-gap="12" style="margin-top: 12px">
        <n-grid-item>
          <ResourceTrendChart title="实例 CPU" :samples="cpuTrendSamples" unit="percent" />
        </n-grid-item>
        <n-grid-item>
          <ResourceTrendChart title="实例内存 used/limit" :samples="memoryTrendSamples" unit="bytes" />
        </n-grid-item>
        <n-grid-item>
          <ResourceTrendChart title="实例磁盘读写" :samples="diskTrendSamples" unit="bytes" />
        </n-grid-item>
        <n-grid-item>
          <ResourceTrendChart title="实例网络 RX/TX" :samples="networkTrendSamples" unit="bytes" />
        </n-grid-item>
        <n-grid-item :span="2">
          <p class="state-text" style="margin: 0">
            <template v-if="latestSample">
              最新采样：{{ latestSample.container_status ?? '未知状态' }} ｜ {{ formatTime(latestSample.sampled_at) }}
              <span v-if="latestSample.last_error" class="danger"> ｜ 采样错误：{{ latestSample.last_error }}</span>
            </template>
            <template v-else>资源指标尚未采集（首次采集需 30s 内完成）。</template>
          </p>
        </n-grid-item>
      </n-grid>
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
import { NButton, NCard, NGrid, NGridItem, NSpace } from 'naive-ui'

import {
  useAppResourcesQuery,
  useAppRuntimeQuery,
  useJobQuery,
  useTriggerRuntimeOperation,
  type AppDTO,
} from '@/api/hooks/useApps'
import type { InstanceResourceSample, ResourceRange } from '@/api/hooks/useRuntimeNodes'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'
import ResourceTrendChart from '@/components/ResourceTrendChart.vue'
import { canManageApp } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppRuntimeTab 展示应用容器运行时信息，并触发 start/stop/restart/delete 操作。
const props = defineProps<{ appId: string }>()
const appId = computed<string | undefined>(() => props.appId)

const app = inject<Ref<AppDTO | null>>('app')
const auth = useAuthStore()

const runtimeQuery = useAppRuntimeQuery(appId)
const runtime = computed(() => runtimeQuery.data.value ?? null)
const rangeOptions: ResourceRange[] = ['1h', '24h', '7d', '30d']
// resourceRange 与资源趋势 query 绑定，默认展示最近 1 小时，便于打开页面后直接查看最新资源波动。
const resourceRange = ref<ResourceRange>('1h')
const resourcesQuery = useAppResourcesQuery(appId, resourceRange)
const resourceSamples = computed(() => resourcesQuery.data.value ?? [])
const latestSample = computed(() => resourceSamples.value.at(-1) ?? null)
const cpuTrendSamples = computed(() => resourceSamples.value.map((sample) => trendSample(sample, 'cpu_percent')))
const memoryTrendSamples = computed(() => resourceSamples.value.map((sample) => ({
  sampled_at: sample.sampled_at,
  value: sample.memory_used_bytes,
  secondary: sample.memory_limit_bytes,
})))
const diskTrendSamples = computed(() => resourceSamples.value.map((sample) => ({
  sampled_at: sample.sampled_at,
  value: sample.disk_read_bytes,
  secondary: sample.disk_write_bytes,
})))
const networkTrendSamples = computed(() => resourceSamples.value.map((sample) => ({
  sampled_at: sample.sampled_at,
  value: sample.network_rx_bytes,
  secondary: sample.network_tx_bytes,
})))

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
const canManage = computed(() => canManageApp(auth.user, app?.value))
const canStart = computed(() => canManage.value && app?.value?.status === 'stopped')
const canStop = computed(() => {
  const status = app?.value?.status
  return canManage.value && (status === 'running' || status === 'binding_waiting')
})
const canDelete = computed(() => canManage.value && app?.value?.status !== 'deleted')

// onAction 对 stop/delete 先弹二次确认，其他操作直接提交运行时任务。
async function onAction(op: 'start' | 'stop' | 'restart' | 'delete') {
  if (op === 'delete') { confirmDelete.value = true; return }
  if (op === 'stop') { confirmStop.value = true; return }
  await runMutation(op)
}

async function onConfirmDelete() { confirmDelete.value = false; await runMutation('delete') }
async function onConfirmStop() { confirmStop.value = false; await runMutation('stop') }

// trendSample 只做字段挑选，缺失指标交给 ResourceTrendChart 保持空点，不在页面层补 0。
function trendSample(sample: InstanceResourceSample, field: 'cpu_percent') {
  return {
    sampled_at: sample.sampled_at,
    value: sample[field],
  }
}

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
