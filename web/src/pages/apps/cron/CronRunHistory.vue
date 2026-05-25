<template>
  <div class="history">
    <p v-if="runs.length === 0" class="state-text">暂无执行历史。</p>
    <button
      v-for="(run, index) in runs"
      v-else
      :key="run.file_name || `${run.run_time}-${index}`"
      class="run-row"
      :class="{ selected: canSelectOutput(run) && run.file_name === selectedFile }"
      :disabled="!canSelectOutput(run)"
      @click="onSelect(run)"
    >
      <span>
        <strong>{{ run.run_time || '未知时间' }}</strong>
        <small>{{ run.status || 'unknown' }}</small>
      </span>
      <span class="run-output">{{ outputText(run) }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import type { CronRunEntry } from '@/api/hooks/useCron'

// CronRunHistory 展示单个 Cron 任务的执行历史，并只允许选择真实存在的输出文件。
const props = defineProps<{
  // runs 由 oc-cron history 返回；synthetic 行可能没有 file_name。
  runs: CronRunEntry[]
  // selectedFile 来自 URL query.file，用于恢复输出选择态。
  selectedFile?: string
}>()

const emit = defineEmits<{
  // select 仅在 file_name 存在且 has_output 未明确为 false 时触发。
  select: [fileName: string]
}>()

// canSelectOutput 判断该执行记录是否有可读取输出；synthetic / 无文件行不可点击。
function canSelectOutput(run: CronRunEntry): run is CronRunEntry & { file_name: string } {
  return Boolean(run.file_name && run.has_output !== false)
}

// outputText 为无输出文件的 synthetic 行提供明确文案，避免用户误以为是加载失败。
function outputText(run: CronRunEntry): string {
  if (!canSelectOutput(run)) return '无输出文件'
  const size = typeof run.size === 'number' && run.size > 0 ? ` · ${run.size} B` : ''
  return `${run.file_name}${size}`
}

// onSelect 防止 disabled button 被脚本触发时仍向父组件发出无效文件名。
function onSelect(run: CronRunEntry) {
  if (!canSelectOutput(run)) return
  emit('select', run.file_name)
}
</script>

<style scoped>
.history {
  display: grid;
  gap: 6px;
}
.run-row {
  width: 100%;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(120px, 220px);
  align-items: center;
  gap: 8px;
  min-height: 42px;
  padding: 8px 10px;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 4px;
  background: transparent;
  color: var(--color-text-primary, #1f2329);
  cursor: pointer;
  text-align: left;
}
.run-row:disabled {
  cursor: default;
  opacity: 0.64;
}
.run-row.selected {
  border-color: var(--color-brand);
  background: var(--color-brand-soft);
}
.run-row strong,
.run-row small,
.run-output {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-row small {
  color: var(--color-text-secondary, #6b7280);
  margin-top: 2px;
}
.run-output {
  color: var(--color-text-secondary, #6b7280);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  text-align: right;
}
.state-text {
  color: var(--color-text-secondary, #6b7280);
  font-size: 13px;
}
</style>
