// voiceSettings 纯逻辑单测：档位→仓库映射、源→host 映射、localStorage 读写与容错。
import { describe, it, expect, beforeEach } from 'vitest'
import {
  repoForTier,
  hostForSource,
  dtypeForTier,
  decoderFileForTier,
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
  // 档位到 whisper 仓库名的映射，四档齐全(turbo 走 onnx-community large-v3-turbo)
  it('repoForTier 映射四档到对应仓库', () => {
    expect(repoForTier('tiny')).toBe('Xenova/whisper-tiny')
    expect(repoForTier('base')).toBe('Xenova/whisper-base')
    expect(repoForTier('small')).toBe('Xenova/whisper-small')
    expect(repoForTier('turbo')).toBe('onnx-community/whisper-large-v3-turbo')
  })

  // 各档位量化精度：tiny/base/small 用 q8 字符串，turbo 用 q4 的分子模型对象
  it('dtypeForTier 按档位返回量化精度', () => {
    expect(dtypeForTier('base')).toBe('q8')
    expect(dtypeForTier('turbo')).toEqual({ encoder_model: 'q4', decoder_model_merged: 'q4' })
  })

  // decoder 文件名随量化命名变化：q8→*_quantized.onnx，q4→*_q4.onnx(供缓存判定)
  it('decoderFileForTier 按档位返回 decoder 文件名', () => {
    expect(decoderFileForTier('base')).toBe('decoder_model_merged_quantized.onnx')
    expect(decoderFileForTier('turbo')).toBe('decoder_model_merged_q4.onnx')
  })

  // 下载源到 remoteHost 的映射：国内 ModelScope 与官方
  it('hostForSource 映射国内 ModelScope 与官方站点', () => {
    expect(hostForSource('domestic')).toBe('https://www.modelscope.cn')
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
