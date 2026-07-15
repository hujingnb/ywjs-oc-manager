<template>
  <section class="leads-view">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">Leads</p>
        <h4>{{ t('aicc.leads.title') }}</h4>
      </div>
      <n-space>
        <n-button :loading="isExporting" @click="exportLeads">
          <template #icon><Download :size="16" /></template>
          {{ t('aicc.leads.exportCsv') }}
        </n-button>
      </n-space>
    </div>

    <n-spin :show="leadsQuery.isLoading.value">
      <n-alert v-if="leadsQuery.error.value" type="error" :bordered="false">
        {{ leadsQuery.error.value.message }}
      </n-alert>
      <div v-else-if="leads.length === 0" class="empty-state">
        <Inbox :size="30" />
        <strong>{{ t('aicc.leads.emptyTitle') }}</strong>
        <span>{{ t('aicc.leads.emptyDesc') }}</span>
      </div>
      <div v-else class="lead-list">
        <article v-for="lead in leads" :key="lead.id" class="lead-row">
          <div class="lead-main">
            <strong>{{ lead.display_name || t('aicc.leads.anonymousIntentLead') }}</strong>
            <small>{{ t('aicc.leads.sessionPrefix', { id: formatShortId(lead.latest_session_id) }) }} · {{ formatDate(lead.updated_at || lead.created_at) }}</small>
            <div v-if="lead.values?.length" class="lead-values">
              <span v-for="value in lead.values" :key="`${lead.id}-${value.field_key}`">
                {{ value.label }}：{{ value.value }}
              </span>
            </div>
          </div>
          <n-tag size="small" :type="lead.unread ? 'warning' : 'default'" :bordered="false">
            {{ lead.unread ? t('aicc.leads.unread') : t('aicc.leads.read') }}
          </n-tag>
          <n-button size="small" :disabled="!lead.latest_session_id" @click="openLeadConversation(lead)">
            <template #icon><Eye :size="14" /></template>
            {{ t('aicc.leads.viewConversation') }}
          </n-button>
          <n-button size="small" :disabled="!lead.unread || !canManageAICC" :loading="markReadMutation.isPending.value && activeLeadId === lead.id" @click="markRead(lead.id)">
            <template #icon><Check :size="14" /></template>
            {{ t('aicc.leads.markRead') }}
          </n-button>
        </article>
      </div>
    </n-spin>
    <div v-if="transcriptOpen" class="lead-transcript-overlay" role="presentation" @click.self="closeLeadConversation">
      <aside class="lead-transcript-drawer" role="dialog" aria-modal="true" :aria-label="t('aicc.leads.conversationDialogLabel')">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Transcript</p>
            <h4>{{ t('aicc.leads.conversationTitle', { name: selectedLead?.display_name || t('aicc.leads.unnamedVisitor') }) }}</h4>
          </div>
          <n-space>
            <n-tag v-if="selectedSessionId" size="small" :bordered="false">
              {{ t('aicc.leads.sessionPrefix', { id: formatShortId(selectedSessionId) }) }}
            </n-tag>
            <n-button size="small" quaternary :title="t('aicc.leads.closeConversation')" @click="closeLeadConversation">
              <template #icon><X :size="14" /></template>
            </n-button>
          </n-space>
        </div>
        <n-spin :show="detailQuery.isLoading.value">
          <n-alert v-if="detailQuery.error.value" type="error" :bordered="false">
            {{ detailQuery.error.value.message }}
          </n-alert>
          <div v-else-if="transcriptMessages.length === 0 && transcriptLeadValues.length === 0" class="empty-state compact">
            <MessageSquareText :size="26" />
            <strong>{{ t('aicc.leads.noConversationTitle') }}</strong>
            <span>{{ t('aicc.leads.noConversationDesc') }}</span>
          </div>
          <div v-else class="transcript-stack">
            <section v-if="sessionIntent" class="intent-profile" :aria-label="t('aicc.leads.intentProfileLabel')">
              <strong>{{ t('aicc.leads.intentProfileTitle') }} · {{ intentLevelLabel(sessionIntent.intent_level) }}</strong>
              <div v-if="Object.keys(sessionIntent.fields).length" class="intent-fields">
                <button v-for="(value, key) in sessionIntent.fields" :key="key" type="button" @click="focusEvidence(sessionIntent.evidence[key])">
                  {{ key }}：{{ value }}
                </button>
              </div>
              <small v-else>{{ t('aicc.leads.intentNoFields') }}</small>
            </section>
            <div v-if="transcriptLeadValues.length" class="drawer-lead-values">
              <span v-for="value in transcriptLeadValues" :key="value.field_key">
                <strong>{{ value.label }}</strong>
                <small>{{ value.value }}</small>
              </span>
            </div>
            <article v-for="message in transcriptMessages" :key="message.id" class="message-item" :class="message.direction" :data-message-id="message.id">
              <div class="message-meta">
                <span>{{ roleLabel(message.direction) }}</span>
                <small>{{ formatDate(message.created_at) }}</small>
              </div>
              <p>{{ message.text || (message.image_object_key ? t('aicc.leads.messageFallbackImage') : t('aicc.leads.messageFallbackEmpty')) }}</p>
              <n-tag v-if="message.image_object_key" size="small" :bordered="false">{{ t('aicc.leads.imageTag') }}</n-tag>
              <n-tag v-if="message.is_fallback" size="small" type="warning" :bordered="false">{{ t('aicc.leads.fallbackTag') }}</n-tag>
              <n-tag v-if="message.is_refusal" size="small" type="warning" :bordered="false">{{ t('aicc.leads.refusalTag') }}</n-tag>
              <n-tag v-if="message.error_summary" size="small" type="error" :bordered="false">{{ message.error_summary }}</n-tag>
              <div v-if="message.sources?.length" class="message-sources">
                <span v-for="source in message.sources" :key="source.reference_id || source.url || source.title">
                  {{ source.title || t('aicc.leads.sourceLabel') }}
                  <em v-if="source.unconfirmed">{{ t('aicc.leads.unconfirmedNetwork') }}</em>
                </span>
              </div>
            </article>
          </div>
        </n-spin>
      </aside>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NAlert, NButton, NSpace, NSpin, NTag, useMessage } from 'naive-ui'
