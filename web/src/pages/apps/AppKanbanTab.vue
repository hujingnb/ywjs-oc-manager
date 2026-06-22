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
        <n-input v-model:value="search" size="small" :placeholder="t('apps.kanban.tab.searchPlaceholder')" style="width: 200px" />
        <span class="spacer" />
        <!-- stats 徽标：任务总数 + 最老就绪任务等待时长（来自 kanban stats 端点）-->
        <!-- stats !== false：features 未知时默认显示，明确 false 才隐藏 -->
        <span v-if="statsSummary && kanbanFeatures?.stats !== false" class="stat-badge">
          <!-- t('apps.kanban.tab.taskCount') 含插值 {n}，用 v-html 内联渲染 bold 数字 -->
          <span>{{ t('apps.kanban.tab.taskCount', { n: statsSummary.total }) }}</span>
          <span v-if="statsSummary.oldestReady">{{ t('apps.kanban.tab.oldestReady', { age: statsSummary.oldestReady }) }}</span>
        </span>
        <!-- streamConnected 为 true 时显示绿点「实时」，否则显示「重连实时流」按钮 -->
        <span v-if="streamConnected" class="live-tag">{{ t('apps.kanban.tab.liveLabel') }}</span>
        <n-button v-else size="small" tertiary @click="reconnectStream">{{ t('apps.kanban.tab.reconnect') }}</n-button>
        <!-- write !== false：features 未知时默认显示，明确 false 才隐藏 -->
        <n-button v-if="kanbanFeatures?.write !== false" class="create-task-btn" size="small" type="primary" @click="showCreate = true">{{ t('apps.kanban.tab.createTask') }}</n-button>
      </n-space>
    </n-card>

    <!-- stub 镜像降级提示：当后端返回 KANBAN_NOT_SUPPORTED_ON_STUB 时显示 -->
    <n-card v-if="isStubInstance" :bordered="true">
      <n-empty :description="t('apps.kanban.tab.stubDesc')" />
    </n-card>

    <!-- 左右分屏：左侧任务列表 + 右侧详情面板 -->
    <div v-else class="split">
      <div class="list-col">
        <!-- 加载中状态 -->
        <p v-if="tasksQuery.isLoading.value" class="state-text">{{ t('common.status.loading') }}</p>
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
        <!-- 加载中传 null 避免切换任务时短暂显示上一个任务的旧数据 -->
        <!-- detail prop 对应 KanbanTaskDetail 组件的嵌套结构 { task, comments, events, ... } -->
        <KanbanTaskDetail
          :detail="taskQuery.isLoading.value ? null : (taskQuery.data.value ?? null)"
          :board="currentBoard"
          :runs="runsQuery.data.value ?? []"
          :live-events="selectedLiveEvents"
          :app-id="appId"
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
import { useI18n } from 'vue-i18n'
import KanbanTaskList from './kanban/KanbanTaskList.vue'
import KanbanTaskDetail from './kanban/KanbanTaskDetail.vue'
import KanbanCreateModal from './kanban/KanbanCreateModal.vue'
import {
  useKanbanBoardsQuery,
  useKanbanTasksQuery,
  useKanbanTaskQuery,
  useKanbanRunsQuery,
  useKanbanStatsQuery,
  useKanbanCapabilitiesQuery,
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
const { t } = useI18n()
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
// statsQuery：拉取当前 board 的统计信息，供工具栏徽标展示。
const statsQuery = useKanbanStatsQuery(appId, currentBoard)
// capabilitiesQuery：探测 oc-kanban 能力，用于按需隐藏不支持的操作和 UI 元素。
// staleTime Infinity 且不轮询，capabilities 在实例生命周期内不变。
const capabilitiesQuery = useKanbanCapabilitiesQuery(appId)
// kanbanFeatures：features 为 undefined 表示能力未知（加载中 / 请求失败 / 老镜像），
// 按「默认显示」语义处理 —— 只有明确 false 才隐藏，避免误隐藏功能。
const kanbanFeatures = computed(() => capabilitiesQuery.data.value?.features)

// formatAge 把秒数格式化为人类可读的时长（用于「最老就绪等待时长」）。
// 使用 i18n key 确保英文/中文单位随语言切换。
function formatAge(seconds: number): string {
  if (seconds < 60) return t('apps.kanban.tab.ageSeconds', { n: Math.floor(seconds) })
  if (seconds < 3600) return t('apps.kanban.tab.ageMinutes', { n: Math.floor(seconds / 60) })
  if (seconds < 86400) return t('apps.kanban.tab.ageHours', { n: Math.floor(seconds / 3600) })
  return t('apps.kanban.tab.ageDays', { n: Math.floor(seconds / 86400) })
}

// statsSummary：把 stats 端点数据归纳为工具栏徽标用的两项 —— 任务总数、最老就绪等待时长。
// 任务总数 = by_status 各状态计数之和；oldest_ready_age_seconds 为 0 时不显示等待时长。
const statsSummary = computed(() => {
  const stats = statsQuery.data.value
  if (!stats) return null
  const total = Object.values(stats.by_status ?? {}).reduce((sum, n) => sum + n, 0)
  const age = stats.oldest_ready_age_seconds ?? 0
  return { total, oldestReady: age > 0 ? formatAge(age) : '' }
})

// ─── Stub 降级判断 ───────────────────────────────────────────────────────────
// isStubError：判断给定错误是否为 KANBAN_NOT_SUPPORTED_ON_STUB，复用于多处查询。
// 必须读 body.code 而非 error.message 来判断 stub 错误。
function isStubError(err: unknown): boolean {
  if (!err) return false
  const body = (err as ApiError).body
  if (body && typeof body === 'object' && 'code' in body) {
    return (body as { code: string }).code === 'KANBAN_NOT_SUPPORTED_ON_STUB'
  }
  return false
}

// isStubInstance：tasksQuery 或 boardsQuery 任一返回 KANBAN_NOT_SUPPORTED_ON_STUB
// 时均判定为 stub 实例，降级展示提示卡片。
const isStubInstance = computed(
  () => isStubError(tasksQuery.error.value) || isStubError(boardsQuery.error.value),
)
// errorText：非 stub 的加载错误文本，直接显示给用户。
const errorText = computed(() => String(tasksQuery.error.value?.message ?? t('apps.kanban.tab.loadError')))

// ─── Board 下拉选项 ───────────────────────────────────────────────────────────
// boardOptions：若 boards 未加载完成则显示 default 占位，防止下拉为空。
const boardOptions = computed(() =>
  (boardsQuery.data.value ?? [{ slug: 'default', name: 'default' }]).map((b) => ({
    label: b.name || b.slug || 'default',
    // ?? 而非 ||：避免空字符串 slug 被静默替换为 'default'
    value: b.slug ?? 'default',
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
    message.success(t('apps.kanban.tab.successCreate'))
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('apps.kanban.tab.errorCreate'))
  }
}

// onAction：KanbanTaskDetail / KanbanTaskActions 发来操作动词时调用。
// comment/block/complete/reassign 需要补充文本输入；archive/reclaim 需要二次确认。
async function onAction(verb: string) {
  // 防重复提交：已有 mutation 在 pending 中时忽略新触发。
  if (actionMutation.isPending.value) return
  const taskId = selectedTaskId.value
  if (!taskId) return

  // 需要文本输入的操作：key 是追加到 mutation payload 的字段名，title 是提示文本。
  const NEEDS_INPUT: Record<string, { title: string; key: string }> = {
    comment: { title: t('apps.kanban.tab.promptComment'), key: 'body' },
    block: { title: t('apps.kanban.tab.promptBlock'), key: 'reason' },
    complete: { title: t('apps.kanban.tab.promptComplete'), key: 'result' },
    reassign: { title: t('apps.kanban.tab.promptReassign'), key: 'to' },
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
      const ok = window.confirm(t('apps.kanban.tab.confirmAction', { verb }))
      if (!ok) return
      await actionMutation.mutateAsync({ verb, taskId } as never)
    } else {
      await actionMutation.mutateAsync({ verb, taskId } as never)
    }
    message.success(t('apps.kanban.tab.successAction'))
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('apps.kanban.tab.errorAction'))
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
  color: var(--color-brand-text, #8a3700);
  font-size: 11px;
}

/* stats 徽标：工具栏内任务总数 + 最老就绪等待时长 */
.stat-badge {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 11px;
  color: var(--color-text-secondary, #6b7280);
  white-space: nowrap;
}
.stat-badge strong {
  color: var(--color-text-primary, #1f2329);
  font-weight: 600;
}
/* 最老就绪等待时长用警示色，提示看板积压 */
.stat-badge strong.warn {
  color: var(--color-warning-text, #92400e);
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
  color: var(--color-text-secondary, #6b7280);
  font-size: 13px;
}

.danger {
  color: var(--color-danger, #d93026);
}
</style>
