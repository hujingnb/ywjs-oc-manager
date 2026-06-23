<template>
  <n-card :bordered="true">
    <!-- 未选中任务时显示引导文案 -->
    <template v-if="!detail">
      <p class="state-text">{{ t('apps.kanban.taskDetail.selectHint') }}</p>
    </template>
    <template v-else>
      <div class="detail-head">
        <!-- 状态条：status 缺失时 formatKanbanStatus('unknown') 返回降级键，t() 解析为当前语言降级文案。 -->
        <div class="status-bar">● {{ taskStatusLabel }}</div>
        <h3 class="detail-title">{{ detail.task?.title ?? t('apps.kanban.taskDetail.noTitle') }}</h3>
        <p class="detail-sub">task_id <code>{{ detail.task?.id ?? '—' }}</code> · board <code>{{ board }}</code></p>
      </div>

      <!-- 操作按钮区：仅当 status 是已知 KanbanStatus 时渲染操作组件，
           防止 hermes 新增状态时传入 KanbanTaskActions 未知 status 值。
           isKnownStatus 用类型谓词保证 v-if 之后 detail.task?.status 类型为 KanbanStatus。-->
      <KanbanTaskActions
        v-if="isKnownStatus(detail.task?.status)"
        :status="detail.task!.status as KanbanStatus"
        :app-id="appId"
        @action="emit('action', $event)"
      />

      <!-- 元信息 -->
      <div class="section">
        <p class="section-title">{{ t('apps.kanban.taskDetail.sectionMeta') }}</p>
        <div class="meta-grid">
          <div><span class="k">assignee</span><span class="v">{{ detail.task?.assignee ?? '—' }}</span></div>
          <div><span class="k">priority</span><span class="v">{{ detail.task?.priority ?? 0 }}</span></div>
          <!-- workspace_kind 字段可选，有值才显示 -->
          <div v-if="detail.task?.workspace_kind"><span class="k">workspace</span><span class="v">{{ detail.task.workspace_kind }}{{ detail.task.workspace_path ? ` · ${detail.task.workspace_path}` : '' }}</span></div>
          <!-- created_by 字段可选，有值才显示 -->
          <div v-if="detail.task?.created_by"><span class="k">created_by</span><span class="v">{{ detail.task.created_by }}</span></div>
          <!-- tenant 字段可选，有值才显示 -->
          <div v-if="detail.task?.tenant"><span class="k">tenant</span><span class="v">{{ detail.task.tenant }}</span></div>
          <!-- skills 是字符串数组，有值才显示，逗号连接展示 -->
          <div v-if="detail.task?.skills?.length"><span class="k">skills</span><span class="v">{{ detail.task.skills.join(', ') }}</span></div>
        </div>
      </div>

      <!-- body：可选字段，有内容时显示 -->
      <div v-if="detail.task?.body" class="section">
        <p class="section-title">{{ t('apps.kanban.taskDetail.sectionBody') }}</p>
        <p class="body-block">{{ detail.task.body }}</p>
      </div>

      <!-- 实时执行流：仅 running 状态显示，由父组件注入 liveEvents -->
      <div v-if="detail.task?.status === 'running'" class="section">
        <p class="section-title">{{ t('apps.kanban.taskDetail.sectionLive') }} <span class="live">{{ t('apps.kanban.taskDetail.sectionLiveSuffix') }}</span></p>
        <div class="events-pane">
          <div v-for="(ev, i) in liveEvents" :key="i" class="ev-line">{{ ev }}</div>
          <p v-if="liveEvents.length === 0" class="state-text">{{ t('apps.kanban.taskDetail.liveEmpty') }}</p>
        </div>
      </div>

      <!-- 历次执行：runs 由父组件通过 useKanbanRunsQuery 注入 -->
      <div class="section">
        <p class="section-title">{{ t('apps.kanban.taskDetail.sectionRuns') }}</p>
        <p v-if="runs.length === 0" class="state-text">{{ t('apps.kanban.taskDetail.runsEmpty') }}</p>
        <table v-else class="runs-table">
          <thead><tr><th>{{ t('apps.kanban.taskDetail.runsColStatus') }}</th><th>{{ t('apps.kanban.taskDetail.runsColProfile') }}</th><th>{{ t('apps.kanban.taskDetail.runsColResult') }}</th></tr></thead>
          <tbody>
            <tr v-for="(run, i) in runs" :key="i">
              <!-- run.status 经 formatKanbanStatus 映射为 i18n 键，再通过 t() 展示当前语言文案。 -->
              <td>{{ run.status ? t(formatKanbanStatus(run.status).label, formatKanbanStatus(run.status).params ?? {}) : '—' }}</td>
              <td>{{ run.profile ?? '—' }}</td>
              <!-- error / summary 均可选，优先显示 error，再 summary，均无则显示 — -->
              <td>{{ run.error || run.summary || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 评论：comments 在 KanbanTaskDetail 中为可选数组，用 ?? [] 防御 -->
      <div class="section">
        <p class="section-title">{{ t('apps.kanban.taskDetail.sectionComments', { n: detail.comments?.length ?? 0 }) }}</p>
        <div v-for="(c, i) in detail.comments ?? []" :key="i" class="comment">
          <div class="comment-head">{{ c.author ?? t('apps.kanban.taskDetail.anonymous') }}</div>
          <div class="comment-body">{{ c.body ?? '' }}</div>
        </div>
      </div>
    </template>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard } from 'naive-ui'
import { formatKanbanStatus } from '@/domain/status'
import KanbanTaskActions from './KanbanTaskActions.vue'
import type { KanbanTaskDetail, KanbanTaskRun, KanbanStatus } from '@/api/hooks/useKanban'

// KanbanTaskDetail 渲染右侧任务详情面板，包含：状态条、操作栏、元信息、body、
// 实时执行流（running 状态）、历次执行列表、评论区。
// 注意：prop 命名为 detail 而非 task，避免与 detail.task 子字段混淆。
const props = defineProps<{
  // detail 为 null 时显示引导文案「从左侧选择任务」。
  // 真实结构：{ task, comments, events, parents, children, latest_summary }
  detail: KanbanTaskDetail | null
  // board 当前 board slug，显示在 task_id 元信息行。
  board: string
  // runs 由父组件通过 useKanbanRunsQuery 注入。
  runs: KanbanTaskRun[]
  // liveEvents 是当前任务的实时事件文本行，由父组件从 SSE 流注入。
  liveEvents: string[]
  // appId 透传给 KanbanTaskActions，用于查询 oc-kanban capabilities 降级 UI。
  appId?: string
}>()

// action 事件向上传递操作动词，由父组件（AppKanbanTab）收集额外参数后调用 mutation。
const emit = defineEmits<{
  action: [verb: string]
}>()

const { t } = useI18n()

// taskStatusLabel 汇集任务状态展示文案；formatKanbanStatus 返回 i18n 键，通过 t() 解析为当前语言文案。
const taskStatusLabel = computed(() => {
  const view = formatKanbanStatus(props.detail?.task?.status ?? 'unknown')
  return t(view.label, view.params ?? {})
})

// KNOWN_STATUSES 是 KanbanStatus 的所有合法值集合，用于类型守卫。
// 当 hermes 新增状态时，此处不会自动扩展，但操作按钮不会渲染（降级策略）。
const KNOWN_STATUSES = new Set<KanbanStatus>([
  'triage', 'todo', 'ready', 'running', 'blocked', 'done', 'archived',
])

// isKnownStatus 是类型谓词：判断 status 是否为已知的 KanbanStatus 联合类型值。
// 用于在模板 v-if 中把 status: string | undefined 收窄为 KanbanStatus，
// 确保传给 KanbanTaskActions 的 status prop 类型正确，通过 vue-tsc 检查。
// 注：KanbanStatus | string 在 TS 里会折叠为 string，参数类型直接写 string | undefined。
function isKnownStatus(status: string | undefined): status is KanbanStatus {
  return typeof status === 'string' && KNOWN_STATUSES.has(status as KanbanStatus)
}
</script>

<style scoped>
.detail-head { margin-bottom: 12px; }
/* 状态色用主题色，与左侧列表的 running 行颜色一致 */
.status-bar { color: var(--color-brand-text, #8a3700); font-size: 12px; font-weight: 500; }
.detail-title { margin: 4px 0; font-size: 16px; }
.detail-sub { color: var(--color-text-secondary, #6b7280); font-size: 11px; }
.section { margin-top: 14px; border-top: 1px solid var(--color-border, #e5e7eb); padding-top: 12px; }
.section-title { font-size: 11px; text-transform: uppercase; color: var(--color-text-secondary, #6b7280); margin: 0 0 8px; }
.live { color: var(--color-brand-text, #8a3700); }
.meta-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; font-size: 12px; }
.meta-grid .k { color: var(--color-text-secondary, #6b7280); margin-right: 8px; }
.body-block { font-size: 12px; white-space: pre-wrap; color: var(--color-text-primary, #1f2329); }
.events-pane {
  background: var(--color-surface, #ffffff);
  border-radius: 3px;
  padding: 10px;
  font-family: ui-monospace, monospace;
  font-size: 11px;
  max-height: 180px;
  overflow-y: auto;
}
.ev-line {
  line-height: 1.5;
  color: var(--color-text-primary, #1f2329);
  word-break: break-all;
}
.runs-table { width: 100%; border-collapse: collapse; font-size: 12px; }
.runs-table th,
.runs-table td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
}
.comment {
  background: var(--color-surface-muted, #fbfcfd);
  border-radius: 3px;
  padding: 8px 10px;
  margin-bottom: 6px;
}
.comment-head { font-size: 11px; color: var(--color-text-secondary, #6b7280); }
.comment-body { font-size: 12px; color: var(--color-text-primary, #1f2329); }
.state-text { color: var(--color-text-secondary, #6b7280); font-size: 13px; }
</style>
