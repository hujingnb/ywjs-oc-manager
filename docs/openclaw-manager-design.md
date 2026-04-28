# OpenClaw Manager 产品需求与设计文档

日期：2026-04-28

## 修订记录

- 2026-04-29：模型调整为账号即客户端——应用创建路径收敛到组织管理员创建成员账号，每个成员账号最多一个 OpenClaw 应用；新增应用工作目录的浏览/下载入口；新增平台默认 AI 指令模板与三层 prompt 拼接顺序；微信绑定泛化为渠道绑定（第一版仅支持微信）。
- 2026-04-29（第二轮）：删除成员级预算（先不做成员级别限制）；删除知识库 DB 表，改为组织级 + 应用级双层目录，filesystem 即事实来源；删除 usage_snapshots 缓存表，所有用量直查 new-api；删除 `apps.workspace_path` 字段，路径由代码推算；删除 `local` 节点概念，所有节点走完整 runtime node agent 注册机制；agent 在节点上以代理模式暴露 Docker（manager 用 Docker SDK 直连）并提供文件 API；manager 不直接管理任何容器或节点文件，全部经由对应 runtime node。

## 1. 背景与目标

OpenClaw Manager 是一个面向组织的在线管理后台。用户可以在页面中创建 OpenClaw 应用，将应用绑定到微信，并通过 `new-api` 按 token 或点数计费。

系统由三个核心外部组件和一个自研后台组成：

- `ollama`：本地运行 AI 模型。
- `new-api`：模型网关，负责模型配置、账号余额、api_key、token 计费、用量日志和统计。
- `OpenClaw`：每个应用独立运行的 AI 应用实例，负责渠道插件（第一版仅微信）、对话能力和知识库处理。
- `manager`：本产品要实现的管理后台，负责组织、成员、应用、Runtime node 注册与心跳、容器编排（经由节点 agent）、渠道绑定、知识库主副本与节点同步、审计和统计展示。

产品目标：

- 平台管理员可以开通组织账号，给组织充值 Token Credit，并注册和管理 Runtime Node。
- 组织管理员可以管理组织成员、组织 AI 人设、组织级知识库和组织下所有应用。
- 组织管理员在创建成员账号时同步创建该成员的 OpenClaw 应用，并指定运行节点。
- 组织成员登录后管理自己唯一的 OpenClaw 应用、应用知识库、绑定渠道账号、查看工作目录文件。
- 每个 OpenClaw 应用对应一个 Docker 容器（运行在某个 Runtime Node 上）、一个 `new-api api_key` 和最多一个渠道绑定。
- manager 展示应用、成员、组织和平台维度的 token 用量，但不承担计费事实来源。
- manager 支持容器启停、日志、资源监控、健康检查和自动重启策略。

## 2. 明确边界

manager 负责：

- 平台管理员、组织管理员、组织成员三类账号和登录。
- 组织生命周期管理。
- 组织充值 Token Credit 的后台操作记录和 `new-api` 调用编排。
- 组织成员账号管理；创建成员账号时同步创建关联 OpenClaw 应用。
- 组织级 AI 人设和成员是否允许覆盖的策略。
- 平台默认 AI 指令模板与"平台默认 → 组织 → 应用"三层 prompt 拼接。
- Runtime Node 注册、心跳、状态监控；通过 agent 间接管理每个应用对应的 Docker 容器生命周期。
- 每个应用对应的 `new-api api_key` 创建、禁用、恢复和映射。
- 渠道扫码或认证绑定流程的触发、状态展示和重试（第一版仅微信渠道，模型为通用渠道抽象）。
- 组织级与应用级双层知识库目录管理；上传/删除时同步到该组织名下应用所在的全部 runtime node。
- 应用工作目录文件的浏览、单文件下载和文件夹打包下载（通过 runtime node agent 文件 API 代理读取）。
- token 用量查询和展示（每次直查 new-api，manager 不缓存）。
- 审计日志。

manager 不负责：

- Ollama 模型安装、下载、删除、参数配置或运行状态管理。
- 大模型配置、模型密钥和模型路由，这些统一由 `new-api` 管理。
- token 价格计算、扣费、真实支付、人民币金额、发票或在线支付。
- 知识库 OCR、文本切分、embedding、向量库写入或检索。
- OpenClaw 流量代理、外部访问域名分配或统一反向代理。
- 多节点调度系统、复杂日志检索和告警平台。

## 3. 部署形态

部署分两层：

**核心层**（manager 自身机器）：

- `manager`（含 worker 与 scheduler）
- `manager-postgres`
- `redis`
- `new-api`（含其自身 PostgreSQL）
- `ollama`

核心层服务全部使用本机部署，新建的应用容器**不**跑在这层。

**节点层**（一台或多台 Runtime Node）：

- 每台节点常驻一个 `agent` 容器
- 节点上运行多个 OpenClaw 应用容器，由 manager 通过 agent 创建和管理
- 节点可与核心层在同一台机器（agent 仍走完整注册流程，与 manager 通过 HTTP 通信），也可分布在不同机器

第一版要求：

- 至少注册一个 Runtime Node，否则无法创建应用。
- 没有 `local` 直连模式；manager 不直接读节点的 Docker socket 或文件系统。
- 应用 `runtime_node_id` 必填，指定运行节点。
- 暂不实现跨节点调度、迁移和高可用，但数据模型已预留多节点能力。

## 4. 用户角色与权限

