(function () {
  'use strict'

  var script = document.currentScript || document.querySelector('script[data-aicc-widget-token]')
  if (!script) return

  var token = (script.getAttribute('data-aicc-widget-token') || '').trim()
  var language = String(document.documentElement.lang || navigator.language || 'en').toLowerCase()
  var isZh = language.indexOf('zh') === 0
  var labels = isZh
    ? { open: '在线客服', close: '收起客服', pendingToken: '保存后生成' }
    : { open: 'Online support', close: 'Hide support', pendingToken: 'Generated after save' }
  if (!token || token === labels.pendingToken || token === '保存后生成') return

  var existing = document.querySelector('[data-aicc-widget-root="' + token + '"]')
  if (existing) return

  var baseURL = script.getAttribute('data-aicc-base-url')
  if (!baseURL) {
    try {
      baseURL = new URL(script.src, window.location.href).origin
    } catch (_) {
      baseURL = window.location.origin
    }
  }
  baseURL = String(baseURL).replace(/\/+$/, '')

  var root = document.createElement('div')
  root.setAttribute('data-aicc-widget-root', token)
  root.style.position = 'fixed'
  root.style.right = '20px'
  root.style.bottom = '20px'
  root.style.zIndex = '2147483647'
  root.style.fontFamily = 'Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'

  var launcher = document.createElement('button')
  launcher.type = 'button'
  launcher.setAttribute('data-aicc-widget-launcher', '')
  launcher.setAttribute('aria-expanded', 'false')
  launcher.textContent = labels.open
  launcher.style.minWidth = '116px'
  launcher.style.height = '44px'
  launcher.style.border = '0'
  launcher.style.borderRadius = '22px'
  launcher.style.padding = '0 18px'
  launcher.style.background = '#111827'
  launcher.style.color = '#fff'
  launcher.style.boxShadow = '0 12px 32px rgba(17, 24, 39, 0.28)'
  launcher.style.cursor = 'pointer'
  launcher.style.fontSize = '14px'
  launcher.style.fontWeight = '700'

  var panel = document.createElement('div')
  panel.setAttribute('data-aicc-widget-panel', '')
  panel.style.display = 'none'
  panel.style.width = 'min(420px, calc(100vw - 32px))'
  panel.style.height = 'min(680px, calc(100vh - 96px))'
  panel.style.marginBottom = '12px'
  panel.style.overflow = 'hidden'
  panel.style.border = '1px solid rgba(17, 24, 39, 0.16)'
  panel.style.borderRadius = '8px'
  panel.style.background = '#fff'
  panel.style.boxShadow = '0 24px 72px rgba(17, 24, 39, 0.24)'

  var iframe = document.createElement('iframe')
  iframe.setAttribute('data-aicc-widget-frame', '')
  iframe.title = labels.open
  iframe.src = baseURL + '/aicc/' + encodeURIComponent(token) + '?aicc_channel=web_widget'
  iframe.allow = 'clipboard-write'
  iframe.style.width = '100%'
  iframe.style.height = '100%'
  iframe.style.border = '0'
  iframe.style.display = 'block'

  panel.appendChild(iframe)
  root.appendChild(panel)
  root.appendChild(launcher)
  document.body.appendChild(root)

  launcher.addEventListener('click', function () {
    var open = panel.style.display !== 'none'
    panel.style.display = open ? 'none' : 'block'
    launcher.setAttribute('aria-expanded', open ? 'false' : 'true')
    launcher.textContent = open ? labels.open : labels.close
  })
})()
