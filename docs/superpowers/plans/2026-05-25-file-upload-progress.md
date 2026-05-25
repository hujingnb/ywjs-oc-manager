# 文件上传进度条与离开警告 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为三处文件上传（组织 / 应用知识库、assistant skill tar）增加可视化进度反馈、批量计数、取消按钮和刷新警告，全部前端改动、后端零改动。

**Architecture:** 新增底层 `xhrUpload` 工具替换三个 mutation hook 内部的 `fetch`；新增 Pinia setup-style store + 全局 Modal 协调上传会话；在 `App.vue` 挂载 Modal + `beforeunload` 守卫；改三个业务页面把直接的 `mutateAsync` 换成 `uploadProgress.run`。一次只允许一个上传会话，互斥；批量上传串行执行并显示「当前文件进度 + N/M」。

**Tech Stack:** Vue 3.5 setup + TypeScript 5.9 / Pinia 3 (setup-style) / naive-ui 2.43 / TanStack vue-query 5.90 / Vitest 3 + @vue/test-utils + jsdom 29

**Spec:** `docs/superpowers/specs/2026-05-25-file-upload-progress-design.md`

---

## 文件结构

| 路径 | 状态 | 职责 |
| --- | --- | --- |
| `web/src/api/client.ts` | 改动 | 导出 `triggerUnauthorized(path)` helper，供 `xhrUpload` 复用 401 处理逻辑 |
| `web/src/api/xhrUpload.ts` | 新增 | XHR 上传工具：Bearer / CSRF / onProgress / AbortSignal / 401 / 非 2xx 错误 |
| `web/src/api/xhrUpload.spec.ts` | 新增 | xhrUpload 单元测试，FakeXHR 替换全局 XMLHttpRequest |
| `web/src/stores/uploadProgress.ts` | 新增 | Pinia setup-style store：会话状态、文件队列、当前进度、取消句柄、互斥规则 |
| `web/src/stores/uploadProgress.spec.ts` | 新增 | store 单元测试 |
| `web/src/composables/useBeforeUnloadGuard.ts` | 新增 | `beforeunload` 监听，会话进行中 `preventDefault` |
| `web/src/composables/useBeforeUnloadGuard.spec.ts` | 新增 | guard 单元测试 |
| `web/src/components/UploadProgressModal.vue` | 新增 | 全局 Modal：进度条 / N/M / 取消 / 关闭 / 汇总 |
| `web/src/components/__tests__/UploadProgressModal.spec.ts` | 新增 | Modal 单元测试（沿用项目 `components/__tests__/` 习惯） |
| `web/src/App.vue` | 改动 | 挂载 `<UploadProgressModal />`，调用 `useBeforeUnloadGuard()` |
| `web/src/api/hooks/useKnowledge.ts` | 改动 | `useUploadOrgKnowledge` / `useUploadAppKnowledge`：内部切到 `xhrUpload`，扩 optional `onProgress` / `signal`，`onSuccess` → `onSettled` |
| `web/src/api/hooks/useAssistantVersions.ts` | 改动 | `useUploadAssistantVersionSkill`：同上（multipart） |
| `web/src/pages/knowledge/OrgKnowledgePage.vue` | 改动 | `onUpload` 走 `useUploadProgressStore().run([...], runner)` |
| `web/src/pages/apps/AppKnowledgeTab.vue` | 改动 | `onUploadFile` 走 store.run |
| `web/src/pages/platform/AssistantVersionsPage.vue` | 改动 | `onSkillFileChange` 单文件 + `uploadPendingSkills` 批量都走 store.run |
| `web/src/pages/platform/AssistantVersionsPage.spec.ts` | 改动 | mock `useUploadProgressStore`，验证 `run` 被以正确 items 调用 |

---

## Task 1: 新增 `xhrUpload` 工具 + 单元测试

**Files:**
- Modify: `web/src/api/client.ts`（追加 `triggerUnauthorized` export）
- Create: `web/src/api/xhrUpload.ts`
- Create: `web/src/api/xhrUpload.spec.ts`

**Why:** 浏览器 `fetch` 不能报告 upload 进度，必须用 `XMLHttpRequest.upload.onprogress`。该工具是后续三个 mutation hook 的底层基石，必须先打地基；先写测试再写实现。

- [ ] **Step 1: 给 `client.ts` 追加 `triggerUnauthorized` 导出**

在 `web/src/api/client.ts` 现有 `setUnauthorizedHandler` 函数下方追加：

```typescript
// triggerUnauthorized 让模块外的请求工具（如 xhrUpload）也能复用 401 跳登录逻辑。
// 该函数仅转发到当前注册的 unauthorizedHandler；handler 未注册时静默无操作，与 apiRequest 行为一致。
export function triggerUnauthorized(path: string): void {
  if (unauthorizedHandler) {
    unauthorizedHandler(path)
  }
}
```

`apiRequest` 内现有的 401 分支保持不动（不做无关重构）。

- [ ] **Step 2: 写 `xhrUpload.spec.ts` 测试（FakeXHR + 全部场景）**

`web/src/api/xhrUpload.spec.ts`：

```typescript
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
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null }
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
})
```

- [ ] **Step 3: 运行测试确认全部失败**

```bash
cd web && npm run test -- src/api/xhrUpload.spec.ts
```

预期：所有用例 FAIL，错误为「Cannot find module './xhrUpload'」。

- [ ] **Step 4: 实现 `xhrUpload.ts`**

`web/src/api/xhrUpload.ts`：

```typescript
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

    // 取消信号：abort 立即触发 xhr.abort，reject AbortError；onabort 防御性兜底（abort 后浏览器仍会触发 onload 的极少数情况）。
    if (opts.signal) {
      opts.signal.addEventListener('abort', () => {
        xhr.abort()
      })
    }
    xhr.onabort = () => reject(makeAbortError())

    xhr.onerror = () => {
      const err = makeApiError('网络错误，请检查连接', 0, undefined)
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
```

- [ ] **Step 5: 运行测试确认全部通过**

```bash
cd web && npm run test -- src/api/xhrUpload.spec.ts
```

预期：所有用例 PASS。

- [ ] **Step 6: 跑一次 typecheck 确保类型 OK**

```bash
cd web && npm run typecheck
```

预期：无新增类型错误。

- [ ] **Step 7: Commit**

```bash
git add web/src/api/client.ts web/src/api/xhrUpload.ts web/src/api/xhrUpload.spec.ts
git commit -m "$(cat <<'EOF'
feat(upload): 增加 xhrUpload 工具支持上传进度与取消

为后续三处文件上传增加可视化进度反馈打底层基础。fetch 不能报告
upload 进度，必须改用 XMLHttpRequest.upload.onprogress；该工具同时
复用 apiRequest 的 Bearer / CSRF / 401 / 错误文案提取逻辑，保证
三处上传不绕过统一鉴权约束。

新增：xhrUpload(url, opts) 接受 Blob/FormData body、可选 onProgress
回调和 AbortSignal；非 2xx 抛 ApiError 形态错误；401 触发 token 清理
与跳登录；网络错误以 status=0 的 ApiError 表达。

client.ts 追加 triggerUnauthorized helper，让模块外工具复用 401 跳转
注册的 handler。
EOF
)"
```

