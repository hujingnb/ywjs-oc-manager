<template>
  <!-- SkillManager：实例已安装技能列表 + 技能市场，供成员技能页与管理员实例 tab 复用。 -->
  <div class="skill-manager">
    <!-- 运行时版本过旧：实例运行的 hermes 镜像无 oc-ops /oc/skills 路由，技能管理整体不可用，
         直接提示更新版本，不再展示 tab（安装/卸载等操作都无法生效）。 -->
    <n-alert v-if="runtimeUnsupported" type="warning" title="技能管理不可用">
      {{ runtimeUnsupportedMessage }}
    </n-alert>
    <n-tabs v-else v-model:value="activeTab" type="line" animated>
      <!-- 已安装视图：实时对账当前实例的 skill 列表，含状态徽章和操作按钮。 -->
      <n-tab-pane name="installed" tab="已安装">
        <div v-if="appSkillsQuery.isLoading.value" class="state-text">加载中…</div>
        <p v-else-if="appSkillsQuery.error.value" class="state-text danger">
          查询失败：{{ appSkillsQuery.error.value?.message }}
        </p>
        <template v-else>
          <!-- 来源筛选工具栏：仅当列表存在 2 种及以上来源时展示（只有一种来源筛选无意义）。
               筛选项动态取自当前列表实际出现的来源，避免展示没有数据的空选项；
               每个 tag 带数量统计，「全部」即总数。 -->
          <div v-if="installedSourceFilters.length > 2" class="installed-toolbar">
            <n-tag
              v-for="filter in installedSourceFilters"
              :key="filter.value"
              :type="selectedInstalledSource === filter.value ? 'primary' : 'default'"
              :bordered="false"
              checkable
              :checked="selectedInstalledSource === filter.value"
              class="filter-tag"
              @click="selectedInstalledSource = filter.value"
            >
              {{ filter.label }} ({{ filter.count }})
            </n-tag>
          </div>
          <!-- 仅一种来源（筛选工具栏隐藏）时，单独展示一行总数统计，保证数量始终可见。 -->
          <div v-else class="installed-count state-text">共 {{ filteredAppSkills.length }} 个技能</div>
          <n-data-table
            :columns="installedColumns"
            :data="filteredAppSkills"
            size="small"
            :bordered="false"
            :row-key="(row: AppSkill) => row.name"
          />
        </template>
      </n-tab-pane>

      <!-- 技能市场视图：来源筛选 + 关键词搜索，卡片网格展示可安装技能。 -->
      <n-tab-pane name="market" tab="技能市场">
        <!-- 筛选工具栏：来源 tag 切换 + 关键词搜索输入框。 -->
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
              @click="onSelectSource(filter.value)"
            >
              {{ filter.label }}
            </n-tag>
          </div>
          <n-input
            v-model:value="searchText"
            placeholder="搜索技能名称…"
            clearable
            size="small"
            class="market-search"
          />
        </div>

        <div v-if="skillMarketQuery.isLoading.value" class="state-text">加载中…</div>
        <p v-else-if="skillMarketQuery.error.value" class="state-text danger">
          市场查询失败：{{ skillMarketQuery.error.value?.message }}
        </p>
        <div v-else-if="!marketEntries.length" class="state-text">暂无技能</div>
        <!-- 市场卡片网格：每个条目展示来源徽章、描述、下载量和安装按钮。 -->
        <div v-else class="market-grid">
          <!-- 卡片整体可点击查看详情（含版本列表）；安装按钮 @click.stop 不触发详情。 -->
          <n-card
            v-for="entry in marketEntries"
            :key="`${entry.source}-${entry.source_ref}`"
            size="small"
            class="market-card market-card-clickable"
            @click="openMarketDetail(entry)"
          >
            <div class="market-card-header">
              <strong class="market-card-name">{{ entry.name }}</strong>
              <!-- 来源徽章与安装按钮放同一行右侧。 -->
              <div class="market-card-meta">
                <n-tag :type="sourceTagType(entry.source)" size="small" :bordered="false">
                  {{ sourceLabel(entry.source) }}
                </n-tag>
              </div>
            </div>
            <p v-if="entry.description" class="market-card-desc">{{ entry.description }}</p>
            <div class="market-card-footer">
              <span class="market-card-version">v{{ entry.version }}</span>
              <span v-if="entry.downloads" class="market-card-downloads">↓ {{ formatCount(entry.downloads) }}</span>
              <!-- 已安装提示：同名 skill 已存在时置灰且不可点击。 -->
              <template v-if="isInstalled(entry.name)">
                <n-tag size="small" type="success" :bordered="false">已安装</n-tag>
              </template>
              <!-- 安装按钮：仅有管理权限且未安装时显示。 -->
              <n-button
                v-else-if="canManage"
                size="small"
                type="primary"
                :loading="installMutation.isPending.value"
                @click.stop="onInstall(entry)"
              >
                安装
              </n-button>
            </div>
          </n-card>
        </div>
        <!-- 滚动加载哨兵：clawhub 还有下一页（hasNextPage）时挂在列表底部，
             进入视口由 IntersectionObserver 自动拉取下一页；加载中显示提示。 -->
        <div
          v-if="marketEntries.length && skillMarketQuery.hasNextPage.value"
          ref="loadMoreSentinel"
          class="market-load-more state-text"
        >
          {{ skillMarketQuery.isFetchingNextPage.value ? '加载中…' : '滚动加载更多' }}
        </div>
      </n-tab-pane>
    </n-tabs>

    <!-- 技能详情抽屉：已安装列表点名称、市场点卡片均打开此抽屉，展示基础信息 + 版本列表。 -->
    <n-drawer v-model:show="detailOpen" :width="420" placement="right">
      <n-drawer-content :title="detailSkill?.name ?? '技能详情'" closable>
        <div v-if="detailSkill" class="skill-detail">
          <!-- 作者（clawhub 才有） -->
          <div v-if="richDetail?.author_name" class="skill-detail-author">
            <img v-if="richDetail.author_avatar" :src="richDetail.author_avatar" class="skill-detail-avatar" alt="" referrerpolicy="no-referrer" />
            <span class="skill-detail-author-name">{{ richDetail.author_name }}</span>
            <span v-if="richDetail.author_handle" class="skill-detail-handle">@{{ richDetail.author_handle }}</span>
          </div>

          <!-- 基础信息行 -->
          <p class="skill-detail-row"><span class="skill-detail-label">来源</span>{{ sourceLabel(detailSkill.source) }}</p>
          <p v-if="detailSkill.version" class="skill-detail-row"><span class="skill-detail-label">版本</span>v{{ detailSkill.version }}</p>
          <p v-if="detailSkill.status" class="skill-detail-row"><span class="skill-detail-label">状态</span>{{ detailStatusLabel(detailSkill.status) }}</p>
          <p v-if="richDetail?.license" class="skill-detail-row"><span class="skill-detail-label">许可</span>{{ richDetail.license }}</p>
          <p v-if="fmtDate(richDetail?.created_at)" class="skill-detail-row"><span class="skill-detail-label">创建</span>{{ fmtDate(richDetail?.created_at) }}</p>
          <p v-if="fmtDate(richDetail?.updated_at)" class="skill-detail-row"><span class="skill-detail-label">更新</span>{{ fmtDate(richDetail?.updated_at) }}</p>

          <!-- 统计（clawhub）：下载/星标/安装，带单位显示。 -->
          <div v-if="richDetail && (richDetail.downloads || richDetail.stars || richDetail.installs)" class="skill-detail-stats">
            <span v-if="richDetail.downloads">↓ {{ formatCount(richDetail.downloads) }} 下载</span>
            <span v-if="richDetail.stars">★ {{ formatCount(richDetail.stars) }} 星标</span>
            <span v-if="richDetail.installs">⤓ {{ formatCount(richDetail.installs) }} 安装</span>
          </div>

          <!-- 关键词 -->
          <div v-if="richDetail?.keywords?.length" class="skill-detail-keywords">
            <n-tag v-for="kw in richDetail.keywords" :key="kw" size="tiny" :bordered="false">{{ kw }}</n-tag>
          </div>

          <!-- 完整描述（富详情优先，回退点击带入的描述） -->
          <p v-if="effectiveDescription" class="skill-detail-desc">{{ effectiveDescription }}</p>

          <!-- 版本列表：platform/clawhub 来源才有；builtin/self_created 无来源版本。 -->
          <div class="skill-detail-versions">
            <strong>版本列表</strong>
            <div v-if="!detailHasUpstream" class="state-text">该来源无版本信息</div>
            <div v-else-if="skillDetailQuery.isLoading.value" class="state-text">加载中…</div>
            <p v-else-if="skillDetailQuery.error.value" class="state-text danger">详情查询失败</p>
            <ul v-else-if="detailVersions.length" class="skill-detail-version-list">
              <li v-for="(v, i) in detailVersions" :key="v.version" class="skill-detail-version-item">
                <div class="skill-detail-version-head">
                  <span class="skill-detail-version-num">v{{ v.version }}</span>
                  <n-tag v-if="i === 0" size="tiny" type="success" :bordered="false">最新</n-tag>
                  <n-tag v-if="v.version === detailSkill.version" size="tiny" type="info" :bordered="false">当前</n-tag>
                  <span v-if="fmtDate(v.published_at)" class="skill-detail-version-date">{{ fmtDate(v.published_at) }}</span>
                </div>
                <div v-if="v.changelog" class="skill-detail-version-log">{{ v.changelog }}</div>
              </li>
            </ul>
            <div v-else class="state-text">暂无版本</div>
          </div>
        </div>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, h, inject, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NInput,
  NTabPane,
  NTabs,
  NTag,
  useDialog,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'

