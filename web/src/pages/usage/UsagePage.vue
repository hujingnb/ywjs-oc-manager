<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Usage · 用量报表</p>
        <h2 style="margin: 0">用量四维度</h2>
      </div>
    </template>

    <n-tabs v-model:value="activeTab" type="line">
      <n-tab-pane name="organization" tab="组织">
        <n-space v-if="isPlatformAdmin" align="center" style="margin-bottom: 12px">
          <span>组织：</span>
          <n-select
            v-model:value="selectedOrgId"
            :options="orgOptions"
            style="width: 220px"
            placeholder="选择组织"
          />
        </n-space>
        <div v-if="orgLoading" class="state-text">加载中…</div>
        <div v-else-if="orgError" class="state-text danger">查询失败：{{ orgError.message }}</div>
        <UsageSummary v-else :view="orgView ?? undefined" empty-text="该组织暂无应用用量记录" />
      </n-tab-pane>

      <n-tab-pane name="member" tab="成员">
        <n-space align="center" style="margin-bottom: 12px" :wrap="false">
          <n-space v-if="isPlatformAdmin" align="center">
            <span>组织：</span>
            <n-select
              v-model:value="selectedOrgId"
              :options="orgOptions"
              style="width: 220px"
              placeholder="选择组织"
            />
          </n-space>
          <n-space align="center">
            <span>成员 ID：</span>
            <n-input v-model:value="memberIdInput" placeholder="user uuid" style="width: 240px" />
          </n-space>
        </n-space>
        <div v-if="memberLoading" class="state-text">加载中…</div>
        <div v-else-if="memberError" class="state-text danger">查询失败：{{ memberError.message }}</div>
        <UsageSummary v-else :view="memberView ?? undefined" empty-text="该成员暂无应用用量记录" />
      </n-tab-pane>

      <n-tab-pane name="app" tab="应用">
        <n-space align="center" style="margin-bottom: 12px">
          <span>应用 ID：</span>
          <n-input v-model:value="appIdInput" placeholder="app uuid" style="width: 240px" />
        </n-space>
        <p class="state-text">应用维度详情请前往 <RouterLink to="/apps">应用列表</RouterLink> 查看。</p>
      </n-tab-pane>

      <n-tab-pane v-if="isPlatformAdmin" name="platform" tab="平台">
        <div v-if="platformLoading" class="state-text">加载中…</div>
        <div v-else-if="platformError" class="state-text danger">查询失败：{{ platformError.message }}</div>
        <UsageSummary v-else :view="platformView ?? undefined" empty-text="暂无平台用量记录" />
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { NCard, NInput, NSelect, NSpace, NTabPane, NTabs } from 'naive-ui'

import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import {
  useMemberUsageQuery,
  useOrgUsageQuery,
  usePlatformUsageQuery,
} from '@/api/hooks/useUsage'
import { useAuthStore } from '@/stores/auth'

import UsageSummary from './UsageSummary.vue'

type TabKey = 'organization' | 'member' | 'app' | 'platform'

const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.user?.role === 'platform_admin')

const activeTab = ref<TabKey>(isPlatformAdmin.value ? 'platform' : 'organization')

const { data: organizations } = useOrganizationsQuery()
const orgOptions = computed(() =>
  (organizations.value ?? []).map((o) => ({ label: o.name, value: o.id })),
)

const selectedOrgId = ref<string | undefined>(auth.user?.org_id)
watch(organizations, (orgs) => {
  if (isPlatformAdmin.value && !selectedOrgId.value && orgs && orgs.length > 0) {
    selectedOrgId.value = orgs[0].id
  }
})
const effectiveOrgId = computed(() =>
  isPlatformAdmin.value ? selectedOrgId.value : auth.user?.org_id,
)

const orgRef = computed(() => effectiveOrgId.value)
const { data: orgView, isLoading: orgLoading, error: orgError } = useOrgUsageQuery(orgRef)

const memberIdInput = ref('')
const memberRef = computed(() => memberIdInput.value.trim() || undefined)
const { data: memberView, isLoading: memberLoading, error: memberError } = useMemberUsageQuery(orgRef, memberRef)

const appIdInput = ref('')

const platformEnabled = computed(() => isPlatformAdmin.value && activeTab.value === 'platform')
const { data: platformView, isLoading: platformLoading, error: platformError } = usePlatformUsageQuery(platformEnabled)
</script>
