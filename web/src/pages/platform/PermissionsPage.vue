<script setup lang="ts">
// PermissionsPage 展示平台权限矩阵，仅平台管理员可访问。
// 内容为静态数据，不调用任何 API；与 docs/superpowers/specs/2026-05-22-permission-refactor-design.md 保持一致。

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

// sections 包含全量权限矩阵，按功能模块分组。
const sections: PermSection[] = [
  {
    title: '组织管理',
    rows: [
      { op: '创建组织', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '组织列表', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '查看组织详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 本组织' },
      { op: '修改组织信息', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '启用 / 禁用组织', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '成员管理',
    rows: [
      { op: '成员列表', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看成员详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '创建成员', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '修改成员资料', admin: '🟡 仅自己', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '启用 / 禁用成员', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '删除成员', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '重置成员密码', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: 'Onboard（初始建实例）', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '为成员复建实例', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
    ],
  },
  {
    title: '应用实例',
    rows: [
      { op: '应用列表', admin: '✅', orgAdmin: '🟡 本组织全部', member: '🟡 仅自己' },
      { op: '查看应用详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '切换助手版本', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '运行时操作',
    rows: [
      { op: '启动 / 停止 / 重启', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '渠道（Channel）',
    rows: [
      { op: '查看渠道信息', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '绑定渠道', admin: '❌', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '知识库',
    rows: [
      { op: '读取组织知识库', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 本组织' },
      { op: '写入组织知识库', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看组织知识库同步状态', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '触发组织知识库同步重试', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '读取应用知识库', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '写入应用知识库', admin: '❌', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '助手版本',
    rows: [
      { op: '查看助手版本列表 / 详情', admin: '✅', orgAdmin: '✅', member: '✅' },
      { op: '创建 / 修改 / 删除助手版本', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '上传技能包', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '任务看板（Kanban）',
    rows: [
      { op: '查看任务看板', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '写操作（评论 / 完成 / 阻塞）', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: 'Cron 任务',
    rows: [
      { op: '查看 Cron 列表 / 详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '创建 / 修改 / 启停 / 删除 Cron', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '用量',
    rows: [
      { op: '查看组织聚合用量', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看成员用量', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '查看应用用量', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '审计日志',
    rows: [
      { op: '查看组织审计', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看应用审计', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '查看"我的审计"', admin: '✅', orgAdmin: '✅', member: '✅' },
    ],
  },
  {
    title: '充值记录',
    rows: [
      { op: '查看充值记录', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看余额', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
    ],
  },
  {
    title: '运行时节点',
    rows: [
      { op: '节点列表 / 详情', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '启用 / 禁用节点', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '平台总览',
    rows: [
      { op: '平台总览统计', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '模型列表',
    rows: [
      { op: '查看可用模型列表', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '后台任务（Jobs）',
    rows: [
      { op: '查看后台任务列表', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '工作区',
    rows: [
      { op: '查看 / 下载 / 打包工作区文件', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '资源指标',
    rows: [
      { op: '查看应用资源指标', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
]
</script>

<template>
  <div style="padding: 24px; max-width: 900px;">
    <n-h2 style="margin-bottom: 4px;">权限说明</n-h2>
    <n-p depth="3" style="margin-bottom: 24px;">各角色可见 / 可操作范围一览</n-p>

    <!-- 图例 -->
    <n-space style="margin-bottom: 24px;">
      <n-tag type="success" :bordered="false">✅ 可操作（无条件）</n-tag>
      <n-tag type="error" :bordered="false">❌ 无权限</n-tag>
      <n-tag type="warning" :bordered="false">🟡 有条件（本组织 / 仅自己）</n-tag>
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
            <th style="width: 40%;">操作</th>
            <th style="width: 20%; text-align: center;">平台管理员</th>
            <th style="width: 20%; text-align: center;">组织管理员</th>
            <th style="width: 20%; text-align: center;">组织成员</th>
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
