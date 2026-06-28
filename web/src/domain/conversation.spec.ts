import { describe, it, expect } from 'vitest'
import { hasRenderableContent, isDialogueMessage, parseFileMarkers } from './conversation'
import type { ConversationMessage } from '@/api/conversations'

describe('hasRenderableContent', () => {
  it('非空字符串视为有内容', () => {
    // 最常见形态：content 为普通文字回复。
    expect(hasRenderableContent('你好')).toBe(true)
  })

  it('空串与纯空白串视为无内容', () => {
    // assistant 仅触发工具调用时 content 常为空串/空白，过滤后避免空气泡。
    expect(hasRenderableContent('')).toBe(false)
    expect(hasRenderableContent('   \n\t ')).toBe(false)
  })

  it('多模态数组含非空 text part 时有内容', () => {
    // parts 数组里有可渲染文字即算有内容。
    expect(hasRenderableContent([{ type: 'text', text: '看这里' }])).toBe(true)
  })

  it('多模态数组含 image_url part 时有内容', () => {
    // 仅含图片也属于可展示内容。
    expect(hasRenderableContent([{ type: 'image_url', image_url: { url: 'http://x/a.png' } }])).toBe(true)
  })

  it('多模态数组仅含空 text part 视为无内容', () => {
    // text 为空白的 part 不算可展示内容，避免渲染空气泡。
    expect(hasRenderableContent([{ type: 'text', text: '  ' }])).toBe(false)
  })

  it('空数组视为无内容', () => {
    // 没有任何 part 自然无可展示内容。
    expect(hasRenderableContent([])).toBe(false)
  })

  it('未知 part 类型不计入可展示内容', () => {
    // 会话页只渲染 text / image_url，其它 part 类型（如工具相关）不算对话正文。
    expect(hasRenderableContent([{ type: 'tool_use', name: 'search' }])).toBe(false)
  })

  // 含 input_file part 的消息可渲染。
  it('input_file part 视为可渲染', () => {
    expect(hasRenderableContent([{ type: 'input_file', file_id: 'f1' }])).toBe(true)
  })

  it('null / undefined / 对象等非串非数组视为无内容', () => {
    // 兜底：无法识别的 content 形态不展示，避免渲染异常。
    expect(hasRenderableContent(null)).toBe(false)
    expect(hasRenderableContent(undefined)).toBe(false)
    expect(hasRenderableContent({ foo: 'bar' })).toBe(false)
  })
})

describe('isDialogueMessage', () => {
  // mk 构造一条消息，简化用例书写。
  const mk = (m: Partial<ConversationMessage>): ConversationMessage =>
    ({ role: 'assistant', content: '', ...m }) as ConversationMessage

  it('role===tool 的工具结果一律过滤', () => {
    // 工具执行结果属于引擎内部过程，即便 content 非空也不展示。
    expect(isDialogueMessage(mk({ role: 'tool', content: 'Search results: ...' }))).toBe(false)
  })

  it('保留含内容的 user 消息', () => {
    // 用户输入是对话正文。
    expect(isDialogueMessage(mk({ role: 'user', content: '请帮我查一下' }))).toBe(true)
  })

  it('保留含内容的 assistant 消息', () => {
    // 客服文字回复是对话正文。
    expect(isDialogueMessage(mk({ role: 'assistant', content: '好的，正在为你处理' }))).toBe(true)
  })

  it('过滤仅含工具调用、content 为空的 assistant 步骤', () => {
    // 这一步只触发工具调用、无文字，过滤后会留下空气泡，应跳过。
    expect(isDialogueMessage(mk({ role: 'assistant', content: '', tool_calls: [{}] }))).toBe(false)
  })
})

describe('parseFileMarkers', () => {
  // 新格式：剥离注记+标记，返回 fileId 与解码文件名，clean 只剩用户正文。
  it('parseFileMarkers 剥离注记与新格式标记并解码文件名', () => {
    const enc = encodeURIComponent('我的 报告.pdf')
    const text = `[The user sent a document: '我的 报告.pdf'. The file is saved at: /opt/data/cache/documents/x.pdf. Ask the user what they'd like you to do with it.] <oc-file:f1:${enc}>\n\n帮我看看`
    const r = parseFileMarkers(text)
    expect(r.files).toEqual([{ fileId: 'f1', filename: '我的 报告.pdf' }])
    expect(r.clean).toBe('帮我看看')
    expect(r.clean).not.toContain('/opt/data')
  })

  // 旧格式（无文件名）兼容。
  it('parseFileMarkers 兼容旧格式标记', () => {
    const r = parseFileMarkers('hi <oc-file:f2>')
    expect(r.files).toEqual([{ fileId: 'f2', filename: '' }])
    expect(r.clean).toBe('hi')
  })
})
