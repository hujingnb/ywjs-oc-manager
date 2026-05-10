<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Runtime Node</p>
          <h2>{{ node?.name ?? '加载中…' }}</h2>
        </div>
        <RouterLink class="secondary-button" to="/runtime-nodes">返回列表</RouterLink>
      </div>

      <div v-if="isLoading" class="state-text">加载中…</div>
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <dl v-else-if="node" class="detail-grid">
        <div>
          <dt>状态</dt>
          <dd><RuntimeStatusTag :status="node.status" /></dd>
        </div>
        <div>
          <dt>Docker endpoint</dt>
          <dd>{{ node.agent_docker_endpoint || '—' }}</dd>
        </div>
        <div>
          <dt>File endpoint</dt>
          <dd>{{ node.agent_file_endpoint || '—' }}</dd>
        </div>
        <div>
          <dt>Agent 版本</dt>
          <dd>{{ node.agent_version || '—' }}</dd>
        </div>
        <div>
          <dt>心跳间隔</dt>
          <dd>{{ node.heartbeat_interval_seconds }} 秒</dd>
        </div>
        <div>
          <dt>数据根目录</dt>
          <dd>{{ node.node_data_root || '—' }}</dd>
        </div>
        <div>
          <dt>Agent ID</dt>
          <dd>{{ node.agent_id || '—' }}</dd>
        </div>
        <div>
          <dt>Agent 已注册</dt>
          <dd>{{ node.has_agent_token ? '是' : '否' }}</dd>
        </div>
        <div>
          <dt>最近探测</dt>
          <dd>{{ node.last_probe_attempted_at ? new Date(node.last_probe_attempted_at).toLocaleString() : '—' }}</dd>
        </div>
        <div>
          <dt>最近成功探测</dt>
          <dd>{{ node.last_probe_ok_at ? new Date(node.last_probe_ok_at).toLocaleString() : '—' }}</dd>
        </div>
        <div>
          <dt>探测失败</dt>
          <dd>{{ node.last_probe_error || '—' }}</dd>
        </div>
        <div>
          <dt>探测计数</dt>
          <dd>失败 {{ node.probe_failure_streak ?? 0 }} / 成功 {{ node.probe_success_streak ?? 0 }}</dd>
        </div>
      </dl>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import RuntimeStatusTag from '@/components/RuntimeStatusTag.vue'
import { useRuntimeNodeQuery } from '@/api/hooks/useRuntimeNodes'

const route = useRoute()
const nodeId = computed(() => (typeof route.params.nodeId === 'string' ? route.params.nodeId : undefined))
const { data: node, isLoading, error } = useRuntimeNodeQuery(nodeId)
</script>

<style scoped>
.detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin-top: 16px;
}

.detail-grid div {
  border: 1px solid #e4eaf2;
  border-radius: 8px;
  padding: 14px;
  background: #f8fafc;
}

.detail-grid dt {
  margin: 0 0 6px;
  color: #66758a;
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.detail-grid dd {
  margin: 0;
  color: #172033;
  font-weight: 600;
  word-break: break-all;
}
</style>
