import { mount } from '@vue/test-utils'
import { defineComponent, h, provide, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AppChannelsTab from './AppChannelsTab.vue'
import type { ChannelProgress } from '@/api/hooks/useChannel'
import type { AppDTO } from '@/api/hooks/useApps'

const progress = ref<ChannelProgress | null>(null)
const beginAuth = vi.fn()
const unbindChannel = vi.fn()
const observedChannelTypes: string[] = []

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
    useChannelProgressQuery: (_appId: unknown, channelTypeRef: { value: string }) => {
      observedChannelTypes.push(channelTypeRef.value)
      return { data: progress }
    },
    useBeginChannelAuth: (_appId: unknown, channelTypeRef: { value: string }) => {
      observedChannelTypes.push(channelTypeRef.value)
      return { mutateAsync: beginAuth }
    },
    useUnbindChannel: (_appId: unknown, channelTypeRef: { value: string }) => {
      observedChannelTypes.push(channelTypeRef.value)
      return { mutateAsync: unbindChannel }
    },
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

function mountChannelsTab(channelType?: string) {
  return mount(defineComponent({
    setup() {
      provide('app', app)
      return () => h(AppChannelsTab, { appId: 'app-1', channelType })
    },
  }))
}

describe('AppChannelsTab', () => {
  beforeEach(() => {
    progress.value = null
    beginAuth.mockReset()
    unbindChannel.mockReset()
    observedChannelTypes.length = 0
  })

  // 渠道清单：实例详情页必须一次性展示全部规划渠道（9 个），并明确哪些渠道当前不可用。
  it('列出全部渠道并置灰暂不支持渠道', () => {
    const wrapper = mountChannelsTab()

    const items = wrapper.findAll('.channel-list-item')
    expect(items).toHaveLength(9)
    expect(items.map(item => item.text())).toEqual([
      expect.stringContaining('微信'),
      expect.stringContaining('企业微信'),
      expect.stringContaining('飞书'),
      expect.stringContaining('钉钉'),
      expect.stringContaining('Telegram'),
      expect.stringContaining('WhatsApp'),
      expect.stringContaining('Discord'),
      expect.stringContaining('Slack'),
      expect.stringContaining('Line'),
    ])

    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(1)
    expect(supported[0].text()).toContain('已支持')
    expect(supported[0].text()).toContain('微信')
    // 微信 logo 用新的 type 钩子，且为内联 SVG
    expect(wrapper.find('.channel-logo--wechat').exists()).toBe(true)

    const unsupported = wrapper.findAll('.channel-list-item.unsupported')
    expect(unsupported).toHaveLength(8)
    expect(unsupported.every(item => item.attributes('aria-disabled') === 'true')).toBe(true)
    expect(unsupported.every(item => item.attributes('disabled') !== undefined)).toBe(true)
    expect(unsupported.every(item => item.text().includes('暂不支持'))).toBe(true)
    // 未支持渠道 logo 均带灰度 muted 标记（原有 3 个不支持 + 新增 5 个 = 8 个）
    expect(wrapper.findAll('.channel-logo.muted')).toHaveLength(8)
    // 抽查 3 个新增渠道的 type 钩子（非全覆盖，discord/slack 由上面 muted 计数间接保障）
    expect(wrapper.find('.channel-logo--telegram').exists()).toBe(true)
    expect(wrapper.find('.channel-logo--whatsapp').exists()).toBe(true)
    expect(wrapper.find('.channel-logo--line').exists()).toBe(true)
  })

  // 非微信路由参数：未支持渠道只能作为灰色入口展示，详情区和接口参数仍固定使用微信。
  it('非微信 channelType 仍固定展示并请求微信渠道详情', () => {
    const wrapper = mountChannelsTab('feishu')
    const detail = wrapper.find('.channel-detail')

    expect(detail.text()).toContain('微信')
    expect(detail.text()).not.toContain('飞书')
    expect(observedChannelTypes).toEqual(['wechat', 'wechat', 'wechat'])
  })

  // 已绑定状态：头部以「微信 · 已绑定」呈现，已绑定身份单独成行；不泄露后端原值 bound。
  it('已绑定渠道头部展示「渠道名 · 状态」且不显示 challenge 空态', () => {
    progress.value = {
      status: 'bound',
      bound_identity: 'alice',
      updated_at: '2026-05-25T12:00:00Z',
    }

    const wrapper = mountChannelsTab()
    const detail = wrapper.find('.channel-detail')

    expect(detail.text()).toContain('微信')
    expect(detail.text()).toContain('· 已绑定') // 状态并入头部同一行
    expect(detail.text()).toContain('已绑定：alice') // 绑定身份仍单独成行
    expect(detail.text()).not.toContain('当前渠道') // 去掉 kicker
    expect(detail.text()).not.toContain('当前状态：') // 去掉旧状态前缀
    expect(detail.text()).not.toContain('· bound') // 头部状态不泄露后端原值（应为中文标签）
    expect(wrapper.text()).not.toContain('尚未发起挑战')
  })
})
