<template>
  <n-tag :type="nType" size="small" :bordered="false">{{ view.label }}</n-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import type { StatusView } from '@/domain/status'

const props = defineProps<{ view: StatusView }>()

// tone → naive-ui NTag.type 的唯一定义点；其他文件不得复制此映射。
const TONE_TO_TAG_TYPE = {
  success: 'success',
  warning: 'warning',
  danger: 'error',
  neutral: 'default',
} as const

const nType = computed(() => TONE_TO_TAG_TYPE[props.view.tone] ?? 'default')
</script>
