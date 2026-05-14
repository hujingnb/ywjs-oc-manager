# 文档重构实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按 `docs/superpowers/specs/2026-05-14-docs-restructure-design.md` 把 oc-manager 仓库的文档全部重写为 15 个 .md，删除 openclaw 残留与历史草稿，新增 Hermes 容器专题。

**Architecture:** 一次性 `git rm` 全部旧 .md，按 spec §6 章节大纲逐个从零写新文档；每写完一个文档自检并单独 commit；最后重写 README.md 作为统一入口；末位跑 spec §8 全套验证。

**Tech Stack:** 纯 Markdown 文档；事实校对靠 ripgrep 阅读 Go 源码与 yaml example。

**约束（全任务通用）：**

- 直接在 `master` 分支工作，不创建 worktree。
- 旧文档**只作素材**，不复制粘贴。事实陈述以**当前代码**为准。
- 每个新文档头部必须按 spec §5.1 加 `>` 引用块摘要。
- 全文不出现 `openclaw` / `Openclaw` / `OPENCLAW` / `OC Manager` / `v1.0.0` / `v1.0.1` / `本次迭代` / `bootstrap token`（已废弃概念）。
- 不在文档里留 `<!-- TODO -->`；遇到代码也无法答的事实，停下来问用户。
- 每个 commit 走 Conventional Commits（参考 `AGENTS.md`）；commit 消息必须中文，第一行简短摘要，正文补充背景。

---

## 文件结构

最终落地的 15 个 .md（不含 AGENTS.md 与本计划/spec）：

| 路径 | 职责 | 行数上限 |
|---|---|---|
| `README.md` | 项目入口 + 文档导航 | 无 |
| `docs/architecture.md` | 模块图、拓扑、关键数据流 | 无 |
| `docs/local-development.md` | 本地开发命令与排查 | 无 |
| `docs/configuration.md` | manager.yaml / agent.yaml / .env 字段 | 无 |
| `docs/product-design.md` | 业务角色、对象、流程、权限 | ≤ 600 |
| `docs/technical-design.md` | 后端模块、状态机、接口、job | ≤ 1200 |
| `docs/user-manual.md` | 三类角色前端操作 | 无 |
| `docs/hermes-container.md` | Hermes 容器机制（新增） | 无 |
| `docs/runtime-agent.md` | runtime-agent 工作原理 | 无 |
| `deploy/README.md` | 生产部署总览 | 无 |
| `deploy/operations.md` | 备份/恢复/升级/排查 | 无 |
| `deploy/manage/README.md` | manager 运行包部署 | 无 |
| `deploy/new-api/README.md` | new-api 运行包部署 | 无 |
| `deploy/ollama/README.md` | ollama 运行包部署 | 无 |
| `deploy/runtime-agent/README.md` | runtime-agent 运行包部署 | 无 |

保留：`AGENTS.md`、本计划、spec、所有非 .md（compose / env example / nginx.conf）。

---

## Task 1: 备份旧文档快照并批量删除

**Files:**
- Backup（临时）: `/tmp/oc-manager-docs-backup-2026-05-14/`
- Delete: spec §3.1 列出的全部路径

- [ ] **Step 1: 把待删除的旧文档复制到 /tmp 做素材索引（不入 git）**

```bash
SRC=/home/hujing/dir/software/ywjs/oc-manager
DST=/tmp/oc-manager-docs-backup-2026-05-14
mkdir -p "$DST/docs" "$DST/deploy"
cp "$SRC/README.md" "$DST/README.md"
cp -r "$SRC/docs/." "$DST/docs/"
cp -r "$SRC/deploy/." "$DST/deploy/"
ls -la "$DST/docs" "$DST/deploy" "$DST"
```

Expected: 备份目录里看到所有现存的 docs 和 deploy 文件。后续任务写新文档时用 Read 这个备份目录里的旧文档作为素材索引（不复制粘贴）。

- [ ] **Step 2: 删除 docs/ 下所有旧文件，仅保留本次 spec 与本计划**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
git rm docs/architecture.md docs/configuration.md docs/local-development.md \
       docs/openclaw-manager-design.md docs/openclaw-manager-technical-design.md \
       docs/runtime-agent-auto-enroll-principles.md docs/runtime-agent-deployment.md \
       docs/user-manual.md docs/verification-report.md
git rm -r docs/plans docs/specs
git rm -r docs/superpowers/plans
# superpowers/specs 下保留本次 spec
git rm docs/superpowers/specs/2026-05-09-auth-authorizer-consolidation-design.md \
       docs/superpowers/specs/2026-05-09-backend-cleanup-collection-design.md \
       docs/superpowers/specs/2026-05-09-frontend-list-form-pattern-design.md \
       docs/superpowers/specs/2026-05-09-openapi-code-first-design.md \
       docs/superpowers/specs/2026-05-09-slog-traceid-design.md \
       docs/superpowers/specs/2026-05-10-runtime-agent-auto-enroll-design.md \
       docs/superpowers/specs/2026-05-11-chinese-comments-design.md \
       docs/superpowers/specs/2026-05-11-permission-fix-design.md \
       docs/superpowers/specs/2026-05-11-permission-management-fix-design.md \
       docs/superpowers/specs/2026-05-12-app-organization-name-display-design.md \
       docs/superpowers/specs/2026-05-12-deploy-compose-split-design.md \
       docs/superpowers/specs/2026-05-12-org-code-login-design.md \
       docs/superpowers/specs/2026-05-12-runtime-node-resource-trends-design.md \
       docs/superpowers/specs/2026-05-12-test-comments-design.md \
       docs/superpowers/specs/2026-05-12-usage-page-data-design.md \
       docs/superpowers/specs/2026-05-12-usage-page-experience-design.md \
       docs/superpowers/specs/2026-05-13-app-model-governance-design.md \
       docs/superpowers/specs/2026-05-13-recreate-member-app-design.md \
       docs/superpowers/specs/2026-05-14-openclaw-to-hermes-design.md
