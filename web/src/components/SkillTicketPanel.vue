<template>
  <div class="skill-ticket-panel">
    <div class="ticket-toolbar">
      <n-button type="primary" size="small" @click="showSubmit = true">提交需求</n-button>
    </div>

    <div v-if="ticketsQuery.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="ticketsQuery.error.value" class="state-text danger">工单查询失败：{{ ticketsQuery.error.value?.message }}</p>
    <n-data-table
      v-else
      :columns="columns"
      :data="tickets"
      size="small"
      :bordered="false"
      :row-key="(row: SkillTicket) => row.id"
    />

    <n-modal v-model:show="showSubmit" preset="card" title="提交定制技能需求" :style="{ width: '520px' }">
      <n-form>
        <n-form-item label="标题">
          <n-input v-model:value="submitTitle" placeholder="一句话说明需要什么技能" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="submitDescription" type="textarea" :autosize="{ minRows: 3, maxRows: 8 }" placeholder="描述场景、输入输出、期望行为" />
        </n-form-item>
        <n-form-item label="附件">
          <input ref="submitFileInput" type="file" multiple @change="onPickSubmitFiles" />
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="modal-footer">
          <n-button @click="showSubmit = false">取消</n-button>
          <n-button type="primary" :loading="submitMut.isPending.value || uploadMut.isPending.value" :disabled="!submitTitle.trim()" @click="onSubmit">
            提交
          </n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NDataTable, NForm, NFormItem, NInput, NModal, NTag, useMessage, type DataTableColumns } from 'naive-ui'

import type { SkillTicket } from '@/api'
import { useMySkillTicketsQuery, useSubmitSkillTicket, useUploadTicketMessage } from '@/api/hooks/useSkillTickets'

const emit = defineEmits<{ goInstall: [name: string | undefined] }>()

const router = useRouter()
const message = useMessage()
const ticketsQuery = useMySkillTicketsQuery()
const submitMut = useSubmitSkillTicket()
const uploadTargetID = ref<string | undefined>()
const uploadMut = useUploadTicketMessage(uploadTargetID)

const showSubmit = ref(false)
const submitTitle = ref('')
const submitDescription = ref('')
const submitFiles = ref<File[]>([])

const tickets = computed<SkillTicket[]>(() => ticketsQuery.data.value ?? [])

const columns: DataTableColumns<SkillTicket> = [
  { title: '标题', key: 'title' },
  { title: '状态', key: 'status', render: (row) => h(NTag, { type: statusTag(row.status).type, bordered: false, size: 'small' }, () => statusTag(row.status).label) },
  { title: '报价', key: 'quote', render: (row) => yuan(row.quote_amount_cents) },
  {
    title: '操作',
    key: 'actions',
    render: (row) =>
      h('div', { class: 'row-actions' }, [
        h(NButton, { size: 'small', onClick: () => router.push(`/skill-tickets/${row.id}`) }, () => '查看'),
        row.status === 'delivered'
          ? h(NButton, { size: 'small', type: 'primary', onClick: () => emit('goInstall', row.custom_skill_name) }, () => '去安装')
          : null,
      ]),
  },
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

function yuan(cents: number | null | undefined) {
  return typeof cents === 'number' ? `¥${(cents / 100).toFixed(2)}` : '—'
}

function onPickSubmitFiles(event: Event) {
  const input = event.target as HTMLInputElement
  submitFiles.value = Array.from(input.files ?? [])
}

async function onSubmit() {
  try {
    const ticket = await submitMut.mutateAsync({
      title: submitTitle.value.trim(),
      description: submitDescription.value.trim(),
    })
    uploadTargetID.value = ticket.id
    for (const file of submitFiles.value) {
      await uploadMut.mutateAsync(file)
    }
    showSubmit.value = false
    submitTitle.value = ''
    submitDescription.value = ''
    submitFiles.value = []
    await router.push(`/skill-tickets/${ticket.id}`)
  } catch {
    message.error('提交失败')
  }
}
</script>

<style scoped>
.skill-ticket-panel {
  display: grid;
  gap: 12px;
}

.ticket-toolbar {
  display: flex;
  justify-content: flex-end;
}

.row-actions {
  display: flex;
  gap: 8px;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
