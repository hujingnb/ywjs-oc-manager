<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Platform · Dashboard</p>
          <h2>平台总览</h2>
        </div>
      </div>

      <div v-if="!isPlatformAdmin" class="state-text">仅平台管理员可访问。</div>
      <div v-else-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <template v-else-if="overview">
        <div class="overview-grid">
          <div class="overview-card">
            <p class="card-label">组织数</p>
            <p class="card-value">{{ overview.organization_count }}</p>
          </div>
          <div class="overview-card">
            <p class="card-label">成员数</p>
            <p class="card-value">{{ overview.member_count }}</p>
            <p class="card-foot">不含平台管理员</p>
          </div>
          <div class="overview-card">
            <p class="card-label">应用数</p>
            <p class="card-value">{{ overview.app_count }}</p>
          </div>
          <div class="overview-card">
            <p class="card-label">运行中</p>
            <p class="card-value running">{{ overview.running_app_count }}</p>
          </div>
          <div class="overview-card">
            <p class="card-label">异常</p>
            <p class="card-value error">{{ overview.error_app_count }}</p>
          </div>
          <div class="overview-card">
            <p class="card-label">总余额</p>
            <p class="card-value">{{ overview.usage_available ? formatQuota(overview.total_remain_quota) : '—' }}</p>
            <p class="card-foot">{{ overview.usage_available ? 'new-api 实时' : '用量服务未启用' }}</p>
          </div>
        </div>
      </template>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { usePlatformOverviewQuery } from '@/api/hooks/usePlatform'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')

const { data: overview, isLoading, error } = usePlatformOverviewQuery(isPlatformAdmin)

function formatQuota(value: number): string {
  return value.toLocaleString('en-US')
}
</script>

<style scoped>
.overview-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;
  margin-top: 12px;
}

.overview-card {
  padding: 16px;
  background: #f5fbfa;
  border: 1px solid #d6e8e3;
  border-radius: 8px;
}

.card-label {
  font-size: 12px;
  color: #6b7c79;
  margin: 0 0 6px;
}

.card-value {
  font-size: 28px;
  font-weight: 600;
  color: #276d5c;
  margin: 0;
}

.card-value.running {
  color: #2c7a2c;
}

.card-value.error {
  color: #b51d1d;
}

.card-foot {
  font-size: 11px;
  color: #99a8a4;
  margin: 4px 0 0;
}
</style>
