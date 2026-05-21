// useCron API hooks 测试覆盖 Hermes Cron 端点 URL、请求体和缓存失效边界。
// 这里用真实 QueryClient 挂载组合式函数，避免只测试静态 helper 而漏掉 Vue Query 行为。
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type Ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { apiRequest } from '@/api/client'
import {
  cronHistoryKey,
  cronJobKey,
  cronJobsKey,
  cronOutputKey,
  cronStatusKey,
  useCreateCronJob,
  useCronHistoryQuery,
  useCronJobAction,
  useCronJobQuery,
  useCronJobsQuery,
  useCronOutputQuery,
} from '@/api/hooks/useCron'

vi.mock('@/api/client', () => ({
  apiRequest: vi.fn(),
}))

const apiRequestMock = vi.mocked(apiRequest)

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function mountWithQueryClient(setupHook: () => Record<string, unknown> | void) {
  const queryClient = createTestQueryClient()
  const wrapper = mount(defineComponent({
    setup(_, { expose }) {
      const exposed = setupHook()
      if (exposed) expose(exposed)
      return () => h('div')
    },
  }), {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
    },
  })
  return { queryClient, wrapper }
}

describe('useCron hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllTimers()
  })

  // 任务列表 query 应使用稳定 filters key，并把空字符串 q/status 原样交给 apiRequest。
  it('useCronJobs calls cron jobs endpoint with filter query', async () => {
    apiRequestMock.mockResolvedValueOnce({ jobs: [] })
    const appId = ref('app-1')
    const filters = ref({ q: '', status: '' })

    mountWithQueryClient(() => {
      useCronJobsQuery(appId, filters)
    })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs', {
        query: { q: '', status: '' },
      })
    })
    expect(cronJobsKey('app-1', filters.value)).toEqual(['cron', 'jobs', 'app-1', { q: '', status: '' }])
  })

  // 新建任务 mutation 应 POST 到 jobs 端点，并在成功后失效列表和调度器状态缓存。
  it('create mutation posts cron job body and invalidates list and status caches', async () => {
    const createdJob = { id: 'job-1', name: '日报' }
    apiRequestMock.mockResolvedValueOnce({ job: createdJob })
    const appId = ref('app-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      createJob: useCreateCronJob(appId),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const setQueryDataSpy = vi.spyOn(queryClient, 'setQueryData')

    await (wrapper.vm as unknown as {
      createJob: ReturnType<typeof useCreateCronJob>
    }).createJob.mutateAsync({
      name: '日报',
      schedule: '0 9 * * *',
      prompt: '生成摘要',
    })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs', {
      method: 'POST',
      body: { name: '日报', schedule: '0 9 * * *', prompt: '生成摘要' },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['cron', 'jobs', 'app-1'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: cronStatusKey('app-1') })
    expect(setQueryDataSpy).toHaveBeenCalledWith(cronJobKey('app-1', 'job-1'), createdJob)
  })

  // 详情、历史和输出 helper 必须保持互不冲突的 key，避免页面切换时读到其他资源缓存。
  it('builds separate cache keys for detail history and output resources', () => {
    expect(cronJobKey('app-1', 'job-1')).toEqual(['cron', 'job', 'app-1', 'job-1'])
    expect(cronHistoryKey('app-1', 'job-1')).toEqual(['cron', 'history', 'app-1', 'job-1'])
    expect(cronOutputKey('app-1', 'job-1', '2026-05-20.md')).toEqual([
      'cron',
      'output',
      'app-1',
      'job-1',
      '2026-05-20.md',
    ])
  })

  // 详情、历史和输出 query 应分别命中 Task 4 暴露的 REST 路由。
  it('detail history and output queries call their cron endpoints', async () => {
    apiRequestMock
      .mockResolvedValueOnce({ job: { id: 'job-1' } })
      .mockResolvedValueOnce({ runs: [] })
      .mockResolvedValueOnce({ output: { job_id: 'job-1', file_name: '2026-05-20.md', content: '# 日报\n' } })
    const appId = ref('app-1')
    const jobId = ref('job-1')
    const fileName = ref('2026-05-20.md')

    mountWithQueryClient(() => {
      useCronJobQuery(appId, jobId)
      useCronHistoryQuery(appId, jobId)
      useCronOutputQuery(appId, jobId, fileName as Ref<string | undefined>)
    })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs/job-1')
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs/job-1/history')
      expect(apiRequestMock).toHaveBeenCalledWith(
        '/api/v1/apps/app-1/hermes/cron/jobs/job-1/output/2026-05-20.md',
      )
    })
  })

  // 删除任务时应移除详情、历史和输出缓存，避免 URL 刚清理前 stale output query 重新打已删除资源。
  it('delete action removes output cache without refetching deleted artifacts', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const appId = ref('app-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      action: useCronJobAction(appId),
    }))
    const removeSpy = vi.spyOn(queryClient, 'removeQueries')
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (wrapper.vm as unknown as {
      action: ReturnType<typeof useCronJobAction>
    }).action.mutateAsync({ verb: 'delete', jobId: 'job-1' })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/hermes/cron/jobs/job-1', {
      method: 'DELETE',
    })
    expect(removeSpy).toHaveBeenCalledWith({ queryKey: cronJobKey('app-1', 'job-1') })
    expect(removeSpy).toHaveBeenCalledWith({ queryKey: cronHistoryKey('app-1', 'job-1') })
    expect(removeSpy).toHaveBeenCalledWith({ queryKey: ['cron', 'output', 'app-1', 'job-1'] })
    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['cron', 'output', 'app-1', 'job-1'] })
  })
})
