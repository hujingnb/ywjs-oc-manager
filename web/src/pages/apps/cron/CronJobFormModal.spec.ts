// CronJobFormModal.spec.ts —— Cron 任务表单弹窗单元测试。
// 覆盖：四区块字段显隐（workdir 收进高级区）、payload 组装、非平台 strip、编辑清空、repeat 保留。
// 子组件 ScheduleField/DeliverField/WorkspaceFilePicker 以 stub 替身，聚焦 Modal 自身的布局与 payload。
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
  NTooltip: { template: '<span><slot /><slot name="trigger" /></span>' },
}))

// stub 子组件：暴露最小接口，让测试能驱动 schedule/deliver/script 值，且不引入 vue-query 依赖。
vi.mock('./ScheduleField.vue', () => ({
  default: {
    props: ['value'],
    emits: ['update:value'],
    template: '<input class="stub-schedule" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
}))
vi.mock('./DeliverField.vue', () => ({
  default: {
    props: ['value', 'appId'],
    emits: ['update:value'],
    template: '<input class="stub-deliver" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
}))
vi.mock('./WorkspaceFilePicker.vue', () => ({
  default: {
    props: ['value', 'appId'],
    emits: ['update:value'],
    template: '<input class="stub-script" :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
}))

function mountFormModal(isPlatformAdmin: boolean, job: CronJob | null = null) {
  return mount(CronJobFormModal, {
    props: { show: true, submitting: false, job, isPlatformAdmin, appId: 'app_1' },
  })
}

describe('CronJobFormModal', () => {
  // 组织成员：可见 script/no_agent，不可见高级区（含已下沉的 workdir）。
  it('org member 不再看到 workdir 与高级字段', () => {
    const text = mountFormModal(false).text()
    expect(text).toContain('script')
    expect(text).toContain('no_agent')
    expect(text).not.toContain('workdir')
    expect(text).not.toContain('model')
    expect(text).not.toContain('skills')
  })

  // 平台管理员：高级区可见，workdir 在高级区出现。
  it('platform admin 看到 workdir 与全部高级字段', () => {
    const text = mountFormModal(true).text()
    expect(text).toContain('workdir')
    expect(text).toContain('skills')
    expect(text).toContain('model')
    expect(text).toContain('provider')
    expect(text).toContain('base_url')
  })

  // no_agent 文案改为「不使用 AI，仅运行脚本」。
  it('no_agent 文案更友好', () => {
    expect(mountFormModal(false).text()).toContain('不使用 AI，仅运行脚本')
  })

  // 提交 payload：schedule/deliver/script 来自子组件，字符串 trim，skills 拆分。
  it('submit 组装 payload', async () => {
    const wrapper = mountFormModal(true)

    await wrapper.find('[placeholder="任务名称"]').setValue('  日报  ')
    await wrapper.find('.stub-schedule').setValue('cron 0 9 * * *')
    await wrapper.find('[placeholder="触发时交给 Hermes 的提示词"]').setValue('  汇总  ')
    await wrapper.find('.stub-deliver').setValue('wechat')
    await wrapper.find('.stub-script').setValue('daily.py')
    await wrapper.find('[placeholder="任务运行目录"]').setValue('  /workspace/app  ')
    await wrapper.find('[placeholder="逗号分隔，如 shell,git"]').setValue(' shell, git ')
    await wrapper.find('[placeholder="模型名称"]').setValue('  gpt-5  ')
    await wrapper.find('input[type="checkbox"]').setValue(true)
    await wrapper.find('input[type="number"]').setValue('3')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      name: '日报',
      schedule: 'cron 0 9 * * *',
      prompt: '汇总',
      deliver: 'wechat',
      repeat: 3,
      script: 'daily.py',
      no_agent: true,
      workdir: '/workspace/app',
      skills: ['shell', 'git'],
      model: 'gpt-5',
    })
  })

  // 非平台用户提交不带高级字段（含 workdir，因为它在高级区不渲染）。
  it('非平台 payload 不含高级字段', async () => {
    const wrapper = mountFormModal(false)
    await wrapper.find('[placeholder="任务名称"]').setValue('日报')
    await wrapper.find('.stub-schedule').setValue('cron 0 9 * * *')
    await wrapper.find('.stub-script').setValue('daily.py')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ name: '日报', schedule: 'cron 0 9 * * *', script: 'daily.py' })
    expect(payload).not.toHaveProperty('workdir')
    expect(payload).not.toHaveProperty('skills')
    expect(payload).not.toHaveProperty('model')
  })

  // 编辑模式清空基础可选字段：空字符串保留为清空语义。
  it('编辑清空基础可选字段发送空串', async () => {
    const wrapper = mountFormModal(false, {
      id: 'cron_daily',
      name: '日报',
      schedule: { expr: '0 9 * * *', display: '每天 09:00' },
      prompt: '旧 prompt',
      deliver: 'wechat',
      script: 'old.py',
      no_agent: true,
    })
    await wrapper.find('[placeholder="触发时交给 Hermes 的提示词"]').setValue('   ')
    await wrapper.find('.stub-deliver').setValue('   ')
    await wrapper.find('.stub-script').setValue('   ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      name: '日报', prompt: '', deliver: '', script: '', no_agent: true,
    })
  })

  // 平台管理员编辑清空 skills → clear_skills:true。
  it('编辑清空 skills 转 clear_skills', async () => {
    const wrapper = mountFormModal(true, {
      id: 'cron_daily', name: '日报', schedule: { expr: '0 9 * * *' },
      skills: ['shell', 'git'], model: 'gpt-5',
    })
    await wrapper.find('[placeholder="逗号分隔，如 shell,git"]').setValue('   ')
    await wrapper.find('[placeholder="模型名称"]').setValue('   ')
    await wrapper.findAll('button').at(-1)?.trigger('click')

    const payload = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ clear_skills: true, model: '' })
    expect(payload).not.toHaveProperty('skills')
  })

  // 编辑已有 repeat 清空尝试：保留并提交原值。
  it('编辑保留已有 repeat', async () => {
    const wrapper = mountFormModal(false, {
      id: 'cron_daily', name: '日报', schedule: { expr: '0 9 * * *' },
      repeat: { times: 5, completed: 2 },
    })
    await wrapper.find('input[type="number"]').setValue('')
    await nextTick()
    await wrapper.findAll('button').at(-1)?.trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ name: '日报', repeat: 5 })
  })
})
