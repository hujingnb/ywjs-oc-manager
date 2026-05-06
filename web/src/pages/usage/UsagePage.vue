<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Usage · 用量报表</p>
          <h2>用量四维度</h2>
        </div>
        <div class="tab-group" role="tablist">
          <button
            v-for="tab in availableTabs"
            :key="tab.key"
            type="button"
            :class="['secondary-button', { active: activeTab === tab.key }]"
            @click="activeTab = tab.key"
          >
            {{ tab.label }}
          </button>
        </div>
      </div>

      <!-- platform_admin 专用：跨组织切换器 -->
      <div v-if="isPlatformAdmin && activeTab !== 'platform'" class="filter-row">
        <label>
          组织：
          <select v-model="selectedOrgId">
            <option v-for="org in organizations ?? []" :key="org.id" :value="org.id">
              {{ org.name }}
            </option>
          </select>
        </label>
      </div>

      <!-- platform 维度 -->
      <div v-if="activeTab === 'platform'">
        <div v-if="platformLoading" class="state-text">加载中…</div>
        <div v-else-if="platformError" class="state-text danger">查询失败：{{ platformError.message }}</div>
        <UsageSummary v-else :view="platformView ?? undefined" empty-text="暂无平台用量记录" />
      </div>

      <!-- organization 维度 -->
      <div v-else-if="activeTab === 'organization'">
        <div v-if="orgLoading" class="state-text">加载中…</div>
        <div v-else-if="orgError" class="state-text danger">查询失败：{{ orgError.message }}</div>
        <UsageSummary v-else :view="orgView ?? undefined" empty-text="该组织暂无应用用量记录" />
      </div>

      <!-- member 维度 -->
      <div v-else-if="activeTab === 'member'">
        <div class="filter-row">
          <label>
            成员 ID：
            <input v-model="memberIdInput" type="text" placeholder="user uuid" />
          </label>
        </div>
        <div v-if="memberLoading" class="state-text">加载中…</div>
        <div v-else-if="memberError" class="state-text danger">查询失败：{{ memberError.message }}</div>
        <UsageSummary v-else :view="memberView ?? undefined" empty-text="该成员暂无应用用量记录" />
      </div>

      <!-- app 维度 -->
      <div v-else-if="activeTab === 'app'">
        <div class="filter-row">
          <label>
            应用 ID：
            <input v-model="appIdInput" type="text" placeholder="app uuid" />
          </label>
        </div>
        <p class="state-text">应用维度详情请前往 <RouterLink to="/apps">应用列表</RouterLink> 查看。</p>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

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

// 平台管理员默认进 platform 视图，其他角色进 organization。
const activeTab = ref<TabKey>(isPlatformAdmin.value ? 'platform' : 'organization')

const availableTabs = computed(() => {
  const base: { key: TabKey; label: string }[] = [
    { key: 'organization', label: '组织' },
    { key: 'member', label: '成员' },
    { key: 'app', label: '应用' },
  ]
  if (isPlatformAdmin.value) base.push({ key: 'platform', label: '平台' })
  return base
})

// 平台管理员可切换组织；其他角色固定使用自己 org_id。
// 平台管理员通常没绑定组织（org_id 为空），首次拿到列表后自动选第一个。
const { data: organizations } = useOrganizationsQuery()
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
const memberRef = computed(() => (memberIdInput.value.trim() || undefined))
const { data: memberView, isLoading: memberLoading, error: memberError } = useMemberUsageQuery(orgRef, memberRef)

const appIdInput = ref('')

const platformEnabled = computed(() => isPlatformAdmin.value && activeTab.value === 'platform')
const { data: platformView, isLoading: platformLoading, error: platformError } = usePlatformUsageQuery(platformEnabled)
</script>

<style scoped>
.tab-group {
  display: flex;
  gap: 8px;
}

.tab-group .active {
  background: #276d5c;
  color: white;
}

.filter-row {
  display: flex;
  gap: 16px;
  margin-bottom: 12px;
  align-items: center;
}

.filter-row label {
  display: flex;
  align-items: center;
  gap: 6px;
}

.filter-row input,
.filter-row select {
  padding: 4px 8px;
  border: 1px solid #ccc;
  border-radius: 4px;
}
</style>
