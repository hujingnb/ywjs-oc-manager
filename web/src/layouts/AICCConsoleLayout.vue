<template>
  <n-layout has-sider class="aicc-console-layout">
    <n-layout-sider
      bordered
      :width="232"
      content-style="display: flex; flex-direction: column; height: 100%"
    >
      <div class="aicc-brand">
        <div class="aicc-brand-mark">AI</div>
        <div>
          <p>{{ t('aicc.console.eyebrow') }}</p>
          <strong>{{ t('aicc.console.title') }}</strong>
        </div>
      </div>

      <!-- AICC 工作台内部导航独立于主后台菜单，后续分区路由可在此扩展。 -->
      <n-menu
        :value="activeKey"
        :options="navOptions"
        :indent="16"
        style="flex: 1; min-height: 0; overflow-y: auto"
        @update:value="onNav"
      />
    </n-layout-sider>

    <n-layout>
      <n-layout-header bordered class="aicc-console-header">
        <div class="aicc-title">
          <p>{{ t('aicc.console.eyebrow') }}</p>
          <h1>{{ t('aicc.console.title') }}</h1>
        </div>
        <div class="aicc-header-actions">
          <LocaleSwitcher :persist="true" />
          <n-button secondary @click="returnToOverview">
            <template #icon><ArrowLeft :size="16" /></template>
            {{ t('aicc.console.returnToOverview') }}
          </n-button>
        </div>
      </n-layout-header>

      <n-layout-content content-style="height: calc(100vh - 64px); padding: 24px; display: flex; flex-direction: column; overflow: auto">
        <section class="aicc-content-frame" :aria-label="t('aicc.console.navLabel')">
          <RouterView />
        </section>
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NButton, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu,
  type MenuOption,
} from 'naive-ui'
import { ArrowLeft, BarChart3, BookOpen, LayoutDashboard, MessageSquare, Settings, Users } from 'lucide-vue-next'

import LocaleSwitcher from '@/components/LocaleSwitcher.vue'

interface ConsoleNavItem {
  path: string
  labelKey: string
  icon: typeof LayoutDashboard
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

// navItems 是 AICC 工作台的信息架构入口；暂未拆页的分区先保留导航目标，路由在后续任务扩展。
const navItems: ConsoleNavItem[] = [
  { path: '/aicc-console', labelKey: 'aicc.console.nav.reception', icon: LayoutDashboard },
  { path: '/aicc-console/sessions', labelKey: 'aicc.console.nav.sessions', icon: MessageSquare },
  { path: '/aicc-console/knowledge', labelKey: 'aicc.console.nav.knowledge', icon: BookOpen },
  { path: '/aicc-console/leads', labelKey: 'aicc.console.nav.leads', icon: Users },
  { path: '/aicc-console/analytics', labelKey: 'aicc.console.nav.analytics', icon: BarChart3 },
  { path: '/aicc-console/settings', labelKey: 'aicc.console.nav.settings', icon: Settings },
]

// activeKey 按当前路径高亮工作台内部导航；根路径需精确匹配，避免吞掉所有子路径。
const activeKey = computed(() => {
  if (route.path === '/aicc-console') return '/aicc-console'
  return navItems.find(item => route.path === item.path || route.path.startsWith(`${item.path}/`))?.path ?? '/aicc-console'
})

// navOptions 随语言切换重新计算 label，同时保留 lucide 图标渲染函数给 Naive Menu 使用。
const navOptions = computed<MenuOption[]>(() => navItems.map(item => ({
  key: item.path,
  label: t(item.labelKey),
  icon: () => h(item.icon, { size: 18 }),
})))

function onNav(key: string) {
  router.push(key)
}

function returnToOverview() {
  router.push('/')
}
</script>

<style scoped>
.aicc-console-layout {
  height: 100vh;
  background: var(--color-bg);
}

.aicc-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 64px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--color-divider);
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

.aicc-brand p,
.aicc-title p {
  margin: 0 0 2px;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.2;
}

.aicc-brand strong {
  display: block;
  color: var(--color-text-primary);
  font-size: 15px;
  line-height: 1.3;
}

.aicc-console-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 64px;
  padding: 0 24px;
  background: var(--color-surface);
}

.aicc-title h1 {
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

.aicc-content-frame {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}

.aicc-content-frame :deep(> *) {
  flex: 1;
  min-height: 0;
}
</style>