系统固定三类角色，不做完整 RBAC。

### 4.1 平台管理员

能力：

- 登录平台后台。
- 创建、启用、禁用组织。
- 为组织充值 Token Credit。
- 查看所有组织、成员、应用、容器状态和 token 用量。
- 查看全平台审计日志。
- 管理平台管理员账号。

限制：

- 不直接修改组织成员的应用内容，除非通过平台管理入口执行运维或风控操作。

### 4.2 组织管理员

能力：

- 登录本组织后台。
- 创建、禁用、重置、删除组织成员账号；创建账号时同步创建该成员名下的 OpenClaw 应用。
- 查看和管理本组织所有 OpenClaw 应用。
- 设置组织级 AI 人设。
- 设置是否允许成员覆盖应用级 AI 人设。
- 上传、删除组织级知识库文件（同步到本组织下所有应用所在的 runtime node）。
- 查看本组织 token 用量、成员用量和应用用量。
- 查看本组织审计日志。

限制：

- 不能访问其他组织数据。
- 不能给组织充值，只能查看组织余额和消耗。
- 不能管理 runtime node，节点由平台管理员注册和维护。

### 4.3 组织成员

能力：

- 登录组织后台。
- 查看和管理自己名下唯一的 OpenClaw 应用（应用由组织管理员在创建账号时建立，成员不能自助创建或删除）。
- 在权限策略允许时编辑应用级 AI 人设。
- 上传和删除自己应用的知识库文件（应用级目录，同步到该应用所在 runtime node）。
- 绑定和重试绑定渠道账号（第一版仅微信渠道）。
- 启动、停止、重启自己的应用容器。
- 浏览和下载自己应用工作目录中的文件（OpenClaw 输出的 PDF/Word/图片等，由 runtime node agent 提供）。
- 查看自己的应用状态、日志和 token 用量。

限制：

- 不能创建或删除应用。
- 不能查看其他成员应用，除非其角色是组织管理员。
- 不能编辑组织级知识库（仅可读）。
- 不能访问组织级配置。
- 不能向工作目录写入文件（写入只能由 OpenClaw 容器进程完成）。

权限校验必须由后端 API 执行。前端菜单和按钮隐藏只作为体验优化。

## 5. 核心对象模型

### 5.1 Organization

组织代表一个租户、客户、学校或机构。

字段建议：

- 组织 ID
- 组织名称
- 状态：启用、禁用
- 联系人
- 联系方式
- 备注
- `new-api` 账号 ID
- Token Credit 余额展示字段
- 创建时间
- 更新时间

关系：

- 一个组织对应 `new-api` 中的一个账号。
- 一个组织拥有多个成员。
- 一个组织拥有多个 OpenClaw 应用。

### 5.2 Member

成员是组织内的后台账号。

字段建议：

- 成员 ID
- 组织 ID
- 登录账号
- 密码哈希
- 显示名称
- 角色：组织管理员、组织成员
- 状态：启用、禁用
- 最近登录时间
- 创建时间
- 更新时间

成员不映射 `new-api` 账号。第一版不做成员级别预算限制；token 用量按需向 new-api 查询展示。

### 5.3 OpenClaw App

OpenClaw 应用是产品核心运营单元。

字段建议：

- 应用 ID
- 组织 ID
- 所属成员 ID（owner，每个未删除成员账号最多对应一个未删除应用）
- 应用名称
- 描述
- 应用状态
- AI 人设来源：组织继承、应用覆盖
- 应用级 AI 人设内容
- Runtime Node ID
- Docker 容器 ID
- `new-api api_key` ID
- api_key 状态
- 渠道绑定状态
- 工作目录路径
- 创建时间
- 更新时间
- 删除时间

每个应用对应：

- 一个所属成员账号（一对一强绑定）。
- 一个 Docker 容器。
- 一个 `new-api api_key`。
- 最多一个渠道账号绑定（第一版仅微信渠道）。
- 一份应用私有知识库。
- 一个工作目录，存放 OpenClaw 输出的生成文件。

### 5.4 Runtime Node

运行节点。每个节点是一台运行 OpenClaw 容器的物理或虚拟机器，节点上常驻一个 agent 容器，agent 负责对外暴露本机 Docker 与文件 API，manager 通过 agent 进行所有节点级操作。

字段建议：

- 节点 ID
- 节点名称（如 `node-shanghai-1`）
- 状态：待注册、活跃、不可达、禁用
- agent Docker 代理 endpoint（agent 暴露的 Docker API URL）
- agent 文件 API endpoint（agent 暴露的文件管理 URL）
- agent 自签 CA 证书（manager 用于校验 agent TLS）
- 一次性注册令牌（bootstrap token，注册成功后清空）
- bootstrap token 过期时间
- 长期通信令牌（agent token，hash 存储）
- agent 版本
- 心跳间隔（秒）
- 最近心跳时间
- 资源摘要（agent 上报的 CPU/内存/磁盘/容器数）
- 节点元数据（OS、内核、Docker 版本）
- agent 数据根目录（agent 在节点上的存储路径）
- 注册时间
- 创建时间
- 更新时间

第一版没有 `local` 节点的概念，manager 自己所在机器若要承担 OpenClaw 运行，也必须装 agent 并完成注册。

通信模型：

