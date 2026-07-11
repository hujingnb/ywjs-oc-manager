import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import PublicAICCChatPage from './PublicAICCChatPage.vue'

const routeState = vi.hoisted(() => ({
  params: { publicToken: 'public-token' },
  query: {},
}))

const apiState = vi.hoisted(() => ({
  fetchConfig: vi.fn(),
  createSession: vi.fn(),
  sendMessage: vi.fn(),
  consent: vi.fn(),
  submitLeadValues: vi.fn(),
  submitFeedback: vi.fn(),
  uploadImage: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
}))

vi.mock('@/api/hooks/useAICC', () => ({
  fetchAICCPublicConfig: apiState.fetchConfig,
  createAICCPublicSession: apiState.createSession,
  sendAICCPublicMessage: apiState.sendMessage,
  consentAICCPublicSession: apiState.consent,
  submitAICCPublicLeadValues: apiState.submitLeadValues,
  submitAICCPublicFeedback: apiState.submitFeedback,
  uploadAICCPublicImage: apiState.uploadImage,
}))

const ButtonStub = defineComponent({
  props: ['disabled', 'loading', 'attrType'],
  emits: ['click'],
  setup(props, { slots, emit }) {
    return () => h('button', {
      type: props.attrType === 'submit' ? 'submit' : 'button',
      disabled: Boolean(props.disabled || props.loading),
      onClick: () => emit('click'),
    }, [slots.icon?.(), slots.default?.()])
  },
})

const InputStub = defineComponent({
  props: ['value', 'type', 'disabled', 'placeholder'],
  emits: ['update:value'],
  setup(props, { emit }) {
    const tag = props.type === 'textarea' ? 'textarea' : 'input'
    return () => h(tag, {
      value: props.value ?? '',
      disabled: Boolean(props.disabled),
      placeholder: props.placeholder as string,
      onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
    })
  },
})

const TagStub = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  },
})

const AlertStub = defineComponent({
  setup(_, { slots }) {
    return () => h('div', slots.default?.())
  },
})

function mountPublicChat() {
  i18n.global.locale.value = 'zh'
  return mount(PublicAICCChatPage, {
    global: {
      plugins: [i18n],
      stubs: {
        NAlert: AlertStub,
        NButton: ButtonStub,
        NInput: InputStub,
        NTag: TagStub,
        'n-alert': AlertStub,
        'n-button': ButtonStub,
        'n-input': InputStub,
        'n-tag': TagStub,
      },
    },
  })
}

describe('PublicAICCChatPage', () => {
  beforeEach(() => {
    routeState.params.publicToken = 'public-token'
    routeState.query = {}
    apiState.fetchConfig.mockReset()
    apiState.createSession.mockReset()
    apiState.sendMessage.mockReset()
    apiState.consent.mockReset()
    apiState.submitLeadValues.mockReset()
    apiState.submitFeedback.mockReset()
    apiState.uploadImage.mockReset()
    apiState.fetchConfig.mockResolvedValue({
      name: '售前接待',
      greeting: '您好，请问有什么可以帮您？',
      privacy_mode: 'notice',
      privacy_text: '我们会使用本次对话内容回答问题。',
      retention_days: 180,
      lead_fields: [],
    })
    apiState.createSession.mockResolvedValue({ session_token: 'session-token' })
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', text: '收到' })
  })

  // 场景：访客或挂件只打开公开页但没有发送消息时，不应创建 0 消息会话。
  it('loads public config without creating a session on page open', async () => {
    mountPublicChat()

    await flushPromises()

    expect(apiState.fetchConfig).toHaveBeenCalledWith('public-token', 'web_link')
    expect(apiState.createSession).not.toHaveBeenCalled()
    expect(apiState.sendMessage).not.toHaveBeenCalled()
  })

  // 场景：首次发送消息时才创建会话，并使用新 session token 发送访客消息。
  it('creates the public session lazily when the first message is submitted', async () => {
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('报价多少')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()

    expect(apiState.createSession).toHaveBeenCalledTimes(1)
    expect(apiState.createSession).toHaveBeenCalledWith('public-token', 'web_link')
    expect(apiState.sendMessage).toHaveBeenCalledWith('session-token', { text: '报价多少', image_file_id: undefined })
    expect(wrapper.text()).toContain('收到')
  })

  // 场景：隐私提示只在访客刚进入公开页时展示，发送消息后不再占用输入区上方空间。
  it('hides the privacy notice after the visitor sends the first message', async () => {
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.text()).toContain('我们会使用本次对话内容回答问题。')

    await wrapper.find('textarea').setValue('报价多少')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()

    expect(wrapper.text()).not.toContain('我们会使用本次对话内容回答问题。')
  })

  // 场景：notice 模式的隐私提示是轻量文本，不再使用带图标和背景框的提示容器。
  it('renders the privacy notice as plain helper text before the first message', async () => {
    const wrapper = mountPublicChat()
    await flushPromises()

    const privacyNotice = wrapper.find('.privacy-copy')
    expect(privacyNotice.exists()).toBe(true)
    expect(privacyNotice.text()).toBe('我们会使用本次对话内容回答问题。')
    expect(wrapper.find('.privacy-note').exists()).toBe(false)
  })
})
