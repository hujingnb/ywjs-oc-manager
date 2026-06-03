<template>
  <n-layout has-sider style="height: 100vh">
    <n-layout-sider
      bordered
      :width="220"
      :collapsed-width="64"
      content-style="display: flex; flex-direction: column; height: 100%"
    >
      <!-- Logo -->
      <div class="brand-block">
        <div class="brand-mark">OC</div>
        <div class="logo-text">
          <strong>FlashAI</strong>
          <span>Manager</span>
        </div>
      </div>

      <!-- Nav -->
      <n-menu
        :value="activeKey"
        :options="menuOptions"
        :collapsed-width="64"
        :collapsed-icon-size="22"
        :indent="16"
        style="flex: 1"
        @update:value="onNav"
      />

      <!-- User footer -->
      <div class="sidebar-footer">
        <p v-if="auth.user" class="me-info">
          <strong>{{ auth.user.display_name }}</strong>
          <small>{{ auth.user.username }}</small>
        </p>
        <n-button
          v-if="auth.user"
          size="small"
          quaternary
          class="password-button"
          style="width: 100%; justify-content: flex-start"
          @click="openPasswordModal"
        >
          <template #icon><KeyRound :size="15" /></template>
          修改密码
        </n-button>
        <n-button
          v-if="auth.user"
          size="small"
          quaternary
          class="logout-button"
          style="width: 100%; justify-content: flex-start"
          @click="onLogout"
        >
          <template #icon><LogOut :size="15" /></template>
          退出
        </n-button>
      </div>
    </n-layout-sider>

    <n-layout>
      <n-layout-header
        bordered
        style="padding: 0 24px; display: flex; align-items: center; justify-content: space-between; min-height: 64px"
      >
        <div>
          <p class="eyebrow">{{ environmentLabel }}</p>
          <h1 style="margin: 0; font-size: 20px">控制台</h1>
        </div>
        <div class="topbar-actions">
          <n-tag type="success" size="small" :bordered="false">API 正常</n-tag>
          <!-- 使用手册入口：右上角文字按钮，点开右侧抽屉按当前角色展示对应手册 -->
          <n-button quaternary title="使用手册" @click="helpOpen = true">
            使用手册
          </n-button>
          <n-button quaternary circle @click="reload">
            <template #icon><RefreshCw :size="17" /></template>
          </n-button>
        </div>
      </n-layout-header>

      <n-layout-content content-style="height: calc(100vh - 64px); padding: 24px; display: flex; flex-direction: column; overflow: auto">
        <div class="dashboard-page-frame">
          <RouterView />
        </div>
      </n-layout-content>
    </n-layout>

    <n-modal
      :show="passwordModalOpen"
      preset="card"
      title="修改密码"
      class="password-modal"
      data-test="password-modal"
      :style="{ width: '420px', maxWidth: 'calc(100vw - 32px)' }"
      :mask-closable="!passwordChanging"
      :closable="!passwordChanging"
      @update:show="onPasswordModalShowUpdate"
    >
      <n-form data-test="password-form" :model="passwordForm" @submit.prevent="onChangePassword">
        <n-form-item label="当前密码">
          <n-input
            v-model:value="passwordForm.oldPassword"
            type="password"
            autocomplete="current-password"
            placeholder="请输入当前密码"
          />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input
            v-model:value="passwordForm.newPassword"
            type="password"
            autocomplete="new-password"
            placeholder="至少 8 位"
          />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input
            v-model:value="passwordForm.confirmPassword"
            type="password"
            autocomplete="new-password"
            placeholder="再次输入新密码"
          />
        </n-form-item>
        <n-alert v-if="passwordError" type="error" :bordered="false" style="margin-bottom: 12px">
          {{ passwordError }}
        </n-alert>
        <n-space justify="end">
          <n-button attr-type="button" :disabled="passwordChanging" @click="closePasswordModal">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="passwordChanging">确认修改</n-button>
        </n-space>
      </n-form>
    </n-modal>

    <!-- 使用手册抽屉：内容随当前登录角色切换，由 helpOpen 控制显隐。 -->
    <HelpDrawer v-model:show="helpOpen" :role="auth.user?.role" />
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert, NButton, NForm, NFormItem, NInput, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu, NModal,
  NSpace, NTag,
  type MenuOption,
} from 'naive-ui'
import {
  BarChart3, BookOpen, Bot, Boxes, Building2, CalendarClock, FileSearch,
  FolderOpen, Gauge, KeyRound, LayoutDashboard, ListChecks, LogOut, Radio, RefreshCw,
  ShieldCheck, Users, Wallet,
} from 'lucide-vue-next'

import HelpDrawer from '@/components/HelpDrawer.vue'
import { useAuthStore } from '@/stores/auth'
import { useMemberApp } from '@/composables/useMemberApp'

// DashboardLayout 负责已登录后台的导航外壳、环境标识和退出入口。
// 具体页面权限仍由路由和页面级查询控制，这里只隐藏不适合当前角色的导航项。
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

