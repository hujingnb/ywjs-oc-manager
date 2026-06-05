# 产品设计

> 业务角色、对象模型、核心流程与权限模型。本文回答"系统在业务上做什么"，实现细节见 technical-design.md。

---

## 1. 角色

系统固定三类角色，不实现完整 RBAC。权限谓词的具体判定逻辑集中在
`internal/auth/authorizer.go`，本节仅说明业务定位与边界。

### 1.1 平台管理员（platform_admin）

**定位**：系统运维层，管理企业开通和计费，不介入企业日常业务。

**典型操作**：
- 创建、启用、禁用企业
- 为企业充值 Token Credit（写入 new-api 余额）
- 注册和维护 Runtime Node
- 查看全平台应用状态、审计日志和用量概览
- 为已有成员补建应用实例（`CanCreateAppForMember`）
- 跨企业维护企业知识库，管理平台级行业知识库，供助手版本选择后检索

**权限边界**：
- 可跨企业读取所有资源（企业、成员、应用、用量、审计）
- **不可**直接写入任意企业成员、应用或应用知识库（不绕过应用边界）
- 可写企业知识库和平台级行业知识库；行业知识库不归属单个企业
- 不直接介入企业成员生命周期（无 `CanManageMember` 权限）

### 1.2 企业管理员（org_admin）

**定位**：企业管理层，负责本企业成员账号、应用和知识库的全生命周期。

**典型操作**：
- 创建、禁用、重置、删除本企业成员账号；创建账号时同步创建对应应用
- 查看和管理本企业所有应用（渠道绑定、启停、重建）
- 设置企业级 AI 人设及成员是否允许覆盖的策略
- 上传、删除企业级知识库文件（由 RAGFlow 解析后供本企业应用检索）
- 浏览和下载本企业企业级、应用级知识库文件
- 查看本企业 Token 用量、成员用量、审计日志
- 对解析失败或已停止的知识库文件触发重新解析

**权限边界**：
- 只能操作本企业资源（`p.OrgID == orgID` 校验）
- 不能为其他企业创建/修改任何内容
- 不能充值，只能查看余额和消耗

### 1.3 企业成员（org_member）

**定位**：最终用户，管理自己唯一的应用和渠道。

**典型操作**：
- 查看和操作自己名下唯一的应用（启停、重启、绑定渠道）
- 在策略允许时编辑应用级 AI 人设
- 上传、删除自己应用的知识库文件
- 浏览和下载本企业企业级知识库、自己应用的知识库与工作目录文件
- 查看自己的应用状态和 Token 用量

**权限边界**：
- 只能读写自己的应用（`p.UserID == appOwnerUserID` 校验）
- 不能创建或删除应用（仅企业管理员有此权限）
- 不能查看其他成员的应用或数据
- 不能写入企业级知识库（只读）

---

## 2. 对象模型

### 2.1 企业（Organization）

| 属性 | 说明 |
|------|------|
| 企业 ID | 主键 |
| 企业标识（slug） | 登录时区分企业的短字符串 |
| 企业名称 | 展示用 |
| 状态 | `active` / `disabled` / `deleted` |
| new-api 账号 ID | 对应 new-api 中的一个账号，用于充值和用量查询 |
| 联系人 / 联系方式 | 运营记录 |
| 创建时间 / 更新时间 / 删除时间 | 软删除，`deleted_at` 非空即删除 |

**关联**：一个企业拥有多个成员、多个应用、一个 new-api 账号映射。

### 2.2 成员（Member / User）

| 属性 | 说明 |
|------|------|
| 用户 ID | 主键 |
| 企业 ID | 所属企业 |
| 登录账号 | 唯一，企业内 |
| 角色 | `org_admin` / `org_member` |
| 状态 | `active` / `disabled` |
| `deleted_at` | 语义为"下线时间戳"；`status=disabled` 时写入，重新启用时清空；与企业 `deleted_at`（真删除）语义不同 |
| 最近登录时间 / 创建时间 / 更新时间 | 运维记录 |

**关联**：每个未删除成员账号最多对应一个未删除应用（一对一强绑定）。

### 2.3 应用（App）

应用是核心运营单元，代表一个运行在 Runtime Node 上的 Hermes 容器实例。

| 属性 | 说明 |
|------|------|
| 应用 ID | 主键 |
| 企业 ID / 所属成员 ID | 归属 |
| 应用名称 / 描述 | 展示用 |
| 状态（AppStatus） | 见下方状态表 |
| Runtime Node ID | 指定运行节点 |
| Docker 容器 ID | 运行时写入 |
| new-api api_key ID | 对应 new-api 中的 token |
| api_key 状态（APIKeyStatus） | `pending` / `active` / `disabled` / `error` |
| 人设来源（PersonaMode） | `org_inherited` / `app_override` |
| 应用级人设内容 | `app_override` 时生效 |
| 创建时间 / 更新时间 / 删除时间 | 软删除 |

**AppStatus 状态机**：

