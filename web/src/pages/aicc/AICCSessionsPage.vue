<template>
  <section class="sessions-view">
    <div v-if="!agentId" class="empty-state">
      <MessageSquareText :size="30" />
      <strong>选择智能体后查看会话</strong>
      <span>会话按最近创建时间排序，便于运营快速回看访客问题。</span>
    </div>
    <template v-else>
      <div class="session-list">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Sessions</p>
            <h4>最近会话</h4>
          </div>
          <n-tag size="small" :bordered="false">{{ sessions.length }}</n-tag>
        </div>
        <div class="session-filters">
          <n-select v-model:value="resolutionFilter" clearable :options="resolutionOptions" placeholder="解决状态" />
          <n-select v-model:value="leadFilter" clearable :options="leadOptions" placeholder="留资状态" />
          <n-select v-model:value="channelFilter" clearable :options="channelOptions" placeholder="渠道" />
          <n-input v-model:value="regionFilter" clearable placeholder="地域" />
          <n-date-picker v-model:value="dateRange" type="datetimerange" clearable start-placeholder="开始时间" end-placeholder="结束时间" />
          <n-input v-model:value="keywordFilter" clearable placeholder="来源关键词" />
        </div>
        <n-spin :show="sessionsQuery.isLoading.value">
          <n-alert v-if="sessionsQuery.error.value" type="error" :bordered="false">
            {{ sessionsQuery.error.value.message }}
          </n-alert>
          <div v-else-if="sessions.length === 0" class="empty-state compact">
            <MessageSquareText :size="24" />
            <strong>暂无访客会话</strong>
            <span>公开链接产生对话后会出现在这里。</span>
          </div>
          <template v-else>
            <button
              v-for="session in sessions"
              :key="session.id"
              class="session-row"
              :class="{ active: session.id === selectedSessionId }"
              type="button"
              @click="selectedSessionId = session.id"
            >
              <span>
                <strong>{{ formatShortId(session.id) }}</strong>
                <small>{{ session.channel || 'web_link' }} · {{ session.region || '未知地域' }}</small>
              </span>
              <n-tag size="small" :type="session.resolution_status === 'resolved' ? 'success' : 'warning'" :bordered="false">
                {{ session.resolution_status === 'resolved' ? '已解决' : '跟进中' }}
              </n-tag>
              <small class="meta-text">{{ session.message_count ?? 0 }} 条消息</small>
              <small class="time-text">{{ formatDate(session.last_active_at || session.created_at) }}</small>
            </button>
          </template>
        </n-spin>
      </div>

      <div class="session-detail">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Transcript</p>
            <h4>{{ selectedSessionId ? formatShortId(selectedSessionId) : '未选择会话' }}</h4>
          </div>
        </div>
        <n-spin :show="detailQuery.isLoading.value">
          <n-alert v-if="detailQuery.error.value" type="error" :bordered="false">
            {{ detailQuery.error.value.message }}
          </n-alert>
          <div v-else-if="messages.length === 0 && leadValues.length === 0" class="empty-state">
            <MessageSquareText :size="28" />
            <strong>暂无消息</strong>
            <span>选择左侧会话后查看访客与助手对话。</span>
          </div>
          <div v-else class="message-stack">
            <div v-if="selectedSession" class="session-summary">
              <span>
                <strong>地域</strong>
                <small>{{ selectedSession.region || '未知地域' }}</small>
              </span>
              <span>
                <strong>消息数</strong>
                <small>{{ selectedSession.message_count ?? 0 }}</small>
              </span>
              <span>
                <strong>渠道</strong>
                <small>{{ selectedSession.channel || 'web_link' }}</small>
              </span>
              <span>
                <strong>来源</strong>
                <small>{{ selectedSession.source_url || '-' }}</small>
              </span>
            </div>
            <div v-if="leadValues.length" class="lead-summary">
              <span v-for="value in leadValues" :key="value.field_key">
                <strong>{{ value.label }}</strong>
                <small>{{ value.value }}</small>
              </span>
            </div>
            <article v-for="message in messages" :key="message.id" class="message-item" :class="message.direction">
              <div class="message-meta">
                <span>{{ roleLabel(message.direction) }}</span>
                <small>{{ formatDate(message.created_at) }}</small>
              </div>
              <p>{{ message.text || (message.image_object_key ? '图片消息' : '空消息') }}</p>
              <n-tag v-if="message.image_object_key" size="small" :bordered="false">含图片</n-tag>
              <n-tag v-if="message.is_fallback" size="small" type="warning" :bordered="false">兜底回答</n-tag>
              <n-tag v-if="message.is_refusal" size="small" type="warning" :bordered="false">拒答</n-tag>
              <n-tag v-if="message.error_summary" size="small" type="error" :bordered="false">{{ message.error_summary }}</n-tag>
            </article>
          </div>
        </n-spin>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NDatePicker, NInput, NSelect, NSpin, NTag, type SelectOption } from 'naive-ui'
