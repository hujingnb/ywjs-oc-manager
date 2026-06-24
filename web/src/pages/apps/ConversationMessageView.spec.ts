import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ConversationMessageView from './ConversationMessageView.vue'

// ConversationMessageView 负责单条消息渲染：assistant 文本走 markdown，其余角色保持纯文本。
describe('ConversationMessageView', () => {
  // assistant 字符串内容应按 markdown 渲染为对应 HTML 结构（标题/加粗/列表/行内代码）。
  it('renders assistant string content as markdown', () => {
    const md = '# 标题\n\n这是 **加粗** 与 `code`\n\n- 项一\n- 项二'
    const wrapper = mount(ConversationMessageView, {
      props: { message: { role: 'assistant', content: md } },
    })
    const html = wrapper.find('.markdown-body').html()
    expect(html).toContain('<h1>') // # → 标题
    expect(html).toContain('<strong>加粗</strong>') // ** → 加粗
    expect(html).toContain('<code>code</code>') // ` → 行内代码
    expect(html).toContain('<ul>') // - → 无序列表
    expect(wrapper.findAll('.markdown-body li')).toHaveLength(2) // 两个列表项
  })

  // assistant 的代码块（``` 围栏）应渲染为 <pre><code>，保证多行代码原样展示。
  it('renders fenced code block for assistant', () => {
    const wrapper = mount(ConversationMessageView, {
      props: { message: { role: 'assistant', content: '```\nline1\nline2\n```' } },
    })
    const html = wrapper.find('.markdown-body').html()
    expect(html).toContain('<pre>') // 围栏代码块
    expect(html).toContain('<code>') // 代码块内 code 节点
  })

  // user 角色不启用 markdown：markdown 语法应原样以纯文本展示，不产生 strong/markdown-body 节点。
  it('keeps user content as plain text', () => {
    const wrapper = mount(ConversationMessageView, {
      props: { message: { role: 'user', content: '这是 **不应加粗** 的文本' } },
    })
    expect(wrapper.find('.markdown-body').exists()).toBe(false) // 无 markdown 容器
    expect(wrapper.find('strong').exists()).toBe(false) // ** 未被解析为加粗
    expect(wrapper.text()).toContain('**不应加粗**') // 原样保留 markdown 字面
  })

  // 安全边界：assistant 内容里的原始 HTML 必须被转义（markdown-it html:false），
  // 不能在 DOM 中生成真实的 <script>/<img> 元素，防止 LLM 输出导致 XSS。
  it('escapes raw HTML in assistant content to prevent XSS', () => {
    const wrapper = mount(ConversationMessageView, {
      props: { message: { role: 'assistant', content: '<script>alert(1)<\/script><img src=x onerror=alert(2)>' } },
    })
    // 不应注入可执行/危险的真实元素
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.find('img[onerror]').exists()).toBe(false)
    // 原始标签应以转义文本形式出现在 HTML 中
    expect(wrapper.find('.markdown-body').html()).toContain('&lt;script&gt;')
  })

  // 多模态数组内容：assistant 的 text part 走 markdown，image_url part 仍渲染为图片。
  it('renders multimodal assistant parts: markdown text + image', () => {
    const wrapper = mount(ConversationMessageView, {
      props: {
        message: {
          role: 'assistant',
          content: [
            { type: 'text', text: '**重点**' },
            { type: 'image_url', image_url: { url: 'https://example.com/a.png' } },
          ],
        },
      },
    })
    expect(wrapper.find('.markdown-body strong').exists()).toBe(true) // text part markdown 生效
    expect(wrapper.find('img.msg-image').attributes('src')).toBe('https://example.com/a.png') // 图片渲染
  })

  // 多模态里 user 的 text part 保持纯文本，不渲染 markdown 容器。
  it('keeps multimodal user text part as plain text', () => {
    const wrapper = mount(ConversationMessageView, {
      props: {
        message: { role: 'user', content: [{ type: 'text', text: '**保持原样**' }] },
      },
    })
    expect(wrapper.find('.markdown-body').exists()).toBe(false) // 无 markdown 渲染
    expect(wrapper.text()).toContain('**保持原样**') // 字面保留
  })
})
