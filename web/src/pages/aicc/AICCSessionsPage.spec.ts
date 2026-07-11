import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, nextTick, type Component } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import type { AICCSession, AICCSessionFilters, AICCSessionListResult } from '@/domain/aicc'
import AICCSessionsPage from './AICCSessionsPage.vue'

const routeState = vi.hoisted(() => {
  const { reactive } = require('vue') as typeof import('vue')
  return {
    route: reactive({ query: {} as Record<string, unknown> }),
    replace: vi.fn(),
  }
})

const queryState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')
  return {
    sessionFilters: undefined as { value: AICCSessionFilters } | undefined,
    sessions: { data: ref({ sessions: [], total: 0 } as AICCSessionListResult), isLoading: ref(false), error: ref(null) },
    detail: { data: ref(null), isLoading: ref(false), error: ref(null) },
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => routeState.route,
  useRouter: () => ({ replace: routeState.replace }),
}))

vi.mock('@/api/hooks/useAICC', () => ({
  useAICCSessionsQuery: (_agentId: unknown, filters: { value: AICCSessionFilters }) => {
    queryState.sessionFilters = filters
    return queryState.sessions
  },
  useAICCSessionQuery: () => queryState.detail,
}))

vi.mock('naive-ui', () => {
  const { defineComponent, h } = require('vue') as typeof import('vue')
  const Passthrough = defineComponent({
    setup(_, { slots }) {
      return () => h('div', slots.default?.())
    },
  })
  const Input = defineComponent({
    props: ['value', 'placeholder'],
    emits: ['update:value'],
    setup(props, { emit }) {
      return () => h('input', {
        value: props.value ?? '',
        placeholder: props.placeholder as string,
        onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
      })
    },
  })
  const Select = defineComponent({
    props: ['value', 'options', 'placeholder'],
    emits: ['update:value'],
    setup(props, { emit }) {
      return () => h('label', [
        h('span', String(props.placeholder ?? '')),
        h('select', {
          value: props.value ?? '',
          onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value || null),
        }, [
          h('option', { value: '' }, ''),
          ...(props.options ?? []).map((option: { label: string; value: string }) => h('option', {
            value: option.value,
          }, option.label)),
        ]),
      ])
    },
  })
  const Pagination = defineComponent({
    props: ['page', 'pageSize', 'itemCount'],
    emits: ['update:page', 'update:pageSize'],
    setup(props, { emit }) {
      return () => h('nav', [
        h('span', `total:${props.itemCount ?? 0}`),
        h('button', { type: 'button', onClick: () => emit('update:page', Number(props.page ?? 1) + 1) }, '下一页'),
        h('button', { type: 'button', onClick: () => emit('update:pageSize', 50) }, '每页50'),
      ])
    },
  })
  return {
    NAlert: Passthrough,
    NDatePicker: Input,
    NInput: Input,
    NPagination: Pagination,
    NSelect: Select,
    NSpin: Passthrough,
    NTag: Passthrough,
  }
})

const SelectStub = defineComponent({
  props: ['value', 'options', 'placeholder'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('label', [
      h('span', String(props.placeholder ?? '')),
      h('select', {
        value: props.value ?? '',
        onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value || null),
      }, [
        h('option', { value: '' }, ''),
        ...(props.options ?? []).map((option: { label: string; value: string }) => h('option', {
          value: option.value,
        }, option.label)),
      ]),
    ])
  },
})

const InputStub = defineComponent({
  props: ['value', 'placeholder'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('input', {
      value: props.value ?? '',
      placeholder: props.placeholder as string,
      onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
    })
  },
})

const DatePickerStub = defineComponent({
  setup() {
    return () => h('input', { type: 'text' })
  },
})

const PassthroughStub = defineComponent({
  setup(_, { slots }) {
    return () => h('div', slots.default?.())
  },
})

const PaginationStub = defineComponent({
  props: ['page', 'pageSize', 'itemCount'],
  emits: ['update:page', 'update:pageSize'],
  setup(props, { emit }) {
    return () => h('nav', [
      h('span', `total:${props.itemCount ?? 0}`),
      h('button', { type: 'button', onClick: () => emit('update:page', Number(props.page ?? 1) + 1) }, '下一页'),
      h('button', { type: 'button', onClick: () => emit('update:pageSize', 50) }, '每页50'),
    ])
  },
})

