import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AuthLayout from './AuthLayout.vue'

// mountLayout：挂载 AuthLayout，注入 i18n 插件（AuthLayout 使用 useI18n() 渲染平台介绍文案）。
function mountLayout() {
  return mount(AuthLayout, {
    global: {
      plugins: [i18n],
      stubs: {
        RouterView: { template: '<form class="login-card">登录表单</form>' },
      },
    },
  })
}

describe('AuthLayout', () => {
  beforeEach(() => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // 该用例验证背景装饰层与登录内容层分离，避免后续居中布局再次回到 auth-stage 上耦合。
  it('uses a centered content wrapper for hero and login shell', () => {
    const wrapper = mountLayout()
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