- manager → agent Docker 代理：用 Docker SDK 连 agent 暴露的 HTTP 端口，agent 内部转发到本机 Docker socket。
- manager → agent 文件 API：HTTP，由 agent 强制路径沙箱（仅允许操作受管 scope 的根目录内）。
- agent → manager：周期性心跳上报状态。
- 所有 agent 接口认证用 agent_token（Bearer），manager 必须验证 agent TLS 证书。

### 5.5 Channel Binding

渠道绑定记录。第一版仅支持微信，但表结构和接口按通用渠道抽象设计，未来可扩展 Telegram、企业微信、飞书、Web 聊天等。

字段建议：

- 绑定 ID
- 应用 ID
- 渠道类型（第一版固定为 `wechat`）
- 状态：未绑定、待认证、已绑定、绑定失败、凭证失效、用户主动解绑
- 渠道账号标识（如微信 wxid，语义由渠道类型决定）
- 渠道显示名
- 渠道元数据（JSON，存放二维码 payload、过期时间等渠道特有字段）
- 最近在线时间
- 绑定时间
- 错误信息

每个应用最多一个渠道绑定（第一版限制）。未来若支持单应用挂多渠道，约束改为按渠道类型唯一即可，无需迁移数据。

### 5.6 Knowledge Base（无 DB 表，目录即事实来源）

知识库不再使用数据库表。manager 在本地以目录形式存放主副本，并在容器创建/知识库变更时把文件同步到目标 runtime node。

层级：

- **组织级知识库**：组织管理员维护，组织下所有应用都能读到。manager 本地路径 `{data_root}/orgs/{org_id}/knowledge/`，节点上 `{node_data_root}/orgs/{org_id}/knowledge/`，挂载到容器内 `/knowledge/org/`。
- **应用级知识库**：应用所属成员（或组织管理员）维护，仅本应用可见。manager 本地路径 `{data_root}/apps/{app_id}/knowledge/`，节点上 `{node_data_root}/apps/{app_id}/knowledge/`，挂载到容器内 `/knowledge/app/`。

manager 不解析文件内容；OpenClaw 通过系统提示词得知挂载位置，自行读取文件。

操作流程：

- 上传：manager 校验权限/类型/大小 → 写本地主副本 → 调用受影响节点 agent 的文件 API 同步过去 → 写审计。
- 删除：manager 删本地主副本 → 调用受影响节点 agent 的文件 API 删除 → 写审计。
- 容器创建时：manager 把主副本里的 org 与 app 知识库一次性 tar 流发给目标节点的 agent 解压，然后才创建容器并 bind mount 这两个目录。

同步策略（第一版）：

- 应用级知识库：节点单一，**同步推送**，全部成功才返回。
- 组织级知识库：节点可能多个，**异步推送**（由 `knowledge_sync_node` job 重试到一致），manager API 主副本写完即返回；后台展示每节点同步状态。

文件元信息（上传者、原始文件名、上传时间、大小）走审计日志，不再单独建表。审计 target_type 用 `org_knowledge` / `app_knowledge`，target_id 用路径标识（如 `org:{org_id}:filename.pdf`）。

### 5.7 Audit Log

审计日志记录敏感操作。

字段建议：

- 日志 ID
- 操作人 ID
- 操作人角色
- 组织 ID
- 目标类型
- 目标 ID
- 动作
- 结果：成功、失败
- 错误信息
- 来源 IP
- 创建时间

## 6. 系统关系

```text
Organization 1 ── 1 new-api account
Organization 1 ── N Members
Organization 1 ── 1 OrgKnowledgeDir      # 文件系统目录，无 DB 行
Member 1 ── 0..1 OpenClaw App            # 1:1 强绑定，由组织管理员创建账号时建立
OpenClaw App 1 ── 1 Docker Container     # 容器在 runtime node 上，通过 agent 操作
OpenClaw App 1 ── 1 new-api api_key
OpenClaw App 1 ── 0..1 Channel Binding   # 第一版仅微信
OpenClaw App 1 ── 1 AppKnowledgeDir      # 文件系统目录，无 DB 行
OpenClaw App 1 ── 1 WorkspaceDir         # 节点上目录，通过 agent 访问
OpenClaw App N ── 1 Runtime Node
Runtime Node 1 ── 1 Agent                # 节点上常驻容器，提供 Docker 代理 + 文件 API
```

## 7. 账号与应用创建流程

应用不再由成员自助创建，也没有独立的"应用创建向导"。组织管理员在创建成员账号时**强制配套创建应用**，账号与应用一对一强绑定。

### 7.1 创建表单

组织管理员在"创建成员"页面填写：

- 成员账号信息：登录名、显示名、初始密码、角色（默认 `org_member`）
- 应用名称（默认与成员显示名相同）
- 应用描述（可选）
- AI 人设来源：组织继承（默认）/ 应用覆盖（仅当组织允许成员覆盖时可选）
- 若选择应用覆盖，填写应用级人设
- 初始知识库文件（可选，可后续在应用详情继续上传）
- 渠道类型（第一版固定为微信）

### 7.2 后端流程

提交表单后，后端在同一事务里：

1. 创建 `users` 行（成员账号）。
2. 创建 `apps` 行，状态置为 `draft`，`owner_user_id` 指向新成员。
3. 创建空 `channel_bindings` 行（`channel_type=wechat`，`status=unbound`）。
4. 写复合审计日志（创建账号 + 应用）。

事务提交后立即入队 `app_initialize` job，由 worker 异步：

