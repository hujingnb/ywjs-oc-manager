// KanbanCreateModal.spec.ts —— 新建任务弹窗单元测试。
// 聚焦本次新增的 assignee slug 校验：非法输入禁用提交并给出错误反馈，
// 合法输入放行并组装 trim 后的 payload。
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { describe, expect, it, vi, beforeEach } from 'vitest'

import { i18n } from '@/i18n'
import KanbanCreateModal from './KanbanCreateModal.vue'

// mock auth store：组件通过 useAuthStore().isPlatformAdmin 控制高级字段显隐，
// 这里固定为非平台管理员，测试只覆盖基础字段（标题/assignee/优先级/描述）。
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isPlatformAdmin: false }),
}))

// mock naive-ui：聚焦字段与反馈，避免 NModal teleport 干扰；NFormItem 渲染 feedback
// 与 validation-status，便于断言 assignee 校验反馈文案与错误态。
vi.mock('naive-ui', () => ({
  NModal: {
    props: ['show', 'title'],
    emits: ['update:show'],
    template: '<section v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>',
  },
  NForm: { template: '<form><slot /></form>' },
  NFormItem: {
    props: ['label', 'feedback', 'validationStatus'],
    template:
      '<label :data-status="validationStatus"><span>{{ label }}</span><slot /><em class="feedback">{{ feedback }}</em></label>',
  },
  NInput: {
    props: ['value', 'placeholder', 'type'],
    emits: ['update:value'],
    template:
      '<input :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NInputNumber: {
    props: ['value'],
    emits: ['update:value'],
    template: '<input type="number" :value="value ?? \'\'" />',
  },
  NSelect: {
    props: ['value', 'options'],
    emits: ['update:value'],
    template: '<select :value="value"></select>',
  },
  NButton: {
    props: ['disabled', 'loading', 'type'],
    emits: ['click'],
    template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
  },
  NSpace: { template: '<div><slot /></div>' },
}))

// 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
beforeEach(() => {
  i18n.global.locale.value = 'zh'
})

function mountModal() {
  // 注入 i18n 插件，使组件内 t() 调用能够解析翻译文案。
  return mount(KanbanCreateModal, { props: { show: true, submitting: false }, global: { plugins: [i18n] } })
}

// createBtn：footer 最后一个按钮即「创建」。
function createBtn(wrapper: ReturnType<typeof mountModal>) {
  return wrapper.findAll('button').at(-1)!
}

async function fillByPlaceholder(
  wrapper: ReturnType<typeof mountModal>,
  placeholder: string,
  value: string,
) {
  const input = wrapper.find(`[placeholder="${placeholder}"]`)
  expect(input.exists()).toBe(true)
  await input.setValue(value)
}

describe('KanbanCreateModal assignee 校验', () => {
  // 覆盖：assignee 为空时常驻展示格式提示，且因标题/assignee 为空禁用创建。
  it('空表单展示 assignee 格式提示并禁用创建', () => {
    const wrapper = mountModal()
    expect(wrapper.text()).toContain('小写字母/数字开头，仅含小写字母、数字、_、-')
    expect(createBtn(wrapper).attributes('disabled')).toBeDefined()
  })

  // 覆盖：assignee 含大写/空格/中文等非法字符时，给出错误反馈并禁止提交（不触达后端）。
  it('非法 assignee 显示错误反馈且禁用创建', async () => {
    const wrapper = mountModal()
    await fillByPlaceholder(wrapper, '任务标题', '测试任务')
    await fillByPlaceholder(wrapper, '如 devops、claude（小写 slug）', 'Claude')
    await nextTick()

    expect(wrapper.text()).toContain('assignee 含非法字符')
    expect(wrapper.find('label[data-status="error"]').exists()).toBe(true)
    expect(createBtn(wrapper).attributes('disabled')).toBeDefined()
  })

  // 覆盖：合法小写 slug 清除错误态、放行创建，并在提交时组装 trim 后的 payload。
  it('合法 assignee 放行并提交 trim 后的 payload', async () => {
    const wrapper = mountModal()
    await fillByPlaceholder(wrapper, '任务标题', '  测试任务  ')
    await fillByPlaceholder(wrapper, '如 devops、claude（小写 slug）', '  devops  ')
    await nextTick()

    expect(wrapper.find('label[data-status="error"]').exists()).toBe(false)
    expect(createBtn(wrapper).attributes('disabled')).toBeUndefined()

    await createBtn(wrapper).trigger('click')
    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ title: '测试任务', assignee: 'devops' })
  })
})
