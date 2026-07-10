<template>
  <main class="aicc-page">
    <section class="aicc-hero">
      <div>
        <p class="eyebrow">AI Contact Center</p>
        <h2>AICC 接待台</h2>
        <p>统一维护在线客服智能体、公开链接和访客隐私口径。</p>
      </div>
      <n-button type="primary" @click="startCreate">
        <template #icon><Plus :size="16" /></template>
        新建智能体
      </n-button>
    </section>

    <section class="aicc-shell">
      <aside class="agent-rail">
        <div class="rail-heading">
          <strong>智能体</strong>
          <n-tag size="small" :bordered="false">{{ agents.length }}</n-tag>
        </div>
        <div v-if="agentsQuery.isLoading.value" class="state-text">正在加载</div>
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
            <small>{{ agent.scenario || '未填写业务场景' }}</small>
          </button>
        </template>
        <div v-if="!agentsQuery.isLoading.value && agents.length === 0" class="empty-block">
          <MessageSquareText :size="28" />
          <strong>还没有客服智能体</strong>
          <span>创建后会自动绑定隐藏实例，并生成公开访问入口。</span>
        </div>
      </aside>

      <section class="editor-panel">
        <div class="editor-toolbar">
          <div>
            <p class="eyebrow">{{ form.id ? '编辑智能体' : '新建智能体' }}</p>
            <h3>{{ form.name || '未命名接待员' }}</h3>
          </div>
          <n-space>
            <n-button v-if="selectedAgent" :disabled="statusBusy" @click="toggleStatus">
              <template #icon>
                <component :is="isSelectedRunning ? PauseCircle : PlayCircle" :size="16" />
              </template>
              {{ isSelectedRunning ? '停止接待' : '启动接待' }}
            </n-button>
            <n-button v-if="selectedAgent" type="error" ghost :disabled="deleteBusy" @click="deleteModalOpen = true">
              <template #icon><Trash2 :size="16" /></template>
              删除
            </n-button>
          </n-space>
        </div>

        <n-alert v-if="feedback" :type="feedbackDanger ? 'error' : 'success'" :bordered="false" class="feedback">
          {{ feedback }}
        </n-alert>

        <n-tabs type="segment" animated class="aicc-tabs">
          <n-tab-pane name="config" tab="智能体配置">
            <div class="status-grid">
              <div class="status-tile">
                <span>运行状态</span>
                <strong>{{ selectedAgent ? statusMeta(selectedAgent.status).label : '草稿' }}</strong>
              </div>
              <div class="status-tile">
                <span>保留天数</span>
                <strong>{{ form.retention_days || 0 }} 天</strong>
              </div>
              <div class="status-tile">
                <span>公开入口</span>
                <strong>{{ selectedAgent?.public_token ? '已生成' : '保存后生成' }}</strong>
              </div>
            </div>

            <n-form class="agent-form" :model="form" label-placement="top" @submit.prevent="submitForm">
              <div class="agent-fields">
                <div>
                  <n-form-item label="智能体名称" required>
                    <n-input v-model:value="form.name" maxlength="80" placeholder="例如：售前咨询接待员" />
                  </n-form-item>
                </div>
                <div>
                  <n-form-item label="数据保留天数">
                    <n-input-number v-model:value="form.retention_days" :min="1" :max="3650" style="width: 100%" />
                  </n-form-item>
                </div>
                <div>
                  <n-form-item label="隐私模式">
                    <n-select v-model:value="form.privacy_mode" :options="privacyOptions" />
                  </n-form-item>
                </div>
                <div>
                  <n-form-item label="欢迎语">
                    <n-input v-model:value="form.greeting" maxlength="240" placeholder="访客打开聊天时看到的第一句话" />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item label="业务场景">
                    <n-input
                      v-model:value="form.scenario"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 5 }"
                      placeholder="说明这个智能体服务的客群、问题类型和转人工边界"
                    />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item label="回答边界">
                    <n-input
                      v-model:value="form.answer_boundary"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 5 }"
                      placeholder="例如：不承诺价格、不处理退款审批、遇到投诉需建议人工介入"
                    />
                  </n-form-item>
                </div>
                <div class="field-full">
                  <n-form-item label="隐私说明">
                    <n-input
                      v-model:value="form.privacy_text"
                      type="textarea"
                      :autosize="{ minRows: 3, maxRows: 5 }"
                      placeholder="说明会收集哪些信息、保存多久、用于什么目的"
                    />
                  </n-form-item>
                </div>
              </div>
              <n-space justify="end">
                <n-button attr-type="button" @click="resetForm">重置</n-button>
                <n-button type="primary" attr-type="submit" :loading="submitBusy">
                  <template #icon><Save :size="16" /></template>
                  保存配置
                </n-button>
              </n-space>
            </n-form>

            <div class="lead-field-panel">
              <div class="section-heading">
                <div>
                  <p class="eyebrow">访客留资</p>
                  <strong>公开页联系信息</strong>
                </div>
                <n-button size="small" :disabled="!selectedAgent" @click="addLeadField">
                  <template #icon><Plus :size="14" /></template>
                  添加字段
                </n-button>
              </div>
              <div v-if="!selectedAgent" class="state-text">保存智能体后可配置公开页留资字段。</div>
              <div v-else-if="leadFieldRows.length === 0" class="empty-inline">未配置留资字段，访客可直接发起咨询。</div>
              <div v-else class="lead-field-list">
                <div v-for="(field, index) in leadFieldRows" :key="field.local_id" class="lead-field-row">
                  <n-input v-model:value="field.label" placeholder="字段名称" maxlength="128" />
                  <n-input v-model:value="field.field_key" placeholder="字段 key" maxlength="64" />
                  <n-select v-model:value="field.field_type" :options="leadFieldTypeOptions" />
                  <n-checkbox v-model:checked="field.required">必填</n-checkbox>
                  <n-input v-model:value="field.prompt_text" placeholder="输入提示" maxlength="160" />
                  <n-button quaternary circle type="error" @click="removeLeadField(index)">
                    <template #icon><Trash2 :size="15" /></template>
                  </n-button>
                </div>
              </div>
              <n-space justify="end">
                <n-button :disabled="!selectedAgent" :loading="leadFieldBusy" @click="saveLeadFields">
                  <template #icon><Save :size="16" /></template>
                  保存留资字段
                </n-button>
              </n-space>
            </div>

            <div class="publish-panel">
              <div>
                <p class="eyebrow">公开链接</p>
                <strong>{{ publicLink || '保存智能体后生成' }}</strong>
              </div>
              <n-space>
                <n-button :disabled="!publicLink" @click="copyText(publicLink)">
                  <template #icon><Copy :size="16" /></template>
                  复制链接
                </n-button>
                <n-button :disabled="!publicLink" @click="openPublicLink">
                  <template #icon><ExternalLink :size="16" /></template>
                  预览
                </n-button>
              </n-space>
            </div>
            <div class="snippet-panel">
              <span>嵌入占位</span>
              <code>{{ widgetSnippet }}</code>
              <n-button size="small" :disabled="!selectedAgent?.widget_token" @click="copyText(widgetSnippet)">
                <template #icon><Copy :size="14" /></template>
              </n-button>
            </div>
          </n-tab-pane>
          <n-tab-pane name="sessions" tab="会话">
            <AICCSessionsPage :agent-id="selectedAgentId" />
          </n-tab-pane>
          <n-tab-pane name="leads" tab="线索">
            <AICCLeadsPage />
          </n-tab-pane>
          <n-tab-pane name="analytics" tab="统计">
            <AICCAnalyticsPage :agent-count="agents.length" :active-agent-count="activeAgentCount" />
          </n-tab-pane>
        </n-tabs>
      </section>
    </section>

    <ConfirmActionModal
      :visible="deleteModalOpen"
      title="删除 AICC 智能体"
      :message="`删除后公开链接将不可用。请输入 ${selectedAgent?.name ?? ''} 确认。`"
      :verify-value="selectedAgent?.name"
      :busy="deleteBusy"
      confirm-label="删除"
      @cancel="deleteModalOpen = false"
      @confirm="deleteAgent"
    />
  </main>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NTag,
  NTabPane, NTabs, type SelectOption,
} from 'naive-ui'
import {
  Copy, ExternalLink, MessageSquareText, PauseCircle, PlayCircle, Plus, Save, Trash2,
} from 'lucide-vue-next'

