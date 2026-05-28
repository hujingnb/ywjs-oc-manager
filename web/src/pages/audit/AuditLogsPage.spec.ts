import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AuditLogsPage from './AuditLogsPage.vue'

// AuditLogsPage 通过 useOrgAuditLogsQuery 拉取审计行；这里 mock 后端返回的四种典型场景，
// 验证操作者 / 资源 / 详情列的渲染、已删除徽章、系统行等。
vi.mock('@/api/hooks/useAuditLogs', () => ({
  useOrgAuditLogsQuery: () => ({
    data: ref([
      {
        // 普通行：企业管理员对应用做 update_model；资源已删除。
        id: 'a1',
        actor_id: '06258106-7b34-49b0-9a2b-ed13b8ba1524',
        actor_role: 'org_admin',
        actor_role_label: '企业管理员',
        actor_name: '张三',
        actor_deleted: false,
        target_id: '4eee1d51-c4c7-427c-addc-cb4a51848e4e',
        target_type: 'app',
        target_type_label: '应用实例',
        target_name: '客服小助手',
        target_deleted: true,
        action: 'update_model',
        action_label: '更换模型',
        action_detail: 'gpt-4o → claude-opus-4-7',
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T10:00:00Z',
      },
      {
        // 系统行：actor_role=system 无 actor_id；详情为空，应展示「—」。
        // actor_name 为空以反映实际后端返回的数据，renderPrincipal 会 fallback 到 actor_role_label（系统）。
        id: 'a2',
        actor_id: '',
        actor_role: 'system',
        actor_role_label: '系统',
        actor_name: '',
        actor_deleted: false,
        target_id: 'node-99',
        target_type: 'runtime_node',
        target_type_label: '运行节点',
        target_name: '上海节点',
        target_deleted: false,
        action: 'agent_enrolled',
        action_label: '节点注册',
        action_detail: '',
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T11:00:00Z',
      },
    ]),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/composables/usePlatformOrgSelection', () => ({
  usePlatformOrgSelection: () => ({
    isPlatformAdmin: ref(false),
    selectedOrgId: ref(''),
    effectiveOrgId: ref('00000000-0000-0000-0000-000000000101'),
    orgOptions: ref([]),
    organizationsLoading: ref(false),
    organizationsError: ref(null),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_admin',
    },
  }),
}))

vi.mock('@/domain/permissions', () => ({
  canViewOrgAudit: () => true,
}))

describe('AuditLogsPage', () => {
  // 普通行应展示操作者中文名 + 角色副文 + 资源名 + 「已删除」徽章。
  it('renders actor name, role subtitle, target name and deleted badge', () => {
    const wrapper = mount(AuditLogsPage)
    const text = wrapper.text()
    expect(text).toContain('张三')
    expect(text).toContain('企业管理员')
    expect(text).toContain('客服小助手')
    expect(text).toContain('已删除')
  })

  // 详情列应展示后端冻结的字符串。
  it('renders action detail from action_detail field', () => {
    const wrapper = mount(AuditLogsPage)
    expect(wrapper.text()).toContain('gpt-4o → claude-opus-4-7')
  })

  // 详情为空时应渲染「—」灰字占位。
  it('falls back to dash when action_detail is empty', () => {
    const wrapper = mount(AuditLogsPage)
    expect(wrapper.text()).toContain('—')
  })

  // 系统行：actor_role=system 且无 actor_id 应展示「系统」主文。
  it('renders system actor with role label only', () => {
    const wrapper = mount(AuditLogsPage)
    expect(wrapper.text()).toContain('系统')
  })
})
