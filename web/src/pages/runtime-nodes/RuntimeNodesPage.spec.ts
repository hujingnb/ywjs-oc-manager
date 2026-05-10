import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import RuntimeNodesPage from './RuntimeNodesPage.vue'

vi.mock('@/api/hooks/useRuntimeNodes', () => ({
  useRuntimeNodesQuery: () => ({
    data: ref([{
      id: 'node-1',
      name: 'node-1',
      status: 'active',
      max_apps: 3,
      heartbeat_interval_seconds: 30,
    }]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useSetRuntimeNodeStatus: () => ({ mutate: vi.fn() }),
}))

vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
}))

describe('RuntimeNodesPage', () => {
  it('只展示 agent 配置上报的最大应用数，不提供编辑入口', () => {
    const wrapper = mount(RuntimeNodesPage)

    expect(wrapper.text()).toContain('最大应用数')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).not.toContain('编辑')
  })
})
