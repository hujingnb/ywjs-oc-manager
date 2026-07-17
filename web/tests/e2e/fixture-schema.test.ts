// fixture schema 测试锁定 Go seed JSON 的完整字段契约与 stdout 多候选选择行为。
import { describe, expect, it } from 'vitest'

import {
  parseE2EFixturePool,
  parseE2EFixturePoolFromOutput,
  type E2EFixture,
} from './fixture-schema'

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
