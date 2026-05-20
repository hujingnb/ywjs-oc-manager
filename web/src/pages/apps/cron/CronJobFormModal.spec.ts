// CronJobFormModal.spec.ts —— Cron 任务表单弹窗单元测试。
// 覆盖：角色字段显隐、字符串 trim、skills 解析和非平台 payload strip。
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import type { CronJob } from '@/api/hooks/useCron'
import CronJobFormModal from './CronJobFormModal.vue'

// mock naive-ui：表单测试聚焦字段显隐与 payload 组装，避免 NModal teleport 干扰 wrapper 查询。
vi.mock('naive-ui', () => ({
  NModal: {
    props: ['show', 'title'],
    emits: ['update:show'],
    template: '<section v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>',
  },
  NForm: { template: '<form><slot /></form>' },
  NFormItem: { props: ['label'], template: '<label><span>{{ label }}</span><slot /></label>' },
  NInput: {
    props: ['value', 'placeholder', 'type'],
    emits: ['update:value'],
    template: '<textarea v-if="type === \'textarea\'" :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" /><input v-else :placeholder="placeholder" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NInputNumber: {
    props: ['value', 'clearable'],
    emits: ['update:value'],
    template: '<input type="number" :data-clearable="String(clearable)" :value="value ?? \'\'" @input="$emit(\'update:value\', $event.target.value === \'\' ? null : Number($event.target.value))" />',
  },
  NCheckbox: {
    props: ['checked'],
    emits: ['update:checked'],
    template: '<label><input type="checkbox" :checked="checked" @change="$emit(\'update:checked\', $event.target.checked)" /><slot /></label>',
  },
  NButton: {
    props: ['disabled', 'loading', 'type'],
    emits: ['click'],
    template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
  },
  NSpace: { template: '<div><slot /></div>' },
}))

function mountFormModal(isPlatformAdmin: boolean, job: CronJob | null = null) {
  return mount(CronJobFormModal, {
    props: {
      show: true,
      submitting: false,
      job,
      isPlatformAdmin,
    },
  })
}

async function fillByPlaceholder(wrapper: ReturnType<typeof mountFormModal>, placeholder: string, value: string) {
  const input = wrapper.find(`[placeholder="${placeholder}"]`)
  expect(input.exists()).toBe(true)
  await input.setValue(value)
}

