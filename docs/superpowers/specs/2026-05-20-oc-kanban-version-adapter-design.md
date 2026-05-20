# oc-kanban 版本适配层 · 设计文档

- 日期：2026-05-20
- 状态：设计已确认，待转实施计划
- 关联：`docs/superpowers/specs/2026-05-19-hermes-task-dashboard-design.md`（任务看板原始设计）

## 1. 背景与目标

### 1.1 要解决的问题

manager 当前直接在 hermes 容器里执行 `hermes kanban ...` 命令，并按 hermes
v0.14.0 的真实 `--json` 输出，在 `internal/service/hermes_kanban_types.go` /
`hermes_kanban.go` 里硬编码了一整套 Go 类型与 CLI 调用约定（board 走全局
参数、`--skill` 单数可重复、`reassign` profile 是 positional 等）。前端
`web/src/api` 又按这套 Go 类型生成了 TS 类型。

一旦未来 hermes 升级，kanban CLI 的参数顺序、flag 名、输出结构发生变化，
manager 的 Go 代码与前端类型会被动 break，且 break 点分散、难以一次定位。
同时，未来生产环境可能同时运行多个 hermes 版本的镜像，manager 无法用同一
套代码同时适配。

### 1.2 目标

在 hermes 镜像里增加一个 `oc-kanban` 命令，作为 manager 与 hermes 之间的
**稳定适配层**：

- 对 manager 暴露一套**版本无关的稳定契约**（CLI 形态 + JSON 结构 + 错误码）。
- 对内吸收各 hermes 版本 kanban CLI 的差异。
- manager 只对 `oc-kanban` 契约编程，与 hermes 版本解耦。
- hermes 升级导致 CLI 约定变化时，只需改对应镜像变体的 `oc-kanban`，
  manager 零改动。

`oc-kanban` 与现有 `oc-entrypoint` / `oc-doctor` / `oc-info` / `oc-channel-*`
属于同一类「镜像对外命令」，构成 manager ↔ hermes 的稳定接口层。

## 2. 设计决策

本设计经过澄清问答确定了以下 5 项关键决策：

| # | 决策点 | 结论 |
|---|---|---|
| D1 | 适配范围 | **完整多版本框架**：定义一套严格的、版本无关的契约规范作为「框架」；每个 hermes 版本镜像各自实现一份符合该契约的 `oc-kanban`，构建期即完成适配。 |
| D2 | `oc-kanban` 内部调用方式 | **subprocess 包 `hermes kanban` CLI**：调用容器内 `hermes kanban` 并解析其 `--json` 输出。hermes CLI 是官方对外接口，跨版本相对稳定。 |
| D3 | 稳定契约形态 | **重新设计规整契约**：统一 `show`/`create`/写操作的返回结构与参数风格，不冻结 hermes v0.14.0 的原始输出。manager 端 Go/TS 类型与前端需跟随调整并重新验证。 |
| D4 | 版本分发 | **无运行时探测、无 adapter 分发**：不同 hermes 版本镜像各自携带不同的 `oc-kanban`，构建镜像时即完成适配。manager 直接调用 `oc-kanban`，输入输出在所有版本间保持一致，版本差异在源码层面（不同变体目录的不同实现文件）就已分开。 |
| D5 | 契约演进 | **自描述能力 + 契约版本号**：每个 `oc-kanban` 提供 `capabilities` verb，输出契约版本号与支持的 verb 清单；契约只做向后兼容的增量变更；manager 据此降级。 |

代码组织采用**方案 C**：契约一致性由「契约规范工件 + 强制契约测试」机械守护，
而非靠 review。不引入跨目录 `COPY`（详见第 6 节）。

## 3. 总览与边界

### 3.1 四个组成部分

