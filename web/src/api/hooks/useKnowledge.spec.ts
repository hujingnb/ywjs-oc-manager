import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { clearStoredTokens, setStoredTokens } from '@/api/client'
import {
  KNOWLEDGE_DEFAULT_QUOTA_BYTES,
  KNOWLEDGE_UPLOAD_MAX_BYTES,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  buildKnowledgeListQuery,
  downloadAppKnowledgeFile,
  downloadOrgKnowledgeFile,
  formatKnowledgeBytes,
  getKnowledgeUploadMaxMessage,
  isKnowledgeUploadOverRemaining,
  isKnowledgeUploadTooLarge,
  KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL,
  KNOWLEDGE_UPLOAD_ACCEPT,
  getKnowledgeUploadTypeRejectedMessage,
  isKnowledgeUploadTypeAllowed,
  normalizeKnowledgeListing,
} from './useKnowledge'
import { i18n } from '@/i18n'

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

// 切换到中文 locale，确保断言的是用户可见的中文文案。
beforeEach(() => {
  i18n.global.locale.value = 'zh'
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
  // 覆盖前端展示与本地校验共用的上限（1GB=1024MB），文案 MB 数值由字节常量换算得出，避免漂移。
  it('导出 1024MB 上限和统一提示文案', () => {
    expect(KNOWLEDGE_UPLOAD_MAX_BYTES).toBe(1024 * 1024 * 1024)
    expect(KNOWLEDGE_UPLOAD_MAX_LABEL).toBe('1024MB')
    // getKnowledgeUploadMaxMessage 通过 i18n 动态生成当前语言的提示文案。
    expect(getKnowledgeUploadMaxMessage()).toBe('单文件最大支持 1024MB')
  })

  // 覆盖边界：刚好达到上限允许上传，超过 1 字节立即拒绝。
  it('允许达到上限的文件并拒绝超过 1 字节的文件', () => {
    expect(isKnowledgeUploadTooLarge({ size: KNOWLEDGE_UPLOAD_MAX_BYTES })).toBe(false)
    expect(isKnowledgeUploadTooLarge({ size: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })).toBe(true)
  })
})

describe('知识库上传类型白名单', () => {
  // 覆盖白名单命中：常见文档 / 表格 / 图片扩展名允许上传，扩展名大小写不敏感。
  it('允许白名单内的文件类型且忽略扩展名大小写', () => {
    expect(isKnowledgeUploadTypeAllowed({ name: 'report.pdf' })).toBe(true) // 场景：常见 PDF 文档。
    expect(isKnowledgeUploadTypeAllowed({ name: 'notes.MD' })).toBe(true) // 场景：大写扩展名应归一化后命中。
    expect(isKnowledgeUploadTypeAllowed({ name: 'photo.JPEG' })).toBe(true) // 场景：图片类型且大写扩展名。
  })

  // 覆盖白名单未命中：可执行文件、无扩展名、以点结尾的文件名一律拒绝。
  it('拒绝可执行文件与无有效扩展名的文件', () => {
    expect(isKnowledgeUploadTypeAllowed({ name: 'virus.exe' })).toBe(false) // 场景：可执行文件直接拒绝。
    expect(isKnowledgeUploadTypeAllowed({ name: 'archive.zip' })).toBe(false) // 场景：压缩包不在白名单。
    expect(isKnowledgeUploadTypeAllowed({ name: 'README' })).toBe(false) // 场景：无扩展名无法判断类型。
    expect(isKnowledgeUploadTypeAllowed({ name: 'trailing.' })).toBe(false) // 场景：以点结尾扩展名为空。
  })

  // 覆盖 accept 属性与提示文案：accept 由白名单派生为带点扩展名列表，拒绝提示包含可读类型列表。
  it('导出 accept 属性并生成含类型列表的拒绝提示', () => {
    expect(KNOWLEDGE_UPLOAD_ACCEPT).toContain('.pdf') // accept 用带点扩展名引导系统文件选择器。
    expect(KNOWLEDGE_UPLOAD_ACCEPT).not.toContain('.exe') // 白名单外类型不应出现在 accept 中。
    // getKnowledgeUploadTypeRejectedMessage 通过 i18n 生成当前语言提示，并插入白名单类型列表。
    expect(getKnowledgeUploadTypeRejectedMessage()).toBe(`文件类型不支持，仅支持：${KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL}`)
  })
})

