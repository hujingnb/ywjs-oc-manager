import { createRouter, createWebHistory } from 'vue-router'

import { getStoredAccessToken } from '@/api/client'
import AuthLayout from '@/layouts/AuthLayout.vue'
import DashboardLayout from '@/layouts/DashboardLayout.vue'
import AuditLogsPage from '@/pages/audit/AuditLogsPage.vue'
import DashboardHome from '@/pages/dashboard/DashboardHome.vue'
import LoginPage from '@/pages/login/LoginPage.vue'
import MembersPage from '@/pages/org/MembersPage.vue'
import OrganizationsPage from '@/pages/platform/OrganizationsPage.vue'
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
        { path: '', component: DashboardHome },
        { path: 'organizations', component: OrganizationsPage },
        { path: 'members', component: MembersPage },
        { path: 'audit-logs', component: AuditLogsPage },
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
