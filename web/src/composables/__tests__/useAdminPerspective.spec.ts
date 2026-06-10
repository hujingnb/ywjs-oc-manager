import { beforeEach, describe, expect, it, vi } from 'vitest'

// useAdminPerspective 用模块级单例 ref;每个用例前清 localStorage + resetModules,
// 再动态 import,保证每次拿到「按当前 localStorage 初始化」的全新单例,用例间互不串状态。
async function loadComposable() {
  return (await import('../useAdminPerspective')).useAdminPerspective()
}

describe('useAdminPerspective', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  // 无持久化值时默认落「企业管理」视角(manage)
  it('defaults to manage when nothing stored', async () => {
    const { perspective } = await loadComposable()
    expect(perspective.value).toBe('manage')
  })

  // setPerspective 写 localStorage;重新加载模块(模拟刷新)应从持久化恢复实例视角
  it('persists perspective to localStorage', async () => {
    const { setPerspective } = await loadComposable()
    setPerspective('instance')
    expect(localStorage.getItem('oc.admin.perspective')).toBe('instance')
    vi.resetModules()
    const { perspective } = await loadComposable()
    expect(perspective.value).toBe('instance')
  })

  // resetPerspective 清持久化并回默认(退出登录场景)
  it('clears persistence on reset', async () => {
    const { setPerspective, resetPerspective, perspective } = await loadComposable()
    setPerspective('instance')
    resetPerspective()
    expect(perspective.value).toBe('manage')
    expect(localStorage.getItem('oc.admin.perspective')).toBeNull()
  })

  // 非法持久化值回退默认,避免脏数据污染视角
  it('falls back to manage on invalid stored value', async () => {
    localStorage.setItem('oc.admin.perspective', 'bogus')
    const { perspective } = await loadComposable()
    expect(perspective.value).toBe('manage')
  })
})
