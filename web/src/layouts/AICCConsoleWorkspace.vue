<template>
  <section class="aicc-console-workspace" :aria-label="t('aicc.console.navLabel')">
    <nav class="aicc-workspace-nav" :aria-label="t('aicc.console.navLabel')">
      <!-- 顶部模块导航只负责 AICC 工作区内分区切换，外层启用门禁仍由 AICCConsoleLayout 控制。 -->
      <a
        v-for="item in navItems"
        :key="item.path"
        class="aicc-workspace-nav-item"
        :class="{ active: activeKey === item.path }"
        :href="item.path"
        data-test="workspace-nav-item"
        @click.prevent="navigateTo(item.path)"
      >
        <component :is="item.icon" :size="16" />
        <span>{{ t(item.labelKey) }}</span>
      </a>
    </nav>

    <header class="aicc-agent-context">
      <div class="aicc-agent-identity">
        <span>{{ t('aicc.console.currentAgent') }}</span>
        <strong>{{ selectedAgent?.name || t('aicc.console.noAgentSelected') }}</strong>
      </div>

      <div class="aicc-agent-meta" aria-live="polite">
        <span v-if="agentsLoading">{{ t('aicc.console.agentLoading') }}</span>
        <span v-else-if="agentsError">{{ t('aicc.console.agentLoadFailed') }}</span>
        <template v-else>
          <span>
            {{ t('aicc.manager.status.runtime') }}
            <n-tag size="small" :type="selectedAgentStatusType">{{ selectedAgentStatusText }}</n-tag>
          </span>
          <span>
            {{ t('aicc.manager.status.publicEntry') }}
            <n-tag size="small" :type="selectedAgent?.public_token ? 'success' : 'default'">
              {{ selectedAgentPublicEntryText }}
            </n-tag>
          </span>
          <span>
            {{ t('aicc.manager.status.retention') }}
            <strong>{{ selectedAgentRetentionText }}</strong>
          </span>
        </template>
      </div>

      <div class="aicc-agent-actions">
        <n-select
          v-model:value="selectedAgentIdModel"
          class="aicc-agent-select"
          size="small"
          :options="agentOptions"
          :loading="agentsLoading"
          :placeholder="t('aicc.console.switchAgent')"
          clearable
        />
        <n-button size="small" type="primary" secondary @click="startCreateAgent">
          <template #icon><Plus :size="15" /></template>
          {{ t('aicc.console.createAgent') }}
        </n-button>
      </div>
    </header>

    <main class="aicc-workspace-content">
      <RouterView />
    </main>
  </section>
</template>

<script setup lang="ts">
import { computed, provide, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton, NSelect, NTag, type SelectOption } from 'naive-ui'
import { BarChart3, BookOpen, LayoutDashboard, MessageSquare, Plus, Settings, Users } from 'lucide-vue-next'

import { useAICCAgentsQuery } from '@/api/hooks/useAICC'
import type { AICCAgent } from '@/domain/aicc'
import { AICCConsoleContextKey } from '@/pages/aicc/aiccConsoleContext'

interface WorkspaceNavItem {
  path: string
  labelKey: string
  icon: typeof LayoutDashboard
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const selectedAgentIdState = ref<string | undefined>()
const isCreatingAgent = ref(false)
const agentsQuery = useAICCAgentsQuery()

// 顶部导航按工作台信息架构排序；路径与后续子页面路由保持一一对应。
const navItems: WorkspaceNavItem[] = [
  { path: '/aicc-console', labelKey: 'aicc.console.nav.reception', icon: LayoutDashboard },
  { path: '/aicc-console/sessions', labelKey: 'aicc.console.nav.sessions', icon: MessageSquare },
  { path: '/aicc-console/leads', labelKey: 'aicc.console.nav.leads', icon: Users },
  { path: '/aicc-console/knowledge', labelKey: 'aicc.console.nav.knowledge', icon: BookOpen },
  { path: '/aicc-console/analytics', labelKey: 'aicc.console.nav.analytics', icon: BarChart3 },
  { path: '/aicc-console/settings', labelKey: 'aicc.console.nav.settings', icon: Settings },
]

const agents = computed(() => agentsQuery.data.value ?? [])
const agentsLoading = computed(() => agentsQuery.isLoading.value)
const agentsError = computed<Error | null>(() => agentsQuery.error.value instanceof Error ? agentsQuery.error.value : null)
const selectedAgentId = computed(() => selectedAgentIdState.value)
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentIdState.value))
const agentOptions = computed<SelectOption[]>(() => agents.value.map(agent => ({
  label: agent.name || t('aicc.console.noAgentSelected'),
  value: agent.id,
})))

