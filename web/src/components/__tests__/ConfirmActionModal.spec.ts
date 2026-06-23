import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ConfirmActionModal from '../ConfirmActionModal.vue'
import { i18n } from '@/i18n'

// 覆盖二次确认弹框的 verifyValue 业务保护，确保危险操作不会在未输入确认值时提交。
describe('ConfirmActionModal verifyValue', () => {
  it('verifyValue 非空且输入为空时，确认按钮 disabled', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: {
        visible: true,
        title: 't',
        message: 'm',
        verifyValue: 'smoke-app-1',
        verifyHint: '输入实例名 "smoke-app-1" 以确认',
      },
      global: { plugins: [i18n] },
    })
    await nextTick()
    const btn = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    wrapper.unmount()
  })

  it('verifyValue 非空且输入错误时，确认按钮 disabled', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: { visible: true, title: 't', message: 'm', verifyValue: 'smoke-app-1' },
      global: { plugins: [i18n] },
    })
    await nextTick()
    const input = document.querySelector('.n-modal input') as HTMLInputElement
    input.value = 'wrong-name'
    input.dispatchEvent(new Event('input'))
    await wrapper.vm.$nextTick()
    const btn = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    wrapper.unmount()
  })

  it('verifyValue 大小写不一致仍可解锁（不区分大小写）', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: { visible: true, title: 't', message: 'm', verifyValue: 'Smoke-App' },
      global: { plugins: [i18n] },
    })
    await nextTick()
    const input = document.querySelector('.n-modal input') as HTMLInputElement
    input.value = 'SMOKE-app'
    input.dispatchEvent(new Event('input'))
    await wrapper.vm.$nextTick()
    const btn = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(btn.disabled).toBe(false)
    wrapper.unmount()
  })

  it('verifyValue 缺省时确认按钮始终 enabled（兼容禁用 api_key 等旧调用）', async () => {
    const wrapper = mount(ConfirmActionModal, {
      attachTo: document.body,
      props: { visible: true, title: 't', message: 'm' },
      global: { plugins: [i18n] },
    })
    await nextTick()
    const btn = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(btn.disabled).toBe(false)
    wrapper.unmount()
  })
})
