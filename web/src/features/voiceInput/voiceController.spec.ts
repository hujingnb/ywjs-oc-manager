// voiceController 状态机单测：注入假录音器/识别器/解码，覆盖
// idle→recording→transcribing→idle 主链路、首次未选档位需弹选择、
// 下载进度、权限拒绝、识别为空、下载失败等分支。
import { describe, it, expect, vi } from 'vitest'
import { createVoiceController, type Recorder, type Recognizer } from './voiceController'

// mkDeps 组装一套可控的假依赖；各方法默认成功，用例按需覆盖。
function mkDeps(over: Partial<{
  recorder: Partial<Recorder>
  recognizer: Partial<Recognizer>
  decode: (b: Blob) => Promise<Float32Array>
  language: () => string
  onText: (t: string) => void
  initialTier: 'tiny' | 'base' | 'small' | null
}> = {}) {
  const recorder: Recorder = {
    start: vi.fn().mockResolvedValue(undefined),
    stop: vi.fn().mockResolvedValue(new Blob()),
    ...over.recorder,
  }
  const recognizer: Recognizer = {
    ready: vi.fn().mockReturnValue(true),
    ensureModel: vi.fn().mockResolvedValue(undefined),
    transcribe: vi.fn().mockResolvedValue('识别文本'),
    ...over.recognizer,
  }
  const onText = over.onText ?? vi.fn()
  const ctrl = createVoiceController({
    recorder,
    recognizer,
    decode: over.decode ?? vi.fn().mockResolvedValue(new Float32Array(16000)),
    language: over.language ?? (() => 'chinese'),
    onText,
    // 用 !== undefined 而非 ?? 以区分「未传(undefined)→回退'base'」和「明确传 null→tier=null」
    loadSettings: () => ({ tier: over.initialTier !== undefined ? over.initialTier : 'base', source: 'domestic' }),
    saveSettings: vi.fn(),
  })
  return { ctrl, recorder, recognizer, onText }
}

describe('voiceController', () => {
  // 初始状态为 idle
  it('初始状态 idle', () => {
    const { ctrl } = mkDeps()
    expect(ctrl.state.value).toBe('idle')
  })

  // 已选档位且模型就绪时，第一次 toggle 进入 recording
  it('toggle 从 idle 进入 recording', async () => {
    const { ctrl, recorder } = mkDeps()
    await ctrl.toggle()
    expect(recorder.start).toHaveBeenCalledOnce()
    expect(ctrl.state.value).toBe('recording')
  })

  // recording 时再 toggle：停录→解码→识别→落文本→回 idle
  it('toggle 从 recording 完成识别并落文本', async () => {
    const { ctrl, recognizer, onText } = mkDeps()
    await ctrl.toggle() // 进入 recording
    await ctrl.toggle() // 结束并识别
    expect(recognizer.transcribe).toHaveBeenCalledOnce()
    expect(onText).toHaveBeenCalledWith('识别文本')
    expect(ctrl.state.value).toBe('idle')
  })

  // 从未选过档位(tier=null)：toggle 不录音，置 needModelPick 供组件弹选择框
  it('未选档位时 toggle 请求选择模型而不录音', async () => {
    const { ctrl, recorder } = mkDeps({ initialTier: null })
    await ctrl.toggle()
    expect(ctrl.needModelPick.value).toBe(true)
    expect(recorder.start).not.toHaveBeenCalled()
    expect(ctrl.state.value).toBe('idle')
  })

  // chooseModel：保存设置→下载(带进度)→就绪后自动开始录音
  it('chooseModel 下载模型后自动进入 recording', async () => {
    const progresses: number[] = []
    const ensureModel = vi.fn().mockImplementation(async (_t, _s, onProgress: (p: number) => void) => {
      onProgress(0.5)
      onProgress(1)
    })
    const { ctrl, recorder } = mkDeps({ recognizer: { ready: vi.fn().mockReturnValue(false), ensureModel } })
    ctrl.onProgress((p) => progresses.push(p))
    await ctrl.chooseModel('base', 'domestic')
    expect(ensureModel).toHaveBeenCalledOnce()
    expect(progresses).toEqual([0.5, 1])
    expect(recorder.start).toHaveBeenCalledOnce()
    expect(ctrl.state.value).toBe('recording')
    expect(ctrl.needModelPick.value).toBe(false)
  })

  // 麦克风权限被拒：start 抛 NotAllowedError → errorKey=permissionDenied，回 idle
  it('录音权限被拒置 errorKey 并回 idle', async () => {
    const err = Object.assign(new Error('denied'), { name: 'NotAllowedError' })
    const { ctrl } = mkDeps({ recorder: { start: vi.fn().mockRejectedValue(err) } })
    await ctrl.toggle()
    expect(ctrl.errorKey.value).toBe('permissionDenied')
    expect(ctrl.state.value).toBe('idle')
  })

  // 识别结果为空白：不落文本，置 errorKey=noSpeech
  it('识别为空时不落文本并置 noSpeech', async () => {
    const { ctrl, onText } = mkDeps({ recognizer: { ready: vi.fn().mockReturnValue(true), ensureModel: vi.fn(), transcribe: vi.fn().mockResolvedValue('   ') } })
    await ctrl.toggle()
    await ctrl.toggle()
    expect(onText).not.toHaveBeenCalled()
    expect(ctrl.errorKey.value).toBe('noSpeech')
    expect(ctrl.state.value).toBe('idle')
  })

  // 模型下载失败：errorKey=downloadFailed，回 idle 且不录音
  it('下载失败置 downloadFailed 并回 idle', async () => {
    const ensureModel = vi.fn().mockRejectedValue(new Error('network'))
    const { ctrl, recorder } = mkDeps({ recognizer: { ready: vi.fn().mockReturnValue(false), ensureModel } })
    await ctrl.chooseModel('base', 'domestic')
    expect(ctrl.errorKey.value).toBe('downloadFailed')
    expect(recorder.start).not.toHaveBeenCalled()
    expect(ctrl.state.value).toBe('idle')
  })
})
