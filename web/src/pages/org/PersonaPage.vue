<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Org · Persona</p>
        <h2 style="margin: 0">组织 AI 人设</h2>
        <p v-if="personaSubtitle" class="state-text" style="margin: 4px 0 0">{{ personaSubtitle }}</p>
      </div>
    </template>

    <p v-if="!orgId" class="state-text">当前账号未关联组织</p>
    <p v-else-if="personaQuery.isLoading.value" class="state-text">加载中…</p>
    <p v-else-if="personaQuery.error.value" class="state-text danger">{{ personaQuery.error.value?.message }}</p>

    <n-form v-if="orgId" label-placement="top" @submit.prevent="onSubmit">
      <n-form-item label="系统提示词（必填）">
        <n-input v-model:value="form.system_prompt" type="textarea" :rows="6" />
      </n-form-item>
      <n-form-item label="对话规范（可选）">
        <n-input v-model:value="form.conversation_rules" type="textarea" :rows="3" />
      </n-form-item>
      <n-form-item label="禁止事项（可选）">
        <n-input v-model:value="form.forbidden_rules" type="textarea" :rows="3" />
      </n-form-item>
      <n-form-item label="回复风格（可选）">
        <n-input v-model:value="form.reply_style" type="textarea" :rows="2" />
      </n-form-item>
      <n-form-item>
        <n-checkbox v-model:checked="form.allow_member_override">
          允许成员实例通过 app_prompt 覆盖组织默认人设
        </n-checkbox>
      </n-form-item>
      <n-space align="center">
        <n-button type="primary" attr-type="submit" :disabled="!canEdit || mutation.isPending.value">
          {{ mutation.isPending.value ? '保存中…' : '保存新版本' }}
        </n-button>
        <span v-if="!canEdit" class="state-text">仅平台/组织管理员可编辑</span>
        <span v-else class="state-text">提交后会创建新版本（旧版本保留），下次容器重建生效</span>
      </n-space>
      <p v-if="feedback" class="state-text" :class="{ danger: feedbackError }" style="margin-top: 8px">{{ feedback }}</p>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NButton, NCard, NCheckbox, NForm, NFormItem, NInput, NSpace } from 'naive-ui'

import { usePersonaMutation, usePersonaQuery, type PersonaDTO } from '@/api/hooks/usePersona'
import { useAuthStore } from '@/stores/auth'

// PersonaPage 管理组织默认 AI 人设，组织成员只能查看，管理员可保存新版本。
const auth = useAuthStore()

// orgId 来自当前登录账号；未绑定组织时查询和保存都不会具备有效目标。
const orgId = computed<string | undefined>(() => auth.user?.org_id ?? undefined)

// personaSubtitle 展示当前生效版本，未配置时给出空状态说明。
const personaSubtitle = computed(() => {
  if (!persona.value) return '尚未配置人设'
  return `当前生效版本：v${persona.value.version}`
})

const personaQuery = usePersonaQuery(orgId)
const persona = computed<PersonaDTO | null>(() => personaQuery.data.value ?? null)
const mutation = usePersonaMutation(orgId)

// form 对齐人设保存 API；空的可选规则在提交时转成 undefined。
const form = reactive({
  system_prompt: '',
  conversation_rules: '',
  forbidden_rules: '',
  reply_style: '',
  allow_member_override: false,
})

// 服务端返回的人设变化时同步到表单，避免编辑旧版本内容。
watch(persona, (value) => {
  if (!value) return
  form.system_prompt = value.system_prompt
  form.conversation_rules = value.conversation_rules ?? ''
  form.forbidden_rules = value.forbidden_rules ?? ''
  form.reply_style = value.reply_style ?? ''
  form.allow_member_override = value.allow_member_override
}, { immediate: true })

// canEdit 控制编辑表单和保存按钮，后端仍根据角色做最终权限校验。
const canEdit = computed(() => {
  const role = auth.user?.role
  return role === 'platform_admin' || role === 'org_admin'
})

const feedback = ref('')
const feedbackError = ref(false)

// onSubmit 保存新版本人设，成功后显示版本号，失败时显示接口错误。
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
