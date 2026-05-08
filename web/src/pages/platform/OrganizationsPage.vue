<template>
  <div style="display: grid; gap: 18px">
    <!-- 组织列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform</p>
          <h2 style="margin: 0">组织列表</h2>
        </div>
      </template>
      <template #header-extra>
        <n-button type="primary" @click="openForm">
          <template #icon><Plus :size="16" /></template>
          新增组织
        </n-button>
      </template>

      <div v-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <n-data-table
        v-else
        :columns="columns"
        :data="organizations ?? []"
        size="small"
        :bordered="false"
        :row-key="(row) => row.id"
      />
    </n-card>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">New</p>
            <h2 style="margin: 0">创建组织</h2>
          </div>
          <n-button quaternary circle @click="formVisible = false">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="onSubmit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="组织名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="联系人">
              <n-input v-model:value="form.contact_name" placeholder="联系人姓名" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="联系电话">
              <n-input v-model:value="form.contact_phone" placeholder="手机号" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="余额预警阈值 (%)">
              <n-input-number v-model:value="form.credit_warning_threshold" :min="0" :max="100" style="width: 100%" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="备注">
              <n-input v-model:value="form.remark" type="textarea" :rows="2" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="formVisible = false">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { h, reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NSpace, NTag, type DataTableColumns,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useOrganizationsQuery, useUpdateOrganizationStatus,
  type OrganizationFormPayload,
} from '@/api/hooks/useOrganizations'
import type { Organization } from '@/api/types'

const { data: organizations, isLoading, error } = useOrganizationsQuery()
const createMutation = useCreateOrganization()
const statusMutation = useUpdateOrganizationStatus()

const formVisible = ref(false)
const submitError = ref<string | null>(null)
const creating = ref(false)
const form = reactive<OrganizationFormPayload>({
  name: '', contact_name: '', contact_phone: '', remark: '',
  credit_warning_threshold: undefined,
})

function toneToTagType(tone: string): 'success' | 'warning' | 'error' | 'default' {
  const m: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return m[tone] ?? 'default'
}

const columns: DataTableColumns<Organization> = [
  {
    title: '名称', key: 'name',
    render: (row) => [
      h('strong', row.name),
      row.remark ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.remark) : null,
    ],
  },
  {
    title: '状态', key: 'status',
    render: (row) => {
      const v = formatOrgStatus(row.status)
      return h(NTag, { type: toneToTagType(v.tone), size: 'small', bordered: false }, { default: () => v.label })
    },
  },
  { title: '联系人', key: 'contact_name', render: (row) => row.contact_name || '—' },
  { title: '电话', key: 'contact_phone', render: (row) => row.contact_phone || '—' },
  {
    title: '预警阈值', key: 'credit_warning_threshold',
    render: (row) => typeof row.credit_warning_threshold === 'number' ? `${row.credit_warning_threshold}%` : '—',
  },
  {
    title: '操作', key: 'actions',
    render: (row) => row.status === 'active'
      ? h(NButton, { size: 'small', onClick: () => onToggle(row, 'disable') }, { default: () => '禁用' })
      : h(NButton, { size: 'small', type: 'primary', onClick: () => onToggle(row, 'enable') }, { default: () => '启用' }),
  },
]

function openForm() {
  formVisible.value = true; submitError.value = null
  form.name = ''; form.contact_name = ''; form.contact_phone = ''; form.remark = ''
  form.credit_warning_threshold = undefined
}

async function onSubmit() {
  submitError.value = null; creating.value = true
  try {
    await createMutation.mutateAsync({
      name: form.name,
      contact_name: form.contact_name || undefined,
      contact_phone: form.contact_phone || undefined,
      remark: form.remark || undefined,
      credit_warning_threshold: typeof form.credit_warning_threshold === 'number'
        ? form.credit_warning_threshold : undefined,
    })
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '创建组织失败'
  } finally { creating.value = false }
}

function onToggle(org: Organization, action: 'enable' | 'disable') {
  statusMutation.mutate({ orgId: org.id, action })
}
</script>
