<template>
  <main class="aicc-page">
    <section class="aicc-hero">
      <div>
        <p class="eyebrow">AI Integrated Customer Care</p>
        <h2>{{ t('aicc.manager.title') }}</h2>
        <p>{{ t('aicc.manager.subtitle') }}</p>
      </div>
      <n-button type="primary" @click="startCreate">
        <template #icon><Plus :size="16" /></template>
        {{ t('aicc.manager.createAgent') }}
      </n-button>
    </section>

    <section class="aicc-shell">
      <aside class="agent-rail">
        <div class="rail-heading">
          <strong>{{ t('aicc.manager.agents') }}</strong>
          <n-tag size="small" :bordered="false">{{ agents.length }}</n-tag>
        </div>
        <div v-if="agentsQuery.isLoading.value" class="state-text">{{ t('aicc.manager.loading') }}</div>
        <p v-else-if="agentsQuery.error.value" class="state-text danger">{{ agentsQuery.error.value.message }}</p>
        <template v-else>
          <button
            v-for="agent in agents"
            :key="agent.id"
            class="agent-card"
            :class="{ active: agent.id === selectedAgentId }"
            type="button"
            @click="selectAgent(agent.id)"
          >
            <span class="agent-topline">
              <strong>{{ agent.name }}</strong>
              <n-tag size="small" :type="statusMeta(agent.status).type" :bordered="false">
                {{ statusMeta(agent.status).label }}
              </n-tag>
            </span>
            <small>{{ agent.scenario || t('aicc.manager.noScenario') }}</small>
          </button>
        </template>
        <div v-if="!agentsQuery.isLoading.value && agents.length === 0" class="empty-block">
          <MessageSquareText :size="28" />
          <strong>{{ t('aicc.manager.emptyAgentsTitle') }}</strong>
          <span>{{ t('aicc.manager.emptyAgentsDesc') }}</span>
        </div>
      </aside>

      <section class="editor-panel">
        <div class="editor-toolbar">
          <div>
            <p class="eyebrow">{{ form.id ? t('aicc.manager.editAgent') : t('aicc.manager.newAgent') }}</p>
            <h3>{{ form.name || t('aicc.manager.unnamedAgent') }}</h3>
          </div>
          <n-space>
            <n-button v-if="selectedAgent" :disabled="statusBusy" @click="toggleStatus">
              <template #icon>
                <component :is="isSelectedRunning ? PauseCircle : PlayCircle" :size="16" />
              </template>
              {{ isSelectedRunning ? t('aicc.manager.stopReception') : t('aicc.manager.startReception') }}
            </n-button>
            <n-button v-if="selectedAgent" type="error" ghost :disabled="deleteBusy" @click="deleteModalOpen = true">
              <template #icon><Trash2 :size="16" /></template>
              {{ t('aicc.manager.delete') }}
            </n-button>
          </n-space>
        </div>

        <n-alert v-if="feedback" :type="feedbackDanger ? 'error' : 'success'" :bordered="false" class="feedback">
          {{ feedback }}
        </n-alert>

        <n-tabs v-model:value="activeSection" type="segment" animated class="aicc-tabs">
          <n-tab-pane name="config" :tab="t('aicc.manager.tabs.config')">
            <div class="status-grid">
              <div class="status-tile">
                <span>{{ t('aicc.manager.status.runtime') }}</span>
                <strong>{{ selectedAgent ? statusMeta(selectedAgent.status).label : t('aicc.manager.status.draft') }}</strong>
              </div>
              <div class="status-tile">
                <span>{{ t('aicc.manager.status.retention') }}</span>
                <strong>{{ t('aicc.manager.status.days', { count: form.retention_days || 0 }) }}</strong>
              </div>
              <div class="status-tile">
                <span>{{ t('aicc.manager.status.publicEntry') }}</span>
                <strong>{{ selectedAgent?.public_token ? t('aicc.manager.status.generated') : t('aicc.manager.status.generatedAfterSave') }}</strong>
              </div>
            </div>

            <n-form class="agent-form" :model="form" label-placement="top" @submit.prevent="submitForm">
              <div class="agent-fields">
                <div>
                  <n-form-item :label="t('aicc.manager.form.name')" required>
                    <n-input v-model:value="form.name" maxlength="80" :placeholder="t('aicc.manager.form.namePlaceholder')" />
                  </n-form-item>
                </div>
                <div>
                  <n-form-item :label="t('aicc.manager.form.retentionDays')">
                    <n-input-number v-model:value="form.retention_days" :min="1" :max="3650" style="width: 100%" />
                  </n-form-item>
                </div>
                <div>
                  <n-form-item :label="t('aicc.manager.form.privacyMode')">
                    <n-select v-model:value="form.privacy_mode" :options="privacyOptions" />
                  </n-form-item>
                </div>
                <div>
                  <n-form-item :label="t('aicc.manager.form.greeting')">
                    <n-input v-model:value="form.greeting" maxlength="240" :placeholder="t('aicc.manager.form.greetingPlaceholder')" />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item :label="t('aicc.manager.form.scenario')">
                    <n-input
                      v-model:value="form.scenario"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 5 }"
                      :placeholder="t('aicc.manager.form.scenarioPlaceholder')"
                    />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item :label="t('aicc.manager.form.answerBoundary')">
                    <n-input
                      v-model:value="form.answer_boundary"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 5 }"
                      :placeholder="t('aicc.manager.form.answerBoundaryPlaceholder')"
                    />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item :label="t('aicc.manager.form.privacyText')">
                    <n-input
                      v-model:value="form.privacy_text"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 5 }"
                      :placeholder="t('aicc.manager.form.privacyTextPlaceholder')"
                    />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item :label="t('aicc.manager.form.allowedDomains')">
                    <n-input
                      v-model:value="form.allowed_domains_text"
                      type="textarea"
                      :autosize="{ minRows: 2, maxRows: 4 }"
                      :placeholder="t('aicc.manager.form.allowedDomainsPlaceholder')"
                    />
                  </n-form-item>
                </div>
              </div>
              <n-space justify="end">
                <n-button attr-type="button" @click="resetForm">{{ t('aicc.manager.form.reset') }}</n-button>
                <n-button type="primary" attr-type="submit" :loading="submitBusy">
                  <template #icon><Save :size="16" /></template>
                  {{ t('aicc.manager.form.saveConfig') }}
                </n-button>
              </n-space>
            </n-form>

            <div class="operations-panel">
              <div class="section-heading">
                <div>
                  <p class="eyebrow">{{ t('aicc.manager.delivery.eyebrow') }}</p>
                  <strong>{{ t('aicc.manager.delivery.title') }}</strong>
                </div>
                <n-tag v-if="settingsQuery.data.value?.blocked_visitor_count !== undefined" size="small" :bordered="false">
                  {{ t('aicc.manager.delivery.blockedVisitors', { count: settingsQuery.data.value?.blocked_visitor_count }) }}
                </n-tag>
              </div>
              <div v-if="!selectedAgent" class="state-text">{{ t('aicc.manager.delivery.noAgent') }}</div>
              <template v-else>
                <div class="delivery-grid">
                  <div class="public-link-box">
                    <span>{{ t('aicc.manager.delivery.publicLink') }}</span>
                    <n-input :value="publicLink || t('aicc.manager.status.generatedAfterSave')" readonly :input-props="{ readonly: true }" />
                    <n-space>
                      <n-button size="small" :disabled="!publicLink" @click="copyText(publicLink)">
                        <template #icon><Copy :size="14" /></template>
                        {{ t('aicc.manager.delivery.copy') }}
                      </n-button>
                      <n-button size="small" :disabled="!publicLink" @click="openPublicLink">
                        <template #icon><ExternalLink :size="14" /></template>
                        {{ t('aicc.manager.delivery.preview') }}
                      </n-button>
                    </n-space>
                  </div>
                  <div class="qr-box">
                    <div v-if="qrDataUrl" class="qr-preview">
                      <img :src="qrDataUrl" :alt="t('aicc.manager.delivery.qrAlt')" />
                    </div>
                    <div v-else class="qr-placeholder">
                      <QrCode :size="30" />
                      <span>{{ t('aicc.manager.delivery.qrPending') }}</span>
                    </div>
                    <n-button size="small" :disabled="!qrDataUrl" @click="downloadQRCode">
                      <template #icon><Download :size="14" /></template>
                      {{ t('aicc.manager.delivery.downloadPng') }}
                    </n-button>
                  </div>
                </div>

                <n-form class="settings-form" :model="settingsForm" label-placement="top">
                  <div class="settings-grid">
                    <n-form-item :label="t('aicc.manager.delivery.messageLimit')">
                      <n-input-number v-model:value="settingsForm.message_limit_per_session" :min="1" :max="1000" style="width: 100%" />
                    </n-form-item>
                    <n-form-item :label="t('aicc.manager.delivery.resumeTtl')">
                      <n-input-number v-model:value="settingsForm.session_resume_ttl_minutes" :min="1" :max="1440" style="width: 100%" />
                    </n-form-item>
                    <n-form-item :label="t('aicc.manager.delivery.enableBlockedVisitors')">
                      <n-switch v-model:value="settingsForm.blocked_visitor_enabled" />
                    </n-form-item>
                    <n-form-item class="field-full" :label="t('aicc.manager.delivery.sensitiveWords')">
                      <n-input
                        v-model:value="settingsForm.sensitive_words_text"
                        type="textarea"
                        :autosize="{ minRows: 3, maxRows: 6 }"
                        :placeholder="t('aicc.manager.delivery.sensitiveWordsPlaceholder')"
                      />
                    </n-form-item>
                  </div>
                </n-form>
                <n-space justify="end">
                  <n-button :loading="settingsBusy" @click="saveSettings">
                    <template #icon><Save :size="16" /></template>
                    {{ t('aicc.manager.delivery.saveSettings') }}
                  </n-button>
                </n-space>
              </template>
            </div>

            <div ref="knowledgePanelEl" class="knowledge-panel">
              <div class="section-heading">
                <div>
                  <p class="eyebrow">{{ t('aicc.manager.knowledge.eyebrow') }}</p>
                  <strong>{{ t('aicc.manager.knowledge.title') }}</strong>
                </div>
                <n-button :disabled="!selectedAgent" @click="openDedicatedKnowledge">
                  <template #icon><ExternalLink :size="16" /></template>
                  {{ t('aicc.manager.knowledge.dedicatedDocs') }}
                </n-button>
              </div>
              <div v-if="!selectedAgent" class="state-text">{{ t('aicc.manager.knowledge.noAgent') }}</div>
              <template v-else>
                <n-checkbox v-model:checked="knowledgeForm.use_org_knowledge">
                  {{ t('aicc.manager.knowledge.useOrgKnowledge') }}
                </n-checkbox>
                <n-form-item :label="t('aicc.manager.knowledge.industryKnowledge')">
                  <n-select
                    v-model:value="knowledgeForm.industry_knowledge_base_ids"
                    multiple
                    clearable
                    filterable
                    :loading="knowledgeOptionsQuery.isFetching.value"
                    :options="industryKnowledgeOptions"
                    :placeholder="t('aicc.manager.knowledge.industryPlaceholder')"
                  />
                </n-form-item>
                <n-form-item :label="t('aicc.manager.knowledge.dedicatedDocuments')">
                  <n-select
                    v-model:value="knowledgeForm.app_document_ids"
                    multiple
                    clearable
                    filterable
                    :loading="knowledgeOptionsQuery.isFetching.value"
                    :options="appDocumentOptions"
                    :placeholder="t('aicc.manager.knowledge.appDocsPlaceholder')"
                  />
                </n-form-item>
                <n-space justify="end">
                  <n-button :loading="knowledgeBusy" @click="saveKnowledge">
                    <template #icon><Save :size="16" /></template>
                    {{ t('aicc.manager.knowledge.save') }}
                  </n-button>
                </n-space>
              </template>
            </div>

            <div class="lead-field-panel">
              <div class="section-heading">
                <div>
                  <p class="eyebrow">{{ t('aicc.manager.leadFields.eyebrow') }}</p>
                  <strong>{{ t('aicc.manager.leadFields.title') }}</strong>
                </div>
                <n-button size="small" :disabled="!selectedAgent" @click="addLeadField">
                  <template #icon><Plus :size="14" /></template>
                  {{ t('aicc.manager.leadFields.add') }}
                </n-button>
              </div>
              <div v-if="!selectedAgent" class="state-text">{{ t('aicc.manager.leadFields.noAgent') }}</div>
              <div v-else-if="leadFieldRows.length === 0" class="empty-inline">{{ t('aicc.manager.leadFields.empty') }}</div>
              <div v-else class="lead-field-list">
                <div v-for="(field, index) in leadFieldRows" :key="field.local_id" class="lead-field-row">
                  <n-input v-model:value="field.label" :placeholder="t('aicc.manager.leadFields.labelPlaceholder')" maxlength="128" />
                  <n-input v-model:value="field.field_key" :placeholder="t('aicc.manager.leadFields.keyPlaceholder')" maxlength="64" />
                  <n-select v-model:value="field.field_type" :options="leadFieldTypeOptions" />
                  <n-checkbox v-model:checked="field.required">{{ t('aicc.manager.leadFields.required') }}</n-checkbox>
                  <n-input v-model:value="field.prompt_text" :placeholder="t('aicc.manager.leadFields.promptPlaceholder')" maxlength="160" />
                  <n-button quaternary circle type="error" @click="removeLeadField(index)">
                    <template #icon><Trash2 :size="15" /></template>
                  </n-button>
                </div>
              </div>
              <n-space justify="end">
                <n-button :disabled="!selectedAgent" :loading="leadFieldBusy" @click="saveLeadFields">
                  <template #icon><Save :size="16" /></template>
                  {{ t('aicc.manager.leadFields.save') }}
                </n-button>
              </n-space>
            </div>

            <div class="snippet-panel">
              <span>{{ t('aicc.manager.snippet.placeholder') }}</span>
              <code>{{ widgetSnippet }}</code>
              <n-button size="small" :disabled="!selectedAgent?.widget_token" @click="copyText(widgetSnippet)">
                <template #icon><Copy :size="14" /></template>
              </n-button>
            </div>
          </n-tab-pane>
          <n-tab-pane name="sessions" :tab="t('aicc.manager.tabs.sessions')">
            <AICCSessionsPage :agent-id="selectedAgentId" />
          </n-tab-pane>
          <n-tab-pane name="leads" :tab="t('aicc.manager.tabs.leads')">
            <AICCLeadsPage />
          </n-tab-pane>
          <n-tab-pane name="analytics" :tab="t('aicc.manager.tabs.analytics')">
            <AICCAnalyticsPage :agent-id="selectedAgentId" :agent-count="agents.length" :active-agent-count="activeAgentCount" />
          </n-tab-pane>
        </n-tabs>
      </section>
    </section>

    <ConfirmActionModal
      :visible="deleteModalOpen"
      :title="t('aicc.manager.deleteModal.title')"
      :message="t('aicc.manager.deleteModal.message', { name: selectedAgent?.name ?? '' })"
      :verify-value="selectedAgent?.name"
      :busy="deleteBusy"
      :confirm-label="t('aicc.manager.deleteModal.confirm')"
      @cancel="deleteModalOpen = false"
      @confirm="deleteAgent"
    />
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import QRCode from 'qrcode'
import {
  NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NTag,
  NSwitch, NTabPane, NTabs, type SelectOption,
} from 'naive-ui'
import { useI18n } from 'vue-i18n'
import {
  Copy, Download, ExternalLink, MessageSquareText, PauseCircle, PlayCircle, Plus, QrCode, Save, Trash2,
} from 'lucide-vue-next'

