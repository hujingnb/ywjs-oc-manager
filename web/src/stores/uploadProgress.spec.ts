import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useUploadProgressStore } from './uploadProgress'

describe('uploadProgress store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // 单文件成功路径：item.status=succeeded，run resolve 含 1 个 succeeded。
  it('单文件成功路径', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    const runner = vi.fn().mockResolvedValue('ok')
    const result = await store.run([{ file, label: 'a.txt' }], runner)
    expect(runner).toHaveBeenCalledOnce()
    expect(result.succeeded).toHaveLength(1)
    expect(result.failed).toHaveLength(0)
    expect(result.cancelled).toHaveLength(0)
    expect(store.session?.items[0].status).toBe('succeeded')
    expect(store.isUploading).toBe(false)
  })

  // 单文件失败路径：runner 抛错，item.status=failed，error 保留 message；run 不抛。
  it('单文件失败路径，run 不抛错', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    const runner = vi.fn().mockRejectedValue(new Error('boom'))
    const result = await store.run([{ file, label: 'a.txt' }], runner)
    expect(result.failed).toHaveLength(1)
    expect(store.session?.items[0].status).toBe('failed')
    expect(store.session?.items[0].error).toBe('boom')
  })

  // 批量部分失败：第一个失败不阻止第二个执行，整体 succeeded=1 / failed=1。
  it('批量部分失败仍继续后续 item', async () => {
    const store = useUploadProgressStore()
    const file1 = new File(['x'], 'a.txt')
    const file2 = new File(['y'], 'b.txt')
    const runner = vi.fn()
      .mockRejectedValueOnce(new Error('first failed'))
      .mockResolvedValueOnce('ok')
    const result = await store.run([
      { file: file1, label: 'a.txt' },
      { file: file2, label: 'b.txt' },
    ], runner)
    expect(runner).toHaveBeenCalledTimes(2)
    expect(result.succeeded).toHaveLength(1)
    expect(result.failed).toHaveLength(1)
  })

  // 取消会让当前 runner 收到 AbortError，并把所有 pending item 标 cancelled。
  it('cancel 传播给当前 runner 与后续 pending item', async () => {
    const store = useUploadProgressStore()
    const file1 = new File(['x'], 'a.txt')
    const file2 = new File(['y'], 'b.txt')
    // 第一个 runner 永不 resolve，仅在 signal abort 时 reject AbortError，模拟真实 XHR 行为。
    const runner = vi.fn().mockImplementation(async (_item, _file, ctx) => {
      await new Promise((_, reject) => {
        ctx.signal.addEventListener('abort', () => {
          const err = new Error('aborted')
          err.name = 'AbortError'
          reject(err)
        })
      })
    })
    const promise = store.run([
      { file: file1, label: 'a.txt' },
      { file: file2, label: 'b.txt' },
    ], runner)
    // 等一个 microtask 让 runner 启动
    await Promise.resolve()
    store.cancel()
    const result = await promise
    expect(result.cancelled.length).toBeGreaterThanOrEqual(1)
    expect(store.session?.items[0].status).toBe('cancelled')
    expect(store.session?.items[1].status).toBe('cancelled')
    // 第二个 runner 不应被调用
    expect(runner).toHaveBeenCalledOnce()
  })

  // onProgress 回调把字节数写入 store.session.currentLoaded，供 Modal 渲染。
  it('runner 内 onProgress 回调更新 currentLoaded', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    let snapshot = 0
    const runner = vi.fn().mockImplementation(async (_item, _f, ctx) => {
      ctx.onProgress(50, 100)
      snapshot = store.session?.currentLoaded ?? -1
    })
    await store.run([{ file, label: 'a.txt' }], runner)
    expect(snapshot).toBe(50)
  })

  // 互斥规则：会话进行中第二次调用 run 抛错，且不影响第一次会话。
  it('会话进行中第二次 run 抛错', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    // 用永不 resolve 的 runner 保持会话激活
    const blockingRunner = vi.fn().mockImplementation(() => new Promise(() => {}))
    void store.run([{ file, label: 'a.txt' }], blockingRunner)
    await Promise.resolve()
    expect(() => store.run([{ file, label: 'b.txt' }], vi.fn())).toThrow('已有上传任务正在进行')
  })

  // reset 把 session 置空，isUploading 翻 false，再次 run 不再受互斥规则限制。
  it('reset 释放会话锁', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    const runner = vi.fn().mockResolvedValue('ok')
    await store.run([{ file, label: 'a.txt' }], runner)
    expect(store.session).not.toBeNull()
    store.reset()
    expect(store.session).toBeNull()
    expect(store.isUploading).toBe(false)
  })

  // isUploading 在会话中为 true、全部 item 结束后翻 false，但 session 在 reset 前仍保留供 Modal 展示汇总。
  it('isUploading 仅在仍有未结束 item 时为 true', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    let snapshotDuring = false
    const runner = vi.fn().mockImplementation(async () => {
      snapshotDuring = store.isUploading
    })
    await store.run([{ file, label: 'a.txt' }], runner)
    expect(snapshotDuring).toBe(true)
    expect(store.isUploading).toBe(false)
    // session 仍保留，等 reset 后才置 null（Modal 关闭时调 reset）
    expect(store.session).not.toBeNull()
  })
})
