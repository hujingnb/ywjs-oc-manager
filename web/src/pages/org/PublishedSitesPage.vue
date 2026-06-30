<template>
  <div style="display: grid; gap: 18px">
    <!-- 已发布站点列表 -->
    <DataTableList
      :title="t('org.publishedSites.list.title')"
      :eyebrow="orgEyebrow"
      :columns="columns"
      :data="sites ?? []"
      :loading="isLoading || organizationsLoading"
      :error-message="errorMessage"
      :row-key="(row: SiteResult) => row.id"
    >
      <template #toolbar>
        <!-- 平台管理员可切换组织；组织管理员默认展示自身组织 -->
        <n-select
          v-if="isPlatformAdmin"
          v-model:value="selectedOrgId"
          :options="orgOptions"
          style="width: 220px"
          :placeholder="t('org.publishedSites.list.selectOrg')"
        />
      </template>
    </DataTableList>

    <!-- 证书状态面板：企业管理员只读（canRetry=false），平台管理员可重试（canRetry=true）。
         两处复用同一 WebPublishCertPanel 组件，仅 canRetry 不同。
         模板内 computed ref 自动解包，effectiveOrgId/isPlatformAdmin 无需 .value。 -->
    <WebPublishCertPanel
      v-if="effectiveOrgId"
      :org-id="effectiveOrgId"
      :can-retry="isPlatformAdmin"
    />

    <!-- 下线确认弹窗：要求用户确认后才调用下线接口，避免误操作 -->
    <ConfirmActionModal
      :visible="!!siteToTakedown"
      :title="t('org.publishedSites.modal.takedownTitle')"
      :message="siteToTakedown ? t('org.publishedSites.modal.takedownMessage', { url: siteToTakedown.url ?? siteToTakedown.id }) : ''"
      :confirm-label="t('org.publishedSites.modal.takedownConfirm')"
      :busy="takedownMutation.isPending.value"
      @confirm="onConfirmTakedown"
      @cancel="siteToTakedown = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NSelect } from 'naive-ui'

import { usePublishedSitesQuery, useTakedownSite, useRenewSite } from '@/api/hooks/useWebPublish'
import { formatSiteStatus } from '@/domain/status'
import { statusColumn, actionColumn, linkColumn, timeColumn } from '@/components/columns'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import DataTableList from '@/components/DataTableList.vue'
import WebPublishCertPanel from '@/components/WebPublishCertPanel.vue'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { useAuthStore } from '@/stores/auth'
import type { SiteResult } from '@/api'

// PublishedSitesPage 展示企业已发布站点列表，并内嵌证书状态面板。
// 平台管理员可跨组织查看和重试证书；企业管理员只读证书信息并管理站点。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const { t } = useI18n()

// 平台管理员通过组织选择器切换目标企业；企业管理员默认使用自身 org_id。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))

// 副标题文案根据角色区分：平台管理员跨企业视角 / 企业管理员本企业视角。
const orgEyebrow = computed(() =>
  isPlatformAdmin.value
    ? t('org.publishedSites.page.eyebrowPlatform')
    : t('org.publishedSites.page.eyebrowOrg'),
)

// 查询目标企业的已发布站点列表；effectiveOrgId 为 undefined 时 query 暂停。
const { data: sites, isLoading } = usePublishedSitesQuery(effectiveOrgId)

// 下线和续期 mutation，成功后由 hook 自动失效站点列表缓存。
const takedownMutation = useTakedownSite(effectiveOrgId)
const renewMutation = useRenewSite(effectiveOrgId)

// siteToTakedown 保存二次确认中的目标站点，确认后才调用下线接口。
const siteToTakedown = ref<SiteResult | null>(null)

// errorMessage 区分平台管理员无可选组织和组织用户无归属两种空态。
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value)
    return isPlatformAdmin.value
      ? t('org.publishedSites.state.noOrg')
      : t('org.publishedSites.state.noOrgLinked')
  return undefined
})

// formatSiteSize 将字节数格式化为人类可读的 KB/MB 字符串。
// 小于 1 MB 时显示 KB，否则显示 MB（保留两位小数）。
function formatSiteSize(bytes: number | undefined): string {
  if (bytes == null) return '—'
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}

// columns 使用 computed 确保语言切换时列头文案和操作按钮文案响应式更新。
const columns = computed(() => [
  // URL 列：点击在新标签页打开站点；使用 linkColumn 统一展示风格。
  linkColumn<SiteResult>({
    title: t('org.publishedSites.table.url'),
    key: 'url',
    text: r => r.url ?? r.id,
    onClick: r => { if (r.url) window.open(r.url, '_blank', 'noopener,noreferrer') },
  }),
  // 发布者列：显示发布该站点的用户（显示名优先，回退登录名，再回退占位符）。
  {
    title: t('org.publishedSites.table.owner'),
    key: 'owner',
    render: (r: SiteResult) => r.owner_display_name || r.owner_username || '—',
  },
  // 状态列：active/disabled/expired 映射到不同颜色的徽章。
  statusColumn<SiteResult>(
    t('org.publishedSites.table.status'),
    r => formatSiteStatus(r.status),
  ),
  // 到期时间列：显示格式化的本地时间，未设置时展示占位符。
  timeColumn<SiteResult>(
    t('org.publishedSites.table.expiresAt'),
    r => r.expires_at,
    { key: 'expires_at' },
  ),
  // 大小列：将 size_bytes 格式化为 KB/MB 字符串。
  {
    title: t('org.publishedSites.table.size'),
    key: 'size',
    render: (r: SiteResult) => formatSiteSize(r.size_bytes),
  },
  // 操作列：续期（所有状态可用）；下线（仅 active 状态显示，type=error）。
  actionColumn<SiteResult>([
    {
      label: t('org.publishedSites.actions.renew'),
      type: 'primary',
      // 续期正在进行中时禁用按钮，避免重复提交；完成后 hook 自动失效列表缓存。
      disabled: () => renewMutation.isPending.value,
      onClick: r => renewMutation.mutate(r.id),
    },
    {
      label: t('org.publishedSites.actions.takedown'),
      type: 'error',
      // 下线仅对 active 站点有意义；其他状态已下线或已过期，无需再次下线。
      hidden: r => r.status !== 'active',
      onClick: r => { siteToTakedown.value = r },
    },
  ]),
])

// onConfirmTakedown 确认后调用下线接口，完成后关闭弹窗。
// 失败只写控制台，避免弹窗残留阻塞后续操作。
async function onConfirmTakedown() {
  if (!siteToTakedown.value) return
  try {
    await takedownMutation.mutateAsync(siteToTakedown.value.id)
  } catch (err) {
    console.error('站点下线失败', err)
  } finally {
    siteToTakedown.value = null
  }
}
</script>
