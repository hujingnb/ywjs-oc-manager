# 对话语音输入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给对话输入框增加纯浏览器本地语音输入——录音后用 Transformers.js + whisper 在浏览器里识别成文字，填入草稿供编辑；模型档位与下载源用户可选，录音不上传、零后端改动。

**Architecture:** 新增前端 feature 目录 `web/src/features/voiceInput/`，分层为：纯配置/持久化（`voiceSettings.ts`）、音频解码（`audioDecode.ts`）、Web Worker 识别（`speechRecognizer.worker.ts` + `speechRecognizerClient.ts`）、录音 composable（`useVoiceRecorder.ts`）、编排状态机（`voiceController.ts` 工厂 + `useVoiceInput.ts` composable）、UI（`VoiceInputButton.vue` + `ModelPickerPopover.vue`）。编排器用依赖注入，便于对状态机做单测；识别在 Worker 里跑避免冻结主线程。接入点是 `AppConversationsTab.vue` 的 composer，识别结果追加到 `draft`，不触碰现有发送/排队逻辑。

**Tech Stack:** Vue 3.5 + Vite 7 + Naive UI + vue-i18n + Pinia（不新增 store）；新增依赖 `@huggingface/transformers`（Transformers.js v3）；测试 vitest + jsdom + @vue/test-utils。

**分支：** 全程在 `feat/conversation-voice-input` 分支开发（已创建）。

---

## 文件结构

新建：

- `web/src/features/voiceInput/voiceSettings.ts` — 模型档位清单、下载源清单、`localStorage` 读写（纯逻辑，TDD）
- `web/src/features/voiceInput/voiceSettings.spec.ts` — 上者单测
- `web/src/features/voiceInput/audioDecode.ts` — 录音 Blob → 16kHz 单声道 Float32（浏览器 Web Audio，浏览器验证）
- `web/src/features/voiceInput/speechRecognizer.worker.ts` — Web Worker：加载 whisper pipeline、下载进度、识别（浏览器验证）
- `web/src/features/voiceInput/speechRecognizerClient.ts` — 主线程包装 worker → Promise + 进度回调（浏览器验证）
- `web/src/features/voiceInput/useVoiceRecorder.ts` — `getUserMedia` + `MediaRecorder` composable（浏览器验证）
- `web/src/features/voiceInput/voiceController.ts` — 编排状态机工厂（依赖注入，TDD）
- `web/src/features/voiceInput/voiceController.spec.ts` — 状态机单测
- `web/src/features/voiceInput/useVoiceInput.ts` — 把真实录音器/识别器/解码接到 `voiceController` 的 composable（浏览器验证）
- `web/src/features/voiceInput/VoiceInputButton.vue` — 麦克风按钮 + 状态视觉
- `web/src/features/voiceInput/ModelPickerPopover.vue` — 选下载源 + 档位 + 下载进度

修改：

- `web/package.json` — 加依赖 `@huggingface/transformers`
- `web/src/i18n/locales/zh/apps/conversations.ts` — 加 `voice` 子命名空间文案
- `web/src/i18n/locales/en/apps/conversations.ts` — 同结构英文文案
- `web/src/pages/apps/AppConversationsTab.vue` — composer 接入麦克风按钮 + popover，识别结果追加到 `draft`

---

## Task 1: 安装 Transformers.js 依赖

**Files:**
- Modify: `web/package.json`

- [ ] **Step 1: 安装依赖**

Run:
```bash
cd web && npm install @huggingface/transformers@^3.0.0
```
Expected: `package.json` 的 `dependencies` 出现 `"@huggingface/transformers"`，`package-lock.json` 更新，无报错。

- [ ] **Step 2: 验证类型可解析**

Run:
```bash
cd web && node -e "require('@huggingface/transformers/package.json') && console.log('ok')"
```
Expected: 输出 `ok`。

- [ ] **Step 3: Commit**

```bash
git add web/package.json web/package-lock.json
git commit -m "chore(web): 引入 Transformers.js 用于浏览器本地语音识别"
```

---

## Task 2: voiceSettings —— 模型/源清单与持久化（TDD）

**Files:**
- Create: `web/src/features/voiceInput/voiceSettings.ts`
- Test: `web/src/features/voiceInput/voiceSettings.spec.ts`

- [ ] **Step 1: 写失败测试**

写 `web/src/features/voiceInput/voiceSettings.spec.ts`：