import type { AppSkill, SkillEntry } from '@/api'
import type { AppDTO } from '@/api/hooks/useApps'
import {
  useAppSkillsQuery,
  useInstallAppSkill,
  useSkillMarketQuery,
  useSkillDetailQuery,
  useReinstallAppSkill,
  useUninstallAppSkill,
  useUpdateAppSkill,
} from '@/api/hooks/useSkills'
import { canManageAppSkill } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// SkillManager 接受 appId prop，app 可从 inject 获取（管理员入口由父级 provide）。
const props = defineProps<{
  // 目标实例 ID，用于已安装列表查询与安装/卸载/更新操作。
  appId: string
}>()

const auth = useAuthStore()
const message = useMessage()
const dialog = useDialog()

// app 由管理员页面（AppSkillsTab / OrgSkillsPage）通过 provide 注入；
// 成员页面场景下 app 从 useMemberApp() 传入，两条路径都走 inject。
const app = inject<Ref<AppDTO | null>>('app')

// canManage：由 canManageAppSkill 根据当前用户角色和实例归属判断写入权限；
// 平台管理员可管理任意实例 skill，org_admin 限本组织，org_member 限 owner 本人。
const canManage = computed(() => canManageAppSkill(auth.user, app?.value))

// appIdRef：将 props.appId 包装为 Ref，供 hooks 响应式使用。
const appIdRef = computed<string | undefined>(() => props.appId)

