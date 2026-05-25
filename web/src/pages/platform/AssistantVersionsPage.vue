<template>
  <div style="display: grid; gap: 18px">
    <!-- 版本列表 -->
    <DataTableList
      title="助手版本"
      eyebrow="Platform"
      :columns="columns"
      :data="versions ?? []"
      :loading="isLoading"
      :error-message="error?.message"
      :row-key="(row: AssistantVersionDTO) => row.id"
    >
      <template #toolbar>
        <n-button type="primary" @click="openCreate">
          <template #icon><Plus :size="16" /></template>
          新增版本
        </n-button>
      </template>
    </DataTableList>
    <p v-if="actionFeedback" class="state-text" :class="{ danger: actionFeedbackError }">{{ actionFeedback }}</p>

    <!-- 新建 / 编辑表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">{{ editingId ? 'Edit' : 'New' }}</p>
            <h2 style="margin: 0">{{ editingId ? '编辑助手版本' : '新建助手版本' }}</h2>
          </div>
          <n-button quaternary circle @click="closeForm">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="版本名称（唯一）" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="使用镜像 *">
              <n-select
                v-model:value="form.image_id"
                :loading="imagesQuery.isLoading.value"
                :disabled="imagesQuery.isError.value"
                :options="imageOptions"
                placeholder="选择 Hermes 镜像"
              />
              <p v-if="imagesQuery.isError.value" class="state-text danger">镜像列表获取失败，请重试</p>
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="描述">
              <n-input v-model:value="form.description" type="textarea" :rows="2" placeholder="版本用途说明" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="内置提示词 *">
              <n-input
                v-model:value="form.system_prompt"
                type="textarea"
                :rows="4"
                placeholder="可填写助手人设、行为规则等；将注入容器 SOUL.md 的版本层"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="主模型 *">
              <n-select
                v-model:value="form.main_model"
                filterable
                :loading="modelsQuery.isLoading.value"
                :disabled="modelsQuery.isError.value"
                :options="modelOptions"
                placeholder="选择主对话模型"
              />
              <p v-if="modelsQuery.isError.value" class="state-text danger">模型列表获取失败，请重试</p>
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <p class="eyebrow" style="margin: 4px 0">智能路由（留空走主模型）</p>
          </n-grid-item>
          <n-grid-item v-for="slot in AUXILIARY_SLOTS" :key="slot.key">
            <n-form-item :label="slot.label">
              <n-select
                v-model:value="form.routing[slot.key]"
                filterable
                clearable
                :options="modelOptions"
                placeholder="默认走主模型"
              />
            </n-form-item>
          </n-grid-item>

          <!-- skill 管理：编辑态对已存在版本即时上传/删除；新建态版本尚未落库，先暂存文件 -->
          <n-grid-item :span="2">
            <n-form-item label="Skill 列表">
              <div style="display: grid; gap: 8px; width: 100%">
                <!-- 编辑态：展示服务端已有 skill，删除操作即时生效 -->
                <template v-if="editingId">
                  <div v-if="editingSkills.length === 0" class="state-text">暂无 skill</div>
                  <div
                    v-for="skill in editingSkills"
                    :key="skill.name"
                    style="display: flex; align-items: center; justify-content: space-between; gap: 12px"
                  >
                    <span>{{ skill.name }} <small class="data-table-subtitle">{{ formatBytes(skill.file_size) }}</small></span>
                    <n-button size="small" tertiary @click="onDeleteSkill(skill.name)">删除</n-button>
                  </div>
                </template>
                <!-- 新建态：展示已选待上传文件，移除仅清理本地暂存项（版本保存后才真正上传） -->
                <template v-else>
                  <div v-if="pendingSkillFiles.length === 0" class="state-text">暂无 skill</div>
                  <div
                    v-for="(file, idx) in pendingSkillFiles"
                    :key="`${file.name}-${idx}`"
                    style="display: flex; align-items: center; justify-content: space-between; gap: 12px"
                  >
                    <span>{{ file.name }} <small class="data-table-subtitle">{{ formatBytes(file.size) }}</small></span>
                    <n-button size="small" tertiary @click="removePendingSkill(idx)">移除</n-button>
                  </div>
                </template>
                <div>
                  <input
                    ref="skillFileInput"
                    type="file"
                    accept=".tar"
                    style="display: none"
                    @change="onSkillFileChange"
                  />
                  <n-button size="small" :loading="skillUploading" @click="triggerSkillUpload">
                    {{ editingId ? '上传 skill tar' : '添加 skill tar' }}
                  </n-button>
                </div>
                <p v-if="skillFeedback" class="state-text" :class="{ danger: skillFeedbackError }">{{ skillFeedback }}</p>
              </div>
            </n-form-item>
          </n-grid-item>

          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="closeForm">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="submitting" :disabled="!canSubmit">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <!-- 删除二次确认：删除是破坏性操作，需用户确认后才发起请求 -->
    <ConfirmActionModal
      :visible="deleteTarget !== null"
      title="删除助手版本"
      :message="deleteTarget ? `确定删除版本「${deleteTarget.name}」？删除后不可恢复。` : ''"
      :busy="deleteBusy"
      confirm-label="删除"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import { NButton, NCard, NForm, NFormItem, NGrid, NGridItem, NInput, NSelect, NSpace, useMessage } from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import { actionColumn } from '@/components/columns'
