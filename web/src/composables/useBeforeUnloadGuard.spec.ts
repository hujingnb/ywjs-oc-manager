// useBeforeUnloadGuard 组合式函数测试覆盖上传会话拦截浏览器刷新 / 关闭 tab 的行为。
// 通过手动派发 beforeunload 事件，断言 preventDefault 与 returnValue 是否被正确设置。
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useBeforeUnloadGuard } from './useBeforeUnloadGuard'
import { useUploadProgressStore } from '@/stores/uploadProgress'

describe('useBeforeUnloadGuard', () => {
  beforeEach(() => {
    // 每个用例独立的 pinia 实例，避免 store 状态跨用例污染。
    setActivePinia(createPinia())
  })

  // mount 一个挂载 guard 的组件，通过手动派发 beforeunload 事件断言 preventDefault 是否触发。
  function mountWithGuard() {
    const Host = defineComponent({
      setup() { useBeforeUnloadGuard(); return () => h('div') },
    })
    return mount(Host)
  }

  // 上传进行中：preventDefault 与 returnValue 都被设置（浏览器据此弹原生确认框）。
  it('isUploading=true 时拦截 beforeunload', () => {
    const wrapper = mountWithGuard()
    const store = useUploadProgressStore()
    // 直接构造一个最小 session 让 isUploading=true，避免依赖 run 的完整流程。
    store.session = {
      items: [{ id: '1', label: 'a', size: 10, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    // jsdom 不允许直接 new BeforeUnloadEvent，且原生 Event 的 returnValue 是 legacy boolean，
    // 直接读 event.returnValue 会拿到布尔值而非赋的字符串；用 defineProperty 覆盖 setter，
    // 单独捕获 handler 实际赋给 returnValue 的值，等价于模拟 BeforeUnloadEvent 的字符串 returnValue。
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    let assignedReturnValue: unknown = 'NOT_SET'
    Object.defineProperty(event, 'returnValue', {
      configurable: true,
      get() { return assignedReturnValue },
      set(v) { assignedReturnValue = v },
    })
    const preventSpy = vi.spyOn(event, 'preventDefault')
    window.dispatchEvent(event)
    expect(preventSpy).toHaveBeenCalled()
    expect(assignedReturnValue).toBe('')
    wrapper.unmount()
  })

  // 空闲：beforeunload 不被拦截，preventDefault 不触发。
  it('isUploading=false 时不拦截 beforeunload', () => {
    const wrapper = mountWithGuard()
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    const preventSpy = vi.spyOn(event, 'preventDefault')
    window.dispatchEvent(event)
    expect(preventSpy).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // 组件卸载后监听器应被移除，避免内存泄漏与跨测试污染。
  it('卸载组件后不再监听 beforeunload', () => {
    const wrapper = mountWithGuard()
    const store = useUploadProgressStore()
    store.session = {
      items: [{ id: '1', label: 'a', size: 10, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    wrapper.unmount()
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    const preventSpy = vi.spyOn(event, 'preventDefault')
    window.dispatchEvent(event)
    expect(preventSpy).not.toHaveBeenCalled()
  })
})