// 已安装列表 query。
const appSkillsQuery = useAppSkillsQuery(appIdRef)

// runtimeUnsupported 判定：已安装列表查询失败且后端返回 APP_SKILL_RUNTIME_UNSUPPORTED
// （实例运行的 hermes 版本过旧、oc-ops 无 /oc/skills 路由）。此时技能管理整体不可用。
const runtimeUnsupported = computed(() => {
  const body = (appSkillsQuery.error.value as { body?: { code?: string } } | null)?.body
  return body?.code === 'APP_SKILL_RUNTIME_UNSUPPORTED'
})
// runtimeUnsupportedMessage 优先用后端返回的提示文案，缺失时兜底固定文案。
const runtimeUnsupportedMessage = computed(() => {
  const body = (appSkillsQuery.error.value as { body?: { message?: string } } | null)?.body
  return body?.message ?? '当前实例运行的 hermes 版本过旧，不支持技能管理，请更新实例的运行时版本后重试。'
})

// 安装/卸载/更新/重装 mutations。
const installMutation = useInstallAppSkill(appIdRef)
const uninstallMutation = useUninstallAppSkill(appIdRef)
const updateMutation = useUpdateAppSkill(appIdRef)
const reinstallMutation = useReinstallAppSkill(appIdRef)

// activeTab 控制当前视图（installed / market）。
const activeTab = ref<'installed' | 'market'>('installed')