// selectedAgentIdModel 是选择器的可写桥接层；注入给子页面的 selectedAgentId 保持 ComputedRef 只读。
const selectedAgentIdModel = computed({
  get: () => selectedAgentIdState.value,
  set: (agentId?: string) => selectAgent(agentId),
})

// activeKey 对根路径做精确匹配，避免 /aicc-console 吞掉所有子模块高亮。
const activeKey = computed(() => {
  if (route.path === '/aicc-console') return '/aicc-console'
  return navItems.find(item => route.path === item.path || route.path.startsWith(`${item.path}/`))?.path ?? '/aicc-console'
})

const selectedAgentStatusText = computed(() => {
  switch (selectedAgent.value?.status) {
    case 'active':
      return t('aicc.manager.status.active')
    case 'paused':
      return t('aicc.manager.status.paused')
    case 'deleted':
      return t('aicc.manager.status.deleted')
    case 'draft':
      return t('aicc.manager.status.draft')
    default:
      return t('aicc.console.noAgentSelected')
  }
})

const selectedAgentStatusType = computed(() => {
  if (selectedAgent.value?.status === 'active') return 'success'
  if (selectedAgent.value?.status === 'paused') return 'warning'
  if (selectedAgent.value?.status === 'deleted') return 'error'
  return 'default'
})

const selectedAgentPublicEntryText = computed(() => (
  selectedAgent.value?.public_token ? t('aicc.manager.status.generated') : t('aicc.manager.status.generatedAfterSave')
))

const selectedAgentRetentionText = computed(() => {
  if (!selectedAgent.value) return t('aicc.console.noAgentSelected')
  return t('aicc.manager.status.days', { count: selectedAgent.value.retention_days || 0 })
})

// 智能体列表首次返回时默认进入第一个智能体；用户点击“新建智能体”后保留未选择态供子页面创建表单使用。
watch(
  agents,
  (items) => {
    if (items.length === 0) {
      selectedAgentIdState.value = undefined
      isCreatingAgent.value = false
      return
    }
    if (selectedAgentIdState.value && items.some(agent => agent.id === selectedAgentIdState.value)) return
    if (!isCreatingAgent.value) {
      selectedAgentIdState.value = items[0].id
    }
  },
  { immediate: true },
)

provide(AICCConsoleContextKey, {
  agents,
  selectedAgentId,
  selectedAgent,
  agentsLoading,
  agentsError,
  selectAgent,
  startCreateAgent,
})

function selectAgent(agentId?: string) {
  selectedAgentIdState.value = agentId
  isCreatingAgent.value = false
}

function startCreateAgent() {
  selectedAgentIdState.value = undefined
  isCreatingAgent.value = true
}

function navigateTo(path: string) {
  void router.push(path)
}
</script>

<style scoped>
.aicc-console-workspace {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 12px;
}

.aicc-workspace-nav {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: 4px;
  overflow-x: auto;
  border-bottom: 1px solid var(--color-divider);
}

.aicc-workspace-nav-item {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  padding: 0 12px;
  border-bottom: 2px solid transparent;
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0;
  text-decoration: none;
}

.aicc-workspace-nav-item:hover,
.aicc-workspace-nav-item.active {
  border-bottom-color: #0f766e;
  color: var(--color-text-primary);
}

.aicc-agent-context {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) minmax(320px, 2fr) auto;
  gap: 16px;
  align-items: center;
  min-height: 58px;
  padding: 10px 14px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface);
}

.aicc-agent-identity,
.aicc-agent-meta {
  min-width: 0;
}

.aicc-agent-identity span {
  display: block;
  margin-bottom: 2px;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.2;
}

.aicc-agent-identity strong {
  display: block;
  overflow: hidden;
  color: var(--color-text-primary);
  font-size: 15px;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.aicc-agent-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 14px;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.aicc-agent-meta span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.aicc-agent-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.aicc-agent-select {
  width: 196px;
}

.aicc-workspace-content {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
}

.aicc-workspace-content :deep(> *) {
  flex: 1;
  min-height: 0;
}

@media (max-width: 900px) {
  .aicc-agent-context {
    grid-template-columns: 1fr;
  }

  .aicc-agent-actions {
    justify-content: flex-start;
  }
}
</style>
