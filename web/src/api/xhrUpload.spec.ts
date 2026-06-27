import { beforeEach, describe, expect, it, vi } from 'vitest'

const clientMocks = vi.hoisted(() => ({
  getStoredAccessToken: vi.fn(),
  getCsrfToken: vi.fn(),
  clearStoredTokens: vi.fn(),
  triggerUnauthorized: vi.fn(),
  extractErrorMessage: vi.fn((body: unknown, status: number) => {
    if (body && typeof body === 'object' && 'error' in body) {
      return String((body as { error: unknown }).error)
    }
    return `请求失败 (${status})`
  }),
}))

vi.mock('@/api/client', () => clientMocks)

// FakeXHR 模拟 jsdom 缺失的 upload 进度事件与 abort 行为。
// 通过 lastInstance 暴露给测试，便于断言 requestHeaders 与触发回调。
class FakeXHR {
  static lastInstance: FakeXHR | null = null
  // upload 暴露 onprogress 与 onload：onload 对应请求体发送完成（字节已传完、等服务端响应）。
  upload = {
    onprogress: null as ((e: ProgressEvent) => void) | null,
    onload: null as (() => void) | null,
  }
  status = 0
  responseText = ''
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  onabort: (() => void) | null = null
  aborted = false
  method = ''
  url = ''
  body: unknown = null
  requestHeaders: Array<[string, string]> = []
  private responseHeaders: Record<string, string> = {}
  constructor() { FakeXHR.lastInstance = this }
  open(method: string, url: string): void { this.method = method; this.url = url }
  setRequestHeader(name: string, value: string): void { this.requestHeaders.push([name, value]) }
  send(body: unknown): void { this.body = body }
  abort(): void { this.aborted = true; this.onabort?.() }
  getResponseHeader(name: string): string | null { return this.responseHeaders[name.toLowerCase()] ?? null }
  // 测试辅助：触发 upload 进度事件
  _emitProgress(loaded: number, total: number): void {
    this.upload.onprogress?.({ loaded, total, lengthComputable: true } as ProgressEvent)
  }
  // 测试辅助：触发 upload.onload，模拟请求体已全部发送完成
  _emitUploadComplete(): void {
    this.upload.onload?.()
  }
  // 测试辅助：模拟服务端 2xx/4xx/5xx 响应
  _complete(status: number, body: string, contentType = 'application/json'): void {
    this.status = status
    this.responseText = body
    this.responseHeaders['content-type'] = contentType
    this.onload?.()
  }
  // 测试辅助：模拟网络层错误
  _error(): void { this.onerror?.() }
}

