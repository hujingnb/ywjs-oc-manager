import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// routerPush / authState 用 vi.hoisted 确保在所有 vi.mock 工厂执行前可用。
const routerPush = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  isOrgAdmin: false,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

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

import AppEmptyPage from './AppEmptyPage.vue'

function mountPage() {
  return mount(AppEmptyPage, {
    global: {
      stubs: {
        Bot: { template: '<i />' },
      },
    },
  })
}

describe('AppEmptyPage', () => {
  beforeEach(() => {
    routerPush.mockClear()
    authState.isOrgAdmin = false
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
  })
})