// sourceFilters 是市场来源筛选选项。
const sourceFilters = [
  { label: '全部', value: '' },
  { label: '平台库', value: 'platform' },
  { label: 'ClawHub', value: 'clawhub' },
] as const

// selectedSource 当前选中的来源筛选值，空字符串表示全部。
const selectedSource = ref<string>('')

// searchText 关键词搜索框内容；使用 debounce 避免每次按键都发请求。
const searchText = ref('')
// debouncedSearch 防抖后的搜索关键词，实际传给市场 query。
const debouncedSearch = ref('')

// 监听 searchText 变化，300ms 防抖后同步到 debouncedSearch。
let debounceTimer: ReturnType<typeof setTimeout> | null = null
watch(searchText, (val) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    debouncedSearch.value = val
  }, 300)
})

// marketParams 汇总市场查询入参（来源筛选 + 防抖搜索词）。
const marketParams = computed(() => ({
  source: selectedSource.value || undefined,
  q: debouncedSearch.value || undefined,
}))

// 市场 query，仅在切换到市场视图时加载（enabled 由 TanStack Query 内部 lazy 处理）。
const skillMarketQuery = useSkillMarketQuery(marketParams)

// marketEntries 把已加载的所有页展平为单一列表，并按 source+source_ref 去重。
// 聚合模式（source=""）下后端每页都会重复返回 platform 条目，必须去重避免重复卡片。
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

// ===== 市场滚动加载（IntersectionObserver） =====
// loadMoreSentinel 是挂在市场列表底部的哨兵元素；进入视口即自动拉取下一页。
const loadMoreSentinel = ref<HTMLElement | null>(null)
// observer 在哨兵存在时观察其与视口的相交；切走市场 tab / 无下一页时哨兵被移除并断开。
let loadMoreObserver: IntersectionObserver | null = null

// setupLoadMoreObserver 为哨兵元素建立/重建 IntersectionObserver。
// el 为 null（哨兵被 v-if 移除）时断开观察，避免泄漏。
function setupLoadMoreObserver(el: HTMLElement | null) {
  loadMoreObserver?.disconnect()
  loadMoreObserver = null
  if (!el) return
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      // 哨兵进入视口、还有下一页、且当前未在拉取时，自动追加下一页。
      if (
        entries.some((e) => e.isIntersecting) &&
        skillMarketQuery.hasNextPage.value &&
        !skillMarketQuery.isFetchingNextPage.value
      ) {
        void skillMarketQuery.fetchNextPage()
      }
    },
    // rootMargin 提前 200px 触发，滚动体验更顺（不必精确到底）。
    { rootMargin: '200px' },
  )
  loadMoreObserver.observe(el)
}

// 哨兵元素挂载/卸载（切 tab、source/q 变化、hasNextPage 变化）时同步观察状态。
watch(loadMoreSentinel, (el) => setupLoadMoreObserver(el))
// 组件卸载时断开观察，释放资源。
onBeforeUnmount(() => loadMoreObserver?.disconnect())

// installedNames 将已安装 skill name 放入 Set，用于市场安装按钮去重判断。
const installedNames = computed<Set<string>>(() => {
  const names = appSkillsQuery.data.value?.map((s) => s.name) ?? []
  return new Set(names)
})

// isInstalled 判断市场条目是否已被安装（按 name 匹配）。
function isInstalled(name: string): boolean {
  return installedNames.value.has(name)
}

// onSelectSource 切换市场来源筛选（再次点击同一 tag 不取消，保持至少选一个）。
function onSelectSource(value: string) {
  selectedSource.value = value
}

// sourceLabel 将来源字符串转换为用户可读标签。
// 空来源（内置/自创 skill 无 source）显示「内置」，避免详情页「来源」一栏为空。
function sourceLabel(source?: string): string {
  if (source === 'platform') return '平台库'
  if (source === 'clawhub') return 'ClawHub'
  return source || '内置'
}