| 状态 | 含义 |
|------|------|
| `draft` | 刚创建，尚未初始化 |
| `initializing` | `app_initialize` Job 运行中 |
| `binding_waiting` | 容器就绪，等待渠道扫码绑定 |
| `binding_failed` | 渠道绑定超时或 token 过期 |
| `running` | 容器运行中，渠道已绑定或无需绑定 |
| `stopped` | 容器已停止，数据保留 |
| `error` | 任意步骤失败；由用户手工 retry 离开 |
| `deleted` | 终态，`deleted_at` 非空 |

合法转移由 `internal/domain/app_state_machine.go` 维护。

**关联**：每个应用对应一个容器、一个 new-api api_key、最多一个渠道绑定、一份应用私有知识库、一个工作目录。

### 2.4 Runtime Node（运行节点）

| 属性 | 说明 |
|------|------|
| 节点 ID | 主键 |
| 节点名称 | 如 `node-shanghai-1` |
| 状态（RuntimeNodeStatus） | `pending` / `active` / `unreachable` / `disabled` / `degraded` |
| Docker 代理 endpoint | agent 暴露的 Docker API URL（:7001） |
| 文件 API endpoint | agent 暴露的文件管理 URL（:7002） |
| 最近心跳时间 | 节点活跃性依据 |
| 资源摘要 | CPU / 内存 / 磁盘 / 容器数（agent 上报） |

**关联**：一个节点承载多个应用容器。节点必须先注册并处于 `active` 状态，才可分配给新应用。

### 2.5 知识库（Knowledge）

分为三类 scope：

- **企业级知识库**：由企业管理员上传到 RAGFlow org dataset；Hermes 只读检索，读取者可下载单个原文件。
- **应用级知识库**：由应用所有者上传到 RAGFlow app dataset；Hermes 可检索并可通过 `oc-kb add` 写入当前实例知识库。
- **行业知识库**：由平台管理员或外部商业知识库上传入口写入 RAGFlow industry dataset；助手版本可选择一个或多个行业库，Hermes 只读检索。

知识库内容以 RAGFlow 为事实来源；manager 只维护 org/app/industry 与 RAGFlow dataset/document 的映射，并在自身权限模型内控制读写边界。

### 2.6 渠道（Channel）

| 属性 | 说明 |
|------|------|
| 渠道类型 | 当前仅 `wechat` |
| 绑定状态（ChannelStatus） | `unbound` / `pending_auth` / `bound` / `failed` / `expired` / `unbound_by_user` / `deleted` |

渠道与应用一对一绑定。绑定流程为异步 Job（`channel_start_login` → `channel_check_binding`）。

### 2.7 用量（Usage）

用量数据不在 manager 数据库存储，每次按需直查 new-api。查询维度：

- 企业聚合用量（仅平台管理员 / 企业管理员可查）
- 成员维度用量
- 应用维度用量（由成员自查或管理员代查）

---

## 3. 核心业务流程

### 3.1 注册与登录

```
浏览器 → POST /auth/login（账号 + 密码 + 企业标识）
  → auth_service 校验凭证 + 角色
  → 返回 JWT（含 user_id / org_id / role）
  → 后续请求携带 Bearer token
```

平台管理员登录不需要填写企业标识（`org_id` 留空）。成员登录必须填写企业标识以定位企业。

### 3.2 应用初始化（Onboarding）

```
企业管理员 → 创建成员账号（CanCreateAppForOrg）
  → onboarding_service 同步创建 App（status=draft）
  → 分配 Runtime Node（node_selector 按可用节点选择）
  → 入队 app_initialize Job

worker 执行 app_initialize：
  → 在节点上拉起 Hermes 容器（通过 runtime_node_agent Docker 代理）
  → 在 new-api 创建 api_key，写回 app.api_key_id
  → 写入容器 ID，app.status: draft → initializing → binding_waiting

成员 → 扫码绑定渠道
  → app.status: binding_waiting → running（绑定成功）
         binding_waiting → binding_failed（超时 / token 过期）
```

关键状态变更：`draft → initializing → binding_waiting → running`

### 3.3 知识库管理与检索

```
管理员/成员 → 上传知识库文件（CanWriteOrgKnowledge / CanWriteAppKnowledge）
  → manager 校验企业/实例权限
  → manager 上传文件到 RAGFlow document
  → manager 触发 RAGFlow parse 并缓存 document 元数据

平台管理员/外部服务 → 上传行业知识库文件（CanManageIndustryKnowledge / 固定 upload token）
  → manager 按行业库 ID 或行业名称定位 industry dataset
  → 同名文件覆盖旧 document，并触发 RAGFlow parse

Hermes → oc-kb search/add
  → 调 manager runtime API
  → manager 用 app runtime token 解析当前实例
  → 固定访问当前实例 dataset、所属企业 dataset 和当前助手版本选择的行业 dataset

企业知识库和行业知识库对 Hermes 只读；实例知识库对 Hermes 读写。行业知识库按助手版本关联，检索时每个关联行业库都会返回最多 `top_k` 条。
```

### 3.4 容器治理（启停 / 重启 / 健康自愈 / 重建）

