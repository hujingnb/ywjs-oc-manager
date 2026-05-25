<template>
  <NConfigProvider :theme="darkTheme" :theme-overrides="themeOverrides">
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
import { darkTheme, type GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider, NMessageProvider } from 'naive-ui'

import UploadProgressModal from '@/components/UploadProgressModal.vue'
import { useBeforeUnloadGuard } from '@/composables/useBeforeUnloadGuard'

// App 是前端根组件，统一挂载全局 Naive UI 主题并把页面渲染交给路由出口。
// 这里不承载业务状态，避免根组件和页面权限、请求生命周期耦合。
// 上传相关的全局副作用（进度对话框、刷新拦截）统一在此装配，业务页面无需自行挂载。
useBeforeUnloadGuard()

const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#00F0FF',
    primaryColorHover: '#33F5FF',
    primaryColorPressed: '#00C8D4',
    primaryColorSuppl: '#00C8D4',
    bodyColor: '#0A0E27',
    cardColor: 'rgba(20,28,58,0.8)',
    modalColor: 'rgba(15,21,53,0.98)',
    tableColor: 'rgba(20,28,58,0.6)',
    tableColorStriped: 'rgba(20,28,58,0.3)',
    borderColor: 'rgba(0,240,255,0.2)',
    dividerColor: 'rgba(0,240,255,0.12)',
    textColorBase: '#FFFFFF',
    textColor1: '#FFFFFF',
    textColor2: '#CBD6E5',
    textColor3: '#8A94C6',
    successColor: '#00FF88',
    warningColor: '#FFB800',
    errorColor: '#FF3B5C',
    inputColor: 'rgba(15,21,53,0.8)',
    inputColorDisabled: 'rgba(15,21,53,0.4)',
    placeholderColor: '#8A94C6',
  },
  Layout: {
    siderColor: 'rgba(10,14,39,0.95)',
    headerColor: 'rgba(10,14,39,0.6)',
    footerColor: 'transparent',
    color: '#0A0E27',
  },
  Menu: {
    itemTextColor: '#8A94C6',
    itemTextColorHover: '#FFFFFF',
    itemTextColorActive: '#FFFFFF',
    itemTextColorActiveHover: '#FFFFFF',
    itemColorActive: 'rgba(0,240,255,0.15)',
    itemColorActiveHover: 'rgba(0,240,255,0.18)',
    itemColorHover: 'rgba(255,255,255,0.05)',
    borderColorActive: 'rgba(0,240,255,0.4)',
  },
  DataTable: {
    thColor: 'rgba(10,14,39,0.8)',
    tdColor: 'transparent',
    tdColorHover: 'rgba(0,240,255,0.05)',
    borderColor: 'rgba(0,240,255,0.12)',
    thTextColor: '#8A94C6',
  },
}
</script>
