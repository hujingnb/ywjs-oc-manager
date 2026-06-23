// locale store 集中管理界面语言：解析优先级、应用到 vue-i18n、持久化（localStorage + 已登录则后端）。
// 不直接依赖 auth store，是否持久化由调用方通过 persist 选项决定，避免 store 间循环依赖。
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { apiRequest, setLocaleProvider } from '@/api/client'
import { DEFAULT_LOCALE, SUPPORTED_LOCALES, i18n, type SupportedLocale } from '@/i18n'

// localStorage 中持久化语言选择的 key。
const STORAGE_KEY = 'ocm.locale'

// normalize 把任意输入规范化为受支持 locale，非法/空值回退 DEFAULT_LOCALE。
function normalize(value: string | null | undefined): SupportedLocale {
  return SUPPORTED_LOCALES.includes(value as SupportedLocale) ? (value as SupportedLocale) : DEFAULT_LOCALE
}

export const useLocaleStore = defineStore('locale', () => {
  // locale 是当前激活的语言，始终与 i18n.global.locale 保持一致。
  const locale = ref<SupportedLocale>(DEFAULT_LOCALE)

  // apply 把目标语言写入内存、vue-i18n、localStorage 与 <html lang>（单一出口，保证四者一致）。
  // 同步 document.documentElement.lang 让无障碍工具/搜索引擎/浏览器翻译提示识别真实内容语言，
  // 否则始终是 index.html 硬编码的 zh-CN，与实际界面语言不符。
  function apply(next: SupportedLocale): void {
    locale.value = next
    i18n.global.locale.value = next
    localStorage.setItem(STORAGE_KEY, next)
    document.documentElement.lang = next
  }

  // fetchDefault 读取平台默认语言（登录页 localStorage 为空时使用）；端点不可达时回退 DEFAULT_LOCALE。
  async function fetchDefault(): Promise<SupportedLocale> {
    try {
      const cfg = await apiRequest<{ default_locale: string }>('/api/v1/config', { withAuth: false })
      return normalize(cfg.default_locale)
    } catch {
      return DEFAULT_LOCALE
    }
  }

  // init 在应用启动时解析初值：localStorage → 平台默认 → 兜底；并把 locale provider 注入 api client。
  async function init(): Promise<void> {
    setLocaleProvider(() => locale.value)
    const stored = localStorage.getItem(STORAGE_KEY)
    apply(stored ? normalize(stored) : await fetchDefault())
  }

  // setLocale 用户主动切换：应用语言；persist 为 true（已登录）时持久化到后端。
  async function setLocale(next: SupportedLocale, opts: { persist?: boolean } = {}): Promise<void> {
    apply(normalize(next))
    if (opts.persist) {
      await apiRequest('/api/v1/auth/me/locale', { method: 'PATCH', body: { locale: locale.value } })
    }
  }

  // applyFromUser 登录后用 DB 中的用户语言覆盖（user.locale 为空表示未选择，保持当前值）。
  function applyFromUser(userLocale: string | undefined): void {
    if (userLocale) apply(normalize(userLocale))
  }

  return { locale, init, setLocale, applyFromUser }
})