import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import AICCAnalyticsPage from '@/pages/aicc/AICCAnalyticsPage.vue'
import AICCLeadsPage from '@/pages/aicc/AICCLeadsPage.vue'
import AICCSessionsPage from '@/pages/aicc/AICCSessionsPage.vue'
import {
  useAICCAgentsQuery,
  useAICCLeadFieldsQuery,
  useCreateAICCAgent,
  useDeleteAICCAgent,
  useReplaceAICCLeadFields,
  useSetAICCAgentStatus,
  useUpdateAICCAgent,
} from '@/api/hooks/useAICC'
import type { AICCAgent, AICCAgentPayload, AICCAgentStatus, AICCLeadField, AICCLeadFieldPayload, AICCPrivacyMode } from '@/domain/aicc'
import { isAICCAgentRunning } from '@/domain/aicc'

// AICCManagerPage 是企业管理员维护 AI Contact Center 在线客服智能体和运营数据的入口。
interface AgentForm extends AICCAgentPayload {
  id?: string
  privacy_mode: AICCPrivacyMode
  retention_days: number
}

interface LeadFieldRow extends AICCLeadFieldPayload {
  local_id: string
}

const selectedAgentId = ref<string | undefined>()
const deleteModalOpen = ref(false)
const feedback = ref('')
const feedbackDanger = ref(false)

const agentsQuery = useAICCAgentsQuery()
const leadFieldsQuery = useAICCLeadFieldsQuery(selectedAgentId)
const createMutation = useCreateAICCAgent()
const updateMutation = useUpdateAICCAgent()
const leadFieldMutation = useReplaceAICCLeadFields()
const statusMutation = useSetAICCAgentStatus()
const deleteMutation = useDeleteAICCAgent()