// helpOpen 控制右上角「使用手册」抽屉的显隐；抽屉内容由 HelpDrawer 按当前角色渲染。
const helpOpen = ref(false)
// 改密弹窗的密码字段只保存在组件内存中；打开和关闭都会清空，避免密码残留在布局状态里。
const passwordModalOpen = ref(false)
const passwordChanging = ref(false)
const passwordError = ref('')
const passwordForm = ref({ oldPassword: '', newPassword: '', confirmPassword: '' })

const environmentLabel = computed(() => {
  if (!auth.user) return '本地调试环境'
  return `本地调试环境 · ${auth.user.role}`
})

// 根据当前路由计算激活的菜单项 key（前缀匹配）
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return isOrgMember.value ? memberAppTabKey('overview') : '/'
  // org_member 的实例 tab 已拉平到左侧菜单，需要按子路由末段分别高亮。
  if (isOrgMember.value && p.startsWith('/apps')) {
    if (p === '/apps/empty') return memberAppTabKey('overview')
    const tab = p.split('/')[3] as MemberAppTab | undefined
    return tab && memberAppTabs.includes(tab) ? memberAppTabKey(tab) : memberAppTabKey('overview')
  }
  if (p.startsWith('/apps')) return memberAppPath.value
  const prefixes = [
    '/console',
    '/organizations',
    '/assistant-versions',
    '/members',
    '/knowledge',
    '/usage',
    '/balance',
    '/audit-logs',
    '/platform/permissions',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})

const isPlatformAdmin = computed(() => auth.isPlatformAdmin)
const isOrgMember = computed(() => auth.isOrgMember)
// isOrgAdmin 用于控制账户余额菜单项的可见性，仅组织管理员需要此入口。
const isOrgAdmin = computed(() => auth.isOrgAdmin)

const { appId: memberAppId, hasApp: memberHasApp } = useMemberApp()

// MemberAppTab 是组织成员左侧菜单可直达的实例业务分区；值必须与 /apps/:appId/:tab 子路由末段一致。
type MemberAppTab = 'overview' | 'kanban' | 'cron' | 'channels' | 'knowledge' | 'workspace'

// memberAppTabs 用于从当前路由末段反查成员菜单高亮项，避免所有 /apps 路径都落到同一个「实例」入口。
const memberAppTabs: readonly MemberAppTab[] = ['overview', 'kanban', 'cron', 'channels', 'knowledge', 'workspace']

// memberAppTabPath 根据成员唯一实例生成现有详情页路由；无实例时统一落到空状态页。
function memberAppTabPath(tab: MemberAppTab) {
  if (!isOrgMember.value) return '/apps'
  if (memberHasApp.value && memberAppId.value) return `/apps/${memberAppId.value}/${tab}`
  return '/apps/empty'
}

// memberAppTabKey 是菜单项 identity；无实例时不能复用 /apps/empty，
// 否则 Naive UI 会把多个入口视作同一个节点。
function memberAppTabKey(tab: MemberAppTab) {
  if (memberHasApp.value && memberAppId.value) return memberAppTabPath(tab)
  return `member-empty-${tab}`
}

// org_member 的总览目标：有实例指向唯一实例 overview，无实例指向空状态。
const memberAppPath = computed(() => memberAppTabPath('overview'))

// menuOptions 根据角色裁剪入口：普通成员不显示组织管理和审计，平台管理员仅显示控制台单一入口。
const menuOptions = computed<MenuOption[]>(() => {
  if (isOrgMember.value) {
    return [
      { key: memberAppTabKey('overview'), label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) },
      { key: memberAppTabKey('channels'), label: '渠道', icon: () => h(Radio, { size: 18 }) },
      { key: memberAppTabKey('workspace'), label: '工作目录', icon: () => h(FolderOpen, { size: 18 }) },
      { key: memberAppTabKey('knowledge'), label: '个人知识库', icon: () => h(BookOpen, { size: 18 }) },
      { key: '/knowledge', label: '企业知识库', icon: () => h(BookOpen, { size: 18 }) },
      { key: memberAppTabKey('kanban'), label: '任务', icon: () => h(ListChecks, { size: 18 }) },
      { key: memberAppTabKey('cron'), label: '定时任务', icon: () => h(CalendarClock, { size: 18 }) },
      { key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) },
    ]
  }
  // platform_admin 使用单一「控制台」入口，替代原来「总览+平台」两个菜单项。
  const items: MenuOption[] = isPlatformAdmin.value
    ? [{ key: '/console', label: '控制台', icon: () => h(Gauge, { size: 18 }) }]
    : [{ key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) }]
  if (isPlatformAdmin.value) {
    items.push({ key: '/organizations', label: '企业', icon: () => h(Building2, { size: 18 }) })
    items.push({ key: '/assistant-versions', label: '助手版本', icon: () => h(Boxes, { size: 18 }) })
  }
  // 成员/审计 是组织管理视角，普通成员不展示。
  if (!isOrgMember.value) {
    items.push({ key: '/members', label: '成员', icon: () => h(Users, { size: 18 }) })
  }
  items.push(
    { key: memberAppPath.value, label: '实例', icon: () => h(Bot, { size: 18 }) },
    { key: '/knowledge', label: '企业知识库', icon: () => h(BookOpen, { size: 18 }) },
  )
  // 账户余额仅对 org_admin 显示；org_member 和 platform_admin 无此入口。
  if (isOrgAdmin.value) {
    items.push({ key: '/balance', label: '账户余额', icon: () => h(Wallet, { size: 18 }) })
  }
  if (!isOrgMember.value) {
    items.push({ key: '/audit-logs', label: '审计', icon: () => h(FileSearch, { size: 18 }) })
  }
  if (isPlatformAdmin.value) {
    items.push({ key: '/platform/permissions', label: '权限说明', icon: () => h(ShieldCheck, { size: 18 }) })
  }
  // 用量统一落到所有管理员菜单末尾，与成员视角保持一致的收尾位置。
  items.push({ key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) })
  return items
})

