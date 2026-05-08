<template>
  <div style="display: grid; gap: 18px">
    <n-card :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">{{ orgEyebrow }}</p>
            <h2 style="margin: 0">创建成员并初始化应用</h2>
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

        <!-- 应用信息 -->
        <p class="form-section-label">应用信息</p>
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="应用名 *">
              <n-input v-model:value="form.app_name" placeholder="应用名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="人设模式">
              <n-select v-model:value="form.persona_mode" :options="personaModeOptions" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="应用 prompt（可选）">
              <n-input v-model:value="form.app_prompt" type="textarea" :rows="3" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <RouterLink class="secondary-button" to="/members">取消</RouterLink>
              <n-button type="primary" attr-type="submit" :loading="creating">
                {{ creating ? '提交中…' : '创建并初始化' }}
              </n-button>
            </n-space>
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
        当前应用尚未初始化容器，worker 会按 app_initialize 任务推进。
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
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 创建成员' : '组织 · 创建成员'))

const onboardMutation = useOnboardMember(effectiveOrgId)
const creating = ref(false)
const errorMessage = ref<string | null>(null)
const lastResult = ref<OnboardMemberResult | null>(null)

const form = reactive<OnboardMemberPayload>({
  username: '',
  display_name: '',
  password: '',
  role: 'org_member',
  app_name: '',
  persona_mode: 'org_inherited',
  channel_type: 'wechat',
})

const roleOptions: SelectOption[] = [
  { label: '组织成员', value: 'org_member' },
  { label: '组织管理员', value: 'org_admin' },
]

const personaModeOptions: SelectOption[] = [
  { label: '沿用组织人设', value: 'org_inherited' },
  { label: '应用覆盖', value: 'app_override' },
]

async function onSubmit() {
  errorMessage.value = null
  creating.value = true
  try {
    const result = await onboardMutation.mutateAsync({ ...form })
    lastResult.value = result as OnboardMemberResult
    form.username = ''
    form.password = ''
    form.display_name = ''
    form.app_name = ''
    form.app_prompt = ''
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
  color: #8A94C6;
  letter-spacing: 0.08em;
  margin: 12px 0 4px;
}
</style>
