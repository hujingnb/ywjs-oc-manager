// audit 模块文案（en）。审计日志页面文案。
// 结构必须与 zh/audit.ts 完全对齐（相同 key 路径）。
export default {
  // page 页面标题与副标题。
  page: {
    eyebrowPlatform: 'Platform · Audit',
    eyebrowOrg: 'Org · Audit',
    title: 'Audit Log',
  },
  // filters 筛选区占位符与标签。
  filters: {
    selectOrg: 'Select organization',
  },
  // table 列表列名。
  table: {
    time: 'Time',
    actor: 'Actor',
    target: 'Resource',
    action: 'Action',
    detail: 'Detail',
    result: 'Result',
    deleted: 'Deleted',
  },
  // state 空态/错误文案。
  state: {
    noOrg: 'No organizations available',
    noOrgLinked: 'Current account is not linked to an organization. Cannot view audit log.',
    noPermission: 'Current account cannot view org-level audit. Check audit in your own app details.',
  },
  // actorRole 操作者角色代码到英文展示名映射。
  actorRole: {
    system: 'System',
    platform_admin: 'Platform admin',
    org_admin: 'Org admin',
    org_member: 'Org member',
  },
  // result 操作结果代码到英文展示名映射。
  result: {
    succeeded: 'Succeeded',
    failed: 'Failed',
  },
  // targetType 资源类型代码到英文展示名映射。
  targetType: {
    app: 'App instance',
    user: 'Member user',
    member: 'Member',
    organization: 'Organization',
    newapi_call: 'API call',
    app_skill: 'Instance skill',
  },
  // action 操作代码到英文展示名映射。
  // 格式：action.<target_type>.<action_code> 用于区分同名 action 在不同 target_type 下含义。
  // app_skill 的 action 原始码含点号（如 skill.install），此处将点替换为下划线以符合 vue-i18n key 规范。
  action: {
    member: {
      create_with_app: 'Join org (with app creation)',
    },
    app: {
      create: 'Create app',
      create_for_existing_member: 'Create app for existing member',
      update_model: 'Change model',
      channel_auth_start: 'Channel auth started',
      channel_bound: 'Channel bound',
      start: 'Start app',
      stop: 'Stop app',
      restart: 'Restart app',
      delete: 'Delete app',
      disable_api_key: 'Disable API key',
      restore_api_key: 'Restore API key',
      initialize: 'Initialize app',
      runtime_image_changed: 'App image changed',
    },
    user: {
      delete_member: 'Remove member',
    },
    organization: {
      recharge: 'Recharge org',
    },
    app_skill: {
      // key 用下划线代替点号（原始 action 码 skill.install → skill_install），避免 vue-i18n 将点视为 key 分隔符。
      skill_install: 'Install skill',
      skill_uninstall: 'Uninstall skill',
      skill_update: 'Update skill',
      skill_reinstall: 'Reinstall skill',
    },
  },
  // channelType 渠道代码到英文展示名映射，用于详情模板内联渠道名称。
  channelType: {
    wechat: 'WeChat',
  },
  // detail 详情模板，按 (target_type, action) 二元组索引，插值变量来自 metadata。
  // 变量占位符采用 vue-i18n 命名插值格式 {varName}，由 t(key, params) 自动填充。
  detail: {
    app_skill: {
      // key 用下划线代替点号，与 action 命名规则一致。
      // skill_ref 由调用方预拼接为「name@version」，避免 vue-i18n 将模板内的 @ 解析为 linked message 语法。
      skill_install: 'Installed skill {skill_ref} to instance {app_id}',
      skill_uninstall: 'Uninstalled skill {skill_name} from instance {app_id}',
      skill_update: 'Updated skill {skill_name} to version {skill_version} on instance {app_id}',
      skill_reinstall: 'Reinstalled skill {skill_ref} on instance {app_id}',
    },
    app: {
      channel_auth_start: 'Started {channel_type} channel auth, job {job_id}',
      create: 'Created app {app_name} for {member_name}',
      create_for_existing_member: 'Created app {app_name} for existing member {member_name}',
      delete: 'Deleted app, cascade-removed {cascade_count} channel binding(s)',
    },
    user: {
      delete_member: 'Cascade-deleted {cascade_count} app(s)',
    },
    member: {
      create_with_app: 'Member {member_name} joined with app {app_name}',
    },
    organization: {
      recharge: 'Recharged {amount} point(s)',
      recharge_with_remark: 'Recharged {amount} point(s) — {remark}',
    },
    newapi_call: {
      default: 'Endpoint {endpoint}, status {status_code}',
      not_sent: 'Endpoint {endpoint} (not sent)',
    },
  },
  // unknownLabel 未知代码的兜底展示模板，{code} 替换为原始字符串。
  unknownLabel: '{code}',
} as const