---

## Task 2: 新增 `uploadProgress` Pinia store + 单元测试

**Files:**
- Create: `web/src/stores/uploadProgress.ts`
- Create: `web/src/stores/uploadProgress.spec.ts`

**Why:** 协调上传会话状态；按 spec 一次只允许一个会话，串行执行 items，失败继续后续 items，取消传播给所有 pending。

- [ ] **Step 1: 写 `uploadProgress.spec.ts` 测试**

`web/src/stores/uploadProgress.spec.ts`：

```typescript
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useUploadProgressStore } from './uploadProgress'

describe('uploadProgress store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // 单文件成功路径：item.status=succeeded，run resolve 含 1 个 succeeded。
  it('单文件成功路径', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    const runner = vi.fn().mockResolvedValue('ok')
    const result = await store.run([{ file, label: 'a.txt' }], runner)
    expect(runner).toHaveBeenCalledOnce()
    expect(result.succeeded).toHaveLength(1)
    expect(result.failed).toHaveLength(0)
    expect(result.cancelled).toHaveLength(0)
    expect(store.session?.items[0].status).toBe('succeeded')
    expect(store.isUploading).toBe(false)
  })

  // 单文件失败路径：runner 抛错，item.status=failed，error 保留 message；run 不抛。
  it('单文件失败路径，run 不抛错', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    const runner = vi.fn().mockRejectedValue(new Error('boom'))
    const result = await store.run([{ file, label: 'a.txt' }], runner)
    expect(result.failed).toHaveLength(1)
    expect(store.session?.items[0].status).toBe('failed')
    expect(store.session?.items[0].error).toBe('boom')
  })

  // 批量部分失败：第一个失败不阻止第二个执行，整体 succeeded=1 / failed=1。
  it('批量部分失败仍继续后续 item', async () => {
    const store = useUploadProgressStore()
    const file1 = new File(['x'], 'a.txt')
    const file2 = new File(['y'], 'b.txt')
    const runner = vi.fn()
      .mockRejectedValueOnce(new Error('first failed'))
      .mockResolvedValueOnce('ok')
    const result = await store.run([
      { file: file1, label: 'a.txt' },
      { file: file2, label: 'b.txt' },
    ], runner)
    expect(runner).toHaveBeenCalledTimes(2)
    expect(result.succeeded).toHaveLength(1)
    expect(result.failed).toHaveLength(1)
  })

  // 取消会让当前 runner 收到 AbortError，并把所有 pending item 标 cancelled。
  it('cancel 传播给当前 runner 与后续 pending item', async () => {
    const store = useUploadProgressStore()
    const file1 = new File(['x'], 'a.txt')
    const file2 = new File(['y'], 'b.txt')
    // 第一个 runner 永不 resolve，仅在 signal abort 时 reject AbortError，模拟真实 XHR 行为。
    const runner = vi.fn().mockImplementation(async (_item, _file, ctx) => {
      await new Promise((_, reject) => {
        ctx.signal.addEventListener('abort', () => {
          const err = new Error('aborted')
          err.name = 'AbortError'
          reject(err)
        })
      })
    })
    const promise = store.run([
      { file: file1, label: 'a.txt' },
      { file: file2, label: 'b.txt' },
    ], runner)
    // 等一个 microtask 让 runner 启动
    await Promise.resolve()
    store.cancel()
    const result = await promise
    expect(result.cancelled.length).toBeGreaterThanOrEqual(1)
    expect(store.session?.items[0].status).toBe('cancelled')
    expect(store.session?.items[1].status).toBe('cancelled')
    // 第二个 runner 不应被调用
    expect(runner).toHaveBeenCalledOnce()
  })

  // onProgress 回调把字节数写入 store.session.currentLoaded，供 Modal 渲染。
  it('runner 内 onProgress 回调更新 currentLoaded', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    let snapshot = 0
    const runner = vi.fn().mockImplementation(async (_item, _f, ctx) => {
      ctx.onProgress(50, 100)
      snapshot = store.session?.currentLoaded ?? -1
    })
    await store.run([{ file, label: 'a.txt' }], runner)
    expect(snapshot).toBe(50)
  })

  // 互斥规则：会话进行中第二次调用 run 抛错，且不影响第一次会话。
  it('会话进行中第二次 run 抛错', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    // 用永不 resolve 的 runner 保持会话激活
    const blockingRunner = vi.fn().mockImplementation(() => new Promise(() => {}))
    void store.run([{ file, label: 'a.txt' }], blockingRunner)
    await Promise.resolve()
    expect(() => store.run([{ file, label: 'b.txt' }], vi.fn())).toThrow('已有上传任务正在进行')
  })

  // reset 把 session 置空，isUploading 翻 false，再次 run 不再受互斥规则限制。
  it('reset 释放会话锁', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    const runner = vi.fn().mockResolvedValue('ok')
    await store.run([{ file, label: 'a.txt' }], runner)
    expect(store.session).not.toBeNull()
    store.reset()
    expect(store.session).toBeNull()
    expect(store.isUploading).toBe(false)
  })

  // isUploading 在会话中为 true、全部 item 结束后翻 false，但 session 在 reset 前仍保留供 Modal 展示汇总。
  it('isUploading 仅在仍有未结束 item 时为 true', async () => {
    const store = useUploadProgressStore()
    const file = new File(['x'], 'a.txt')
    let snapshotDuring = false
    const runner = vi.fn().mockImplementation(async () => {
      snapshotDuring = store.isUploading
    })
    await store.run([{ file, label: 'a.txt' }], runner)
    expect(snapshotDuring).toBe(true)
    expect(store.isUploading).toBe(false)
    // session 仍保留，等 reset 后才置 null（Modal 关闭时调 reset）
    expect(store.session).not.toBeNull()
  })
})
```

- [ ] **Step 2: 运行测试确认全部失败**

```bash
cd web && npm run test -- src/stores/uploadProgress.spec.ts
```

预期：FAIL，错误「Cannot find module './uploadProgress'」。

- [ ] **Step 3: 实现 `uploadProgress.ts`**

`web/src/stores/uploadProgress.ts`：

