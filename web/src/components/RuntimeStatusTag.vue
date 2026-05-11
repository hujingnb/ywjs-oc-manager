<template>
  <StatusBadge :view="formatRuntimeNodeStatus(status)" />
</template>

<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { formatRuntimeNodeStatus } from '@/domain/status'

// RuntimeStatusTag 将运行节点状态字符串转换为统一状态徽标视图。
// status 来自节点 API，未知状态由格式化函数保留原始文案。
defineProps<{ status: string }>()
</script>