const agents = computed(() => agentsQuery.data.value ?? [])
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentId.value))
const isSelectedRunning = computed(() => selectedAgent.value ? isAICCAgentRunning(selectedAgent.value) : false)
const activeAgentCount = computed(() => agents.value.filter(agent => isAICCAgentRunning(agent)).length)
const submitBusy = computed(() => createMutation.isPending.value || updateMutation.isPending.value)
const statusBusy = computed(() => statusMutation.isPending.value)
const deleteBusy = computed(() => deleteMutation.isPending.value)
const leadFieldBusy = computed(() => leadFieldMutation.isPending.value || leadFieldsQuery.isFetching.value)

const form = reactive<AgentForm>(emptyForm())
const leadFieldRows = ref<LeadFieldRow[]>([])

const privacyOptions: SelectOption[] = [
  { label: '展示隐私提示', value: 'notice' },
  { label: '必须同意后接待', value: 'consent_required' },
]

const leadFieldTypeOptions: SelectOption[] = [
  { label: '文本', value: 'text' },
  { label: '手机号', value: 'phone' },
  { label: '邮箱', value: 'email' },
  { label: '数字', value: 'number' },
]

const publicLink = computed(() => {
  if (!selectedAgent.value?.public_token || typeof window === 'undefined') return ''
  return `${window.location.origin}/aicc/${selectedAgent.value.public_token}`
})

const widgetSnippet = computed(() => {
  const token = selectedAgent.value?.widget_token ?? '保存后生成'
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
  () => leadFieldsQuery.data.value,
  (fields) => {
    leadFieldRows.value = (fields ?? []).map(toLeadFieldRow)
  },
  { immediate: true },
)

function emptyForm(): AgentForm {
  return {
    id: undefined,
    name: '',
    scenario: '',
    greeting: '您好，我是在线客服，请问有什么可以帮您？',
    answer_boundary: '',
    privacy_mode: 'notice',
    privacy_text: '我们会使用本次对话内容来回答您的问题，并按企业数据保留策略保存。',
    retention_days: 180,
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
  }
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
    label: next === 1 ? '联系电话' : `字段 ${next}`,
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
    setFeedback('请补全留资字段名称和 key', true)
    return
  }
  try {
    await leadFieldMutation.mutateAsync({ agentId: selectedAgent.value.id, fields })
    setFeedback('留资字段已保存')
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : '留资字段保存失败', true)
  }
}

function statusMeta(status?: AICCAgentStatus): { label: string; type: 'success' | 'warning' | 'default' | 'error' } {
  if (status === 'active') return { label: '接待中', type: 'success' }
  if (status === 'paused') return { label: '已暂停', type: 'warning' }
  if (status === 'deleted') return { label: '已删除', type: 'error' }
  return { label: '草稿', type: 'default' }
}

async function submitForm() {
  if (!form.name.trim()) {
    setFeedback('请填写智能体名称', true)
    return
  }
  try {
    const payload = payloadFromForm()
    const agent = form.id
      ? await updateMutation.mutateAsync({ agentId: form.id, payload })
      : await createMutation.mutateAsync(payload)
    selectedAgentId.value = agent.id
    fillForm(agent)
    setFeedback('配置已保存')
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : '保存失败', true)
  }
}

async function toggleStatus() {
  if (!selectedAgent.value) return
  try {
    const action = isSelectedRunning.value ? 'stop' : 'start'
    await statusMutation.mutateAsync({ agentId: selectedAgent.value.id, action })
    setFeedback(action === 'start' ? '已启动接待' : '已停止接待')
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : '状态切换失败', true)
  }
}

async function deleteAgent() {
  if (!selectedAgent.value) return
  try {
    const deletedId = selectedAgent.value.id
    await deleteMutation.mutateAsync(deletedId)
    deleteModalOpen.value = false
    if (selectedAgentId.value === deletedId) startCreate()
    setFeedback('智能体已删除')
  } catch (err) {
    setFeedback(err instanceof Error ? err.message : '删除失败', true)
  }
}

async function copyText(text: string) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    setFeedback('已复制')
  } catch {
    setFeedback('复制失败，请手动选择文本复制', true)
  }
}

function openPublicLink() {
  if (!publicLink.value) return
  window.open(publicLink.value, '_blank', 'noopener,noreferrer')
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
.publish-panel,
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

.lead-field-panel {
  display: grid;
  gap: 12px;
  min-width: 0;
  padding: 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
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

.publish-panel,
.snippet-panel {
  min-width: 0;
  padding: 12px 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.publish-panel strong,
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
  .publish-panel {
    align-items: stretch;
    flex-direction: column;
  }

  .aicc-shell {
    grid-template-columns: 1fr;
  }

  .status-grid,
  .agent-fields,
  .lead-field-row {
    grid-template-columns: 1fr;
  }

  .field-full {
    grid-column: auto;
  }
}
</style>
