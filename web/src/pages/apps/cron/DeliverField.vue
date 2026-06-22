<template>
  <n-select :value="value" :options="options" @update:value="emit('update:value', $event)" />
  <p v-if="boundTypes.length === 0" class="deliver-hint">{{ t('apps.cron.deliver.noChannelHint') }}</p>
</template>

<script setup lang="ts">
import { computed, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { NSelect } from 'naive-ui'
import type { SelectOption } from 'naive-ui'

const { t } = useI18n()

import { useChannelProgressQuery } from '@/api/hooks/useChannel'
import { buildDeliverOptions } from './deliverOptions'

// DeliverField 是 deliver 字段薄壳：查询当前支持渠道的绑定状态，组装下拉选项。
// 目前产品仅 wechat 真正可投递；新增渠道时在下方增加一处 useChannelProgressQuery 调用并在 boundTypes 里追加判断即可。
const props = defineProps<{ value: string; appId: string }>()
const emit = defineEmits<{ 'update:value': [value: string] }>()

const appId = toRef(props, 'appId')
// 静态渠道清单，故可在 setup 顶层固定调用 hook（数量不变，不违反 hook 规则）。
const wechatProgress = useChannelProgressQuery(appId, computed(() => 'wechat'))

// boundTypes 收集 status==='bound' 的渠道类型。
const boundTypes = computed(() => {
  const bound: string[] = []
  if (wechatProgress.data.value?.status === 'bound') bound.push('wechat')
  return bound
})

// 强制转为 SelectOption[]：DeliverOption 结构与 SelectOption 兼容，但 naive-ui 类型联合
// 导致 TS 无法自动收窄；显式断言避免 TS2322 误报。
// 在 computed 中传入 t 保证语言切换时选项标签响应式更新。
const options = computed(() => buildDeliverOptions(boundTypes.value, props.value, t) as SelectOption[])
</script>

<style scoped>
.deliver-hint { margin: 4px 0 0; font-size: 12px; color: #999; }
</style>