// formatCount 把大数字格式化为带单位的可读文案：1300000→「1.3百万」、25000→「2.5万」、
// 小于 1 万的原样显示。去掉多余的 ".0"（如 20000→「2万」而非「2.0万」）。
function formatCount(n?: number): string {
  if (!n || n < 10000) return String(n ?? 0)
  const fmt = (v: number, unit: string) => `${v.toFixed(1).replace(/\.0$/, '')}${unit}`
  if (n >= 1_000_000) return fmt(n / 1_000_000, '百万')
  return fmt(n / 10_000, '万')
}

// sourceTagType 将来源映射为 NaiveUI tag 颜色类型。
// platform→info(蓝) / clawhub→warning(橙) / 其余→default(灰/默认)
function sourceTagType(source?: string): 'info' | 'warning' | 'default' {
  if (source === 'platform') return 'info'
  if (source === 'clawhub') return 'warning'
  return 'default'
}

// categoryTagType 按 category 或 source 渲染已安装列表的来源徽章。
// platform→info(蓝) / clawhub→warning(橙) / builtin→default(灰) / self_created→info(紫，用 info 近似)
function installedSourceTagType(row: AppSkill): 'info' | 'warning' | 'default' | 'primary' {
  if (row.status === 'builtin') return 'default'
  if (row.status === 'self_created') return 'primary'
  if (row.source === 'platform') return 'info'
  if (row.source === 'clawhub') return 'warning'
  return 'default'
}

// installedSourceLabel 渲染已安装列表的来源文案。
function installedSourceLabel(row: AppSkill): string {
  if (row.status === 'builtin') return '内置'
  if (row.status === 'self_created') return '自创'
  return sourceLabel(row.source)
}

// ===== 已安装列表来源筛选 =====
// INSTALLED_SOURCE_DEFS 是已安装列表可能出现的来源类别（顺序即筛选项展示顺序）。
// 比市场多「内置/自创」两类——builtin/self_created skill 无 source 标识、按 status 归类。
// 归类口径与 installedSourceLabel / installedSourceKey 保持一致。
const INSTALLED_SOURCE_DEFS = [
  { label: '平台库', value: 'platform' },
  { label: 'ClawHub', value: 'clawhub' },
  { label: '内置', value: 'builtin' },
  { label: '自创', value: 'self_created' },
] as const

// selectedInstalledSource 当前选中的已安装来源筛选值，空字符串表示「全部」。
const selectedInstalledSource = ref<string>('')

// installedSourceKey 把对账行归类到筛选值（与 installedSourceLabel 同口径）：
// builtin/self_created 按 status 归类，其余按 source；无来源标识兜底为 builtin。
function installedSourceKey(row: AppSkill): string {
  if (row.status === 'builtin') return 'builtin'
  if (row.status === 'self_created') return 'self_created'
  if (row.source === 'platform') return 'platform'
  if (row.source === 'clawhub') return 'clawhub'
  return 'builtin'
}

// installedSourceFilters 动态来源筛选项——「全部」+ 当前列表实际出现的来源类别，
// 每项带 count 数量统计（「全部」为总数）。避免展示没有数据的空筛选项
// （如实例无 clawhub skill 时不显示「ClawHub」）。
const installedSourceFilters = computed(() => {
  const all = appSkillsQuery.data.value ?? []
  // 统计每个来源类别的 skill 数量。
  const counts = new Map<string, number>()
  for (const row of all) {
    const k = installedSourceKey(row)
    counts.set(k, (counts.get(k) ?? 0) + 1)
  }
  return [
    { label: '全部', value: '', count: all.length },
    ...INSTALLED_SOURCE_DEFS.filter((f) => counts.has(f.value)).map((f) => ({
      label: f.label,
      value: f.value,
      count: counts.get(f.value) ?? 0,
    })),
  ]
})

// filteredAppSkills 按选中来源筛选已安装列表（「全部」时原样返回）。
const filteredAppSkills = computed<AppSkill[]>(() => {
  const all = appSkillsQuery.data.value ?? []
  if (!selectedInstalledSource.value) return all
  return all.filter((row) => installedSourceKey(row) === selectedInstalledSource.value)
})

