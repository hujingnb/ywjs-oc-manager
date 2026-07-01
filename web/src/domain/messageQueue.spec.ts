// messageQueue 纯逻辑单测：队列的取项、删除、失败回插、改状态、编辑写回、按会话过滤，
// 均为无副作用的数组变换，覆盖排队消息在「任务进行中入队 → 串行消费 → 失败保留 → 重试」链路上用到的全部操作。
import { describe, it, expect } from 'vitest'
import {
  nextPending,
  removeById,
  prependFailed,
  setStatus,
  applyEdit,
  forSession,
  type QueuedMessage,
} from './messageQueue'

// mk 构造一条队列项，简化用例书写；files 默认空。
function mk(id: string, sessionId: string, status: 'pending' | 'failed' = 'pending'): QueuedMessage {
  return { id, sessionId, text: id, files: [], status }
}

describe('messageQueue', () => {
  // nextPending 应返回当前会话中第一个 pending 项，跳过其它会话与 failed 项
  it('nextPending 返回当前会话首个 pending 项', () => {
    const q = [mk('a', 's2'), mk('b', 's1', 'failed'), mk('c', 's1'), mk('d', 's1')]
    expect(nextPending(q, 's1')?.id).toBe('c') // b 是 failed 跳过，c 是 s1 首个 pending
  })

  // 队列无当前会话的 pending 项时返回 undefined，用于 drainQueue 结束循环
  it('nextPending 无可发送项时返回 undefined', () => {
    const q = [mk('a', 's2'), mk('b', 's1', 'failed')]
    expect(nextPending(q, 's1')).toBeUndefined()
  })

  // removeById 按 id 移除且不改动其余项（消费开始时把项移出队列）
  it('removeById 按 id 移除项', () => {
    const q = [mk('a', 's1'), mk('b', 's1')]
    expect(removeById(q, 'a').map((x) => x.id)).toEqual(['b'])
  })

  // prependFailed 把项以 failed 放回队列头（发送失败停止并保留）
  it('prependFailed 以 failed 状态回插到队头', () => {
    const q = [mk('b', 's1')]
    const out = prependFailed(q, mk('a', 's1'))
    expect(out.map((x) => x.id)).toEqual(['a', 'b'])
    expect(out[0].status).toBe('failed')
  })

  // setStatus 把指定项改回 pending（重试）
  it('setStatus 改指定项状态', () => {
    const q = [mk('a', 's1', 'failed')]
    expect(setStatus(q, 'a', 'pending')[0].status).toBe('pending')
  })

  // applyEdit 写回文本与文件，不影响其它项（队列项编辑）
  it('applyEdit 写回文本与文件', () => {
    const f = new File(['x'], 'x.txt')
    const q = [mk('a', 's1'), mk('b', 's1')]
    const out = applyEdit(q, 'a', '改后', [f])
    expect(out[0]).toMatchObject({ id: 'a', text: '改后' })
    expect(out[0].files).toHaveLength(1)
    expect(out[1].text).toBe('b') // 其它项不变
  })

  // forSession 仅返回当前会话的项（队列面板按会话隔离渲染）
  it('forSession 按会话过滤', () => {
    const q = [mk('a', 's1'), mk('b', 's2'), mk('c', 's1')]
    expect(forSession(q, 's1').map((x) => x.id)).toEqual(['a', 'c'])
  })
})
