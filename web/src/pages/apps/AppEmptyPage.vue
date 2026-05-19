<!-- web/src/pages/apps/AppEmptyPage.vue -->
<template>
  <div class="empty-container">
    <n-empty description="请联系管理员创建实例">
      <template #icon>
        <Bot :size="48" :stroke-width="1" />
      </template>
    </n-empty>
  </div>
</template>

<script setup lang="ts">
import { NEmpty } from 'naive-ui'
import { Bot } from 'lucide-vue-next'
</script>

<style scoped>
.empty-container {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
  min-height: 400px;
}
</style>
