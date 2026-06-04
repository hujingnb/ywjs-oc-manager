// SkillDetailDrawer.spec.ts — SkillDetailDrawer 详情抽屉单元测试。
// 覆盖：版本列表「最新/当前/changelog」渲染、富详情描述优先、内置无版本信息、
// allowVersionPick 时版本行点「添加此版本」emit pick-version 带该版本。
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import SkillDetailDrawer from './SkillDetailDrawer.vue'

// ======================== 可变 reactive 状态 ========================
// detailState 控制 useSkillDetailQuery 的返回值（富详情 + 版本列表）。
const detailState = {
  data: ref<{
    detail: Record<string, unknown>
    versions: { version: string; changelog?: string; published_at?: number }[]
  }>({ detail: {}, versions: [] }),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}

// dlMocks 在 vi.mock 提升前创建，承载 downloadSkillArchive 与 useMessage().error 的桩，供断言。
const dlMocks = vi.hoisted(() => ({
  downloadSkillArchive: vi.fn(),
  messageError: vi.fn(),
}))

// ======================== vi.mock ========================
vi.mock('@/api/hooks/useSkills', () => ({
  // useSkillDetailQuery 用 detailState 控制，供测试可变注入。
  useSkillDetailQuery: () => detailState,
  // downloadSkillArchive 桩：断言下载按钮以正确 source/ref/版本调用。
  downloadSkillArchive: dlMocks.downloadSkillArchive,
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    // useMessage 桩：抽屉下载失败时调用 error，单测用 dlMocks.messageError 断言。
    useMessage: () => ({ error: dlMocks.messageError, success: vi.fn() }),
    // NDrawer/NDrawerContent stub：show=true 时渲染内容，用于断言抽屉内文案。
    NDrawer: { props: ['show'], template: '<div v-if="show" class="n-drawer"><slot /></div>' },
    NDrawerContent: { props: ['title'], template: '<div class="n-drawer-content">{{ title }}<slot /></div>' },
    // NTag stub 渲染为 span。
    NTag: { template: '<span class="n-tag" v-bind="$attrs"><slot /></span>' },
    // NButton stub 渲染为 button。
    NButton: { template: '<button class="n-button" v-bind="$attrs"><slot /></button>' },
  }
})

// ======================== 挂载辅助 ========================
// mountDrawer：打开抽屉并传入指定 skill 数据。
function mountDrawer(skill: InstanceType<typeof SkillDetailDrawer>['$props']['skill'], extra: Record<string, unknown> = {}) {
  return mount(SkillDetailDrawer, {
    props: {
      show: true,
      skill,
      ...extra,
    },
  })
}

