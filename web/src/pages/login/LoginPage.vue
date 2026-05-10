<template>
  <n-form class="login-form" label-placement="top" @submit.prevent="onSubmit">
    <div style="margin-bottom: 24px">
      <p class="eyebrow">OpenClaw Manager</p>
      <h1 style="margin: 0">登录控制台</h1>
    </div>

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

const auth = useAuthStore()
const router = useRouter()

const username = ref('')
const password = ref('')
const errorMessage = ref<string | null>(null)

async function onSubmit() {
  errorMessage.value = null
  try {
    await auth.login(username.value, password.value)
    const target = (router.currentRoute.value.query.redirect as string | undefined) ?? '/'
    await router.replace(target)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '登录失败'
  }
}
</script>
