import { mount } from '@vue/test-utils'
import { NTag } from 'naive-ui'
import { describe, expect, it, beforeEach } from 'vitest'
import StatusBadge from '../StatusBadge.vue'
import type { StatusView } from '@/domain/status'
import { i18n } from '@/i18n'

describe('StatusBadge', () => {
  // 每次用例前切换为中文，确保断言中文文案的用例与翻译文件一致。
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  // 直接断言传给 NTag 的 type prop，不依赖 CSS class（naive-ui 不为 type 添加类名）。
  // StatusBadge 新增了 useI18n，测试需注入 i18n 插件。
  it.each<[StatusView['tone'], string]>([
    ['success', 'success'],
    ['warning', 'warning'],
    ['danger', 'error'],
    ['neutral', 'default'],
  ])('tone=%s 映射到 NTag type=%s', (tone, expected) => {
    const wrapper = mount(StatusBadge, {
      props: { view: { label: 'domain.appStatus.running', tone } },
      global: { plugins: [i18n] },
    })
    expect(wrapper.findComponent(NTag).props('type')).toBe(expected)
  })

  it('渲染 view.label 对应的当前语言文案', () => {
    // label 迁移为 i18n 键后，StatusBadge 通过 t() 解析；zh locale 下 running → 运行中。
    const wrapper = mount(StatusBadge, {
      props: { view: { label: 'domain.appStatus.running', tone: 'success' } },
      global: { plugins: [i18n] },
    })
    expect(wrapper.text()).toContain('运行中')
  })
})
