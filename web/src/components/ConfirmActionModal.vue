<template>
  <Teleport to="body">
    <div v-if="visible" class="modal-overlay" @click.self="onCancel">
      <div class="modal-card">
        <header>
          <h3>{{ title }}</h3>
        </header>
        <p>{{ message }}</p>

        <label v-if="verifyValue" class="verify-block">
          <span class="verify-hint">{{ verifyHint || `输入 "${verifyValue}" 以确认` }}</span>
          <input
            v-model="verifyInput"
            class="verify-input"
            type="text"
            autocomplete="off"
            spellcheck="false"
          />
        </label>

        <footer>
          <button class="secondary-button" type="button" @click="onCancel">{{ cancelLabel }}</button>
          <button
            class="primary-button"
            type="button"
            :disabled="busy || !canConfirm"
            @click="onConfirm"
          >
            {{ busy ? '执行中…' : confirmLabel }}
          </button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

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

const confirmLabel = props.confirmLabel ?? '确认'
const cancelLabel = props.cancelLabel ?? '取消'
const verifyInput = ref('')

// 每次打开 modal 都清空输入，避免二次打开复用旧输入。
watch(
  () => props.visible,
  (visible) => {
    if (visible) verifyInput.value = ''
  },
)

// 强校验：要求用户在输入框输入与 verifyValue 一致的值（大小写不敏感、忽略首尾空白）。
// 未传 verifyValue 时直接放行，兼容已有简单二次确认场景。
const canConfirm = computed(() => {
  if (!props.verifyValue) return true
  return verifyInput.value.trim().toLowerCase() === props.verifyValue.trim().toLowerCase()
})

function onConfirm() {
  emit('confirm')
}

function onCancel() {
  emit('cancel')
}
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  display: grid;
  place-items: center;
  background: rgb(15 23 42 / 60%);
  z-index: 100;
}

.modal-card {
  width: min(420px, 92vw);
  padding: 24px;
  border-radius: 10px;
  background: #ffffff;
  box-shadow: 0 18px 48px rgb(15 23 42 / 25%);
}

.modal-card h3 {
  margin: 0 0 12px;
  font-size: 18px;
}

.modal-card p {
  margin: 0 0 20px;
  color: #415065;
}

.verify-block {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0 0 20px;
}

.verify-hint {
  font-size: 13px;
  color: #1f2937;
}

.verify-input {
  padding: 8px 10px;
  border: 1px solid rgba(0, 0, 0, 0.15);
  border-radius: 6px;
  font-size: 14px;
}

.modal-card footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