1. 校验目标 runtime node 处于 `active` 状态，否则任务进入 error 待管理员介入。
2. 在组织对应 `new-api` 账号下创建应用专用 api_key。
3. 通过 agent 文件 API 在目标节点上创建应用相关目录（`apps/{app_id}/knowledge/`、`workspace/`、`state/`、`logs/`），并把 manager 本地主副本中的 org + app 知识库 tar 流推送到 agent 解压。
4. 渲染拼接 prompt：平台默认模板（注入工作目录、组织/应用知识库目录等变量）→ 组织人设 → 应用人设（若 `persona_mode = app_override`）。
5. 通过 agent 暴露的 Docker 代理创建容器（bind mount 节点上的各目录到容器路径，注入环境变量与拼接后的 prompt，启用应用绑定的渠道插件）。
6. 启动容器并检查 OpenClaw 健康状态。
7. 应用状态置为 `binding_waiting`。

创建表单提交后页面跳转到应用详情页，等待 worker 推进状态。

### 7.3 知识库上传

支持文件类型：

- PDF
- Word
- 图片
- 试卷文件

manager 校验：

- 文件类型
- 文件大小
- 上传人权限（应用级仅本应用 owner 或组织管理员；组织级仅组织管理员）
- 目标应用或组织归属

写入流程：先写 manager 本地主副本，再调对应 runtime node 的 agent 文件 API 同步：

- 应用级：同步推送到该应用所在节点，全部成功才返回。
- 组织级：主副本写完即返回；后台异步将变更推送到该组织所有应用所在节点（`knowledge_sync_node` job）。

可在管理员创建账号表单中预上传应用级文件；也可在应用详情或组织设置中继续上传或删除。容器创建前已上传的应用级文件会作为初始知识库随容器创建一起 tar 流推送到节点。

### 7.4 渠道绑定

应用初始化完成后，由管理员或成员在应用详情页"渠道绑定"模块手动触发：

- 调用 `POST /apps/{appId}/channels/{channelType}/login`（第一版 `channelType=wechat`）。
- manager 在容器内执行 OpenClaw 渠道插件登录命令。
- 后端返回 `AuthChallenge`，包含认证类型（第一版仅 `qr_code`）和 payload。
- 前端按 `AuthChallenge.type` 渲染：扫码组件（微信）、跳转外部授权（OAuth，未来）、填写 token（未来）。
- 后台轮询绑定状态，更新 `channel_bindings.status`。
- 用户完成认证后，绑定成功，状态置 `bound`。

绑定失败：容器和 api_key 保留，状态置 `failed` 或 `expired`，允许重试。

### 7.5 中途中断与恢复

- `app_initialize` job 失败：应用进入 `error` 状态，保留所有已创建资源，允许管理员重试。
- 容器创建失败：禁用或清理已创建的 api_key。
- 容器启动失败：保留 api_key，标记 `error`，允许重试。
- 渠道绑定失败：保留容器和 api_key，状态置 `binding_failed` 或 `failed`，允许重试。
- worker 中断：job 标记可重试，重启后由 reconciler 恢复执行。

应用没有独立"发布"步骤——`app_initialize` 成功且渠道绑定 `bound` 后即进入 `running` 状态。

## 8. 应用状态

应用状态建议：

- `draft`：账号与应用记录已创建，尚未初始化 api_key 和容器。
- `initializing`：正在创建 api_key、容器或写入配置。
- `binding_waiting`：容器已启动，等待用户完成渠道认证（第一版为微信扫码）。
- `binding_failed`：渠道认证过期、失败、插件错误或登录命令失败。
- `running`：渠道已绑定且容器运行中（无独立 ready/publish 步骤）。
- `stopped`：应用存在但容器停止（含管理员/成员主动停止、所属账号被禁用、所属节点被禁用或不可达）。
- `error`：容器异常、配置错误或 OpenClaw 健康检查失败。
- `deleted`：应用软删除，容器已删除，api_key 已禁用，工作目录归档。

应用是否有可用 api_key 由 `apps.api_key_status` 单独反映（`active` / `disabled` / `error`），不进入应用主状态机；前端综合两者展示"运行中但 api_key 已禁用"提示。

## 9. 创建与删除的补偿策略

资源创建顺序：

1. 创建成员账号 + 应用草稿（同一事务，应用绑定到指定 runtime node）。
2. 创建 `new-api api_key`。
3. 通过 agent 文件 API 在节点上创建应用目录并推送 org + app 知识库主副本。
4. 渲染拼接 prompt（平台默认 + 组织 + 应用）。
5. 通过 agent Docker 代理创建容器（bind mount 节点目录，注入环境变量和 prompt，启用渠道插件）。
6. 启动容器并执行健康检查。
7. 用户在应用详情页触发渠道绑定。

失败处理：

- api_key 创建失败：应用停留在 `draft` 或 `error`，允许管理员重试 `app_initialize`。
- 节点不可达或 agent 文件 API 失败：应用置 `error`，可重试；不能用其他节点替代（应用已绑节点）。
- 容器创建失败：禁用已创建的 api_key 并记录错误。
- 容器启动失败：保留 api_key，标记 `error`，允许重试。
- 渠道绑定失败：保留容器和 api_key，状态置 `binding_failed`/`expired`，允许重试。
- worker 中断：job 标记可重试，重启后继续。

删除应用（联动账号删除时自动触发）：