```typescript
// uploadProgress store 用一个会话集中管理多文件串行上传的状态：当前文件、字节进度、取消句柄。
// 一次只允许一个会话（互斥规则），保证全局 UploadProgressModal 不会被两个并发上传争用。
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

// UploadItemStatus 标记单文件在会话中的生命周期阶段。
export type UploadItemStatus = 'pending' | 'uploading' | 'succeeded' | 'failed' | 'cancelled'

// UploadItem 是会话内单个文件的视图。
export interface UploadItem {
  // 自动生成的 id；Modal 用作 v-for key、失败列表去重，便于排错。
  id: string
  // 显示名，一般是 file.name；批量上传时业务侧可传更易读的标签。
  label: string
  // 字节数，模板算 % 时分母用它。
  size: number
  status: UploadItemStatus
  // 仅 failed 用，文案来自 mutation 抛出的 Error.message。
  error?: string
}

// UploadSession 描述一次 run() 调用的全局状态。
export interface UploadSession {
  items: UploadItem[]
  // 0-based 指向当前正在传的 item。
  currentIndex: number
  // 当前 item 已传字节，由 runner 内 ctx.onProgress 回调写入。
  currentLoaded: number
  // 会话起始时间戳；v1 不渲染速率，仅留作 log。
  startedAt: number
}

// RunItem 是 run() 入参的最小形态：调用方提供 file 与 label。
export interface RunItem {
  file: File
  label?: string
}

// RunnerContext 由 store 注入给 runner：onProgress 用于上报字节进度，signal 用于响应取消。
export interface RunnerContext {
  onProgress: (loaded: number, total: number) => void
  signal: AbortSignal
}

// Runner 是业务侧上传函数：调用对应 mutation hook 的 mutateAsync 并把 ctx 透传给 hook。
export type RunnerFn<T> = (item: UploadItem, file: File, ctx: RunnerContext) => Promise<T>

// RunResult 汇总会话结束时的成功 / 失败 / 取消项与 runner 返回值。
// results 与 succeeded 一一对应，只在成功路径上累加，便于业务侧拿到 mutation 结果。
export interface RunResult<T> {
  succeeded: UploadItem[]
  failed: UploadItem[]
  cancelled: UploadItem[]
  results: T[]
}

export const useUploadProgressStore = defineStore('uploadProgress', () => {
  const session = ref<UploadSession | null>(null)
  // 当前会话的 AbortController；cancel() 调用它的 abort()，单 item 结束后丢弃。
  let currentAbort: AbortController | null = null

  // isUploading 仅在仍有未结束 item 时为 true；Modal 据此决定显示「取消」还是「关闭」按钮。
  const isUploading = computed(() => {
    if (!session.value) return false
    return session.value.items.some(i => i.status === 'pending' || i.status === 'uploading')
  })

  // run 顺序执行 items，失败的 item 不阻塞后续；返回汇总结果；不主动抛错（除互斥规则）。
  async function run<T>(items: RunItem[], runner: RunnerFn<T>): Promise<RunResult<T>> {
    // 互斥：会话进行中第二次 run 抛错，业务页应 catch 并用 n-message 提示用户。
    if (session.value && isUploading.value) {
      throw new Error('已有上传任务正在进行')
    }

    const uploadItems: UploadItem[] = items.map((it, idx) => ({
      id: makeId(idx),
      label: it.label ?? it.file.name,
      size: it.file.size,
      status: 'pending',
    }))
    session.value = {
      items: uploadItems,
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }

    const results: T[] = []
    let cancelledByUser = false

    for (let i = 0; i < items.length; i++) {
      const item = uploadItems[i]
      // 用户已通过 cancel() 中断会话：把当前及后续 item 全部标 cancelled，跳过 runner。
      if (cancelledByUser) {
        item.status = 'cancelled'
        continue
      }
      session.value.currentIndex = i
      session.value.currentLoaded = 0
      item.status = 'uploading'
      currentAbort = new AbortController()
      try {
        const result = await runner(item, items[i].file, {
          onProgress: (loaded) => {
            if (session.value) session.value.currentLoaded = loaded
          },
          signal: currentAbort.signal,
        })
        item.status = 'succeeded'
        results.push(result)
      } catch (err) {
        // AbortError 来自 store.cancel() 或上游 xhrUpload 的 signal abort：标记 cancelled 并停掉后续。
        if (err instanceof Error && err.name === 'AbortError') {
          item.status = 'cancelled'
          cancelledByUser = true
        } else {
          item.status = 'failed'
          item.error = err instanceof Error ? err.message : '上传失败'
        }
      } finally {
        currentAbort = null
      }
    }

    return {
      succeeded: uploadItems.filter(i => i.status === 'succeeded'),
      failed: uploadItems.filter(i => i.status === 'failed'),
      cancelled: uploadItems.filter(i => i.status === 'cancelled'),
      results,
    }
  }

  // cancel 中断当前 runner；后续 pending item 由 run 循环检测 cancelledByUser 后跳过。
  // 不抛错、不 resolve 任何 promise；run() 自然走到循环结束并 resolve。
  function cancel(): void {
    currentAbort?.abort()
  }

  // reset 把 session 置空，让 Modal 隐藏、beforeunload 守卫解除。
  // 仅允许在 isUploading=false 时调用；进行中调用是 UI bug，但这里防御性允许（abort 一次再清）。
  function reset(): void {
    if (currentAbort) {
      currentAbort.abort()
      currentAbort = null
    }
    session.value = null
  }

  return { session, isUploading, run, cancel, reset }
})

// makeId 生成一个本地唯一 id。优先用浏览器原生 randomUUID；不可用时退回到 idx + 时间戳。
function makeId(idx: number): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${idx}`
}
```

- [ ] **Step 4: 运行测试确认全部通过**

```bash
cd web && npm run test -- src/stores/uploadProgress.spec.ts
```

预期：所有用例 PASS。

- [ ] **Step 5: 跑 typecheck**

```bash
cd web && npm run typecheck
```

- [ ] **Step 6: Commit**

```bash
git add web/src/stores/uploadProgress.ts web/src/stores/uploadProgress.spec.ts
git commit -m "$(cat <<'EOF'
feat(upload): 增加 uploadProgress Pinia store 协调上传会话

为支持三处文件上传的统一进度 UI，新增一个集中管理上传会话状态的
store：会话内串行执行 items，单文件失败不阻塞后续，取消则把当前
及后续 pending item 全部标 cancelled。

一次只允许一个会话（互斥规则），保证全局 UploadProgressModal 不被
两个并发上传争用；业务页 catch 互斥错并用 n-message 提示用户。

onProgress 由 store 注入给 runner，runner 内透传给 mutation hook，
进而透传给底层 xhrUpload；signal 同理协调取消传播链。
EOF
)"
```

---

## Task 3: 新增 `useBeforeUnloadGuard` composable + 单元测试

**Files:**
- Create: `web/src/composables/useBeforeUnloadGuard.ts`
- Create: `web/src/composables/useBeforeUnloadGuard.spec.ts`

**Why:** 上传进行中拦截浏览器刷新 / 关闭 tab，触发原生确认框；离开会话后立即解除拦截。

- [ ] **Step 1: 写 `useBeforeUnloadGuard.spec.ts` 测试**

`web/src/composables/useBeforeUnloadGuard.spec.ts`：

```typescript
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useBeforeUnloadGuard } from './useBeforeUnloadGuard'
import { useUploadProgressStore } from '@/stores/uploadProgress'

