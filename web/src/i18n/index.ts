// i18n 单例：Composition API 模式（legacy:false），默认与兜底语言均为 en。
// locale 初值占位 'en'，真实初值由 useLocaleStore 在应用启动时按优先级解析后通过 setLocale 设置。
import { createI18n } from 'vue-i18n'

import en from './locales/en'
import zh from './locales/zh'

// SupportedLocale 是前端受支持语言联合类型；与后端 service.SupportedLocales 保持一致。
export type SupportedLocale = 'en' | 'zh'

// SUPPORTED_LOCALES 供选择器渲染与校验；顺序即选择器展示顺序。
export const SUPPORTED_LOCALES: SupportedLocale[] = ['en', 'zh']

// DEFAULT_LOCALE 是前端硬兜底（后端公开配置不可达时使用）。
export const DEFAULT_LOCALE: SupportedLocale = 'en'

export const i18n = createI18n({
  legacy: false,
  locale: DEFAULT_LOCALE,
  fallbackLocale: DEFAULT_LOCALE,
  messages: { en, zh },
})