| 部分 | 位置 | 职责 |
|---|---|---|
| 契约规范工件 | `runtime/hermes/kanban-contract/`（仓库内唯一 canonical） | 权威定义 CLI verb、各结构 JSON Schema、错误码、契约版本号规则。所有变体共同遵守的「框架」。 |
| `oc-kanban` 实现 | 每个变体目录（`hermes-main/oc-kanban.py`，未来 `hermes-v0.x/oc-kanban.py`） | 该版本专属。subprocess 调容器内 `hermes kanban`，把输出规整成契约结构。 |
| 契约一致性测试 | 每个变体 `tests/test_kanban_contract.py` | 调本变体 `oc-kanban` 各 verb，用 canonical JSON Schema 校验输出；校验失败则 `make verify-hermes-runtime` 失败，镜像发不出去。 |
| manager 改造 | `internal/service` + `internal/api` + `web/` | `HermesKanbanService` 改调 `oc-kanban`；Go/TS 类型按新契约重写；新增 capabilities 探测与按能力降级。 |

### 3.2 明确不做的事（边界）

- 不修改 hermes 上游。`oc-kanban` 只是 hermes CLI 的外层包装。
- 不做运行时版本探测 / adapter 分发。版本差异在源码层面分开，构建期完成适配。
- 不引入跨目录 `COPY`。沿用现有「build context = 变体目录」约束。
- `oc-kanban` 不持有数据、不加缓存。每次调用即时透传，与 `HermesKanbanService`
  的无状态原则一致。
- 本次只覆盖 manager 当前已用到的 15 个 kanban 能力，不预先实现 hermes 有、
  但 manager 不用的能力。

## 4. oc-kanban 对外 CLI 契约

这是 manager 唯一编程的目标。完全统一、无 positional 参数、所有 verb 同一种
参数风格。

### 4.1 命令形态与输出信封

```
oc-kanban <verb> [--flag value ...]
```

stdout 永远是单行 JSON（`watch` 除外），采用统一信封：

```jsonc
// 成功
{ "ok": true,  "data": <payload> }
// 失败
{ "ok": false, "error": { "code": "<CODE>", "message": "<可读说明>" } }
```

退出码：

- `0`：成功。
- `1`：业务错误（错误信封已写入 stdout）。
- `2`：用法错误（argparse 解析失败，无法产出信封时输出 stderr 文本）。

manager 以 stdout 信封的 `ok` 字段为权威判断依据，退出码仅作冗余信号。

### 4.2 verb 全集（15 个）

| verb | 关键 flag | data 返回 |
|---|---|---|
| `capabilities` | （无） | `Capabilities` |
| `boards` | （无） | `Board[]` |
| `list` | `--board` `--status` `--assignee` | `Task[]`（摘要） |
| `show` | `--board` `--id` | `TaskDetail` |
| `runs` | `--board` `--id` | `Run[]` |
| `stats` | `--board` | `Stats` |
| `watch` | `--board` | NDJSON 事件流（见 4.6） |
| `create` | `--board` `--title` `--assignee` `--priority` `--body` `--skill`（可重复）`--workspace` `--parent` `--max-retries` | `TaskDetail` |
| `comment` | `--board` `--id` `--body` | `TaskDetail` |
| `complete` | `--board` `--id` `--result` | `TaskDetail` |
| `block` | `--board` `--id` `--reason` | `TaskDetail` |
| `unblock` | `--board` `--id` | `TaskDetail` |
| `archive` | `--board` `--id` | `TaskDetail` |
| `reassign` | `--board` `--id` `--to` | `TaskDetail` |
| `reclaim` | `--board` `--id` | `TaskDetail` |

参数约定：

- `--board` 默认 `default`；`capabilities` 与 `boards` 不接受 `--board`。
- `--id` 表示 task id；`--to` 表示 `reassign` 的目标 profile。
- 全部使用 flag，无 positional 参数。

两处关键规整：

1. **写操作统一返回 `TaskDetail`**（更新后的完整详情）。现状 manager 写完
   只拿到 error，需再查一次；新契约写完即拿到最新状态，省一次往返。
2. **`create` 返回 `TaskDetail`** 而非现状的扁平 task，与 `show` 一致
   （`oc-kanban` 内部 create 后补一次 show）。

