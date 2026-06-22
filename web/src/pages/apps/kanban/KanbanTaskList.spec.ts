// KanbanTaskList.spec.ts 覆盖任务列表的状态分组展示契约。
// 重点验证 Hermes 新增状态时，列表仍保留任务并展示可诊断的未知状态文案。
import { mount } from '@vue/test-utils'
import { describe, expect, it, beforeEach } from 'vitest'

import { i18n } from '@/i18n'
import KanbanTaskList from './KanbanTaskList.vue'
import type { KanbanTask } from '@/api/hooks/useKanban'

function mountList(tasks: KanbanTask[]) {
  // 注入 i18n 插件，使组件内 t() 调用能够解析翻译文案。
  return mount(KanbanTaskList, {
    props: {
      tasks,
      appId: 'app-1',
      selectedId: undefined,
      latestEvents: {},
    },
    global: {
      plugins: [i18n],
      stubs: {
        NCard: { template: '<section><slot /></section>' },
        NCollapse: { template: '<div class="collapse"><slot /></div>' },
        NCollapseItem: {
          props: ['title', 'name'],
          template: '<section class="group" :data-name="name"><h3 class="group-title">{{ title }}</h3><slot /></section>',
        },
        KanbanTaskRow: {
          props: ['task'],
          template: '<article class="task-row-stub">{{ task.title }}</article>',
        },
      },
    },
  })
}

describe('KanbanTaskList', () => {
  // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
    localStorage.clear()
  })

  // 覆盖已知状态：列表分组标题使用中文文案，不直接展示 Hermes 英文状态。
  // 分组标签由 formatKanbanStatus（domain map）产生，与 i18n 无关，仍为中文。
  it('renders known status group labels in Chinese', () => {
    const wrapper = mountList([
      { id: 'task-running', title: '运行中任务', status: 'running' },
    ])

    expect(wrapper.text()).toContain('运行中 (1)')
    expect(wrapper.text()).not.toContain('Running')
  })

  // 覆盖未知状态：Hermes 灰度新增状态时，任务不能被固定分组过滤掉。
  // 断言分组标题和数量即可证明任务进入了可见分组；分组内容默认可折叠。
  it('keeps tasks with unknown statuses visible in fallback groups', () => {
    const wrapper = mountList([
      { id: 'task-unknown', title: '灰度状态任务', status: 'paused_by_policy' },
    ])

    expect(wrapper.text()).toContain('未知状态：paused_by_policy (1)')
  })
})
