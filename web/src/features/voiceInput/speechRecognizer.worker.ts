// speechRecognizer.worker：在 Web Worker 里跑 whisper 推理，避免冻结主线程。
// 协议：主线程发 {type:'load'|'transcribe'}，Worker 回 {type:'progress'|'ready'|'result'|'error'}。
// 模型按所选下载源(remoteHost)与仓库名加载，优先 WebGPU，否则单线程 WASM
// (刻意不用多线程 WASM，避免需要 COOP/COEP 跨源隔离头)。
import { pipeline, env, type AutomaticSpeechRecognitionPipeline } from '@huggingface/transformers'

// 禁用本地模型查找，仅走远端(自选源)。
env.allowLocalModels = false

// LoadMsg 加载模型请求。
interface LoadMsg { type: 'load'; repo: string; host: string }
// TranscribeMsg 识别请求；pcm 为 16kHz 单声道 Float32(以 transferable 传入)。
interface TranscribeMsg { type: 'transcribe'; id: number; pcm: Float32Array; language: string }
type InMsg = LoadMsg | TranscribeMsg

// asr 缓存当前已加载的 pipeline，key 为 host+repo，切源/切档位时重建。
let asr: AutomaticSpeechRecognitionPipeline | null = null
let loadedKey = ''

// pickDevice WebGPU 可用则用之(快很多)，否则退回 wasm。
function pickDevice(): 'webgpu' | 'wasm' {
  return typeof navigator !== 'undefined' && 'gpu' in navigator ? 'webgpu' : 'wasm'
}

// loadModel 按源+仓库加载 pipeline；进度经 progress_callback 归一化后回传主线程。
async function loadModel(repo: string, host: string) {
  const key = `${host}::${repo}`
  if (asr && loadedKey === key) {
    postMessage({ type: 'ready' })
    return
  }
  env.remoteHost = host
  // pipeline 泛型在推导 AllTasks 联合类型时复杂度超出 TypeScript 上限(TS2590)。
  // 用局部类型断言把 pipeline 收窄为只返回 AutomaticSpeechRecognitionPipeline 的函数，
  // 运行时行为完全不变：仍传相同的 task/model/options 参数。
  type PipelineFn = (
    task: string,
    model: string,
    opts?: Record<string, unknown>,
  ) => Promise<AutomaticSpeechRecognitionPipeline>
  asr = await (pipeline as unknown as PipelineFn)('automatic-speech-recognition', repo, {
    device: pickDevice(),
    dtype: 'q8',
    // progress_callback 参数类型为 ProgressCallback=(p: ProgressInfo)=>void，
    // 用 unknown 接收可绕过 strictFunctionTypes 约束(unknown 是 ProgressInfo 的超类型，
    // 满足参数逆变要求)；内部通过类型缩窄安全访问 status/progress 字段。
    progress_callback: (p: unknown) => {
      // ProgressInfo 的 DownloadProgressInfo 成员满足 status==='progress' 且含 progress(0..100)。
      const info = p as { status?: string; progress?: number }
      if (info.status === 'progress' && typeof info.progress === 'number') {
        postMessage({ type: 'progress', progress: Math.max(0, Math.min(1, info.progress / 100)) })
      }
    },
  })
  loadedKey = key
  postMessage({ type: 'ready' })
}

// runTranscribe 执行识别；language 传给 whisper 以提升目标语种准确率。
async function runTranscribe(msg: TranscribeMsg) {
  if (!asr) {
    postMessage({ type: 'error', id: msg.id, message: 'model-not-loaded' })
    return
  }
  // ASR 调用返回 AutomaticSpeechRecognitionOutput | AutomaticSpeechRecognitionOutput[]，
  // 统一取第一个元素获取 text 字段。
  const raw = await asr(msg.pcm, { language: msg.language, task: 'transcribe' })
  const out = Array.isArray(raw) ? raw[0] : raw
  postMessage({ type: 'result', id: msg.id, text: out.text ?? '' })
}

// onmessage 分发主线程请求；任何异常统一回 error。
// DOM lib 中 self.onmessage 的参数为 MessageEvent(无泛型)，内部通过 as InMsg 缩窄。
self.onmessage = async (ev: MessageEvent) => {
  const msg = ev.data as InMsg
  try {
    if (msg.type === 'load') await loadModel(msg.repo, msg.host)
    else if (msg.type === 'transcribe') await runTranscribe(msg)
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e)
    postMessage({ type: 'error', id: (msg as TranscribeMsg).id, message })
  }
}
