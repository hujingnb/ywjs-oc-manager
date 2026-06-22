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
        <p v-if="group.tasks.length === 0" class="empty-hint">{{ t('apps.kanban.taskList.empty') }}</p>
      </n-collapse-item>
    </n-collapse>
  </n-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NCollapse, NCollapseItem } from 'naive-ui'
import { formatKanbanStatus } from '@/domain/status'
import KanbanTaskRow from './KanbanTaskRow.vue'
import type { KanbanTask, KanbanStatus } from '@/api/hooks/useKanban'

const { t } = useI18n()

// KanbanTaskList 把任务按状态分组渲染为可折叠列表。
const props = defineProps<{
  tasks: KanbanTask[]
  selectedId?: string
  appId: string
  latestEvents: Record<string, string> // taskId → 最新事件预览文本
}>()
const emit = defineEmits<{ select: [taskId: string] }>()

// KANBAN_STATUSES 固定看板状态排列顺序，与状态流转保持一致。
// label 键由 formatKanbanStatus 在 groups computed 内通过 t() 动态解析，确保语言切换时响应式更新。
const KANBAN_STATUSES = ['running', 'ready', 'todo', 'blocked', 'triage', 'done', 'archived'] as const satisfies ReadonlyArray<KanbanStatus>

// KNOWN_STATUS_SET 用于识别 Hermes 约定状态；未知状态会追加为降级分组，避免任务被隐藏。
const KNOWN_STATUS_SET = new Set<string>(KANBAN_STATUSES)

// taskStatusKey 统一处理任务状态缺失的边界，确保列表里没有任务因为 status 为空而丢失。
function taskStatusKey(task: KanbanTask): string {
  return task.status || 'unknown'
}

// groups 把 tasks 按状态分桶；已知状态保持固定顺序，未知状态按接口返回的首次出现顺序追加。
// label 通过 t(view.label, view.params) 解析为当前语言文案，确保语言切换时分组标题响应式更新。
const groups = computed(() => {
  const knownGroups = KANBAN_STATUSES.map((status) => {
    const view = formatKanbanStatus(status)
    return {
      status,
      label: t(view.label, view.params ?? {}),
      tasks: props.tasks.filter((task) => taskStatusKey(task) === status),
    }
  })
  const unknownGroups = new Map<string, KanbanTask[]>()
  for (const task of props.tasks) {
    const status = taskStatusKey(task)
    if (KNOWN_STATUS_SET.has(status)) continue
    const tasks = unknownGroups.get(status) ?? []
    tasks.push(task)
    unknownGroups.set(status, tasks)
  }
  return [
    ...knownGroups,
    ...Array.from(unknownGroups, ([status, tasks]) => {
      const view = formatKanbanStatus(status)
      return {
        status,
        label: t(view.label, view.params ?? {}),
        tasks,
      }
    }),
  ]
})

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
