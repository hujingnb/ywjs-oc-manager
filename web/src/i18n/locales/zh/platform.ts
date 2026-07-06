// platform 模块文案（zh）。P2 迁移：平台管理各页面可见文案。
// 结构必须与 en/platform.ts 完全对齐（相同 key 路径）。
export default {
  // orgs：企业管理页
  orgs: {
    title: '企业列表',
    addButton: '新增企业',
    form: {
      createTitle: '创建企业',
      editTitle: '编辑企业',
      labelName: '名称 *',
      labelCode: '企业标识 *',
      labelCodeReadonly: '企业标识（不可修改）',
      labelAdminUsername: '管理员账号 *',
      labelAdminDisplayName: '管理员姓名 *',
      labelAdminPassword: '管理员密码 *',
      labelContact: '联系人',
      labelPhone: '联系电话',
      labelCreditWarning: '余额预警阈值 (%)',
      labelMaxInstance: '实例数量上限（留空 = 不限制）',
      labelKnowledgeQuota: '企业知识库空间 (GB)',
      labelPersonalKnowledgeQuota: '个人知识库空间 (GB)',
      personalKnowledgeQuotaHint: '该企业新建实例的默认个人知识库空间上限。仅对之后新建的实例生效，不影响已有实例；平台管理员仍可在实例中单独调整。',
      labelRemark: '备注',
      labelVersions: '可用助手版本',
      placeholderName: '企业名称',
      placeholderCode: 'test-org',
      placeholderAdminUsername: '登录账号',
      placeholderAdminDisplayName: '管理员姓名',
      placeholderAdminPassword: '初始登录密码',
      placeholderContact: '联系人姓名',
      placeholderPhone: '手机号',
      placeholderMaxInstance: '留空表示不限制',
      placeholderVersions: '选择该企业可用的助手版本（可多选，可留空）',
    },
    columns: {
      name: '名称',
      code: '企业标识',
      status: '状态',
      contact: '联系人',
      phone: '电话',
      warningThreshold: '预警阈值',
      maxInstance: '实例上限',
      knowledgeQuota: '知识库空间',
      balance: '当前余额',
      unlimited: '不限',
    },
    actions: {
      edit: '编辑',
      copyInfo: '复制信息',
      rechargeHistory: '充值记录',
      recharge: '充值',
      disable: '禁用',
      enable: '启用',
    },
    copy: {
      successMsg: '已复制 {name} 的企业信息',
      failMsg: '复制失败，请检查浏览器剪贴板权限',
      adminPasswordHint: '<创建时设置，系统不保存明文；如忘记请重置密码>',
      // formatCode/Name/AdminUsername/AdminPassword：复制企业登录信息时的标签前缀行。
      formatCode: '标识： {code}',
      formatName: '名称： {name}',
      formatAdminUsername: '管理员账号： {username}',
      formatAdminPassword: '管理员密码： {hint}',
    },
    editError: '编辑失败',
    rechargeModal: {
      title: '企业充值',
      currentBalance: '当前余额：',
      balanceLoading: '加载中…',
      balanceFail: '查询失败',
      remain: '剩余 {remain}',
      used: '已用 {used}',
      labelAmount: '充值金额（正整数）',
      labelRemark: '备注',
      placeholderAmount: '输入金额',
      placeholderRemark: '业务说明，可选',
      confirmButton: '确认充值',
      invalidAmount: '请输入正整数充值金额',
      successMsg: '已充值 {amount}',
      failMsg: '充值失败',
    },
    historyModal: {
      titleWithOrg: '充值记录 · {name}',
      titleFallback: '充值记录',
      totalRecharged: '累计充值金额',
      currentBalance: '当前剩余金额',
      queryFail: '查询失败',
      loading: '加载中…',
      columns: {
        time: '时间',
        amount: '金额',
        remark: '备注',
        status: '状态',
        operator: '操作人',
        statusSucceeded: '成功',
        statusFailed: '失败',
      },
    },
  },
  // permissions：权限说明页
  permissions: {
    title: '权限说明',
    subtitle: '各角色可见 / 可操作范围一览',
    legendFull: '✅ 可操作（无条件）',
    legendNone: '❌ 无权限',
    legendCond: '🟡 有条件（本企业 / 仅自己）',
    tableOp: '操作',
    tableAdmin: '平台管理员',
    tableOrgAdmin: '企业管理员',
    tableMember: '企业成员',
    sections: {
      orgMgmt: '企业管理',
      memberMgmt: '成员管理',
      appInstance: '应用实例',
      runtime: '运行时操作',
      channel: '渠道（Channel）',
      knowledge: '知识库',
      version: '助手版本',
      kanban: '任务看板（Kanban）',
      cron: 'Cron 任务',
      usage: '用量',
      audit: '审计日志',
      recharge: '充值记录',
      node: '运行时节点',
      overview: '平台总览',
      models: '模型列表',
      jobs: '后台任务（Jobs）',
      workspace: '工作区',
      metrics: '资源指标',
    },
    // ops：权限矩阵中每条操作名称，key 与 en/platform.ts 和 PermissionsPage.vue 完全对齐。
    ops: {
      // 企业管理
      createOrg: '创建企业',
      listOrgs: '企业列表',
      viewOrgDetail: '查看企业详情',
      editOrgInfo: '修改企业信息',
      toggleOrg: '启用 / 禁用企业',
      // 成员管理
      listMembers: '成员列表',
      viewMemberDetail: '查看成员详情',
      createMember: '创建成员',
      editMember: '修改成员资料',
      toggleMember: '启用 / 禁用成员',
      deleteMember: '删除成员',
      resetPassword: '重置成员密码',
      onboard: 'Onboard（初始建实例）',
      rebuildInstance: '为成员复建实例',
      // 应用实例
      listApps: '应用列表',
      viewAppDetail: '查看应用详情',
      switchVersion: '切换助手版本',
      // 运行时操作
      startStopRestart: '启动 / 停止 / 重启',
      // 渠道
      viewChannel: '查看渠道信息',
      bindChannel: '绑定渠道',
      // 知识库
      readOrgKnowledge: '读取企业知识库',
      writeOrgKnowledge: '写入 / 重解析企业知识库',
      readAppKnowledge: '读取应用知识库',
      writeAppKnowledge: '写入 / 重解析应用知识库',
      // 助手版本
      viewVersions: '查看助手版本列表 / 详情',
      manageVersions: '创建 / 修改 / 删除助手版本',
      uploadSkill: '上传技能包',
      // 任务看板
      viewKanban: '查看任务看板',
      writeKanban: '写操作（评论 / 完成 / 阻塞）',
      // Cron 任务
      viewCron: '查看 Cron 列表 / 详情',
      manageCron: '创建 / 修改 / 启停 / 删除 Cron',
      // 用量
      viewOrgUsage: '查看企业聚合用量',
      viewMemberUsage: '查看成员用量',
      viewAppUsage: '查看应用用量',
      // 审计日志
      viewOrgAudit: '查看企业审计',
      viewAppAudit: '查看应用审计',
      viewMyAudit: '查看"我的审计"',
      // 充值记录
      viewRecharge: '查看充值记录',
      viewBalance: '查看余额',
      // 运行时节点
      viewNodes: '节点列表 / 详情',
      toggleNode: '启用 / 禁用节点',
      // 平台总览
      viewOverview: '平台总览统计',
      // 模型列表
      viewModels: '查看可用模型列表',
      // 后台任务
      viewJobs: '查看后台任务列表',
      // 工作区
      workspaceAccess: '查看 / 下载 / 打包工作区文件',
      // 资源指标
      viewMetrics: '查看应用资源指标',
    },
    // condOrg/condOrgAll/condSelf：条件权限描述，与 en/platform.ts 和 PermissionsPage.vue 对齐（平铺，非嵌套）。
    condOrg: '🟡 本企业',
    condOrgAll: '🟡 本企业全部',
    condSelf: '🟡 仅自己',
  },
  // console：控制台首页
  console: {
    tabs: {
      tokenTrend: 'Token 趋势',
      orgUsage: '各企业用量',
      instanceStatus: '实例状态',
    },
    stats: {
      orgCount: '企业数',
      memberCount: '成员数',
      memberNote: '不含平台管理员',
      instanceCount: '实例数',
      running: '运行中',
      error: '异常',
      todayToken: '今日 Token',
      todayTokenNoteRealtime: 'new-api 实时',
      todayTokenNoteLoading: '加载中…',
      todayTokenNoteUnavail: '不可用',
    },
    chart: {
      loading: '加载中…',
      usageUnavail: '用量服务不可用',
      platformUnavail: '平台数据不可用',
      empty: '暂无数据',
      pieRunning: '运行中',
      pieStopped: '停止',
      pieError: '异常',
    },
  },
  // tickets：定制技能工单页
  tickets: {
    heading: '定制技能工单',
    filterPlaceholder: '按标题过滤',
    loading: '加载中…',
    loadError: '工单查询失败：{msg}',
    filterAll: '全部状态',
    filterOrgAll: '全部组织',
    statusPending: '待处理',
    statusProcessing: '制作中',
    statusDelivered: '已交付',
    statusRejected: '已拒绝',
    columns: {
      title: '标题',
      requester: '提交者',
      status: '状态',
      quote: '报价',
    },
    roleAdmin: '管理员',
    roleMember: '成员',
  },
  // industry：行业知识库页
  industry: {
    title: '行业知识库',
    subtitle: '平台级通用资料库，可被助手版本选择参与运行时检索。',
    toolbar: {
      searchPlaceholder: '搜索行业名称',
      apiDocButton: '接口文档',
      createButton: '新建行业库',
    },
    fileSection: {
      clearButton: '清空文件',
      uploadButton: '上传文件',
      overwriteAlert: '同名文件会覆盖当前行业库内的旧文件。',
      fileSearchPlaceholder: '搜索文件名称',
      fileStatusPlaceholder: '全部状态',
      dateStartPlaceholder: '创建开始日期',
      dateEndPlaceholder: '创建结束日期',
      loading: '加载中…',
      queryFail: '查询失败：{msg}',
      fileCountPrefix: '共 {n} 个文件',
    },
    empty: '暂无行业知识库',
    createDialog: {
      title: '新建行业库',
      fieldLabel: '行业名称',
      placeholder: '请输入行业名称',
      confirmButton: '确认创建',
      emptyWarning: '请输入行业名称',
      successMsg: '已创建行业库 {name}',
      failMsg: '创建失败',
    },
    clearDialog: {
      title: '确认清空行业知识库文件',
      message: '将删除行业库「{name}」中的全部文件内容，行业库记录和助手版本关联会保留。该操作不可撤销。',
      confirmLabel: '确认清空',
      verifyHint: '输入行业库名称 "{name}" 以确认清空',
      successMsg: '已清空行业库「{name}」文件',
      failMsg: '清空失败',
    },
    apiDoc: {
      title: '行业知识库外部上传接口',
      summary: '外部商业知识库服务可通过固定 token 上传行业资料。manager 会按行业名称自动创建或复用行业库，同名文件会覆盖旧文件。',
      copyMarkdownButton: '复制 Markdown',
      sectionRequest: '请求',
      sectionFields: '表单字段',
      sectionCurl: 'curl 示例',
      sectionStatusCodes: '返回码',
      fieldIndustryName: '行业名称，必填；不存在时自动创建行业库。',
      fieldFile: '上传文件，必填；同一行业库内同名文件会覆盖。',
      tokenLoading: '读取中...',
      tokenError: '读取失败，请刷新页面',
      tokenMissing: '未配置，外部上传入口禁用',
      copySuccess: '已复制 Markdown 文档',
      copyFail: '复制失败，请手动复制文档内容',
      // authHeader/authHeaderCurrentValue：API 文档中鉴权 Header 说明行。
      authHeader: '鉴权 Header：',
      authHeaderCurrentValue: '，当前值：',
      // authHeaderSuffix：鉴权说明句末标点，随语言切换。
      authHeaderSuffix: '。',
      // 返回码描述。
      status202: '上传成功，文件进入 RAGFlow 解析队列。',
      status400: '参数缺失、行业名称为空或请求体格式错误。',
      status401: '缺少或错误的',
      status413: '文件大小超过平台上传限制。',
      // curlExampleIndustryName：curl 示例中的行业名称占位值，仅作为演示用途。
      curlExampleIndustryName: '保险',
      // apiDocMarkdown：复制给外部服务方的完整 Markdown 接口文档；{uploadToken} 与 {curlExample} 由计算属性注入。
      apiDocMarkdown: `# 行业知识库外部上传接口

外部商业知识库服务通过固定鉴权字符串把文件上传到平台级行业知识库。manager 会按行业名称自动创建或复用行业库，同一行业库内同名文件会覆盖旧文件。

## 接口

- Method: \`POST\`
- URL: \`https://<manager-domain>/api/v1/external/industry-knowledge/files\`
- Content-Type: \`multipart/form-data\`

## 鉴权

请求必须携带 Header：

\`\`\`text
X-OC-Industry-Knowledge-Token: {uploadToken}
\`\`\`

token 来自 manager 配置项 \`industry_knowledge.upload_token\`。该配置为空时外部上传入口禁用；只包含空白字符时 manager 会启动失败。

## 表单字段

| 字段 | 必填 | 说明 |
|---|---|---|
| \`industry_name\` | 是 | 行业名称。不存在时自动创建行业库；未删除行业库中名称唯一。 |
| \`file\` | 是 | 上传文件。同一行业库内同名文件会覆盖旧文件。 |

## curl 示例

\`\`\`bash
{curlExample}
\`\`\`

## 返回码

| 状态码 | 说明 |
|---|---|
| \`202\` | 上传成功，文件已进入 RAGFlow 解析队列。 |
| \`400\` | 参数缺失、行业名称为空或请求体格式错误。 |
| \`401\` | 缺少或错误的 \`X-OC-Industry-Knowledge-Token\`。 |
| \`413\` | 文件大小超过平台上传限制。 |

## 注意事项

- 上传成功后通常先返回 \`parse_status=queued\`，解析完成后才能稳定参与检索。
- 外部上传只负责写入行业库；实例是否检索该行业库，由助手版本的行业知识库关联决定。
- 每个关联行业库都会在检索时单独召回最多 \`top_k\` 条结果，关联过多会增加上下文长度和响应成本。`,
    },
    // uploadConflict：上传文件时已有任务在进行的提示
    uploadConflict: '已有上传任务正在进行',
    baseColumns: {
      name: '行业名称',
      docCount: '文件数',
      updatedAt: '更新时间',
    },
    baseActions: {
      files: '文件',
      ragflow: 'RAGFlow 信息',
      rename: '重命名',
      delete: '删除',
      renamePrompt: '新的行业名称',
      renameSuccess: '已重命名为 {name}',
      renameFail: '重命名失败',
      deleteConfirm: '确认删除行业库「{name}」？',
      deleteSuccess: '已删除行业库 {name}',
      deleteFail: '删除失败',
    },
    fileColumns: {
      name: '文件名称',
      size: '大小',
      type: '类型',
      parseStatus: '解析状态',
      createdAt: '创建时间',
    },
    fileActions: {
      download: '下载',
      downloading: '下载中…',
      reparse: '重解析',
      reparsing: '提交中…',
      delete: '删除',
      deleteConfirm: '确认删除 {name} ？',
      downloadFail: '下载失败',
    },
  },
  // recharge：独立充值页
  recharge: {
    heading: '企业充值',
    orgIdLabel: '企业 {id}',
    missingOrgId: 'URL 缺少企业 ID',
    backToOrgs: '返回企业列表',
    currentBalance: '当前余额：',
    balanceLoading: '加载中…',
    balanceFail: '查询失败',
    labelAmount: '充值金额（正整数）',
    labelRemark: '备注（可选）',
    placeholderAmount: '输入金额',
    placeholderRemark: '业务说明',
    submitPending: '充值中…',
    submitButton: '提交充值',
    confirmTitle: '确认企业充值',
    confirmMessage: '将给当前企业充值 {amount}。该操作会调用 new-api 修改余额。',
    confirmLabel: '确认充值',
    confirmHint: '输入企业名称 "{name}" 以确认充值',
    successMsg: '已充值 {amount}（{status}）',
    failMsg: '充值失败',
    orgNameFallback: '企业 {id}',
    historyTitle: '充值历史',
    historyLoading: '加载中…',
    historyError: '查询失败：{msg}',
    columns: {
      time: '时间',
      amount: '金额',
      remark: '备注',
      status: '状态',
      error: '错误',
    },
  },
  // skills：平台技能管理页
  skills: {
    uploadTitle: '上传平台技能',
    listTitle: '平台技能列表',
    uploadMode: {
      label: '上传方式',
      markdown: '粘贴 Markdown',
      folder: '上传文件夹',
    },
    markdownMode: {
      label: 'SKILL.md 内容 *',
      placeholder: '粘贴 SKILL.md 全文，需含 --- 包裹的 frontmatter（至少含 name 字段）',
      hintFillExample: '填充示例',
      hintPrefix: '格式：以',
      hintSuffix: '包裹的 frontmatter 开头（至少含',
      hintDescOpt: '可选），其后是 Markdown 正文。示例：',
    },
    folderMode: {
      label: 'Skill 文件夹 *',
      selectButton: '选择文件夹',
      selectedInfo: '{name}（{count} 个文件）',
      noFolder: '未选择文件夹',
      hintSkillMd: '文件夹需包含',
      hintMdFrontmatter: '文件需包含 YAML 格式的技能名称（',
      hintAndDesc: '）和描述（',
      hintClose: '）。',
      tipSelectSkillFolder: '选择 skill 自身的文件夹，其中需直接包含 SKILL.md（即 所选文件夹/SKILL.md，不要选它的上层目录）。',
      tipSubdir: '文件夹内的子目录与附属文件会原样保留（如 scripts/、assets/、reference.md）。',
      tipName: '技能名取自 SKILL.md 的 name 字段，与文件夹名无关；上传时会自动剥掉最外层目录、按扁平结构打包。',
    },
    parsedPreview: '识别到技能：',
    // markdownExample：粘贴 Markdown 模式的格式示例，既用于展示也用于填充示例按钮
    markdownExample: '---\nname: my-skill\ndescription: 一句话描述这个技能的用途\n---\n\n# My Skill\n\n用 Markdown 说明这个技能：什么时候触发、做什么、怎么用。',
    versionLabel: '版本 *',
    versionPlaceholder: '如 1.0.0',
    descLabel: '描述',
    descPlaceholder: '技能描述（默认取自 SKILL.md，可修改）',
    uploadButton: '上传',
    loading: '加载中…',
    queryFail: '查询失败：{msg}',
    columns: {
      name: '名称',
      version: '版本',
      fileSize: '文件大小',
    },
    deleteDialog: {
      title: '删除 Skill',
      content: '确定删除 skill「{name} {version}」？删除后不可恢复。',
      confirm: '删除',
      cancel: '取消',
    },
    uploadSuccess: '已上传 skill {name} {version}',
    uploadFail: '上传失败',
    deleteSuccess: '已删除 skill {name} {version}',
    deleteFail: '删除失败',
  },
  // versions：助手版本管理页
  versions: {
    listTitle: '助手版本',
    addButton: '新增版本',
    form: {
      createTitle: '新建助手版本',
      editTitle: '编辑助手版本',
      labelName: '名称 *',
      labelImage: '使用镜像 *',
      labelDesc: '描述',
      labelSystemPrompt: '内置提示词 *',
      labelMainModel: '主模型 *',
      labelRouting: '智能路由（留空走主模型）',
      labelIndustryKnowledge: '行业知识库',
      labelSkills: 'Skill 列表',
      placeholderName: '版本名称（唯一）',
      placeholderImage: '选择 Hermes 镜像',
      placeholderDesc: '版本用途说明',
      placeholderSystemPrompt: '可填写助手人设、行为规则等；将注入容器 SOUL.md 的版本层',
      placeholderMainModel: '选择主对话模型',
      placeholderRouting: '默认走主模型',
      placeholderIndustry: '选择该版本运行时要额外检索的行业知识库',
      industryAlert: '每选一个行业知识库，系统都会多查一批参考内容。选得越多，回答要处理的内容越多，速度和费用都可能增加。建议只选当前版本真正需要的行业库。',
      imageLoadFail: '镜像列表获取失败，请重试',
      modelLoadFail: '模型列表获取失败，请重试',
      industryLoadFail: '行业知识库列表获取失败，请重试',
      noSkill: '暂无 skill',
      skillDeleteButton: '删除',
      skillSaveFirst: '保存版本后可配置 skill',
      skillAdded: '已添加 skill {name} v{version}',
      skillDeletedMsg: '已删除 skill {name}',
      skillAddFail: '添加失败',
      skillDeleteFail: '删除失败',
      skillActionLabel: '添加',
      skillExistingLabel: '已添加',
      saveFail: '保存失败',
      createSuccessHint: '版本已创建，可在下方从市场选择 skill',
    },
    columns: {
      name: '名称',
      image: '镜像',
      mainModel: '主模型',
      revision: '修订号',
      skillCount: 'Skill 数',
    },
    actions: {
      edit: '编辑',
      delete: '删除',
    },
    deleteDialog: {
      title: '删除助手版本',
      message: '确定删除版本「{name}」？删除后不可恢复。',
      confirmLabel: '删除',
    },
    deleteSuccess: '已删除版本 {name}',
    deleteFail: '删除失败',
    // routingSlots 是智能路由 8 个 auxiliary 槽位的用户可见标签，对应 AUXILIARY_SLOTS 常量 key。
    routingSlots: {
      vision: '图像识别',
      compression: '上下文压缩',
      web_extract: '网页提取',
      session_search: '会话搜索',
      title_generation: '标题生成',
      approval: '智能审批',
      skills_hub: '技能检索',
      mcp: 'MCP 路由',
    },
  },
  // webPublishConfig：平台管理员 web-publish 开通配置页（WebPublishConfigPage）。
  webPublishConfig: {
    // 页面标题及卡片标题。
    title: 'Web 发布开通配置',
    formTitle: '配置参数',
    enableDisableTitle: '开通 / 停用',
    // 企业选择器。
    labelOrg: '企业',
    placeholderOrg: '选择企业',
    // 配置表单字段标签与占位符。
    labelBaseDomain: '根域名 *',
    placeholderBaseDomain: '如 apps.example.com',
    labelDnsProvider: 'DNS 服务商 *',
    placeholderDnsProvider: '选择 DNS 服务商',
    labelSiteTtlDays: '站点存活天数',
    placeholderSiteTtlDays: '默认 7 天',
    labelMaxSites: '最大站点数',
    placeholderMaxSites: '默认 20',
    // 凭证区域。
    credentialHint: '凭证仅写入，不回填。留空表示保留已有加密凭证。',
    labelAccessKeyId: 'Access Key ID',
    placeholderAccessKeyId: '访问密钥 ID',
    labelAccessKeySecret: 'Access Key Secret',
    placeholderAccessKeySecret: '访问密钥 Secret',
    labelRegion: '地域（仅华为云需要）',
    placeholderRegion: '如 cn-north-4',
    // 保存按钮及反馈。
    saveButton: '保存配置',
    saveSuccess: '配置已保存',
    saveFail: '保存失败',
    // 状态展示。
    statusLoading: '加载状态中…',
    currentStatus: '当前状态：',
    statusEnabled: '已开通',
    statusDisabled: '未开通',
    // 开通/停用按钮及反馈。
    enableButton: '开通 Web 发布',
    disableButton: '停用 Web 发布',
    enableSuccess: '开通已提交，请稍候查看下方 provisioning 状态',
    enableFail: '开通失败',
    disableSuccess: 'Web 发布已停用',
    disableFail: '停用失败',
    // 停用二次确认弹框。
    disableConfirmTitle: '确认停用 Web 发布？',
    disableConfirmMessage: '停用后 provisioning 状态将置为 disabled，配置数据和站点数据保留。',
    disableConfirmOk: '确认停用',
  },
  // webPublishCert：企业 web-publish 通配证书状态面板（WebPublishCertPanel）。
  webPublishCert: {
    // 面板标题。
    title: 'Web 发布证书',
    // 字段标签。
    wildcardDomain: '通配域名',
    certStatus: '证书状态',
    certNotAfter: '证书到期时间',
    certLastIssuedAt: '最近首签时间',
    certLastRenewedAt: '最近续签时间',
    certMessage: '失败原因',
    // 重试按钮及操作反馈。
    retryButton: '重试签发/续签',
    retrySuccess: '已提交重试，请稍候刷新查看最新状态',
    retryError: '重试失败，请稍后再试',
    // 未配置时的空态提示。
    noConfig: '当前企业暂无 web-publish 证书配置',
  },
} as const
