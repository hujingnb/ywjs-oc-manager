import { mount } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ref } from 'vue'

import TicketConversation from './TicketConversation.vue'
import { i18n } from '@/i18n'

const mocks = vi.hoisted(() => ({
  send: vi.fn(),
  upload: vi.fn(),
  download: vi.fn(),
  fetchUrl: vi.fn(),
  messageError: vi.fn(),
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  useSendTicketMessage: () => ({ mutateAsync: mocks.send, isPending: ref(false) }),
  useUploadTicketMessage: () => ({ mutateAsync: mocks.upload, isPending: ref(false) }),
  downloadTicketMessage: mocks.download,
  fetchTicketMessageBlobUrl: mocks.fetchUrl,
}))

vi.mock('naive-ui', () => ({
  useMessage: () => ({ error: mocks.messageError }),
  NInput: {
    props: ['value'],
    emits: ['update:value'],
    template: '<textarea class="n-input" :value="value" @input="$emit(\'update:value\', $event.target.value)"></textarea>',
  },
  NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
  NModal: {
    props: ['show'],
    emits: ['update:show'],
    template: '<div v-if="show" class="n-modal"><slot /><button class="modal-close" @click="$emit(\'update:show\', false)">关闭</button></div>',
  },
}))

describe('TicketConversation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.fetchUrl.mockResolvedValue('blob:image')
    globalThis.URL.revokeObjectURL = vi.fn()
    // locale 设为 zh，使文案断言沿用中文词条。
    i18n.global.locale.value = 'zh'
  })

  // text 消息按作者区分左右气泡,并显示发送时间。
  it('renders text bubbles aligned by author with timestamp', () => {
    const wrapper = mount(TicketConversation, {
      props: {
        ticketId: 't-1',
        currentUserId: 'me',
        messages: [
          { id: 'm1', kind: 'text', text: '我的消息', author_user_id: 'me', created_at: '2026-06-11T01:00:00Z' },
          { id: 'm2', kind: 'text', text: '对方消息', author_user_id: 'other', created_at: '2026-06-11T01:01:00Z' },
        ],
      },
      global: { plugins: [i18n] },
    })
    expect(wrapper.text()).toContain('我的消息')
    expect(wrapper.text()).toContain('对方消息')
    expect(wrapper.findAll('.message-item.mine')).toHaveLength(1)
    expect(wrapper.findAll('.message-item.theirs')).toHaveLength(1)
  })

  // image 消息渲染缩略图,file 消息渲染文件名与大小并可点击下载。
  it('renders image thumbnail and file download', async () => {
    const wrapper = mount(TicketConversation, {
      props: {
        ticketId: 't-1',
        currentUserId: 'me',
        messages: [
          { id: 'img', kind: 'image', file_name: '图.png', author_user_id: 'other' },
          { id: 'file', kind: 'file', file_name: '需求.pdf', file_size: 2048, author_user_id: 'other' },
        ],
      },
      global: { plugins: [i18n] },
    })
    await vi.waitFor(() => expect(mocks.fetchUrl).toHaveBeenCalled())
    expect(wrapper.find('img').attributes('src')).toBe('blob:image')
    expect(wrapper.text()).toContain('需求.pdf')
    await wrapper.find('.file-message').trigger('click')
    expect(mocks.download).toHaveBeenCalledWith('t-1', expect.objectContaining({ id: 'file' }))
  })

  // 图片消息点击后打开大图预览弹层，不再触发原文件下载。
  it('opens image preview modal when clicking image message', async () => {
    const wrapper = mount(TicketConversation, {
      props: {
        ticketId: 't-1',
        currentUserId: 'me',
        messages: [
          { id: 'img', kind: 'image', file_name: '图.png', author_user_id: 'other' },
        ],
      },
      global: { plugins: [i18n] },
    })
    await vi.waitFor(() => expect(wrapper.find('.image-message img').exists()).toBe(true))

    await wrapper.find('.image-message').trigger('click')

    const preview = wrapper.find('.image-preview-modal img')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('blob:image')
    expect(preview.attributes('alt')).toBe('图.png')
    expect(mocks.download).not.toHaveBeenCalled()
  })

  // composer 输入文本点发送调用 send hook;选择文件调用 upload hook。
  it('sends text and uploads file via composer', async () => {
    const wrapper = mount(TicketConversation, {
      props: { ticketId: 't-1', currentUserId: 'me', messages: [] },
      global: { plugins: [i18n] },
    })
    await wrapper.find('textarea').setValue('新消息')
    await wrapper.findAll('button').find((button) => button.text() === '发送')!.trigger('click')
    expect(mocks.send).toHaveBeenCalledWith({ text: '新消息' })

    const file = new File(['pdf'], '需求.pdf', { type: 'application/pdf' })
    Object.defineProperty(wrapper.find('input[type="file"]').element, 'files', { value: [file] })
    await wrapper.find('input[type="file"]').trigger('change')
    expect(mocks.upload).toHaveBeenCalledWith(file)
  })
})
