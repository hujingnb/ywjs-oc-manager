<template>
  <!-- 登录卡片：嵌入 AuthLayout 的右侧登录控制台外框中，承载本地账号登录表单。 -->
  <form class="login-card" @submit.prevent="onSubmit">
    <p class="login-brand">AGENT RUNTIME MANAGER</p>
    <h2 class="login-heading">登录控制台</h2>

    <div class="login-field">
      <label for="org-code">企业标识</label>
      <div class="login-input-wrap">
        <input
          id="org-code"
          v-model="orgCode"
          type="text"
          autocomplete="organization"
          aria-label="企业标识"
          placeholder="企业用户填写，平台管理员留空"
        />
      </div>
    </div>

    <div class="login-field">
      <label for="username">账号</label>
      <div class="login-input-wrap">
        <input
          id="username"
          v-model="username"
          type="text"
          autocomplete="username"
          aria-label="账号"
          placeholder="platform-admin"
        />
      </div>
    </div>

    <div class="login-field">
      <label for="password">密码</label>
      <div class="login-input-wrap">
        <input
          id="password"
          v-model="password"
          :type="showPassword ? 'text' : 'password'"
          autocomplete="current-password"
          aria-label="密码"
          placeholder="请输入密码"
        />
        <!-- eye 图标点击切换密码显隐；末段斜线在显示明文时隐藏。 -->
        <button
          type="button"
          class="login-eye"
          :aria-label="showPassword ? '隐藏密码' : '显示密码'"
          @click="showPassword = !showPassword"
        >
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path
              d="M3 12s3.2-5.5 9-5.5S21 12 21 12s-3.2 5.5-9 5.5S3 12 3 12Z"
              stroke="currentColor"
              stroke-width="1.6"
            />
            <path
              d="M12 9.2a2.8 2.8 0 1 1 0 5.6 2.8 2.8 0 0 1 0-5.6Z"
              stroke="currentColor"
              stroke-width="1.6"
            />
            <path
              v-if="!showPassword"
              d="m4.5 4.5 15 15"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
            />
          </svg>
        </button>
      </div>
    </div>

    <p v-if="errorMessage" class="login-error">{{ errorMessage }}</p>

    <!-- 验证码：常驻、auto=onload 加载即自动取题+Web Worker 解，无需点击。
         captchaActive 由挂载时探测出题接口是否 204 决定（关闭则不渲染、不挡按钮）。 -->
    <div v-if="captchaActive" class="login-captcha">
      <altcha-widget
        ref="captchaRef"
        challengeurl="/api/v1/auth/altcha-challenge"
        auto="onload"
        hidefooter
        hidelogo
        @statechange="onCaptchaState"
      />
      <p v-if="!captchaVerified" class="login-captcha-hint">🔄 人机校验中…</p>
    </div>

    <button
      type="submit"
      class="login-submit"
      :disabled="auth.loading || (captchaActive && !captchaVerified)"
    >
      {{ auth.loading ? '登录中…' : '登录' }}
    </button>

    <div class="login-security">
      <span>Secure runtime access</span>
      <span>AI task control plane</span>
    </div>
  </form>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

// LoginPage 负责本地账号登录，并在登录成功后回跳原始受保护路径。
const auth = useAuthStore()
const router = useRouter()

const orgCode = ref('')
const username = ref('')
const password = ref('')
// showPassword 控制密码框明文显示，仅前端交互不影响提交逻辑。
const showPassword = ref(false)
// errorMessage 只保存本次登录失败原因，下一次提交前会清空。
const errorMessage = ref<string | null>(null)

// captchaActive：是否启用验证码（挂载时探测出题接口决定）；初值 true 以默认禁用按钮（安全侧）。
const captchaActive = ref(true)
// captchaVerified：widget 是否已算出有效解。
const captchaVerified = ref(false)
// captchaPayload：已验证的 Altcha payload，提交时带上。
const captchaPayload = ref('')
// captchaRef：widget 元素引用，失败后 reset() 触发重新出题。
const captchaRef = ref<(HTMLElement & { reset?: () => void }) | null>(null)

// 挂载时探测出题接口：204 表示后端关闭验证码 → 不渲染 widget、不挡按钮；
// 其它（200 或网络错误）按开启处理，渲染 widget，由其自身展示进度/错误。
onMounted(async () => {
  try {
    const res = await fetch('/api/v1/auth/altcha-challenge')
    captchaActive.value = res.status !== 204
  } catch {
    captchaActive.value = true
  }
})

