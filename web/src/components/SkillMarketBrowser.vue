<template>
  <!-- SkillMarketBrowser：平台库 + ClawHub 聚合市场浏览，供实例安装与助手版本选 skill 共用。 -->
  <div class="skill-market-browser">
    <!-- 筛选工具栏：来源 tag 切换 + 关键词搜索。 -->
    <div class="market-toolbar">
      <div class="market-filters">
        <n-tag
          v-for="filter in sourceFilters"
          :key="filter.value"
          :type="selectedSource === filter.value ? 'primary' : 'default'"
          :bordered="false"
          checkable
          :checked="selectedSource === filter.value"
          class="filter-tag"
          @click="selectedSource = filter.value"
        >
          {{ filter.label }}
        </n-tag>
      </div>
      <n-input v-model:value="searchText" placeholder="搜索技能名称…" clearable size="small" class="market-search" />
    </div>

    <div v-if="skillMarketQuery.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="skillMarketQuery.error.value" class="state-text danger">
      市场查询失败：{{ skillMarketQuery.error.value?.message }}
    </p>
    <div v-else-if="!marketEntries.length" class="state-text">暂无技能</div>
    <div v-else class="market-grid">
      <n-card
        v-for="entry in marketEntries"
        :key="`${entry.source}-${entry.source_ref}`"
        size="small"
        class="market-card market-card-clickable"
        @click="openDetail(entry)"
      >
        <div class="market-card-header">
          <strong class="market-card-name">{{ entry.name }}</strong>
          <div class="market-card-meta">
            <n-tag :type="sourceTagType(entry.source)" size="small" :bordered="false">{{ sourceLabel(entry.source) }}</n-tag>
          </div>
        </div>
        <p v-if="entry.description" class="market-card-desc">{{ entry.description }}</p>
        <div class="market-card-footer">
          <span class="market-card-version">v{{ entry.version }}</span>
          <span v-if="entry.downloads" class="market-card-downloads">↓ {{ formatCount(entry.downloads) }}</span>
          <template v-if="existingNames.has(entry.name)">
            <n-tag size="small" type="success" :bordered="false">{{ existingLabel }}</n-tag>
          </template>
          <n-button
            v-else-if="canAction"
            size="small"
            type="primary"
            :loading="actionPending"
            @click.stop="emitAction(entry, entry.version ?? '')"
          >
            {{ actionLabel }}
          </n-button>
        </div>
      </n-card>
    </div>
    <div
      v-if="marketEntries.length && skillMarketQuery.hasNextPage.value"
      ref="loadMoreSentinel"
      class="market-load-more state-text"
    >
      {{ skillMarketQuery.isFetchingNextPage.value ? '加载中…' : '滚动加载更多' }}
    </div>

    <!-- 详情抽屉：点卡片打开，版本场景下可锁旧版（pick-version）。 -->
    <skill-detail-drawer
      v-model:show="detailOpen"
      :skill="detailSkill"
      :allow-version-pick="allowVersionPick"
      :action-pending="actionPending"
      :existing-names="existingNames"
      @pick-version="onPickVersion"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NButton, NCard, NInput, NTag } from 'naive-ui'
import type { SkillEntry } from '@/api'
import { useSkillMarketQuery } from '@/api/hooks/useSkills'
import SkillDetailDrawer, { type SkillDetail } from './SkillDetailDrawer.vue'

const props = withDefaults(
  defineProps<{
    existingNames?: Set<string> // 已安装/已配置名集合，命中则不显示操作按钮
    actionLabel?: string // 主操作文案：安装 / 添加
    existingLabel?: string // 已存在标记文案：已安装 / 已添加
    actionPending?: boolean
    canAction?: boolean // 是否有权限展示操作
    allowVersionPick?: boolean // true：详情抽屉版本行可「添加此版本」
  }>(),
  {
    existingNames: () => new Set<string>(),
    actionLabel: '安装',
    existingLabel: '已安装',
    actionPending: false,
    canAction: true,
    allowVersionPick: false,
  },
)
// action 事件：携带来源/标识/名称/选定版本，由父级执行安装或加入版本。
const emit = defineEmits<{ action: [{ source: string; source_ref: string; name: string; version: string }] }>()

