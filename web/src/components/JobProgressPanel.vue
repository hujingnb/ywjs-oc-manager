<template>
  <n-card size="small" :bordered="true" style="margin-top: 16px">
    <template #header>
      <div style="display: flex; align-items: center; justify-content: space-between">
        <div>
          <p class="eyebrow">{{ subtitle ?? 'Job' }}</p>
          <strong>{{ title }}</strong>
        </div>
        <n-tag :type="tagType" size="small" :bordered="false">{{ labelFor(job?.status) }}</n-tag>
      </div>
    </template>

    <div v-if="!job" style="color: var(--color-text-secondary); font-size: 13px">尚未触发任务</div>
    <n-descriptions v-else :column="2" size="small" label-style="color:var(--color-text-secondary)" content-style="font-weight:600">
      <n-descriptions-item label="类型">{{ job.type }}</n-descriptions-item>
      <n-descriptions-item label="尝试次数">{{ job.attempts }} / {{ job.max_attempts }}</n-descriptions-item>
      <n-descriptions-item label="下一次执行">{{ formatTime(job.run_after) }}</n-descriptions-item>
      <n-descriptions-item label="完成时间">{{ formatTime(job.finished_at) }}</n-descriptions-item>
      <n-descriptions-item v-if="job.last_error" label="最近错误" :span="2">
        <span style="color: var(--color-danger-text)">{{ job.last_error }}</span>
      </n-descriptions-item>
    </n-descriptions>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NDescriptions, NDescriptionsItem, NTag } from 'naive-ui'

// JobProgressPanel 展示异步任务的执行状态、重试次数和最近错误。
// job 为空表示当前页面尚未触发任务，而不是查询失败。
const props = defineProps<{
  title: string
  subtitle?: string
  job?: {
    id: string; type: string; status: string; attempts: number; max_attempts: number;
    run_after?: string | null; finished_at?: string | null; last_error?: string
  } | null
}>()

// statusViews 是任务状态到中文文案和标签色的局部映射；未知状态保留原值便于排查。
const statusViews: Record<string, { label: string; tone: string }> = {
  pending: { label: '待执行', tone: 'warning' },
  running: { label: '执行中', tone: 'warning' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'error' },
  canceled: { label: '已取消', tone: 'default' },
}

// labelFor 负责处理未触发和未知状态两种降级展示。
function labelFor(status?: string) {
  return status ? (statusViews[status]?.label ?? status) : '未触发'
}

// tagType 将任务 tone 收敛到 Naive UI 支持的 NTag 类型。
const tagType = computed(() => {
  const tone = statusViews[props.job?.status ?? '']?.tone ?? 'default'
  return tone as 'success' | 'warning' | 'error' | 'default'
})

// formatTime 对后端时间做本地化展示；解析失败时保留原字符串便于定位异常数据。
function formatTime(value?: string | null) {
  if (!value) return '—'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN', { hour12: false })
}

void props
</script>
