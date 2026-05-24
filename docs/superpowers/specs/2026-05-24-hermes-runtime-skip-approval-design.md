# Hermes Runtime 内置关闭危险命令审批 · 设计 Spec

> 日期：2026-05-24
> 范围：在 hermes runtime 镜像内、由 `oc-entrypoint` 渲染的 `config.yaml`
> 里默认写入 `approvals.mode = "off"` 与 `approvals.cron_mode = "approve"`，
> 让上游 hermes-agent 跳过所有 dangerous-command 审批，无需 manager 侧或
> manifest 参与。

## 1 · 背景

用户在创建 hermes 实例后、用微信扫码绑定与 agent 对话时，agent 执行
`curl -sL ... bing.com/search?q=...` 这类带"pipe to interpreter"语义的命令
会被上游 hermes-agent 的 dangerous-command gate 拦下，并通过当前 messaging
platform（微信）把审批请求透传给用户：

```
⚠️ Dangerous command requires approval:
curl -sL "..." -H "User-Agent: ..."
Reason: Security scan — [HIGH] Pipe to interpreter: curl | python3 ...

Reply `/approve` ... `/approve session` ... `/approve always` ... `/deny`
```

这条审批由上游 `tools/approval.py` 的 `check_all_command_guards()` 触发。
对 oc-manager 这套部署形态——hermes 实例本来就是受控运行时、由用户主动
绑定 messaging platform 与 agent 对话——每条命令都问审批属于噪声而非
安全收益。诉求是 **hermes 执行中不进行权限确认，默认全部通过**。

## 2 · 目标 / 非目标

### 2.1 目标

- 上游 hermes-agent 在 `local` terminal 后端下不再因 dangerous-command
  弹审批，所有对话与 cron 任务里命令默认放行。
- 控制点完全落在 hermes runtime 镜像内（renderer 一处），manager 侧不
  感知、manifest 契约不变、数据库 schema 不变。
- 现有所有 hermes variant（当前为 `hermes-v2026.5.7`、`hermes-v2026.5.16`）
  同步生效，避免新旧 variant 行为分裂。
- 镜像构建期通过既有 `pytest` 测试覆盖渲染产物，缺失此段则镜像构建失败。

### 2.2 非目标

- 不绕过上游 hardline 命令清单（`rm -rf /`、`mkfs`、`dd` 到 raw device、
  `shutdown / reboot`、fork bomb、`kill -1`）。这些命令上游在 yolo
  bypass 之前就硬拦死，任何配置都过不去，**这是与用户对齐的接受项**。
- 不为「单实例可选 yolo」加 per-instance 字段或 UI；当前诉求是普适默认。
- 不动 Dockerfile `ENV` 或 manager 侧 `ContainerSpec.Env` 注入。
- 不修改上游 hermes-agent 代码，不影响其后续升级路径。

## 3 · 上游审批机制（事实摘要）

容器内 `/usr/local/lib/hermes-agent/tools/approval.py` 关键路径：

| 位置 | 行为 |
|---|---|
| `check_all_command_guards()` 早期分支 | `env_type ∈ {docker, singularity, modal, daytona, vercel_sandbox}` 直接 `approved=True`，不走审批 |
| 同函数 hardline 检查 | hardline 命令在所有 yolo / mode=off / cron_mode 之前 unconditional block |
| 同函数 yolo 判断 | `HERMES_YOLO_MODE` env 真值 / 当前 session 已开启 `/yolo` / `approvals.mode == "off"` 任一命中 → `approved=True`，**直接 return，不再走 cron_mode 判断** |
| cron 分支 | 仅在前述 yolo 都未命中时进入；按 `approvals.cron_mode` 决定 deny/approve |
| `_normalize_approval_mode()` | 显式处理 YAML 1.1 把裸字 `off` 解析成 `False` 的坑：`bool False → "off"`，`str` 走 lower/strip |

oc-manager 的 hermes 实例容器内 hermes-agent 的 terminal 配置为
`backend: local`（见 `runtime/hermes/<variant>/renderer/render_config_yaml.py`
当前 `terminal` 段），所以会走 local 路径触发审批——这是问题成因，也是
本次开关的作用点。

