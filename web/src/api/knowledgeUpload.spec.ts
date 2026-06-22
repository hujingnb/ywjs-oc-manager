import { beforeEach, describe, expect, it, vi } from 'vitest'

// 把底层依赖替换成可观察的 mock：apiRequest 处理 init/complete/abort 等 JSON 调用，
// xhrUpload 处理直传与分片 PUT。断言上传流程是否按预期切片、顺序、回退。
vi.mock('@/api/client', () => ({ apiRequest: vi.fn() }))
vi.mock('@/api/xhrUpload', () => ({ xhrUpload: vi.fn() }))

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import { uploadKnowledgeFile } from './knowledgeUpload'

const target = {
  directPath: '/api/v1/organizations/o1/knowledge',
  uploadsPath: '/api/v1/organizations/o1/knowledge-uploads',
}

// makeFile 用指定字节数造一个 File（内容全 0，仅测分片逻辑，不关心内容）。
function makeFile(size: number, name = 'doc.pdf'): File {
  return new File([new Uint8Array(size)], name, { type: 'application/pdf' })
}

describe('知识库文件上传', () => {
  beforeEach(() => {
    vi.mocked(apiRequest).mockReset()
    vi.mocked(xhrUpload).mockReset()
  })

  // 小文件（<8MB 阈值）走直传：只调一次 xhrUpload POST 到 directPath，不发起任何分片 JSON 调用。
  it('小文件走直传不分片', async () => {
    vi.mocked(xhrUpload).mockResolvedValue({ status: 202, body: {} })
    await uploadKnowledgeFile(target, makeFile(1024))
    expect(apiRequest).not.toHaveBeenCalled()
    expect(xhrUpload).toHaveBeenCalledTimes(1)
    const [url, opts] = vi.mocked(xhrUpload).mock.calls[0]
    expect(url).toContain('/api/v1/organizations/o1/knowledge?filename=')
    expect(opts.method).toBe('POST')
  })

  // 大文件分片：init → 顺序 PUT 每片（17MB÷8MB=3 片，序号 1/2/3、末片 1MB）→ complete。
  it('大文件按 8MB 顺序分片并合并', async () => {
    vi.mocked(apiRequest).mockImplementation(async (path: string) => {
      if (path.endsWith('/knowledge-uploads')) return { upload_id: 'u1', part_size: 8 * 1024 * 1024 } as never
      return {} as never // complete
    })
    vi.mocked(xhrUpload).mockResolvedValue({ status: 204, body: '' })

    await uploadKnowledgeFile(target, makeFile(17 * 1024 * 1024))

    // init + complete 各一次
    expect(apiRequest).toHaveBeenCalledWith('/api/v1/organizations/o1/knowledge-uploads', {
      method: 'POST',
      body: { filename: 'doc.pdf', size: 17 * 1024 * 1024 },
    })
    expect(apiRequest).toHaveBeenCalledWith('/api/v1/organizations/o1/knowledge-uploads/u1/complete', { method: 'POST' })
    // 三片 PUT，按序号 1/2/3，URL 正确，且最后一片大小为 1MB
    const putCalls = vi.mocked(xhrUpload).mock.calls
    expect(putCalls).toHaveLength(3)
    expect(putCalls[0][0]).toBe('/api/v1/organizations/o1/knowledge-uploads/u1/parts/1')
    expect(putCalls[1][0]).toBe('/api/v1/organizations/o1/knowledge-uploads/u1/parts/2')
    expect(putCalls[2][0]).toBe('/api/v1/organizations/o1/knowledge-uploads/u1/parts/3')
    expect(putCalls.every(([, o]) => o.method === 'PUT')).toBe(true)
    expect((putCalls[2][1].body as Blob).size).toBe(1 * 1024 * 1024)
  })

  // 分片中途某片失败：抛出错误，并尽力调用 DELETE 中止会话清理暂存。
  it('分片失败时中止会话', async () => {
    vi.mocked(apiRequest).mockImplementation(async (path: string) => {
      if (path.endsWith('/knowledge-uploads')) return { upload_id: 'u9', part_size: 8 * 1024 * 1024 } as never
      return {} as never
    })
    vi.mocked(xhrUpload).mockRejectedValue(new Error('boom'))

    await expect(uploadKnowledgeFile(target, makeFile(17 * 1024 * 1024))).rejects.toThrow('boom')
    expect(apiRequest).toHaveBeenCalledWith('/api/v1/organizations/o1/knowledge-uploads/u9', { method: 'DELETE' })
  })

  // init 返回 503 多分片不可用：回退到直传，保证功能可用。
  it('后端未启用分片时回退直传', async () => {
    vi.mocked(apiRequest).mockRejectedValue(
      Object.assign(new Error('unavailable'), { status: 503, body: { code: 'KNOWLEDGE_MULTIPART_UNAVAILABLE' } }),
    )
    vi.mocked(xhrUpload).mockResolvedValue({ status: 202, body: {} })

    await uploadKnowledgeFile(target, makeFile(17 * 1024 * 1024))

    // 直传被调用（POST 到 directPath），且未发生 PUT 分片
    const calls = vi.mocked(xhrUpload).mock.calls
    expect(calls).toHaveLength(1)
    expect(calls[0][0]).toContain('/api/v1/organizations/o1/knowledge?filename=')
    expect(calls[0][1].method).toBe('POST')
  })
})
