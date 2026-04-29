<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Platform</p>
          <h2>组织列表</h2>
        </div>
        <button class="primary-button" type="button" @click="openForm">
          <Plus :size="16" />
          <span>新增组织</span>
        </button>
      </div>

      <div v-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <table v-else>
        <thead>
          <tr>
            <th>名称</th>
            <th>状态</th>
            <th>联系人</th>
            <th>电话</th>
            <th>预警阈值</th>
            <th class="actions-column">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="org in organizations" :key="org.id">
            <td>
              <strong>{{ org.name }}</strong>
              <small v-if="org.remark">{{ org.remark }}</small>
            </td>
            <td>
              <span :class="['status-pill', formatOrgStatus(org.status).tone]">
                {{ formatOrgStatus(org.status).label }}
              </span>
            </td>
            <td>{{ org.contact_name || '—' }}</td>
            <td>{{ org.contact_phone || '—' }}</td>
            <td>{{ formatThreshold(org.credit_warning_threshold) }}</td>
            <td class="actions-column">
              <button v-if="org.status === 'active'" class="secondary-button" type="button" @click="onToggle(org, 'disable')">
                禁用
              </button>
              <button v-else class="secondary-button" type="button" @click="onToggle(org, 'enable')">
                启用
              </button>
            </td>
          </tr>
          <tr v-if="!organizations?.length">
            <td colspan="6" class="state-text">尚未创建组织</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="formVisible" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">New</p>
          <h2>创建组织</h2>
        </div>
        <button class="icon-button" type="button" aria-label="关闭" @click="formVisible = false">
          <X :size="18" />
        </button>
      </div>
      <form class="form-grid" @submit.prevent="onSubmit">
        <label>
          <span>名称 *</span>
          <input v-model.trim="form.name" required type="text" />
        </label>
        <label>
          <span>联系人</span>
          <input v-model.trim="form.contact_name" type="text" />
        </label>
        <label>
          <span>联系电话</span>
          <input v-model.trim="form.contact_phone" type="text" />
        </label>
        <label>
          <span>余额预警阈值 (%)</span>
          <input v-model.number="form.credit_warning_threshold" type="number" min="0" max="100" />
        </label>
        <label class="form-grid-full">
          <span>备注</span>
          <textarea v-model.trim="form.remark" rows="2"></textarea>
        </label>
        <div class="form-actions">
          <button class="secondary-button" type="button" @click="formVisible = false">取消</button>
          <button class="primary-button" type="submit" :disabled="creating">
            {{ creating ? '提交中…' : '保存' }}
          </button>
        </div>
        <p v-if="submitError" class="state-text danger form-grid-full">{{ submitError }}</p>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization,
  useOrganizationsQuery,
  useUpdateOrganizationStatus,
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
  name: '',
  contact_name: '',
  contact_phone: '',
  remark: '',
  credit_warning_threshold: undefined,
})

function openForm() {
  formVisible.value = true
  submitError.value = null
  form.name = ''
  form.contact_name = ''
  form.contact_phone = ''
  form.remark = ''
  form.credit_warning_threshold = undefined
}

async function onSubmit() {
  submitError.value = null
  creating.value = true
  try {
    await createMutation.mutateAsync({
      name: form.name,
      contact_name: form.contact_name || undefined,
      contact_phone: form.contact_phone || undefined,
      remark: form.remark || undefined,
      credit_warning_threshold:
        typeof form.credit_warning_threshold === 'number' ? form.credit_warning_threshold : undefined,
    })
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '创建组织失败'
  } finally {
    creating.value = false
  }
}

function onToggle(org: Organization, action: 'enable' | 'disable') {
  statusMutation.mutate({ orgId: org.id, action })
}

function formatThreshold(value?: number) {
  return typeof value === 'number' ? `${value}%` : '—'
}
</script>
