// LoginHost.spec.ts — 登录路由入口单测。
// 验证：按 resolveVariant 选定的变体组件被渲染，且挂载时应用了 chrome（标题）。
// registry 被 mock，隔离真实默认变体的 store/router/fetch 依赖，聚焦粘合逻辑。
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

// 用带标记的桩变体替换真实注册表；resolveVariant 恒返回该桩。
const fakeVariant = {
  component: { template: '<div class="fake-variant" />' },
  documentTitle: 'FAKE-TITLE',
}
vi.mock('./variants/registry', () => ({
  resolveVariant: vi.fn(() => fakeVariant),
  applyVariantChrome: (v: { documentTitle?: string }, doc: Document = document) => {
    if (v.documentTitle) doc.title = v.documentTitle
  },
}))

import LoginHost from './LoginHost.vue'

describe('LoginHost', () => {
  // 渲染 resolveVariant 选定的变体组件，并在挂载时应用 chrome（覆盖 document.title）。
  it('渲染选定变体并应用 chrome', () => {
    const wrapper = mount(LoginHost)
    expect(wrapper.find('.fake-variant').exists()).toBe(true)
    expect(document.title).toBe('FAKE-TITLE')
  })
})
