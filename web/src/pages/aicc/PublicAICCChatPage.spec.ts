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
  fetchSession: vi.fn(),
  consent: vi.fn(),
  submitLeadValues: vi.fn(),
  submitFeedback: vi.fn(),
  updateResolution: vi.fn(),
  uploadImage: vi.fn(),
  readStoredSession: vi.fn(),
  clearStoredSession: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
}))

vi.mock('@/api/hooks/useAICC', () => ({
  fetchAICCPublicConfig: apiState.fetchConfig,
  createAICCPublicSession: apiState.createSession,
  fetchAICCPublicSession: apiState.fetchSession,
  readAICCPublicStoredSessionToken: apiState.readStoredSession,
  clearAICCPublicStoredSessionToken: apiState.clearStoredSession,
  sendAICCPublicMessage: apiState.sendMessage,
  consentAICCPublicSession: apiState.consent,
  submitAICCPublicLeadValues: apiState.submitLeadValues,
  submitAICCPublicFeedback: apiState.submitFeedback,
  updateAICCPublicSessionResolution: apiState.updateResolution,
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
    apiState.fetchSession.mockReset()
    apiState.consent.mockReset()
    apiState.submitLeadValues.mockReset()
    apiState.submitFeedback.mockReset()
    apiState.updateResolution.mockReset()
    apiState.uploadImage.mockReset()
    apiState.readStoredSession.mockReset()
    apiState.clearStoredSession.mockReset()
    apiState.readStoredSession.mockReturnValue('')
    apiState.fetchConfig.mockResolvedValue({
      name: '售前接待',
      greeting: '您好，请问有什么可以帮您？',
      privacy_mode: 'notice',
      privacy_text: '我们会使用本次对话内容回答问题。',
      retention_days: 180,
      lead_fields: [],
    })
    apiState.createSession.mockResolvedValue({ session_token: 'session-token' })
    apiState.fetchSession.mockResolvedValue({ messages: [], resolution_status: 'unknown' })
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', text: '收到' })
    apiState.updateResolution.mockImplementation(async (_token: string, status: string) => ({ resolution_status: status }))
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
    expect(apiState.sendMessage).toHaveBeenCalledWith('session-token', expect.objectContaining({ client_message_id: expect.any(String), text: '报价多少', image_file_id: undefined }))
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

  // 场景：公开页刷新后应恢复本地 session token，继续发送消息时不重新创建会话。
  it('resumes the stored public session after page refresh', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.text()).toContain('在线')
    expect(apiState.createSession).not.toHaveBeenCalled()

    await wrapper.find('textarea').setValue('继续刚才的问题')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()

    expect(apiState.createSession).not.toHaveBeenCalled()
    expect(apiState.sendMessage).toHaveBeenCalledWith('stored-session-token', expect.objectContaining({ client_message_id: expect.any(String), text: '继续刚才的问题', image_file_id: undefined }))
  })

  // 场景：公开页刷新续接会话时，应拉取并渲染服务端已保存的历史消息。
  it('restores stored session messages after page refresh', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      messages: [
        { id: 'msg-1', direction: 'visitor', text: '刷新前的问题' },
        { id: 'msg-2', direction: 'assistant', text: '刷新前的回复' },
      ],
    })

    const wrapper = mountPublicChat()
    await flushPromises()

    expect(apiState.fetchSession).toHaveBeenCalledWith('stored-session-token')
    expect(wrapper.text()).toContain('刷新前的问题')
    expect(wrapper.text()).toContain('刷新前的回复')
    expect(wrapper.text()).not.toContain('您好，请问有什么可以帮您？')
  })

  // 场景：刷新恢复的服务端历史消息必须在各自气泡下方显示浏览器本地时区的发送时分。
  it('renders local timestamps below restored message bubbles', async () => {
    const sentAt = new Date(2026, 6, 14, 9, 5).toISOString()
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      messages: [
        { id: 'msg-1', direction: 'visitor', text: '历史问题', created_at: sentAt },
        { id: 'msg-2', direction: 'assistant', text: '历史回复', created_at: sentAt },
      ],
    })

    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.findAll('.message-time')).toHaveLength(2)
    expect(wrapper.findAll('.message-time').map(time => time.text())).toEqual(['09:05', '09:05'])
  })

  // 场景：欢迎语、当前发送的访客消息和客服回复无需刷新，也必须立即显示发送时分。
  it('renders timestamps for greeting and newly exchanged messages', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 6, 14, 14, 30))
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      await wrapper.find('textarea').setValue('报价多少')
      await wrapper.find('form.composer').trigger('submit')
      await flushPromises()

      expect(wrapper.findAll('.message-time').map(time => time.text())).toEqual(['14:30', '14:30', '14:30'])
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：公开页刷新恢复已完成留资的会话时，不能因为当前客服配置了必填字段而重复展示留资表单。
  it('does not show the lead form when the restored session already completed lead capture', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchConfig.mockResolvedValue({
      name: '售前接待',
      greeting: '您好，请问有什么可以帮您？',
      privacy_mode: 'notice',
      privacy_text: '我们会使用本次对话内容回答问题。',
      retention_days: 180,
      lead_fields: [
        { field_key: 'contact_phone', label: '联系电话', field_type: 'phone', required: true },
      ],
    })
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      lead_status: 'complete',
      messages: [
        { id: 'msg-1', direction: 'visitor', text: '刷新前的问题' },
        { id: 'msg-2', direction: 'assistant', text: '刷新前的回复' },
      ],
    })

    const wrapper = mountPublicChat()
    await flushPromises()
    await nextTick()

    expect(wrapper.text()).not.toContain('请先留下联系方式')
    expect(wrapper.find('.lead-gate').exists()).toBe(false)
    expect(wrapper.find('textarea').attributes('disabled')).toBeUndefined()
  })

  // 场景：公开页不再渲染单条助手回复反馈，避免把某条消息评价误当成会话解决状态。
  it('does not render per-message feedback controls for assistant replies', async () => {
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('报价多少')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()

    expect(wrapper.text()).toContain('收到')
    expect(wrapper.find('.feedback-row').exists()).toBe(false)
    expect(apiState.submitFeedback).not.toHaveBeenCalled()
  })

  // 场景：访客点击顶部“已解决/未解决”时，只标记当前会话，不依赖任何 message id。
  it('marks the current session resolved or unresolved from header actions', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.text()).toContain('已解决')
    expect(wrapper.text()).toContain('未解决')

    await wrapper.findAll('button').find(button => button.text().includes('未解决'))?.trigger('click')
    await flushPromises()
    await nextTick()

    expect(apiState.updateResolution).toHaveBeenLastCalledWith('stored-session-token', 'unresolved')
    expect(apiState.submitFeedback).not.toHaveBeenCalled()
    expect(wrapper.findAll('button').find(button => button.text().includes('未解决'))?.attributes('disabled')).toBeDefined()

    await wrapper.findAll('button').find(button => button.text().includes('已解决'))?.trigger('click')
    await flushPromises()
    await nextTick()

    expect(apiState.updateResolution).toHaveBeenLastCalledWith('stored-session-token', 'resolved')
    expect(apiState.submitFeedback).not.toHaveBeenCalled()
  })

  // 场景：访客主动新建对话时只清除当前会话，下一次发送才懒创建新 session，避免空会话。
  it('clears the current session when starting a new conversation', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('旧会话消息')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()
    expect(wrapper.text()).toContain('旧会话消息')

    await wrapper.findAll('button').find(button => button.text().includes('新建对话'))?.trigger('click')
    await nextTick()

    expect(apiState.clearStoredSession).toHaveBeenCalledWith('public-token', 'web_link')
    expect(apiState.createSession).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('旧会话消息')
    expect(wrapper.text()).toContain('您好，请问有什么可以帮您？')

    await wrapper.find('textarea').setValue('新会话消息')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()

    expect(apiState.createSession).toHaveBeenCalledTimes(1)
    expect(apiState.sendMessage).toHaveBeenLastCalledWith('session-token', expect.objectContaining({ client_message_id: expect.any(String), text: '新会话消息', image_file_id: undefined }))
  })

  // 场景：选择非图片文件时前端立即提示，不能创建图片预览或调用上传接口。
  it('rejects non-image files before creating a pending upload', async () => {
    const wrapper = mountPublicChat()
    await flushPromises()

    const fileInput = wrapper.find('#aicc-public-image')
    Object.defineProperty(fileInput.element, 'files', {
      configurable: true,
      value: [new File(['text'], 'notes.txt', { type: 'text/plain' })],
    })
    await fileInput.trigger('change')
    await flushPromises()

    expect(wrapper.text()).toContain('请选择图片文件')
    expect(wrapper.find('.pending-image').exists()).toBe(false)
    expect(apiState.uploadImage).not.toHaveBeenCalled()
  })

  // 场景：服务端敏感词错误码必须映射为本地化提示，不能直接向访客暴露后端原始文案。
  it('renders a localized message for the sensitive-word error code', async () => {
    apiState.sendMessage.mockRejectedValue(Object.assign(new Error('消息包含暂不支持发送的内容'), {
      status: 400,
      body: { code: 'AICC_SENSITIVE_WORD' },
    }))
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('包含违禁词')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('这条消息包含暂不支持发送的内容，请调整后再试。')
  })
})
