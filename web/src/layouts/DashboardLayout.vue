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
          {{ t('layout.password.changePassword') }}
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
          {{ t('layout.sidebar.logout') }}
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
          <h1 style="margin: 0; font-size: 20px">{{ t('layout.header.console') }}</h1>
        </div>
        <!-- 视角切换器:仅 org_admin 可见,在「企业管理」与「我的实例」两视角间切换并导航。 -->
        <div v-if="isOrgAdmin" class="perspective-switch">
          <n-button
            size="small"
            :type="perspective === 'manage' ? 'primary' : 'default'"
            :aria-pressed="perspective === 'manage'"
            @click="onSwitchPerspective('manage')"
          >
            {{ t('layout.perspective.manage') }}
          </n-button>
          <n-button
            size="small"
            :type="perspective === 'instance' ? 'primary' : 'default'"
            :aria-pressed="perspective === 'instance'"
            @click="onSwitchPerspective('instance')"
          >
            {{ t('layout.perspective.instance') }}
          </n-button>
        </div>
        <div class="topbar-actions">
          <LocaleSwitcher :persist="true" />
          <n-tag type="success" size="small" :bordered="false">{{ t('layout.header.apiStatus') }}</n-tag>
          <!-- 使用手册入口：右上角文字按钮，点开右侧抽屉按当前角色展示对应手册 -->
          <n-button quaternary :title="t('layout.header.helpManual')" @click="helpOpen = true">
            {{ t('layout.header.helpManual') }}
          </n-button>
          <n-button quaternary circle @click="reload">
            <template #icon><RefreshCw :size="17" /></template>
          </n-button>
        </div>
      </n-layout-header>

      <n-layout-content content-style="height: calc(100vh - 64px); padding: 24px; display: flex; flex-direction: column; overflow: auto">
        <div class="dashboard-page-frame">
          <RouterView v-slot="{ Component }">
            <keep-alive :include="['CustomSkillTicketsPage', 'OrgSkillsPage']">
              <component :is="Component" />
            </keep-alive>
          </RouterView>
        </div>
      </n-layout-content>
    </n-layout>

    <n-modal
      :show="passwordModalOpen"
      preset="card"
      :title="t('layout.password.changePassword')"
      class="password-modal"
      data-test="password-modal"
      :style="{ width: '420px', maxWidth: 'calc(100vw - 32px)' }"
      :mask-closable="!passwordChanging"
      :closable="!passwordChanging"
      @update:show="onPasswordModalShowUpdate"
    >
      <n-form data-test="password-form" :model="passwordForm" @submit.prevent="onChangePassword">
        <n-form-item :label="t('layout.password.currentPassword')">
          <n-input
            v-model:value="passwordForm.oldPassword"
            type="password"
            autocomplete="current-password"
            :placeholder="t('layout.password.currentPasswordPlaceholder')"
          />
        </n-form-item>
        <n-form-item :label="t('layout.password.newPassword')">
          <n-input
            v-model:value="passwordForm.newPassword"
            type="password"
            autocomplete="new-password"
            :placeholder="t('layout.password.newPasswordPlaceholder')"
          />
        </n-form-item>
        <n-form-item :label="t('layout.password.confirmPassword')">
          <n-input
            v-model:value="passwordForm.confirmPassword"
            type="password"
            autocomplete="new-password"
            :placeholder="t('layout.password.confirmPasswordPlaceholder')"
          />
        </n-form-item>
        <n-alert v-if="passwordError" type="error" :bordered="false" style="margin-bottom: 12px">
          {{ passwordError }}
        </n-alert>
        <n-space justify="end">
          <n-button attr-type="button" :disabled="passwordChanging" @click="closePasswordModal">{{ t('common.actions.cancel') }}</n-button>
          <n-button type="primary" attr-type="submit" :loading="passwordChanging" :disabled="passwordChanging">{{ t('layout.password.submitButton') }}</n-button>
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
import { useI18n } from 'vue-i18n'
import {
  NAlert, NBadge, NButton, NForm, NFormItem, NInput, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu, NModal,
  NSpace, NTag,
  type MenuOption,
} from 'naive-ui'
import {
  BarChart3, BookOpen, Bot, Boxes, Building2, CalendarClock, FileSearch,
  FolderOpen, Gauge, Globe, KeyRound, LayoutDashboard, ListChecks, LogOut, MessageSquare, Package, Puzzle, Radio, RefreshCw,
  ShieldCheck, Users, Wallet, Wrench,
} from 'lucide-vue-next'