import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import AICCAnalyticsPage from '@/pages/aicc/AICCAnalyticsPage.vue'
import AICCLeadsPage from '@/pages/aicc/AICCLeadsPage.vue'
import AICCSessionsPage from '@/pages/aicc/AICCSessionsPage.vue'
import {
  useAICCAgentsQuery,
  useAICCKnowledgeQuery,
  useAICCKnowledgeOptionsQuery,
  useAICCLeadFieldsQuery,
  useAICCSettingsQuery,
  useCreateAICCAgent,
  useDeleteAICCAgent,
  useReplaceAICCKnowledge,
  useReplaceAICCLeadFields,
  useSetAICCAgentStatus,
  useUpdateAICCAgent,
  useUpdateAICCSettings,
} from '@/api/hooks/useAICC'
import type {
  AICCAgent,
  AICCAgentPayload,
  AICCAgentSettings,
  AICCAgentSettingsPayload,
  AICCAgentStatus,
  AICCLeadField,
  AICCLeadFieldPayload,
  AICCKnowledgePayload,
  AICCPrivacyMode,
} from '@/domain/aicc'
import { isAICCAgentRunning } from '@/domain/aicc'

// AICCManagerPage 是企业管理员维护 AI Integrated Customer Care 在线客服智能体和运营数据的入口。
type InitialSection = 'reception' | 'sessions' | 'knowledge' | 'leads' | 'analytics' | 'settings'
type ManagerTab = 'config' | 'sessions' | 'leads' | 'analytics'

