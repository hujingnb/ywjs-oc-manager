import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import TicketStatusStepper from './TicketStatusStepper.vue'

vi.mock('naive-ui', () => ({
  NTag: { template: '<span class="n-tag"><slot /></span>' },
}))

describe('TicketStatusStepper', () => {
  // processing 状态应渲染三步并高亮制作中节点。
  it('highlights current step', () => {
    const wrapper = mount(TicketStatusStepper, { props: { status: 'processing' } })
    expect(wrapper.text()).toContain('待处理')
    expect(wrapper.text()).toContain('制作中')
    expect(wrapper.text()).toContain('已交付')
    expect(wrapper.find('[data-status="processing"]').exists()).toBe(true)
    expect(wrapper.findAll('.status-step.active')).toHaveLength(2)
  })

  // rejected 状态不推进主线,应显示独立的已拒绝红色标记。
  it('shows rejected badge for rejected status', () => {
    const wrapper = mount(TicketStatusStepper, { props: { status: 'rejected' } })
    expect(wrapper.text()).toContain('已拒绝')
    expect(wrapper.find('.n-tag').exists()).toBe(true)
  })
})
