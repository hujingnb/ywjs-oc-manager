// helpContent.ts 收敛「使用手册」抽屉的静态文案。
// 三种角色看到的能力边界完全不同，这里按角色各维护一套独立手册，
// 内容与 DashboardLayout 的菜单裁剪、PermissionsPage 的权限矩阵保持一致。
// 纯静态数据、不调用任何 API；改文案即改此文件，无需后端配合。
// 国际化：每种语言维护独立的 HELP_MANUALS_ZH / HELP_MANUALS_EN；
// getHelpManual 根据 locale 参数选择对应语言版本，未知 locale 降级为中文。
import type { Role } from '@/domain/permissions'

// HelpSection 是「功能介绍」中的一个菜单分区：一个标题 + 若干条详细说明。
export interface HelpSection {
  // 分区标题，通常对应一个菜单入口或一类操作。
  title: string
  // items 为该分区的要点说明，每条是一句可直接展示的文本，覆盖用途、范围与边界。
  items: string[]
}

// HelpGuide 是「操作指引」中的一个分步教程：一个任务标题 + 有序步骤。
export interface HelpGuide {
  // title 为操作任务名，例如「创建企业」「新增成员并开通实例」。
  title: string
  // steps 为完成该任务的有序步骤，逐条按操作顺序展示。
  steps: string[]
}

// HelpManual 是单个角色的完整手册视图。
export interface HelpManual {
  // roleLabel 为角色名，用于抽屉标题。
  roleLabel: string
  // summary 是角色定位的一句话概述，置于手册顶部。
  summary: string
  // sections 为按菜单分区组织的功能介绍。
  sections: HelpSection[]
  // guides 为面向常见任务的分步操作指引，回答「怎么用」。
  guides: HelpGuide[]
}

