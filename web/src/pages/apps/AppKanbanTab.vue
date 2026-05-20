<template>
  <div class="kanban-tab">
    <!-- 工具栏：board 选择 / 任务搜索 / 实时状态指示 / 新建任务按钮 -->
    <n-card :bordered="true" class="toolbar-card">
      <n-space align="center" :size="8">
        <n-select
          v-model:value="currentBoard"
          :options="boardOptions"
          size="small"
          style="width: 180px"
        />
        <n-input v-model:value="search" size="small" placeholder="搜索任务标题" style="width: 200px" />
        <span class="spacer" />
        <!-- streamConnected 为 true 时显示绿点「实时」，否则显示「重连实时流」按钮 -->
        <span v-if="streamConnected" class="live-tag">● 实时</span>
        <n-button v-else size="small" tertiary @click="reconnectStream">重连实时流</n-button>
        <n-button size="small" type="primary" @click="showCreate = true">+ 新建任务</n-button>
      </n-space>
    </n-card>

    <!-- stub 镜像降级提示：当后端返回 KANBAN_NOT_SUPPORTED_ON_STUB 时显示 -->
    <n-card v-if="isStubInstance" :bordered="true">
      <n-empty description="该实例运行的是本地 dev 镜像，任务看板不可用；切换到生产镜像后该功能自动启用。" />
    </n-card>

    <!-- 左右分屏：左侧任务列表 + 右侧详情面板 -->
    <div v-else class="split">
      <div class="list-col">
        <!-- 加载中状态 -->
        <p v-if="tasksQuery.isLoading.value" class="state-text">加载中…</p>
        <!-- 非 stub 的加载错误 -->
        <p v-else-if="tasksQuery.error.value" class="state-text danger">{{ errorText }}</p>
        <!-- 任务列表：按状态分组、可折叠 -->
        <KanbanTaskList
          v-else
          :tasks="filteredTasks"
          :selected-id="selectedTaskId"
          :app-id="appId"
          :latest-events="latestEvents"
          @select="onSelect"
        />
      </div>
      <div class="detail-col">
        <!-- 任务详情面板：task 为 null 时显示「从左侧选择任务」引导文案 -->
        <KanbanTaskDetail
          :task="taskQuery.data.value ?? null"
          :board="currentBoard"
          :runs="runsQuery.data.value ?? []"
          :live-events="selectedLiveEvents"
          @action="onAction"
        />
      </div>
    </div>

    <!-- 新建任务模态框 -->
    <KanbanCreateModal
      v-model:show="showCreate"
      :submitting="createMutation.isPending.value"
      @submit="onCreate"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NSpace, NSelect, NInput, NButton, NEmpty, useMessage } from 'naive-ui'
import KanbanTaskList from './kanban/KanbanTaskList.vue'
import KanbanTaskDetail from './kanban/KanbanTaskDetail.vue'
import KanbanCreateModal from './kanban/KanbanCreateModal.vue'
import {
  useKanbanBoardsQuery,
  useKanbanTasksQuery,
  useKanbanTaskQuery,
  useKanbanRunsQuery,
  useCreateKanbanTask,
  useKanbanTaskAction,
} from '@/api/hooks/useKanban'
import { useKanbanEventStream } from './kanban/useKanbanEventStream'
import type { ApiError } from '@/api/client'

// AppKanbanTab 是实例任务看板顶层组件，组装工具栏 + 左右分屏 + 写操作 + stub 降级提示。
// appId 由路由 props: true 注入。
const props = defineProps<{ appId: string }>()
// 转为 Ref 供 composable 使用（composable 接受 Ref<string | undefined>）。
const appId = computed(() => props.appId)
const route = useRoute()
const router = useRouter()
const message = useMessage()

// ─── URL query 同步 ──────────────────────────────────────────────────────────
// currentBoard：board slug 与 URL query.board 双向同步，默认 'default'。
const currentBoard = computed<string>({
  get: () => (route.query.board as string) || 'default',
  set: (v) => router.replace({ query: { ...route.query, board: v } }),
})
// selectedTaskId：选中任务 ID 与 URL query.task 同步。
const selectedTaskId = computed<string | undefined>(() => route.query.task as string | undefined)
// search：本地搜索关键词，不持久化到 URL。
const search = ref('')
// showCreate：新建任务模态框显隐状态。
const showCreate = ref(false)

// ─── 数据查询 ────────────────────────────────────────────────────────────────
// boardsQuery：拉取实例的所有 board，供工具栏下拉选择。
const boardsQuery = useKanbanBoardsQuery(appId)
// tasksQuery：拉取当前 board 的任务列表，每 5s 轮询一次。
const tasksQuery = useKanbanTasksQuery(appId, currentBoard)
// taskIdRef 是 computed Ref，供 useKanbanTaskQuery / useKanbanRunsQuery 响应式使用。
const taskIdRef = computed(() => selectedTaskId.value)
// taskQuery：拉取选中任务的完整详情（含评论、事件等）。
const taskQuery = useKanbanTaskQuery(appId, currentBoard, taskIdRef)
// runsQuery：拉取选中任务的历次执行记录。
const runsQuery = useKanbanRunsQuery(appId, currentBoard, taskIdRef)

// ─── Stub 降级判断 ───────────────────────────────────────────────────────────
// isStubInstance：后端返回 KANBAN_NOT_SUPPORTED_ON_STUB code 时降级展示提示卡片。
// 后端 ErrorResponse 结构：{ code: string, message: string }。
// ApiError.message 是从 body.message 提取的中文文案，code 在 body.code 里。
// 必须读 body.code 而非 error.message 来判断 stub 错误。
const isStubInstance = computed(() => {
  const err = tasksQuery.error.value
  if (!err) return false
  const body = (err as ApiError).body
  if (body && typeof body === 'object' && 'code' in body) {
    return (body as { code: string }).code === 'KANBAN_NOT_SUPPORTED_ON_STUB'
  }
  return false
})
// errorText：非 stub 的加载错误文本，直接显示给用户。
const errorText = computed(() => String(tasksQuery.error.value?.message ?? '加载失败'))

