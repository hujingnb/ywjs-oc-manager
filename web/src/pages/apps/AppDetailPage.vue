<template>
  <div class="app-detail-root" :class="{ 'app-detail-root--fill': currentTab === 'conversations' }">
    <n-card :bordered="true" content-style="display: none" header-style="padding-bottom: 0">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">Instance · Detail</p>
            <h2 style="margin: 0">{{ app?.name ?? t('apps.detail.title') }}</h2>
            <!-- 平台管理员需要用 manager UUID 跨系统排障；组织用户不展示这类底层标识。 -->
            <p v-if="app && auth.isPlatformAdmin" class="instance-uuid">
              {{ t('apps.detail.uuid') }}<code>{{ app.id }}</code>
            </p>
          </div>
          <AppStatusTag v-if="app" :status="app.status" />
        </div>
        <p v-if="appQuery.isLoading.value" class="state-text" style="padding: 12px 0 0">{{ t('apps.detail.loading') }}</p>
        <p v-else-if="appQuery.error.value" class="state-text danger" style="padding: 12px 0 0">{{ t('apps.detail.loadError') }}{{ appQuery.error.value?.message }}</p>
        <div v-if="app && showTabNav" class="tab-nav">
          <button
            v-for="tab in tabs"
            :key="tab.path"
            class="tab-item"
            :class="{ active: currentTab === tab.path }"
            @click="onTabChange(tab.path)"
          >{{ tab.label }}</button>
        </div>
      </template>
    </n-card>

    <RouterView v-if="app" :app-id="app.id" />
  </div>
</template>

<script setup lang="ts">
import { computed, provide } from 'vue'
import { useRoute, useRouter, RouterView } from 'vue-router'
import { NCard } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import { useAppQuery, type AppDTO } from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import { useAuthStore } from '@/stores/auth'

// AppDetailPage 是应用详情的父页面，负责加载应用基础信息并向子 tab 注入应用上下文。
const { t } = useI18n()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const appIdRef = computed(() => route.params.appId as string | undefined)
const appQuery = useAppQuery(appIdRef)
const app = computed<AppDTO | null>(() => appQuery.data.value ?? null)
// showTabNav 控制详情页顶部 tab 是否展示；组织成员已通过左侧菜单直达实例能力，隐藏可避免重复导航。
const showTabNav = computed(() => !auth.isOrgMember)

// 子 tab 共享 app，避免每个 tab 重复查询并保持权限判断基于同一份应用数据。
provide<typeof app>('app', app)

// allTabs 定义详情页的全部业务分区，path 必须和子路由末段保持一致。
// 使用 computed 包裹以支持语言切换时 tab 标签响应式更新。
const allTabs = computed<ReadonlyArray<{ path: string; label: string }>>(() => [
  // conversations tab：实例 hermes 会话管理，所有角色均可访问，置于首位（最常用）。
  { path: 'conversations', label: t('apps.detail.tabs.conversations') },
  { path: 'overview', label: t('apps.detail.tabs.overview') },
  { path: 'kanban', label: t('apps.detail.tabs.kanban') },
  { path: 'cron', label: t('apps.detail.tabs.cron') },
  { path: 'runtime', label: t('apps.detail.tabs.runtime') },
  { path: 'channels', label: t('apps.detail.tabs.channels') },
  { path: 'knowledge', label: t('apps.detail.tabs.knowledge') },
  // skills tab：管理员（platform_admin / org_admin）可通过此 tab 管理实例的技能。
  { path: 'skills', label: t('apps.detail.tabs.skills') },
  { path: 'workspace', label: t('apps.detail.tabs.workspace') },
  { path: 'audit', label: t('apps.detail.tabs.audit') },
])

// 运行时 tab 仅对平台管理员可见，属基础设施层信息不向组织用户暴露。
const tabs = computed(() =>
  auth.isPlatformAdmin ? allTabs.value : allTabs.value.filter(tab => tab.path !== 'runtime')
)

// currentTab 根据当前路由末段驱动 Naive tabs 激活态。
const currentTab = computed(() => {
  const parts = route.path.split('/')
  return parts[parts.length - 1] ?? 'overview'
})

// onTabChange 只在有 appId 时导航，避免缺失路由参数时拼出无效详情地址。
function onTabChange(name: string | number) {
  if (!appIdRef.value) return
  void router.push(`/apps/${appIdRef.value}/${name}`)
}
</script>

<style scoped>
/* 详情页根容器：默认 grid 自上而下排布（header 卡片 + RouterView），内容靠上。
   根容器由 DashboardLayout 的 .dashboard-page-frame 赋予 flex:1，已填满内容区高度。 */
.app-detail-root {
  display: grid;
  gap: 18px;
  align-items: start;
  align-content: start;
}
/* 对话 tab 专用：把 RouterView 所在行设为 minmax(0, 1fr) 吃满剩余高度，
   使 AppConversationsTab 能以 height:100% 填满并由其内部消息列表自身滚动，
   从而消除整页右侧滚动条。仅作用于对话 tab，不影响其他内容靠上排布的 tab。 */
.app-detail-root--fill {
  grid-template-rows: auto minmax(0, 1fr);
  align-content: stretch;
}
.tab-nav {
  display: flex;
  /* header slot 内，左右对齐 header padding，通过负 margin 拉齐两端 */
  margin: 8px -24px 0;
  padding: 0 24px;
  border-top: 1px solid var(--color-border, #e5e7eb);
  gap: 4px;
}
.tab-item {
  background: none;
  border: none;
  border-bottom: 3px solid transparent;
  color: var(--color-text-secondary, #6b7280);
  cursor: pointer;
  font-size: 14px;
  padding: 10px 12px;
  /* margin-bottom 负值让 border-bottom 紧贴卡片底边 */
  margin-bottom: -1px;
  transition: color 0.2s, border-color 0.2s;
}
.tab-item:hover {
  color: var(--color-text-primary, #1f2329);
}
.tab-item.active {
  color: var(--color-brand-text, #8a3700);
  border-bottom-color: var(--color-brand, #ff6a00);
}
.instance-uuid {
  color: var(--color-text-secondary, #6b7280);
  font-size: 12px;
  line-height: 1.5;
  margin: 6px 0 0;
}
.instance-uuid code {
  color: var(--color-text-primary, #1f2329);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  overflow-wrap: anywhere;
}
</style>
