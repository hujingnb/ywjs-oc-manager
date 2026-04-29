<template>
  <main class="dashboard-main">
    <section class="metric-grid" aria-label="核心指标">
      <article v-for="metric in metrics" :key="metric.label" class="metric-card">
        <span>{{ metric.label }}</span>
        <strong>{{ metric.value }}</strong>
        <small>{{ metric.note }}</small>
      </article>
    </section>

    <section class="content-grid">
      <article class="panel runtime-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Runtime Nodes</p>
            <h2>节点状态</h2>
          </div>
          <span class="status-pill ok">1 active</span>
        </div>
        <div class="node-row">
          <Server :size="20" />
          <div>
            <strong>node-local-dev</strong>
            <span>Docker proxy 与文件 API 待注册</span>
          </div>
          <span class="status-pill warn">pending</span>
        </div>
      </article>

      <article class="panel app-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Applications</p>
            <h2>应用队列</h2>
          </div>
        </div>
        <table>
          <thead>
            <tr>
              <th>应用</th>
              <th>节点</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="app in apps" :key="app.name">
              <td>{{ app.name }}</td>
              <td>{{ app.node }}</td>
              <td>
                <span :class="['status-pill', formatAppStatus(app.status).tone]">
                  {{ formatAppStatus(app.status).label }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </article>
    </section>
  </main>
</template>

<script setup lang="ts">
import { Server } from 'lucide-vue-next'

import { formatAppStatus } from '@/domain/status'

const metrics = [
  { label: '组织', value: '0', note: '等待初始化' },
  { label: '应用', value: '0', note: '尚未创建' },
  { label: '运行节点', value: '1', note: '本地调试节点' },
  { label: '今日调用', value: '0', note: '直查 new-api' },
]

const apps = [
  { name: 'demo-openclaw', node: 'node-local-dev', status: 'draft' },
  { name: 'wechat-assistant', node: 'node-local-dev', status: 'binding_waiting' },
]
</script>
