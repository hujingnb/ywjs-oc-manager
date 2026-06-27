// 知识库文件上传：小文件直传，大文件分片顺序上传。
//
// 背景：manager 对上传 body 限速（带宽保护），大文件单请求一次传完会超过公网入口的固定超时
// 而失败。改成把 ≥CHUNK_THRESHOLD 的文件切成小片、顺序逐片上传——每片是短请求，聚合速率仍受
// 限速约束（顺序传，不并发），既守住带宽又规避代理超时。小文件仍走原 octet 直传，零回归。
import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'

// CHUNK_THRESHOLD：达到该大小才走分片，低于则直传，避免给小文件加无谓的三次往返。
const CHUNK_THRESHOLD = 8 * 1024 * 1024
// DEFAULT_PART_SIZE：后端未返回 part_size 时的兜底分片大小（与后端默认一致，8MB）。
const DEFAULT_PART_SIZE = 8 * 1024 * 1024

// KnowledgeUploadTarget 描述一个作用域的两个上传端点：直传与分片。
export interface KnowledgeUploadTarget {
  // directPath：octet-stream 直传端点，如 /api/v1/organizations/{id}/knowledge
  directPath: string
  // uploadsPath：分片端点前缀，如 /api/v1/organizations/{id}/knowledge-uploads
  uploadsPath: string
}

// InitUploadResponse 是发起分片上传的返回。
interface InitUploadResponse {
  upload_id: string
  part_size: number
}

// uploadKnowledgeFile 按文件大小选择直传或分片上传；onProgress 上报聚合字节进度，signal 支持取消，
// onFinalizing 在字节传完、进入服务端处理阶段时触发：分片上传在 complete 前调用，直传在请求体发完、
// 等服务端响应期间调用。前端据此显示「处理中…」，避免进度卡在 100% 看起来像卡死。
export async function uploadKnowledgeFile(
  target: KnowledgeUploadTarget,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
  onFinalizing?: () => void,
): Promise<void> {
  if (file.size < CHUNK_THRESHOLD) {
    await directUpload(target.directPath, file, onProgress, signal, onFinalizing)
    return
  }
  try {
    await chunkedUpload(target.uploadsPath, file, onProgress, signal, onFinalizing)
  } catch (err) {
    // 后端未启用对象存储（分片不可用）时回退直传，保证功能可用。
    if (isMultipartUnavailable(err)) {
      await directUpload(target.directPath, file, onProgress, signal, onFinalizing)
      return
    }
    throw err
  }
}

// directUpload 以 application/octet-stream 把整个文件直传到知识库端点（原有行为）。
// onFinalizing 在请求体发送完成（字节已全部上传、等服务端把文件推给 RAGFlow）时触发，
// 前端据此从字节进度切到「处理中…」，消除卡在 100% 的错觉。
async function directUpload(
  directPath: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
  onFinalizing?: () => void,
): Promise<void> {
  const params = new URLSearchParams({ filename: file.name })
  await xhrUpload(`${directPath}?${params.toString()}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/octet-stream' },
    body: file,
    onProgress,
    signal,
    onUploadComplete: onFinalizing,
  })
}

// chunkedUpload 分片上传：init → 顺序 PUT 每片 → complete；失败/取消时尽力中止会话清理暂存。
async function chunkedUpload(
  uploadsPath: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
  onFinalizing?: () => void,
): Promise<void> {
  // 1) 发起会话：拿 uploadId 与分片大小（init 失败若为 503 多半是分片不可用，交由上层回退直传）。
  const init = await apiRequest<InitUploadResponse>(uploadsPath, {
    method: 'POST',
    body: { filename: file.name, size: file.size },
  })
  const uploadId = init.upload_id
  const partSize = init.part_size > 0 ? init.part_size : DEFAULT_PART_SIZE
  const total = file.size
  const totalParts = Math.ceil(total / partSize)

  try {
    // 2) 顺序逐片上传：不并发，保证聚合速率不超过单请求限速、守住带宽。
    for (let i = 0; i < totalParts; i++) {
      // 两片之间检查取消：xhrUpload 负责传输途中的取消，这里兜住片与片之间的取消窗口。
      if (signal?.aborted) throw abortError()
      const start = i * partSize
      const end = Math.min(start + partSize, total)
      const chunk = file.slice(start, end)
      const partNumber = i + 1
      const baseLoaded = start // 已完成分片的累计字节，作为本片进度的基线
      await xhrUpload(`${uploadsPath}/${uploadId}/parts/${partNumber}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: chunk,
        // 把单片进度叠加到已完成字节上，得到整文件的聚合进度。
        onProgress: (loaded) => onProgress?.(baseLoaded + loaded, total),
        signal,
      })
      onProgress?.(end, total)
    }
    // 3) 合并并触发解析。complete 期间服务端要把整文件从对象存储推给 RAGFlow，可能耗时若干秒，
    //    先通知前端进入「处理中」状态，避免进度卡在 100% 看起来像卡死。
    onFinalizing?.()
    await apiRequest(`${uploadsPath}/${uploadId}/complete`, { method: 'POST' })
  } catch (err) {
    // 失败或取消：尽力中止会话，让后端 Abort multipart 回收已上传分片，不阻塞错误抛出。
    void abortQuietly(`${uploadsPath}/${uploadId}`)
    throw err
  }
}

// abortQuietly best-effort 中止上传会话，吞掉自身错误（清理失败不应覆盖真正的上传错误）。
async function abortQuietly(sessionPath: string): Promise<void> {
  try {
    await apiRequest(sessionPath, { method: 'DELETE' })
  } catch {
    // 忽略：会话可能已被后端按 TTL 回收，或网络已断。
  }
}

// isMultipartUnavailable 判断错误是否为「后端未启用分片」，据此回退直传。
function isMultipartUnavailable(err: unknown): boolean {
  const e = err as { status?: number; body?: { code?: string } }
  return e?.status === 503 && e?.body?.code === 'KNOWLEDGE_MULTIPART_UNAVAILABLE'
}

// abortError 构造与 xhrUpload 一致的 AbortError，让上传 store 把它归类为「已取消」。
function abortError(): Error {
  const e = new Error('aborted') as Error & { name: string }
  e.name = 'AbortError'
  return e
}
