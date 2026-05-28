<template>
  <n-form class="login-form" label-placement="top" @submit.prevent="onSubmit">
    <div style="margin-bottom: 24px">
      <p class="eyebrow login-eyebrow">Agent Runtime Manager</p>
      <h1 style="margin: 0">登录控制台</h1>
    </div>

    <n-form-item label="企业标识" path="orgCode">
      <n-input
        v-model:value="orgCode"
        autocomplete="organization"
        :input-props="{ id: 'org-code', 'aria-label': '企业标识' }"
        placeholder="企业用户填写，平台管理员留空"
      />
    </n-form-item>

    <n-form-item label="账号" path="username">
      <n-input
        v-model:value="username"
        autocomplete="username"
        :input-props="{ id: 'username', 'aria-label': '账号' }"
        placeholder="platform-admin"
      />
    </n-form-item>

    <n-form-item label="密码" path="password">
      <n-input
        v-model:value="password"
        type="password"
        autocomplete="current-password"
        :input-props="{ id: 'password', 'aria-label': '密码' }"
        placeholder="请输入密码"
        show-password-on="click"
      />
    </n-form-item>

    <p v-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>

    <n-button type="primary" attr-type="submit" :loading="auth.loading" style="width: 100%">
      {{ auth.loading ? '登录中…' : '登录' }}
    </n-button>
  </n-form>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NForm, NFormItem, NInput } from 'naive-ui'

import { useAuthStore } from '@/stores/auth'

// LoginPage 负责本地账号登录，并在登录成功后回跳原始受保护路径。
const auth = useAuthStore()
const router = useRouter()

const orgCode = ref('')
const username = ref('')
const password = ref('')
// errorMessage 只保存本次登录失败原因，下一次提交前会清空。
const errorMessage = ref<string | null>(null)

// onSubmit 调用 auth store 登录；redirect 查询参数由全局 401 处理器写入。
async function onSubmit() {
  errorMessage.value = null
  try {
    await auth.login(username.value, password.value, orgCode.value)
    const target = (router.currentRoute.value.query.redirect as string | undefined) ?? '/'
    await router.replace(target)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '登录失败'
  }
}
</script>

<style scoped>
.login-eyebrow {
  color: var(--color-brand-text);
}
</style>
