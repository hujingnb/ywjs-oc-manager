<template>
  <n-modal
    :show="show"
    preset="card"
    :title="job ? '编辑定时任务' : '新建定时任务'"
    style="width: 640px"
    @update:show="emit('update:show', $event)"
  >
    <n-form>
      <!-- name / schedule 是创建任务所需的最小字段。 -->
      <n-form-item label="name" required>
        <n-input v-model:value="form.name" placeholder="任务名称" />
      </n-form-item>
      <n-form-item label="schedule" required>
        <n-input v-model:value="form.schedule" placeholder="cron 或 every 表达式" />
      </n-form-item>
      <n-form-item label="prompt">
        <n-input v-model:value="form.prompt" type="textarea" placeholder="触发时交给 Hermes 的提示词" />
      </n-form-item>
      <n-form-item label="deliver">
        <n-input v-model:value="form.deliver" placeholder="wechat / email / none" />
      </n-form-item>
      <n-form-item label="repeat">
        <n-input-number
          :value="form.repeat"
          :min="1"
          :clearable="!hasExistingRepeat"
          @update:value="onRepeatUpdate"
        />
      </n-form-item>
      <n-form-item label="script">
        <n-input v-model:value="form.script" placeholder="仓库内脚本文件名" />
      </n-form-item>
      <n-form-item label="no_agent">
        <n-checkbox v-model:checked="form.no_agent">跳过 agent 执行路径</n-checkbox>
      </n-form-item>
      <n-form-item label="workdir">
        <n-input v-model:value="form.workdir" placeholder="任务运行目录" />
      </n-form-item>

      <!-- 平台高级字段仅平台管理员可见；后端仍会做最终权限裁剪。 -->
      <template v-if="isPlatformAdmin">
        <n-form-item label="skills">
          <n-input v-model:value="form.skills" placeholder="逗号分隔，如 shell,git" />
        </n-form-item>
        <n-form-item label="model">
          <n-input v-model:value="form.model" placeholder="模型名称" />
        </n-form-item>
        <n-form-item label="provider">
          <n-input v-model:value="form.provider" placeholder="provider 名称" />
        </n-form-item>
        <n-form-item label="base_url">
          <n-input v-model:value="form.base_url" placeholder="https://provider.example/v1" />
        </n-form-item>
      </template>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('update:show', false)">取消</n-button>
        <n-button
          type="primary"
          :loading="submitting"
          :disabled="!canSubmit"
          @click="onSubmit"
        >{{ job ? '保存' : '创建' }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NModal, NSpace } from 'naive-ui'

import type { CronJob, CreateCronJobRequest, UpdateCronJobRequest } from '@/api/hooks/useCron'

interface CronFormState {
  // 表单中字符串统一先用原始输入保存，提交时再 trim。
  name: string
  schedule: string
  prompt: string
  deliver: string
  repeat: number | null
  script: string
  no_agent: boolean
  workdir: string
  skills: string
  model: string
  provider: string
  base_url: string
}

type CronFormPayload = CreateCronJobRequest | UpdateCronJobRequest

// CronJobFormModal 负责新建 / 编辑 Cron 任务字段，不直接调用 API。
const props = withDefaults(defineProps<{
  // show 控制弹窗显隐。
  show: boolean
  // submitting 来自父组件 mutation pending 状态。
  submitting: boolean
  // job 有值时进入编辑模式，无值时进入新建模式。
  job?: CronJob | null
  // isPlatformAdmin 控制高级字段显隐和 payload strip。
  isPlatformAdmin?: boolean
}>(), {
  job: null,
  isPlatformAdmin: false,
})

const emit = defineEmits<{
  // update:show 支持父组件 v-model:show。
  'update:show': [value: boolean]
  // submit 只发出已规整 payload，由父组件决定 create 还是 update。
  submit: [payload: CronFormPayload]
}>()

// emptyState 返回全新对象，避免 reactive 状态被引用复用。
function emptyState(): CronFormState {
  return {
    name: '',
    schedule: '',
    prompt: '',
    deliver: '',
    repeat: null,
    script: '',
    no_agent: false,
    workdir: '',
    skills: '',
    model: '',
    provider: '',
    base_url: '',
  }
}

