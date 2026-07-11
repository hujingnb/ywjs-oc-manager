<template>
  <main class="aicc-page">
    <section class="aicc-page-heading">
      <div>
        <p class="eyebrow">{{ currentSectionEyebrow }}</p>
        <h2>{{ currentSectionTitle }}</h2>
        <p>{{ currentSectionDescription }}</p>
      </div>
    </section>

    <section class="aicc-shell">
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

        <div v-if="activeSection === 'config'" class="aicc-section-content">
          <div v-if="isReceptionRoute" class="status-grid">
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

          <n-form v-if="isSettingsRoute" class="agent-form" :model="form" label-placement="top" @submit.prevent="submitForm">
            <div class="agent-fields">
              <div>
                <n-form-item required>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.name')" :help="t('aicc.manager.form.help.name')" />
                  </template>
                  <n-input
                    v-model:value="form.name"
                    maxlength="80"
                    :input-props="{ id: 'aicc-agent-name', name: 'aicc_agent_name' }"
                    :placeholder="t('aicc.manager.form.namePlaceholder')"
                  />
                </n-form-item>
              </div>
              <div>
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.retentionDays')" :help="t('aicc.manager.form.help.retentionDays')" />
                  </template>
                  <n-input-number
                    v-model:value="form.retention_days"
                    :min="1"
                    :max="3650"
                    :input-props="{ id: 'aicc-retention-days', name: 'aicc_retention_days' }"
                    style="width: 100%"
                  />
                </n-form-item>
              </div>
              <div>
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.privacyMode')" :help="t('aicc.manager.form.help.privacyMode')" />
                  </template>
                  <n-select
                    v-model:value="form.privacy_mode"
                    :input-props="{ id: 'aicc-privacy-mode', name: 'aicc_privacy_mode' }"
                    :options="privacyOptions"
                  />
                </n-form-item>
              </div>
              <div>
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.greeting')" :help="t('aicc.manager.form.help.greeting')" />
                  </template>
                  <n-input
                    v-model:value="form.greeting"
                    maxlength="240"
                    :input-props="{ id: 'aicc-greeting', name: 'aicc_greeting' }"
                    :placeholder="t('aicc.manager.form.greetingPlaceholder')"
                  />
                </n-form-item>
              </div>
              <div class="field-full">
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.scenario')" :help="t('aicc.manager.form.help.scenario')" />
                  </template>
                  <n-input
                    v-model:value="form.scenario"
                    type="textarea"
                    :autosize="{ minRows: 3, maxRows: 5 }"
                    :input-props="{ id: 'aicc-scenario', name: 'aicc_scenario' }"
                    :placeholder="t('aicc.manager.form.scenarioPlaceholder')"
                  />
                </n-form-item>
              </div>
              <div class="field-full">
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.answerBoundary')" :help="t('aicc.manager.form.help.answerBoundary')" />
                  </template>
                  <n-input
                    v-model:value="form.answer_boundary"
                    type="textarea"
                    :autosize="{ minRows: 3, maxRows: 5 }"
                    :input-props="{ id: 'aicc-answer-boundary', name: 'aicc_answer_boundary' }"
                    :placeholder="t('aicc.manager.form.answerBoundaryPlaceholder')"
                  />
                </n-form-item>
              </div>
              <div class="field-full">
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.privacyText')" :help="t('aicc.manager.form.help.privacyText')" />
                  </template>
                  <n-input
                    v-model:value="form.privacy_text"
                    type="textarea"
                    :autosize="{ minRows: 3, maxRows: 5 }"
                    :input-props="{ id: 'aicc-privacy-text', name: 'aicc_privacy_text' }"
                    :placeholder="t('aicc.manager.form.privacyTextPlaceholder')"
                  />
                </n-form-item>
              </div>
              <div class="field-full">
                <n-form-item>
                  <template #label>
                    <FieldLabel :label="t('aicc.manager.form.allowedDomains')" :help="t('aicc.manager.form.help.allowedDomains')" />
                  </template>
                  <n-input
                    v-model:value="form.allowed_domains_text"
                    type="textarea"
                    :autosize="{ minRows: 2, maxRows: 4 }"
                    :input-props="{ id: 'aicc-allowed-domains', name: 'aicc_allowed_domains' }"
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

          <div v-if="isReceptionRoute || isSettingsRoute" class="operations-panel">
            <div class="section-heading">
              <div>
                <p class="eyebrow">{{ t('aicc.manager.delivery.eyebrow') }}</p>
                <strong>{{ isReceptionRoute ? t('aicc.manager.delivery.publicTitle') : t('aicc.manager.delivery.settingsTitle') }}</strong>
              </div>
              <n-tag v-if="isSettingsRoute && settingsQuery.data.value?.blocked_visitor_count !== undefined" size="small" :bordered="false">
                {{ t('aicc.manager.delivery.blockedVisitors', { count: settingsQuery.data.value?.blocked_visitor_count }) }}
              </n-tag>
            </div>
            <div v-if="!selectedAgent" class="state-text">{{ t('aicc.manager.delivery.noAgent') }}</div>
            <template v-else>
              <div v-if="isReceptionRoute" class="delivery-grid">
                <div class="public-link-box">
                  <span>{{ t('aicc.manager.delivery.publicLink') }}</span>
                  <n-input
                    :value="publicLink || t('aicc.manager.status.generatedAfterSave')"
                    readonly
                    :input-props="{ id: 'aicc-public-link', name: 'aicc_public_link', readonly: true }"
                  />
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

              <n-form v-if="isSettingsRoute" class="settings-form" :model="settingsForm" label-placement="top">
                <div class="settings-grid">
                  <n-form-item>
                    <template #label>
                      <FieldLabel :label="t('aicc.manager.delivery.messageLimit')" :help="t('aicc.manager.delivery.help.messageLimit')" />
                    </template>
                    <n-input-number
                      v-model:value="settingsForm.message_limit_per_session"
                      :min="1"
                      :max="1000"
                      :input-props="{ id: 'aicc-message-limit', name: 'aicc_message_limit' }"
                      style="width: 100%"
                    />
                  </n-form-item>
                  <n-form-item>
                    <template #label>
                      <FieldLabel :label="t('aicc.manager.delivery.resumeTtl')" :help="t('aicc.manager.delivery.help.resumeTtl')" />
                    </template>
                    <n-input-number
                      v-model:value="settingsForm.session_resume_ttl_minutes"
                      :min="1"
                      :max="1440"
                      :input-props="{ id: 'aicc-session-resume-ttl', name: 'aicc_session_resume_ttl' }"
                      style="width: 100%"
                    />
                  </n-form-item>
                  <n-form-item>
                    <template #label>
                      <FieldLabel :label="t('aicc.manager.delivery.enableBlockedVisitors')" :help="t('aicc.manager.delivery.help.enableBlockedVisitors')" />
                    </template>
                    <n-switch v-model:value="settingsForm.blocked_visitor_enabled" />
                  </n-form-item>
                  <n-form-item class="field-full">
                    <template #label>
                      <FieldLabel :label="t('aicc.manager.delivery.sensitiveWords')" :help="t('aicc.manager.delivery.help.sensitiveWords')" />
                    </template>
                    <n-input
                      v-model:value="settingsForm.sensitive_words_text"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 6 }"
                      :input-props="{ id: 'aicc-sensitive-words', name: 'aicc_sensitive_words' }"
                      :placeholder="t('aicc.manager.delivery.sensitiveWordsPlaceholder')"
                    />
                  </n-form-item>
                </div>
              </n-form>
              <n-space v-if="isSettingsRoute" justify="end">
                <n-button :loading="settingsBusy" @click="saveSettings">
                  <template #icon><Save :size="16" /></template>
                  {{ t('aicc.manager.delivery.saveSettings') }}
                </n-button>
              </n-space>
            </template>
          </div>

          <div v-if="isSettingsRoute" ref="knowledgePanelEl" class="knowledge-panel">
            <div class="section-heading">
              <div>
                <p class="eyebrow">{{ t('aicc.manager.knowledge.eyebrow') }}</p>
                <strong>{{ t('aicc.manager.knowledge.title') }}</strong>
              </div>
              <n-button :disabled="!selectedAgent" @click="openCurrentAgentKnowledge">
                <template #icon><ExternalLink :size="16" /></template>
                {{ t('aicc.manager.knowledge.manageCurrentKnowledge') }}
              </n-button>
            </div>
            <div v-if="!selectedAgent" class="state-text">{{ t('aicc.manager.knowledge.noAgent') }}</div>
            <template v-else>
              <n-checkbox v-model:checked="knowledgeForm.use_org_knowledge">
                <FieldLabel :label="t('aicc.manager.knowledge.useOrgKnowledge')" :help="t('aicc.manager.knowledge.help.useOrgKnowledge')" />
              </n-checkbox>
              <n-form-item>
                <template #label>
                  <FieldLabel :label="t('aicc.manager.knowledge.industryKnowledge')" :help="t('aicc.manager.knowledge.help.industryKnowledge')" />
                </template>
                <n-select
                  v-model:value="knowledgeForm.industry_knowledge_base_ids"
                  multiple
                  clearable
                  filterable
                  :loading="knowledgeOptionsQuery.isFetching.value"
                  :input-props="{ id: 'aicc-industry-knowledge', name: 'aicc_industry_knowledge' }"
                  :options="industryKnowledgeOptions"
                  :placeholder="t('aicc.manager.knowledge.industryPlaceholder')"
                />
              </n-form-item>
              <div class="current-knowledge-row">
                <div>
                  <FieldLabel :label="t('aicc.manager.knowledge.currentAgentKnowledge')" :help="t('aicc.manager.knowledge.help.currentAgentKnowledge')" />
                  <p>{{ t('aicc.manager.knowledge.currentAgentKnowledgeDesc') }}</p>
                </div>
                <n-tag size="small" type="success" :bordered="false">{{ t('aicc.manager.knowledge.enabled') }}</n-tag>
              </div>
              <n-space justify="end">
                <n-button :loading="knowledgeBusy" @click="saveKnowledge">
                  <template #icon><Save :size="16" /></template>
                  {{ t('aicc.manager.knowledge.save') }}
                </n-button>
              </n-space>
            </template>
          </div>

          <div v-if="isSettingsRoute" class="lead-field-panel">
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
                <n-input
                  v-model:value="field.label"
                  :input-props="{ id: `aicc-lead-field-label-${index}`, name: `aicc_lead_field_label_${index}` }"
                  :placeholder="t('aicc.manager.leadFields.labelPlaceholder')"
                  maxlength="128"
                />
                <n-input
                  v-model:value="field.field_key"
                  :input-props="{ id: `aicc-lead-field-key-${index}`, name: `aicc_lead_field_key_${index}` }"
                  :placeholder="t('aicc.manager.leadFields.keyPlaceholder')"
                  maxlength="64"
                />
                <n-select
                  v-model:value="field.field_type"
                  :input-props="{ id: `aicc-lead-field-type-${index}`, name: `aicc_lead_field_type_${index}` }"
                  :options="leadFieldTypeOptions"
                />
                <n-checkbox v-model:checked="field.required">{{ t('aicc.manager.leadFields.required') }}</n-checkbox>
                <n-input
                  v-model:value="field.prompt_text"
                  :input-props="{ id: `aicc-lead-field-prompt-${index}`, name: `aicc_lead_field_prompt_${index}` }"
                  :placeholder="t('aicc.manager.leadFields.promptPlaceholder')"
                  maxlength="160"
                />
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

          <div v-if="isReceptionRoute" class="snippet-panel">
            <span>{{ t('aicc.manager.snippet.placeholder') }}</span>
            <code>{{ widgetSnippet }}</code>
            <div class="snippet-actions">
              <n-button size="small" :disabled="!selectedAgent?.widget_token" @click="copyText(widgetSnippet)">
                <template #icon><Copy :size="14" /></template>
              </n-button>
              <n-button size="small" :disabled="!selectedAgent?.widget_token" @click="openWidgetPreview">
                <template #icon><ExternalLink :size="14" /></template>
                {{ t('aicc.manager.snippet.previewWidget') }}
              </n-button>
            </div>
          </div>
        </div>
        <AICCSessionsPage v-else-if="activeSection === 'sessions'" :agent-id="selectedAgentId" />
        <AICCLeadsPage v-else-if="activeSection === 'leads'" />
        <AICCAnalyticsPage
          v-else-if="activeSection === 'analytics'"
          :agent-id="selectedAgentId"
          :agent-count="agents.length"
          :active-agent-count="activeAgentCount"
        />
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
import { computed, defineComponent, h, nextTick, reactive, ref, watch } from 'vue'
import QRCode from 'qrcode'
import {
  NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NTag,
  NSwitch, NTooltip, type SelectOption,
} from 'naive-ui'
import { useI18n } from 'vue-i18n'
import {
  Copy, Download, ExternalLink, PauseCircle, PlayCircle, Plus, QrCode, Save, Trash2,
} from 'lucide-vue-next'

