import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, nextTick, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppOverviewTab from './AppOverviewTab.vue'

const organizationName = ref<string | undefined>('测试组织')
// orgAssistantVersionIds 控制组织 allowlist，用于版本切换测试中验证交集逻辑。
const orgAssistantVersionIds = ref<string[]>(['version-001', 'version-002'])

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_admin',
    },
    isPlatformAdmin: false,
  }),
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => ({
    data: computed(() => organizationName.value
      ? {
          id: '00000000-0000-0000-0000-000000000101',
          name: organizationName.value,
          status: 'active',
          // assistant_version_ids 用于版本选项过滤，与后端字段一致。
          assistant_version_ids: orgAssistantVersionIds.value,
        }
      : null),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

// switchVersionMutateAsync 作为 spy 供版本切换用例断言调用参数。
const switchVersionMutateAsync = vi.fn()

// triggerRuntimeMutateAsync 作为 spy 供「立即重启」用例断言传入的操作类型为 'restart'。
const triggerRuntimeMutateAsync = vi.fn()
// triggerRuntimeIsPending 暴露给用例切换以模拟按钮 loading 文案。
const triggerRuntimeIsPending = ref(false)

vi.mock('@/api/hooks/useApps', () => ({
  useInitializeAppMutation: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
  useJobQuery: () => ({
    data: ref(null),
  }),
  useToggleAppAPIKey: () => ({
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }),
  useSwitchAppVersion: () => ({
    isPending: ref(false),
    mutateAsync: switchVersionMutateAsync,
  }),
  // useTriggerRuntimeOperation 在概览页用于「立即重启」按钮，复用 RuntimeTab 同一个后端接口；
  // mock 暴露 spy 让用例可以断言传入 'restart' 操作。
  useTriggerRuntimeOperation: () => ({
    isPending: triggerRuntimeIsPending,
    mutateAsync: triggerRuntimeMutateAsync,
  }),
}))

// assistantVersionsData 控制版本目录 mock 数据，用于 versionOptions 计算与版本名展示。
const assistantVersionsData = ref([
  // version-001：在组织 allowlist 中，名称用于断言版本名展示正确。
  { id: 'version-001', name: '稳定版 v1', description: '', system_prompt: '', image_id: '', main_model: '', routing: {}, skills: [], revision: 1 },
  // version-002：在组织 allowlist 中，供切换测试使用。
  { id: 'version-002', name: '测试版 v2', description: '', system_prompt: '', image_id: '', main_model: '', routing: {}, skills: [], revision: 2 },
  // version-003：不在组织 allowlist 中，应被过滤掉。
  { id: 'version-003', name: '禁用版 v3', description: '', system_prompt: '', image_id: '', main_model: '', routing: {}, skills: [], revision: 3 },
])

vi.mock('@/api/hooks/useAssistantVersions', () => ({
  useAssistantVersionsQuery: () => ({
    data: assistantVersionsData,
    isLoading: ref(false),
  }),
}))

const appRef = ref({
  id: '00000000-0000-0000-0000-000000000001',
  org_id: '00000000-0000-0000-0000-000000000101',
  owner_user_id: '00000000-0000-0000-0000-000000000201',
  name: '测试实例',
  status: 'running',
  api_key_status: 'active',
  container_id: 'container-1',
  // version_id 默认绑定 version-001，供助手版本展示与切换测试使用。
  version_id: 'version-001',
  version_synced: true,
})

// makeStubs 返回共用 stub 对象，NModal / NSelect 简化渲染以方便断言。
function makeStubs() {
  return {
    AppStatusTag: { template: '<span />' },
    ConfirmActionModal: true,
    JobProgressPanel: { props: ['title'], template: '<section>{{ title }}</section>' },
    NButton: defineComponent({
      props: ['disabled'],
      emits: ['click'],
      setup(props, { slots, emit }) {
        return () => h('button', {
          disabled: props.disabled,
          onClick: () => emit('click'),
        }, slots.default?.())
      },
    }),
    NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
    NDescriptions: { template: '<dl><slot /></dl>' },
    NDescriptionsItem: { props: ['label'], template: '<div><dt>{{ label }}</dt><dd><slot /></dd></div>' },
    NSpace: { template: '<span><slot /></span>' },
    NTag: { template: '<span><slot /></span>' },
    // NProgress 仅作占位,断言关心父节点 .init-progress 是否渲染,而不是进度条本身。
    NProgress: { props: ['percentage', 'processing'], template: '<div class="progress-stub" />' },
    // NModal 始终渲染内容区（忽略 show 状态），通过 data-show 属性供断言识别弹窗是否「打开」。
    // 简化渲染让弹窗内的 select 和按钮在点击切换前就存在于 DOM 中，便于 setValue/trigger 操作。
    NModal: defineComponent({
      props: ['show'],
      setup(props, { slots }) {
        return () => h('div', { class: 'modal-stub', 'data-show': String(props.show) }, slots.default?.())
      },
    }),
    // NSelect 渲染简单 select 标签，modelValue 与 onUpdate:modelValue 配合 setValue 测试。
    NSelect: defineComponent({
      props: ['modelValue', 'options'],
      emits: ['update:modelValue'],
      setup(props, { emit }) {
        return () => h('select', {
          value: props.modelValue ?? '',
          onChange: (e: Event) => emit('update:modelValue', (e.target as HTMLSelectElement).value),
        }, (props.options ?? []).map((opt: { label: string; value: string }) =>
          h('option', { value: opt.value }, opt.label),
        ))
      },
    }),
  }
}

function mountOverview() {
  return mount(AppOverviewTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: appRef },
      stubs: makeStubs(),
    },
  })
}

