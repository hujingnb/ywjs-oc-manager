<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Platform · Dashboard</p>
        <h2 style="margin: 0">平台总览</h2>
      </div>
    </template>

    <div v-if="!isPlatformAdmin" class="state-text">仅平台管理员可访问。</div>
    <div v-else-if="isLoading" class="state-text">加载中…</div>
    <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
    <n-grid v-else-if="overview" :cols="6" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="stat in stats" :key="stat.label" :span="1" :xs="2">
        <n-card size="small" :bordered="true">
          <n-statistic :label="stat.label" :value="stat.value" />
          <div v-if="stat.note" style="font-size: 11px; color: #8A94C6; margin-top: 4px">{{ stat.note }}</div>
        </n-card>
      </n-grid-item>
    </n-grid>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NGrid, NGridItem, NStatistic } from 'naive-ui'

import { usePlatformOverviewQuery } from '@/api/hooks/usePlatform'
import { useAuthStore } from '@/stores/auth'

// PlatformDashboardPage 展示平台级概览，只在平台管理员身份下启用查询。
const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')
const { data: overview, isLoading, error } = usePlatformOverviewQuery(isPlatformAdmin)

// formatQuota 统一平台余额数字格式，避免不同统计卡片使用不同分隔符。
function formatQuota(value: number) { return `￥${value.toLocaleString('en-US')}` }

// stats 将平台概览 DTO 转为统计卡片数据，用量服务不可用时余额显示占位符。
const stats = computed(() => {
  if (!overview.value) return []
  const o = overview.value
  return [
    { label: '组织数', value: String(o.organization_count), note: '' },
    { label: '成员数', value: String(o.member_count), note: '不含平台管理员' },
    { label: '实例数', value: String(o.app_count), note: '' },
    { label: '运行中', value: String(o.running_app_count), note: '' },
    { label: '异常', value: String(o.error_app_count), note: '' },
    {
      label: '总余额',
      value: o.usage_available ? formatQuota(o.total_remain_quota) : '—',
      note: o.usage_available ? 'new-api 实时' : '用量服务未启用',
    },
  ]
})
</script>