// HELP_MANUALS_ZH 是中文版三角色手册。
// 仅覆盖该角色在左侧菜单中实际可达的入口与可执行的操作，避免误导用户去点没有权限的功能。
const HELP_MANUALS_ZH: Record<Role, HelpManual> = {
  // 平台管理员：全平台治理视角，可跨企业观察与运维，但不直接代企业建成员、也无企业余额入口。
  platform_admin: {
    roleLabel: '平台管理员',
    summary: '平台管理员负责全平台的企业、助手版本与运行资源治理，可跨企业观察和运维，但不直接为企业创建成员。日常工作是开通企业、维护助手版本、监控平台用量与排查异常实例。',
    sections: [
      {
        title: '控制台',
        items: [
          '登录后的默认首页，集中展示平台级关键指标。',
          '可看到企业总数、成员总数（不含平台管理员）、实例数及运行 / 异常分布。',
          '还包含今日 Token 消耗趋势、各企业用量对比与可用模型列表，数据实时来自 new-api。',
        ],
      },
      {
        title: '企业',
        items: [
          '平台的企业台账：创建企业、编辑企业信息、启用 / 禁用企业、为企业充值。',
          '创建企业时会同时生成该企业的首个企业管理员账号，后续成员由企业管理员自行新增。',
          '禁用企业会冻结其下所有成员与实例的访问，启用后恢复。',
        ],
      },
      {
        title: '助手版本',
        items: [
          '管理全平台共享的助手版本库：新建版本、上传技能包、维护版本元信息。',
          '每个版本对应一套智能体能力与技能集，实例通过「切换版本」绑定到具体版本。',
          '升级版本后，相关实例通常需要重启才能生效。',
        ],
      },
      {
        title: '成员',
        items: [
          '可跨企业查看成员列表与成员详情，用于核对账号归属与状态。',
          '平台管理员不在此处新增企业成员，新增成员是企业管理员的职责。',
        ],
      },
      {
        title: '实例（跨企业）',
        items: [
          '查看全平台所有企业的实例，并进入实例详情进行运维。',
          '可执行启动 / 停止 / 重启、切换助手版本等操作。',
          '实例详情中的「运行时」分区为平台管理员专属，用于查看节点与容器状态、定位失败阶段与原因。',
        ],
      },
      {
        title: '企业知识库',
        items: [
          '可跨企业上传、重新解析与搜索知识库文档。',
          '适合下发公共制度、运维补充资料等需要平台侧统一维护的内容。',
        ],
      },
      {
        title: '用量',
        items: [
          '查看平台范围内的 Token 消耗与调用统计，可按企业、成员、应用维度下钻。',
          '数据实时来自 new-api，平台不做本地缓存。',
        ],
      },
      {
        title: '审计',
        items: [
          '回溯平台范围内的高危操作记录，用于安全追责与问题复盘。',
        ],
      },
      {
        title: '权限说明',
        items: [
          '提供完整的三角色权限矩阵，逐项列出各操作对平台管理员 / 企业管理员 / 企业成员的可见与可操作范围。',
          '不确定某操作谁能做时，先来这里核对。',
        ],
      },
    ],
    guides: [
      {
        title: '创建一个新企业',
        steps: [
          '左侧菜单进入「企业」。',
          '点击右上角的新建企业按钮。',
          '填写企业名称、企业标识（code），以及首个企业管理员的账号、显示名与初始密码。',
          '提交后系统会随企业生成该企业管理员账号。',
          '把企业标识、管理员账号与初始密码交给企业对接人，后续成员由其自行开通。',
        ],
      },
      {
        title: '为企业充值额度',
        steps: [
          '进入「企业」，在列表中找到目标企业。',
          '打开该企业的充值入口，填写充值额度并确认。',
          '余额与充值记录由对应企业管理员在其「账户余额」中查看。',
        ],
      },
      {
        title: '维护助手版本并升级实例',
        steps: [
          '进入「助手版本」，新建一个版本并上传对应技能包。',
          '进入「实例」，打开需要升级的实例详情。',
          '在版本区域点击「切换」，选择目标版本。',
          '切换后按提示重启实例使新版本生效。',
        ],
      },
      {
        title: '排查异常实例',
        steps: [
          '进入「实例」，打开状态为「异常」的实例详情。',
          '在「总览」查看失败阶段与错误原因。',
          '切到「运行时」分区查看节点与容器状态。',
          '根据原因执行重启，或处理底层依赖（如企业 new-api 凭据缺失）后再重启。',
        ],
      },
    ],
  },
  // 企业管理员：本企业全权管理，所有操作严格限定在自己所属企业范围内。
  org_admin: {
    roleLabel: '企业管理员',
    summary: '企业管理员负责本企业的成员、实例、知识库、余额与审计，所有操作都限定在本企业范围内。日常工作是为员工开通账号和专属实例、维护企业知识库、关注余额与用量。',
    sections: [
      {
        title: '总览',
        items: [
          '企业管理员的默认首页，展示本企业的整体运行状况。',
          '包含成员数、实例数、实例运行 / 异常分布、当前余额与今日 Token 消耗。',
          '还有本企业的用量趋势与实例状态分布图。',
        ],
      },
      {
        title: '成员',
        items: [
          '管理本企业的全部成员：新增、编辑资料、启用 / 禁用、删除、重置密码。',
          '新增成员时可一并初始化其专属实例（Onboard），员工登录后即有可用实例。',
          '管理范围仅限本企业成员，看不到其他企业的成员。',
        ],
      },
      {
        title: '实例',
        items: [
          '管理本企业全部实例，进入详情可使用总览、任务、定时任务、渠道、知识库、工作目录、审计等分区。',
          '可执行启动 / 停止 / 重启、切换助手版本、绑定第三方渠道等操作。',
          '当成员实例异常时，可在此协助排查与重启。',
        ],
      },
      {
        title: '企业知识库',
        items: [
          '维护本企业共享文档：上传、重新解析、搜索。',
          '企业内所有成员都可读取此处文档，但只有企业管理员可写入与重解析。',
          '上传后需等待解析完成才能被智能体检索；解析异常可重新解析。',
        ],
      },
      {
        title: '用量',
        items: [
          '查看本企业聚合用量，并可下钻到成员与实例维度的明细。',
          '用于核对各成员 / 实例的 Token 消耗，数据实时来自 new-api。',
        ],
      },
      {
        title: '账户余额',
        items: [
          '查看本企业当前余额与历史充值记录。',
          '余额为实时数据，直接来自 new-api，不做本地缓存。',
          '充值由平台管理员执行，余额不足时请联系平台侧。',
        ],
      },
      {
        title: '审计',
        items: [
          '回溯本企业范围内的高危操作记录，便于追责与排查。',
          '可看到成员管理、实例运维、知识库写入等关键动作的执行人与结果。',
        ],
      },
    ],
    guides: [
      {
        title: '新增成员并开通专属实例',
        steps: [
          '左侧菜单进入「成员」。',
          '点击「新增成员」。',
          '填写成员账号、显示名、初始密码，并选择角色（通常为企业成员）。',
          '确认初始化实例（Onboard），系统会为该成员创建专属实例。',
          '提交后把账号与初始密码交给员工，员工登录即可使用自己的实例。',
        ],
      },
      {
        title: '管理某个实例',
        steps: [
          '进入「实例」，选择目标实例打开详情。',
          '在「总览」查看运行状态与助手版本，可执行启动 / 停止 / 重启。',
          '在「渠道」绑定对外对话入口，在「定时任务」配置调度，在「知识库」维护该实例资料。',
          '需要升级能力时点「切换」更换助手版本，并按提示重启。',
        ],
      },
      {
        title: '维护企业知识库',
        steps: [
          '进入「企业知识库」。',
          '上传文档，等待解析状态变为完成。',
          '若解析异常，点击重新解析。',
          '解析完成后，企业内成员与实例即可检索到这些资料。',
        ],
      },
      {
        title: '重置成员密码或停用成员',
        steps: [
          '进入「成员」，找到目标成员。',
          '在该成员的操作中选择「重置密码」，把新密码交给员工。',
          '员工离职或需冻结时，选择「停用」即可暂停其访问，必要时可删除。',
        ],
      },
    ],
  },
  // 企业成员：只管理分配给自己的唯一实例与个人知识库，菜单已将实例分区拉平到左侧。
  org_member: {
    roleLabel: '企业成员',
    summary: '企业成员只管理分配给自己的实例与个人知识库，可查看企业共享知识库与个人用量。日常工作是配置好自己的实例、上传个人资料、用任务看板与定时任务驱动智能体干活。',
    sections: [
      {
        title: '我的实例',
        items: [
          '左侧菜单已把实例的各分区直接拉平展示，无需先进入实例列表。',
          '你只能看到分配给自己的那一个实例。',
          '若还没有分配实例，相关入口会引导到空状态页。',
        ],
      },
      {
        title: '总览',
        items: [
          '查看实例的基本信息、运行状态、API Key 状态与当前助手版本。',
          '实例异常时这里会显示失败阶段与原因，并提供「重新初始化」入口。',
        ],
      },
      {
        title: '任务',
        items: [
          '任务看板，用于把要让智能体处理的事项组织成卡片。',
          '可创建任务、添加评论、标记完成或标记阻塞。',
        ],
      },
      {
        title: '定时任务',
        items: [
          '为自己的实例配置周期性 / 定时执行的任务。',
          '可创建、修改、启停与删除调度。',
        ],
      },
      {
        title: '渠道',
        items: [
          '查看并绑定第三方渠道，把实例接入外部对话入口。',
          '绑定后即可通过外部渠道与你的智能体对话。',
        ],
      },
      {
        title: '个人知识库',
        items: [
          '上传、重新解析与搜索属于你自己实例的知识库文件。',
          '上传的资料会被智能体在对话时检索引用；解析完成后才生效。',
        ],
      },
      {
        title: '工作目录',
        items: [
          '查看与下载实例工作目录中的文件，了解智能体产出的内容。',
        ],
      },
      {
        title: '企业知识库',
        items: [
          '只读查看企业下发的共享文档，无法在此上传或修改。',
          '如需补充企业级资料，请联系企业管理员。',
        ],
      },
      {
        title: '用量',
        items: [
          '查看属于你个人的 Token 消耗与调用统计，数据实时来自 new-api。',
        ],
      },
    ],
    guides: [
      {
        title: '开始使用我的实例',
        steps: [
          '登录后默认进入「总览」，确认实例状态正常。',
          '进入「渠道」，绑定一个对外对话入口。',
          '进入「个人知识库」，上传你希望智能体参考的资料。',
          '资料解析完成后，即可通过渠道与智能体对话。',
        ],
      },
      {
        title: '创建一个定时任务',
        steps: [
          '进入「定时任务」。',
          '新建任务，设置调度规则与要执行的内容。',
          '保存后可随时启停或删除该任务。',
        ],
      },
      {
        title: '在任务看板上协作',
        steps: [
          '进入「任务」。',
          '新建任务卡片，描述要让智能体处理的事项。',
          '在卡片上添加评论补充细节。',
          '完成后标记完成，遇到卡点标记阻塞。',
        ],
      },
      {
        title: '实例显示异常时怎么办',
        steps: [
          '在「总览」查看失败阶段与错误原因。',
          '尝试点击「重新初始化」让实例重建运行环境。',
          '若仍然异常，把总览上的错误信息提供给企业管理员协助处理。',
        ],
      },
    ],
  },
}