// ─── Board 下拉选项 ───────────────────────────────────────────────────────────
// boardOptions：若 boards 未加载完成则显示 default 占位，防止下拉为空。
const boardOptions = computed(() =>
  (boardsQuery.data.value ?? [{ slug: 'default', name: 'default' }]).map((b) => ({
    label: b.name || b.slug || 'default',
    value: b.slug || 'default',
  })),
)

// ─── 任务搜索过滤 ─────────────────────────────────────────────────────────────
// filteredTasks：按 search 关键词过滤任务标题（大小写不敏感）。
// title 为可选字段，用 ?? '' 防御。
const filteredTasks = computed(() => {
  const all = tasksQuery.data.value ?? []
  const q = search.value.trim().toLowerCase()
  if (!q) return all
  return all.filter((t) => (t.title ?? '').toLowerCase().includes(q))
})

// ─── 实时事件流 ───────────────────────────────────────────────────────────────
// useKanbanEventStream 连接 SSE 端点，按 task_id 分发事件。
// board 变化时自动重连，组件卸载时自动关闭连接。
const {
  latestEvents,
  eventsByTask,
  connected: streamConnected,
  reconnect: reconnectStream,
} = useKanbanEventStream(appId, currentBoard)
// selectedLiveEvents：当前选中任务的实时事件文本行，注入给 KanbanTaskDetail。
const selectedLiveEvents = computed(() =>
  selectedTaskId.value ? (eventsByTask.value[selectedTaskId.value] ?? []) : [],
)

// ─── 交互逻辑 ─────────────────────────────────────────────────────────────────
// onSelect：点击左侧任务行，把 task ID 写入 URL query.task，实现刷新保留选中态。
function onSelect(taskId: string) {
  router.replace({ query: { ...route.query, task: taskId } })
}

// ─── 写操作 Mutations ─────────────────────────────────────────────────────────
const createMutation = useCreateKanbanTask(appId, currentBoard)
const actionMutation = useKanbanTaskAction(appId, currentBoard)

// onCreate：KanbanCreateModal 提交新建任务时调用。
async function onCreate(payload: Record<string, unknown>) {
  try {
    await createMutation.mutateAsync(payload as never)
    showCreate.value = false
    message.success('任务已创建')
  } catch (e) {
    message.error(e instanceof Error ? e.message : '创建失败')
  }
}

// onAction：KanbanTaskDetail / KanbanTaskActions 发来操作动词时调用。
// comment/block/complete/reassign 需要补充文本输入；archive/reclaim 需要二次确认。
async function onAction(verb: string) {
  const taskId = selectedTaskId.value
  if (!taskId) return

  // 需要文本输入的操作：key 是追加到 mutation payload 的字段名，title 是提示文本。
  const NEEDS_INPUT: Record<string, { title: string; key: string }> = {
    comment: { title: '添加评论', key: 'body' },
    block: { title: '阻塞原因', key: 'reason' },
    complete: { title: '完成结果（可选）', key: 'result' },
    reassign: { title: '重新分配给（profile）', key: 'to' },
  }
  // 高风险操作：执行前弹二次确认。
  const NEEDS_CONFIRM = new Set(['archive', 'reclaim'])

  try {
    if (verb in NEEDS_INPUT) {
      // 用 promptText 收集文本输入，取消返回 null 则中止。
      const cfg = NEEDS_INPUT[verb]
      const value = await promptText(cfg.title)
      if (value === null) return
      await actionMutation.mutateAsync({ verb, taskId, [cfg.key]: value } as never)
    } else if (NEEDS_CONFIRM.has(verb)) {
      // window.confirm 作为二次确认，取消则中止。
      const ok = window.confirm(`确定要执行「${verb}」吗？`)
      if (!ok) return
      await actionMutation.mutateAsync({ verb, taskId } as never)
    } else {
      await actionMutation.mutateAsync({ verb, taskId } as never)
    }
    message.success('操作成功')
  } catch (e) {
    message.error(e instanceof Error ? e.message : '操作失败')
  }
}

// promptText 用浏览器原生 prompt 收集一行文本输入，取消返回 null。
function promptText(title: string): Promise<string | null> {
  return Promise.resolve(window.prompt(title))
}
</script>

<style scoped>
.kanban-tab {
  display: grid;
  gap: 12px;
}

/* 工具栏内边距调小，与 NCard 默认 padding 协调 */
.toolbar-card :deep(.n-card__content) {
  padding: 10px 14px;
}

/* spacer 把左侧控件和右侧按钮推到两端 */
.spacer {
  flex: 1;
}

/* 实时连接绿点标签 */
.live-tag {
  color: var(--primary-color, #18a058);
  font-size: 11px;
}

/* 左右分屏：左侧任务列表 380px 固定宽，右侧详情面板占剩余空间 */
.split {
  display: grid;
  grid-template-columns: 380px 1fr;
  gap: 12px;
  align-items: start;
}

/* 小屏时折叠为单列 */
@media (max-width: 1200px) {
  .split {
    grid-template-columns: 1fr;
  }
}

/* 加载 / 错误状态文本 */
.state-text {
  color: var(--n-text-color-3, #707078);
  font-size: 13px;
}

.danger {
  color: var(--error-color, #d03050);
}
</style>
