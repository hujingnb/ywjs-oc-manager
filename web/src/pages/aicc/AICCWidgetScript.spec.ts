// AICC 挂件脚本测试覆盖外站复制脚本后的最小运行闭环：
// 脚本读取 data-aicc-widget-token，创建固定入口按钮，点击后用 iframe 打开隔离聊天页。
import { afterEach, describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('aicc-widget.js', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.head.innerHTML = ''
  })

  // 场景：客户官网贴入 script 后，页面右下角出现 AICC 入口，并且 iframe 使用 web_widget 渠道打开。
  it('mounts a floating launcher and opens chat iframe with widget channel', () => {
    const script = document.createElement('script')
    script.src = 'https://ocm.localhost/aicc-widget.js'
    script.dataset.aiccWidgetToken = 'widget-token-1'
    document.head.appendChild(script)

    const source = readFileSync(resolve(__dirname, '../../../public/aicc-widget.js'), 'utf8')
    window.eval(source)

    const launcher = document.querySelector<HTMLButtonElement>('[data-aicc-widget-launcher]')
    expect(launcher).not.toBeNull()
    expect(launcher?.textContent).toContain('在线客服')

    launcher?.click()

    const iframe = document.querySelector<HTMLIFrameElement>('[data-aicc-widget-frame]')
    expect(iframe).not.toBeNull()
    expect(iframe?.src).toBe('https://ocm.localhost/aicc/widget-token-1?aicc_channel=web_widget')
  })
})
