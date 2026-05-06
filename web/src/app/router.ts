import { createRouter, createWebHistory } from 'vue-router'

import { getStoredAccessToken } from '@/api/client'
import AuthLayout from '@/layouts/AuthLayout.vue'
import DashboardLayout from '@/layouts/DashboardLayout.vue'
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
import { useAuthStore } from '@/stores/auth'

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
        { path: 'organizations', component: OrganizationsPage },
        { path: 'platform/dashboard', component: PlatformDashboardPage },
        { path: 'platform/organizations/:orgId/recharge', component: RechargePage },
        { path: 'members', component: MembersPage },
        { path: 'members/new', component: CreateMemberPage },
        { path: 'org/persona', component: PersonaPage },
        { path: 'audit-logs', component: AuditLogsPage },
        { path: 'runtime-nodes', component: RuntimeNodesPage },
        { path: 'runtime-nodes/:nodeId', component: RuntimeNodeDetailPage },
        { path: 'knowledge', component: OrgKnowledgePage },
        { path: 'usage', component: UsagePage },
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
  return true
})
