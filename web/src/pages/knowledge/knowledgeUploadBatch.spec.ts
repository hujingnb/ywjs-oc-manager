import { describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES, KNOWLEDGE_UPLOAD_MAX_MESSAGE } from '@/api/hooks/useKnowledge'
import {
  filterKnowledgeUploadFiles,
  hasKnowledgeFilesInDrag,
  knowledgeFilesFromDrop,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from './knowledgeUploadBatch'

// fileWithSize 构造指定 size 的 File，用于覆盖上传上限边界。
function fileWithSize(name: string, size: number): File {
  const file = new File(['x'], name)
  Object.defineProperty(file, 'size', { value: size, configurable: true })
  return file
}

describe('knowledgeUploadBatch', () => {
  // 覆盖 input 多选：原生 FileList 应按选择顺序转为数组。
  it('从 input 中按顺序收集多个文件', () => {
    const input = document.createElement('input')
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')
    Object.defineProperty(input, 'files', { value: [first, second], configurable: true })

    expect(knowledgeFilesFromInput(input)).toEqual([first, second])
  })

  // 覆盖拖拽文件：只收集 kind=file 且 getAsFile 成功的条目，目录或文本项会被忽略。
  it('从 drop 事件中收集文件并忽略非文件项', () => {
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')
    const event = {
      dataTransfer: {
        items: [
          { kind: 'file', getAsFile: () => first }, // 场景：普通文件进入上传队列。
          { kind: 'string', getAsFile: () => null }, // 场景：拖拽文本不应进入上传队列。
          { kind: 'file', getAsFile: () => null }, // 场景：目录项在这里表现为无法取到 File，应被忽略。
          { kind: 'file', getAsFile: () => second }, // 场景：后续普通文件保持原顺序。
        ],
      },
    } as unknown as DragEvent

    expect(knowledgeFilesFromDrop(event)).toEqual([first, second])
  })

  // 覆盖 dragover 判断：存在文件项时才标记为可上传拖拽。
  it('识别包含文件的拖拽事件', () => {
    const fileEvent = {
      dataTransfer: {
        items: [
          { kind: 'string' }, // 场景：混入文本项时不影响文件判断。
          { kind: 'file' }, // 场景：至少一个文件项即可允许页面进入拖拽态。
        ],
      },
    } as unknown as DragEvent
    const textEvent = {
      dataTransfer: {
        items: [
          { kind: 'string' }, // 场景：纯文本拖拽不应触发上传态。
        ],
      },
    } as unknown as DragEvent

    expect(hasKnowledgeFilesInDrag(fileEvent)).toBe(true)
    expect(hasKnowledgeFilesInDrag(textEvent)).toBe(false)
  })

  // 覆盖单文件上限过滤：超过上限的文件被跳过，合法文件仍继续上传。
  it('过滤超过单文件上限的文件并保留合法文件', () => {
    const warning = vi.fn()
    const ok = fileWithSize('ok.md', KNOWLEDGE_UPLOAD_MAX_BYTES)
    const tooLarge = fileWithSize('too-large.md', KNOWLEDGE_UPLOAD_MAX_BYTES + 1)

    expect(filterKnowledgeUploadFiles([tooLarge, ok], warning)).toEqual([ok])
    expect(warning).toHaveBeenCalledWith(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
  })

  // 覆盖批量 items：上传队列 label 使用文件名，File 对象必须原样传递给 XHR 上传。
  it('把文件转换为 uploadProgress items', () => {
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    expect(toKnowledgeUploadItems([first, second])).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
  })
})