describe('useBeforeUnloadGuard', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  // mount 一个挂载 guard 的组件，通过手动派发 beforeunload 事件断言 preventDefault 是否触发。
  function mountWithGuard() {
    const Host = defineComponent({
      setup() { useBeforeUnloadGuard(); return () => h('div') },
    })
    return mount(Host)
  }

  // 上传进行中：preventDefault 与 returnValue 都被设置（浏览器据此弹原生确认框）。
  it('isUploading=true 时拦截 beforeunload', () => {
    const wrapper = mountWithGuard()
    const store = useUploadProgressStore()
    // 直接构造一个最小 session 让 isUploading=true，避免依赖 run 的完整流程。
    store.session = {
      items: [{ id: '1', label: 'a', size: 10, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    const preventSpy = vi.spyOn(event, 'preventDefault')
    window.dispatchEvent(event)
    expect(preventSpy).toHaveBeenCalled()
    expect(event.returnValue).toBe('')
    wrapper.unmount()
  })

  // 空闲：beforeunload 不被拦截，preventDefault 不触发。
  it('isUploading=false 时不拦截 beforeunload', () => {
    const wrapper = mountWithGuard()
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    const preventSpy = vi.spyOn(event, 'preventDefault')
    window.dispatchEvent(event)
    expect(preventSpy).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // 组件卸载后监听器应被移除，避免内存泄漏与跨测试污染。
  it('卸载组件后不再监听 beforeunload', () => {
    const wrapper = mountWithGuard()
    const store = useUploadProgressStore()
    store.session = {
      items: [{ id: '1', label: 'a', size: 10, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    wrapper.unmount()
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    const preventSpy = vi.spyOn(event, 'preventDefault')
    window.dispatchEvent(event)
    expect(preventSpy).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd web && npm run test -- src/composables/useBeforeUnloadGuard.spec.ts
```

- [ ] **Step 3: 实现 `useBeforeUnloadGuard.ts`**

`web/src/composables/useBeforeUnloadGuard.ts`：

```typescript
// useBeforeUnloadGuard 在上传会话进行中拦截浏览器刷新 / 关闭 tab，触发原生「确定离开？」确认框。
// 现代浏览器忽略自定义文案，但仍要求事件被 preventDefault 且 returnValue 非空。
// 该 composable 只应在 App 根挂一次，避免重复注册同一个监听器。
import { onBeforeUnmount, onMounted } from 'vue'

import { useUploadProgressStore } from '@/stores/uploadProgress'

export function useBeforeUnloadGuard(): void {
  const store = useUploadProgressStore()

  // handler 直接读 store.isUploading（reactive），不需要 watch；浏览器只在用户尝试离开时触发一次。
  function handler(event: BeforeUnloadEvent): void {
    if (!store.isUploading) return
    event.preventDefault()
    event.returnValue = ''
  }

  onMounted(() => {
    window.addEventListener('beforeunload', handler)
  })
  onBeforeUnmount(() => {
    window.removeEventListener('beforeunload', handler)
  })
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd web && npm run test -- src/composables/useBeforeUnloadGuard.spec.ts
```

- [ ] **Step 5: Commit**

```bash
git add web/src/composables/useBeforeUnloadGuard.ts web/src/composables/useBeforeUnloadGuard.spec.ts
git commit -m "$(cat <<'EOF'
feat(upload): 增加 useBeforeUnloadGuard 拦截上传中的刷新

会话进行中时拦截浏览器 beforeunload 事件，让用户看到原生「确定
离开？」确认框；上传结束或会话 reset 后自动放行。

guard 直接读 store.isUploading 派生状态，不需要 watch；该 composable
只在 App.vue 根挂一次，避免重复注册同一个监听器。
EOF
)"
```

---

## Task 4: 新增 `UploadProgressModal.vue` 组件 + 单元测试

**Files:**
- Create: `web/src/components/UploadProgressModal.vue`
- Create: `web/src/components/__tests__/UploadProgressModal.spec.ts`

**Why:** 全局唯一 Modal，订阅 store 渲染当前进度 + N/M 计数 + 取消 / 关闭按钮 + 失败汇总；会话中阻止 X 关闭与 mask 关闭，结束后允许关闭。

- [ ] **Step 1: 写 `UploadProgressModal.spec.ts` 测试**

`web/src/components/__tests__/UploadProgressModal.spec.ts`：

```typescript
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, nextTick } from 'vue'
import { beforeEach, describe, expect, it } from 'vitest'

import UploadProgressModal from '../UploadProgressModal.vue'
import { useUploadProgressStore } from '@/stores/uploadProgress'

// stub 出最小 naive-ui 组件集合：NModal 始终渲染 slot（按 show 控制可见），
// NProgress / NButton / NCollapse / NCollapseItem 渲染最小 DOM，便于断言文案与点击。
function mountModal() {
  return mount(UploadProgressModal, {
    global: {
      stubs: {
        NModal: defineComponent({
          props: ['show'],
          setup(p, { slots }) {
            return () => p.show ? h('div', { class: 'modal' }, slots.default?.()) : null
          },
        }),
        NProgress: defineComponent({
          props: ['percentage'],
          setup(p) { return () => h('div', { class: 'progress', 'data-pct': p.percentage }) },
        }),
        NButton: defineComponent({
          emits: ['click'],
          setup(_, { slots, emit }) {
            return () => h('button', { onClick: () => emit('click') }, slots.default?.())
          },
        }),
        NCollapse: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        NCollapseItem: defineComponent({
          props: ['title'],
          setup(p, { slots }) { return () => h('div', [h('span', p.title), slots.default?.()]) },
        }),
      },
    },
  })
}

describe('UploadProgressModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  // 无会话：Modal 不渲染。
  it('session=null 时不渲染', () => {
    const wrapper = mountModal()
    expect(wrapper.find('.modal').exists()).toBe(false)
  })

  // 会话进行中：渲染当前文件名 + N/M + 「取消上传」按钮。
  it('会话进行中渲染当前文件、N/M 与取消按钮', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [
        { id: '1', label: 'a.txt', size: 100, status: 'uploading' },
        { id: '2', label: 'b.txt', size: 200, status: 'pending' },
      ],
      currentIndex: 0,
      currentLoaded: 30,
      startedAt: Date.now(),
    }
    await nextTick()
    expect(wrapper.text()).toContain('a.txt')
    expect(wrapper.text()).toContain('1/2')
    expect(wrapper.find('.progress').attributes('data-pct')).toBe('30')
    expect(wrapper.text()).toContain('取消上传')
    expect(wrapper.text()).not.toContain('关闭')
  })

  // 全部 item 结束：按钮变「关闭」，汇总区显示成功 / 失败 / 取消计数。
  it('会话结束渲染关闭按钮与汇总', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [
        { id: '1', label: 'a.txt', size: 100, status: 'succeeded' },
        { id: '2', label: 'b.txt', size: 200, status: 'failed', error: 'boom' },
      ],
      currentIndex: 1,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    await nextTick()
    expect(wrapper.text()).toContain('关闭')
    expect(wrapper.text()).not.toContain('取消上传')
    expect(wrapper.text()).toContain('成功 1')
    expect(wrapper.text()).toContain('失败 1')
    expect(wrapper.text()).toContain('取消 0')
    // 失败列表里能看到文件名与错误原因。
    expect(wrapper.text()).toContain('b.txt')
    expect(wrapper.text()).toContain('boom')
  })

  // 零字节文件 guard：size=0 时 % 渲染为 100，不出现 NaN。
  it('零字节文件渲染 100% 而非 NaN', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [{ id: '1', label: 'empty.txt', size: 0, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 0,
      startedAt: Date.now(),
    }
    await nextTick()
    expect(wrapper.find('.progress').attributes('data-pct')).toBe('100')
  })

  // 点「取消上传」调用 store.cancel；点「关闭」调用 store.reset。
  it('点击按钮触发 store.cancel / store.reset', async () => {
    const wrapper = mountModal()
    const store = useUploadProgressStore()
    store.session = {
      items: [{ id: '1', label: 'a.txt', size: 100, status: 'uploading' }],
      currentIndex: 0,
      currentLoaded: 50,
      startedAt: Date.now(),
    }
    await nextTick()
    await wrapper.find('button').trigger('click')
    // 取消按钮：cancel 不会立刻把 status 翻 cancelled（那是 store.run 循环里做的）；
    // 这里仅验证 cancel 被调用即可——通过点击后 store.session 是否仍存在判断。
    expect(store.session).not.toBeNull()

    // 把状态改成已结束，再点击「关闭」应触发 reset → session 归 null。
    store.session = {
      items: [{ id: '1', label: 'a.txt', size: 100, status: 'succeeded' }],
      currentIndex: 0,
      currentLoaded: 100,
      startedAt: Date.now(),
    }
    await nextTick()
    await wrapper.find('button').trigger('click')
    expect(store.session).toBeNull()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd web && npm run test -- src/components/__tests__/UploadProgressModal.spec.ts
```

- [ ] **Step 3: 实现 `UploadProgressModal.vue`**

`web/src/components/UploadProgressModal.vue`：

```vue
<template>
  <NModal
    :show="session !== null"
    :mask-closable="false"
    :closable="!isUploading"
    preset="card"
    title="文件上传"
    style="max-width: 480px"
    @close="onClose"
  >
    <div v-if="session" style="display: grid; gap: 12px">
      <!-- 进行中：当前文件 + N/M + 字节进度 -->
      <template v-if="isUploading && currentItem">
        <div>
          <strong>{{ currentItem.label }}</strong>
          <span class="state-text" style="margin-left: 8px">
            ({{ session.currentIndex + 1 }}/{{ session.items.length }})
          </span>
        </div>
        <NProgress type="line" :percentage="currentPct" />
        <p class="state-text">
          {{ formatBytes(session.currentLoaded) }} / {{ formatBytes(currentItem.size) }}
        </p>
        <NButton type="warning" @click="store.cancel()">取消上传</NButton>
      </template>

      <!-- 全部结束：汇总 + 失败详情 + 关闭按钮 -->
      <template v-else>
        <p>
          成功 {{ counts.succeeded }} · 失败 {{ counts.failed }} · 取消 {{ counts.cancelled }}
        </p>
        <NCollapse v-if="failedItems.length">
          <NCollapseItem title="失败详情" name="failed">
            <ul style="margin: 0; padding-left: 16px">
              <li v-for="it in failedItems" :key="it.id">
                {{ it.label }}：{{ it.error }}
              </li>
            </ul>
          </NCollapseItem>
        </NCollapse>
        <NButton type="primary" @click="onClose">关闭</NButton>
      </template>
    </div>
  </NModal>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NCollapse, NCollapseItem, NModal, NProgress } from 'naive-ui'

import { useUploadProgressStore } from '@/stores/uploadProgress'

// UploadProgressModal 是全局唯一的文件上传进度反馈窗口；订阅 store 自动显示 / 隐藏。
// App.vue 根节点统一挂载，业务页面不需要自己渲染 modal。
const store = useUploadProgressStore()
const session = computed(() => store.session)
const isUploading = computed(() => store.isUploading)
const currentItem = computed(() => store.session?.items[store.session.currentIndex] ?? null)

// 字节百分比 guard：零字节文件直接 100%；编码膨胀（loaded > size）截到 100%。
const currentPct = computed(() => {
  const item = currentItem.value
  if (!item || item.size <= 0) return 100
  const loaded = store.session?.currentLoaded ?? 0
  return Math.min(Math.round((loaded / item.size) * 100), 100)
})

// failedItems 仅在会话结束后展示，供用户查看出错原因；不影响进行中视图。
const failedItems = computed(() => store.session?.items.filter(i => i.status === 'failed') ?? [])

const counts = computed(() => {
  const items = store.session?.items ?? []
  return {
    succeeded: items.filter(i => i.status === 'succeeded').length,
    failed: items.filter(i => i.status === 'failed').length,
    cancelled: items.filter(i => i.status === 'cancelled').length,
  }
})

// onClose 同时响应 NModal 的 X 按钮和「关闭」按钮：仅在非上传中时 reset；上传中由 NModal closable=false 阻止触发。
function onClose(): void {
  if (!isUploading.value) {
    store.reset()
  }
}

// formatBytes 与既有页面一致的字节格式化，避免引入 lodash 这类大依赖。
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}
</script>
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd web && npm run test -- src/components/__tests__/UploadProgressModal.spec.ts
```

- [ ] **Step 5: 跑 typecheck**

```bash
cd web && npm run typecheck
```

- [ ] **Step 6: Commit**

```bash
git add web/src/components/UploadProgressModal.vue web/src/components/__tests__/UploadProgressModal.spec.ts
git commit -m "$(cat <<'EOF'
feat(upload): 增加 UploadProgressModal 全局进度对话框

订阅 uploadProgress store 自动显示/隐藏；会话进行中展示当前文件名、
N/M 计数、字节进度条与「取消上传」按钮，禁用 X 与 mask 关闭；
全部 item 结束后切到汇总视图，展示成功/失败/取消计数与失败明细，
按钮变「关闭」。

零字节文件 guard：size=0 时百分比直接显示 100；编码膨胀（loaded
大于 size）截到 100%，避免 NaN 与超过 100% 的视觉异常。
EOF
)"
```

---

## Task 5: 在 `App.vue` 挂载 Modal + 启用守卫

**Files:**
- Modify: `web/src/App.vue`

**Why:** Modal 与 guard 都是全局单例，必须在根组件挂载才能跨页面生效。

- [ ] **Step 1: 改 `App.vue`，在 `<NMessageProvider>` 内挂 Modal + 调用 guard**

`web/src/App.vue` 完整 `<template>` 和 `<script setup>` 区域改为：

```vue
<template>
  <NConfigProvider :theme="darkTheme" :theme-overrides="themeOverrides">
    <!-- NMessageProvider 提供全局 message API，供页面通过 useMessage() 弹出操作反馈 -->
    <NMessageProvider>
      <RouterView />
      <!-- 全局上传进度对话框：订阅 uploadProgress store 自动显示 / 隐藏，
           App 根挂一次即可覆盖所有业务页面 -->
      <UploadProgressModal />
    </NMessageProvider>
  </NConfigProvider>
</template>

<script setup lang="ts">
import { darkTheme, type GlobalThemeOverrides } from 'naive-ui'
import { NConfigProvider, NMessageProvider } from 'naive-ui'

import UploadProgressModal from '@/components/UploadProgressModal.vue'
import { useBeforeUnloadGuard } from '@/composables/useBeforeUnloadGuard'

// App 是前端根组件，统一挂载全局 Naive UI 主题并把页面渲染交给路由出口。
// 这里不承载业务状态，避免根组件和页面权限、请求生命周期耦合。
// 上传相关的全局副作用（进度对话框、刷新拦截）统一在此装配，业务页面无需自行挂载。
useBeforeUnloadGuard()

const themeOverrides: GlobalThemeOverrides = {
  // ... 保持原有内容不变
}
</script>
```

> 注意：保留 `themeOverrides` 原有内容，本步骤只在 setup 顶部增加 `useBeforeUnloadGuard()` 调用、在模板里新增 `<UploadProgressModal />`、import 这两项。

- [ ] **Step 2: 运行所有现有测试，确保 App 改动不破坏其他用例**

```bash
cd web && npm run test
```

预期：所有现有用例 PASS，没有新增失败。

- [ ] **Step 3: typecheck**

```bash
cd web && npm run typecheck
```

- [ ] **Step 4: Commit**

```bash
git add web/src/App.vue
git commit -m "$(cat <<'EOF'
feat(upload): 在 App 根挂载 UploadProgressModal 与刷新守卫

全局单例：业务页面无需自行渲染 modal 或注册 beforeunload，
所有上传场景统一从 uploadProgress store 触发对话框、自动启用刷新
拦截，结束后自动解除。
EOF
)"
```

---

## Task 6: 改造知识库两个 upload mutation hook 内部走 `xhrUpload`

**Files:**
- Modify: `web/src/api/hooks/useKnowledge.ts`

**Why:** 把 `useUploadOrgKnowledge` / `useUploadAppKnowledge` 内部的 `fetch` 调用替换为 `xhrUpload`，向调用方扩 optional `onProgress` / `signal`；`onSuccess` → `onSettled` 让取消 / 失败也刷新列表。无新单测（hook 行为由后续业务页面 + xhrUpload 单测共同覆盖）。

- [ ] **Step 1: 改 `useUploadOrgKnowledge`**

替换 `web/src/api/hooks/useKnowledge.ts` 中 `useUploadOrgKnowledge` 整段为：

```typescript
// useUploadOrgKnowledge 上传组织级文件。
// 走 xhrUpload 以支持进度回调与取消信号；底层会自动注入 Bearer + CSRF，
// 与 apiRequest 等价的 401 处理也由 xhrUpload 复用。
// onSuccess 改为 onSettled：取消或失败时同样刷新当前目录，避免列表与实际状态脱节。
export function useUploadOrgKnowledge(orgId: Ref<string | undefined>, relative: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      path: string
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const params = new URLSearchParams({ path: input.path })
      await xhrUpload(`/api/v1/organizations/${orgId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value, relative.value) })
    },
  })
}
```

- [ ] **Step 2: 改 `useUploadAppKnowledge`**

替换 `useUploadAppKnowledge` 整段为：

```typescript
// useUploadAppKnowledge 上传应用级文件。
// 走 xhrUpload 以支持进度回调与取消信号；其他语义与 useUploadOrgKnowledge 一致。
// 失效 app 级前缀：兜底覆盖当前目录与可能受新增目录影响的列表。
export function useUploadAppKnowledge(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; path: string } | undefined>,
) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      path: string
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!appId.value || !context.value) throw new Error('缺少实例知识库上下文')
      const params = new URLSearchParams({
        org_id: context.value.orgId,
        owner_user_id: context.value.ownerUserId,
        path: input.path,
      })
      await xhrUpload(`/api/v1/apps/${appId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['knowledge', 'app'] })
    },
  })
}
```

- [ ] **Step 3: 调整 import**

`web/src/api/hooks/useKnowledge.ts` 顶部 import 改为：

```typescript
// 知识库 API hooks 负责组织级与应用级文件列表、上传、删除和节点同步状态。
// 上传走 xhrUpload 支持进度反馈与取消；其余 JSON 接口统一走 apiRequest。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
```

> 删除原来 import 中的 `getCsrfToken` 和 `getStoredAccessToken`（这两个调用方场景不再直接使用）。

- [ ] **Step 4: 运行知识库相关测试**

```bash
cd web && npm run test -- src/api/hooks src/pages/knowledge src/pages/apps
```

预期：现有测试 PASS（这些 hook 没有专门 spec，行为由 xhrUpload 与后续页面测试覆盖）。

- [ ] **Step 5: typecheck**

```bash
cd web && npm run typecheck
```

- [ ] **Step 6: Commit**

```bash
git add web/src/api/hooks/useKnowledge.ts
git commit -m "$(cat <<'EOF'
refactor(knowledge): 知识库上传 hook 切换到 xhrUpload

useUploadOrgKnowledge / useUploadAppKnowledge 内部 fetch 替换为
xhrUpload，扩展 mutationFn 接受 optional onProgress 与 signal 参数，
为页面层接入全局上传进度对话框打通数据通路。

onSuccess 改为 onSettled：取消或失败时也触发列表 invalidateQueries，
避免列表与实际状态脱节（用户取消后看不到行变化会误以为操作未生效）。

旧的手工 Bearer / CSRF 头注入由 xhrUpload 统一处理，调用方不再
直接接触 token / csrf cookie。
EOF
)"
```

---

## Task 7: 改造 `useUploadAssistantVersionSkill` 同样走 `xhrUpload`

**Files:**
- Modify: `web/src/api/hooks/useAssistantVersions.ts`

**Why:** assistant skill 上传也要接全局进度对话框；multipart 形态由 `xhrUpload` 一并支持。

- [ ] **Step 1: 改 `useUploadAssistantVersionSkill`**

替换 `useUploadAssistantVersionSkill` 整段为：

```typescript
// useUploadAssistantVersionSkill 上传一个 skill tar（multipart 表单字段名 file）。
// 走 xhrUpload 支持进度回调与取消信号；multipart body 由 xhrUpload 透传，不强制 Content-Type，
// 让浏览器自动设置 boundary。
// onSuccess 改为 onSettled：取消或失败时同样刷新列表，避免新建态批量上传部分失败后视图错位。
export function useUploadAssistantVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      id: string
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      const body = new FormData()
      body.append('file', input.file)
      const res = await xhrUpload(`/api/v1/assistant-versions/${input.id}/skills`, {
        method: 'POST',
        body,
        onProgress: input.onProgress,
        signal: input.signal,
      })
      // 后端响应体形如 { version: AssistantVersionDTO }；xhrUpload 已按 content-type 解析为对象。
      return (res.body as { version: AssistantVersionDTO }).version
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: VERSION_LIST_KEY })
    },
  })
}
```

- [ ] **Step 2: 调整 import**

`web/src/api/hooks/useAssistantVersions.ts` 顶部 import 改为：

```typescript
// 助手版本 API hooks：平台管理员维护版本目录（列表、详情、增删改）与 skill tar 上传。
// 写操作统一失效版本列表缓存；skill 上传走 xhrUpload 以支持进度反馈与取消。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
```

> 删除原 import 中的 `extractErrorMessage`、`getCsrfToken`、`getStoredAccessToken`（这些不再被本文件直接使用）。

- [ ] **Step 3: 运行 assistant version 相关测试**

```bash
cd web && npm run test -- src/api/hooks/useAssistantVersions.spec.ts
```

预期：PASS（既有 spec 只测 `AUXILIARY_SLOTS` / `emptyRouting`，不受影响）。

- [ ] **Step 4: typecheck**

```bash
cd web && npm run typecheck
```

- [ ] **Step 5: Commit**

```bash
git add web/src/api/hooks/useAssistantVersions.ts
git commit -m "$(cat <<'EOF'
refactor(assistant-versions): skill 上传 hook 切换到 xhrUpload

