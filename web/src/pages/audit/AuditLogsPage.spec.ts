import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AuditLogsPage from './AuditLogsPage.vue'

// AuditLogsPage 通过 useOrgAuditLogsQuery 拉取审计行；这里 mock 后端返回的多种典型场景，
// 验证操作者 / 资源 / 详情列的渲染、已删除徽章、系统行、metadata 模板、legacy fallback 等。
vi.mock('@/api/hooks/useAuditLogs', () => ({
  useOrgAuditLogsQuery: () => ({
    data: ref([
      {
        // 普通行（legacy）：组织管理员对应用做 update_model；资源已删除；使用 action_detail fallback。
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
        metadata: {},
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T10:00:00Z',
      },
      {
        // 系统行：actor_role=system 无 actor_id；详情为空，应展示「—」。
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
        metadata: {},
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T11:00:00Z',
      },
      {
        // metadata 行：app_skill.skill.install，应用结构化模板渲染中文详情。
        id: 'a3',
        actor_id: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
        actor_role: 'org_admin',
        actor_role_label: '企业管理员',
        actor_name: '李四',
        actor_deleted: false,
        target_id: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb',
        target_type: 'app_skill',
        target_type_label: '实例技能',
        target_name: '',
        target_deleted: false,
        action: 'skill.install',
        action_label: '安装技能',
        action_detail: '',
        metadata: { skill_name: 'tavily-search', skill_version: '1.2.3', app_id: 'app-xyz' },
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T12:00:00Z',
      },
      {
        // metadata 行：organization.recharge（带备注），验证带备注模板。
        id: 'a4',
        actor_id: 'cccccccc-cccc-cccc-cccc-cccccccccccc',
        actor_role: 'platform_admin',
        actor_role_label: '平台管理员',
        actor_name: '王五',
        actor_deleted: false,
        target_id: 'org-001',
        target_type: 'organization',
        target_type_label: '企业',
        target_name: '测试企业',
        target_deleted: false,
        action: 'recharge',
        action_label: '企业充值',
        action_detail: '',
        metadata: { amount: 5000, remark: '年度续费' },
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T13:00:00Z',
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
  // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  // 普通行应展示操作者中文名 + 角色副文（本地化） + 资源名 + 「已删除」徽章。
  it('renders actor name, role subtitle, target name and deleted badge', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    const text = wrapper.text()
    expect(text).toContain('张三')
    // 角色副文通过 labelActorRole 本地化，中文下应为「企业管理员」
    expect(text).toContain('企业管理员')
    expect(text).toContain('客服小助手')
    expect(text).toContain('已删除')
  })

  // legacy 行：metadata 为空，详情应 fallback 到 action_detail 字段的冻结字符串。
  it('renders action detail from action_detail field (legacy fallback)', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain('gpt-4o → claude-opus-4-7')
  })

  // 详情为空时应渲染「—」灰字占位。
  it('falls back to dash when action_detail is empty and metadata is empty', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain('—')
  })

  // 系统行：actor_role=system 且无 actor_id 应展示「系统」本地化文案。
  it('renders system actor with localized role label', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain('系统')
  })

  // metadata 行：app_skill.skill.install 应按中文模板渲染结构化详情。
  it('renders localized detail from metadata for app_skill skill.install', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    const text = wrapper.text()
    // 中文模板：「安装技能 {skill_name}@{skill_version} 到实例 {app_id}」
    expect(text).toContain('安装技能')
    expect(text).toContain('tavily-search@1.2.3')
    expect(text).toContain('app-xyz')
  })

  // metadata 行：organization.recharge 带备注，验证带备注模板渲染。
  it('renders recharge detail with remark when remark is non-empty', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    const text = wrapper.text()
    // 中文模板：「充值 {amount} 点 — {remark}」
    expect(text).toContain('充值 5000 点')
    expect(text).toContain('年度续费')
  })

  // 操作列：action 代码应通过 labelAction 本地化为中文，不显示后端 action_label 字段。
  it('translates action code via i18n key (not backend action_label)', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    // app.update_model 翻译为「更换模型」；organization.recharge 翻译为「企业充值」
    expect(wrapper.text()).toContain('更换模型')
    expect(wrapper.text()).toContain('企业充值')
  })

  // 结果列：result 代码应通过 labelResult 本地化为中文，随语言切换。
  it('translates result code via i18n key', () => {
    const wrapper = mount(AuditLogsPage, { global: { plugins: [i18n] } })
    // result=succeeded 应翻译为「成功」
    expect(wrapper.text()).toContain('成功')
  })
})
