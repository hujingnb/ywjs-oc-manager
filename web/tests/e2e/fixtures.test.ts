import { describe, expect, it } from 'vitest'

import { loadE2EFixture } from './fixtures'

describe('loadE2EFixture', () => {
  // 场景：slow suite 固定单 worker 时，可从已验证的 fixture pool 安全恢复唯一 fixture，供旧 AICC slow spec 使用。
  it('returns the only fixture in a single-worker pool', () => {
    const original = process.env.OCM_E2E_FIXTURE_POOL
    process.env.OCM_E2E_FIXTURE_POOL = JSON.stringify({
      run_id: 'run-a',
      suite: 'slow',
      fixtures: [{
        run_id: 'run-a', worker_index: 0,
        platform_admin_login: 'platform', platform_admin_password: 'password',
        org_id: 'org-id', org_name: 'org-name', org_code: 'org-code',
        org_admin_login: 'admin', org_admin_password: 'password',
        org_member_login: 'member', org_member_password: 'password',
        app_id: 'app-id', app_name: 'app-name',
      }],
    })

    expect(loadE2EFixture()).toMatchObject({ org_id: 'org-id', worker_index: 0 })
    if (original === undefined) delete process.env.OCM_E2E_FIXTURE_POOL
    else process.env.OCM_E2E_FIXTURE_POOL = original
  })
})
