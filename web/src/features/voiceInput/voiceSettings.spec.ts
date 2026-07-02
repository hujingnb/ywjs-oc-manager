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
