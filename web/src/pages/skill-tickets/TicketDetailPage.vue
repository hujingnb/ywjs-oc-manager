<template>
  <div class="ticket-detail-page">
    <div v-if="detailQuery.isLoading.value" class="state-text">{{ t('common.status.loading') }}</div>
    <p v-else-if="detailQuery.error.value" class="state-text danger">{{ t('tickets.state.loadFailed') }}</p>
    <template v-else-if="ticket">
      <header class="ticket-header">
        <div class="title-block">
          <n-button quaternary size="small" @click="router.back()">{{ t('tickets.actions.back') }}</n-button>
          <h1>{{ ticket.title }}</h1>
          <ticket-status-stepper :status="ticket.status" />
        </div>
        <div class="action-bar">
          <template v-if="isAdmin">
            <n-button v-if="ticket.status === 'pending'" type="primary" @click="onStart">{{ t('tickets.actions.start') }}</n-button>
            <n-button v-if="ticket.status === 'processing'" type="primary" @click="deliverOpen = true">{{ t('tickets.actions.deliver') }}</n-button>
            <n-button v-if="ticket.status === 'delivered'" type="primary" @click="deliverOpen = true">{{ t('tickets.actions.redeliver') }}</n-button>
            <n-button v-if="ticket.status === 'delivered'" @click="openTargets">{{ t('tickets.actions.editTargets') }}</n-button>
            <n-button v-if="ticket.status === 'rejected'" type="primary" @click="onReopen">{{ t('tickets.actions.reopen') }}</n-button>
            <n-button v-if="ticket.status === 'pending' || ticket.status === 'processing'" type="error" @click="rejectOpen = true">{{ t('tickets.actions.reject') }}</n-button>
          </template>
          <n-button v-else-if="ticket.status === 'delivered'" type="primary" @click="router.push('/skills')">{{ t('tickets.actions.install') }}</n-button>
        </div>
      </header>

      <section v-if="isAdmin" class="detail-section submitter-section">
        <h2>{{ t('tickets.section.submitter') }}</h2>
        <dl class="submitter-grid">
          <div>
            <dt>{{ t('tickets.fields.submitter') }}</dt>
            <dd>
              {{ requesterDisplay }}
              <span v-if="ticket.requester_role" class="role-chip">{{ requesterRoleLabel(ticket.requester_role) }}</span>
            </dd>
          </div>
          <div>
            <dt>{{ t('tickets.fields.organization') }}</dt>
            <dd>{{ organizationDisplay }}</dd>
          </div>
        </dl>
      </section>

      <section v-if="ticket.reject_reason" class="detail-section">
        <h2>{{ t('tickets.section.rejectReason') }}</h2>
        <p class="reject-reason">{{ ticket.reject_reason }}</p>
      </section>

      <section class="detail-section split-section">
        <div>
          <h2>{{ t('tickets.section.quote') }}</h2>
          <div v-if="isAdmin && canEditQuote" class="quote-editor">
            <n-input-number v-model:value="quoteYuan" :min="0" :precision="2" :placeholder="t('tickets.fields.quotePlaceholder')" />
            <n-button size="small" :loading="quoteMut.isPending.value" @click="onSaveQuote">{{ t('tickets.actions.saveQuote') }}</n-button>
          </div>
          <p v-else class="readonly-value">{{ yuan(ticket.quote_amount_cents) }}</p>
        </div>
        <div>
          <h2>{{ t('tickets.section.targets') }}</h2>
          <div v-if="targets.length" class="target-list">
            <span v-for="target in targets" :key="`${target.org_id}-${target.audience}`">
              {{ orgLabel(target.org_id) }} · {{ audienceLabel(target.audience) }}
            </span>
          </div>
          <p v-else class="state-text">{{ t('tickets.state.noDelivery') }}</p>
        </div>
      </section>

      <section class="detail-section">
        <h2>{{ t('tickets.section.conversation') }}</h2>
        <ticket-conversation
          :ticket-id="ticket.id"
          :messages="messages"
          :current-user-id="auth.user?.id"
        />
      </section>

      <deliver-custom-skill-modal
        v-model:show="deliverOpen"
        :ticket="ticket"
        :orgs="orgs"
      />

      <n-modal v-model:show="rejectOpen" preset="card" :title="t('tickets.modal.rejectTitle')" :style="{ width: '460px' }">
        <n-input
          v-model:value="rejectReason"
          type="textarea"
          :autosize="{ minRows: 3, maxRows: 6 }"
          :placeholder="t('tickets.modal.rejectPlaceholder')"
        />
        <template #footer>
          <div class="modal-footer">
            <n-button @click="rejectOpen = false">{{ t('tickets.actions.cancel') }}</n-button>
            <n-button type="error" :loading="rejectMut.isPending.value" @click="onReject">{{ t('tickets.actions.confirmReject') }}</n-button>
          </div>
        </template>
      </n-modal>

      <n-modal v-model:show="targetsOpen" preset="card" :title="t('tickets.modal.targetsTitle')" :style="{ width: '560px' }">
        <ticket-targets-editor v-model="editableTargets" :orgs="orgs" />
        <template #footer>
          <div class="modal-footer">
            <n-button @click="targetsOpen = false">{{ t('tickets.actions.cancel') }}</n-button>
            <n-button type="primary" :loading="targetsMut.isPending.value" @click="onSaveTargets">{{ t('tickets.actions.save') }}</n-button>
          </div>
        </template>
      </n-modal>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NInput, NInputNumber, NModal, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import type { SkillTicketDetail, SkillTicketMessage } from '@/api'
