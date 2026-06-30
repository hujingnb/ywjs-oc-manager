<template>
  <n-modal
    :show="show"
    preset="card"
    :title="job ? t('apps.cron.form.titleEdit') : t('apps.cron.form.titleCreate')"
    style="width: 640px"
    @update:show="emit('update:show', $event)"
  >
    <n-form>
      <!-- ① 基础：name 必填 + prompt。 -->
      <n-form-item :label="t('apps.cron.form.labels.name')" required>
        <n-input v-model:value="form.name" :placeholder="t('apps.cron.form.namePlaceholder')" />
      </n-form-item>
      <n-form-item :label="t('apps.cron.form.labels.prompt')">
        <n-input v-model:value="form.prompt" type="textarea" :placeholder="t('apps.cron.form.promptPlaceholder')" />
      </n-form-item>

      <!-- ② 调度：可视化点选器 + 运行次数上限（原 repeat）。 -->
      <n-form-item :label="t('apps.cron.form.labels.schedule')" required>
        <ScheduleField v-model:value="form.schedule" />
      </n-form-item>
      <n-form-item :label="t('apps.cron.form.repeatLabel')">
        <n-space vertical :size="2" style="width: 100%">
          <n-input-number
            :value="form.repeat"
            :min="1"
            :clearable="!hasExistingRepeat"
            @update:value="onRepeatUpdate"
          />
          <span class="field-hint">{{ t('apps.cron.form.repeatHint') }}</span>
        </n-space>
      </n-form-item>

      <!-- ③ 投递：从已绑定渠道点选。 -->
      <n-form-item :label="t('apps.cron.form.labels.deliver')">
        <DeliverField v-model:value="form.deliver" :app-id="appId" />
      </n-form-item>

      <!-- ④ 执行：脚本点选 + 是否仅跑脚本。 -->
      <n-form-item :label="t('apps.cron.form.labels.script')">
        <WorkspaceFilePicker v-model:value="form.script" :app-id="appId" />
      </n-form-item>
      <n-form-item :label="t('apps.cron.form.labels.noAgent')">
        <n-space align="center" :size="6">
          <n-checkbox v-model:checked="form.no_agent">{{ t('apps.cron.form.noAgentLabel') }}</n-checkbox>
          <n-tooltip>
            <template #trigger><span class="field-help">?</span></template>
            {{ t('apps.cron.form.noAgentTooltip') }}
          </n-tooltip>
        </n-space>
      </n-form-item>

      <!-- 平台管理员·高级：workdir 与模型相关字段仅平台管理员可见，后端仍会做最终权限裁剪。 -->
      <template v-if="isPlatformAdmin">
        <n-form-item :label="t('apps.cron.form.labels.workdir')">
          <n-input v-model:value="form.workdir" :placeholder="t('apps.cron.form.workdirPlaceholder')" />
        </n-form-item>
        <n-form-item :label="t('apps.cron.form.labels.skills')">
          <n-input v-model:value="form.skills" :placeholder="t('apps.cron.form.skillsPlaceholder')" />
        </n-form-item>
        <n-form-item :label="t('apps.cron.form.labels.model')">
          <n-input v-model:value="form.model" :placeholder="t('apps.cron.form.modelPlaceholder')" />
        </n-form-item>
        <n-form-item :label="t('apps.cron.form.labels.provider')">
          <n-input v-model:value="form.provider" :placeholder="t('apps.cron.form.providerPlaceholder')" />
        </n-form-item>
        <n-form-item :label="t('apps.cron.form.labels.baseUrl')">
          <n-input v-model:value="form.base_url" :placeholder="t('apps.cron.form.baseUrlPlaceholder')" />
        </n-form-item>
      </template>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('update:show', false)">{{ t('common.actions.cancel') }}</n-button>
        <n-button
          type="primary"
          :loading="submitting"
          :disabled="!canSubmit"
          @click="onSubmit"
        >{{ job ? t('common.actions.save') : t('common.actions.create') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NModal, NSpace, NTooltip } from 'naive-ui'

const { t } = useI18n()

import ScheduleField from './ScheduleField.vue'
import DeliverField from './DeliverField.vue'
import WorkspaceFilePicker from './WorkspaceFilePicker.vue'

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
  // appId 透传给 deliver / script 子组件用于查询渠道与工作目录。
  appId: string
  // job 有值时进入编辑模式，无值时进入新建模式。
  job?: CronJob | null
  // isPlatformAdmin 控制高级字段显隐和 payload strip。
  isPlatformAdmin?: boolean
}>(), {
  job: null,
  isPlatformAdmin: false,
})

// appId 给模板内子组件直接引用。
const appId = computed(() => props.appId)

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

<style scoped>
.field-hint { font-size: 12px; color: #999; }
.field-help {
  display: inline-flex; width: 16px; height: 16px; border-radius: 50%;
  align-items: center; justify-content: center; font-size: 12px;
  background: #eee; color: #666; cursor: help;
}
</style>
