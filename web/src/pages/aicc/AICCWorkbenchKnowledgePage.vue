<template>
  <div v-if="context?.agentsLoading.value" class="knowledge-inline-state" role="status">
    {{ t('aicc.console.knowledgeRedirect.loading') }}
  </div>
  <div v-else-if="!appId" class="knowledge-inline-state" role="status">
    {{ t('aicc.console.knowledgeRedirect.noAgent') }}
  </div>
  <div v-else-if="appQuery.isLoading.value" class="knowledge-inline-state" role="status">
    {{ t('aicc.console.knowledgeRedirect.loading') }}
  </div>
  <div v-else-if="appQuery.error.value" class="knowledge-inline-state danger" role="alert">
    {{ t('aicc.console.knowledgeRedirect.loadFailed') }}
  </div>
  <AppKnowledgeTab v-else :app-id="appId" />
</template>

<script setup lang="ts">
import { computed, inject, provide } from 'vue'
import { useI18n } from 'vue-i18n'

import { useAppQuery, type AppDTO } from '@/api/hooks/useApps'
import AppKnowledgeTab from '@/pages/apps/AppKnowledgeTab.vue'
import { AICCConsoleContextKey } from './aiccConsoleContext'

// AICCWorkbenchKnowledgePage 在工作台内复用实例知识库组件；路由保持在 /aicc-console/knowledge。
const { t } = useI18n()
const context = inject(AICCConsoleContextKey)
const selectedAgent = computed(() => context?.selectedAgent.value)
const appId = computed(() => selectedAgent.value?.app_id)
const appQuery = useAppQuery(appId)
const app = computed<AppDTO | null>(() => appQuery.data.value ?? null)

// AppKnowledgeTab 的上传权限依赖上层注入的 app 详情；这里用隐藏实例补齐同一契约。
provide('app', app)
</script>

<style scoped>
.knowledge-inline-state {
  display: grid;
  min-height: 240px;
  place-items: center;
  color: var(--color-text-secondary);
  font-size: 14px;
}

.knowledge-inline-state.danger {
  color: var(--color-danger);
}
</style>
