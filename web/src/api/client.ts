// HTTP 客户端封装。
// 统一处理 base URL、Authorization 头部、JSON 解析和错误抛出，避免每个 hook 重复实现。
import { i18n } from '@/i18n'

const TOKEN_STORAGE_KEY = 'ocm.access_token'
const REFRESH_STORAGE_KEY = 'ocm.refresh_token'

// ApiError 是 apiRequest 对非 2xx 响应抛出的统一错误形态。
export interface ApiError extends Error {
  // HTTP 状态码，调用方可据此区分 403/409 等业务分支。
  status: number
  // 后端响应体原文，供高级页面读取 code/message 等结构化字段。
  body?: unknown
}

// RequestOptions 描述 apiRequest 支持的请求配置。
export interface RequestOptions {
  // HTTP 方法，缺省为 GET。
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  // JSON 请求体；非 JSON 上传不要使用 apiRequest。
  body?: unknown
  // 查询参数；undefined、null 和空字符串会被 buildUrl 忽略。
  query?: Record<string, string | number | undefined>
  /** 关闭时不附加 Authorization，例如登录接口 */
  withAuth?: boolean
  // 已认证请求可声明特定 401 业务错误码不触发全局清会话，例如当前密码错误。
  preserveAuthOnUnauthorizedCodes?: string[]
}

// AuthTokens 是前端持久化的访问令牌和刷新令牌组合。
export interface AuthTokens {
  // 短期访问令牌，用于 Authorization header。
  accessToken: string
  // 刷新/注销使用的长期令牌。
  refreshToken: string
}

export function getStoredAccessToken(): string | null {
  return readStorage(TOKEN_STORAGE_KEY)
}

// readCookie 读取 document.cookie 中指定名字的值；用于 CSRF double-submit。
// 在 SSR / 单测环境（无 document）时返回 null，避免崩溃。
function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null
  const target = `${name}=`
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim()
    if (trimmed.startsWith(target)) {
      return decodeURIComponent(trimmed.slice(target.length))
    }
  }
  return null
}

// getCsrfToken 返回 CSRF double-submit cookie 的当前值。
// 供需要绕过 apiRequest 直接 fetch 的写操作（如知识库二进制文件上传）复用，
// 确保这类请求也带上 X-CSRF-Token，否则会被后端 RequireCSRF middleware 拒绝。
export function getCsrfToken(): string | null {
  return readCookie('csrf_token')
}

export function getStoredRefreshToken(): string | null {
  return readStorage(REFRESH_STORAGE_KEY)
}

export function setStoredTokens(tokens: AuthTokens): void {
  writeStorage(TOKEN_STORAGE_KEY, tokens.accessToken)
  writeStorage(REFRESH_STORAGE_KEY, tokens.refreshToken)
}

export function clearStoredTokens(): void {
  writeStorage(TOKEN_STORAGE_KEY, null)
  writeStorage(REFRESH_STORAGE_KEY, null)
}

// 当前 locale 提供者：由 locale store 在初始化时注入，apiRequest 据此附加 Accept-Language。
// 用函数注入而非直接 import store，避免 client 与 pinia 形成循环依赖。
let currentLocaleProvider: (() => string) | null = null

// setLocaleProvider 注册 locale 读取函数；传 null 可清除（测试用）。
export function setLocaleProvider(provider: (() => string) | null): void {
  currentLocaleProvider = provider
}

// onUnauthorized 是全局 401 处理钩子（app 入口注册）。
// apiRequest 收到 401 时调一次，便于 router 跳 login + 清 token；
// 不直接在 client 引 router 是为了避免依赖循环，调用方通过 setUnauthorizedHandler 注入。
type UnauthorizedHandler = (path: string) => void

let unauthorizedHandler: UnauthorizedHandler | null = null

export function setUnauthorizedHandler(h: UnauthorizedHandler | null): void {
  unauthorizedHandler = h
}

// triggerUnauthorized 让模块外的请求工具（如 xhrUpload）也能复用 401 跳登录逻辑。
// 该函数仅转发到当前注册的 unauthorizedHandler；handler 未注册时静默无操作，与 apiRequest 行为一致。
export function triggerUnauthorized(path: string): void {
  if (unauthorizedHandler) {
    unauthorizedHandler(path)
  }
}

