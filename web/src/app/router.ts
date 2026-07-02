// router.ts 定义前端路由表、角色访问边界和登录重定向行为。
// 路由守卫只做轻量会话校验，具体资源权限仍由后端和页面 helper 共同约束。
import { createRouter, createWebHistory } from 'vue-router'

import { getStoredAccessToken } from '@/api/client'
import DashboardLayout from '@/layouts/DashboardLayout.vue'
import AppAuditTab from '@/pages/apps/AppAuditTab.vue'
import AppChannelsTab from '@/pages/apps/AppChannelsTab.vue'
import AppCronTab from '@/pages/apps/AppCronTab.vue'
import AppKanbanTab from '@/pages/apps/AppKanbanTab.vue'
import AppDetailPage from '@/pages/apps/AppDetailPage.vue'
import AppKnowledgeTab from '@/pages/apps/AppKnowledgeTab.vue'
import AppSkillsTab from '@/pages/apps/AppSkillsTab.vue'
import AppOverviewTab from '@/pages/apps/AppOverviewTab.vue'
import AppRuntimeTab from '@/pages/apps/AppRuntimeTab.vue'
import AppWorkspaceTab from '@/pages/apps/AppWorkspaceTab.vue'
import AppEmptyPage from '@/pages/apps/AppEmptyPage.vue'
import AppsPage from '@/pages/apps/AppsPage.vue'
import AuditLogsPage from '@/pages/audit/AuditLogsPage.vue'
import RoleAwareHome from '@/pages/dashboard/RoleAwareHome.vue'
import LoginHost from '@/pages/login/LoginHost.vue'
import CreateMemberPage from '@/pages/org/CreateMemberPage.vue'
import MembersPage from '@/pages/org/MembersPage.vue'
import PublishedSitesPage from '@/pages/org/PublishedSitesPage.vue'
import AssistantVersionsPage from '@/pages/platform/AssistantVersionsPage.vue'
import IndustryKnowledgePage from '@/pages/platform/IndustryKnowledgePage.vue'
import PlatformSkillsPage from '@/pages/platform/PlatformSkillsPage.vue'
import CustomSkillTicketsPage from '@/pages/platform/CustomSkillTicketsPage.vue'
import OrganizationsPage from '@/pages/platform/OrganizationsPage.vue'
import WebPublishConfigPage from '@/pages/platform/WebPublishConfigPage.vue'
import ConsolePage from '@/pages/platform/ConsolePage.vue'
import RechargePage from '@/pages/platform/RechargePage.vue'
import OrgKnowledgePage from '@/pages/knowledge/OrgKnowledgePage.vue'
import OrgSkillsPage from '@/pages/skills/OrgSkillsPage.vue'
import UsagePage from '@/pages/usage/UsagePage.vue'
import OrgConsolePage from '@/pages/org/OrgConsolePage.vue'
import OrgBalancePage from '@/pages/org/OrgBalancePage.vue'
import PermissionsPage from '@/pages/platform/PermissionsPage.vue'
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
      component: LoginHost,
      meta: { public: true },
    },
    {
      path: '/',
      component: DashboardLayout,
      children: [
        { path: '', component: RoleAwareHome },
        { path: 'console', component: ConsolePage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'org-console', component: OrgConsolePage, meta: { allowedRoles: ORG_ADMIN_ONLY } },
        { path: 'platform/dashboard', redirect: '/console' },
        { path: 'dashboard', redirect: '/console' },
        { path: 'organizations', component: OrganizationsPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'assistant-versions', component: AssistantVersionsPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/industry-knowledge', component: IndustryKnowledgePage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/skills', component: PlatformSkillsPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/custom-skills', component: CustomSkillTicketsPage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/organizations/:orgId/recharge', component: RechargePage, meta: { allowedRoles: PLATFORM_ONLY } },
        { path: 'platform/permissions', component: PermissionsPage, meta: { allowedRoles: PLATFORM_ONLY } },
        // platform/web-publish-config：web-publish 配置页。平台管理员可开通/停用/跨企业配置；
        // 企业管理员可配置「自己企业且平台已开通」的 web-publish（页面按角色自适应，开通/停用仍仅平台管理员）。
        { path: 'platform/web-publish-config', component: WebPublishConfigPage, meta: { allowedRoles: ORG_ADMIN_ABOVE } },
        { path: 'members', component: MembersPage, meta: { allowedRoles: ORG_ADMIN_ABOVE } },
        // published-sites：企业已发布站点列表 + 证书状态面板；平台管理员可跨企业查看并重试证书。
        { path: 'published-sites', component: PublishedSitesPage, meta: { allowedRoles: ORG_ADMIN_ABOVE } },
        { path: 'members/new', component: CreateMemberPage, meta: { allowedRoles: ORG_ADMIN_ONLY } },
        { path: 'audit-logs', component: AuditLogsPage, meta: { allowedRoles: ORG_ADMIN_ABOVE } },
        { path: 'knowledge', component: OrgKnowledgePage },
        // skills：成员「技能」顶级页，无 allowedRoles，所有已登录用户（尤其是 org_member）均可访问。
        { path: 'skills', component: OrgSkillsPage },
        { path: 'skill-tickets/:id', name: 'ticket-detail', component: () => import('@/pages/skill-tickets/TicketDetailPage.vue') },
        { path: 'usage', component: UsagePage },
        { path: 'balance', component: OrgBalancePage, meta: { allowedRoles: ORG_ADMIN_ONLY } },
        {
          path: 'apps',
          component: AppsPage,
          beforeEnter: async (to, from, next) => {
            const auth = useAuthStore()
            if (auth.user?.role !== 'org_member') return next()
            // org_member 不进列表页，重定向到详情或空状态
            const orgId = auth.user.org_id
            if (!orgId) return next('/apps/empty')
            try {
              const { apiRequest } = await import('@/api/client')
              const response = await apiRequest<{ apps?: { id: string; owner_user_id: string }[] }>(
                `/api/v1/organizations/${orgId}/apps`, { query: { limit: 10 } },
              )
              const myApp = response.apps?.find(a => a.owner_user_id === auth.user!.id)
              return next(myApp ? `/apps/${myApp.id}/overview` : '/apps/empty')
            } catch {
              return next('/apps/empty')
            }
          },
        },
        { path: 'apps/empty', component: AppEmptyPage },
        {
          path: 'apps/:appId',
          component: AppDetailPage,
          children: [
            { path: '', redirect: (to) => ({ path: `/apps/${to.params.appId}/overview` }) },
            { path: 'overview', component: AppOverviewTab, props: true },
            { path: 'kanban', component: AppKanbanTab, props: true },
            { path: 'cron', component: AppCronTab, props: true },
            { path: 'runtime', component: AppRuntimeTab, props: true, meta: { allowedRoles: PLATFORM_ONLY } },
            { path: 'channels', component: AppChannelsTab, props: true },
            { path: 'knowledge', component: AppKnowledgeTab, props: true },
            // skills tab：管理员在实例详情页管理该实例的技能，props: true 将 appId 从路由参数传入。
            { path: 'skills', component: AppSkillsTab, props: true },
            { path: 'workspace', component: AppWorkspaceTab, props: true },
            { path: 'audit', component: AppAuditTab, props: true },
            // conversations tab：实例 hermes 会话管理（流式续聊 + 会话列表）。
            { path: 'conversations', component: () => import('@/pages/apps/AppConversationsTab.vue'), props: true },
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
