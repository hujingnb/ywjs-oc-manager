<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Usage · 用量报表</p>
        <h2 style="margin: 0">用量四维度</h2>
      </div>
    </template>

    <n-tabs v-model:value="activeTab" type="line">
      <n-tab-pane v-if="!isOrgMember" name="organization" tab="企业">
        <n-space v-if="isPlatformAdmin" align="center" style="margin-bottom: 12px">
          <span>企业：</span>
          <n-select
            v-model:value="selectedOrgId"
            :options="orgOptions"
            filterable
            style="width: 220px"
            placeholder="选择企业"
          />
        </n-space>
        <div v-if="orgLoading" class="state-text">加载中…</div>
        <div v-else-if="orgError" class="state-text danger">查询失败：{{ orgError.message }}</div>
        <UsageSummary
          v-else
          :view="orgView ?? undefined"
          :billing-status="billingStatus ?? undefined"
          empty-text="该企业暂无实例用量记录"
        />
      </n-tab-pane>

      <n-tab-pane name="member" :tab="isOrgMember ? '我的用量' : '成员'">
        <n-space v-if="!isOrgMember" align="center" style="margin-bottom: 12px" :wrap="false">
          <n-space v-if="isPlatformAdmin" align="center">
            <span>企业：</span>
            <n-select
              v-model:value="selectedOrgId"
              :options="orgOptions"
              filterable
              style="width: 220px"
              placeholder="选择企业"
            />
          </n-space>
          <n-space align="center">
            <span>成员：</span>
            <n-select
              v-model:value="selectedMemberId"
              :options="memberOptions"
              filterable
              clearable
              style="width: 280px"
              placeholder="搜索成员"
            />
          </n-space>
        </n-space>
        <div v-if="memberLoading" class="state-text">加载中…</div>
        <div v-else-if="memberError" class="state-text danger">查询失败：{{ memberError.message }}</div>
        <UsageSummary
          v-else
          :view="memberView ?? undefined"
          :billing-status="billingStatus ?? undefined"
          empty-text="暂无实例用量记录"
        />
      </n-tab-pane>

      <n-tab-pane name="app" tab="实例">
        <n-space align="center" style="margin-bottom: 12px" :wrap="false">
          <n-space v-if="isPlatformAdmin" align="center">
            <span>企业：</span>
            <n-select
              v-model:value="selectedOrgId"
              :options="orgOptions"
              filterable
              style="width: 220px"
              placeholder="选择企业"
            />
          </n-space>
          <span>实例：</span>
          <n-select
            v-model:value="selectedAppId"
            :options="appOptions"
            filterable
            clearable
            style="width: 300px"
            placeholder="搜索实例"
          />
        </n-space>
        <div v-if="appLoading" class="state-text">加载中…</div>
        <div v-else-if="appError" class="state-text danger">查询失败：{{ appError.message }}</div>
        <div v-else-if="selectedApp && !selectedApp.newapi_key_id" class="state-text">
          该实例尚未绑定 new-api key，暂无实例维度用量。
        </div>
        <UsageSummary
          v-else
          :view="appView ?? undefined"
          :billing-status="billingStatus ?? undefined"
          empty-text="暂无实例用量记录"
        />
      </n-tab-pane>

      <n-tab-pane v-if="isPlatformAdmin" name="platform" tab="平台">
        <div v-if="platformLoading" class="state-text">加载中…</div>
        <div v-else-if="platformError" class="state-text danger">查询失败：{{ platformError.message }}</div>
        <UsageSummary
          v-else
          :view="platformView ?? undefined"
          :billing-status="billingStatus ?? undefined"
          empty-text="暂无平台用量记录"
        />
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NCard, NSelect, NSpace, NTabPane, NTabs } from 'naive-ui'

import { useAppUsageQuery, useAppsByOrgQuery } from '@/api/hooks/useApps'
import { useMembersQuery } from '@/api/hooks/useMembers'
import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import { useBillingStatusQuery } from '@/api/hooks/useRecharge'
import {
  useMemberUsageQuery,
  useOrgUsageQuery,
  usePlatformUsageQuery,
} from '@/api/hooks/useUsage'
import { useAuthStore } from '@/stores/auth'

import UsageSummary from './UsageSummary.vue'

// UsagePage 聚合组织、成员、应用和平台四类用量入口，并按角色裁剪可见查询。
type TabKey = 'organization' | 'member' | 'app' | 'platform'

const auth = useAuthStore()
// isPlatformAdmin/isOrgMember 控制 tab 可见性和查询启用条件。
const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')
const isOrgMember = computed(() => auth.user?.role === 'org_member')

// 普通成员只允许查询自己的用量；默认落在"成员"tab。
const activeTab = ref<TabKey>(
  isPlatformAdmin.value ? 'platform' : isOrgMember.value ? 'member' : 'organization',
)

