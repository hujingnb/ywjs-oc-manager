<template>
  <div style="display: grid; gap: 18px">
    <!-- 成员列表 -->
    <DataTableList
      title="成员列表"
      :eyebrow="orgEyebrow"
      :columns="columns"
      :data="members ?? []"
      :loading="isLoading || organizationsLoading"
      :error-message="errorMessage"
      :row-key="(row: Member) => row.id"
    >
      <template #toolbar>
        <n-select
          v-if="isPlatformAdmin"
          v-model:value="selectedOrgId"
          :options="orgOptions"
          style="width: 220px"
          placeholder="选择组织"
        />
        <n-button v-if="canOnboardMember" @click="router.push('/members/new')">
          创建并初始化
        </n-button>
        <n-button v-if="canManageMembers" type="primary" @click="openForm">
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

    <!-- 平台管理员为已有成员复建实例 -->
    <n-card v-if="createAppTarget" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">创建新实例</h2>
          <n-button quaternary circle @click="createAppTarget = null">✕</n-button>
        </div>
      </template>
      <n-form label-placement="top" @submit.prevent="onSubmitCreateApp">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="实例名 *">
              <n-input v-model:value="createAppForm.app_name" placeholder="实例名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="人设模式">
              <n-select v-model:value="createAppForm.persona_mode" :options="personaModeOptions" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="模型 *">
              <n-select
                v-model:value="createAppForm.model_id"
                :options="modelOptions"
                :loading="organizationQuery.isLoading.value"
                :disabled="organizationQuery.isError.value || modelOptions.length === 0"
                placeholder="选择模型"
              />
              <p v-if="createAppModelError" class="state-text danger">{{ createAppModelError }}</p>
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="实例 prompt（可选）">
              <n-input v-model:value="createAppForm.app_prompt" type="textarea" :rows="3" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="createAppTarget = null">取消</n-button>
              <n-button
                type="primary"
                attr-type="submit"
                :loading="createAppMutation.isPending.value"
                :disabled="!canSubmitCreateApp"
                @click.prevent="onSubmitCreateApp"
              >
                提交创建
              </n-button>
            </n-space>
            <p v-if="createAppError" class="state-text danger">{{ createAppError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <p v-if="createAppResult" class="state-text">
      已创建实例 {{ createAppResult.app.name }}，Job ID：{{ createAppResult.job_id }}
    </p>

    <!-- Modals -->
    <ConfirmActionModal
      :visible="!!memberToDelete"
      title="确认删除成员"
      :message="memberToDelete ? `将禁用账号 ${memberToDelete.username} 并提交其名下实例的删除任务，操作不可撤销。` : ''"
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
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, type SelectOption,
} from 'naive-ui'

import { formatMemberRole, formatMemberStatus } from '@/domain/status'
import {
  useCreateMember, useCreateMemberApp, useDeleteMember, useMembersQuery, useResetMemberPassword,
  useSetMemberStatus, type CreateMemberAppPayload, type CreateMemberAppResult, type MemberFormPayload,
} from '@/api/hooks/useMembers'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { useFormModal } from '@/composables/useFormModal'
import type { Member } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'

// MembersPage 管理组织成员列表，支持创建、启停、删除和重置密码。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const router = useRouter()
// 平台管理员通过组织选择器查看成员，组织管理员默认管理自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 组织成员' : '我的组织')
// 一键开户会同步创建应用，按后端 CanCreateAppForOrg 规则仅开放给本组织管理员。
const canOnboardMember = computed(() => auth.user?.role === 'org_admin' && Boolean(effectiveOrgId.value))
// 成员写操作只允许本组织管理员；平台管理员在本页仅查看成员信息。
const canManageMembers = computed(() => auth.user?.role === 'org_admin' && auth.user?.org_id === effectiveOrgId.value)
// 当前登录用户不能删除自身；后端同样会兜底拒绝，前端隐藏危险入口减少误操作。
const currentUserId = computed(() => auth.user?.id)

const { data: members, isLoading } = useMembersQuery(effectiveOrgId)
const organizationQuery = useOrganizationQuery(effectiveOrgId)
const modelOptions = computed(() => (organizationQuery.data.value?.enabled_models ?? []).map(model => ({
  label: model,
  value: model,
})))
const createMutation = useCreateMember(effectiveOrgId)
const createAppMutation = useCreateMemberApp(effectiveOrgId)
const statusMutation = useSetMemberStatus(effectiveOrgId)
const deleteMutation = useDeleteMember(effectiveOrgId)
// memberToDelete 保存二次确认中的目标成员，确认后才调用删除接口。
const memberToDelete = ref<Member | null>(null)
// resetTarget/resetNewPassword 暂存重置密码确认流程中的成员和新密码。
const resetTarget = ref<Member | null>(null)
const resetNewPassword = ref('')
const resetMutation = useResetMemberPassword()
const resetFeedback = ref('')
const resetError = ref(false)
// createAppTarget 仅在平台管理员复建成员实例时有值，关闭表单即清空。
const createAppTarget = ref<Member | null>(null)
const createAppResult = ref<CreateMemberAppResult | null>(null)
const createAppError = ref('')
const createAppForm = ref<CreateMemberAppPayload>({
  app_name: '',
  persona_mode: 'org_inherited',
  channel_type: 'wechat',
  model_id: '',
})
// canSubmitCreateApp 保证复建实例时始终提交组织 allowlist 内的模型 ID。
const canSubmitCreateApp = computed(() =>
  Boolean(createAppForm.value.model_id && !createAppModelError.value && !createAppMutation.isPending.value),
)
// createAppModelError 暴露组织模型配置问题，避免平台管理员只能看到禁用的提交按钮。
const createAppModelError = computed(() => {
  if (organizationQuery.isError.value) return '组织可用模型获取失败，请重试'
  if (!organizationQuery.isLoading.value && modelOptions.value.length === 0) return '当前组织未配置可用模型'
  return ''
})
// 平台管理员切换组织时关闭复建表单，防止旧成员和新组织 ID 组合成跨组织提交。
watch(effectiveOrgId, () => {
  createAppTarget.value = null
  createAppResult.value = null
  createAppError.value = ''
})

// errorMessage 区分平台管理员无可选组织和组织用户无归属。
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value) return isPlatformAdmin.value ? '暂无可查看组织' : '当前账号未关联组织'
  return undefined
})

