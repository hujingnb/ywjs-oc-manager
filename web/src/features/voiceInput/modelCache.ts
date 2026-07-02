// modelCache：检测某档位+源的 whisper 模型是否已下载到浏览器本地缓存。
// Transformers.js 用 Cache API（缓存名 'transformers-cache'）按「完整远端 URL」为键缓存每个模型文件，
// 键形如 https://www.modelscope.cn/Xenova/whisper-base/resolve/main/onnx/decoder_model_merged_quantized.onnx。
// decoder onnx 是加载时最后/最大的文件，它存在即代表该档位在该源下已完整下载可离线使用；
// 半截下载（只有 config 等小文件）不会命中，故不会误报「已下载」。
import type { ModelTier, SourceId } from './voiceSettings'
import { hostForSource, repoForTier, decoderFileForTier } from './voiceSettings'

// TRANSFORMERS_CACHE_NAME 与 Transformers.js 内部 caches.open(...) 名称一致。
const TRANSFORMERS_CACHE_NAME = 'transformers-cache'

// modelMarkerUrl 构造某档位+源下用于判定的缓存键 URL（纯函数，与 Transformers.js 的 URL 拼接规则一致）。
// 判定文件用该档位的 decoder 权重（不同档位量化命名不同，如 base 是 *_quantized.onnx、turbo 是 *_q4.onnx）。
export function modelMarkerUrl(tier: ModelTier, source: SourceId): string {
  return `${hostForSource(source)}/${repoForTier(tier)}/resolve/main/onnx/${decoderFileForTier(tier)}`
}

// isTierCached 查询某档位在指定源下是否已完整缓存；无 Cache API 或异常时保守返回 false（当作未下载）。
export async function isTierCached(tier: ModelTier, source: SourceId): Promise<boolean> {
  if (typeof caches === 'undefined') return false
  try {
    const cache = await caches.open(TRANSFORMERS_CACHE_NAME)
    const hit = await cache.match(modelMarkerUrl(tier, source))
    return !!hit
  } catch {
    return false
  }
}

// cachedTiers 批量查询多个档位在指定源下的缓存状态，返回 tier→是否已下载 的映射，供 UI 一次性标记。
export async function cachedTiers(tiers: ModelTier[], source: SourceId): Promise<Record<string, boolean>> {
  const entries = await Promise.all(tiers.map(async (t) => [t, await isTierCached(t, source)] as const))
  return Object.fromEntries(entries)
}
