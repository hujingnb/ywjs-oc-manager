<template>
  <n-card :bordered="true" content-style="padding: 0">
    <n-collapse :default-expanded-names="expandedGroups" @update:expanded-names="onExpandChange">
      <n-collapse-item
        v-for="group in groups"
        :key="group.status"
        :name="group.status"
        :title="`${group.label} (${group.tasks.length})`"
      >
        <KanbanTaskRow
          v-for="task in group.tasks"
          :key="task.id"
          :task="task"
          :selected="task.id === selectedId"
          :latest-event="latestEvents[task.id ?? '']"
          @select="emit('select', $event)"
        />
        <p v-if="group.tasks.length === 0" class="empty-hint">无任务</p>
      </n-collapse-item>
    </n-collapse>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NCollapse, NCollapseItem } from 'naive-ui'
import KanbanTaskRow from './KanbanTaskRow.vue'
import type { KanbanTask, KanbanStatus } from '@/api/hooks/useKanban'

// KanbanTaskList 把任务按状态分组渲染为可折叠列表。
const props = defineProps<{
  tasks: KanbanTask[]
  selectedId?: string
  appId: string
  latestEvents: Record<string, string> // taskId → 最新事件预览文本
}>()
const emit = defineEmits<{ select: [taskId: string] }>()

// 状态分组顺序与中文标签。
const GROUP_DEFS: ReadonlyArray<{ status: KanbanStatus; label: string }> = [
  { status: 'running', label: 'Running' },
  { status: 'ready', label: 'Ready' },
  { status: 'todo', label: 'Todo' },
  { status: 'blocked', label: 'Blocked' },
  { status: 'triage', label: 'Triage' },
  { status: 'done', label: 'Done' },
  { status: 'archived', label: 'Archived' },
]

// groups 把 tasks 按状态分桶。
const groups = computed(() =>
  GROUP_DEFS.map((def) => ({
    ...def,
    tasks: props.tasks.filter((t) => t.status === def.status),
  })),
)

// 折叠态 localStorage key（含 appId，按实例隔离）。
const storageKey = computed(() => `kanban-expanded-${props.appId}`)

// expandedGroups 初值：localStorage 有则用，否则默认展开活跃状态。
const expandedGroups = computed<string[]>(() => {
  const saved = localStorage.getItem(storageKey.value)
  if (saved) {
    try { return JSON.parse(saved) as string[] } catch { /* 忽略损坏数据 */ }
  }
  return ['running', 'ready', 'todo', 'blocked']
})

// onExpandChange 持久化折叠态。
function onExpandChange(names: Array<string | number>) {
  localStorage.setItem(storageKey.value, JSON.stringify(names))
}
</script>

<style scoped>
.empty-hint {
  padding: 12px 14px;
  color: var(--n-text-color-3, #707078);
  font-size: 11px;
  text-align: center;
}
</style>
