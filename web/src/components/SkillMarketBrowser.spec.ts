// SkillMarketBrowser.spec.ts — SkillMarketBrowser 市场浏览器单元测试。
// 覆盖：来源徽章渲染、安装按钮展示与隐藏、existingNames 已存在标记、
// canAction=false 无按钮、滚动加载三例、点卡片 emit action 带最新版。
import { flushPromises, mount } from '@vue/test-utils'
import { computed, nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import SkillMarketBrowser from './SkillMarketBrowser.vue'
import type { SkillEntry } from '@/api'

// ======================== hoisted mocks ========================
const mocks = vi.hoisted(() => ({
  // 无需 mutation mocks，SkillMarketBrowser 只 emit action，安装逻辑在父级
}))

// ======================== 可变 reactive 状态 ========================
// marketState 控制 useSkillMarketQuery 的返回值，data 以扁平 { entries } 存放，
// mock 工厂包装成 useInfiniteQuery 的 { pages } 形状。
const marketState = {
  data: ref<{ entries: SkillEntry[] }>({ entries: [] }),
  isLoading: ref(false),
  error: ref<Error | null>(null),
  hasNextPage: ref(false),
  isFetchingNextPage: ref(false),
  fetchNextPage: vi.fn(),
}

// ======================== IntersectionObserver mock ========================
// jsdom 无 IntersectionObserver，组件建立滚动加载哨兵观察时会报错，需 mock。
// lastIntersectionCallback 捕获最近一次构造时传入的回调，测试可手动触发模拟「哨兵进入视口」。
let lastIntersectionCallback: ((entries: { isIntersecting: boolean }[]) => void) | null = null
const ioObserve = vi.fn()
const ioUnobserve = vi.fn()
const ioDisconnect = vi.fn()
class MockIntersectionObserver {
  constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
    lastIntersectionCallback = cb
  }
  observe = ioObserve
  disconnect = ioDisconnect
  unobserve = ioUnobserve
  takeRecords = vi.fn(() => [])
}
vi.stubGlobal('IntersectionObserver', MockIntersectionObserver)

// ======================== vi.mock ========================
// auth store mock：SkillMarketBrowser 用 auth.isPlatformAdmin 决定详情抽屉「下载」按钮可见性。
// 抽屉已 stub，此处仅需让 useAuthStore() 可被调用，固定返回非平台管理员。
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isPlatformAdmin: false }),
}))

