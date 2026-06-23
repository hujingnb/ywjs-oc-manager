<template>
  <!-- SkillDetailDrawer：skill 详情抽屉，展示富信息 + 版本列表，供已安装列表与市场共用。 -->
  <n-drawer :show="show" :width="420" placement="right" @update:show="$emit('update:show', $event)">
    <n-drawer-content :title="skill?.name ?? t('components.skillDetailDrawer.drawerTitle')" closable>
      <div v-if="skill" class="skill-detail">
        <!-- 作者（clawhub 才有） -->
        <div v-if="richDetail?.author_name" class="skill-detail-author">
          <img v-if="richDetail.author_avatar" :src="richDetail.author_avatar" class="skill-detail-avatar" alt="" referrerpolicy="no-referrer" />
          <span class="skill-detail-author-name">{{ richDetail.author_name }}</span>
          <span v-if="richDetail.author_handle" class="skill-detail-handle">@{{ richDetail.author_handle }}</span>
        </div>

        <!-- 基础信息行 -->
        <p class="skill-detail-row"><span class="skill-detail-label">{{ t('components.skillDetailDrawer.labelSource') }}</span>{{ sourceLabel(skill.source) }}</p>
        <p v-if="skill.version" class="skill-detail-row"><span class="skill-detail-label">{{ t('components.skillDetailDrawer.labelVersion') }}</span>v{{ skill.version }}</p>
        <p v-if="skill.status" class="skill-detail-row"><span class="skill-detail-label">{{ t('components.skillDetailDrawer.labelStatus') }}</span>{{ statusLabel(skill.status) }}</p>
        <p v-if="richDetail?.license" class="skill-detail-row"><span class="skill-detail-label">{{ t('components.skillDetailDrawer.labelLicense') }}</span>{{ richDetail.license }}</p>
        <p v-if="fmtDate(richDetail?.created_at)" class="skill-detail-row"><span class="skill-detail-label">{{ t('components.skillDetailDrawer.labelCreated') }}</span>{{ fmtDate(richDetail?.created_at) }}</p>
        <p v-if="fmtDate(richDetail?.updated_at)" class="skill-detail-row"><span class="skill-detail-label">{{ t('components.skillDetailDrawer.labelUpdated') }}</span>{{ fmtDate(richDetail?.updated_at) }}</p>

        <!-- 统计（clawhub）：下载/星标/安装，带单位显示。 -->
        <div v-if="richDetail && (richDetail.downloads || richDetail.stars || richDetail.installs)" class="skill-detail-stats">
          <span v-if="richDetail.downloads">↓ {{ t('components.skillDetailDrawer.statDownloads', { count: formatCount(richDetail.downloads) }) }}</span>
          <span v-if="richDetail.stars">★ {{ t('components.skillDetailDrawer.statStars', { count: formatCount(richDetail.stars) }) }}</span>
          <span v-if="richDetail.installs">⤓ {{ t('components.skillDetailDrawer.statInstalls', { count: formatCount(richDetail.installs) }) }}</span>
        </div>

        <!-- 关键词 -->
        <div v-if="richDetail?.keywords?.length" class="skill-detail-keywords">
          <n-tag v-for="kw in richDetail.keywords" :key="kw" size="tiny" :bordered="false">{{ kw }}</n-tag>
        </div>

        <!-- 完整描述（富详情优先，回退点击带入的描述） -->
        <p v-if="effectiveDescription" class="skill-detail-desc">{{ effectiveDescription }}</p>

        <!-- 版本列表：platform/clawhub 来源才有；builtin/self_created 无来源版本。 -->
        <div class="skill-detail-versions">
          <strong>{{ t('components.skillDetailDrawer.versionsTitle') }}</strong>
          <div v-if="!hasUpstream" class="state-text">{{ t('components.skillDetailDrawer.noVersions') }}</div>
          <div v-else-if="detailQuery.isLoading.value" class="state-text">{{ t('common.status.loading') }}</div>
          <p v-else-if="detailQuery.error.value" class="state-text danger">{{ t('components.skillDetailDrawer.drawerTitle') }}</p>
          <ul v-else-if="versions.length" class="skill-detail-version-list">
            <li v-for="(v, i) in versions" :key="v.version" class="skill-detail-version-item">
              <div class="skill-detail-version-head">
                <span class="skill-detail-version-num">v{{ v.version }}</span>
                <n-tag v-if="i === 0" size="tiny" type="success" :bordered="false">{{ t('components.skillDetailDrawer.tagLatest') }}</n-tag>
                <n-tag v-if="v.version === skill.version" size="tiny" type="info" :bordered="false">{{ t('components.skillDetailDrawer.tagCurrent') }}</n-tag>
                <span v-if="fmtDate(v.published_at)" class="skill-detail-version-date">{{ fmtDate(v.published_at) }}</span>
                <!-- 版本场景：每个版本可锁定加入助手版本。 -->
                <n-button
                  v-if="allowVersionPick"
                  size="tiny"
                  type="primary"
                  :loading="actionPending"
                  :disabled="existingNames.has(skill.name)"
                  @click="$emit('pick-version', v.version)"
                >
                  {{ existingNames.has(skill.name) ? t('components.skillDetailDrawer.alreadyAdded') : t('components.skillDetailDrawer.addThisVersion') }}
                </n-button>
                <!-- 平台管理员可下载该版本归档（platform=tar，clawhub=zip）。 -->
                <n-button
                  v-if="allowDownload"
                  size="tiny"
                  :loading="downloadingVersion === v.version"
                  @click="onDownload(v.version)"
                >
                  {{ t('components.skillDetailDrawer.downloadBtn') }}
                </n-button>
              </div>
              <div v-if="v.changelog" class="skill-detail-version-log">{{ v.changelog }}</div>
            </li>
          </ul>
          <div v-else class="state-text">{{ t('components.skillDetailDrawer.noVersionList') }}</div>
        </div>
      </div>
    </n-drawer-content>
  </n-drawer>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NDrawer, NDrawerContent, NTag, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { downloadSkillArchive, useSkillDetailQuery } from '@/api/hooks/useSkills'