// onCaptchaState 监听 widget 状态：verified 时存 payload 并放行按钮，其它状态清空。
function onCaptchaState(e: Event) {
  const detail = (e as CustomEvent).detail as { state?: string; payload?: string } | undefined
  if (detail?.state === 'verified' && detail.payload) {
    captchaPayload.value = detail.payload
    captchaVerified.value = true
  } else {
    captchaVerified.value = false
    captchaPayload.value = ''
  }
}

// onSubmit 调用 auth store 登录；redirect 查询参数由全局 401 处理器写入。
async function onSubmit() {
  errorMessage.value = null
  try {
    await auth.login(
      username.value,
      password.value,
      orgCode.value,
      captchaActive.value ? captchaPayload.value : undefined,
    )
    const target = (router.currentRoute.value.query.redirect as string | undefined) ?? '/'
    await router.replace(target)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '登录失败'
    // payload 一次性：无论密码错(401)还是验证码错(400)，本次 payload 已消费，
    // 重置 widget 触发重新出题+重算，让用户可再试。
    if (captchaActive.value) {
      captchaVerified.value = false
      captchaPayload.value = ''
      captchaRef.value?.reset?.()
    }
  }
}
</script>

<style scoped>
.login-card {
  position: relative;
  padding: 30px 32px 28px;
  color: #0f172a;
  background: rgba(255, 255, 255, 0.92);
  border: 1px solid rgba(255, 255, 255, 0.74);
  box-shadow:
    0 34px 90px rgba(0, 0, 0, 0.34),
    0 0 0 1px rgba(32, 215, 255, 0.08) inset;
  backdrop-filter: blur(22px);
}

.login-brand {
  margin: 0 0 10px;
  color: #7c421c;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 1.6px;
}

.login-heading {
  margin: 0 0 28px;
  color: #111827;
  font-size: 31px;
  line-height: 1.1;
}

.login-field {
  margin-bottom: 22px;
}

.login-field label {
  display: block;
  margin-bottom: 10px;
  color: #151c2d;
  font-size: 15px;
  font-weight: 650;
}

.login-input-wrap {
  position: relative;
}

.login-input-wrap input {
  width: 100%;
  height: 48px;
  border: 1px solid rgba(15, 23, 42, 0.12);
  border-radius: 0;
  padding: 0 44px 0 15px;
  color: #111827;
  background: rgba(255, 255, 255, 0.86);
  font-size: 15px;
  outline: none;
  transition:
    border-color 180ms ease,
    box-shadow 180ms ease,
    background 180ms ease;
}

.login-input-wrap input::placeholder {
  color: #9aa3b2;
}

.login-input-wrap input:focus {
  border-color: rgba(32, 215, 255, 0.85);
  background: #ffffff;
  box-shadow: 0 0 0 4px rgba(32, 215, 255, 0.16);
}

.login-eye {
  position: absolute;
  right: 12px;
  top: 50%;
  width: 24px;
  height: 24px;
  padding: 1px;
  transform: translateY(-50%);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: #9ba4b3;
  cursor: pointer;
}

.login-eye svg {
  width: 22px;
  height: 22px;
}

.login-eye:hover {
  color: #475569;
}

.login-error {
  margin: 0 0 14px;
  color: #b42318;
  font-size: 13px;
}

.login-captcha {
  margin-bottom: 14px;
}

.login-captcha-hint {
  margin: 8px 0 0;
  color: #7a8597;
  font-size: 12px;
}

.login-submit {
  width: 100%;
  height: 46px;
  margin-top: 12px;
  border: 0;
  border-radius: 4px;
  color: #1b120b;
  background: linear-gradient(90deg, #ff8a22, #ff6b16 52%, #ffb13d);
  box-shadow: 0 14px 30px rgba(255, 112, 20, 0.28);
  font-size: 15px;
  font-weight: 760;
  cursor: pointer;
  transition:
    transform 180ms ease,
    box-shadow 180ms ease,
    opacity 180ms ease;
}

.login-submit:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 20px 38px rgba(255, 112, 20, 0.36);
}

.login-submit:disabled {
  cursor: not-allowed;
  opacity: 0.7;
}

.login-security {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  color: #7a8597;
  font-size: 12px;
}

.login-security span {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.login-security span::before {
  content: '';
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  background: var(--auth-cyan, #20d7ff);
  box-shadow: 0 0 10px var(--auth-cyan, #20d7ff);
}

@media (max-width: 560px) {
  .login-card {
    padding: 30px 22px 26px;
  }

  .login-heading {
    font-size: 28px;
  }

  .login-security {
    flex-direction: column;
  }
}
</style>
