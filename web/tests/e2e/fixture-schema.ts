// fixture schema 集中维护 cmd/seed-e2e 当前 JSON 契约，避免 global setup 与 worker 重复校验字段。
import {
  fixtureForWorker,
  parseFixturePool,
  type E2ESuite,
  type FixturePool,
} from './suite'

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

// E2EFixtureStringField 是 Go fixture 中由业务 schema 负责的全部字符串字段。
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
