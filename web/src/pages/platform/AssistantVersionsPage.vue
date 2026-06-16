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
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px">
          <div>
            <p class="eyebrow">{{ editingId ? 'Edit' : 'New' }}</p>
            <h2 style="margin: 0">{{ editingId ? '编辑助手版本' : '新建助手版本' }}</h2>
          </div>
          <!-- 保存/取消固定在表单顶部：下方 Skill 列表会持续撑高表单，按钮放底部时够不到，放顶部确保始终可点 -->
          <n-space align="center">
            <n-button @click="closeForm">取消</n-button>
            <n-button type="primary" :loading="submitting" :disabled="!canSubmit" @click="submit">保存</n-button>
          </n-space>
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

          <!-- 行业知识库：运行时检索额外范围，保存后立即生效，不触发版本 revision 变化。 -->
          <n-grid-item :span="2">
            <n-form-item label="行业知识库">
              <div style="display: grid; gap: 8px; width: 100%">
                <n-select
                  v-model:value="form.industry_knowledge_base_ids"
                  multiple
                  filterable
                  clearable
                  :loading="industryBasesQuery.isLoading.value"
                  :disabled="industryBasesQuery.isError.value"
                  :options="industryKnowledgeOptions"
                  placeholder="选择该版本运行时要额外检索的行业知识库"
                />
                <n-alert type="warning" :bordered="false">
                  每选一个行业知识库，系统都会多查一批参考内容。选得越多，回答要处理的内容越多，速度和费用都可能增加。建议只选当前版本真正需要的行业库。
                </n-alert>
                <p v-if="industryBasesQuery.isError.value" class="state-text danger">行业知识库列表获取失败，请重试</p>
              </div>
            </n-form-item>
          </n-grid-item>

          <!-- skill 管理：从市场（平台库/ClawHub）选 skill 配进版本；编辑态即时生效，新建态暂不支持（需先保存版本） -->
          <n-grid-item :span="2">
            <n-form-item label="Skill 列表">
              <div style="display: grid; gap: 8px; width: 100%">
                <!-- 已配 skill 列表展示 name/version + 删除 -->
                <div v-if="editingSkills.length === 0" class="state-text">暂无 skill</div>
                <div
                  v-for="skill in editingSkills"
                  :key="skill.name"
                  style="display: flex; align-items: center; justify-content: space-between; gap: 12px"
                >
                  <span>
                    {{ skill.name }}
                    <small v-if="skill.version" class="data-table-subtitle">v{{ skill.version }}</small>
                  </span>
                  <n-button size="small" tertiary @click="onDeleteSkill(skill.name)">删除</n-button>
                </div>
                <!-- 编辑态才可从市场选 skill；新建态需先保存版本才有 ID -->
                <template v-if="editingId">
                  <skill-market-browser
                    action-label="添加"
                    existing-label="已添加"
                    :allow-version-pick="true"
                    :action-pending="skillAdding"
                    :existing-names="editingSkillNames"
                    @action="onAddFromMarket"
                  />
                </template>
                <p v-else class="state-text">保存版本后可配置 skill</p>
                <p v-if="skillFeedback" class="state-text" :class="{ danger: skillFeedbackError }">{{ skillFeedback }}</p>
              </div>
            </n-form-item>
          </n-grid-item>

          <n-grid-item :span="2">
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
import { Plus } from 'lucide-vue-next'
import { NAlert, NButton, NCard, NForm, NFormItem, NGrid, NGridItem, NInput, NSelect, NSpace } from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import SkillMarketBrowser from '@/components/SkillMarketBrowser.vue'
import { actionColumn } from '@/components/columns'
import { useModelsQuery } from '@/api/hooks/useOrganizations'
import { useIndustryKnowledgeBasesQuery } from '@/api/hooks/useIndustryKnowledge'
import {
  AUXILIARY_SLOTS,
  emptyRouting,
  useAddVersionSkill,
  useAssistantVersionsQuery,
  useCreateAssistantVersion,
  useDeleteAssistantVersion,
  useDeleteAssistantVersionSkill,
  useRuntimeImagesQuery,
  useUpdateAssistantVersion,
  type AssistantVersionDTO,
  type AssistantVersionFormPayload,
  type AssistantVersionSkillDTO,
} from '@/api/hooks/useAssistantVersions'

// AssistantVersionsPage 是平台管理员的助手版本目录管理页：列表 + 新建/编辑 + 删除。
const { data: versions, isLoading, error } = useAssistantVersionsQuery()
const createMutation = useCreateAssistantVersion()
const updateMutation = useUpdateAssistantVersion()
const deleteMutation = useDeleteAssistantVersion()

