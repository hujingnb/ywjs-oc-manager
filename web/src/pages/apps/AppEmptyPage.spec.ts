import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// routerPush / routerReplace / authState 用 vi.hoisted 确保在所有 vi.mock 工厂执行前可用。
const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  isOrgAdmin: false,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush, replace: routerReplace }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

const ownAppState = vi.hoisted(() => ({ appId: undefined as string | undefined, hasApp: false }))

vi.mock('@/composables/useOwnApp', async () => {
  const { ref } = await import('vue')
  // 返回真实 ref(组件内 watch 依赖),初值取自 hoisted 状态(用例在 mount 前设置)。
  return {
    useOwnApp: () => ({
      appId: ref(ownAppState.appId),
      hasApp: ref(ownAppState.hasApp),
      isLoading: ref(false),
      app: ref(null),
    }),
  }
})

// naive-ui 命名导入在 <script setup> 中无法被 global.stubs 拦截，
// 必须在模块层面用 vi.mock 替换，确保 EmptyStub/ButtonStub 真正生效。
// EmptyStub 暴露 description 属性与 extra 插槽，便于断言文案与按钮渲染。
vi.mock('naive-ui', () => {
  const EmptyStub = defineComponent({
    props: ['description'],
    setup(props, { slots }) {
      return () => h('div', { 'data-test': 'empty', 'data-desc': props.description }, [
        slots.icon?.(),
        slots.extra?.(),
      ])
    },
  })
  const ButtonStub = defineComponent({
    emits: ['click'],
    setup(_, { slots, emit }) {
      return () => h('button', { onClick: () => emit('click') }, slots.default?.())
    },
  })
  return { NEmpty: EmptyStub, NButton: ButtonStub }
})

import { i18n } from '@/i18n'
import AppEmptyPage from './AppEmptyPage.vue'

// 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
beforeEach(() => {
  i18n.global.locale.value = 'zh'
})

function mountPage() {
  return mount(AppEmptyPage, {
    global: {
      stubs: {
        Bot: { template: '<i />' },
      },
      plugins: [i18n],
    },
  })
}

describe('AppEmptyPage', () => {
  beforeEach(() => {
    routerPush.mockClear()
    routerReplace.mockClear()
    authState.isOrgAdmin = false
    ownAppState.appId = undefined
    ownAppState.hasApp = false
  })

  // org_member(非管理员):仅提示联系管理员,无建实例按钮。
  it('shows contact-admin hint without create button for non-admin', () => {
    const wrapper = mountPage()
    expect(wrapper.find('[data-test="empty"]').attributes('data-desc')).toBe('请联系管理员创建实例')
    expect(wrapper.findAll('button').length).toBe(0)
  })

  // org_admin:显示自助建实例入口,点击跳成员页。
  it('shows self-service create entry for org_admin', async () => {
    authState.isOrgAdmin = true
    const wrapper = mountPage()
    expect(wrapper.find('[data-test="empty"]').attributes('data-desc')).toBe('你还没有属于自己的实例')
    const button = wrapper.findAll('button').find(b => b.text().includes('去成员页创建'))
    expect(button).toBeTruthy()
    await button!.trigger('click')
    expect(routerPush).toHaveBeenCalledWith('/members')
    expect(routerReplace).not.toHaveBeenCalled()
  })

  // org_admin 其实已有自有实例:空状态页自愈,自动 replace 到该实例总览。
  it('redirects org_admin to own app overview when own app exists', () => {
    authState.isOrgAdmin = true
    ownAppState.appId = 'admin-app'
    ownAppState.hasApp = true
    mountPage()
    expect(routerReplace).toHaveBeenCalledWith('/apps/admin-app/overview')
  })

  // 自愈重定向仅对 org_admin:org_member 即使(异常)有 app 标记也不跳转。
  it('does not redirect non-admin even when own app is present', () => {
    authState.isOrgAdmin = false
    ownAppState.appId = 'some-app'
    ownAppState.hasApp = true
    mountPage()
    expect(routerReplace).not.toHaveBeenCalled()
  })
})
