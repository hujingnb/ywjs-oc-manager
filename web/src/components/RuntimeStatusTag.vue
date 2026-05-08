<template>
  <n-tag :type="nType" size="small" :bordered="false">{{ view.label }}</n-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import { formatRuntimeNodeStatus } from '@/domain/status'

const props = defineProps<{ status: string }>()
const view = computed(() => formatRuntimeNodeStatus(props.status))
const nType = computed(() => {
  const map: Record<string, 'success' | 'warning' | 'error' | 'default'> = {
    success: 'success', warning: 'warning', danger: 'error', neutral: 'default',
  }
  return map[view.value.tone] ?? 'default'
})
</script>
