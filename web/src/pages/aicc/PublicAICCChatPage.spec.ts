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
  fetchMessageStatus: vi.fn(),
  fetchSession: vi.fn(),
  consent: vi.fn(),
  submitLeadValues: vi.fn(),
  declineLead: vi.fn(),
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
  fetchAICCPublicMessageStatus: apiState.fetchMessageStatus,
  consentAICCPublicSession: apiState.consent,
  submitAICCPublicLeadValues: apiState.submitLeadValues,
  declineAICCPublicLeadInvitation: apiState.declineLead,
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
    apiState.fetchMessageStatus.mockReset()
    apiState.fetchSession.mockReset()
    apiState.consent.mockReset()
    apiState.submitLeadValues.mockReset()
    apiState.declineLead.mockReset()
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
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '收到' })
    apiState.fetchMessageStatus.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '收到' })
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

  // 场景：异步任务刚受理时，访客消息与排队中的助手占位必须立即显示，不能伪造一条默认回复。
  it('shows a queued placeholder after the visitor message is accepted asynchronously', async () => {
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'queued' })
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('异步问题')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('异步问题')
    expect(wrapper.text()).toContain('消息已提交，正在排队处理。')
    expect(wrapper.text()).not.toContain('我已收到，请继续补充您的问题。')
    wrapper.unmount()
  })

  // 场景：轮询完成后应将原占位替换为唯一一条助手回复，不能额外追加重复气泡。
  it('replaces the queued placeholder with one completed assistant reply', async () => {
    vi.useFakeTimers()
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'queued' })
    apiState.fetchMessageStatus.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '异步回复' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      await wrapper.find('textarea').setValue('异步问题')
      await wrapper.find('form.composer').trigger('submit')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(2_000)
      await flushPromises()

      expect(apiState.fetchMessageStatus).toHaveBeenCalledWith('session-token', 'message-1')
      expect(wrapper.text()).toContain('异步回复')
      expect(wrapper.findAll('.message-row.assistant')).toHaveLength(2)
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：服务端要求等待重试时，公开页需展示等待状态并按 retry_after_seconds 调度下一次查询。
  it('shows retry-wait status and uses the server polling delay', async () => {
    vi.useFakeTimers()
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'queued' })
    apiState.fetchMessageStatus
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'retry_wait', retry_after_seconds: 5 })
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'completed', text: '恢复后的回复' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      await wrapper.find('textarea').setValue('稍后重试')
      await wrapper.find('form.composer').trigger('submit')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(2_000)
      await flushPromises()

      expect(wrapper.text()).toContain('服务繁忙，正在稍后重试。')
      await vi.advanceTimersByTimeAsync(4_999)
      expect(apiState.fetchMessageStatus).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1)
      await flushPromises()
      expect(wrapper.text()).toContain('恢复后的回复')
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：服务端错误返回 0 秒等待时，前端必须至少等待一秒，不能立刻递归发起下一次状态查询。
  it('clamps a zero-second retry delay before polling again', async () => {
    vi.useFakeTimers()
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'queued' })
    apiState.fetchMessageStatus
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'retry_wait', retry_after_seconds: 0 })
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'completed', text: '延迟后的回复' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      await wrapper.find('textarea').setValue('等待下界')
      await wrapper.find('form.composer').trigger('submit')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(2_000)
      await flushPromises()

      expect(apiState.fetchMessageStatus).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(999)
      expect(apiState.fetchMessageStatus).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1)
      await flushPromises()
      expect(wrapper.text()).toContain('延迟后的回复')
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：刷新恢复的排队访客消息必须重建助手占位并继续轮询，而不是再次发送访客原文。
  it('restores and polls a queued message after the public chat page refreshes', async () => {
    vi.useFakeTimers()
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      messages: [
        { id: 'message-1', direction: 'visitor', text: '刷新中的问题', client_message_id: 'client-message-1', task_status: 'queued' },
      ],
    })
    apiState.fetchMessageStatus.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '刷新后的回复' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      expect(wrapper.text()).toContain('刷新中的问题')
      expect(wrapper.text()).toContain('消息已提交，正在排队处理。')
      expect(apiState.sendMessage).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(2_000)
      await flushPromises()

      expect(apiState.fetchMessageStatus).toHaveBeenCalledWith('stored-session-token', 'message-1')
      expect(wrapper.text()).toContain('刷新后的回复')
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：旧会话中的待处理任务缺少 client_message_id 时，仍须按消息 ID 恢复轮询，不能永久停留在排队提示。
  it('restores and polls a legacy queued message without a client message ID', async () => {
    vi.useFakeTimers()
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      messages: [
        { id: 'legacy-message-1', direction: 'visitor', text: '旧会话中的问题', task_status: 'queued' },
      ],
    })
    apiState.fetchMessageStatus.mockResolvedValue({ message_id: 'legacy-message-1', status: 'completed', text: '旧会话的回复' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      expect(wrapper.text()).toContain('旧会话中的问题')
      expect(apiState.sendMessage).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(2_000)
      await flushPromises()

      expect(apiState.fetchMessageStatus).toHaveBeenCalledWith('stored-session-token', 'legacy-message-1')
      expect(wrapper.text()).toContain('旧会话的回复')
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：旧失败任务没有幂等键时，应明确提示访客重新发送，且不展示无法执行的重试按钮。
  it('explains that a legacy failed message without a client message ID cannot be retried', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      messages: [
        { id: 'legacy-message-2', direction: 'visitor', text: '旧失败问题', task_status: 'failed' },
      ],
    })
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.text()).toContain('此历史消息无法重试，请重新发送问题。')
    expect(wrapper.findAll('button').some(button => button.text().includes('重试'))).toBe(false)
    wrapper.unmount()
  })

  // 场景：刷新恢复失败任务时，必须保留原 client_message_id 并重新展示访客可点击的重试操作。
  it('restores a failed message with a retry action after the public chat page refreshes', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    apiState.fetchSession.mockResolvedValue({
      resolution_status: 'unknown',
      messages: [
        { id: 'message-1', direction: 'visitor', text: '刷新后的失败问题', client_message_id: 'client-message-1', task_status: 'failed' },
      ],
    })
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'queued' })
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.text()).toContain('回复失败，请重试。')
    await wrapper.findAll('button').find(button => button.text().includes('重试'))?.trigger('click')
    await flushPromises()

    expect(apiState.sendMessage).toHaveBeenCalledWith('stored-session-token', expect.objectContaining({ client_message_id: 'client-message-1', text: '刷新后的失败问题' }))
    expect(wrapper.findAll('.message-row.visitor')).toHaveLength(1)
    wrapper.unmount()
  })

  // 场景：页面卸载后必须取消未完成任务的定时轮询，不能由旧页面继续调用公开状态接口。
  it('stops polling queued messages after the chat page unmounts', async () => {
    vi.useFakeTimers()
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'queued' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      await wrapper.find('textarea').setValue('离开页面')
      await wrapper.find('form.composer').trigger('submit')
      await flushPromises()
      wrapper.unmount()
      await vi.advanceTimersByTimeAsync(2_000)

      expect(apiState.fetchMessageStatus).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  // 场景：任务失败后点击重试必须复用原 client_message_id，且页面中只保留原来的访客消息。
  it('retries a failed task with the original client message id without duplicating the visitor message', async () => {
    vi.useFakeTimers()
    apiState.sendMessage
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'failed' })
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'queued' })
    apiState.fetchMessageStatus.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '重试成功' })
    try {
      const wrapper = mountPublicChat()
      await flushPromises()

      await wrapper.find('textarea').setValue('失败后重试')
      await wrapper.find('form.composer').trigger('submit')
      await flushPromises()
      const firstPayload = apiState.sendMessage.mock.calls[0][1]

      expect(wrapper.text()).toContain('回复失败，请重试。')
      await wrapper.findAll('button').find(button => button.text().includes('重试'))?.trigger('click')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(2_000)
      await flushPromises()

      expect(apiState.sendMessage).toHaveBeenCalledTimes(2)
      expect(apiState.sendMessage.mock.calls[1][1].client_message_id).toBe(firstPayload.client_message_id)
      expect(apiState.fetchMessageStatus).toHaveBeenCalledWith('session-token', 'message-1')
      expect(wrapper.findAll('.message-row.visitor')).toHaveLength(1)
      expect(wrapper.text()).toContain('重试成功')
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
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
    expect(apiState.declineLead).not.toHaveBeenCalled()
  })

  // 场景：高意向回复才展示留资邀请；访客拒绝后仍可继续匿名咨询，且不再阻塞输入框。
  it('shows a server-driven lead invitation with a decline path', async () => {
    apiState.fetchConfig.mockResolvedValue({
      name: '售前接待', greeting: '您好', privacy_mode: 'notice', lead_fields: [
        { field_key: 'phone', label: '联系电话', field_type: 'phone', required: true },
      ],
    })
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '可以为您安排演示', next_action: 'offer_lead' })
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.find('textarea').attributes('disabled')).toBeUndefined()
    await wrapper.find('textarea').setValue('想预约演示')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('暂不留资，继续咨询')
    await wrapper.findAll('button').find(button => button.text().includes('暂不留资'))?.trigger('click')
    await flushPromises()
    expect(apiState.declineLead).toHaveBeenCalledWith('session-token')
    expect(wrapper.find('.lead-gate').exists()).toBe(false)
    expect(wrapper.find('textarea').attributes('disabled')).toBeUndefined()
  })

  // 场景：公开网络补充内容必须贴上“未经企业确认”标签，避免访客误以为是企业承诺。
  it('labels unconfirmed web sources on an assistant reply', async () => {
    apiState.sendMessage.mockResolvedValue({
      message_id: 'message-1', status: 'completed', text: '公开资料显示',
      sources: [{ reference_id: 'web-1', title: '公开网页', unconfirmed: true }],
    })
    const wrapper = mountPublicChat()
    await flushPromises()
    await wrapper.find('textarea').setValue('查询')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('公开网页')
    expect(wrapper.text()).toContain('公开网络，未经企业确认')
  })

  // 场景：服务端要求确认解决状态时，访客可从会话级卡片标记结果或继续咨询。
  it('marks resolution from the server-driven resolution card and can dismiss it', async () => {
    apiState.sendMessage.mockResolvedValue({ message_id: 'message-1', status: 'completed', text: '已回答', next_action: 'ask_resolution' })
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('问题')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('已解决')
    expect(wrapper.text()).toContain('未解决')

    await wrapper.findAll('button').find(button => button.text().includes('未解决'))?.trigger('click')
    await flushPromises()
    await nextTick()

    expect(apiState.updateResolution).toHaveBeenLastCalledWith('session-token', 'unresolved')
    expect(apiState.declineLead).not.toHaveBeenCalled()
    expect(wrapper.find('.resolution-card').exists()).toBe(false)
  })

  // 场景：第二条非拒答回复被服务端标记为 ask_resolution 后，“继续咨询”只收起卡片，输入区仍能发送下一轮消息。
  it('hides the second-response resolution card and keeps the composer usable', async () => {
    apiState.sendMessage
      .mockResolvedValueOnce({ message_id: 'message-1', status: 'completed', text: '第一条回复' })
      .mockResolvedValueOnce({ message_id: 'message-2', status: 'completed', text: '第二条回复', next_action: 'ask_resolution' })
      .mockResolvedValueOnce({ message_id: 'message-3', status: 'completed', text: '继续答复' })
    const wrapper = mountPublicChat()
    await flushPromises()
    await wrapper.find('textarea').setValue('第一个问题')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await wrapper.find('textarea').setValue('第二个问题')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()

    expect(wrapper.find('.resolution-card').exists()).toBe(true)
    await wrapper.findAll('button').find(button => button.text().includes('继续咨询'))?.trigger('click')
    expect(wrapper.find('.resolution-card').exists()).toBe(false)
    expect(wrapper.find('textarea').attributes('disabled')).toBeUndefined()
    await wrapper.find('textarea').setValue('补充问题')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    expect(apiState.sendMessage).toHaveBeenLastCalledWith('session-token', expect.objectContaining({ text: '补充问题' }))
  })

  // 场景：顶部次要操作必须结束本次咨询，清除续接凭证并禁止继续发送，而不是静默新建会话。
  it('ends the current consultation instead of starting a new conversation', async () => {
    apiState.readStoredSession.mockReturnValue('stored-session-token')
    const wrapper = mountPublicChat()
    await flushPromises()

    await wrapper.find('textarea').setValue('旧会话消息')
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    await nextTick()
    expect(wrapper.text()).toContain('旧会话消息')

    await wrapper.findAll('button').find(button => button.text().includes('结束本次咨询'))?.trigger('click')
    await nextTick()

    expect(apiState.clearStoredSession).toHaveBeenCalledWith('public-token', 'web_link')
    expect(apiState.createSession).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('旧会话消息')
    expect(apiState.createSession).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('本次咨询已结束')
    expect(wrapper.find('textarea').attributes('disabled')).toBeDefined()
  })

  // 场景：窄屏下公开页必须保留可收缩的消息区和输入区，不因头部操作或长消息产生横向页面滚动。
  it('keeps mobile chat containers shrinkable for narrow screens', async () => {
    const wrapper = mountPublicChat()
    await flushPromises()

    expect(wrapper.find('.public-chat').exists()).toBe(true)
    expect(wrapper.find('.chat-window').exists()).toBe(true)
    expect(wrapper.find('.composer').exists()).toBe(true)
    await wrapper.find('textarea').setValue('https://example.com/' + 'unbroken-token-'.repeat(80))
    await wrapper.find('form.composer').trigger('submit')
    await flushPromises()
    expect(wrapper.find('.message-row.visitor .bubble').text()).toContain('unbroken-token')
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