// mountWithApp 复用 makeStubs 配置,但允许覆盖 provide 的 app 数据,
// 便于 init / error 等状态的进度条断言。原 mountOverview 不动以保持既有用例的语义。
function mountWithApp(appOverride: Record<string, unknown>) {
  const customApp = ref({ ...appRef.value, ...appOverride })
  return mount(AppOverviewTab, {
    props: { appId: '00000000-0000-0000-0000-000000000001' },
    global: {
      provide: { app: customApp },
      stubs: makeStubs(),
    },
  })
}

describe('AppOverviewTab', () => {
  it('所属组织展示组织名称而不是组织 UUID', () => {
    organizationName.value = '测试组织'

    const wrapper = mountOverview()

    expect(wrapper.text()).toContain('测试组织')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000101')
  })

  it('组织名称缺失时展示友好兜底文案', () => {
    organizationName.value = undefined

    const wrapper = mountOverview()

    expect(wrapper.text()).toContain('未知组织')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000101')
  })
})

// AppOverviewTab 助手版本覆盖版本名展示、需重启标签、切换按钮与 mutation 调用四条场景。
describe('AppOverviewTab 助手版本', () => {
  // 版本目录与组织 allowlist 均有效时，应展示解析后的版本名而不是原始 id。
  it('展示已绑定的助手版本名称', () => {
    const wrapper = mountOverview()
    // 断言版本名「稳定版 v1」出现在页面；version_id=version-001 已在 mock 中对应该名称。
    expect(wrapper.text()).toContain('稳定版 v1')
    // 版本 id 本身不应直接暴露在页面上（名称解析成功时）。
    expect(wrapper.text()).not.toContain('version-001')
  })

  // version_synced=false 时应渲染「需重启」标签，提示用户实例与版本配置不一致。
  it('version_synced=false 时展示需重启标签', () => {
    const wrapper = mountWithApp({ version_synced: false })
    expect(wrapper.text()).toContain('需重启')
  })

  // version_synced=true 时不应出现「需重启」标签，避免误导用户。
  it('version_synced=true 时不展示需重启标签', () => {
    const wrapper = mountWithApp({ version_synced: true })
    expect(wrapper.text()).not.toContain('需重启')
  })

  // 组织管理员（role=org_admin 且 canManageApp 为 true）应看到「切换」按钮。
  it('组织管理员看到切换按钮', () => {
    const wrapper = mountOverview()
    const buttons = wrapper.findAll('button')
    const switchBtn = buttons.find(b => b.text() === '切换')
    expect(switchBtn).toBeDefined()
  })

  // 点击切换按钮后，组件内部状态应切换为弹窗打开且预选当前绑定版本；
  // 通过 vm 直接设置选中版本并调用确认函数，断言 mutation 以所选版本 id 被调用。
  // 注：naive-ui NModal 在 jsdom 中通过 teleport 渲染到 document.body 之外，
  // 无法通过 wrapper.find 捕获；此处直接操作 vm 内部状态验证业务逻辑。
  it('确认切换版本时调用 mutation 并传入所选版本 id', async () => {
    switchVersionMutateAsync.mockResolvedValueOnce({
      // 模拟后端返回更新后的 app 对象（仅需关键字段）
      id: '00000000-0000-0000-0000-000000000001',
      version_id: 'version-002',
      version_synced: false,
    })
    const wrapper = mountOverview()

    // 1. 点击「切换」按钮触发 openSwitchVersionModal，selectedVersionId 预设为当前绑定版本。
    const buttons = wrapper.findAll('button')
    const switchBtn = buttons.find(b => b.text() === '切换')
    expect(switchBtn).toBeDefined()
    await switchBtn!.trigger('click')
    await nextTick()

    // 2. 通过 vm 内部状态验证弹窗已打开（showSwitchVersionModal=true）且预选版本正确。
    const vm = wrapper.vm as unknown as {
      showSwitchVersionModal: boolean
      selectedVersionId: string | null
      onConfirmSwitchVersion: () => Promise<void>
    }
    expect(vm.showSwitchVersionModal).toBe(true)
    // 预选版本应等于当前绑定的 version-001
    expect(vm.selectedVersionId).toBe('version-001')

    // 3. 模拟用户在弹窗中切换选择 version-002（测试版 v2）。
    vm.selectedVersionId = 'version-002'
    await nextTick()

    // 4. 调用确认函数，等待 mutation 执行完成。
    await vm.onConfirmSwitchVersion()

    // 5. 断言 mutation 以 version-002 调用，业务切换逻辑正确。
    expect(switchVersionMutateAsync).toHaveBeenCalledWith('version-002')
  })

  // 镜像/版本变更后实例需要重启同步，概览页应该提供「立即重启」入口避免用户去找运行时 tab。
  // version_synced=false 且 status=running 时按钮可见，与 canRestartForVersionSync 三条件吻合。
  it('version_synced=false 且实例运行中时展示立即重启按钮', () => {
    const wrapper = mountWithApp({ status: 'running', version_synced: false })
    const buttons = wrapper.findAll('button')
    // 仅匹配「立即重启」文案，与「重新初始化」「切换」等其他按钮明确区分。
    const restartBtn = buttons.find(b => b.text() === '立即重启')
    expect(restartBtn).toBeDefined()
  })

  // version_synced=true 时实例已与版本同步，不应展示重启按钮，避免误导用户做无意义重启。
  it('version_synced=true 时不展示立即重启按钮', () => {
    const wrapper = mountWithApp({ status: 'running', version_synced: true })
    const buttons = wrapper.findAll('button')
    const restartBtn = buttons.find(b => b.text() === '立即重启')
    expect(restartBtn).toBeUndefined()
  })

  // stopped 状态下容器不存在，restart 后端要么直接拒绝、要么走 stop→start 失败；
  // 这种边界场景应当让用户去运行时 tab 走「启动」入口，本按钮不展示。
  it('实例非运行状态时不展示立即重启按钮', () => {
    const wrapper = mountWithApp({ status: 'stopped', version_synced: false })
    const buttons = wrapper.findAll('button')
    const restartBtn = buttons.find(b => b.text() === '立即重启')
    expect(restartBtn).toBeUndefined()
  })

  // 点击「立即重启」按钮应该提交 restart 任务；断言 mutation 以 'restart' 参数调用。
  it('点击立即重启按钮以 restart 操作调用 mutation', async () => {
    triggerRuntimeMutateAsync.mockResolvedValueOnce({ job_id: 'job-restart-001', operation: 'restart' })
    const wrapper = mountWithApp({ status: 'running', version_synced: false })
    const buttons = wrapper.findAll('button')
    const restartBtn = buttons.find(b => b.text() === '立即重启')
    expect(restartBtn).toBeDefined()
    await restartBtn!.trigger('click')
    await nextTick()
    // mutation 的入参类型固定为四个枚举值之一，这里只验证 'restart' 被传入。
    expect(triggerRuntimeMutateAsync).toHaveBeenCalledWith('restart')
  })
})

