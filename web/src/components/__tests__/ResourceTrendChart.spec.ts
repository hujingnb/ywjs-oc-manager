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

  // 单点采样没有折线长度，应渲染点标记保证用户仍能看到有效数据。
  it('renders a marker when samples contain one value', () => {
    const wrapper = mount(ResourceTrendChart, {
      props: {
        title: 'CPU 趋势',
        samples: [{ sampled_at: '2026-05-13T00:00:00Z', value: 10 }],
        unit: 'percent',
      },
    })

    expect(wrapper.find('.trend-marker').exists()).toBe(true)
  })

  // secondary 指标用于同图展示关联序列，必须有独立线条和可见标签。
  it('renders secondary values as a separate series and visible label', () => {
    const wrapper = mount(ResourceTrendChart, {
      props: {
        title: '网络趋势',
        samples: [
          { sampled_at: '2026-05-13T00:00:00Z', value: 1024, secondary: 2048 },
          { sampled_at: '2026-05-13T00:05:00Z', value: 4096, secondary: 8192 },
        ],
        unit: 'bytes',
      },
    })

    expect(wrapper.find('.secondary-line').exists()).toBe(true)
    expect(wrapper.text()).toContain('次要')
    expect(wrapper.text()).toMatch(/8 KB/)
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
