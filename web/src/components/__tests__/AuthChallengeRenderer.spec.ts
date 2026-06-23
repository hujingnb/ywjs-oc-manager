import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import AuthChallengeRenderer from '../AuthChallengeRenderer.vue'
import { i18n } from '@/i18n'

describe('AuthChallengeRenderer', () => {
  // 无 challenge：组件不再展示”尚未发起挑战”，该状态由父级渠道页解释。
  it('challenge 为空时不渲染内部空态文案', () => {
    const wrapper = mount(AuthChallengeRenderer, { props: { challenge: null }, global: { plugins: [i18n] } })

    expect(wrapper.text()).not.toContain('尚未发起挑战')
    expect(wrapper.text()).toBe('')
  })
})
