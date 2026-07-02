// speechRecognizer.worker：在 Web Worker 里跑 whisper 推理，避免冻结主线程。
// 协议：主线程发 {type:'load'|'transcribe'}，Worker 回 {type:'progress'|'ready'|'result'|'error'}。
// 模型按所选下载源(remoteHost)与仓库名加载，优先 WebGPU，否则单线程 WASM
// (刻意不用多线程 WASM，避免需要 COOP/COEP 跨源隔离头)。
import { pipeline, env, type AutomaticSpeechRecognitionPipeline } from '@huggingface/transformers'

// 禁用本地模型查找，仅走远端(自选源)。
env.allowLocalModels = false

// LoadMsg 加载模型请求；dtype 为该档位的量化精度(不同档位 onnx 量化不同，由主线程按档位传入)。
interface LoadMsg { type: 'load'; repo: string; host: string; dtype: string | Record<string, string> }
// TranscribeMsg 识别请求；pcm 为 16kHz 单声道 Float32(以 transferable 传入)。
interface TranscribeMsg { type: 'transcribe'; id: number; pcm: Float32Array; language: string }
type InMsg = LoadMsg | TranscribeMsg

// asr 缓存当前已加载的 pipeline，key 为 host+repo，切源/切档位时重建。
let asr: AutomaticSpeechRecognitionPipeline | null = null
let loadedKey = ''

// ── 下载总进度聚合 ──
// Transformers.js 的 progress 事件是「按文件」各自 0→100 的，且多文件并发下载、乱序穿插；
// 直接转发单个文件的百分比会在文件切换时从高跳回低（进度条倒退，即用户看到的现象）。
// 改为按字节聚合所有文件的 loaded/total 得到一条总进度，并做「单调不回退」+ 下载阶段封顶 99%
// （满格 100% 留给加载完成时收口），既消除倒退，也避免小文件先下完导致过早满格。
const fileBytes = new Map<string, { loaded: number; total: number }>()
let lastProgress = 0

// resetProgress 每次开始加载新模型时清空聚合状态。
function resetProgress() {
  fileBytes.clear()
  lastProgress = 0
}

// reportAggregateProgress 累计各文件字节并回传总进度（单调递增、封顶 0.99）。
function reportAggregateProgress(info: { file?: string; name?: string; loaded?: number; total?: number }) {
  const file = info.file ?? info.name
  if (!file || typeof info.total !== 'number' || info.total <= 0) return
  fileBytes.set(file, { loaded: info.loaded ?? 0, total: info.total })
  let loaded = 0
  let total = 0
  for (const v of fileBytes.values()) {
    loaded += v.loaded
    total += v.total
  }
  if (total <= 0) return
  const frac = Math.min(0.99, loaded / total)
  if (frac > lastProgress) {
    lastProgress = frac
    postMessage({ type: 'progress', progress: frac })
  }
}

// pickDevice 选择推理后端：仅当确有可用 WebGPU adapter 时才用 webgpu(快很多)，否则单线程 wasm。
// 关键：`'gpu' in navigator` 为真只代表浏览器暴露了 WebGPU API，并不代表有可用适配器——
// headless、用户禁用 WebGPU、GPU 在黑名单、部分 Linux 环境下 requestAdapter() 会返回 null。
// 若此时仍选 webgpu，pipeline 加载会失败，用户会看到「下载失败」而其实 wasm 完全可用。
// 因此必须实际探测 adapter；任何异常也按 wasm 处理。
async function pickDevice(): Promise<'webgpu' | 'wasm'> {
  try {
    const nav = typeof navigator !== 'undefined' ? (navigator as Navigator & { gpu?: { requestAdapter(): Promise<unknown> } }) : undefined
    if (nav?.gpu) {
      const adapter = await nav.gpu.requestAdapter()
      if (adapter) return 'webgpu'
    }
  } catch {
    // 探测过程本身抛错(极少数环境)，按 wasm 处理。
  }
  return 'wasm'
}

// buildPipeline 按指定后端构建 ASR pipeline；进度经 progress_callback 归一化后回传主线程。
// pipeline 泛型在推导 AllTasks 联合类型时复杂度超出 TypeScript 上限(TS2590)。
// 用局部类型断言把 pipeline 收窄为只返回 AutomaticSpeechRecognitionPipeline 的函数，
// 运行时行为完全不变：仍传相同的 task/model/options 参数。
async function buildPipeline(repo: string, device: 'webgpu' | 'wasm', dtype: string | Record<string, string>) {
  type PipelineFn = (
    task: string,
    model: string,
    opts?: Record<string, unknown>,
  ) => Promise<AutomaticSpeechRecognitionPipeline>
  return (pipeline as unknown as PipelineFn)('automatic-speech-recognition', repo, {
    device,
    dtype,
    // progress_callback 参数类型为 ProgressCallback=(p: ProgressInfo)=>void，
    // 用 unknown 接收可绕过 strictFunctionTypes 约束(unknown 是 ProgressInfo 的超类型，
    // 满足参数逆变要求)；内部通过类型缩窄安全访问字段，按字节聚合成总进度。
    progress_callback: (p: unknown) => {
      const info = p as { status?: string; file?: string; name?: string; loaded?: number; total?: number }
      // 仅下载中的 progress 事件带 loaded/total 字节，用于聚合总进度。
      if (info.status === 'progress') reportAggregateProgress(info)
    },
  })
}

// loadModel 按源+仓库加载 pipeline；优先 webgpu，若 webgpu 构建失败(如适配器探测漏网的边缘情况)兜底重试 wasm。
async function loadModel(repo: string, host: string, dtype: string | Record<string, string>) {
  const key = `${host}::${repo}`
  if (asr && loadedKey === key) {
    postMessage({ type: 'ready' })
    return
  }
  env.remoteHost = host
  resetProgress()
  const device = await pickDevice()
  try {
    asr = await buildPipeline(repo, device, dtype)
  } catch (e) {
    // webgpu 探测通过但实际构建仍失败时(驱动/适配器边缘问题)，回退单线程 wasm 再试一次。
    if (device === 'webgpu') {
      asr = await buildPipeline(repo, 'wasm', dtype)
    } else {
      throw e
    }
  }
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
    if (msg.type === 'load') await loadModel(msg.repo, msg.host, msg.dtype)
    else if (msg.type === 'transcribe') await runTranscribe(msg)
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e)
    postMessage({ type: 'error', id: (msg as TranscribeMsg).id, message })
  }
}
