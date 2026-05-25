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

  // 渠道清单：实例详情页必须一次性展示全部规划渠道，并明确哪些渠道当前不可用。
  it('列出全部渠道并置灰暂不支持渠道', () => {
    const wrapper = mountChannelsTab()

    const items = wrapper.findAll('.channel-list-item')
    expect(items).toHaveLength(4)
    expect(items.map(item => item.text())).toEqual([
      expect.stringContaining('微信'),
      expect.stringContaining('企业微信'),
      expect.stringContaining('飞书'),
      expect.stringContaining('钉钉'),
    ])

    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(1)
    expect(supported[0].text()).toContain('已支持')
    expect(supported[0].text()).toContain('微信')
    expect(wrapper.find('.channel-logo.wechat').exists()).toBe(true)

    const unsupported = wrapper.findAll('.channel-list-item.unsupported')
    expect(unsupported).toHaveLength(3)
    expect(unsupported.every(item => item.attributes('aria-disabled') === 'true')).toBe(true)
    expect(unsupported.every(item => item.text().includes('暂不支持'))).toBe(true)
    expect(wrapper.find('.channel-logo.work-wechat').exists()).toBe(true)
    expect(wrapper.find('.channel-logo.feishu').exists()).toBe(true)
    expect(wrapper.find('.channel-logo.dingtalk').exists()).toBe(true)
  })

  // 已绑定状态：页面展示中文“已绑定”，不泄露后端原值 bound 或内部 challenge 空态。
  it('已绑定渠道展示中文状态且不显示 challenge 空态', () => {
    progress.value = {
      status: 'bound',
      bound_identity: 'alice',
      updated_at: '2026-05-25T12:00:00Z',
    }

    const wrapper = mountChannelsTab()
    const detail = wrapper.find('.channel-detail')

    expect(detail.text()).toContain('微信')
    expect(detail.text()).toContain('当前状态：已绑定')
    expect(detail.text()).toContain('已绑定：alice')
    expect(wrapper.text()).not.toContain('当前状态：bound')
    expect(wrapper.text()).not.toContain('尚未发起挑战')
  })
})
