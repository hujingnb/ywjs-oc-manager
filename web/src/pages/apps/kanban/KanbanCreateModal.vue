<template>
  <n-modal
    :show="show"
    preset="card"
    title="新建任务"
    style="width: 520px"
    @update:show="emit('update:show', $event)"
  >
    <n-form>
      <!-- 标题：必填 -->
      <n-form-item label="标题" required>
        <n-input v-model:value="form.title" placeholder="任务标题" />
      </n-form-item>
      <!-- assignee：必填，指定执行任务的 hermes profile -->
      <n-form-item label="assignee" required>
        <n-input v-model:value="form.assignee" placeholder="处理该任务的 profile" />
      </n-form-item>
      <!-- 优先级：下拉选择，默认低(1) -->
      <n-form-item label="优先级">
        <n-select v-model:value="form.priority" :options="priorityOptions" />
      </n-form-item>
      <!-- 任务描述：多行文本，可选 -->
      <n-form-item label="任务描述">
        <n-input v-model:value="form.body" type="textarea" placeholder="任务详细说明" />
      </n-form-item>

      <!-- 高级字段：仅平台管理员可见（spec §5.5 字段级权限）。
           前端隐藏是 UX 优化；后端 handler 对非平台管理员仍会 strip 这些字段。-->
      <template v-if="isPlatformAdmin">
        <!-- skills：逗号分隔的技能标签，控制 hermes 选择 worker 的能力匹配 -->
        <n-form-item label="skills">
          <n-input v-model:value="form.skills" placeholder="逗号分隔的技能" />
        </n-form-item>
        <!-- workspace_kind：工作目录类型，如 scratch / dir:<path> / worktree -->
        <n-form-item label="workspace_kind">
          <n-input v-model:value="form.workspace_kind" placeholder="scratch / dir:<path> / worktree" />
        </n-form-item>
        <!-- parent_id：可选父任务 ID，用于任务子树结构 -->
        <n-form-item label="parent_id">
          <n-input v-model:value="form.parent_id" placeholder="父任务 ID（可选）" />
        </n-form-item>
        <!-- max_retries：任务失败后最大重试次数，0 表示不重试 -->
        <n-form-item label="max_retries">
          <n-input-number v-model:value="form.max_retries" :min="0" />
        </n-form-item>
      </template>
    </n-form>

    <!-- 底部操作区：取消 / 创建 -->
    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('update:show', false)">取消</n-button>
        <n-button
          type="primary"
          :loading="submitting"
          :disabled="!canSubmit"
          @click="onSubmit"
        >创建</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NButton, NSpace } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'

// KanbanCreateModal 是新建任务模态框。
// 高级字段（skills / workspace_kind / parent_id / max_retries）按角色显隐：
// 平台管理员可见并填写，其他角色只见必填字段（标题 / assignee / 优先级 / 描述）。
defineProps<{
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

// 从 auth store 获取角色信息，用于高级字段显隐控制。
const auth = useAuthStore()
// isPlatformAdmin 是 computed，需通过 .value 访问；模板里自动解包。
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// form 是表单的响应式状态。
// 所有字段初始化为空/默认值，提交时按角色组装 payload。
const form = reactive({
  title: '',
  assignee: '',
  priority: 1,
  body: '',
  // 以下高级字段仅在 isPlatformAdmin 时显示，非平台管理员的 payload 不带这些字段。
  skills: '',
  workspace_kind: '',
  parent_id: '',
  max_retries: 0,
})

// priorityOptions 是优先级下拉选项，value 对应后端的 0-9 整数。
const priorityOptions = [
  { label: '低 (1)', value: 1 },
  { label: '中 (2)', value: 2 },
  { label: '高 (3)', value: 3 },
]

// canSubmit：标题与 assignee 不能为空时才允许提交。
const canSubmit = computed(() => form.title.trim() !== '' && form.assignee.trim() !== '')

// onSubmit 按角色组装 payload：
// - 基础字段：所有角色都带（title / assignee / priority / body）。
// - 高级字段：仅平台管理员带（skills / workspace_kind / parent_id / max_retries）。
// 空字符串字段用 undefined 传递，避免后端收到空字符串被错误解析。
function onSubmit() {
  const payload: Record<string, unknown> = {
    title: form.title.trim(),
    assignee: form.assignee.trim(),
    priority: form.priority,
    body: form.body.trim() || undefined,
  }
  if (isPlatformAdmin.value) {
    payload.skills = form.skills.trim() || undefined
    payload.workspace_kind = form.workspace_kind.trim() || undefined
    payload.parent_id = form.parent_id.trim() || undefined
    payload.max_retries = form.max_retries || undefined
  }
  emit('submit', payload)
}
</script>
