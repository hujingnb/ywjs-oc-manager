// xhrUpload 把上传请求封装为 XMLHttpRequest，暴露 onProgress 回调与 AbortSignal 取消能力。
// 与 apiRequest 等价的 Bearer / CSRF / 401 处理统一在这里复用，保证三处文件上传不绕过统一鉴权约束。
import {
  clearStoredTokens,
  extractErrorMessage,
  getCsrfToken,
  getStoredAccessToken,
  triggerUnauthorized,
  type ApiError,
} from '@/api/client'
import { i18n } from '@/i18n'

// XhrUploadOptions 描述一次上传请求的全部入参；onProgress 与 signal 为可选。
export interface XhrUploadOptions {
  // HTTP 方法，缺省 POST。上传场景目前只用 POST/PUT。
  method?: 'POST' | 'PUT'
  // 调用方自定义 header；与内部注入的 Authorization / X-CSRF-Token 合并，调用方覆盖优先。
  headers?: Record<string, string>
  // 上传内容：原始字节流（Blob/File）或 multipart 表单。
  body: Blob | FormData
  // 进度回调，loaded / total 由浏览器 upload.onprogress 事件提供。
  onProgress?: (loaded: number, total: number) => void
  // 取消信号，调用方在中途 abort 上传。
  signal?: AbortSignal
  // 请求体发送完成（字节已全部上传、等待服务端响应）时触发。
  // 直传场景据此进入「处理中」反馈，避免进度卡在 100% 看起来像卡死。
  onUploadComplete?: () => void
  // 是否注入 Bearer。默认 true；登录类接口可置 false。
  withAuth?: boolean
}

// XhrUploadResponse 是成功路径返回值，body 在 JSON 响应时为解析后的对象，否则为原始字符串。
export interface XhrUploadResponse {
  status: number
  body: unknown
}

// xhrUpload 发送一次带进度反馈的上传请求。
// resolve：HTTP 2xx，body 按 content-type 解析为 JSON 或字符串；
// reject：非 2xx 抛带 status/body/message 的 ApiError；signal abort 抛 AbortError；网络错误抛 status=0 的 ApiError。
export function xhrUpload(url: string, opts: XhrUploadOptions): Promise<XhrUploadResponse> {
  return new Promise((resolve, reject) => {
    // 已取消的 signal：不发请求直接 reject，与 fetch + signal 行为一致。
    if (opts.signal?.aborted) {
      reject(makeAbortError())
      return
    }

    const method = opts.method ?? 'POST'
    const withAuth = opts.withAuth !== false
    const xhr = new XMLHttpRequest()
    xhr.open(method, url)

    // 头部注入顺序：先 Authorization / CSRF，再调用方 headers 覆盖（调用方需要显式覆盖 Content-Type 时优先生效）。
    if (withAuth) {
      const token = getStoredAccessToken()
      if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    }
    // 写操作要带 CSRF double-submit 头；GET/HEAD/OPTIONS 不需要。
    const upperMethod = method.toUpperCase()
    if (upperMethod !== 'GET' && upperMethod !== 'HEAD' && upperMethod !== 'OPTIONS') {
      const csrf = getCsrfToken()
      if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf)
    }
    if (opts.headers) {
      for (const [k, v] of Object.entries(opts.headers)) {
        xhr.setRequestHeader(k, v)
      }
    }

    // 进度事件：浏览器自身节流到 ~16ms，调用方不必再做节流。
    if (opts.onProgress) {
      xhr.upload.onprogress = (e: ProgressEvent) => {
        opts.onProgress!(e.loaded, e.total)
      }
    }

    // upload.onload：浏览器在请求体发送完成时触发——此刻字节已全部上传，仅在等服务端响应。
    if (opts.onUploadComplete) {
      xhr.upload.onload = () => {
        opts.onUploadComplete!()
      }
    }

    // 取消信号：abort 立即触发 xhr.abort，reject AbortError；onabort 防御性兜底（abort 后浏览器仍会触发 onload 的极少数情况）。
    if (opts.signal) {
      opts.signal.addEventListener('abort', () => {
        xhr.abort()
      })
    }
    xhr.onabort = () => reject(makeAbortError())

    xhr.onerror = () => {
      const err = makeApiError(i18n.global.t('common.errors.networkError'), 0, undefined)
      reject(err)
    }

    xhr.onload = () => {
      const status = xhr.status
      const contentType = xhr.getResponseHeader('content-type') ?? ''
      // 按 content-type 解析响应体：JSON 解析失败时退回 raw text，避免吞掉服务端原始错误文案。
      let body: unknown = xhr.responseText
      if (contentType.includes('application/json') && xhr.responseText) {
        try {
          body = JSON.parse(xhr.responseText)
        } catch {
          body = xhr.responseText
        }
      }
      if (status >= 200 && status < 300) {
        resolve({ status, body })
        return
      }
      // 401：与 apiRequest 一致清 token 并触发跳登录；登录类接口（withAuth=false）跳过，避免把登录接口自身的 401 也跳了。
      if (status === 401 && withAuth) {
        clearStoredTokens()
        triggerUnauthorized(url)
      }
      reject(makeApiError(extractErrorMessage(body, status), status, body))
    }

    xhr.send(opts.body)
  })
}

// makeAbortError 构造一个与 DOMException('aborted', 'AbortError') 等价的错误对象。
// 直接用 DOMException 会让 jsdom 在部分环境抛构造异常，因此手工赋 name。
function makeAbortError(): Error {
  const err = new Error('aborted') as Error & { name: string }
  err.name = 'AbortError'
  return err
}

// makeApiError 构造与 client.ts 的 ApiError 形态一致的错误对象。
// 不直接 import ApiError 类型（它是 interface 不是 class），用 Object.assign 注入 status / body 字段。
function makeApiError(message: string, status: number, body: unknown): ApiError {
  return Object.assign(new Error(message), { status, body }) as ApiError
}
