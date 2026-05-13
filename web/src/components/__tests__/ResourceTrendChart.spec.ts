import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ResourceTrendChart from '../ResourceTrendChart.vue'

const echartsMock = vi.hoisted(() => ({
  latestOption: null as any,
  setOption: vi.fn((option: any) => {
    echartsMock.latestOption = option
  }),
  resize: vi.fn(),
  dispose: vi.fn(),
}))

// ECharts 在 jsdom 中不需要真实初始化 canvas，测试只校验组件传入的 option。
vi.mock('echarts/core', () => ({
  use: vi.fn(),
  init: vi.fn(() => ({
    setOption: echartsMock.setOption,
    resize: echartsMock.resize,
    dispose: echartsMock.dispose,
  })),
}))

// ResourceTrendChart 测试覆盖资源趋势图的空态、ECharts 配置、纵轴和字节单位格式化。
describe('ResourceTrendChart', () => {
  beforeEach(() => {
    echartsMock.latestOption = null
    echartsMock.setOption.mockClear()
    echartsMock.resize.mockClear()
    echartsMock.dispose.mockClear()
  })

  // 空采样场景应展示占位文案，并且不渲染 ECharts 图表。
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
    expect(wrapper.find('.resource-echarts').exists()).toBe(false)
    expect(echartsMock.setOption).not.toHaveBeenCalled()
  })

  // 有数值采样场景应交给 ECharts 渲染，组件自身不再输出 SVG 图形。
  it('renders an ECharts chart without svg when samples contain values', async () => {
    const wrapper = await mountChart({
      props: {
        title: 'CPU 趋势',
        samples: [
          { sampled_at: '2026-05-13T00:00:00Z', value: 10 },
          { sampled_at: '2026-05-13T00:05:00Z', value: 30 },
        ],
        unit: 'percent',
      },
    })

    expect(wrapper.find('.resource-echarts').exists()).toBe(true)
    expect(wrapper.find('svg').exists()).toBe(false)
    expect(echartsMock.setOption).toHaveBeenCalled()
  })

  // 纵轴配置必须显示数值轴和单位格式，避免抽屉内只能看到无刻度折线。
  it('configures y-axis labels for visible scale values', async () => {
    await mountChart({
      props: {
        title: 'CPU 趋势',
        samples: [
          { sampled_at: '2026-05-13T00:00:00Z', value: 10 },
          { sampled_at: '2026-05-13T00:05:00Z', value: 30 },
        ],
        unit: 'percent',
      },
    })

    const option = chartOption()

    expect(option.yAxis).toMatchObject({
      type: 'value',
      axisLabel: expect.objectContaining({ formatter: expect.any(Function) }),
    })
    expect(option.grid).toMatchObject({ left: 52 })
    expect(option.yAxis.axisLabel.formatter(30)).toBe('30%')
  })

  // tooltip 使用 axis 触发，鼠标靠近采样点时 ECharts 会展示当前时间和值。
  it('configures axis tooltip with formatted point data', async () => {
    await mountChart({
      props: {
        title: 'CPU 趋势',
        samples: [
          { sampled_at: '2026-05-13T00:00:00Z', value: 10 },
          { sampled_at: '2026-05-13T00:05:00Z', value: 30 },
        ],
        unit: 'percent',
      },
    })
    const option = chartOption()

    expect(option.tooltip).toMatchObject({
      trigger: 'axis',
      axisPointer: { type: 'line' },
    })
    expect(option.tooltip.formatter([
      { axisValue: '05/13 08:00', seriesName: 'CPU 趋势', value: 10 },
    ])).toContain('CPU 趋势：10%')
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

async function mountChart(options: Parameters<typeof mount>[1]) {
  const wrapper = mount(ResourceTrendChart, options)
  await nextTick()
  await nextTick()
  return wrapper
}

function chartOption(): any {
  return echartsMock.latestOption
}
