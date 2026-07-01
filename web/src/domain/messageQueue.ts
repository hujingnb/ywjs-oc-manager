// messageQueue 收敛「实例会话页任务进行中消息排队」的纯数据操作，与组件的 Vue 响应式状态解耦，
// 便于单测。所有函数无副作用：接收队列数组、返回新数组或查询结果，不修改入参。
// 串行消费编排（调用 chatStream、开关 sending）留在组件，因其依赖运行时副作用不宜在此实现。

// QueueStatus 队列项状态：pending 待发送；failed 发送失败、停留队列供重试。
export type QueueStatus = 'pending' | 'failed'

// QueuedMessage 一条排队消息。
export interface QueuedMessage {
  // id 本地生成的唯一标识，用于 v-for key、编辑与删除定位。
  id: string
  // sessionId 该消息归属的会话 id，支持切会话时按会话隔离。
  sessionId: string
  // text 文本内容。
  text: string
  // files 用户已选文件，暂存内存，轮到发送时才上传。
  files: File[]
  // status 见 QueueStatus。
  status: QueueStatus
}

// nextPending 返回指定会话中第一个 pending 项，供 drainQueue 逐条取用；无则 undefined。
export function nextPending(queue: QueuedMessage[], sessionId: string): QueuedMessage | undefined {
  return queue.find((q) => q.sessionId === sessionId && q.status === 'pending')
}

// removeById 按 id 移除项，返回新数组（消费开始时把项移出队列，使其变为真实消息气泡）。
export function removeById(queue: QueuedMessage[], id: string): QueuedMessage[] {
  return queue.filter((q) => q.id !== id)
}

// prependFailed 把项以 failed 状态放回队头，返回新数组（发送失败：停止并保留失败项）。
export function prependFailed(queue: QueuedMessage[], item: QueuedMessage): QueuedMessage[] {
  return [{ ...item, status: 'failed' }, ...queue]
}

// setStatus 把指定项改为目标状态，返回新数组（重试时把 failed 改回 pending）。
export function setStatus(queue: QueuedMessage[], id: string, status: QueueStatus): QueuedMessage[] {
  return queue.map((q) => (q.id === id ? { ...q, status } : q))
}

// applyEdit 写回指定项的文本与文件，返回新数组（队列项内联编辑保存）。
export function applyEdit(queue: QueuedMessage[], id: string, text: string, files: File[]): QueuedMessage[] {
  return queue.map((q) => (q.id === id ? { ...q, text, files } : q))
}

// forSession 返回指定会话的全部项，保持原顺序（队列面板按当前会话渲染）。
export function forSession(queue: QueuedMessage[], sessionId: string): QueuedMessage[] {
  return queue.filter((q) => q.sessionId === sessionId)
}
