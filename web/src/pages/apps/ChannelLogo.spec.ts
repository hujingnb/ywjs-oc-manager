import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'

import ChannelLogo from './ChannelLogo.vue'

describe('ChannelLogo', () => {
  // 指定渠道：根据 type 输出 channel-logo 与 channel-logo--{type} 钩子，并内联 SVG
  it('渲染对应渠道的 class 钩子与内联 SVG', () => {
    const wrapper = mount(ChannelLogo, { props: { type: 'wechat' } })
    expect(wrapper.classes()).toContain('channel-logo')
    expect(wrapper.classes()).toContain('channel-logo--wechat')
    expect(wrapper.html()).toContain('<svg')
  })

  // 未支持渠道：muted=true 时附加灰度 class，并仍渲染 SVG
  it('muted 时附加 muted class', () => {
    const wrapper = mount(ChannelLogo, { props: { type: 'telegram', muted: true } })
    expect(wrapper.classes()).toContain('muted')
    expect(wrapper.html()).toContain('<svg')
  })

  // 详情区大尺寸：large=true 时附加 large class
  it('large 时附加 large class', () => {
    const wrapper = mount(ChannelLogo, { props: { type: 'wechat', large: true } })
    expect(wrapper.classes()).toContain('large')
  })

  // 全部 9 个渠道都必须有 SVG 映射，防止扩展时漏配某个 type
  it('全部渠道 type 均能渲染 SVG', () => {
    const types = [
      'wechat', 'work_wechat', 'feishu', 'dingtalk',
      'telegram', 'whatsapp', 'discord', 'slack', 'line',
    ] as const
    for (const type of types) {
      const wrapper = mount(ChannelLogo, { props: { type } })
      expect(wrapper.html()).toContain('<svg') // 每个 type 都有内联 SVG
    }
  })
})