import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import {
  useReopenTicket,
  useRejectSkillTicket,
  useSetSkillTicketQuote,
  useSkillTicketDetailQuery,
  useStartTicket,
  useUpdateTicketTargets,
  type DeliverTarget,
} from '@/api/hooks/useSkillTickets'
import { useAuthStore } from '@/stores/auth'
import DeliverCustomSkillModal from '@/components/ticket/DeliverCustomSkillModal.vue'
import TicketConversation from '@/components/ticket/TicketConversation.vue'
import TicketStatusStepper from '@/components/ticket/TicketStatusStepper.vue'
import TicketTargetsEditor from '@/components/ticket/TicketTargetsEditor.vue'

const REALTIME_SIMULATION_INTERVAL_MS = 5_000

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const message = useMessage()
const { t } = useI18n()

const id = computed(() => String(route.params.id || ''))
const detailQuery = useSkillTicketDetailQuery(id)
const isAdmin = computed(() => auth.user?.role === 'platform_admin')
const orgsQuery = useOrganizationsQuery(() => isAdmin.value)
const startMut = useStartTicket()
const reopenMut = useReopenTicket()
const quoteMut = useSetSkillTicketQuote()
const rejectMut = useRejectSkillTicket()
const targetsMut = useUpdateTicketTargets()

const deliverOpen = ref(false)
const rejectOpen = ref(false)
const targetsOpen = ref(false)
const rejectReason = ref('')
const quoteYuan = ref<number | null>(null)
const editableTargets = ref<DeliverTarget[]>([])

const ticket = computed<SkillTicketDetail | null>(() => detailQuery.data.value ?? null)
const orgs = computed(() => orgsQuery.data.value ?? [])
const targets = computed<DeliverTarget[]>(() => normalizeTargets(ticket.value?.targets))
const messages = computed<SkillTicketMessage[]>(() => normalizeMessages(ticket.value?.messages))
const canEditQuote = computed(() => ticket.value?.status === 'pending' || ticket.value?.status === 'processing')
const requesterDisplay = computed(() => ticket.value?.requester_name || ticket.value?.requester_user_id || '—')
const organizationDisplay = computed(() => {
  if (!ticket.value) return '—'
  return ticket.value.org_name || orgLabel(ticket.value.org_id || '') || ticket.value.org_id || '—'
})
let realtimeSimulationTimer: number | undefined

onMounted(() => {
  // 本地没有真实消息推送通道时,定时刷新详情 query 来模拟对话实时消息到达。
  realtimeSimulationTimer = window.setInterval(() => {
    if (!id.value) return
    void detailQuery.refetch()
  }, REALTIME_SIMULATION_INTERVAL_MS)
})

onBeforeUnmount(() => {
  if (realtimeSimulationTimer !== undefined) {
    window.clearInterval(realtimeSimulationTimer)
    realtimeSimulationTimer = undefined
  }
})

watch(
  () => ticket.value?.quote_amount_cents,
  (cents) => {
    quoteYuan.value = typeof cents === 'number' ? cents / 100 : null
  },
  { immediate: true },
)

function orgLabel(orgID: string) {
  const org = orgs.value.find((item) => item.id === orgID)
  if (org?.name || org?.code) return org.name || org.code
  // 非平台管理员拿不到全量组织列表(orgsQuery 仅平台管理员启用),其工单目标企业必然是工单自身企业,
  // 用详情携带的 org_name 兜底,避免在「可见范围」直接把企业 UUID 暴露给用户。
  if (orgID && orgID === ticket.value?.org_id && ticket.value?.org_name) return ticket.value.org_name
  return orgID
}

