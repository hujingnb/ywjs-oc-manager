// zh 简体中文目录，结构必须与 en 完全对齐（同样的 key 路径）。
export default {
  common: {
    languageName: '简体中文',
  },
  locale: {
    switcherLabel: '语言',
    // 持久化失败提示：语言已在本地切换，但未能保存到账号偏好（不影响本次使用）。
    saveFailed: '语言已切换，但保存偏好失败。',
  },
} as const