// 创建成员表单状态聚合到 useFormModal
const { form, formVisible, creating, submitError, openForm, submit } = useFormModal<MemberFormPayload>({
  initial: { username: '', display_name: '', password: '', role: 'org_member' },
  mutation: createMutation,
})

const roleOptions: SelectOption[] = [
  { label: '组织成员', value: 'org_member' },
  { label: '组织管理员', value: 'org_admin' },
]

const personaModeOptions: SelectOption[] = [
  { label: '沿用组织人设', value: 'org_inherited' },
  { label: '实例覆盖', value: 'app_override' },
]

// columns 展示成员身份和状态，启用/禁用按钮按当前成员状态互斥显示。
const columns = [
  { title: '用户名', key: 'username' },
  { title: '姓名', key: 'display_name' },
  // 角色列页面内 render，不抽 factory
  { title: '角色', key: 'role', render: (row: Member) => formatMemberRole(row.role) },
  statusColumn<Member>('状态', r => formatMemberStatus(r.status)),
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染
  actionColumn<Member>([
    { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => !canManageMembers.value || r.status !== 'active' },
    { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => !canManageMembers.value || r.status === 'active' },
    { label: '重置密码', hidden: () => !canManageMembers.value, onClick: r => openResetForm(r) },
    { label: '删除', type: 'error', hidden: r => !canManageMembers.value || r.id === currentUserId.value, onClick: r => { memberToDelete.value = r } },
    { label: '创建新实例', type: 'primary', hidden: () => auth.user?.role !== 'platform_admin', onClick: r => openCreateAppForm(r) },
  ]),
]

// onToggle 调用成员状态 mutation，列表刷新由 hook 的失效策略处理。
function onToggle(member: Member, action: 'enable' | 'disable') {
  statusMutation.mutate({ userId: member.id, action })
}

// onConfirmDelete 删除确认目标；失败只记录控制台，避免弹框残留阻塞后续操作。
async function onConfirmDelete() {
  if (!memberToDelete.value) return
  try { await deleteMutation.mutateAsync(memberToDelete.value.id) }
  catch (err) { console.error('删除成员失败', err) }
  finally { memberToDelete.value = null }
}

// openResetForm 通过 prompt 获取新密码，长度不满足时不进入确认流程。
function openResetForm(member: Member) {
  const pwd = window.prompt(`输入成员 ${member.username} 的新密码（至少 8 位）`)
  if (!pwd || pwd.length < 8) return
  resetTarget.value = member; resetNewPassword.value = pwd
  resetFeedback.value = ''; resetError.value = false
}

// openCreateAppForm 打开平台管理员为已有成员复建实例的表单。
function openCreateAppForm(member: Member) {
  createAppTarget.value = member
  createAppResult.value = null
  createAppError.value = ''
  createAppForm.value = {
    app_name: '',
    persona_mode: 'org_inherited',
    channel_type: 'wechat',
    model_id: String(modelOptions.value[0]?.value ?? ''),
  }
}

// onSubmitCreateApp 提交已有成员实例创建请求，并展示后端返回的新实例与 job。
async function onSubmitCreateApp() {
  if (!createAppTarget.value) return
  if (!createAppForm.value.model_id) {
    createAppError.value = '请选择模型'
    return
  }
  createAppError.value = ''
  try {
    createAppResult.value = await createAppMutation.mutateAsync({
      userId: createAppTarget.value.id,
      payload: { ...createAppForm.value },
    })
    createAppTarget.value = null
  } catch (err) {
    createAppError.value = err instanceof Error ? err.message : '创建实例失败'
  }
}

// onConfirmReset 提交重置密码，并把结果反馈到页面内状态文本。
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
