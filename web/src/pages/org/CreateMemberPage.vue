<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">{{ orgEyebrow }}</p>
          <h2>创建成员并初始化应用</h2>
        </div>
        <RouterLink class="secondary-button" to="/members">返回列表</RouterLink>
      </div>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法创建成员。</div>
      <form v-else class="form-grid" @submit.prevent="onSubmit">
        <fieldset class="form-grid-full form-section">
          <legend>账号信息</legend>
          <div class="form-grid">
            <label>
              <span>用户名 *</span>
              <input v-model.trim="form.username" required type="text" autocomplete="username" />
            </label>
            <label>
              <span>显示名 *</span>
              <input v-model.trim="form.display_name" required type="text" />
            </label>
            <label>
              <span>初始密码 *</span>
              <input v-model="form.password" required type="password" autocomplete="new-password" />
            </label>
            <label>
              <span>角色</span>
              <select v-model="form.role">
                <option value="org_member">组织成员</option>
                <option value="org_admin">组织管理员</option>
              </select>
            </label>
          </div>
        </fieldset>

        <fieldset class="form-grid-full form-section">
          <legend>应用信息</legend>
          <div class="form-grid">
            <label>
              <span>应用名 *</span>
              <input v-model.trim="form.app_name" required type="text" />
            </label>
            <label>
              <span>人设模式</span>
              <select v-model="form.persona_mode">
                <option value="org_inherited">沿用组织人设</option>
                <option value="app_override">应用覆盖</option>
              </select>
            </label>
            <label class="form-grid-full">
              <span>应用 prompt（可选）</span>
              <textarea v-model.trim="form.app_prompt" rows="3"></textarea>
            </label>
            <label class="form-grid-full">
              <span>Runtime 节点 ID（可选）</span>
              <input v-model.trim="form.runtime_node_id" placeholder="留空由系统自动选择" type="text" />
            </label>
          </div>
        </fieldset>

        <div class="form-actions">
          <RouterLink class="secondary-button" to="/members">取消</RouterLink>
          <button class="primary-button" type="submit" :disabled="creating">
            {{ creating ? '提交中…' : '创建并初始化' }}
          </button>
        </div>
        <p v-if="errorMessage" class="state-text danger form-grid-full">{{ errorMessage }}</p>
      </form>
    </section>

    <section v-if="lastResult" class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">已创建</p>
          <h2>{{ lastResult.member.display_name }} · {{ lastResult.app.name }}</h2>
        </div>
        <span class="status-pill success">事务提交</span>
      </div>
      <p class="state-text">
        Job ID：{{ lastResult.job_id }} ｜ App 状态：{{ lastResult.app.status }} ｜ API key：{{ lastResult.app.api_key_status }}。
        当前应用尚未初始化容器，worker 会按 app_initialize 任务推进。
      </p>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'

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
    form.runtime_node_id = ''
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '创建失败'
  } finally {
    creating.value = false
  }
}
</script>

<style scoped>
.form-section {
  margin: 0;
  padding: 14px;
  border: 1px solid #dfe5ee;
  border-radius: 8px;
}

.form-section legend {
  padding: 0 6px;
  color: #66758a;
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}
</style>