// SkillDetail 是抽屉展示的数据，已安装行与市场卡片各取所需字段填充。
export interface SkillDetail {
  name: string
  source?: string
  source_ref?: string
  version?: string
  description?: string
  downloads?: number
  status?: string // 仅已安装列表有
}

const props = withDefaults(
  defineProps<{
    show: boolean
    skill: SkillDetail | null
    allowVersionPick?: boolean // 版本场景=true，版本行显示「添加此版本」
    actionPending?: boolean
    existingNames?: Set<string> // 已配置/已安装名集合，命中则禁用添加
    allowDownload?: boolean // 平台管理员=true，版本行显示「下载」按钮（platform/clawhub 来源）
  }>(),
  { allowVersionPick: false, actionPending: false, existingNames: () => new Set<string>(), allowDownload: false },
)
defineEmits<{ 'update:show': [boolean]; 'pick-version': [string] }>()

const { t } = useI18n()
const message = useMessage()
// downloadingVersion 记录正在下载的版本号，用于该行按钮显示 loading；null 表示无下载进行中。
const downloadingVersion = ref<string | null>(null)

// onDownload 下载指定版本的归档并触发浏览器保存；按来源（platform/clawhub）由后端返回 tar/zip。
// 失败（无权/网络/不存在）时 toast 错误，不抛到组件外。
async function onDownload(version: string) {
  const source = props.skill?.source
  const ref = props.skill?.source_ref
  if (!source || !ref) return
  downloadingVersion.value = version
  try {
    await downloadSkillArchive(source, ref, version)
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('components.skillDetailDrawer.downloadFailed'))
  } finally {
    downloadingVersion.value = null
  }
}

