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
        v-for="entry in pagedEntries"
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
        <!-- 定制技能额外信息：范围徽章（可见范围）+ 申请人小字，仅 source=custom 时渲染。 -->
        <template v-if="entry.source === 'custom'">
          <n-tag
            v-if="audienceTag(entry.audience).label"
            size="small"
            :type="audienceTag(entry.audience).type"
            :bordered="false"
            class="market-card-audience"
          >
            {{ audienceTag(entry.audience).label }}
          </n-tag>
          <span v-if="entry.requester_name" class="market-card-requester">由 {{ entry.requester_name }} 申请</span>
        </template>
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
    <!-- 翻页器：只渲染当前页条目，避免页面随加载无限拉长。clawhub 为游标分页，总页数按已加载条目推算。 -->
    <div v-if="marketEntries.length && pageCount > 1" class="market-pagination">
      <n-pagination
        :page="currentPage"
        :page-count="pageCount"
        :disabled="skillMarketQuery.isFetchingNextPage.value"
        @update:page="onPageChange"
      />
    </div>

    <!-- 详情抽屉：点卡片打开，版本场景下可锁旧版（pick-version）。 -->
    <skill-detail-drawer
      v-model:show="detailOpen"
      :skill="detailSkill"
      :allow-version-pick="allowVersionPick"
      :action-pending="actionPending"
      :existing-names="existingNames"
      :allow-download="auth.isPlatformAdmin"
      @pick-version="onPickVersion"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NCard, NInput, NPagination, NTag } from 'naive-ui'
import type { SkillEntry } from '@/api'
import { useSkillMarketQuery } from '@/api/hooks/useSkills'
import { useAuthStore } from '@/stores/auth'
import SkillDetailDrawer, { type SkillDetail } from './SkillDetailDrawer.vue'

// auth 用于判断是否平台管理员，控制详情抽屉「下载」按钮的可见性。
const auth = useAuthStore()

const props = withDefaults(
  defineProps<{
    existingNames?: Set<string> // 已安装/已配置名集合，命中则不显示操作按钮
    actionLabel?: string // 主操作文案：安装 / 添加
    existingLabel?: string // 已存在标记文案：已安装 / 已添加
    actionPending?: boolean
    canAction?: boolean // 是否有权限展示操作
    allowVersionPick?: boolean // true：详情抽屉版本行可「添加此版本」
    // source：外部受控的初始来源筛选值（如 'custom'）；prop 变化时同步到内部 selectedSource，
    // 但用户手动点击筛选 chip 后内部仍可自由切换（不做双向绑定，避免打断用户操作）。
    source?: string
  }>(),
  {
    existingNames: () => new Set<string>(),
    actionLabel: '安装',
    existingLabel: '已安装',
    actionPending: false,
    canAction: true,
    allowVersionPick: false,
    source: '',
  },
)
// action 事件：携带来源/标识/名称/选定版本，由父级执行安装或加入版本。
const emit = defineEmits<{ action: [{ source: string; source_ref: string; name: string; version: string }] }>()

// 来源筛选项：全部 / 平台技能 / ClawHub / 定制（custom 定向定制技能）。
const sourceFilters = [
  { label: '全部', value: '' },
  { label: '平台技能', value: 'platform' },
  { label: 'ClawHub', value: 'clawhub' },
  { label: '定制', value: 'custom' },
] as const
// selectedSource：当前激活的来源筛选值。
// 以 prop.source 初始化；prop 变化时（如父组件「去安装」切到 custom）同步更新，
// 但用户手动点击筛选 chip 后内部自由切换，不做双向绑定。
const selectedSource = ref<string>(props.source ?? '')

// 监听 prop.source 变化，父组件（如 onGoInstall）修改时同步到内部筛选状态。
watch(
  () => props.source,
  (newSource) => {
    if (newSource !== undefined) {
      selectedSource.value = newSource
    }
  },
)

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

// 每页展示条目数；技能市场以卡片网格展示，12 条可在常见屏宽下铺满数行又不至于过长。
const pageSize = 12
// currentPage：当前页码（从 1 开始）。
const currentPage = ref(1)

