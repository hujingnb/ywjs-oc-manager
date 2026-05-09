import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DataTableList from '../DataTableList.vue'
import { NDataTable } from 'naive-ui'

const baseProps = {
  title: '测试列表',
  columns: [{ title: '名', key: 'name' }],
  data: [{ id: '1', name: 'A' }],
}

describe('DataTableList', () => {
  it('renders title and toolbar slot', () => {
    const wrapper = mount(DataTableList, {
      props: baseProps,
      slots: { toolbar: '<button class="t-btn">新建</button>' },
    })
    expect(wrapper.text()).toContain('测试列表')
    expect(wrapper.find('.t-btn').exists()).toBe(true)
  })

  it('shows eyebrow and subtitle when provided', () => {
    const wrapper = mount(DataTableList, {
      props: { ...baseProps, eyebrow: 'Platform · 测试', subtitle: '副标题文本' },
    })
    expect(wrapper.text()).toContain('Platform · 测试')
    expect(wrapper.text()).toContain('副标题文本')
  })

  it('does not render eyebrow/subtitle when not provided', () => {
    const wrapper = mount(DataTableList, { props: baseProps })
    expect(wrapper.text()).not.toContain('Platform · 测试')
  })

  it('shows errorMessage when set', () => {
    const wrapper = mount(DataTableList, {
      props: { ...baseProps, errorMessage: '加载失败' },
    })
    expect(wrapper.text()).toContain('加载失败')
  })

  it('does not render error block when errorMessage is empty', () => {
    const wrapper = mount(DataTableList, { props: baseProps })
    expect(wrapper.html()).not.toContain('加载失败')
  })

  it('passes loading prop to NDataTable', () => {
    const wrapper = mount(DataTableList, { props: { ...baseProps, loading: true } })
    const table = wrapper.findComponent(NDataTable)
    expect(table.props('loading')).toBe(true)
  })
})