import { MessageSquareText } from 'lucide-vue-next'

import { useAICCSessionsQuery, useAICCSessionQuery } from '@/api/hooks/useAICC'
import type { AICCSessionFilters } from '@/domain/aicc'

const props = defineProps<{
  agentId?: string
}>()

const route = useRoute()
const router = useRouter()
const selectedSessionId = ref<string | undefined>()
const resolutionFilter = ref<string | null>(null)
const leadFilter = ref<string | null>(null)
const channelFilter = ref<string | null>(null)
const regionFilter = ref('')
const dateRange = ref<[number, number] | null>(null)
const keywordFilter = ref('')
const isApplyingRouteQuery = ref(false)
const currentAgentId = computed(() => props.agentId)
const sessionFilters = computed<AICCSessionFilters>(() => ({
  resolution_status: resolutionFilter.value || undefined,
  lead_status: leadFilter.value || undefined,
  channel: channelFilter.value || undefined,
  region: regionFilter.value.trim() || undefined,
  start_at: dateRange.value ? new Date(dateRange.value[0]).toISOString() : undefined,
  end_at: dateRange.value ? new Date(dateRange.value[1]).toISOString() : undefined,
  keyword: keywordFilter.value.trim() || undefined,
}))
const sessionsQuery = useAICCSessionsQuery(currentAgentId, sessionFilters)
const detailQuery = useAICCSessionQuery(selectedSessionId)

const sessions = computed(() => sessionsQuery.data.value ?? [])
const selectedSession = computed(() => detailQuery.data.value?.session)
const messages = computed(() => detailQuery.data.value?.messages ?? [])
const leadValues = computed(() => detailQuery.data.value?.lead_values ?? [])

const resolutionOptions: SelectOption[] = [
  { label: '已解决', value: 'resolved' },
  { label: '未解决', value: 'unresolved' },
  { label: '未知', value: 'unknown' },
]

const leadOptions: SelectOption[] = [
  { label: '待留资', value: 'pending' },
  { label: '已留资', value: 'complete' },
  { label: '已跳过', value: 'skipped' },
]

const channelOptions: SelectOption[] = [
  { label: '公开链接', value: 'web_link' },
  { label: '网页挂件', value: 'web_widget' },
  { label: '语音客服', value: 'voice' },
]

watch(
  () => route.query,
  (query) => {
    isApplyingRouteQuery.value = true
    resolutionFilter.value = stringQuery(query.resolution_status)
    leadFilter.value = stringQuery(query.lead_status)
    channelFilter.value = stringQuery(query.channel)
    regionFilter.value = stringQuery(query.region) ?? ''
    keywordFilter.value = stringQuery(query.keyword) ?? ''
    const start = parseQueryDate(query.start_at)
    const end = parseQueryDate(query.end_at)
    dateRange.value = start !== null && end !== null ? [start, end] : null
    isApplyingRouteQuery.value = false
  },
  { immediate: true },
)

