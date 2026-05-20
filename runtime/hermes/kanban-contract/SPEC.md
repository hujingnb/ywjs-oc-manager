# oc-kanban 契约规范 v1.0

本文档是 `oc-kanban` 命令对外契约的规范文档，供镜像内开发者参考。
是所有变体共同遵守的 single source of truth。

---

## 1. 命令形态与信封

### 1.1 命令形态

```
oc-kanban <verb> [--flag value ...]
```

所有参数均为具名 flag，无 positional 参数。`--board` 默认 `default`。

### 1.2 输出信封

stdout 永远是单行 JSON（`watch` 除外），采用统一信封：

```json
{ "ok": true, "data": <payload> }
```

```json
{ "ok": false, "error": { "code": "<CODE>", "message": "<可读说明>" } }
```

manager 以 stdout 信封的 `ok` 字段为权威判断依据，退出码仅作冗余信号。

### 1.3 退出码

| 退出码 | 含义 |
|---|---|
| `0` | 成功。 |
| `1` | 业务错误（错误信封已写入 stdout）。 |
| `2` | 用法错误（argparse 解析失败，无法产出信封时输出 stderr 文本）。 |

---

## 2. Verb 全集（15 个）

| verb | 关键 flag | data 返回 |
|---|---|---|
| `capabilities` | （无） | `Capabilities` |
| `boards` | （无） | `Board[]` |
| `list` | `--board` `--status` `--assignee` | `Task[]`（摘要） |
| `show` | `--board` `--id` | `TaskDetail` |
| `runs` | `--board` `--id` | `Run[]` |
| `stats` | `--board` | `Stats` |
| `watch` | `--board` | NDJSON 事件流（见第 4 节） |
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
- 写操作（`create` / `comment` / `complete` / `block` / `unblock` / `archive` / `reassign` / `reclaim`）统一返回 `TaskDetail`（更新后的完整详情）。

---

## 3. 错误码枚举

| code | 含义 |
|---|---|
| `BAD_REQUEST` | 参数非法（manager 侧也校验，双保险）。 |
| `NOT_FOUND` | board / task 不存在。 |
| `UNSUPPORTED` | 该 hermes 版本不支持此 verb / 能力。 |
| `HERMES_CLI_FAILED` | 底层 `hermes kanban` 执行失败，且无法归入上述分类。 |
| `INTERNAL` | `oc-kanban` 自身错误（输出解析失败等）。 |

---

## 4. `watch` NDJSON 流约定

`oc-kanban watch --board X` 输出 NDJSON：每行一个 `Event` 对象。

- 启动失败时：首行输出错误信封 `{"ok":false,"error":{...}}` 后以退出码 `1` 结束。
- 流正常时：持续输出直到进程被终止。
- `watch` 流与 `TaskDetail.events` 使用同一 `Event` schema。

`Event` 结构：`kind, payload, created_at, run_id`

---

## 5. 版本号规则

`capabilities` verb 的 `data.contract_version` 形如 `MAJOR.MINOR`：

- **MINOR 递增** = 向后兼容变更（新增字段 / 新增 verb）。
- **MAJOR 递增** = 破坏性变更（契约约定上尽量不发生）。

`verbs` 列表是本镜像实际支持的**功能 verb**，不含 `capabilities` 自身
（`capabilities` 是能力发现入口，在所有版本中恒定存在）。

manager 在代码中声明所需最低 `contract_version`。访问实例时先调一次
`capabilities` 并按实例缓存：MAJOR 不匹配则整体降级提示；某 verb 不在
`verbs` 列表中则前端隐藏对应按钮。

---

各结构的精确字段以同目录 `schema/*.json` 为准；schema 是契约的机器可校验形式。