```ts
// voiceSettings 纯逻辑单测：档位→仓库映射、源→host 映射、localStorage 读写与容错。
import { describe, it, expect, beforeEach } from 'vitest'
import {
  repoForTier,
  hostForSource,
  loadVoiceSettings,
  saveVoiceSettings,
  DEFAULT_SOURCE,
  DEFAULT_TIER,
  STORAGE_KEY,
} from './voiceSettings'

// fakeStorage 构造一个内存版 Storage，隔离测试互不污染。
function fakeStorage(init: Record<string, string> = {}): Storage {
  const m = new Map(Object.entries(init))
  return {
    get length() { return m.size },
    clear: () => m.clear(),
    getItem: (k: string) => (m.has(k) ? m.get(k)! : null),
    key: (i: number) => [...m.keys()][i] ?? null,
    removeItem: (k: string) => void m.delete(k),
    setItem: (k: string, v: string) => void m.set(k, v),
  }
}

describe('voiceSettings', () => {
  // 档位到 Xenova whisper 仓库名的映射，三档齐全
  it('repoForTier 映射三档到对应 Xenova 仓库', () => {
    expect(repoForTier('tiny')).toBe('Xenova/whisper-tiny')
    expect(repoForTier('base')).toBe('Xenova/whisper-base')
    expect(repoForTier('small')).toBe('Xenova/whisper-small')
  })

  // 下载源到 remoteHost 的映射：国内镜像与官方
  it('hostForSource 映射国内镜像与官方站点', () => {
    expect(hostForSource('domestic')).toBe('https://hf-mirror.com')
    expect(hostForSource('official')).toBe('https://huggingface.co')
  })

  // 空存储时返回默认：源为国内镜像、档位未选（null，触发首次选择弹窗）
  it('loadVoiceSettings 空存储返回默认(源=国内, 档位=null)', () => {
    const s = loadVoiceSettings(fakeStorage())
    expect(s.source).toBe(DEFAULT_SOURCE)
    expect(s.tier).toBeNull()
  })

  // 存储内容损坏(非法 JSON)时安全回退到默认，不抛异常
  it('loadVoiceSettings 遇损坏数据回退默认', () => {
    const s = loadVoiceSettings(fakeStorage({ [STORAGE_KEY]: '{bad json' }))
    expect(s.source).toBe(DEFAULT_SOURCE)
    expect(s.tier).toBeNull()
  })

  // 存储含非法枚举值时也回退默认(校验 tier/source 合法性)
  it('loadVoiceSettings 遇非法枚举回退默认', () => {
    const raw = JSON.stringify({ tier: 'huge', source: 'mars' })
    const s = loadVoiceSettings(fakeStorage({ [STORAGE_KEY]: raw }))
    expect(s.source).toBe(DEFAULT_SOURCE)
    expect(s.tier).toBeNull()
  })

  // 保存后再读取应得到同一份设置(round-trip)
  it('saveVoiceSettings 写入后 loadVoiceSettings 可还原', () => {
    const st = fakeStorage()
    saveVoiceSettings({ tier: 'small', source: 'official' }, st)
    const s = loadVoiceSettings(st)
    expect(s.tier).toBe('small')
    expect(s.source).toBe('official')
  })

  // 默认档位常量供 popover 预选，默认源为国内
  it('默认常量: 档位=base、源=domestic', () => {
    expect(DEFAULT_TIER).toBe('base')
    expect(DEFAULT_SOURCE).toBe('domestic')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/features/voiceInput/voiceSettings.spec.ts`
Expected: FAIL，报找不到模块 `./voiceSettings`。

- [ ] **Step 3: 写实现**

写 `web/src/features/voiceInput/voiceSettings.ts`：