// ======================== 测试套件 ========================
describe('SkillDetailDrawer', () => {
  beforeEach(() => {
    // 每个用例前重置 detailState 为空白状态。
    detailState.data.value = { detail: {}, versions: [] }
    detailState.isLoading.value = false
    detailState.error.value = null
    // 重置下载桩。
    dlMocks.downloadSkillArchive.mockReset()
    dlMocks.messageError.mockReset()
  })

  // ======== 版本列表渲染 ========

  it('版本列表展示「最新」/「当前」标记与 changelog', async () => {
    // 覆盖：版本列表首个条目标「最新」，匹配 skill.version 的条目标「当前」，
    // 含 changelog 时渲染在版本行下方。
    detailState.data.value = {
      detail: { description: '完整描述' },
      versions: [
        { version: '2.0.0', changelog: '新增 X 功能' },
        { version: '1.0.0' },
      ],
    }
    const wrapper = mountDrawer({
      name: 'oc-clawtest',
      source: 'clawhub',
      source_ref: 'oc-clawtest',
      version: '1.0.0',
    })
    await nextTick()
    const drawer = wrapper.find('.n-drawer')
    expect(drawer.text()).toContain('版本列表')
    expect(drawer.text()).toContain('v2.0.0')
    // 首个版本标「最新」。
    expect(drawer.text()).toContain('最新')
    // 匹配当前安装版本 1.0.0，标「当前」。
    expect(drawer.text()).toContain('当前')
    // changelog 渲染在版本行下方。
    expect(drawer.text()).toContain('新增 X 功能')
  })

  it('富详情描述优先于卡片摘要', async () => {
    // 覆盖：detail.description 优先于 skill.description 展示（避免截断摘要）。
    detailState.data.value = {
      detail: { description: '完整未截断描述' },
      versions: [{ version: '3.0.21' }],
    }
    const wrapper = mountDrawer({
      name: 'Self-Improving Agent',
      source: 'clawhub',
      source_ref: 'self-improving-agent',
      version: '3.0.21',
      description: '摘要(截断)',
    })
    await nextTick()
    const drawer = wrapper.find('.n-drawer')
    // 富详情描述优先。
    expect(drawer.text()).toContain('完整未截断描述')
    // 截断摘要不应出现。
    expect(drawer.text()).not.toContain('摘要(截断)')
  })

  it('富详情含作者/下载量/星标时正确渲染统计信息', async () => {
    // 覆盖：clawhub skill 富详情展示作者名、下载量带单位（457324 → 45.7万）、星标计数。
    detailState.data.value = {
      detail: {
        description: '完整未截断描述',
        author_name: 'pskoett',
        stars: 3735,
        downloads: 457324,
      },
      versions: [{ version: '3.0.21', changelog: 're-upload' }],
    }
    const wrapper = mountDrawer({
      name: 'Self-Improving Agent',
      source: 'clawhub',
      source_ref: 'self-improving-agent',
      version: '3.0.21',
      description: '摘要(截断)',
    })
    await nextTick()
    const drawer = wrapper.find('.n-drawer')
    expect(drawer.text()).toContain('pskoett')
    // 下载量带单位（457324 → 45.7万）。
    expect(drawer.text()).toContain('45.7万')
    expect(drawer.text()).not.toContain('457324')
    // 统计区有「星标」文案。
    expect(drawer.text()).toContain('星标')
    // 不含「评论」（评论字段被刻意去除）。
    expect(drawer.text()).not.toContain('评论')
  })

  // ======== 内置 skill ========

  it('builtin skill 无来源标识时显示「该来源无版本信息」', async () => {
    // 边界：builtin skill 无 source/source_ref，hasUpstream=false，版本区显示「该来源无版本信息」；
    // 「来源」一栏应显示「内置」而非空。
    const wrapper = mountDrawer({
      name: 'airtable',
      status: 'builtin',
      source: undefined,
      version: '内置',
      description: 'Airtable 内置技能介绍',
    })
    await nextTick()
    const drawer = wrapper.find('.n-drawer')
    // 来自容器 SKILL.md 的描述。
    expect(drawer.text()).toContain('Airtable 内置技能介绍')
    // 无版本信息提示。
    expect(drawer.text()).toContain('该来源无版本信息')
    // 来源一栏显示「内置」而非空。
    expect(drawer.text()).toContain('来源内置')
  })

  // ======== allowVersionPick 锁旧版 ========

  it('allowVersionPick 时版本行点「添加此版本」emit pick-version 带该版本', async () => {
    // 覆盖：版本场景下每个版本行可锁定加入，emit 携带该行版本号（而非最新版）。
    detailState.data.value = {
      detail: { description: 'd' },
      versions: [{ version: '2.0.0' }, { version: '1.0.0' }],
    }
    const wrapper = mountDrawer(
      { name: 'sv', source: 'clawhub', source_ref: 'sv', version: '2.0.0' },
      { allowVersionPick: true, existingNames: new Set<string>() },
    )
    await nextTick()
    // 找「添加此版本」按钮（首个版本行）。
    const btn = wrapper.findAll('button').find((b) => b.text().includes('添加此版本'))
    expect(btn).toBeTruthy()
    await btn!.trigger('click')
    // emit pick-version 应携带点击行的版本号。
    expect(wrapper.emitted('pick-version')?.[0]).toEqual(['2.0.0'])
  })

  // ======== allowDownload 下载（仅平台管理员） ========

  it('allowDownload 时版本行展示「下载」并点击调用 downloadSkillArchive（带 source/ref/版本）', async () => {
    // 覆盖：平台管理员可在每个版本行下载该版本归档，按来源（platform）与点击行版本号调用。
    dlMocks.downloadSkillArchive.mockResolvedValue(undefined)
    detailState.data.value = {
      detail: { description: 'd' },
      versions: [{ version: '2.0.0' }, { version: '1.0.0' }],
    }
    const wrapper = mountDrawer(
      { name: 'sv', source: 'platform', source_ref: 'sv', version: '2.0.0' },
      { allowDownload: true },
    )
    await nextTick()
    // 首个版本行（2.0.0）的「下载」按钮。
    const btn = wrapper.findAll('button').find((b) => b.text().trim() === '下载')
    expect(btn).toBeTruthy()
    await btn!.trigger('click')
    // 以 source=platform、ref=sv、首行版本 2.0.0 调用下载。
    expect(dlMocks.downloadSkillArchive).toHaveBeenCalledWith('platform', 'sv', '2.0.0')
  })

  it('未授权（默认 allowDownload=false）时版本行无「下载」按钮', async () => {
    // 边界：非平台管理员不传 allowDownload，版本行不应出现「下载」按钮。
    detailState.data.value = {
      detail: { description: 'd' },
      versions: [{ version: '1.0.0' }],
    }
    const wrapper = mountDrawer({ name: 'sv', source: 'platform', source_ref: 'sv', version: '1.0.0' })
    await nextTick()
    const btn = wrapper.findAll('button').find((b) => b.text().trim() === '下载')
    expect(btn).toBeUndefined()
  })

  it('existingNames 命中时版本行按钮显示「已添加」且禁用', async () => {
    // 边界：已配置/已安装同名 skill 时，版本行按钮变为「已添加」并禁用，防止重复添加。
    detailState.data.value = {
      detail: { description: 'd' },
      versions: [{ version: '1.0.0' }],
    }
    const wrapper = mountDrawer(
      { name: 'sv', source: 'clawhub', source_ref: 'sv', version: '1.0.0' },
      { allowVersionPick: true, existingNames: new Set(['sv']) },
    )
    await nextTick()
    // 按钮文案变为「已添加」。
    const btn = wrapper.findAll('button').find((b) => b.text().includes('已添加'))
    expect(btn).toBeTruthy()
    // 按钮应处于 disabled 状态（NButton stub 透传 $attrs）。
    expect(btn!.attributes('disabled')).toBeDefined()
  })
})