// pageCount：翻页器展示的总页数。
// clawhub 来源是游标分页（只能向后翻、无法预知总数），故总页数按「已去重加载条目数」推算；
// 仍有下一页（hasNextPage）时额外 +1，给出一个「下一页」入口，用户翻到该页时再后台拉取游标下一页。
const pageCount = computed(() => {
  const loaded = Math.max(1, Math.ceil(marketEntries.value.length / pageSize))
  return skillMarketQuery.hasNextPage.value ? loaded + 1 : loaded
})

// pagedEntries：当前页要渲染的条目，对去重后的 marketEntries 做客户端切片。
// 只渲染一页而非累积全部，避免页面随翻页无限拉长（取代原滚动加载）。
const pagedEntries = computed<SkillEntry[]>(() => {
  const start = (currentPage.value - 1) * pageSize
  return marketEntries.value.slice(start, start + pageSize)
})

// ensureLoadedForCurrentPage：保证已加载条目足够填满当前页。
// 当前页所需条目数不足、仍有游标下一页且未在拉取时，后台拉取下一页；拉取完成后 marketEntries
// 增长会再次触发下方 watch，必要时继续「续拉」直到填满当前页或没有下一页为止
// （应对单个 clawhub 游标页返回不足一屏的情况）。
function ensureLoadedForCurrentPage() {
  const needed = currentPage.value * pageSize
  if (
    marketEntries.value.length < needed &&
    skillMarketQuery.hasNextPage.value &&
    !skillMarketQuery.isFetchingNextPage.value
  ) {
    void skillMarketQuery.fetchNextPage()
  }
}

// onPageChange：翻页器回调，仅更新页码；按需补拉数据交给下方 watch 统一处理，避免重复触发。
function onPageChange(page: number) {
  currentPage.value = page
}

// 翻页或一页加载完成（marketEntries 增长 / isFetchingNextPage 回落）后复查是否需继续补拉。
watch(
  () => [currentPage.value, marketEntries.value.length, skillMarketQuery.isFetchingNextPage.value],
  () => ensureLoadedForCurrentPage(),
)

// 来源筛选 / 搜索词变化时 useInfiniteQuery 会重置回第一页，这里同步把页码复位到 1，
// 避免停留在筛选后已不存在的页码导致空白。
watch(marketParams, () => {
  currentPage.value = 1
})

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
  if (source === 'platform') return '平台技能'
  if (source === 'clawhub') return 'ClawHub'
  if (source === 'custom') return '定制'
  return source || '内置'
}
// sourceTagType 将来源映射为 NaiveUI tag 颜色：platform→info(蓝)、clawhub→warning(橙)、
// custom→error(紫/红，Naive UI 无原生紫色 type，error 在多数主题下呈紫红色，视觉上与其他来源区分)。
function sourceTagType(source?: string): 'info' | 'warning' | 'default' | 'error' {
  if (source === 'platform') return 'info'
  if (source === 'clawhub') return 'warning'
  if (source === 'custom') return 'error'
  return 'default'
}

// audienceTag 将 custom 来源的可见范围标识映射为徽章文案与颜色。
// all_org→整企业可见(success 绿)、org_admins→仅企业管理员可见(warning 橙)、
// requester_only→仅本人可见(default 灰)；未知值返回灰色空文案。
function audienceTag(audience?: string): { type: 'success' | 'warning' | 'default'; label: string } {
  if (audience === 'all_org') return { type: 'success', label: '整企业可见' }
  if (audience === 'org_admins') return { type: 'warning', label: '仅企业管理员可见' }
  if (audience === 'requester_only') return { type: 'default', label: '仅本人可见' }
  return { type: 'default', label: '' }
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
.market-pagination { display: flex; justify-content: center; margin-top: 16px; }
.market-card-clickable { cursor: pointer; }
.market-card-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; margin-bottom: 6px; }
.market-card-name { font-size: 14px; word-break: break-all; }
.market-card-meta { flex-shrink: 0; }
.market-card-desc { font-size: 12px; color: var(--color-text-secondary); margin: 0 0 8px; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.market-card-footer { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.market-card-version { font-size: 12px; color: var(--color-text-secondary); }
.market-card-downloads { font-size: 12px; color: var(--color-text-secondary); }
/* 定制技能卡片额外信息：范围徽章与申请人小字，紧接卡片描述下方。 */
.market-card-audience { margin-bottom: 4px; }
.market-card-requester { font-size: 11px; color: var(--color-text-secondary); display: block; margin-bottom: 6px; }
</style>
