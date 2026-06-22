<template>
  <n-space align="center" :size="6">
    <n-input :value="value" :placeholder="t('apps.cron.picker.scriptPlaceholder')" @update:value="emit('update:value', $event)" />
    <n-select
      :value="(null as never)"
      :options="fileOptions"
      :loading="query.isLoading.value"
      :placeholder="t('apps.cron.picker.selectFile')"
      style="width: 180px"
      @update:value="onPick"
    />
  </n-space>
</template>

<script setup lang="ts">
import { computed, ref, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { NInput, NSelect, NSpace } from 'naive-ui'
import type { SelectOption } from 'naive-ui'

const { t } = useI18n()

import { useWorkspaceQuery } from '@/api/hooks/useWorkspace'
import { workspaceFileNames } from './workspaceFiles'

// WorkspaceFilePicker 给 script 字段提供「手输 + 从工作目录根层文件点选」两种方式。
const props = defineProps<{ value: string; appId: string }>()
const emit = defineEmits<{ 'update:value': [value: string] }>()

const appId = toRef(props, 'appId')
// 列工作目录根层（path='' / keyword=''）。
const query = useWorkspaceQuery(appId, ref(''), ref(''))

// 强制转为 SelectOption[]：workspaceFileNames 返回 string[]，naive-ui options 期望 SelectOption[]。
const fileOptions = computed(() =>
  workspaceFileNames(query.data.value).map((name) => ({ label: name, value: name })) as SelectOption[],
)

// 选中文件即把 basename 回填到 script；name 可能是 string | number，统一转字符串。
function onPick(name: string | number) {
  emit('update:value', String(name))
}
</script>
