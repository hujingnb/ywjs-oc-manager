<template>
  <n-modal
    :show="show"
    preset="card"
    title="交付定制技能"
    class="deliver-custom-skill-modal"
    :style="{ width: '640px', maxWidth: 'calc(100vw - 32px)' }"
    @update:show="(value) => emit('update:show', value)"
  >
    <n-form label-placement="top">
      <n-form-item label="上传方式">
        <n-radio-group v-model:value="mode">
          <n-radio-button value="markdown">粘贴 Markdown</n-radio-button>
          <n-radio-button value="folder">上传文件夹</n-radio-button>
        </n-radio-group>
      </n-form-item>
      <n-form-item v-if="mode === 'markdown'" label="SKILL.md 内容">
        <n-input
          v-model:value="mdText"
          type="textarea"
          :rows="10"
          placeholder="粘贴带 frontmatter 的 SKILL.md"
        />
      </n-form-item>
      <n-form-item v-else label="Skill 文件夹">
        <input ref="folderInput" type="file" multiple class="folder-input" @change="onFolderChange" />
        <n-button @click="folderInput?.click()">选择文件夹</n-button>
        <span v-if="folderFiles.length" class="folder-count">{{ folderFiles.length }} 个文件</span>
      </n-form-item>
      <n-form-item label="可见范围">
        <ticket-targets-editor v-model="targets" :orgs="orgs" />
      </n-form-item>
    </n-form>
    <template #footer>
      <div class="modal-footer">
        <n-button @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="deliverMut.isPending.value" @click="onDeliver">交付</n-button>
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

// defaultTargets 根据申请者角色给出首个目标范围;管理员申请默认仅管理员,成员申请默认整企业。
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
      description: pack.description || props.ticket.description || '',
      targets: targets.value,
      file,
    })
    message.success('已交付')
    emit('delivered')
    emit('update:show', false)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '交付失败')
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