```

- [ ] **Step 3: 删除 deploy/ 下所有旧 .md**

```bash
git rm deploy/README.md deploy/backup.md deploy/upgrade.md \
       deploy/manage/README.md deploy/new-api/README.md \
       deploy/ollama/README.md deploy/runtime-agent/README.md
```

- [ ] **Step 4: 删除根 README.md**

```bash
git rm README.md
```

- [ ] **Step 5: 验证删除清单与 spec §3.1 一致**

```bash
find docs deploy -name '*.md' -type f | sort
test ! -f README.md && echo "README removed"
```

Expected：仅看到 `docs/superpowers/specs/2026-05-14-docs-restructure-design.md` 与 `docs/superpowers/plans/2026-05-14-docs-restructure.md`；deploy/ 下无任何 .md；根目录无 README.md。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
chore(docs): 清理旧文档准备重写

按 docs/superpowers/specs/2026-05-14-docs-restructure-design.md §3.1 删除
全部旧 README.md / docs/*.md / deploy/*.md（含 plans 与 specs 历史草稿、
v1.0/v1.0.1 验证报告、openclaw 命名的设计文档）。后续按 spec §6 章节大纲
从零重写。本次重构 spec 与计划保留。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 写 docs/architecture.md

**Files:**
- Create: `docs/architecture.md`
- Read for facts:
  - `internal/api/` 子目录列表
  - `internal/service/` 子目录列表
  - `internal/domain/`、`internal/store/`、`internal/scheduler/`、`internal/worker/`、`internal/integrations/`、`internal/runtime/imagesync/`
  - `web/src/` 顶层结构
  - `runtime/agent/`、`runtime/hermes/`
  - 旧 `/tmp/oc-manager-docs-backup-2026-05-14/docs/architecture.md`（仅作素材）

- [ ] **Step 1: 读源码确认模块边界**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
ls internal/ internal/api internal/service internal/integrations internal/worker
ls web/src
ls runtime/
```

记录每个目录的真实子目录列表，作为「模块清单」的事实来源。

- [ ] **Step 2: 读旧 architecture.md 与 README 抽取还有效的内容**

```bash
cat /tmp/oc-manager-docs-backup-2026-05-14/docs/architecture.md
cat /tmp/oc-manager-docs-backup-2026-05-14/README.md
```

抽取 ASCII 拓扑图（manager / new-api / ollama / runtime-agent / Hermes）；模块描述、数据流叙述需要重写。

- [ ] **Step 3: 写 `docs/architecture.md`**

文档骨架（spec §6.2）：

```markdown
# 架构总览

> manager 与 runtime-agent 的模块图、节点拓扑与关键数据流。
> 新协作者读完本文，应能理解 manager / agent / new-api / Hermes 容器
> 之间的边界、调用方向与持久化划分。

## 1. 系统拓扑

<ASCII 图：manager 所在机器 + 多个 Runtime Node>
（节点：Browser / Vue SPA / manager-api / PostgreSQL / Redis / new-api / ollama
 / Runtime Node n × oc-runtime-agent {docker proxy:7001, file API:7002} / Hermes 容器 ×N）

## 2. 模块边界

- manager 后端（internal/）：按目录列出各模块职责（api / service / domain /
  store / scheduler / worker / integrations / runtime/imagesync / auth / files / log / redis）
- manager 前端（web/src/）：pages / api / stores / components / layouts / domain
- runtime-agent（runtime/agent/）：docker proxy + file API + enroll/heartbeat 客户端
- Hermes 容器（runtime/hermes/）：构建上下文与挂载约定（细节见 hermes-container.md）

## 3. 数据持久化划分

| 存储 | 内容 | 备份策略 |
| PostgreSQL | 业务库 / job / 审计 | deploy/operations.md |
| Redis | 队列 / 短期状态 / 锁 | 不需要长期备份 |
| agent 文件系统 | <node.data_root>/apps/<id>/.hermes（挂载到 Hermes /opt/data） | 详见 hermes-container.md |
| Hermes 镜像 | skills 内置库 / bin | 由 runtime/hermes 构建产物决定 |

## 4. 关键数据流

- 成员 onboarding：注册 → 创建用户 → app_initialize job → 容器就绪
- 知识库同步：manager 主副本 → knowledge_sync_node job → agent /v1/scopes/.../runtime/file PUT
- 容器生命周期：app_initialize / app_health_check / app restart
- token 用量直查：UI → manager-api → new-api（不缓存）

## 5. 跨模块约束

- 权限：所有 Can* 谓词在 internal/auth/authorizer.go（AGENTS.md 已写明）
- OpenAPI 同步：handler 注解 → openapi/openapi.yaml → web/src/api/generated.ts
- 镜像同步：runtime/imagesync 以 Docker ImageID 为对账锚点
```

实际写作时不要把 `<...>` 占位符留在文档里——把每个表/段落用真实内容补齐。

- [ ] **Step 4: 自检**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
head -5 docs/architecture.md   # 摘要紧跟标题
rg -i 'openclaw|本次迭代|v1\.0\.[01]' docs/architecture.md   # 无命中
rg '\[.+\]\(.+\)' docs/architecture.md   # 列出所有链接，确认相对路径正确
```

Expected：摘要在；术语扫描无命中；内链全部相对路径且文件即将在后续 task 创建。

- [ ] **Step 5: Commit**

```bash
git add docs/architecture.md
git commit -m "$(cat <<'EOF'
docs(architecture): 从零重写架构总览

新增模块边界、数据持久化划分、关键数据流、跨模块约束四类章节。事实以
internal/、web/src、runtime/ 当前目录结构为准；删除 openclaw 残留与
v1.0 阶段叙述。Hermes 容器机制单独拆到 docs/hermes-container.md，本文
只保留拓扑层视角。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 写 docs/local-development.md