// AppOverviewTab progress 覆盖 init 子状态的进度条与失败阶段提示三条分支:
// 1) total=0 时走不定进度,不展示字节文案;
// 2) total>0 时按字节渲染 current/total;
// 3) status=error + last_error_status 显示对应中文阶段。
describe('AppOverviewTab progress', () => {
  // pulling_runtime_image 阶段且 total 未知时只渲染不定进度条,不展示字节文案
  it('init 阶段且 total=0 时展示不定进度', () => {
    const wrapper = mountWithApp({
      status: 'pulling_runtime_image',
      progress_current: 0,
      progress_total: 0,
    })
    expect(wrapper.find('.init-progress').exists()).toBe(true)
    expect(wrapper.find('.init-progress-bytes').exists()).toBe(false)
  })

  // pulling_runtime_image 阶段且 total>0 时按 1.0 KB / 4.0 KB 渲染字节文案
  it('init 阶段且 total>0 时展示字节进度', () => {
    const wrapper = mountWithApp({
      status: 'pulling_runtime_image',
      progress_current: 1024,
      progress_total: 4096,
    })
    const bytes = wrapper.find('.init-progress-bytes')
    expect(bytes.exists()).toBe(true)
    expect(bytes.text()).toContain('1.0 KB')
    expect(bytes.text()).toContain('4.0 KB')
  })

  // error + last_error_status=pulling_runtime_image 时按 status.ts 映射展示「拉取运行时镜像」中文
  it('error 时展示失败阶段', () => {
    const wrapper = mountWithApp({
      status: 'error',
      last_error_status: 'pulling_runtime_image',
    })
    expect(wrapper.find('.init-failure').text()).toContain('拉取运行时镜像')
  })
})
