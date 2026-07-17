// suite 配置集中定义 E2E 分层、并发和认证状态路径，供 Playwright 配置统一消费。
import { resolve } from 'node:path'

// E2ESuite 限定 CI 和本地允许选择的测试层级，未知值不得静默回退。
export type E2ESuite = 'quick' | 'regression' | 'slow'

// e2eConflictEnvKeys 汇总 Makefile 会识别的全部参数别名，子进程环境必须先移除它们。
const e2eConflictEnvKeys = [
  'OCM_E2E_ACTION', 'OCM_E2E_RUN_ID', 'OCM_E2E_SUITE', 'OCM_E2E_WORKERS',
  'ACTION', 'RUN_ID', 'SUITE', 'WORKERS',
  'E2E_INPUT_ACTION', 'E2E_INPUT_RUN_ID', 'E2E_INPUT_SUITE', 'E2E_INPUT_WORKERS',
  'MAKEFLAGS', 'MAKEOVERRIDES', 'GNUMAKEFLAGS', 'MFLAGS', 'MAKELEVEL',
] as const

// createE2ERunID 把六字节随机源编码为 Go 允许的 16 字符安全 run ID。
export function createE2ERunID(
  // randomBytes 必须由调用方提供恰好六字节，测试可注入固定值而无需概率循环。
  randomBytes: Uint8Array,
): string {
  if (randomBytes.byteLength !== 6) {
    throw new Error('E2E run ID 随机源必须恰好为 6 字节')
  }

  return `run-${Buffer.from(randomBytes).toString('hex')}`
}

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
export type FixturePool<T extends FixtureIdentity = FixtureIdentity> = {
  // run_id 标识本轮隔离数据，后续清理必须使用同一值。
  run_id: string
  // suite 标识 fixture 对应的测试层级，禁止跨层级误用。
  suite: E2ESuite
  // fixtures 保存每个 Playwright worker 独占的数据。
  fixtures: T[]
}

// FixtureIdentity 是所有 worker fixture 必须具备的基础运行时隔离字段。
export type FixtureIdentity = {
  // run_id 必须与 pool 顶层一致，防止跨运行数据混入。
  run_id: string
  // worker_index 必须是有限非负整数，供 Playwright parallelIndex 精确选择。
  worker_index: number
}

// parseFixturePool 解析 seed-e2e stdout 中的候选 JSON，并统一收敛语法和顶层结构错误。
export function parseFixturePool<T extends FixtureIdentity = FixtureIdentity>(raw: string): FixturePool<T> {
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
    // 每项先验证跨业务通用的隔离字段，后续完整 E2EFixture 契约再收紧业务字段。
    for (const fixture of candidate.fixtures) {
      if (typeof fixture !== 'object' || fixture === null || Array.isArray(fixture)) {
        throw new TypeError('fixture 必须是对象')
      }
      const identity = fixture as Partial<FixtureIdentity>
      if (typeof identity.run_id !== 'string' || identity.run_id.trim() === '' || identity.run_id !== candidate.run_id) {
        throw new TypeError('fixture run_id 必须非空且与 pool 一致')
      }
      if (typeof identity.worker_index !== 'number'
        || !Number.isFinite(identity.worker_index)
        || !Number.isInteger(identity.worker_index)
        || identity.worker_index < 0) {
        throw new TypeError('fixture worker_index 必须是有限非负整数')
      }
    }

    return candidate as unknown as FixturePool<T>
  } catch (cause) {
    // 对外固定错误语义，cause 仅供 setup 诊断原始 JSON 或字段问题。
    throw new Error('seed-e2e 未返回合法 fixture pool', { cause })
  }
}

