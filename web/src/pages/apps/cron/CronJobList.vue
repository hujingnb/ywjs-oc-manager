<template>
  <n-card :bordered="true" content-style="padding: 0">
    <!-- 空列表保留最小高度，避免加载完成后左右分屏高度明显跳动。 -->
    <n-empty v-if="jobs.length === 0" class="list-empty" :description="t('apps.cron.list.empty')" />
    <div v-else class="card-list">
      <div
        v-for="job in jobs"
        :key="job.id ?? job.name"
        class="job-card"
        :class="{ selected: job.id === selectedId }"
        @click="onSelect(job)"
      >
        <!-- 第一行：任务名称 + 中文状态标签，名称过长才省略，状态始终完整。 -->
        <div class="card-head">
          <span class="job-name">{{ job.name || t('apps.cron.list.unnamed') }}</span>
          <n-tag size="small" :type="stateTagType(job.state)">{{ translateState(job.state) }}</n-tag>
        </div>
        <!-- 次要灰色小字展示 job_id，便于排查。 -->
        <code class="job-id">{{ job.id || '—' }}</code>
        <!-- 调度走统一展示入口：上游 display 优先，缺失时前端兜底翻译。 -->
        <div class="card-row">
          <span class="k">{{ t('apps.cron.list.schedule') }}</span>
          <span class="v">{{ scheduleDisplay(job.schedule) }}</span>
        </div>
        <!-- 下次执行与投递渠道同行展示，投递中文化。 -->
        <div class="card-row">
          <span class="k">{{ t('apps.cron.list.next') }}</span>
          <span class="v">{{ formatTime(job.next_run_at) }} · {{ translateDeliver(job.deliver) }}</span>
        </div>
      </div>
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { NCard, NEmpty, NTag } from 'naive-ui'

import type { CronJob } from '@/api/hooks/useCron'
import { scheduleDisplay, translateDeliver, translateState } from './cronDisplay'

const { t } = useI18n()

// CronJobList 渲染 Cron 任务左侧卡片列表；选择态只改变背景和左侧色条，不改变卡片结构。
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
.card-list {
  display: flex;
  flex-direction: column;
}
.job-card {
  display: grid;
  gap: 4px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: background 0.15s, border-color 0.15s;
}
.job-card:last-child {
  border-bottom: none;
}
.job-card:hover {
  background: var(--color-surface-muted, #fbfcfd);
}
.job-card.selected {
  background: var(--color-brand-soft);
  border-left-color: var(--color-brand);
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.job-name {
  font-weight: 600;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  /* flex 子项默认 min-width: auto，会阻止 overflow:hidden 生效，长名称会挤掉状态标签 */
  min-width: 0;
}
.job-id {
  color: var(--color-text-secondary, #6b7280);
  font-size: 11px;
}
.card-row {
  display: flex;
  gap: 8px;
  font-size: 12px;
  line-height: 1.5;
}
.card-row .k {
  color: var(--color-text-secondary, #6b7280);
  flex: 0 0 28px;
}
.card-row .v {
  color: var(--color-text-primary, #1f2329);
  /* 用 break-word 而非 break-all：避免把 ISO 时间戳等连续 ASCII 串在字符中间断开。 */
  overflow-wrap: break-word;
  word-break: break-word;
}
</style>
