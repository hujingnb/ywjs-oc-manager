<template>
  <div style="display: grid; gap: 18px">
    <!-- 成员列表 -->
    <DataTableList
      :title="t('org.members.list.title')"
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
          :placeholder="t('org.members.list.selectOrg')"
        />
        <n-button v-if="canOnboardMember" @click="router.push('/members/new')">
          {{ t('org.members.list.createAndInit') }}
        </n-button>
        <n-button v-if="canManageMembers" type="primary" @click="openForm">
          {{ t('org.members.list.addMember') }}
        </n-button>
      </template>
    </DataTableList>

    <p v-if="resetFeedback" class="state-text" :class="{ danger: resetError }">{{ resetFeedback }}</p>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">{{ t('org.members.form.createTitle') }}</h2>
          <n-button quaternary circle @click="formVisible = false">✕</n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item :label="t('org.members.form.username')">
              <n-input v-model:value="form.username" :placeholder="t('org.members.form.usernamePlaceholder')" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('org.members.form.displayName')">
              <n-input v-model:value="form.display_name" :placeholder="t('org.members.form.displayNamePlaceholder')" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('org.members.form.password')">
              <n-input v-model:value="form.password" type="password" :placeholder="t('org.members.form.passwordPlaceholder')" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('org.members.form.role')">
              <n-select v-model:value="form.role" :options="roleOptions" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="formVisible = false">{{ t('common.actions.cancel') }}</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating">{{ t('common.actions.save') }}</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <!-- 为已有成员补建实例：platform_admin 跨组织 / org_admin 本组织共用此表单 -->
    <n-card v-if="createAppTarget" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <h2 style="margin: 0">{{ t('org.members.createApp.title') }}</h2>
          <n-button quaternary circle @click="createAppTarget = null">✕</n-button>
        </div>
      </template>
      <n-form label-placement="top" @submit.prevent="onSubmitCreateApp">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item :label="t('org.members.createApp.appName')">
              <n-input v-model:value="createAppForm.app_name" :placeholder="t('org.members.form.displayNamePlaceholder')" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <!-- 助手版本从组织 allowlist 过滤，必选；与 CreateMemberPage 保持一致 -->
            <n-form-item :label="t('org.members.createApp.assistantVersion')">
              <n-select
                v-model:value="createAppForm.version_id"
                :options="versionOptions"
                :loading="versionsLoading || organizationQuery.isLoading.value"
                :placeholder="t('org.members.createApp.assistantVersionPlaceholder')"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="createAppTarget = null">{{ t('common.actions.cancel') }}</n-button>
              <n-button
                type="primary"
                attr-type="submit"
                :loading="createAppMutation.isPending.value"
                :disabled="!createAppForm.app_name || !createAppForm.version_id || createAppMutation.isPending.value"
                @click.prevent="onSubmitCreateApp"
              >
                {{ t('org.members.createApp.submitCreate') }}
              </n-button>
            </n-space>
            <p v-if="createAppError" class="state-text danger">{{ createAppError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <p v-if="createAppResult" class="state-text">
      {{ t('org.members.createApp.createdResult', { name: createAppResult.app.name, jobId: createAppResult.job_id }) }}
    </p>

    <!-- Modals -->
    <ConfirmActionModal
      :visible="!!memberToDelete"
      :title="t('org.members.modal.deleteTitle')"
      :message="memberToDelete ? t('org.members.modal.deleteMessage', { username: memberToDelete.username }) : ''"
      :confirm-label="t('org.members.modal.deleteConfirm')"
      :busy="deleteMutation.isPending.value"
      @confirm="onConfirmDelete"
      @cancel="memberToDelete = null"
    />
    <ConfirmActionModal
      :visible="!!resetTarget"
      :title="t('org.members.modal.resetTitle')"
      :message="resetTarget ? t('org.members.modal.resetMessage', { username: resetTarget.username }) : ''"
      :confirm-label="t('org.members.modal.resetConfirm')"
      :busy="resetMutation.isPending.value"
      :verify-value="resetTarget?.username"
      :verify-hint='resetTarget ? t("org.members.modal.resetPasswordPrompt", { username: resetTarget.username }) : ""'
      @confirm="onConfirmReset"
      @cancel="resetTarget = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, NTag, type SelectOption,
} from 'naive-ui'

import { formatMemberRole, formatMemberStatus } from '@/domain/status'
import {
  useCreateMember, useCreateMemberApp, useDeleteMember, useMembersQuery, useResetMemberPassword,
  useSetMemberStatus, type CreateMemberAppPayload, type CreateMemberAppResult, type MemberFormPayload,
} from '@/api/hooks/useMembers'
import { useAssistantVersionsQuery } from '@/api/hooks/useAssistantVersions'
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
const { t } = useI18n()
// 平台管理员通过组织选择器查看成员，组织管理员默认管理自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))
// orgEyebrow 随角色与语言切换响应式更新副标题。
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? t('org.members.page.eyebrowPlatform') : t('org.members.page.eyebrowOrg'))
// 一键开户会同步创建应用，按后端 CanCreateAppForOrg 规则仅开放给本组织管理员。
const canOnboardMember = computed(() => auth.user?.role === 'org_admin' && Boolean(effectiveOrgId.value))
// 成员写操作只允许本组织管理员；平台管理员在本页仅查看成员信息。
const canManageMembers = computed(() => auth.user?.role === 'org_admin' && auth.user?.org_id === effectiveOrgId.value)
// canCreateAppForMember 与后端 auth.CanCreateAppForMember 对齐：
// platform_admin 跨组织可补建；org_admin 仅在本组织可补建；普通成员不可。
const canCreateAppForMember = computed(() =>
  auth.user?.role === 'platform_admin' ||
  (auth.user?.role === 'org_admin' && auth.user?.org_id === effectiveOrgId.value))
