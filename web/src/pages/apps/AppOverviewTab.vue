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
        <dd class="apikey-cell">
          <span :class="['key-tag', `key-${app.api_key_status}`]">{{ apiKeyLabel(app.api_key_status) }}</span>
          <button
            v-if="canToggleKey && app.api_key_status === 'active'"
            class="secondary-button danger"
            type="button"
            :disabled="keyMutation.isPending.value"
            @click="confirmDisableKey = true"
          >
            禁用
          </button>
          <button
            v-if="canToggleKey && app.api_key_status === 'disabled'"
            class="secondary-button"
            type="button"
            :disabled="keyMutation.isPending.value"
            @click="onRestoreKey"
          >
            恢复
          </button>
        </dd>
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
    <p v-if="keyFeedback" class="state-text" :class="{ danger: keyError }">{{ keyFeedback }}</p>

    <JobProgressPanel
      v-if="trackingJobId"
      :title="'初始化任务'"
      :subtitle="trackingJobId"
      :job="trackedJob ?? undefined"
    />

    <ConfirmActionModal
      :visible="confirmDisableKey"
      title="确认禁用 API key"
      message="禁用后 OpenClaw 容器将无法调用模型，对话立即停止；可在恢复时重新启用。"
      confirm-label="确认禁用"
      :busy="keyMutation.isPending.value"
      @confirm="onConfirmDisable"
      @cancel="confirmDisableKey = false"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'

import {
  useInitializeAppMutation,
  useJobQuery,
  useToggleAppAPIKey,
  type AppDTO,
} from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'
import { useAuthStore } from '@/stores/auth'

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

const auth = useAuthStore()
const canToggleKey = computed(() => {
  const role = auth.user?.role
  return role === 'platform_admin' || role === 'org_admin'
})

const keyMutation = useToggleAppAPIKey(appId)
const confirmDisableKey = ref(false)
const keyFeedback = ref('')
const keyError = ref(false)

function apiKeyLabel(s: string): string {
  return s === 'active' ? '启用' : s === 'disabled' ? '已禁用' : s
}

async function onConfirmDisable() {
  confirmDisableKey.value = false
  await runKeyMutation('disable')
}

async function onRestoreKey() {
  await runKeyMutation('restore')
}

async function runKeyMutation(action: 'disable' | 'restore') {
  keyFeedback.value = ''
  keyError.value = false
  try {
    const result = await keyMutation.mutateAsync(action)
    trackingJobId.value = result.job_id
    keyFeedback.value = `已提交 ${action} 任务：${result.job_id}`
  } catch (err: unknown) {
    keyError.value = true
    keyFeedback.value = err instanceof Error ? err.message : `${action} 失败`
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

.apikey-cell {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.key-tag {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 12px;
  font-weight: 500;
}

.key-active {
  background: #e6f7e0;
  color: #2c7a2c;
}

.key-disabled {
  background: #ffe1e1;
  color: #b51d1d;
}

.key-pending {
  background: #fff7e6;
  color: #ad6800;
}

.danger {
  color: rgba(220, 38, 38, 1);
  border-color: rgba(220, 38, 38, 0.4);
}
</style>