// 列表来源类别变化（数据刷新 / 卸载后）导致已选筛选项消失时回退「全部」，
// 避免选中一个不再存在的来源而表格永远为空。
watch(installedSourceFilters, (filters) => {
  if (
    selectedInstalledSource.value &&
    !filters.some((f) => f.value === selectedInstalledSource.value)
  ) {
    selectedInstalledSource.value = ''
  }
})

// statusTagType 将对账状态映射为 NaiveUI tag 颜色类型。
// active→success / pending→warning / builtin/self_created→default
function statusTagType(status: string): 'success' | 'warning' | 'default' {
  if (status === 'active') return 'success'
  if (status === 'pending') return 'warning'
  return 'default'
}

// statusLabel 将对账状态转换为用户可读文案。
function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    active: '已生效',
    pending: '待生效',
    builtin: '内置',
    self_created: '自创',
  }
  return labels[status] ?? status
}

// ===== 技能详情抽屉 =====
// SkillDetail 是详情抽屉展示的数据，已安装行与市场卡片各取所需字段填充。
interface SkillDetail {
  name: string
  source?: string
  source_ref?: string
  version?: string
  description?: string
  downloads?: number
  status?: string // 仅已安装列表有
}
const detailOpen = ref(false)
const detailSkill = ref<SkillDetail | null>(null)

// detailHasUpstream：仅 platform/clawhub 来源有上游富详情/版本（builtin/self_created 无来源标识）。
const detailHasUpstream = computed(() => {
  const d = detailSkill.value
  return Boolean(d?.source_ref && (d.source === 'platform' || d.source === 'clawhub'))
})
// 富详情查询：source/ref 来自当前选中 skill，仅在 detailHasUpstream 时实际发请求。
const detailParams = computed(() => ({
  source: detailHasUpstream.value ? detailSkill.value?.source : undefined,
  ref: detailHasUpstream.value ? detailSkill.value?.source_ref : undefined,
}))
const skillDetailQuery = useSkillDetailQuery(detailParams)

// richDetail：后端返回的富详情（作者/统计/许可等），未取到时为 null。
const richDetail = computed(() => skillDetailQuery.data.value?.detail ?? null)
// detailVersions：版本列表（含 changelog/发布时间）。
const detailVersions = computed(() => skillDetailQuery.data.value?.versions ?? [])
// effectiveDescription：优先用富详情的完整描述，回退到点击时带入的描述（市场卡片摘要 / 容器 SKILL.md）。
const effectiveDescription = computed(
  () => richDetail.value?.description || detailSkill.value?.description || '',
)

// detailStatusLabel 复用 statusLabel，pending 在详情里也补「待生效」语义。
function detailStatusLabel(status: string): string {
  return statusLabel(status)
}

// fmtDate 把 ISO 字符串或 epoch 毫秒时间戳格式化为 YYYY-MM-DD（拿不到时返回空）。
function fmtDate(v?: string | number): string {
  if (!v) return ''
  const d = typeof v === 'number' ? new Date(v) : new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
}

// openInstalledDetail 由已安装列表名称点击触发，带入对账行的来源/版本/状态/描述（容器 SKILL.md）。
function openInstalledDetail(row: AppSkill) {
  detailSkill.value = {
    name: row.name,
    source: row.source,
    source_ref: row.source_ref,
    version: row.version,
    status: row.status,
    description: row.description,
  }
  detailOpen.value = true
}

// openMarketDetail 由市场卡片点击触发，带入市场条目的描述/下载量/版本。
function openMarketDetail(entry: SkillEntry) {
  detailSkill.value = {
    name: entry.name,
    source: entry.source,
    source_ref: entry.source_ref,
    version: entry.version,
    description: entry.description,
    downloads: entry.downloads,
  }
  detailOpen.value = true
}

