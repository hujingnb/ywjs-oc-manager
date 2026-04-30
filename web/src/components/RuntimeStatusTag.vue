<template>
  <span :class="['status-pill', view.tone]">{{ view.label }}</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { formatRuntimeNodeStatus } from '@/domain/status'

const props = defineProps<{ status: string }>()
const view = computed(() => formatRuntimeNodeStatus(props.status))
</script>