describe('xhrUpload', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    FakeXHR.lastInstance = null
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
  })

  // 2xx + JSON 响应：返回 status 与解析后的 body。
  it('2xx JSON 响应返回 status 与解析后的 body', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._complete(201, JSON.stringify({ ok: true }))
    const res = await promise
    expect(res).toEqual({ status: 201, body: { ok: true } })
  })

  // Bearer + CSRF 头：默认 withAuth=true 且 POST/PUT/PATCH/DELETE 自动加 CSRF。
  it('默认注入 Bearer 与 CSRF 头', async () => {
    clientMocks.getStoredAccessToken.mockReturnValue('access-1')
    clientMocks.getCsrfToken.mockReturnValue('csrf-1')
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._complete(200, '{}')
    await promise
    const headers = Object.fromEntries(FakeXHR.lastInstance!.requestHeaders)
    expect(headers.Authorization).toBe('Bearer access-1')
    expect(headers['X-CSRF-Token']).toBe('csrf-1')
  })

  // 自定义 headers 与 withAuth=false：不注入 Bearer，调用方 headers 透传。
  it('withAuth=false 不注入 Bearer 但透传自定义 headers', async () => {
    clientMocks.getStoredAccessToken.mockReturnValue('should-not-appear')
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', {
      body: new Blob(['x']),
      withAuth: false,
      headers: { 'Content-Type': 'application/octet-stream' },
    })
    FakeXHR.lastInstance!._complete(200, '{}')
    await promise
    const headers = Object.fromEntries(FakeXHR.lastInstance!.requestHeaders)
    expect(headers.Authorization).toBeUndefined()
    expect(headers['Content-Type']).toBe('application/octet-stream')
  })

  // 进度回调：upload.onprogress 事件触发用户 onProgress(loaded, total)。
  it('进度事件转发到 onProgress 回调', async () => {
    const onProgress = vi.fn()
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']), onProgress })
    FakeXHR.lastInstance!._emitProgress(30, 100)
    FakeXHR.lastInstance!._emitProgress(100, 100)
    FakeXHR.lastInstance!._complete(200, '{}')
    await promise
    expect(onProgress).toHaveBeenCalledWith(30, 100)
    expect(onProgress).toHaveBeenCalledWith(100, 100)
  })

  // AbortSignal：触发 abort 后 xhr.abort 被调用，promise reject AbortError。
  it('AbortSignal 触发 xhr.abort 并 reject AbortError', async () => {
    const controller = new AbortController()
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']), signal: controller.signal })
    controller.abort()
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' })
    expect(FakeXHR.lastInstance!.aborted).toBe(true)
  })

  // signal 在调用前已 aborted：直接 reject，不发出请求。
  it('已 aborted 的 signal 直接 reject', async () => {
    const controller = new AbortController()
    controller.abort()
    const { xhrUpload } = await import('./xhrUpload')
    await expect(
      xhrUpload('/api/v1/upload', { body: new Blob(['x']), signal: controller.signal }),
    ).rejects.toMatchObject({ name: 'AbortError' })
  })

  // 非 2xx：抛 ApiError，message 来自 extractErrorMessage(body, status)。
  it('非 2xx 抛 ApiError 并附带 status 与 body', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._complete(403, JSON.stringify({ error: '没有权限' }))
    await expect(promise).rejects.toMatchObject({
      status: 403,
      body: { error: '没有权限' },
      message: '没有权限',
    })
  })

  // 401 且 withAuth=true：clearStoredTokens + triggerUnauthorized(path) 被调用。
  it('401 触发 token 清理与 unauthorized 跳转', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._complete(401, JSON.stringify({ error: 'unauthorized' }))
    await expect(promise).rejects.toMatchObject({ status: 401 })
    expect(clientMocks.clearStoredTokens).toHaveBeenCalled()
    expect(clientMocks.triggerUnauthorized).toHaveBeenCalledWith('/api/v1/upload')
  })

  // 401 且 withAuth=false：不清 token、不跳转（与登录接口语义一致）。
  it('401 在 withAuth=false 时不清 token', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']), withAuth: false })
    FakeXHR.lastInstance!._complete(401, '{}')
    await expect(promise).rejects.toMatchObject({ status: 401 })
    expect(clientMocks.clearStoredTokens).not.toHaveBeenCalled()
  })

  // 网络错误：xhr.onerror 触发后 reject 一个含 status=0 的 ApiError。
  it('网络错误 reject 0 状态的 ApiError', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._error()
    await expect(promise).rejects.toMatchObject({ status: 0 })
  })

  // FormData body 也能透传，且不强制 Content-Type（浏览器自动设 boundary）。
  it('FormData body 不强制 Content-Type', async () => {
    const body = new FormData()
    body.append('file', new Blob(['x']))
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body })
    FakeXHR.lastInstance!._complete(200, '{}')
    await promise
    expect(FakeXHR.lastInstance!.body).toBe(body)
    const headers = Object.fromEntries(FakeXHR.lastInstance!.requestHeaders)
    expect(headers['Content-Type']).toBeUndefined()
  })

  // onUploadComplete：请求体发送完成（upload.onload）时被调用一次，用于进入「处理中」反馈。
  it('请求体发送完成触发 onUploadComplete', async () => {
    const onUploadComplete = vi.fn()
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']), onUploadComplete })
    FakeXHR.lastInstance!._emitUploadComplete()
    FakeXHR.lastInstance!._complete(200, '{}')
    await promise
    expect(onUploadComplete).toHaveBeenCalledTimes(1)
  })

  // 不传 onUploadComplete：upload.onload 触发也不报错（可选回调，缺省无操作）。
  it('未传 onUploadComplete 时 upload.onload 不报错', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._emitUploadComplete()
    FakeXHR.lastInstance!._complete(200, '{}')
    await expect(promise).resolves.toMatchObject({ status: 200 })
  })
})
