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
                <small>{{ session.channel || 'web_link' }}</small>
              </span>
              <n-tag size="small" :type="session.resolution_status === 'resolved' ? 'success' : 'warning'" :bordered="false">
                {{ session.resolution_status === 'resolved' ? '已解决' : '跟进中' }}
              </n-tag>
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
          <div v-else-if="messages.length === 0" class="empty-state">
            <MessageSquareText :size="28" />
            <strong>暂无消息</strong>
            <span>选择左侧会话后查看访客与助手对话。</span>
          </div>
          <div v-else class="message-stack">
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
import { NAlert, NSpin, NTag } from 'naive-ui'
import { MessageSquareText } from 'lucide-vue-next'

import { useAICCSessionsQuery, useAICCSessionQuery } from '@/api/hooks/useAICC'

const props = defineProps<{
  agentId?: string
}>()

const selectedSessionId = ref<string | undefined>()
const currentAgentId = computed(() => props.agentId)
const sessionsQuery = useAICCSessionsQuery(currentAgentId)
const detailQuery = useAICCSessionQuery(selectedSessionId)

const sessions = computed(() => sessionsQuery.data.value ?? [])
const messages = computed(() => detailQuery.data.value?.messages ?? [])

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
}
</style>
