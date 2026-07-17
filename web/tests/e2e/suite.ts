// suite 配置集中定义 E2E 分层、并发和认证状态路径，供 Playwright 配置统一消费。
import { resolve } from 'node:path'

// E2ESuite 限定 CI 和本地允许选择的测试层级，未知值不得静默回退。
export type E2ESuite = 'quick' | 'regression' | 'slow'

// parseE2ESuite 解析外部 suite 配置；缺省执行常规回归，显式错误配置立即终止启动。
export function parseE2ESuite(value: string | undefined): E2ESuite {
  if (value === undefined) {
    return 'regression'
  }

  // 仅接受公开的三个层级，防止拼写错误意外缩小或扩大测试范围。
  if (value !== 'quick' && value !== 'regression' && value !== 'slow') {
    throw new Error(`未知 E2E suite: ${value}`)
  }

  return value
}

// resolveWorkerCount 解析并发覆盖；slow 始终串行，其余层级缺省使用两个 worker。
export function resolveWorkerCount(suite: E2ESuite, value: string | undefined): number {
  if (value !== undefined) {
    const workerCount = Number(value)

    // 外部覆盖只允许有限正整数，避免无并发执行或过量占用共享测试资源。
    if (!Number.isInteger(workerCount) || workerCount < 1 || workerCount > 4) {
      throw new Error(`E2E worker 覆盖值 ${value} 无效，只允许 1 到 4 的整数`)
    }

    // slow 用例可能操作共享状态，即使覆盖合法也不得并行。
    return suite === 'slow' ? 1 : workerCount
  }

  return suite === 'slow' ? 1 : 2
}

// suiteGrep 将 suite 映射为 Playwright 标签过滤条件，保持默认回归排除高成本用例。
export function suiteGrep(suite: E2ESuite): { grep?: RegExp; grepInvert?: RegExp } {
  if (suite === 'quick') {
    return { grep: /@quick/ }
  }
  if (suite === 'slow') {
    return { grep: /@slow/ }
  }
  return { grepInvert: /@slow/ }
}

// authStatePath 按运行批次、worker 和角色生成绝对路径，隔离并发测试的登录状态。
export function authStatePath(runID: string, workerIndex: number, role: string): string {
  return resolve('test-results', '.auth', runID, `worker-${workerIndex}-${role}.json`)
}
