// modelCache 纯逻辑单测：验证缓存判定用的标记 URL 拼接与 Transformers.js 实际缓存键一致
// （键形如 <host>/<repo>/resolve/main/onnx/decoder_model_merged_quantized.onnx，已由真机 dump 缓存 keys 核对）。
// isTierCached/cachedTiers 依赖浏览器 Cache API，jsdom 不具备，故这里只覆盖可纯测的 URL 构造。
import { describe, it, expect } from 'vitest'
import { modelMarkerUrl } from './modelCache'

describe('modelCache', () => {
  // 国内源 ModelScope + base 档：应拼出魔搭上 base 仓库的 decoder 权重完整 URL
  it('modelMarkerUrl 国内源 base 档拼接正确', () => {
    expect(modelMarkerUrl('base', 'domestic')).toBe(
      'https://www.modelscope.cn/Xenova/whisper-base/resolve/main/onnx/decoder_model_merged_quantized.onnx',
    )
  })

  // 官方源 + tiny 档：host 换成 huggingface.co、仓库换成 whisper-tiny
  it('modelMarkerUrl 官方源 tiny 档拼接正确', () => {
    expect(modelMarkerUrl('tiny', 'official')).toBe(
      'https://huggingface.co/Xenova/whisper-tiny/resolve/main/onnx/decoder_model_merged_quantized.onnx',
    )
  })

  // 同一档位在不同源下 URL 不同（缓存按 host 区分，故「已下载」需按当前所选源判定）
  it('同档位不同源的标记 URL 不同', () => {
    expect(modelMarkerUrl('small', 'domestic')).not.toBe(modelMarkerUrl('small', 'official'))
  })
})
