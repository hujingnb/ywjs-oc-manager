import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AppAuditTab from './AppAuditTab.vue'

// AppAuditTab 渲染单个应用的审计行；mock useTargetAuditLogsQuery 返回普通行、已删除 actor 行、
// 以及含 metadata 的结构化详情行，验证本地化翻译与 legacy fallback 均正常工作。
vi.mock('@/api/hooks/useAuditLogs', () => ({
  useTargetAuditLogsQuery: () => ({
    data: ref([
      {
        // legacy 行：成员对应用做了渠道绑定；action_detail 已有中文字符串，作为旧行 fallback 验证。
        id: 'b1',
        actor_id: '00000000-0000-0000-0000-000000000201',
        actor_role: 'org_member',
        actor_role_label: '企业成员',
        actor_name: '李四',
        actor_deleted: false,
        target_id: '00000000-0000-0000-0000-000000000001',
        target_type: 'app',
        target_type_label: '应用实例',
        target_name: '测试实例',
        target_deleted: false,
        action: 'channel_bound',
        action_label: '绑定渠道',
        action_detail: '渠道 微信，身份 18601000000',
        metadata: {},
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T10:00:00Z',
      },
      {
        // 已删除 actor 行：成员被下线后审计行仍展示其姓名 + 「已删除」徽章。
        id: 'b2',
        actor_id: '00000000-0000-0000-0000-000000000202',
        actor_role: 'org_member',
        actor_role_label: '企业成员',
        actor_name: '已下线成员',
        actor_deleted: true,
        target_id: '00000000-0000-0000-0000-000000000001',
        target_type: 'app',
        target_type_label: '应用实例',
        target_name: '测试实例',
        target_deleted: false,
        action: 'update_model',
        action_label: '更换模型',
        action_detail: '',
        metadata: {},
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-17T10:00:00Z',
      },
      {
        // metadata 行：user.delete_member，验证结构化详情模板按 cascade_count 渲染。
        id: 'b3',
        actor_id: '00000000-0000-0000-0000-000000000203',
        actor_role: 'org_admin',
        actor_role_label: '企业管理员',
        actor_name: '管理员',
        actor_deleted: false,
        target_id: '00000000-0000-0000-0000-000000000301',
        target_type: 'user',
        target_type_label: '成员用户',
        target_name: '被移除用户',
        target_deleted: false,
        action: 'delete_member',
        action_label: '移除成员',
        action_detail: '',
        metadata: { cascade_count: 3 },
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-17T09:00:00Z',
      },
    ]),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: '00000000-0000-0000-0000-000000000201',
      org_id: '00000000-0000-0000-0000-000000000101',
      role: 'org_member',
    },
  }),
}))

vi.mock('@/domain/permissions', () => ({
  canViewOwnAppAudit: () => true,
}))

describe('AppAuditTab', () => {
  beforeEach(() => {
    // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
    i18n.global.locale.value = 'zh'
  })

  // 普通行：actor_name + 副文角色（本地化）+ legacy action_detail 字符串。
  it('renders actor name, localized role subtitle and legacy detail string', () => {
    const wrapper = mount(AppAuditTab, { props: { appId: '00000000-0000-0000-0000-000000000001' }, global: { plugins: [i18n] } })
    const text = wrapper.text()
    expect(text).toContain('李四')
    // 角色通过 labelActorRole 本地化，不依赖后端 actor_role_label 字段
    expect(text).toContain('企业成员')
    // legacy 行 fallback 到 action_detail 冻结字符串
    expect(text).toContain('渠道 微信，身份 18601000000')
  })

  // 已删除行：actor_deleted=true 时显示「已删除」徽章。
  it('renders deleted badge for soft-deleted actor', () => {
    const wrapper = mount(AppAuditTab, { props: { appId: '00000000-0000-0000-0000-000000000001' }, global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain('已下线成员')
    expect(wrapper.text()).toContain('已删除')
  })

  // 空 action_detail 且无 metadata 时应展示「—」。
  it('falls back to dash when action_detail is empty and metadata is empty', () => {
    const wrapper = mount(AppAuditTab, { props: { appId: '00000000-0000-0000-0000-000000000001' }, global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain('—')
  })

  // metadata 行：user.delete_member 应按中文模板渲染，展示级联删除数量。
  it('renders localized detail from metadata for user.delete_member', () => {
    const wrapper = mount(AppAuditTab, { props: { appId: '00000000-0000-0000-0000-000000000001' }, global: { plugins: [i18n] } })
    // 中文模板：「级联删除 {cascade_count} 个应用」
    expect(wrapper.text()).toContain('级联删除 3 个应用')
  })

  // 操作列：action 代码应通过 labelAction 本地化，而非直接使用后端 action_label 字段。
  it('translates action code via i18n key', () => {
    const wrapper = mount(AppAuditTab, { props: { appId: '00000000-0000-0000-0000-000000000001' }, global: { plugins: [i18n] } })
    // app.update_model → 「更换模型」；user.delete_member → 「移除成员」
    expect(wrapper.text()).toContain('更换模型')
    expect(wrapper.text()).toContain('移除成员')
  })
})
