<template>
  <NConfigProvider :theme-overrides="themeOverrides">
    <!-- NMessageProvider 提供全局 message API，供页面通过 useMessage() 弹出操作反馈 -->
    <NMessageProvider>
      <RouterView />
      <!-- 全局上传进度对话框：订阅 uploadProgress store 自动显示 / 隐藏，
           App 根挂一次即可覆盖所有业务页面 -->
      <UploadProgressModal />
    </NMessageProvider>
  </NConfigProvider>
</template>

<script setup lang="ts">
import type { GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider, NMessageProvider } from 'naive-ui'

import UploadProgressModal from '@/components/UploadProgressModal.vue'
import { useBeforeUnloadGuard } from '@/composables/useBeforeUnloadGuard'

// App 是前端根组件，统一挂载全局 Naive UI 主题并把页面渲染交给路由出口。
// 这里不承载业务状态，避免根组件和页面权限、请求生命周期耦合。
// 上传相关的全局副作用（进度对话框、刷新拦截）统一在此装配，业务页面无需自行挂载。
useBeforeUnloadGuard()

const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#ff6a00',
    primaryColorHover: '#ff8126',
    primaryColorPressed: '#e65f00',
    primaryColorSuppl: '#ff6a00',
    infoColor: '#1677ff',
    infoColorHover: '#4096ff',
    infoColorPressed: '#0958d9',
    successColor: '#16a34a',
    warningColor: '#f59e0b',
    errorColor: '#d93026',
    bodyColor: '#f5f7fa',
    cardColor: '#ffffff',
    modalColor: '#ffffff',
    popoverColor: '#ffffff',
    tableColor: '#ffffff',
    tableColorStriped: '#fbfcfd',
    borderColor: '#e5e7eb',
    dividerColor: '#edf0f5',
    textColorBase: '#1f2329',
    textColor1: '#1f2329',
    textColor2: '#4b5563',
    textColor3: '#6b7280',
    inputColor: '#ffffff',
    inputColorDisabled: '#f3f5f8',
    placeholderColor: '#8a94a6',
  },
  Layout: {
    siderColor: '#ffffff',
    headerColor: '#ffffff',
    footerColor: '#ffffff',
    color: '#f5f7fa',
  },
  Menu: {
    itemTextColor: '#4b5563',
    itemTextColorHover: '#ff6a00',
    itemTextColorActive: '#ff6a00',
    itemTextColorActiveHover: '#ff6a00',
    itemColorActive: '#fff4ed',
    itemColorActiveHover: '#fff4ed',
    itemColorHover: '#f5f7fa',
    borderColorActive: '#ff6a00',
  },
  DataTable: {
    thColor: '#fbfcfd',
    tdColor: '#ffffff',
    tdColorHover: '#f8fafc',
    borderColor: '#edf0f5',
    thTextColor: '#6b7280',
  },
  Card: {
    borderColor: '#e5e7eb',
    color: '#ffffff',
  },
  Button: {
    borderRadiusMedium: '4px',
    borderRadiusSmall: '4px',
    // 主按钮保留阿里云风格亮橙背景，文字使用深色以满足普通文本对比度。
    textColorPrimary: '#1f2329',
    textColorHoverPrimary: '#1f2329',
    textColorPressedPrimary: '#1f2329',
    textColorFocusPrimary: '#1f2329',
    textColorDisabledPrimary: 'rgba(31, 35, 41, 0.45)',
    textColorTextPrimary: '#8a3700',
    textColorTextHoverPrimary: '#8a3700',
    textColorTextPressedPrimary: '#8a3700',
    textColorTextFocusPrimary: '#8a3700',
    textColorGhostPrimary: '#8a3700',
    textColorGhostHoverPrimary: '#8a3700',
    textColorGhostPressedPrimary: '#8a3700',
    textColorGhostFocusPrimary: '#8a3700',
  },
}
</script>
