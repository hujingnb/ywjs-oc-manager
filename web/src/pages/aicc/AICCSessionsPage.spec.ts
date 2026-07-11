import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, nextTick, type Component } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import type { AICCSessionFilters } from '@/domain/aicc'
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
    sessions: { data: ref([]), isLoading: ref(false), error: ref(null) },
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
  return {
    NAlert: Passthrough,
    NDatePicker: Input,
    NInput: Input,
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
        NSelect: SelectStub,
        NSpin: PassthroughStub,
        NTag: PassthroughStub,
        'n-alert': PassthroughStub,
        'n-date-picker': DatePickerStub,
        'n-input': InputStub,
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
    queryState.sessions.data.value = []
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
})
