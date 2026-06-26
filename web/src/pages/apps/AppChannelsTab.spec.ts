import { mount } from '@vue/test-utils'
import { defineComponent, h, provide, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
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
    // 飞书发起 hook 仅接收 appId，内部调用 useQueryClient；测试环境无 VueQueryPlugin，
    // 故以桩替换避免真实初始化报错。飞书面板默认不渲染（默认选中微信），mutateAsync 不会被触发。
    useBeginFeishuAuth: () => ({ mutateAsync: vi.fn() }),
  }
})

const app = ref<AppDTO>({
  id: 'app-1',
  org_id: 'org-1',
  owner_user_id: 'user-1',
  name: '测试实例',
  status: 'running',
  api_key_status: 'succeeded',
  // 实例知识库容量字段为 AppDTO 必填字段；渠道页本身不读取该值。
  knowledge_quota_bytes: 1024 * 1024 * 1024,
})

function mountChannelsTab(channelType?: string) {
  return mount(defineComponent({
    setup() {
      provide('app', app)
      return () => h(AppChannelsTab, { appId: 'app-1', channelType })
    },
  }), { global: { plugins: [i18n] } })
}

describe('AppChannelsTab', () => {
  beforeEach(() => {
    // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
    i18n.global.locale.value = 'zh'
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

    // 当前已支持渠道为微信 + 飞书共 2 个；两者均展示「已支持」且可点击进入详情。
    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(2)
    expect(supported.every(item => item.text().includes('已支持'))).toBe(true)
    const supportedText = supported.map(item => item.text()).join('|')
    expect(supportedText).toContain('微信')
    expect(supportedText).toContain('飞书')
    // 微信 logo 用新的 type 钩子，且为内联 SVG
    expect(wrapper.find('.channel-logo--wechat').exists()).toBe(true)

    // 飞书转为已支持后，其余 7 个渠道仍为灰色不可用入口。
    const unsupported = wrapper.findAll('.channel-list-item.unsupported')
    expect(unsupported).toHaveLength(7)
    expect(unsupported.every(item => item.attributes('aria-disabled') === 'true')).toBe(true)
    expect(unsupported.every(item => item.attributes('disabled') !== undefined)).toBe(true)
    expect(unsupported.every(item => item.text().includes('暂不支持'))).toBe(true)
    // 未支持渠道 logo 均带灰度 muted 标记（9 个渠道中微信、飞书已支持，余 7 个置灰）
    expect(wrapper.findAll('.channel-logo.muted')).toHaveLength(7)
    // 抽查 3 个新增渠道的 type 钩子（非全覆盖，discord/slack 由上面 muted 计数间接保障）
    expect(wrapper.find('.channel-logo--telegram').exists()).toBe(true)
    expect(wrapper.find('.channel-logo--whatsapp').exists()).toBe(true)
    expect(wrapper.find('.channel-logo--line').exists()).toBe(true)
  })

  // 路由 channelType 参数不自动选中：飞书虽已支持，但默认详情区仍落在微信，需用户主动点击切换。
  // 飞书 hook 现也会在 setup 阶段初始化（进度查询在未选中飞书时被禁用），因此只断言微信进度/发起/解绑
  // 三个 hook 各以 'wechat' 调用一次，而不再断言 observedChannelTypes 完全等于三个 'wechat'。
  it('路由 channelType=feishu 时详情区仍默认展示微信', () => {
    const wrapper = mountChannelsTab('feishu')
    const detail = wrapper.find('.channel-detail')

    expect(detail.text()).toContain('微信')
    expect(detail.text()).not.toContain('飞书')
    expect(observedChannelTypes.filter(type => type === 'wechat')).toHaveLength(3)
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
