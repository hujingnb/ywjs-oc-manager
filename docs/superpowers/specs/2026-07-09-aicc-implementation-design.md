# AICC（AI Contact Center）实现设计

- 日期：2026-07-09
- 状态：设计确认稿
- 关联需求：`docs/superpowers/specs/2026-07-08-online-customer-service-design.md`

## 1. 命名与范围

本子系统统一使用 **AICC** 作为代码、目录、表、API、前端路由和 i18n key 的业务关键字。AICC 表示 **AI Contact Center**，中文业务名为「AI 客服中心」或「智能接待中心」。

选择 `aicc` 的原因：

- 不限定为文字客服智能体，后续可覆盖语音客服、电话接待等多渠道能力。
- 足够短，适合表名、API path、前端路由和文件名前缀。
- 避免与现有 `app`、`conversation`、`assistant`、`web_publish` 等概念混淆。

本设计覆盖完整产品目标，但实施上必须拆成可验证阶段，不把所有能力塞进一个不可评审的大提交。

## 2. 已确认产品决策

- 实现完整产品目标，按阶段交付。
- 每个 AICC 智能体自动创建一个隐藏 app/hermes 运行时，企业管理员只看到 AICC 智能体。
- 第 10 节开放问题不照单全收，按本次确认结果落地。
- 深度品牌定制：支持访客聊天页和挂件的高级外观配置，但必须隔离和限制。
- 访客端支持文字和图片上传，不支持通用文件。
- 必填线索字段会阻断继续咨询，阻断必须由服务端强制执行。
- 隐私说明按智能体配置：仅展示，或必须同意后才能开始对话。
- 会话、图片和线索保留期按智能体配置，默认 180 天，到期自动清理。
- 新线索做站内未读/新线索标记，不做邮件、Webhook 或 CRM 推送。
- 不设置每日消费硬上限，依靠域名白名单、限流、单会话消息数和企业余额兜底。
- 不做匿名跨会话追踪；按访客主动提交的联系方式做线索去重。
- 智能体答不上来时展示兜底话术，并引导留资。

## 3. 架构

采用「manager 管控 + 隐藏 app/hermes 应答」的三层结构。

1. manager AICC 子系统是唯一公网业务入口，负责平台开通、智能体配置、投放入口、域名白名单、深度样式配置、访客会话状态、线索、反馈、统计、导出、限流和数据保留。
2. 每个 AICC 智能体自动创建一个隐藏 app。隐藏 app 复用现有 app 初始化、RAGFlow 知识库、new-api key、余额和用量链路。
3. hermes 负责真实 AI 对话。访客消息先到 manager，manager 完成域名、限流、隐私同意和必填留资校验后，把消息转发给对应隐藏 app 的 hermes，再把回答写入 AICC 会话记录并返回访客端。

关键边界：

- hermes 不直接暴露给匿名访客。
- manager 必须保存 AICC 运营数据，否则线索、反馈、未解决率、来源页、导出和保留期无法稳定实现。
- 隐藏 app 默认不出现在普通 app 列表；如运维需要展示，必须带明确 `aicc` 标识并避免企业管理员误操作。

## 4. 数据模型

### 4.1 组织开通字段

在 `organizations` 扩展：

- `aicc_enabled`：是否开通 AICC，默认关闭。
- `aicc_agent_limit`：智能体数量上限，允许为空表示不限。

关闭 AICC 后，该企业所有公开入口立即返回「客服已下线」。

### 4.2 AICC 主表

- `aicc_agents`：智能体主配置。字段包括组织、隐藏 `app_id`、名称、状态、场景说明、开场白、回答边界提示、隐私模式、保留天数、主题配置、域名白名单、独立链接 token、挂件 token、软删除时间等。
- `aicc_agent_knowledge`：智能体知识库选择。记录企业库、行业库和专属文档范围。专属文档优先复用隐藏 app 的 app 知识库能力，避免新增 RAGFlow scope。
- `aicc_sessions`：公开会话。字段包括智能体、渠道类型、来源 URL、referrer、地域、IP hash、user-agent 摘要、隐私展示/同意状态、解决状态、留资完成状态、最后活跃时间、过期时间。
- `aicc_messages`：访客消息和 AI 回答镜像。字段包括方向、内容类型、文本、图片文件引用、hermes message id、兜底/拒答标记、token 或错误摘要。
- `aicc_lead_fields`：每个智能体的留资字段配置，包含字段名称、类型、必填、展示顺序和提问文案。
- `aicc_lead_values`：会话内提交的原始字段值。
- `aicc_leads`：按企业和联系方式归并后的线索主记录，带未读状态和最近会话信息。
- `aicc_feedback`：每条回答的有帮助/没帮助反馈，并驱动会话解决状态更新。
- `aicc_blocklist`：后续增强项，用于记录组织或智能体维度的 IP hash / 指纹封禁。

普通限流使用 Redis，不把每次限流计数写入数据库。

### 4.3 图片对象

