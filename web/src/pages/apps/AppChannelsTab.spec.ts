import { mount } from '@vue/test-utils'
import { defineComponent, h, provide, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AppChannelsTab from './AppChannelsTab.vue'
import type { ChannelProgress } from '@/api/hooks/useChannel'
import type { AppDTO } from '@/api/hooks/useApps'

const progress = ref<ChannelProgress | null>(null)
const beginAuth = vi.fn()
const unbindChannel = vi.fn()

vi.mock('naive-ui', () => ({
  NButton: defineComponent({
    name: 'NButton',
    props: ['disabled', 'loading'],
    emits: ['click'],
    setup(props, { slots, emit }) {
      return () => h('button', {
        disabled: props.disabled as boolean,
        onClick: () => emit('click'),
      }, slots.default?.())
    },
  }),
  NCard: defineComponent({
    name: 'NCard',
    setup(_, { slots }) {
      return () => h('section', [
        slots.header?.(),
        slots['header-extra']?.(),
        slots.default?.(),
      ])
    },
  }),
  NSpace: defineComponent({
    name: 'NSpace',
    setup(_, { slots }) {
      return () => h('div', slots.default?.())
    },
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 'user-1', role: 'org_member', org_id: 'org-1' },
  }),
}))

vi.mock('@/api/hooks/useChannel', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/hooks/useChannel')>()
  return {
    ...actual,
    useChannelProgressQuery: () => ({ data: progress }),
    useBeginChannelAuth: () => ({ mutateAsync: beginAuth }),
    useUnbindChannel: () => ({ mutateAsync: unbindChannel }),
  }
})

const app = ref<AppDTO>({
  id: 'app-1',
  org_id: 'org-1',
  owner_user_id: 'user-1',
  name: '测试实例',
  status: 'running',
  api_key_status: 'succeeded',
})

function mountChannelsTab() {
  return mount(defineComponent({
    setup() {
      provide('app', app)
      return () => h(AppChannelsTab, { appId: 'app-1' })
    },
  }))
}

describe('AppChannelsTab', () => {
  beforeEach(() => {
    progress.value = null
    beginAuth.mockReset()
    unbindChannel.mockReset()
  })

  // 已绑定状态：页面展示中文“已绑定”，不泄露后端原值 bound 或内部 challenge 空态。
  it('已绑定渠道展示中文状态且不显示 challenge 空态', () => {
    progress.value = {
      status: 'bound',
      bound_identity: 'alice',
      updated_at: '2026-05-25T12:00:00Z',
    }

    const wrapper = mountChannelsTab()

    expect(wrapper.text()).toContain('当前状态：已绑定')
    expect(wrapper.text()).toContain('已绑定：alice')
    expect(wrapper.text()).not.toContain('当前状态：bound')
    expect(wrapper.text()).not.toContain('尚未发起挑战')
  })
})