// hasUpstream：仅 platform/clawhub 来源有上游富详情/版本（builtin/self_created 无来源标识）。
const hasUpstream = computed(() => {
  const d = props.skill
  return Boolean(d?.source_ref && (d.source === 'platform' || d.source === 'clawhub'))
})
const detailParams = computed(() => ({
  source: hasUpstream.value ? props.skill?.source : undefined,
  ref: hasUpstream.value ? props.skill?.source_ref : undefined,
}))
const detailQuery = useSkillDetailQuery(detailParams)
const richDetail = computed(() => detailQuery.data.value?.detail ?? null)
const versions = computed(() => detailQuery.data.value?.versions ?? [])
const effectiveDescription = computed(
  () => richDetail.value?.description || props.skill?.description || '',
)

// sourceLabel：空来源（内置/自创）显示「内置」，避免「来源」一栏为空。
function sourceLabel(source?: string): string {
  if (source === 'platform') return t('components.skillDetailDrawer.sourcePlatform')
  if (source === 'clawhub') return 'ClawHub'
  return source || t('components.skillDetailDrawer.sourceBuiltin')
}
function statusLabel(status: string): string {
  const keys: Record<string, string> = {
    active: 'components.skillDetailDrawer.statusActive',
    pending: 'components.skillDetailDrawer.statusPending',
    builtin: 'components.skillDetailDrawer.statusBuiltin',
    self_created: 'components.skillDetailDrawer.statusSelfCreated',
  }
  return keys[status] ? t(keys[status]) : status
}
function fmtDate(v?: string | number): string {
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
}
function formatCount(n?: number): string {
  if (!n || n < 10000) return String(n ?? 0)
  const fmt = (val: number, unit: string) => `${val.toFixed(1).replace(/\.0$/, '')}${unit}`
  // 数量单位随语言切换：中文用「万」/「百万」，英文用 'K'/'M'。
  if (n >= 1_000_000) return fmt(n / 1_000_000, t('components.skillDetailDrawer.unitMillion'))
  return fmt(n / 10_000, t('components.skillDetailDrawer.unitTenThousand'))
}
</script>

<style scoped>
/* 详情抽屉样式：从 SkillManager.vue 原样迁入（行/标签/描述/版本列表/作者/统计/关键词）。 */
.skill-detail-row { margin: 4px 0; font-size: 13px; }
.skill-detail-label { display: inline-block; width: 56px; color: var(--text-muted, #888); }
.skill-detail-desc { margin: 12px 0; font-size: 13px; line-height: 1.6; white-space: pre-wrap; }
.skill-detail-versions { margin-top: 16px; }
.skill-detail-version-list { list-style: none; padding: 0; margin: 8px 0 0; }
.skill-detail-version-list li { font-size: 13px; }
.skill-detail-version-num { font-family: var(--font-mono, monospace); }
.skill-detail-author { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.skill-detail-avatar { width: 28px; height: 28px; border-radius: 50%; object-fit: cover; }
.skill-detail-author-name { font-weight: 600; font-size: 13px; }
.skill-detail-handle { color: var(--text-muted, #888); font-size: 12px; }
.skill-detail-stats { display: flex; flex-wrap: wrap; gap: 12px; margin: 10px 0; font-size: 12px; color: var(--text-muted, #888); }
.skill-detail-keywords { display: flex; flex-wrap: wrap; gap: 6px; margin: 8px 0; }
.skill-detail-version-item { display: block; padding: 8px 0; border-bottom: 1px solid var(--border-color, #eee); }
.skill-detail-version-head { display: flex; align-items: center; gap: 8px; }
.skill-detail-version-date { margin-left: auto; color: var(--text-muted, #999); font-size: 12px; }
.skill-detail-version-log { margin-top: 4px; font-size: 12px; color: var(--text-muted, #666); line-height: 1.5; }
</style>
