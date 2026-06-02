# 定时任务页面样式与文案优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把实例 Hermes Cron 管理页左侧列表改为字段完整的卡片式布局，并让调度、状态、投递在前端统一中文化可读。

**Architecture:** 新增一个无副作用的共享展示工具 `cronDisplay.ts` 收口所有翻译逻辑（状态 / 投递映射、调度兜底翻译），列表 / 详情 / 筛选下拉复用它；列表组件从 5 列定宽表格重构为多行卡片，消除截断。纯前端改动，不触碰后端、OpenAPI 契约与数据库。

**Tech Stack:** Vue 3 `<script setup>` + TypeScript、Naive UI、Vitest + @vue/test-utils。所有命令在 `web/` 目录下执行。

**Spec:** `docs/superpowers/specs/2026-06-02-cron-page-style-i18n-design.md`

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `web/src/pages/apps/cron/cronDisplay.ts` | 共享纯函数：状态 / 投递中文化、调度兜底翻译、统一调度展示入口 | 新增 |
| `web/src/pages/apps/cron/cronDisplay.spec.ts` | 上述纯函数单测 | 新增 |
| `web/src/pages/apps/cron/CronJobList.vue` | 左侧任务列表，由表格重构为卡片式 | 修改 |
| `web/src/pages/apps/cron/CronJobList.spec.ts` | 卡片渲染、中文状态、翻译后调度断言 | 新增 |
| `web/src/pages/apps/cron/CronJobDetail.vue` | 右侧详情：状态行 + 调度文案一致化 | 修改 |
| `web/src/pages/apps/AppCronTab.vue` | 工具栏状态筛选下拉 label 中文化 | 修改 |

> 类型 `CronSchedule` / `CronJob` 已存在于 `web/src/api/hooks/useCron.ts`，本计划不改动它们，仅 import 使用。

---

## Task 1: 共享展示工具 `cronDisplay.ts`（TDD）

**Files:**
- Create: `web/src/pages/apps/cron/cronDisplay.ts`
- Test: `web/src/pages/apps/cron/cronDisplay.spec.ts`

- [ ] **Step 1: 写失败测试**

创建 `web/src/pages/apps/cron/cronDisplay.spec.ts`：

