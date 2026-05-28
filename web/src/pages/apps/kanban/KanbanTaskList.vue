<template>
  <n-card :bordered="true" content-style="padding: 0">
    <n-collapse :expanded-names="expandedGroups" @update:expanded-names="onExpandChange">
      <n-collapse-item
        v-for="group in groups"
        :key="group.status"
        :name="group.status"
        :title="`${group.label} (${group.tasks.length})`"
      >
        <KanbanTaskRow
          v-for="task in group.tasks"
          :key="task.id ?? task.title"
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
import { computed, ref, watch } from 'vue'
import { NCard, NCollapse, NCollapseItem } from 'naive-ui'
import { formatKanbanStatus } from '@/domain/status'
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

// 状态分组顺序与看板状态流转保持一致；label 统一由状态格式化函数生成。
const GROUP_DEFS: ReadonlyArray<{ status: KanbanStatus; label: string }> = ([
  'running',
  'ready',
  'todo',
  'blocked',
  'triage',
  'done',
  'archived',
] as const).map((status) => ({
  status,
  label: formatKanbanStatus(status).label,
}))

// groups 把 tasks 按状态分桶。
const groups = computed(() =>
  GROUP_DEFS.map((def) => ({
    ...def,
    tasks: props.tasks.filter((t) => t.status === def.status),
  })),
)

// readExpanded 从 localStorage 读某 appId 的分组折叠态，无记录则给默认展开集。
function readExpanded(appId: string): string[] {
  const saved = localStorage.getItem(`kanban-expanded-${appId}`)
  if (saved) {
    try { return JSON.parse(saved) as string[] } catch { /* 忽略损坏数据 */ }
  }
  return ['running', 'ready', 'todo', 'blocked']
}

// expandedGroups 使用 ref 实现 controlled 模式：
// 用 ref 而非 computed，确保 appId 切换时可通过 watch 重读 localStorage，
// 同时支持 NCollapse :expanded-names 的 controlled 绑定（而非 default-expanded-names 一次性 prop）。
const expandedGroups = ref<string[]>(readExpanded(props.appId))

// 监听 appId 变化，切换实例时重读对应折叠态。
watch(() => props.appId, (id) => { expandedGroups.value = readExpanded(id) })

// onExpandChange 先更新 ref（driven controlled 绑定），再持久化到 localStorage。
function onExpandChange(names: Array<string | number>) {
  expandedGroups.value = names as string[]
  localStorage.setItem(`kanban-expanded-${props.appId}`, JSON.stringify(names))
}
</script>

<style scoped>
.empty-hint {
  padding: 12px 14px;
  color: var(--color-text-secondary, #6b7280);
  font-size: 11px;
  text-align: center;
}
</style>
