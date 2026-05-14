# 文档重构设计

> 把 oc-manager 仓库的项目文档（README + docs/ + deploy/*.md）按读者视角重组、
> 删除过时草稿、用激进力度修订内容，并新增 Hermes 容器运行机制专题。
> 完成后，README.md 是唯一文档入口，所有 docs/ 与 deploy/ 下的 .md 都从 README
> 文档导航能找到；不再保留 openclaw 旧术语与 v1.0/v1.0.1 阶段性叙述。

## 1. 背景与动机

当前文档存在以下问题：

- **历史草稿堆积**：`docs/plans/` `docs/specs/` `docs/superpowers/plans/`
  `docs/superpowers/specs/` 共 35 个临时计划/设计文件，已无指引价值。
- **过时术语**：`openclaw-manager-*.md` 等沿用废弃前缀；项目已切到
  `manager` + `Hermes` 命名。
- **阶段性叙述残留**：`verification-report.md`（1177 行）是 v1.0/v1.0.1
  实测时序记录，GA 已过，应删除。
- **零散文档无入口**：`runtime-agent-auto-enroll-principles.md`、
  `runtime-agent-deployment.md` 没有从 README 链接，读者难发现。
- **缺关键专题**：Hermes 容器如何被创建、注入什么、挂载什么、知识库怎么
  到容器，这条链路当前没有任何文档解释。
- **目录冗长**：PRD 957 行、技术设计 2171 行，含与 README/architecture 重复
  的总体描述与已实现的 TODO。

本次重构目标：

1. 文档列表收敛到 14 个 .md（不含 AGENTS.md），按读者视角组织。
2. 全文删除 openclaw 残留与阶段性叙述，事实陈述以**当前代码**为准。
3. 每个文档开头放一段简短摘要（1–3 句），随后才是详细内容。
4. README.md 是唯一统一入口，文档导航覆盖 docs/ 与 deploy/ 全部 .md。

## 2. 最终文件清单

共 15 个 .md（不含 AGENTS.md；AGENTS.md 不变）。
分布：1 个根 README + 8 个 docs/ 文档 + 6 个 deploy/ 文档（含 4 个子目录 README）。

```text
README.md                         项目入口 + 全部文档导航
AGENTS.md                         协作规范（不动）

docs/
├── architecture.md               架构总览（模块图 / 拓扑 / 数据流）
├── local-development.md          本地开发（make 目标 / 常见问题）
├── configuration.md              配置参考（manager.yaml / agent.yaml / .env）
├── product-design.md             产品设计（角色 / 对象 / 业务流程 / 权限）
├── technical-design.md           技术设计（模块 / 状态机 / 接口 / job）
├── user-manual.md                用户手册（三类角色操作）
├── hermes-container.md           Hermes 容器运行机制（新增）
└── runtime-agent.md              runtime-agent 工作原理（自动注册 / 心跳 / 探测）

deploy/
├── README.md                     生产部署总览
├── operations.md                 运维手册（备份 + 升级 + 故障排查）
├── manage/README.md              manager 服务部署（不动）
├── new-api/README.md             new-api 网关部署（不动）
├── ollama/README.md              ollama 部署（不动）
└── runtime-agent/README.md       runtime-agent 部署（合入旧 docs/runtime-agent-deployment 缺失细节）
```

## 3. 删除清单

直接 `git rm`：

- `docs/verification-report.md`（1177 行，v1.0/v1.0.1 验证报告）
- `docs/plans/`（整目录，含 `2026-05-08-dark-theme-naive-ui.md`）
- `docs/specs/`（整目录，含 `2026-05-08-dark-theme-naive-ui-design.md`）
- `docs/superpowers/plans/`（整目录，16 个 .md + 1 个 .txt）
- `docs/superpowers/specs/` 下除本 spec 外的所有文件（17 个旧 spec）

**保留**：`docs/superpowers/specs/2026-05-14-docs-restructure-design.md`
（本文件）作为本次重构的历史记录；`docs/superpowers/specs/` 目录因此保留。

## 4. 重命名 / 合并 / 移动清单

| 旧路径 | 新路径 | 动作 |
|---|---|---|
| `docs/openclaw-manager-design.md` | `docs/product-design.md` | 重命名 + 激进精简（目标 ≤ 600 行） |
| `docs/openclaw-manager-technical-design.md` | `docs/technical-design.md` | 重命名 + 激进精简（目标 ≤ 1200 行） |
| `docs/runtime-agent-auto-enroll-principles.md` | `docs/runtime-agent.md` | 移动 + 重命名 + 加摘要 |
| `docs/runtime-agent-deployment.md` | （合并） `deploy/runtime-agent/README.md` | 内容合入 deploy 子目录 README，原文件删除 |
| `deploy/backup.md` + `deploy/upgrade.md` | `deploy/operations.md` | 合并为一份运维手册 |

## 5. 通用规范：每个文档的写作规则

### 5.1 开头摘要

每个保留/新建文档必须以摘要起头：

```markdown
# <文档标题>

> 一到两句话：本文说了什么、给谁看、回答什么问题。
> 可以多写一行补充读完后读者应能做什么。

## <第一章节>
...
```

### 5.2 术语统一

全文搜索替换：

- `openclaw` / `Openclaw` / `OPENCLAW` → `manager` 或 `Hermes`（按语境）
- `OC Manager` → `Agent Runtime Manager` 或 `manager`（按语境）
- 已废弃前缀如 `OPENCLAW_*` 环境变量 → 核对实际代码后改为 `OC_*` 实际名

### 5.3 事实校对

字段名、表结构、接口路径、状态机枚举、Make 目标、命令行示例必须与
当前代码一致。不确定的地方直接读代码核实（不脑补）；代码也答不上来的，
在文档里留 `<!-- TODO: 核实 -->` 注释并在 PR 描述里列出。

### 5.4 删除过时内容

- 「v1.0 GA」「v1.0.1 计划」「本次迭代」等阶段性表述全部删除
- 已实现的 TODO / 待定项删除
- bootstrap token / 管理员后台手工创建节点的描述删除（auto-enroll
  已是唯一模式）

### 5.5 删除冗余

技术设计与 PRD 之间重复的章节只在一处保留，另一处用相对链接引用。
原则上：业务规则 / 角色权限放 product-design.md；模块边界 / 接口契约 /
状态机放 technical-design.md。

## 6. 每个文档的章节大纲

### 6.1 README.md（项目入口）

```text
# Agent Runtime Manager
> 一句话：面向组织的 Hermes Agent 应用管理后台。
1. 核心能力（保留现状，去 v1.0 字样）
2. 系统拓扑（保留 ASCII 图）
3. 技术栈
4. 仓库结构
5. 快速开始（本地）
6. 文档导航 ← 全量重写（见下方 §7）
7. 端口约定
8. 健康检查
9. 许可与反馈
```

文档导航必须覆盖 docs/ 与 deploy/ 全部 .md（含 deploy 子目录 README）。
不再用 `### 入门 / 概览` 这种小标题分块——直接按读者目的分两栏：
**开发与设计** / **部署与运维**。

### 6.2 docs/architecture.md

```text
> 摘要：manager 与 runtime-agent 的模块图、节点拓扑、关键数据流。
1. 系统拓扑（高保真图，覆盖 manager / agent / new-api / ollama / Hermes）
2. 模块边界（manager 后端 / 前端 / agent / Hermes 容器）
3. 数据持久化划分（PostgreSQL / Redis / agent 文件系统 / Hermes 挂载）
4. 关键数据流（onboarding / 知识库同步 / 容器生命周期 / token 用量）
5. 跨模块约束（权限校验 / OpenAPI 同步 / 镜像同步）
```

### 6.3 docs/local-development.md

```text
> 摘要：起本地、跑测试、定位常见问题。
1. 前置条件（Linux / Docker 24+ / make）
2. 第一次起本地（make 目标顺序）
3. 调试账号（来自 CLAUDE.md，复制过来）
4. 常用 Make 目标速查（对照当前 Makefile）
5. 常见问题排查
```

### 6.4 docs/configuration.md

```text
> 摘要：manager.yaml / agent.yaml / .env 全部字段含义。
1. manager.yaml（按 example 字段表逐项）
2. agent.yaml（按 example 字段表逐项）
3. .env / 端口映射
4. 密钥生成与轮换
```

字段表对照 `config/manager.example.yaml` 与 `config/agent.example.yaml`。

### 6.5 docs/product-design.md（从 openclaw-manager-design.md 精简）

```text
> 摘要：业务角色、对象模型、核心流程与权限模型。
1. 角色（平台管理员 / 组织管理员 / 组织成员）
2. 对象模型（组织 / 成员 / 应用 / 节点 / 知识库 / 渠道 / 用量）
3. 核心业务流程（注册 / app 初始化 / 知识库同步 / 容器治理 / 用量直查）
4. 权限模型（参考 internal/auth/authorizer.go）
5. 计费与 token credit
```

目标 ≤ 600 行。砍掉与 README / architecture 重复的总体描述、已实现 TODO、
v1.0 阶段叙述。

### 6.6 docs/technical-design.md（从 openclaw-manager-technical-design.md 精简）

```text
> 摘要：后端模块边界、状态机、接口契约、job 与 worker 模型。
1. 模块清单（internal/api / service / domain / store / scheduler / worker / integrations / runtime / files / auth）
2. 状态机（app / runtime_node / job 等，对照 internal/domain）
3. 接口契约（链接到 openapi/openapi.yaml，不重复 yaml 内容）
4. job 与 worker 模型（scheduler / worker 包结构、关键 handler）
5. 数据访问层（sqlc 生成的 store，约定与示例）
6. 跨模块约束（OpenAPI 同步、权限校验、审计、镜像同步）
```

目标 ≤ 1200 行。砍掉与 README / architecture 重复的拓扑描述、
已实现 TODO、版本规划叙述。

### 6.7 docs/user-manual.md

```text
> 摘要：三类角色在前端能做什么。
1. 平台管理员（组织管理 / token credit / 运行节点）
2. 组织管理员（成员 / 应用 / 知识库 / 渠道 / 用量）
3. 组织成员（应用控制台 / 工作目录 / 日志 / 知识库）
4. 截图（必要时补）
```

对照 `web/src/pages/` 当前实际页面校对。

### 6.8 docs/hermes-container.md（新增）

```text
> 摘要：manager 如何让 runtime-agent 把 Hermes 容器跑起来：
> 创建链路、镜像同步、环境变量、挂载目录、工作目录、知识库注入、
> 注入 vs 运行时生成、生命周期事件。

1. 总览：一次 app 上线时发生了什么
   - 时序：成员注册 → app_initialize → imagesync → 容器创建 → SOUL/skills 注入 → 启动 → 探活
   - 涉及的 service / worker / agent 接口（带文件路径行号）

2. 镜像同步（imagesync）
   - 构建：runtime/hermes/
   - 版本：AppInitializeConfig.RuntimeImage（默认 hermes-runtime:dev）
   - 同步：本地 ImageID vs 节点 InspectImage；不一致时 agent /v1/images/load

3. 容器创建参数
   - 入口：ContainerCreator.CreateContainer (AgentBackedAdapter)
   - 经由：agent docker proxy 转 docker API
   - 环境变量表
     | key | 含义 | 来源 | 何时追加 |
     | OPENAI_API_KEY | new-api 凭据 | new-api channel | 创建时 |
     | OPENAI_BASE_URL | new-api /v1 | manager 配置 | 创建时 |
     | GATEWAY_ALLOW_ALL_USERS | 绕过 Hermes user pairing | 固定 true | 创建时 |
     | WEIXIN_DM_POLICY | 微信策略 | 固定 open | 创建时 |
     | WEIXIN_ACCOUNT_ID/TOKEN/BASE_URL/CDN_BASE_URL | 绑微信后追加 | channel 绑定 | 绑定后 |
   - 挂载
     | bind | <node.data_root>/apps/<appID>/.hermes → /opt/data | 唯一 |

4. 挂载目录结构（按注入来源分类）
   /opt/data/
   ├── manager 注入（app_initialize 经 agent /v1/scopes/.../runtime/file PUT）
   │   ├── SOUL.md
   │   ├── config.yaml
   │   └── skills/kb-app-*  /  skills/kb-org-*
   ├── Hermes 镜像自带（首次启动从镜像复制）
   │   ├── bin/
   │   └── skills/<非 kb-* 类目>/
   └── Hermes 容器运行时生成
       ├── workspace/                terminal cwd（agent 预建空目录）
       ├── sessions/                 会话 jsonl
       ├── memories/                 记忆
       ├── logs/                     agent.log / gateway.log / errors.log
       ├── cron/output/              定时任务输出
       ├── cache/{documents,images}/ 文件/图片缓存
       ├── state.db                  主状态库
       ├── kanban.db                 kanban 数据
       ├── gateway.lock              网关锁
       ├── gateway_state.json        网关状态
       ├── channel_directory.json    渠道目录运行时状态
       ├── sandboxes/singularity/    skill 沙盒
       ├── platforms/pairing/        平台配对状态
       └── weixin/accounts/          微信 token / sync state（绑渠道后才有）

5. skills/ 目录的混合归属
   - manager 注入仅限 kb-app-<slug>/ 与 kb-org-<slug>/
   - 其他类目（apple / autonomous-ai-agents / creative / github / mlops / ...）
     来自 Hermes 镜像，首次启动从镜像复制到挂载点
   - 知识库新增 / 修改 → manager 写 kb-* 目录；不影响 Hermes 内置技能

6. 工作目录如何定位
   - config.yaml 里 terminal.cwd = /opt/data/workspace
   - workspace 目录由 agent 在 ensureScope 阶段预建
   - 单一物理路径：宿主机 .hermes/workspace 和容器内 /opt/data/workspace 是
     同一份数据，manager 不做路径映射

7. 知识库链路：从 manager 主副本到 skills/kb-*
   - 主副本：manager 端 KnowledgeReader 读取 organization 与 app 的知识库
   - app 初始化：app_initialize handler 调 KnowledgeReader.WalkFiles 遍历主副本，
     通过 UploadAppRuntimeFile → agent /v1/scopes/apps/<id>/runtime/file PUT
     写入 skills/kb-*
   - 增量同步：knowledge_sync_node job 周期对比主副本与节点副本，差异时
     重传文件
   - 历史 knowledge/ 目录的兼容说明（legacy）

8. 注入 vs 运行时生成（总表）
   | 路径 | 来源 | 写入方 | 何时写 | app 重启清空 | app restart 命令清空 |
   | SOUL.md | manager 注入 | app_initialize | 创建时 | 否 | 否 |
   | config.yaml | manager 注入 | app_initialize | 创建时 / 配置变更 | 否 | 否 |
   | skills/kb-* | manager 注入 | app_initialize / knowledge_sync_node | 创建时 / 同步时 | 否 | 否 |
   | skills/<非 kb-*> | 镜像自带 | Hermes 首次启动复制 | 容器创建后 | 否 | 否 |
   | workspace/ | agent 预建 | ensureScope | scope 建立时 | 否 | 否（用户文件） |
   | sessions/ | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 是（commit 40f01a8） |
   | memories/ | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
   | logs/ | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
   | state.db / kanban.db | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |
   | gateway.lock / gateway_state.json | Hermes 生成 | Hermes 进程 | 运行时 | 是（lock）/ 否 | 否 |
   | weixin/accounts/* | Hermes 生成 | Hermes 进程 | 绑渠道后 | 否 | 否 |
   | sandboxes/ / platforms/ / cron/ / cache/ | Hermes 生成 | Hermes 进程 | 运行时 | 否 | 否 |

9. 生命周期事件
   - 启动：从 /opt/data 读 SOUL / config / skills；workspace 必须存在
   - 停止：保留挂载内容
   - app_health_check 自动拉起（commit 040878c）
   - app restart 命令清空 session（commit 40f01a8）让新配置进入对话
   - 配置变更（SOUL / config / skills）触发重新注入 + 重启

10. 排查 cheatsheet
    - 容器没起来 → 看 app_initialize job 状态 + agent docker proxy 日志
    - 环境变量没生效 → 看容器 inspect Env + manager 注入日志
    - 知识库看不到 → 看 knowledge_sync_node job 状态 + skills/kb-* 是否在
    - workspace 不存在 → agent ensureScope 失败
```

### 6.9 docs/runtime-agent.md（从 auto-enroll-principles 重构）

```text
> 摘要：runtime-agent 的身份模型、enroll 流程、心跳与重新注册、
> manager 主动探测、安全边界。部署细节在 deploy/runtime-agent/README.md。

1. 目标与设计原则
2. 身份与凭证（agent_id / enrollment_secret / agent_token）
3. Enroll 流程
4. 心跳与重新注册
5. 主动探测与 degraded
6. 安全边界
7. 运维含义（新增节点 / 替换硬件 / 容量调整 / token 异常 / 密钥轮换）
```

内容大部分搬自原文，但所有事实必须对照当前代码核实。

### 6.10 deploy/README.md

```text
> 摘要：生产部署的四个独立运行包（new-api / ollama / runtime-agent / manage）
> 与推荐部署顺序。

1. 部署拓扑（哪个包部署到哪台机器）
2. 推荐部署顺序
3. 防火墙摘要
4. 真实值放置位置约定（.env / config/*.yaml / TLS 不入 git）
5. 跳转：各子包 README、operations.md
```

### 6.11 deploy/operations.md（合并 backup.md + upgrade.md）

```text
> 摘要：备份 / 恢复 / 升级 / 紧急回滚 / 常见故障排查。

1. 数据范围（PostgreSQL / 知识库主副本 / 节点 state_dir）
2. 备份策略与脚本
3. 恢复演练步骤
4. 升级流程（SemVer / 迁移 / 滚动替换）
5. 紧急回滚
6. 常见故障排查（manager / agent / Hermes 容器层各一）
```

### 6.12 deploy/runtime-agent/README.md

```text
> 摘要：每台 Runtime Node 部署 oc-runtime-agent 的完整步骤。

1. 启动（compose）
2. 必改配置（.env + agent.yaml 字段表，合入旧 docs 版本的完整字段）
3. 防火墙
4. 状态检查
5. 验证清单（节点出现 / 状态 / 探测）
6. 常见问题
```

### 6.13 deploy/{manage,new-api,ollama}/README.md

按现状保留，仅在以下情况修改：

- 开头未加摘要 → 加摘要
- 出现 openclaw / 阶段性叙述 → 修订
- 配置字段与当前代码不一致 → 修正

## 7. README.md 文档导航新结构

文档导航章节替换为：

```markdown
## 文档导航

### 开发与设计

- [架构总览](./docs/architecture.md) — 模块图、拓扑、关键数据流；新协作者从这里读起
- [本地开发指南](./docs/local-development.md) — Make 目标、调试账号、常见问题
- [产品设计](./docs/product-design.md) — 角色、对象模型、业务流程、权限
- [技术设计](./docs/technical-design.md) — 后端模块、状态机、接口契约、job
- [Hermes 容器运行机制](./docs/hermes-container.md) — 创建链路、挂载、注入、知识库
- [runtime-agent 工作原理](./docs/runtime-agent.md) — 注册、心跳、探测
- [配置参考](./docs/configuration.md) — manager.yaml / agent.yaml / .env
- [用户手册](./docs/user-manual.md) — 三类角色操作
- [API 契约](./openapi/openapi.yaml) — OpenAPI 3.0
- [协作规范](./AGENTS.md) — 提交、注释、测试规范

### 部署与运维

- [部署总览](./deploy/README.md) — 四个运行包与部署顺序
- [运维手册](./deploy/operations.md) — 备份、恢复、升级、回滚、排查
- [manager 服务部署](./deploy/manage/README.md)
- [new-api 部署](./deploy/new-api/README.md)
- [ollama 部署](./deploy/ollama/README.md)
- [runtime-agent 部署](./deploy/runtime-agent/README.md)
```

## 8. 验证标准

实施完成时必须满足：

1. **文件清单一致**：`docs/`、`deploy/` 下的 .md 文件数量与 §2 一致；删除清单
   §3 的文件不再存在。
2. **链接可达**：从 README.md 文档导航出发，能 1 跳到达每个保留的 .md；
   `grep -r ']\(.\?/docs/' README.md deploy/README.md` 无死链。
3. **术语统一**：`rg -i 'openclaw' --type md` 在 docs/ deploy/ README.md
   下无命中（本 spec 文件不计）。
4. **阶段性叙述清空**：`rg -i 'v1\.0\.[01]| GA |本次迭代' --type md` 在
   保留文档中无命中。
5. **摘要规范**：保留/新建的每个 .md 第一个 `>` 引用块紧跟在 `#` 标题之后。
6. **关键事实校对**：hermes-container.md 中的环境变量表、挂载表、目录归属
   表与当前代码一致（人工对照）。
7. **行数控制**：product-design.md ≤ 600 行；technical-design.md ≤ 1200 行。

## 9. 实施风险与边界

- **激进重构容易丢信息**：每个被重构/合并的旧文档，实施时必须在临时
  分支保留一份，确认新文档覆盖了所有有效信息后再删。
- **事实校对依赖代码现状**：实施过程中若代码与文档冲突，**以代码为准**；
  代码本身有疑似 bug 的，记录在 PR 描述里，不在文档里解释。
- **范围外**：本次不修改 OpenAPI、不动 web/、不重写 AGENTS.md、不重写
  deploy 子目录的运行包脚本与 compose 文件。
