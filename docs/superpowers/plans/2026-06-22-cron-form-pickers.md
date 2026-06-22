# 定时任务表单点选化改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「新建/编辑定时任务」表单从裸字段手填改为点选式（schedule 可视化、deliver 渠道下拉、script 文件点选、workdir 收进高级区、repeat 改名、no_agent 改文案），全程不改后端。

**Architecture:** 把所有易错逻辑（cron 表达式拼装/解析/触发点枚举、deliver 选项构建、workspace 文件过滤）抽成纯函数模块单测；`.vue` 组件做薄壳消费纯函数 + 既有 query hook。`CronJobFormModal.vue` 改为「基础/调度/投递/执行」四区块布局，schedule、deliver、script 各拆一个子组件，workdir 移入平台管理员折叠区。`buildPayload` 与后端契约完全不变。

**Tech Stack:** Vue 3 `<script setup>` + naive-ui + @tanstack/vue-query；测试 vitest + @vue/test-utils（jsdom）。

参考设计文档：`docs/superpowers/specs/2026-06-22-cron-form-pickers-design.md`

---

## 文件结构

- 新建 `web/src/pages/apps/cron/scheduleExpr.ts` — schedule 纯逻辑：状态类型、`buildScheduleExpr` / `parseScheduleExpr` / `describeSchedule`。
- 新建 `web/src/pages/apps/cron/scheduleExpr.spec.ts` — 上述纯函数单测。
- 新建 `web/src/pages/apps/cron/ScheduleField.vue` — schedule 三模式点选 UI，对外 `v-model:value` 一个 schedule 字符串。
- 新建 `web/src/pages/apps/cron/ScheduleField.spec.ts` — ScheduleField 交互/回填单测。
- 新建 `web/src/pages/apps/cron/deliverOptions.ts` — `buildDeliverOptions` 纯函数。
- 新建 `web/src/pages/apps/cron/deliverOptions.spec.ts` — 单测。
- 新建 `web/src/pages/apps/cron/DeliverField.vue` — deliver 渠道下拉薄壳（消费 `useChannelProgressQuery` + `buildDeliverOptions`）。
- 新建 `web/src/pages/apps/cron/WorkspaceFilePicker.vue` — script 文件选择按钮 + 弹窗（消费 `useWorkspaceQuery`）。
- 新建 `web/src/pages/apps/cron/workspaceFiles.ts` + `workspaceFiles.spec.ts` — `workspaceFileNames` 纯过滤函数。
- 修改 `web/src/pages/apps/cron/CronJobFormModal.vue` — 四区块布局、接入子组件、workdir 移入高级区、no_agent 文案、新增 `appId` prop。
- 修改 `web/src/pages/apps/cron/CronJobFormModal.spec.ts` — 适配新结构（子组件 stub）。
- 修改 `web/src/pages/apps/AppCronTab.vue:60-66` — 给 Modal 传 `:app-id="appId"`。

---

## Task 1: schedule 纯逻辑模块 scheduleExpr.ts

**Files:**
- Create: `web/src/pages/apps/cron/scheduleExpr.ts`
- Test: `web/src/pages/apps/cron/scheduleExpr.spec.ts`

- [ ] **Step 1: 写失败测试**

Create `web/src/pages/apps/cron/scheduleExpr.spec.ts`:

```ts
// scheduleExpr.spec.ts —— schedule 点选器纯逻辑单测。
// 覆盖：日历模式拼 cron、间隔模式拼 every、表达式回填、触发点枚举与笛卡尔积告警。
import { describe, expect, it } from 'vitest'

import {
  buildScheduleExpr,
  parseScheduleExpr,
  describeSchedule,
  defaultScheduleState,
  type ScheduleState,
} from './scheduleExpr'

// 构造日历态的便捷函数，减少样板。
function calendar(partial: Partial<ScheduleState['calendar']>): ScheduleState {
  return {
    ...defaultScheduleState(),
    mode: 'calendar',
    calendar: { frequency: 'daily', weekdays: [], times: [{ hour: 9, minute: 0 }], ...partial },
  }
}

describe('buildScheduleExpr', () => {
  // 每天单时间点：生成最简 cron。
  it('每天 09:00 → cron 0 9 * * *', () => {
    expect(buildScheduleExpr(calendar({ frequency: 'daily', times: [{ hour: 9, minute: 0 }] }))).toBe('cron 0 9 * * *')
  })

  // 每周多日 + 多个同分钟时间点：分钟去重，小时升序列表，dow 升序列表。
  it('每周一三五 09:00 18:00 → cron 0 9,18 * * 1,3,5', () => {
    const expr = buildScheduleExpr(calendar({
      frequency: 'weekly',
      weekdays: [1, 3, 5],
      times: [{ hour: 18, minute: 0 }, { hour: 9, minute: 0 }],
    }))
    expect(expr).toBe('cron 0 9,18 * * 1,3,5')
  })

  // 分钟不一致：分钟字段也变成列表，构成笛卡尔积（多触发，由 describe 暴露）。
  it('分钟不一致 → 分钟与小时都成列表', () => {
    const expr = buildScheduleExpr(calendar({
      frequency: 'daily',
      times: [{ hour: 9, minute: 0 }, { hour: 18, minute: 30 }],
    }))
    expect(expr).toBe('cron 0,30 9,18 * * *')
  })

  // 间隔模式：拼 every 表达式。
  it('间隔 10 分钟 → every 10m', () => {
    const state: ScheduleState = { ...defaultScheduleState(), mode: 'interval', interval: { value: 10, unit: 'm' } }
    expect(buildScheduleExpr(state)).toBe('every 10m')
  })

  // 高级表达式模式：原样输出（trim）。
  it('表达式模式原样输出', () => {
    const state: ScheduleState = { ...defaultScheduleState(), mode: 'expr', expr: '  cron 0 0 1 * *  ' }
    expect(buildScheduleExpr(state)).toBe('cron 0 0 1 * *')
  })

  // 周模式未选任何星期：视为非法，返回空串让 UI 拦截提交。
  it('每周但未选星期 → 空串', () => {
    expect(buildScheduleExpr(calendar({ frequency: 'weekly', weekdays: [] }))).toBe('')
  })

  // 无时间点：返回空串。
  it('无时间点 → 空串', () => {
    expect(buildScheduleExpr(calendar({ times: [] }))).toBe('')
  })
})

describe('parseScheduleExpr', () => {
  // 裸 cron 表达式（无 cron 前缀，列表页 expr 形态）回填日历态，并把小时×分钟笛卡尔展开为时间点。
  it('回填每天多时间点', () => {
    const state = parseScheduleExpr('0 9,18 * * *')
    expect(state.mode).toBe('calendar')
    expect(state.calendar.frequency).toBe('daily')
    expect(state.calendar.times).toEqual([{ hour: 9, minute: 0 }, { hour: 18, minute: 0 }])
  })

  // 带 cron 前缀 + 周列表 + 范围都能解析；dow 7 归一到 0（周日）。
  it('回填每周（范围）', () => {
    const state = parseScheduleExpr('cron 0 9 * * 1-5')
    expect(state.mode).toBe('calendar')
    expect(state.calendar.frequency).toBe('weekly')
    expect(state.calendar.weekdays).toEqual([1, 2, 3, 4, 5])
  })

  // every 表达式回填间隔态。
  it('回填间隔', () => {
    const state = parseScheduleExpr('every 30m')
    expect(state.mode).toBe('interval')
    expect(state.interval).toEqual({ value: 30, unit: 'm' })
  })

  // 无法识别为日历/间隔的复杂表达式（带步进）落到 expr 模式，原值保留。
  it('复杂表达式落到 expr 模式', () => {
    const state = parseScheduleExpr('*/15 * * * *')
    expect(state.mode).toBe('expr')
    expect(state.expr).toBe('*/15 * * * *')
  })

  // 空串返回默认日历态，不抛错。
  it('空串安全降级为默认态', () => {
    expect(parseScheduleExpr('').mode).toBe('calendar')
  })
})

describe('describeSchedule', () => {
  // 同分钟多时间点：预览精确，无告警。
  it('每天 09:00、18:00 无告警', () => {
    const { text, warn } = describeSchedule(calendar({
      frequency: 'daily',
      times: [{ hour: 9, minute: 0 }, { hour: 18, minute: 0 }],
    }))
    expect(text).toBe('每天 09:00、18:00')
    expect(warn).toBe(false)
  })

  // 分钟不一致：枚举出 4 个触发点并告警（用户选了 2 个）。
  it('分钟不一致触发笛卡尔积告警', () => {
    const { text, warn } = describeSchedule(calendar({
      frequency: 'daily',
      times: [{ hour: 9, minute: 0 }, { hour: 18, minute: 30 }],
    }))
    expect(text).toBe('每天 09:00、09:30、18:00、18:30')
    expect(warn).toBe(true)
  })

  // 每周预览带星期文案。
  it('每周预览', () => {
    const { text } = describeSchedule(calendar({ frequency: 'weekly', weekdays: [1, 5], times: [{ hour: 8, minute: 0 }] }))
    expect(text).toBe('周一、周五 08:00')
  })

  // 间隔模式预览。
  it('间隔预览', () => {
    const state: ScheduleState = { ...defaultScheduleState(), mode: 'interval', interval: { value: 2, unit: 'h' } }
    expect(describeSchedule(state).text).toBe('每 2 小时')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm test -- run src/pages/apps/cron/scheduleExpr.spec.ts`
Expected: FAIL（`scheduleExpr` 模块不存在 / 函数未定义）

- [ ] **Step 3: 实现 scheduleExpr.ts**

Create `web/src/pages/apps/cron/scheduleExpr.ts`:

```ts
// scheduleExpr.ts —— 定时任务调度点选器的纯逻辑层。
// 收口 cron / every 表达式的拼装、回填解析与人类可读预览，UI 层只消费这些纯函数，
// 不感知 cron 语法。所有解析失败一律安全降级，绝不抛错（与 cronDisplay 的翻译原则一致）。
import { translateCronExpr } from './cronDisplay'

// ScheduleMode 是点选器三种输入方式：日历（按天/周+时间点）、间隔、原始表达式兜底。
export type ScheduleMode = 'calendar' | 'interval' | 'expr'

// TimePoint 是一个 HH:MM 时间点。
export interface TimePoint {
  hour: number
  minute: number
}

// CalendarState 描述「按天/周 + 多时间点」的选择。weekdays 用标准 cron 数值（0=周日…6=周六）。
export interface CalendarState {
  frequency: 'daily' | 'weekly'
  weekdays: number[]
  times: TimePoint[]
}

// IntervalState 描述「每 N 分钟/小时」。
export interface IntervalState {
  value: number
  unit: 'm' | 'h'
}

// ScheduleState 是点选器完整内部状态；三种模式各持有自己的子状态，切换模式不丢数据。
export interface ScheduleState {
  mode: ScheduleMode
  calendar: CalendarState
  interval: IntervalState
  expr: string
}

// WEEKDAY_LABELS 以 cron dow 数值为索引（0=周日）；与 cronDisplay 同约定，本模块自带一份保持自包含。
const WEEKDAY_LABELS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

// defaultScheduleState 返回默认日历态：每天 09:00，避免空表单无法提交。
export function defaultScheduleState(): ScheduleState {
  return {
    mode: 'calendar',
    calendar: { frequency: 'daily', weekdays: [], times: [{ hour: 9, minute: 0 }] },
    interval: { value: 10, unit: 'm' },
    expr: '',
  }
}

// pad2 把小时/分钟补两位，保证 HH:MM 稳定。
function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

// uniqSortNums 去重升序，用于把多个时间点的分钟/小时收敛成 cron 列表字段。
function uniqSortNums(nums: number[]): number[] {
  return [...new Set(nums)].sort((a, b) => a - b)
}

// buildScheduleExpr 把内部状态拼成后端契约接受的 schedule 字符串；非法选择返回空串由 UI 拦截。
export function buildScheduleExpr(state: ScheduleState): string {
  if (state.mode === 'interval') {
    return `every ${state.interval.value}${state.interval.unit}`
  }
  if (state.mode === 'expr') {
    return state.expr.trim()
  }
  // calendar 模式：分钟、小时各自去重升序成列表，dow 由频率决定。
  const { frequency, weekdays, times } = state.calendar
  if (times.length === 0) return ''
  if (frequency === 'weekly' && weekdays.length === 0) return ''
  const minutes = uniqSortNums(times.map((t) => t.minute))
  const hours = uniqSortNums(times.map((t) => t.hour))
  const dow = frequency === 'daily' ? '*' : uniqSortNums(weekdays).join(',')
  return `cron ${minutes.join(',')} ${hours.join(',')} * * ${dow}`
}

// parseNumField 解析单个 cron 数值字段，支持 'n' / 'a,b' / 'a-b' 混合；含步进或非数字返回 null。
function parseNumField(field: string): number[] | null {
  const out: number[] = []
  for (const token of field.split(',')) {
    const range = token.match(/^(\d+)-(\d+)$/)
    if (range) {
      const start = Number(range[1])
      const end = Number(range[2])
      if (start > end) return null
      for (let i = start; i <= end; i += 1) out.push(i)
      continue
    }
    if (/^\d+$/.test(token)) {
      out.push(Number(token))
      continue
    }
    return null
  }
  return out
}

// parseScheduleExpr 把后端 schedule 字符串回填为内部状态；识别不了一律落 expr 模式保留原值。
export function parseScheduleExpr(raw: string): ScheduleState {
  const state = defaultScheduleState()
  const s = raw.trim()
  if (!s) return state

  // 间隔：every 10m / 10m / every 2h。
  const iv = s.match(/^(?:every\s+)?(\d+)\s*([mh])$/i)
  if (iv) {
    state.mode = 'interval'
    state.interval = { value: Number(iv[1]), unit: iv[2].toLowerCase() as 'm' | 'h' }
    return state
  }

  // 标准 5 段 cron（允许 cron 前缀）。
  const parts = s.replace(/^cron\s+/i, '').split(/\s+/)
  if (parts.length === 5) {
    const [min, hour, dom, mon, dow] = parts
    const mins = parseNumField(min)
    const hours = parseNumField(hour)
    if (mins && hours && dom === '*' && mon === '*') {
      if (dow === '*') {
        state.mode = 'calendar'
        state.calendar = {
          frequency: 'daily',
          weekdays: [],
          // 小时×分钟笛卡尔展开为时间点，与 build 的列表语义一致，保证往返。
          times: hours.flatMap((h) => mins.map((m) => ({ hour: h, minute: m }))),
        }
        return state
      }
      const days = parseNumField(dow)
      if (days) {
        state.mode = 'calendar'
        state.calendar = {
          frequency: 'weekly',
          weekdays: uniqSortNums(days.map((d) => d % 7)),
          times: hours.flatMap((h) => mins.map((m) => ({ hour: h, minute: m }))),
        }
        return state
      }
    }
  }

  // 兜底：表达式模式。
  state.mode = 'expr'
  state.expr = s
  return state
}

// describeSchedule 产出「实际运行」人类可读预览；calendar 模式按真实 cron 网格枚举触发点，
// 故分钟不一致导致的笛卡尔多触发会被如实展示并置 warn=true，绝不静默隐藏。
export function describeSchedule(state: ScheduleState): { text: string; warn: boolean } {
  if (state.mode === 'interval') {
    const unit = state.interval.unit === 'h' ? '小时' : '分钟'
    return { text: `每 ${state.interval.value} ${unit}`, warn: false }
  }
  if (state.mode === 'expr') {
    return { text: translateCronExpr(undefined, state.expr), warn: false }
  }
  const { frequency, weekdays, times } = state.calendar
  if (times.length === 0) return { text: '', warn: false }
  const minutes = uniqSortNums(times.map((t) => t.minute))
  const hours = uniqSortNums(times.map((t) => t.hour))
  const days =
    frequency === 'daily' || weekdays.length === 0
      ? '每天'
      : uniqSortNums(weekdays).map((d) => WEEKDAY_LABELS[d % 7]).join('、')
  // 按小时升序、再分钟升序枚举所有触发点。
  const combos = hours.flatMap((h) => minutes.map((m) => `${pad2(h)}:${pad2(m)}`))
  const selectedCount = new Set(times.map((t) => `${t.hour}:${t.minute}`)).size
  return { text: `${days} ${combos.join('、')}`, warn: combos.length > selectedCount }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- run src/pages/apps/cron/scheduleExpr.spec.ts`
Expected: PASS（所有用例绿）

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/apps/cron/scheduleExpr.ts web/src/pages/apps/cron/scheduleExpr.spec.ts
git commit -m "feat(cron): 新增 schedule 点选器纯逻辑模块

抽出 buildScheduleExpr / parseScheduleExpr / describeSchedule，封装 cron 与
every 表达式的拼装、回填解析与触发点枚举预览；calendar 模式按真实 cron 网格
枚举触发点，分钟不一致导致的笛卡尔多触发如实暴露并告警。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: ScheduleField.vue 点选 UI

**Files:**
- Create: `web/src/pages/apps/cron/ScheduleField.vue`
- Test: `web/src/pages/apps/cron/ScheduleField.spec.ts`

- [ ] **Step 1: 写失败测试**

Create `web/src/pages/apps/cron/ScheduleField.spec.ts`:

```ts
// ScheduleField.spec.ts —— 调度点选子组件交互单测。
// 覆盖：默认态发出 cron、添加时间点重算表达式、外部 value 回填模式、预览文案渲染。
// mock naive-ui：聚焦数据流，避免真实组件 teleport / 时间控件干扰。
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import ScheduleField from './ScheduleField.vue'

vi.mock('naive-ui', () => ({
  NRadioGroup: {
    props: ['value'],
    emits: ['update:value'],
    template: '<div class="radio-group" :data-value="value"><slot /></div>',
  },
  NRadio: { props: ['value'], template: '<label><slot /></label>' },
  NRadioButton: { props: ['value'], template: '<label><slot /></label>' },
  NCheckboxGroup: {
    props: ['value'],
    emits: ['update:value'],
    template: '<div class="cbg" :data-value="JSON.stringify(value)"><slot /></div>',
  },
  NCheckbox: { props: ['value', 'label'], template: '<label>{{ label }}</label>' },
  NInputNumber: {
    props: ['value'],
    emits: ['update:value'],
    template: '<input type="number" :value="value ?? \'\'" @input="$emit(\'update:value\', Number($event.target.value))" />',
  },
  NInput: {
    props: ['value', 'placeholder'],
    emits: ['update:value'],
    template: '<input :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NSelect: {
    props: ['value', 'options'],
    emits: ['update:value'],
    template: '<select :value="value" @change="$emit(\'update:value\', $event.target.value)"></select>',
  },
  NButton: { emits: ['click'], template: '<button @click="$emit(\'click\')"><slot /></button>' },
  NSpace: { template: '<div><slot /></div>' },
}))

function mountField(value = '') {
  return mount(ScheduleField, { props: { value, 'onUpdate:value': () => {} } })
}

describe('ScheduleField', () => {
  // 默认日历态（每天 09:00）挂载即发出对应 cron。
  it('挂载发出默认 cron', async () => {
    const wrapper = mountField('')
    await nextTick()
    const emitted = wrapper.emitted('update:value')
    expect(emitted?.at(-1)?.[0]).toBe('cron 0 9 * * *')
  })

  // 外部传入 every 表达式：组件解析后预览展示「每 10 分钟」。
  it('回填间隔表达式并预览', async () => {
    const wrapper = mountField('every 10m')
    await nextTick()
    expect(wrapper.text()).toContain('每 10 分钟')
  })

  // 改一个时间点的小时：重新发出更新后的 cron 表达式。
  it('修改时间点重算表达式', async () => {
    const wrapper = mountField('cron 0 9 * * *')
    await nextTick()
    const hourInput = wrapper.find('input[type="number"]')
    await hourInput.setValue(18)
    await nextTick()
    expect(wrapper.emitted('update:value')?.at(-1)?.[0]).toBe('cron 0 18 * * *')
  })

  // 预览区展示「实际运行」中文文案。
  it('展示实际运行预览', async () => {
    const wrapper = mountField('cron 0 9 * * *')
    await nextTick()
    expect(wrapper.text()).toContain('每天 09:00')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm test -- run src/pages/apps/cron/ScheduleField.spec.ts`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现 ScheduleField.vue**

Create `web/src/pages/apps/cron/ScheduleField.vue`:

```vue
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- run src/pages/apps/cron/ScheduleField.spec.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/apps/cron/ScheduleField.vue web/src/pages/apps/cron/ScheduleField.spec.ts
git commit -m "feat(cron): 新增 schedule 可视化点选子组件 ScheduleField

三模式（按天/周+多时间点、按间隔、高级表达式）点选，对外只进出一个 schedule
字符串；常驻「实际运行」预览枚举真实触发点，分钟不一致时给笛卡尔多触发告警。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: deliver 渠道下拉（纯函数 + 子组件）

**Files:**
- Create: `web/src/pages/apps/cron/deliverOptions.ts`
- Create: `web/src/pages/apps/cron/deliverOptions.spec.ts`
- Create: `web/src/pages/apps/cron/DeliverField.vue`

- [ ] **Step 1: 写失败测试**

Create `web/src/pages/apps/cron/deliverOptions.spec.ts`:

```ts
// deliverOptions.spec.ts —— deliver 下拉选项构建纯逻辑单测。
// 覆盖：不投递常驻、仅已绑定渠道入选项、编辑态未知值保留不丢。
import { describe, expect, it } from 'vitest'

import { buildDeliverOptions } from './deliverOptions'