// 当前登录用户不能删除自身；后端同样会兜底拒绝，前端隐藏危险入口减少误操作。
const currentUserId = computed(() => auth.user?.id)

const { data: members, isLoading } = useMembersQuery(effectiveOrgId)
const organizationQuery = useOrganizationQuery(effectiveOrgId)
// 查询全部助手版本目录，与组织 allowlist 取交集供补建表单选择。
const { data: versionsData, isLoading: versionsLoading } = useAssistantVersionsQuery()
// versionOptions 由组织 allowlist 与全量版本目录取交集，仅展示允许使用的版本。
const versionOptions = computed<SelectOption[]>(() => {
  const org = organizationQuery.data.value
  const versions = versionsData.value
  if (!org || !versions) return []
  const allowedIds = new Set(org.assistant_version_ids ?? [])
  return versions
    .filter(v => allowedIds.has(v.id))
    .map(v => ({ label: v.name, value: v.id }))
})
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
// createAppTarget 仅在管理员补建成员实例时有值，关闭表单即清空。
const createAppTarget = ref<Member | null>(null)
const createAppResult = ref<CreateMemberAppResult | null>(null)
const createAppError = ref('')
// createAppForm 补建实例表单；version_id 必填，与 CreateMemberPage 保持一致。
const createAppForm = ref<CreateMemberAppPayload>({
  app_name: '',
  version_id: '',
  channel_type: 'wechat',
})
// 切换组织时关闭补建表单，防止旧成员和新组织 ID 组合成跨组织提交。
watch(effectiveOrgId, () => {
  createAppTarget.value = null
  createAppResult.value = null
  createAppError.value = ''
})

// errorMessage 区分平台管理员无可选组织和组织用户无归属。
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value) return isPlatformAdmin.value ? t('org.members.state.noOrg') : t('org.members.state.noOrgLinked')
  return undefined
})

// 创建成员表单状态聚合到 useFormModal
const { form, formVisible, creating, submitError, openForm, submit } = useFormModal<MemberFormPayload>({
  initial: { username: '', display_name: '', password: '', role: 'org_member' },
  mutation: createMutation,
})