const props = withDefaults(defineProps<{
  initialSection?: InitialSection
}>(), {
  initialSection: 'reception',
})

interface AgentForm extends AICCAgentPayload {
  id?: string
  privacy_mode: AICCPrivacyMode
  retention_days: number
  allowed_domains_text: string
}

interface LeadFieldRow extends AICCLeadFieldPayload {
  local_id: string
}

interface KnowledgeForm extends AICCKnowledgePayload {}

interface SettingsForm {
  message_limit_per_session: number
  sensitive_words_text: string
  blocked_visitor_enabled: boolean
  blocked_visitor_threshold_json?: Record<string, unknown>
  session_resume_ttl_minutes: number
}

const selectedAgentId = ref<string | undefined>()
const deleteModalOpen = ref(false)
const feedback = ref('')
const feedbackDanger = ref(false)
const activeSection = ref<ManagerTab>(sectionToTab(props.initialSection))
const knowledgePanelEl = ref<HTMLElement>()
const { t } = useI18n()

const agentsQuery = useAICCAgentsQuery()
const settingsQuery = useAICCSettingsQuery(selectedAgentId)
const leadFieldsQuery = useAICCLeadFieldsQuery(selectedAgentId)
const knowledgeQuery = useAICCKnowledgeQuery(selectedAgentId)
const knowledgeOptionsQuery = useAICCKnowledgeOptionsQuery(selectedAgentId)
const createMutation = useCreateAICCAgent()
const updateMutation = useUpdateAICCAgent()
const settingsMutation = useUpdateAICCSettings()
const leadFieldMutation = useReplaceAICCLeadFields()
const knowledgeMutation = useReplaceAICCKnowledge()
const statusMutation = useSetAICCAgentStatus()
const deleteMutation = useDeleteAICCAgent()

