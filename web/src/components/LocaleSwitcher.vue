<template>
  <!-- 语言选择器：登录页(persist=false)与顶栏(persist=true)复用同一组件。 -->
  <n-dropdown trigger="click" :options="options" @select="onSelect">
    <n-button quaternary size="small" :aria-label="t('locale.switcherLabel')">
      <template #icon><Languages :size="16" /></template>
      {{ currentLabel }}
    </n-button>
  </n-dropdown>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NDropdown } from 'naive-ui'
import { Languages } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import { SUPPORTED_LOCALES, type SupportedLocale } from '@/i18n'
import { useLocaleStore } from '@/stores/locale'

// persist 决定切换后是否持久化到后端：顶栏(已登录)为 true，登录页为 false。
const props = withDefaults(defineProps<{ persist?: boolean }>(), { persist: false })

const { t, messages, locale: i18nLocale } = useI18n()
const localeStore = useLocaleStore()

// options 用各语言自报名（languageName）渲染，保证「该语言的母语者」总能认出自己的语言。
const options = computed(() =>
  SUPPORTED_LOCALES.map((code) => ({
    key: code,
    label: (messages.value[code] as { common: { languageName: string } }).common.languageName,
  })),
)

// currentLabel 展示当前语言的自报名。
const currentLabel = computed(
  () => (messages.value[i18nLocale.value as string] as { common: { languageName: string } }).common.languageName,
)

// onSelect 切换语言并按 persist 透传给 store；导出以便单测直接调用。
async function onSelect(key: SupportedLocale): Promise<void> {
  await localeStore.setLocale(key, { persist: props.persist })
}

defineExpose({ onSelect })
</script>
