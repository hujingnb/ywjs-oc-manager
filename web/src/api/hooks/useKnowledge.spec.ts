import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { clearStoredTokens, setStoredTokens } from '@/api/client'
import {
  KNOWLEDGE_UPLOAD_MAX_BYTES,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  downloadAppKnowledgeFile,
  downloadOrgKnowledgeFile,
  isKnowledgeUploadTooLarge,
} from './useKnowledge'

let clickSpy: ReturnType<typeof vi.spyOn>
const originalCreateObjectURLDescriptor = Object.getOwnPropertyDescriptor(URL, 'createObjectURL')
const originalRevokeObjectURLDescriptor = Object.getOwnPropertyDescriptor(URL, 'revokeObjectURL')

// restoreURLDescriptor 恢复测试前的 URL 全局方法状态，避免手动 defineProperty 污染后续用例。
function restoreURLDescriptor(name: 'createObjectURL' | 'revokeObjectURL', descriptor: PropertyDescriptor | undefined) {
  if (descriptor) {
    Object.defineProperty(URL, name, descriptor)
    return
  }
  Reflect.deleteProperty(URL, name)
}

beforeEach(() => {
  setStoredTokens({ accessToken: 'access-1', refreshToken: 'refresh-1' })
  Object.defineProperty(URL, 'createObjectURL', {
    value: vi.fn(() => 'blob:knowledge'),
    configurable: true,
  })
  Object.defineProperty(URL, 'revokeObjectURL', {
    value: vi.fn(),
    configurable: true,
  })
  clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
})

afterEach(() => {
  clearStoredTokens()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  restoreURLDescriptor('createObjectURL', originalCreateObjectURLDescriptor)
  restoreURLDescriptor('revokeObjectURL', originalRevokeObjectURLDescriptor)
})

describe('知识库上传大小限制', () => {
  // 覆盖前端展示与本地校验共用的 100MB 限制，避免页面文案和判断条件漂移。
  it('导出 100MB 上限和统一提示文案', () => {
    expect(KNOWLEDGE_UPLOAD_MAX_BYTES).toBe(100 * 1024 * 1024)
    expect(KNOWLEDGE_UPLOAD_MAX_LABEL).toBe('100MB')
    expect(KNOWLEDGE_UPLOAD_MAX_MESSAGE).toBe('单文件最多支持 100MB')
  })

  // 覆盖边界：刚好 100MB 允许上传，超过 1 字节立即拒绝。
  it('允许 100MB 文件并拒绝超过 1 字节的文件', () => {
    expect(isKnowledgeUploadTooLarge({ size: KNOWLEDGE_UPLOAD_MAX_BYTES })).toBe(false)
    expect(isKnowledgeUploadTooLarge({ size: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })).toBe(true)
  })
})

describe('知识库文件下载', () => {
  // 覆盖组织知识库下载工具：document ID 进入路径，且受保护接口必须携带 Bearer token。
  it('请求组织知识库下载接口并触发浏览器下载', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Blob(['hello']), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await downloadOrgKnowledgeFile('org-1', 'doc-1', 'read me.md')

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/organizations/org-1/knowledge/doc-1/file', {
      headers: { Authorization: 'Bearer access-1' },
    })
    expect(clickSpy).toHaveBeenCalledTimes(1)
  })

  // 覆盖实例知识库下载工具：实例 ID 与 document ID 共同定位应用级知识库文件。
  it('请求实例知识库下载接口并触发浏览器下载', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Blob(['app']), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await downloadAppKnowledgeFile('app-1', 'doc-app-1', 'app.md')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/apps/app-1/knowledge/doc-app-1/file',
      { headers: { Authorization: 'Bearer access-1' } },
    )
    expect(clickSpy).toHaveBeenCalledTimes(1)
  })

  // 覆盖浏览器下载触发失败的异常路径：即使 click 抛错，也必须释放 object URL 并移除临时 a 标签。
  it('浏览器下载点击失败时仍清理临时资源并继续抛出错误', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Blob(['hello']), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    clickSpy.mockImplementationOnce(() => {
      throw new Error('click failed')
    })

    await expect(downloadOrgKnowledgeFile('org-1', 'doc-1', 'read me.md')).rejects.toThrow('click failed')

    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:knowledge')
    expect(document.body.querySelector('a[download="read me.md"]')).toBeNull()
  })

  // 覆盖 JSON 错误响应：知识库下载接口应复用统一错误提取逻辑，优先展示后端 message 字段。
  it('下载接口返回 JSON 错误时展示后端错误文案', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: 'KNOWLEDGE_FORBIDDEN', message: '无权访问该知识库' }), {
        status: 403,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    try {
      await downloadOrgKnowledgeFile('org-1', 'doc-secret', 'secret.md')
      throw new Error('expected download to fail')
    } catch (error) {
      expect((error as Error).message).toBe('无权访问该知识库')
    }
  })
})