const agents = computed(() => agentsQuery.data.value ?? [])
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentId.value))
const selectedKnowledgeAppId = computed(() => knowledgeQuery.data.value?.app_id || selectedAgent.value?.app_id)
const isSelectedRunning = computed(() => selectedAgent.value ? isAICCAgentRunning(selectedAgent.value) : false)
const activeAgentCount = computed(() => agents.value.filter(agent => isAICCAgentRunning(agent)).length)
const submitBusy = computed(() => createMutation.isPending.value || updateMutation.isPending.value)
const statusBusy = computed(() => statusMutation.isPending.value)
const deleteBusy = computed(() => deleteMutation.isPending.value)
const settingsBusy = computed(() => settingsMutation.isPending.value || settingsQuery.isFetching.value)
const leadFieldBusy = computed(() => leadFieldMutation.isPending.value || leadFieldsQuery.isFetching.value)
const knowledgeBusy = computed(() => knowledgeMutation.isPending.value || knowledgeQuery.isFetching.value)

const form = reactive<AgentForm>(emptyForm())
const settingsForm = reactive<SettingsForm>(emptySettingsForm())
const leadFieldRows = ref<LeadFieldRow[]>([])
const knowledgeForm = reactive<KnowledgeForm>(emptyKnowledgeForm())
const qrDataUrl = ref('')