describe('buildDeliverOptions', () => {
  // 无已绑定渠道：只有「不投递」。
  it('无绑定渠道仅不投递', () => {
    expect(buildDeliverOptions([], '')).toEqual([{ label: '不投递', value: '' }])
  })

  // 已绑定 wechat：追加中文渠道项。
  it('已绑定渠道入选项', () => {
    expect(buildDeliverOptions(['wechat'], '')).toEqual([
      { label: '不投递', value: '' },
      { label: '微信', value: 'wechat' },
    ])
  })

  // 编辑态当前值不在已绑定列表：保留为额外项，避免回填丢值。
  it('保留未知的当前值', () => {
    const opts = buildDeliverOptions([], 'email')
    expect(opts).toContainEqual({ label: '邮件', value: 'email' })
  })

  // 当前值已在已绑定列表：不重复追加。
  it('当前值已存在不重复', () => {
    expect(buildDeliverOptions(['wechat'], 'wechat')).toHaveLength(2)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm test -- run src/pages/apps/cron/deliverOptions.spec.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现 deliverOptions.ts**

Create `web/src/pages/apps/cron/deliverOptions.ts`:

```ts
// deliverOptions.ts —— deliver 投递渠道下拉选项构建。
// 规则：「不投递」常驻置顶；只列出已绑定渠道；编辑态保留当前值（即使未绑定）避免回填丢值。
import { translateDeliver } from './cronDisplay'

// DeliverOption 是 n-select 选项结构。
export interface DeliverOption {
  label: string
  value: string
}

// buildDeliverOptions 组装下拉项。boundTypes 是已绑定渠道类型集合；currentValue 是编辑态原值。
export function buildDeliverOptions(boundTypes: string[], currentValue: string): DeliverOption[] {
  const options: DeliverOption[] = [{ label: '不投递', value: '' }]
  for (const type of boundTypes) {
    options.push({ label: translateDeliver(type), value: type })
  }
  // 编辑态当前值非空且不在已绑定列表里时，单独保留，避免下拉无对应项导致显示空白。
  if (currentValue && !boundTypes.includes(currentValue)) {
    options.push({ label: translateDeliver(currentValue), value: currentValue })
  }
  return options
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- run src/pages/apps/cron/deliverOptions.spec.ts`
Expected: PASS

- [ ] **Step 5: 实现 DeliverField.vue（薄壳，无独立测试）**

Create `web/src/pages/apps/cron/DeliverField.vue`:

```vue
<template>
  <n-select :value="value" :options="options" @update:value="emit('update:value', $event)" />
  <p v-if="boundTypes.length === 0" class="deliver-hint">暂无已绑定渠道，去「渠道」页绑定后可在此选择。</p>
</template>

<script setup lang="ts">
import { computed, toRef } from 'vue'
import { NSelect } from 'naive-ui'

import { useChannelProgressQuery } from '@/api/hooks/useChannel'
import { buildDeliverOptions } from './deliverOptions'

// DeliverField 是 deliver 字段薄壳：查询当前支持渠道的绑定状态，组装下拉选项。
// 目前产品仅 wechat 真正可投递；新增渠道时扩 SUPPORTED_CHANNELS 即可。
const props = defineProps<{ value: string; appId: string }>()
const emit = defineEmits<{ 'update:value': [value: string] }>()

const SUPPORTED_CHANNELS = ['wechat'] as const

const appId = toRef(props, 'appId')
// 静态渠道清单，故可在 setup 顶层固定调用 hook（数量不变，不违反 hook 规则）。
const wechatProgress = useChannelProgressQuery(appId, computed(() => 'wechat'))

// boundTypes 收集 status==='bound' 的渠道类型。
const boundTypes = computed(() => {
  const bound: string[] = []
  if (wechatProgress.data.value?.status === 'bound') bound.push('wechat')
  return bound
})

const options = computed(() => buildDeliverOptions(boundTypes.value, props.value))
</script>

<style scoped>
.deliver-hint { margin: 4px 0 0; font-size: 12px; color: #999; }
</style>
```

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/apps/cron/deliverOptions.ts web/src/pages/apps/cron/deliverOptions.spec.ts web/src/pages/apps/cron/DeliverField.vue
git commit -m "feat(cron): 新增 deliver 渠道下拉子组件

buildDeliverOptions 纯函数组装选项（不投递常驻、仅已绑定渠道入选、编辑态保留
未知当前值）；DeliverField 复用 useChannelProgressQuery 查 wechat 绑定状态。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: script 文件点选（纯函数 + 选择器子组件）

**Files:**
- Create: `web/src/pages/apps/cron/workspaceFiles.ts`
- Create: `web/src/pages/apps/cron/workspaceFiles.spec.ts`
- Create: `web/src/pages/apps/cron/WorkspaceFilePicker.vue`

- [ ] **Step 1: 写失败测试**

Create `web/src/pages/apps/cron/workspaceFiles.spec.ts`:

```ts
// workspaceFiles.spec.ts —— 工作目录根层文件名提取纯逻辑单测。
// 覆盖：只取文件不取目录、空列表、回填 basename（script 不允许带路径）。
import { describe, expect, it } from 'vitest'

import { workspaceFileNames } from './workspaceFiles'
import type { WorkspaceListing } from '@/api/hooks/useWorkspace'

describe('workspaceFileNames', () => {
  // 只保留 is_dir=false 的条目，目录被过滤。
  it('过滤掉目录只留文件', () => {
    const listing: WorkspaceListing = {
      path: '/',
      entries: [
        { path: 'daily.py', name: 'daily.py', size: 10, is_dir: false, mod_time: '' },
        { path: 'logs', name: 'logs', size: 0, is_dir: true, mod_time: '' },
      ],
    }
    expect(workspaceFileNames(listing)).toEqual(['daily.py'])
  })

  // null / 空列表返回空数组。
  it('空列表返回空', () => {
    expect(workspaceFileNames(null)).toEqual([])
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm test -- run src/pages/apps/cron/workspaceFiles.spec.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现 workspaceFiles.ts**

Create `web/src/pages/apps/cron/workspaceFiles.ts`:

```ts
// workspaceFiles.ts —— 从工作目录列表提取可选脚本文件名。
// 后端要求 script 是无路径单文件名，故只取根目录直接子项中的文件（is_dir=false）的 name。
import type { WorkspaceListing } from '@/api/hooks/useWorkspace'

// workspaceFileNames 返回根层文件名列表；空响应安全返回空数组。
export function workspaceFileNames(listing: WorkspaceListing | null | undefined): string[] {
  if (!listing) return []
  return listing.entries.filter((e) => !e.is_dir).map((e) => e.name)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm test -- run src/pages/apps/cron/workspaceFiles.spec.ts`
Expected: PASS

- [ ] **Step 5: 实现 WorkspaceFilePicker.vue（薄壳）**

Create `web/src/pages/apps/cron/WorkspaceFilePicker.vue`:

```vue
<template>
  <n-space align="center" :size="6">
    <n-input :value="value" placeholder="仓库内脚本文件名" @update:value="emit('update:value', $event)" />
    <n-select
      :value="null"
      :options="fileOptions"
      :loading="query.isLoading.value"
      placeholder="选择文件"
      style="width: 180px"
      @update:value="onPick"
    />
  </n-space>
</template>

<script setup lang="ts">
import { computed, ref, toRef } from 'vue'
import { NInput, NSelect, NSpace } from 'naive-ui'

import { useWorkspaceQuery } from '@/api/hooks/useWorkspace'
import { workspaceFileNames } from './workspaceFiles'

// WorkspaceFilePicker 给 script 字段提供「手输 + 从工作目录根层文件点选」两种方式。
const props = defineProps<{ value: string; appId: string }>()
const emit = defineEmits<{ 'update:value': [value: string] }>()

const appId = toRef(props, 'appId')
// 列工作目录根层（path='' / keyword=''）。
const query = useWorkspaceQuery(appId, ref(''), ref(''))

const fileOptions = computed(() =>
  workspaceFileNames(query.data.value).map((name) => ({ label: name, value: name })),
)

// 选中文件即把 basename 回填到 script。
function onPick(name: string) {
  emit('update:value', name)
}
</script>
```

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/apps/cron/workspaceFiles.ts web/src/pages/apps/cron/workspaceFiles.spec.ts web/src/pages/apps/cron/WorkspaceFilePicker.vue
git commit -m "feat(cron): 新增 script 文件点选子组件

workspaceFileNames 纯函数提取工作目录根层文件名；WorkspaceFilePicker 复用
useWorkspaceQuery，支持手输 + 从根层文件点选回填 basename（script 不允许带路径）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: 重构 CronJobFormModal.vue + 适配测试

**Files:**
- Modify: `web/src/pages/apps/cron/CronJobFormModal.vue`
- Modify: `web/src/pages/apps/cron/CronJobFormModal.spec.ts`

说明：`buildPayload` / `addStringField` / `parseSkills` / repeat 逻辑 / `emptyState` / `fillFromJob` 全部保持现状不动（payload 与后端契约不变）；只改 `<template>` 布局、引入子组件、新增 `appId` prop、把 workdir 移入高级区、改 no_agent 文案。

- [ ] **Step 1: 改写测试（先红）**

Replace `web/src/pages/apps/cron/CronJobFormModal.spec.ts` 全文为：

```ts
// CronJobFormModal.spec.ts —— Cron 任务表单弹窗单元测试。
// 覆盖：四区块字段显隐（workdir 收进高级区）、payload 组装、非平台 strip、编辑清空、repeat 保留。
// 子组件 ScheduleField/DeliverField/WorkspaceFilePicker 以 stub 替身，聚焦 Modal 自身的布局与 payload。
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import type { CronJob } from '@/api/hooks/useCron'
import CronJobFormModal from './CronJobFormModal.vue'

// mock naive-ui：表单测试聚焦字段显隐与 payload 组装，避免 NModal teleport 干扰 wrapper 查询。
vi.mock('naive-ui', () => ({
  NModal: {
    props: ['show', 'title'],
    emits: ['update:show'],
    template: '<section v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>',
  },
  NForm: { template: '<form><slot /></form>' },
  NFormItem: { props: ['label'], template: '<label><span>{{ label }}</span><slot /></label>' },
  NInput: {
    props: ['value', 'placeholder', 'type'],
    emits: ['update:value'],
    template: '<textarea v-if="type === \'textarea\'" :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" /><input v-else :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NInputNumber: {
    props: ['value', 'clearable'],
    emits: ['update:value'],
    template: '<input type="number" :data-clearable="String(clearable)" :value="value ?? \'\'" @input="$emit(\'update:value\', $event.target.value === \'\' ? null : Number($event.target.value))" />',
  },
  NCheckbox: {
    props: ['checked'],
    emits: ['update:checked'],
    template: '<label><input type="checkbox" :checked="checked" @change="$emit(\'update:checked\', $event.target.checked)" /><slot /></label>',
  },
  NButton: {
    props: ['disabled', 'loading', 'type'],
    emits: ['click'],
    template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
  },
  NSpace: { template: '<div><slot /></div>' },
  NTooltip: { template: '<span><slot /><slot name="trigger" /></span>' },
}))

// stub 子组件：暴露最小接口，让测试能驱动 schedule/deliver/script 值，且不引入 vue-query 依赖。
vi.mock('./ScheduleField.vue', () => ({
  default: {
    props: ['value'],
    emits: ['update:value'],
    template: '<input class="stub-schedule" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
}))
vi.mock('./DeliverField.vue', () => ({
  default: {
    props: ['value', 'appId'],
    emits: ['update:value'],
    template: '<input class="stub-deliver" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
}))
vi.mock('./WorkspaceFilePicker.vue', () => ({
  default: {
    props: ['value', 'appId'],
    emits: ['update:value'],
    template: '<input class="stub-script" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
}))

function mountFormModal(isPlatformAdmin: boolean, job: CronJob | null = null) {
  return mount(CronJobFormModal, {
    props: { show: true, submitting: false, job, isPlatformAdmin, appId: 'app_1' },
  })
}

describe('CronJobFormModal', () => {
  // 组织成员：可见 script/no_agent，不可见高级区（含已下沉的 workdir）。
  it('org member 不再看到 workdir 与高级字段', () => {
    const text = mountFormModal(false).text()
    expect(text).toContain('script')
    expect(text).toContain('no_agent')
    expect(text).not.toContain('workdir')
    expect(text).not.toContain('model')
    expect(text).not.toContain('skills')
  })

  // 平台管理员：高级区可见，workdir 在高级区出现。
  it('platform admin 看到 workdir 与全部高级字段', () => {
    const text = mountFormModal(true).text()
    expect(text).toContain('workdir')
    expect(text).toContain('skills')
    expect(text).toContain('model')
    expect(text).toContain('provider')
    expect(text).toContain('base_url')
  })

  // no_agent 文案改为「不使用 AI，仅运行脚本」。
  it('no_agent 文案更友好', () => {
    expect(mountFormModal(false).text()).toContain('不使用 AI，仅运行脚本')
  })

  // 提交 payload：schedule/deliver/script 来自子组件，字符串 trim，skills 拆分。
  it('submit 组装 payload', async () => {
    const wrapper = mountFormModal(true)

    await wrapper.find('[placeholder="任务名称"]').setValue('  日报  ')
    await wrapper.find('.stub-schedule').setValue('cron 0 9 * * *')
    await wrapper.find('[placeholder="触发时交给 Hermes 的提示词"]').setValue('  汇总  ')
    await wrapper.find('.stub-deliver').setValue('wechat')
    await wrapper.find('.stub-script').setValue('daily.py')
    await wrapper.find('[placeholder="任务运行目录"]').setValue('  /workspace/app  ')
    await wrapper.find('[placeholder="逗号分隔，如 shell,git"]').setValue(' shell, git ')
    await wrapper.find('[placeholder="模型名称"]').setValue('  gpt-5  ')
    await wrapper.find('input[type="checkbox"]').setValue(true)
    await wrapper.find('input[type="number"]').setValue('3')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      name: '日报',
      schedule: 'cron 0 9 * * *',
      prompt: '汇总',
      deliver: 'wechat',
      repeat: 3,
      script: 'daily.py',
      no_agent: true,
      workdir: '/workspace/app',
      skills: ['shell', 'git'],
      model: 'gpt-5',
    })
  })

  // 非平台用户提交不带高级字段（含 workdir，因为它在高级区不渲染）。
  it('非平台 payload 不含高级字段', async () => {
    const wrapper = mountFormModal(false)
    await wrapper.find('[placeholder="任务名称"]').setValue('日报')
    await wrapper.find('.stub-schedule').setValue('cron 0 9 * * *')
    await wrapper.find('.stub-script').setValue('daily.py')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ name: '日报', schedule: 'cron 0 9 * * *', script: 'daily.py' })
    expect(payload).not.toHaveProperty('workdir')
    expect(payload).not.toHaveProperty('skills')
    expect(payload).not.toHaveProperty('model')
  })

  // 编辑模式清空基础可选字段：空字符串保留为清空语义。
  it('编辑清空基础可选字段发送空串', async () => {
    const wrapper = mountFormModal(false, {
      id: 'cron_daily',
      name: '日报',
      schedule: { expr: '0 9 * * *', display: '每天 09:00' },
      prompt: '旧 prompt',
      deliver: 'wechat',
      script: 'old.py',
      no_agent: true,
    })
    await wrapper.find('[placeholder="触发时交给 Hermes 的提示词"]').setValue('   ')
    await wrapper.find('.stub-deliver').setValue('   ')
    await wrapper.find('.stub-script').setValue('   ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      name: '日报', prompt: '', deliver: '', script: '', no_agent: true,
    })
  })

  // 平台管理员编辑清空 skills → clear_skills:true。
  it('编辑清空 skills 转 clear_skills', async () => {
    const wrapper = mountFormModal(true, {
      id: 'cron_daily', name: '日报', schedule: { expr: '0 9 * * *' },
      skills: ['shell', 'git'], model: 'gpt-5',
    })
    await wrapper.find('[placeholder="逗号分隔，如 shell,git"]').setValue('   ')
    await wrapper.find('[placeholder="模型名称"]').setValue('   ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ clear_skills: true, model: '' })
    expect(payload).not.toHaveProperty('skills')
  })

  // 编辑已有 repeat 清空尝试：保留并提交原值。
  it('编辑保留已有 repeat', async () => {
    const wrapper = mountFormModal(false, {
      id: 'cron_daily', name: '日报', schedule: { expr: '0 9 * * *' },
      repeat: { times: 5, completed: 2 },
    })
    await wrapper.find('input[type="number"]').setValue('')
    await nextTick()
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ name: '日报', repeat: 5 })
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm test -- run src/pages/apps/cron/CronJobFormModal.spec.ts`
Expected: FAIL（Modal 还没有 `appId` prop / 还没引入子组件 / workdir 仍在普通区 / no_agent 旧文案）

- [ ] **Step 3: 改 CronJobFormModal.vue 的 `<template>` 与 props**

把 `web/src/pages/apps/cron/CronJobFormModal.vue` 第 9-56 行的 `<n-form>...</n-form>` 整体替换为：

```vue
    <n-form>
      <!-- ① 基础：name 必填 + prompt。 -->
      <n-form-item label="name" required>
        <n-input v-model:value="form.name" placeholder="任务名称" />
      </n-form-item>
      <n-form-item label="prompt">
        <n-input v-model:value="form.prompt" type="textarea" placeholder="触发时交给 Hermes 的提示词" />
      </n-form-item>

      <!-- ② 调度：可视化点选器 + 运行次数上限（原 repeat）。 -->
      <n-form-item label="schedule" required>
        <ScheduleField v-model:value="form.schedule" />
      </n-form-item>
      <n-form-item label="运行次数上限">
        <n-space vertical :size="2" style="width: 100%">
          <n-input-number
            :value="form.repeat"
            :min="1"
            :clearable="!hasExistingRepeat"
            @update:value="onRepeatUpdate"
          />
          <span class="field-hint">留空 = 一直按计划运行；填 N = 运行 N 次后停止</span>
        </n-space>
      </n-form-item>

      <!-- ③ 投递：从已绑定渠道点选。 -->
      <n-form-item label="deliver">
        <DeliverField v-model:value="form.deliver" :app-id="appId" />
      </n-form-item>

      <!-- ④ 执行：脚本点选 + 是否仅跑脚本。 -->
      <n-form-item label="script">
        <WorkspaceFilePicker v-model:value="form.script" :app-id="appId" />
      </n-form-item>
      <n-form-item label="no_agent">
        <n-space align="center" :size="6">
          <n-checkbox v-model:checked="form.no_agent">不使用 AI，仅运行脚本</n-checkbox>
          <n-tooltip>
            <template #trigger><span class="field-help">?</span></template>
            勾选后跳过 AI agent，直接执行 script 指定脚本（更快、不消耗 token），适合纯脚本任务；不勾选则由 AI 按 prompt 执行。
          </n-tooltip>
        </n-space>
      </n-form-item>

      <!-- 平台管理员·高级：workdir 与模型相关字段仅平台管理员可见，后端仍会做最终权限裁剪。 -->
      <template v-if="isPlatformAdmin">
        <n-form-item label="workdir">
          <n-input v-model:value="form.workdir" placeholder="任务运行目录" />
        </n-form-item>
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
```

- [ ] **Step 4: 改 CronJobFormModal.vue 的 imports 与 props**

把第 73-74 行 import 段：

```ts
import { computed, reactive, watch } from 'vue'
import { NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NModal, NSpace } from 'naive-ui'
```

替换为（新增 NTooltip 与三个子组件）：

```ts
import { computed, reactive, watch } from 'vue'
import { NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NModal, NSpace, NTooltip } from 'naive-ui'

import ScheduleField from './ScheduleField.vue'
import DeliverField from './DeliverField.vue'
import WorkspaceFilePicker from './WorkspaceFilePicker.vue'
```

把第 97-109 行 props 定义中新增 `appId`。将：

```ts
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
```

替换为：

```ts
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
```

- [ ] **Step 5: 在 `<style>` 末尾补两个提示样式**

`CronJobFormModal.vue` 当前没有 `<style>` 块。在文件末尾 `</script>` 之后追加：

```vue
<style scoped>
.field-hint { font-size: 12px; color: #999; }
.field-help {
  display: inline-flex; width: 16px; height: 16px; border-radius: 50%;
  align-items: center; justify-content: center; font-size: 12px;
  background: #eee; color: #666; cursor: help;
}
</style>
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npm test -- run src/pages/apps/cron/CronJobFormModal.spec.ts`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add web/src/pages/apps/cron/CronJobFormModal.vue web/src/pages/apps/cron/CronJobFormModal.spec.ts
git commit -m "feat(cron): 表单重构为基础/调度/投递/执行四区块并接入点选子组件

schedule 改 ScheduleField 可视化点选、deliver 改 DeliverField 渠道下拉、script
改 WorkspaceFilePicker 文件点选；repeat 改名「运行次数上限」并加说明；no_agent
文案改「不使用 AI，仅运行脚本」加 tooltip；workdir 下沉到平台管理员高级区。
payload 组装逻辑与后端契约不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: AppCronTab 给 Modal 传 appId

**Files:**
- Modify: `web/src/pages/apps/AppCronTab.vue:60-66`

- [ ] **Step 1: 加 prop**

把 `web/src/pages/apps/AppCronTab.vue` 第 60-66 行：

```vue
    <CronJobFormModal
      v-model:show="showForm"
      :submitting="formSubmitting"
      :job="editingJob"
      :is-platform-admin="canShowAdvancedFields"
      @submit="onSubmitForm"
    />
```

替换为（新增 `:app-id`）：

```vue
    <CronJobFormModal
      v-model:show="showForm"
      :app-id="appId"
      :submitting="formSubmitting"
      :job="editingJob"
      :is-platform-admin="canShowAdvancedFields"
      @submit="onSubmitForm"
    />
```

注意：`appId` 在 `AppCronTab.vue:105` 是 `computed(() => props.appId)`（`ComputedRef<string>`），传给 prop 时 Vue 自动解包，子组件 `appId: string` 类型匹配。

- [ ] **Step 2: typecheck 验证**

Run: `cd web && npm run typecheck`
Expected: 无新增类型错误（关注 CronJobFormModal / AppCronTab 相关报错为 0）

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/apps/AppCronTab.vue
git commit -m "feat(cron): AppCronTab 给表单弹窗透传 appId

deliver 渠道下拉与 script 文件点选子组件需要 appId 查询渠道绑定状态与工作目录。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: 全量验证

- [ ] **Step 1: 跑 cron 目录全部单测**

Run: `cd web && npm test -- run src/pages/apps/cron`
Expected: 全 PASS（含既有 `cronDisplay.spec.ts` / `CronJobList.spec.ts` 不回归）

- [ ] **Step 2: 全量 typecheck**

Run: `cd web && npm run typecheck`
Expected: PASS（无错误）

- [ ] **Step 3: 真实浏览器验证（AGENTS.md 强制）**

按 `CLAUDE.md`「交付前检查」：必须用真实浏览器验证，不能用 curl 替代。本地环境登录见 AGENTS.md（org_member 账号见 `prod-cluster-ops` / 本地 k3d）。逐项核对：
- 新建任务：模式 A 选「每周一三五 + 09:00/18:00」→ 预览显示「周一、周三、周五 09:00、18:00」，提交成功；列表/详情调度文案正确。
- 模式 A 故意设 09:00 + 18:30 → 预览出现告警与 4 个触发点。
- 模式 B「每 10 分钟」、模式 C 手写 `cron 0 0 1 * *` 均能提交。
- deliver 下拉：已绑定 wechat 时可选「微信」；未绑定时只有「不投递」并显示提示。
- script：点「选择文件」能列出工作目录根层文件并回填。
- no_agent：hover `?` 显示解释文案。
- org_member 登录看不到 workdir / 高级字段；平台管理员能看到 workdir 在高级区。
- 编辑已有任务：schedule 正确回填到对应模式；deliver/script 回填不丢值。

若发现问题，先修复并重新验证，直到全部正常。

- [ ] **Step 4: 交付说明**

在交付消息中给出逐文件矩阵与浏览器验证证据（截图/逐项结论），符合用户「验证标准要求」。

---

## Self-Review

- **Spec coverage:** ① 四区块布局 → Task 5；schedule 可视化（三模式+预览+笛卡尔告警）→ Task 1/2；deliver 下拉（仅已绑定+空态提示+编辑保留）→ Task 3；script 文件点选 → Task 4；workdir 下沉高级区 → Task 5；repeat 改名收进调度区 → Task 5；no_agent 文案+tooltip → Task 5；appId 接线 → Task 6；不改后端/OpenAPI → 全程仅动 `web/`。✓ 全覆盖。
- **Placeholder scan:** 无 TBD/TODO；每个 code step 均给出完整代码与可运行命令。✓
- **Type consistency:** `ScheduleState`/`CalendarState`/`TimePoint`/`IntervalState` 在 Task 1 定义，Task 2 一致引用；`buildScheduleExpr`/`parseScheduleExpr`/`describeSchedule`/`defaultScheduleState` 命名贯穿 Task 1-2；`buildDeliverOptions`(Task 3)、`workspaceFileNames`(Task 4) 在对应子组件一致调用；Modal 新增 `appId: string` prop 与 Task 6 传入一致；子组件 `v-model:value` 事件名 `update:value` 全一致。✓