useUploadAssistantVersionSkill 内部 fetch 替换为 xhrUpload，扩展
mutationFn 接受 optional onProgress / signal 参数，为后续接入全局
上传进度对话框打通数据通路。

multipart body 由 xhrUpload 透传，不强制 Content-Type，让浏览器
自动设置 boundary；错误文案与 401 处理也复用 xhrUpload 的统一逻辑。

onSuccess 改为 onSettled：新建态批量上传部分 skill 失败时也触发
列表刷新，避免视图错位。
EOF
)"
```

---

## Task 8: 改三个业务页面调用 `store.run` + 更新现有 spec

**Files:**
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/platform/AssistantVersionsPage.vue`
- Modify: `web/src/pages/platform/AssistantVersionsPage.spec.ts`

**Why:** 业务页面不再直接 `mutateAsync`，改成 `uploadProgress.run(items, runner)`，把进度反馈交给全局 Modal；同步更新唯一会被影响的 spec。

- [ ] **Step 1: 改 `OrgKnowledgePage.vue` 的 `onUpload`**

`web/src/pages/knowledge/OrgKnowledgePage.vue`：

`<script setup>` 顶部 import 区追加：

```typescript
import { useMessage } from 'naive-ui'

import { useUploadProgressStore } from '@/stores/uploadProgress'
```

