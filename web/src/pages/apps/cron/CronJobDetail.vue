<template>
  <n-card :bordered="true">
    <!-- 未选中任务时展示引导文案，避免右侧区域空白。 -->
    <template v-if="!job">
      <p class="state-text">{{ t('apps.cron.detail.selectHint') }}</p>
    </template>

    <template v-else>
      <div class="detail-head">
        <div>
          <p class="status-line">● {{ translateState(job.state) }}</p>
          <h3>{{ job.name || t('apps.cron.detail.unnamed') }}</h3>
          <p class="detail-sub">job_id <code>{{ job.id || '—' }}</code></p>
        </div>
        <n-space v-if="canWrite" :size="8">
          <n-button size="small" tertiary @click="emit('action', 'run')">{{ t('apps.cron.detail.runNow') }}</n-button>
          <n-button size="small" tertiary @click="emit('action', pauseVerb)">
            {{ pauseVerb === 'resume' ? t('apps.cron.detail.resume') : t('apps.cron.detail.pause') }}
          </n-button>
          <n-button size="small" tertiary @click="emit('edit')">{{ t('common.actions.edit') }}</n-button>
          <n-button size="small" type="error" tertiary @click="emit('action', 'delete')">{{ t('common.actions.delete') }}</n-button>
        </n-space>
      </div>

      <div class="section">
        <p class="section-title">Prompt</p>
        <p class="prompt-block">{{ job.prompt || '—' }}</p>
      </div>

      <div class="section">
        <p class="section-title">{{ t('apps.cron.detail.sectionBasic') }}</p>
        <div class="meta-grid">
          <div><span class="k">schedule</span><span class="v">{{ scheduleText }}</span></div>
          <div><span class="k">repeat</span><span class="v">{{ repeatText }}</span></div>
          <div><span class="k">enabled</span><span class="v">{{ job.enabled === false ? 'false' : 'true' }}</span></div>
          <div><span class="k">deliver</span><span class="v">{{ job.deliver || '—' }}</span></div>
          <div><span class="k">script</span><span class="v">{{ job.script || '—' }}</span></div>
          <div><span class="k">no_agent</span><span class="v">{{ job.no_agent ? 'true' : 'false' }}</span></div>
          <div><span class="k">workdir</span><span class="v">{{ job.workdir || '—' }}</span></div>
          <div><span class="k">next_run_at</span><span class="v">{{ job.next_run_at || '—' }}</span></div>
          <div><span class="k">last_run_at</span><span class="v">{{ job.last_run_at || '—' }}</span></div>
          <div><span class="k">last_status</span><span class="v">{{ job.last_status || '—' }}</span></div>
        </div>
        <p v-if="job.last_error || job.last_delivery_error" class="error-text">
          {{ job.last_error || job.last_delivery_error }}
        </p>
      </div>

      <div v-if="isPlatformAdmin" class="section">
        <p class="section-title">{{ t('apps.cron.detail.sectionAdvanced') }}</p>
        <div class="meta-grid">
          <div><span class="k">skills</span><span class="v">{{ job.skills?.length ? job.skills.join(', ') : '—' }}</span></div>
          <div><span class="k">model</span><span class="v">{{ job.model || '—' }}</span></div>
          <div><span class="k">provider</span><span class="v">{{ job.provider || '—' }}</span></div>
          <div><span class="k">base_url</span><span class="v">{{ job.base_url || '—' }}</span></div>
        </div>
      </div>

      <div class="section">
        <p class="section-title">{{ t('apps.cron.detail.sectionHistory') }}</p>
        <CronRunHistory
          :runs="history"
          :selected-file="selectedFile"
          @select="emit('select-output', $event)"
        />
      </div>

      <div class="section">
        <p class="section-title">{{ t('apps.cron.detail.sectionOutput') }}</p>
        <pre v-if="output?.content" class="output-pane">{{ output.content }}</pre>
        <p v-else class="state-text">{{ t('apps.cron.detail.selectOutputHint') }}</p>
      </div>
    </template>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NSpace } from 'naive-ui'