```ts
// voiceSettings：语音输入的可选项清单与用户偏好持久化。
// - 模型档位(tiny/base/small)映射到 Xenova whisper 仓库名。
// - 下载源(国内镜像/官方)映射到 Transformers.js 的 remoteHost。
// - 用户所选档位与源存 localStorage，跨会话记住；tier 为 null 表示尚未选过，
//   首次点击麦克风时据此弹出选择框。

// ModelTier 语音识别模型档位，越大中文越准但下载越大、CPU 上越慢。
export type ModelTier = 'tiny' | 'base' | 'small'
// SourceId 模型下载源：domestic=国内镜像(默认)，official=HuggingFace 官方。
export type SourceId = 'domestic' | 'official'

// ModelOption 单个档位的展示与仓库信息，sizeLabel 为量化权重的近似体积(供 UI 提示)。
export interface ModelOption {
  tier: ModelTier
  repo: string
  sizeLabel: string
}

// SourceOption 单个下载源的展示与 remoteHost。
export interface SourceOption {
  id: SourceId
  host: string
}

// VoiceSettings 持久化的用户偏好；tier 为 null 表示从未选择过档位。
export interface VoiceSettings {
  tier: ModelTier | null
  source: SourceId
}

// MODEL_OPTIONS 三个可选档位；顺序即 UI 展示顺序(由轻到重)。
export const MODEL_OPTIONS: ModelOption[] = [
  { tier: 'tiny', repo: 'Xenova/whisper-tiny', sizeLabel: '~40MB' },
  { tier: 'base', repo: 'Xenova/whisper-base', sizeLabel: '~80MB' },
  { tier: 'small', repo: 'Xenova/whisper-small', sizeLabel: '~250MB' },
]

// SOURCE_OPTIONS 两个下载源；domestic 在前作为默认。
export const SOURCE_OPTIONS: SourceOption[] = [
  { id: 'domestic', host: 'https://hf-mirror.com' },
  { id: 'official', host: 'https://huggingface.co' },
]

// DEFAULT_TIER popover 首次打开时预选的档位(base 是中文准确率与体积的折中)。
export const DEFAULT_TIER: ModelTier = 'base'
// DEFAULT_SOURCE 默认下载源，面向大陆用户默认走国内镜像。
export const DEFAULT_SOURCE: SourceId = 'domestic'
// STORAGE_KEY localStorage 键名，带 oc 前缀避免与其它键冲突。
export const STORAGE_KEY = 'oc.voiceInput.settings'

// repoForTier 返回档位对应的 whisper 仓库名。
export function repoForTier(tier: ModelTier): string {
  const opt = MODEL_OPTIONS.find((o) => o.tier === tier)
  // 枚举受类型约束，正常不会缺；兜底回退 base 仓库避免运行时 undefined。
  return opt ? opt.repo : 'Xenova/whisper-base'
}

// hostForSource 返回下载源对应的 remoteHost。
export function hostForSource(source: SourceId): string {
  const opt = SOURCE_OPTIONS.find((o) => o.id === source)
  return opt ? opt.host : SOURCE_OPTIONS[0].host
}

// isTier / isSource 运行时枚举校验，用于从不可信的 localStorage 读回时过滤脏数据。
function isTier(v: unknown): v is ModelTier {
  return v === 'tiny' || v === 'base' || v === 'small'
}
function isSource(v: unknown): v is SourceId {
  return v === 'domestic' || v === 'official'
}

// resolveStorage 取传入 storage，缺省用全局 localStorage；SSR/隐私模式下访问会抛错，
// 调用方通过传入内存 storage 或 try/catch 规避。
function resolveStorage(storage?: Storage): Storage | null {
  if (storage) return storage
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    return null
  }
}

// loadVoiceSettings 读取用户偏好；缺失/损坏/非法枚举一律回退默认，绝不抛出。
export function loadVoiceSettings(storage?: Storage): VoiceSettings {
  const st = resolveStorage(storage)
  const fallback: VoiceSettings = { tier: null, source: DEFAULT_SOURCE }
  if (!st) return fallback
  const raw = st.getItem(STORAGE_KEY)
  if (!raw) return fallback
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    return {
      tier: isTier(parsed.tier) ? parsed.tier : null,
      source: isSource(parsed.source) ? parsed.source : DEFAULT_SOURCE,
    }
  } catch {
    return fallback
  }
}

// saveVoiceSettings 持久化用户偏好；storage 不可用时静默跳过(不影响功能，仅不记忆)。
export function saveVoiceSettings(settings: VoiceSettings, storage?: Storage): void {
  const st = resolveStorage(storage)
  if (!st) return
  try {
    st.setItem(STORAGE_KEY, JSON.stringify(settings))
  } catch {
    // 隐私模式/配额满时忽略写入失败。
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/features/voiceInput/voiceSettings.spec.ts`
Expected: PASS，全部用例绿。

- [ ] **Step 5: Commit**

```bash
git add web/src/features/voiceInput/voiceSettings.ts web/src/features/voiceInput/voiceSettings.spec.ts
git commit -m "feat(web): 增加语音输入模型档位与下载源配置及持久化"
```

---

## Task 3: voiceController —— 编排状态机（TDD）

**Files:**
- Create: `web/src/features/voiceInput/voiceController.ts`
- Test: `web/src/features/voiceInput/voiceController.spec.ts`

编排器用工厂函数 + 依赖注入，把「录音器 / 识别器 / 解码 / 语言 / 落文本」都作为参数传入，从而脱离浏览器 API 单测状态机。

- [ ] **Step 1: 写失败测试**

写 `web/src/features/voiceInput/voiceController.spec.ts`：

```ts
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
    loadSettings: () => ({ tier: over.initialTier ?? 'base', source: 'domestic' }),
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/features/voiceInput/voiceController.spec.ts`
Expected: FAIL，找不到模块 `./voiceController`。

- [ ] **Step 3: 写实现**

写 `web/src/features/voiceInput/voiceController.ts`：

```ts
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
    emitProgress(0)
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/features/voiceInput/voiceController.spec.ts`
Expected: PASS，全部用例绿。

- [ ] **Step 5: Commit**

```bash
git add web/src/features/voiceInput/voiceController.ts web/src/features/voiceInput/voiceController.spec.ts
git commit -m "feat(web): 增加语音输入编排状态机及单测"
```

---

## Task 4: audioDecode —— 录音解码为 16kHz 单声道 PCM

**Files:**
- Create: `web/src/features/voiceInput/audioDecode.ts`

`OfflineAudioContext` 在 jsdom 不可用，本单元靠 Task 10 真实浏览器验证；此处只实现。

- [ ] **Step 1: 写实现**

写 `web/src/features/voiceInput/audioDecode.ts`：