### 4.3 规整数据结构

时间戳统一为 Unix 秒整数（沿用现状，前端已在用）。

**`Task`**（任务摘要，亦即 `TaskDetail.task`）：

`id, title, body, assignee, status, priority, tenant, workspace_kind,
workspace_path, created_by, created_at, started_at, completed_at, result,
skills[], max_retries`

字段沿用现状（已是规整 snake_case）。可空字段在 JSON Schema 中标注 nullable。

**`TaskDetail`**（消除 create/show 不一致，写操作统一返回）：

```jsonc
{
  "task": Task,
  "latest_summary": string | null,
  "parents":  [taskId, ...],
  "children": [taskId, ...],
  "comments": [Comment, ...],
  "events":   [Event, ...]
}
```

**`Board`**：`slug, name, description, icon, color, archived, is_current,
counts{}, total`

**`Stats`**：`by_status{}, by_assignee{}, oldest_ready_age_seconds, now`
（`by_assignee` 为 `{assignee: {status: count}}` 两层映射）

**`Comment`**：`author, body, created_at`

**`Event`**：`kind, payload, created_at, run_id`
（`watch` 流与 `TaskDetail.events` 使用同一 schema）

**`Run`**：`profile, status, worker_pid, started_at, ended_at, outcome,
summary, error`。现有 `KanbanTaskRun` 类型为调研报告推测、未经实测。实现
`oc-kanban` 时需跑一个有真实执行历史的任务实测校准（见 5.6）。

### 4.4 `Capabilities` —— 自描述与版本协商

`oc-kanban capabilities` 的 `data`：

```jsonc
{
  "contract_version": "1.0",        // MAJOR.MINOR，本契约版本
  "oc_kanban_version": "1",         // oc-kanban 实现版本
  "hermes_version":    "v0.14.0",   // 底层 hermes 版本（信息性）
  "variant":           "hermes-main",
  "verbs":   ["boards", "list", "show", "runs", "stats", "watch",
              "create", "comment", "complete", "block", "unblock",
              "archive", "reassign", "reclaim"],
  "features": { "write": true, "watch": true, "runs": true, "stats": true }
}
```

`verbs` 列表是本镜像实际支持的**功能 verb**，不含 `capabilities` 自身
（`capabilities` 是能力发现入口，在所有版本中恒定存在）。因此第 4.2 节的
15 个 verb 中，`verbs` 列表恒为其余 14 个的子集。

版本规则：

- `contract_version` 形如 `MAJOR.MINOR`。
- **MINOR 递增** = 向后兼容变更（新增字段 / 新增 verb）。
- **MAJOR 递增** = 破坏性变更（契约约定上尽量不发生）。
- manager 在代码中声明所需最低 `contract_version`。访问实例时先调一次
  `capabilities` 并按实例缓存：MAJOR 不匹配则整体降级提示；某 verb 不在
  `verbs` 列表中则前端隐藏对应按钮。

### 4.5 错误码枚举

| code | 含义 |
|---|---|
| `BAD_REQUEST` | 参数非法（manager 侧也校验，双保险）。 |
| `NOT_FOUND` | board / task 不存在。 |
| `UNSUPPORTED` | 该 hermes 版本不支持此 verb / 能力。 |
| `HERMES_CLI_FAILED` | 底层 `hermes kanban` 执行失败，且无法归入上述分类。 |
| `INTERNAL` | `oc-kanban` 自身错误（输出解析失败等）。 |

### 4.6 `watch` 流契约

`oc-kanban watch --board X` 输出 NDJSON：每行一个 `Event` 对象。启动失败时
首行输出错误信封 `{"ok":false,"error":{...}}` 后以退出码 `1` 结束。流正常时
持续输出直到进程被终止。

## 5. oc-kanban 内部实现

### 5.1 脚本形态与执行环境

单文件 `oc-kanban.py`，`COPY` 到 `/usr/local/bin/oc-kanban`，与现有
`oc-channel-*` 同风格。argparse subparsers 分发 verb。