- 采用软删除。
- 通过 agent Docker 代理停止并删除容器。
- 禁用 `new-api api_key`。
- 通过 agent 文件 API 把节点上的应用目录归档（agent 把 `apps/{app_id}/` 移到 `archived/{app_id}-{timestamp}/`），保留 N 天后由 agent 自身的清理任务物理删除（manager 通过 `workspace_archive_cleanup` job 触发或 agent 周期性执行）。
- manager 本地主副本（`apps/{app_id}/knowledge/`）一并删除。
- 保留应用记录、用量映射、审计记录和充值历史。
- 联动语义：删除账号时自动触发应用软删；禁用账号时停容器并禁 api_key（不删除）。

## 10. 计费、余额与预算

### 10.1 计费事实来源

`new-api` 是 token 计费和余额事实来源。

manager 不做：

- token 单价计算
- 实际扣费
- 模型计费规则维护
- token 明细落库

### 10.2 组织充值

平台管理员给组织充值 Token Credit。

流程：

1. 平台管理员选择组织。
2. 输入充值点数和备注。
3. manager 调用 `new-api` 给组织对应账号增加额度。
4. `new-api` 成功后，manager 保存充值记录。
5. 如果 `new-api` 失败，manager 不写成功记录。

充值记录包含：

- 组织 ID
- 充值点数
- 操作人
- 操作时间
- 备注
- `new-api` 引用 ID
- 成功或失败状态

充值单位只使用 Token Credit/点数。

### 10.3 Token 统计维度

报表支持：

- 应用维度：按应用 api_key 查询。
- 成员维度：聚合成员名下应用 api_key。
- 组织维度：查询组织对应 `new-api` 账号或聚合组织下 api_key。
- 平台总量：平台管理员视角聚合所有组织。

展示字段根据 `new-api` 能力决定：

- 请求次数
- prompt tokens
- completion tokens
- total tokens
- 消耗点数
- 时间范围趋势
- 模型维度分布

统计每次请求都直查 `new-api`，manager 不维护完整 token 明细表，也不在本地缓存快照。短时间内重复查询可由 manager 进程内做轻量内存缓存（实现细节，不进 schema）。

## 11. 容器运行与运维

### 11.1 容器生命周期

每个应用对应一个独立 Docker 容器。

manager 支持：

- 创建容器
- 启动容器
- 停止容器
- 重启容器
- 删除容器
- 查看容器状态
- 查看最近日志
- 查询资源使用情况

### 11.2 容器配置

容器创建时由 manager 通过该应用所属 runtime node 的 agent Docker 代理调用 Docker SDK，注入：

- 应用 ID
- 组织 ID
- `new-api` base URL
- 应用专用 api_key
- 工作目录 bind mount：节点路径 `{node_data_root}/apps/{app_id}/workspace/` ↔ 容器路径 `/workspace`，环境变量 `OPENCLAW_WORKSPACE_DIR=/workspace`
- 组织级知识库 bind mount：节点路径 `{node_data_root}/orgs/{org_id}/knowledge/` ↔ 容器路径 `/knowledge/org`，环境变量 `OPENCLAW_KNOWLEDGE_ORG_DIR=/knowledge/org`
- 应用级知识库 bind mount：节点路径 `{node_data_root}/apps/{app_id}/knowledge/` ↔ 容器路径 `/knowledge/app`，环境变量 `OPENCLAW_KNOWLEDGE_APP_DIR=/knowledge/app`
- 状态目录 bind mount：节点路径 `{node_data_root}/apps/{app_id}/state/` ↔ 容器路径 `/state`
- 日志目录 bind mount：节点路径 `{node_data_root}/apps/{app_id}/logs/` ↔ 容器路径 `/logs`
- 拼接后的系统 prompt（平台默认 + 组织人设 + 应用人设三层，已注入路径变量）作为环境变量
- 启用的渠道插件名（如 `openclaw-weixin`）
- 健康检查端口或命令

api_key 不在页面明文展示。manager 应加密保存敏感信息（用配置文件中 `master_key` 加密），或仅保存可用于查询、禁用、恢复的 key 标识。

### 11.3 运行状态判断

应用运行状态由三类信息综合得到：

- manager 业务状态。
- Docker 容器状态。
- OpenClaw 健康检查结果。

运行状态包括：

- 运行中
- 启动中
- 已停止
- 重启中
- 错误
- 未知
- 预算受限

### 11.4 增强运维能力

后台应提供：

- 启动、停止、重启应用容器。
- 查看最近日志，支持手动刷新。
- 查看 CPU、内存、网络、磁盘使用。
- 浏览和下载应用工作目录文件（支持子目录树、面包屑、单文件下载、文件夹打包下载）。
- 设置异常退出自动重启策略。
- 健康检查失败状态和最近失败时间。
- 高风险操作确认弹窗。
- 运行操作审计日志。

不提供：

- OpenClaw 统一代理。
- 应用外部域名分配。
- 复杂日志全文搜索。
- 告警平台。
- 多节点调度。
- 工作目录写入入口（写入只能由 OpenClaw 容器进程完成）。

### 11.5 工作目录

每个应用拥有一个独立工作目录，OpenClaw 容器内进程将生成的文件（PDF、Word、Excel、图片等）输出到该目录。**工作目录在 runtime node 上**，manager 通过 agent 文件 API 代理读取。

文件结构：

- 节点上：`{node_data_root}/apps/{app_id}/workspace/`
- 容器内：`/workspace`（bind mount，实时同步）
- manager 本地：**无**（manager 不持有节点的工作目录数据）