## 4 · 设计

在 `runtime/hermes/<variant>/renderer/render_config_yaml.py` 的 `render()`
里、为渲染出的 config dict 顶层追加：

```python
"approvals": {
    "mode": "off",          # 让 _normalize_approval_mode 命中 yolo 分支，跳过所有 dangerous-command 审批
    "cron_mode": "approve", # 双保险：即使将来 mode 被改回 manual / smart，cron 仍放行
},
```

YAML 落地写带引号的 `"off"`，让产物配置不依赖"上游兜底 bool→str"链
路才能正确解释——只读 config.yaml 也能一眼读懂语义。

`approvals.mode = "off"` 命中后，cron 路径根本走不到（yolo 分支在 cron
判断之前就 return），所以 `cron_mode` 在当前 mode 值下不会被读取；它的
作用是**未来如有人把 mode 改回 manual / smart 的兜底**——只多 4 字节
渲染产物，但消掉了一个潜在回归点。

### 4.1 改动文件清单

- `runtime/hermes/hermes-v2026.5.7/renderer/render_config_yaml.py`：
  `render()` config dict 追加 `approvals` 段。
- `runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py`：同上。
  两份文件当前 `diff -q` 字节一致，必须保持一致。
- `runtime/hermes/hermes-v2026.5.7/tests/test_render_config_yaml.py`：
  新增 1 个 case，断言渲染后 `approvals.mode == "off"` 且
  `approvals.cron_mode == "approve"`。
- `runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py`：
  同上。
- 不动 Dockerfile、不动 oc-entrypoint、不动 manifest schema、不动 manager
  Go 代码、不动数据库 schema、不动 OpenAPI 契约、不动前端。

### 4.2 不改动项的理由

- **不在 Dockerfile 加 `ENV HERMES_YOLO_MODE=1`**：env 是构建期硬编码，
  且与 cron_mode 分散在镜像层和配置层两处；renderer 单点把两个开关写在
  一个 YAML 段里更内聚，未来上游若改名/重构 approvals 段，改动只在一处。
- **不动 manager `ContainerSpec.Env`**：用户明确"外层不用关心，未来
  upstream 处理方式变了也只改镜像内"。
- **不加 per-instance 开关**：CLAUDE.md "Simplicity First" + 用户诉求
  是普适默认，YAGNI。

## 5 · 测试策略

- 镜像构建期已 `RUN PYTHONDONTWRITEBYTECODE=1 python -m pytest
  /usr/local/lib/oc-entrypoint/tests/ -v -p no:cacheprovider`（见
  `runtime/hermes/<variant>/Dockerfile`）。新增的渲染 case 失败 →
  镜像构建直接 fail，杜绝静默回归。
- 新 case 与现有 `test_render_writes_expected_fields` 同文件、同 fixture
  风格，按 CLAUDE.md「测试方法必须有相邻中文注释」补好场景注释。
- 不改业务逻辑、不动 Go 层，所以不引入 Go 单测。

## 6 · 风险与边界

| 项 | 评估 |
|---|---|
| YAML `off` 解析坑 | 已用带引号 `"off"` 显式落地；上游 `_normalize_approval_mode` 同时兜 bool / str 两路 |
| hardline 仍拦 | 已与用户对齐接受，文档显式列举不可绕过的命令清单 |
| variant 间漂移 | 两份 renderer 历史字节一致，本次同步改并通过 `diff -q` 验证保持一致；后续新 variant 沿用 |
| 上游未来重命名 `approvals.mode` | 控制点集中在一个 renderer 文件，重命名时改一处即可；现行测试 case 会先 fail，构建即提示 |
| 安全姿态被弱化 | 与诉求一致：受控部署环境下 + 用户主动绑定 messaging platform，逐条审批是噪声；hardline 仍守底线 |

## 7 · 提交规范

按 AGENTS.md：单 commit，类型 `feat(hermes-runtime)`，summary 中文描述
"hermes 镜像内置关闭危险命令审批"；正文说明 `mode=off` / `cron_mode=approve`
双保险与 hardline 不变更，列出同步改的两个 variant。