```ts
// cronDisplay.spec.ts —— 定时任务页面共享展示工具单元测试。
// 覆盖：状态/投递中文映射与兜底、cron/every/at 翻译、display 优先级与原文回退。
import { describe, expect, it } from 'vitest'

import {
  scheduleDisplay,
  translateCronExpr,
  translateDeliver,
  translateState,
} from './cronDisplay'

describe('translateState', () => {
  // 已知状态返回对应中文标签
  it('把已知状态映射为中文', () => {
    expect(translateState('scheduled')).toBe('已调度')
    expect(translateState('paused')).toBe('已暂停')
    expect(translateState('running')).toBe('运行中')
    expect(translateState('disabled')).toBe('已禁用')
    expect(translateState('error')).toBe('错误')
    expect(translateState('removed')).toBe('已移除')
  })
  // 空值回退 unknown，与列表旧文案保持一致，避免出现空白标签
  it('空值回退 unknown', () => {
    expect(translateState(undefined)).toBe('unknown')
    expect(translateState('')).toBe('unknown')
  })
  // 未知状态原样返回，保证未来上游新增状态不被吞掉
  it('未知状态原样返回', () => {
    expect(translateState('weird')).toBe('weird')
  })
})

describe('translateDeliver', () => {
  // 已知投递渠道返回中文
  it('把投递渠道映射为中文', () => {
    expect(translateDeliver('wechat')).toBe('微信')
    expect(translateDeliver('email')).toBe('邮件')
    expect(translateDeliver('none')).toBe('不投递')
  })
  // 空值回退占位符
  it('空值回退 —', () => {
    expect(translateDeliver(undefined)).toBe('—')
    expect(translateDeliver('')).toBe('—')
  })
  // 未知渠道原样返回
  it('未知渠道原样返回', () => {
    expect(translateDeliver('sms')).toBe('sms')
  })
})

describe('translateCronExpr', () => {
  // 标准 5 段 cron：每天固定时刻
  it('每天固定时刻', () => {
    expect(translateCronExpr('cron', '0 9 * * *')).toBe('每天 09:00')
    expect(translateCronExpr('cron', '30 18 * * *')).toBe('每天 18:30')
  })
  // 每周某天固定时刻，dow 0 与 7 都代表周日
  it('每周某天固定时刻', () => {
    expect(translateCronExpr('cron', '0 10 * * 1')).toBe('每周一 10:00')
    expect(translateCronExpr('cron', '0 10 * * 0')).toBe('每周日 10:00')
    expect(translateCronExpr('cron', '0 10 * * 7')).toBe('每周日 10:00')
  })
  // 每月某日固定时刻
  it('每月某日固定时刻', () => {
    expect(translateCronExpr('cron', '0 8 15 * *')).toBe('每月15日 08:00')
  })
  // 每小时（分钟固定、小时通配）
  it('每小时', () => {
    expect(translateCronExpr('cron', '0 * * * *')).toBe('每小时')
  })
  // 每 N 分钟：*/N 步进
  it('每 N 分钟（步进）', () => {
    expect(translateCronExpr('cron', '*/5 * * * *')).toBe('每 5 分钟')
  })
  // every 格式：分钟与小时
  it('every 格式', () => {
    expect(translateCronExpr('every', 'every 10m')).toBe('每 10 分钟')
    expect(translateCronExpr('every', '10m')).toBe('每 10 分钟')
    expect(translateCronExpr('every', 'every 2h')).toBe('每 2 小时')
  })
  // at 格式：一次性绝对时间，保留原始时间串
  it('at 格式保留原始时间', () => {
    expect(translateCronExpr('at', 'at 2026-06-03 09:00')).toBe('指定时间 2026-06-03 09:00')
  })
  // 不可识别的复杂表达式回退原文，且不抛错
  it('无法识别时回退原文', () => {
    expect(translateCronExpr('cron', '0 9 1-5 * 1,3,5')).toBe('0 9 1-5 * 1,3,5')
    expect(translateCronExpr('', '')).toBe('')
  })
})

describe('scheduleDisplay', () => {
  // display 非空时优先使用上游文案，不触发前端翻译
  it('优先使用上游 display', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * *', display: '上游文案' })).toBe('上游文案')
  })
  // display 缺失时走前端兜底翻译
  it('display 缺失走兜底翻译', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * *' })).toBe('每天 09:00')
  })
  // 全部缺失返回占位符
  it('全部缺失返回 —', () => {
    expect(scheduleDisplay(undefined)).toBe('—')
    expect(scheduleDisplay({})).toBe('—')
  })
})
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `npx vitest run src/pages/apps/cron/cronDisplay.spec.ts`
Expected: FAIL —— 无法解析模块 `./cronDisplay`（文件尚未创建）。

- [ ] **Step 3: 写最小实现**

创建 `web/src/pages/apps/cron/cronDisplay.ts`：

```ts
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

  // every 格式：'every 10m' / '10m' / 'every 2h'，kind 可能是 every 也可能为空。
  const everyMatch = raw.match(/^(?:every\s+)?(\d+)\s*([mh])$/i)
  if (everyMatch) {
    const value = everyMatch[1]
    return everyMatch[2].toLowerCase() === 'h' ? `每 ${value} 小时` : `每 ${value} 分钟`
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

// scheduleDisplay 是页面统一入口：优先上游 display，其次前端兜底翻译，再退原文，最后占位符。
export function scheduleDisplay(schedule?: CronSchedule): string {
  if (schedule?.display) return schedule.display
  const translated = translateCronExpr(schedule?.kind, schedule?.expr)
  return translated || '—'
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `npx vitest run src/pages/apps/cron/cronDisplay.spec.ts`
Expected: PASS —— 全部用例通过。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/apps/cron/cronDisplay.ts web/src/pages/apps/cron/cronDisplay.spec.ts
git commit -m "feat(cron): 新增调度/状态/投递前端展示翻译工具

收口定时任务页面面向用户的文案转换：状态与投递渠道中文映射、
调度表达式在上游 display 缺失时的前端兜底翻译（覆盖标准 cron、
every、at 常见格式，无法识别回退原文），供列表/详情/筛选下拉复用。"
```

---

## Task 2: 左侧列表 `CronJobList.vue` 卡片化

**Files:**
- Modify: `web/src/pages/apps/cron/CronJobList.vue`（整文件重写 template/script/style）

- [ ] **Step 1: 重写组件为卡片式**

把 `web/src/pages/apps/cron/CronJobList.vue` 全文替换为：

```vue
<template>
  <n-card :bordered="true" content-style="padding: 0">
    <!-- 空列表保留最小高度，避免加载完成后左右分屏高度明显跳动。 -->
    <n-empty v-if="jobs.length === 0" class="list-empty" description="暂无定时任务" />
    <div v-else class="card-list">
      <div
        v-for="job in jobs"
        :key="job.id ?? job.name"
        class="job-card"
        :class="{ selected: job.id === selectedId }"
        @click="onSelect(job)"
      >
        <!-- 第一行：任务名称 + 中文状态标签，名称过长才省略，状态始终完整。 -->
        <div class="card-head">
          <span class="job-name">{{ job.name || '未命名任务' }}</span>
          <n-tag size="small" :type="stateTagType(job.state)">{{ translateState(job.state) }}</n-tag>
        </div>
        <!-- 次要灰色小字展示 job_id，便于排查。 -->
        <code class="job-id">{{ job.id || '—' }}</code>
        <!-- 调度走统一展示入口：上游 display 优先，缺失时前端兜底翻译。 -->
        <div class="card-row">
          <span class="k">调度</span>
          <span class="v">{{ scheduleDisplay(job.schedule) }}</span>
        </div>
        <!-- 下次执行与投递渠道同行展示，投递中文化。 -->
        <div class="card-row">
          <span class="k">下次</span>
          <span class="v">{{ formatTime(job.next_run_at) }} · {{ translateDeliver(job.deliver) }}</span>
        </div>
      </div>
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { NCard, NEmpty, NTag } from 'naive-ui'

import type { CronJob } from '@/api/hooks/useCron'
import { scheduleDisplay, translateDeliver, translateState } from './cronDisplay'

// CronJobList 渲染 Cron 任务左侧卡片列表；选择态只改变背景和左侧色条，不改变卡片结构。
const props = defineProps<{
  // jobs 是父组件已按搜索 / 状态筛选后的列表。
  jobs: CronJob[]
  // selectedId 来自 URL query.job，用于刷新后恢复选择态。
  selectedId?: string
}>()

const emit = defineEmits<{
  // select 只向上传递后端任务 ID；缺少 ID 的异常行不可选。
  select: [jobId: string]
}>()

// formatTime 仅负责 UI 兜底；后端保留原始 ISO 字符串，页面不做时区转换。
function formatTime(value: string | undefined): string {
  return value || '—'
}

// stateTagType 把常见 Cron 状态映射到 Naive UI 语义色，未知状态保持默认色。
function stateTagType(state: string | undefined): 'success' | 'warning' | 'error' | 'default' {
  if (state === 'scheduled' || state === 'running') return 'success'
  if (state === 'paused' || state === 'disabled') return 'warning'
  if (state === 'removed' || state === 'error') return 'error'
  return 'default'
}

// onSelect 防御旧数据缺少 id 的情况，避免把空 job 写入 URL query。
function onSelect(job: CronJob) {
  if (!job.id) return
  emit('select', job.id)
}
</script>

<style scoped>
.list-empty {
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.card-list {
  display: flex;
  flex-direction: column;
}
.job-card {
  display: grid;
  gap: 4px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: background 0.15s, border-color 0.15s;
}
.job-card:last-child {
  border-bottom: none;
}
.job-card:hover {
  background: var(--color-surface-muted, #fbfcfd);
}
.job-card.selected {
  background: var(--color-brand-soft);
  border-left-color: var(--color-brand);
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.job-name {
  font-weight: 600;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.job-id {
  color: var(--color-text-secondary, #6b7280);
  font-size: 11px;
}
.card-row {
  display: flex;
  gap: 8px;
  font-size: 12px;
  line-height: 1.5;
}
.card-row .k {
  color: var(--color-text-secondary, #6b7280);
  flex: 0 0 28px;
}
.card-row .v {
  color: var(--color-text-primary, #1f2329);
  word-break: break-all;
}
</style>
```

- [ ] **Step 2: 类型检查**

Run: `npx vue-tsc --noEmit`
Expected: 无与 `CronJobList.vue` / `cronDisplay.ts` 相关的类型错误。

> 注：若仓库历史已有无关类型告警，只需确认本次改动未引入新错误即可。

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/apps/cron/CronJobList.vue
git commit -m "feat(cron): 定时任务左侧列表改为卡片式布局

将固定 420px 宽的 5 列表格重构为多行卡片，名称/调度/状态/下次执行/
投递分行完整展示，消除原 nowrap+省略号导致的字段截断；状态与投递
中文化，调度复用统一展示入口，保留 hover 与选中态。"
```

---

## Task 3: 列表卡片渲染测试 `CronJobList.spec.ts`

**Files:**
- Create: `web/src/pages/apps/cron/CronJobList.spec.ts`

- [ ] **Step 1: 写测试**

创建 `web/src/pages/apps/cron/CronJobList.spec.ts`：

```ts
// CronJobList.spec.ts —— Cron 任务左侧卡片列表单元测试。
// 覆盖：卡片渲染中文状态与翻译后调度、空列表占位、缺 id 行不可选、选中态。
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { CronJob } from '@/api/hooks/useCron'
import CronJobList from './CronJobList.vue'

// mock naive-ui：聚焦卡片文案与点击行为，避免组件库渲染细节干扰查询。
vi.mock('naive-ui', () => ({
  NCard: { template: '<div><slot /></div>' },
  NEmpty: { props: ['description'], template: '<div class="empty">{{ description }}</div>' },
  NTag: { props: ['type'], template: '<span class="tag"><slot /></span>' },
}))

function mountList(jobs: CronJob[], selectedId?: string) {
  return mount(CronJobList, { props: { jobs, selectedId } })
}

describe('CronJobList', () => {
  // 卡片应展示中文状态与翻译后的调度文案，而非英文原文 / 原始 cron 表达式
  it('渲染中文状态与翻译后调度', () => {
    const wrapper = mountList([
      { id: 'job-1', name: '每日报表', state: 'scheduled', deliver: 'wechat', schedule: { kind: 'cron', expr: '0 9 * * *' } },
    ])
    const text = wrapper.text()
    expect(text).toContain('每日报表')
    expect(text).toContain('已调度')
    expect(text).toContain('每天 09:00')
    expect(text).toContain('微信')
    // 不应再出现原始 cron 表达式
    expect(text).not.toContain('0 9 * * *')
  })

  // 空列表渲染占位文案
  it('空列表渲染占位', () => {
    const wrapper = mountList([])
    expect(wrapper.text()).toContain('暂无定时任务')
  })

  // 点击有 id 的卡片向上 emit 该 id
  it('点击卡片 emit 任务 id', async () => {
    const wrapper = mountList([{ id: 'job-1', name: 'A', state: 'scheduled' }])
    await wrapper.find('.job-card').trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual(['job-1'])
  })

  // 缺 id 的异常行点击不 emit，避免把空 job 写入 URL
  it('缺 id 行点击不 emit', async () => {
    const wrapper = mountList([{ name: '无 id 任务', state: 'scheduled' }])
    await wrapper.find('.job-card').trigger('click')
    expect(wrapper.emitted('select')).toBeUndefined()
  })

  // selectedId 命中的卡片带 selected 类
  it('选中卡片带 selected 类', () => {
    const wrapper = mountList([{ id: 'job-1', name: 'A', state: 'scheduled' }], 'job-1')
    expect(wrapper.find('.job-card').classes()).toContain('selected')
  })
})
```

- [ ] **Step 2: 运行测试，确认通过**

Run: `npx vitest run src/pages/apps/cron/CronJobList.spec.ts`
Expected: PASS —— 全部用例通过。

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/apps/cron/CronJobList.spec.ts
git commit -m "test(cron): 覆盖定时任务卡片列表渲染与交互

断言卡片展示中文状态与翻译后调度文案、空列表占位、缺 id 行不可选、
选中态类名。"
```

---

## Task 4: 右侧详情 `CronJobDetail.vue` 一致化

**Files:**
- Modify: `web/src/pages/apps/cron/CronJobDetail.vue:11`（状态行）、`:119-120`（scheduleText）、import 区

- [ ] **Step 1: 引入共享工具**

在 `web/src/pages/apps/cron/CronJobDetail.vue` 的 import 区（现有 `import CronRunHistory from './CronRunHistory.vue'` 之后）新增：

```ts
import { scheduleDisplay, translateState } from './cronDisplay'
```

- [ ] **Step 2: 状态行中文化**

把第 11 行：

```html
<p class="status-line">● {{ job.state || 'unknown' }}</p>
```

改为：

```html
<p class="status-line">● {{ translateState(job.state) }}</p>
```

- [ ] **Step 3: scheduleText 走统一入口**

把第 119-120 行：

```ts
// scheduleText 优先用后端面向人的 display，缺失时退回表达式。
const scheduleText = computed(() => props.job?.schedule?.display || props.job?.schedule?.expr || '—')
```

改为：

```ts
// scheduleText 走统一展示入口：上游 display 优先，缺失时前端兜底翻译，再退原文。
const scheduleText = computed(() => scheduleDisplay(props.job?.schedule))
```

> 「基础字段」技术区的英文 key 与原始 value 保持不动（面向管理员的原始字段视图）。

- [ ] **Step 4: 类型检查**

Run: `npx vue-tsc --noEmit`
Expected: 无与 `CronJobDetail.vue` 相关的新类型错误。

- [ ] **Step 5: 提交**

```bash
git add web/src/pages/apps/cron/CronJobDetail.vue
git commit -m "feat(cron): 详情面板状态行与调度文案中文化一致

详情顶部状态行改用 translateState 输出中文；schedule 文案改用统一
scheduleDisplay 入口，使前端兜底翻译与列表一致。基础字段技术区保留
英文原始 key 不变。"
```

---

## Task 5: 工具栏状态筛选下拉中文化

**Files:**
- Modify: `web/src/pages/apps/AppCronTab.vue:163-170`（`statusOptions`）

- [ ] **Step 1: 翻译下拉 label**

把 `web/src/pages/apps/AppCronTab.vue` 第 163-170 行：

```ts
const statusOptions = [
  { label: '全部状态', value: '' },
  { label: 'scheduled', value: 'scheduled' },
  { label: 'paused', value: 'paused' },
  { label: 'running', value: 'running' },
  { label: 'disabled', value: 'disabled' },
  { label: 'error', value: 'error' },
]
```

改为（label 中文化，value 保持后端识别的英文不变）：

```ts
// statusOptions 的 label 中文化便于用户识别，value 仍为后端识别的英文枚举。
const statusOptions = [
  { label: '全部状态', value: '' },
  { label: '已调度', value: 'scheduled' },
  { label: '已暂停', value: 'paused' },
  { label: '运行中', value: 'running' },
  { label: '已禁用', value: 'disabled' },
  { label: '错误', value: 'error' },
]
```

> `statusSummary` 顶部摘要按现有注释保留英文 `Gateway cron running …`，本任务不动。

- [ ] **Step 2: 类型检查**

Run: `npx vue-tsc --noEmit`
Expected: 无新类型错误。

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/apps/AppCronTab.vue
git commit -m "feat(cron): 状态筛选下拉 label 中文化

工具栏状态筛选项 label 改为中文（已调度/已暂停等），value 仍保持
后端识别的英文枚举；顶部英文运行摘要按产品要求保留。"
```

---

## Task 6: 整体验证

**Files:** 无（验证任务）

- [ ] **Step 1: 跑 cron 相关全部单测**

Run: `npx vitest run src/pages/apps/cron/ src/pages/apps/AppCronTab.spec.ts`
Expected: PASS —— 新增的 `cronDisplay.spec.ts`、`CronJobList.spec.ts` 与既有 `CronJobFormModal.spec.ts`、`AppCronTab.spec.ts` 全部通过，无回归。

- [ ] **Step 2: 全量类型检查**

Run: `npx vue-tsc --noEmit`
Expected: 无本次改动引入的新类型错误。

- [ ] **Step 3: 真实浏览器验证（本地 k3d）**

前置：本地 k3d 环境已起（`make local-up`），用 AGENTS.md 本地账号登录 manager 后台，进入某实例的「定时任务」标签页。逐项确认（建议截图留证）：

1. 左侧列表每个任务的名称、调度、状态、下次执行、投递都完整展示，无截断。
2. 调度展示为中文可读（如「每天 09:00」）；对 `display` 缺失的任务也显示中文翻译或原文兜底，不报错、不空白。
3. 状态标签、投递均为中文（已调度 / 微信 等）。
4. 工具栏状态筛选下拉为中文选项，筛选行为正常（切换后列表按状态过滤）。
5. 点击任务，右侧详情顶部状态行为中文、调度文案与左侧一致；「基础字段」技术区仍为英文 key。
6. 选中态（左侧色条 + 背景）、hover、空列表占位正常。

Expected: 全部满足；若发现问题，先修复并重新验证，直到正常再交付。

- [ ] **Step 4: 终检**

确认工作区无无关文件改动；确认未提交密钥 / 临时调试代码；确认文档与实际行为一致。

---

## 自检记录（Spec 覆盖核对）

- 字段展示不完整 → Task 2 卡片化消除截断；Task 3 列表测试断言。✅
- 调度前端兜底翻译（cron/every/at + 原文兜底）→ Task 1 `translateCronExpr` / `scheduleDisplay`，Task 1 测试覆盖。✅
- 状态中文化 → Task 1 `translateState`，应用于 Task 2 列表、Task 4 详情、Task 5 下拉。✅
- 投递中文化 → Task 1 `translateDeliver`，应用于 Task 2 列表。✅
- 「基础字段」技术区保留英文 → Task 4 Step 3 明确不动。✅
- `statusSummary` 保留英文 → Task 5 明确不动。✅
- 测试（cronDisplay / 列表 / 不回归 / 浏览器）→ Task 1、Task 3、Task 6。✅