`<script setup>` 内部追加：

```typescript
const uploadProgress = useUploadProgressStore()
const message = useMessage()
```

把 `onUpload` 函数整段替换为：

```typescript
// onUpload 将文件保存到当前目录；上传进度统一由全局 UploadProgressModal 展示。
// 互斥规则：会话进行中 store.run 抛错，业务侧用 n-message 提示用户。
async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 不论是否真正发起上传，都清空 input.value 以便用户重新选择同名文件。
  input.value = ''
  if (!file) return
  const target = relativePath.value ? `${relativePath.value}/${file.name}` : file.name
  try {
    await uploadProgress.run([{ file, label: file.name }], async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        path: target,
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    // 唯一会被抛出的错误是「会话互斥」：仅此一种情况下提示用户。
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}
```

- [ ] **Step 2: 改 `AppKnowledgeTab.vue` 的 `onUploadFile`**

`web/src/pages/apps/AppKnowledgeTab.vue`：

`<script setup>` 顶部 import 区追加：

```typescript
import { useMessage } from 'naive-ui'

import { useUploadProgressStore } from '@/stores/uploadProgress'
```

`<script setup>` 内部追加（在 `errorMessage` 声明附近）：

```typescript
const uploadProgress = useUploadProgressStore()
const message = useMessage()
```