// onInstall 触发市场 skill 安装，成功后已安装列表自动刷新。
async function onInstall(entry: SkillEntry) {
  try {
    await installMutation.mutateAsync({
      source: entry.source,
      source_ref: entry.source_ref,
      name: entry.name,
      // version 在 SkillEntry 中可选，回退到空字符串；后端若不支持会返回错误。
      version: entry.version ?? '',
    })
    message.success(`已安装 ${entry.name}`)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '安装失败')
  }
}

// onUninstall 经 useDialog 二次确认后卸载指定 skill。
function onUninstall(row: AppSkill) {
  dialog.warning({
    title: '确认卸载',
    content: `确认卸载技能「${row.name}」？`,
    positiveText: '卸载',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await uninstallMutation.mutateAsync(row.name)
        message.success(`已卸载 ${row.name}`)
      } catch (err) {
        message.error(err instanceof Error ? err.message : '卸载失败')
      }
    },
  })
}

// onUpdate 将指定 skill 更新到 latest_version。
async function onUpdate(row: AppSkill) {
  if (!row.latest_version) return
  try {
    await updateMutation.mutateAsync({ name: row.name, version: row.latest_version })
    message.success(`${row.name} 已更新到 ${row.latest_version}`)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '更新失败')
  }
}

// onReinstall 对 pending 状态的 skill 重新触发热装 + reload（重试）；成功转 active，仍失败保持 pending。
async function onReinstall(row: AppSkill) {
  try {
    const r = await reinstallMutation.mutateAsync(row.name)
    if (r?.status === 'active') {
      message.success(`已重新安装 ${row.name}`)
    } else {
      message.warning(`${row.name} 仍未生效，可稍后再试`)
    }
  } catch (err) {
    message.error(err instanceof Error ? err.message : '重新安装失败')
  }
}

// installedColumns 定义已安装列表表格列。
const installedColumns: DataTableColumns<AppSkill> = [
  // 技能名称列：渲染为可点击链接，点击打开详情抽屉（含版本列表）。
  {
    title: '名称',
    key: 'name',
    render: (row) =>
      h(
        NButton,
        { text: true, type: 'primary', onClick: () => openInstalledDetail(row) },
        { default: () => row.name },
      ),
  },
  // 来源徽章列：根据 status/source 渲染颜色区分。
  {
    title: '来源',
    key: 'source',
    render: (row) =>
      h(
        NTag,
        { type: installedSourceTagType(row), size: 'small', bordered: false },
        { default: () => installedSourceLabel(row) },
      ),
  },
  // 版本号列。
  { title: '版本', key: 'version', render: (row) => row.version ?? '—' },
  // 对账状态徽章列：active 绿/pending 黄「待生效·重新安装」/builtin 灰/self_created 灰。
  {
    title: '状态',
    key: 'status',
    render: (row) => {
      const tagLabel = row.status === 'pending' ? '待生效·重新安装' : statusLabel(row.status)
      return h(
        NTag,
        { type: statusTagType(row.status), size: 'small', bordered: false },
        { default: () => tagLabel },
      )
    },
  },
  // 更新列：latest_version 大于 version 时显示更新按钮。
  {
    title: '更新',
    key: 'update',
    render: (row) => {
      // builtin/self_created skill 无来源版本，不显示更新按钮。
      if (!row.latest_version || row.latest_version === row.version) return '—'
      return h(
        NButton,
        {
          size: 'small',
          type: 'info',
          loading: updateMutation.isPending.value,
          onClick: () => onUpdate(row),
        },
        { default: () => `更新至 ${row.latest_version}` },
      )
    },
  },
  // 操作列：protected 隐藏卸载并显示锁标记；builtin 只读；pending 额外给「重新安装」重试；其余显示卸载按钮。
  {
    title: '操作',
    key: 'actions',
    render: (row) => {
      // builtin 镜像内置 skill（含 oc-kb 等运行时强制系统 skill）只读展示，不允许任何操作。
      if (row.status === 'builtin') {
        return h('span', { class: 'state-text', style: 'font-size: 12px; margin: 0' }, '内置只读')
      }
      // protected skill（当前版本必需）隐藏卸载入口，显示锁标记提示用户。
      if (row.protected) {
        return h('span', { class: 'protected-lock', title: '当前版本必需，不可卸载' }, '🔒')
      }
      // 无写权限时不显示任何操作按钮。
      if (!canManage.value) return null
      // pending（首次热装/reload 未成功）额外给「重新安装」重试按钮，与卸载并排。
      const reinstallBtn =
        row.status === 'pending'
          ? [
              h(
                NButton,
                {
                  size: 'small',
                  type: 'warning',
                  loading: reinstallMutation.isPending.value,
                  onClick: () => onReinstall(row),
                },
                { default: () => '重新安装' },
              ),
            ]
          : []
      const uninstallBtn = h(
        NButton,
        {
          size: 'small',
          type: 'error',
          loading: uninstallMutation.isPending.value,
          onClick: () => onUninstall(row),
        },
        { default: () => '卸载' },
      )
      return h('div', { style: 'display: flex; gap: 8px' }, [...reinstallBtn, uninstallBtn])
    },
  },
]
</script>

