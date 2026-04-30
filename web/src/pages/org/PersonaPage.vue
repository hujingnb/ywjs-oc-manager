<template>
  <main class="dashboard-main">
    <section class="panel">
      <DataTableToolbar title="组织 AI 人设" eyebrow="Org · Persona" :subtitle="personaSubtitle" />

      <p v-if="!orgId" class="state-text">当前账号未关联组织</p>
      <p v-else-if="personaQuery.isLoading.value" class="state-text">加载中…</p>
      <p v-else-if="personaQuery.error.value" class="state-text danger">{{ personaQuery.error.value?.message }}</p>

      <form v-if="orgId" class="form-stack" @submit.prevent="onSubmit">
        <label>
          <span class="label">系统提示词（必填）</span>
          <textarea v-model="form.system_prompt" rows="6" required />
        </label>
        <label>
          <span class="label">对话规范（可选）</span>
          <textarea v-model="form.conversation_rules" rows="3" />
        </label>
        <label>
          <span class="label">禁止事项（可选）</span>
          <textarea v-model="form.forbidden_rules" rows="3" />
        </label>
        <label>
          <span class="label">回复风格（可选）</span>
          <textarea v-model="form.reply_style" rows="2" />
        </label>
        <label class="checkbox-row">
          <input v-model="form.allow_member_override" type="checkbox" />
          <span>允许成员应用通过 app_prompt 覆盖组织默认人设</span>
        </label>

        <div class="actions-row">
          <button class="primary-button" type="submit" :disabled="!canEdit || mutation.isPending.value">
            {{ mutation.isPending.value ? '保存中…' : '保存新版本' }}
          </button>
          <span v-if="!canEdit" class="state-text">仅平台/组织管理员可编辑</span>
          <span v-else class="state-text">提交后会创建新版本（旧版本保留），下次容器重建生效</span>
        </div>

        <p v-if="feedback" class="state-text" :class="{ danger: feedbackError }">{{ feedback }}</p>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'

import { usePersonaMutation, usePersonaQuery, type PersonaDTO } from '@/api/hooks/usePersona'
import DataTableToolbar from '@/components/DataTableToolbar.vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

const orgId = computed<string | undefined>(() => auth.user?.org_id ?? undefined)

const personaSubtitle = computed(() => {
  if (!persona.value) return '尚未配置人设'
  return `当前生效版本：v${persona.value.version}`
})

const personaQuery = usePersonaQuery(orgId)
const persona = computed<PersonaDTO | null>(() => personaQuery.data.value ?? null)
const mutation = usePersonaMutation(orgId)

const form = reactive({
  system_prompt: '',
  conversation_rules: '',
  forbidden_rules: '',
  reply_style: '',
  allow_member_override: false,
})

watch(persona, (value) => {
  if (!value) return
  form.system_prompt = value.system_prompt
  form.conversation_rules = value.conversation_rules ?? ''
  form.forbidden_rules = value.forbidden_rules ?? ''
  form.reply_style = value.reply_style ?? ''
  form.allow_member_override = value.allow_member_override
}, { immediate: true })

const canEdit = computed(() => {
  const role = auth.user?.role
  return role === 'platform_admin' || role === 'org_admin'
})

const feedback = ref('')
const feedbackError = ref(false)

async function onSubmit() {
  feedback.value = ''
  feedbackError.value = false
  try {
    const result = await mutation.mutateAsync({
      system_prompt: form.system_prompt,
      conversation_rules: form.conversation_rules || undefined,
      forbidden_rules: form.forbidden_rules || undefined,
      reply_style: form.reply_style || undefined,
      allow_member_override: form.allow_member_override,
    })
    feedback.value = `已保存版本 v${result.version}`
  } catch (err: unknown) {
    feedbackError.value = true
    feedback.value = err instanceof Error ? err.message : '保存失败'
  }
}
</script>

<style scoped>
.form-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-top: 16px;
}

.form-stack label {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-stack textarea {
  width: 100%;
  padding: 8px;
  border: 1px solid rgba(0, 0, 0, 0.15);
  border-radius: 6px;
  font-family: inherit;
  font-size: 13px;
}

.label {
  font-size: 13px;
  color: rgba(0, 0, 0, 0.65);
  font-weight: 500;
}

.checkbox-row {
  flex-direction: row;
  align-items: center;
  gap: 8px;
}

.actions-row {
  display: flex;
  gap: 12px;
  align-items: center;
}

.danger {
  color: rgba(220, 38, 38, 1);
}
</style>
