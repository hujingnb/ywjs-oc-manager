<template>
  <div
    class="task-row"
    :class="{ selected: selected }"
    @click="task.id && emit('select', task.id)"
  >
    <div class="row-title">{{ task.title }}</div>
    <div class="row-meta">
      <n-tag v-if="task.assignee" size="tiny" type="info">{{ task.assignee }}</n-tag>
      <n-tag v-if="(task.priority ?? 0) >= 2" size="tiny" :type="priorityType">{{ priorityLabel }}</n-tag>
      <span class="row-time">{{ relativeTime }}</span>
    </div>
    <div v-if="latestEvent" class="row-running">● {{ latestEvent }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import type { KanbanTask } from '@/api/hooks/useKanban'

// KanbanTaskRow 渲染左侧列表的单个任务行。
const props = defineProps<{
  task: KanbanTask
  selected: boolean
  latestEvent?: string // running 任务的最新事件预览，由父组件按事件流注入
}>()
const emit = defineEmits<{ select: [taskId: string] }>()

// priorityType / priorityLabel：priority>=3 高(红)、==2 中(橙)。
const priorityType = computed(() => ((props.task.priority ?? 0) >= 3 ? 'error' : 'warning'))
const priorityLabel = computed(() => ((props.task.priority ?? 0) >= 3 ? 'high' : 'medium'))

// relativeTime：把 created_at（秒级 epoch）转成相对时间中文。
// created_at 缺失时返回占位符，避免 fallback 到 epoch(0) 显示「20000+ 天前」。
const relativeTime = computed(() => {
  if (!props.task.created_at) return '—'
  const diff = Date.now() / 1000 - props.task.created_at
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
})
</script>

<style scoped>
.task-row {
  padding: 10px 14px;
  border-left: 2px solid transparent;
  cursor: pointer;
}
.task-row:hover { background: var(--n-color-embedded, #1f1f24); }
.task-row.selected {
  background: var(--n-color-embedded, #1f1f24);
  border-left-color: var(--primary-color, #18a058);
}
.row-title {
  font-size: 13px;
  font-weight: 500;
  margin-bottom: 5px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.row-meta { display: flex; align-items: center; gap: 5px; }
.row-time { margin-left: auto; color: var(--n-text-color-3, #707078); font-size: 11px; }
.row-running {
  margin-top: 6px;
  font-size: 10px;
  color: var(--primary-color, #18a058);
  font-family: ui-monospace, monospace;
}
</style>
