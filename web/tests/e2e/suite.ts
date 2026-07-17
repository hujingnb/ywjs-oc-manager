// suite 配置集中定义 E2E 分层、并发和认证状态路径，供 Playwright 配置统一消费。
import { resolve } from 'node:path'

// E2ESuite 限定 CI 和本地允许选择的测试层级，未知值不得静默回退。
export type E2ESuite = 'quick' | 'regression' | 'slow'

// e2eConflictEnvKeys 汇总 Makefile 会识别的全部参数别名，子进程环境必须先移除它们。
const e2eConflictEnvKeys = [
  'OCM_E2E_ACTION', 'OCM_E2E_RUN_ID', 'OCM_E2E_SUITE', 'OCM_E2E_WORKERS',
  'ACTION', 'RUN_ID', 'SUITE', 'WORKERS',
  'E2E_INPUT_ACTION', 'E2E_INPUT_RUN_ID', 'E2E_INPUT_SUITE', 'E2E_INPUT_WORKERS',
] as const

// e2eCommandEnv 从宿主环境构造无冲突的 make 子进程环境，并只注入当前运行的精确参数。
export function e2eCommandEnv(
  // source 是宿主进程环境，除 E2E 冲突参数外均需原样传播给 make。
  source: NodeJS.ProcessEnv,
  // runID 是当前测试批次的唯一清理边界。
  runID: string,
  // suite 是本轮 Playwright 与 seed 共用的测试层级。
  suite: E2ESuite,
  // workers 是 seed 必须创建的隔离 fixture 数量。
  workers: number,
  // action 限定当前命令只执行 seed 或精确 cleanup。
  action: 'seed' | 'cleanup',
): NodeJS.ProcessEnv {
  const env = { ...source }
  // Makefile 对 OCM、短别名和 E2E_INPUT 有优先级；必须全删后再设置，不能依赖覆盖顺序猜测。
  for (const key of e2eConflictEnvKeys) {
    delete env[key]
  }

  env.OCM_E2E_ACTION = action
  env.OCM_E2E_RUN_ID = runID
  env.OCM_E2E_SUITE = suite
  env.OCM_E2E_WORKERS = String(workers)
  return env
}

// FixturePool 是 seed-e2e 单次运行的完整输出；泛型 T 保留各业务 fixture 的字段契约。
export type FixturePool<T> = {
  // run_id 标识本轮隔离数据，后续清理必须使用同一值。
  run_id: string
  // suite 标识 fixture 对应的测试层级，禁止跨层级误用。
  suite: E2ESuite
  // fixtures 保存每个 Playwright worker 独占的数据。
  fixtures: T[]
}

// parseFixturePool 解析 seed-e2e stdout 中的候选 JSON，并统一收敛语法和顶层结构错误。
export function parseFixturePool<T>(raw: string): FixturePool<T> {
  try {
    const parsed: unknown = JSON.parse(raw)

    // 只校验通用 pool 契约；业务 fixture 字段由具体消费者和 Go seed 保持同步。
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      throw new TypeError('fixture pool 顶层必须是对象')
    }
    const candidate = parsed as Partial<FixturePool<T>>
    if (typeof candidate.run_id !== 'string' || candidate.run_id.trim() === '') {
      throw new TypeError('fixture pool 缺少 run_id')
    }
    if (candidate.suite !== 'quick' && candidate.suite !== 'regression' && candidate.suite !== 'slow') {
      throw new TypeError('fixture pool suite 非法')
    }
    if (!Array.isArray(candidate.fixtures) || candidate.fixtures.length === 0) {
      throw new TypeError('fixture pool fixtures 必须是非空数组')
    }

    return candidate as FixturePool<T>
  } catch (cause) {
    // 对外固定错误语义，cause 仅供 setup 诊断原始 JSON 或字段问题。
    throw new Error('seed-e2e 未返回合法 fixture pool', { cause })
  }
}

// fixtureForWorker 按 worker_index 唯一选择 fixture；缺失或重复都禁止回退和共享。
export function fixtureForWorker<T extends { worker_index: number }>(
  pool: FixturePool<T>,
  workerIndex: number,
): T {
  const matches = pool.fixtures.filter((fixture) => fixture.worker_index === workerIndex)
  if (matches.length === 0) {
    throw new Error(`fixture pool 不包含 worker ${workerIndex}`)
  }
  if (matches.length > 1) {
    throw new Error(`fixture pool 包含重复的 worker ${workerIndex}`)
  }

  return matches[0]
}

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
