// ScheduleField.spec.ts —— 调度点选子组件交互单测。
// 覆盖：默认态发出 cron、添加时间点重算表达式、外部 value 回填模式、预览文案渲染。
// mock naive-ui：聚焦数据流，避免真实组件 teleport / 时间控件干扰。
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import ScheduleField from './ScheduleField.vue'

vi.mock('naive-ui', () => ({
  NRadioGroup: {
    props: ['value'],
    emits: ['update:value'],
    template: '<div class="radio-group" :data-value="value"><slot /></div>',
  },
  NRadio: { props: ['value'], template: '<label><slot /></label>' },
  NRadioButton: { props: ['value'], template: '<label><slot /></label>' },
  NCheckboxGroup: {
    props: ['value'],
    emits: ['update:value'],
    template: '<div class="cbg" :data-value="JSON.stringify(value)"><slot /></div>',
  },
  NCheckbox: { props: ['value', 'label'], template: '<label>{{ label }}</label>' },
  NInputNumber: {
    props: ['value'],
    emits: ['update:value'],
    template: '<input type="number" :value="value ?? \'\'" @input="$emit(\'update:value\', Number($event.target.value))" />',
  },
  NInput: {
    props: ['value', 'placeholder'],
    emits: ['update:value'],
    template: '<input :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NSelect: {
    props: ['value', 'options'],
    emits: ['update:value'],
    template: '<select :value="value" @change="$emit(\'update:value\', $event.target.value)"></select>',
  },
  NButton: { emits: ['click'], template: '<button @click="$emit(\'click\')"><slot /></button>' },
  NSpace: { template: '<div><slot /></div>' },
}))

function mountField(value = '') {
  return mount(ScheduleField, { props: { value, 'onUpdate:value': () => {} } })
}

describe('ScheduleField', () => {
  // 默认日历态（每天 09:00）挂载即发出对应 cron。
  it('挂载发出默认 cron', async () => {
    const wrapper = mountField('')
    await nextTick()
    const emitted = wrapper.emitted('update:value')
    expect(emitted?.at(-1)?.[0]).toBe('cron 0 9 * * *')
  })

  // 外部传入 every 表达式：组件解析后预览展示「每 10 分钟」。
  it('回填间隔表达式并预览', async () => {
    const wrapper = mountField('every 10m')
    await nextTick()
    expect(wrapper.text()).toContain('每 10 分钟')
  })

  // 改一个时间点的小时：重新发出更新后的 cron 表达式。
  it('修改时间点重算表达式', async () => {
    const wrapper = mountField('cron 0 9 * * *')
    await nextTick()
    const hourInput = wrapper.find('input[type="number"]')
    await hourInput.setValue(18)
    await nextTick()
    expect(wrapper.emitted('update:value')?.at(-1)?.[0]).toBe('cron 0 18 * * *')
  })

  // 预览区展示「实际运行」中文文案。
  it('展示实际运行预览', async () => {
    const wrapper = mountField('cron 0 9 * * *')
    await nextTick()
    expect(wrapper.text()).toContain('每天 09:00')
  })
})
