// knowledgeUploadBatch 提供知识库多文件上传的前端编排 helper。
// 后端仍是单文件接口，这里只负责把 input/drop 事件转成 uploadProgress 可消费的队列。
import {
  KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  getKnowledgeUploadMaxMessage,
  getKnowledgeUploadTypeRejectedMessage,
  isKnowledgeUploadTooLarge,
  isKnowledgeUploadTypeAllowed,
} from '@/api/hooks/useKnowledge'
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

// filterKnowledgeUploadFiles 做类型白名单与单文件上限两层前端拦截；容量不足等动态条件交给后端逐个判断。
// 类型拦截先于上限判断：既过滤掉 exe 等无法解析且有安全风险的文件，也避免超限提示误导用户以为是大小问题。
export function filterKnowledgeUploadFiles(files: File[], warning: WarningFn): File[] {
  const accepted: File[] = []
  let typeRejectedCount = 0
  let tooLargeCount = 0
  for (const file of files) {
    // 类型不在白名单（如 exe）直接拒绝，不再判断大小。
    if (!isKnowledgeUploadTypeAllowed(file)) {
      typeRejectedCount += 1
      continue
    }
    if (isKnowledgeUploadTooLarge(file)) {
      tooLargeCount += 1
      continue
    }
    accepted.push(file)
  }
  // 类型不支持与超限分别提示，保证用户能区分被拒的真实原因。
  if (typeRejectedCount === 1) {
    // 单文件类型不支持：展示允许的类型列表。
    warning(getKnowledgeUploadTypeRejectedMessage())
  } else if (typeRejectedCount > 1) {
    // 多文件批量存在不支持类型：说明跳过数量并附上允许的类型列表。
    warning(i18n.global.t('knowledge.messages.uploadSkipTypeMultiple', { count: typeRejectedCount, label: KNOWLEDGE_ALLOWED_EXTENSIONS_LABEL }))
  }
  if (tooLargeCount === 1) {
    // 单文件超限：直接展示上限提示。
    warning(getKnowledgeUploadMaxMessage())
  } else if (tooLargeCount > 1) {
    // 多文件批量超限：说明跳过数量并附上单文件上限提示。
    warning(i18n.global.t('knowledge.messages.uploadSkipMultiple', { count: tooLargeCount, label: KNOWLEDGE_UPLOAD_MAX_LABEL }))
  }
  return accepted
}

// toKnowledgeUploadItems 将 File[] 转成全局上传进度 store 的最小队列结构。
export function toKnowledgeUploadItems(files: File[]): RunItem[] {
  return files.map(file => ({ file, label: file.name }))
}