```ts
// audioDecode：把 MediaRecorder 产出的录音 Blob 解码并重采样为 whisper 要求的
// 16kHz 单声道 Float32 PCM。使用 OfflineAudioContext 一步完成解码+下混+重采样，
// 兼容各浏览器录音编码(webm/opus、mp4/aac 等)。仅在浏览器运行。
const TARGET_SAMPLE_RATE = 16000

// decodeToPcm16k 解码录音 Blob → 单声道 16kHz Float32Array。
// 空录音(0 采样)返回空数组，交由上层判定为「未识别到语音」。
export async function decodeToPcm16k(blob: Blob): Promise<Float32Array> {
  const arrayBuffer = await blob.arrayBuffer()
  if (arrayBuffer.byteLength === 0) return new Float32Array(0)

  // 先用临时 AudioContext 解码为 AudioBuffer(拿到原始采样率与声道)。
  const AudioCtx = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
  const tmpCtx = new AudioCtx()
  let decoded: AudioBuffer
  try {
    decoded = await tmpCtx.decodeAudioData(arrayBuffer.slice(0))
  } finally {
    void tmpCtx.close()
  }

  // 用 OfflineAudioContext 以目标采样率渲染，得到重采样后的单声道数据。
  const frameCount = Math.ceil((decoded.duration || 0) * TARGET_SAMPLE_RATE)
  if (frameCount === 0) return new Float32Array(0)
  const offline = new OfflineAudioContext(1, frameCount, TARGET_SAMPLE_RATE)
  const src = offline.createBufferSource()
  src.buffer = decoded
  src.connect(offline.destination)
  src.start()
  const rendered = await offline.startRendering()
  return rendered.getChannelData(0).slice()
}
```

- [ ] **Step 2: 类型检查通过**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无与 `audioDecode.ts` 相关的类型错误。

- [ ] **Step 3: Commit**

```bash
git add web/src/features/voiceInput/audioDecode.ts
git commit -m "feat(web): 增加录音解码为 16kHz 单声道 PCM 工具"
```

---

## Task 5: speechRecognizer Worker 与主线程客户端

**Files:**
- Create: `web/src/features/voiceInput/speechRecognizer.worker.ts`
- Create: `web/src/features/voiceInput/speechRecognizerClient.ts`

浏览器/Worker 环境，靠 Task 10 验证。

- [ ] **Step 1: 写 Worker**

写 `web/src/features/voiceInput/speechRecognizer.worker.ts`：

```ts
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
  asr = await pipeline('automatic-speech-recognition', repo, {
    device: pickDevice(),
    dtype: 'q8',
    progress_callback: (p: { status?: string; progress?: number }) => {
      // 仅下载阶段有 progress(0..100)，归一化为 0..1 上报。
      if (typeof p.progress === 'number') {
        postMessage({ type: 'progress', progress: Math.max(0, Math.min(1, p.progress / 100)) })
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
  const out = (await asr(msg.pcm, { language: msg.language, task: 'transcribe' })) as { text: string }
  postMessage({ type: 'result', id: msg.id, text: out.text ?? '' })
}

// onmessage 分发主线程请求；任何异常统一回 error。
self.onmessage = async (ev: MessageEvent<InMsg>) => {
  const msg = ev.data
  try {
    if (msg.type === 'load') await loadModel(msg.repo, msg.host)
    else if (msg.type === 'transcribe') await runTranscribe(msg)
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e)
    postMessage({ type: 'error', id: (msg as TranscribeMsg).id, message })
  }
}
```

- [ ] **Step 2: 写主线程客户端**

写 `web/src/features/voiceInput/speechRecognizerClient.ts`：

```ts
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
          const m = ev.data
          if (m.type === 'progress') onProgress(m.progress)
          else if (m.type === 'ready') {
            w.removeEventListener('message', handler)
            readyKey = keyOf(tier, source)
            resolve()
          } else if (m.type === 'error') {
            w.removeEventListener('message', handler)
            reject(new Error(m.message))
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
          const m = ev.data
          if (m.id !== id) return
          w.removeEventListener('message', handler)
          if (m.type === 'result') resolve(m.text)
          else if (m.type === 'error') reject(new Error(m.message))
        }
        w.addEventListener('message', handler)
        // pcm.buffer 作为 transferable 转移所有权，避免大数组拷贝。
        w.postMessage({ type: 'transcribe', id, pcm, language }, [pcm.buffer])
      })
    },
  }
}
```

- [ ] **Step 3: 类型检查通过**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无相关类型错误（若 `@huggingface/transformers` 的 `AutomaticSpeechRecognitionPipeline` 导出名有出入，按其 d.ts 调整类型名，逻辑不变）。

- [ ] **Step 4: Commit**

```bash
git add web/src/features/voiceInput/speechRecognizer.worker.ts web/src/features/voiceInput/speechRecognizerClient.ts
git commit -m "feat(web): 增加 Web Worker whisper 识别器与主线程客户端"
```

---

## Task 6: useVoiceRecorder —— 录音 composable

**Files:**
- Create: `web/src/features/voiceInput/useVoiceRecorder.ts`

浏览器 API，靠 Task 10 验证。

- [ ] **Step 1: 写实现**

写 `web/src/features/voiceInput/useVoiceRecorder.ts`：

```ts
// useVoiceRecorder：封装 getUserMedia + MediaRecorder，实现 voiceController 的 Recorder 接口。
// start 申请麦克风并开始录音，stop 结束并把所有分片合成一个 Blob，同时释放音轨。
import type { Recorder } from './voiceController'

// createRecorder 返回一个 Recorder 实例；录音数据累积在闭包内。
export function createRecorder(): Recorder {
  let media: MediaRecorder | null = null
  let stream: MediaStream | null = null
  let chunks: Blob[] = []

  return {
    async start() {
      // getUserMedia 在非安全上下文或被拒时抛错，交由 voiceController 映射为 errorKey。
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      chunks = []
      media = new MediaRecorder(stream)
      media.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.push(e.data)
      }
      media.start()
    },

    stop() {
      return new Promise<Blob>((resolve) => {
        const mr = media
        if (!mr) {
          resolve(new Blob())
          return
        }
        mr.onstop = () => {
          const blob = new Blob(chunks, { type: mr.mimeType || 'audio/webm' })
          // 释放麦克风占用(否则浏览器标签页一直显示录音中)。
          stream?.getTracks().forEach((t) => t.stop())
          stream = null
          media = null
          resolve(blob)
        }
        mr.stop()
      })
    },
  }
}
```

