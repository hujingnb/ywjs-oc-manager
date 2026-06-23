// uploadProgress store 用一个会话集中管理多文件串行上传的状态：当前文件、字节进度、取消句柄。
// 一次只允许一个会话（互斥规则），保证全局 UploadProgressModal 不会被两个并发上传争用。
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

// UploadItemStatus 标记单文件在会话中的生命周期阶段。
export type UploadItemStatus = 'pending' | 'uploading' | 'succeeded' | 'failed' | 'cancelled'

// UploadItem 是会话内单个文件的视图。
export interface UploadItem {
  // 自动生成的 id；Modal 用作 v-for key、失败列表去重，便于排错。
  id: string
  // 显示名，一般是 file.name；批量上传时业务侧可传更易读的标签。
  label: string
  // 字节数，模板算 % 时分母用它。
  size: number
  status: UploadItemStatus
  // 仅 failed 用，文案来自 mutation 抛出的 Error.message。
  error?: string
}

// UploadSession 描述一次 run() 调用的全局状态。
export interface UploadSession {
  items: UploadItem[]
  // 0-based 指向当前正在传的 item。
  currentIndex: number
  // 当前 item 已传字节，由 runner 内 ctx.onProgress 回调写入。
  currentLoaded: number
  // 当前 item 是否进入服务端「合并/收尾」阶段（分片上传 complete 期间）：
  // 此时字节已传完(100%)但服务端仍在合并并推送 RAGFlow，UI 据此显示「合并中…」而非干卡 100%。
  // 可选：run() 总会初始化为 false，但部分测试夹具构造 session 时不关心该字段。
  currentFinalizing?: boolean
  // 会话起始时间戳；v1 不渲染速率，仅留作 log。
  startedAt: number
}

// RunItem 是 run() 入参的最小形态：调用方提供 file 与 label。
export interface RunItem {
  file: File
  label?: string
}

// RunnerContext 由 store 注入给 runner：onProgress 上报字节进度，signal 响应取消，
// onFinalizing 在字节传完、进入服务端合并阶段时调用（仅分片上传用，直传不调）。
export interface RunnerContext {
  onProgress: (loaded: number, total: number) => void
  signal: AbortSignal
  onFinalizing: () => void
}

// Runner 是业务侧上传函数：调用对应 mutation hook 的 mutateAsync 并把 ctx 透传给 hook。
export type RunnerFn<T> = (item: UploadItem, file: File, ctx: RunnerContext) => Promise<T>

// RunResult 汇总会话结束时的成功 / 失败 / 取消项与 runner 返回值。
// results 与 succeeded 一一对应，只在成功路径上累加，便于业务侧拿到 mutation 结果。
export interface RunResult<T> {
  succeeded: UploadItem[]
  failed: UploadItem[]
  cancelled: UploadItem[]
  results: T[]
}

export const useUploadProgressStore = defineStore('uploadProgress', () => {
  const session = ref<UploadSession | null>(null)
  // 当前会话的 AbortController；cancel() 调用它的 abort()，单 item 结束后丢弃。
  let currentAbort: AbortController | null = null

  // isUploading 仅在仍有未结束 item 时为 true；Modal 据此决定显示「取消」还是「关闭」按钮。
  const isUploading = computed(() => {
    if (!session.value) return false
    return session.value.items.some(i => i.status === 'pending' || i.status === 'uploading')
  })

  // run 顺序执行 items，失败的 item 不阻塞后续；返回汇总结果；不主动抛错（除互斥规则）。
  // 注意：互斥规则的抛错必须同步发生（业务侧用 try/catch 直接捕获，不是 .catch），
  // 因此外层不能用 async function——async 会把 throw 自动包成 rejected promise。
  function run<T>(items: RunItem[], runner: RunnerFn<T>): Promise<RunResult<T>> {
    // 互斥：会话进行中第二次 run 抛错，业务页应 catch 并用 n-message 提示用户。
    if (session.value && isUploading.value) {
      throw new Error('已有上传任务正在进行')
    }

    const initialItems: UploadItem[] = items.map((it, idx) => ({
      id: makeId(idx),
      label: it.label ?? it.file.name,
      size: it.file.size,
      status: 'pending',
    }))
    session.value = {
      items: initialItems,
      currentIndex: 0,
      currentLoaded: 0,
      currentFinalizing: false,
      startedAt: Date.now(),
    }
    // 必须通过 session.value.items 拿到响应式代理后的数组；直接复用 initialItems 闭包会绕过 Vue 的响应式
    // 追踪，computed(isUploading) 不会重算，Modal/按钮态都会停在错误的旧值。
    const reactiveItems = session.value.items
    const activeSession = session.value

    // 串行执行用 async IIFE 包起来，让外层 run 同步抛互斥错；其余逻辑沿用原来的 await/for 写法。
    return (async (): Promise<RunResult<T>> => {
      const results: T[] = []
      let cancelledByUser = false

      for (let i = 0; i < items.length; i++) {
        const item = reactiveItems[i]
        // 用户已通过 cancel() 中断会话：把当前及后续 item 全部标 cancelled，跳过 runner。
        if (cancelledByUser) {
          item.status = 'cancelled'
          continue
        }
        activeSession.currentIndex = i
        activeSession.currentLoaded = 0
        activeSession.currentFinalizing = false
        item.status = 'uploading'
        currentAbort = new AbortController()
        try {
          const result = await runner(item, items[i].file, {
            // 守卫：只在 session 仍指向当前 item 时回写字节数。
            // 防止前一文件结束后浏览器仍然投递的延迟 progress 事件覆盖下一文件已开始的进度。
            onProgress: (loaded) => {
              if (session.value && session.value.currentIndex === i) {
                session.value.currentLoaded = loaded
              }
            },
            // 进入合并阶段：同样加 currentIndex 守卫，避免延迟回调污染下一文件。
            onFinalizing: () => {
              if (session.value && session.value.currentIndex === i) {
                session.value.currentFinalizing = true
              }
            },
            signal: currentAbort.signal,
          })
          item.status = 'succeeded'
          results.push(result)
        } catch (err) {
          // AbortError 来自 store.cancel() 或上游 xhrUpload 的 signal abort：标记 cancelled 并停掉后续。
          if (err instanceof Error && err.name === 'AbortError') {
            item.status = 'cancelled'
            cancelledByUser = true
          } else {
            item.status = 'failed'
            item.error = err instanceof Error ? err.message : '上传失败'
          }
        } finally {
          currentAbort = null
        }
      }

      return {
        succeeded: reactiveItems.filter(i => i.status === 'succeeded'),
        failed: reactiveItems.filter(i => i.status === 'failed'),
        cancelled: reactiveItems.filter(i => i.status === 'cancelled'),
        results,
      }
    })()
  }

  // cancel 中断当前 runner；后续 pending item 由 run 循环检测 cancelledByUser 后跳过。
  // 不抛错、不 resolve 任何 promise；run() 自然走到循环结束并 resolve。
  function cancel(): void {
    currentAbort?.abort()
  }

  // reset 把 session 置空，让 Modal 隐藏、beforeunload 守卫解除。
  // 仅允许在 isUploading=false 时调用；进行中调用是 UI bug，但这里防御性允许（abort 一次再清）。
  function reset(): void {
    if (currentAbort) {
      currentAbort.abort()
      currentAbort = null
    }
    session.value = null
  }

  return { session, isUploading, run, cancel, reset }
})

// makeId 生成一个本地唯一 id。优先用浏览器原生 randomUUID；不可用时退回到 idx + 时间戳。
function makeId(idx: number): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${idx}`
}
