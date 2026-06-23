// knowledgeUploadBatch 提供知识库多文件上传的前端编排 helper。
// 后端仍是单文件接口，这里只负责把 input/drop 事件转成 uploadProgress 可消费的队列。
import { KNOWLEDGE_UPLOAD_MAX_LABEL, getKnowledgeUploadMaxMessage, isKnowledgeUploadTooLarge } from '@/api/hooks/useKnowledge'
import { i18n } from '@/i18n'
import type { RunItem } from '@/stores/uploadProgress'

type WarningFn = (message: string) => void

// knowledgeFilesFromInput 将原生 file input 的 FileList 转为数组，保留浏览器选择顺序。
export function knowledgeFilesFromInput(input: HTMLInputElement): File[] {
  return Array.from(input.files ?? [])
}

// knowledgeFilesFromDrop 从拖拽事件中收集文件；目录和文本项不会返回 File，因此会被过滤。
export function knowledgeFilesFromDrop(event: DragEvent): File[] {
  const transfer = event.dataTransfer
  if (!transfer) return []
  if (transfer.items && transfer.items.length > 0) {
    return Array.from(transfer.items)
      .filter(item => item.kind === 'file')
      .map(item => item.getAsFile())
      .filter((file): file is File => file instanceof File)
  }
  return Array.from(transfer.files ?? [])
}

// hasKnowledgeFilesInDrag 用于 dragenter/dragover 判断是否需要进入可上传视觉态。
export function hasKnowledgeFilesInDrag(event: DragEvent): boolean {
  const transfer = event.dataTransfer
  if (!transfer) return false
  if (transfer.items && transfer.items.length > 0) {
    return Array.from(transfer.items).some(item => item.kind === 'file')
  }
  return (transfer.files?.length ?? 0) > 0
}

// filterKnowledgeUploadFiles 只做单文件上限拦截；容量不足等动态条件交给后端逐个判断。
export function filterKnowledgeUploadFiles(files: File[], warning: WarningFn): File[] {
  const accepted: File[] = []
  let rejectedCount = 0
  for (const file of files) {
    if (isKnowledgeUploadTooLarge(file)) {
      rejectedCount += 1
      continue
    }
    accepted.push(file)
  }
  if (rejectedCount === 1) {
    // 单文件超限：直接展示上限提示。
    warning(getKnowledgeUploadMaxMessage())
  } else if (rejectedCount > 1) {
    // 多文件批量超限：说明跳过数量并附上单文件上限提示。
    warning(i18n.global.t('knowledge.messages.uploadSkipMultiple', { count: rejectedCount, label: KNOWLEDGE_UPLOAD_MAX_LABEL }))
  }
  return accepted
}

// toKnowledgeUploadItems 将 File[] 转成全局上传进度 store 的最小队列结构。
export function toKnowledgeUploadItems(files: File[]): RunItem[] {
  return files.map(file => ({ file, label: file.name }))
}