访客图片上传走对象存储，DB 只保存对象键、mime、大小、所属会话和过期时间。下载或转发 hermes 前必须校验 session token 与对象归属。

## 5. 权限

所有权限谓词放在 `internal/auth/authorizer.go`，service 和 handler 不内联角色判断。

- `CanManageAICCConfig(p)`：仅平台管理员可开通/关闭企业 AICC 能力、设置智能体数量上限。
- `CanManageAICCAgent(p, orgID)`：仅本企业 `org_admin` 可创建、编辑、启停、删除、配置投放和查看本企业 AICC 数据。
- `CanViewAICC(p, orgID)`：本企业 `org_admin` 可查看会话、线索和统计；平台管理员保留跨组织只读排障能力，但前端默认不提供业务入口。

企业普通成员没有 AICC 子系统入口，也不能访问 AICC 管理 API。

## 6. API 设计

### 6.1 管理端 API

管理端 API 使用登录态 JWT。

- `PATCH /api/v1/organizations/:orgId/aicc-config`：平台开通、关闭、设置数量上限。
- `GET /api/v1/aicc/agents`：企业管理员查看智能体列表。
- `POST /api/v1/aicc/agents`：创建智能体并自动创建隐藏 app。
- `GET /api/v1/aicc/agents/:agentId`：查看智能体详情。
- `PATCH /api/v1/aicc/agents/:agentId`：编辑智能体。
- `DELETE /api/v1/aicc/agents/:agentId`：软删除智能体。
- `POST /api/v1/aicc/agents/:agentId/start`：启用公开入口。
- `POST /api/v1/aicc/agents/:agentId/stop`：停用公开入口。
- `GET /api/v1/aicc/agents/:agentId/deployments`：查看独立链接、挂件代码、域名白名单和样式配置。
- `PATCH /api/v1/aicc/agents/:agentId/deployments`：更新投放配置。
- `GET /api/v1/aicc/agents/:agentId/lead-fields`：查看留资字段配置。
- `PATCH /api/v1/aicc/agents/:agentId/lead-fields`：更新留资字段配置。
- `GET /api/v1/aicc/agents/:agentId/sessions`：查看智能体会话列表。
- `GET /api/v1/aicc/sessions/:sessionId`：查看会话详情。
- `GET /api/v1/aicc/leads`：查看线索列表。
- `GET /api/v1/aicc/leads/export`：导出 CSV。
- `GET /api/v1/aicc/analytics`：统计看板。

修改 handler 签名、DTO、响应结构或路由后，必须运行 `make openapi-gen` 和 `make web-types-gen`。

### 6.2 访客公开 API

公开 API 不使用登录 JWT，使用 `public token + session token + Origin/Referer 校验 + 限流`。

- `GET /api/v1/public/aicc/agents/:publicToken/config`：加载聊天页或挂件公开配置。
- `POST /api/v1/public/aicc/agents/:publicToken/sessions`：创建或恢复会话，记录来源页、隐私展示状态。
- `POST /api/v1/public/aicc/sessions/:sessionToken/consent`：记录隐私同意。
- `POST /api/v1/public/aicc/sessions/:sessionToken/images`：上传访客图片。
- `POST /api/v1/public/aicc/sessions/:sessionToken/messages`：发送文字或图片消息。
- `POST /api/v1/public/aicc/sessions/:sessionToken/lead-values`：提交线索字段。
- `POST /api/v1/public/aicc/messages/:messageId/feedback`：提交有帮助/没帮助反馈。

`publicToken` 只用于定位智能体，不授予管理权限；`sessionToken` 是高熵随机值，只能访问单个会话。

## 7. 运行流程

### 7.1 创建智能体

1. 校验企业已开通 AICC。
2. 校验 `aicc_agent_limit`。
3. 创建隐藏 app，继承企业默认 app 知识库配额。
4. 走现有 app 初始化链路创建 new-api token、启动 hermes runtime。
5. 写入 `aicc_agents.app_id`。
6. 若隐藏 app 创建失败，回滚 AICC 智能体创建，避免孤儿配置。

### 7.2 访客聊天

1. 访客从独立链接或 iframe 挂件进入。
2. manager 校验智能体状态、企业 AICC 开通状态、token、域名白名单和限流。
3. 创建或恢复 `aicc_sessions`。
4. 根据隐私模式展示说明或要求同意。
5. 若存在未完成必填线索字段，服务端拒绝继续聊天并返回需要填写的字段。
6. manager 接收文字或图片消息，写入 `aicc_messages`。
7. manager 将消息转发到隐藏 app 的 hermes conversation API。
8. manager 写入 AI 回答镜像，并返回访客端。
9. 答不上来时标记未解决候选，展示兜底话术并引导留资。

## 8. 前端与投放

管理后台新增 `/aicc` 模块，仅对已开通 AICC 的企业管理员显示入口。平台管理员在企业管理页维护 `aicc_enabled` 和 `aicc_agent_limit`。