describe('知识库累计容量展示', () => {
  // 覆盖容量格式化：GB、MB 和字节按固定精度展示。
  it('格式化知识库容量字节数', () => {
    expect(formatKnowledgeBytes(1024 * 1024 * 1024)).toBe('1.00 GB')
    expect(formatKnowledgeBytes(512 * 1024 * 1024)).toBe('512.0 MB')
    expect(formatKnowledgeBytes(512)).toBe('512 B')
  })

  // 覆盖旧接口兼容：列表缺少配额字段时使用 1GB 默认上限，不能展示 NaN 或退化为无限容量。
  it('归一化缺少累计容量字段的旧知识库列表响应', () => {
    expect(normalizeKnowledgeListing({ items: [], total: 0 })).toEqual({
      items: [],
      total: 0,
      used_bytes: 0,
      quota_bytes: KNOWLEDGE_DEFAULT_QUOTA_BYTES,
      remaining_bytes: KNOWLEDGE_DEFAULT_QUOTA_BYTES,
    })
  })

  // 覆盖异常数值兼容：后端或旧缓存返回 NaN、负数时前端展示使用明确默认值。
  it('归一化异常容量数字并避免 NaN 展示', () => {
    const listing = normalizeKnowledgeListing({
      items: [],
      total: Number.NaN,
      used_bytes: Number.NaN,
      quota_bytes: -1,
      remaining_bytes: undefined,
    })

    expect(listing.total).toBe(0)
    expect(listing.quota_bytes).toBe(KNOWLEDGE_DEFAULT_QUOTA_BYTES)
    expect(formatKnowledgeBytes(Number.NaN)).toBe('0 B')
  })

  // 覆盖旧接口非空列表：缺少 used_bytes 时必须从文件大小推导已用容量，避免误当成空知识库。
  it('旧知识库列表响应缺少已用容量时按文件大小汇总', () => {
    const listing = normalizeKnowledgeListing({
      items: [
        // 第一条覆盖普通文件大小计入累计容量。
        { id: 'doc-1', name: 'a.md', size: 60, parse_status: 'completed', progress: 100, created_at: '2026-06-02T00:00:00Z' },
        // 第二条覆盖失败文件仍计入容量，直到用户删除。
        { id: 'doc-2', name: 'b.md', size: 50, parse_status: 'failed', progress: 0, created_at: '2026-06-02T00:00:00Z' },
      ],
      quota_bytes: 100,
    })

    expect(listing.used_bytes).toBe(110)
    expect(listing.remaining_bytes).toBe(0)
  })

  // 覆盖异常剩余容量：后端返回的 remaining_bytes 不能大于 quota-used，避免前端放大可上传空间。
  it('将剩余容量钳制到配额减已用容量', () => {
    const listing = normalizeKnowledgeListing({
      items: [],
      used_bytes: 60,
      quota_bytes: 100,
      remaining_bytes: 90,
    })

    expect(listing.remaining_bytes).toBe(40)
  })

  // 覆盖旧接口分页边界：缺少 used_bytes 且 total 大于当前 items 时，当前页大小不足以判断全量剩余。
  it('旧分页响应缺少已用容量时保守禁止上传', () => {
    const listing = normalizeKnowledgeListing({
      items: [
        // 当前页只有一条文件，但 total 表示还有未返回文件，不能用当前页大小估算可用空间。
        { id: 'doc-1', name: 'a.md', size: 10, parse_status: 'completed', progress: 100, created_at: '2026-06-02T00:00:00Z' },
      ],
      total: 2,
      quota_bytes: 100,
    })

    expect(listing.used_bytes).toBe(10)
    expect(listing.remaining_bytes).toBe(0)
    expect(isKnowledgeUploadOverRemaining({ size: 1 }, listing)).toBe(true)
  })

  // 覆盖剩余空间本地拦截：超过 remaining_bytes 时阻止上传。
  it('判断文件是否超过剩余空间', () => {
    expect(isKnowledgeUploadOverRemaining({ size: 11 }, { remaining_bytes: 10 })).toBe(true)
    expect(isKnowledgeUploadOverRemaining({ size: 10 }, { remaining_bytes: 10 })).toBe(false)
    expect(isKnowledgeUploadOverRemaining({ size: 10 }, null)).toBe(true)
  })
})

describe('知识库列表查询参数', () => {
  // 覆盖默认分页：页面未显式传参时仍按后端默认第一页和 50 条请求。
  it('默认请求第一页和 50 条知识库文件', () => {
    expect(buildKnowledgeListQuery({})).toEqual({
      page: 1,
      page_size: 50,
    })
  })

  // 覆盖搜索和分页组合：文件名关键词需要裁剪空白，解析状态保留给后端精确过滤。
  it('构造分页搜索查询参数', () => {
    expect(buildKnowledgeListQuery({
      page: 2,
      pageSize: 20,
      keyword: ' readme ',
      status: ' failed ',
    })).toEqual({
      page: 2,
      page_size: 20,
      keyword: 'readme',
      status: 'failed',
    })
  })

  // 覆盖异常分页输入：小于 1 的页码和页大小回退到安全默认值，避免请求无效 offset。
  it('归一化无效分页查询参数', () => {
    expect(buildKnowledgeListQuery({
      page: 0,
      pageSize: -1,
      keyword: '   ',
    })).toEqual({
      page: 1,
      page_size: 50,
    })
  })
})

describe('知识库文件下载', () => {
  // 覆盖组织知识库下载工具：document ID 进入路径，且受保护接口必须携带 Bearer token。
  it('请求企业知识库下载接口并触发浏览器下载', async () => {
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
