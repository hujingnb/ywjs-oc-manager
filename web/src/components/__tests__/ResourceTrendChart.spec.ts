import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ResourceTrendChart from '../ResourceTrendChart.vue'

// ResourceTrendChart 测试覆盖资源趋势图的空态、折线和字节单位格式化。
describe('ResourceTrendChart', () => {
  // 空采样场景应展示占位文案，并且不渲染 SVG 折线。
  it('renders empty state when samples are empty', () => {
    const wrapper = mount(ResourceTrendChart, {
      props: {
        title: 'CPU 趋势',
        samples: [],
        unit: 'percent',
        emptyText: '暂无采样',
      },
    })

    expect(wrapper.text()).toContain('暂无采样')
    expect(wrapper.find('polyline').exists()).toBe(false)
  })

  // 有数值采样场景应渲染一条主指标趋势折线。
  it('renders a polyline when samples contain values', () => {
    const wrapper = mount(ResourceTrendChart, {
      props: {
        title: 'CPU 趋势',
        samples: [
          { sampled_at: '2026-05-13T00:00:00Z', value: 10 },
          { sampled_at: '2026-05-13T00:05:00Z', value: 30 },
        ],
        unit: 'percent',
      },
    })

    expect(wrapper.findAll('polyline')).toHaveLength(1)
  })

  // 字节单位场景应把原始 byte 数值格式化为 KB/MB 等可读标签。
  it('formats byte values in tooltip labels', () => {
    const wrapper = mount(ResourceTrendChart, {
      props: {
        title: '内存趋势',
        samples: [
          { sampled_at: '2026-05-13T00:00:00Z', value: 1024 },
          { sampled_at: '2026-05-13T00:05:00Z', value: 2 * 1024 * 1024 },
        ],
        unit: 'bytes',
      },
    })

    expect(wrapper.text()).toMatch(/KB|MB/)
  })
})