```
用户触发（CanTriggerRuntimeOperation）：
  → 启动：入队 app_start_container Job
           worker 调用 agent Docker API 启动容器
           app.status: stopped → running
  → 停止：入队 app_stop_container Job
           worker 停止容器
           app.status: running → stopped
  → 重启：入队 app_restart_container Job
           worker 先停后启；同时清空 Hermes session 使新配置生效

自动治理（worker 定时 Job）：
  → app_health_check：探测 running 状态应用的容器是否存活
           发现容器已停止 → 自动触发 app_start_container（健康自愈）
           多次失败 → app.status: running → error
  → runtime_node_health_reconcile：按心跳时间批量修正节点状态
           超时未心跳 → RuntimeNodeStatus: active → unreachable
  → runtime_refresh_status：刷新运行中应用的容器 inspect 快照

平台管理员为成员补建实例（CanCreateAppForMember）：
  → 直接入队 app_initialize Job 对已有 App 重走初始化
```

### 3.5 用量直查 new-api

```
用户 → 查看用量（CanViewOrgUsage / CanViewMemberUsage）
  → usage_service 向 new-api HTTP API 发起实时查询
  → 不在 manager 数据库落缓存
  → 原始用量数据聚合后展示
```

---

## 4. 权限模型

权限谓词全部集中在 `internal/auth/authorizer.go`。下表按谓词列出触发场景与三类角色的判定结果。

| 谓词 | 触发场景 | platform_admin | org_admin | org_member |
|------|---------|----------------|-----------|------------|
| `CanManageOrg` | 企业写操作（成员管理、状态调整） | 全部企业 | 本企业 | 不可 |
| `CanViewOrg` | 企业资源读取 | 全部企业 | 本企业 | 本企业 |
| `CanViewMember` | 查看成员明细 | 全部 | 本企业成员 | 仅自己 |
| `CanManageMember` | 成员写操作（创建、角色、状态、密码） | 不可 | 本企业成员 | 不可 |
| `CanEditMember` | 编辑成员资料（含本人编辑自身） | 仅自己 | 本企业成员或自己 | 仅自己 |
| `CanViewApp` | 查看应用 | 全部 | 本企业应用 | 自己应用 |
| `CanViewAppAudit` | 查看应用审计记录 | 全部 | 本企业应用 | 自己应用 |
| `CanManageApp` | 应用写操作（渠道绑定等） | 不可 | 本企业应用 | 自己应用 |
| `CanCreateAppForOrg` | 在企业下创建应用（onboarding） | 不可 | 本企业 | 不可 |
| `CanCreateAppForMember` | 为已有成员补建应用实例 | 全部 | 本企业 | 不可 |
| `CanTriggerRuntimeOperation` | 启停/重启容器等运行时操作 | 不可 | 本企业应用 | 自己应用 |
| `CanReadOrgKnowledge` | 读取 / 下载企业知识库 | 全部 | 本企业 | 本企业 |
| `CanWriteOrgKnowledge` | 写入 / 删除 / 重解析企业知识库文档 | 全部 | 本企业 | 不可 |
| `CanReadAppKnowledge` | 读取 / 下载应用知识库 | 全部 | 本企业应用 | 自己应用 |
| `CanWriteAppKnowledge` | 写入 / 删除 / 重解析应用知识库文档 | 不可 | 本企业应用 | 自己应用 |
| `CanManageIndustryKnowledge` | 管理平台级行业知识库 | 全部 | 不可 | 不可 |
| `CanViewOrgPersona` | 读取企业人设 | 全部 | 本企业 | 本企业 |
| `CanManageOrgPersona` | 写入企业人设 | 全部（等同 CanManageOrg） | 本企业 | 不可 |
| `CanViewOrgUsage` | 查看企业聚合用量 | 全部 | 本企业 | 不可 |
| `CanViewMemberUsage` | 查看成员用量 | 全部 | 本企业成员 | 仅自己 |
| `CanViewOrgAudit` | 查看企业审计列表 | 全部 | 本企业 | 不可 |
| `CanViewOwnAudit` | 查看"我的审计"视角 | 是（需非空 userID） | 是 | 是 |

**说明**：
- `disabled` 状态的账号不得触发任何运行时操作（调用方在 `CanTriggerRuntimeOperation` 之前额外校验 `user.status != disabled`）。
- 平台管理员可跨企业维护企业知识库，但对应用写操作无权限；行业知识库是平台级资源，由平台管理员管理。

---

## 5. 计费与 Token Credit

```
平台管理员 → 操作充值（manager 记录充值历史）
  → manager 调用 new-api API 写入企业账号余额（Token Credit）

企业成员/应用 → 通过 new-api api_key 发起模型调用
  → new-api 实时扣减对应账号余额
  → 计费事实来源完全在 new-api

manager → 查看用量时直查 new-api（不缓存）
  → 聚合企业 / 成员 / 应用维度后展示

应用停用/删除 → 入队 newapi_disable_key Job 禁用对应 api_key
应用恢复     → 入队 newapi_restore_key Job 重新启用 api_key
```

**边界**：
- manager 只负责充值操作记录（`recharge_service`）和用量展示（`usage_service`）。
- token 价格计算、真实扣费、发票、在线支付均不在 manager 范围内。
- manager 不缓存用量数据，每次展示直查 new-api 保证实时性。