import { Check, Download, Eye, Inbox, MessageSquareText, X } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import { downloadAICCLeadsCSV, useAICCLeadsQuery, useAICCSessionQuery, useMarkAICCLeadRead } from '@/api/hooks/useAICC'
import type { AICCLead } from '@/domain/aicc'
import { useRequiredAICCConsoleContext } from './aiccConsoleContext'

const message = useMessage()
const { t } = useI18n()
const consoleContext = useRequiredAICCConsoleContext()
const leadsQuery = useAICCLeadsQuery(
  consoleContext.selectedOrgId,
  () => !consoleContext.isPlatformAdmin.value || Boolean(consoleContext.selectedOrgId.value),
)
const markReadMutation = useMarkAICCLeadRead()
const canManageAICC = computed(() => !consoleContext.isPlatformAdmin.value)

const isExporting = ref(false)
const activeLeadId = ref<string | undefined>()
const selectedLead = ref<AICCLead | undefined>()
const selectedSessionId = ref<string | undefined>()
const transcriptOpen = ref(false)
const leads = computed(() => leadsQuery.data.value ?? [])
const detailQuery = useAICCSessionQuery(selectedSessionId)
const transcriptMessages = computed(() => detailQuery.data.value?.messages ?? [])
const transcriptLeadValues = computed(() => detailQuery.data.value?.lead_values ?? selectedLead.value?.values ?? [])
const sessionIntent = computed(() => detailQuery.data.value?.intent)

async function markRead(leadId: string) {
  activeLeadId.value = leadId
  try {
    await markReadMutation.mutateAsync(leadId)
    message.success(t('aicc.leads.markedRead'))
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('aicc.leads.markFailed'))
  } finally {
    activeLeadId.value = undefined
  }
}

async function markReadQuietly(leadId: string) {
  activeLeadId.value = leadId
  try {
    await markReadMutation.mutateAsync(leadId)
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('aicc.leads.markFailed'))
  } finally {
    activeLeadId.value = undefined
  }
}

