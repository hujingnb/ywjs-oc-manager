// suite 配置测试用于锁定 E2E 分层执行边界，防止标签或并发策略被静默放宽。
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  authStatePath,
  createE2ERunID,
  e2eCommandEnv,
  fixtureForWorker,
  parseE2EFixturePool,
  parseE2EFixturePoolFromOutput,
  parseE2ESuite,
  parseFixturePool,
  resolveWorkerCount,
  suiteGrep,
  type E2EFixture,
} from './suite'

// validFixture 构造与当前 Go fixture JSON 对齐的完整测试数据。
function validFixture(workerIndex = 0, runID = 'run-a'): E2EFixture {
  return {
    run_id: runID,
    worker_index: workerIndex,
    platform_admin_login: `platform-${workerIndex}`,
    platform_admin_password: 'platform-password',
    org_id: `org-id-${workerIndex}`,
    org_name: `org-${workerIndex}`,
    org_code: `org-code-${workerIndex}`,
    org_admin_login: `admin-${workerIndex}`,
    org_admin_password: 'admin-password',
    org_member_login: `member-${workerIndex}`,
    org_member_password: 'member-password',
    app_id: `app-id-${workerIndex}`,
    app_name: `app-${workerIndex}`,
  }
}

describe('E2E suite 配置契约', () => {
  // 缺少显式配置时使用 regression，保证默认执行覆盖常规回归场景。
  it('默认解析为 regression', () => {
    expect(parseE2ESuite(undefined)).toBe('regression')
  })

  // 三个公开 suite 都应保持原值，确保显式配置不会被默认值覆盖。
  it.each([
    // quick 覆盖最小冒烟套件的显式解析场景。
    { value: 'quick', expected: 'quick' as const },
    // regression 覆盖常规回归套件的显式解析场景。
    { value: 'regression', expected: 'regression' as const },
    // slow 覆盖高成本串行套件的显式解析场景。
    { value: 'slow', expected: 'slow' as const },
  ])('解析显式合法 suite $value', ({ value, expected }) => {
    expect(parseE2ESuite(value)).toBe(expected)
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

  // quick 接受并发上限 4，锁定合法边界不会被误判为资源超限。
  it('quick 接受合法的四 worker 上界覆盖', () => {
    expect(resolveWorkerCount('quick', '4')).toBe(4)
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

  // slow 的串行规则不得绕过非法值校验，错误配置仍需在启动阶段失败。
  it.each([
    // 0 覆盖 slow 下界越界场景。
    '0',
    // 5 覆盖 slow 上界越界场景。
    '5',
    // abc 覆盖 slow 非数字输入场景。
    'abc',
  ])('slow 拒绝非法 worker 覆盖 %s', (value) => {
    expect(() => resolveWorkerCount('slow', value)).toThrow('1 到 4')
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
    const expected = resolve('test-results', '.auth', 'run-a', 'worker-1-org_admin.json')

    expect(authStatePath('run-a', 1, 'org_admin')).toBe(expected)
  })

  // 合法 pool 应保留运行元数据，并按 worker_index 精确选择对应组织。
  it('解析合法 fixture pool 并选择 worker 1', () => {
    const pool = parseFixturePool<{ run_id: string; worker_index: number; org_name: string }>(JSON.stringify({
      run_id: 'run-a',
      suite: 'quick',
      fixtures: [
        { run_id: 'run-a', worker_index: 0, org_name: 'org-0' },
        { run_id: 'run-a', worker_index: 1, org_name: 'org-1' },
      ],
    }))

    expect(pool.run_id).toBe('run-a')
    expect(pool.suite).toBe('quick')
    expect(fixtureForWorker(pool, 1).org_name).toBe('org-1')
  })

  // worker 2 不存在时必须显式失败，禁止回退到 worker 0 并共享数据。
  it('拒绝选择越界 worker', () => {
    const pool = parseFixturePool(JSON.stringify({
      run_id: 'run-a',
      suite: 'regression',
      fixtures: [{ run_id: 'run-a', worker_index: 0 }],
    }))

    expect(() => fixtureForWorker(pool, 2)).toThrow('fixture pool 不包含 worker 2')
  })

  // pool 顶层结构非法时统一使用合法 pool 语义报错，避免暴露不一致的 JSON 细节。
  it.each([
    // 非法 JSON 覆盖 JSON.parse 抛错路径。
    { raw: '{invalid', scene: '非法 JSON' },
    // 缺少 run_id 覆盖运行边界缺失场景。
    { raw: JSON.stringify({ suite: 'quick', fixtures: [{ run_id: 'run-a', worker_index: 0 }] }), scene: '缺少 run_id' },
    // 非法 suite 覆盖未知测试层级场景。
    { raw: JSON.stringify({ run_id: 'run-a', suite: 'all', fixtures: [{ run_id: 'run-a', worker_index: 0 }] }), scene: '非法 suite' },
    // 空 fixtures 覆盖没有 worker 隔离数据的场景。
    { raw: JSON.stringify({ run_id: 'run-a', suite: 'quick', fixtures: [] }), scene: '空 fixtures' },
  ])('拒绝 $scene', ({ raw }) => {
    expect(() => parseFixturePool(raw)).toThrow('seed-e2e 未返回合法 fixture pool')
  })

  // 同一 worker 出现两份 fixture 时必须失败，避免并发任务静默共享或随机选中数据。
  it('拒绝重复 worker_index', () => {
    const pool = parseFixturePool(JSON.stringify({
      run_id: 'run-a',
      suite: 'quick',
      fixtures: [
        { run_id: 'run-a', worker_index: 0 },
        { run_id: 'run-a', worker_index: 0 },
      ],
    }))

    expect(() => fixtureForWorker(pool, 0)).toThrow('fixture pool 包含重复的 worker 0')
  })

  // fixture 基础边界拒绝非对象、非法索引和跨 run 数据，防止后续 schema 在错误前提上运行。
  it.each([
    // null 覆盖 fixture 不是对象的场景。
    { fixture: null, scene: 'null fixture' },
    // 负数覆盖 worker_index 小于零的场景。
    { fixture: { run_id: 'run-a', worker_index: -1 }, scene: '负 worker_index' },
    // 小数覆盖 worker_index 不是整数的场景。
    { fixture: { run_id: 'run-a', worker_index: 1.5 }, scene: '小数 worker_index' },
    // 字符串覆盖 worker_index 类型错误的场景。
    { fixture: { run_id: 'run-a', worker_index: '0' }, scene: '字符串 worker_index' },
    // 不同 run_id 覆盖 fixture 逃逸当前 pool 的场景。
    { fixture: { run_id: 'run-b', worker_index: 0 }, scene: '跨 run fixture' },
  ])('拒绝 $scene', ({ fixture }) => {
    const raw = JSON.stringify({ run_id: 'run-a', suite: 'quick', fixtures: [fixture] })

    expect(() => parseFixturePool(raw)).toThrow('seed-e2e 未返回合法 fixture pool')
  })

  // 命令环境必须清除所有历史别名，只让本轮 OCM_E2E_* 精确参数进入 make。
  it('清理冲突环境并注入本轮 E2E 命令参数', () => {
    const env = e2eCommandEnv({
      PATH: '/usr/bin',
      OCM_E2E_ACTION: 'cleanup-expired',
      OCM_E2E_RUN_ID: 'stale-ocm',
      OCM_E2E_SUITE: 'slow',
      OCM_E2E_WORKERS: '4',
      ACTION: 'cleanup-expired',
      RUN_ID: 'stale-short',
      SUITE: 'slow',
      WORKERS: '4',
      E2E_INPUT_ACTION: 'cleanup-expired',
      E2E_INPUT_RUN_ID: 'stale-input',
      E2E_INPUT_SUITE: 'slow',
      E2E_INPUT_WORKERS: '4',
      MAKEFLAGS: 'RUN_ID=stale-make',
      MAKEOVERRIDES: 'RUN_ID',
      GNUMAKEFLAGS: '--warn-undefined-variables',
      MFLAGS: '--no-print-directory',
      MAKELEVEL: '9',
    }, 'run-current', 'quick', 2, 'seed')

    expect(env).toMatchObject({
      PATH: '/usr/bin',
      OCM_E2E_ACTION: 'seed',
      OCM_E2E_RUN_ID: 'run-current',
      OCM_E2E_SUITE: 'quick',
      OCM_E2E_WORKERS: '2',
    })
    expect(env.ACTION).toBeUndefined()
    expect(env.RUN_ID).toBeUndefined()
    expect(env.SUITE).toBeUndefined()
    expect(env.WORKERS).toBeUndefined()
    expect(env.E2E_INPUT_ACTION).toBeUndefined()
    expect(env.E2E_INPUT_RUN_ID).toBeUndefined()
    expect(env.E2E_INPUT_SUITE).toBeUndefined()
    expect(env.E2E_INPUT_WORKERS).toBeUndefined()
    expect(env.MAKEFLAGS).toBeUndefined()
    expect(env.MAKEOVERRIDES).toBeUndefined()
    expect(env.GNUMAKEFLAGS).toBeUndefined()
    expect(env.MFLAGS).toBeUndefined()
    expect(env.MAKELEVEL).toBeUndefined()
  })

  // 固定六字节输入应生成 16 字符安全 run ID，避免依赖概率循环测试随机性。
  it('由固定随机字节生成安全 run ID', () => {
    const runID = createE2ERunID(Buffer.from('001122334455', 'hex'))

    expect(runID).toBe('run-001122334455')
    expect(runID).toHaveLength(16)
    expect(runID).toMatch(/^[a-z0-9-]+$/)
  })
})

describe('E2E fixture 完整 schema', () => {
  // 完整的当前 Go fixture 字段应通过 runtime 校验并保留强类型结果。
  it('接受完整 fixture pool', () => {
    const pool = parseE2EFixturePool(JSON.stringify({
      run_id: 'run-a',
      suite: 'quick',
      fixtures: [validFixture()],
    }))

    expect(pool.fixtures[0].org_name).toBe('org-0')
  })

  // 当前 Go fixture 的每个业务字符串字段都必须非空，防止 validator 集合漏掉任何字段。
  it.each([
    // 平台管理员登录名覆盖首个账号字段。
    { field: 'platform_admin_login' },
    // 平台管理员密码覆盖首个密码字段。
    { field: 'platform_admin_password' },
    // 组织 ID 覆盖数据库 UUID 字段。
    { field: 'org_id' },
    // 组织名称覆盖展示字段。
    { field: 'org_name' },
    // 组织代码覆盖登录边界字段。
    { field: 'org_code' },
    // 组织管理员登录名覆盖企业管理员账号字段。
    { field: 'org_admin_login' },
    // 组织管理员密码覆盖企业管理员密码字段。
    { field: 'org_admin_password' },
    // 普通成员登录名覆盖成员账号字段。
    { field: 'org_member_login' },
    // 普通成员密码覆盖成员密码字段。
    { field: 'org_member_password' },
    // 应用 ID 覆盖应用 UUID 字段。
    { field: 'app_id' },
    // 应用名称覆盖应用展示字段。
    { field: 'app_name' },
  ] as const)('拒绝空字段 $field', ({ field }) => {
    const fixture = { ...validFixture(), [field]: '   ' }
    const raw = JSON.stringify({ run_id: 'run-a', suite: 'quick', fixtures: [fixture] })

    expect(() => parseE2EFixturePool(raw)).toThrow('fixture 字段')
  })

  // 字段存在但类型错误也必须拒绝，避免仅靠 trim 判空导致运行时异常。
  it('拒绝非字符串业务字段', () => {
    const fixture = { ...validFixture(), app_id: 42 }
    const raw = JSON.stringify({ run_id: 'run-a', suite: 'quick', fixtures: [fixture] })

    expect(() => parseE2EFixturePool(raw)).toThrow('fixture 字段 app_id')
  })

  // stdout 末尾的伪 JSON 和别 run 合法 pool 都应跳过，继续选择当前 run 的完整 pool。
  it('从多个 JSON 候选选择当前运行的完整 pool', () => {
    const current = JSON.stringify({ run_id: 'run-a', suite: 'quick', fixtures: [validFixture()] })
    const stale = JSON.stringify({ run_id: 'run-old', suite: 'quick', fixtures: [validFixture(0, 'run-old')] })
    const stdout = `make noise\n${current}\n${stale}\n{"diagnostic":true}\nmake tail`

    const pool = parseE2EFixturePoolFromOutput(stdout, 'run-a', 'quick', 1)

    expect(pool.run_id).toBe('run-a')
    expect(pool.fixtures[0].org_name).toBe('org-0')
  })

  // 所有候选都不属于本轮时必须失败，并在诊断中保留完整 stdout。
  it('没有本轮合法 pool 时报告完整 stdout', () => {
    const stale = JSON.stringify({ run_id: 'run-old', suite: 'quick', fixtures: [validFixture(0, 'run-old')] })
    const stdout = `seed start\n${stale}\nseed end`

    expect(() => parseE2EFixturePoolFromOutput(stdout, 'run-a', 'quick', 1)).toThrow(stdout)
  })
})
