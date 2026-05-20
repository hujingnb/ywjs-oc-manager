<template>
  <n-card :bordered="true">
    <template #header>
      <p class="eyebrow">Instance · Kanban</p>
      <h2 style="margin: 0">任务</h2>
    </template>
    <p class="state-text">任务看板加载中…</p>
  </n-card>
</template>

<script setup lang="ts">
import { NCard } from 'naive-ui'
// AppKanbanTab 是实例任务看板顶层组件，后续 Task F2 起逐步填充。
defineProps<{ appId: string }>()
</script>
