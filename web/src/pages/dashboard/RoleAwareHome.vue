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
import { computed, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'
import { useMemberApp } from '@/composables/useMemberApp'

// RoleAwareHome 根据当前角色展示首屏快捷入口，避免不同角色看到无权限入口。
const auth = useAuthStore()
const router = useRouter()
const { t } = useI18n()

const { appId: memberAppId, hasApp: memberHasApp, isLoading: memberAppLoading } = useMemberApp()

// memberHomePath 复用现有实例详情路由；无实例时进入空状态页，避免生成缺少 appId 的路径。
const memberHomePath = computed(() =>
  memberHasApp.value && memberAppId.value ? `/apps/${memberAppId.value}/overview` : '/apps/empty',
)

// 首页只承担按角色分流的职责；成员实例查询未完成前不跳转，避免误落到空状态。
watch(
  () => ({
    role: auth.user?.role,
    memberLoading: memberAppLoading.value,
    memberPath: memberHomePath.value,
  }),
  ({ role, memberLoading, memberPath }) => {
    if (role === 'platform_admin') {
      void router.replace('/console')
    } else if (role === 'org_admin') {
      void router.replace('/org-console')
    } else if (role === 'org_member' && !memberLoading) {
      void router.replace(memberPath)
    }
  },
  { immediate: true },
)

// roleLabel 只用于欢迎区的角色展示，未知角色返回空字符串。
const roleLabel = computed(() => {
  switch (auth.user?.role) {
    case 'platform_admin':
      return t('dashboard.role.platformAdmin')
    case 'org_admin':
      return t('dashboard.role.orgAdmin')
    case 'org_member':
      return t('dashboard.role.member')
    default:
      return ''
  }
})

// greeting 欢迎区标语，随语言切换响应式更新。
const greeting = computed(() =>
  t('dashboard.greeting', { name: auth.user?.display_name ?? auth.user?.username ?? '' }),
)

// QuickCard 描述一个首页快捷入口，path 必须对应路由表中的后台路径。
interface QuickCard { path: string; title: string; subtitle: string }

// cards 按角色返回可访问的核心工作流入口；权限兜底仍由路由和接口控制。
// 使用 computed 确保语言切换时文案响应式刷新。
const cards = computed<QuickCard[]>(() => {
  const role = auth.user?.role
  if (role === 'platform_admin') {
    return [
      { path: '/organizations', title: t('dashboard.cards.organizations.title'), subtitle: t('dashboard.cards.organizations.subtitle') },
      { path: '/audit-logs', title: t('dashboard.cards.auditLogs.title'), subtitle: t('dashboard.cards.auditLogs.subtitle') },
    ]
  }
  if (role === 'org_admin') {
    return [
      { path: '/members', title: t('dashboard.cards.members.title'), subtitle: t('dashboard.cards.members.subtitle') },
      { path: '/apps', title: t('dashboard.cards.apps.title'), subtitle: t('dashboard.cards.apps.subtitle') },
      { path: '/knowledge', title: t('dashboard.cards.orgKnowledge.title'), subtitle: t('dashboard.cards.orgKnowledge.subtitle') },
    ]
  }
  if (role === 'org_member') {
    // 有实例时直达详情，无实例时进入空状态页。
    const appPath = memberHasApp.value && memberAppId.value
      ? `/apps/${memberAppId.value}/overview`
      : '/apps/empty'
    return [
      { path: appPath, title: t('dashboard.cards.myApp.title'), subtitle: t('dashboard.cards.myApp.subtitle') },
      { path: '/usage', title: t('dashboard.cards.myUsage.title'), subtitle: t('dashboard.cards.myUsage.subtitle') },
      { path: '/knowledge', title: t('dashboard.cards.readKnowledge.title'), subtitle: t('dashboard.cards.readKnowledge.subtitle') },
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
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  color: var(--color-text-primary);
  text-decoration: none;
  transition: transform 0.12s ease, box-shadow 0.12s ease, border-color 0.12s ease;
}

.quick-card:hover {
  transform: translateY(-2px);
  border-color: var(--color-brand);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08);
}

.quick-card h3 {
  margin: 0 0 6px;
  font-size: 16px;
}

.quick-card p {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: 13px;
}
</style>