import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import AICCAnalyticsPage from '@/pages/aicc/AICCAnalyticsPage.vue'
import { useRequiredAICCConsoleContext } from '@/pages/aicc/aiccConsoleContext'
import AICCLeadsPage from '@/pages/aicc/AICCLeadsPage.vue'
import AICCSessionsPage from '@/pages/aicc/AICCSessionsPage.vue'
import {
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

// FieldLabel 统一渲染设置页字段说明入口：label 后的问号图标只承载解释，不改变表单提交语义。
const FieldLabel = defineComponent({
  name: 'FieldLabel',
  props: {
    label: { type: String, required: true },
    help: { type: String, required: true },
  },
  setup(labelProps) {
    return () => h('span', { class: 'field-label-with-help' }, [
      h('span', labelProps.label),
      h(NTooltip, { trigger: 'hover', placement: 'top', width: 260 }, {
        trigger: () => h('button', {
          type: 'button',
          class: 'field-help-trigger',
          'aria-label': labelProps.help,
          'data-test': 'field-help',
        }, '?'),
        default: () => labelProps.help,
      }),
    ])
  },
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

const consoleContext = useRequiredAICCConsoleContext()
const selectedAgentId = consoleContext.selectedAgentId
const agents = consoleContext.agents
const selectedAgent = consoleContext.selectedAgent
const deleteModalOpen = ref(false)
const feedback = ref('')
const feedbackDanger = ref(false)
const activeSection = ref<ManagerTab>(sectionToTab(props.initialSection))
const currentRouteSection = ref<InitialSection>(props.initialSection)
const knowledgePanelEl = ref<HTMLElement>()
const { t } = useI18n()

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

const selectedKnowledgeAppId = computed(() => knowledgeQuery.data.value?.app_id || selectedAgent.value?.app_id)
const isSelectedRunning = computed(() => selectedAgent.value ? isAICCAgentRunning(selectedAgent.value) : false)
const activeAgentCount = computed(() => agents.value.filter(agent => isAICCAgentRunning(agent)).length)
const submitBusy = computed(() => createMutation.isPending.value || updateMutation.isPending.value)
const statusBusy = computed(() => statusMutation.isPending.value)
const deleteBusy = computed(() => deleteMutation.isPending.value)
const settingsBusy = computed(() => settingsMutation.isPending.value || settingsQuery.isFetching.value)
const leadFieldBusy = computed(() => leadFieldMutation.isPending.value || leadFieldsQuery.isFetching.value)
const knowledgeBusy = computed(() => knowledgeMutation.isPending.value || knowledgeQuery.isFetching.value)
const isReceptionRoute = computed(() => currentRouteSection.value === 'reception')
const isSettingsRoute = computed(() => currentRouteSection.value === 'settings')
// 标题文案跟随顶部路由语义；接待台和设置页在同一数据容器内展示不同职责的内容。
const currentSectionKey = computed(() => currentRouteSection.value === 'reception' ? 'config' : currentRouteSection.value)
const currentSectionEyebrow = computed(() => t(`aicc.manager.sections.${currentSectionKey.value}.eyebrow`))
const currentSectionTitle = computed(() => t(`aicc.manager.sections.${currentSectionKey.value}.title`))
const currentSectionDescription = computed(() => t(`aicc.manager.sections.${currentSectionKey.value}.description`))

const form = reactive<AgentForm>(emptyForm())
const settingsForm = reactive<SettingsForm>(emptySettingsForm())
const leadFieldRows = ref<LeadFieldRow[]>([])
const knowledgeForm = reactive<KnowledgeForm>(emptyKnowledgeForm())
const qrDataUrl = ref('')

const industryKnowledgeOptions = computed<SelectOption[]>(() =>
  (knowledgeOptionsQuery.data.value?.industry_knowledge_bases ?? []).map(item => ({
    label: item.document_count > 0 ? `${item.name} (${item.document_count})` : item.name,
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

watch(selectedAgent, (agent) => {
  if (agent) {
    fillForm(agent)
    return
  }
  delete form.id
  Object.assign(form, emptyForm())
  feedback.value = ''
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
        }
      : emptyKnowledgeForm())
  },
  { immediate: true },
)

watch(
  () => props.initialSection,
  (section) => {
    currentRouteSection.value = section
    activeSection.value = sectionToTab(section)
    if (section === 'knowledge') {
      void scrollKnowledgePanelIntoView()
    }
  },
  { immediate: true },
)

// 路由级入口把工作台拆成概览、运营数据和设置几类内容；知识库页面已独立复用实例知识库。
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

function startCreate() {
  consoleContext.startCreateAgent()
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
    consoleContext.selectAgent(agent.id)
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

function openWidgetPreview() {
  const token = selectedAgent.value?.widget_token
  if (!token || typeof window === 'undefined') return
  window.open(`/aicc-widget-preview/${encodeURIComponent(token)}`, '_blank', 'noopener,noreferrer')
}

function downloadQRCode() {
  if (!qrDataUrl.value || !selectedAgent.value) return
  const anchor = document.createElement('a')
  anchor.href = qrDataUrl.value
  anchor.download = `${selectedAgent.value.name || 'aicc'}-qrcode.png`
  anchor.click()
}

function openCurrentAgentKnowledge() {
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

.aicc-page-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 16px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.aicc-page-heading h2,
.aicc-page-heading p {
  margin: 0;
}

.aicc-page-heading h2 {
  font-size: 20px;
}

.aicc-page-heading p:last-child {
  margin-top: 6px;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.aicc-shell {
  display: block;
  min-height: 0;
}

.editor-panel {
  min-width: 0;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.editor-toolbar,
.section-heading,
.snippet-panel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.status-tile span {
  color: var(--color-text-secondary);
}

:deep(.field-label-with-help) {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  vertical-align: middle;
}

:deep(.field-help-trigger) {
  display: inline-grid;
  width: 15px;
  height: 15px;
  place-items: center;
  padding: 0;
  border: 1px solid var(--color-divider);
  border-radius: 50%;
  background: var(--color-surface);
  color: var(--color-text-secondary);
  cursor: help;
  font-size: 10px;
  font-weight: 600;
  line-height: 1;
  opacity: 0.72;
}

:deep(.field-help-trigger:hover),
:deep(.field-help-trigger:focus-visible) {
  border-color: var(--color-primary);
  background: var(--color-surface-muted);
  color: var(--color-primary);
  opacity: 1;
}

:deep(.field-help-trigger:focus-visible) {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
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

.aicc-section-content {
  display: grid;
  gap: 16px;
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

.current-knowledge-row {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  justify-content: space-between;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.current-knowledge-row p {
  margin: 6px 0 0;
  color: var(--color-text-secondary);
  font-size: 13px;
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

.snippet-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: flex-end;
}

@media (max-width: 900px) {
  .aicc-page-heading,
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