import HelpDrawer from '@/components/HelpDrawer.vue'
import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import { useAuthStore } from '@/stores/auth'
import { useOwnApp } from '@/composables/useOwnApp'
import { useAdminPerspective, type AdminPerspective } from '@/composables/useAdminPerspective'
import { useSkillTicketBadgeQuery } from '@/api/hooks/useSkillTickets'

// DashboardLayout 负责已登录后台的导航外壳、环境标识和退出入口。
// 具体页面权限仍由路由和页面级查询控制，这里只隐藏不适合当前角色的导航项。
const { t } = useI18n()
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

// environmentLabel 根据是否登录以及当前语言返回环境标识文案，响应语言切换。
const environmentLabel = computed(() => {
  if (!auth.user) return t('layout.header.envLabel')
  return t('layout.header.envLabelWithRole', { role: auth.user.role })
})

// 根据当前路由计算激活的菜单项 key（前缀匹配）
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return inOwnInstanceView.value ? memberAppTabKey('overview') : '/'
  // org_member / org_admin(instance 视角) 的实例 tab 已拉平到左侧菜单，需要按子路由末段分别高亮。
  if (inOwnInstanceView.value && p.startsWith('/apps')) {
    if (p === '/apps/empty') return memberAppTabKey('overview')
    const tab = p.split('/')[3] as MemberAppTab | undefined
    return tab && memberAppTabs.includes(tab) ? memberAppTabKey(tab) : memberAppTabKey('overview')
  }
  if (p.startsWith('/apps')) return memberAppPath.value
  const prefixes = [
    '/console',
    '/organizations',
    '/assistant-versions',
    '/platform/industry-knowledge',
    '/platform/skills',
    '/platform/custom-skills',
    '/platform/web-publish-config',
    '/members',
    '/published-sites',
    '/knowledge',
    // /skills 成员技能页顶级路由；需早于更短 prefix 匹配，放在 /knowledge 后面即可。
    '/skills',
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

// ticketBadge 提供「定制技能」菜单待处理工单角标数；该端点仅平台管理员有权，
// 角标也仅在平台管理员菜单分支渲染，非平台管理员视角下不会读取此值。
const ticketBadge = useSkillTicketBadgeQuery(isPlatformAdmin)
// pendingTicketCount 是待处理工单数；查询未就绪时为 0（不显示角标）。
const pendingTicketCount = computed(() => ticketBadge.data.value ?? 0)

const { appId: memberAppId, hasApp: memberHasApp } = useOwnApp()

// 视角状态:仅 org_admin 消费,决定渲染企业管理菜单还是我的实例菜单。
const { perspective, setPerspective, resetPerspective } = useAdminPerspective()

// inOwnInstanceView:当前是否处于「以自有实例为中心」的视角。
// org_member 恒为 true;org_admin 仅在 instance 视角为 true;platform_admin 恒为 false。
const inOwnInstanceView = computed(() => isOrgMember.value || (isOrgAdmin.value && perspective.value === 'instance'))

// MemberAppTab 是组织成员左侧菜单可直达的实例业务分区；值必须与 /apps/:appId/:tab 子路由末段一致。
type MemberAppTab = 'overview' | 'kanban' | 'cron' | 'channels' | 'knowledge' | 'workspace' | 'conversations'

// memberAppTabs 用于从当前路由末段反查成员菜单高亮项，避免所有 /apps 路径都落到同一个「实例」入口。
const memberAppTabs: readonly MemberAppTab[] = ['overview', 'kanban', 'cron', 'channels', 'knowledge', 'workspace', 'conversations']

// memberAppTabPath 根据成员唯一实例生成现有详情页路由；无实例时统一落到空状态页。
function memberAppTabPath(tab: MemberAppTab) {
  if (!inOwnInstanceView.value) return '/apps'
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

// menuOptions 根据角色裁剪入口，并随语言切换自动更新 label。
// 使用 computed 保证切换语言后菜单文案立即响应。
const menuOptions = computed<MenuOption[]>(() => {
  if (inOwnInstanceView.value) {
    return [
      // conversations：实例 hermes 会话对话管理入口，置于成员菜单首位（最常用）。
      { key: memberAppTabKey('conversations'), label: t('layout.nav.conversations'), icon: () => h(MessageSquare, { size: 18 }) },
      { key: memberAppTabKey('overview'), label: t('layout.nav.overview'), icon: () => h(LayoutDashboard, { size: 18 }) },
      { key: memberAppTabKey('channels'), label: t('layout.nav.channels'), icon: () => h(Radio, { size: 18 }) },
      { key: memberAppTabKey('workspace'), label: t('layout.nav.workspace'), icon: () => h(FolderOpen, { size: 18 }) },
      { key: memberAppTabKey('knowledge'), label: t('layout.nav.personalKnowledge'), icon: () => h(BookOpen, { size: 18 }) },
      { key: '/knowledge', label: t('layout.nav.orgKnowledge'), icon: () => h(BookOpen, { size: 18 }) },
      // 技能：成员顶级技能页，key 为 /skills，与路由保持一致。
      { key: '/skills', label: t('layout.nav.skills'), icon: () => h(Puzzle, { size: 18 }) },
      { key: memberAppTabKey('kanban'), label: t('layout.nav.tasks'), icon: () => h(ListChecks, { size: 18 }) },
      { key: memberAppTabKey('cron'), label: t('layout.nav.cron'), icon: () => h(CalendarClock, { size: 18 }) },
      { key: '/usage', label: t('layout.nav.usage'), icon: () => h(BarChart3, { size: 18 }) },
    ]
  }
  // platform_admin 使用单一「控制台」入口，替代原来「总览+平台」两个菜单项。
  const items: MenuOption[] = isPlatformAdmin.value
    ? [{ key: '/console', label: t('layout.nav.console'), icon: () => h(Gauge, { size: 18 }) }]
    : [{ key: '/', label: t('layout.nav.overview'), icon: () => h(LayoutDashboard, { size: 18 }) }]
  if (isPlatformAdmin.value) {
    items.push({ key: '/organizations', label: t('layout.nav.organizations'), icon: () => h(Building2, { size: 18 }) })
    items.push({ key: '/assistant-versions', label: t('layout.nav.assistantVersions'), icon: () => h(Boxes, { size: 18 }) })
    items.push({ key: '/platform/industry-knowledge', label: t('layout.nav.industryKnowledge'), icon: () => h(BookOpen, { size: 18 }) })
    // 平台库管理入口：仅平台管理员可见，用于上传/删除 skill tar 包。
    items.push({ key: '/platform/skills', label: t('layout.nav.platformSkills'), icon: () => h(Package, { size: 18 }) })
    // 定制技能工单入口：label 用 render 函数，在文案后挂 n-badge 显示待处理工单数（>0 时才显示）。
    items.push({
      key: '/platform/custom-skills',
      label: () =>
        h('span', { style: 'display: inline-flex; align-items: center; gap: 8px' }, [
          t('layout.nav.customSkills'),
          pendingTicketCount.value > 0
            ? h(NBadge, { value: pendingTicketCount.value, type: 'error' })
            : null,
        ]),
      icon: () => h(Wrench, { size: 18 }),
    })
    // web-publish 开通配置入口：平台管理员专属，用于开通企业站点发布能力。
    items.push({ key: '/platform/web-publish-config', label: t('layout.nav.webPublishConfig'), icon: () => h(Globe, { size: 18 }) })
  }
  // 成员/审计 是组织管理视角，普通成员不展示。
  if (!isOrgMember.value) {
    items.push({ key: '/members', label: t('layout.nav.members'), icon: () => h(Users, { size: 18 }) })
    // 已发布站点入口：与 members/audit 同属组织管理视角，org_admin 与 platform_admin 可见，
    // 路由 allowedRoles 为 ORG_ADMIN_ABOVE，普通成员不展示。
    items.push({ key: '/published-sites', label: t('layout.nav.publishedSites'), icon: () => h(Globe, { size: 18 }) })
  }
  items.push(
    { key: memberAppPath.value, label: t('layout.nav.instance'), icon: () => h(Bot, { size: 18 }) },
    { key: '/knowledge', label: t('layout.nav.orgKnowledge'), icon: () => h(BookOpen, { size: 18 }) },
  )
  // 账户余额仅对 org_admin 显示；org_member 和 platform_admin 无此入口。
  if (isOrgAdmin.value) {
    items.push({ key: '/balance', label: t('layout.nav.balance'), icon: () => h(Wallet, { size: 18 }) })
  }
  if (!isOrgMember.value) {
    items.push({ key: '/audit-logs', label: t('layout.nav.audit'), icon: () => h(FileSearch, { size: 18 }) })
  }
  if (isPlatformAdmin.value) {
    items.push({ key: '/platform/permissions', label: t('layout.nav.permissions'), icon: () => h(ShieldCheck, { size: 18 }) })
  }
  // 用量统一落到所有管理员菜单末尾，与成员视角保持一致的收尾位置。
  items.push({ key: '/usage', label: t('layout.nav.usage'), icon: () => h(BarChart3, { size: 18 }) })
  return items
})

// onNav 由 Naive Menu 传入 key；普通菜单 key 即路径，成员无实例的占位 key 统一跳空状态页。
function onNav(key: string) {
  router.push(key.startsWith('member-empty-') ? '/apps/empty' : key)
}

// onSwitchPerspective:切换 org_admin 视角并导航到目标视角的落地页。
// 切到我的实例:有自有实例进总览,无实例进空状态页(由 AppEmptyPage 给自助建实例入口)。
// 切回企业管理:回管理总览根路由。
function onSwitchPerspective(view: AdminPerspective) {
  // 点击当前已激活视角时直接返回,避免重复持久化与重复导航(NavigationDuplicated)。
  if (perspective.value === view) return
  setPerspective(view)
  if (view === 'instance') {
    router.push(memberHasApp.value && memberAppId.value ? `/apps/${memberAppId.value}/overview` : '/apps/empty')
    return
  }
  router.push('/')
}

// onLogout 先清理登录态再回到登录页，避免旧 token 继续驱动后台查询。
async function onLogout() {
  // 退出前清除视角持久化,避免下个登录用户沿用上一个视角
  resetPerspective()
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

// validatePasswordForm 做提交前的本地校验，减少无效请求并给用户明确的错误原因。
function validatePasswordForm() {
  const { oldPassword, newPassword, confirmPassword } = passwordForm.value
  if (!oldPassword || !newPassword || !confirmPassword) return t('layout.password.errAllRequired')
  if (newPassword.length < 8) return t('layout.password.errMinLength')
  if (newPassword === oldPassword) return t('layout.password.errSameAsOld')
  if (newPassword !== confirmPassword) return t('layout.password.errMismatch')
  return null
}

// onChangePassword 只调用 auth store 暴露的改密动作；成功后 store 会清理登录态，这里负责跳回登录页。
async function onChangePassword() {
  if (passwordChanging.value) return
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
    passwordError.value = err instanceof Error && err.message ? err.message : t('layout.password.errChangeFailed')
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
  /* min-height: 0 让本帧作为 n-layout-content(flex column) 的子项可收缩到分配高度以下，
     而非被内部内容(如对话页)撑大。缺它时对话页 .app-detail-root--fill 的 minmax(0,1fr) 行会
     退化为 max-content，使消息区+输入栏整体溢出、触发内容区整页滚动并把输入栏顶出视口；
     补上后剩余高度精确传导，msg-list 自身滚动、composer 钉在视口底部。
     普通(非对话)页内容仍 overflow:visible 溢出、照常由 n-layout-content 外层滚动，行为不变。 */
  min-height: 0;
  flex: 1;
  flex-direction: column;
}

.dashboard-page-frame :deep(> *) {
  min-height: 0;
  flex: 1;
}

/* 视角切换器:两个分段按钮紧贴,放在顶栏标题与右侧操作之间。 */
.perspective-switch {
  display: flex;
  gap: 4px;
}
</style>