**Files:**
- Create: `docs/local-development.md`
- Read: `Makefile`, `docker-compose.yml`, `CLAUDE.md`（调试账号一节）
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/docs/local-development.md`

- [ ] **Step 1: 抽取 Makefile 目标清单**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
grep -E '^[a-zA-Z][a-zA-Z0-9_-]*:' Makefile | sort -u
```

记录所有目标作为「常用 Make 目标速查」的事实来源。

- [ ] **Step 2: 读 docker-compose.yml 确认本地服务清单**

```bash
sed -n '/services:/,/^volumes:\|^networks:/p' docker-compose.yml | head -120
```

- [ ] **Step 3: 写 `docs/local-development.md`**

骨架（spec §6.3）：

```markdown
# 本地开发指南

> 在 Linux + Docker 24+ 环境起本地 manager + agent + 依赖、跑测试、
> 定位常见问题。新协作者按本文一遍走通即可进入正常开发。

## 1. 前置条件

- Linux + Docker ≥ 24
- make
- 至少 8 GB 空闲内存

## 2. 第一次起本地

复制配置、构建 Hermes 镜像、起 compose、跑迁移、注入种子。
（按当前 Makefile 实际目标顺序：build-hermes-runtime → check-compose → dev-up → migrate-up → seed-admin）

## 3. 调试账号

| 角色 | 组织标识 | 用户名 / 密码 |
|---|---|---|
| new-api 管理员 | — | admin / admin123 |
| manager 平台管理员 | 空 | admin / admin123 |
| manager 测试组织管理员 | test-org | test-org / test-org123 |
| manager 测试组织成员 | test-org | test-org-user1 / test-org-user1 |

## 4. 常用 Make 目标

按类别（后端测试 / 前端 / 代码生成 / 运行）逐条列出，含一句话说明。

## 5. 常见问题排查

- compose 起不来 → make check-compose
- 数据库连不上 → 看 .env 是否覆盖端口、看 manager-postgres 容器日志
- web typecheck 失败 → 跑 make web-types-gen 检查 OpenAPI 是否漂移
```

调试账号一节直接抄 `CLAUDE.md` 的「本地调试账号」section（这是 CLAUDE.md 已写明的事实）。

- [ ] **Step 4: 自检 + Commit**

```bash
rg -i 'openclaw|本次迭代|v1\.0\.[01]' docs/local-development.md
head -5 docs/local-development.md
git add docs/local-development.md
git commit -m "$(cat <<'EOF'
docs(local-development): 从零重写本地开发指南

按当前 Makefile 实际目标列出第一次起本地的命令序列、调试账号、Make
目标速查与常见排查路径。删除旧文件里已废弃的 docker-compose 路径与
v1.0 阶段说明。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 写 docs/configuration.md

**Files:**
- Create: `docs/configuration.md`
- Read: `config/manager.example.yaml`, `config/agent.example.yaml`, `.env.example`
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/docs/configuration.md`

- [ ] **Step 1: 抽取 example yaml 字段**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
cat config/manager.example.yaml
cat config/agent.example.yaml
cat .env.example 2>/dev/null || echo "no .env.example"
```

逐字段记录：key / 类型 / 默认值 / 含义来源（如果 example 里没注释，去对应 Go struct 找）。

- [ ] **Step 2: 写 `docs/configuration.md`**

骨架（spec §6.4）：

```markdown
# 配置参考

> manager.yaml、agent.yaml 与 .env 全部字段的含义与默认值。
> 部署或排查配置类问题时按章节定位字段。

## 1. config/manager.yaml

按 example 的顶层 key 分小节（如 server / database / redis / auth /
security / new_api / runtime / scheduler / logging / 等实际存在的小节）。
每节给一张字段表：name / type / 默认 / 说明。

## 2. config/agent.yaml

按 example 的顶层 key 分小节（agent / manager 两块）。
每节给一张字段表。`agent.max_apps` 注明会随 enroll 同步到 manager 端
（参考 docs/runtime-agent.md §3）。

## 3. .env / 端口映射

按 .env.example 列出可覆盖的宿主端口映射变量，给出本地默认值表
（与 README 的端口约定保持一致）。

## 4. 密钥生成与轮换

- security.master_key：openssl rand -base64 32
- runtime.enrollment_secret：openssl rand -base64 32（manager + 所有 agent 共享）
- 轮换流程：同步修改 → 重启 manager → 逐节点重启 agent
```

字段表必须以 example yaml 实际存在的字段为准，不要凭空列字段。

- [ ] **Step 3: 自检 + Commit**

```bash
rg -i 'openclaw|OPENCLAW_|本次迭代|v1\.0\.[01]' docs/configuration.md
head -5 docs/configuration.md
git add docs/configuration.md
git commit -m "$(cat <<'EOF'
docs(configuration): 从零重写配置参考

按 config/manager.example.yaml 与 config/agent.example.yaml 的实际字段
重新整理，新增 .env 端口映射表与密钥生成/轮换流程。删除已不存在的字段
与 OPENCLAW_* 前缀环境变量描述。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 写 docs/product-design.md

**Files:**
- Create: `docs/product-design.md`（≤ 600 行）
- Read: `internal/auth/authorizer.go`, `internal/domain/`, `internal/service/` 关键服务命名
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/docs/openclaw-manager-design.md`

- [ ] **Step 1: 读权限实现与领域枚举**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
sed -n '1,200p' internal/auth/authorizer.go
ls internal/domain/
ls internal/service/
```

记录所有 `Can*` 函数名 + 三类角色的判定规则；记录 domain 包里所有枚举类型（如 AppStatus、RuntimeNodeStatus、JobStatus 等实际存在的）。

- [ ] **Step 2: 写 `docs/product-design.md`**

