<template>
  <n-layout class="aicc-console-layout">
    <n-layout-header bordered class="aicc-console-header">
      <div class="aicc-brand">
        <div class="aicc-brand-mark">AI</div>
        <div>
          <p>{{ t('aicc.console.eyebrow') }}</p>
          <h1>{{ t('aicc.console.title') }}</h1>
        </div>
      </div>

      <div class="aicc-header-actions">
        <LocaleSwitcher :persist="true" />
        <n-button secondary @click="returnToOverview">
          <template #icon><ArrowLeft :size="16" /></template>
          {{ t('aicc.console.returnToOverview') }}
        </n-button>
      </div>
    </n-layout-header>

    <n-layout-content content-style="flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: auto">
      <section class="aicc-console-content" :aria-label="t('aicc.console.title')">
        <div v-if="orgLoading" class="aicc-loading-state" role="status">
          {{ t('aicc.console.checkingAccess') }}
        </div>
        <AICCConsoleWorkspace v-else-if="canRenderConsole" />
      </section>
    </n-layout-content>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton, NLayout, NLayoutContent, NLayoutHeader } from 'naive-ui'
import { ArrowLeft } from 'lucide-vue-next'

import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
import { useAuthStore } from '@/stores/auth'
import AICCConsoleWorkspace from './AICCConsoleWorkspace.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const ownOrgId = computed(() => auth.user?.role === 'org_admin' ? auth.user.org_id ?? undefined : undefined)
const { data: ownOrganization, isLoading: orgLoading } = useOrganizationQuery(ownOrgId)
// canRenderConsole 在企业开通状态确认前保持 false，避免子页面提前挂载并发起 AICC API 请求。
const canRenderConsole = computed(() => !orgLoading.value && ownOrganization.value?.aicc_enabled === true)

// AICC 子系统入口由企业开通状态控制；直接访问 /aicc-console 时也必须兜底拦截未开通企业。
watch(
  () => ({ loading: orgLoading.value, enabled: ownOrganization.value?.aicc_enabled }),
  ({ loading, enabled }) => {
    if (!loading && enabled === false) {
      void router.replace('/')
    }
  },
  { immediate: true },
)

function returnToOverview() {
  router.push('/')
}
</script>

<style scoped>
.aicc-console-layout {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--color-bg);
}

.aicc-console-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  min-height: 64px;
  padding: 0 24px;
  background: var(--color-surface);
}

.aicc-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.aicc-brand-mark {
  display: grid;
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 6px;
  background: #0f766e;
  color: #ffffff;
  font-size: 13px;
  font-weight: 800;
}

.aicc-brand p {
  margin: 0 0 2px;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.2;
}

.aicc-brand h1 {
  margin: 0;
  color: var(--color-text-primary);
  font-size: 20px;
  font-weight: 700;
  letter-spacing: 0;
}

.aicc-header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.aicc-console-content {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  box-sizing: border-box;
  padding: 16px 20px;
}

.aicc-console-content :deep(> *) {
  flex: 1;
  min-height: 0;
}

.aicc-loading-state {
  display: grid;
  flex: 1;
  min-height: 240px;
  place-items: center;
  color: var(--color-text-secondary);
  font-size: 14px;
}

@media (max-width: 640px) {
  .aicc-console-header {
    padding: 12px;
  }

  .aicc-console-content {
    padding: 12px;
  }
}
</style>
