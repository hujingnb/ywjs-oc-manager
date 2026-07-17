// suite 配置测试用于锁定 E2E 分层执行边界，防止标签或并发策略被静默放宽。
import { describe, expect, it } from 'vitest'

import { authStatePath, parseE2ESuite, resolveWorkerCount, suiteGrep } from './suite'

describe('E2E suite 配置契约', () => {
  // 缺少显式配置时使用 regression，保证默认执行覆盖常规回归场景。
  it('默认解析为 regression', () => {
    expect(parseE2ESuite(undefined)).toBe('regression')
  })

  // 未知 suite 必须立即失败，避免拼写错误导致错误范围的测试被执行。
  it('拒绝未知 suite', () => {
    expect(() => parseE2ESuite('all')).toThrow('未知 E2E suite: all')
  })

  // quick 与 regression 缺省保持两个 worker，在执行速度和本地资源占用间取固定平衡。
  it('quick 与 regression 默认使用两个 worker', () => {
    expect(resolveWorkerCount('quick', undefined)).toBe(2)
    expect(resolveWorkerCount('regression', undefined)).toBe(2)
  })

  // slow 场景即使收到并发覆盖也必须串行，避免共享环境状态互相干扰。
  it('slow 忽略合法覆盖并固定使用一个 worker', () => {
    expect(resolveWorkerCount('slow', '4')).toBe(1)
  })

  // regression 允许在资源受限环境中显式降为单 worker。
  it('regression 接受合法的单 worker 覆盖', () => {
    expect(resolveWorkerCount('regression', '1')).toBe(1)
  })

  // 非法 worker 覆盖应携带约束范围，便于 CI 配置错误快速定位。
  it.each([
    // 0 覆盖下界越界场景。
    '0',
    // 5 覆盖上界越界场景。
    '5',
    // abc 覆盖非数字输入场景。
    'abc',
  ])('拒绝非法 worker 覆盖 %s', (value) => {
    expect(() => resolveWorkerCount('quick', value)).toThrow('1 到 4')
  })

  // quick 只选择明确标记的快速用例，作为最小冒烟范围。
  it('quick 仅匹配 @quick 标签', () => {
    expect(suiteGrep('quick')).toEqual({ grep: /@quick/ })
  })

  // regression 排除 slow 标签，避免默认回归包含高成本场景。
  it('regression 排除 @slow 标签', () => {
    expect(suiteGrep('regression')).toEqual({ grepInvert: /@slow/ })
  })

  // slow 只选择慢速标签，以便独立串行调度高成本场景。
  it('slow 仅匹配 @slow 标签', () => {
    expect(suiteGrep('slow')).toEqual({ grep: /@slow/ })
  })

  // 认证状态按 run、worker 与角色隔离，避免并发任务复用登录态文件。
  it('生成隔离到 run 和 worker 的认证状态路径', () => {
    expect(authStatePath('run-a', 1, 'org_admin')).toContain('run-a/worker-1-org_admin.json')
  })
})