企业管理员 AICC 页面包含：

1. 智能体列表和配置：创建、编辑、启停、软删，展示状态、投放入口、今日会话和未读线索。
2. 投放配置：独立链接、二维码、网页挂件代码、允许域名、深度样式配置。
3. 会话历史：按智能体、时间、解决状态和关键词筛选，详情展示消息、图片、来源、地域、留资和反馈。
4. 线索与统计：线索列表、未读标记、去重提示、CSV 导出、会话趋势、热门问题、未解决率、地域、来源页、留资转化。

样式方向：

- 后台首页采用运营控制台风格，信息密度高，便于日常看会话、线索和转化。
- 新建/编辑智能体采用分步配置向导，降低首次配置成本。
- 访客独立聊天页和 iframe 挂件采用品牌接待风格，强调头像、主题色、欢迎语和企业品牌一致性。

访客入口：

- 独立聊天页：`/aicc/chat/:publicToken`，适合链接和二维码。
- 网页挂件：企业复制 `<script>` 嵌入代码。脚本创建 iframe，把聊天 UI 隔离在 iframe 内，宿主页通过 URL/referrer 传来源信息。

深度样式配置默认保存受控配置：主题色、头像、字体族白名单、圆角、窗口尺寸、入口位置、自定义文案、CSS 变量。如确需自定义 CSS，只允许作用在 iframe 内的 AICC DOM，做长度限制和危险规则过滤。

## 9. 安全与合规

- 访客端所有请求必须通过 manager 公共 API。
- 挂件入口校验 `Origin`/`Referer` 与智能体域名白名单；独立链接不要求域名白名单。
- 图片上传限制为 JPEG、PNG、WebP、GIF，设置单张大小上限。
- 访客不能触发任何业务操作，只能发送消息、上传图片、提交线索和反馈。
- 自定义样式通过 iframe 隔离，限制 CSS 长度、作用域和危险关键字。
- manager 在转发 hermes 前注入 AICC 专用安全约束：只回答企业业务和知识库范围，拒绝提示词套取、无关任务和操作型请求。
- 企业可配置的场景说明不能覆盖平台安全约束。

## 10. 限流与消费保护

使用 Redis 做多维限流：

- IP hash。
- session token。
- agent。
- org。

限制项包括：

- 创建会话频率。
- 发送消息频率。
- 单会话消息数。
- 图片上传频率和大小。

本期不做每日消费硬上限。余额耗尽时沿用 new-api/app runtime 的失败表现，manager 将该次回答标为失败并提示客服暂不可用。

## 11. 数据保留

每个智能体配置 `retention_days`，默认 180 天。定时任务按智能体清理：

- 过期 `aicc_sessions`。
- 关联 `aicc_messages`。
- 图片对象。
- 会话关联的 `aicc_lead_values`。
- `aicc_feedback`。

去重后的 `aicc_leads` 若仍被其他未过期会话引用则保留；没有任何未过期会话引用时删除，避免联系方式长期脱离会话保留期继续保存。清理行为写审计日志，后台明确提示「到期后不可恢复」。

## 12. 分阶段实施

1. 基础设施与开通控制：迁移、sqlc 查询、authorizer 谓词、平台企业管理页、OpenAPI 同步。
2. 智能体管理与隐藏 app 编排：创建、列表、详情、启停、软删、知识库和留资字段配置，隐藏 app 过滤。
3. 访客运行时：公开配置、会话创建/恢复、隐私同意、文字聊天转发 hermes、图片上传、必填留资阻断、答不上来引导留资、反馈。
4. 投放与样式：独立聊天页、二维码、iframe 挂件、域名白名单、深度样式配置与隔离。
5. 运营数据：会话历史、线索去重和未读、CSV 导出、统计看板、热门问题基础归类。
6. 治理任务：保留期清理、图片对象清理、限流配置固化、审计日志补齐。

## 13. 测试策略

Go 单元测试：

- authorizer 权限。
- service 创建、编辑、启停、删除。
- 数量上限。
- 隐藏 app 创建失败回滚。
- 必填留资阻断。
- 隐私同意 gate。
- 域名白名单。
- session token 校验。
- 反馈更新解决状态。
- 保留期清理。

Handler 测试：

- 管理端鉴权。
- 公开 API token、域名和限流错误映射。
- CSV 导出响应。

迁移和 sqlc 测试：

- 表结构。
- 索引。
- 外键。
- 软删除。
- 保留期查询。

前端测试：

- 路由入口可见性。
- AICC 表单。
- 投放配置。
- 会话详情。
- 线索列表。
- 访客聊天状态机。

E2E 浏览器验证：

- 平台开通企业 AICC。
- 企业管理员创建智能体并生成独立链接。
- 访客同意隐私、发送消息、上传图片、填写必填线索、反馈「没帮助」。
- 后台看到会话、线索和统计变化。

所有新功能完成后必须用真实浏览器验证，不能用 curl 替代前端验证。