把 `onUploadFile` 函数整段替换为：

```typescript
// onUploadFile 处理原生 file input 事件；上传进度统一由全局 UploadProgressModal 展示。
// 失败 / 取消的视觉反馈也来自 Modal 汇总区，本页只承担互斥提示。
async function onUploadFile(event: Event) {
  errorMessage.value = ''
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!canManage.value) return
  if (!file) return
  try {
    await uploadProgress.run([{ file, label: file.name }], async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        path: f.name,
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}
```

> `uploading` computed 仍可保留，但语义已退化为「mutation 是否 pending」，不再用来禁用按钮（按钮的 disabled 状态可改成读 `uploadProgress.isUploading`）。为最小化改动，本步骤保留 `uploading` 不动。

- [ ] **Step 3: 改 `AssistantVersionsPage.vue` 单文件 `onSkillFileChange`**

`web/src/pages/platform/AssistantVersionsPage.vue`：

`<script setup>` 顶部 import 区追加：

```typescript
import { useMessage } from 'naive-ui'

import { useUploadProgressStore } from '@/stores/uploadProgress'
```

`<script setup>` 内部追加：

```typescript
const uploadProgress = useUploadProgressStore()
const message = useMessage()
```

把 `onSkillFileChange` 函数中编辑态分支重写（保留新建态暂存逻辑不变）：

```typescript
// onSkillFileChange 处理 skill tar 选择：编辑态版本已存在，立即上传；
// 上传进度统一由全局 UploadProgressModal 展示，按钮 loading 退化为短暂闪烁。
// 新建态版本尚未创建，先把文件暂存进 pendingSkillFiles，待保存表单时一并上传。
async function onSkillFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许重复选择同名文件
  if (!file) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  // 编辑态：版本已存在，沿用即时上传。
  if (editingId.value) {
    try {
      const result = await uploadProgress.run(
        [{ file, label: file.name }],
        async (_item, f, ctx) => {
          return uploadSkillMutation.mutateAsync({
            id: editingId.value!,
            file: f,
            onProgress: ctx.onProgress,
            signal: ctx.signal,
          })
        },
      )
      // run 不抛错；成功路径取最新 skill 列表回写本地状态。
      const updated = result.results[0]
      if (updated) {
        editingSkills.value = updated.skills
        skillFeedback.value = `已上传 skill ${file.name}`
      } else if (result.failed.length > 0) {
        skillFeedbackError.value = true
        skillFeedback.value = result.failed[0].error ?? '上传失败'
      }
    } catch (err) {
      // 唯一会被抛的错误是会话互斥：用 message 提示，不破坏本地状态。
      message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
    }
    return
  }
  // 新建态：拒绝重复添加同名文件，避免保存时对同一文件触发两次上传。
  if (pendingSkillFiles.value.some(f => f.name === file.name)) {
    skillFeedbackError.value = true
    skillFeedback.value = `已添加过同名文件 ${file.name}`
    return
  }
  pendingSkillFiles.value = [...pendingSkillFiles.value, file]
  skillFeedback.value = `已添加 skill ${file.name}，将在保存版本时上传`
}
```

- [ ] **Step 4: 改 `AssistantVersionsPage.vue` 批量 `uploadPendingSkills`**

替换 `uploadPendingSkills` 整段为：

```typescript
// uploadPendingSkills 把新建态暂存的 skill tar 通过全局 uploadProgress.run 一次性提交。
// 串行执行 + N/M 计数由 store 管理；单文件失败不阻塞后续，run 返回汇总后供调用方判断。
async function uploadPendingSkills(
  versionId: string,
): Promise<{ skills: AssistantVersionSkillDTO[]; failed: string[] }> {
  if (pendingSkillFiles.value.length === 0) {
    return { skills: [], failed: [] }
  }
  const items = pendingSkillFiles.value.map(f => ({ file: f, label: f.name }))
  try {
    const result = await uploadProgress.run(items, async (_item, f, ctx) => {
      return uploadSkillMutation.mutateAsync({
        id: versionId,
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
    // 取最后一次成功的 skill 列表作为最终视图；后端每次返回的都是完整列表，最后一次为准。
    const lastVersion = result.results[result.results.length - 1]
    const skills = lastVersion?.skills ?? []
    // failed + cancelled 都视为「未成功」，返回给调用方提示用户。
    const failed = [...result.failed, ...result.cancelled].map(it => it.label)
    return { skills, failed }
  } catch (err) {
    // 会话互斥：返回全部待传文件为 failed，让 submit 流程把表单切到编辑态并提示。
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
    return { skills: [], failed: pendingSkillFiles.value.map(f => f.name) }
  }
}
```

