<template>
  <section class="aicc-console-workspace" :aria-label="t('aicc.console.navLabel')">
    <header class="aicc-workspace-topbar" data-test="workspace-topbar">
      <div class="aicc-brand" data-test="workspace-brand">
        <div class="aicc-brand-mark">AI</div>
        <div>
          <p>{{ t('aicc.console.eyebrow') }}</p>
          <h1>{{ t('aicc.console.title') }}</h1>
        </div>
      </div>

      <section class="aicc-agent-context" data-test="workspace-agent-bar" :aria-label="t('aicc.console.currentAgent')">
        <n-select
          v-if="isPlatformAdmin"
          v-model:value="selectedOrgIdModel"
          class="aicc-org-select"
          data-test="org-switcher"
          size="small"
          :options="organizationOptions"
          :loading="organizationsLoading"
          :placeholder="t('aicc.console.switchOrganization')"
        />

        <n-select
          v-model:value="selectedAgentIdModel"
          class="aicc-agent-select"
          data-test="agent-switcher"
          size="small"
          :options="agentOptions"
          :loading="agentsLoading"
          :placeholder="t('aicc.console.switchAgent')"
        />

        <div class="aicc-agent-summary">
          <p>{{ t('aicc.console.currentAgent') }}</p>
          <strong>{{ selectedAgent?.name || t('aicc.console.noAgentSelected') }}</strong>
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
        </div>

        <n-button v-if="!isPlatformAdmin" size="small" type="primary" secondary @click="startCreateAgent">
          <template #icon><Plus :size="15" /></template>
          {{ t('aicc.console.createAgent') }}
        </n-button>
      </section>

      <div class="aicc-header-actions">
        <LocaleSwitcher :persist="true" />
        <n-button secondary @click="returnToOverview">
          <template #icon><ArrowLeft :size="16" /></template>
          {{ t('aicc.console.returnToOverview') }}
        </n-button>
      </div>
    </header>

    <div class="aicc-workspace-shell">
      <nav class="aicc-workspace-nav" data-test="workspace-module-menu" :aria-label="t('aicc.console.navLabel')">
        <!-- 左侧模块菜单只负责 AICC 工作区内分区切换，外层启用门禁仍由 AICCConsoleLayout 控制。 -->
        <a
          v-for="item in navItems"
          :key="item.path"
          class="aicc-workspace-nav-item"
          :class="{ active: activeKey === item.path }"
          :href="resolveNavTarget(item)"
          data-test="workspace-nav-item"
          @click.prevent="navigateTo(item)"
        >
          <component :is="item.icon" :size="16" />
          <span>{{ t(item.labelKey) }}</span>
        </a>
      </nav>

      <main class="aicc-workspace-content">
        <RouterView />
      </main>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, provide, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton, NSelect, NTag, type SelectOption } from 'naive-ui'
import { ArrowLeft, BarChart3, BookOpen, LayoutDashboard, MessageSquare, Plus, Settings, Users } from 'lucide-vue-next'

import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import { useAICCAgentsQuery } from '@/api/hooks/useAICC'
import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import type { Organization } from '@/api'
import type { AICCAgent } from '@/domain/aicc'
import { AICCConsoleContextKey } from '@/pages/aicc/aiccConsoleContext'
import { useAuthStore } from '@/stores/auth'

interface WorkspaceNavItem {
  path: string
  labelKey: string
  icon: typeof LayoutDashboard
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')
const selectedOrgIdState = ref<string | undefined>()
const selectedAgentIdState = ref<string | undefined>()
const isCreatingAgent = ref(false)
const organizationsQuery = useOrganizationsQuery(() => isPlatformAdmin.value)
const selectedOrgIdForAgents = computed(() => isPlatformAdmin.value ? selectedOrgIdState.value : undefined)
const agentsQuery = useAICCAgentsQuery(selectedOrgIdForAgents, () => !isPlatformAdmin.value || Boolean(selectedOrgIdState.value))

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
const organizations = computed<Organization[]>(() => organizationsQuery.data.value ?? [])
const aiccOrganizations = computed(() => organizations.value.filter(org => org.aicc_enabled === true))
const organizationsLoading = computed(() => organizationsQuery.isLoading.value)
const agentsLoading = computed(() => agentsQuery.isLoading.value)
const agentsError = computed<Error | null>(() => agentsQuery.error.value instanceof Error ? agentsQuery.error.value : null)
const selectedOrgId = computed(() => selectedOrgIdState.value)
const selectedAgentId = computed(() => selectedAgentIdState.value)
const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentIdState.value))
const requestedOrgId = computed(() => typeof route.query.org_id === 'string' ? route.query.org_id : undefined)
const organizationOptions = computed<SelectOption[]>(() => aiccOrganizations.value.map(org => ({
  label: org.name || org.code || org.id,
  value: org.id,
})))
const agentOptions = computed<SelectOption[]>(() => agents.value.map(agent => ({
  label: agent.name || t('aicc.console.noAgentSelected'),
  value: agent.id,
})))

