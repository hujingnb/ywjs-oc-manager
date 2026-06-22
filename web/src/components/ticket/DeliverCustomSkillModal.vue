<template>
  <n-modal
    :show="show"
    preset="card"
    :title="t('components.deliverCustomSkillModal.modalTitle')"
    class="deliver-custom-skill-modal"
    :style="{ width: '640px', maxWidth: 'calc(100vw - 32px)' }"
    @update:show="(value) => emit('update:show', value)"
  >
    <n-form label-placement="top">
      <n-form-item :label="t('components.deliverCustomSkillModal.fieldUploadMode')">
        <n-radio-group v-model:value="mode">
          <n-radio-button value="markdown">{{ t('components.deliverCustomSkillModal.modeMarkdown') }}</n-radio-button>
          <n-radio-button value="folder">{{ t('components.deliverCustomSkillModal.modeFolder') }}</n-radio-button>
        </n-radio-group>
      </n-form-item>
      <n-form-item v-if="mode === 'markdown'" :label="t('components.deliverCustomSkillModal.fieldSkillMd')">
        <n-input
          v-model:value="mdText"
          type="textarea"
          :rows="10"
          :placeholder="t('components.deliverCustomSkillModal.skillMdPlaceholder')"
        />
      </n-form-item>
      <n-form-item v-else :label="t('components.deliverCustomSkillModal.fieldSkillFolder')">
        <input ref="folderInput" type="file" multiple class="folder-input" @change="onFolderChange" />
        <n-button @click="folderInput?.click()">{{ t('components.deliverCustomSkillModal.selectFolderBtn') }}</n-button>
        <span v-if="folderFiles.length" class="folder-count">{{ t('components.deliverCustomSkillModal.folderFileCount', { count: folderFiles.length }) }}</span>
      </n-form-item>
      <n-form-item :label="t('components.deliverCustomSkillModal.fieldVisibility')">
        <ticket-targets-editor v-model="targets" :orgs="orgs" />
      </n-form-item>
    </n-form>
    <template #footer>
      <div class="modal-footer">
        <n-button @click="emit('update:show', false)">{{ t('components.deliverCustomSkillModal.cancelBtn') }}</n-button>
        <n-button type="primary" :loading="deliverMut.isPending.value" @click="onDeliver">{{ t('components.deliverCustomSkillModal.deliverBtn') }}</n-button>
      </div>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import {
  NButton,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NRadioButton,
  NRadioGroup,
  useMessage,
} from 'naive-ui'
import { useI18n } from 'vue-i18n'

import type { SkillTicketDetail } from '@/api'
import { packFromFolder, packFromMarkdown, type UploadedFile } from '@/domain/skillPackaging'
import { useDeliverCustomSkill, type DeliverTarget } from '@/api/hooks/useSkillTickets'
import TicketTargetsEditor from './TicketTargetsEditor.vue'

interface OrgOption {
  id: string
  name?: string
  code?: string
}

const props = defineProps<{
  show: boolean
  ticket: SkillTicketDetail | null
  orgs: OrgOption[]
}>()

const emit = defineEmits<{
  'update:show': [boolean]
  delivered: []
}>()

const message = useMessage()
const { t } = useI18n()
const deliverMut = useDeliverCustomSkill()
const mode = ref<'markdown' | 'folder'>('markdown')
const mdText = ref('')
const folderInput = ref<HTMLInputElement | null>(null)
const folderFiles = ref<UploadedFile[]>([])
const targets = ref<DeliverTarget[]>([])

watch(
  () => [props.show, props.ticket?.id] as const,
  ([show]) => {
    if (!show || !props.ticket) return
    mode.value = 'markdown'
    mdText.value = ''
    folderFiles.value = []
    targets.value = defaultTargets(props.ticket)
  },
  { immediate: true },
)

// defaultTargets 根据申请者角色给出首个目标范围;企业管理员申请默认仅企业管理员,成员申请默认整企业。
function defaultTargets(ticket: SkillTicketDetail): DeliverTarget[] {
  const audience = ticket.requester_role === 'org_admin' ? 'org_admins' : 'all_org'
  return ticket.org_id ? [{ org_id: ticket.org_id, audience }] : []
}

async function onFolderChange(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  folderFiles.value = await Promise.all(
    files.map(async (file) => ({
      relativePath: file.webkitRelativePath || file.name,
      data: new Uint8Array(await file.arrayBuffer()),
    })),
  )
}

async function onDeliver() {
  if (!props.ticket?.id) return
  try {
    const pack = mode.value === 'markdown'
      ? packFromMarkdown(mdText.value)
      : packFromFolder(folderFiles.value)
    const file = new File([toArrayBuffer(pack.tar)], `${pack.name}.tar`, { type: 'application/x-tar' })
    await deliverMut.mutateAsync({
      ticketId: props.ticket.id,
      description: pack.description || '',
      targets: targets.value,
      file,
    })
    message.success(t('components.deliverCustomSkillModal.deliverSuccess'))
    emit('delivered')
    emit('update:show', false)
  } catch (error) {
    message.error(error instanceof Error ? error.message : t('components.deliverCustomSkillModal.deliverFailed'))
  }
}

// File 构造函数需要 ArrayBuffer 形态的 BlobPart；复制一份避免 Uint8Array 的泛型 buffer 类型过宽。
function toArrayBuffer(bytes: Uint8Array): ArrayBuffer {
  const copy = new Uint8Array(bytes.byteLength)
  copy.set(bytes)
  return copy.buffer as ArrayBuffer
}
</script>

<style scoped>
.folder-input {
  display: none;
}

.folder-count {
  margin-left: 12px;
  color: #64748b;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