浏览能力：

- 支持子目录树（OpenClaw 可按对话日期、任务、主题建子目录）。
- 列目录返回文件名、类型（文件/目录）、大小、修改时间。
- 单文件流式下载。
- 文件夹流式打包下载（zip）。
- 第一版不提供在线预览（PDF/Word/图片等需下载后查看）。
- 第一版不提供删除/重命名/上传入口（避免误删用户文件，且写入由 OpenClaw 负责）。

实现：

- manager 收到 `/apps/{appId}/workspace/...` 请求 → 校验权限 → 查应用所在 runtime node → 用 agent_token 调 agent 文件 API（带 scope 限定）→ 流式 proxy 响应给前端。
- agent 在节点侧再做一层路径沙箱校验（双层防御）。

权限：

- 应用 owner（组织成员）可访问自己的工作目录。
- 组织管理员可访问本组织所有应用工作目录。
- 平台管理员可访问全部。

安全：

- manager 和 agent 都必须校验请求路径不逃逸应用工作目录（拒绝 `..`、符号链接逃逸）。
- 单文件下载、archive 大小、archive 条目数有配置上限（manager 校验、agent 校验、Docker bind mount 隔离三层防御）。
- 工作目录浏览和下载行为写审计日志。

软删处理：

- 应用软删除时，agent 把节点上 `{node_data_root}/apps/{app_id}/workspace/` 移到 `{node_data_root}/archived/{app_id}-{timestamp}/workspace/`。
- 保留 N 天后由 agent 自身清理任务物理删除，或由 manager 的 `workspace_archive_cleanup` job 触发清理（N 由配置项决定）。

### 11.6 平台默认 AI 指令

平台维护一段默认指令模板，强制作为所有应用 prompt 的不可覆盖前缀。它解决了"必须把生成的文件输出到工作目录"以及"知识库挂载位置告知"这类技术约束不能被业务侧覆盖的问题。

模板位置：

- 配置文件 `openclaw.system_prompt_template`，平台部署时设置。
- 不暴露任何在线编辑 API，平台管理员要修改需改配置文件并重启 manager 服务。

模板变量：

- `{{app_id}}`、`{{org_id}}`
- `{{workspace_dir}}`：容器内工作目录路径（默认 `/workspace`）。
- `{{knowledge_org_dir}}`：容器内组织级知识库路径（默认 `/knowledge/org`）。
- `{{knowledge_app_dir}}`：容器内应用级知识库路径（默认 `/knowledge/app`）。

默认模板示例：

```
你是 OpenClaw 智能助手。
当需要生成文件（PDF / Word / Excel / 图片等）时，必须将文件输出到目录 {{workspace_dir}}，
按主题或日期建子目录组织，使用清晰可读的文件名。
组织级知识库挂载在 {{knowledge_org_dir}}（同组织所有应用共享，仅读），
应用级知识库挂载在 {{knowledge_app_dir}}（仅本应用，仅读）。
检索时优先应用级，未找到再查组织级，仅作为信息来源使用。
```

拼接顺序（在 OpenClaw 配置渲染时固定）：

1. 平台默认指令（强制前缀，已注入路径变量）。
2. 组织级人设（`organization_personas` 当前生效版本）。
3. 应用级人设（仅当 `apps.persona_mode = app_override`）。

每次容器创建或重建时渲染并以环境变量形式注入容器。组织 / 应用人设无法移除或修改平台默认前缀。

## 12. 页面模块

### 12.1 平台管理员后台

模块：

- 平台总览
- 组织管理
- 组织充值
- 全平台应用
- 全平台 Token 统计
- Runtime Node 管理（创建节点、生成 bootstrap token、查看节点状态/资源/心跳、禁用节点）
- 平台管理员账号
- 审计日志

平台总览展示：

- 组织数
- 应用数
- 运行中容器数
- 总 Token Credit
- 总消耗
- 异常应用数

### 12.2 组织管理员后台

模块：

- 组织总览
- 成员账号管理（创建账号同步创建关联应用，可在表单中选择目标 runtime node）
- OpenClaw 应用（查看本组织所有应用，可触发运维操作）
- 组织 AI 人设
- Token 统计
- 组织级知识库（上传、删除，节点同步状态展示）
- 组织审计日志

组织总览展示：

- 组织余额
- 余额预警阈值
- 成员数
- 应用数
- 异常应用（含节点不可达）
- 用量趋势

### 12.3 组织成员后台

模块：

- 我的总览
- 我的 OpenClaw 应用（查看，由组织管理员创建，不能新建/删除）
- 渠道绑定（第一版仅微信）
- 应用级知识库（上传、删除）
- 组织级知识库（只读查看）
- 工作目录文件（浏览 + 下载，通过 runtime node agent）
- 我的 Token 用量
- 个人账号设置

我的总览展示：

- 我的应用数
- 我的预算
- 我的用量
- 异常应用状态

## 13. 外部系统接口

### 13.1 new-api

manager 依赖 `new-api` 能力：

- 创建或绑定组织对应账号。
- 查询组织余额和配额。
- 给组织充值 Token Credit。
- 创建应用专用 api_key。
- 启用、禁用、恢复 api_key。
- 查询 api_key 用量。
- 查询组织账号用量。
- 查询日志或统计报表。

如果 `new-api` 某些管理 API 不支持，需要在实施前确认替代方案。

