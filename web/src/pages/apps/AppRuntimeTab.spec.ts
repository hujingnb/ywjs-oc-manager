import { mount } from '@vue/test-utils'
import { nextTick, ref, type Ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AppRuntimeTab from './AppRuntimeTab.vue'

const appRef = ref({
  id: '00000000-0000-0000-0000-000000000001',
  org_id: '00000000-0000-0000-0000-000000000101',
  owner_user_id: '00000000-0000-0000-0000-000000000201',
  name: '测试实例',
  status: 'running',
  api_key_status: 'active',
})
const runtimeData = ref({
  status: 'running',
})

// 运行时页测试通过 hook mock 控制容器状态、快照和操作权限，不依赖真实后端。
// spec-A2b：已删除 ResourceTrendChart 与 useRuntimeNodes 相关 mock（节点与资源趋势图已去除）。
vi.mock('@/api/hooks/useApps', () => ({
  useAppRuntimeQuery: () => ({
    data: runtimeData,
    isLoading: ref(false),
    error: ref(null),
  }),
  useJobQuery: () => ({
    data: ref(null),
  }),
  useTriggerRuntimeOperation: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_admin',
    },
  }),
}))

function mountRuntimeTab() {
  return mount(AppRuntimeTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: appRef },
      plugins: [i18n],
      stubs: {
        ConfirmActionModal: true,
        JobProgressPanel: true,
        NButton: { template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>', props: ['disabled'] },
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NSpace: { template: '<div><slot /></div>' },
      },
    },
  })
}

describe('AppRuntimeTab', () => {
  beforeEach(() => {
    // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
    i18n.global.locale.value = 'zh'
    appRef.value = {
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试实例',
      status: 'running',
      api_key_status: 'active',
    }
    runtimeData.value = {
      status: 'running',
    }
  })

  // 覆盖运行中实例可执行停止、重启和删除操作入口均已渲染。
  it('运行中实例展示停止、重启和删除操作按钮', () => {
    const wrapper = mountRuntimeTab()

    expect(wrapper.findAll('button').map((button) => button.text())).toEqual(
      expect.arrayContaining(['停止', '重启', '删除']),
    )
  })

  // 覆盖 no_container 状态时展示业务文案而非原始 sentinel 值。
  it('no_container 状态展示业务文案', async () => {
    runtimeData.value = { status: 'no_container' }
    await nextTick()

    const wrapper = mountRuntimeTab()

    expect(wrapper.text()).toContain('尚未创建容器')
  })

  // 覆盖 stopped 状态时启动按钮可用、停止与重启按钮不可用（disabled）。
  it('stopped 状态下启动按钮可用，停止/重启按钮 disabled', () => {
    appRef.value = { ...appRef.value, status: 'stopped' }

    const wrapper = mountRuntimeTab()
    const buttons = wrapper.findAll('button')
    const startBtn = buttons.find(b => b.text() === '启动')
    const stopBtn = buttons.find(b => b.text() === '停止')
    const restartBtn = buttons.find(b => b.text() === '重启')

    expect(startBtn?.attributes('disabled')).toBeUndefined()
    expect(stopBtn?.attributes('disabled')).toBeDefined()
    expect(restartBtn?.attributes('disabled')).toBeDefined()
  })
})
