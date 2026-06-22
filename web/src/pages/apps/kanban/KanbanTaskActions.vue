<template>
  <n-space :size="6">
    <n-button v-if="show('complete') && verbSupported('complete')" size="small" type="primary" @click="emit('action', 'complete')">{{ t('apps.kanban.taskActions.complete') }}</n-button>
    <n-button v-if="show('block') && verbSupported('block')" size="small" @click="emit('action', 'block')">{{ t('apps.kanban.taskActions.block') }}</n-button>
    <n-button v-if="show('unblock') && verbSupported('unblock')" size="small" type="primary" @click="emit('action', 'unblock')">{{ t('apps.kanban.taskActions.unblock') }}</n-button>
    <n-button v-if="show('reclaim') && verbSupported('reclaim')" size="small" @click="emit('action', 'reclaim')">{{ t('apps.kanban.taskActions.reclaim') }}</n-button>
    <n-button v-if="show('reassign') && verbSupported('reassign')" size="small" @click="emit('action', 'reassign')">{{ t('apps.kanban.taskActions.reassign') }}</n-button>
    <n-button v-if="show('comment') && verbSupported('comment')" size="small" @click="emit('action', 'comment')">{{ t('apps.kanban.taskActions.comment') }}</n-button>
    <n-button v-if="show('archive') && verbSupported('archive')" size="small" type="error" @click="emit('action', 'archive')">{{ t('apps.kanban.taskActions.archive') }}</n-button>
  </n-space>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NSpace } from 'naive-ui'
import type { KanbanStatus } from '@/api/hooks/useKanban'
import { useKanbanCapabilitiesQuery } from '@/api/hooks/useKanban'

// KanbanTaskActions 按任务状态决定显示哪些操作按钮（spec §5.4 操作矩阵）。
// status prop 为 KanbanStatus，由父组件 KanbanTaskDetail 通过 isKnownStatus 类型守卫
// 保证传入的 status 是合法 KanbanStatus，v-if="isKnownStatus(task.status)" 之后才渲染本组件。
// appId 透传自 AppKanbanTab，用于查询 oc-kanban capabilities 以隐藏不支持的操作。
const props = defineProps<{
  // status 是当前任务的状态，用于查 ACTION_MATRIX 决定显示哪些按钮。
  status: KanbanStatus
  // appId 用于查询 capabilities；可选，缺失时 capabilities 不加载，所有按钮默认显示。
  appId?: string
}>()

// emit 的 verb 字符串与 KanbanWriteAction 的 verb 字段一致：
// comment / complete / block / unblock / archive / reassign / reclaim
const emit = defineEmits<{
  // action 事件携带操作动词，父组件负责弹框收集额外参数后调用 mutation。
  action: [verb: string]
}>()

const { t } = useI18n()

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

// show 判断某操作按钮在当前状态下是否显示（基于状态矩阵）。
// 类型上 status 必为合法 KanbanStatus，ACTION_MATRIX 已覆盖所有值；
// ?. 仅为额外防御，防止运行时意外情形，不代表 status 可能不在矩阵中。
function show(verb: string): boolean {
  return ACTION_MATRIX[props.status]?.includes(verb) ?? false
}

// appId 将 appId prop 包装为 Ref<string | undefined>，满足 useKanbanCapabilitiesQuery 参数要求。
const appId = computed(() => props.appId)

// capabilitiesQuery 探测 oc-kanban 能力，appId 缺失时 enabled=false 不发起请求。
const capabilitiesQuery = useKanbanCapabilitiesQuery(appId)

// verbSupported 判定某操作是否可用：能力未知时默认可用。
// 降级语义：verbs 未知时默认可用（返回 true），只有明确不在 verbs 列表中才隐藏。
function verbSupported(verb: string): boolean {
  const verbs = capabilitiesQuery.data.value?.verbs
  return !verbs || verbs.includes(verb)
}
</script>
