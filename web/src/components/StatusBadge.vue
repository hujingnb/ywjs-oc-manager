<template>
  <!-- view.label 为 i18n 键，通过 t() 解析为当前语言文案。
       未知状态时 view.params 携带 { status } 插值，用于降级文案展示原始状态值。 -->
  <n-tag :type="nType" size="small" :bordered="false">{{ t(view.label, view.params ?? {}) }}</n-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTag } from 'naive-ui'
import type { StatusView } from '@/domain/status'

// StatusBadge 是业务状态视图到 Naive UI 标签的统一渲染点。
// view.label 为 i18n 键，view.tone 只表达业务严重程度，不泄露组件库类型。
const props = defineProps<{ view: StatusView }>()

const { t } = useI18n()

// tone → naive-ui NTag.type 的唯一定义点；其他文件不得复制此映射。
const TONE_TO_TAG_TYPE = {
  success: 'success',
  warning: 'warning',
  danger: 'error',
  neutral: 'default',
} as const

const nType = computed(() => TONE_TO_TAG_TYPE[props.view.tone] ?? 'default')
</script>
