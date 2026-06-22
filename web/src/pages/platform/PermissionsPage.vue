<script setup lang="ts">
// PermissionsPage 展示平台权限矩阵，仅平台管理员可访问。
// 内容为静态数据，不调用任何 API；与 docs/superpowers/specs/2026-05-22-permission-refactor-design.md 保持一致。
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

interface PermRow {
  // op 是操作名称。
  op: string
  // admin / orgAdmin / member 分别是三角色的权限描述。
  admin: string
  orgAdmin: string
  member: string
}

interface PermSection {
  title: string
  rows: PermRow[]
}

// sections 包含全量权限矩阵，按功能模块分组；转为 computed 以响应语言切换。
const sections = computed<PermSection[]>(() => {
  // 权限值常量：各角色完整可操作、无权限和有条件的标记
  const FULL = '✅'
  const NONE = '❌'
  const ORG = t('platform.permissions.condOrg')
  const ORG_ALL = t('platform.permissions.condOrgAll')
  const SELF = t('platform.permissions.condSelf')

  return [
    {
      title: t('platform.permissions.sections.orgMgmt'),
      rows: [
        { op: t('platform.permissions.ops.createOrg'), admin: FULL, orgAdmin: NONE, member: NONE },
        { op: t('platform.permissions.ops.listOrgs'), admin: FULL, orgAdmin: NONE, member: NONE },
        { op: t('platform.permissions.ops.viewOrgDetail'), admin: FULL, orgAdmin: ORG, member: ORG },
        { op: t('platform.permissions.ops.editOrgInfo'), admin: FULL, orgAdmin: NONE, member: NONE },
        { op: t('platform.permissions.ops.toggleOrg'), admin: FULL, orgAdmin: NONE, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.memberMgmt'),
      rows: [
        { op: t('platform.permissions.ops.listMembers'), admin: FULL, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.viewMemberDetail'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.createMember'), admin: NONE, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.editMember'), admin: SELF, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.toggleMember'), admin: NONE, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.deleteMember'), admin: NONE, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.resetPassword'), admin: NONE, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.onboard'), admin: NONE, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.rebuildInstance'), admin: FULL, orgAdmin: ORG, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.appInstance'),
      rows: [
        { op: t('platform.permissions.ops.listApps'), admin: FULL, orgAdmin: ORG_ALL, member: SELF },
        { op: t('platform.permissions.ops.viewAppDetail'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.switchVersion'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.runtime'),
      rows: [
        { op: t('platform.permissions.ops.startStopRestart'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.channel'),
      rows: [
        { op: t('platform.permissions.ops.viewChannel'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.bindChannel'), admin: NONE, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.knowledge'),
      rows: [
        { op: t('platform.permissions.ops.readOrgKnowledge'), admin: FULL, orgAdmin: ORG, member: ORG },
        { op: t('platform.permissions.ops.writeOrgKnowledge'), admin: NONE, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.readAppKnowledge'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.writeAppKnowledge'), admin: NONE, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.version'),
      rows: [
        { op: t('platform.permissions.ops.viewVersions'), admin: FULL, orgAdmin: FULL, member: FULL },
        { op: t('platform.permissions.ops.manageVersions'), admin: FULL, orgAdmin: NONE, member: NONE },
        { op: t('platform.permissions.ops.uploadSkill'), admin: FULL, orgAdmin: NONE, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.kanban'),
      rows: [
        { op: t('platform.permissions.ops.viewKanban'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.writeKanban'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.cron'),
      rows: [
        { op: t('platform.permissions.ops.viewCron'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.manageCron'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.usage'),
      rows: [
        { op: t('platform.permissions.ops.viewOrgUsage'), admin: FULL, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.viewMemberUsage'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.viewAppUsage'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.audit'),
      rows: [
        { op: t('platform.permissions.ops.viewOrgAudit'), admin: FULL, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.viewAppAudit'), admin: FULL, orgAdmin: ORG, member: SELF },
        { op: t('platform.permissions.ops.viewMyAudit'), admin: FULL, orgAdmin: FULL, member: FULL },
      ],
    },
    {
      title: t('platform.permissions.sections.recharge'),
      rows: [
        { op: t('platform.permissions.ops.viewRecharge'), admin: FULL, orgAdmin: ORG, member: NONE },
        { op: t('platform.permissions.ops.viewBalance'), admin: FULL, orgAdmin: ORG, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.node'),
      rows: [
        { op: t('platform.permissions.ops.viewNodes'), admin: FULL, orgAdmin: NONE, member: NONE },
        { op: t('platform.permissions.ops.toggleNode'), admin: FULL, orgAdmin: NONE, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.overview'),
      rows: [
        { op: t('platform.permissions.ops.viewOverview'), admin: FULL, orgAdmin: NONE, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.models'),
      rows: [
        { op: t('platform.permissions.ops.viewModels'), admin: FULL, orgAdmin: NONE, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.jobs'),
      rows: [
        { op: t('platform.permissions.ops.viewJobs'), admin: FULL, orgAdmin: NONE, member: NONE },
      ],
    },
    {
      title: t('platform.permissions.sections.workspace'),
      rows: [
        { op: t('platform.permissions.ops.workspaceAccess'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
    {
      title: t('platform.permissions.sections.metrics'),
      rows: [
        { op: t('platform.permissions.ops.viewMetrics'), admin: FULL, orgAdmin: ORG, member: SELF },
      ],
    },
  ]
})
</script>

<template>
  <div style="padding: 24px; max-width: 900px;">
    <n-h2 style="margin-bottom: 4px;">{{ t('platform.permissions.title') }}</n-h2>
    <n-p depth="3" style="margin-bottom: 24px;">{{ t('platform.permissions.subtitle') }}</n-p>

    <!-- 图例 -->
    <n-space style="margin-bottom: 24px;">
      <n-tag type="success" :bordered="false">{{ t('platform.permissions.legendFull') }}</n-tag>
      <n-tag type="error" :bordered="false">{{ t('platform.permissions.legendNone') }}</n-tag>
      <n-tag type="warning" :bordered="false">{{ t('platform.permissions.legendCond') }}</n-tag>
    </n-space>

    <!-- 每个功能模块一个表格 -->
    <div
      v-for="section in sections"
      :key="section.title"
      style="margin-bottom: 32px;"
    >
      <n-h4 style="margin-bottom: 8px;">{{ section.title }}</n-h4>
      <n-table size="small" :bordered="true" :single-line="false">
        <thead>
          <tr>
            <th style="width: 40%;">{{ t('platform.permissions.tableOp') }}</th>
            <th style="width: 20%; text-align: center;">{{ t('platform.permissions.tableAdmin') }}</th>
            <th style="width: 20%; text-align: center;">{{ t('platform.permissions.tableOrgAdmin') }}</th>
            <th style="width: 20%; text-align: center;">{{ t('platform.permissions.tableMember') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in section.rows" :key="row.op">
            <td>{{ row.op }}</td>
            <td style="text-align: center;">{{ row.admin }}</td>
            <td style="text-align: center;">{{ row.orgAdmin }}</td>
            <td style="text-align: center;">{{ row.member }}</td>
          </tr>
        </tbody>
      </n-table>
    </div>
  </div>
</template>