const industryKnowledgeOptions = computed<SelectOption[]>(() =>
  (knowledgeOptionsQuery.data.value?.industry_knowledge_bases ?? []).map(item => ({
    label: `${item.name} (${item.document_count})`,
    value: item.id,
  })),
)

const appDocumentOptions = computed<SelectOption[]>(() =>
  (knowledgeOptionsQuery.data.value?.app_documents ?? []).map(item => ({
    label: item.name,
    value: item.id,
  })),
)

const privacyOptions = computed<SelectOption[]>(() => [
  { label: t('aicc.manager.options.privacyNotice'), value: 'notice' },
  { label: t('aicc.manager.options.consentRequired'), value: 'consent_required' },
])

const leadFieldTypeOptions = computed<SelectOption[]>(() => [
  { label: t('aicc.manager.options.fieldText'), value: 'text' },
  { label: t('aicc.manager.options.fieldPhone'), value: 'phone' },
  { label: t('aicc.manager.options.fieldEmail'), value: 'email' },
  { label: t('aicc.manager.options.fieldNumber'), value: 'number' },
])

const publicLink = computed(() => {
  if (!selectedAgent.value?.public_token || typeof window === 'undefined') return ''
  return `${window.location.origin}/aicc/${selectedAgent.value.public_token}`
})

const widgetSnippet = computed(() => {
  const token = selectedAgent.value?.widget_token ?? t('aicc.manager.status.generatedAfterSave')
  const base = typeof window === 'undefined' ? '' : window.location.origin
  return `<script src="${base}/aicc-widget.js" data-aicc-widget-token="${token}"></` + 'script>'
})

watch(
  agents,
  (items) => {
    if (!selectedAgentId.value && items.length > 0) {
      selectedAgentId.value = items[0].id
    }
  },
  { immediate: true },
)

watch(selectedAgent, (agent) => {
  if (agent) fillForm(agent)
}, { immediate: true })

watch(
  () => settingsQuery.data.value,
  (settings) => {
    Object.assign(settingsForm, settings ? toSettingsForm(settings) : emptySettingsForm())
  },
  { immediate: true },
)

watch(publicLink, async (value) => {
  if (!value) {
    qrDataUrl.value = ''
    return
  }
  try {
    qrDataUrl.value = await QRCode.toDataURL(value, { width: 192, margin: 1 })
  } catch {
    qrDataUrl.value = ''
  }
}, { immediate: true })

watch(
  () => leadFieldsQuery.data.value,
  (fields) => {
    leadFieldRows.value = (fields ?? []).map(toLeadFieldRow)
  },
  { immediate: true },
)

watch(
  () => knowledgeQuery.data.value,
  (knowledge) => {
    Object.assign(knowledgeForm, knowledge
      ? {
          use_org_knowledge: knowledge.use_org_knowledge,
          industry_knowledge_base_ids: [...knowledge.industry_knowledge_base_ids],
          app_document_ids: [...knowledge.app_document_ids],
        }
      : emptyKnowledgeForm())
  },
  { immediate: true },
)