### 13.2 Runtime Node Agent

manager 不直接访问任何 Docker socket。所有节点级操作经由 runtime node 上常驻的 agent 容器：

- agent 暴露 Docker 代理（HTTP，与 Docker remote API 协议兼容），manager 用 Docker SDK 通过 agent 的 endpoint 发起容器创建、启动、停止、重启、删除、状态查询、日志读取、资源监控、容器内命令执行。
- agent 暴露文件 API（HTTP），manager 借此完成节点上目录创建、知识库同步推送、工作目录浏览/下载/打包、应用目录归档与清理。
- agent 周期性向 manager 心跳上报本节点资源摘要、agent 版本、状态。
- agent 通过一次性 bootstrap token 注册，注册成功后用 agent token 长期通信。
- 所有 agent 接口认证：`Authorization: Bearer {agent_token}` + agent 自签 TLS 证书校验。

### 13.3 OpenClaw 与渠道插件

manager 通过 runtime node agent 间接操作 OpenClaw：

- 容器创建时通过 agent Docker 代理注入环境变量（拼接后的系统 prompt、`new-api` api_key、`new-api` base URL、工作目录路径、组织/应用知识库路径、启用的渠道插件名）。
- 启用对应渠道插件（第一版仅 `openclaw-weixin`，未来按应用 channel_type 启用其他插件）。
- 通过 agent Docker 代理在容器内 exec：`openclaw channels login --channel <plugin_name>`。
- 获取 AuthChallenge（第一版为二维码 payload）。
- 查询绑定状态。
- 查询 OpenClaw 健康状态（exec 命令或 HTTP 探针）。
- 知识库文件通过 agent 文件 API 在节点上 bind mount 目录中维护，manager 不调用 OpenClaw 自己的导入接口；OpenClaw 通过系统提示词获知挂载路径，自行从目录读取。
- 工作目录通过 agent 文件 API 浏览和下载，manager 不通过 OpenClaw 接口管理工作目录文件。

## 14. 异常处理

需要显式展示并记录的异常：

- `new-api` 不可用。
- 组织对应 `new-api` 账号不存在或失效。
- api_key 创建失败。
- api_key 禁用或恢复失败。
- Docker 不可用。
- 容器创建失败。
- 容器启动失败。
- 容器异常退出。
- 健康检查失败。
- 渠道插件缺失或加载失败。
- 渠道认证过期（如微信二维码过期）。
- 渠道认证失败（如扫码失败）。
- 渠道凭证失效。
- 知识库上传失败（manager 主副本写入失败 / agent 同步失败）。
- 知识库删除失败。
- 知识库节点同步状态不一致（组织级异步同步部分节点失败）。
- 工作目录路径越权访问尝试。
- 工作目录文件大小超过下载/打包上限。
- Runtime node 心跳超时、不可达。
- Runtime node bootstrap token 过期或被反复使用。
- Runtime node agent 版本不兼容。

异常展示原则：

- 用户能看到当前状态、失败原因和可执行动作。
- 管理员能看到更完整的错误详情。
- 敏感信息不展示在前端。
- 所有敏感失败写入审计日志或操作记录。

## 15. 审计日志

审计范围：

- 登录成功和失败。
- 组织创建、启用、禁用。
- 组织充值。
- 成员创建（含同步创建关联应用，复合事件）、禁用、重置密码、删除。
- api_key 手动禁用和恢复。
- 应用初始化、删除。
- 容器启动、停止、重启、删除。
- 渠道绑定、解绑、重试。
- 组织级知识库文件上传、删除（含每节点同步状态）。
- 应用级知识库文件上传、删除。
- 工作目录浏览、单文件下载、文件夹打包下载。
- 组织 AI 人设变更。
- Runtime node 创建（生成 bootstrap）、注册、心跳超时、禁用、启用、bootstrap rotate。

审计日志不可由普通业务 API 修改或删除。

## 16. 非功能需求

### 16.1 安全

- 密码必须哈希存储。
- 后端必须校验角色和组织数据范围。
- api_key 等敏感配置不能明文展示。
- 删除应用、停止容器、重置密码、充值等操作需要确认和审计。
- 禁用组织后，该组织管理员和成员不能登录。

### 16.2 可恢复性

- 创建流程支持中途退出后继续。
- 应用状态可通过后端和外部系统状态重新恢复。
- 跨系统创建失败必须有补偿策略。

### 16.3 可观测性

- 应用详情展示容器状态、健康状态、渠道绑定状态、工作目录摘要、api_key 状态和最近日志。
- 平台和组织首页展示异常应用和预算风险。

### 16.4 扩展性

- 预留 Runtime Node。
- 预留多节点运行能力。
- 预留组织知识库复用能力，但当前不实现。
- 预留邀请注册能力，但当前不实现。

## 17. 验收范围

完整蓝图验收应覆盖：

