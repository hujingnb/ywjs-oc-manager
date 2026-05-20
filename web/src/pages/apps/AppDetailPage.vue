<template>
  <div style="display: grid; gap: 18px; align-items: start; align-content: start">
    <n-card :bordered="true" content-style="display: none" header-style="padding-bottom: 0">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">Instance · Detail</p>
            <h2 style="margin: 0">{{ app?.name ?? '实例详情' }}</h2>
          </div>
          <AppStatusTag v-if="app" :status="app.status" />
        </div>
        <p v-if="appQuery.isLoading.value" class="state-text" style="padding: 12px 0 0">加载中…</p>
        <p v-else-if="appQuery.error.value" class="state-text danger" style="padding: 12px 0 0">查询失败：{{ appQuery.error.value?.message }}</p>
        <div v-if="app" class="tab-nav">
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

import { useAppQuery, type AppDTO } from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import { useAuthStore } from '@/stores/auth'

// AppDetailPage 是应用详情的父页面，负责加载应用基础信息并向子 tab 注入应用上下文。
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const appIdRef = computed(() => route.params.appId as string | undefined)
const appQuery = useAppQuery(appIdRef)
const app = computed<AppDTO | null>(() => appQuery.data.value ?? null)

// 子 tab 共享 app，避免每个 tab 重复查询并保持权限判断基于同一份应用数据。
provide<typeof app>('app', app)

// allTabs 定义详情页的全部业务分区，path 必须和子路由末段保持一致。
const allTabs: ReadonlyArray<{ path: string; label: string }> = [
  { path: 'overview', label: '概览' },
  { path: 'kanban', label: '任务' },
  { path: 'cron', label: '定时任务' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '实例知识库' },
  { path: 'workspace', label: '工作目录' },
  { path: 'audit', label: '审计' },
]

// 运行时 tab 仅对平台管理员可见，属基础设施层信息不向组织用户暴露。
const tabs = computed(() =>
  auth.isPlatformAdmin ? allTabs : allTabs.filter(t => t.path !== 'runtime')
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
.tab-nav {
  display: flex;
  /* header slot 内，左右对齐 header padding，通过负 margin 拉齐两端 */
  margin: 8px -24px 0;
  padding: 0 24px;
  border-top: 1px solid var(--n-border-color, #333);
  gap: 4px;
}
.tab-item {
  background: none;
  border: none;
  border-bottom: 3px solid transparent;
  color: var(--n-text-color-3, #999);
  cursor: pointer;
  font-size: 14px;
  padding: 10px 12px;
  /* margin-bottom 负值让 border-bottom 紧贴卡片底边 */
  margin-bottom: -1px;
  transition: color 0.2s, border-color 0.2s;
}
.tab-item:hover {
  color: var(--n-text-color, #fff);
}
.tab-item.active {
  color: var(--primary-color, #18a058);
  border-bottom-color: var(--primary-color, #18a058);
}
</style>
