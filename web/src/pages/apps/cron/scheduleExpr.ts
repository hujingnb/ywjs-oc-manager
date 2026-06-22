// scheduleExpr.ts —— 定时任务调度点选器的纯逻辑层。
// 收口 cron / every 表达式的拼装、回填解析与人类可读预览，UI 层只消费这些纯函数，
// 不感知 cron 语法。所有解析失败一律安全降级，绝不抛错（与 cronDisplay 的翻译原则一致）。
// describeSchedule 接受 t 参数以支持 i18n，调用方在 computed 中传入保证响应性。
import { translateCronExpr } from './cronDisplay'

// TFunc 是 vue-i18n t 函数的精简签名，与 cronDisplay 保持一致。
type TFunc = (key: string, params?: Record<string, string | number>) => string

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

// WEEKDAY_KEYS 以 cron dow 数值为索引（0=周日），映射到 i18n key；与 cronDisplay 同约定，本模块自带一份保持自包含。
const WEEKDAY_KEYS = [
  'apps.cron.display.weekday.sun', // 0 = 周日
  'apps.cron.display.weekday.mon', // 1 = 周一
  'apps.cron.display.weekday.tue', // 2 = 周二
  'apps.cron.display.weekday.wed', // 3 = 周三
  'apps.cron.display.weekday.thu', // 4 = 周四
  'apps.cron.display.weekday.fri', // 5 = 周五
  'apps.cron.display.weekday.sat', // 6 = 周六
]

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
// t 为 vue-i18n 翻译函数，调用方在 computed 中传入以保证语言切换时响应式更新。
export function describeSchedule(state: ScheduleState, t: TFunc): { text: string; warn: boolean } {
  if (state.mode === 'interval') {
    // 间隔模式：根据单位翻译
    const unitKey = state.interval.unit === 'h'
      ? 'apps.cron.display.schedule.unitHour'
      : 'apps.cron.display.schedule.unitMinute'
    const unit = t(unitKey)
    return {
      text: t('apps.cron.display.schedule.calendarInterval', { value: state.interval.value, unit }),
      warn: false,
    }
  }
  if (state.mode === 'expr') {
    return { text: translateCronExpr(undefined, state.expr, t), warn: false }
  }
  const { frequency, weekdays, times } = state.calendar
  if (times.length === 0) return { text: '', warn: false }
  const minutes = uniqSortNums(times.map((tp) => tp.minute))
  const hours = uniqSortNums(times.map((tp) => tp.hour))
  // 按小时升序、再分钟升序枚举所有触发点。
  const combos = hours.flatMap((h) => minutes.map((m) => `${pad2(h)}:${pad2(m)}`))
  const selectedCount = new Set(times.map((tp) => `${tp.hour}:${tp.minute}`)).size
  const timesStr = combos.join('、')

  if (frequency === 'daily' || weekdays.length === 0) {
    // 每天：使用 calendarDaily 模板
    return {
      text: t('apps.cron.display.schedule.calendarDaily', { times: timesStr }),
      warn: combos.length > selectedCount,
    }
  }
  // 每周：把 weekday 列表翻译为本地化星期名再连接
  const days = uniqSortNums(weekdays).map((d) => t(WEEKDAY_KEYS[d % 7])).join('、')
  return {
    text: t('apps.cron.display.schedule.calendarWeekly', { days, times: timesStr }),
    warn: combos.length > selectedCount,
  }
}