watch(
  () => props.initialSection,
  (section) => {
    activeSection.value = sectionToTab(section)
    if (section === 'knowledge') {
      void scrollKnowledgePanelIntoView()
    }
  },
  { immediate: true },
)

// 路由级入口只负责打开现有工作台区域；知识库暂复用配置页并滚动到知识配置块。
function sectionToTab(section: InitialSection): ManagerTab {
  if (section === 'sessions' || section === 'leads' || section === 'analytics') return section
  return 'config'
}

async function scrollKnowledgePanelIntoView() {
  await nextTick()
  if (typeof knowledgePanelEl.value?.scrollIntoView === 'function') {
    knowledgePanelEl.value.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }
}

function emptyForm(): AgentForm {
  return {
    id: undefined,
    name: '',
    scenario: '',
    greeting: t('aicc.manager.defaults.greeting'),
    answer_boundary: '',
    privacy_mode: 'notice',
    privacy_text: t('aicc.manager.defaults.privacyText'),
    retention_days: 180,
    allowed_domains_text: '',
  }
}

function emptyKnowledgeForm(): KnowledgeForm {
  return {
    use_org_knowledge: true,
    industry_knowledge_base_ids: [],
    app_document_ids: [],
  }
}

function emptySettingsForm(): SettingsForm {
  return {
    message_limit_per_session: 100,
    sensitive_words_text: '',
    blocked_visitor_enabled: true,
    blocked_visitor_threshold_json: undefined,
    session_resume_ttl_minutes: 30,
  }
}

function toSettingsForm(settings: AICCAgentSettings): SettingsForm {
  return {
    message_limit_per_session: settings.message_limit_per_session,
    sensitive_words_text: settings.sensitive_words.join('\n'),
    blocked_visitor_enabled: settings.blocked_visitor_enabled,
    blocked_visitor_threshold_json: settings.blocked_visitor_threshold_json,
    session_resume_ttl_minutes: settings.session_resume_ttl_minutes,
  }
}

function fillForm(agent: AICCAgent) {
  form.id = agent.id
  form.name = agent.name
  form.scenario = agent.scenario ?? ''
  form.greeting = agent.greeting ?? ''
  form.answer_boundary = agent.answer_boundary ?? ''
  form.privacy_mode = agent.privacy_mode
  form.privacy_text = agent.privacy_text ?? ''
  form.retention_days = agent.retention_days || 180
  form.allowed_domains_text = (agent.allowed_domains ?? []).join('\n')
}

function payloadFromForm(): AICCAgentPayload {
  return {
    name: form.name.trim(),
    scenario: form.scenario?.trim() || undefined,
    greeting: form.greeting?.trim() || undefined,
    answer_boundary: form.answer_boundary?.trim() || undefined,
    privacy_mode: form.privacy_mode,
    privacy_text: form.privacy_text?.trim() || undefined,
    retention_days: form.retention_days,
    allowed_domains: parseAllowedDomains(form.allowed_domains_text),
  }
}

function parseAllowedDomains(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map(item => item.trim())
    .filter(Boolean)
}

function parseSensitiveWords(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map(item => item.trim())
    .filter(Boolean)
}

function setFeedback(message: string, danger = false) {
  feedback.value = message
  feedbackDanger.value = danger
}

function selectAgent(agentId?: string) {
  selectedAgentId.value = agentId
}

function startCreate() {
  selectedAgentId.value = undefined
  delete form.id
  Object.assign(form, emptyForm())
  feedback.value = ''
}

function resetForm() {
  if (selectedAgent.value) {
    fillForm(selectedAgent.value)
    return
  }
  delete form.id
  Object.assign(form, emptyForm())
}

function toLeadFieldRow(field: AICCLeadField): LeadFieldRow {
  return {
    local_id: field.id || crypto.randomUUID(),
    field_key: field.field_key,
    label: field.label,
    field_type: field.field_type,
    required: field.required,
    prompt_text: field.prompt_text ?? '',
    sort_order: field.sort_order ?? 0,
  }
}

function addLeadField() {
  const next = leadFieldRows.value.length + 1
  leadFieldRows.value.push({
    local_id: crypto.randomUUID(),
    field_key: next === 1 ? 'phone' : `field_${next}`,
    label: next === 1 ? t('aicc.manager.leadFields.defaultPhone') : t('aicc.manager.leadFields.defaultField', { count: next }),
    field_type: next === 1 ? 'phone' : 'text',
    required: next === 1,
    prompt_text: '',
    sort_order: next,
  })
}