function mountSessions() {
  i18n.global.locale.value = 'zh'
  return mount(AICCSessionsPage, {
    props: { agentId: 'agent-sales' },
    global: {
      plugins: [i18n],
      stubs: {
        NAlert: PassthroughStub,
        NDatePicker: DatePickerStub,
        NInput: InputStub,
        NPagination: PaginationStub,
        NSelect: SelectStub,
        NSpin: PassthroughStub,
        NTag: PassthroughStub,
        'n-alert': PassthroughStub,
        'n-date-picker': DatePickerStub,
        'n-input': InputStub,
        'n-pagination': PaginationStub,
        'n-select': SelectStub,
        'n-spin': PassthroughStub,
        'n-tag': PassthroughStub,
      } satisfies Record<string, Component>,
    },
  })
}

describe('AICCSessionsPage', () => {
  beforeEach(() => {
    routeState.route.query = {}
    routeState.replace.mockReset()
    queryState.sessionFilters = undefined
    queryState.sessions.data.value = { sessions: [], total: 0 }
    queryState.sessions.isLoading.value = false
    queryState.sessions.error.value = null
    queryState.detail.data.value = null
    queryState.detail.isLoading.value = false
    queryState.detail.error.value = null
  })

  // 覆盖当前渠道能力边界：AICC 暂不支持语音客服，管理端渠道筛选只能展示已可用入口。
  it('only renders currently supported public channel filters', () => {
    const wrapper = mountSessions()

    const channelOptions = wrapper.findAll('select')[2].findAll('option').map(option => option.text())
    expect(channelOptions).toContain('公开链接')
    expect(channelOptions).toContain('网页挂件')
    expect(channelOptions).not.toContain('语音客服')
  })

  // 覆盖旧链接兼容：历史 URL 带 channel=voice 时不应继续把未支持渠道传给会话查询。
  it('ignores stale voice channel query values', async () => {
    routeState.route.query = { channel: 'voice' }

    mountSessions()
    await nextTick()

    expect(queryState.sessionFilters?.value.channel).toBeUndefined()
  })

  // 覆盖分页默认值：列表初次查询只加载第一页，避免一次性拉取全部会话。
  it('queries the first page with default pagination parameters', () => {
    mountSessions()

    expect(queryState.sessionFilters?.value.limit).toBe(20)
    expect(queryState.sessionFilters?.value.offset).toBe(0)
  })

  // 覆盖分页交互：点击下一页后，查询条件中的 offset 按当前 pageSize 推进。
  it('updates query offset when changing pages', async () => {
    queryState.sessions.data.value = { sessions: makeSessions(25), total: 25 }
    const wrapper = mountSessions()

    await wrapper.find('nav button').trigger('click')
    await nextTick()

    expect(queryState.sessionFilters?.value.limit).toBe(20)
    expect(queryState.sessionFilters?.value.offset).toBe(20)
  })

  // 覆盖筛选交互：用户切换筛选条件后回到第一页，避免沿用旧分页偏移导致列表为空。
  it('resets to the first page when filters change', async () => {
    queryState.sessions.data.value = { sessions: makeSessions(25), total: 25 }
    const wrapper = mountSessions()
    await wrapper.find('nav button').trigger('click')
    await nextTick()

    await wrapper.findAll('select')[0].setValue('resolved')
    await nextTick()

    expect(queryState.sessionFilters?.value.resolution_status).toBe('resolved')
    expect(queryState.sessionFilters?.value.offset).toBe(0)
  })

  // 覆盖会话解决状态展示：未知状态显示为“跟进中”，与筛选项文案保持一致。
  it('renders resolution labels consistently with session statuses', () => {
    queryState.sessions.data.value = {
      sessions: [
        { id: 'session-resolved', agent_id: 'agent-sales', resolution_status: 'resolved', message_count: 1 },
        { id: 'session-unresolved', agent_id: 'agent-sales', resolution_status: 'unresolved', message_count: 1 },
        { id: 'session-unknown', agent_id: 'agent-sales', resolution_status: 'unknown', message_count: 1 },
      ],
      total: 3,
    }

    const wrapper = mountSessions()

    expect(wrapper.text()).toContain('已解决')
    expect(wrapper.text()).toContain('未解决')
    expect(wrapper.text()).toContain('跟进中')
  })
})

function makeSessions(count: number): AICCSession[] {
  return Array.from({ length: count }, (_, index) => ({
    id: `session-${index + 1}`,
    agent_id: 'agent-sales',
    resolution_status: 'unknown',
    message_count: 1,
  }))
}