function openLeadConversation(lead: AICCLead) {
  if (!lead.latest_session_id) return
  selectedLead.value = lead
  selectedSessionId.value = lead.latest_session_id
  transcriptOpen.value = true
  if (lead.unread && canManageAICC.value) {
    void markReadQuietly(lead.id)
  }
}

function closeLeadConversation() {
  transcriptOpen.value = false
}

async function exportLeads() {
  isExporting.value = true
  try {
    const { blob, filename } = await downloadAICCLeadsCSV(consoleContext.selectedOrgId.value)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename ?? 'aicc-leads.csv'
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  } catch (err) {
    message.error(err instanceof Error ? err.message : t('aicc.leads.exportFailed'))
  } finally {
    isExporting.value = false
  }
}

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

// focusEvidence 让运营人员从字段直接跳回对应访客原话，避免把模型画像当成不可核验的结论。
function focusEvidence(messageID?: string) {
  if (!messageID) return
  // 不拼接 CSS 选择器，避免消息 ID 中的特殊字符影响定位；data 属性精确比较即可。
  Array.from(document.querySelectorAll<HTMLElement>('[data-message-id]'))
    .find(element => element.dataset.messageId === messageID)
    ?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

function intentLevelLabel(level?: string) {
  return t(`aicc.leads.intentLevels.${level === 'high' || level === 'medium' || level === 'low' ? level : 'unknown'}`)
}
</script>

<style scoped>
.leads-view {
  display: grid;
  gap: 14px;
  min-height: 360px;
  padding: 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.panel-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.panel-heading h4 {
  margin: 2px 0 0;
  font-size: 16px;
}

.lead-list {
  display: grid;
  gap: 10px;
}

.lead-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto auto;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.lead-main {
  display: grid;
  gap: 4px;
  min-width: 0;
}

.lead-values {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.lead-values span {
  max-width: 220px;
  padding: 2px 7px;
  border-radius: 999px;
  background: var(--color-surface-muted);
  color: var(--color-text-secondary);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.message-sources {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.message-sources span {
  padding: 2px 6px;
  border-radius: 999px;
  color: var(--color-text-secondary);
  background: var(--color-surface-muted);
  font-size: 12px;
}

.message-sources em {
  margin-left: 4px;
  color: var(--color-warning-text);
  font-style: normal;
}

.intent-profile {
  display: grid;
  gap: 8px;
  padding: 10px;
  border: 1px solid var(--color-brand);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.intent-fields {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.intent-fields button {
  border: 0;
  border-bottom: 1px dashed var(--color-brand);
  color: var(--color-text-primary);
  background: transparent;
  cursor: pointer;
}

.lead-main small,
.empty-state span {
  color: var(--color-text-secondary);
}

.empty-state {
  display: grid;
  place-items: center;
  gap: 8px;
  min-height: 240px;
  color: var(--color-text-secondary);
  text-align: center;
}

.empty-state.compact {
  min-height: 220px;
}

.empty-state strong {
  color: var(--color-text-primary);
}

.lead-transcript-overlay {
  position: fixed;
  inset: 0;
  z-index: 30;
  display: flex;
  justify-content: flex-end;
  background: rgba(15, 23, 42, 0.28);
}

.lead-transcript-drawer {
  display: grid;
  align-content: start;
  gap: 14px;
  width: min(560px, 100%);
  height: 100vh;
  padding: 18px;
  overflow: auto;
  border-left: 1px solid var(--color-divider);
  background: var(--color-surface-muted);
  box-shadow: -18px 0 48px rgba(15, 23, 42, 0.18);
}

.transcript-stack {
  display: grid;
  gap: 10px;
}

.drawer-lead-values {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.drawer-lead-values span {
  display: grid;
  min-width: 120px;
  gap: 2px;
}

.drawer-lead-values strong {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.drawer-lead-values small {
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

.message-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.message-meta small {
  color: var(--color-text-secondary);
}

.message-item p {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}

@media (max-width: 760px) {
  .panel-heading,
  .lead-row {
    align-items: stretch;
    grid-template-columns: 1fr;
  }

  .panel-heading {
    display: grid;
  }
}
</style>