watch(sessionFilters, (filters) => {
  if (isApplyingRouteQuery.value) return
  const nextQuery = {
    ...route.query,
    resolution_status: filters.resolution_status,
    lead_status: filters.lead_status,
    channel: filters.channel,
    region: filters.region,
    start_at: filters.start_at,
    end_at: filters.end_at,
    keyword: filters.keyword,
  }
  if (isSameQuery(route.query, nextQuery)) return
  void router.replace({ query: nextQuery })
}, { deep: true })

watch(sessions, (items) => {
  if (!items.some(item => item.id === selectedSessionId.value)) {
    selectedSessionId.value = items[0]?.id
  }
})

watch(currentAgentId, () => {
  selectedSessionId.value = undefined
})

function formatShortId(id?: string) {
  if (!id) return '-'
  return id.length > 12 ? `${id.slice(0, 8)}...` : id
}

function formatDate(value?: string) {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}

function roleLabel(role?: string) {
  if (role === 'assistant') return '助手'
  if (role === 'system') return '系统'
  return '访客'
}

function stringQuery(value: unknown): string | null {
  if (Array.isArray(value)) return typeof value[0] === 'string' ? value[0] : null
  return typeof value === 'string' && value ? value : null
}

function parseQueryDate(value: unknown): number | null {
  const text = stringQuery(value)
  if (!text) return null
  const time = new Date(text).getTime()
  return Number.isFinite(time) ? time : null
}

function isSameQuery(current: Record<string, unknown>, next: Record<string, unknown>): boolean {
  const keys = new Set([...Object.keys(current), ...Object.keys(next)])
  for (const key of keys) {
    const left = normalizeQueryValue(current[key])
    const right = normalizeQueryValue(next[key])
    if (left !== right) return false
  }
  return true
}

function normalizeQueryValue(value: unknown): string {
  if (Array.isArray(value)) return value.join(',')
  return value === undefined || value === null ? '' : String(value)
}
</script>

<style scoped>
.sessions-view {
  display: grid;
  grid-template-columns: minmax(240px, 0.34fr) minmax(0, 1fr);
  gap: 14px;
  min-height: 420px;
}

.session-list,
.session-detail {
  min-width: 0;
  padding: 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.session-list {
  display: grid;
  align-content: start;
  gap: 10px;
}

.session-filters {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
}

.session-filters :deep(.n-date-picker),
.session-filters :deep(.n-input:last-child) {
  grid-column: 1 / -1;
}

.panel-heading,
.message-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.panel-heading h4 {
  margin: 2px 0 0;
  font-size: 16px;
}

.session-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 6px 10px;
  width: 100%;
  padding: 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  color: var(--color-text-primary);
  text-align: left;
  cursor: pointer;
}

.session-row.active {
  border-color: var(--color-brand);
  box-shadow: inset 3px 0 0 var(--color-brand);
}

.session-row span {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.session-row small,
.time-text,
.empty-state span,
.message-meta small {
  color: var(--color-text-secondary);
}

.time-text {
  grid-column: 1 / -1;
}

.meta-text {
  justify-self: end;
}

.empty-state {
  display: grid;
  place-items: center;
  gap: 8px;
  min-height: 260px;
  color: var(--color-text-secondary);
  text-align: center;
}

.empty-state strong {
  color: var(--color-text-primary);
}

.empty-state.compact {
  min-height: 180px;
}

.message-stack {
  display: grid;
  gap: 10px;
}

.session-summary,
.lead-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.session-summary span,
.lead-summary span {
  display: grid;
  min-width: 120px;
  gap: 2px;
}

.session-summary strong,
.lead-summary strong {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.session-summary small,
.lead-summary small {
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
}

.message-item {
  display: grid;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.message-item.assistant {
  border-left: 3px solid #0ea5e9;
}

.message-item.visitor {
  border-left: 3px solid #22c55e;
}

.message-item p {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}

@media (max-width: 980px) {
  .sessions-view {
    grid-template-columns: 1fr;
  }

  .session-filters {
    grid-template-columns: 1fr;
  }

  .session-filters :deep(.n-input) {
    grid-column: auto;
  }
}
</style>
