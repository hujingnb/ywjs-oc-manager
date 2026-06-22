import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import LocaleSwitcher from '../LocaleSwitcher.vue'
import { i18n } from '@/i18n'

const setLocale = vi.fn()
vi.mock('@/stores/locale', () => ({
  useLocaleStore: () => ({ locale: { value: 'en' }, setLocale }),
}))

describe('LocaleSwitcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale.mockReset()
  })

  // 选择 zh 时按 persist 透传调用 store.setLocale
  it('选择语言时调用 setLocale 并透传 persist', async () => {
    const wrapper = mount(LocaleSwitcher, {
      global: { plugins: [i18n] },
      props: { persist: true },
    })
    await wrapper.vm.onSelect('zh')
    expect(setLocale).toHaveBeenCalledWith('zh', { persist: true })
  })
})