import { useModelsQuery } from '@/api/hooks/useOrganizations'
import {
  AUXILIARY_SLOTS,
  emptyRouting,
  useAssistantVersionsQuery,
  useCreateAssistantVersion,
  useDeleteAssistantVersion,
  useDeleteAssistantVersionSkill,
  useRuntimeImagesQuery,
  useUpdateAssistantVersion,
  useUploadAssistantVersionSkill,
  type AssistantVersionDTO,
  type AssistantVersionFormPayload,
  type AssistantVersionSkillDTO,
} from '@/api/hooks/useAssistantVersions'
import { useUploadProgressStore } from '@/stores/uploadProgress'

// AssistantVersionsPage 是平台管理员的助手版本目录管理页：列表 + 新建/编辑 + 删除。
const { data: versions, isLoading, error } = useAssistantVersionsQuery()
const createMutation = useCreateAssistantVersion()
const updateMutation = useUpdateAssistantVersion()
const deleteMutation = useDeleteAssistantVersion()
const uploadProgress = useUploadProgressStore()
const message = useMessage()

// skill 管理状态：editingSkills 是当前编辑版本的 skill 列表，随上传/删除即时刷新。
const uploadSkillMutation = useUploadAssistantVersionSkill()
const deleteSkillMutation = useDeleteAssistantVersionSkill()
const editingSkills = ref<AssistantVersionSkillDTO[]>([])
// pendingSkillFiles 是新建态下用户已选、尚未上传的 skill tar 文件。
// 新建版本时后端尚无版本 ID，无法即时上传，先在本地暂存，待版本创建成功后再逐个上传。
const pendingSkillFiles = ref<File[]>([])
const skillFileInput = ref<HTMLInputElement | null>(null)
const skillUploading = ref(false)
const skillFeedback = ref('')
const skillFeedbackError = ref(false)

// formatBytes 把字节数格式化为人类可读大小。
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

// triggerSkillUpload 触发隐藏的文件选择框。
function triggerSkillUpload() {
  skillFileInput.value?.click()
}

// onSkillFileChange 处理 skill tar 选择：编辑态版本已存在，立即上传；
// 上传进度统一由全局 UploadProgressModal 展示，按钮 loading 退化为短暂闪烁。
// 新建态版本尚未创建，先把文件暂存进 pendingSkillFiles，待保存表单时一并上传。
async function onSkillFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许重复选择同名文件
  if (!file) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  // 编辑态：版本已存在，沿用即时上传。
  if (editingId.value) {
    try {
      const result = await uploadProgress.run(
        [{ file, label: file.name }],
        async (_item, f, ctx) => {
          return uploadSkillMutation.mutateAsync({
            id: editingId.value!,
            file: f,
            onProgress: ctx.onProgress,
            signal: ctx.signal,
          })
        },
      )
      // run 不抛错；成功路径取最新 skill 列表回写本地状态。
      const updated = result.results[0]
      if (updated) {
        editingSkills.value = updated.skills
        skillFeedback.value = `已上传 skill ${file.name}`
      } else if (result.failed.length > 0) {
        skillFeedbackError.value = true
        skillFeedback.value = result.failed[0].error ?? '上传失败'
      }
    } catch (err) {
      // 唯一会被抛的错误是会话互斥：用 message 提示，不破坏本地状态。
      message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
    }
    return
  }
  // 新建态：拒绝重复添加同名文件，避免保存时对同一文件触发两次上传。
  if (pendingSkillFiles.value.some(f => f.name === file.name)) {
    skillFeedbackError.value = true
    skillFeedback.value = `已添加过同名文件 ${file.name}`
    return
  }
  pendingSkillFiles.value = [...pendingSkillFiles.value, file]
  skillFeedback.value = `已添加 skill ${file.name}，将在保存版本时上传`
}

