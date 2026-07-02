// DefaultLoginPage.spec.ts — 默认变体整页布局单测。
// 验证背景装饰层与登录内容层分离，且登录表单挂在内容层的 login-shell 内。
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import DefaultLoginPage from './DefaultLoginPage.vue'

// mountPage：挂载默认整页，注入 i18n（hero 文案用 useI18n）。
// LoginForm 依赖 useLogin（fetch 探测 / store / router），此处用占位桩替代，
// 让布局测试聚焦于结构而非登录行为（登录行为由 LoginForm.spec 覆盖）。
function mountPage() {
  return mount(DefaultLoginPage, {
    global: {
      plugins: [i18n],
      stubs: {
        LoginForm: { template: '<form class="login-card">登录表单</form>' },
      },
    },
  })
}

describe('DefaultLoginPage', () => {
  beforeEach(() => {
    // 画布 2D 上下文取不到时动画早退，避免 jsdom 无 canvas 实现报错。
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // 背景装饰层与登录内容层分离，登录卡片落在内容层的 login-shell 内。
  it('内容层承载 hero 与登录表单，背景层独立', () => {
    const wrapper = mountPage()
    const stage = wrapper.get('.auth-stage')
    const content = wrapper.find('.auth-content')

    expect(content.exists()).toBe(true)
    expect(content.find('.auth-hero').exists()).toBe(true)
    expect(content.find('.auth-login-shell').exists()).toBe(true)
    expect(content.find('.login-card').exists()).toBe(true)
    expect(content.find('.auth-neural').exists()).toBe(false)

    const directChildClasses = Array.from(stage.element.children).map((child) =>
      Array.from((child as HTMLElement).classList),
    )
    expect(directChildClasses).toEqual([
      ['auth-neural'],
      ['auth-aurora'],
      ['auth-grid'],
      ['auth-scan'],
      ['auth-content'],
    ])
  })
})
