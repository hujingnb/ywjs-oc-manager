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

      <!-- 技能市场视图：委托给 SkillMarketBrowser 子组件实现。 -->
      <n-tab-pane name="market" tab="技能市场">
        <skill-market-browser
          :existing-names="installedNames"
          action-label="安装"
          existing-label="已安装"
          :action-pending="installMutation.isPending.value"
          :can-action="canManage"
          @action="onMarketAction"
        />
      </n-tab-pane>
    </n-tabs>

    <!-- 已安装 skill 详情抽屉：点已安装名称打开（无版本锁定动作）。 -->
    <skill-detail-drawer v-model:show="detailOpen" :skill="detailSkill" />
  </div>
</template>

<script setup lang="ts">
import { computed, h, inject, ref, watch, type Ref } from 'vue'
import {
  NAlert,
  NButton,
  NDataTable,
  NTabPane,
  NTabs,
  NTag,
  useDialog,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'

import type { AppSkill } from '@/api'
import type { AppDTO } from '@/api/hooks/useApps'
import {
  useAppSkillsQuery,
  useInstallAppSkill,
  useReinstallAppSkill,
  useUninstallAppSkill,
  useUpdateAppSkill,
} from '@/api/hooks/useSkills'
import { canManageAppSkill } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import SkillMarketBrowser from './SkillMarketBrowser.vue'
import SkillDetailDrawer, { type SkillDetail } from './SkillDetailDrawer.vue'

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

// installedNames 将已安装 skill name 放入 Set，用于市场安装按钮去重判断。
// 取自完整列表（而非 visibleAppSkills）：市场只含 platform/clawhub 来源、不含 builtin，
// 去重判断不受内置隐藏影响，用全集更稳妥。
const installedNames = computed<Set<string>>(() => {
  const names = appSkillsQuery.data.value?.map((s) => s.name) ?? []
  return new Set(names)
})

// visibleAppSkills：已安装列表的「可见集合」，作为来源筛选计数与表格渲染的共同数据源。
// 内置 skill（status==='builtin'，含 oc-kb 等运行时强制系统 skill）对普通用户只读、无操作
// 价值且数量较多，默认对非平台管理员隐藏，让企业管理员/成员只看到自己可管理的来源；
// 平台管理员仍可见，便于运维排查内置 skill 状态。隐藏在前端做（与来源筛选同口径），
// 后端仍返回全集。
const visibleAppSkills = computed<AppSkill[]>(() => {
  const all = appSkillsQuery.data.value ?? []
  if (auth.isPlatformAdmin) return all
  return all.filter((row) => row.status !== 'builtin')
})

// sourceLabel 将来源字符串转换为用户可读标签。
// 空来源（内置/自创 skill 无 source）显示「内置」，避免详情页「来源」一栏为空。
function sourceLabel(source?: string): string {
  if (source === 'platform') return '平台库'
  if (source === 'clawhub') return 'ClawHub'
  return source || '内置'
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
  const all = visibleAppSkills.value
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
  const all = visibleAppSkills.value
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
const detailOpen = ref(false)
const detailSkill = ref<SkillDetail | null>(null)

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

// onMarketAction 处理市场浏览器的安装动作（payload 含 source/source_ref/name/version）。
async function onMarketAction(p: { source: string; source_ref: string; name: string; version: string }) {
  try {
    await installMutation.mutateAsync({ source: p.source, source_ref: p.source_ref, name: p.name, version: p.version })
    message.success(`已安装 ${p.name}`)
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

/* protected-lock 锁标记提示：不可卸载时替代操作按钮展示。 */
.protected-lock {
  cursor: help;
  font-size: 14px;
}
</style>