// removePendingSkill 从新建态的待上传列表移除一个暂存文件（按下标定位）。
function removePendingSkill(idx: number) {
  pendingSkillFiles.value = pendingSkillFiles.value.filter((_, i) => i !== idx)
  skillFeedback.value = ''
  skillFeedbackError.value = false
}

// onDeleteSkill 从当前编辑的版本删除一个 skill。
async function onDeleteSkill(skillName: string) {
  if (!editingId.value) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  try {
    const updated = await deleteSkillMutation.mutateAsync({ id: editingId.value, skillName })
    editingSkills.value = updated.skills
    skillFeedback.value = `已删除 skill ${skillName}`
  } catch (err) {
    skillFeedbackError.value = true
    skillFeedback.value = err instanceof Error ? err.message : '删除失败'
  }
}

// 表单状态：editingId 为 null 时是新建，否则是编辑该 id。
const formVisible = ref(false)
const editingId = ref<string | null>(null)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const actionFeedback = ref('')
const actionFeedbackError = ref(false)

const form = reactive<AssistantVersionFormPayload>({
  name: '', description: '', system_prompt: '', image_id: '', main_model: '',
  routing: emptyRouting(),
})

// 镜像与模型列表仅在表单打开时请求。
const imagesQuery = useRuntimeImagesQuery(() => formVisible.value)
const modelsQuery = useModelsQuery(() => formVisible.value)
const imageOptions = computed(() => (imagesQuery.data.value ?? []).map(img => ({ label: img.label, value: img.id })))
const modelOptions = computed(() => (modelsQuery.data.value ?? []).map(m => ({ label: m.name, value: m.id })))

// canSubmit 在必填项齐备且依赖列表未出错时才允许提交。
const canSubmit = computed(() =>
  !submitting.value
  && !imagesQuery.isError.value
  && !modelsQuery.isError.value
  && Boolean(form.name.trim())
  && Boolean(form.system_prompt.trim())
  && Boolean(form.image_id)
  && Boolean(form.main_model),
)

// resetForm 把表单恢复为空白新建态。
function resetForm() {
  form.name = ''
  form.description = ''
  form.system_prompt = ''
  form.image_id = ''
  form.main_model = ''
  form.routing = emptyRouting()
}

// openCreate 打开空白新建表单。
function openCreate() {
  resetForm()
  editingId.value = null
  submitError.value = null
  actionFeedback.value = ''
  editingSkills.value = []
  pendingSkillFiles.value = []
  skillFeedback.value = ''
  skillFeedbackError.value = false
  formVisible.value = true
}

// openEdit 用已有版本数据填充表单进入编辑态。
// routing 后端只返回非空槽位，用 emptyRouting 兜底补齐 8 个 key。
function openEdit(version: AssistantVersionDTO) {
  form.name = version.name
  form.description = version.description
  form.system_prompt = version.system_prompt
  form.image_id = version.image_id
  form.main_model = version.main_model
  form.routing = { ...emptyRouting(), ...version.routing }
  editingId.value = version.id
  submitError.value = null
  actionFeedback.value = ''
  editingSkills.value = [...version.skills]
  pendingSkillFiles.value = []
  skillFeedback.value = ''
  skillFeedbackError.value = false
  formVisible.value = true
}

// closeForm 关闭表单，不清空（下次 openCreate/openEdit 会重置）。
function closeForm() {
  formVisible.value = false
}

// buildPayload 把表单组装成创建/更新提交体。
function buildPayload(): AssistantVersionFormPayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    // system_prompt 不做 trim：多行人设/规则内容按用户原样保存，与后端一致。
    system_prompt: form.system_prompt,
    image_id: form.image_id,
    main_model: form.main_model,
    routing: { ...form.routing },
  }
}

