<template>
  <section class="sessions-view">
    <div v-if="!agentId" class="empty-state">
      <MessageSquareText :size="30" />
      <strong>{{ t('aicc.sessions.selectAgentTitle') }}</strong>
      <span>{{ t('aicc.sessions.selectAgentDesc') }}</span>
    </div>
    <template v-else>
      <div class="session-list">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Sessions</p>
            <h4>{{ t('aicc.sessions.recentSessions') }}</h4>
          </div>
          <n-tag size="small" :bordered="false">{{ sessionTotal }}</n-tag>
        </div>
        <div ref="sessionFiltersEl" class="session-filters">
          <n-select v-model:value="resolutionFilter" clearable :options="resolutionOptions" :placeholder="t('aicc.sessions.filters.resolution')" />
          <n-select v-model:value="leadFilter" clearable :options="leadOptions" :placeholder="t('aicc.sessions.filters.lead')" />
          <n-select v-model:value="channelFilter" clearable :options="channelOptions" :placeholder="t('aicc.sessions.filters.channel')" />
          <n-input v-model:value="regionFilter" clearable :placeholder="t('aicc.sessions.filters.region')" :input-props="{ id: 'aicc-session-region-filter', name: 'aicc_session_region_filter' }" />
          <n-date-picker class="date-range-filter" v-model:value="dateRange" type="datetimerange" clearable :start-placeholder="t('aicc.sessions.filters.startTime')" :end-placeholder="t('aicc.sessions.filters.endTime')" />
          <n-input v-model:value="keywordFilter" clearable :placeholder="t('aicc.sessions.filters.keyword')" :input-props="{ id: 'aicc-session-keyword-filter', name: 'aicc_session_keyword_filter' }" />
        </div>
        <n-spin :show="sessionsQuery.isLoading.value">
          <n-alert v-if="sessionsQuery.error.value" type="error" :bordered="false">
            {{ sessionsQuery.error.value.message }}
          </n-alert>
          <div v-else-if="sessions.length === 0" class="empty-state compact">
            <MessageSquareText :size="24" />
            <strong>{{ t('aicc.sessions.emptyTitle') }}</strong>
            <span>{{ t('aicc.sessions.emptyDesc') }}</span>
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
                <small>{{ session.channel || 'web_link' }} · {{ session.region || t('aicc.sessions.unknownRegion') }}</small>
              </span>
              <n-tag size="small" :type="resolutionTagType(session.resolution_status)" :bordered="false">
                {{ resolutionLabel(session.resolution_status) }}
              </n-tag>
              <small class="meta-text">{{ t('aicc.sessions.messageCount', { count: session.message_count ?? 0 }) }}</small>
              <small class="time-text">{{ formatDate(session.last_active_at || session.created_at) }}</small>
            </button>
          </template>
        </n-spin>
        <n-pagination
          v-if="sessionTotal > pageSize || page > 1"
          v-model:page="page"
          v-model:page-size="pageSize"
          class="session-pagination"
          :item-count="sessionTotal"
          :page-sizes="[20, 50, 100]"
          show-size-picker
        />
      </div>

      <div class="session-detail">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Transcript</p>
            <h4>{{ selectedSessionId ? formatShortId(selectedSessionId) : t('aicc.sessions.unselected') }}</h4>
          </div>
        </div>
        <n-spin :show="detailQuery.isLoading.value">
          <n-alert v-if="detailQuery.error.value" type="error" :bordered="false">
            {{ detailQuery.error.value.message }}
          </n-alert>
          <div v-else-if="messages.length === 0 && leadValues.length === 0" class="empty-state">
            <MessageSquareText :size="28" />
            <strong>{{ t('aicc.sessions.noMessagesTitle') }}</strong>
            <span>{{ t('aicc.sessions.noMessagesDesc') }}</span>
          </div>
          <div v-else class="message-stack">
            <div v-if="selectedSession" class="session-summary">
              <span>
                <strong>{{ t('aicc.sessions.summary.region') }}</strong>
                <small>{{ selectedSession.region || t('aicc.sessions.unknownRegion') }}</small>
              </span>
              <span>
                <strong>{{ t('aicc.sessions.summary.messageCount') }}</strong>
                <small>{{ selectedSession.message_count ?? 0 }}</small>
              </span>
              <span>
                <strong>{{ t('aicc.sessions.summary.channel') }}</strong>
                <small>{{ selectedSession.channel || 'web_link' }}</small>
              </span>
              <span>
                <strong>{{ t('aicc.sessions.summary.source') }}</strong>
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
              <p>{{ message.text || (message.image_object_key ? t('aicc.sessions.messageFallbackImage') : t('aicc.sessions.messageFallbackEmpty')) }}</p>
              <n-tag v-if="message.image_object_key" size="small" :bordered="false">{{ t('aicc.sessions.imageTag') }}</n-tag>
              <n-tag v-if="message.is_fallback" size="small" type="warning" :bordered="false">{{ t('aicc.sessions.fallbackTag') }}</n-tag>
              <n-tag v-if="message.is_refusal" size="small" type="warning" :bordered="false">{{ t('aicc.sessions.refusalTag') }}</n-tag>
              <n-tag v-if="message.error_summary" size="small" type="error" :bordered="false">{{ message.error_summary }}</n-tag>
            </article>
          </div>
        </n-spin>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUpdated, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NDatePicker, NInput, NPagination, NSelect, NSpin, NTag, type SelectOption } from 'naive-ui'