- 平台管理员账号登录。
- 组织创建、启用、禁用。
- 组织绑定或创建 `new-api` 账号。
- 组织充值 Token Credit。
- Runtime node 创建（生成 bootstrap token）、agent 注册、心跳上报、状态翻转（active / unreachable）、禁用。
- 组织管理员登录。
- 组织管理员创建成员账号同步创建关联应用并指定目标 runtime node，创建后自动初始化 api_key + 节点目录 + 知识库同步 + 容器创建 + 启动 + 健康检查。
- 成员账号禁用、重置密码、删除联动应用状态变化。
- 组织 AI 人设编辑和成员覆盖策略。
- 平台默认指令模板渲染、变量注入和"平台 → 组织 → 应用"三层 prompt 拼接顺序。
- 渠道扫码绑定（第一版微信），AuthChallenge 协议与前端按 type 渲染。
- 容器启停、重启、日志、资源监控和健康检查（全部经由 agent Docker 代理）。
- 组织级知识库上传/删除并异步同步到本组织所有应用所在节点；应用级知识库上传/删除并同步推送到该应用所在节点。
- 应用工作目录浏览（子目录、面包屑）、单文件下载、文件夹打包下载（全部经由 agent 文件 API 代理）。
- 应用、成员、组织、平台维度 token 统计（每次直查 new-api）。
- 应用软删除、容器删除、api_key 禁用、工作目录归档（agent 完成）、知识库主副本删除。
- 审计日志查询（含工作目录浏览/下载、知识库变更、节点生命周期事件）。

## 18. 已确认产品决策

- 文档目标是完整产品蓝图。
- 部署形态：manager + DB + Redis + new-api + ollama 一组核心服务；OpenClaw 容器分布在一台或多台 runtime node；从第一版起就走多节点抽象。
- 账号即客户端：组织管理员创建成员账号时强制创建关联 OpenClaw 应用，账号与应用一对一绑定，成员不能创建或删除应用。
- 每个 OpenClaw 应用最多绑定一个渠道账号（第一版仅微信渠道）。
- 渠道绑定采用通用抽象（数据模型 + ChannelAdapter），第一版仅注册微信适配器，未来扩展不需 schema 迁移。
- 组织对应 `new-api` 一个账号。
- OpenClaw 应用对应 `new-api` 一个 api_key。
- 组织统一充值；第一版**不做**成员级别预算限制，仅保留组织级余额预警与平台/管理员手动禁用 api_key 能力。
- 充值单位为 Token Credit/点数，不处理真实支付。
- 应用创建由组织管理员在创建成员账号时同步发起，无独立向导；应用没有"发布"步骤，渠道绑定成功后自动进入 running。
- 渠道绑定前必须创建并启动容器。
- 绑定失败后容器保持运行。
- 应用删除采用软删除，删除容器、禁用 api_key、节点上目录归档（由 agent 执行）、manager 知识库主副本删除。
- 每个应用拥有独立工作目录，OpenClaw 输出文件存在节点上，用户后台浏览/下载经由 agent 文件 API 代理，manager 不写入工作目录。
- 平台维护默认 AI 指令模板（不可在线编辑），强制作为所有应用 prompt 不可覆盖前缀，承载"输出到工作目录"和"知识库挂载位置告知"等技术约束。
- 知识库分组织级与应用级两层，filesystem 即事实来源，无 DB 表；manager 持有主副本，agent 同步副本到节点并 bind mount 进容器。
- 账号体系采用账号密码登录。
- 权限采用固定三角色，不做完整 RBAC。
- Token 统计每次直查 `new-api`，manager 不保存 token 明细，也不缓存到 DB。
- 运行运维包含资源监控、健康检查和自动重启策略，全部经由 runtime node agent 执行。
- AI 人设为组织级默认，组织可决定成员是否允许覆盖；平台默认前缀始终生效。
- manager 不管理 Ollama 或大模型配置。
- manager 不提供 OpenClaw 反向代理或统一流量入口。
- 知识库上传可在创建表单中预上传，也可在创建后继续上传或删除。
- manager 不直接访问任何节点 Docker 或文件系统，所有节点级操作通过该节点 agent 暴露的 Docker 代理与文件 API；不存在 `local` 节点回退路径。
- agent 在节点上以代理模式暴露 Docker（manager 用 Docker SDK 透传），同时提供文件 API；agent 不修改节点 Docker daemon 配置（零侵入）。
- 节点注册凭证（client TLS key、agent token 等）使用 manager 配置文件中的 `master_key` 加密保存。

## 19. 后续可拆分阶段

虽然本文档是完整蓝图，实施时建议拆分：

### Phase 1：核心闭环

- 账号登录
- 组织和成员
- 组织充值
- Runtime node 注册（agent 镜像构建 + 注册流程 + 心跳）
- 组织管理员创建成员账号 + 应用同步初始化（含选择目标 runtime node）
- api_key 创建
- 应用知识库与组织知识库 manager 主副本管理
- 容器创建时 agent 文件 API 推送知识库 + 创建容器
- 平台默认指令模板渲染与三层 prompt 拼接
- 通过 agent Docker 代理创建/启动/停止/重启容器
- 渠道绑定（微信）
- 工作目录浏览和下载（agent 文件 API 代理）
- 基础启停和日志（agent Docker 代理）
- 应用级 token 用量展示（直查 new-api）

### Phase 2：组织治理

- 组织 AI 人设
- 组织和成员维度报表（直查 new-api）
- 审计日志
- 组织级知识库异步同步到多节点（`knowledge_sync_node` job + 同步状态展示）
- 平台/管理员手动禁用 / 恢复 api_key（风控）

### Phase 3：运维增强

- 资源监控
- 健康检查
- 自动重启策略
- 运行节点抽象完善
- 平台总览和全平台统计

### Phase 4：扩展能力

- 多运行节点
- 组织公共知识库
- 邀请注册
- 更细粒度权限
- 告警和日志检索
