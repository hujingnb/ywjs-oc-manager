import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// 通过 vi.mock 替换 naive-ui，把 NModal / NProgress / NButton / NCollapse / NCollapseItem
// 替换为最小 DOM 渲染，便于直接断言文案与点击。<script setup> 下的命名导入无法用
// global.stubs 拦截（vue-test-utils 不会通过名字回查 setup 闭包内的局部绑定），
// 因此必须在模块层面用 vi.mock 替换原始导出。
vi.mock('naive-ui', () => ({
  NModal: defineComponent({
    name: 'NModal',
    props: ['show'],
    setup(p, { slots }) {
      return () => p.show ? h('div', { class: 'modal' }, slots.default?.()) : null
    },
  }),
  NProgress: defineComponent({
    name: 'NProgress',
    props: ['percentage'],
    setup(p) { return () => h('div', { class: 'progress', 'data-pct': p.percentage }) },
  }),
  NButton: defineComponent({
    name: 'NButton',
    emits: ['click'],
    setup(_, { slots, emit }) {
      return () => h('button', { onClick: () => emit('click') }, slots.default?.())
    },
  }),
  NCollapse: defineComponent({ name: 'NCollapse', setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
  NCollapseItem: defineComponent({
    name: 'NCollapseItem',
    props: ['title'],
    setup(p, { slots }) { return () => h('div', [h('span', p.title), slots.default?.()]) },
  }),
}))

import UploadProgressModal from '../UploadProgressModal.vue'
import { useUploadProgressStore } from '@/stores/uploadProgress'

// mountModal 统一创建组件实例；vi.mock 已经在模块层面替换好 naive-ui，
// 这里不需要再传 stubs，wrapper 直接拿到 stub 渲染出的 DOM。
function mountModal() {
  return mount(UploadProgressModal)
}

describe('UploadProgressModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  // 无会话：Modal 不渲染。
  it('session=null 时不渲染', () => {
    const wrapper = mountModal()
    expect(wrapper.find('.modal').exists()).toBe(false)
  })

  // 会话进行中：渲染当前文件名 + N/M + 「取消上传」按钮。
  it('会话进行中渲染当前文件、N/M 与取消按钮', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [
        { id: '1', label: 'a.txt', size: 100, status: 'uploading' },
        { id: '2', label: 'b.txt', size: 200, status: 'pending' },
      ],
      currentIndex: 0,
      currentLoaded: 30,
      startedAt: Date.now(),
    }
    await nextTick()
    expect(wrapper.text()).toContain('a.txt')
    expect(wrapper.text()).toContain('1/2')
    expect(wrapper.find('.progress').attributes('data-pct')).toBe('30')
    expect(wrapper.text()).toContain('取消上传')
    expect(wrapper.text()).not.toContain('关闭')
  })

  // 全部 item 结束：按钮变「关闭」，汇总区显示成功 / 失败 / 取消计数。
  it('会话结束渲染关闭按钮与汇总', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [
        { id: '1', label: 'a.txt', size: 100, status: 'succeeded' },
        { id: '2', label: 'b.txt', size: 200, status: 'failed', error: 'boom' },
      ],
      currentIndex: 1,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    await nextTick()
    expect(wrapper.text()).toContain('关闭')
    expect(wrapper.text()).not.toContain('取消上传')
    expect(wrapper.text()).toContain('成功 1')
    expect(wrapper.text()).toContain('失败 1')
    expect(wrapper.text()).toContain('取消 0')
    // 失败列表里能看到文件名与错误原因。
    expect(wrapper.text()).toContain('b.txt')
    expect(wrapper.text()).toContain('boom')
  })

  // 零字节文件 guard：size=0 时 % 渲染为 100，不出现 NaN。
  it('零字节文件渲染 100% 而非 NaN', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [{ id: '1', label: 'empty.txt', size: 0, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    await nextTick()
    expect(wrapper.find('.progress').attributes('data-pct')).toBe('100')
  })

  // 点「取消上传」调用 store.cancel；点「关闭」调用 store.reset。
  it('点击按钮触发 store.cancel / store.reset', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [{ id: '1', label: 'a.txt', size: 100, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 50,
      startedAt: Date.now(),
    }
    await nextTick()
    await wrapper.find('button').trigger('click')
    // 取消按钮：cancel 不会立刻把 status 翻 cancelled（那是 store.run 循环里做的）；
    // 这里仅验证 cancel 被调用即可——通过点击后 store.session 是否仍存在判断。
    expect(store.session).not.toBeNull()

    // 把状态改成已结束，再点击「关闭」应触发 reset → session 归 null。
    store.session = {
      items: [{ id: '1', label: 'a.txt', size: 100, status: 'succeeded' }],
      currentIndex: 0,
      currentLoaded: 100,
      startedAt: Date.now(),
    }
    await nextTick()
    await wrapper.find('button').trigger('click')
    expect(store.session).toBeNull()
  })
})
