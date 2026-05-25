<template>
  <div style="display: grid; gap: 18px">
    <n-card :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">{{ orgEyebrow }}</p>
            <h2 style="margin: 0">创建成员并初始化实例</h2>
          </div>
          <RouterLink class="secondary-button" to="/members">返回列表</RouterLink>
        </div>
      </template>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法创建成员。</div>
      <n-form v-else label-placement="top" @submit.prevent="onSubmit">
        <!-- 账号信息 -->
        <p class="form-section-label">账号信息</p>
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="用户名 *">
              <n-input v-model:value="form.username" placeholder="username" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="显示名 *">
              <n-input v-model:value="form.display_name" placeholder="显示名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="初始密码 *">
              <n-input v-model:value="form.password" type="password" placeholder="至少 8 位" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="角色">
              <n-select v-model:value="form.role" :options="roleOptions" />
            </n-form-item>
          </n-grid-item>
        </n-grid>

        <!-- 实例信息 -->
        <p class="form-section-label">实例信息</p>
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="实例名 *">
              <n-input v-model:value="form.app_name" placeholder="实例名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <!-- 助手版本从组织 allowlist 过滤，必选 -->
            <n-form-item label="助手版本 *">
              <n-select
                v-model:value="form.version_id"
                :options="versionOptions"
                :loading="versionsLoading || orgLoading"
                placeholder="请选择助手版本"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <RouterLink class="secondary-button" to="/members">取消</RouterLink>
              <n-button
                type="primary"
                attr-type="submit"
                :loading="creating"
                :disabled="!form.version_id"
              >
                {{ creating ? '提交中…' : '创建并初始化' }}
              </n-button>
            </n-space>
            <p v-if="versionValidationError" class="state-text danger">{{ versionValidationError }}</p>
            <p v-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <n-card v-if="lastResult" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">已创建</p>
            <h2 style="margin: 0">{{ lastResult.member.display_name }} · {{ lastResult.app.name }}</h2>
          </div>
          <n-tag type="success" size="small" :bordered="false">事务提交</n-tag>
        </div>
      </template>
      <p class="state-text">
        Job ID：{{ lastResult.job_id }} ｜ App 状态：{{ lastResult.app.status }} ｜ API key：{{ lastResult.app.api_key_status }}。
        当前实例尚未初始化容器，worker 会按 app_initialize 任务推进。
      </p>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, NTag, type SelectOption,
} from 'naive-ui'

import {
  useOnboardMember,
  type OnboardMemberPayload,
  type OnboardMemberResult,
} from '@/api/hooks/useMembers'
import { useAssistantVersionsQuery } from '@/api/hooks/useAssistantVersions'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
import { useAuthStore } from '@/stores/auth'

// CreateMemberPage 是组织成员一站式开通页，同时创建成员、初始应用和渠道配置。
// 助手版本从当前组织 allowlist 过滤，开通时必须选择。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
// effectiveOrgId 支持平台管理员指定组织，组织管理员则默认使用自身组织。
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 创建成员' : '组织 · 创建成员'))

// orgIdRef 转为 Ref<string | undefined> 供 vue-query hook 订阅。
const orgIdRef = computed(() => effectiveOrgId.value)

// 查询当前组织以获取 assistant_version_ids allowlist。
const { data: orgData, isLoading: orgLoading } = useOrganizationQuery(orgIdRef)
// 查询全部助手版本目录，与 allowlist 做交集。
const { data: versionsData, isLoading: versionsLoading } = useAssistantVersionsQuery()

// versionOptions 由组织 allowlist 与全量版本目录取交集，仅展示允许使用的版本。
const versionOptions = computed<SelectOption[]>(() => {
  const org = orgData.value
  const versions = versionsData.value
  if (!org || !versions) return []
  const allowedIds = new Set(org.assistant_version_ids ?? [])
  return versions
    .filter(v => allowedIds.has(v.id))
    .map(v => ({ label: v.name, value: v.id }))
})

const onboardMutation = useOnboardMember(effectiveOrgId)
// creating 是页面本地提交态，用于覆盖 mutation 返回前的按钮禁用和文案。
const creating = ref(false)
const errorMessage = ref<string | null>(null)
// lastResult 保存最近一次开通结果，供页面展示生成的成员和应用信息。
const lastResult = ref<OnboardMemberResult | null>(null)

// form 对齐 onboard API 请求体；version_id 必填，channel_type 固定 wechat。
const form = reactive<OnboardMemberPayload>({
  username: '',
  display_name: '',
  password: '',
  role: 'org_member',
  app_name: '',
  version_id: '',
  channel_type: 'wechat',
})

const roleOptions: SelectOption[] = [
  { label: '组织成员', value: 'org_member' },
  { label: '组织管理员', value: 'org_admin' },
]

// versionValidationError 在用户尝试提交但未选择版本时给出明确提示。
const versionValidationError = ref<string | null>(null)

// onSubmit 提交完整开通流程；成功后清空敏感密码和文本输入，失败时保留表单便于修正。
async function onSubmit() {
  // version_id 必填校验：未选择时阻断提交并给出提示。
  if (!form.version_id) {
    versionValidationError.value = '请选择助手版本'
    return
  }
  versionValidationError.value = null
  errorMessage.value = null
  creating.value = true
  try {
    const result = await onboardMutation.mutateAsync({ ...form })
    lastResult.value = result as OnboardMemberResult
    form.username = ''
    form.password = ''
    form.display_name = ''
    form.app_name = ''
    form.version_id = ''
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '创建失败'
  } finally {
    creating.value = false
  }
}
</script>

<style scoped>
.form-section-label {
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  color: var(--color-text-secondary);
  letter-spacing: 0.08em;
  margin: 12px 0 4px;
}
</style>
