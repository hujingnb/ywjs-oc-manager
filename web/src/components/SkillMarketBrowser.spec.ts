// SkillMarketBrowser.spec.ts — SkillMarketBrowser 市场浏览器单元测试。
// 覆盖：来源徽章渲染、安装按钮展示与隐藏、existingNames 已存在标记、
// canAction=false 无按钮、翻页（分页切片 / 后台补拉 / 筛选复位）、点卡片 emit action 带最新版。
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
    // NPagination stub：暴露 page/page-count，并提供「下一页」按钮触发 update:page，
    // 用于断言翻页切片与后台补拉行为。
    NPagination: {
      props: ['page', 'pageCount', 'disabled'],
      emits: ['update:page'],
      template:
        '<div class="n-pagination" :data-page="page" :data-page-count="pageCount">'
        + '<button class="page-next" @click="$emit(\'update:page\', page + 1)">next</button></div>',
    },
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

  // ======== 翻页 ========

  // makeEntries 批量构造 clawhub 条目，便于覆盖超过一页（pageSize=12）的切片场景。
  function makeEntries(count: number): SkillEntry[] {
    return Array.from({ length: count }, (_, i) => ({
      source: 'clawhub' as const,
      source_ref: `c${i}`,
      name: `c${i}`,
      version: '1.0.0',
      downloads: i,
    }))
  }

  it('条目超过一页时仅渲染当前页（pageSize=12）并展示翻页器', async () => {
    // 覆盖客户端分页切片：15 条目首页只渲染前 12 张卡片，且因总页数>1 渲染翻页器。
    marketState.data.value = { entries: makeEntries(15) }
    const wrapper = mountBrowser()
    await flushPromises()
    // 首页应只渲染 12 张卡片，而非把全部 15 条一次性铺开。
    expect(wrapper.findAll('.n-card')).toHaveLength(12)
    // 翻页器应出现（总页数 ceil(15/12)=2）。
    const pager = wrapper.find('.n-pagination')
    expect(pager.exists()).toBe(true)
    expect(pager.attributes('data-page-count')).toBe('2')
  })

  it('翻到第二页时渲染剩余条目而非累积全部', async () => {
    // 覆盖翻页切片：15 条目翻到第 2 页应渲染剩余 3 张卡片（第 13-15 条），首页 12 张不再保留。
    marketState.data.value = { entries: makeEntries(15) }
    const wrapper = mountBrowser()
    await flushPromises()
    await wrapper.find('.page-next').trigger('click')
    await nextTick()
    expect(wrapper.findAll('.n-card')).toHaveLength(3)
  })

  it('单页且无下一页时不渲染翻页器', async () => {
    // 边界：条目不足一页且 hasNextPage=false 时总页数为 1，翻页器不渲染。
    marketState.data.value = { entries: makeEntries(3) }
    marketState.hasNextPage.value = false
    const wrapper = mountBrowser()
    await flushPromises()
    expect(wrapper.find('.n-pagination').exists()).toBe(false)
  })

  it('仍有下一页时翻页器多给一页入口，翻到该页触发后台补拉', async () => {
    // 覆盖游标分页后台补拉：恰好一页（12 条）且 hasNextPage=true 时总页数 +1=2；
    // 翻到第 2 页时当前已加载条目不足以填满该页，应触发 fetchNextPage 拉取下一游标页。
    marketState.data.value = { entries: makeEntries(12) }
    marketState.hasNextPage.value = true
    // fetchNextPage 与真实 useInfiniteQuery 一致：拉取开始即置 isFetchingNextPage=true，
    // 用于验证补拉触发后守卫生效、不会重复拉取。
    marketState.fetchNextPage.mockImplementation(() => {
      marketState.isFetchingNextPage.value = true
    })
    const wrapper = mountBrowser()
    await flushPromises()
    // 总页数应为「已加载页 1 + 下一页入口 1」=2。
    expect(wrapper.find('.n-pagination').attributes('data-page-count')).toBe('2')
    await wrapper.find('.page-next').trigger('click')
    await nextTick()
    expect(marketState.fetchNextPage).toHaveBeenCalledTimes(1)
  })

  it('正在补拉下一页时不重复触发 fetchNextPage', async () => {
    // 防抖：isFetchingNextPage=true 时翻到需补拉的页码不应重复调用 fetchNextPage。
    marketState.data.value = { entries: makeEntries(12) }
    marketState.hasNextPage.value = true
    marketState.isFetchingNextPage.value = true
    const wrapper = mountBrowser()
    await flushPromises()
    await wrapper.find('.page-next').trigger('click')
    await nextTick()
    expect(marketState.fetchNextPage).not.toHaveBeenCalled()
  })

  it('切换来源筛选后页码复位到第一页', async () => {
    // 覆盖筛选复位：翻到第 2 页后点击「平台技能」筛选 chip，marketParams 变化使页码回到 1，
    // 重新渲染首页 12 张卡片（避免停留在筛选后已不存在的页码导致空白）。
    marketState.data.value = { entries: makeEntries(15) }
    const wrapper = mountBrowser()
    await flushPromises()
    await wrapper.find('.page-next').trigger('click')
    await nextTick()
    expect(wrapper.findAll('.n-card')).toHaveLength(3)
    // 点击「平台技能」筛选 chip，selectedSource 变化触发页码复位。
    const platformTag = wrapper.findAll('.filter-tag').find((t) => t.text().includes('平台技能'))
    await platformTag!.trigger('click')
    await nextTick()
    expect(wrapper.findAll('.n-card')).toHaveLength(12)
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
    expect(wrapper.find('.market-card').text()).toContain('仅企业管理员可见')
    expect(wrapper.find('.market-card').text()).not.toContain('仅管理员可见')
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
