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
const ioDisconnect = vi.fn()
class MockIntersectionObserver {
  constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
    lastIntersectionCallback = cb
  }
  observe = ioObserve
  disconnect = ioDisconnect
  unobserve = vi.fn()
  takeRecords = vi.fn(() => [])
}
vi.stubGlobal('IntersectionObserver', MockIntersectionObserver)

// ======================== vi.mock ========================
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

// stub SkillDetailDrawer：避免拉起其内部详情查询，SkillMarketBrowser 测试只关注卡片区域。
vi.mock('./SkillDetailDrawer.vue', () => ({
  default: { name: 'SkillDetailDrawer', template: '<div class="stub-drawer" />' },
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
    ioDisconnect.mockReset()
  })

  // ======== 来源徽章 ========

  it('平台库条目来源徽章显示「平台库」', () => {
    // 覆盖 source=platform 时来源徽章文案正确，用户可区分来源。
    marketState.data.value = {
      entries: [
        { source: 'platform', source_ref: 'p-skill', name: 'p-skill', version: '1.0.0', downloads: 0 },
      ],
    }
    const wrapper = mountBrowser()
    expect(wrapper.find('.n-card').text()).toContain('平台库')
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
})