describe('CronJobFormModal', () => {
  // 覆盖组织成员字段显隐：基础执行字段可见，高级模型字段不可见。
  it('org member sees script/no_agent/workdir but not model/provider/base_url/skills', () => {
    const wrapper = mountFormModal(false)
    const text = wrapper.text()

    expect(text).toContain('script')
    expect(text).toContain('no_agent')
    expect(text).toContain('workdir')
    expect(text).not.toContain('model')
    expect(text).not.toContain('provider')
    expect(text).not.toContain('base_url')
    expect(text).not.toContain('skills')
  })

  // 覆盖平台管理员字段显隐：高级模型和 skills 字段全部展示。
  it('platform admin sees all fields', () => {
    const wrapper = mountFormModal(true)
    const text = wrapper.text()

    expect(text).toContain('script')
    expect(text).toContain('no_agent')
    expect(text).toContain('workdir')
    expect(text).toContain('skills')
    expect(text).toContain('model')
    expect(text).toContain('provider')
    expect(text).toContain('base_url')
  })

  // 覆盖提交 payload 规整：字符串 trim，skills 按逗号或空白拆分，布尔字段保留。
  it('submit trims fields and emits payload', async () => {
    const wrapper = mountFormModal(true)

    await fillByPlaceholder(wrapper, '任务名称', '  日报  ')
    await fillByPlaceholder(wrapper, 'cron 或 every 表达式', '  0 9 * * *  ')
    await fillByPlaceholder(wrapper, '触发时交给 Hermes 的提示词', '  汇总昨天的工作  ')
    await fillByPlaceholder(wrapper, 'wechat / email / none', '  wechat  ')
    await fillByPlaceholder(wrapper, '仓库内脚本文件名', '  scripts/daily.py  ')
    await fillByPlaceholder(wrapper, '任务运行目录', '  /workspace/app  ')
    await fillByPlaceholder(wrapper, '逗号分隔，如 shell,git', ' shell, git  ')
    await fillByPlaceholder(wrapper, '模型名称', '  gpt-5  ')
    await fillByPlaceholder(wrapper, 'provider 名称', '  openai  ')
    await fillByPlaceholder(wrapper, 'https://provider.example/v1', '  https://example.test/v1  ')
    await wrapper.find('input[type="checkbox"]').setValue(true)
    await wrapper.find('input[type="number"]').setValue('3')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual({
      name: '日报',
      schedule: '0 9 * * *',
      prompt: '汇总昨天的工作',
      deliver: 'wechat',
      repeat: 3,
      script: 'scripts/daily.py',
      no_agent: true,
      workdir: '/workspace/app',
      skills: ['shell', 'git'],
      model: 'gpt-5',
      provider: 'openai',
      base_url: 'https://example.test/v1',
    })
  })

  // 覆盖非平台用户提交时的字段边界：即使表单有基础执行字段，也不能带高级字段。
  it('non-platform payload does not include advanced fields', async () => {
    const wrapper = mountFormModal(false)

    await fillByPlaceholder(wrapper, '任务名称', '  日报  ')
    await fillByPlaceholder(wrapper, 'cron 或 every 表达式', '  0 9 * * *  ')
    await fillByPlaceholder(wrapper, '仓库内脚本文件名', '  scripts/daily.py  ')
    await fillByPlaceholder(wrapper, '任务运行目录', '  /workspace/app  ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({
      name: '日报',
      schedule: '0 9 * * *',
      script: 'scripts/daily.py',
      workdir: '/workspace/app',
    })
    expect(payload).not.toHaveProperty('skills')
    expect(payload).not.toHaveProperty('model')
    expect(payload).not.toHaveProperty('provider')
    expect(payload).not.toHaveProperty('base_url')
  })

  // 覆盖编辑模式清空基础可选字段：PATCH 中空字符串是清空语义，不能被 payload builder 省略。
  it('edit payload includes cleared base optional fields', async () => {
    const wrapper = mountFormModal(false, {
      id: 'cron_daily',
      name: '日报',
      schedule: { expr: '0 9 * * *', display: '每天 09:00' },
      prompt: '旧 prompt',
      deliver: 'wechat',
      script: 'scripts/old.py',
      workdir: '/workspace/old',
      no_agent: true,
    })

    await fillByPlaceholder(wrapper, '触发时交给 Hermes 的提示词', '   ')
    await fillByPlaceholder(wrapper, 'wechat / email / none', '   ')
    await fillByPlaceholder(wrapper, '仓库内脚本文件名', '   ')
    await fillByPlaceholder(wrapper, '任务运行目录', '   ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      name: '日报',
      schedule: '0 9 * * *',
      prompt: '',
      deliver: '',
      script: '',
      workdir: '',
      no_agent: true,
    })
  })

  // 覆盖平台管理员编辑模式清空 skills：空输入应转为 clear_skills:true，而不是省略导致旧 skills 保留。
  it('platform admin edit payload clears skills and advanced strings', async () => {
    const wrapper = mountFormModal(true, {
      id: 'cron_daily',
      name: '日报',
      schedule: { expr: '0 9 * * *' },
      skills: ['shell', 'git'],
      model: 'gpt-5',
      provider: 'openai',
      base_url: 'https://example.test/v1',
    })

    await fillByPlaceholder(wrapper, '逗号分隔，如 shell,git', '   ')
    await fillByPlaceholder(wrapper, '模型名称', '   ')
    await fillByPlaceholder(wrapper, 'provider 名称', '   ')
    await fillByPlaceholder(wrapper, 'https://provider.example/v1', '   ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({
      clear_skills: true,
      model: '',
      provider: '',
      base_url: '',
    })
    expect(payload).not.toHaveProperty('skills')
  })

  // 覆盖编辑模式已有 repeat 时的清空尝试：clear_repeat 暂不支持，表单应保留并提交原 repeat。
  it('edit payload preserves existing repeat when repeat input is cleared', async () => {
    const wrapper = mountFormModal(false, {
      id: 'cron_daily',
      name: '日报',
      schedule: { expr: '0 9 * * *' },
      repeat: { times: 5, completed: 2 },
    })

    await wrapper.find('input[type="number"]').setValue('')
    await nextTick()
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.find('input[type="number"]').attributes('data-clearable')).toBe('false')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      name: '日报',
      schedule: '0 9 * * *',
      repeat: 5,
    })
  })
})
