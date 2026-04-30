<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Overview</p>
        <h2>概览</h2>
      </div>
      <button
        class="primary-button"
        type="button"
        :disabled="!canRetryInit || initMutation.isPending.value"
        @click="onRetryInit"
      >
        {{ initMutation.isPending.value ? '提交中…' : '重新初始化' }}
      </button>
    </div>

    <p v-if="!app" class="state-text">尚未加载应用信息</p>
    <dl v-else class="info-grid">
      <div>
        <dt>状态</dt>
        <dd><AppStatusTag :status="app.status" /></dd>
      </div>
      <div>
        <dt>API key</dt>
        <dd>{{ app.api_key_status }}</dd>
      </div>
      <div>
        <dt>容器 ID</dt>
        <dd><code>{{ app.container_id || '—' }}</code></dd>
      </div>
      <div>
        <dt>Runtime Node</dt>
        <dd><code>{{ app.runtime_node_id || '—' }}</code></dd>
      </div>
      <div>
        <dt>人设模式</dt>
        <dd>{{ app.persona_mode }}</dd>
      </div>
      <div>
        <dt>所属组织</dt>
        <dd><code>{{ app.org_id }}</code></dd>
      </div>
      <div v-if="app.description" style="grid-column: span 2">
        <dt>描述</dt>
        <dd>{{ app.description }}</dd>
      </div>
    </dl>

    <p v-if="initFeedback" class="state-text" :class="{ danger: initError }">{{ initFeedback }}</p>

    <JobProgressPanel
      v-if="trackingJobId"
      :title="'初始化任务'"
      :subtitle="trackingJobId"
      :job="trackedJob ?? undefined"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'

import {
  useInitializeAppMutation,
  useJobQuery,
  type AppDTO,
} from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'

const props = defineProps<{ appId: string }>()
const appId = computed<string | undefined>(() => props.appId)

// AppDetailPage 通过 provide('app', ...) 注入应用 ref。
const app = inject<Ref<AppDTO | null>>('app')

const initMutation = useInitializeAppMutation(appId)
const trackingJobId = ref<string | undefined>()
const jobIdRef = computed<string | undefined>(() => trackingJobId.value)
const jobQuery = useJobQuery(jobIdRef)
const trackedJob = computed(() => jobQuery.data.value ?? null)

const canRetryInit = computed(() => {
  const status = app?.value?.status
  return status === 'error' || status === 'draft'
})

const initFeedback = ref('')
const initError = ref(false)

async function onRetryInit() {
  initFeedback.value = ''
  initError.value = false
  try {
    const result = await initMutation.mutateAsync()
    trackingJobId.value = result.job_id
    initFeedback.value = `已提交初始化任务：${result.job_id}`
  } catch (err: unknown) {
    initError.value = true
    initFeedback.value = err instanceof Error ? err.message : '初始化失败'
  }
}
</script>

<style scoped>
.info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px 24px;
  margin: 16px 0;
}

.info-grid dt {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.55);
  margin-bottom: 4px;
}

.info-grid dd {
  margin: 0;
  font-weight: 500;
}
</style>
