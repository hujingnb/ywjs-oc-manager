<template>
  <div class="dashboard-shell">
    <aside class="sidebar" aria-label="主导航">
      <div class="brand-block">
        <div class="brand-mark">OC</div>
        <div>
          <strong>OpenClaw</strong>
          <span>Manager</span>
        </div>
      </div>

      <nav class="nav-list">
        <RouterLink class="nav-item" exact-active-class="active" to="/">
          <LayoutDashboard :size="18" />
          <span>总览</span>
        </RouterLink>
        <RouterLink
          v-if="auth.user?.role === 'platform_admin'"
          class="nav-item"
          active-class="active"
          to="/organizations"
        >
          <Building2 :size="18" />
          <span>组织</span>
        </RouterLink>
        <RouterLink class="nav-item" active-class="active" to="/members">
          <Users :size="18" />
          <span>成员</span>
        </RouterLink>
        <RouterLink class="nav-item" active-class="active" to="/apps">
          <Bot :size="18" />
          <span>应用</span>
        </RouterLink>
        <RouterLink class="nav-item" active-class="active" to="/knowledge">
          <BookOpen :size="18" />
          <span>知识库</span>
        </RouterLink>
        <RouterLink class="nav-item" active-class="active" to="/audit-logs">
          <FileSearch :size="18" />
          <span>审计</span>
        </RouterLink>
        <RouterLink
          v-if="auth.user?.role === 'platform_admin'"
          class="nav-item"
          active-class="active"
          to="/runtime-nodes"
        >
          <Server :size="18" />
          <span>运行节点</span>
        </RouterLink>
      </nav>

      <div class="sidebar-footer">
        <p v-if="auth.user" class="me-info">
          <strong>{{ auth.user.display_name }}</strong>
          <small>{{ auth.user.username }}</small>
        </p>
        <button v-if="auth.user" class="secondary-button" type="button" @click="onLogout">
          <LogOut :size="16" />
          <span>退出</span>
        </button>
      </div>
    </aside>

    <div class="workspace">
      <header class="topbar">
        <div>
          <p class="eyebrow">{{ environmentLabel }}</p>
          <h1>控制台</h1>
        </div>
        <div class="topbar-actions">
          <span class="status-pill ok">API 正常</span>
          <span class="status-pill warn">Ollama 待配置模型</span>
          <button class="icon-button" type="button" aria-label="刷新" @click="reload">
            <RefreshCw :size="18" />
          </button>
        </div>
      </header>

      <RouterView />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import {
  BookOpen,
  Bot,
  Building2,
  FileSearch,
  LayoutDashboard,
  LogOut,
  RefreshCw,
  Server,
  Users,
} from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()

const environmentLabel = computed(() => {
  if (!auth.user) return '本地调试环境'
  return `本地调试环境 · ${auth.user.role}`
})

async function onLogout() {
  await auth.logout()
  await router.replace('/login')
}

function reload() {
  window.location.reload()
}
</script>
