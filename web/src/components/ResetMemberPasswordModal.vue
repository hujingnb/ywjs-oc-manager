<template>
  <n-modal :show="visible" :mask-closable="true" @update:show="onVisibleUpdate">
    <n-card
      :title="t('org.members.modal.resetTitle')"
      :bordered="false"
      role="dialog"
      aria-modal="true"
      :aria-label="t('org.members.modal.resetTitle')"
      style="width: min(440px, 92vw)"
    >
      <p class="reset-message">
        {{ t('org.members.modal.resetMessage', { username }) }}
      </p>

      <n-form-item
        :label="t('org.members.modal.resetPasswordPrompt', { username })"
        :label-props="{ for: passwordInputId }"
        :show-feedback="false"
      >
        <n-input
          v-model:value="password"
          type="password"
          show-password-on="click"
          :input-props="{ id: passwordInputId, autocomplete: 'new-password' }"
        />
      </n-form-item>

      <template #footer>
        <n-space justify="end">
          <n-button @click="onCancel">
            {{ t('common.actions.cancel') }}
          </n-button>
          <n-button
            type="error"
            :disabled="busy || !canConfirm"
            :loading="busy"
            @click="onConfirm"
          >
            {{ t('org.members.modal.resetConfirm') }}
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

// ResetMemberPasswordModal 将密码限制在组件内部，调用方仅在用户确认时收到合法值。
const props = defineProps<{
  visible: boolean
  username: string
  busy?: boolean
}>()

// confirm 只携带通过最小长度校验的密码；cancel 统一覆盖按钮和遮罩关闭路径。
const emit = defineEmits<{
  (event: 'confirm', password: string): void
  (event: 'cancel'): void
}>()

const { t } = useI18n()
// 成员页同一时间只展示一个重置弹窗，固定 id 可稳定关联原生 input 与表单标签。
const passwordInputId = 'reset-member-password-input'
// 密码不进入父组件状态；API 失败且弹窗仍打开时，此 ref 会保留用户输入。
const password = ref('')
const canConfirm = computed(() => password.value.length >= 8)

// visible 任意切换都清除敏感数据，确保关闭后以及下一次打开时不复用旧密码。
watch(
  () => props.visible,
  () => { password.value = '' },
)

// 点击确认时再次校验状态，避免按钮状态更新前的重复点击绕过界面限制。
function onConfirm() {
  if (props.busy || !canConfirm.value) return
  emit('confirm', password.value)
}

// 取消操作先清理密码再通知父组件，防止父组件异步关闭期间敏感值仍留在内存中。
function onCancel() {
  password.value = ''
  emit('cancel')
}

// Naive UI 仅在遮罩或 Escape 请求关闭时回传 false，此路径与取消按钮保持一致。
function onVisibleUpdate(visible: boolean) {
  if (!visible) onCancel()
}
</script>

<style scoped>
.reset-message {
  margin: 0 0 16px;
  color: var(--color-text-secondary);
}
</style>
