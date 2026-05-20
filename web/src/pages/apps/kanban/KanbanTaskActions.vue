<template>
  <n-space :size="6">
    <n-button v-if="show('complete')" size="small" type="primary" @click="emit('action', 'complete')">标记完成</n-button>
    <n-button v-if="show('block')" size="small" @click="emit('action', 'block')">阻塞</n-button>
    <n-button v-if="show('unblock')" size="small" type="primary" @click="emit('action', 'unblock')">解除阻塞</n-button>
    <n-button v-if="show('reclaim')" size="small" @click="emit('action', 'reclaim')">释放 claim</n-button>
    <n-button v-if="show('reassign')" size="small" @click="emit('action', 'reassign')">重新分配</n-button>
    <n-button v-if="show('comment')" size="small" @click="emit('action', 'comment')">评论</n-button>
    <n-button v-if="show('archive')" size="small" type="error" @click="emit('action', 'archive')">归档</n-button>
  </n-space>
</template>

<script setup lang="ts">
import { NButton, NSpace } from 'naive-ui'
import type { KanbanStatus } from '@/api/hooks/useKanban'

// KanbanTaskActions 按任务状态决定显示哪些操作按钮（spec §5.4 操作矩阵）。
// status prop 为 KanbanStatus，由父组件 KanbanTaskDetail 在 v-if="task" 保护后传入，
// 从而保证此处拿到的 status 已是已知非空值。
const props = defineProps<{
  // status 是当前任务的状态，用于查 ACTION_MATRIX 决定显示哪些按钮。
  status: KanbanStatus
}>()

// emit 的 verb 字符串与 KanbanWriteAction 的 verb 字段一致：
// comment / complete / block / unblock / archive / reassign / reclaim
const emit = defineEmits<{
  // action 事件携带操作动词，父组件负责弹框收集额外参数后调用 mutation。
  action: [verb: string]
}>()

// ACTION_MATRIX 规定每个状态下可执行的操作集合（spec §5.4 矩阵）。
// key 为任务状态，value 为该状态下可见的操作 verb 列表。
const ACTION_MATRIX: Record<KanbanStatus, string[]> = {
  triage: ['comment', 'block', 'archive', 'reassign'],
  todo: ['comment', 'block', 'archive', 'reassign'],
  ready: ['comment', 'block', 'archive', 'reassign'],
  running: ['comment', 'complete', 'block', 'reclaim', 'archive'],
  blocked: ['comment', 'unblock', 'archive', 'reassign'],
  done: ['comment', 'archive'],
  archived: ['comment'],
}

// show 判断某操作按钮在当前状态下是否显示。
// 使用 ?. 防御性访问：若 status 在 ACTION_MATRIX 中不存在则返回 false。
function show(verb: string): boolean {
  return ACTION_MATRIX[props.status]?.includes(verb) ?? false
}
</script>
