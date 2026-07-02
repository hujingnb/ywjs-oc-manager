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
// domestic 用 ModelScope(魔搭)：国内直连、对模型文件返回 access-control-allow-origin:* 且无重定向，
// 浏览器可直接 fetch。注意不能用 hf-mirror.com——它把 /resolve/ 请求 308 重定向到被墙的
// huggingface.co 且重定向跳不带 CORS 头，浏览器会 net::ERR_FAILED(hf-mirror 仅供 Python CLI 设
// HF_ENDPOINT，不适配浏览器 fetch)。ModelScope 镜像了 Xenova/whisper-* 同名仓库与路径布局，
// 故 repoForTier 无需改动，默认 remotePathTemplate 也可直接工作。
export const SOURCE_OPTIONS: SourceOption[] = [
  { id: 'domestic', host: 'https://www.modelscope.cn' },
  { id: 'official', host: 'https://huggingface.co' },
]

// DEFAULT_TIER popover 首次打开时预选的档位(base 是中文准确率与体积的折中)。
export const DEFAULT_TIER: ModelTier = 'base'
// DEFAULT_SOURCE 默认下载源，面向大陆用户默认走国内 ModelScope。
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
