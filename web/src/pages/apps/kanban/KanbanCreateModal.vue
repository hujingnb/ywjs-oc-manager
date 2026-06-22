<template>
  <n-modal
    :show="show"
    preset="card"
    :title="t('apps.kanban.createModal.title')"
    style="width: 520px"
    @update:show="emit('update:show', $event)"
  >
    <n-form>
      <!-- 标题：必填 -->
      <n-form-item :label="t('apps.kanban.createModal.fieldTitle')" required>
        <n-input v-model:value="form.title" :placeholder="t('apps.kanban.createModal.fieldTitlePlaceholder')" />
      </n-form-item>
      <!-- assignee：必填，指定执行任务的 hermes profile。
           后端按 slug 规则（^[a-z0-9][a-z0-9_-]{0,63}$）校验，含大写/空格/中文会被拒；
           这里做提交前同规则校验并常驻展示格式要求，避免用户填显示名后才在后端踩 400。 -->
      <n-form-item
        :label="t('apps.kanban.createModal.fieldAssignee')"
        required
        :validation-status="assigneeInvalid ? 'error' : undefined"
        :feedback="assigneeFeedback"
      >
        <n-input v-model:value="form.assignee" :placeholder="t('apps.kanban.createModal.fieldAssigneePlaceholder')" />
      </n-form-item>
      <!-- 优先级：下拉选择，默认低(1) -->
      <n-form-item :label="t('apps.kanban.createModal.fieldPriority')">
        <n-select v-model:value="form.priority" :options="priorityOptions" />
      </n-form-item>
      <!-- 任务描述：多行文本，可选 -->
      <n-form-item :label="t('apps.kanban.createModal.fieldBody')">
        <n-input v-model:value="form.body" type="textarea" :placeholder="t('apps.kanban.createModal.fieldBodyPlaceholder')" />
      </n-form-item>

      <!-- 高级字段：仅平台管理员可见（spec §5.5 字段级权限）。
           前端隐藏是 UX 优化；后端 handler 对非平台管理员仍会 strip 这些字段。-->
      <template v-if="isPlatformAdmin">
        <!-- skills：逗号或空格分隔的多技能，提交时 split 成 string[]，
             对应后端 CreateKanbanTaskRequest.skills（[]string） -->
        <n-form-item :label="t('apps.kanban.createModal.fieldSkills')">
          <n-input v-model:value="form.skills" :placeholder="t('apps.kanban.createModal.fieldSkillsPlaceholder')" />
        </n-form-item>
        <!-- workspace：单个 workspace 参数，对应后端 CreateKanbanTaskRequest.workspace，
             接受 scratch / worktree / dir:/路径 三种形式 -->
        <n-form-item :label="t('apps.kanban.createModal.fieldWorkspace')">
          <n-input v-model:value="form.workspace" :placeholder="t('apps.kanban.createModal.fieldWorkspacePlaceholder')" />
        </n-form-item>
        <!-- parent_id：可选父任务 ID，用于任务子树结构 -->
        <n-form-item :label="t('apps.kanban.createModal.fieldParentId')">
          <n-input v-model:value="form.parent_id" :placeholder="t('apps.kanban.createModal.fieldParentIdPlaceholder')" />
        </n-form-item>
        <!-- max_retries：任务失败后最大重试次数，0 表示不重试 -->
        <n-form-item :label="t('apps.kanban.createModal.fieldMaxRetries')">
          <n-input-number v-model:value="form.max_retries" :min="0" />
        </n-form-item>
      </template>
    </n-form>

    <!-- 底部操作区：取消 / 创建 -->
    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('update:show', false)">{{ t('common.actions.cancel') }}</n-button>
        <n-button
          type="primary"
          :loading="submitting"
          :disabled="!canSubmit"
          @click="onSubmit"
        >{{ t('common.actions.create') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NButton, NSpace } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'

// KanbanCreateModal 是新建任务模态框。
// 高级字段（skills / workspace / parent_id / max_retries）按角色显隐：
// 平台管理员可见并填写，其他角色只见必填字段（标题 / assignee / 优先级 / 描述）。
const props = defineProps<{
  // show 控制模态框显隐，由父组件通过 v-model:show 绑定。
  show: boolean
  // submitting 由父组件注入 mutation 的 isPending 状态，创建中时禁用提交按钮并显示 loading。
  submitting: boolean
}>()

const emit = defineEmits<{
  // 'update:show' 实现 v-model:show 双向绑定，点取消或遮罩关闭时触发。
  'update:show': [value: boolean]
  // submit 携带已组装好的 payload，由父组件负责调用 mutation。
  // 高级字段仅平台管理员时才包含在 payload 中（前端二次 strip）。
  submit: [payload: Record<string, unknown>]
}>()

const { t } = useI18n()

// 从 auth store 获取角色信息，用于高级字段显隐控制。
const auth = useAuthStore()
// isPlatformAdmin 是 computed，需通过 .value 访问；模板里自动解包。
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// form 是表单的响应式状态。
// 所有字段初始化为空/默认值，提交时按角色组装 payload。
// skills 以逗号分隔字符串收集用户输入，onSubmit 时 split 成 string[]。
// workspace 对应后端单字段（scratch / worktree / dir:/路径），不再拆分为两个字段。
const form = reactive({
  title: '',
  assignee: '',
  priority: 1,
  body: '',
  // 以下高级字段仅在 isPlatformAdmin 时显示，非平台管理员的 payload 不带这些字段。
  skills: '',
  workspace: '',
  parent_id: '',
  max_retries: 0,
})

// modal 关闭时重置表单，避免下次新建任务显示上次的脏数据。
watch(() => props.show, (visible) => {
  if (!visible) {
    Object.assign(form, {
      title: '', assignee: '', priority: 1, body: '',
      skills: '', workspace: '', parent_id: '', max_retries: 0,
    })
  }
})

// priorityOptions 是优先级下拉选项，用 computed 确保语言切换时随 t() 响应式更新。
// UI 仅暴露 低(1)/中(2)/高(3) 三档常用优先级，后端支持 0-9，更细粒度可后续按需扩展。
const priorityOptions = computed(() => [
  { label: `${t('apps.kanban.createModal.priorityLow')} (1)`, value: 1 },
  { label: `${t('apps.kanban.createModal.priorityMid')} (2)`, value: 2 },
  { label: `${t('apps.kanban.createModal.priorityHigh')} (3)`, value: 3 },
])

// ASSIGNEE_RE 与后端 service 层 boardSlugRe 完全一致：小写字母/数字开头，
// 仅含小写字母、数字、下划线、连字符，最长 64 字符。前端同规则提前拦截，
// 把后端笼统的 400「任务看板请求参数非法」转成可照做的输入提示。
const ASSIGNEE_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/

// assigneeInvalid：assignee 已填写但不符合 slug 规则时为 true（空值不算非法，由 required + canSubmit 兜底）。
const assigneeInvalid = computed(() => {
  const v = form.assignee.trim()
  return v !== '' && !ASSIGNEE_RE.test(v)
})

// assigneeFeedback：非法时给出纠正提示，否则常驻展示格式要求，降低用户试错成本。
const assigneeFeedback = computed(() =>
  assigneeInvalid.value
    ? t('apps.kanban.createModal.assigneeError')
    : t('apps.kanban.createModal.assigneeHint'),
)

// canSubmit：标题非空、assignee 非空且符合 slug 规则时才允许提交。
const canSubmit = computed(
  () => form.title.trim() !== '' && form.assignee.trim() !== '' && !assigneeInvalid.value,
)

// onSubmit 按角色组装 payload：
// - 基础字段：所有角色都带（title / assignee / priority / body）。
// - 高级字段：仅平台管理员带（skills / workspace / parent_id / max_retries）。
// 空字符串字段用 undefined 传递，避免后端收到空字符串被错误解析。
// skills：用户输入逗号或空格分隔的字符串，split 后 trim、过滤空项，得到 string[]。
// workspace：单个字符串直接传后端（scratch / worktree / dir:/路径）。
function onSubmit() {
  const payload: Record<string, unknown> = {
    title: form.title.trim(),
    assignee: form.assignee.trim(),
    priority: form.priority,
    body: form.body.trim() || undefined,
  }
  if (isPlatformAdmin.value) {
    // skills 输入框内容按逗号或空格分割，去空 trim，空数组时不传。
    const skillList = form.skills
      .split(/[,\s]+/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
    payload.skills = skillList.length > 0 ? skillList : undefined
    payload.workspace = form.workspace.trim() || undefined
    payload.parent_id = form.parent_id.trim() || undefined
    // max_retries 为 0 时不传，表示用后端默认重试次数；
    // 与后端 service 层「MaxRetries > 0 才生效」的语义一致，并非 bug。
    payload.max_retries = form.max_retries || undefined
  }
  emit('submit', payload)
}
</script>
