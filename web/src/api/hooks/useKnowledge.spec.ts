import { describe, expect, it } from 'vitest'

import {
  KNOWLEDGE_UPLOAD_MAX_BYTES,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  isKnowledgeUploadTooLarge,
} from './useKnowledge'

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
