<template>
  <!-- WebPublishCertPanel 展示企业 web-publish 配置的通配证书状态。
       canRetry=true 时（平台管理员）显示「重试签发/续签」按钮；
       canRetry=false 时（企业管理员）仅查看，不显示写操作入口。 -->
  <n-card :title="t('platform.webPublishCert.title')">
    <n-spin :show="isLoading">
      <n-alert
        v-if="errorMessage"
        type="error"
        :show-icon="false"
        style="margin-bottom: 12px"
      >
        {{ errorMessage }}
      </n-alert>

      <template v-if="config">
        <n-descriptions :column="2" label-placement="left" bordered>
          <!-- 通配域名 -->
          <n-descriptions-item :label="t('platform.webPublishCert.wildcardDomain')">
            <code>{{ config.wildcard_domain ?? '—' }}</code>
          </n-descriptions-item>

          <!-- 证书状态：带颜色的状态徽章 -->
          <n-descriptions-item :label="t('platform.webPublishCert.certStatus')">
            <StatusBadge :view="certStatusView" />
          </n-descriptions-item>

          <!-- 证书到期时间 -->
          <n-descriptions-item :label="t('platform.webPublishCert.certNotAfter')">
            {{ config.cert_not_after ? new Date(config.cert_not_after).toLocaleString() : '—' }}
          </n-descriptions-item>

          <!-- 最近首签成功时间 -->
          <n-descriptions-item :label="t('platform.webPublishCert.certLastIssuedAt')">
            {{ config.cert_last_issued_at ? new Date(config.cert_last_issued_at).toLocaleString() : '—' }}
          </n-descriptions-item>

          <!-- 最近续签成功时间 -->
          <n-descriptions-item :label="t('platform.webPublishCert.certLastRenewedAt')">
            {{ config.cert_last_renewed_at ? new Date(config.cert_last_renewed_at).toLocaleString() : '—' }}
          </n-descriptions-item>

          <!-- 失败原因：仅在 cert_message 非空时展示 -->
          <n-descriptions-item
            v-if="config.cert_message"
            :label="t('platform.webPublishCert.certMessage')"
            :span="2"
          >
            <span style="color: var(--n-color-danger, #d03050)">{{ config.cert_message }}</span>
          </n-descriptions-item>
        </n-descriptions>

        <!-- 重试按钮：仅平台管理员（canRetry=true）可见，
             用于手动触发证书签发/续签重试。 -->
        <div v-if="canRetry" style="margin-top: 16px; display: flex; justify-content: flex-end">
          <n-button
            type="warning"
            :loading="retryCert.isPending.value"
            :disabled="retryCert.isPending.value"
            @click="onRetryCert"
          >
            {{ t('platform.webPublishCert.retryButton') }}
          </n-button>
        </div>

        <!-- 重试结果反馈：成功/失败均在卡片内联展示，避免全局 message 闪烁 -->
        <p v-if="retryFeedback" class="state-text" :class="{ danger: retryError }">
          {{ retryFeedback }}
        </p>
      </template>

      <!-- 未加载到配置时的空状态提示 -->
      <p v-else-if="!isLoading" class="state-text">
        {{ t('platform.webPublishCert.noConfig') }}
      </p>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDescriptions, NDescriptionsItem, NAlert, NSpin } from 'naive-ui'

import { useWebPublishConfigQuery, useRetryCert } from '@/api/hooks/useWebPublish'
import StatusBadge from '@/components/StatusBadge.vue'
import { formatCertStatus } from '@/domain/status'

// WebPublishCertPanel 展示证书当前状态，平台管理员可触发重试。
// orgId 由父页面传入（普通 prop），内部包装为 computed ref 供 hooks 订阅。
// canRetry 由父页面根据登录角色决定（平台管理员 true，企业管理员 false）。
const props = defineProps<{
  // 当前展示证书信息的企业 ID（普通字符串 prop，非 undefined 时 query 才激活）。
  orgId: string | undefined
  // true = 平台管理员视角（可重试签发/续签）；false = 企业管理员（只读）。
  canRetry: boolean
}>()

const { t } = useI18n()

// orgIdRef 把普通 prop 包装为 ComputedRef，满足 hooks 对 Ref<string|undefined> 的要求。
const orgIdRef = computed(() => props.orgId)

// 查询 web-publish 配置（含证书状态）。
const { data: config, isLoading, error } = useWebPublishConfigQuery(orgIdRef)

// 重试证书签发/续签 mutation，仅 canRetry=true 时会被调用。
const retryCert = useRetryCert(orgIdRef)

// retryFeedback/retryError 保存重试操作的内联反馈，避免全局 message 闪烁。
const retryFeedback = ref('')
const retryError = ref(false)

// errorMessage 将查询错误转为字符串展示。
const errorMessage = computed(() => error.value ? String(error.value) : undefined)

// certStatusView 将后端 cert_status 原值映射为带颜色的状态视图。
// config 为 null/undefined 时降级到 none，避免组件崩溃。
const certStatusView = computed(() => formatCertStatus(config.value?.cert_status ?? 'none'))

// onRetryCert 调用重试接口并展示内联结果；接口成功后缓存由 hook 自动失效。
async function onRetryCert() {
  retryFeedback.value = ''
  retryError.value = false
  try {
    await retryCert.mutateAsync()
    retryFeedback.value = t('platform.webPublishCert.retrySuccess')
  } catch (err) {
    retryError.value = true
    retryFeedback.value = err instanceof Error ? err.message : t('platform.webPublishCert.retryError')
  }
}
</script>

<style scoped>
/* state-text：操作结果内联反馈，danger 变体表示失败。 */
.state-text {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--color-text-secondary, #6b7280);
}
.state-text.danger {
  color: var(--n-color-danger, #d03050);
}
</style>
