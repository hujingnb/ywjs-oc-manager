// AppKanbanTab.spec.ts —— AppKanbanTab 顶层组件单元测试。
// 覆盖：任务按状态分组渲染、stub 实例降级提示。
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

import { i18n } from '@/i18n'
import AppKanbanTab from './AppKanbanTab.vue'

// mock vue-router：useRoute 提供空 query，useRouter 提供 replace stub。
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}))

// mock naive-ui useMessage：jsdom 环境无 NMessageProvider，通过 mock 避免缺 provider 报错。
vi.mock('naive-ui', async (importOriginal) => {
  const original = await importOriginal<typeof import('naive-ui')>()
  return {
    ...original,
    useMessage: () => ({
      success: vi.fn(),
      error: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    }),
  }
})

// tasksError 和 boardsError 分别控制各 query 的 error 响应式 ref，供各测试分别覆盖。
const tasksError = ref<unknown>(null)
const boardsError = ref<unknown>(null)
// kanbanCapabilities 控制 useKanbanCapabilitiesQuery 返回的能力数据，供降级用例覆盖。
const kanbanCapabilities = ref<unknown>({
  contract_version: '1.0',
  verbs: ['create', 'comment'],
  features: { write: true, watch: true, runs: true, stats: true },
})

// mock @/api/hooks/useKanban：提供两条不同状态的任务用于分组渲染测试。
vi.mock('@/api/hooks/useKanban', () => ({
  useKanbanBoardsQuery: () => ({
    data: ref([{ slug: 'default', name: 'default' }]),
    isLoading: ref(false),
    error: boardsError,
  }),
  useKanbanTasksQuery: () => ({
    data: ref([
      // 运行中任务：status=running，用于验证「任务标题出现在渲染结果」。
      { id: 't_1', title: '运行中任务', status: 'running', assignee: 'devops', priority: 3, created_at: 0 },
      // 待办任务：status=todo，验证不同状态的任务都能出现。
      { id: 't_2', title: '待办任务', status: 'todo', assignee: 'analyst', priority: 1, created_at: 0 },
    ]),
    isLoading: ref(false),
    error: tasksError,
  }),
  useKanbanTaskQuery: () => ({
    data: ref(null),
    isLoading: ref(false),
    error: ref(null),
  }),
  useKanbanRunsQuery: () => ({
    data: ref([]),
    isLoading: ref(false),
    error: ref(null),
  }),
  // useKanbanStatsQuery：返回工具栏徽标用的统计数据（by_status 计数 + 最老就绪等待秒数）。
  useKanbanStatsQuery: () => ({
    data: ref({ by_status: { running: 1, todo: 1 }, oldest_ready_age_seconds: 0 }),
    isLoading: ref(false),
    error: ref(null),
  }),
  // useKanbanCapabilitiesQuery：默认返回全部能力可用，不触发降级。
  // data 指向顶层可变 ref kanbanCapabilities，供降级用例在挂载前修改。
  useKanbanCapabilitiesQuery: () => ({
    data: kanbanCapabilities,
    isLoading: ref(false),
    error: ref(null),
  }),
  useCreateKanbanTask: () => ({
    mutateAsync: vi.fn(),
    isPending: ref(false),
  }),
  useKanbanTaskAction: () => ({
    mutateAsync: vi.fn(),
    isPending: ref(false),
  }),
}))

// mock useKanbanEventStream：jsdom 无原生 EventSource，直接返回空响应式数据。
vi.mock('./kanban/useKanbanEventStream', () => ({
  useKanbanEventStream: () => ({
    eventsByTask: ref({}),
    latestEvents: ref({}),
    connected: ref(true),
    reconnect: vi.fn(),
  }),
}))

