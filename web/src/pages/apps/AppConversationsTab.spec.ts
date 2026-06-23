// AppConversationsTab.spec.ts —— AppConversationsTab 顶层组件单元测试。
// 覆盖：加载并展示会话列表；选中会话加载消息；发送消息流式回复；重命名调用 API 并刷新。
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { NButton, NInput, NModal, NSpace, NTag } from 'naive-ui'

import { i18n } from '@/i18n'
import AppConversationsTab from './AppConversationsTab.vue'

// mock naive-ui useMessage：jsdom 环境无 NMessageProvider，通过 mock 避免缺 provider 报错。
vi.mock('naive-ui', async (importOriginal) => {
  const original = await importOriginal<typeof import('naive-ui')>()
  return {
    ...original,
    useMessage: () => ({
      success: vi.fn(),
      error: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    }),
  }
})

// 准备会话和消息测试数据。
const mockSessions = [
  { id: 'sess-1', source: 'web', title: '测试会话一', message_count: 2 },
  { id: 'sess-2', source: 'wechat', title: '测试会话二', message_count: 0 },
]
const mockMessages = [
  { role: 'user', content: '你好' },
  { role: 'assistant', content: '你好！有什么可以帮您？' },
]

// mock @/api/conversations：提供所有 API 函数的 vi.fn() 桩。
const mockListConversations = vi.fn()
const mockListMessages = vi.fn()
const mockCreateConversation = vi.fn()
const mockRenameConversation = vi.fn()
const mockDeleteConversation = vi.fn()
const mockChatStream = vi.fn()

vi.mock('@/api/conversations', () => ({
  listConversations: (...args: unknown[]) => mockListConversations(...args),
  listMessages: (...args: unknown[]) => mockListMessages(...args),
  createConversation: (...args: unknown[]) => mockCreateConversation(...args),
  renameConversation: (...args: unknown[]) => mockRenameConversation(...args),
  deleteConversation: (...args: unknown[]) => mockDeleteConversation(...args),
  chatStream: (...args: unknown[]) => mockChatStream(...args),
}))

// mountTab 封装挂载逻辑。
// 使用 Naive UI 的实际组件名（如 'Input'、'Button'）而非 Vue 注册名（如 'NInput'、'NButton'）来 stub，
// 确保组件解析时能正确命中 stub，避免真实组件渲染复杂 DOM 导致选择器失效。
function mountTab() {
  return mount(AppConversationsTab, {
    props: { appId: 'app-test-1' },
    global: {
      plugins: [i18n],
      stubs: {
        // ConversationMessageView：渲染消息 content 字符串供断言。
        ConversationMessageView: {
          props: ['message'],
          template: '<span class="msg-stub">{{ typeof message.content === "string" ? message.content : "" }}</span>',
        },
        // 使用 Naive UI 组件的内部名称（无 N 前缀）进行 stub，与 Vue Test Utils 解析逻辑一致。
        // NButton 的内部名为 'Button'，NInput 的内部名为 'Input'，以此类推。
        [NButton.name!]: {
          template: '<button @click="$emit(\'click\')"><slot /></button>',
          emits: ['click'],
        },
        [NInput.name!]: {
          // NInput stub：同时接受 value 和 modelValue，支持 v-model:value 绑定。
          props: ['value', 'modelValue', 'placeholder', 'disabled', 'type', 'autosize'],
          emits: ['update:value', 'update:modelValue'],
          template: `<input
            data-stub-input
            :value="value ?? modelValue ?? ''"
            :disabled="disabled || undefined"
            @input="$emit('update:value', $event.target.value)"
          />`,
        },
        [NModal.name!]: {
          // NModal stub：show=true 时渲染 data-modal 容器，含 slot 和 footer slot。
          props: ['show', 'title'],
          template: '<div v-if="show" data-modal><slot /><slot name="footer" /></div>',
        },
        [NSpace.name!]: { template: '<div><slot /></div>' },
        [NTag.name!]: { template: '<span><slot /></span>' },
      },
    },
  })
}

