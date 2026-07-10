<template>
  <section class="leads-view">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">Leads</p>
        <h4>访客线索</h4>
      </div>
      <n-space>
        <n-button :loading="isExporting" @click="exportLeads">
          <template #icon><Download :size="16" /></template>
          导出 CSV
        </n-button>
      </n-space>
    </div>

    <n-spin :show="leadsQuery.isLoading.value">
      <n-alert v-if="leadsQuery.error.value" type="error" :bordered="false">
        {{ leadsQuery.error.value.message }}
      </n-alert>
      <div v-else-if="leads.length === 0" class="empty-state">
        <Inbox :size="30" />
        <strong>暂无访客线索</strong>
        <span>后续从会话中识别到的访客信息会进入这里。</span>
      </div>
      <div v-else class="lead-list">
        <article v-for="lead in leads" :key="lead.id" class="lead-row">
          <div class="lead-main">
            <strong>{{ lead.display_name || '未命名访客' }}</strong>
            <small>会话 {{ formatShortId(lead.latest_session_id) }} · {{ formatDate(lead.updated_at || lead.created_at) }}</small>
            <div v-if="lead.values?.length" class="lead-values">
              <span v-for="value in lead.values" :key="`${lead.id}-${value.field_key}`">
                {{ value.label }}：{{ value.value }}
              </span>
            </div>
          </div>
          <n-tag size="small" :type="lead.unread ? 'warning' : 'default'" :bordered="false">
            {{ lead.unread ? '未读' : '已读' }}
          </n-tag>
          <n-button size="small" :disabled="!lead.unread" :loading="markReadMutation.isPending.value && activeLeadId === lead.id" @click="markRead(lead.id)">
            <template #icon><Check :size="14" /></template>
            标记已读
          </n-button>
        </article>
      </div>
    </n-spin>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NAlert, NButton, NSpace, NSpin, NTag, useMessage } from 'naive-ui'
import { Check, Download, Inbox } from 'lucide-vue-next'

import { downloadAICCLeadsCSV, useAICCLeadsQuery, useMarkAICCLeadRead } from '@/api/hooks/useAICC'

const message = useMessage()
const leadsQuery = useAICCLeadsQuery()
const markReadMutation = useMarkAICCLeadRead()

const isExporting = ref(false)
const activeLeadId = ref<string | undefined>()
const leads = computed(() => leadsQuery.data.value ?? [])

async function markRead(leadId: string) {
  activeLeadId.value = leadId
  try {
    await markReadMutation.mutateAsync(leadId)
    message.success('已标记为已读')
  } catch (err) {
    message.error(err instanceof Error ? err.message : '标记失败')
  } finally {
    activeLeadId.value = undefined
  }
}

async function exportLeads() {
  isExporting.value = true
  try {
    const { blob, filename } = await downloadAICCLeadsCSV()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename ?? 'aicc-leads.csv'
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '导出失败')
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
  grid-template-columns: minmax(0, 1fr) auto auto;
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

.empty-state strong {
  color: var(--color-text-primary);
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