// skill 管理状态：editingSkills 是当前编辑版本的 skill 列表，随添加/删除即时刷新。
// 新建态无版本 ID，不支持即时添加 skill（表单提交后切编辑态再配）。
const addSkillMutation = useAddVersionSkill()
const deleteSkillMutation = useDeleteAssistantVersionSkill()
const editingSkills = ref<AssistantVersionSkillDTO[]>([])
const skillAdding = ref(false)
const skillFeedback = ref('')
const skillFeedbackError = ref(false)

// editingSkillNames 是当前编辑版本已配 skill 名集合，传给市场浏览器做去重（已配则不可再加）。
const editingSkillNames = computed(() => new Set(editingSkills.value.map((s) => s.name)))

// onAddFromMarket 接收市场浏览器的添加动作，调后端 AddSkillFromLibrary，成功后刷新本地 skill 列表。
async function onAddFromMarket(p: { source: string; source_ref: string; name: string; version: string }) {
  if (!editingId.value) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  skillAdding.value = true
  try {
    const updated = await addSkillMutation.mutateAsync({
      id: editingId.value,
      input: { source: p.source, source_ref: p.source_ref, name: p.name, version: p.version },
    })
    // 后端返回更新后的完整版本，取其 skills 字段刷新本地状态。
    editingSkills.value = updated.skills ?? []
    skillFeedback.value = `已添加 skill ${p.name} v${p.version}`
  } catch (err) {
    skillFeedbackError.value = true
    skillFeedback.value = err instanceof Error ? err.message : '添加失败'
  } finally {
    skillAdding.value = false
  }
}

// onDeleteSkill 从当前编辑的版本删除一个 skill。
async function onDeleteSkill(skillName: string) {
  if (!editingId.value) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  try {
    const updated = await deleteSkillMutation.mutateAsync({ id: editingId.value, skillName })
    editingSkills.value = updated.skills ?? []
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
  industry_knowledge_base_ids: [],
})

// 模型与知识库列表仅在表单打开时请求；镜像列表常驻请求，列表的「镜像」列需要用它把 image_id 映射成可读 label。
const imagesQuery = useRuntimeImagesQuery()
const modelsQuery = useModelsQuery(() => formVisible.value)
const industryBasesQuery = useIndustryKnowledgeBasesQuery(() => formVisible.value)
const imageOptions = computed(() => (imagesQuery.data.value ?? []).map(img => ({ label: img.label, value: img.id })))
// imageLabelMap 把镜像 id 映射到 label，供列表「镜像」列展示可读名称。
const imageLabelMap = computed(() => new Map((imagesQuery.data.value ?? []).map(img => [img.id, img.label])))
const modelOptions = computed(() => (modelsQuery.data.value ?? []).map(m => ({ label: m.name, value: m.id })))
const industryKnowledgeOptions = computed(() => (industryBasesQuery.data.value?.items ?? []).map(item => ({
  label: item.name,
  value: item.id,
})))

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
  form.industry_knowledge_base_ids = []
}

// openCreate 打开空白新建表单。
function openCreate() {
  resetForm()
  editingId.value = null
  submitError.value = null
  actionFeedback.value = ''
  editingSkills.value = []
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
  // 后端返回行业库 id/name；表单只提交 id 列表，名称由多选选项展示。
  form.industry_knowledge_base_ids = (version.industry_knowledge_bases ?? []).map(item => item.id)
  editingId.value = version.id
  submitError.value = null
  actionFeedback.value = ''
  editingSkills.value = [...version.skills]
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
    industry_knowledge_base_ids: [...form.industry_knowledge_base_ids],
  }
}

// submit 根据 editingId 决定走创建还是更新。
// 新建态不支持在保存前添加 skill，版本落库后切换至编辑态再配置。
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
    // 新建版本：创建成功后切换至该版本的编辑态，让用户继续配置 skill。
    const created = await createMutation.mutateAsync(buildPayload())
    editingId.value = created.id
    editingSkills.value = created.skills ?? []
    skillFeedback.value = '版本已创建，可在下方从市场选择 skill'
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
  { title: '镜像', key: 'image_id', render: (row: AssistantVersionDTO) => imageLabelMap.value.get(row.image_id) || row.image_id || '—' },
  { title: '主模型', key: 'main_model', render: (row: AssistantVersionDTO) => row.main_model || '—' },
  { title: '修订号', key: 'revision', render: (row: AssistantVersionDTO) => `r${row.revision}` },
  { title: 'Skill 数', key: 'skills', render: (row: AssistantVersionDTO) => String(row.skills?.length ?? 0) },
  actionColumn<AssistantVersionDTO>([
    { label: '编辑', type: 'primary', onClick: openEdit },
    { label: '删除', onClick: requestDelete },
  ]),
])
</script>