// mountKanbanTab 封装挂载逻辑，子组件全部 stub 避免拉入深层 Naive UI 依赖。
// KanbanTaskList 渲染为一个透传 tasks prop 中标题文本的 div，以便断言任务标题出现。
function mountKanbanTab() {
  return mount(AppKanbanTab, {
    props: { appId: 'app-1' },
    global: {
      plugins: [i18n],
      stubs: {
        // KanbanTaskList：渲染传入的每条任务标题，供测试断言内容可见性。
        KanbanTaskList: {
          template: '<div class="task-list-stub"><span v-for="t in tasks" :key="t.id">{{ t.title }}</span></div>',
          props: ['tasks', 'selectedId', 'appId', 'latestEvents'],
        },
        // KanbanTaskDetail：纯占位，详情面板测试不在本文件覆盖。
        KanbanTaskDetail: true,
        // KanbanCreateModal：纯占位，模态框交互测试不在本文件覆盖。
        KanbanCreateModal: true,
        // Naive UI 布局组件：保留 slot 透传，避免内容被吞掉。
        NCard: { template: '<div><slot /></div>' },
        NSpace: { template: '<div><slot /></div>' },
        // 交互组件：不需要渲染真实内容。
        NSelect: true,
        NInput: true,
        NButton: true,
        // NEmpty：渲染 description prop 为段落文本，供降级文案断言使用。
        NEmpty: { props: ['description'], template: '<p>{{ description }}</p>' },
      },
    },
  })
}

describe('AppKanbanTab', () => {
  beforeEach(() => {
    // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
    i18n.global.locale.value = 'zh'
    // 每个测试前重置 error 状态，防止测试间状态污染。
    tasksError.value = null
    boardsError.value = null
    // 重置 capabilities 为全部能力可用，确保非降级用例不受影响。
    kanbanCapabilities.value = {
      contract_version: '1.0',
      verbs: ['create', 'comment'],
      features: { write: true, watch: true, runs: true, stats: true },
    }
  })

  // 覆盖：任务按状态分组渲染 —— mock 返回 running 和 todo 两条任务，
  // 断言两条任务标题都出现在渲染输出中（通过 KanbanTaskList stub 中的 span 展示）。
  // 同时验证默认能力（write: true）下「新建任务」按钮存在（正向断言，防止 v-if 写反时 false-positive）。
  it('按状态渲染任务：运行中与待办任务标题均出现', () => {
    const wrapper = mountKanbanTab()
    const text = wrapper.text()
    expect(text).toContain('运行中任务')
    expect(text).toContain('待办任务')
    // 默认能力（write 未被降级）下「新建任务」按钮应渲染。
    expect(wrapper.find('.create-task-btn').exists()).toBe(true)
  })

  // 覆盖：stub 实例降级提示 —— 当 tasksQuery.error 含 body.code === 'KANBAN_NOT_SUPPORTED_ON_STUB'
  // 时，isStubInstance 计算属性为 true，隐藏分屏面板并显示降级说明文案。
  it('stub 镜像降级：tasksQuery 返回 KANBAN_NOT_SUPPORTED_ON_STUB 时显示降级文案', async () => {
    // 构造符合 ApiError 结构的 stub 错误对象，body.code 使用后端约定的 sentinel 值。
    tasksError.value = { body: { code: 'KANBAN_NOT_SUPPORTED_ON_STUB' }, message: 'stub' }

    const wrapper = mountKanbanTab()
    const text = wrapper.text()
    // 断言降级说明文案出现（来自组件内 n-empty description 属性）。
    expect(text).toContain('该实例运行的是本地 dev 镜像，任务看板不可用')
    // 断言任务列表（分屏面板）不再显示。
    expect(wrapper.find('.task-list-stub').exists()).toBe(false)
  })

  // 覆盖：capabilities 报告 write 不支持时，工具栏「新建任务」按钮被隐藏。
  // NButton 被 stub 为空元素，无法通过文案断言；改用 CSS class selector 区分按钮是否渲染。
  it('能力降级：features.write 为 false 时隐藏新建任务按钮', () => {
    // 设置 capabilities 中 write 为 false，模拟旧版 kanban 不支持创建任务的情形。
    kanbanCapabilities.value = {
      contract_version: '1.0',
      verbs: ['list', 'show'],
      features: { write: false, watch: true, runs: true, stats: true },
    }
    const wrapper = mountKanbanTab()
    // 断言新建任务按钮不渲染（v-if="kanbanFeatures?.write !== false" 应为 false）。
    expect(wrapper.find('.create-task-btn').exists()).toBe(false)
  })
})