// uploadPendingSkills 把新建态暂存的 skill tar 通过全局 uploadProgress.run 一次性提交。
// 串行执行 + N/M 计数由 store 管理；单文件失败不阻塞后续，run 返回汇总后供调用方判断。
async function uploadPendingSkills(
  versionId: string,
): Promise<{ skills: AssistantVersionSkillDTO[]; failed: string[] }> {
  if (pendingSkillFiles.value.length === 0) {
    return { skills: [], failed: [] }
  }
  const items = pendingSkillFiles.value.map(f => ({ file: f, label: f.name }))
  try {
    const result = await uploadProgress.run(items, async (_item, f, ctx) => {
      return uploadSkillMutation.mutateAsync({
        id: versionId,
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
    // 取最后一次成功的 skill 列表作为最终视图；后端每次返回的都是完整列表，最后一次为准。
    const lastVersion = result.results[result.results.length - 1]
    const skills = lastVersion?.skills ?? []
    // failed + cancelled 都视为「未成功」，返回给调用方提示用户。
    const failed = [...result.failed, ...result.cancelled].map(it => it.label)
    return { skills, failed }
  } catch (err) {
    // 会话互斥：返回全部待传文件为 failed，让 submit 流程把表单切到编辑态并提示。
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
    return { skills: [], failed: pendingSkillFiles.value.map(f => f.name) }
  }
}

// submit 根据 editingId 决定走创建还是更新；新建态在版本落库后再上传暂存的 skill。
async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  submitError.value = null
  try {
    if (editingId.value) {
      await updateMutation.mutateAsync({ id: editingId.value, payload: buildPayload() })
      formVisible.value = false
      return
    }
    const created = await createMutation.mutateAsync(buildPayload())
    if (pendingSkillFiles.value.length === 0) {
      formVisible.value = false
      return
    }
    // 版本已落库，逐个上传暂存 skill，并把表单切到该版本的编辑态：
    // 即便部分 skill 上传失败，用户也能在编辑态直接补传，而不会因再次保存误触发重复创建。
    const { skills, failed } = await uploadPendingSkills(created.id)
    editingId.value = created.id
    editingSkills.value = skills
    pendingSkillFiles.value = []
    if (failed.length > 0) {
      skillFeedbackError.value = true
      skillFeedback.value = `版本已创建，以下 skill 上传失败，可在下方重试：${failed.join('、')}`
      return
    }
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '保存失败'
  } finally {
    submitting.value = false
  }
}

// 删除确认状态：deleteTarget 非空时弹出二次确认窗。
const deleteTarget = ref<AssistantVersionDTO | null>(null)
const deleteBusy = ref(false)

// requestDelete 由列表「删除」操作触发，打开二次确认窗（不直接发请求）。
function requestDelete(version: AssistantVersionDTO) {
  deleteTarget.value = version
}

// cancelDelete 关闭二次确认窗，不做任何删除。
function cancelDelete() {
  deleteTarget.value = null
}

// confirmDelete 用户确认后执行删除；后端在版本被引用时返回 409，错误文案直接展示给用户。
async function confirmDelete() {
  const version = deleteTarget.value
  if (!version) return
  deleteBusy.value = true
  actionFeedback.value = ''
  actionFeedbackError.value = false
  try {
    await deleteMutation.mutateAsync(version.id)
    actionFeedback.value = `已删除版本 ${version.name}`
  } catch (err) {
    actionFeedbackError.value = true
    actionFeedback.value = err instanceof Error ? err.message : '删除失败'
  } finally {
    deleteBusy.value = false
    deleteTarget.value = null
  }
}

// columns 展示版本基础信息、修订号、skill 数与操作。
const columns = computed(() => [
  {
    title: '名称',
    key: 'name',
    render: (row: AssistantVersionDTO) => [
      h('strong', row.name),
      row.description ? h('small', { class: 'data-table-subtitle' }, row.description) : null,
    ],
  },
  { title: '镜像', key: 'image_id', render: (row: AssistantVersionDTO) => row.image_id || '—' },
  { title: '主模型', key: 'main_model', render: (row: AssistantVersionDTO) => row.main_model || '—' },
  { title: '修订号', key: 'revision', render: (row: AssistantVersionDTO) => `r${row.revision}` },
  { title: 'Skill 数', key: 'skills', render: (row: AssistantVersionDTO) => String(row.skills?.length ?? 0) },
  actionColumn<AssistantVersionDTO>([
    { label: '编辑', type: 'primary', onClick: openEdit },
    { label: '删除', onClick: requestDelete },
  ]),
])
</script>
