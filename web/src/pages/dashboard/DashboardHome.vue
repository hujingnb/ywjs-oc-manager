<template>
  <div style="display: grid; gap: 18px">
    <!-- Metric 卡片行 -->
    <n-grid :cols="4" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="m in metrics" :key="m.label" :span="1" :xs="2" :sm="1">
        <n-card size="small" :bordered="true" style="height: 100%">
          <n-statistic :label="m.label" :value="m.value">
            <template #suffix>
              <span style="font-size: 12px; color: #8A94C6">{{ m.unit }}</span>
            </template>
          </n-statistic>
          <n-progress
            type="line"
            :percentage="m.pct"
            :show-indicator="false"
            :height="4"
            style="margin-top: 10px"
          />
          <div style="font-size: 11px; color: #8A94C6; margin-top: 4px">{{ m.note }}</div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 图表占位 -->
    <n-card size="small" :bordered="true">
      <template #header>
        <span style="font-size: 14px; font-weight: 600">Token 趋势</span>
      </template>
      <div style="display: grid; place-items: center; min-height: 80px; color: #8A94C6; font-size: 13px">
        即将上线 · 引入 vue-echarts 后填充
      </div>
    </n-card>

    <!-- 主体双列 -->
    <n-grid :cols="24" :x-gap="14">
      <!-- 应用队列 -->
      <n-grid-item :span="17" :xs="24" :md="17">
        <n-card size="small" :bordered="true">
          <template #header>
            <span style="font-size: 14px; font-weight: 600">实例队列</span>
          </template>
          <template #header-extra>
            <n-button size="small" type="primary" @click="router.push('/apps')">前往实例列表</n-button>
          </template>
          <n-data-table
            :columns="appColumns"
            :data="apps ?? []"
            :loading="appsLoading"
            size="small"
            :bordered="false"
          />
        </n-card>
      </n-grid-item>

      <!-- 右侧面板 -->
      <n-grid-item :span="7" :xs="24" :md="7">
        <div style="display: grid; gap: 14px">
          <!-- 节点状态 -->
          <n-card size="small" :bordered="true" title="节点状态">
            <div class="node-row">
              <Server :size="18" style="color: #8A94C6; flex-shrink: 0" />
              <div>
                <strong>node-local-dev</strong>
                <span style="display: block; font-size: 12px; color: #8A94C6">Docker proxy 与文件 API 待注册</span>
              </div>
              <n-tag type="warning" size="small">pending</n-tag>
            </div>
          </n-card>

          <!-- 快捷操作 -->
          <n-card size="small" :bordered="true" title="快捷操作">
            <div style="display: grid; gap: 8px">
              <n-button block>重启系统服务</n-button>
              <n-button block>清理系统缓存</n-button>
              <n-button block>查看系统日志</n-button>
            </div>
          </n-card>
        </div>
      </n-grid-item>
    </n-grid>
  </div>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton, NCard, NDataTable, NGrid, NGridItem, NProgress, NStatistic, NTag,
  type DataTableColumns,
} from 'naive-ui'
import { Server } from 'lucide-vue-next'

import { useAppsByOrgQuery, type AppDTO } from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import { useAuthStore } from '@/stores/auth'

// DashboardHome 是组织视角的调试总览页，展示当前组织应用和固定的本地环境指标。
const auth = useAuthStore()
const router = useRouter()
// effectiveOrgId 来自当前登录用户，组织未绑定时应用查询不会发起有效请求。
const effectiveOrgId = computed(() => auth.user?.org_id)
const { data: apps, isLoading: appsLoading } = useAppsByOrgQuery(effectiveOrgId)

// metrics 是本地调试占位指标，真实用量在 Usage 页面直接查询 new-api 汇总。
const metrics = [
  { label: '组织', value: '0', unit: '', pct: 0, note: '等待初始化' },
  { label: '实例', value: '0', unit: '', pct: 0, note: '尚未创建' },
  { label: '运行节点', value: '1', unit: '', pct: 100, note: '本地调试节点' },
  { label: '今日调用', value: '0', unit: '', pct: 0, note: '直查 new-api' },
]

// appColumns 展示应用名称、节点和状态，状态渲染复用统一应用状态徽标。
const appColumns: DataTableColumns<AppDTO> = [
  { title: '实例名称', key: 'name', render: (row) => h('strong', row.name) },
  { title: '节点', key: 'runtime_node_id', render: (row) => row.runtime_node_id ?? '—' },
  {
    title: '状态',
    key: 'status',
    render: (row) => h(AppStatusTag, { status: row.status }),
  },
]
</script>
