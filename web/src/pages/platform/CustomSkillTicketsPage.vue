<template>
  <div class="custom-skill-tickets-page">
    <div class="page-head">
      <div>
        <p class="eyebrow">Platform</p>
        <h2>{{ t('platform.tickets.heading') }}</h2>
      </div>
      <div class="ticket-filters">
        <n-select v-model:value="filterOrgID" :options="orgFilterOptions" size="small" class="org-filter" />
        <n-select v-model:value="filterStatus" :options="statusFilterOptions" size="small" class="status-filter" />
        <n-input v-model:value="filterKeyword" size="small" clearable :placeholder="t('platform.tickets.filterPlaceholder')" class="keyword-filter" />
      </div>
    </div>

    <div v-if="ticketsQuery.isLoading.value" class="state-text">{{ t('platform.tickets.loading') }}</div>
    <p v-else-if="ticketsQuery.error.value" class="state-text danger">{{ t('platform.tickets.loadError', { msg: ticketsQuery.error.value?.message }) }}</p>
    <n-data-table
      v-else
      :columns="columns"
      :data="filteredTickets"
      size="small"
      :bordered="false"
      :row-key="(row: SkillTicket) => row.id"
      :row-props="ticketRowProps"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NDataTable, NInput, NSelect, NTag, type DataTableColumns } from 'naive-ui'

import type { Organization, SkillTicket } from '@/api'
import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import { useAdminSkillTicketsQuery } from '@/api/hooks/useSkillTickets'

defineOptions({ name: 'CustomSkillTicketsPage' })

const { t } = useI18n()
const router = useRouter()
const ticketsQuery = useAdminSkillTicketsQuery()
const organizationsQuery = useOrganizationsQuery()
const filterOrgID = ref<string>('all')
const filterStatus = ref<string>('all')
const filterKeyword = ref('')

// statusFilterOptions 随语言响应式切换，转为 computed。
const statusFilterOptions = computed(() => [
  { label: t('platform.tickets.filterAll'), value: 'all' },
  { label: t('platform.tickets.statusPending'), value: 'pending' },
  { label: t('platform.tickets.statusProcessing'), value: 'processing' },
  { label: t('platform.tickets.statusDelivered'), value: 'delivered' },
  { label: t('platform.tickets.statusRejected'), value: 'rejected' },
])

const organizations = computed<Organization[]>(() => organizationsQuery.data.value ?? [])
const orgFilterOptions = computed(() => [
  { label: t('platform.tickets.filterOrgAll'), value: 'all' },
  ...organizations.value.map((org) => ({
    label: org.code ? `${org.name}（${org.code}）` : org.name,
    value: org.id,
  })),
])

const tickets = computed<SkillTicket[]>(() => ticketsQuery.data.value ?? [])
const filteredTickets = computed(() => {
  const kw = filterKeyword.value.trim().toLowerCase()
  return tickets.value.filter((ticket) => {
    const statusOK = filterStatus.value === 'all' || ticket.status === filterStatus.value
    const orgOK = filterOrgID.value === 'all' || ticket.org_id === filterOrgID.value
    const keywordOK = !kw || `${ticket.title ?? ''}`.toLowerCase().includes(kw)
    return statusOK && orgOK && keywordOK
  })
})

// columns 随语言响应式切换，转为 computed。
const columns = computed<DataTableColumns<SkillTicket>>(() => [
  { title: t('platform.tickets.columns.title'), key: 'title' },
  { title: t('platform.tickets.columns.requester'), key: 'requester', render: (row) => roleLabel(row.requester_role) },
  { title: t('platform.tickets.columns.status'), key: 'status', render: (row) => h(NTag, { type: statusTag(row.status).type, bordered: false, size: 'small' }, () => statusTag(row.status).label) },
  { title: t('platform.tickets.columns.quote'), key: 'quote', render: (row) => yuan(row.quote_amount_cents) },
])

interface StatusTag {
  type: 'default' | 'warning' | 'success' | 'error'
  label: string
}

// statusTagsMap 随语言响应式切换，转为 computed。
const statusTagsMap = computed<Record<string, StatusTag>>(() => ({
  pending: { type: 'default', label: t('platform.tickets.statusPending') },
  processing: { type: 'warning', label: t('platform.tickets.statusProcessing') },
  delivered: { type: 'success', label: t('platform.tickets.statusDelivered') },
  rejected: { type: 'error', label: t('platform.tickets.statusRejected') },
}))

function statusTag(status: string | undefined): StatusTag {
  return statusTagsMap.value[status ?? ''] ?? { type: 'default', label: status || t('common.status.unknown') }
}

function roleLabel(role: string | undefined) {
  return role === 'org_admin' ? t('platform.tickets.roleAdmin') : role === 'org_member' ? t('platform.tickets.roleMember') : role || '—'
}

function yuan(cents: number | null | undefined) {
  return typeof cents === 'number' ? `¥${(cents / 100).toFixed(2)}` : '—'
}

// openTicket 统一处理工单详情跳转，供鼠标点击和键盘回车/空格复用。
function openTicket(row: SkillTicket) {
  router.push(`/skill-tickets/${row.id}`)
}

// ticketRowProps 将整行变为详情入口；保留键盘触发，避免移除按钮后只能用鼠标访问。
function ticketRowProps(row: SkillTicket) {
  return {
    class: 'ticket-row',
    tabindex: 0,
    role: 'link',
    'data-test': `skill-ticket-row-${row.id}`,
    onClick: () => openTicket(row),
    onKeydown: (event: KeyboardEvent) => {
      if (event.key !== 'Enter' && event.key !== ' ') return
      event.preventDefault()
      openTicket(row)
    },
  }
}
</script>

<style scoped>
.custom-skill-tickets-page {
  display: grid;
  /* 工单页由外层布局撑满高度，内容应贴齐顶部，避免 grid 默认拉伸 auto 行造成页头留白。 */
  align-content: start;
  gap: 16px;
}

.page-head {
  display: flex;
  justify-content: space-between;
  align-items: end;
  gap: 16px;
}

.eyebrow {
  margin: 0 0 4px;
  color: #64748b;
  font-size: 12px;
  text-transform: uppercase;
}

h2 {
  margin: 0;
}

.ticket-filters {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.status-filter {
  width: 150px;
}

.org-filter {
  width: 220px;
}

.keyword-filter {
  width: 240px;
}

.custom-skill-tickets-page :deep(.ticket-row) {
  cursor: pointer;
}

.custom-skill-tickets-page :deep(.ticket-row:focus-visible) {
  outline: 2px solid var(--color-brand);
  outline-offset: -2px;
}
</style>