// 来源筛选项。
const sourceFilters = [
  { label: '全部', value: '' },
  { label: '平台库', value: 'platform' },
  { label: 'ClawHub', value: 'clawhub' },
] as const
const selectedSource = ref<string>('')

const searchText = ref('')
const debouncedSearch = ref('')
let debounceTimer: ReturnType<typeof setTimeout> | null = null
watch(searchText, (val) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => { debouncedSearch.value = val }, 300)
})

const marketParams = computed(() => ({
  source: selectedSource.value || undefined,
  q: debouncedSearch.value || undefined,
}))
const skillMarketQuery = useSkillMarketQuery(marketParams)

// marketEntries 展平所有页并按 source+source_ref 去重（聚合模式下 platform 每页重复返回）。
const marketEntries = computed<SkillEntry[]>(() => {
  const pages = skillMarketQuery.data.value?.pages ?? []
  const seen = new Set<string>()
  const out: SkillEntry[] = []
  for (const page of pages) {
    for (const entry of page.entries ?? []) {
      const key = `${entry.source}-${entry.source_ref}`
      if (seen.has(key)) continue
      seen.add(key)
      out.push(entry)
    }
  }
  return out
})

// 滚动加载哨兵（IntersectionObserver）。
const loadMoreSentinel = ref<HTMLElement | null>(null)
let loadMoreObserver: IntersectionObserver | null = null
function setupLoadMoreObserver(el: HTMLElement | null) {
  loadMoreObserver?.disconnect()
  loadMoreObserver = null
  if (!el) return
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (
        entries.some((e) => e.isIntersecting) &&
        skillMarketQuery.hasNextPage.value &&
        !skillMarketQuery.isFetchingNextPage.value
      ) {
        void skillMarketQuery.fetchNextPage()
      }
    },
    { rootMargin: '200px' },
  )
  loadMoreObserver.observe(el)
}
watch(loadMoreSentinel, (el) => setupLoadMoreObserver(el))
onBeforeUnmount(() => loadMoreObserver?.disconnect())

// 详情抽屉。
const detailOpen = ref(false)
const detailSkill = ref<SkillDetail | null>(null)
function openDetail(entry: SkillEntry) {
  detailSkill.value = {
    name: entry.name, source: entry.source, source_ref: entry.source_ref,
    version: entry.version, description: entry.description, downloads: entry.downloads,
  }
  detailOpen.value = true
}
// 详情抽屉锁定某个具体版本加入。
function onPickVersion(version: string) {
  const d = detailSkill.value
  if (!d) return
  emit('action', { source: d.source ?? '', source_ref: d.source_ref ?? '', name: d.name, version })
}
function emitAction(entry: SkillEntry, version: string) {
  emit('action', { source: entry.source, source_ref: entry.source_ref, name: entry.name, version })
}

function sourceLabel(source?: string): string {
  if (source === 'platform') return '平台库'
  if (source === 'clawhub') return 'ClawHub'
  return source || '内置'
}
function sourceTagType(source?: string): 'info' | 'warning' | 'default' {
  if (source === 'platform') return 'info'
  if (source === 'clawhub') return 'warning'
  return 'default'
}
function formatCount(n?: number): string {
  if (!n || n < 10000) return String(n ?? 0)
  const fmt = (val: number, unit: string) => `${val.toFixed(1).replace(/\.0$/, '')}${unit}`
  if (n >= 1_000_000) return fmt(n / 1_000_000, '百万')
  return fmt(n / 10_000, '万')
}
</script>

<style scoped>
/* 市场样式：从 SkillManager.vue 原样迁入。 */
.market-toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.market-filters { display: flex; gap: 8px; flex-wrap: wrap; }
.filter-tag { cursor: pointer; }
.market-search { width: 200px; flex-shrink: 0; }
.market-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 12px; }
.market-load-more { display: flex; justify-content: center; margin-top: 12px; }
.market-card-clickable { cursor: pointer; }
.market-card-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; margin-bottom: 6px; }
.market-card-name { font-size: 14px; word-break: break-all; }
.market-card-meta { flex-shrink: 0; }
.market-card-desc { font-size: 12px; color: var(--color-text-secondary); margin: 0 0 8px; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.market-card-footer { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.market-card-version { font-size: 12px; color: var(--color-text-secondary); }
.market-card-downloads { font-size: 12px; color: var(--color-text-secondary); }
</style>