function removeLeadField(index: number) {
  leadFieldRows.value.splice(index, 1)
}

async function saveLeadFields() {
  if (!selectedAgent.value) return
  const fields: AICCLeadFieldPayload[] = leadFieldRows.value.map((field, index) => ({
    field_key: field.field_key.trim(),
    label: field.label.trim(),
    field_type: field.field_type,
    required: field.required,
    prompt_text: field.prompt_text?.trim() || undefined,
    sort_order: index + 1,
  }))
  if (fields.some(field => !field.field_key || !field.label)) {
    setFeedback(t('aicc.manager.feedback.missingLeadField'), true)
    return
  }
  try {
    await leadFieldMutation.mutateAsync({ agentId: selectedAgent.value.id, fields })
    setFeedback(t('aicc.manager.feedback.leadFieldsSaved'))
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : t('aicc.manager.feedback.leadFieldsSaveFailed'), true)
  }
}

async function saveKnowledge() {
  if (!selectedAgent.value) return
  try {
    await knowledgeMutation.mutateAsync({
      agentId: selectedAgent.value.id,
      payload: {
        use_org_knowledge: knowledgeForm.use_org_knowledge,
        industry_knowledge_base_ids: [...knowledgeForm.industry_knowledge_base_ids],
        app_document_ids: [...knowledgeForm.app_document_ids],
      },
    })
    setFeedback(t('aicc.manager.feedback.knowledgeSaved'))
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : t('aicc.manager.feedback.knowledgeSaveFailed'), true)
  }
}

async function saveSettings() {
  if (!selectedAgent.value) return
  const payload: AICCAgentSettingsPayload = {
    message_limit_per_session: settingsForm.message_limit_per_session,
    sensitive_words: parseSensitiveWords(settingsForm.sensitive_words_text),
    blocked_visitor_enabled: settingsForm.blocked_visitor_enabled,
    blocked_visitor_threshold_json: settingsForm.blocked_visitor_threshold_json,
    session_resume_ttl_minutes: settingsForm.session_resume_ttl_minutes,
  }
  try {
    const settings = await settingsMutation.mutateAsync({ agentId: selectedAgent.value.id, payload })
    Object.assign(settingsForm, toSettingsForm(settings))
    setFeedback(t('aicc.manager.feedback.settingsSaved'))
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : t('aicc.manager.feedback.settingsSaveFailed'), true)
  }
}

function statusMeta(status?: AICCAgentStatus): { label: string; type: 'success' | 'warning' | 'default' | 'error' } {
  if (status === 'active') return { label: t('aicc.manager.status.active'), type: 'success' }
  if (status === 'paused') return { label: t('aicc.manager.status.paused'), type: 'warning' }
  if (status === 'deleted') return { label: t('aicc.manager.status.deleted'), type: 'error' }
  return { label: t('aicc.manager.status.draft'), type: 'default' }
}

async function submitForm() {
  if (!form.name.trim()) {
    setFeedback(t('aicc.manager.feedback.missingName'), true)
    return
  }
  try {
    const payload = payloadFromForm()
    const agent = form.id
      ? await updateMutation.mutateAsync({ agentId: form.id, payload })
      : await createMutation.mutateAsync(payload)
    selectedAgentId.value = agent.id
    fillForm(agent)
    setFeedback(t('aicc.manager.feedback.configSaved'))
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : t('aicc.manager.feedback.saveFailed'), true)
  }
}

async function toggleStatus() {
  if (!selectedAgent.value) return
  try {
    const action = isSelectedRunning.value ? 'stop' : 'start'
    await statusMutation.mutateAsync({ agentId: selectedAgent.value.id, action })
    setFeedback(action === 'start' ? t('aicc.manager.feedback.started') : t('aicc.manager.feedback.stopped'))
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : t('aicc.manager.feedback.statusSwitchFailed'), true)
  }
}

async function deleteAgent() {
  if (!selectedAgent.value) return
  try {
    const deletedId = selectedAgent.value.id
    await deleteMutation.mutateAsync(deletedId)
    deleteModalOpen.value = false
    if (selectedAgentId.value === deletedId) startCreate()
    setFeedback(t('aicc.manager.feedback.deleted'))
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : t('aicc.manager.feedback.deleteFailed'), true)
  }
}

async function copyText(text: string) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    setFeedback(t('aicc.manager.feedback.copied'))
  } catch {
    setFeedback(t('aicc.manager.feedback.copyFailed'), true)
  }
}

function openPublicLink() {
  if (!publicLink.value) return
  window.open(publicLink.value, '_blank', 'noopener,noreferrer')
}

