import { describe, it, expect } from 'vitest'
import { hasRenderableContent, isDialogueMessage, parseFileMarkers, deriveSessionTitle } from './conversation'
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

describe('deriveSessionTitle', () => {
  // mk 构造一条消息，简化用例书写；默认 user 角色。
  const mk = (m: Partial<ConversationMessage>): ConversationMessage =>
    ({ role: 'user', content: '', ...m }) as ConversationMessage

  it('取第一条 user 消息的纯文本作为标题', () => {
    // 最常见形态：首条 user 文字直接作会话名。
    expect(deriveSessionTitle([mk({ content: '查一下我的订单' })])).toBe('查一下我的订单')
  })

  it('折叠换行与连续空白为单个空格', () => {
    // 首句含换行/多空格时归一化为单空格，避免标题里出现断行与大段空白。
    expect(deriveSessionTitle([mk({ content: '第一行\n第二行   有空格' })])).toBe('第一行 第二行 有空格')
  })

  it('超过 20 字符时截断并补省略号', () => {
    // 21 字符输入应截到前 20 字符并追加 …（省略号不计入 20）。
    const long = '一二三四五六七八九十一二三四五六七八九十甲' // 21 个字符
    expect(deriveSessionTitle([mk({ content: long })])).toBe('一二三四五六七八九十一二三四五六七八九十…')
  })

  it('恰好 20 字符不加省略号', () => {
    // 边界：长度等于上限时原样返回，不截断、不加省略号。
    const exact = '一二三四五六七八九十一二三四五六七八九十' // 20 个字符
    expect(deriveSessionTitle([mk({ content: exact })])).toBe(exact)
  })

  it('content 为数组时取第一个非空 text part', () => {
    // 多模态消息优先用文字 part 作标题。
    const parts = [{ type: 'text', text: '来自数组的标题' }]
    expect(deriveSessionTitle([mk({ content: parts })])).toBe('来自数组的标题')
  })

  it('纯附件数组取第一个 input_file 的文件名', () => {
    // 首句只发了文件、没有文字时，用文件名当会话名。
    const parts = [{ type: 'input_file', file_id: 'f1', filename: '季度报告.pdf' }]
    expect(deriveSessionTitle([mk({ content: parts })])).toBe('季度报告.pdf')
  })

  it('字符串含 oc-file 标记与正文时剥标记后取正文', () => {
    // 服务端回读的带文件消息，标记需剥除，只保留用户正文。
    const enc = encodeURIComponent('发票.pdf')
    const content = `<oc-file:f1:${enc}>\n帮我看看这个文件`
    expect(deriveSessionTitle([mk({ content })])).toBe('帮我看看这个文件')
  })

  it('字符串仅含 oc-file 标记（纯附件回读）时取文件名', () => {
    // 纯附件消息回读后只剩标记，正文为空，退回用解码后的文件名。
    const enc = encodeURIComponent('发票.pdf')
    expect(deriveSessionTitle([mk({ content: `<oc-file:f1:${enc}>` })])).toBe('发票.pdf')
  })

  it('跳过引擎开场白 assistant，取第一条 user 消息', () => {
    // 首条是引擎自动问候（assistant），标题应取用户真正发起的第一句。
    const msgs = [mk({ role: 'assistant', content: '您好，有什么可以帮您？' }), mk({ content: '我要退货' })]
    expect(deriveSessionTitle(msgs)).toBe('我要退货')
  })

  it('没有 user 消息时返回 null', () => {
    // 只有 assistant / 无对话时无法派生标题。
    expect(deriveSessionTitle([mk({ role: 'assistant', content: '在的' })])).toBeNull()
    expect(deriveSessionTitle([])).toBeNull()
  })

  it('首条 user 内容为空或全空白时返回 null', () => {
    // 空内容不派生标题，调用方保持 id 兜底显示。
    expect(deriveSessionTitle([mk({ content: '   \n\t ' })])).toBeNull()
    expect(deriveSessionTitle([mk({ content: [] })])).toBeNull()
  })
})
