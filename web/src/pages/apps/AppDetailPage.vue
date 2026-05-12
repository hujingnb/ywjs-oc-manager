<template>
  <div style="display: grid; gap: 18px">
    <n-card :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">App · Detail</p>
            <h2 style="margin: 0">{{ app?.name ?? '应用详情' }}</h2>
          </div>
          <AppStatusTag v-if="app" :status="app.status" />
        </div>
      </template>

      <p v-if="appQuery.isLoading.value" class="state-text">加载中…</p>
      <p v-else-if="appQuery.error.value" class="state-text danger">查询失败：{{ appQuery.error.value?.message }}</p>

      <n-tabs v-if="app" :value="currentTab" type="line" @update:value="onTabChange">
        <n-tab-pane v-for="tab in tabs" :key="tab.path" :name="tab.path" :tab="tab.label" />
      </n-tabs>
    </n-card>

    <RouterView v-if="app" :app-id="app.id" />
  </div>
</template>

<script setup lang="ts">
import { computed, provide } from 'vue'
import { useRoute, useRouter, RouterView } from 'vue-router'
import { NCard, NTabPane, NTabs } from 'naive-ui'

import { useAppQuery, type AppDTO } from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'

// AppDetailPage 是应用详情的父页面，负责加载应用基础信息并向子 tab 注入应用上下文。
const route = useRoute()
const router = useRouter()

const appIdRef = computed(() => route.params.appId as string | undefined)
const appQuery = useAppQuery(appIdRef)
const app = computed<AppDTO | null>(() => appQuery.data.value ?? null)

// 子 tab 共享 app，避免每个 tab 重复查询并保持权限判断基于同一份应用数据。
provide<typeof app>('app', app)

// tabs 定义详情页的业务分区，path 必须和子路由末段保持一致。
const tabs: ReadonlyArray<{ path: string; label: string }> = [
  { path: 'overview', label: '概览' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '应用知识库' },
  { path: 'workspace', label: '工作目录' },
  { path: 'audit', label: '审计' },
]

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
/* 隐藏空 tab pane body，内容由 RouterView 渲染 */
:deep(.n-tabs-pane-wrapper) {
  display: none;
}
</style>
