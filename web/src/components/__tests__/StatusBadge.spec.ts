import { mount } from '@vue/test-utils'
import { NTag } from 'naive-ui'
import { describe, expect, it } from 'vitest'
import StatusBadge from '../StatusBadge.vue'
import type { StatusView } from '@/domain/status'

describe('StatusBadge', () => {
  // 直接断言传给 NTag 的 type prop，不依赖 CSS class（naive-ui 不为 type 添加类名）
  it.each<[StatusView['tone'], string]>([
    ['success', 'success'],
    ['warning', 'warning'],
    ['danger', 'error'],
    ['neutral', 'default'],
  ])('tone=%s 映射到 NTag type=%s', (tone, expected) => {
    const wrapper = mount(StatusBadge, { props: { view: { label: 'L', tone } } })
    expect(wrapper.findComponent(NTag).props('type')).toBe(expected)
  })

  it('渲染 view.label 文本', () => {
    const wrapper = mount(StatusBadge, { props: { view: { label: '运行中', tone: 'success' } } })
    expect(wrapper.text()).toContain('运行中')
  })
})
