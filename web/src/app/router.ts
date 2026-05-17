// router.ts 定义前端路由表、角色访问边界和登录重定向行为。
// 路由守卫只做轻量会话校验，具体资源权限仍由后端和页面 helper 共同约束。
import { createRouter, createWebHistory } from 'vue-router'

import { getStoredAccessToken } from '@/api/client'
import AuthLayout from '@/layouts/AuthLayout.vue'
import DashboardLayout from '@/layouts/DashboardLayout.vue'
import AppAuditTab from '@/pages/apps/AppAuditTab.vue'
import AppChannelsTab from '@/pages/apps/AppChannelsTab.vue'
import AppDetailPage from '@/pages/apps/AppDetailPage.vue'
import AppKnowledgeTab from '@/pages/apps/AppKnowledgeTab.vue'
import AppOverviewTab from '@/pages/apps/AppOverviewTab.vue'
import AppRuntimeTab from '@/pages/apps/AppRuntimeTab.vue'
import AppWorkspaceTab from '@/pages/apps/AppWorkspaceTab.vue'
import AppsPage from '@/pages/apps/AppsPage.vue'
import AuditLogsPage from '@/pages/audit/AuditLogsPage.vue'
import DashboardHome from '@/pages/dashboard/DashboardHome.vue'
import RoleAwareHome from '@/pages/dashboard/RoleAwareHome.vue'
import LoginPage from '@/pages/login/LoginPage.vue'
import CreateMemberPage from '@/pages/org/CreateMemberPage.vue'
import MembersPage from '@/pages/org/MembersPage.vue'
import PersonaPage from '@/pages/org/PersonaPage.vue'
import OrganizationsPage from '@/pages/platform/OrganizationsPage.vue'
import PlatformDashboardPage from '@/pages/platform/PlatformDashboardPage.vue'
import RechargePage from '@/pages/platform/RechargePage.vue'
import OrgKnowledgePage from '@/pages/knowledge/OrgKnowledgePage.vue'
import RuntimeNodeDetailPage from '@/pages/runtime-nodes/RuntimeNodeDetailPage.vue'
import RuntimeNodesPage from '@/pages/runtime-nodes/RuntimeNodesPage.vue'
import UsagePage from '@/pages/usage/UsagePage.vue'
import OrgBalancePage from '@/pages/org/OrgBalancePage.vue'
import { useAuthStore } from '@/stores/auth'

// allowedRoles 表示可访问该路由的角色集合；undefined 表示对所有已登录用户开放。
// 平台专属：仅 platform_admin；组织管理视角（成员/审计）：禁止 org_member。
const PLATFORM_ONLY = ['platform_admin'] as const
const ORG_ADMIN_ABOVE = ['platform_admin', 'org_admin'] as const
const ORG_ADMIN_ONLY = ['org_admin'] as const

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      component: AuthLayout,
      meta: { public: true },
      children: [{ path: '', component: LoginPage }],
    },
    {
      path: '/',
      component: DashboardLayout,
      children: [
        { path: '', component: RoleAwareHome },
        { path: 'dashboard', component: DashboardHome },
        { path: 'organizations', component: OrganizationsPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/dashboard', component: PlatformDashboardPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/organizations/:orgId/recharge', component: RechargePage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'members', component: MembersPage, meta: { allowedRoles: ORG_ADMIN_ABOVE } },
        { path: 'members/new', component: CreateMemberPage, meta: { allowedRoles: ORG_ADMIN_ONLY } },
        { path: 'org/persona', component: PersonaPage },
        { path: 'audit-logs', component: AuditLogsPage, meta: { allowedRoles: ORG_ADMIN_ABOVE } },
        { path: 'runtime-nodes', component: RuntimeNodesPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'runtime-nodes/:nodeId', component: RuntimeNodeDetailPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'knowledge', component: OrgKnowledgePage },
        { path: 'usage', component: UsagePage },
        { path: 'balance', component: OrgBalancePage, meta: { allowedRoles: ORG_ADMIN_ONLY } },
        { path: 'apps', component: AppsPage },
        {
          path: 'apps/:appId',
          component: AppDetailPage,
          children: [
            { path: '', redirect: (to) => ({ path: `/apps/${to.params.appId}/overview` }) },
            { path: 'overview', component: AppOverviewTab, props: true },
            { path: 'runtime', component: AppRuntimeTab, props: true },
            { path: 'channels', component: AppChannelsTab, props: true },
            { path: 'knowledge', component: AppKnowledgeTab, props: true },
            { path: 'workspace', component: AppWorkspaceTab, props: true },
            { path: 'audit', component: AppAuditTab, props: true },
          ],
        },
      ],
    },
  ],
})

// 路由守卫：未登录时强制跳转登录页。
// 这里使用同步的 access token 判定，避免阻塞首屏渲染；
// 用户信息的最终校验由 fetchCurrentUser 在 dashboard 内异步完成。
router.beforeEach(async (to) => {
  if (to.meta.public) {
    return true
  }
  if (!getStoredAccessToken()) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  const auth = useAuthStore()
  if (!auth.user) {
    try {
      await auth.fetchCurrentUser()
    } catch {
      return { path: '/login', query: { redirect: to.fullPath } }
    }
  }
  // 角色守卫：命中 allowedRoles 限制时把用户兜回首页 RoleAwareHome，
  // 由 RoleAwareHome 根据角色展示对应入口，避免出现"被卡死"。
  const allowed = to.meta.allowedRoles as readonly string[] | undefined
  const role = auth.user?.role
  if (allowed && role && !allowed.includes(role)) {
    return { path: '/' }
  }
  return true
})
