<template>
  <StatusBadge :view="formatAppStatus(status)" />
</template>

<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { formatAppStatus } from '@/domain/status'

// AppStatusTag 将后端应用状态字符串转换为统一状态徽标视图。
// status 来自应用 API，未知状态会由 formatAppStatus 降级为中性展示。
defineProps<{ status: string }>()
</script>
