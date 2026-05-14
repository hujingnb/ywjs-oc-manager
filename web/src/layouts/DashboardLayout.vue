<template>
  <n-layout has-sider style="min-height: 100vh">
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
          <strong>Hermes</strong>
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
          style="width: 100%; justify-content: flex-start; color: #8A94C6"
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
          <n-tag type="warning" size="small" :bordered="false">Ollama 待配置模型</n-tag>
          <n-button quaternary circle @click="reload">
            <template #icon><RefreshCw :size="17" /></template>
          </n-button>
        </div>
      </n-layout-header>

      <n-layout-content content-style="min-height: calc(100vh - 64px); padding: 24px; display: flex; flex-direction: column">
        <div class="dashboard-page-frame">
          <RouterView />
        </div>
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu, NTag,
  type MenuOption,
} from 'naive-ui'
import {
  BarChart3, BookOpen, Bot, Building2, FileSearch, Gauge,
  LayoutDashboard, LogOut, RefreshCw, Server, Users,
} from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'

// DashboardLayout 负责已登录后台的导航外壳、环境标识和退出入口。
// 具体页面权限仍由路由和页面级查询控制，这里只隐藏不适合当前角色的导航项。
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const environmentLabel = computed(() => {
  if (!auth.user) return '本地调试环境'
  return `本地调试环境 · ${auth.user.role}`
})

// 根据当前路由计算激活的菜单项 key（前缀匹配）
const activeKey = computed(() => {
  const p = route.path
  if (p === '/') return '/'
  const prefixes = [
    '/platform/dashboard',
    '/organizations',
    '/members',
    '/apps',
    '/knowledge',
    '/usage',
    '/audit-logs',
    '/runtime-nodes',
    '/org/persona',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})

const isPlatformAdmin = computed(() => auth.isPlatformAdmin)
const isOrgMember = computed(() => auth.isOrgMember)

// menuOptions 根据角色裁剪入口：普通成员不显示组织管理和审计，平台管理员额外显示平台能力。
const menuOptions = computed<MenuOption[]>(() => {
  const items: MenuOption[] = [
    { key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) },
  ]
  if (isPlatformAdmin.value) {
    items.push({ key: '/platform/dashboard', label: '平台', icon: () => h(Gauge, { size: 18 }) })
    items.push({ key: '/organizations', label: '组织', icon: () => h(Building2, { size: 18 }) })
  }
  // 成员/审计 是组织管理视角，普通成员不展示。
  if (!isOrgMember.value) {
    items.push({ key: '/members', label: '成员', icon: () => h(Users, { size: 18 }) })
  }
  items.push(
    { key: '/apps', label: '实例', icon: () => h(Bot, { size: 18 }) },
    { key: '/knowledge', label: '知识库', icon: () => h(BookOpen, { size: 18 }) },
    { key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) },
  )
  if (!isOrgMember.value) {
    items.push({ key: '/audit-logs', label: '审计', icon: () => h(FileSearch, { size: 18 }) })
  }
  if (isPlatformAdmin.value) {
    items.push({ key: '/runtime-nodes', label: '运行节点', icon: () => h(Server, { size: 18 }) })
  }
  return items
})

// onNav 由 Naive Menu 传入 key，key 与路由路径保持一致。
function onNav(key: string) {
  router.push(key)
}

// onLogout 先清理登录态再回到登录页，避免旧 token 继续驱动后台查询。
async function onLogout() {
  await auth.logout()
  await router.replace('/login')
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
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  min-height: 64px;
}

.brand-mark {
  width: 36px;
  height: 36px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  background: linear-gradient(135deg, #00F0FF, #7B2EDA);
  box-shadow: 0 0 14px rgba(0, 240, 255, 0.3);
  font-size: 13px;
  font-weight: 800;
  flex-shrink: 0;
}

.logo-text strong { display: block; font-size: 15px; }
.logo-text span { display: block; font-size: 11px; color: #8A94C6; }

.sidebar-footer {
  padding: 12px 14px 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.dashboard-page-frame {
  display: flex;
  min-height: calc(100vh - 112px);
  min-width: 0;
  flex: 1;
  flex-direction: column;
}

.dashboard-page-frame :deep(> *) {
  min-height: 0;
  flex: 1;
}
</style>