<style scoped>
/* 市场工具栏：来源筛选 tag + 搜索输入框横向排列。 */
.market-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}

.market-filters {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

/* 已安装列表来源筛选工具栏：与市场筛选风格一致，底部留间距。 */
.installed-toolbar {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

/* 单一来源时的总数统计行。 */
.installed-count {
  margin-bottom: 12px;
}

/* filter-tag 可点击样式；NaiveUI checkable tag 已有 cursor:pointer，此处仅加间距。 */
.filter-tag {
  cursor: pointer;
}

.market-search {
  width: 200px;
  flex-shrink: 0;
}

/* 市场卡片网格：响应式多列布局，最小宽度 220px。 */
.market-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}

/* 加载更多按钮容器：水平居中、与卡片网格留出间距。 */
.market-load-more {
  display: flex;
  justify-content: center;
  margin-top: 12px;
}

/* 可点击的市场卡片：悬浮手型，提示可点开详情。 */
.market-card-clickable {
  cursor: pointer;
}

/* 详情抽屉：信息行与版本列表布局。 */
.skill-detail-row {
  margin: 4px 0;
  font-size: 13px;
}
.skill-detail-label {
  display: inline-block;
  width: 56px;
  color: var(--text-muted, #888);
}
.skill-detail-desc {
  margin: 12px 0;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
}
.skill-detail-versions {
  margin-top: 16px;
}
.skill-detail-version-list {
  list-style: none;
  padding: 0;
  margin: 8px 0 0;
}
.skill-detail-version-list li {
  font-size: 13px;
}
.skill-detail-version-num {
  font-family: var(--font-mono, monospace);
}

/* 作者行：头像 + 名称 + handle。 */
.skill-detail-author {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.skill-detail-avatar {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  object-fit: cover;
}
.skill-detail-author-name {
  font-weight: 600;
  font-size: 13px;
}
.skill-detail-handle {
  color: var(--text-muted, #888);
  font-size: 12px;
}
/* 统计行：下载/星标/安装/评论。 */
.skill-detail-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin: 10px 0;
  font-size: 12px;
  color: var(--text-muted, #888);
}
/* 关键词标签行。 */
.skill-detail-keywords {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 8px 0;
}
/* 版本项：版本头 + changelog。 */
.skill-detail-version-item {
  display: block;
  padding: 8px 0;
  border-bottom: 1px solid var(--border-color, #eee);
}
.skill-detail-version-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.skill-detail-version-date {
  margin-left: auto;
  color: var(--text-muted, #999);
  font-size: 12px;
}
.skill-detail-version-log {
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-muted, #666);
  line-height: 1.5;
}

.market-card-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}

.market-card-name {
  font-size: 14px;
  word-break: break-all;
}

.market-card-meta {
  flex-shrink: 0;
}

.market-card-desc {
  font-size: 12px;
  color: var(--color-text-secondary);
  margin: 0 0 8px;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}

.market-card-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.market-card-version {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.market-card-downloads {
  font-size: 12px;
  color: var(--color-text-secondary);
}

/* protected-lock 锁标记提示：不可卸载时替代操作按钮展示。 */
.protected-lock {
  cursor: help;
  font-size: 14px;
}
</style>