- [ ] **Step 2: 类型检查通过**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无相关类型错误。

- [ ] **Step 3: Commit**

```bash
git add web/src/features/voiceInput/useVoiceRecorder.ts
git commit -m "feat(web): 增加基于 MediaRecorder 的录音器"
```

---

## Task 7: i18n 文案（zh + en，结构对齐）

**Files:**
- Modify: `web/src/i18n/locales/zh/apps/conversations.ts`
- Modify: `web/src/i18n/locales/en/apps/conversations.ts`
- Test: `web/src/i18n/locales/completeness.spec.ts`（已存在，用于校验 zh/en 结构一致）

- [ ] **Step 1: 加中文文案**

在 `web/src/i18n/locales/zh/apps/conversations.ts` 的 `queueFailed` 行之后、`} as const` 之前追加：

```ts
  // ─── 语音输入 ───────────────────────────────────────────────────────────────
  voice: {
    // 麦克风按钮：空闲态提示(开始录音)
    start: '语音输入',
    // 录音中提示(再次点击结束)
    recording: '录音中，点击结束',
    // 识别处理中提示
    transcribing: '识别中…',
    // 下载模型进度(带百分比参数)
    downloading: '下载模型 {percent}%',
    // 模型选择弹层标题
    pickTitle: '选择语音识别模型',
    // 下载源分组标签
    sourceLabel: '下载源',
    // 下载源选项：国内镜像
    sourceDomestic: '国内镜像',
    // 下载源选项：官方站点
    sourceOfficial: 'HuggingFace 官方',
    // 模型档位分组标签
    tierLabel: '模型档位',
    // 档位说明：tiny
    tierTiny: '轻量（最快，中文一般）',
    // 档位说明：base
    tierBase: '均衡（推荐）',
    // 档位说明：small
    tierSmall: '精准（最准，最大最慢）',
    // 弹层确认按钮
    confirm: '下载并使用',
    // 切换模型入口
    switch: '切换模型',
    // 错误文案
    errors: {
      // 麦克风权限被拒或非安全上下文
      permissionDenied: '无法访问麦克风，请检查浏览器权限',
      // 浏览器不支持所需能力
      notSupported: '当前浏览器不支持语音输入',
      // 未识别到有效语音
      noSpeech: '未识别到语音',
      // 模型下载失败
      downloadFailed: '模型下载失败，可切换下载源后重试',
      // 识别过程出错
      transcribeFailed: '语音识别失败，请重试',
    },
  },
```

- [ ] **Step 2: 加英文文案（同结构）**

在 `web/src/i18n/locales/en/apps/conversations.ts` 的 `queueFailed` 行之后、`} as const` 之前追加：

```ts
  // ─── Voice input ─────────────────────────────────────────────────────────────
  voice: {
    // Mic button idle hint (start recording)
    start: 'Voice input',
    // Recording hint (click again to stop)
    recording: 'Recording, click to stop',
    // Transcribing hint
    transcribing: 'Transcribing…',
    // Model download progress (with percent param)
    downloading: 'Downloading model {percent}%',
    // Model picker popover title
    pickTitle: 'Choose speech model',
    // Download source group label
    sourceLabel: 'Download source',
    // Source option: domestic mirror
    sourceDomestic: 'Domestic mirror',
    // Source option: official site
    sourceOfficial: 'HuggingFace official',
    // Model tier group label
    tierLabel: 'Model size',
    // Tier hint: tiny
    tierTiny: 'Tiny (fastest, fair Chinese)',
    // Tier hint: base
    tierBase: 'Balanced (recommended)',
    // Tier hint: small
    tierSmall: 'Small (most accurate, largest/slowest)',
    // Popover confirm button
    confirm: 'Download & use',
    // Switch model entry
    switch: 'Switch model',
    // Error messages
    errors: {
      // Mic permission denied or insecure context
      permissionDenied: 'Cannot access microphone, check browser permissions',
      // Browser lacks required capability
      notSupported: 'Voice input is not supported in this browser',
      // No valid speech detected
      noSpeech: 'No speech detected',
      // Model download failed
      downloadFailed: 'Model download failed, try switching source',
      // Transcription error
      transcribeFailed: 'Speech recognition failed, please retry',
    },
  },
```

- [ ] **Step 3: 跑 i18n 完整性测试确认 zh/en 对齐**

Run: `cd web && npx vitest run src/i18n/locales/completeness.spec.ts`
Expected: PASS。若失败，按报错补齐缺失键/修正插值 token `{percent}` 两侧一致。

- [ ] **Step 4: Commit**

```bash
git add web/src/i18n/locales/zh/apps/conversations.ts web/src/i18n/locales/en/apps/conversations.ts
git commit -m "feat(web): 增加对话语音输入中英文案"
```

