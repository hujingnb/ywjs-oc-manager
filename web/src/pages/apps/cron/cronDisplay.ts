// cronDisplay.ts —— 定时任务页面共享展示工具：状态/投递 i18n 化与调度兜底翻译。
// 收口所有面向用户的文案转换，供列表、详情、筛选下拉复用，避免文案散落多处。
// 翻译原则：尽力翻译，识别不了一律回退原文或占位符，绝不抛错或显示空白。
// 所有面向用户的输出函数接受 t（vue-i18n 翻译函数），调用方在 computed 中传入以保证响应性。
import type { CronJob, CronSchedule } from '@/api/hooks/useCron'

// TFunc 是 vue-i18n t 函数的精简签名，避免引入 vue-i18n 类型依赖。
// 支持无参数调用（纯 key）和带插值参数调用（key + params 对象）。
type TFunc = (key: string, params?: Record<string, string | number>) => string

// WEEKDAY_KEY_MAP 把 cron dow 数值映射到 i18n weekday key；索引 0 与 7 均为周日。
const WEEKDAY_KEY_MAP = [
  'apps.cron.display.weekday.sun', // 0 = 周日
  'apps.cron.display.weekday.mon', // 1 = 周一
  'apps.cron.display.weekday.tue', // 2 = 周二
  'apps.cron.display.weekday.wed', // 3 = 周三
  'apps.cron.display.weekday.thu', // 4 = 周四
  'apps.cron.display.weekday.fri', // 5 = 周五
  'apps.cron.display.weekday.sat', // 6 = 周六
]

// pad2 把小时/分钟补齐两位，保证 HH:MM 展示稳定。
function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

// translateState 返回状态 i18n 标签；空值回退 'unknown'（与列表旧文案一致），未知值原样返回。
export function translateState(state: string | undefined, t: TFunc): string {
  if (!state) return t('apps.cron.display.state.unknown')
  const key = `apps.cron.display.state.${state}`
  const translated = t(key)
  // 若 t 返回了 key 本身（未命中），则说明是未知状态，回退原值。
  return translated === key ? state : translated
}

// translateDeliver 返回投递渠道 i18n 标签；空值回退占位符 '—'，未知值原样返回。
export function translateDeliver(deliver: string | undefined, t: TFunc): string {
  if (!deliver) return t('apps.cron.display.deliver.empty')
  const key = `apps.cron.display.deliver.${deliver}`
  const translated = t(key)
  // 未命中则回退原值，保证未来新渠道不被吞掉。
  return translated === key ? deliver : translated
}

