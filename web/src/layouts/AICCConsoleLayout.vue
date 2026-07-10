<template>
  <n-layout class="aicc-console-layout">
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
import { NLayout, NLayoutContent } from 'naive-ui'

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

</script>

<style scoped>
.aicc-console-layout {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--color-bg);
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
  .aicc-console-content {
    padding: 12px;
  }
}
</style>