// roleOptions 随语言切换响应式更新角色选项文案。
const roleOptions = computed<SelectOption[]>(() => [
  { label: t('org.members.role.orgMember'), value: 'org_member' },
  { label: t('org.members.role.orgAdmin'), value: 'org_admin' },
])

// columns 展示成员身份和状态，启用/禁用按钮按当前成员状态互斥显示。
// 使用 computed 确保语言切换时列头文案和操作按钮文案响应式更新。
const columns = computed(() => [
  { title: t('org.members.table.username'), key: 'username' },
  { title: t('org.members.table.displayName'), key: 'display_name' },
  // 角色列页面内 render，不抽 factory
  { title: t('org.members.table.role'), key: 'role', render: (row: Member) => formatMemberRole(row.role) },
  statusColumn<Member>(t('org.members.table.status'), r => formatMemberStatus(r.status)),
  {
    title: t('org.members.table.instance'),
    key: 'active_app_name',
    render: (row: Member) =>
      row.active_app_id
        ? h(RouterLink, { to: `/apps/${row.active_app_id}/overview` }, () => row.active_app_name ?? '')
        : h(NTag, { type: 'warning', size: 'small' }, () => t('org.members.table.noInstance')),
  },
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染
  actionColumn<Member>([
    { label: t('org.members.actions.disable'), onClick: r => onToggle(r, 'disable'), hidden: r => !canManageMembers.value || r.status !== 'active' },
    { label: t('org.members.actions.enable'), type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => !canManageMembers.value || r.status === 'active' },
    { label: t('org.members.actions.resetPassword'), hidden: () => !canManageMembers.value, onClick: r => openResetForm(r) },
    { label: t('common.actions.delete'), type: 'error', hidden: r => !canManageMembers.value || r.id === currentUserId.value, onClick: r => { memberToDelete.value = r } },
    // 仅在「当前账号有补建权限」且「该行没有活跃实例」时显示，避免点击后被后端 ErrMemberCreateInvalid 兜底。
    { label: t('org.members.actions.createInstance'), type: 'primary',
      hidden: r => !canCreateAppForMember.value || Boolean(r.active_app_id),
      onClick: r => openCreateAppForm(r) },
  ]),
])

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
  const pwd = window.prompt(t('org.members.modal.resetPasswordPrompt', { username: member.username }))
  if (!pwd || pwd.length < 8) return
  resetTarget.value = member; resetNewPassword.value = pwd
  resetFeedback.value = ''; resetError.value = false
}

// openCreateAppForm 打开补建实例表单，默认 app_name 取「{显示名} 的实例」。
// version_id 需用户从组织 allowlist 中选择，与 CreateMemberPage 保持一致。
function openCreateAppForm(member: Member) {
  createAppTarget.value = member
  createAppResult.value = null
  createAppError.value = ''
  createAppForm.value = {
    app_name: `${member.display_name} 的实例`,
    version_id: '',
    channel_type: 'wechat',
  }
}

// onSubmitCreateApp 提交已有成员实例创建请求，并展示后端返回的新实例与 job。
// version_id 必填校验在按钮 disabled 条件中前置，此处二次兜底防止绕过。
async function onSubmitCreateApp() {
  if (!createAppTarget.value) return
  if (!createAppForm.value.version_id) {
    createAppError.value = t('org.members.createApp.selectVersionError')
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
    createAppError.value = err instanceof Error ? err.message : t('org.members.createApp.createError')
  }
}

// onConfirmReset 提交重置密码，并把结果反馈到页面内状态文本。
async function onConfirmReset() {
  if (!resetTarget.value) return
  resetFeedback.value = ''; resetError.value = false
  try {
    await resetMutation.mutateAsync({ userId: resetTarget.value.id, password: resetNewPassword.value })
    resetFeedback.value = t('org.members.modal.resetSuccess'); resetTarget.value = null
  } catch (err) {
    resetError.value = true
    resetFeedback.value = err instanceof Error ? err.message : t('org.members.modal.resetFailed')
  }
}
</script>
