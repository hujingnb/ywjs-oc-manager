<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">App · Overview</p>
        <h2 style="margin: 0">概览</h2>
      </div>
    </template>
    <template #header-extra>
      <n-button
        type="primary"
        :disabled="!canRetryInit || initMutation.isPending.value"
        @click="onRetryInit"
      >
        {{ initMutation.isPending.value ? '提交中…' : '重新初始化' }}
      </n-button>
    </template>

    <p v-if="!app" class="state-text">尚未加载应用信息</p>
    <n-descriptions v-else :column="2" bordered size="small">
      <n-descriptions-item label="状态">
        <AppStatusTag :status="app.status" />
      </n-descriptions-item>
      <n-descriptions-item label="API key">
        <n-space align="center" :size="8">
          <n-tag :type="keyTagType(app.api_key_status)" size="small" :bordered="false">
            {{ apiKeyLabel(app.api_key_status) }}
          </n-tag>
          <n-button
            v-if="canToggleKey && app.api_key_status === 'active'"
            size="small"
            type="error"
            :disabled="keyMutation.isPending.value"
            @click="confirmDisableKey = true"
          >
            禁用
          </n-button>
          <n-button
            v-if="canToggleKey && app.api_key_status === 'disabled'"
            size="small"
            :disabled="keyMutation.isPending.value"
            @click="onRestoreKey"
          >
            恢复
          </n-button>
        </n-space>
      </n-descriptions-item>
      <n-descriptions-item label="容器 ID">
        <code>{{ app.container_id || '—' }}</code>
      </n-descriptions-item>
      <n-descriptions-item label="Runtime Node">
        <code>{{ app.runtime_node_id || '—' }}</code>
      </n-descriptions-item>
      <n-descriptions-item label="人设模式">{{ app.persona_mode }}</n-descriptions-item>
      <n-descriptions-item label="所属组织">
        <code>{{ app.org_id }}</code>
      </n-descriptions-item>
      <n-descriptions-item v-if="app.description" label="描述" :span="2">
        {{ app.description }}
      </n-descriptions-item>
    </n-descriptions>

    <p v-if="initFeedback" class="state-text" :class="{ danger: initError }" style="margin-top: 8px">{{ initFeedback }}</p>
    <p v-if="keyFeedback" class="state-text" :class="{ danger: keyError }" style="margin-top: 8px">{{ keyFeedback }}</p>

    <JobProgressPanel
      v-if="trackingJobId"
      :title="'初始化任务'"
      :subtitle="trackingJobId"
      :job="trackedJob ?? undefined"
      style="margin-top: 12px"
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
  </n-card>
</template>

<script setup lang="ts">
import { computed, inject, ref, type Ref } from 'vue'
import { NButton, NCard, NDescriptions, NDescriptionsItem, NSpace, NTag } from 'naive-ui'

import {
  useInitializeAppMutation,
  useJobQuery,
  useToggleAppAPIKey,
  type AppDTO,
} from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import JobProgressPanel from '@/components/JobProgressPanel.vue'
import { canManageApp } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppOverviewTab 展示应用基础信息，并提供初始化重试和 API key 启停入口。
const props = defineProps<{ appId: string }>()
const appId = computed<string | undefined>(() => props.appId)

const app = inject<Ref<AppDTO | null>>('app')
const auth = useAuthStore()

const initMutation = useInitializeAppMutation(appId)
// trackingJobId 记录最近一次后台任务，供 JobProgressPanel 轮询展示执行进度。
const trackingJobId = ref<string | undefined>()
const jobIdRef = computed<string | undefined>(() => trackingJobId.value)
const jobQuery = useJobQuery(jobIdRef)
const trackedJob = computed(() => jobQuery.data.value ?? null)

// canRetryInit 仅允许可管理用户在草稿或错误状态重新提交初始化任务。
const canRetryInit = computed(() => {
  const status = app?.value?.status
  return canManageApp(auth.user, app?.value) && (status === 'error' || status === 'draft')
})

const initFeedback = ref('')
const initError = ref(false)

// onRetryInit 提交初始化任务，成功后切换到任务跟踪视图，失败时保留错误文案。
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

// canToggleKey 控制 API key 启停按钮展示，避免非管理者看到危险操作入口。
const canToggleKey = computed(() => canManageApp(auth.user, app?.value))

const keyMutation = useToggleAppAPIKey(appId)
const confirmDisableKey = ref(false)
const keyFeedback = ref('')
const keyError = ref(false)

// keyTagType 将 API key 状态映射为标签色，未知状态用 warning 提醒确认。
function keyTagType(s: string): 'success' | 'warning' | 'error' | 'default' {
  return s === 'active' ? 'success' : s === 'disabled' ? 'error' : 'warning'
}

// apiKeyLabel 将后端 key 状态转换为用户可读文案，未知状态保留原值。
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

// runKeyMutation 提交 disable/restore 后端任务；任务完成由 JobProgressPanel 继续轮询。
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