vi.mock('@/api/hooks/useSkills', () => ({
  // 把扁平 marketState.data（{ entries }）包装成 useInfiniteQuery 的 { pages } 形状。
  useSkillMarketQuery: () => ({
    data: computed(() => ({ pages: [{ entries: marketState.data.value.entries ?? [] }] })),
    isLoading: marketState.isLoading,
    error: marketState.error,
    hasNextPage: marketState.hasNextPage,
    isFetchingNextPage: marketState.isFetchingNextPage,
    fetchNextPage: marketState.fetchNextPage,
  }),
  // useSkillDetailQuery mock：SkillMarketBrowser 内嵌的 SkillDetailDrawer 需要（stub 已拦截，
  // 但为防 stub 失效引起 import 错误，保留此 mock）。
  useSkillDetailQuery: () => ({
    data: ref({ detail: {}, versions: [] }),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    // NCard stub 渲染 slot 内容。
    NCard: { template: '<div class="n-card"><slot /></div>' },
    // NButton stub 渲染为 button。
    NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
    // NTag stub 渲染为 span。
    NTag: { template: '<span class="n-tag" v-bind="$attrs"><slot /></span>' },
    // NInput stub：避免 DOM attribute warning。
    NInput: { template: '<div class="n-input"><input /></div>' },
    // NDrawer/NDrawerContent stub：被 SkillDetailDrawer 内嵌，show=true 时渲染内容。
    NDrawer: { props: ['show'], template: '<div v-if="show" class="n-drawer"><slot /></div>' },
    NDrawerContent: { props: ['title'], template: '<div class="n-drawer-content">{{ title }}<slot /></div>' },
  }
})

// stub SkillDetailDrawer：声明 pick-version 事件和全部 props，使 SkillMarketBrowser 的
// onPickVersion 路径可在测试中触发。template 带 show 属性以符合 v-model:show 绑定。
vi.mock('./SkillDetailDrawer.vue', () => ({
  default: {
    name: 'SkillDetailDrawer',
    props: ['show', 'skill', 'allowVersionPick', 'actionPending', 'existingNames'],
    emits: ['update:show', 'pick-version'],
    template: '<div class="stub-drawer" />',
  },
}))

// ======================== 挂载辅助 ========================
function mountBrowser(props: Record<string, unknown> = {}) {
  return mount(SkillMarketBrowser, {
    props: { canAction: true, ...props },
  })
}

// ======================== 测试套件 ========================
describe('SkillMarketBrowser', () => {
  beforeEach(() => {
    // 重置每个用例前的状态。
    marketState.data.value = { entries: [] }
    marketState.isLoading.value = false
    marketState.error.value = null
    marketState.hasNextPage.value = false
    marketState.isFetchingNextPage.value = false
    marketState.fetchNextPage.mockReset()
    lastIntersectionCallback = null
    ioObserve.mockReset()
    ioUnobserve.mockReset()
    ioDisconnect.mockReset()
  })

  // ======== 来源徽章 ========

  it('平台技能条目来源徽章显示「平台技能」', () => {
    // 覆盖 source=platform 时来源徽章文案正确，用户可区分来源。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'p-skill', name: 'p-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountBrowser()
    expect(wrapper.find('.n-card').text()).toContain('平台技能')
  })

  it('ClawHub 条目来源徽章显示「ClawHub」', () => {
    // 覆盖 source=clawhub 时来源徽章文案正确。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c-skill', name: 'c-skill', version: '2.0.0', downloads: 10 },
      ],
    }
    const wrapper = mountBrowser()
    expect(wrapper.find('.n-card').text()).toContain('ClawHub')
  })

  // ======== 安装按钮展示 ========

  it('技能市场展示条目并显示安装按钮（canAction=true）', () => {
    // 覆盖市场条目正常加载、有权限时可点击安装的场景。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'my-skill', name: 'my-skill', version: '2.0.0', downloads: 42 },
      ],
    }
    const wrapper = mountBrowser({ canAction: true })
    const card = wrapper.find('.n-card')
    expect(card.exists()).toBe(true)
    expect(card.text()).toContain('my-skill')
    expect(card.text()).toContain('安装')
  })

  it('existingNames 命中时显示已安装/已添加标记而非安装按钮', () => {
    // 覆盖市场展示与已存在名集合交叉：同名 skill 显示「已安装」标记，不显示安装按钮。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'existing-skill', name: 'existing-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountBrowser({
      existingNames: new Set(['existing-skill']),
      existingLabel: '已安装',
      canAction: true,
    })
    const card = wrapper.find('.n-card')
    // 已安装标记（span）应存在。
    expect(card.text()).toContain('已安装')
    // 安装按钮（button 元素）不应存在——检查 button 元素避免文本子串误判。
    expect(card.find('button').exists()).toBe(false)
  })

  it('existingLabel=「已添加」时命中条目显示「已添加」标记', () => {
    // 覆盖助手版本场景：existingLabel 可自定义，命中时显示「已添加」而非「已安装」。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'sv', name: 'sv-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountBrowser({
      existingNames: new Set(['sv-skill']),
      existingLabel: '已添加',
      canAction: true,
    })
    expect(wrapper.find('.n-card').text()).toContain('已添加')
    expect(wrapper.find('.n-card').find('button').exists()).toBe(false)
  })

  it('canAction=false 时不显示安装按钮', () => {
    // 覆盖只读角色浏览市场时没有安装入口的场景。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'remote-skill', name: 'remote-skill', version: '3.0.0', downloads: 100 },
      ],
    }
    const wrapper = mountBrowser({ canAction: false })
    expect(wrapper.find('.n-card').text()).not.toContain('安装')
    expect(wrapper.find('.n-card').find('button').exists()).toBe(false)
  })

  // ======== 滚动加载 ========

  it('有下一页时哨兵进入视口自动拉取下一页', async () => {
    // 覆盖滚动加载：hasNextPage=true 时底部哨兵被 observe，进入视口触发 fetchNextPage。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c1', name: 'c1', version: '1.0.0', downloads: 5 },
      ],
    }
    marketState.hasNextPage.value = true
    mountBrowser()
    await flushPromises()
    await nextTick()
    // 哨兵元素应已被 IntersectionObserver 观察。
    expect(ioObserve).toHaveBeenCalled()
    // 模拟哨兵进入视口 → 自动拉取下一页。
    lastIntersectionCallback?.([{ isIntersecting: true }])
    expect(marketState.fetchNextPage).toHaveBeenCalledTimes(1)
  })

  it('正在拉取下一页时哨兵再次进入视口不重复触发', async () => {
    // 防抖：isFetchingNextPage=true 时再次相交不应重复调用 fetchNextPage。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c1', name: 'c1', version: '1.0.0', downloads: 5 },
      ],
    }
    marketState.hasNextPage.value = true
    marketState.isFetchingNextPage.value = true
    mountBrowser()
    await flushPromises()
    await nextTick()
    lastIntersectionCallback?.([{ isIntersecting: true }])
    expect(marketState.fetchNextPage).not.toHaveBeenCalled()
  })

  it('没有下一页时不渲染哨兵、不建立观察', async () => {
    // 边界：hasNextPage=false 时哨兵不渲染，IntersectionObserver 不 observe。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'p1', name: 'p1', version: '1.0.0', downloads: 0 },
      ],
    }
    marketState.hasNextPage.value = false
    mountBrowser()
    await flushPromises()
    await nextTick()
    expect(ioObserve).not.toHaveBeenCalled()
  })

  it('一页加载完成后仍有下一页时重新观察哨兵，避免首屏不满导致卡死', async () => {
    // 回归防御：多列网格首屏不满一屏时，哨兵常驻视口、相交状态不变，
    // IntersectionObserver 不会二次回调，旧实现会卡死在前几页。修复后每当一页
    // 加载完成（isFetchingNextPage 由 true 回落 false），应重新 observe 哨兵
    // （先 unobserve 再 observe），让仍在视口内的哨兵能再次触发相交回调继续翻页。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'c1', name: 'c1', version: '1.0.0', downloads: 5 },
      ],
    }
    marketState.hasNextPage.value = true
    // 初始处于「正在加载下一页」，模拟刚发出翻页请求尚未返回。
    marketState.isFetchingNextPage.value = true
    mountBrowser()
    await flushPromises()
    await nextTick()
    // 记录此前 observe 次数（挂载时哨兵首次被观察一次）。
    const observeBefore = ioObserve.mock.calls.length
    // 模拟这一页加载完成：isFetchingNextPage 下降沿，触发对哨兵的重新观察。
    marketState.isFetchingNextPage.value = false
    await flushPromises()
    await nextTick()
    // 修复点：加载完成后重新观察哨兵（unobserve + observe），而非永久停止。
    expect(ioUnobserve).toHaveBeenCalled()
    expect(ioObserve.mock.calls.length).toBeGreaterThan(observeBefore)
  })

  // ======== 点卡片 emit action ========

  it('点击卡片安装按钮 emit action 带最新版本', async () => {
    // 覆盖：卡片主操作 emit action，version 为卡片展示的最新版。
    marketState.data.value = {
      entries: [
        { source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountBrowser({ actionLabel: '添加', canAction: true })
    await wrapper.find('.market-card button').trigger('click')
    expect(wrapper.emitted('action')?.[0][0]).toMatchObject({
      source: 'clawhub',
      source_ref: 'sv',
      name: 'Skill Vetter',
      version: '1.0.0',
    })
  })

  // ======== 定制卡片 ========

  it('定制技能卡片渲染范围徽章、申请人小字与安装按钮', async () => {
    // 覆盖 source=custom 时：范围徽章（audienceTag）、申请人「由 X 申请」小字、来源徽章「定制」均渲染；
    // 安装按钮仍可点击（custom 来源走同一 emitAction 路径）。
    marketState.data.value = {
      entries: [
        {
          source: 'custom',
          source_ref: 'my-custom-skill',
          name: 'my-custom-skill',
          version: '1.0.0',
          downloads: 0,
          audience: 'all_org',
          requester_name: '张三',
        },
      ],
    }
    const wrapper = mountBrowser({ canAction: true })
    const card = wrapper.find('.n-card')
    // 来源徽章应显示「定制」。
    expect(card.text()).toContain('定制')
    // 范围徽章应显示「整企业可见」（audience=all_org）。
    expect(card.text()).toContain('整企业可见')
    // 申请人小字应显示「由 张三 申请」。
    expect(card.text()).toContain('由 张三 申请')
    // 安装按钮应存在（custom 来源复用同一安装路径）。
    expect(card.find('button').exists()).toBe(true)
  })

  it('定制卡片点击安装按钮 emit action 携带正确的 source/source_ref/name/version', async () => {
    // 覆盖 custom 来源的 emitAction：source='custom'、source_ref/name/version 均来自 entry。
    marketState.data.value = {
      entries: [
        {
          source: 'custom',
          source_ref: 'custom-skill-ref',
          name: 'custom-skill-ref',
          version: '2.0.0',
          downloads: 0,
          audience: 'org_admins',
          requester_name: '李四',
        },
      ],
    }
    const wrapper = mountBrowser({ canAction: true })
    await wrapper.find('.market-card button').trigger('click')
    expect(wrapper.emitted('action')?.[0][0]).toMatchObject({
      source: 'custom',
      source_ref: 'custom-skill-ref',
      name: 'custom-skill-ref',
      version: '2.0.0',
    })
  })

  it('source prop 传入 custom 时 selectedSource 初始为 custom（定制筛选被选中）', async () => {
    // 覆盖父组件「去安装」联动：prop source='custom' 使市场初始选中「定制」筛选，
    // 筛选 chip 中「定制」应带 primary 类型（checked=true 绑定）。
    marketState.data.value = { entries: [] }
    const wrapper = mountBrowser({ source: 'custom' })
    // 找到所有筛选 chip，「定制」chip 应有 checked 属性且值为 true。
    const filterTags = wrapper.findAll('.filter-tag')
    const customTag = filterTags.find((t) => t.text().includes('定制'))
    expect(customTag).toBeTruthy()
    // NTag stub 透传 $attrs（含 checked），断言 checked 属性存在且为 true。
    expect(customTag?.attributes('checked')).toBe('true')
  })

  // ======== 详情抽屉锁旧版 pick-version ========

  it('详情抽屉 pick-version 事件 emit action 使用抽屉版本而非卡片最新版', async () => {
    // 覆盖：onPickVersion 路径——先点卡片打开详情（detailSkill 有值），
    // 再由抽屉 emit pick-version 传回历史版本号，action payload 应携带该历史版本
    // 而非卡片上展示的最新版。验证锁旧版场景中版本来自抽屉而非 entry.version。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'weather', name: 'weather', version: '2.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountBrowser({ allowVersionPick: true, canAction: true })

    // 点击卡片触发 openDetail，使 detailSkill 填充为该 entry 的信息。
    await wrapper.find('.market-card').trigger('click')
    await nextTick()

    // 抽屉 emit pick-version 传回历史版本 '1.5.0'（不同于卡片最新版 '2.0.0'）。
    wrapper.findComponent({ name: 'SkillDetailDrawer' }).vm.$emit('pick-version', '1.5.0')
    await nextTick()

    // action payload 的 version 应为抽屉传回的 '1.5.0'，而非卡片最新版 '2.0.0'。
    const emitted = wrapper.emitted('action')
    expect(emitted).toBeTruthy()
    const lastPayload = emitted![emitted!.length - 1][0] as { source: string; source_ref: string; name: string; version: string }
    expect(lastPayload).toMatchObject({
      source: 'platform',
      source_ref: 'weather',
      name: 'weather',
      version: '1.5.0',
    })
  })
})