const form = reactive<CronFormState>(emptyState())

// fillFromJob 在弹窗打开时把详情数据转成表单字段；编辑模式保留后端表达式。
function fillFromJob(job: CronJob | null | undefined) {
  Object.assign(form, emptyState(), {
    name: job?.name ?? '',
    schedule: job?.schedule?.expr || job?.schedule?.display || '',
    prompt: job?.prompt ?? '',
    deliver: job?.deliver ?? '',
    repeat: typeof job?.repeat?.times === 'number' ? job.repeat.times : null,
    script: job?.script ?? '',
    no_agent: Boolean(job?.no_agent),
    workdir: job?.workdir ?? '',
    skills: job?.skills?.join(', ') ?? '',
    model: job?.model ?? '',
    provider: job?.provider ?? '',
    base_url: job?.base_url ?? '',
  })
}

// 仅在弹窗打开时同步 job，避免用户编辑过程中父查询刷新覆盖输入。
watch(
  () => [props.show, props.job] as const,
  ([visible, job]) => {
    if (visible) fillFromJob(job)
  },
  { immediate: true },
)

// canSubmit 保证后端必填字段不为空；prompt 允许空，支持只运行脚本的任务。
const canSubmit = computed(() => form.name.trim() !== '' && form.schedule.trim() !== '')

// existingRepeatTimes 仅在编辑已有有限 repeat 任务时存在；clear_repeat 暂不支持，不能暴露清空路径。
const existingRepeatTimes = computed(() => {
  const times = props.job?.repeat?.times
  return typeof times === 'number' && times > 0 ? times : null
})

// hasExistingRepeat 控制编辑已有 repeat 任务时隐藏清空按钮，并配合 onRepeatUpdate 防御键盘清空。
const hasExistingRepeat = computed(() => existingRepeatTimes.value !== null)

// onRepeatUpdate 在创建模式允许为空；编辑已有 repeat 时把清空尝试恢复为原 repeat。
function onRepeatUpdate(value: number | null) {
  if (value === null && existingRepeatTimes.value !== null) {
    form.repeat = existingRepeatTimes.value
    return
  }
  form.repeat = value
}

// addStringField 对可选字符串字段统一 trim；编辑模式下空字符串表示清空旧值。
function addStringField(
  payload: Record<string, unknown>,
  key: string,
  value: string,
  includeEmpty: boolean,
) {
  const trimmed = value.trim()
  if (trimmed || includeEmpty) payload[key] = trimmed
}

// parseSkills 支持逗号和空白分隔，兼容用户从命令行参数复制过来的输入。
function parseSkills(value: string): string[] {
  return value
    .split(/[,\s]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

// buildPayload 是唯一 payload 组装点：非平台用户不会携带高级字段。
function buildPayload(): CronFormPayload {
  const isEdit = Boolean(props.job)
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    schedule: form.schedule.trim(),
    no_agent: form.no_agent,
  }
  addStringField(payload, 'prompt', form.prompt, isEdit)
  addStringField(payload, 'deliver', form.deliver, isEdit)
  addStringField(payload, 'script', form.script, isEdit)
  addStringField(payload, 'workdir', form.workdir, isEdit)
  if (typeof form.repeat === 'number' && form.repeat > 0) {
    payload.repeat = form.repeat
  } else if (existingRepeatTimes.value !== null) {
    payload.repeat = existingRepeatTimes.value
  }
  if (props.isPlatformAdmin) {
    const skills = parseSkills(form.skills)
    if (skills.length > 0) {
      payload.skills = skills
    } else if (isEdit && (props.job?.skills?.length ?? 0) > 0) {
      payload.clear_skills = true
    }
    addStringField(payload, 'model', form.model, isEdit)
    addStringField(payload, 'provider', form.provider, isEdit)
    addStringField(payload, 'base_url', form.base_url, isEdit)
  }
  return payload as CronFormPayload
}

// onSubmit 只在基础必填字段满足时发出 submit，防止禁用按钮被测试或脚本触发。
function onSubmit() {
  if (!canSubmit.value) return
  emit('submit', buildPayload())
}
</script>
