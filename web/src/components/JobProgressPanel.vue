<template>
  <article class="panel job-panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">{{ subtitle ?? 'Job' }}</p>
        <h2>{{ title }}</h2>
      </div>
      <span :class="['status-pill', toneFor(job?.status)]">
        {{ labelFor(job?.status) }}
      </span>
    </div>

    <div v-if="!job" class="state-text">尚未触发任务</div>
    <dl v-else class="job-grid">
      <div>
        <dt>类型</dt>
        <dd>{{ job.type }}</dd>
      </div>
      <div>
        <dt>尝试次数</dt>
        <dd>{{ job.attempts }} / {{ job.max_attempts }}</dd>
      </div>
      <div>
        <dt>下一次执行</dt>
        <dd>{{ formatTime(job.run_after) }}</dd>
      </div>
      <div>
        <dt>完成时间</dt>
        <dd>{{ formatTime(job.finished_at) }}</dd>
      </div>
      <div v-if="job.last_error" class="job-error">
        <dt>最近错误</dt>
        <dd>{{ job.last_error }}</dd>
      </div>
    </dl>
  </article>
</template>

<script setup lang="ts">
const props = defineProps<{
  title: string
  subtitle?: string
  job?: {
    id: string
    type: string
    status: string
    attempts: number
    max_attempts: number
    run_after?: string | null
    finished_at?: string | null
    last_error?: string
  } | null
}>()

const statusViews: Record<string, { label: string; tone: 'success' | 'warning' | 'danger' | 'neutral' }> = {
  pending: { label: '待执行', tone: 'warning' },
  running: { label: '执行中', tone: 'warning' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'danger' },
  canceled: { label: '已取消', tone: 'neutral' },
}

function labelFor(status?: string) {
  if (!status) return '未触发'
  return statusViews[status]?.label ?? status
}

function toneFor(status?: string) {
  if (!status) return 'neutral'
  return statusViews[status]?.tone ?? 'neutral'
}

function formatTime(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

// 维持 props 引用，避免被意外摇树。
void props
</script>

<style scoped>
.job-panel {
  margin-top: 16px;
}

.job-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 14px;
}

.job-grid div {
  border: 1px solid #e4eaf2;
  border-radius: 8px;
  padding: 12px;
  background: #f8fafc;
}

.job-grid dt {
  margin: 0 0 4px;
  color: #66758a;
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.job-grid dd {
  margin: 0;
  color: #172033;
  font-weight: 600;
  word-break: break-all;
}

.job-error {
  grid-column: 1 / -1;
  background: #fff4f0;
}
</style>
