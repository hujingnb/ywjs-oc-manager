<template>
  <div style="display: grid; gap: 18px">
    <!-- 成员列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">{{ orgEyebrow }}</p>
          <h2 style="margin: 0">成员列表</h2>
        </div>
      </template>
      <template #header-extra>
        <n-space>
          <n-button v-if="effectiveOrgId" @click="router.push('/members/new')">
            创建并初始化
          </n-button>
          <n-button type="primary" :disabled="!effectiveOrgId" @click="openForm">
            新增成员
          </n-button>
        </n-space>
      </template>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法查看成员。</div>
      <n-data-table
        v-else
        :columns="columns"
        :data="members ?? []"
        :loading="isLoading"
        size="small"
        :bordered="false"
        :row-key="(row) => row.id"
      />

      <p v-if="resetFeedback" class="state-text" :class="{ danger: resetError }">{{ resetFeedback }}</p>
    </n-card>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">创建成员</h2>
          <n-button quaternary circle @click="formVisible = false">✕</n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="onSubmit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="用户名 *">
              <n-input v-model:value="form.username" placeholder="username" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="显示名 *">
              <n-input v-model:value="form.display_name" placeholder="显示名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="初始密码 *">
              <n-input v-model:value="form.password" type="password" placeholder="至少 8 位" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="角色">
              <n-select v-model:value="form.role" :options="roleOptions" />
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

    <!-- Modals -->
    <ConfirmActionModal
      :visible="!!memberToDelete"
      title="确认删除成员"
      :message="memberToDelete ? `将禁用账号 ${memberToDelete.username} 并提交其名下应用的删除任务，操作不可撤销。` : ''"
      confirm-label="确认删除"
      :busy="deleteMutation.isPending.value"
      @confirm="onConfirmDelete"
      @cancel="memberToDelete = null"
    />
    <ConfirmActionModal
      :visible="!!resetTarget"
      title="确认重置成员密码"
      :message="resetTarget ? `将强制重置成员 ${resetTarget.username} 的登录密码，原密码立即失效。` : ''"
      confirm-label="确认重置"
      :busy="resetMutation.isPending.value"
      :verify-value="resetTarget?.username"
      :verify-hint='resetTarget ? `输入成员登录名 "${resetTarget.username}" 以确认重置` : ""'
      @confirm="onConfirmReset"
      @cancel="resetTarget = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, NTag, type DataTableColumns, type SelectOption,
} from 'naive-ui'

import { formatMemberRole, formatMemberStatus } from '@/domain/status'
import {
  useCreateMember, useDeleteMember, useMembersQuery, useResetMemberPassword,
  useSetMemberStatus, type MemberFormPayload,
} from '@/api/hooks/useMembers'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import type { Member } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const router = useRouter()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 组织成员' : '我的组织')

const { data: members, isLoading } = useMembersQuery(effectiveOrgId)
const createMutation = useCreateMember(effectiveOrgId)
const statusMutation = useSetMemberStatus(effectiveOrgId)
const deleteMutation = useDeleteMember(effectiveOrgId)
const memberToDelete = ref<Member | null>(null)
const resetTarget = ref<Member | null>(null)
const resetNewPassword = ref('')
const resetMutation = useResetMemberPassword()
const resetFeedback = ref('')
const resetError = ref(false)

const formVisible = ref(false)
const submitError = ref<string | null>(null)
const creating = ref(false)
const form = reactive<MemberFormPayload>({
  username: '', display_name: '', password: '', role: 'org_member',
})

const roleOptions: SelectOption[] = [
  { label: '组织成员', value: 'org_member' },
  { label: '组织管理员', value: 'org_admin' },
]

function toneToTagType(tone: string): 'success' | 'warning' | 'error' | 'default' {
  const m: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return m[tone] ?? 'default'
}

const columns: DataTableColumns<Member> = [
  { title: '用户名', key: 'username' },
  { title: '姓名', key: 'display_name' },
  { title: '角色', key: 'role', render: (row) => formatMemberRole(row.role) },
  {
    title: '状态', key: 'status',
    render: (row) => {
      const v = formatMemberStatus(row.status)
      return h(NTag, { type: toneToTagType(v.tone), size: 'small', bordered: false }, { default: () => v.label })
    },
  },
  {
    title: '操作', key: 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => [
        row.status === 'active'
          ? h(NButton, { size: 'small', onClick: () => onToggle(row, 'disable') }, { default: () => '禁用' })
          : h(NButton, { size: 'small', type: 'primary', onClick: () => onToggle(row, 'enable') }, { default: () => '启用' }),
        h(NButton, { size: 'small', onClick: () => openResetForm(row) }, { default: () => '重置密码' }),
        h(NButton, { size: 'small', type: 'error', onClick: () => { memberToDelete.value = row } }, { default: () => '删除' }),
      ]
    }),
  },
]

function openForm() {
  formVisible.value = true; submitError.value = null
  form.username = ''; form.display_name = ''; form.password = ''; form.role = 'org_member'
}

async function onSubmit() {
  submitError.value = null; creating.value = true
  try {
    await createMutation.mutateAsync({ ...form })
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '创建成员失败'
  } finally { creating.value = false }
}

function onToggle(member: Member, action: 'enable' | 'disable') {
  statusMutation.mutate({ userId: member.id, action })
}

async function onConfirmDelete() {
  if (!memberToDelete.value) return
  try { await deleteMutation.mutateAsync(memberToDelete.value.id) }
  catch (err) { submitError.value = err instanceof Error ? err.message : '删除成员失败' }
  finally { memberToDelete.value = null }
}

function openResetForm(member: Member) {
  const pwd = window.prompt(`输入成员 ${member.username} 的新密码（至少 8 位）`)
  if (!pwd || pwd.length < 8) return
  resetTarget.value = member; resetNewPassword.value = pwd
  resetFeedback.value = ''; resetError.value = false
}

async function onConfirmReset() {
  if (!resetTarget.value) return
  resetFeedback.value = ''; resetError.value = false
  try {
    await resetMutation.mutateAsync({ userId: resetTarget.value.id, password: resetNewPassword.value })
    resetFeedback.value = '已重置密码'; resetTarget.value = null
  } catch (err) {
    resetError.value = true
    resetFeedback.value = err instanceof Error ? err.message : '重置失败'
  }
}
</script>
