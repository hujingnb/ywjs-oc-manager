// useKanbanEventStream.ts —— 订阅任务看板实时事件流（SSE）。
// 连接 manager 的 /hermes/kanban/events 端点，把 NDJSON 事件按 task 分发。
// 断线后以 1s / 3s / 5s 三次自动重连，超过三次放弃（由 useKanbanTasksQuery 的
// 5s 轮询兜底保证数据最终一致）。
//
// 实现说明：
// manager 的认证使用 Authorization: Bearer <token>（token 存 localStorage）。
// 标准 EventSource 无法设置自定义请求头，导致 SSE 请求被后端 RequireUserAuth
// 中间件拒绝（401）。因此改用 fetch + ReadableStream 方式，可携带 Authorization
// header，同时手动实现 SSE 文本流解析逻辑。
import { ref, watch, onUnmounted, type Ref } from 'vue'
import { getStoredAccessToken } from '@/api/client'

// KanbanStreamEvent 是从后端 SSE 收到的单条事件（NDJSON 解析后）。
// task_id 可选：hermes kanban watch 输出若无任务维度归属，事件退化为全局事件。
interface KanbanStreamEvent {
  task_id?: string
  kind?: string
  payload?: unknown
  [k: string]: unknown
}

// useKanbanEventStream 订阅 SSE 实时事件流，暴露响应式的按 task 分组事件数据。
//
// 参数：
//   appId — 实例 ID，为 undefined 时暂停订阅。
//   board — 当前 board slug，变化时重新订阅。
//
// 返回：
//   eventsByTask — taskId → 该任务的事件文本行数组（详情面板实时流用）。
//   latestEvents — taskId → 最新一条事件的简短预览文本（列表行小尾巴用）。
//   connected    — 当前 SSE 连接是否已建立且 open。
//   reconnect    — 手动重连函数，供「重连实时流」按钮调用，重置重试计数。
export function useKanbanEventStream(appId: Ref<string | undefined>, board: Ref<string>) {
  // eventsByTask：taskId → 该任务的事件文本行（最多保留最近 100 条）。
  const eventsByTask = ref<Record<string, string[]>>({})
  // latestEvents：taskId → 最新一条事件的简短预览（列表行用）。
  const latestEvents = ref<Record<string, string>>({})
  // connected：true 表示 fetch 流已建立且正在读取。
  const connected = ref(false)

  // controller 是当前活跃的 AbortController，abort() 代替 EventSource.close()。
  let controller: AbortController | null = null
  // retries 记录已尝试的断线重连次数，超过 3 次后停止自动重连。
  let retries = 0
  // retryTimer 保存待执行的重连 setTimeout 句柄，组件卸载时需清除以防游离连接。
  let retryTimer: ReturnType<typeof setTimeout> | null = null
  // aborted 标记当前 controller 是否是主动 abort（切换 board / 卸载）。
  // 主动 abort 不应触发重连逻辑。
  let aborted = false

  // describe 把事件对象转成一行可读预览文本，供 latestEvents 用。
  // payload 为对象时用 JSON.stringify，避免 String() 输出无意义的 [object Object]。
  function describe(ev: KanbanStreamEvent): string {
    const payloadText =
      typeof ev.payload === 'object' && ev.payload !== null
        ? JSON.stringify(ev.payload)
        : String(ev.payload ?? '')
    const payload = payloadText ? ' · ' + payloadText : ''
    return `${ev.kind ?? 'event'}${payload}`
  }

  // dispatchEvent 将解析后的事件分发到 eventsByTask / latestEvents 响应式数据。
  function dispatchEvent(ev: KanbanStreamEvent): void {
    const text = describe(ev)
    if (ev.task_id) {
      // 只保留最近 100 条，防止内存无限增长。
      const lines = eventsByTask.value[ev.task_id] ?? []
      // 用展开赋值触发 Vue 响应式，使 ref 内 key 变化能被侦听到。
      eventsByTask.value = {
        ...eventsByTask.value,
        [ev.task_id]: [...lines, text].slice(-100),
      }
      latestEvents.value = { ...latestEvents.value, [ev.task_id]: text }
    }
  }

  // parseSSEChunk 解析一个完整的 SSE 事件块（以 \n\n 结尾的字符串）。
  // 后端格式：普通事件 "data: <JSON>\n\n"，错误事件 "event: error\ndata: {...}\n\n"。
  // 只取 data: 开头的行、拼接后 JSON.parse，忽略 event:/comment:/id: 等其他行。
  function parseSSEChunk(chunk: string): void {
    // 收集所有 data: 行，去掉前缀后拼接成完整 JSON 字符串。
    const dataLines: string[] = []
    for (const line of chunk.split('\n')) {
      if (line.startsWith('data: ')) {
        dataLines.push(line.slice('data: '.length))
      }
    }
    if (dataLines.length === 0) return
    const json = dataLines.join('')
    try {
      const ev = JSON.parse(json) as KanbanStreamEvent
      dispatchEvent(ev)
    } catch {
      // 非 JSON 块（如 SSE 注释、心跳行）忽略，不报错。
    }
  }

  // connect 建立 fetch 流式连接，开始读取 SSE 数据。
  // 若已有连接先关闭再重建（closeSource 保证 aborted 正确设置）。
  async function connect(): Promise<void> {
    if (!appId.value) return
    closeSource()
    // 重置 aborted 标记：新连接启动，不属于主动中止。
    aborted = false
    controller = new AbortController()
    const url = `/api/v1/apps/${appId.value}/hermes/kanban/events?board=${encodeURIComponent(board.value)}`
    const token = getStoredAccessToken()

    let resp: Response
    try {
      resp = await fetch(url, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        credentials: 'include',
        signal: controller.signal,
      })
    } catch (err) {
      // AbortError 表示主动中止（closeSource / 卸载），不触发重连。
      if (aborted || (err instanceof DOMException && err.name === 'AbortError')) return
      scheduleRetry()
      return
    }

    // 响应非 2xx 或无 body（如 503 stub 实例）时，触发重连逻辑。
    if (!resp.ok || !resp.body) {
      scheduleRetry()
      return
    }

    // 连接成功，重置状态。
    connected.value = true
    retries = 0

    const reader = resp.body.getReader()
    const decoder = new TextDecoder()
    // buffer 累积从流中读到的文本，按 \n\n 切分完整 SSE 事件块。
    let buffer = ''

    // 循环读取字节流，按 SSE 协议解析事件。
    while (true) {
      let done: boolean
      let value: Uint8Array | undefined
      try {
        ;({ done, value } = await reader.read())
      } catch (err) {
        // AbortError 表示主动中止，正常退出，不触发重连。
        if (aborted || (err instanceof DOMException && err.name === 'AbortError')) break
        // 其他读取错误（网络断开等），触发重连。
        connected.value = false
        scheduleRetry()
        return
      }

      if (done) {
        // 流正常结束（服务端关闭连接），若还有未处理的 buffer 尝试解析。
        if (buffer.trim()) parseSSEChunk(buffer)
        connected.value = false
        // 非主动 abort 的流结束（服务端主动关闭），安排重连。
        if (!aborted) scheduleRetry()
        return
      }

      // 解码新到的字节追加到 buffer，按 \n\n 切分完整事件块。
      buffer += decoder.decode(value, { stream: true })
      const parts = buffer.split('\n\n')
      // 最后一部分可能是不完整的块，留在 buffer 等待后续数据。
      buffer = parts.pop() ?? ''
      for (const part of parts) {
        if (part.trim()) parseSSEChunk(part)
      }
    }
  }

  // scheduleRetry 安排三次重连（1s / 3s / 5s），超过三次后放弃。
  // 由 useKanbanTasksQuery 的 5s 轮询兜底保证数据最终一致。
  function scheduleRetry(): void {
    if (retries < 3) {
      const delay = [1000, 3000, 5000][retries] ?? 5000
      retries += 1
      retryTimer = setTimeout(() => {
        void connect()
      }, delay)
    }
  }

  // closeSource 中止当前 fetch 流，释放连接资源。
  // 同时清除待执行的重连 timer，防止组件卸载后仍触发 connect() 产生游离连接。
  // aborted 设为 true，使 connect() 读循环中的 AbortError 不触发重连。
  function closeSource() {
    aborted = true
    if (retryTimer !== null) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
    if (controller) {
      controller.abort()
      controller = null
    }
    connected.value = false
  }

  // reconnect 供用户手动点「重连实时流」按钮调用。
  // 重置重试计数器，使下次出错时重新启动三次自动重连序列。
  function reconnect() {
    retries = 0
    void connect()
  }

  // appId / board 变化时重连：appId 从 undefined 变为有效值时立即建立连接。
  // 切换到新 board/appId 时重置 retries，避免旧 board 消耗重连预算影响新订阅。
  watch([appId, board], () => {
    retries = 0
    void connect()
  }, { immediate: true })

  // 组件卸载时关闭连接，避免游离的 fetch 流泄漏。
  onUnmounted(closeSource)

  return { eventsByTask, latestEvents, connected, reconnect }
}
