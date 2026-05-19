<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">{{ roleLabel }}</p>
          <h2>{{ greeting }}</h2>
        </div>
      </div>

      <div class="quick-grid">
        <RouterLink v-for="card in cards" :key="card.path" class="quick-card" :to="card.path">
          <h3>{{ card.title }}</h3>
          <p>{{ card.subtitle }}</p>
        </RouterLink>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { useMemberApp } from '@/composables/useMemberApp'

// RoleAwareHome 根据当前角色展示首屏快捷入口，避免不同角色看到无权限入口。
const auth = useAuthStore()
const { appId: memberAppId, hasApp: memberHasApp } = useMemberApp()

// roleLabel 只用于欢迎区的角色展示，未知角色返回空字符串。
const roleLabel = computed(() => {
  switch (auth.user?.role) {
    case 'platform_admin':
      return 'Platform Admin'
    case 'org_admin':
      return 'Org Admin'
    case 'org_member':
      return 'Member'
    default:
      return ''
  }
})

const greeting = computed(() => `欢迎回来，${auth.user?.display_name ?? auth.user?.username ?? '用户'}`)

// QuickCard 描述一个首页快捷入口，path 必须对应路由表中的后台路径。
interface QuickCard { path: string; title: string; subtitle: string }

// cards 按角色返回可访问的核心工作流入口；权限兜底仍由路由和接口控制。
const cards = computed<QuickCard[]>(() => {
  const role = auth.user?.role
  if (role === 'platform_admin') {
    return [
      { path: '/organizations', title: '组织管理', subtitle: '查看 / 创建 / 充值组织' },
      { path: '/runtime-nodes', title: 'Runtime Node', subtitle: '注册和监控节点' },
      { path: '/audit-logs', title: '审计日志', subtitle: '高风险操作回溯' },
    ]
  }
  if (role === 'org_admin') {
    return [
      { path: '/members', title: '成员管理', subtitle: '创建 / 禁用 / 删除组织成员' },
      { path: '/org/persona', title: 'AI 人设', subtitle: '调整组织默认人设' },
      { path: '/apps', title: '实例列表', subtitle: '组织内全部实例状态' },
      { path: '/knowledge', title: '组织知识库', subtitle: '上传共享文件' },
    ]
  }
  if (role === 'org_member') {
    // 有实例时直达详情，无实例时进入空状态页。
    const appPath = memberHasApp.value && memberAppId.value
      ? `/apps/${memberAppId.value}/overview`
      : '/apps/empty'
    return [
      { path: appPath, title: '我的实例', subtitle: '查看状态、用量与实例审计' },
      { path: '/usage', title: '我的用量', subtitle: '查看自己实例的调用记录' },
      { path: '/knowledge', title: '组织知识库', subtitle: '可读资料' },
    ]
  }
  return []
})
</script>

<style scoped>
.quick-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
  margin-top: 16px;
}

.quick-card {
  display: block;
  padding: 16px;
  border: 1px solid rgba(0, 0, 0, 0.08);
  border-radius: 10px;
  background: white;
  color: var(--color-text);
  text-decoration: none;
  transition: transform 0.12s ease, box-shadow 0.12s ease;
}

.quick-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.08);
}

.quick-card h3 {
  margin: 0 0 6px;
  font-size: 16px;
}

.quick-card p {
  margin: 0;
  color: rgba(0, 0, 0, 0.55);
  font-size: 13px;
}
</style>
