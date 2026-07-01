// voiceController：语音输入的编排状态机。
// 用依赖注入(录音器/识别器/解码/语言/落文本)以便脱离浏览器 API 单测；
// 真实依赖由 useVoiceInput.ts 组装。状态机管理一次「点击录音→再点结束→识别→落草稿」
// 的完整生命周期，并把首次未选档位、下载进度、各类错误映射成 UI 可消费的响应式状态。
import { ref, type Ref } from 'vue'
import type { ModelTier, SourceId, VoiceSettings } from './voiceSettings'

// VoiceState 状态机取值。
// idle=空闲；requesting=正在申请麦克风；recording=录音中；
// transcribing=解码+识别中；downloading=下载模型中。
export type VoiceState = 'idle' | 'requesting' | 'recording' | 'transcribing' | 'downloading'

// VoiceErrorKey 错误种类，对应 i18n voice.errors.* 文案键。
export type VoiceErrorKey = 'permissionDenied' | 'notSupported' | 'noSpeech' | 'downloadFailed' | 'transcribeFailed'

// Recorder 录音器接口：start 申请麦克风并开始，stop 结束并返回录音 Blob。
export interface Recorder {
  start(): Promise<void>
  stop(): Promise<Blob>
}

// Recognizer 识别器接口：ready 查询某档位/源是否已就绪，ensureModel 加载(下载)模型并吐进度，
// transcribe 把 16kHz 单声道 PCM 识别为文本。
export interface Recognizer {
  ready(tier: ModelTier, source: SourceId): boolean
  ensureModel(tier: ModelTier, source: SourceId, onProgress: (p: number) => void): Promise<void>
  transcribe(pcm: Float32Array, language: string): Promise<string>
}

// ControllerDeps 编排器依赖集合。
export interface ControllerDeps {
  recorder: Recorder
  recognizer: Recognizer
  decode: (blob: Blob) => Promise<Float32Array>
  language: () => string
  onText: (text: string) => void
  loadSettings: () => VoiceSettings
  saveSettings: (s: VoiceSettings) => void
}

// VoiceController 暴露给 UI 的响应式接口。
export interface VoiceController {
  state: Ref<VoiceState>
  downloadProgress: Ref<number>
  errorKey: Ref<VoiceErrorKey | null>
  needModelPick: Ref<boolean>
  toggle(): Promise<void>
  chooseModel(tier: ModelTier, source: SourceId): Promise<void>
  onProgress(cb: (p: number) => void): void
}

// mapError 把底层异常映射为 UI 错误键：麦克风拒绝→permissionDenied，其余→fallback。
function mapError(e: unknown, fallback: VoiceErrorKey): VoiceErrorKey {
  const name = (e as { name?: string })?.name
  if (name === 'NotAllowedError' || name === 'SecurityError') return 'permissionDenied'
  return fallback
}

// createVoiceController 组装状态机。所有异步分支都保证最终把 state 收敛回 idle。
export function createVoiceController(deps: ControllerDeps): VoiceController {
  const state = ref<VoiceState>('idle')
  const downloadProgress = ref(0)
  const errorKey = ref<VoiceErrorKey | null>(null)
  const needModelPick = ref(false)
  let progressCb: ((p: number) => void) | null = null

  // onProgress 注册下载进度回调(popover 内进度条用)。
  function onProgress(cb: (p: number) => void) {
    progressCb = cb
  }

  // emitProgress 同步更新内部进度并通知订阅方。
  function emitProgress(p: number) {
    downloadProgress.value = p
    progressCb?.(p)
  }

  // startRecording 申请麦克风并进入录音态；权限异常映射为 errorKey 后回 idle。
  async function startRecording() {
    errorKey.value = null
    state.value = 'requesting'
    try {
      await deps.recorder.start()
      state.value = 'recording'
    } catch (e) {
      errorKey.value = mapError(e, 'notSupported')
      state.value = 'idle'
    }
  }

  // finishRecording 停录→解码→识别→落文本；空结果置 noSpeech，异常置 transcribeFailed。
  async function finishRecording() {
    state.value = 'transcribing'
    try {
      const blob = await deps.recorder.stop()
      const pcm = await deps.decode(blob)
      const text = (await deps.recognizer.transcribe(pcm, deps.language())).trim()
      if (!text) {
        errorKey.value = 'noSpeech'
      } else {
        deps.onText(text)
      }
    } catch (e) {
      errorKey.value = mapError(e, 'transcribeFailed')
    } finally {
      state.value = 'idle'
    }
  }

  // toggle 主按钮点击入口：
  // - 忙碌态(requesting/transcribing/downloading)忽略；
  // - idle 且未选档位→请求弹选择框；已选且就绪→开始录音；
  // - recording→结束并识别。
  async function toggle() {
    if (state.value === 'requesting' || state.value === 'transcribing' || state.value === 'downloading') return
    if (state.value === 'recording') {
      await finishRecording()
      return
    }
    const settings = deps.loadSettings()
    if (settings.tier === null) {
      needModelPick.value = true
      return
    }
    if (!deps.recognizer.ready(settings.tier, settings.source)) {
      // 已选过档位但模型未就绪(如换过浏览器/清过缓存)：先下载再录。
      await downloadThen(settings.tier, settings.source, startRecording)
      return
    }
    await startRecording()
  }

  // downloadThen 下载模型并在成功后执行 next；失败置 downloadFailed 回 idle。
  async function downloadThen(tier: ModelTier, source: SourceId, next: () => Promise<void>) {
    errorKey.value = null
    state.value = 'downloading'
    // 仅重置内部进度值，不通知回调(避免订阅方收到无意义的 0 初始值)。
    downloadProgress.value = 0
    try {
      await deps.recognizer.ensureModel(tier, source, emitProgress)
      state.value = 'idle'
      await next()
    } catch (e) {
      errorKey.value = mapError(e, 'downloadFailed')
      state.value = 'idle'
    }
  }

  // chooseModel popover 确认：持久化选择→下载→就绪后自动开始录音。
  async function chooseModel(tier: ModelTier, source: SourceId) {
    deps.saveSettings({ tier, source })
    needModelPick.value = false
    await downloadThen(tier, source, startRecording)
  }

  return { state, downloadProgress, errorKey, needModelPick, toggle, chooseModel, onProgress }
}
