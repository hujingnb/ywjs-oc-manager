// useLogin.ts — 登录页共享行为 composable。
// 抽取自原 LoginPage.vue：承载本地账号登录的全部交互状态、验证码探测/交互与提交逻辑，
// 供所有登录变体（默认变体与各白标变体）复用，保证认证行为只有一份实现，不因各变体
// 重复编写而漂移。
//
// 变体作者契约：任何登录变体的模板都必须——
//   1. 表单 submit 绑定 onSubmit；输入框 v-model 绑定 orgCode/username/password；
//   2. captchaActive 为真时渲染 altcha 挂载点，ref 绑 captchaRef、@statechange 绑 onCaptchaState；
//   3. submit 按钮 disabled 绑定 auth.loading || (captchaActive && !captchaVerified)；
//   4. 展示 errorMessage。
// 缺任一接线点会导致验证码或登录失效。
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'

// useLogin 返回登录页所需的响应式状态与交互方法；auth 直接透出以便模板绑定 auth.loading。
export function useLogin() {
  const auth = useAuthStore()
  const router = useRouter()
  const { t } = useI18n()

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
  // captchaRef：widget 元素引用，失败后 reset()+verify() 触发重新出题和重算。
  const captchaRef = ref<
    (HTMLElement & { reset?: () => void; verify?: () => Promise<unknown> }) | null
  >(null)

  // 挂载时探测出题接口：204 表示后端关闭验证码 → 不渲染 widget、不挡按钮；
  // 其它（200 或网络错误）按开启处理。
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

  // onSubmit 调用 auth store 登录；redirect 查询参数由全局 401 处理器写入，缺省回根路径。
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
      // 后端错误信息优先展示；无具体信息时使用本地化兜底文案。
      errorMessage.value = err instanceof Error ? err.message : t('login.loginFailed')
      // payload 一次性：本次已消费，重置 widget 触发重新出题+重算，让用户可再试。
      if (captchaActive.value) {
        captchaVerified.value = false
        captchaPayload.value = ''
        captchaRef.value?.reset?.()
        // Altcha auto=onload 只在加载时触发；失败后必须显式 verify 才会重新出题。
        void captchaRef.value?.verify?.()
      }
    }
  }

  return {
    auth,
    orgCode,
    username,
    password,
    showPassword,
    errorMessage,
    captchaActive,
    captchaVerified,
    captchaPayload,
    captchaRef,
    onCaptchaState,
    onSubmit,
  }
}
