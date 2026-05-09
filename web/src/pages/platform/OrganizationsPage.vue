<template>
  <div style="display: grid; gap: 18px">
    <!-- 组织列表 -->
    <DataTableList
      title="组织列表"
      eyebrow="Platform"
      :columns="columns"
      :data="organizations ?? []"
      :loading="isLoading"
      :error-message="error?.message"
      :row-key="(row: Organization) => row.id"
    >
      <template #toolbar>
        <n-button type="primary" @click="openForm">
          <template #icon><Plus :size="16" /></template>
          新增组织
        </n-button>
      </template>
    </DataTableList>

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
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
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
import { h } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NSpace,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useOrganizationsQuery, useUpdateOrganizationStatus,
} from '@/api/hooks/useOrganizations'
import type { Organization } from '@/api/types'
import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { useFormModal } from '@/composables/useFormModal'

const { data: organizations, isLoading, error } = useOrganizationsQuery()
const createMutation = useCreateOrganization()
const statusMutation = useUpdateOrganizationStatus()

// 创建组织表单状态聚合到 useFormModal；toPayload 处理可选字段的 || undefined 过滤
const { form, formVisible, creating, submitError, openForm, submit } = useFormModal({
  initial: {
    name: '',
    contact_name: '',
    contact_phone: '',
    remark: '',
    credit_warning_threshold: undefined as number | undefined,
  },
  mutation: createMutation,
  toPayload: (f) => ({
    name: f.name,
    contact_name: f.contact_name || undefined,
    contact_phone: f.contact_phone || undefined,
    remark: f.remark || undefined,
    credit_warning_threshold: typeof f.credit_warning_threshold === 'number'
      ? f.credit_warning_threshold : undefined,
  }),
})

const columns = [
  // 名称列：含 remark 副标题，保留页面内 render
  {
    title: '名称',
    key: 'name',
    render: (row: Organization) => [
      h('strong', row.name),
      row.remark
        ? h('small', { class: 'data-table-subtitle' }, row.remark)
        : null,
    ],
  },
  statusColumn<Organization>('状态', r => formatOrgStatus(r.status)),
  // 联系人/电话/预警阈值列：保留页面内 render
  { title: '联系人', key: 'contact_name', render: (row: Organization) => row.contact_name || '—' },
  { title: '电话', key: 'contact_phone', render: (row: Organization) => row.contact_phone || '—' },
  {
    title: '预警阈值',
    key: 'credit_warning_threshold',
    render: (row: Organization) => typeof row.credit_warning_threshold === 'number'
      ? `${row.credit_warning_threshold}%` : '—',
  },
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染
  actionColumn<Organization>([
    { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
    { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
  ]),
]

function onToggle(org: Organization, action: 'enable' | 'disable') {
  statusMutation.mutate({ orgId: org.id, action })
}
</script>
