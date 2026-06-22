// LoginPage.spec.ts — 登录页验证码交互单测。
// 覆盖：开启时未 verified 按钮禁用、verified 后可提交且带 captcha、
// 失败后重置 widget、关闭(204)时按钮直接可用。
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import LoginPage from './LoginPage.vue'

// ======================== mocks ========================
const loginMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ loading: false, login: loginMock }),
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ currentRoute: { value: { query: {} } }, replace: replaceMock }),
}))
// LocaleSwitcher 依赖 i18n 与 Pinia 插件，登录页测试环境无这两个插件；
// 用占位桩替代，避免引入无关的插件配置影响既有验证码测试用例。
vi.mock('@/components/LocaleSwitcher.vue', () => ({
  default: { template: '<div />' },
}))

// 把出题探测 fetch 固定为指定状态码。
function stubChallenge(status: number) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status }))
}

// mountPage：挂载登录页，注入 i18n 插件（LoginPage 使用 useI18n() 需要）。
// altcha-widget 是 isCustomElement 注册的原生自定义元素，不走 Vue 组件 stub；
// 通过 vi.fn() 直接挂在 DOM 实例上来拦截 reset()（见各测试用例）。
function mountPage() {
  return mount(LoginPage, { global: { plugins: [i18n] } })
}

// dispatchVerified：在 altcha-widget 元素上派发带 verified detail 的 statechange 事件，
// 模拟 Altcha widget 完成工作量证明后的状态变更回调。
function dispatchVerified(wrapper: ReturnType<typeof mountPage>, payload: string) {
  const widget = wrapper.find('altcha-widget')
  widget.element.dispatchEvent(
    new CustomEvent('statechange', { detail: { state: 'verified', payload } }),
  )
}

describe('LoginPage 验证码交互', () => {
  beforeEach(() => {
    loginMock.mockReset()
    replaceMock.mockReset()
  })

  // 开启验证码(200)时，未 verified 前提交按钮禁用。
  it('未 verified 时按钮禁用', async () => {
    stubChallenge(200)
    const wrapper = mountPage()
    await flushPromises()
    const widget = wrapper.find('altcha-widget')
    const btn = wrapper.find('button.login-submit')
    expect(widget.attributes('challenge')).toBe('/api/v1/auth/altcha-challenge')
    expect(widget.attributes('configuration')).toContain('"hideFooter":true')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(wrapper.find('.login-captcha-hint').exists()).toBe(true)
  })

  // verified 后按钮可用，提交把 payload 传给 auth.login。
  it('verified 后提交带 captcha payload', async () => {
    stubChallenge(200)
    loginMock.mockResolvedValue({})
    const wrapper = mountPage()
    await flushPromises()
    // 模拟 widget 触发 verified 状态事件（派发带 detail 的 CustomEvent）。
    dispatchVerified(wrapper, 'PAYLOAD123')
    await flushPromises()
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(loginMock).toHaveBeenCalledWith('', '', '', 'PAYLOAD123')
  })

  // 登录失败后 widget 重置并重新启动验证，同时清空 captchaVerified → 按钮重新禁用。
  // altcha-widget 是原生自定义元素，不走 Vue stub；通过直接给 DOM 实例挂 reset spy
  // 与 verify spy 来验证组件调用了 captchaRef.value.reset()+verify()。
  it('登录失败后重置 widget', async () => {
    stubChallenge(200)
    loginMock.mockRejectedValue(new Error('账号或密码错误'))
    const wrapper = mountPage()
    await flushPromises()

    // 先把 spy 挂到实际 DOM 实例，之后 captchaRef.value?.reset?.() 就会调到它。
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
    // 失败后 captchaVerified 被清空 → 按钮重新禁用。
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeDefined()
  })

  // 关闭验证码(204)时不渲染 widget、按钮直接可用。
  it('204 时按钮直接可用且无 widget', async () => {
    stubChallenge(204)
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('altcha-widget').exists()).toBe(false)
    expect(wrapper.find('button.login-submit').attributes('disabled')).toBeUndefined()
  })
})
