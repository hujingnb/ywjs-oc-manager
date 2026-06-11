<template>
  <div class="custom-skill-tickets-page">
    <div class="page-head">
      <div>
        <p class="eyebrow">Platform</p>
        <h2>定制技能工单</h2>
      </div>
      <div class="ticket-filters">
        <n-select v-model:value="filterOrgID" :options="orgFilterOptions" size="small" class="org-filter" />
        <n-select v-model:value="filterStatus" :options="statusFilterOptions" size="small" class="status-filter" />
        <n-input v-model:value="filterKeyword" size="small" clearable placeholder="按标题过滤" class="keyword-filter" />
      </div>
    </div>

    <div v-if="ticketsQuery.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="ticketsQuery.error.value" class="state-text danger">工单查询失败：{{ ticketsQuery.error.value?.message }}</p>
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
import { useRouter } from 'vue-router'
import { NDataTable, NInput, NSelect, NTag, type DataTableColumns } from 'naive-ui'

import type { Organization, SkillTicket } from '@/api'
import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import { useAdminSkillTicketsQuery } from '@/api/hooks/useSkillTickets'

defineOptions({ name: 'CustomSkillTicketsPage' })

const router = useRouter()
const ticketsQuery = useAdminSkillTicketsQuery()
const organizationsQuery = useOrganizationsQuery()
const filterOrgID = ref<string>('all')
const filterStatus = ref<string>('all')
const filterKeyword = ref('')

const statusFilterOptions = [
  { label: '全部状态', value: 'all' },
  { label: '待处理', value: 'pending' },
  { label: '制作中', value: 'processing' },
  { label: '已交付', value: 'delivered' },
  { label: '已拒绝', value: 'rejected' },
]

const organizations = computed<Organization[]>(() => organizationsQuery.data.value ?? [])
const orgFilterOptions = computed(() => [
  { label: '全部组织', value: 'all' },
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

const columns: DataTableColumns<SkillTicket> = [
  { title: '标题', key: 'title' },
  { title: '提交者', key: 'requester', render: (row) => roleLabel(row.requester_role) },
  { title: '状态', key: 'status', render: (row) => h(NTag, { type: statusTag(row.status).type, bordered: false, size: 'small' }, () => statusTag(row.status).label) },
  { title: '报价', key: 'quote', render: (row) => yuan(row.quote_amount_cents) },
]

interface StatusTag {
  type: 'default' | 'warning' | 'success' | 'error'
  label: string
}

const statusTags: Record<string, StatusTag> = {
  pending: { type: 'default', label: '待处理' },
  processing: { type: 'warning', label: '制作中' },
  delivered: { type: 'success', label: '已交付' },
  rejected: { type: 'error', label: '已拒绝' },
}

function statusTag(status: string | undefined): StatusTag {
  return statusTags[status ?? ''] ?? { type: 'default', label: status || '未知' }
}

function roleLabel(role: string | undefined) {
  return role === 'org_admin' ? '管理员' : role === 'org_member' ? '成员' : role || '—'
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
