<template>
  <StatusBadge :view="formatAppStatus(status)" />
</template>

<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { formatAppStatus } from '@/domain/status'

defineProps<{ status: string }>()
</script>
