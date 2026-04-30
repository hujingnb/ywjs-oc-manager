<template>
  <Teleport to="body">
    <div v-if="visible" class="modal-overlay" @click.self="onCancel">
      <div class="modal-card">
        <header>
          <h3>{{ title }}</h3>
        </header>
        <p>{{ message }}</p>
        <footer>
          <button class="secondary-button" type="button" @click="onCancel">{{ cancelLabel }}</button>
          <button class="primary-button" type="button" :disabled="busy" @click="onConfirm">
            {{ busy ? '执行中…' : confirmLabel }}
          </button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
const props = defineProps<{
  visible: boolean
  title: string
  message: string
  busy?: boolean
  confirmLabel?: string
  cancelLabel?: string
}>()

const emit = defineEmits<{
  (event: 'confirm'): void
  (event: 'cancel'): void
}>()

const confirmLabel = props.confirmLabel ?? '确认'
const cancelLabel = props.cancelLabel ?? '取消'

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

.modal-card footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
