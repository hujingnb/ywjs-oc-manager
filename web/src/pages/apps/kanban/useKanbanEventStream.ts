// useKanbanEventStream.ts —— 订阅任务看板实时事件流（SSE）。
// 连接 manager 的 /hermes/kanban/events 端点，把 NDJSON 事件按 task 分发。
// 断线后以 1s / 3s / 5s 三次自动重连，超过三次放弃（由 useKanbanTasksQuery 的
// 5s 轮询兜底保证数据最终一致）。
import { ref, watch, onUnmounted, type Ref } from 'vue'

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
  // connected：true 表示 SSE 连接已 open。
  const connected = ref(false)

  // source 是当前活跃的 EventSource 实例，null 表示未连接。
  let source: EventSource | null = null
  // retries 记录已尝试的断线重连次数，超过 3 次后停止自动重连。
  let retries = 0
  // retryTimer 保存待执行的重连 setTimeout 句柄，组件卸载时需清除以防游离连接。
  let retryTimer: ReturnType<typeof setTimeout> | null = null

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

  // connect 创建新的 EventSource 连接。若已有连接先关闭再重建。
  function connect() {
    if (!appId.value) return
    closeSource()
    const url = `/api/v1/apps/${appId.value}/hermes/kanban/events?board=${encodeURIComponent(board.value)}`
    source = new EventSource(url, { withCredentials: true })

    // onopen：连接建立，重置重试计数器。
    source.onopen = () => {
      connected.value = true
      retries = 0
    }

    // onmessage：接收 NDJSON 事件，按 task_id 分发到 eventsByTask / latestEvents。
    source.onmessage = (msg) => {
      try {
        const ev = JSON.parse(msg.data) as KanbanStreamEvent
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
      } catch {
        // 非 JSON 行（如 SSE 注释、心跳）忽略，不报错。
      }
    }

    // onerror：连接出错或被服务端关闭，安排三次重连（1s / 3s / 5s）。
    source.onerror = () => {
      connected.value = false
      closeSource()
      // 1s / 3s / 5s 三次重连后放弃，由 useKanbanTasksQuery 的 5s 轮询兜底。
      if (retries < 3) {
        const delay = [1000, 3000, 5000][retries] ?? 5000
        retries += 1
        retryTimer = setTimeout(connect, delay)
      }
    }
  }

  // closeSource 关闭当前 EventSource，释放连接资源。
  // 同时清除待执行的重连 timer，防止组件卸载后仍触发 connect() 产生游离连接。
  function closeSource() {
    if (retryTimer !== null) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
    if (source) {
      source.close()
      source = null
    }
  }

  // reconnect 供用户手动点「重连实时流」按钮调用。
  // 重置重试计数器，使下次出错时重新启动三次自动重连序列。
  function reconnect() {
    retries = 0
    connect()
  }

  // appId / board 变化时重连：appId 从 undefined 变为有效值时立即建立连接。
  // 切换到新 board/appId 时重置 retries，避免旧 board 消耗重连预算影响新订阅。
  watch([appId, board], () => {
    retries = 0
    connect()
  }, { immediate: true })

  // 组件卸载时关闭连接，避免游离的 EventSource 泄漏。
  onUnmounted(closeSource)

  return { eventsByTask, latestEvents, connected, reconnect }
}
