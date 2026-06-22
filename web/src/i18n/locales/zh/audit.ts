// audit 模块文案（zh）。审计日志页面文案。
// 结构必须与 en/audit.ts 完全对齐（相同 key 路径）。
export default {
  // page 页面标题与副标题。
  page: {
    eyebrowPlatform: 'Platform · 审计',
    eyebrowOrg: '企业 · 审计',
    title: '审计日志',
  },
  // filters 筛选区占位符与标签。
  filters: {
    selectOrg: '选择企业',
  },
  // table 列表列名。
  table: {
    time: '时间',
    actor: '操作者',
    target: '资源',
    action: '操作',
    detail: '详情',
    result: '结果',
    deleted: '已删除',
  },
  // state 空态/错误文案。
  state: {
    noOrg: '暂无可查看企业',
    noOrgLinked: '当前账号未关联企业，无法查看审计日志。',
    noPermission: '当前账号无权查看企业级审计，请在自己的实例详情中查看实例审计。',
  },
  // actorRole 操作者角色代码到中文展示名映射，来源：internal/service/audit_label.go actorRoleLabels。
  actorRole: {
    system: '系统',
    platform_admin: '平台管理员',
    org_admin: '企业管理员',
    org_member: '企业成员',
  },
  // result 操作结果代码到中文展示名映射，来源：internal/service/audit_label.go resultLabels。
  result: {
    succeeded: '成功',
    failed: '失败',
  },
  // targetType 资源类型代码到中文展示名映射，来源：internal/service/audit_label.go targetTypeLabels。
  targetType: {
    app: '应用实例',
    user: '成员用户',
    member: '成员',
    organization: '企业',
    newapi_call: 'API 调用',
    app_skill: '实例技能',
  },
  // action 操作代码到中文展示名映射，来源：internal/service/audit_label.go actionLabels。
  // 格式：action.<target_type>.<action_code>，用于区分同名 action 在不同 target_type 下含义。
  // app_skill 的 action 原始码含点号（如 skill.install），此处将点替换为下划线以符合 vue-i18n key 规范。
  action: {
    member: {
      create_with_app: '加入企业（含应用创建）',
    },
    app: {
      create: '创建应用',
      create_for_existing_member: '为已有成员创建应用',
      update_model: '更换模型',
      channel_auth_start: '渠道认证开始',
      channel_bound: '绑定渠道',
      start: '启动应用',
      stop: '停止应用',
      restart: '重启应用',
      delete: '删除应用',
      disable_api_key: '禁用 API Key',
      restore_api_key: '恢复 API Key',
      initialize: '初始化应用',
      runtime_image_changed: '应用镜像变更',
    },
    user: {
      delete_member: '移除成员',
    },
    organization: {
      recharge: '企业充值',
    },
    app_skill: {
      // key 用下划线代替点号（原始 action 码 skill.install → skill_install），避免 vue-i18n 将点视为 key 分隔符。
      skill_install: '安装技能',
      skill_uninstall: '卸载技能',
      skill_update: '更新技能',
      skill_reinstall: '重装技能',
    },
  },
  // channelType 渠道代码到中文展示名映射，用于详情模板内联渠道名称。
  channelType: {
    wechat: '微信',
  },
  // detail 详情模板，按 (target_type, action) 二元组索引，插值变量来自 metadata。
  // 变量占位符采用 vue-i18n 命名插值格式 {varName}，由 t(key, params) 自动填充。
  detail: {
    app_skill: {
      // key 用下划线代替点号，与 action 命名规则一致。
      // skill_ref 由调用方预拼接为「name@version」，避免 vue-i18n 将模板内的 @ 解析为 linked message 语法。
      skill_install: '安装技能 {skill_ref} 到实例 {app_id}',
      skill_uninstall: '从实例 {app_id} 卸载技能 {skill_name}',
      skill_update: '将实例 {app_id} 的技能 {skill_name} 更新至版本 {skill_version}',
      skill_reinstall: '在实例 {app_id} 重装技能 {skill_ref}',
    },
    app: {
      channel_auth_start: '开始 {channel_type} 渠道认证，任务 {job_id}',
      create: '为 {member_name} 创建应用 {app_name}',
      create_for_existing_member: '为已有成员 {member_name} 创建应用 {app_name}',
      delete: '删除应用，级联清除 {cascade_count} 个渠道绑定',
    },
    user: {
      delete_member: '级联删除 {cascade_count} 个应用',
    },
    member: {
      create_with_app: '成员 {member_name} 加入企业，同时创建应用 {app_name}',
    },
    organization: {
      recharge: '充值 {amount} 点',
      recharge_with_remark: '充值 {amount} 点 — {remark}',
    },
    newapi_call: {
      default: '接口 {endpoint}，状态码 {status_code}',
      not_sent: '接口 {endpoint}（未发送）',
    },
  },
  // unknownLabel 未知代码的兜底展示模板，{code} 替换为原始字符串。
  unknownLabel: '{code}',
} as const