// HELP_MANUALS_EN 是英文版三角色手册，内容与中文版保持语义一致。
// 仅覆盖该角色在左侧菜单中实际可达的入口与可执行的操作，避免误导用户。
const HELP_MANUALS_EN: Record<Role, HelpManual> = {
  // Platform Admin: platform-wide governance perspective; can observe and operate across organizations,
  // but does not directly create members on behalf of organizations.
  platform_admin: {
    roleLabel: 'Platform Admin',
    summary: 'Platform Admins govern organizations, assistant versions, and runtime resources across the entire platform. They can observe and operate across organizations, but do not directly create members for them. Day-to-day work includes onboarding organizations, maintaining assistant versions, monitoring platform-wide usage, and troubleshooting abnormal instances.',
    sections: [
      {
        title: 'Console',
        items: [
          'The default home page after login, displaying key platform-level metrics at a glance.',
          'Shows total organizations, total members (excluding platform admins), instance count, and running/abnormal distribution.',
          'Also includes today\'s token consumption trend, per-organization usage comparison, and available model list — all data sourced in real time from new-api.',
        ],
      },
      {
        title: 'Organizations',
        items: [
          'The platform\'s organization registry: create organizations, edit organization info, enable/disable organizations, and top up balances.',
          'Creating an organization simultaneously generates the first Org Admin account for it; subsequent members are added by the Org Admin.',
          'Disabling an organization freezes access for all its members and instances; re-enabling restores access.',
        ],
      },
      {
        title: 'Assistant Versions',
        items: [
          'Manage the platform-wide shared assistant version library: create versions, upload skill packages, and maintain version metadata.',
          'Each version corresponds to a specific set of agent capabilities and skills; instances are bound to a version via "Switch Version".',
          'After upgrading a version, the affected instances typically need to be restarted for the change to take effect.',
        ],
      },
      {
        title: 'Members',
        items: [
          'View member lists and member details across organizations — useful for verifying account ownership and status.',
          'Platform Admins do not add org members here; that is the Org Admin\'s responsibility.',
        ],
      },
      {
        title: 'Instances (Cross-Org)',
        items: [
          'View instances from all organizations across the platform and open instance details for operations.',
          'Supported actions: start / stop / restart and switch assistant version.',
          'The "Runtime" tab in instance details is exclusive to Platform Admins, used to inspect node and container status and pinpoint failure stages and causes.',
        ],
      },
      {
        title: 'Organization Knowledge Base',
        items: [
          'Upload, re-parse, and search knowledge base documents across organizations.',
          'Suitable for distributing common policies, supplementary operational materials, or any content that requires centralized platform-side maintenance.',
        ],
      },
      {
        title: 'Usage',
        items: [
          'View token consumption and call statistics across the platform; drill down by organization, member, or application dimension.',
          'Data is sourced in real time from new-api; the platform does not cache it locally.',
        ],
      },
      {
        title: 'Audit',
        items: [
          'Review platform-wide records of high-risk operations for security accountability and incident post-mortems.',
        ],
      },
      {
        title: 'Permissions',
        items: [
          'Provides a complete three-role permission matrix listing, for each operation, what is visible and actionable by Platform Admin / Org Admin / Org Member.',
          'When unsure who can perform a certain action, check here first.',
        ],
      },
    ],
    guides: [
      {
        title: 'Create a New Organization',
        steps: [
          'Go to "Organizations" from the left-side menu.',
          'Click the "New Organization" button in the top-right corner.',
          'Fill in the organization name, organization code, and the first Org Admin\'s account, display name, and initial password.',
          'On submission, the system creates the Org Admin account together with the organization.',
          'Hand over the organization code, admin account, and initial password to the organization\'s contact person — they will onboard subsequent members themselves.',
        ],
      },
      {
        title: 'Top Up an Organization\'s Balance',
        steps: [
          'Go to "Organizations" and locate the target organization in the list.',
          'Open the top-up entry for that organization, enter the amount, and confirm.',
          'The updated balance and top-up history can be viewed by the Org Admin in their "Account Balance" section.',
        ],
      },
      {
        title: 'Maintain Assistant Versions and Upgrade Instances',
        steps: [
          'Go to "Assistant Versions", create a new version, and upload the corresponding skill package.',
          'Go to "Instances" and open the detail page of the instance to upgrade.',
          'Click "Switch" in the version section and select the target version.',
          'After switching, restart the instance as prompted so the new version takes effect.',
        ],
      },
      {
        title: 'Troubleshoot an Abnormal Instance',
        steps: [
          'Go to "Instances" and open the detail page of an instance with "Abnormal" status.',
          'Check the failure stage and error cause in the "Overview" tab.',
          'Switch to the "Runtime" tab to inspect node and container status.',
          'Restart the instance based on the cause, or resolve underlying dependencies (e.g., missing new-api credentials for the organization) before restarting.',
        ],
      },
    ],
  },
  // Org Admin: full authority within their own organization; all operations are strictly scoped to it.
  org_admin: {
    roleLabel: 'Org Admin',
    summary: 'Org Admins manage members, instances, knowledge bases, account balance, and audit logs within their own organization. All operations are scoped to that organization. Day-to-day work includes onboarding employee accounts and dedicated instances, maintaining the organization knowledge base, and monitoring balance and usage.',
    sections: [
      {
        title: 'Overview',
        items: [
          'The default home page for Org Admins, showing the overall operational status of the organization.',
          'Includes member count, instance count, running/abnormal instance distribution, current balance, and today\'s token consumption.',
          'Also shows the organization\'s usage trend and instance status distribution charts.',
        ],
      },
      {
        title: 'Members',
        items: [
          'Manage all members in the organization: add, edit profiles, enable/disable, delete, and reset passwords.',
          'When adding a member, you can simultaneously initialize a dedicated instance for them (Onboard), so the employee has a ready-to-use instance upon first login.',
          'Management scope is limited to this organization\'s members; members of other organizations are not visible.',
        ],
      },
      {
        title: 'Instances',
        items: [
          'Manage all instances in the organization; open an instance detail to access the Overview, Tasks, Scheduled Jobs, Channels, Knowledge Base, Workspace, and Audit tabs.',
          'Supported actions: start / stop / restart, switch assistant version, and bind third-party channels.',
          'When a member\'s instance is abnormal, you can assist in troubleshooting and restarting it here.',
        ],
      },
      {
        title: 'Organization Knowledge Base',
        items: [
          'Maintain shared documents for the organization: upload, re-parse, and search.',
          'All members in the organization can read these documents, but only Org Admins can write or re-parse them.',
          'After uploading, wait for parsing to complete before the agent can retrieve the content; re-parse if parsing fails.',
        ],
      },
      {
        title: 'Usage',
        items: [
          'View aggregated usage for the organization, with the ability to drill down to per-member and per-instance detail.',
          'Use this to verify each member\'s or instance\'s token consumption; data is sourced in real time from new-api.',
        ],
      },
      {
        title: 'Account Balance',
        items: [
          'View the organization\'s current balance and historical top-up records.',
          'Balance is real-time data sourced directly from new-api; no local caching.',
          'Top-ups are performed by Platform Admins — contact the platform side when the balance is insufficient.',
        ],
      },
      {
        title: 'Audit',
        items: [
          'Review records of high-risk operations within the organization for accountability and investigation.',
          'Includes the executor and outcome of key actions such as member management, instance operations, and knowledge base writes.',
        ],
      },
    ],
    guides: [
      {
        title: 'Add a Member and Provision a Dedicated Instance',
        steps: [
          'Go to "Members" from the left-side menu.',
          'Click "Add Member".',
          'Fill in the member\'s account, display name, initial password, and select a role (typically Org Member).',
          'Confirm instance initialization (Onboard); the system will create a dedicated instance for the member.',
          'After submission, hand the account and initial password to the employee — they can log in and use their instance immediately.',
        ],
      },
      {
        title: 'Manage an Instance',
        steps: [
          'Go to "Instances" and select the target instance to open its detail page.',
          'In "Overview", check the running status and assistant version; you can start / stop / restart from here.',
          'In "Channels", bind external conversation entry points; in "Scheduled Jobs", configure scheduling; in "Knowledge Base", maintain instance-specific materials.',
          'To upgrade capabilities, click "Switch" to change the assistant version and restart as prompted.',
        ],
      },
      {
        title: 'Maintain the Organization Knowledge Base',
        steps: [
          'Go to "Organization Knowledge Base".',
          'Upload documents and wait for the parsing status to become "Complete".',
          'If parsing fails, click "Re-parse".',
          'Once parsing is complete, members and instances within the organization can retrieve these materials.',
        ],
      },
      {
        title: 'Reset a Member\'s Password or Deactivate a Member',
        steps: [
          'Go to "Members" and locate the target member.',
          'Select "Reset Password" from the member\'s action menu and hand the new password to the employee.',
          'When an employee leaves or needs to be frozen, select "Deactivate" to suspend their access; delete the account if necessary.',
        ],
      },
    ],
  },
  // Org Member: manages only their own assigned instance and personal knowledge base;
  // the left menu flattens instance sections directly.
  org_member: {
    roleLabel: 'Org Member',
    summary: 'Org Members manage only the instance assigned to them and their personal knowledge base. They can view the organization\'s shared knowledge base and their own usage. Day-to-day work includes configuring their instance, uploading personal materials, and using the task board and scheduled jobs to drive the agent.',
    sections: [
      {
        title: 'My Instance',
        items: [
          'The left-side menu directly exposes all sections of your instance — no need to navigate through an instance list first.',
          'You can only see the single instance assigned to you.',
          'If no instance has been assigned yet, the relevant entries will display an empty state page.',
        ],
      },
      {
        title: 'Overview',
        items: [
          'View your instance\'s basic information, running status, API Key status, and current assistant version.',
          'When the instance is abnormal, this page shows the failure stage and cause, and provides a "Re-initialize" entry.',
        ],
      },
      {
        title: 'Tasks',
        items: [
          'A task board for organizing work items you want the agent to handle into cards.',
          'You can create tasks, add comments, mark tasks as complete, or flag them as blocked.',
        ],
      },
      {
        title: 'Scheduled Jobs',
        items: [
          'Configure periodic or scheduled tasks for your instance.',
          'You can create, modify, enable/disable, and delete schedules.',
        ],
      },
      {
        title: 'Channels',
        items: [
          'View and bind third-party channels to connect your instance to external conversation entry points.',
          'Once bound, you can interact with your agent through those external channels.',
        ],
      },
      {
        title: 'Personal Knowledge Base',
        items: [
          'Upload, re-parse, and search knowledge base files belonging to your own instance.',
          'Uploaded materials will be retrieved and referenced by the agent during conversations; they take effect only after parsing is complete.',
        ],
      },
      {
        title: 'Workspace',
        items: [
          'View and download files in the instance\'s workspace directory to see what the agent has produced.',
        ],
      },
      {
        title: 'Organization Knowledge Base',
        items: [
          'Read-only access to shared documents distributed by the organization; uploading or modifying is not available here.',
          'If you need to add organization-level materials, contact your Org Admin.',
        ],
      },
      {
        title: 'Usage',
        items: [
          'View your personal token consumption and call statistics; data is sourced in real time from new-api.',
        ],
      },
    ],
    guides: [
      {
        title: 'Get Started with My Instance',
        steps: [
          'After logging in, you land on "Overview" by default — confirm the instance status is normal.',
          'Go to "Channels" and bind an external conversation entry point.',
          'Go to "Personal Knowledge Base" and upload materials you want the agent to reference.',
          'Once the materials finish parsing, you can start conversing with the agent through your channel.',
        ],
      },
      {
        title: 'Create a Scheduled Job',
        steps: [
          'Go to "Scheduled Jobs".',
          'Create a new job, set the schedule rule and the content to execute.',
          'After saving, you can enable, disable, or delete the job at any time.',
        ],
      },
      {
        title: 'Collaborate on the Task Board',
        steps: [
          'Go to "Tasks".',
          'Create a new task card describing what you want the agent to handle.',
          'Add comments on the card to provide additional context.',
          'Mark the task as complete when done, or flag it as blocked when you hit a blocker.',
        ],
      },
      {
        title: 'What to Do When the Instance Shows as Abnormal',
        steps: [
          'Check the failure stage and error cause in "Overview".',
          'Try clicking "Re-initialize" to rebuild the instance\'s runtime environment.',
          'If it remains abnormal, share the error message shown in Overview with your Org Admin for assistance.',
        ],
      },
    ],
  },
}

// getHelpManual 按角色与语言取对应手册。
// role 允许任意字符串：后端未来新增角色或取值异常时，统一降级到企业成员手册（权限最小的视图），
// 避免抽屉因未知角色而空白。
// locale 参数决定语言版本：'en' 返回英文，其他值（含未传）均返回中文。
export function getHelpManual(role: string | undefined | null, locale?: string): HelpManual {
  // 根据 locale 选择对应语言的手册数据集；未传或非 'en' 均使用中文。
  const manuals = locale === 'en' ? HELP_MANUALS_EN : HELP_MANUALS_ZH
  if (role && role in manuals) {
    return manuals[role as Role]
  }
  return manuals.org_member
}