// onNav 由 Naive Menu 传入 key；普通菜单 key 即路径，成员无实例的占位 key 统一跳空状态页。
function onNav(key: string) {
  router.push(key.startsWith('member-empty-') ? '/apps/empty' : key)
}

// onLogout 先清理登录态再回到登录页，避免旧 token 继续驱动后台查询。
async function onLogout() {
  await auth.logout()
  await router.replace('/login')
}

// resetPasswordForm 只重置内存中的表单字段和错误提示，不触碰 auth store，避免把密码写入共享状态。
function resetPasswordForm() {
  passwordForm.value = { oldPassword: '', newPassword: '', confirmPassword: '' }
  passwordError.value = ''
}

// openPasswordModal 每次打开前都清空旧输入，防止用户上一次未提交的密码仍显示在弹窗内。
function openPasswordModal() {
  resetPasswordForm()
  passwordModalOpen.value = true
}

// closePasswordModal 在非提交中关闭并清空密码；提交期间禁止关闭，避免请求中途被打断后状态不一致。
function closePasswordModal() {
  if (passwordChanging.value) return
  passwordModalOpen.value = false
  resetPasswordForm()
}

// onPasswordModalShowUpdate 统一处理遮罩和右上角关闭事件，保证关闭路径都会清理密码字段。
function onPasswordModalShowUpdate(show: boolean) {
  if (show) {
    passwordModalOpen.value = true
    return
  }
  closePasswordModal()
}

// validatePasswordForm 做提交前的本地校验，减少无效请求并给用户明确的中文错误原因。
function validatePasswordForm() {
  const { oldPassword, newPassword, confirmPassword } = passwordForm.value
  if (!oldPassword || !newPassword || !confirmPassword) return '请填写当前密码、新密码和确认新密码'
  if (newPassword.length < 8) return '新密码至少 8 位'
  if (newPassword === oldPassword) return '新密码不能与当前密码相同'
  if (newPassword !== confirmPassword) return '两次输入的新密码不一致'
  return null
}

// onChangePassword 只调用 auth store 暴露的改密动作；成功后 store 会清理登录态，这里负责跳回登录页。
async function onChangePassword() {
  const validationError = validatePasswordForm()
  if (validationError) {
    passwordError.value = validationError
    return
  }
  const { oldPassword, newPassword } = passwordForm.value
  passwordChanging.value = true
  passwordError.value = ''
  try {
    await auth.changePassword(oldPassword, newPassword)
    passwordModalOpen.value = false
    resetPasswordForm()
    await router.replace('/login')
  } catch (err) {
    passwordError.value = err instanceof Error && err.message ? err.message : '修改密码失败'
  } finally {
    passwordChanging.value = false
  }
}

// reload 用于调试环境快速刷新当前后台状态。
function reload() {
  window.location.reload()
}
</script>

<style scoped>
.brand-block {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border-bottom: 1px solid var(--color-divider);
  min-height: 64px;
}

.brand-mark {
  width: 36px;
  height: 36px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  background: var(--color-brand);
  box-shadow: none;
  color: var(--color-on-brand);
  font-size: 13px;
  font-weight: 800;
  flex-shrink: 0;
}

.logo-text strong { display: block; font-size: 15px; color: var(--color-text-primary); }
.logo-text span { display: block; font-size: 11px; color: var(--color-text-secondary); }

.sidebar-footer {
  padding: 12px 14px 16px;
  border-top: 1px solid var(--color-divider);
  background: var(--color-surface);
}

.logout-button,
.password-button {
  color: var(--color-text-secondary);
}

.logout-button:hover,
.password-button:hover {
  color: var(--color-brand-text);
}

.dashboard-page-frame {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
}

.dashboard-page-frame :deep(> *) {
  min-height: 0;
  flex: 1;
}
</style>