const { data: organizations } = useOrganizationsQuery(() => isPlatformAdmin.value)
// orgOptions 仅平台管理员使用，用于切换查看不同组织的用量。
const orgOptions = computed(() =>
  (organizations.value ?? []).map((o) => ({ label: `${o.name} · ${o.status}`, value: o.id })),
)

const selectedOrgId = ref<string | undefined>(auth.user?.org_id)
// 平台管理员首次拿到组织列表时默认选中第一个组织，避免组织维度空查询。
watch(organizations, (orgs) => {
  if (isPlatformAdmin.value && !selectedOrgId.value && orgs && orgs.length > 0) {
    selectedOrgId.value = orgs[0].id
  }
})
const effectiveOrgId = computed(() =>
  isPlatformAdmin.value ? selectedOrgId.value : auth.user?.org_id,
)
const { data: billingStatus } = useBillingStatusQuery()

// 组织维度用量对普通成员不开放，前端不发起查询避免无谓 403。
// 成员维度仍需要 org_id 作为权限边界，因此单独保留 memberOrgRef。
const orgUsageRef = computed(() => (isOrgMember.value ? undefined : effectiveOrgId.value))
const memberOrgRef = computed(() => effectiveOrgId.value)
const { data: orgView, isLoading: orgLoading, error: orgError } = useOrgUsageQuery(orgUsageRef)

// 普通成员强制锁定为查询自身的用量，UI 上不暴露成员 ID 输入框。
const selectedMemberId = ref(isOrgMember.value ? auth.user?.id ?? '' : '')
const memberListOrgRef = computed(() => (isOrgMember.value ? undefined : effectiveOrgId.value))
const { data: members } = useMembersQuery(memberListOrgRef)

// effectiveMemberId 把"成员 ID 必须落在当前 members 列表里"作为硬约束，
// 避免切换组织瞬间 vue-query 还以旧 memberId + 新 orgId 发查询。
// members 列表未加载时回退到 undefined，让 watch(members) auto-select 接管。
const effectiveMemberId = computed<string | undefined>(() => {
  if (isOrgMember.value) return auth.user?.id
  const id = selectedMemberId.value
  if (!id) return undefined
  if (!members.value) return undefined
  return members.value.some((m) => m.id === id) ? id : undefined
})

const memberRef = effectiveMemberId
const { data: memberView, isLoading: memberLoading, error: memberError } = useMemberUsageQuery(memberOrgRef, memberRef)
const memberOptions = computed(() =>
  (members.value ?? []).map((member) => ({
    label: `${member.display_name || member.username} · ${member.username}`,
    value: member.id,
  })),
)

const selectedAppId = ref<string | undefined>()
const { data: apps } = useAppsByOrgQuery(effectiveOrgId)

// effectiveAppId 同 effectiveMemberId，消除跨组织残留。
const effectiveAppId = computed<string | undefined>(() => {
  const id = selectedAppId.value
  if (!id) return undefined
  if (!apps.value) return undefined
  return apps.value.some((a) => a.id === id) ? id : undefined
})

const appOptions = computed(() =>
  (apps.value ?? []).map((app) => ({
    label: `${app.name} · ${app.status}`,
    value: app.id,
  })),
)
const selectedApp = computed(() => (apps.value ?? []).find((app) => app.id === selectedAppId.value))
const appUsageContext = computed(() => {
  if (!selectedApp.value?.newapi_key_id) return undefined
  return {
    orgId: selectedApp.value.org_id,
    ownerUserId: selectedApp.value.owner_user_id,
    newapiKeyId: selectedApp.value.newapi_key_id,
  }
})
const { data: appView, isLoading: appLoading, error: appError } = useAppUsageQuery(effectiveAppId, appUsageContext)

// 列表加载后，如果 effective ID 还没解析出来（要么没选过、要么旧选中
// 不在新列表里），自动选第一项。条件用 effective.value 而非
// selectedXxx.value，确保跨组织时也能正确 auto-select。immediate 保证
// 列表已经在 setup 阶段拿到时也能立即选中。
watch(
  members,
  (list) => {
    if (!isOrgMember.value && effectiveMemberId.value === undefined && list && list.length > 0) {
      selectedMemberId.value = list[0].id
    }
  },
  { immediate: true },
)

watch(
  apps,
  (list) => {
    if (effectiveAppId.value === undefined && list && list.length > 0) {
      selectedAppId.value = list[0].id
    }
  },
  { immediate: true },
)

// platformEnabled 只在平台管理员打开平台 tab 时启用查询，减少后台不必要请求。
const platformEnabled = computed(() => isPlatformAdmin.value && activeTab.value === 'platform')
const { data: platformView, isLoading: platformLoading, error: platformError } = usePlatformUsageQuery(platformEnabled)
</script>
