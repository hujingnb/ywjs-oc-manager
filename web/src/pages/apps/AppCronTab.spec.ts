// AppCronTab.spec.ts —— Hermes Cron 顶层 tab 单元测试。
// 覆盖：列表渲染、URL query 同步、stub 降级、状态摘要和写能力降级。
import { mount } from '@vue/test-utils'
import { computed, reactive, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AppCronTab from './AppCronTab.vue'

type TestQueryValue = string | string[]

const routeState = reactive<{ query: Record<string, TestQueryValue> }>({ query: {} })
const routerReplace = vi.fn((location: { query?: Record<string, unknown> }) => {
  const nextQuery: Record<string, string> = {}
  for (const [key, value] of Object.entries(location.query ?? {})) {
    if (typeof value === 'string' && value !== '') {
      nextQuery[key] = value
    }
  }
  routeState.query = nextQuery
})

// mock vue-router：router.replace 会同步更新 reactive routeState，便于断言点击后的详情联动。
vi.mock('vue-router', () => ({
  useRoute: () => routeState,
  useRouter: () => ({ replace: routerReplace }),
}))

// mock naive-ui useMessage：组件测试不挂 NMessageProvider，直接提供空实现。
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

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isPlatformAdmin: false,
  }),
}))

const jobsError = ref<unknown>(null)
const statusError = ref<unknown>(null)
const capabilitiesError = ref<unknown>(null)
const jobs = ref([
  {
    id: 'cron_daily',
    name: '日报',
    prompt: '汇总昨天的工作',
    schedule: { display: '每天 09:00', expr: '0 9 * * *' },
    state: 'scheduled',
    enabled: true,
    deliver: 'wechat',
    next_run_at: '2026-05-21T09:00:00Z',
  },
  {
    id: 'cron_weekly',
    name: '周报',
    prompt: '汇总本周的工作',
    schedule: { display: '每周一 10:00', expr: '0 10 * * 1' },
    state: 'paused',
    enabled: false,
    deliver: 'email',
  },
])
const capabilities = ref({
  contract_version: '1.0',
  features: { status: true, history: true, output: true, write: true, script: true, advanced_fields: true },
})
const status = ref({
  available: true,
  gateway_running: true,
  active_jobs: 2,
  next_run_at: '2026-05-21T09:00:00Z',
  tick_seconds: 60,
})

// mock useCron hooks：所有 query 都返回响应式 ref，使页面 computed 能跟随 route query 更新。
vi.mock('@/api/hooks/useCron', () => ({
  useCronCapabilitiesQuery: () => ({
    data: capabilities,
    isLoading: ref(false),
    error: capabilitiesError,
    refetch: vi.fn(),
  }),
  useCronStatusQuery: () => ({
    data: status,
    isLoading: ref(false),
    error: statusError,
    refetch: vi.fn(),
  }),
  useCronJobsQuery: () => ({
    data: jobs,
    isLoading: ref(false),
    error: jobsError,
    refetch: vi.fn(),
  }),
  useCronJobQuery: (_appId: unknown, jobId: { value?: string }) => ({
    data: computed(() => jobs.value.find((job) => job.id === jobId.value) ?? null),
    isLoading: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  }),
  useCronHistoryQuery: () => ({
    data: ref([]),
    isLoading: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  }),
  useCronOutputQuery: () => ({
    data: ref(null),
    isLoading: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  }),
  useCreateCronJob: () => ({
    mutateAsync: vi.fn(),
    isPending: ref(false),
  }),
  useUpdateCronJob: () => ({
    mutateAsync: vi.fn(),
    isPending: ref(false),
  }),
  useCronJobAction: () => ({
    mutateAsync: vi.fn(),
    isPending: ref(false),
  }),
}))

