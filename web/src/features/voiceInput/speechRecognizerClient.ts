// speechRecognizerClient：主线程侧封装 speechRecognizer.worker，
// 把 postMessage/onmessage 协议转成 Promise + 进度回调，并实现 Recognizer 接口
// (与 voiceController 对接)。识别请求用自增 id 关联响应。
import type { ModelTier, SourceId } from './voiceSettings'
import { repoForTier, hostForSource } from './voiceSettings'
import type { Recognizer } from './voiceController'

// createRecognizer 创建一个基于 Worker 的识别器实例。
export function createRecognizer(): Recognizer {
  // 惰性创建 Worker：首次用到才启动，避免进对话页就加载。
  let worker: Worker | null = null
  let reqSeq = 0
  // readyKey 记录当前 Worker 已就绪的 host::repo，用于 ready() 判定。
  let readyKey = ''

  function ensureWorker(): Worker {
    if (!worker) {
      worker = new Worker(new URL('./speechRecognizer.worker.ts', import.meta.url), { type: 'module' })
    }
    return worker
  }

  function keyOf(tier: ModelTier, source: SourceId) {
    return `${hostForSource(source)}::${repoForTier(tier)}`
  }

  return {
    ready(tier, source) {
      return readyKey === keyOf(tier, source)
    },

    ensureModel(tier, source, onProgress) {
      const w = ensureWorker()
      return new Promise<void>((resolve, reject) => {
        const handler = (ev: MessageEvent) => {
          const m = ev.data as { type: string; progress?: number; message?: string }
          if (m.type === 'progress' && typeof m.progress === 'number') onProgress(m.progress)
          else if (m.type === 'ready') {
            w.removeEventListener('message', handler)
            readyKey = keyOf(tier, source)
            resolve()
          } else if (m.type === 'error') {
            w.removeEventListener('message', handler)
            reject(new Error(m.message ?? 'unknown error'))
          }
        }
        w.addEventListener('message', handler)
        w.postMessage({ type: 'load', repo: repoForTier(tier), host: hostForSource(source) })
      })
    },

    transcribe(pcm, language) {
      const w = ensureWorker()
      const id = ++reqSeq
      return new Promise<string>((resolve, reject) => {
        const handler = (ev: MessageEvent) => {
          const m = ev.data as { type: string; id?: number; text?: string; message?: string }
          if (m.id !== id) return
          w.removeEventListener('message', handler)
          if (m.type === 'result') resolve(m.text ?? '')
          else if (m.type === 'error') reject(new Error(m.message ?? 'unknown error'))
        }
        w.addEventListener('message', handler)
        // pcm.buffer 作为 transferable 转移所有权，避免大数组拷贝。
        w.postMessage({ type: 'transcribe', id, pcm, language }, [pcm.buffer])
      })
    },

    dispose() {
      // 终止 Worker，释放其加载的 whisper 模型占用的内存与 WebGPU 资源；
      // readyKey 一并清空，下次使用会重新惰性创建 Worker 并重载模型。
      worker?.terminate()
      worker = null
      readyKey = ''
    },
  }
}