---

## Task 8: ModelPickerPopover —— 选源+档位+下载进度

**Files:**
- Create: `web/src/features/voiceInput/ModelPickerPopover.vue`

- [ ] **Step 1: 写组件**

写 `web/src/features/voiceInput/ModelPickerPopover.vue`：

```vue
<template>
  <!-- ModelPickerPopover：语音识别模型选择弹层。选择下载源 + 档位后点确认，
       下载中展示进度条并禁用确认。通过 v-model:show 控制显隐。 -->
  <n-modal
    :show="show"
    preset="card"
    :title="t('apps.conversations.voice.pickTitle')"
    style="width: 420px; max-width: calc(100vw - 32px)"
    :mask-closable="!downloading"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <n-space vertical size="large">
      <!-- 下载源选择 -->
      <div>
        <div class="picker-label">{{ t('apps.conversations.voice.sourceLabel') }}</div>
        <n-radio-group v-model:value="source" :disabled="downloading">
          <n-radio-button value="domestic">{{ t('apps.conversations.voice.sourceDomestic') }}</n-radio-button>
          <n-radio-button value="official">{{ t('apps.conversations.voice.sourceOfficial') }}</n-radio-button>
        </n-radio-group>
      </div>
      <!-- 档位选择 -->
      <div>
        <div class="picker-label">{{ t('apps.conversations.voice.tierLabel') }}</div>
        <n-radio-group v-model:value="tier" :disabled="downloading">
          <n-space vertical>
            <n-radio v-for="opt in MODEL_OPTIONS" :key="opt.tier" :value="opt.tier">
              {{ tierHint(opt.tier) }}（{{ opt.sizeLabel }}）
            </n-radio>
          </n-space>
        </n-radio-group>
      </div>
      <!-- 下载进度条 -->
      <n-progress
        v-if="downloading"
        type="line"
        :percentage="Math.round(progress * 100)"
        :indicator-placement="'inside'"
      />
    </n-space>
    <template #footer>
      <n-space justify="end">
        <n-button
          type="primary"
          :loading="downloading"
          :disabled="downloading"
          @click="onConfirm"
        >
          {{ downloading
            ? t('apps.conversations.voice.downloading', { percent: Math.round(progress * 100) })
            : t('apps.conversations.voice.confirm') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
// 模型选择弹层：受控组件，选择结果通过 confirm 事件回传父组件驱动 voiceController。
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NModal, NSpace, NRadioGroup, NRadioButton, NRadio, NProgress, NButton } from 'naive-ui'
import { MODEL_OPTIONS, DEFAULT_TIER, DEFAULT_SOURCE, type ModelTier, type SourceId } from './voiceSettings'

const props = defineProps<{
  show: boolean
  // downloading 为 true 时锁定选择并显示进度(由父组件依据 voiceController.state 传入)。
  downloading: boolean
  // progress 下载进度 0..1。
  progress: number
  // 初始预选(记住上次选择)。
  initialTier: ModelTier | null
  initialSource: SourceId
}>()
const emit = defineEmits<{
  'update:show': [boolean]
  confirm: [tier: ModelTier, source: SourceId]
}>()

const { t } = useI18n()
// 本地选择态，用初始值(或默认)预填，便于用户直接确认。
const tier = ref<ModelTier>(props.initialTier ?? DEFAULT_TIER)
const source = ref<SourceId>(props.initialSource ?? DEFAULT_SOURCE)

// tierHint 按档位取对应说明文案。
function tierHint(tv: ModelTier): string {
  const map: Record<ModelTier, string> = {
    tiny: t('apps.conversations.voice.tierTiny'),
    base: t('apps.conversations.voice.tierBase'),
    small: t('apps.conversations.voice.tierSmall'),
  }
  return map[tv]
}

// onConfirm 把当前选择回传父组件，由父组件调用 voiceController.chooseModel 触发下载。
function onConfirm() {
  emit('confirm', tier.value, source.value)
}
</script>

<style scoped>
.picker-label {
  margin-bottom: 8px;
  font-weight: 600;
}
</style>
```

- [ ] **Step 2: 类型检查通过**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无相关类型错误。

- [ ] **Step 3: Commit**

```bash
git add web/src/features/voiceInput/ModelPickerPopover.vue
git commit -m "feat(web): 增加语音识别模型选择弹层组件"
```

---

## Task 9: useVoiceInput + VoiceInputButton —— 组装真实依赖与按钮

**Files:**
- Create: `web/src/features/voiceInput/useVoiceInput.ts`
- Create: `web/src/features/voiceInput/VoiceInputButton.vue`

- [ ] **Step 1: 写 composable 组装真实依赖**

写 `web/src/features/voiceInput/useVoiceInput.ts`：

