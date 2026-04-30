<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">App · Detail</p>
          <h2>
            {{ app?.name ?? '应用详情' }}
            <small v-if="app">· {{ app.id }}</small>
          </h2>
        </div>
        <AppStatusTag v-if="app" :status="app.status" />
      </div>

      <p v-if="appQuery.isLoading.value" class="state-text">加载中…</p>
      <p v-else-if="appQuery.error.value" class="state-text danger">查询失败：{{ appQuery.error.value?.message }}</p>

      <nav class="tab-nav" v-if="app">
        <RouterLink
          v-for="tab in tabs"
          :key="tab.path"
          :to="{ path: tabPath(tab.path) }"
          class="tab-link"
          active-class="tab-link-active"
        >
          {{ tab.label }}
        </RouterLink>
      </nav>
    </section>

    <RouterView v-if="app" :app-id="app.id" />
  </main>
</template>

<script setup lang="ts">
import { computed, provide } from 'vue'
import { useRoute, RouterLink, RouterView } from 'vue-router'

import { useAppQuery, type AppDTO } from '@/api/hooks/useApps'
import AppStatusTag from '@/components/AppStatusTag.vue'

const route = useRoute()

// 通过路由 param 拿 appId，把它包成 ref 以满足 useAppQuery 的签名。
const appIdRef = computed(() => route.params.appId as string | undefined)
const appQuery = useAppQuery(appIdRef)
const app = computed<AppDTO | null>(() => appQuery.data.value ?? null)

// provide 把 app 注入给子 tab，避免每个 tab 都重复 useAppQuery 拉一次。
provide<typeof app>('app', app)

const tabs: ReadonlyArray<{ path: string; label: string }> = [
  { path: 'overview', label: '概览' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '应用知识库' },
  { path: 'workspace', label: '工作目录' },
]

function tabPath(name: string) {
  if (!appIdRef.value) return ''
  return `/apps/${appIdRef.value}/${name}`
}
</script>

<style scoped>
.tab-nav {
  display: flex;
  gap: 12px;
  border-bottom: 1px solid rgba(0, 0, 0, 0.08);
  padding: 8px 0 0;
  margin-top: 12px;
}

.tab-link {
  padding: 8px 14px;
  border-radius: 6px 6px 0 0;
  color: var(--color-text);
  text-decoration: none;
  font-weight: 500;
}

.tab-link-active {
  background: rgba(0, 0, 0, 0.04);
  border-bottom: 2px solid var(--color-primary, #2470ff);
}
</style>
