<template>
  <!-- LoginHost 是 /login 路由入口：按当前 hostname 选定的白标变体整页渲染。 -->
  <component :is="variant.component" />
</template>

<script setup lang="ts">
import { onMounted } from 'vue'

import { applyVariantChrome, resolveVariant } from './variants/registry'

// 登录页生命周期内 hostname 不变，故只在 setup 解析一次；未命中回默认变体。
const variant = resolveVariant(window.location.hostname)

// 标题/favicon 属副作用，放 onMounted 应用，避免 SSR/首屏解析期直接触碰 document。
onMounted(() => {
  applyVariantChrome(variant)
})
</script>