不需要 re-exec 进 hermes venv（`oc-channel-login` 那套是为了 `import` hermes
SDK）。`oc-kanban` 是 subprocess 调 `hermes` 命令，`hermes` 已在容器 PATH 中
（manager 现在直接执行 `["hermes","kanban",...]` 可跑通即为证明），用系统
python3 即可，继承容器环境。

### 5.2 verb → hermes CLI 映射表（适配核心）

这张表是「各版本 `oc-kanban` 唯一不同的地方」。`hermes-main` 版直接收编
manager `runCLI` 中已经过浏览器验证的全部 CLI 约定：

| oc-kanban | → hermes v0.14.0 |
|---|---|
| `boards` | `kanban boards list --all --json` |
| `list --board B [--status S] [--assignee A]` | `kanban --board B list --json [--status S] [--assignee A]` |
| `show --board B --id ID` | `kanban --board B show ID --json` |
| `runs --board B --id ID` | `kanban --board B runs ID --json` |
| `stats --board B` | `kanban --board B stats --json` |
| `watch --board B` | `kanban --board B watch` |
| `create --board B --title T --assignee A --priority N ...` | `kanban --board B create T --assignee A --priority N --json [--body][--skill x ...][--workspace w][--parent p][--max-retries n]` |
| `comment --board B --id ID --body T` | `kanban --board B comment ID T` |
| `complete --board B --id ID [--result R]` | `kanban --board B complete ID [--result R]` |
| `block --board B --id ID --reason R` | `kanban --board B block ID R` |
| `unblock --board B --id ID` | `kanban --board B unblock ID` |
| `archive --board B --id ID` | `kanban --board B archive ID` |
| `reclaim --board B --id ID` | `kanban --board B reclaim ID` |
| `reassign --board B --id ID --to P` | `kanban --board B reassign ID P` |

意义：manager 现在散落在 Go 代码中的「CLI 约定知识」整体搬进 `oc-kanban`。
约定随 hermes 版本变化时，只改对应变体的 `oc-kanban`，manager 零改动。

### 5.3 规整流程

- **读 verb**（`boards` / `list` / `show` / `runs` / `stats`）：调 hermes →
  解析 `--json` → 按契约 schema 重映射（v0.14.0 字段基本一致，重映射主要是
  字段筛选与缺省补全）→ 包信封输出。
- **`create`**：调 hermes `create`（hermes 返回扁平 task）→ 取 `id` → 再调
  hermes `show ID` → 组装成 `TaskDetail`。
- **写 verb**（`comment` / `complete` / `block` / `unblock` / `archive` /
  `reassign` / `reclaim`）：调 hermes 写命令成功后，再调一次 `show ID` 取最新
  详情 → 返回 `TaskDetail`。
- **`capabilities`**：`oc-kanban` 自产，不调 hermes。`contract_version` /
  `oc_kanban_version` / `verbs` / `features` 是该版本 `oc-kanban` 的内置常量；
  `hermes_version` / `variant` 读取构建期写入的 `/etc/oc-image.json`
  （`oc-info` 读取的同一文件）。

### 5.4 错误归类

hermes 退出非 0 时，`oc-kanban` 按本版本 hermes 的 stderr 文本模式归类为
契约错误码：含「not found / no such task / unknown board」之类 → `NOT_FOUND`；
输出解析失败 → `INTERNAL`；其余 → `HERMES_CLI_FAILED`（message 带截断的
hermes stderr）。

这种 stderr 文本匹配本身脆弱、跨版本不稳定 —— 但这正是 `oc-kanban` 应承担
的脏活：把脆弱的、版本相关的逻辑关进 `oc-kanban`，每个变体使用自己版本的
模式。manager 永远只见干净的错误码。

`UNSUPPORTED` 由 `oc-kanban` 主动声明：若某 hermes 版本压根没有某能力，那个
版本的 `oc-kanban` 就不在 `capabilities.verbs` 中列出它，且该 verb 被调用时
直接返回 `UNSUPPORTED`，不去调 hermes。

