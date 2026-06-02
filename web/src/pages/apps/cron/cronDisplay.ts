// cronDisplay.ts —— 定时任务页面共享展示工具：状态/投递中文化与调度兜底翻译。
// 收口所有面向用户的文案转换，供列表、详情、筛选下拉复用，避免文案散落多处。
// 翻译原则：尽力翻译，识别不了一律回退原文或占位符，绝不抛错或显示空白。
import type { CronSchedule } from '@/api/hooks/useCron'

// STATE_LABELS 把 oc-cron 状态机的英文状态映射到中文标签；未列出状态走原样兜底。
const STATE_LABELS: Record<string, string> = {
  scheduled: '已调度',
  paused: '已暂停',
  running: '运行中',
  disabled: '已禁用',
  error: '错误',
  removed: '已移除',
}

// translateState 返回状态中文标签；空值回退 'unknown'（与列表旧文案一致），未知值原样返回。
export function translateState(state?: string): string {
  if (!state) return 'unknown'
  return STATE_LABELS[state] ?? state
}

// DELIVER_LABELS 把投递渠道英文枚举映射到中文。
const DELIVER_LABELS: Record<string, string> = {
  wechat: '微信',
  email: '邮件',
  none: '不投递',
}

// translateDeliver 返回投递渠道中文；空值回退占位符 '—'，未知值原样返回。
export function translateDeliver(deliver?: string): string {
  if (!deliver) return '—'
  return DELIVER_LABELS[deliver] ?? deliver
}

// WEEKDAY_LABELS 以 dow 数值为索引；标准 cron 中 0 与 7 都代表周日，故对 7 取模归到 0。
const WEEKDAY_LABELS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

// pad2 把小时/分钟补齐两位，保证 HH:MM 展示稳定。
function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

// translateCronExpr 尽力把调度表达式翻成中文；无法识别时返回原始 expr，绝不抛错。
// kind 仅用于辅助判断 at 类型，主要解析依据是 expr 本身的格式。
export function translateCronExpr(kind?: string, expr?: string): string {
  const raw = (expr ?? '').trim()
  if (!raw) return ''

  // every / interval 格式：'every 10m' / '10m' / 'every 2h' / 'every 30s'，
  // kind 可能是 every / interval 也可能为空。实测 interval 任务的表达式只放在 display。
  const everyMatch = raw.match(/^(?:every\s+)?(\d+)\s*([smh])$/i)
  if (everyMatch) {
    const value = everyMatch[1]
    const unit = everyMatch[2].toLowerCase()
    const label = unit === 'h' ? '小时' : unit === 's' ? '秒' : '分钟'
    return `每 ${value} ${label}`
  }

  // at 格式：一次性绝对时间，保留原始时间串，仅加中文前缀。
  if (kind === 'at' || /^at\s+/i.test(raw)) {
    const at = raw.replace(/^at\s+/i, '')
    return `指定时间 ${at}`
  }

  // 标准 5 段 cron：分 时 日 月 周；允许前缀 'cron '。
  const parts = raw.replace(/^cron\s+/i, '').split(/\s+/)
  if (parts.length === 5) {
    const [min, hour, dom, mon, dow] = parts
    const isNum = (s: string) => /^\d+$/.test(s)
    const allStar = dom === '*' && mon === '*' && dow === '*'

    // 每 N 分钟：*/N * * * *
    const everyMin = min.match(/^\*\/(\d+)$/)
    if (everyMin && hour === '*' && allStar) return `每 ${everyMin[1]} 分钟`

    // 每小时：分钟固定、小时通配，其余通配。
    if (isNum(min) && hour === '*' && allStar) return '每小时'

    // 具体时刻 HH:MM 的几种周期。
    if (isNum(min) && isNum(hour)) {
      const time = `${pad2(Number(hour))}:${pad2(Number(min))}`
      // 每天：m h * * *
      if (allStar) return `每天 ${time}`
      // 每周X：m h * * D
      if (dom === '*' && mon === '*' && isNum(dow)) return `每${WEEKDAY_LABELS[Number(dow) % 7]} ${time}`
      // 每月D日：m h D * *
      if (isNum(dom) && mon === '*' && dow === '*') return `每月${Number(dom)}日 ${time}`
    }
  }

  // 兜底：识别不了就返回原文。
  return raw
}

// scheduleDisplay 是页面统一入口，目标是「尽量给用户可读的中文」，采用「翻译优先」策略。
// 关键约束（实测 oc-cron 行为）：display 并不可靠——cron 任务的 display 只是回显原始
// expr（如 "0 9 * * *"）；interval 任务则没有 expr，表达式以英文形式（如 "every 10m"）
// 放在 display 里。两种情况 display 都不是人类可读中文。因此：先把真实表达式（expr 优先，
// 否则取 display）交给前端翻译器；只要翻出了与原串不同的中文就用它，翻不动时才回退到
// 上游 display（可能是更友好的描述）或原始表达式，最后才是占位符。
export function scheduleDisplay(schedule?: CronSchedule): string {
  if (!schedule) return '—'
  const { kind, expr, display } = schedule
  // 真实表达式可能在 expr，也可能（interval 类型）只放在 display 里，取第一个非空。
  const rawExpr = expr || display || ''
  const translated = translateCronExpr(kind, rawExpr)
  // 翻译确有产出（与原始串不同）就用翻译结果——这是用户可读的关键。
  if (translated && translated !== rawExpr) return translated
  // 翻不动：上游 display 若是更友好的描述就用它，否则退原始表达式，最后占位符。
  return display || rawExpr || '—'
}
