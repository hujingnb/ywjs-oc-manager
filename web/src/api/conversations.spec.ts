import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/xhrUpload', () => ({ xhrUpload: vi.fn() }))
import { xhrUpload } from '@/api/xhrUpload'
import { uploadConversationFile, conversationFileDownloadUrl } from './conversations'

// 上传：调 xhrUpload 到 files 端点（octet-stream，带 filename query），返回 file_id 元数据。
describe('uploadConversationFile', () => {
  it('上传到 files 端点并返回元数据', async () => {
    vi.mocked(xhrUpload).mockResolvedValue({ status: 200, body: { file_id: 'f1', filename: 'a.pdf', mime: 'application/pdf', size: 3 } })
    const res = await uploadConversationFile('app1', 's1', new File(['abc'], 'a.pdf'))
    expect(res.file_id).toBe('f1')
    const [url, opts] = vi.mocked(xhrUpload).mock.calls[0]
    expect(url).toContain('/api/v1/apps/app1/hermes/conversations/s1/files?filename=')
    expect(opts.method).toBe('POST')
  })
})

// 下载 URL 拼装正确（前端用 <a href> / <img src> 指向它）。
describe('conversationFileDownloadUrl', () => {
  it('拼出下载端点', () => {
    expect(conversationFileDownloadUrl('app1', 's1', 'f1'))
      .toBe('/api/v1/apps/app1/hermes/conversations/s1/files/f1')
  })
})
