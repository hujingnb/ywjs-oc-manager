<template>
  <form class="login-form" @submit.prevent="onSubmit">
    <div>
      <p class="eyebrow">OpenClaw Manager</p>
      <h1>登录控制台</h1>
    </div>

    <label>
      <span>账号</span>
      <input
        v-model.trim="username"
        autocomplete="username"
        placeholder="platform-admin"
        type="text"
        required
      />
    </label>

    <label>
      <span>密码</span>
      <input
        v-model="password"
        autocomplete="current-password"
        placeholder="请输入密码"
        type="password"
        required
      />
    </label>

    <p v-if="errorMessage" class="state-text danger">{{ errorMessage }}</p>

    <button class="primary-button" type="submit" :disabled="auth.loading">
      {{ auth.loading ? '登录中…' : '登录' }}
    </button>
  </form>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

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
