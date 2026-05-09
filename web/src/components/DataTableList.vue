<template>
  <div class="data-table-list">
    <header class="toolbar">
      <div class="title-block">
        <p v-if="eyebrow" class="eyebrow">{{ eyebrow }}</p>
        <h2>{{ title }}</h2>
        <p v-if="subtitle" class="subtitle">{{ subtitle }}</p>
      </div>
      <div class="actions">
        <slot name="toolbar" />
      </div>
    </header>
    <n-card>
      <n-alert v-if="errorMessage" type="error" :show-icon="false" class="error-banner">
        {{ errorMessage }}
      </n-alert>
      <n-data-table
        :columns="columns"
        :data="data"
        :loading="loading"
        :row-key="rowKey"
        :bordered="false"
      />
    </n-card>
  </div>
</template>

<script setup lang="ts" generic="T extends Record<string, any>">
import { NCard, NDataTable, NAlert, type DataTableColumn } from 'naive-ui'

defineProps<{
  title: string
  eyebrow?: string
  subtitle?: string
  columns: DataTableColumn<T>[]
  data: T[]
  loading?: boolean
  errorMessage?: string
  rowKey?: (row: T) => string | number
}>()
</script>

<style scoped>
.data-table-list { display: flex; flex-direction: column; gap: 12px; }
.toolbar { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; }
.actions { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
/* eyebrow：列表页眉上方的分类标签文本 */
.eyebrow { font-size: 12px; color: var(--color-text-secondary, #8A94C6); margin: 0 0 4px; }
/* subtitle：标题下方的辅助说明文本 */
.subtitle { font-size: 13px; color: var(--color-text-secondary, #8A94C6); margin: 4px 0 0; }
.error-banner { margin-bottom: 12px; }
</style>
