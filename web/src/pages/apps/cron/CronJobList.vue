<template>
  <n-card :bordered="true" content-style="padding: 0">
    <!-- 空列表保留最小高度，避免加载完成后左右分屏高度明显跳动。 -->
    <n-empty v-if="jobs.length === 0" class="list-empty" description="暂无定时任务" />
    <div v-else class="table-wrap">
      <table class="job-table">
        <thead>
          <tr>
            <th>名称</th>
            <th>调度</th>
            <th>状态</th>
            <th>投递</th>
            <th>下次执行</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="job in jobs"
            :key="job.id ?? job.name"
            class="job-row"
            :class="{ selected: job.id === selectedId }"
            @click="onSelect(job)"
          >
            <td class="name-cell">
              <span class="job-name">{{ job.name || '未命名任务' }}</span>
              <code>{{ job.id || '—' }}</code>
            </td>
            <td>{{ scheduleText(job) }}</td>
            <td>
              <n-tag size="small" :type="stateTagType(job.state)">
                {{ job.state || 'unknown' }}
              </n-tag>
            </td>
            <td>{{ job.deliver || '—' }}</td>
            <td>{{ formatTime(job.next_run_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { NCard, NEmpty, NTag } from 'naive-ui'

import type { CronJob } from '@/api/hooks/useCron'

// CronJobList 渲染 Cron 任务左侧紧凑列表；选择态只改变背景和阴影，不改变行高。
const props = defineProps<{
  // jobs 是父组件已按搜索 / 状态筛选后的列表。
  jobs: CronJob[]
  // selectedId 来自 URL query.job，用于刷新后恢复选择态。
  selectedId?: string
}>()

const emit = defineEmits<{
  // select 只向上传递后端任务 ID；缺少 ID 的异常行不可选。
  select: [jobId: string]
}>()

// scheduleText 优先展示后端规整的 display，缺失时退回机器表达式。
function scheduleText(job: CronJob): string {
  return job.schedule?.display || job.schedule?.expr || '—'
}

// formatTime 仅负责 UI 兜底；后端保留原始 ISO 字符串，页面不做时区转换。
function formatTime(value: string | undefined): string {
  return value || '—'
}

// stateTagType 把常见 Cron 状态映射到 Naive UI 语义色，未知状态保持默认色。
function stateTagType(state: string | undefined): 'success' | 'warning' | 'error' | 'default' {
  if (state === 'scheduled' || state === 'running') return 'success'
  if (state === 'paused' || state === 'disabled') return 'warning'
  if (state === 'removed' || state === 'error') return 'error'
  return 'default'
}

// onSelect 防御旧数据缺少 id 的情况，避免把空 job 写入 URL query。
function onSelect(job: CronJob) {
  if (!job.id) return
  emit('select', job.id)
}
</script>

<style scoped>
.list-empty {
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.table-wrap {
  overflow-x: auto;
}
.job-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 12px;
}
.job-table th {
  color: var(--color-text-secondary, #6b7280);
  font-weight: 500;
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
}
.job-table td {
  padding: 9px 10px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
  min-height: 48px;
  vertical-align: middle;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.job-table th:nth-child(1),
.job-table td:nth-child(1) { width: 32%; }
.job-table th:nth-child(2),
.job-table td:nth-child(2) { width: 24%; }
.job-table th:nth-child(3),
.job-table td:nth-child(3) { width: 14%; }
.job-table th:nth-child(4),
.job-table td:nth-child(4) { width: 12%; }
.job-table th:nth-child(5),
.job-table td:nth-child(5) { width: 18%; }
.job-row {
  cursor: pointer;
  transition: background 0.15s, box-shadow 0.15s;
}
.job-row:hover {
  background: var(--color-surface-muted, #fbfcfd);
}
.job-row.selected {
  background: var(--color-brand-soft);
  box-shadow: inset 3px 0 0 var(--color-brand);
}
.name-cell {
  display: grid;
  gap: 3px;
}
.job-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
}
.name-cell code {
  color: var(--color-text-secondary, #6b7280);
  font-size: 10px;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