// fixtureForWorker 按 worker_index 唯一选择 fixture；缺失或重复都禁止回退和共享。
export function fixtureForWorker<T extends FixtureIdentity>(
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

// E2EFixture 与 cmd/seed-e2e 的 fixture struct 当前 13 个 JSON 字段逐项一致。
export type E2EFixture = {
  // run_id 标识 fixture 所属的隔离运行批次。
  run_id: string
  // worker_index 对应 Playwright parallelIndex，禁止跨 worker 共享。
  worker_index: number
  // platform_admin_login 是当前 worker 独占的平台管理员账号。
  platform_admin_login: string
  // platform_admin_password 是当前 worker 平台管理员的登录密码。
  platform_admin_password: string
  // org_id 是当前 worker 独占组织的数据库 UUID。
  org_id: string
  // org_name 是当前 worker 独占组织的展示名称。
  org_name: string
  // org_code 是组织管理员和普通成员登录时使用的企业标识。
  org_code: string
  // org_admin_login 是当前 worker 独占的组织管理员账号。
  org_admin_login: string
  // org_admin_password 是当前 worker 组织管理员的登录密码。
  org_admin_password: string
  // org_member_login 是当前 worker 独占的普通成员账号。
  org_member_login: string
  // org_member_password 是当前 worker 普通成员的登录密码。
  org_member_password: string
  // app_id 是当前 worker 预置应用的数据库 UUID。
  app_id: string
  // app_name 是当前 worker 预置应用的展示名称。
  app_name: string
}

// E2EFixtureStringField 是 Go fixture 中由完整 schema 负责的全部业务字符串字段。
type E2EFixtureStringField = Exclude<keyof E2EFixture, 'run_id' | 'worker_index'>

// e2eFixtureStringFieldSet 通过 Record 强制字段集合穷尽，interface 新增字段时编译会提醒同步 validator。
const e2eFixtureStringFieldSet = {
  platform_admin_login: true,
  platform_admin_password: true,
  org_id: true,
  org_name: true,
  org_code: true,
  org_admin_login: true,
  org_admin_password: true,
  org_member_login: true,
  org_member_password: true,
  app_id: true,
  app_name: true,
} satisfies Record<E2EFixtureStringField, true>

// e2eFixtureStringFields 提供稳定的字段遍历列表，值只来自上面的穷尽集合。
const e2eFixtureStringFields = Object.keys(e2eFixtureStringFieldSet) as E2EFixtureStringField[]

// parseE2EFixturePool 在通用隔离字段之上验证当前 Go fixture 的全部业务字段。
export function parseE2EFixturePool(raw: string): FixturePool<E2EFixture> {
  const pool = parseFixturePool<E2EFixture>(raw)

  for (const fixture of pool.fixtures) {
    // 字段必须同时满足类型与非空约束，避免登录阶段才暴露不完整 seed 输出。
    for (const field of e2eFixtureStringFields) {
      if (typeof fixture[field] !== 'string' || fixture[field].trim() === '') {
        throw new Error(`fixture 字段 ${field} 必须是非空字符串`)
      }
    }
  }

  return pool
}

// parseE2EFixturePoolFromOutput 从 stdout 尾部逐项尝试，只返回符合本轮全部边界的 pool。
export function parseE2EFixturePoolFromOutput(
  // stdout 是 make seed-e2e 的完整标准输出，仅在全部候选失败时用于诊断。
  stdout: string,
  // runID 是当前 setup 随机生成的运行边界，其他合法 run 也必须跳过。
  runID: string,
  // suite 必须与 Playwright 本轮层级一致。
  suite: E2ESuite,
  // workers 同时约束 fixture 数量与从零连续的唯一 worker_index。
  workers: number,
): FixturePool<E2EFixture> {
  const lines = stdout.trim().split(/\r?\n/).filter(Boolean)
  for (const line of [...lines].reverse()) {
    if (!line.startsWith('{')) {
      continue
    }

    try {
      const pool = parseE2EFixturePool(line)
      if (pool.run_id !== runID || pool.suite !== suite || pool.fixtures.length !== workers) {
        continue
      }
      // 数量相等仍可能重复或缺号，逐个唯一选择才能证明 worker 映射完整。
      for (let workerIndex = 0; workerIndex < workers; workerIndex += 1) {
        fixtureForWorker(pool, workerIndex)
      }
      return pool
    } catch {
      // 单条伪 JSON 或 schema 不完整属于 make 噪声，继续检查更早的候选行。
    }
  }

  throw new Error(`seed-e2e 未找到本轮合法 fixture pool；完整输出：\n${stdout}`)
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
