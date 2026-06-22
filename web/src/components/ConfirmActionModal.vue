<template>
  <n-modal :show="visible" :mask-closable="true" @update:show="(v) => { if (!v) onCancel() }">
    <n-card
      :title="title"
      :bordered="false"
      role="dialog"
      aria-modal="true"
      style="width: min(440px, 92vw)"
    >
      <p class="confirm-message">{{ message }}</p>

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
import { useI18n } from 'vue-i18n'

// ConfirmActionModal 为删除、禁用、充值等高风险操作提供二次确认。
// verifyValue 可要求用户输入业务对象名称，避免误点直接提交破坏性请求。
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

// confirm 表示用户已通过当前弹框校验；cancel 覆盖遮罩关闭和取消按钮两类关闭路径。
const emit = defineEmits<{
  (event: 'confirm'): void
  (event: 'cancel'): void
}>()

const { t } = useI18n()

const confirmLabel = computed(() => props.confirmLabel ?? t('components.confirmActionModal.defaultConfirm'))
const cancelLabel = computed(() => props.cancelLabel ?? t('components.confirmActionModal.defaultCancel'))
// verifyLabel 优先使用调用方给出的业务提示，默认提示展示需要输入的确认值。
const verifyLabel = computed(() =>
  props.verifyHint || t('components.confirmActionModal.defaultVerifyLabel', { value: props.verifyValue }),
)
// verifyInput 每次打开弹框都会重置，避免上一次确认值复用到新的业务对象。
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

<style scoped>
.confirm-message {
  margin: 0 0 16px;
  color: var(--color-text-secondary);
}
</style>