```ts
// useVoiceInput：把真实的录音器、Worker 识别器、音频解码接到 voiceController，
// 并把「识别出的文本」通过回调交给调用方(对话页把它追加进 draft)。
// language 依据当前 UI 语言选择 whisper 语种以提升准确率。
import { useI18n } from 'vue-i18n'
import { createVoiceController } from './voiceController'
import { createRecorder } from './useVoiceRecorder'
import { createRecognizer } from './speechRecognizerClient'
import { decodeToPcm16k } from './audioDecode'
import { loadVoiceSettings, saveVoiceSettings } from './voiceSettings'

// useVoiceInput 接收 onText 回调(落文本目标)，返回 voiceController 的响应式接口。
export function useVoiceInput(onText: (text: string) => void) {
  const { locale } = useI18n()
  // whisper 语种名：中文界面→chinese，其余→english。
  const language = () => (String(locale.value).startsWith('zh') ? 'chinese' : 'english')

  return createVoiceController({
    recorder: createRecorder(),
    recognizer: createRecognizer(),
    decode: decodeToPcm16k,
    language,
    onText,
    loadSettings: () => loadVoiceSettings(),
    saveSettings: (s) => saveVoiceSettings(s),
  })
}
```

- [ ] **Step 2: 写按钮组件**

写 `web/src/features/voiceInput/VoiceInputButton.vue`：

```vue
<template>
  <!-- VoiceInputButton：对话输入区的麦克风按钮 + 模型选择弹层。
       点击麦克风开始/结束录音；未选过档位则先弹选择框；识别结果经 onText 追加到草稿。
       录音中按钮高亮并显示状态提示；识别/下载中禁用点击。 -->
  <div class="voice-input">
    <n-tooltip trigger="hover">
      <template #trigger>
        <n-button
          circle
          :type="ctrl.state.value === 'recording' ? 'error' : 'default'"
          :disabled="disabled || busy"
          :loading="ctrl.state.value === 'transcribing'"
          :class="{ 'voice-input__rec': ctrl.state.value === 'recording' }"
          data-test="voice-toggle"
          @click="ctrl.toggle()"
        >
          🎤
        </n-button>
      </template>
      {{ hint }}
    </n-tooltip>
    <!-- 切换模型入口：小号文字按钮 -->
    <n-button text size="tiny" :disabled="disabled || busy" @click="openPicker">
      {{ t('apps.conversations.voice.switch') }}
    </n-button>

    <ModelPickerPopover
      :show="pickerShow"
      :downloading="ctrl.state.value === 'downloading'"
      :progress="ctrl.downloadProgress.value"
      :initial-tier="settings.tier"
      :initial-source="settings.source"
      @update:show="pickerShow = $event"
      @confirm="onPickerConfirm"
    />
  </div>
</template>

<script setup lang="ts">
// 麦克风按钮：驱动 useVoiceInput 状态机，串联模型选择弹层与错误提示。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NTooltip, useMessage } from 'naive-ui'
import ModelPickerPopover from './ModelPickerPopover.vue'
import { useVoiceInput } from './useVoiceInput'
import { loadVoiceSettings, type ModelTier, type SourceId } from './voiceSettings'

const props = defineProps<{
  // disabled 由对话页传入(未选中会话时禁用)。
  disabled: boolean
}>()
const emit = defineEmits<{ text: [string] }>()

const { t } = useI18n()
const message = useMessage()
// 识别文本经事件抛给对话页追加到 draft。
const ctrl = useVoiceInput((text) => emit('text', text))
// settings 用于弹层预选(展示时读一次最新值)。
const settings = ref(loadVoiceSettings())
const pickerShow = ref(false)

// busy 识别/下载/申请权限期间禁用交互。
const busy = computed(() => ['transcribing', 'downloading', 'requesting'].includes(ctrl.state.value))

// hint 按状态给 tooltip 文案。
const hint = computed(() => {
  switch (ctrl.state.value) {
    case 'recording': return t('apps.conversations.voice.recording')
    case 'transcribing': return t('apps.conversations.voice.transcribing')
    default: return t('apps.conversations.voice.start')
  }
})

// needModelPick 被状态机置真(首次未选档位点麦克风)时，打开弹层。
watch(ctrl.needModelPick, (v) => {
  if (v) {
    settings.value = loadVoiceSettings()
    pickerShow.value = true
  }
})

// errorKey 变化时弹 toast 提示对应错误。
watch(ctrl.errorKey, (k) => {
  if (k) message.error(t(`apps.conversations.voice.errors.${k}`))
})

// openPicker 手动打开「切换模型」弹层。
function openPicker() {
  settings.value = loadVoiceSettings()
  pickerShow.value = true
}

// onPickerConfirm 用户在弹层确认：触发下载并(成功后)自动开始录音；下载完成后关闭弹层。
async function onPickerConfirm(tier: ModelTier, source: SourceId) {
  await ctrl.chooseModel(tier, source)
  settings.value = loadVoiceSettings()
  // 仅在未报错时关闭(下载失败保留弹层供切源重试)。
  if (!ctrl.errorKey.value) pickerShow.value = false
}
</script>

<style scoped>
.voice-input {
  display: flex;
  align-items: center;
  gap: 4px;
}
/* 录音中脉冲动效，提示正在录制 */
.voice-input__rec {
  animation: voice-pulse 1.2s ease-in-out infinite;
}
@keyframes voice-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(208, 48, 80, 0.5); }
  50% { box-shadow: 0 0 0 6px rgba(208, 48, 80, 0); }
}
</style>
```

- [ ] **Step 3: 类型检查通过**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无相关类型错误。

- [ ] **Step 4: Commit**