骨架（spec §6.5），目标 ≤ 600 行。重点不重复 architecture.md 已说过的拓扑/模块。

```markdown
# 产品设计

> 业务角色、对象模型、核心流程与权限模型。本文回答"系统在业务上做什么"，
> 实现细节见 technical-design.md。

## 1. 角色

- 平台管理员
- 组织管理员
- 组织成员

每类一节：定位、典型操作、所属对象、权限边界。

## 2. 对象模型

每个对象一张表（属性 / 关键状态 / 关联关系）：组织 / 成员 / 应用 /
节点 / 知识库 / 渠道 / 用量。

## 3. 核心业务流程

- 注册与登录
- app 初始化（成员创建 → 自动分配节点 → app_initialize job → 容器就绪）
- 知识库同步
- 容器治理（启停 / 重启 / 健康自愈 / 重建）
- 用量直查 new-api

每个流程给一张时序简图（文字版即可）+ 关键状态变更。

## 4. 权限模型

按 Can* 谓词分类（如 CanManageOrganization / CanViewApp / CanWriteKnowledge 等），
对照 internal/auth/authorizer.go 实际函数名。每个谓词一行：触发场景 +
三类角色判定。

## 5. 计费与 token credit

组织充值 → token credit 余额 → app 调用 → new-api 计费扣减的链路。
不深入 new-api 内部实现。
```

- [ ] **Step 3: 自检**

```bash
wc -l docs/product-design.md   # ≤ 600
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' docs/product-design.md
head -5 docs/product-design.md
```

如果超过 600 行，砍掉与 architecture.md 重复的拓扑描述、合并冗长状态说明。

- [ ] **Step 4: Commit**

```bash
git add docs/product-design.md
git commit -m "$(cat <<'EOF'
docs(product-design): 从零重写产品设计 PRD

按业务视角整理角色、对象模型、核心流程与权限模型。权限章节按
internal/auth/authorizer.go 的 Can* 谓词分类。删除与 README/architecture
重复的拓扑描述、已实现 TODO、v1.0 阶段叙述，控制行数 ≤ 600。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: 写 docs/technical-design.md

**Files:**
- Create: `docs/technical-design.md`（≤ 1200 行）
- Read: `internal/api/`, `internal/service/`, `internal/domain/`, `internal/store/`, `internal/scheduler/`, `internal/worker/`, `internal/integrations/`, `internal/runtime/imagesync/`, `internal/auth/`, `internal/files/`, `internal/migrations/`
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/docs/openclaw-manager-technical-design.md`

- [ ] **Step 1: 抽取模块清单**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
for d in internal/api internal/service internal/domain internal/store \
         internal/scheduler internal/worker internal/integrations \
         internal/runtime/imagesync internal/auth internal/files internal/log internal/redis; do
  echo "== $d =="
  ls $d 2>/dev/null
done
```

- [ ] **Step 2: 抽取状态枚举**

```bash
grep -rn 'type.*Status' internal/domain/ | head -30
grep -rn 'const' internal/domain/ | head -40
```

每个 Status 类型抽取所有枚举值与状态迁移注释（如果有）。

- [ ] **Step 3: 抽取 worker 关键 handler**

```bash
ls internal/worker/handlers/
```

每个 handler 一行说明它的触发条件 + 主要行为。

- [ ] **Step 4: 写 `docs/technical-design.md`**

骨架（spec §6.6），目标 ≤ 1200 行。

```markdown
# 技术设计

> 后端模块边界、状态机、接口契约、job 与 worker 模型。本文回答"实现是怎么
> 组织的"，业务规则见 product-design.md。

## 1. 模块清单

按 internal/ 顶层目录分节，每节列子目录 + 一句话职责。

## 2. 状态机

每个 Status 类型一节：枚举值 + 合法迁移 + 触发条件。引用代码位置
(file:line) 方便核对。

## 3. 接口契约

简述："所有 HTTP 接口由 swag 注解扫描生成 openapi/openapi.yaml，前端
类型由 make web-types-gen 生成 web/src/api/generated.ts。"
本文不重复 yaml 内容，给出权威链接：[openapi/openapi.yaml](../openapi/openapi.yaml)。

## 4. job 与 worker 模型

- scheduler 包结构：队列、调度策略、retry/dedup 约定
- worker 包结构：handler 注册、依赖注入
- 关键 handler 一览表：name / trigger / 主要副作用

## 5. 数据访问层

- sqlc 生成的 store 约定（参考 AGENTS.md 的 sqlc-generate 段）
- 关键 query 命名习惯
- users.deleted_at 语义（参考 CLAUDE.md / AGENTS.md）

## 6. 跨模块约束

- OpenAPI 同步：handler 注解 → openapi.yaml → web/src/api/generated.ts，
  `make openapi-check` 守门
- 权限校验：所有 Can* 谓词集中在 internal/auth/authorizer.go
- 审计日志：审计点位与字段
- 镜像同步：internal/runtime/imagesync 以 ImageID 对账
```

- [ ] **Step 5: 自检**

```bash
wc -l docs/technical-design.md   # ≤ 1200
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' docs/technical-design.md
head -5 docs/technical-design.md
```

- [ ] **Step 6: Commit**

```bash
git add docs/technical-design.md
git commit -m "$(cat <<'EOF'
docs(technical-design): 从零重写技术设计

按当前 internal/ 模块结构重写模块清单、状态机、接口契约、job/worker
模型、数据访问层与跨模块约束。OpenAPI 章节链接 openapi/openapi.yaml
不重复内容；权限章节指向 internal/auth/authorizer.go；行数控制 ≤ 1200。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: 写 docs/user-manual.md

**Files:**
- Create: `docs/user-manual.md`
- Read: `web/src/pages/`（所有页面）、`web/src/router`（如有）
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/docs/user-manual.md`

- [ ] **Step 1: 抽取前端页面清单**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
find web/src/pages -type f -name '*.vue' | sort
```

