import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useLocaleStore } from './locale'
import { i18n } from '@/i18n'

// mock api client：拦截 apiRequest，断言持久化调用；保留其余真身（setLocaleProvider 等）。
const apiRequest = vi.fn()
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>()
  return { ...actual, apiRequest: (...a: unknown[]) => apiRequest(...a) }
})

describe('useLocaleStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    apiRequest.mockReset().mockResolvedValue(undefined)
    i18n.global.locale.value = 'en'
  })

  // localStorage 有值时 init 采用它并应用到 i18n
  it('init 优先采用 localStorage', async () => {
    localStorage.setItem('ocm.locale', 'zh')
    const store = useLocaleStore()
    await store.init()
    expect(store.locale).toBe('zh')
    expect(i18n.global.locale.value).toBe('zh')
  })

  // localStorage 为空时 init 回退后端默认（fetchDefault 返回 zh）
  it('init 回退后端默认语言', async () => {
    apiRequest.mockResolvedValueOnce({ default_locale: 'zh', supported_locales: ['en', 'zh'] })
    const store = useLocaleStore()
    await store.init()
    expect(store.locale).toBe('zh')
  })

  // setLocale 切换：写 i18n、写 localStorage；已登录时 PATCH 持久化
  it('setLocale 已登录时持久化到后端', async () => {
    const store = useLocaleStore()
    await store.init()
    await store.setLocale('zh', { persist: true })
    expect(localStorage.getItem('ocm.locale')).toBe('zh')
    expect(i18n.global.locale.value).toBe('zh')
    expect(apiRequest).toHaveBeenCalledWith('/api/v1/auth/me/locale', expect.objectContaining({ method: 'PATCH', body: { locale: 'zh' } }))
  })

  // 非法 locale 被规范化为兜底 en，不写入非法值
  it('applyFromUser 非法值回退兜底', () => {
    const store = useLocaleStore()
    store.applyFromUser('fr')
    expect(store.locale).toBe('en')
  })
})
