<template>
  <div class="ticket-status-stepper" :data-status="status">
    <div class="status-line" :aria-label="t('components.ticketStatusStepper.ariaLabel')">
      <div
        v-for="step in steps"
        :key="step.value"
        class="status-step"
        :class="{ active: step.active, current: step.current }"
      >
        <span class="status-dot" />
        <span class="status-label">{{ step.label }}</span>
      </div>
    </div>
    <n-tag v-if="status === 'rejected'" type="error" size="small" :bordered="false">{{ t('components.ticketStatusStepper.tagRejected') }}</n-tag>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ status: string }>()

const { t } = useI18n()
const order = ['pending', 'processing', 'delivered']

// steps 根据当前状态高亮已到达节点;rejected 不推进主线,单独显示红色状态标记。
// 标签使用 computed 包装，确保语言切换时步进器文案随之更新。
const steps = computed(() => {
  const labels: Record<string, string> = {
    pending: t('components.ticketStatusStepper.stepPending'),
    processing: t('components.ticketStatusStepper.stepProcessing'),
    delivered: t('components.ticketStatusStepper.stepDelivered'),
  }
  const idx = order.indexOf(props.status)
  return order.map((value, index) => ({
    value,
    label: labels[value],
    active: idx >= 0 && index <= idx,
    current: props.status === value,
  }))
})
</script>

<style scoped>
.ticket-status-stepper {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.status-step {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #64748b;
  font-size: 13px;
  white-space: nowrap;
}

.status-step.active {
  color: #0f766e;
}

.status-step.current .status-label {
  font-weight: 650;
}

.status-dot {
  width: 9px;
  height: 9px;
  border-radius: 999px;
  border: 1px solid currentColor;
  background: #fff;
}

.status-step.active .status-dot {
  background: #0f766e;
}
</style>