// apiRequest 是底层的 fetch 包装。
// 仅做 JSON 编解码和状态码映射，不处理重试和缓存——重试与缓存交给 TanStack Query。
export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
  }
  const init: RequestInit = {
    method: options.method ?? 'GET',
    headers,
  }

  if (options.withAuth !== false) {
    const token = getStoredAccessToken()
    if (token) {
      headers.Authorization = `Bearer ${token}`
    }
  }

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(options.body)
  }

  // CSRF double-submit cookie：写操作必须把 csrf_token cookie 复制到 X-CSRF-Token header。
  // 后端 RequireCSRF middleware 校验两者相等才放过；GET 不需要这个 header。
  const method = (init.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    const csrf = readCookie('csrf_token')
    if (csrf) {
      headers['X-CSRF-Token'] = csrf
    }
  }

  // 附加 Accept-Language：后端本期不消费（翻译在前端），但提前带上便于未来后端直出文案场景。
  const locale = currentLocaleProvider?.()
  if (locale) {
    headers['Accept-Language'] = locale
  }

  const url = buildUrl(path, options.query)
  const response = await fetch(url, init)
  if (response.status === 204) {
    return undefined as T
  }

  let payload: unknown
  const contentType = response.headers.get('content-type') ?? ''
  if (contentType.includes('application/json')) {
    payload = await response.json().catch(() => undefined)
  } else {
    payload = await response.text().catch(() => undefined)
  }

  if (!response.ok) {
    const error: ApiError = Object.assign(new Error(extractErrorMessage(payload, response.status)), {
      status: response.status,
      body: payload,
    })
    // 401 默认清 token 并触发跳 login：避免按钮点击后悄悄失败（mutation 错误被
    // 业务组件的 catch 吞掉，用户以为没操作）。仅当响应 code 被调用方显式列入保留
    // 清单时，才把 401 当业务校验结果处理并保留本地会话。
    const unauthorizedCode = extractErrorCode(payload)
    const preserveAuth =
      unauthorizedCode !== null &&
      options.preserveAuthOnUnauthorizedCodes?.includes(unauthorizedCode) === true
    if (response.status === 401 && options.withAuth !== false && !preserveAuth) {
      clearStoredTokens()
      if (unauthorizedHandler) {
        unauthorizedHandler(path)
      }
    }
    throw error
  }
  return payload as T
}

// apiDownload 以二进制方式 GET 一个接口并返回 Blob 与从 Content-Disposition 解析出的文件名，
// 用于 skill 归档下载等非 JSON 响应。自动附加 Authorization；401 时与 apiRequest 一致清会话并跳登录。
// 非 2xx 时按 JSON/文本解析错误体并抛出 ApiError（与 apiRequest 同形态），供调用方 toast。
export async function apiDownload(
  path: string,
  query?: RequestOptions['query'],
): Promise<{ blob: Blob; filename: string | null }> {
  const headers: Record<string, string> = {}
  const token = getStoredAccessToken()
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  const url = buildUrl(path, query)
  const response = await fetch(url, { method: 'GET', headers })
  if (!response.ok) {
    const contentType = response.headers.get('content-type') ?? ''
    const payload: unknown = contentType.includes('application/json')
      ? await response.json().catch(() => undefined)
      : await response.text().catch(() => undefined)
    const error: ApiError = Object.assign(new Error(extractErrorMessage(payload, response.status)), {
      status: response.status,
      body: payload,
    })
    // 401：与 apiRequest 一致清 token 并触发跳登录，避免下载按钮悄悄失败。
    if (response.status === 401) {
      clearStoredTokens()
      if (unauthorizedHandler) {
        unauthorizedHandler(path)
      }
    }
    throw error
  }
  const blob = await response.blob()
  return { blob, filename: parseContentDispositionFilename(response.headers.get('content-disposition')) }
}

// parseContentDispositionFilename 从 Content-Disposition 头解析 filename（支持普通 filename 与 filename*）。
// 解析不到时返回 null，由调用方回退到自定义文件名。
function parseContentDispositionFilename(header: string | null): string | null {
  if (!header) {
    return null
  }
  // 优先 RFC 5987 的 filename*（可能带 UTF-8'' 前缀），其次普通 filename。
  const star = header.match(/filename\*=(?:UTF-8'')?["']?([^"';]+)["']?/i)
  if (star?.[1]) {
    try {
      return decodeURIComponent(star[1])
    } catch {
      return star[1]
    }
  }
  const plain = header.match(/filename=["']?([^"';]+)["']?/i)
  return plain?.[1] ?? null
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  if (!query) {
    return path
  }
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') {
      continue
    }
    params.append(key, String(value))
  }
  const search = params.toString()
  return search ? `${path}?${search}` : path
}

// extractErrorMessage 从后端错误响应体里取可读文案：优先 error/message 字段，
// 取不到再回落到状态码。导出供 multipart 等绕过 apiRequest 的请求复用同一套提取逻辑。
export function extractErrorMessage(body: unknown, status: number): string {
  if (body && typeof body === 'object' && 'error' in body && typeof (body as { error: unknown }).error === 'string') {
    return (body as { error: string }).error
  }
  if (body && typeof body === 'object' && 'message' in body && typeof (body as { message: unknown }).message === 'string') {
    return (body as { message: string }).message
  }
  return i18n.global.t('common.errors.requestFailed', { status })
}

function extractErrorCode(body: unknown): string | null {
  if (body && typeof body === 'object' && 'code' in body && typeof (body as { code: unknown }).code === 'string') {
    return (body as { code: string }).code
  }
  return null
}

function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string | null): void {
  try {
    if (value === null) {
      window.localStorage.removeItem(key)
    } else {
      window.localStorage.setItem(key, value)
    }
  } catch {
    // localStorage 不可用时静默忽略，避免阻断登录流程。
  }
}
