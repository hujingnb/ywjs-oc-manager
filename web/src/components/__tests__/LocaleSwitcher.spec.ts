import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import LocaleSwitcher from '../LocaleSwitcher.vue'
import { i18n } from '@/i18n'

const setLocale = vi.fn()
vi.mock('@/stores/locale', () => ({
  useLocaleStore: () => ({ locale: { value: 'en' }, setLocale }),
}))

// 组件用 naive-ui useMessage 在持久化失败时提示；单测无 NMessageProvider 上下文，
// 故部分 mock naive-ui，仅替换 useMessage 返回可断言的 error stub，其余组件保持真身。
const messageError = vi.fn()
vi.mock('naive-ui', async (orig) => {
  const actual = await orig<typeof import('naive-ui')>()
  return { ...actual, useMessage: () => ({ error: messageError }) }
})

describe('LocaleSwitcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale.mockReset()
    messageError.mockReset()
  })

  // 选择 zh 时按 persist 透传调用 store.setLocale
  it('选择语言时调用 setLocale 并透传 persist', async () => {
    setLocale.mockResolvedValue(undefined)
    const wrapper = mount(LocaleSwitcher, {
      global: { plugins: [i18n] },
      props: { persist: true },
    })
    await wrapper.vm.onSelect('zh')
    expect(setLocale).toHaveBeenCalledWith('zh', { persist: true })
    expect(messageError).not.toHaveBeenCalled()
  })

  // 持久化失败时：onSelect 不应抛出（吞掉 reject 避免未处理 rejection），并提示保存失败
  it('持久化失败时提示且不抛出', async () => {
    setLocale.mockRejectedValue(new Error('network'))
    const wrapper = mount(LocaleSwitcher, {
      global: { plugins: [i18n] },
      props: { persist: true },
    })
    await expect(wrapper.vm.onSelect('zh')).resolves.toBeUndefined()
    expect(messageError).toHaveBeenCalledTimes(1)
  })
})