// translateCronExpr 尽力把调度表达式 i18n 化；无法识别时返回原始 expr，绝不抛错。
// kind 仅用于辅助判断 at 类型，主要解析依据是 expr 本身的格式。
export function translateCronExpr(kind: string | undefined, expr: string | undefined, t: TFunc): string {
  const raw = (expr ?? '').trim()
  if (!raw) return ''

  // every / interval 格式：'every 10m' / '10m' / 'every 2h' / 'every 30s'，
  // kind 可能是 every / interval 也可能为空。实测 interval 任务的表达式只放在 display。
  const everyMatch = raw.match(/^(?:every\s+)?(\d+)\s*([smh])$/i)
  if (everyMatch) {
    const value = everyMatch[1]
    const unit = everyMatch[2].toLowerCase()
    if (unit === 'h') return t('apps.cron.display.schedule.everyHour', { value })
    if (unit === 's') return t('apps.cron.display.schedule.everySecond', { value })
    return t('apps.cron.display.schedule.everyMinute', { value })
  }

  // at 格式：一次性绝对时间，保留原始时间串，仅加 i18n 前缀。
  if (kind === 'at' || /^at\s+/i.test(raw)) {
    const at = raw.replace(/^at\s+/i, '')
    return t('apps.cron.display.schedule.atTime', { time: at })
  }

  // 标准 5 段 cron：分 时 日 月 周；允许前缀 'cron '。
  const parts = raw.replace(/^cron\s+/i, '').split(/\s+/)
  if (parts.length === 5) {
    const [min, hour, dom, mon, dow] = parts
    const isNum = (s: string) => /^\d+$/.test(s)
    const allStar = dom === '*' && mon === '*' && dow === '*'

    // 每 N 分钟：*/N * * * *
    const everyMin = min.match(/^\*\/(\d+)$/)
    if (everyMin && hour === '*' && allStar) return t('apps.cron.display.schedule.everyNMinutes', { n: everyMin[1] })

    // 每小时：分钟固定、小时通配，其余通配。
    if (isNum(min) && hour === '*' && allStar) return t('apps.cron.display.schedule.everyHourFixed')

    // 具体时刻 HH:MM 的几种周期。
    if (isNum(min) && isNum(hour)) {
      const time = `${pad2(Number(hour))}:${pad2(Number(min))}`
      // 每天：m h * * *
      if (allStar) return t('apps.cron.display.schedule.everyDay', { time })
      // 每周X：m h * * D
      if (dom === '*' && mon === '*' && isNum(dow)) {
        const dayKey = WEEKDAY_KEY_MAP[Number(dow) % 7]
        const day = t(dayKey)
        return t('apps.cron.display.schedule.everyWeekday', { day, time })
      }
      // 每月D日：m h D * *
      if (isNum(dom) && mon === '*' && dow === '*') {
        return t('apps.cron.display.schedule.everyMonthDay', { day: String(Number(dom)), time })
      }
    }
  }

  // 兜底：识别不了就返回原文。
  return raw
}

// scheduleDisplay 是页面统一入口，目标是「尽量给用户可读的 i18n 化文案」，采用「翻译优先」策略。
// 关键约束（实测 oc-cron 行为）：display 并不可靠——cron 任务的 display 只是回显原始
// expr（如 "0 9 * * *"）；interval 任务则没有 expr，表达式以英文形式（如 "every 10m"）
// 放在 display 里。两种情况 display 都不是人类可读中文。因此：先把真实表达式（expr 优先，
// 否则取 display）交给前端翻译器；只要翻出了与原串不同的内容就用它，翻不动时才回退到
// 上游 display（可能是更友好的描述）或原始表达式，最后才是占位符。
export function scheduleDisplay(schedule: CronSchedule | undefined, t: TFunc): string {
  if (!schedule) return '—'
  const { kind, expr, display } = schedule
  // 真实表达式可能在 expr，也可能（interval 类型）只放在 display 里，取第一个非空。
  const rawExpr = expr || display || ''
  const translated = translateCronExpr(kind, rawExpr, t)
  // 翻译确有产出（与原始串不同）就用翻译结果——这是用户可读的关键。
  if (translated && translated !== rawExpr) return translated
  // 翻不动：上游 display 若是更友好的描述就用它，否则退原始表达式，最后占位符。
  return display || rawExpr || '—'
}

// filterCronJobs 在前端对已拉取的任务列表做筛选。
// 关键约束（实测后端行为）：列表接口 handler 只读 all 参数，既不实现 status 状态筛选
// 也不实现 q 文本搜索，故两者都必须在前端完成：
// - status 非空时按 job.state 精确匹配（已调度/已暂停/运行中/已禁用/错误）；
// - q 非空时按任务名 + prompt 子串不区分大小写匹配；
// 两个条件是 AND 关系，均满足才保留。
export function filterCronJobs(jobs: CronJob[], q: string, status: string): CronJob[] {
  const keyword = q.trim().toLowerCase()
  return jobs.filter((job) => {
    if (status && job.state !== status) return false
    if (keyword) {
      const haystack = `${job.name ?? ''} ${job.prompt ?? ''}`.toLowerCase()
      if (!haystack.includes(keyword)) return false
    }
    return true
  })
}