### 5.5 `watch` 实现

用 `subprocess.Popen` 启动 `hermes kanban --board B watch`，逐行读取其
stdout → 每行规整成契约 `Event` → 立即 `print` 并 `flush`（NDJSON）。进程
退出或被终止时清理子进程。启动失败则首行输出错误信封、以退出码 `1` 结束。
Dockerfile 已设 `PYTHONUNBUFFERED=1`，输出仍显式 flush 以保证流式投递。

### 5.6 `runs` 实测校准（实现期动作）

现有 `KanbanTaskRun` 是调研报告推测，未经实测（空任务返回 `[]` 无法观察实际
字段）。实现 `oc-kanban` 时需跑一个有真实执行历史的任务，观察
`hermes kanban runs` 真实输出，据此定死 `Run` 的 canonical schema。若实测
确实不可得，`Run` schema 放宽为 best-effort 并在契约文件中标注。

## 6. 代码组织与构建集成（方案 C）

### 6.1 文件布局

```
runtime/hermes/
  kanban-contract/                  # canonical 契约，single source of truth（git tracked）
    SPEC.md                         # 契约规范文档（verb / 信封 / 错误码 / 版本号规则）
    schema/*.json                   # 各结构 JSON Schema：envelope, capabilities,
                                    #   board, task, task-detail, stats, run, event
  hermes-main/
    oc-kanban.py                    # 变体专属适配实现（git tracked，各变体自维护）
    tests/test_kanban_contract.py   # 变体专属契约测试（升级现有文件）
    kanban-contract/                # 构建期注入的 canonical 副本（.gitignore）
```

声明式的契约（schema）单一来源、所有变体共享；命令式的适配代码
（`oc-kanban.py`）各变体独立维护。

### 6.2 用 Makefile 注入避开「跨目录 COPY」约束

矛盾：契约 schema 需进入镜像供镜像内契约测试使用，但 build context = 变体
目录、Dockerfile 明确约束不跨目录 `COPY`。

解法：`make build-hermes-runtime` 在 `docker build` 之前，把 canonical
`runtime/hermes/kanban-contract/` 拷贝进
`$(HERMES_VARIANT_DIR)/kanban-contract/`。Dockerfile 只 `COPY` 本目录内的
副本 —— 不跨目录，约束不破。canonical 仍是唯一真相，变体目录内的副本是
构建产物，加入 `.gitignore`。

Dockerfile 新增：

```dockerfile
COPY oc-kanban.py        /usr/local/bin/oc-kanban
COPY kanban-contract/    /usr/local/lib/oc-kanban/contract/
RUN chmod +x /usr/local/bin/oc-kanban
```

### 6.3 其他构建点

- `Dockerfile`：新增 `jsonschema` pip 依赖（契约测试校验用），追加到现有
  `pyyaml` / `pytest` 安装行。
- `Dockerfile.dev`（stub）：同样安装 `oc-kanban`；stub 无真实 hermes，
  `oc-kanban` 检测后所有 verb 返回 `UNSUPPORTED`、`capabilities.features` 全
  为 `false` —— 给 manager 的 stub 降级一个干净的契约出口。
- `hermes-main/CONTRACT.md`：「镜像对外命令」一节补上 `oc-kanban`。

## 7. manager 侧改造

「重新设计规整契约」（决策 D3）的代价集中在此节。

