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
  BarChart3, BookOpen, Bot, Boxes, Building2, FileSearch, Gauge,
  LayoutDashboard, LogOut, RefreshCw, Server, ShieldCheck, Users, Wallet,
} from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'
import { useMemberApp } from '@/composables/useMemberApp'

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
  // org_member 的实例菜单 key 是动态路径，需要特殊匹配。
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
    '/runtime-nodes',
    '/platform/permissions',
  ]
  return prefixes.find(k => p.startsWith(k)) ?? '/'
})

const isPlatformAdmin = computed(() => auth.isPlatformAdmin)
const isOrgMember = computed(() => auth.isOrgMember)
// isOrgAdmin 用于控制账户余额菜单项的可见性，仅组织管理员需要此入口。
const isOrgAdmin = computed(() => auth.isOrgAdmin)

const { appId: memberAppId, hasApp: memberHasApp } = useMemberApp()

// org_member 的实例菜单目标：有实例指向详情，无实例指向空状态。
const memberAppPath = computed(() => {
  if (!isOrgMember.value) return '/apps'
  if (memberHasApp.value && memberAppId.value) return `/apps/${memberAppId.value}/overview`
  return '/apps/empty'
})

// menuOptions 根据角色裁剪入口：普通成员不显示组织管理和审计，平台管理员仅显示控制台单一入口。
const menuOptions = computed<MenuOption[]>(() => {
  // platform_admin 使用单一「控制台」入口，替代原来「总览+平台」两个菜单项。
  const items: MenuOption[] = isPlatformAdmin.value
    ? [{ key: '/console', label: '控制台', icon: () => h(Gauge, { size: 18 }) }]
    : [{ key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) }]
  if (isPlatformAdmin.value) {
    items.push({ key: '/organizations', label: '组织', icon: () => h(Building2, { size: 18 }) })
    items.push({ key: '/assistant-versions', label: '助手版本', icon: () => h(Boxes, { size: 18 }) })
  }
  // 成员/审计 是组织管理视角，普通成员不展示。
  if (!isOrgMember.value) {
    items.push({ key: '/members', label: '成员', icon: () => h(Users, { size: 18 }) })
  }
  items.push(
    { key: memberAppPath.value, label: '实例', icon: () => h(Bot, { size: 18 }) },
    { key: '/knowledge', label: '知识库', icon: () => h(BookOpen, { size: 18 }) },
    { key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) },
  )
  // 账户余额仅对 org_admin 显示；org_member 和 platform_admin 无此入口。
  if (isOrgAdmin.value) {
    items.push({ key: '/balance', label: '账户余额', icon: () => h(Wallet, { size: 18 }) })
  }
  if (!isOrgMember.value) {
    items.push({ key: '/audit-logs', label: '审计', icon: () => h(FileSearch, { size: 18 }) })
  }
  if (isPlatformAdmin.value) {
    items.push({ key: '/runtime-nodes', label: '运行节点', icon: () => h(Server, { size: 18 }) })
    items.push({ key: '/platform/permissions', label: '权限说明', icon: () => h(ShieldCheck, { size: 18 }) })
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

.logout-button {
  color: var(--color-text-secondary);
}

.logout-button:hover {
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