按子目录归类页面（如 platform/、org/、apps/、auth/、runtime-nodes/、knowledge/）。

- [ ] **Step 2: 写 `docs/user-manual.md`**

骨架（spec §6.7）：

```markdown
# 用户手册

> 平台管理员 / 组织管理员 / 组织成员三类角色在 manager 前端能做什么。
> 按角色组织章节，每章给出页面入口、典型操作步骤与权限边界。

## 1. 平台管理员

- 组织管理（列表 / 创建 / 启停 / 删除 / token credit 充值）
- 运行节点（列表 / 状态 / 探测情况）
- 平台审计日志

每个功能：页面路径 → 主要操作步骤（点哪 / 填什么）→ 权限边界。

## 2. 组织管理员

- 成员（列表 / 新建成员 / 重置密码 / 禁用启用）
- 应用（控制台 / 重启 / 日志 / 工作目录浏览）
- 知识库（组织级 + 应用级）
- 渠道绑定（微信扫码）
- 用量（按应用 / 按时间）

## 3. 组织成员

- 应用控制台
- 工作目录浏览 / 下载 / 打包下载
- 应用日志
- 应用级知识库
```

页面路径与按钮文案以当前 web/src/pages 实际命名为准。

- [ ] **Step 3: 自检 + Commit**

```bash
rg -i 'openclaw|本次迭代|v1\.0\.[01]' docs/user-manual.md
head -5 docs/user-manual.md
git add docs/user-manual.md
git commit -m "$(cat <<'EOF'
docs(user-manual): 从零重写用户手册

按 web/src/pages 当前页面组织三类角色的操作步骤、页面入口与权限边界。
删除已不存在的页面与 v1.0 阶段说明。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: 写 docs/hermes-container.md（核心专题）

**Files:**
- Create: `docs/hermes-container.md`
- Read for facts（强制全部读，逐条核对 spec §6.8 表里的环境变量与挂载）：
  - `internal/worker/handlers/app_initialize.go`
  - `internal/worker/handlers/knowledge_sync.go`
  - `internal/integrations/hermes/config.go`
  - `internal/integrations/runtime/agent_backed.go`
  - `internal/runtime/imagesync/service.go`
  - `runtime/agent/scopes.go`
  - `runtime/agent/proxy.go`
  - `runtime/hermes/`（Dockerfile / 入口脚本）

- [ ] **Step 1: 核对环境变量表**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
sed -n '80,120p' internal/integrations/hermes/config.go
grep -n 'OPENAI\|WEIXIN\|GATEWAY' internal/integrations/hermes/config.go
```

逐项核对 spec §6.8 第 3 节环境变量表的 key / 含义 / 来源 / 何时追加；
如有差异以代码为准。

- [ ] **Step 2: 核对挂载结构**

```bash
sed -n '300,330p' internal/worker/handlers/app_initialize.go
sed -n '190,230p' runtime/agent/scopes.go
```

确认 `<dataRoot>/apps/<appID>/.hermes` → `/opt/data` 的 bind mount 仍然是唯一挂载；
确认 agent ensureScope 预建的目录列表。

- [ ] **Step 3: 核对镜像同步**

```bash
sed -n '50,110p' internal/runtime/imagesync/service.go
```

确认 ImageID 对账逻辑 + /v1/images/load 接口。

- [ ] **Step 4: 核对知识库同步链路**

```bash
sed -n '80,160p' internal/worker/handlers/knowledge_sync.go
grep -n 'KnowledgeReader\|UploadAppRuntimeFile\|kb-app\|kb-org' \
  internal/worker/handlers/app_initialize.go \
  internal/worker/handlers/knowledge_sync.go
```

确认知识库写入路径前缀（`skills/kb-app-*`、`skills/kb-org-*`）。

- [ ] **Step 5: 核对 docker proxy 路由**

```bash
sed -n '30,90p' runtime/agent/proxy.go
```

- [ ] **Step 6: 写 `docs/hermes-container.md`**

按 spec §6.8 第 1–10 节完整展开。每个章节用 Step 1–5 实际读到的事实填表，
不照搬 spec 的占位文字；如代码与 spec 表里某项不符，以代码为准并在 commit 消息里说明。

具体规范：
- 第 4 节挂载目录树用代码块给出，分类标记必须与实际容器一致（参考 brainstorming 时
  用户提供的实际 `/opt/data` 目录树作为视觉模板）
- 第 7 节注入 vs 运行时生成的总表至少包含 spec §6.8 第 8 节列出的 12+ 路径
- 第 8 节生命周期事件引用真实 commit 短 hash（040878c app_health_check / 40f01a8 app restart 清空 session）

- [ ] **Step 7: 自检**

```bash
head -5 docs/hermes-container.md
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' docs/hermes-container.md
# 重点验证关键 key 出现
rg 'OPENAI_API_KEY|OPENAI_BASE_URL|GATEWAY_ALLOW_ALL_USERS|WEIXIN_DM_POLICY' docs/hermes-container.md
rg '/opt/data|skills/kb-' docs/hermes-container.md
```

Expected：术语扫描无命中；关键环境变量与挂载路径全部出现。

- [ ] **Step 8: Commit**

```bash
git add docs/hermes-container.md
git commit -m "$(cat <<'EOF'
docs(hermes-container): 新增 Hermes 容器运行机制专题

完整覆盖 manager → runtime-agent → Hermes 容器的链路：镜像同步策略、
容器创建入参（含环境变量与挂载表）、/opt/data 目录按"manager 注入 /
镜像自带 / 运行时生成"分类的树状结构、工作目录定位、知识库写入路径
(skills/kb-*)、运行时与配置变更对挂载内容的影响、排查 cheatsheet。
所有事实对照 internal/worker/handlers/app_initialize.go、
internal/integrations/hermes/config.go、runtime/agent/scopes.go 等当前
代码核实。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: 写 docs/runtime-agent.md

**Files:**
- Create: `docs/runtime-agent.md`
- Read: `runtime/agent/` 全目录、`internal/service/runtime/`（如存在）、`internal/api/handlers/agent*` / `internal/api/handlers/runtime*`
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/docs/runtime-agent-auto-enroll-principles.md`

