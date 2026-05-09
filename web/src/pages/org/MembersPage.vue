<template>
  <div style="display: grid; gap: 18px">
    <!-- 成员列表 -->
    <DataTableList
      title="成员列表"
      :eyebrow="orgEyebrow"
      :columns="columns"
      :data="members ?? []"
      :loading="isLoading"
      :row-key="(row: Member) => row.id"
    >
      <template #toolbar>
        <n-button v-if="effectiveOrgId" @click="router.push('/members/new')">
          创建并初始化
        </n-button>
        <n-button type="primary" :disabled="!effectiveOrgId" @click="openForm">
          新增成员
        </n-button>
      </template>
    </DataTableList>

    <p v-if="resetFeedback" class="state-text" :class="{ danger: resetError }">{{ resetFeedback }}</p>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">创建成员</h2>
          <n-button quaternary circle @click="formVisible = false">✕</n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
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
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, type SelectOption,
} from 'naive-ui'

import { formatMemberRole, formatMemberStatus } from '@/domain/status'
import {
  useCreateMember, useDeleteMember, useMembersQuery, useResetMemberPassword,
  useSetMemberStatus, type MemberFormPayload,
} from '@/api/hooks/useMembers'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { useFormModal } from '@/composables/useFormModal'
import type { Member } from '@/api'
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

// 创建成员表单状态聚合到 useFormModal
const { form, formVisible, creating, submitError, openForm, submit } = useFormModal<MemberFormPayload>({
  initial: { username: '', display_name: '', password: '', role: 'org_member' },
  mutation: createMutation,
})

const roleOptions: SelectOption[] = [
  { label: '组织成员', value: 'org_member' },
  { label: '组织管理员', value: 'org_admin' },
]

const columns = [
  { title: '用户名', key: 'username' },
  { title: '姓名', key: 'display_name' },
  // 角色列页面内 render，不抽 factory
  { title: '角色', key: 'role', render: (row: Member) => formatMemberRole(row.role) },
  statusColumn<Member>('状态', r => formatMemberStatus(r.status)),
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染
  actionColumn<Member>([
    { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
    { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
    { label: '重置密码', onClick: r => openResetForm(r) },
    { label: '删除', type: 'error', onClick: r => { memberToDelete.value = r } },
  ]),
]

function onToggle(member: Member, action: 'enable' | 'disable') {
  statusMutation.mutate({ userId: member.id, action })
}

async function onConfirmDelete() {
  if (!memberToDelete.value) return
  try { await deleteMutation.mutateAsync(memberToDelete.value.id) }
  catch (err) { console.error('删除成员失败', err) }
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
