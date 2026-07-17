import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { NInput, NModal } from 'naive-ui'
import { i18n } from '@/i18n'
import ResetMemberPasswordModal from '../ResetMemberPasswordModal.vue'

// 集中登记挂载实例，确保断言中途失败时也能释放 Naive UI 的监听器和 Teleport。
const mountedWrappers: VueWrapper[] = []

// 统一使用真实 Naive UI 挂载密码重置弹窗，覆盖 Teleport 后的浏览器 DOM 行为。
function mountModal(props: { visible: boolean; username: string; busy?: boolean }) {
  const wrapper = mount(ResetMemberPasswordModal, {
    attachTo: document.body,
    props,
    global: { plugins: [i18n] },
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

// 通过真实 input 事件驱动 Naive UI 的 v-model，保持测试路径与浏览器输入一致。
async function enterPassword(value: string) {
  const input = document.querySelector('.n-modal input') as HTMLInputElement
  input.value = value
  input.dispatchEvent(new Event('input'))
  await nextTick()
}

// Naive UI 弹窗通过 Teleport 挂载到 body，每个场景结束后必须清理，避免跨用例污染。
afterEach(() => {
  mountedWrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  document.body.innerHTML = ''
})

// 覆盖密码掩码、长度校验、忙碌保护、提交事件和关闭重开后的敏感数据清理。
describe('ResetMemberPasswordModal', () => {
  // 默认掩码和点击切换必须保留，同时原生 input、label 与 dialog 建立可访问语义。
  it('配置密码原生属性并关联标签和弹窗名称', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice' })
    await nextTick()

    const input = document.querySelector('.n-modal input') as HTMLInputElement
    expect(input.type).toBe('password')
    expect(input.autocomplete).toBe('new-password')
    expect(input.id).toBe('reset-member-password-input')
    expect(wrapper.findComponent(NInput).props('showPasswordOn')).toBe('click')
    expect(document.querySelector('label')?.getAttribute('for')).toBe(input.id)
    expect(document.querySelector('[role="dialog"]')?.getAttribute('aria-label')).toBe(
      i18n.global.t('org.members.modal.resetTitle'),
    )
  })

  // 密码不足 8 位时禁止提交，避免把无效请求交给父组件。
  it('密码少于 8 位时禁用确认按钮', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice' })
    await nextTick()

    await enterPassword('1234567')
    const confirmButton = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(confirmButton.disabled).toBe(true)
  })

  // 合法密码解除按钮禁用，并把当前输入原样交给调用方。
  it('密码达到 8 位时启用确认按钮并提交当前密码', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice' })
    await nextTick()

    await enterPassword('password8')
    const confirmButton = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(confirmButton.disabled).toBe(false)
    confirmButton.click()
    await nextTick()
    expect(wrapper.emitted('confirm')).toEqual([['password8']])
  })

  // 提交处理中锁定确认按钮，防止用户连续触发重复请求。
  it('busy 时禁用确认按钮', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice', busy: true })
    await nextTick()

    await enterPassword('password8')
    const confirmButton = document.querySelector('.n-modal button.n-button--error-type') as HTMLButtonElement
    expect(confirmButton.disabled).toBe(true)
  })

  // API 失败且弹窗未关闭时保留输入；真正关闭并重开后清除上一轮敏感数据。
  it('visible 保持 true 时保留输入，关闭并重开后清空', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice' })
    await nextTick()

    await enterPassword('password8')
    await wrapper.setProps({ busy: true })
    await wrapper.setProps({ busy: false })
    expect(wrapper.findComponent(NInput).props('value')).toBe('password8')

    await wrapper.setProps({ visible: false })
    await wrapper.setProps({ visible: true })
    expect(wrapper.findComponent(NInput).props('value')).toBe('')
  })

  // 点击取消必须先清理组件和原生输入值，并且只通知父组件一次。
  it('点击取消立即清空密码并只触发一次 cancel', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice' })
    await nextTick()

    await enterPassword('password8')
    const cancelButton = document.querySelector(
      '.n-modal button:not(.n-button--error-type)',
    ) as HTMLButtonElement
    cancelButton.click()
    await nextTick()

    expect(wrapper.findComponent(NInput).props('value')).toBe('')
    expect((document.querySelector('.n-modal input') as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('cancel')).toHaveLength(1)
  })

  // 遮罩或 Escape 发出的 update:show(false) 与取消按钮共享一次性清理语义。
  it('update:show(false) 立即清空密码并只触发一次 cancel', async () => {
    const wrapper = mountModal({ visible: true, username: 'alice' })
    await nextTick()

    await enterPassword('password8')
    wrapper.findComponent(NModal).vm.$emit('update:show', false)
    await nextTick()

    expect(wrapper.findComponent(NInput).props('value')).toBe('')
    expect((document.querySelector('.n-modal input') as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('cancel')).toHaveLength(1)
  })
})
