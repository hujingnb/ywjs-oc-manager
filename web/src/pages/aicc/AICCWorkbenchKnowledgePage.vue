<template>
  <div class="knowledge-redirect-state" role="status">
    {{ statusText }}
  </div>
</template>

<script setup lang="ts">
import { computed, inject, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { AICCConsoleContextKey } from './aiccConsoleContext'

// AICCWorkbenchKnowledgePage 兼容旧的 /aicc-console/knowledge 子路由；
// 当前知识库管理统一复用绑定实例的 /apps/:appId/knowledge 页面，避免维护两套上传/管理入口。
const router = useRouter()
const { t } = useI18n()
const context = inject(AICCConsoleContextKey)
const selectedAgent = computed(() => context?.selectedAgent.value)
const statusText = computed(() => (
  context?.agentsLoading.value
    ? t('aicc.console.knowledgeRedirect.loading')
    : t('aicc.console.knowledgeRedirect.noAgent')
))

watch(
  () => selectedAgent.value?.app_id,
  (appId) => {
    if (!appId) return
    void router.replace(`/apps/${appId}/knowledge`)
  },
  { immediate: true },
)
</script>

<style scoped>
.knowledge-redirect-state {
  display: grid;
  min-height: 240px;
  place-items: center;
  color: var(--color-text-secondary);
  font-size: 14px;
}
</style>