import { MessageSquareText } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import { useAICCSessionsQuery, useAICCSessionQuery } from '@/api/hooks/useAICC'
import type { AICCSessionFilters } from '@/domain/aicc'

const props = defineProps<{
  agentId?: string
}>()

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const selectedSessionId = ref<string | undefined>()
const resolutionFilter = ref<string | null>(null)
const leadFilter = ref<string | null>(null)
const channelFilter = ref<string | null>(null)
const regionFilter = ref('')
const dateRange = ref<[number, number] | null>(null)
const keywordFilter = ref('')
const page = ref(1)
const pageSize = ref(20)
const isApplyingRouteQuery = ref(false)
const sessionFiltersEl = ref<HTMLElement | null>(null)
const currentAgentId = computed(() => props.agentId)
const supportedChannelFilters = new Set(['web_link', 'web_widget'])
const sessionOffset = computed(() => (page.value - 1) * pageSize.value)
const sessionFilters = computed<AICCSessionFilters>(() => ({
  resolution_status: resolutionFilter.value || undefined,
  lead_status: leadFilter.value || undefined,
  channel: channelFilter.value || undefined,
  region: regionFilter.value.trim() || undefined,
  start_at: dateRange.value ? new Date(dateRange.value[0]).toISOString() : undefined,
  end_at: dateRange.value ? new Date(dateRange.value[1]).toISOString() : undefined,
  keyword: keywordFilter.value.trim() || undefined,
  limit: pageSize.value,
  offset: sessionOffset.value,
}))
const sessionsQuery = useAICCSessionsQuery(currentAgentId, sessionFilters)
const detailQuery = useAICCSessionQuery(selectedSessionId)

const sessions = computed(() => sessionsQuery.data.value?.sessions ?? [])
const sessionTotal = computed(() => sessionsQuery.data.value?.total ?? 0)
const selectedSession = computed(() => detailQuery.data.value?.session)
const messages = computed(() => detailQuery.data.value?.messages ?? [])
const leadValues = computed(() => detailQuery.data.value?.lead_values ?? [])

onMounted(syncDateRangeInputAttrs)
onUpdated(syncDateRangeInputAttrs)

const resolutionOptions = computed<SelectOption[]>(() => [
  { label: t('aicc.sessions.resolutionOptions.resolved'), value: 'resolved' },
  { label: t('aicc.sessions.resolutionOptions.unresolved'), value: 'unresolved' },
  { label: t('aicc.sessions.resolutionOptions.unknown'), value: 'unknown' },
])

const leadOptions = computed<SelectOption[]>(() => [
  { label: t('aicc.sessions.leadOptions.pending'), value: 'pending' },
  { label: t('aicc.sessions.leadOptions.complete'), value: 'complete' },
  { label: t('aicc.sessions.leadOptions.skipped'), value: 'skipped' },
])

const channelOptions = computed<SelectOption[]>(() => [
  { label: t('aicc.sessions.channelOptions.webLink'), value: 'web_link' },
  { label: t('aicc.sessions.channelOptions.webWidget'), value: 'web_widget' },
])

watch(
  () => route.query,
  (query) => {
    isApplyingRouteQuery.value = true
    resolutionFilter.value = stringQuery(query.resolution_status)
    leadFilter.value = stringQuery(query.lead_status)
    channelFilter.value = normalizeChannelQuery(query.channel)
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

watch(
  () => [
    currentAgentId.value,
    resolutionFilter.value,
    leadFilter.value,
    channelFilter.value,
    regionFilter.value.trim(),
    dateRange.value?.[0] ?? null,
    dateRange.value?.[1] ?? null,
    keywordFilter.value.trim(),
  ],
  () => {
    if (isApplyingRouteQuery.value) return
    page.value = 1
  },
)

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
  if (role === 'assistant') return t('aicc.sessions.roles.assistant')
  if (role === 'system') return t('aicc.sessions.roles.system')
  return t('aicc.sessions.roles.visitor')
}

function resolutionLabel(status?: string) {
  if (status === 'resolved') return t('aicc.sessions.resolved')
  if (status === 'unresolved') return t('aicc.sessions.unresolved')
  return t('aicc.sessions.followUp')
}

function resolutionTagType(status?: string) {
  if (status === 'resolved') return 'success'
  if (status === 'unresolved') return 'warning'
  return 'default'
}

function syncDateRangeInputAttrs() {
  void nextTick(() => {
    const inputs = sessionFiltersEl.value?.querySelectorAll<HTMLInputElement>('.date-range-filter input') ?? []
    const attrs = [
      { id: 'aicc-session-start-filter', name: 'aicc_session_start_filter' }, // 开始时间筛选输入。
      { id: 'aicc-session-end-filter', name: 'aicc_session_end_filter' }, // 结束时间筛选输入。
    ]
    inputs.forEach((input, index) => {
      const attr = attrs[index]
      if (!attr) return
      input.id = attr.id
      input.name = attr.name
    })
  })
}

function stringQuery(value: unknown): string | null {
  if (Array.isArray(value)) return typeof value[0] === 'string' ? value[0] : null
  return typeof value === 'string' && value ? value : null
}

function normalizeChannelQuery(value: unknown): string | null {
  const channel = stringQuery(value)
  return channel && supportedChannelFilters.has(channel) ? channel : null
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

.session-pagination {
  justify-content: flex-end;
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