- [ ] **Step 5: 更新 `AssistantVersionsPage.spec.ts` — mock store**

`web/src/pages/platform/AssistantVersionsPage.spec.ts` 改动：

文件顶部 `vi.hoisted` 块追加 store mock：

```typescript
const uploadProgressRun = vi.hoisted(() => vi.fn())
```

新增 store mock（放在 `vi.mock('@/api/hooks/useOrganizations', ...)` 之后）：

```typescript
// uploadProgress store mock：默认行为是直接调用 runner 完成单文件 / 批量上传，
// 让既有用例不感知到 Modal 的存在；用例需要时可改 mockResolvedValueOnce 注入互斥错。
vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({
    run: uploadProgressRun,
    cancel: vi.fn(),
    reset: vi.fn(),
    isUploading: false,
    session: null,
  }),
}))

// useMessage 的 stub：测试里不需要弹窗，只验证调用次数即可。
vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return { ...actual, useMessage: () => ({ warning: vi.fn(), success: vi.fn(), error: vi.fn() }) }
})
```

把现有「新建版本时可暂存 skill 并在保存后上传」用例中的 `uploadSkill` 期望调整为：原来期望 `uploadSkill` 被以 `{id, file}` 调用，现在需要先让 `uploadProgressRun` 调用 runner，runner 再调用 `uploadSkill`。把 `beforeEach` 改为：

```typescript
beforeEach(() => {
  vi.clearAllMocks()
  // 默认 run 行为：顺序调用 runner，返回 { succeeded, failed:[], cancelled:[], results }。
  uploadProgressRun.mockImplementation(async (items, runner) => {
    const results: unknown[] = []
    for (const it of items) {
      // ctx.onProgress 在测试里不需要真正触发；signal 用 AbortController.signal 占位。
      const ctrl = new AbortController()
      results.push(await runner({ id: 'x', label: it.label, size: it.file.size, status: 'uploading' }, it.file, {
        onProgress: () => {},
        signal: ctrl.signal,
      }))
    }
    return { succeeded: items, failed: [], cancelled: [], results }
  })
})
```

> 单文件 / 批量已有断言（`uploadSkill` 被 mockResolvedValue / 被以 `{id: 'ver-new', file}` 调用 / 被 `toHaveBeenCalledTimes(1)`）保持不变 —— 因为 `uploadProgressRun` 的默认实现会真正调用 runner、runner 调用 `uploadSkill`。

- [ ] **Step 6: 运行受影响的全部测试**

```bash
cd web && npm run test -- src/pages/platform/AssistantVersionsPage.spec.ts src/pages/knowledge src/pages/apps
```

预期：所有用例 PASS。

- [ ] **Step 7: 跑全量测试 + typecheck 确认无回归**

```bash
cd web && npm run test && npm run typecheck
```

预期：全部 PASS。

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/knowledge/OrgKnowledgePage.vue \
        web/src/pages/apps/AppKnowledgeTab.vue \
        web/src/pages/platform/AssistantVersionsPage.vue \
        web/src/pages/platform/AssistantVersionsPage.spec.ts
git commit -m "$(cat <<'EOF'
feat(upload): 三处业务页面接入全局上传进度对话框

OrgKnowledgePage / AppKnowledgeTab / AssistantVersionsPage 三处文件
上传从直接 mutateAsync 切换到 uploadProgress.run，把进度反馈、批量
N/M 计数、取消按钮、离开警告统一交给全局 UploadProgressModal。

AssistantVersionsPage 的 uploadPendingSkills 不再手写串行循环：
store.run 内置串行执行 + 单文件失败不阻塞后续；批量上传时用户看到
统一的「正在上传 X（N/M）」对话框，并可中途取消剩余文件。

唯一会被 store.run 抛出的错误是会话互斥（已有上传任务正在进行），
统一用 n-message.warning 提示，不影响本地状态。

AssistantVersionsPage.spec.ts mock useUploadProgressStore，默认实现
顺序调用 runner 让既有用例对单文件 / 批量上传的断言无须改写。
EOF
)"
```

---

## Task 9: 浏览器手测验证清单 + 汇总归档

**Files:** 无代码改动；本任务仅在真实浏览器走 spec 中的手测清单确认行为，并按需补提交。

**Why:** 按 `AGENTS.md`「全功能浏览器验证」要求，curl 无法验证前端逻辑，必须用浏览器走一遍。

- [ ] **Step 1: 启动本地 dev server 与后端 docker-compose**

```bash
make dev   # 若有该 target；否则按 docs/local-development.md 启动 manager-api + web dev server
```

确认 web 在浏览器中可访问（默认 http://localhost:5173），用 `admin / admin123` 登录。

- [ ] **Step 2: 按下列清单逐项验证**

照 spec `docs/superpowers/specs/2026-05-25-file-upload-progress-design.md` 「浏览器手测清单」一节执行 10 条：

1. 组织知识库上传 ~200MB 文件 → Modal 出现，进度条平滑增长到 100%，关闭后列表显示新文件
2. 应用知识库同上
3. assistant skill 单独上传 → 同上
4. assistant skill 批量上传（一次创建多个 skill）→ Modal 显示「正在上传 X（2/3）—— …%」，全部跑完
5. 取消测试：上传途中点取消，Modal 切到汇总，显示 cancelled；后端日志确认连接被断
6. 网络中断测试：上传途中 DevTools Network 切 Offline，Modal 显示失败 + 错误文案
7. 离开警告：上传中按 F5 / 关 tab → 浏览器原生确认框出现；选「留下」上传继续；上传结束后再按 F5 不再提示
8. 互斥测试：上传途中再点上传按钮 → 看到 `n-message.warning`
9. 401 测试：上传途中手动清 `ocm.access_token` localStorage（让下个上传 401）→ 自动跳登录页
10. 零字节文件 → Modal 出现且立即结束，不显示 NaN%

把每条结果记在本任务的工作笔记中（不入 git）。任何一条不符合预期，**回到对应 Task 修复后重新跑测试 + 浏览器验证**，再继续。

- [ ] **Step 3: 如果手测发现需要修补的小问题，每个问题单独 commit**

例如发现 NMessageProvider 在 Modal 外导致 `useMessage` 在 Modal 上下文里失效之类的兼容性问题，定位到具体文件、修复、加测试（如可单测）、commit；commit message 用 `fix(upload): ...` 前缀。

- [ ] **Step 4: 最终确认 — 全量测试 + typecheck + build**

```bash
cd web && npm run test && npm run typecheck && npm run build
```

预期：全部 PASS，build 产物生成成功。

- [ ] **Step 5: 如果有手测补丁的 commit 之外没有遗留改动，则本任务完成；否则按需收尾**

```bash
git status --short
```

预期：工作区干净。

---

## 整体校验

- [ ] **运行最终全量验收**

```bash
cd web && npm run test && npm run typecheck && npm run build
```

预期：测试 / 类型检查 / 构建全 PASS。

- [ ] **核对所有 commit 满足项目规范**

```bash
git log --oneline master..HEAD
```

预期：每个 commit 有中文简短摘要 + 空行 + 多行正文（按 `AGENTS.md` Commit Message 规范）；不混入无关改动。
