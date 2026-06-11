import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import TicketTargetsEditor from './TicketTargetsEditor.vue'

vi.mock('naive-ui', async () => {
  const { defineComponent: dc, h: _h } = await import('vue')
  const NSelect = dc({
    props: ['value', 'options'],
    emits: ['update:value'],
    setup(props, { emit }) {
      return () =>
        _h('div', { class: 'n-select' }, [
          _h('span', String(props.value ?? '')),
          ...(props.options ?? []).map((option: { label: string; value: string }) =>
            _h('button', { class: `select-${option.value}`, onClick: () => emit('update:value', option.value) }, option.label),
          ),
        ])
    },
  })
  return {
    NSelect,
    NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
  }
})

function mountEditor() {
  return mount(
    defineComponent({
      components: { TicketTargetsEditor },
      data: () => ({
        targets: [{ org_id: 'org-1', audience: 'all_org' }],
        orgs: [
          { id: 'org-1', name: '甲公司' },
          { id: 'org-2', name: '乙公司' },
        ],
      }),
      render() {
        return h(TicketTargetsEditor, {
          modelValue: this.targets,
          orgs: this.orgs,
          'onUpdate:modelValue': (value) => {
            this.targets = value
          },
        })
      },
    }),
  )
}

describe('TicketTargetsEditor', () => {
  // 编辑器应渲染现有目标,修改受众时通过 v-model 更新数组。
  it('renders rows and emits update on audience change', async () => {
    const wrapper = mountEditor()
    expect(wrapper.text()).toContain('甲公司')
    expect(wrapper.text()).toContain('仅企业管理员')
    expect(wrapper.text()).not.toContain('仅管理员')
    await wrapper.find('.select-org_admins').trigger('click')
    expect(wrapper.vm.$data.targets[0].audience).toBe('org_admins')
  })

  // 加组织追加一条默认 all_org 目标,移除按钮删除对应目标。
  it('adds and removes target rows', async () => {
    const wrapper = mountEditor()
    await wrapper.find('.select-org-2').trigger('click')
    await wrapper.findAll('button').find((btn) => btn.text() === '加组织')!.trigger('click')
    expect(wrapper.vm.$data.targets).toHaveLength(2)
    expect(wrapper.vm.$data.targets[1]).toEqual({ org_id: 'org-2', audience: 'all_org' })

    await wrapper.findAll('button').find((btn) => btn.text() === '移除')!.trigger('click')
    expect(wrapper.vm.$data.targets).toHaveLength(1)
    expect(wrapper.vm.$data.targets[0].org_id).toBe('org-2')
  })
})
