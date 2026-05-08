<template>
  <n-modal :show="visible" :mask-closable="true" @update:show="(v) => { if (!v) onCancel() }">
    <n-card
      :title="title"
      :bordered="false"
      role="dialog"
      aria-modal="true"
      style="width: min(440px, 92vw)"
    >
      <p style="margin: 0 0 16px; color: #CBD6E5">{{ message }}</p>

      <n-form-item v-if="verifyValue" :label="verifyLabel" :show-feedback="false">
        <n-input
          v-model:value="verifyInput"
          placeholder=""
          autocomplete="off"
          :spellcheck="false"
        />
      </n-form-item>

      <template #footer>
        <n-space justify="end">
          <n-button @click="onCancel">{{ cancelLabel }}</n-button>
          <n-button
            type="error"
            :disabled="busy || !canConfirm"
            :loading="busy"
            @click="onConfirm"
          >
            {{ confirmLabel }}
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NCard, NFormItem, NInput, NModal, NSpace } from 'naive-ui'

const props = defineProps<{
  visible: boolean
  title: string
  message: string
  busy?: boolean
  confirmLabel?: string
  cancelLabel?: string
  verifyValue?: string
  verifyHint?: string
}>()

const emit = defineEmits<{
  (event: 'confirm'): void
  (event: 'cancel'): void
}>()

const confirmLabel = computed(() => props.confirmLabel ?? '确认')
const cancelLabel = computed(() => props.cancelLabel ?? '取消')
const verifyLabel = computed(() => props.verifyHint || `输入 "${props.verifyValue}" 以确认`)
const verifyInput = ref('')

watch(
  () => props.visible,
  (visible) => { if (visible) verifyInput.value = '' },
)

// 强校验：要求用户在输入框输入与 verifyValue 一致的值（大小写不敏感、忽略首尾空白）。
// 未传 verifyValue 时直接放行，兼容已有简单二次确认场景。
const canConfirm = computed(() => {
  if (!props.verifyValue) return true
  return verifyInput.value.trim().toLowerCase() === props.verifyValue.trim().toLowerCase()
})

function onConfirm() { emit('confirm') }
function onCancel() { emit('cancel') }
</script>