- [ ] **Step 1: 核对 enroll 接口**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
grep -rn 'enroll\|/agent/enroll' internal/api/ runtime/agent/ | head -30
grep -n 'enrollment_secret\|agent_token\|agent_id' runtime/agent/*.go | head -30
```

- [ ] **Step 2: 核对心跳与探测**

```bash
grep -rn 'heartbeat\|RuntimeNodeProbeReconciler' internal/ runtime/agent/ | head -30
```

确认 failure_threshold / recovery_threshold / unreachable 状态机迁移。

- [ ] **Step 3: 写 `docs/runtime-agent.md`**

骨架（spec §6.9）：

```markdown
# runtime-agent 工作原理

> runtime-agent 的身份模型、enroll 流程、心跳与重新注册、manager 主动
> 探测、安全边界。部署细节见 deploy/runtime-agent/README.md。

## 1. 目标与设计原则

自动注册（agent 自描述）→ 不依赖 manager 手工建节点；
agent_id 持久化 → 一台机器始终一条 runtime_nodes 记录。

## 2. 身份与凭证

- agent_id：节点稳定身份，state_dir/agent-id
- enrollment_secret：部署级共享密钥，仅 enroll 用
- agent_token：节点长期凭证，manager 加密落库 + agent 持久化

## 3. Enroll 流程

agent 启动伪代码 + manager 处理伪代码 + 状态变更。

## 4. 心跳与重新注册

heartbeat 接口、401 触发 re-enroll、heartbeat_interval_seconds 来源。

## 5. 主动探测与 degraded

probe 配置 / 触发条件 / active ↔ degraded / unreachable 的差异。

## 6. 安全边界

constant-time compare / TLS / Bearer / trusted_cidr / 日志脱敏。

## 7. 运维含义

新增节点 / 替换硬件保 agent_id / 容量调整 / token 异常 / enrollment_secret 轮换。
```

- [ ] **Step 4: 自检 + Commit**

```bash
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' docs/runtime-agent.md
head -5 docs/runtime-agent.md
git add docs/runtime-agent.md
git commit -m "$(cat <<'EOF'
docs(runtime-agent): 从零重写 runtime-agent 工作原理

覆盖身份模型、自动 enroll、心跳与 401 重注册、manager 主动探测的
active/degraded/unreachable 状态机、安全边界与运维场景。事实以
runtime/agent/ 与 internal/api/handlers 当前实现为准；删除已废弃的
bootstrap token 流程。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: 写 deploy/ 四个子目录 README

**Files:**
- Create: `deploy/manage/README.md`, `deploy/new-api/README.md`, `deploy/ollama/README.md`, `deploy/runtime-agent/README.md`
- Read: 对应子目录的 `docker-compose.yml` 与 `.env.example`
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/deploy/{manage,new-api,ollama,runtime-agent}/README.md`

每个子目录 README 的骨架（spec §6.13）：

```markdown
# <运行包名> 生产部署

> 本运行包部署到 <哪台机器>，提供 <什么服务>。

## 1. 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

## 2. 必改配置

按 .env.example 与 docker-compose.yml 的实际变量列表。

## 3. 防火墙

按服务实际监听端口与"谁可以访问"约定。

## 4. 状态检查

健康检查命令与期望输出。

## 5. 常见问题
```

- [ ] **Step 1: 抽取每个子目录的 .env.example + compose**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
for pkg in manage new-api ollama runtime-agent; do
  echo "== deploy/$pkg/.env.example =="
  cat deploy/$pkg/.env.example
  echo "== deploy/$pkg/docker-compose.yml =="
  cat deploy/$pkg/docker-compose.yml
done
```

- [ ] **Step 2: 写 deploy/manage/README.md**

按上述骨架填实际字段；manage 包额外说明：依赖 new-api 与 ollama 已部署、
PostgreSQL/Redis 已起、首次运行须跑 migration + seed-admin。

- [ ] **Step 3: 写 deploy/new-api/README.md**

特别说明：自带 PostgreSQL + Redis；admin 默认账号；要给 manager 生成系统 token。

- [ ] **Step 4: 写 deploy/ollama/README.md**

特别说明：建议只允许 new-api 访问 11434；模型 pull 的流程。

- [ ] **Step 5: 写 deploy/runtime-agent/README.md**

按 spec §6.12 加详细的 agent.yaml 字段表（合入旧 docs/runtime-agent-deployment.md
里 example 完整字段），覆盖 agent.name / advertise_host / max_apps / data_root /
state_dir / docker_socket / trusted_cidr / docker_addr / file_addr 与
manager.endpoint / enrollment_secret / ca_bundle / skip_verify。

- [ ] **Step 6: 自检 + Commit**

```bash
for f in deploy/manage/README.md deploy/new-api/README.md \
         deploy/ollama/README.md deploy/runtime-agent/README.md; do
  echo "== $f =="
  head -5 "$f"
done
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' \
  deploy/manage/README.md deploy/new-api/README.md \
  deploy/ollama/README.md deploy/runtime-agent/README.md

git add deploy/manage/README.md deploy/new-api/README.md \
        deploy/ollama/README.md deploy/runtime-agent/README.md
git commit -m "$(cat <<'EOF'
docs(deploy): 从零重写四个运行包的 README

为 manage / new-api / ollama / runtime-agent 四个 deploy 子目录各写一份
独立 README，按 docker-compose.yml 与 .env.example 的实际变量与端口
重新整理。runtime-agent README 含完整 agent.yaml 字段表，替代旧
docs/runtime-agent-deployment.md。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: 写 deploy/README.md + deploy/operations.md

**Files:**
- Create: `deploy/README.md`, `deploy/operations.md`
- Read: 旧 `/tmp/oc-manager-docs-backup-2026-05-14/deploy/README.md`, `backup.md`, `upgrade.md`

- [ ] **Step 1: 写 deploy/README.md**

骨架（spec §6.10）：

```markdown
# 生产部署总览

> 生产部署的四个独立运行包（new-api / ollama / runtime-agent / manage）
> 与推荐部署顺序。

## 1. 部署拓扑

| 目录 | 部署机器 | 服务 |
|---|---|---|
| new-api/ | new-api 服务器 | new-api + PostgreSQL + Redis |
| ollama/ | Ollama 服务器 | Ollama |
| runtime-agent/ | 每台 Runtime Node | oc-runtime-agent |
| manage/ | manager 服务器 | manager-api + manager-web + nginx + PostgreSQL + Redis |

## 2. 推荐部署顺序

1. ollama → 拉模型
2. new-api → 配 ollama 渠道、生成系统 token
3. manage → 写入 new-api 地址 / token，跑迁移
4. runtime-agent ×N → enroll 自动注册

## 3. 防火墙摘要

按子目录 README 的"防火墙"段落汇总。

## 4. 真实值约定

只写入 .env / config/*.yaml / TLS 文件，不进 git。

## 5. 跳转

各子目录 README + 运维手册的相对链接列表。
```

- [ ] **Step 2: 写 deploy/operations.md**

骨架（spec §6.11）：

```markdown
# 运维手册

> 备份 / 恢复 / 升级 / 紧急回滚 / 常见故障排查。

## 1. 数据范围

- PostgreSQL：manager 业务库（schema + data）
- 知识库主副本：manager 端文件系统目录（具体路径来自 config.knowledge.root）
- 节点 state_dir：runtime-agent 持久化 agent-id / agent-token / TLS

## 2. 备份策略

pg_dump 命令；知识库主副本 rsync 命令；state_dir tar 备份。
（具体命令对照旧 backup.md，但路径与字段以当前 config example 为准。）

## 3. 恢复演练步骤

逐步还原 → 启动顺序 → 验证清单。

## 4. 升级流程

- SemVer 约定
- 数据库迁移：make migrate-up
- 滚动替换：先升 agent，再升 manager（反之亦然？参考代码与兼容性约定）

## 5. 紧急回滚

镜像 tag 回滚 + migration 回滚边界。

## 6. 常见故障排查

manager 层 / agent 层 / Hermes 容器层各一节，给定位命令与日志看哪里。
```

- [ ] **Step 3: 自检 + Commit**

```bash
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' deploy/README.md deploy/operations.md
head -5 deploy/README.md deploy/operations.md
git add deploy/README.md deploy/operations.md
git commit -m "$(cat <<'EOF'
docs(deploy): 重写生产部署总览与运维手册

deploy/README.md 给出四运行包拓扑、推荐部署顺序、防火墙摘要与跳转。
deploy/operations.md 合并原 backup.md + upgrade.md，新增"故障排查"
按 manager / agent / Hermes 三层列定位命令；事实以当前 config example
与 Makefile 为准。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: 写根 README.md

**Files:**
- Create: `README.md`
- Read 旧版本作素材：`/tmp/oc-manager-docs-backup-2026-05-14/README.md`（保留拓扑 ASCII 图与端口表）

- [ ] **Step 1: 抽取要保留的素材**

```bash
sed -n '/^```text$/,/^```$/p' /tmp/oc-manager-docs-backup-2026-05-14/README.md | head -80
```

把拓扑 ASCII 图、端口表、技术栈表、仓库结构这四块复制下来作为新 README 的"骨架填充"
（这些是事实性内容，不属于 spec 禁止的"复制粘贴"——但需要对照当前代码再核一遍）。

- [ ] **Step 2: 写 `README.md`**

骨架（spec §6.1 + §7）：

```markdown
# Agent Runtime Manager

> 面向组织的 Hermes Agent 应用管理后台。负责组织/成员/应用/Runtime Node
> 编排，对接 new-api 网关计费，集中管控运行在多个 Runtime Node 上的
> Hermes 容器。

## 核心能力

（按当前实现列，去掉 v1.0 字样与已废弃 bootstrap token 描述）

## 系统拓扑

```text
<ASCII 图，对照当前实际组件保留>
```

详见 [docs/architecture.md](./docs/architecture.md)。

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.25 / Gin / pgx / sqlc / golang-migrate / go-redis / Docker Go SDK |
| 前端 | Vue 3 / Vite 7 / Pinia / vue-router / TanStack Query / Naive UI |
| 数据 | PostgreSQL 17 / Redis 7 |
| Runtime Agent | Go 1.25 / 自签 TLS + Bearer token / Docker socket 代理 + 文件 HTTP API |
| 部署 | Docker Compose / nginx 反代 |

## 仓库结构

（按当前目录树写）

## 快速开始（本地）

（按当前 Makefile，参考 docs/local-development.md）

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

## 端口约定（本地默认）

| 端口 | 服务 |
|---|---|
| 5173 | manager-web (Vite dev) |
| 8080 | manager-api |
| 15432 | manager-postgres |
| 6379 | manager-redis |
| 6380 | new-api-redis |
| 3000 | new-api |
| 11434 | ollama |
| 7001 | oc-runtime-agent docker proxy |
| 7002 | oc-runtime-agent file API |

可通过 `.env` 覆盖宿主机端口映射，详见 [.env.example](./.env.example)。

## 健康检查

- `GET /healthz` → 200 表示 manager-api 进程存活
- 节点状态：UI "运行节点"页或 `GET /api/v1/runtime-nodes`，`last_heartbeat_at` 持续刷新代表 agent 心跳正常

## 许可与反馈

内部项目，许可与发布策略由仓库所有者确定。Issue 与改进建议通过仓库 issue 区或代码评审通道反馈。
```

- [ ] **Step 3: 自检**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
head -5 README.md
rg -i 'openclaw|本次迭代|v1\.0\.[01]|bootstrap token' README.md
# 校验所有文档导航链接都指向存在的文件
grep -oE '\(\./(docs|deploy)/[^)]+\)' README.md | sed 's/[()]//g' | sed 's|^\./||' | while read f; do
  test -f "$f" && echo "OK: $f" || echo "MISSING: $f"
done
```

Expected：所有 OK；无 MISSING。

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(readme): 从零重写项目入口与文档导航

按当前实现整理核心能力、拓扑、技术栈、仓库结构、快速开始与端口约定。
文档导航重组为「开发与设计」+「部署与运维」两栏，覆盖 docs/ 与
deploy/ 全部 .md（含子目录 README）；删除 v1.0/v1.0.1 阶段叙述与
openclaw 残留。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: 最终全量验证

按 spec §8 七条验证标准逐条跑。

- [ ] **Step 1: 文件清单一致（§8.1）**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
echo "== 期望 15 个 .md =="
ls README.md AGENTS.md 2>&1 | grep -v AGENTS.md   # README.md
find docs -maxdepth 1 -name '*.md' -type f | sort
find deploy -name '*.md' -type f | sort
```

Expected：README.md 存在；docs/ 下 8 个 .md；deploy/ 下 6 个 .md（root + operations + 4 子目录）。

- [ ] **Step 2: 删除清单已清空（§8.1 反验）**

```bash
test ! -d docs/plans && echo "docs/plans removed"
test ! -d docs/specs && echo "docs/specs removed"
test ! -d docs/superpowers/plans && echo "docs/superpowers/plans removed"
test ! -f docs/verification-report.md && echo "verification-report removed"
test ! -f docs/openclaw-manager-design.md && echo "openclaw PRD removed"
test ! -f docs/openclaw-manager-technical-design.md && echo "openclaw tech design removed"
test ! -f deploy/backup.md && echo "backup.md removed"
test ! -f deploy/upgrade.md && echo "upgrade.md removed"
```

- [ ] **Step 3: 链接可达（§8.2）**

```bash
grep -oE '\(\./[^)]+\)' README.md | sed 's/[()]//g' | sed 's|^\./||' | while read f; do
  test -e "$f" && echo "OK $f" || echo "DEAD $f"
done | sort -u
grep -oE '\(\.\./[^)]+\)' deploy/README.md | sed 's/[()]//g' | sed 's|^\.\./||' | while read f; do
  test -e "$f" && echo "OK $f" || echo "DEAD $f"
done
```

Expected：所有 OK。

- [ ] **Step 4: 术语统一（§8.3）**

```bash
rg -i 'openclaw' --type md README.md docs deploy \
  | rg -v 'docs/superpowers/specs|docs/superpowers/plans'
```

Expected：空输出。spec 与本计划文件作为历史记录会包含 openclaw 字样，所以
显式过滤掉这两个子目录。任何其他 .md 命中都必须修复。

- [ ] **Step 5: 阶段性叙述清空（§8.4）**

```bash
rg -i 'v1\.0\.[01]|本次迭代|bootstrap token' --type md README.md docs deploy \
  | rg -v 'docs/superpowers/specs|docs/superpowers/plans'
```

Expected：空输出。

- [ ] **Step 6: 摘要规范（§8.5）**

```bash
for f in README.md docs/*.md deploy/README.md deploy/operations.md \
         deploy/manage/README.md deploy/new-api/README.md \
         deploy/ollama/README.md deploy/runtime-agent/README.md; do
  # 取前 5 行，看 # 标题后第一个非空行是否以 > 起头
  awk 'NR==1 && /^# / {found_h=1; next} found_h && NF==0 {next} found_h {print FILENAME": "$0; exit}' "$f"
done
```

Expected：每行都以 `> ` 起头。

- [ ] **Step 7: 行数控制（§8.7）**

```bash
wc -l docs/product-design.md docs/technical-design.md
```

Expected：product-design.md ≤ 600；technical-design.md ≤ 1200。

- [ ] **Step 8: hermes-container 关键事实抽检（§8.6）**

```bash
# 环境变量五项全到
rg 'OPENAI_API_KEY' docs/hermes-container.md
rg 'OPENAI_BASE_URL' docs/hermes-container.md
rg 'GATEWAY_ALLOW_ALL_USERS' docs/hermes-container.md
rg 'WEIXIN_DM_POLICY' docs/hermes-container.md
rg 'WEIXIN_ACCOUNT_ID' docs/hermes-container.md
# 挂载与目录前缀
rg '/opt/data' docs/hermes-container.md
rg 'skills/kb-app' docs/hermes-container.md
rg 'skills/kb-org' docs/hermes-container.md
# 与代码事实对照
grep -c 'OPENAI_API_KEY' internal/integrations/hermes/config.go
```

Expected：文档中每项至少 1 个命中；代码侧 OPENAI_API_KEY 至少 1 个命中（确认事实未漂移）。

- [ ] **Step 9: 清理临时备份**

```bash
rm -rf /tmp/oc-manager-docs-backup-2026-05-14
```

- [ ] **Step 10: 总结**

把 §8.1–§8.7 的执行结果汇总到本计划文件末尾的"验证记录"小节，或在最终 commit
消息里报告。若全部通过，**不再额外 commit**——所有改动已经在 Task 1–12 提交。
若有任何失败项，回到对应 Task 修复后重新跑本任务。

---

## 验证记录

（实施完成后在此追加 §8 七项的真实执行输出。）