// audienceLabel 将可见范围枚举值映射为本地化标签，走 i18n 随语言切换；未知值原样展示。
function audienceLabel(audience: string) {
  const map: Record<string, string> = {
    all_org: t('tickets.audience.all_org'),
    org_admins: t('tickets.audience.org_admins'),
    requester_only: t('tickets.audience.requester_only'),
  }
  return map[audience] ?? audience
}

// requesterRoleLabel 将申请人角色枚举映射为本地化标签，未知角色直接展示原值。
function requesterRoleLabel(role: string | undefined) {
  if (!role) return t('common.status.unknown')
  const map: Record<string, string> = {
    org_admin: t('tickets.requesterRole.org_admin'),
    org_member: t('tickets.requesterRole.org_member'),
  }
  return map[role] ?? role
}

function yuan(cents: number | null | undefined) {
  return typeof cents === 'number' ? `¥${(cents / 100).toFixed(2)}` : '—'
}

function openTargets() {
  editableTargets.value = targets.value.length ? [...targets.value] : defaultTargets(ticket.value)
  targetsOpen.value = true
}

function defaultTargets(current: SkillTicketDetail | null): DeliverTarget[] {
  if (!current?.org_id) return []
  const audience = current.requester_role === 'org_admin' ? 'org_admins' : 'all_org'
  return [{ org_id: current.org_id, audience }]
}

// generated.ts 对数组元素字段保守标为可选，页面传给共享组件前收紧为运行时必需字段。
function normalizeTargets(raw: SkillTicketDetail['targets'] | undefined): DeliverTarget[] {
  return (raw ?? []).flatMap((target) =>
    target.org_id && target.audience ? [{ org_id: target.org_id, audience: target.audience }] : [],
  )
}

// 消息渲染和下载依赖 id/kind，缺失数据直接丢弃，避免把不完整契约传入对话组件。
function normalizeMessages(raw: SkillTicketDetail['messages'] | undefined): SkillTicketMessage[] {
  return (raw ?? []).flatMap((item) =>
    item.id && item.kind ? [{ ...item, id: item.id, kind: item.kind }] : [],
  )
}

async function onStart() {
  if (!ticket.value) return
  await startMut.mutateAsync({ id: ticket.value.id })
}

async function onReopen() {
  if (!ticket.value) return
  await reopenMut.mutateAsync({ id: ticket.value.id })
}

async function onSaveQuote() {
  if (!ticket.value || quoteYuan.value == null) return
  await quoteMut.mutateAsync({ id: ticket.value.id, quote_amount_cents: Math.round(quoteYuan.value * 100) })
  message.success(t('tickets.messages.quoteSaved'))
}

async function onReject() {
  if (!ticket.value) return
  await rejectMut.mutateAsync({ id: ticket.value.id, reason: rejectReason.value })
  rejectOpen.value = false
  rejectReason.value = ''
}

async function onSaveTargets() {
  if (!ticket.value) return
  await targetsMut.mutateAsync({ id: ticket.value.id, targets: editableTargets.value })
  targetsOpen.value = false
}
</script>

<style scoped>
.ticket-detail-page {
  display: grid;
  gap: 16px;
}

.ticket-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid #e2e8f0;
}

.title-block {
  display: grid;
  gap: 8px;
}

h1,
h2 {
  margin: 0;
}

h1 {
  font-size: 24px;
}

h2 {
  font-size: 15px;
}

.action-bar {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.detail-section {
  display: grid;
  gap: 10px;
  padding: 16px 0;
  border-bottom: 1px solid #e2e8f0;
}

.submitter-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(180px, 1fr));
  gap: 16px;
  margin: 0;
}

.submitter-grid div {
  display: grid;
  gap: 4px;
}

.submitter-grid dt {
  color: #64748b;
  font-size: 12px;
}

.submitter-grid dd {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  color: #0f172a;
  font-weight: 600;
}

.role-chip {
  padding: 2px 6px;
  border-radius: 4px;
  background: #eef2ff;
  color: #3730a3;
  font-size: 12px;
  font-weight: 500;
}

.split-section {
  grid-template-columns: minmax(220px, 0.7fr) minmax(280px, 1fr);
  gap: 24px;
}

.readonly-value,
.reject-reason {
  margin: 0;
}

.reject-reason {
  color: #b91c1c;
}

.quote-editor {
  display: flex;
  gap: 8px;
  align-items: center;
}

.target-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.target-list span {
  padding: 4px 8px;
  border-radius: 6px;
  background: #f1f5f9;
  color: #334155;
  font-size: 13px;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

@media (max-width: 760px) {
  .ticket-header,
  .split-section {
    grid-template-columns: 1fr;
    display: grid;
  }

  .action-bar {
    justify-content: flex-start;
  }
}
</style>