import type { CronJob, CronRunEntry, CronRunOutput } from '@/api/hooks/useCron'
import CronRunHistory from './CronRunHistory.vue'
import { scheduleDisplay, translateState } from './cronDisplay'

const { t } = useI18n()

type CronActionVerb = 'run' | 'pause' | 'resume' | 'delete'

// CronJobDetail 渲染右侧详情、写操作、运行历史和输出预览。
const props = withDefaults(defineProps<{
  // job 为 null 时显示选择引导；非 null 时展示详情。
  job: CronJob | null
  // history 是当前任务的执行记录。
  history: CronRunEntry[]
  // output 是 URL query.file 指向的单次运行输出。
  output: CronRunOutput | null
  // isPlatformAdmin 控制 skills/model/provider/base_url 这类高级字段显隐。
  isPlatformAdmin: boolean
  // selectedFile 来自 URL query.file，用于高亮历史列表。
  selectedFile?: string
  // canWrite 按 capabilities.features.write 控制写操作按钮。
  canWrite?: boolean
}>(), {
  selectedFile: undefined,
  canWrite: true,
})

const emit = defineEmits<{
  // action 向父组件传递写操作动词，父组件负责 mutation 和确认弹窗。
  action: [verb: CronActionVerb]
  // edit 请求父组件打开编辑弹窗。
  edit: []
  // select-output 向父组件传递输出文件名并同步 URL query.file。
  'select-output': [fileName: string]
}>()

// pauseVerb 根据 enabled/state 推导暂停或恢复按钮语义。
const pauseVerb = computed<CronActionVerb>(() =>
  props.job?.enabled === false || props.job?.state === 'paused' ? 'resume' : 'pause',
)

// scheduleText 走统一展示入口：上游 display 优先，缺失时前端兜底翻译，再退原文。
const scheduleText = computed(() => scheduleDisplay(props.job?.schedule))

// repeatText 同时展示重复上限和已完成次数，便于排查有限重复任务进度。
const repeatText = computed(() => {
  const repeat = props.job?.repeat
  if (!repeat) return t('apps.cron.detail.repeatUnlimited')
  const times = typeof repeat.times === 'number' ? repeat.times : t('apps.cron.detail.repeatUnlimited')
  const completed = typeof repeat.completed === 'number' ? repeat.completed : 0
  return `${completed} / ${times}`
})
</script>

<style scoped>
.detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.detail-head h3 {
  margin: 3px 0;
  font-size: 16px;
}
.status-line {
  color: var(--color-brand-text, #8a3700);
  font-size: 12px;
  margin: 0;
}
.detail-sub {
  color: var(--color-text-secondary, #6b7280);
  font-size: 11px;
  margin: 0;
}
.section {
  margin-top: 14px;
  border-top: 1px solid var(--color-border, #e5e7eb);
  padding-top: 12px;
}
.section-title {
  font-size: 11px;
  text-transform: uppercase;
  color: var(--color-text-secondary, #6b7280);
  margin: 0 0 8px;
}
.prompt-block {
  white-space: pre-wrap;
  color: var(--color-text-primary, #1f2329);
  font-size: 13px;
  margin: 0;
}
.meta-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 12px;
  font-size: 12px;
}
.meta-grid .k {
  color: var(--color-text-secondary, #6b7280);
  margin-right: 8px;
}
.meta-grid .v {
  word-break: break-all;
}
.error-text {
  color: var(--color-danger, #d93026);
  font-size: 12px;
  margin: 10px 0 0;
  white-space: pre-wrap;
}
.output-pane {
  background: var(--color-surface, #ffffff);
  border-radius: 4px;
  padding: 12px;
  max-height: 280px;
  overflow: auto;
  white-space: pre-wrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
}
.state-text {
  color: var(--color-text-secondary, #6b7280);
  font-size: 13px;
}
</style>
