// CronJobList.spec.ts —— Cron 任务左侧卡片列表单元测试。
// 覆盖：卡片渲染中文状态与翻译后调度、空列表占位、缺 id 行不可选、选中态。
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import type { CronJob } from '@/api/hooks/useCron'
import CronJobList from './CronJobList.vue'

// mock naive-ui：聚焦卡片文案与点击行为，避免组件库渲染细节干扰查询。
vi.mock('naive-ui', () => ({
  NCard: { template: '<div><slot /></div>' },
  NEmpty: { props: ['description'], template: '<div class="empty">{{ description }}</div>' },
  NTag: { props: ['type'], template: '<span class="tag"><slot /></span>' },
}))

// 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
beforeEach(() => {
  i18n.global.locale.value = 'zh'
})

function mountList(jobs: CronJob[], selectedId?: string) {
  // 注入 i18n 插件，使组件内 t() 调用能够解析翻译文案。
  return mount(CronJobList, { props: { jobs, selectedId }, global: { plugins: [i18n] } })
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