function mountCronTab() {
  return mount(AppCronTab, {
    props: { appId: 'app-1' },
    global: {
      stubs: {
        // CronJobList：透传任务名并把点击事件发回父组件，覆盖父级 query 同步逻辑。
        CronJobList: {
          props: ['jobs', 'selectedId'],
          emits: ['select'],
          template: `
            <div class="cron-list-stub">
              <button
                v-for="job in jobs"
                :key="job.id"
                class="cron-row"
                @click="$emit('select', job.id)"
              >{{ job.name }}</button>
            </div>
          `,
        },
        // CronJobDetail：渲染选中的详情任务名和写操作占位按钮，便于断言选中态与 feature gating。
        CronJobDetail: {
          props: ['job', 'history', 'output', 'isPlatformAdmin', 'canWrite'],
          template: '<section class="cron-detail-stub"><span>{{ job?.name }}</span><button v-if="canWrite" class="write-action">操作</button></section>',
        },
        CronJobFormModal: true,
        NButton: {
          props: ['disabled', 'loading'],
          emits: ['click'],
          template: '<button v-bind="$attrs" :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
        },
        NCard: { template: '<section><slot name="header" /><slot /></section>' },
        NEmpty: { props: ['description'], template: '<p>{{ description }}</p>' },
        NInput: true,
        NSelect: true,
        NSpace: { template: '<div><slot /></div>' },
      },
    },
  })
}

describe('AppCronTab', () => {
  beforeEach(() => {
    routeState.query = {}
    routerReplace.mockClear()
    jobsError.value = null
    statusError.value = null
    capabilitiesError.value = null
    jobs.value = [
      {
        id: 'cron_daily',
        name: '日报',
        prompt: '汇总昨天的工作',
        schedule: { display: '每天 09:00', expr: '0 9 * * *' },
        state: 'scheduled',
        enabled: true,
        deliver: 'wechat',
        next_run_at: '2026-05-21T09:00:00Z',
      },
      {
        id: 'cron_weekly',
        name: '周报',
        prompt: '汇总本周的工作',
        schedule: { display: '每周一 10:00', expr: '0 10 * * 1' },
        state: 'paused',
        enabled: false,
        deliver: 'email',
      },
    ]
    capabilities.value = {
      contract_version: '1.0',
      features: { status: true, history: true, output: true, write: true, script: true, advanced_fields: true },
    }
    status.value = {
      available: true,
      gateway_running: true,
      active_jobs: 2,
      next_run_at: '2026-05-21T09:00:00Z',
      tick_seconds: 60,
    }
  })

  // 覆盖任务列表渲染，mock 返回的「日报」必须传入左侧列表。
  it('jobs list renders 日报', () => {
    const wrapper = mountCronTab()

    expect(wrapper.text()).toContain('日报')
  })

  // 覆盖点击列表项后的 URL query 同步与右侧详情联动。
  it('clicking list item writes job query and right detail receives selected detail', async () => {
    const wrapper = mountCronTab()

    await wrapper.findAll('.cron-row')[1].trigger('click')

    expect(routerReplace).toHaveBeenCalledWith({ query: { job: 'cron_weekly' } })
    expect(wrapper.find('.cron-detail-stub').text()).toContain('周报')
  })

  // 覆盖 stub runtime 降级，CRON_NOT_SUPPORTED_ON_STUB 应展示定时任务不可用文案。
  it('CRON_NOT_SUPPORTED_ON_STUB shows stub copy', () => {
    jobsError.value = { body: { code: 'CRON_NOT_SUPPORTED_ON_STUB' }, message: 'stub' }

    const wrapper = mountCronTab()

    expect(wrapper.text()).toContain('定时任务不可用')
    expect(wrapper.find('.cron-list-stub').exists()).toBe(false)
  })

  // 覆盖调度器状态摘要，gateway_running=true 时展示约定的英文状态文本。
  it('status summary renders Gateway cron running', () => {
    const wrapper = mountCronTab()

    expect(wrapper.text()).toContain('Gateway cron running')
  })

  // 覆盖写能力降级，features.write=false 时隐藏新建按钮和详情写操作。
  it('features.write false hides create button', () => {
    capabilities.value = {
      contract_version: '1.0',
      features: { status: true, history: true, output: true, write: false, script: true, advanced_fields: true },
    }
    routeState.query = { job: 'cron_daily' }

    const wrapper = mountCronTab()

    expect(wrapper.find('.create-cron-btn').exists()).toBe(false)
    expect(wrapper.find('.write-action').exists()).toBe(false)
  })

  // 覆盖 Vue Router query 数组值：页面应取第一个非空字符串作为选中任务和输出文件。
  it('normalizes array query values before selecting job detail', () => {
    routeState.query = { job: ['', 'cron_weekly'], file: ['', 'run.md'] }

    const wrapper = mountCronTab()

    expect(wrapper.find('.cron-detail-stub').text()).toContain('周报')
  })
})
