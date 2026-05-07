<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">{{ orgEyebrow }}</p>
          <h2>成员列表</h2>
        </div>
        <div class="topbar-actions">
          <RouterLink v-if="effectiveOrgId" class="secondary-button" to="/members/new">
            <Plus :size="16" />
            <span>创建并初始化</span>
          </RouterLink>
          <button class="primary-button" type="button" @click="openForm" :disabled="!effectiveOrgId">
            <Plus :size="16" />
            <span>新增成员</span>
          </button>
        </div>
      </div>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法查看成员。</div>
      <template v-else>
        <div v-if="isLoading" class="state-text">加载中…</div>
        <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
        <table v-else>
          <thead>
            <tr>
              <th>用户名</th>
              <th>姓名</th>
              <th>角色</th>
              <th>状态</th>
              <th class="actions-column">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="member in members" :key="member.id">
              <td>{{ member.username }}</td>
              <td>{{ member.display_name }}</td>
              <td>{{ formatMemberRole(member.role) }}</td>
              <td>
                <span :class="['status-pill', formatMemberStatus(member.status).tone]">
                  {{ formatMemberStatus(member.status).label }}
                </span>
              </td>
              <td class="actions-column">
                <button v-if="member.status === 'active'" class="secondary-button" type="button" @click="onToggle(member, 'disable')">
                  禁用
                </button>
                <button v-else class="secondary-button" type="button" @click="onToggle(member, 'enable')">
                  启用
                </button>
                <button class="secondary-button" type="button" @click="openResetForm(member)">
                  重置密码
                </button>
                <button class="secondary-button danger" type="button" @click="confirmDelete(member)">
                  删除
                </button>
              </td>
            </tr>
            <tr v-if="!members?.length">
              <td colspan="5" class="state-text">尚未添加成员</td>
            </tr>
          </tbody>
        </table>
      </template>
    </section>

    <section v-if="formVisible" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">New</p>
          <h2>创建成员</h2>
        </div>
        <button class="icon-button" type="button" aria-label="关闭" @click="formVisible = false">
          <X :size="18" />
        </button>
      </div>
      <form class="form-grid" @submit.prevent="onSubmit">
        <label>
          <span>用户名 *</span>
          <input v-model.trim="form.username" required type="text" autocomplete="username" />
        </label>
        <label>
          <span>显示名 *</span>
          <input v-model.trim="form.display_name" required type="text" />
        </label>
        <label>
          <span>初始密码 *</span>
          <input v-model="form.password" required type="password" autocomplete="new-password" />
        </label>
        <label>
          <span>角色</span>
          <select v-model="form.role">
            <option value="org_member">组织成员</option>
            <option value="org_admin">组织管理员</option>
          </select>
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

    <p v-if="resetFeedback" class="state-text" :class="{ danger: resetError }">{{ resetFeedback }}</p>
  </main>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'

import { formatMemberRole, formatMemberStatus } from '@/domain/status'
import {
  useCreateMember,
  useDeleteMember,
  useMembersQuery,
  useResetMemberPassword,
  useSetMemberStatus,
  type MemberFormPayload,
} from '@/api/hooks/useMembers'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import type { Member } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()

// 组织角色看自己组织；平台管理员可以传入 orgId 指定组织。
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 组织成员' : '我的组织'))

const { data: members, isLoading, error } = useMembersQuery(effectiveOrgId)
const createMutation = useCreateMember(effectiveOrgId)
const statusMutation = useSetMemberStatus(effectiveOrgId)
const deleteMutation = useDeleteMember(effectiveOrgId)
const memberToDelete = ref<Member | null>(null)

// 重置密码：通过 prompt 收集新密码，再以登录名强校验确认。
const resetTarget = ref<Member | null>(null)
const resetNewPassword = ref('')
const resetMutation = useResetMemberPassword()
const resetFeedback = ref('')
const resetError = ref(false)

const formVisible = ref(false)
const submitError = ref<string | null>(null)
const creating = ref(false)
const form = reactive<MemberFormPayload>({
  username: '',
  display_name: '',
  password: '',
  role: 'org_member',
})

function openForm() {
  formVisible.value = true
  submitError.value = null
  form.username = ''
  form.display_name = ''
  form.password = ''
  form.role = 'org_member'
}

async function onSubmit() {
  submitError.value = null
  creating.value = true
  try {
    await createMutation.mutateAsync({
      username: form.username,
      display_name: form.display_name,
      password: form.password,
      role: form.role,
    })
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '创建成员失败'
  } finally {
    creating.value = false
  }
}

function onToggle(member: Member, action: 'enable' | 'disable') {
  statusMutation.mutate({ userId: member.id, action })
}

function confirmDelete(member: Member) {
  memberToDelete.value = member
}

async function onConfirmDelete() {
  if (!memberToDelete.value) return
  try {
    await deleteMutation.mutateAsync(memberToDelete.value.id)
  } catch (err: unknown) {
    submitError.value = err instanceof Error ? err.message : '删除成员失败'
  } finally {
    memberToDelete.value = null
  }
}

// 用 prompt 收集新密码，至少 8 位才打开强校验 modal。
function openResetForm(member: Member) {
  const pwd = window.prompt(`输入成员 ${member.username} 的新密码（至少 8 位）`)
  if (!pwd || pwd.length < 8) {
    return
  }
  resetTarget.value = member
  resetNewPassword.value = pwd
  resetFeedback.value = ''
  resetError.value = false
}

async function onConfirmReset() {
  if (!resetTarget.value) return
  resetFeedback.value = ''
  resetError.value = false
  try {
    await resetMutation.mutateAsync({
      userId: resetTarget.value.id,
      password: resetNewPassword.value,
    })
    resetFeedback.value = '已重置密码'
    resetTarget.value = null
  } catch (err: unknown) {
    resetError.value = true
    resetFeedback.value = err instanceof Error ? err.message : '重置失败'
  }
}
</script>
