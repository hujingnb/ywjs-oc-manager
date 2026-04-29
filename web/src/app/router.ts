import { createRouter, createWebHistory } from 'vue-router'

import DashboardLayout from '@/layouts/DashboardLayout.vue'
import AuthLayout from '@/layouts/AuthLayout.vue'
import DashboardHome from '@/pages/dashboard/DashboardHome.vue'
import LoginPage from '@/pages/login/LoginPage.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      component: AuthLayout,
      children: [{ path: '', component: LoginPage }],
    },
    {
      path: '/',
      component: DashboardLayout,
      children: [{ path: '', component: DashboardHome }],
    },
  ],
})
