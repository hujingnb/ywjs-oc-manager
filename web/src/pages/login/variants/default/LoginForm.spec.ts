// LoginForm.spec.ts — 默认变体登录表单交互单测。
// 通过默认表单这一 useLogin 的真实宿主，覆盖 composable 行为：
// 验证码开启时未 verified 按钮禁用、verified 后带 payload 提交并 redirect、
// 失败后重置 widget、关闭(204)时按钮直接可用。
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import LoginForm from './LoginForm.vue'

const loginMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ loading: false, login: loginMock }),
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ currentRoute: { value: { query: {} } }, replace: replaceMock }),
}))
// LocaleSwitcher 依赖 Pinia/i18n 插件，用占位桩替代，避免无关插件配置。
vi.mock('@/components/LocaleSwitcher.vue', () => ({
  default: { template: '<div />' },
}))

// 把出题探测 fetch 固定为指定状态码。
function stubChallenge(status: number) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status }))
}

// mountForm：挂载登录表单并注入 i18n 插件（useLogin 与模板均用到 useI18n）。
function mountForm() {
  return mount(LoginForm, { global: { plugins: [i18n] } })
}

// dispatchVerified：在 altcha-widget 上派发 verified statechange，模拟工作量证明完成。
function dispatchVerified(wrapper: ReturnType<typeof mountForm>, payload: string) {
  const widget = wrapper.find('altcha-widget')
  widget.element.dispatchEvent(
    new CustomEvent('statechange', { detail: { state: 'verified', payload } }),
  )
}

describe('LoginForm 登录交互', () => {
  beforeEach(() => {
    loginMock.mockReset()
    replaceMock.mockReset()
  })

  // 开启验证码(200)时，未 verified 前提交按钮禁用。
  it('未 verified 时按钮禁用', async () => {
    stubChallenge(200)
    const wrapper = mountForm()
    await flushPromises()
    const widget = wrapper.find('altcha-widget')
    const btn = wrapper.find('button.login-submit')
    expect(widget.attributes('challenge')).toBe('/api/v1/auth/altcha-challenge')
    expect(widget.attributes('configuration')).toContain('"hideFooter":true')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(wrapper.find('.login-captcha-hint').exists()).toBe(true)
  })

  // verified 后按钮可用，提交把 payload 传给 auth.login，成功后 replace 到默认 redirect。
  it('verified 后带 payload 提交并 redirect', async () => {
    stubChallenge(200)
    loginMock.mockResolvedValue({})
    const wrapper = mountForm()
    await flushPromises()
    dispatchVerified(wrapper, 'PAYLOAD123')
    await flushPromises()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(loginMock).toHaveBeenCalledWith('', '', '', 'PAYLOAD123')
    expect(replaceMock).toHaveBeenCalledWith('/')
  })

  // 登录失败后 widget 重置并重新验证，清空 verified → 按钮重新禁用。
  it('登录失败后重置 widget', async () => {
    stubChallenge(200)
    loginMock.mockRejectedValue(new Error('账号或密码错误'))
    const wrapper = mountForm()
    await flushPromises()

    const resetSpy = vi.fn()
    const verifySpy = vi.fn().mockResolvedValue(null)
    const widgetEl = wrapper.find('altcha-widget').element as HTMLElement & {
      reset?: () => void
      verify?: () => Promise<unknown>
    }
    widgetEl.reset = resetSpy
    widgetEl.verify = verifySpy

    dispatchVerified(wrapper, 'P')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(resetSpy).toHaveBeenCalled()
    expect(verifySpy).toHaveBeenCalled()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeDefined()
  })

  // 关闭验证码(204)时不渲染 widget、按钮直接可用。
  it('204 时按钮直接可用且无 widget', async () => {
    stubChallenge(204)
    const wrapper = mountForm()
    await flushPromises()
    expect(wrapper.find('altcha-widget').exists()).toBe(false)
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()
  })
})
