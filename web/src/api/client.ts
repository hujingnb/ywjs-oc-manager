// HTTP 客户端封装。
// 统一处理 base URL、Authorization 头部、JSON 解析和错误抛出，避免每个 hook 重复实现。

const TOKEN_STORAGE_KEY = 'ocm.access_token'
const REFRESH_STORAGE_KEY = 'ocm.refresh_token'

export interface ApiError extends Error {
  status: number
  body?: unknown
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  body?: unknown
  query?: Record<string, string | number | undefined>
  /** 关闭时不附加 Authorization，例如登录接口 */
  withAuth?: boolean
}

export interface AuthTokens {
  accessToken: string
  refreshToken: string
}

export function getStoredAccessToken(): string | null {
  return readStorage(TOKEN_STORAGE_KEY)
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
    throw error
  }
  return payload as T
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

function extractErrorMessage(body: unknown, status: number): string {
  if (body && typeof body === 'object' && 'error' in body && typeof (body as { error: unknown }).error === 'string') {
    return (body as { error: string }).error
  }
  return `请求失败 (${status})`
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