function downloadQRCode() {
  if (!qrDataUrl.value || !selectedAgent.value) return
  const anchor = document.createElement('a')
  anchor.href = qrDataUrl.value
  anchor.download = `${selectedAgent.value.name || 'aicc'}-qrcode.png`
  anchor.click()
}

function openDedicatedKnowledge() {
  if (!selectedKnowledgeAppId.value) return
  window.open(`/apps/${selectedKnowledgeAppId.value}/knowledge`, '_blank', 'noopener,noreferrer')
}
</script>

<style scoped>
.aicc-page {
  display: grid;
  gap: 16px;
  min-height: 0;
}

.aicc-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px;
  border: 1px solid #1f2937;
  border-radius: 8px;
  color: #f8fafc;
  background: linear-gradient(135deg, #111827 0%, #1f2937 58%, #ff6a00 160%);
}

.aicc-hero h2,
.aicc-hero p {
  margin: 0;
}

.aicc-hero h2 {
  font-size: 22px;
}

.aicc-hero p:last-child {
  margin-top: 6px;
  color: #d1d5db;
  font-size: 13px;
}

.aicc-shell {
  display: grid;
  grid-template-columns: minmax(260px, 0.35fr) minmax(0, 1fr);
  gap: 16px;
  min-height: 0;
}

.agent-rail,
.editor-panel {
  min-width: 0;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.agent-rail {
  display: grid;
  align-content: start;
  gap: 8px;
  padding: 12px;
}

.rail-heading,
.agent-topline,
.editor-toolbar,
.section-heading,
.snippet-panel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.agent-card {
  display: grid;
  gap: 8px;
  width: 100%;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface-muted);
  color: var(--color-text-primary);
  text-align: left;
  cursor: pointer;
}

.agent-card.active {
  border-color: var(--color-brand);
  background: var(--color-brand-soft);
  box-shadow: inset 3px 0 0 var(--color-brand);
}

.agent-card small,
.empty-block,
.status-tile span {
  color: var(--color-text-secondary);
}

.empty-block {
  display: grid;
  justify-items: center;
  gap: 8px;
  padding: 28px 12px;
  text-align: center;
  font-size: 13px;
}

.editor-panel {
  display: grid;
  gap: 16px;
  padding: 16px;
}

.editor-toolbar h3 {
  margin: 0;
  font-size: 20px;
}

.feedback {
  margin: 0;
}

.aicc-tabs :deep(.n-tab-pane) {
  display: grid;
  gap: 16px;
  padding-top: 14px;
}

.status-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.status-tile {
  display: grid;
  gap: 4px;
  min-height: 72px;
  padding: 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.status-tile strong {
  font-size: 18px;
}

.agent-form {
  min-width: 0;
}

.agent-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 16px;
}

.field-full {
  grid-column: 1 / -1;
}

.operations-panel,
.knowledge-panel,
.lead-field-panel {
  display: grid;
  gap: 12px;
  min-width: 0;
  padding: 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.delivery-grid,
.settings-grid {
  display: grid;
  gap: 12px;
}

.delivery-grid {
  grid-template-columns: minmax(0, 1fr) 210px;
  align-items: stretch;
}

.public-link-box,
.qr-box {
  display: grid;
  gap: 10px;
  min-width: 0;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.public-link-box span {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.qr-box {
  justify-items: center;
}

.qr-preview,
.qr-placeholder {
  display: grid;
  width: 154px;
  height: 154px;
  place-items: center;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: #ffffff;
}

.qr-preview img {
  display: block;
  width: 142px;
  height: 142px;
}

.qr-placeholder {
  gap: 6px;
  color: var(--color-text-secondary);
  text-align: center;
  font-size: 12px;
}

.settings-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.section-heading strong {
  display: block;
  margin-top: 2px;
}

.empty-inline {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.lead-field-list {
  display: grid;
  gap: 10px;
}

.lead-field-row {
  display: grid;
  grid-template-columns: minmax(120px, 1fr) minmax(120px, 1fr) 110px 72px minmax(140px, 1fr) 36px;
  gap: 8px;
  align-items: center;
}

.snippet-panel {
  min-width: 0;
  padding: 12px 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.snippet-panel code {
  word-break: break-all;
}

.snippet-panel code {
  flex: 1;
  min-width: 0;
  color: var(--color-text-secondary);
  font-size: 12px;
}

@media (max-width: 900px) {
  .aicc-hero,
  .editor-toolbar,
  .section-heading,
  .snippet-panel {
    align-items: stretch;
    flex-direction: column;
  }

  .aicc-shell {
    grid-template-columns: 1fr;
  }

  .status-grid,
  .agent-fields,
  .delivery-grid,
  .settings-grid,
  .lead-field-row {
    grid-template-columns: 1fr;
  }

  .field-full {
    grid-column: auto;
  }
}
</style>
