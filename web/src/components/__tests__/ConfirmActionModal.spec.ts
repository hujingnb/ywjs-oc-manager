import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ConfirmActionModal from '../ConfirmActionModal.vue'

describe('ConfirmActionModal verifyValue', () => {
  it('verifyValue 非空且输入为空时，确认按钮 disabled', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: {
        visible: true,
        title: 't',
        message: 'm',
        verifyValue: 'smoke-app-1',
        verifyHint: '输入应用名 "smoke-app-1" 以确认',
      },
    })
    const btn = document.querySelector('.modal-card .primary-button') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    wrapper.unmount()
  })

  it('verifyValue 非空且输入错误时，确认按钮 disabled', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: { visible: true, title: 't', message: 'm', verifyValue: 'smoke-app-1' },
    })
    const input = document.querySelector('.modal-card .verify-input') as HTMLInputElement
    input.value = 'wrong-name'
    input.dispatchEvent(new Event('input'))
    await wrapper.vm.$nextTick()
    const btn = document.querySelector('.modal-card .primary-button') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    wrapper.unmount()
  })

  it('verifyValue 大小写不一致仍可解锁（不区分大小写）', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: { visible: true, title: 't', message: 'm', verifyValue: 'Smoke-App' },
    })
    const input = document.querySelector('.modal-card .verify-input') as HTMLInputElement
    input.value = 'SMOKE-app'
    input.dispatchEvent(new Event('input'))
    await wrapper.vm.$nextTick()
    const btn = document.querySelector('.modal-card .primary-button') as HTMLButtonElement
    expect(btn.disabled).toBe(false)
    wrapper.unmount()
  })

  it('verifyValue 缺省时确认按钮始终 enabled（兼容禁用 api_key 等旧调用）', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: { visible: true, title: 't', message: 'm' },
    })
    const btn = document.querySelector('.modal-card .primary-button') as HTMLButtonElement
    expect(btn.disabled).toBe(false)
    wrapper.unmount()
  })
})