// selectedOrgIdModel 只在平台管理员视角可写；切换企业时清空当前智能体，避免继续展示上一企业数据。
const selectedOrgIdModel = computed<string | null>({
  get: () => selectedOrgIdState.value ?? null,
  set: (orgId?: string | null) => {
    selectedOrgIdState.value = orgId ?? undefined
    selectedAgentIdState.value = undefined
    isCreatingAgent.value = false
  },
})

// selectedAgentIdModel 是选择器的可写桥接层；注入给子页面的 selectedAgentId 保持 ComputedRef 只读。
const selectedAgentIdModel = computed<string | null>({
  get: () => selectedAgentIdState.value ?? null,
  set: (agentId?: string | null) => selectAgent(agentId ?? undefined),
})

// activeKey 对根路径做精确匹配，避免 /aicc-console 吞掉所有子模块高亮。
const activeKey = computed(() => {
  if (route.path === '/aicc-console') return '/aicc-console'
  const matchedChild = navItems
    .filter(item => item.path !== '/aicc-console')
    .find(item => route.path === item.path || route.path.startsWith(`${item.path}/`))
  return matchedChild?.path ?? '/aicc-console'
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

// 平台管理员首次进入时默认落到第一个已开通 AICC 的企业；企业列表变化时避免保留已失效企业。
watch(
  aiccOrganizations,
  (items) => {
    if (!isPlatformAdmin.value) {
      selectedOrgIdState.value = undefined
      return
    }
    if (selectedOrgIdState.value && items.some(org => org.id === selectedOrgIdState.value)) return
    const requestedOrg = requestedOrgId.value && items.some(org => org.id === requestedOrgId.value)
      ? requestedOrgId.value
      : undefined
    selectedOrgIdState.value = requestedOrg ?? items[0]?.id
    selectedAgentIdState.value = undefined
    isCreatingAgent.value = false
  },
  { immediate: true },
)

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
  selectedOrgId,
  isPlatformAdmin,
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

function resolveNavTarget(item: WorkspaceNavItem) {
  return item.path
}

function navigateTo(item: WorkspaceNavItem) {
  void router.push(resolveNavTarget(item))
}

function returnToOverview() {
  void router.push('/')
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

.aicc-workspace-topbar {
  display: grid;
  grid-template-columns: minmax(220px, auto) minmax(520px, 1fr) auto;
  gap: 18px;
  align-items: center;
  min-height: 72px;
  padding: 12px 22px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
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
  width: 38px;
  height: 38px;
  place-items: center;
  border-radius: 7px;
  background: var(--color-brand);
  color: var(--color-on-brand);
  font-size: 13px;
  font-weight: 800;
}

.aicc-brand p,
.aicc-agent-summary p {
  margin: 0 0 2px;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.2;
}

.aicc-brand h1 {
  margin: 0;
  overflow: hidden;
  color: var(--color-text-primary);
  font-size: 19px;
  font-weight: 750;
  letter-spacing: 0;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.aicc-workspace-shell {
  display: grid;
  min-width: 0;
  min-height: 0;
  flex: 1;
  grid-template-columns: 212px minmax(0, 1fr);
  overflow: hidden;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.aicc-workspace-nav {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  gap: 4px;
  padding: 12px;
  border-right: 1px solid var(--color-divider);
  background: var(--color-surface-muted);
}

.aicc-workspace-nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 40px;
  padding: 0 12px;
  border-radius: 6px;
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0;
  text-decoration: none;
}

.aicc-workspace-nav-item:hover,
.aicc-workspace-nav-item.active {
  background: var(--color-brand-soft);
  box-shadow: inset 3px 0 0 var(--color-brand);
  color: var(--color-brand-text);
}

.aicc-agent-context {
  display: grid;
  grid-template-columns: minmax(260px, 380px) minmax(280px, 1fr) auto;
  gap: 12px;
  align-items: center;
  min-width: 0;
  min-height: 56px;
  padding: 10px 14px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.aicc-agent-summary,
.aicc-agent-meta {
  min-width: 0;
}

.aicc-agent-summary strong {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  color: var(--color-text-primary);
  font-size: 13px;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.aicc-agent-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  color: var(--color-text-secondary);
  font-size: 12px;
}

.aicc-agent-meta span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.aicc-header-actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
}

.aicc-agent-select {
  width: min(320px, 100%);
}

.aicc-workspace-content {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  padding: 16px;
  overflow: auto;
}

.aicc-workspace-content :deep(> *) {
  flex: 1;
  min-height: 0;
}

@media (max-width: 1100px) {
  .aicc-workspace-topbar {
    grid-template-columns: 1fr;
  }

  .aicc-agent-context {
    grid-template-columns: 1fr;
  }

  .aicc-header-actions {
    justify-content: flex-start;
  }

  .aicc-agent-select {
    flex: 1 1 180px;
  }
}

@media (max-width: 760px) {
  .aicc-workspace-shell {
    grid-template-columns: 1fr;
  }

  .aicc-workspace-nav {
    flex-direction: row;
    overflow-x: auto;
    border-right: 0;
    border-bottom: 1px solid var(--color-divider);
  }

  .aicc-workspace-nav-item {
    flex: 0 0 auto;
  }
}
</style>