| 改造点 | 内容 |
|---|---|
| `runCLI` | 命令前缀 `["hermes","kanban"]` → `["oc-kanban"]`；board 从全局参数位改成 `--board` flag；verb 参数改成新 flag 风格（`--id` 等）。 |
| 输出解析 | 从「exit code + 裸 JSON / stderr 文本」改成解析信封 `{ok,data/error}`；`error.code` 映射到现有哨兵错误：`BAD_REQUEST`→`ErrKanbanBadRequest`、`NOT_FOUND`→`ErrNotFound`、`UNSUPPORTED`→`ErrKanbanNotSupported`、其余→`ErrKanbanCLI`。 |
| `hermes_kanban_types.go` | 按新契约调整类型；`create` 直接得 `TaskDetail`，删除手动 `KanbanTaskDetail{Task: task}` 包装。 |
| 写操作签名 | `Comment` / `Complete` / `Block` / `Unblock` / `Archive` / `Reassign` / `Reclaim` 返回值由 `error` 改为 `(KanbanTaskDetail, error)`。 |
| 新增 capabilities | 新增 `Capabilities()` service 方法 + handler route；manager 按实例缓存，首次访问时探测一次。 |
| `StreamEvents` | 命令 `["hermes","kanban","--board",b,"watch"]` → `["oc-kanban","watch","--board",b]`。 |
| OpenAPI 同步 | 写操作响应体变化 + 新增 capabilities route → 必须运行 `make openapi-gen` + `make web-types-gen`（项目硬性要求）。 |
| 前端 `useKanban.ts` | 写 mutation 拿到返回的 `TaskDetail` 后可直接更新缓存；新增 `useKanbanCapabilitiesQuery`；按 `capabilities.verbs` / `features` 隐藏不支持的操作按钮。 |

stub 判定维持现状（image tag `-dev` 后缀），不借本次改动调整，保持聚焦。

## 8. 测试策略

- **镜像内契约测试**：`tests/test_kanban_contract.py` 升级 —— 不再直接调
  `hermes kanban`，改调 `oc-kanban` 各 verb，用 `kanban-contract/schema/` 的
  JSON Schema 逐一校验输出符合契约。`make verify-hermes-runtime` 自动纳入，
  任何 verb 输出违反契约即构建失败、镜像发不出去。
- **Go 单元测试**：`hermes_kanban_test.go` 的 fake execer 输出改成信封格式；
  新增覆盖 —— 错误码到哨兵的映射、`capabilities` 解析、写操作返回
  `TaskDetail`。
- **前端单元测试**：`useKanban` / `AppKanbanTab` 的 mock 更新；新增
  capabilities 降级（隐藏按钮）测试。
- **浏览器全量验证**（项目 CLAUDE.md 强制）：重建 `hermes-runtime:hermes-main`
  镜像 → 重建实例 → 浏览器跑完 15 个能力 + capabilities 降级 + `watch`
  实时流。
- **`runs` 实测**：验证阶段跑一个有执行历史的任务，校准 `Run` schema。

## 9. 落地步骤概要

详细实施计划由后续 writing-plans 产出，此处给出阶段骨架：

1. 编写契约规范工件（`kanban-contract/SPEC.md` + `schema/*.json`）。
2. 实现 `hermes-main/oc-kanban.py`（含 `runs` 实测校准）。
3. 构建集成（Makefile 注入、Dockerfile / Dockerfile.dev、`.gitignore`、
   `CONTRACT.md`）。
4. 升级 `tests/test_kanban_contract.py` 为契约一致性测试。
5. manager 改造（`HermesKanbanService`、类型、capabilities、OpenAPI 同步）。
6. 前端改造（`useKanban`、capabilities 降级）。
7. 重建镜像与实例，浏览器全量验证。

## 10. 风险与权衡

- **工作量**：决策 D3「重新设计规整契约」使 manager 端 Go/TS 类型、前端、
  OpenAPI、单测均需调整并重新浏览器验证。这是契约规整的一次性代价，换取
  此后 hermes 版本变化时 manager 零改动。
- **stderr 文本匹配脆弱**：错误归类依赖 hermes stderr 文本模式（5.4）。该
  脆弱性被有意关在 `oc-kanban` 内，每个变体用自身版本的模式；manager 不受
  影响。
- **`runs` schema 未实测**：现有类型为推测，需实现期实测校准；若不可得则
  降级为 best-effort（5.6）。
- **构建期注入副本**：变体目录内的 `kanban-contract/` 是构建产物，须确保
  `.gitignore` 生效、`make build-hermes-runtime` 每次重新注入，避免陈旧
  副本进入镜像。
