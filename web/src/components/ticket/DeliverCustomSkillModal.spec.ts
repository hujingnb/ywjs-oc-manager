import { mount } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ref } from 'vue'

import DeliverCustomSkillModal from './DeliverCustomSkillModal.vue'
import { i18n } from '@/i18n'

const mocks = vi.hoisted(() => ({
  deliver: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  useDeliverCustomSkill: () => ({ mutateAsync: mocks.deliver, isPending: ref(false) }),
}))

vi.mock('./TicketTargetsEditor.vue', () => ({
  default: { template: '<div class="targets-editor" />' },
}))

vi.mock('naive-ui', () => ({
  useMessage: () => ({ success: mocks.success, error: mocks.error }),
  NModal: { props: ['show'], template: '<div v-if="show" class="n-modal"><slot /><slot name="footer" /></div>' },
  NForm: { template: '<div><slot /></div>' },
  NFormItem: { template: '<label><slot /></label>' },
  NInput: {
    props: ['value'],
    emits: ['update:value'],
    template: '<textarea :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NButton: { template: '<button v-bind="$attrs"><slot /></button>' },
  NRadioGroup: { template: '<div><slot /></div>' },
  NRadioButton: { template: '<span><slot /></span>' },
}))

describe('DeliverCustomSkillModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // locale 设为 zh，使文案断言沿用中文词条。
    i18n.global.locale.value = 'zh'
  })

  // 粘贴 Markdown 后确认交付,应解析 frontmatter 并携带默认 targets 调用交付 hook。
  it('parses markdown and delivers with targets', async () => {
    const wrapper = mount(DeliverCustomSkillModal, {
      props: {
        show: true,
        ticket: {
          id: 't-1',
          status: 'processing',
          org_id: 'org-1',
          requester_role: 'org_member',
        },
        orgs: [{ id: 'org-1', name: '甲公司' }],
      },
      global: { plugins: [i18n] },
    })
    await wrapper.find('textarea').setValue('---\nname: weekly\n---\n# Weekly')
    await wrapper.findAll('button').find((button) => button.text() === '交付')!.trigger('click')

    expect(mocks.deliver).toHaveBeenCalledWith(expect.objectContaining({
      ticketId: 't-1',
      description: '',
      targets: [{ org_id: 'org-1', audience: 'all_org' }],
    }))
    expect(mocks.success).toHaveBeenCalledWith('已交付')
  })
})
