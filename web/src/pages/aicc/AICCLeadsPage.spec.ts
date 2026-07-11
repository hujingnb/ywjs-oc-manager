import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, type Ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import type { AICCLead, AICCSessionDetail } from '@/domain/aicc'
import AICCLeadsPage from './AICCLeadsPage.vue'

const queryState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')
  return {
    leads: { data: ref([] as AICCLead[]), isLoading: ref(false), error: ref(null) },
    detail: { data: ref(null as AICCSessionDetail | null), isLoading: ref(false), error: ref(null) },
    selectedSessionId: undefined as Ref<string | undefined> | undefined,
    markRead: vi.fn(),
    download: vi.fn(),
  }
})

vi.mock('@/api/hooks/useAICC', () => ({
  useAICCLeadsQuery: () => queryState.leads,
  useAICCSessionQuery: (sessionId: Ref<string | undefined>) => {
    queryState.selectedSessionId = sessionId
    return queryState.detail
  },
  useMarkAICCLeadRead: () => ({
    isPending: computed(() => false),
    mutateAsync: queryState.markRead,
  }),
  downloadAICCLeadsCSV: queryState.download,
}))

vi.mock('naive-ui', () => {
  const { defineComponent, h } = require('vue') as typeof import('vue')
  const Passthrough = defineComponent({
    setup(_, { slots }) {
      return () => h('div', slots.default?.())
    },
  })
  const Button = defineComponent({
    props: ['disabled', 'loading'],
    emits: ['click'],
    setup(props, { slots, emit }) {
      return () => h('button', {
        type: 'button',
        disabled: Boolean(props.disabled || props.loading),
        onClick: () => emit('click'),
      }, [slots.icon?.(), slots.default?.()])
    },
  })
  return {
    NAlert: Passthrough,
    NButton: Button,
    NSpace: Passthrough,
    NSpin: Passthrough,
    NTag: Passthrough,
    useMessage: () => ({ success: vi.fn(), error: vi.fn() }),
  }
})

const TagStub = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  },
})

function mountLeadsPage() {
  i18n.global.locale.value = 'zh'
  return mount(AICCLeadsPage, {
    global: {
      plugins: [i18n],
      stubs: {
        NTag: TagStub,
        'n-tag': TagStub,
      },
    },
  })
}

describe('AICCLeadsPage', () => {
  beforeEach(() => {
    queryState.leads.data.value = [{
      id: 'lead-1',
      latest_session_id: 'session-1',
      display_name: '张三',
      unread: true,
      values: [{ field_key: 'phone', label: '联系电话', value: '13800138000' }],
      updated_at: '2026-07-11T10:00:00Z',
    }]
    queryState.detail.data.value = {
      session: { id: 'session-1', agent_id: 'agent-1', message_count: 2, resolution_status: 'unknown' },
      lead_values: [{ field_key: 'phone', label: '联系电话', value: '13800138000' }],
      messages: [
        { id: 'msg-1', direction: 'visitor', text: '我想了解报价', created_at: '2026-07-11T10:00:00Z' },
        { id: 'msg-2', direction: 'assistant', text: '您好，请问需要哪个版本？', created_at: '2026-07-11T10:00:01Z' },
      ],
    }
    queryState.selectedSessionId = undefined
    queryState.markRead.mockReset()
    queryState.markRead.mockResolvedValue('lead-1')
    queryState.download.mockReset()
  })

  // 场景：运营人员在线索列表中核对线索来源时，应能直接查看关联会话内容并自动标记已读。
  it('opens the latest session transcript from a lead row', async () => {
    const wrapper = mountLeadsPage()

    const button = wrapper.findAll('button').find(item => item.text().includes('查看对话'))
    expect(button).toBeTruthy()
    await button?.trigger('click')

    expect(queryState.selectedSessionId?.value).toBe('session-1')
    expect(queryState.markRead).toHaveBeenCalledWith('lead-1')
    expect(wrapper.text()).toContain('我想了解报价')
    expect(wrapper.text()).toContain('您好，请问需要哪个版本？')
    expect(wrapper.text()).toContain('联系电话')
  })
})
