<template>
  <StatusBadge :view="formatRuntimeNodeStatus(status)" />
</template>

<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { formatRuntimeNodeStatus } from '@/domain/status'

defineProps<{ status: string }>()
</script>