describe('AppConversationsTab', () => {
  beforeEach(() => {
    // 重置所有 mock 函数，防止测试间状态污染。
    vi.clearAllMocks()
    // 设置中文语言，确保文案断言与翻译文件对齐。
    i18n.global.locale.value = 'zh'
    // 默认：listConversations 返回两条会话，listMessages 返回空列表。
    mockListConversations.mockResolvedValue(mockSessions)
    mockListMessages.mockResolvedValue([])
  })

  // 覆盖：组件挂载后自动调用 listConversations，并将返回的会话渲染到 DOM 中。
  // 通过 data-test 属性定位各会话条目，验证标题可见性。
  it('挂载后加载并显示会话列表', async () => {
    const wrapper = mountTab()
    await flushPromises()

    // 验证 API 被调用一次且参数正确。
    expect(mockListConversations).toHaveBeenCalledOnce()
    expect(mockListConversations).toHaveBeenCalledWith('app-test-1')

    // 验证两条会话的 data-test 元素出现在 DOM 中。
    expect(wrapper.find('[data-test="session-sess-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="session-sess-2"]').exists()).toBe(true)

    // 验证会话标题出现在文本内容中。
    const text = wrapper.text()
    expect(text).toContain('测试会话一')
    expect(text).toContain('测试会话二')
  })

  // 覆盖：点击会话条目后调用 listMessages，并将返回的消息渲染到消息区域。
  it('选中会话后加载并显示消息历史', async () => {
    mockListMessages.mockResolvedValue(mockMessages)
    const wrapper = mountTab()
    await flushPromises()

    // 点击第一条会话。
    await wrapper.find('[data-test="session-sess-1"]').trigger('click')
    await flushPromises()

    // 验证 listMessages 以正确参数被调用。
    expect(mockListMessages).toHaveBeenCalledWith('app-test-1', 'sess-1')

    // 验证消息内容出现在渲染文本中（通过 ConversationMessageView stub 中的 span.msg-stub 展示）。
    const text = wrapper.text()
    expect(text).toContain('你好')
    expect(text).toContain('你好！有什么可以帮您？')
  })

  // 覆盖：点击发送后调用 chatStream；mock chatStream 同步触发 onDelta('ok') + onDone()，
  // 断言流式回复内容出现在渲染结果中，完成后重新拉取消息列表。
  it('发送消息：流式回复累积 delta，完成后重拉消息', async () => {
    // chatStream mock：同步触发 onDelta 和 onDone，模拟流式回调。
    mockChatStream.mockImplementation(
      async (
        _appId: string,
        _sid: string,
        _msg: string,
        cb: { onDelta: (d: string) => void; onDone: () => void },
      ) => {
        cb.onDelta('ok')
        cb.onDone()
      },
    )
    // 发送完成后重拉消息（第一次 selectSession 返回空，第二次 sendMessage 后含回复）。
    mockListMessages
      .mockResolvedValueOnce([]) // selectSession 初次加载
      .mockResolvedValueOnce([  // sendMessage 后重拉
        { role: 'user', content: 'hello' },
        { role: 'assistant', content: 'ok' },
      ])

    const wrapper = mountTab()
    await flushPromises()

    // 先选中会话，使 currentId 有值、输入框可用。
    await wrapper.find('[data-test="session-sess-1"]').trigger('click')
    await flushPromises()

    // 找到 composer 区域的 NInput stub（data-stub-input），填入消息文本。
    const composerInput = wrapper.find('[data-stub-input]')
    expect(composerInput.exists()).toBe(true)
    await composerInput.setValue('hello')

    // 点击发送按钮。
    await wrapper.find('[data-test="send"]').trigger('click')
    await flushPromises()

    // 验证 chatStream 被以正确参数调用。
    expect(mockChatStream).toHaveBeenCalledWith(
      'app-test-1',
      'sess-1',
      'hello',
      expect.objectContaining({ onDelta: expect.any(Function), onDone: expect.any(Function) }),
    )

    // 流结束后应重新拉取消息（共 2 次：selectSession 1 次 + sendMessage 后 1 次）。
    expect(mockListMessages).toHaveBeenCalledTimes(2)

    // 渲染结果中应包含重拉后的 assistant 内容。
    const text = wrapper.text()
    expect(text).toContain('ok')
  })

  // 覆盖：点击「重命名」按钮后弹窗打开；填入新标题并点击确认后，
  // 调用 renameConversation 并刷新会话列表。
  it('重命名会话：调用 renameConversation 并重新加载列表', async () => {
    mockRenameConversation.mockResolvedValue({ id: 'sess-1', source: 'web', title: '新名称' })
    // 重命名后 listConversations 返回更新后的列表。
    mockListConversations
      .mockResolvedValueOnce(mockSessions) // 初次挂载加载
      .mockResolvedValueOnce([{ id: 'sess-1', source: 'web', title: '新名称' }, mockSessions[1]]) // 重命名后刷新

    const wrapper = mountTab()
    await flushPromises()

    // 找到第一条会话的「重命名」按钮；session-actions 内第一个按钮。
    // @click.stop 阻止事件冒泡，不会触发 selectSession。
    const renameBtn = wrapper.find('.session-actions button')
    await renameBtn.trigger('click')
    await flushPromises()

    // 弹窗应已打开（NModal stub 在 show=true 时渲染 data-modal 容器）。
    expect(wrapper.find('[data-modal]').exists()).toBe(true)

    // 在弹窗内的 NInput stub 填入新标题。
    const modalInput = wrapper.find('[data-modal] [data-stub-input]')
    expect(modalInput.exists()).toBe(true)
    await modalInput.setValue('新名称')

    // 点击弹窗 footer 内的最后一个按钮（「确认」）。
    const confirmBtn = wrapper.findAll('[data-modal] button').at(-1)
    await confirmBtn!.trigger('click')
    await flushPromises()

    // 验证 renameConversation 被调用，参数含正确的新标题。
    expect(mockRenameConversation).toHaveBeenCalledWith('app-test-1', 'sess-1', '新名称')
    // 验证列表刷新（listConversations 共调用 2 次）。
    expect(mockListConversations).toHaveBeenCalledTimes(2)
  })
})
