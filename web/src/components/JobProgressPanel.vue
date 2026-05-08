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

    <div v-if="!job" style="color: #8A94C6; font-size: 13px">尚未触发任务</div>
    <n-descriptions v-else :column="2" size="small" label-style="color:#8A94C6" content-style="font-weight:600">
      <n-descriptions-item label="类型">{{ job.type }}</n-descriptions-item>
      <n-descriptions-item label="尝试次数">{{ job.attempts }} / {{ job.max_attempts }}</n-descriptions-item>
      <n-descriptions-item label="下一次执行">{{ formatTime(job.run_after) }}</n-descriptions-item>
      <n-descriptions-item label="完成时间">{{ formatTime(job.finished_at) }}</n-descriptions-item>
      <n-descriptions-item v-if="job.last_error" label="最近错误" :span="2">
        <span style="color: #FF3B5C">{{ job.last_error }}</span>
      </n-descriptions-item>
    </n-descriptions>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NDescriptions, NDescriptionsItem, NTag } from 'naive-ui'

const props = defineProps<{
  title: string
  subtitle?: string
  job?: {
    id: string; type: string; status: string; attempts: number; max_attempts: number;
    run_after?: string | null; finished_at?: string | null; last_error?: string
  } | null
}>()

const statusViews: Record<string, { label: string; tone: string }> = {
  pending: { label: '待执行', tone: 'warning' },
  running: { label: '执行中', tone: 'warning' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'error' },
  canceled: { label: '已取消', tone: 'default' },
}

function labelFor(status?: string) {
  return status ? (statusViews[status]?.label ?? status) : '未触发'
}

const tagType = computed(() => {
  const tone = statusViews[props.job?.status ?? '']?.tone ?? 'default'
  return tone as 'success' | 'warning' | 'error' | 'default'
})

function formatTime(value?: string | null) {
  if (!value) return '—'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN', { hour12: false })
}

void props
</script>
