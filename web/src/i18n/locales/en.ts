// en 是平台默认与兜底语言目录。key 按页面/领域分组，P1 仅放地基所需文案；
// 后续 P2 按模块批量补全。新增语言只需复制本结构并整体翻译。
export default {
  common: {
    languageName: 'English',
  },
  locale: {
    switcherLabel: 'Language',
    // 持久化失败提示：语言已在本地切换，但未能保存到账号偏好（不影响本次使用）。
    saveFailed: 'Language switched, but saving your preference failed.',
  },
} as const
