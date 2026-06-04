<template>
  <!-- SkillManager：实例已安装技能列表 + 技能市场，供成员技能页与管理员实例 tab 复用。 -->
  <div class="skill-manager">
    <n-tabs v-model:value="activeTab" type="line" animated>
      <!-- 已安装视图：实时对账当前实例的 skill 列表，含状态徽章和操作按钮。 -->
      <n-tab-pane name="installed" tab="已安装">
        <div v-if="appSkillsQuery.isLoading.value" class="state-text">加载中…</div>
        <p v-else-if="appSkillsQuery.error.value" class="state-text danger">
          查询失败：{{ appSkillsQuery.error.value?.message }}
        </p>
        <n-data-table
          v-else
          :columns="installedColumns"
          :data="appSkillsQuery.data.value ?? []"
          size="small"
          :bordered="false"
          :row-key="(row: AppSkill) => row.name"
        />
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
          <n-card
            v-for="entry in marketEntries"
            :key="`${entry.source}-${entry.source_ref}`"
            size="small"
            class="market-card"
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
              <span v-if="entry.downloads" class="market-card-downloads">↓ {{ entry.downloads }}</span>
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
                @click="onInstall(entry)"
              >
                安装
              </n-button>
            </div>
          </n-card>
        </div>
      </n-tab-pane>
    </n-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, h, inject, ref, watch, type Ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
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

// 安装/卸载/更新 mutations。
const installMutation = useInstallAppSkill(appIdRef)
const uninstallMutation = useUninstallAppSkill(appIdRef)
const updateMutation = useUpdateAppSkill(appIdRef)

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

// marketEntries 是市场当前页条目，entries 为 undefined 时降级为空数组。
const marketEntries = computed<SkillEntry[]>(() => skillMarketQuery.data.value?.entries ?? [])

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
function sourceLabel(source?: string): string {
  if (source === 'platform') return '平台库'
  if (source === 'clawhub') return 'ClawHub'
  return source ?? '内置'
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

// installedColumns 定义已安装列表表格列。
const installedColumns: DataTableColumns<AppSkill> = [
  // 技能名称列。
  { title: '名称', key: 'name', render: (row) => h('strong', row.name) },
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
  // 操作列：protected 隐藏卸载并显示锁标记；builtin 只读；其余显示卸载按钮。
  {
    title: '操作',
    key: 'actions',
    render: (row) => {
      // builtin 镜像内置 skill 只读展示，不允许任何操作。
      if (row.status === 'builtin') {
        return h('span', { class: 'state-text', style: 'font-size: 12px; margin: 0' }, '内置只读')
      }
      // protected skill（当前版本必需）隐藏卸载入口，显示锁标记提示用户。
      if (row.protected) {
        return h('span', { class: 'protected-lock', title: '当前版本必需，不可卸载' }, '🔒')
      }
      // 无写权限时不显示任何操作按钮。
      if (!canManage.value) return null
      return h(
        NButton,
        {
          size: 'small',
          type: 'error',
          loading: uninstallMutation.isPending.value,
          onClick: () => onUninstall(row),
        },
        { default: () => '卸载' },
      )
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