```bash
git add web/src/features/voiceInput/useVoiceInput.ts web/src/features/voiceInput/VoiceInputButton.vue
git commit -m "feat(web): 组装语音输入 composable 与麦克风按钮组件"
```

---

## Task 10: 接入对话页 composer

**Files:**
- Modify: `web/src/pages/apps/AppConversationsTab.vue`

- [ ] **Step 1: 引入组件**

在 `<script setup>` 的组件导入区（`import ConversationMessageView from './ConversationMessageView.vue'` 之后）加：

```ts
import VoiceInputButton from '@/features/voiceInput/VoiceInputButton.vue'
```

- [ ] **Step 2: 加落文本处理函数**

在 `onPickFiles` 函数之前（约第 284 行）加：

```ts
// appendVoiceText 把语音识别结果追加到 draft：非空草稿以空格拼接，保留用户已输入内容。
function appendVoiceText(text: string) {
  draft.value = draft.value ? `${draft.value} ${text}` : text
}
```

- [ ] **Step 3: 在 composer 放置麦克风按钮**

把 composer-row 里「附件」`<label>` 之前插入麦克风按钮（即 `<n-input>` 与附件 `<label>` 之间，第 179–180 行之间）：

```vue
          <VoiceInputButton :disabled="!currentId" @text="appendVoiceText" />
```

- [ ] **Step 4: 类型检查与构建通过**

Run: `cd web && npx vue-tsc --noEmit && npx vite build`
Expected: 类型检查无错误；构建成功（Worker 被 Vite 正确打包为独立 chunk）。

- [ ] **Step 5: 跑全部前端单测确保未回归**

Run: `cd web && npx vitest run`
Expected: 全绿，包含 voiceSettings、voiceController、i18n completeness 及既有 domain 测试。

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/apps/AppConversationsTab.vue
git commit -m "feat(web): 对话输入框接入语音输入按钮"
```

---

## Task 11: 真实浏览器端到端验证

**Files:** 无（验证 + 修复）

按 AGENTS.md「所有新功能必须用真实浏览器验证」执行。用本地 k3d 环境或 `cd web && npm run dev` 起前端，登录后进入某实例的「会话」tab。

- [ ] **Step 1: 首次选择模型 + 下载进度**
  - 点击麦克风按钮 → 弹出模型选择弹层，源默认「国内镜像」、档位预选 base。
  - 点「下载并使用」→ 进度条从 0% 走到 100%（首次会真实从 hf-mirror.com 拉模型，耐心等）。
  - Expected：下载完成后弹层关闭并自动进入录音态（按钮变红、脉冲）。

- [ ] **Step 2: 录音 → 识别 → 落草稿**
  - 对麦克风说一句中文，再点按钮结束。
  - Expected：按钮转圈（识别中）→ 识别文本追加进输入框 draft，可编辑。

- [ ] **Step 3: 编辑后发送**
  - 编辑识别文本 → 点发送。
  - Expected：消息正常发送，与手打消息无差别；不影响「任务进行中排队」逻辑。

- [ ] **Step 4: 二次录音走缓存**
  - 再次点麦克风。
  - Expected：不再弹选择、不再下载，直接开始录音（模型已被浏览器缓存）。

- [ ] **Step 5: 切换模型与源**
  - 点「切换模型」→ 选官方源 + tiny → 确认。
  - Expected：重新下载对应模型，之后识别可用。

- [ ] **Step 6: 权限拒绝**
  - 浏览器设置里拒绝麦克风权限后点麦克风。
  - Expected：toast 提示「无法访问麦克风…」，按钮回到空闲态，页面不崩。

- [ ] **Step 7: 英文界面**
  - 切 UI 语言为 English，重复录一句英文。
  - Expected：文案为英文，whisper 以 english 识别。

- [ ] **Step 8: 修复并复验**
  - 若任一步失败，先修复再从该步重验，直至全部通过。修复涉及逻辑的补/改对应单测。

- [ ] **Step 9: 最终提交（若验证中有修复）**

```bash
git add -A
git commit -m "fix(web): 修复语音输入浏览器验证中发现的问题"
```

---

## Self-Review 结论

- **Spec 覆盖**：识别位置(本地 Worker) → Task 5；模型档位用户自选+下载进度+可切换 → Task 2/8/9；下载源默认国内可选官方 → Task 2/8；交互点击切换+填草稿 → Task 3/10；录音→识别数据流 → Task 4/5/6；错误处理(权限/空/下载失败/不支持) → Task 3/7/9/11；i18n → Task 7；WebGPU/单线程 WASM → Task 5；分支开发 → 全程。均有对应任务。
- **无占位符**：每个代码步骤给出完整代码；浏览器 API 单元(Task 4/5/6)明确标注靠 Task 11 真机验证，非留空。
- **类型一致**：`Recorder`/`Recognizer` 接口在 Task 3 定义，Task 5/6 实现；`ModelTier`/`SourceId`/`VoiceSettings` 在 Task 2 定义并被各处引用；`createVoiceController` 返回结构与 Task 9 消费一致；i18n `voice.*` 键在 Task 7 定义并被 Task 8/9 使用。
- **YAGNI**：无实时流式、无 TTS、无波形、无自动发送、无后端改动，与 spec 一致。
