<template>
  <div class="schedule-field">
    <!-- 模式切换：按天/周、按间隔、高级表达式。 -->
    <n-radio-group :value="state.mode" @update:value="onModeChange">
      <n-radio-button value="calendar">按天/周</n-radio-button>
      <n-radio-button value="interval">按间隔</n-radio-button>
      <n-radio-button value="expr">高级表达式</n-radio-button>
    </n-radio-group>

    <!-- 模式 A：按天/周 + 多个时间点。 -->
    <div v-if="state.mode === 'calendar'" class="mode-block">
      <n-radio-group :value="state.calendar.frequency" @update:value="onFrequencyChange">
        <n-radio value="daily">每天</n-radio>
        <n-radio value="weekly">每周</n-radio>
      </n-radio-group>

      <n-checkbox-group
        v-if="state.calendar.frequency === 'weekly'"
        :value="state.calendar.weekdays"
        @update:value="onWeekdaysChange"
      >
        <n-checkbox v-for="d in WEEKDAY_OPTIONS" :key="d.value" :value="d.value" :label="d.label" />
      </n-checkbox-group>

      <div v-for="(t, i) in state.calendar.times" :key="i" class="time-row">
        <n-input-number :value="t.hour" :min="0" :max="23" @update:value="(v) => onTimeChange(i, 'hour', v)" />
        <span>:</span>
        <n-input-number :value="t.minute" :min="0" :max="59" @update:value="(v) => onTimeChange(i, 'minute', v)" />
        <n-button v-if="state.calendar.times.length > 1" @click="removeTime(i)">删除</n-button>
      </div>
      <n-button @click="addTime">+ 添加时间</n-button>
    </div>

    <!-- 模式 B：每 N 分钟/小时。 -->
    <div v-else-if="state.mode === 'interval'" class="mode-block">
      <n-space align="center">
        <span>每</span>
        <n-input-number :value="state.interval.value" :min="1" @update:value="onIntervalValueChange" />
        <n-select
          :value="state.interval.unit"
          :options="UNIT_OPTIONS"
          style="width: 100px"
          @update:value="onIntervalUnitChange"
        />
      </n-space>
    </div>

    <!-- 模式 C：原始表达式兜底。 -->
    <div v-else class="mode-block">
      <n-input :value="state.expr" placeholder="cron 0 9 * * 1-5 或 every 10m" @update:value="onExprChange" />
    </div>

    <!-- 实际运行预览：calendar 模式枚举真实触发点，分钟不一致时给告警。 -->
    <p v-if="preview.text" class="schedule-preview" :class="{ warn: preview.warn }">
      实际运行：{{ preview.text }}
      <span v-if="preview.warn" class="preview-warn-note">（时间点分钟不一致，将产生上述全部触发点）</span>
    </p>
    <p class="schedule-raw">将生成：{{ generated || '—' }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { NButton, NCheckbox, NCheckboxGroup, NInput, NInputNumber, NRadio, NRadioButton, NRadioGroup, NSelect, NSpace } from 'naive-ui'

import {
  buildScheduleExpr,
  parseScheduleExpr,
  describeSchedule,
  defaultScheduleState,
  type ScheduleMode,
} from './scheduleExpr'

// ScheduleField 对外只暴露一个 schedule 字符串（与后端契约一致），cron 语法全封装在内部。
const props = defineProps<{ value: string }>()
const emit = defineEmits<{ 'update:value': [value: string] }>()

// 周几多选项：value 用 cron dow 数值（1=周一…6=周六、0=周日）。
const WEEKDAY_OPTIONS = [
  { label: '一', value: 1 },
  { label: '二', value: 2 },
  { label: '三', value: 3 },
  { label: '四', value: 4 },
  { label: '五', value: 5 },
  { label: '六', value: 6 },
  { label: '日', value: 0 },
]
const UNIT_OPTIONS = [
  { label: '分钟', value: 'm' },
  { label: '小时', value: 'h' },
]

const state = reactive(defaultScheduleState())

// generated 是当前状态拼出的表达式，作为「单一事实源」用于发出与回显。
const generated = computed(() => buildScheduleExpr(state))
const preview = computed(() => describeSchedule(state))

// 外部 value 变化（如编辑态回填）且与当前生成结果不同才解析，避免与发出形成回环。
watch(
  () => props.value,
  (next) => {
    if ((next ?? '') !== generated.value) {
      Object.assign(state, parseScheduleExpr(next ?? ''))
    }
  },
  { immediate: true },
)

// 状态任何变化都把最新表达式发出去。
watch(generated, (expr) => emit('update:value', expr), { immediate: true })

function onModeChange(mode: ScheduleMode) {
  state.mode = mode
}
function onFrequencyChange(freq: 'daily' | 'weekly') {
  state.calendar.frequency = freq
}
function onWeekdaysChange(days: number[]) {
  state.calendar.weekdays = days
}
function onTimeChange(index: number, key: 'hour' | 'minute', value: number | null) {
  state.calendar.times[index][key] = value ?? 0
}
function addTime() {
  state.calendar.times.push({ hour: 9, minute: 0 })
}
function removeTime(index: number) {
  state.calendar.times.splice(index, 1)
}
function onIntervalValueChange(value: number | null) {
  state.interval.value = value && value > 0 ? value : 1
}
function onIntervalUnitChange(unit: 'm' | 'h') {
  state.interval.unit = unit
}
function onExprChange(expr: string) {
  state.expr = expr
}
</script>

<style scoped>
.schedule-field { display: flex; flex-direction: column; gap: 8px; }
.mode-block { display: flex; flex-direction: column; gap: 8px; }
.time-row { display: flex; align-items: center; gap: 6px; }
.schedule-preview { margin: 0; font-size: 13px; color: var(--n-text-color-3, #666); }
.schedule-preview.warn { color: #d97706; }
.preview-warn-note { font-size: 12px; }
.schedule-raw { margin: 0; font-size: 12px; color: #999; }
</style>
