# Hermes Runtime 内置关闭危险命令审批 · 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让两个 hermes runtime variant（`hermes-v2026.5.7` 与 `hermes-v2026.5.16`）的 `render_config_yaml.py` 在渲染 `config.yaml` 时追加 `approvals.mode = "off"` + `approvals.cron_mode = "approve"`，使上游 hermes-agent 跳过所有 dangerous-command 审批；manager / manifest / 数据库都不感知。

**Architecture:** 只动两份 renderer Python 文件 + 各自对应的 1 份 pytest 文件。两个 variant 的 renderer 历史上字节一致（`diff -q` 无输出），本次保持一致同步改。镜像构建期已有的 `RUN python -m pytest` 步骤会把测试当作构建门禁——任意 variant 渲染遗漏 `approvals` 段则镜像构建失败，杜绝静默回归。

**Tech Stack:** Python 3.13、PyYAML（`yaml.safe_dump` 对字符串 `"off"` 会自动加单引号，无需手写引号包装）、pytest（每个 variant 各自的 `tests/conftest.py` 自动注入 `sys.path`，跑测试必须 `cd` 到 variant 目录）。

**Spec：** `docs/superpowers/specs/2026-05-24-hermes-runtime-skip-approval-design.md`

---

## File Structure

本次实现只涉及 4 个文件：

| 文件 | 责任 | 操作 |
|---|---|---|
| `runtime/hermes/hermes-v2026.5.7/renderer/render_config_yaml.py` | 渲染 v2026.5.7 variant 的 config.yaml | 修改：`render()` config dict 追加 `approvals` 段 |
| `runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py` | 渲染 v2026.5.16 variant 的 config.yaml | 修改：同上 |
| `runtime/hermes/hermes-v2026.5.7/tests/test_render_config_yaml.py` | v2026.5.7 渲染单测 | 修改：新增 1 个 case 断言 `approvals` 段就位 |
| `runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py` | v2026.5.16 渲染单测 | 修改：同上 |

**约束：**
- 两份 renderer 必须保持字节一致（`diff -q` 必须无输出）。
- 两份测试必须保持字节一致（同上）。
- 一切按 AGENTS.md 走单 commit：`feat(hermes-runtime): hermes 镜像内置关闭危险命令审批`。

---

## 背景速读（无需熟悉本仓库就能动手）

1. **为什么是 renderer 而不是 Dockerfile / manager Go 层？** spec 第 4.2 节已论证；这里只需要知道：renderer 是镜像里 `oc-entrypoint` 启动时把 `manifest.yaml` 渲染成 `config.yaml` 的 Python 模块，hermes-agent 进程读这份 `config.yaml`。
2. **上游 hermes-agent 怎么读这两个 key？** `tools/approval.py` 的 `_normalize_approval_mode()` 显式处理 YAML 1.1 `off → False` 坑（bool False 视为 "off"，str 走 lower/strip）；`approvals.cron_mode` 被 `cfg_get(config, "approvals", "cron_mode", default="deny")` 读出后 lower/strip，匹配 `{"approve", "off", "allow", "yes"}` 即视为 approve。
3. **为什么 mode=off 命中后 cron_mode 没用？** yolo 分支（mode=off 命中此分支）在 cron 判断之前 `return approved=True`，cron_mode 仅当未来有人把 mode 改回 manual/smart 时才生效——双保险。
4. **hardline 命令仍拦得死死的**（`rm -rf /`、`mkfs`、`dd` raw、`shutdown / reboot`、fork bomb、`kill -1`），与用户对齐接受，不在本计划范围内。

---

## Task 1: 两个 variant 同步追加 approvals 段（TDD + 单 commit）

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.5.7/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.5.7/tests/test_render_config_yaml.py`

### - [ ] Step 1: 验证基线测试通过（防止开始就基础失败被误判为我们的引入）

Run（在仓库根执行）：

```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_render_config_yaml.py -v
```

Expected：4 passed。

```bash
cd runtime/hermes/hermes-v2026.5.7 && python3 -m pytest tests/test_render_config_yaml.py -v
```

Expected：4 passed。

如果任一未通过，**停止**——这是仓库基线问题，不是本次改动引入的，要先和用户/spec 作者澄清。

### - [ ] Step 2: 给 v2026.5.16 测试文件追加 failing case

文件：`runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py`

在文件末尾追加：

```python
def test_render_writes_approvals_skip_block(tmp_data: Path) -> None:
    # 验证 approvals 段就位：mode=off 命中上游 yolo 分支跳过审批；
    # cron_mode=approve 兜未来 mode 被改回 manual/smart 时 cron 仍放行。
    # 业务目的：hermes 实例对话中不再每条命令都通过 messaging platform 问 /approve。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["approvals"]["mode"] == "off"
    assert out["approvals"]["cron_mode"] == "approve"
```

### - [ ] Step 3: 把 Step 2 的同一段同步追加到 v2026.5.7 测试文件

文件：`runtime/hermes/hermes-v2026.5.7/tests/test_render_config_yaml.py`

在文件末尾追加**完全相同**的代码块（同 Step 2）。

### - [ ] Step 4: 验证两份测试文件保持字节一致

Run（在仓库根执行）：

```bash
diff -q runtime/hermes/hermes-v2026.5.7/tests/test_render_config_yaml.py runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py
```

Expected：无输出（exit 0）。如果有输出，说明两份追加内容不一致——**停止**，比对修正再继续。

### - [ ] Step 5: 跑两个 variant 的测试，确认新 case 失败

Run：

```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_render_config_yaml.py::test_render_writes_approvals_skip_block -v
```

Expected：FAIL，错误形如 `KeyError: 'approvals'`（当前 renderer 没渲染这一段）。

```bash
cd runtime/hermes/hermes-v2026.5.7 && python3 -m pytest tests/test_render_config_yaml.py::test_render_writes_approvals_skip_block -v
```

Expected：同上，FAIL。

### - [ ] Step 6: 修改 v2026.5.16 renderer，追加 approvals 段

文件：`runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py`

把 `render()` 函数中 config dict 的 `"terminal"` 段后面追加一个 `"approvals"` 段。当前形如：

```python
        "terminal": {
            "backend": "local", "cwd": "/opt/data/workspace",
            "timeout": 180, "lifetime_seconds": 300,
        },
    }
```

改为：

```python
        "terminal": {
            "backend": "local", "cwd": "/opt/data/workspace",
            "timeout": 180, "lifetime_seconds": 300,
        },
        # 关闭上游 hermes-agent 的 dangerous-command 审批：
        # - mode="off" 命中上游 _normalize_approval_mode 的 yolo 分支，跳过所有
        #   dangerous-command 提示（受控部署形态下，逐条 /approve 是噪声非收益）。
        # - cron_mode="approve" 是兜底——当前 mode=off 命中 yolo 后 cron 路径
        #   走不到，但留这一项保证将来若 mode 被改回 manual/smart，cron 任务遇
        #   危险命令仍放行而非被 deny。
        # 不可绕过的上游 hardline 命令（rm -rf /、mkfs、dd raw、shutdown、
        # fork bomb、kill -1 等）仍由 hermes-agent 硬拦，本配置不影响。
        # YAML 落地：PyYAML 对字符串 "off" 自动加单引号输出 `mode: 'off'`，
        # 不需要手写引号包装；回读后仍是字符串 "off"。
        "approvals": {
            "mode": "off",
            "cron_mode": "approve",
        },
    }
```

### - [ ] Step 7: 把同一段同步追加到 v2026.5.7 renderer

文件：`runtime/hermes/hermes-v2026.5.7/renderer/render_config_yaml.py`

执行**完全相同**的修改（Step 6 的整段）。

### - [ ] Step 8: 验证两份 renderer 保持字节一致

Run：

```bash
diff -q runtime/hermes/hermes-v2026.5.7/renderer/render_config_yaml.py runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py
```

Expected：无输出。

### - [ ] Step 9: 跑两个 variant 的全量测试，确认新 case 通过 + 原有 case 不回归

Run：

```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_render_config_yaml.py -v
```

Expected：5 passed（原 4 个 + 新增 1 个）。

```bash
cd runtime/hermes/hermes-v2026.5.7 && python3 -m pytest tests/test_render_config_yaml.py -v
```

Expected：5 passed。

### - [ ] Step 10: 跑两个 variant 完整 tests 目录，确认未引入跨文件副作用

Run：

```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/ -v -p no:cacheprovider
```

Expected：全部 passed（镜像构建期 `Dockerfile` 用的就是这条命令）。

```bash
cd runtime/hermes/hermes-v2026.5.7 && python3 -m pytest tests/ -v -p no:cacheprovider
```

Expected：全部 passed。

### - [ ] Step 11: 单 commit 提交

Run（在仓库根执行）：

```bash
git add runtime/hermes/hermes-v2026.5.7/renderer/render_config_yaml.py \
        runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py \
        runtime/hermes/hermes-v2026.5.7/tests/test_render_config_yaml.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_render_config_yaml.py
```

```bash
git status --short
```

Expected：4 行 `M`，且**只有这 4 个文件**——确认没有混入无关改动（AGENTS.md 交付前检查）。

```bash
git commit -m "$(cat <<'EOF'
feat(hermes-runtime): hermes 镜像内置关闭危险命令审批

在 renderer 渲染 config.yaml 时追加 approvals 段，让上游 hermes-agent
跳过所有 dangerous-command 审批：

- approvals.mode="off" 命中上游 _normalize_approval_mode 的 yolo 分支，
  跳过所有 dangerous-command 提示（受控部署形态下，每条命令都通过
  messaging platform 问 /approve 是噪声而非安全收益）。
- approvals.cron_mode="approve" 是兜底——当前 mode=off 命中 yolo 后
  cron 路径走不到，但留这一项保证将来若 mode 被改回 manual/smart，
  cron 任务遇危险命令仍放行而非被 deny。

控制点完全落在镜像内 renderer 一处，manager / manifest / 数据库均不
感知；hermes-v2026.5.7 与 hermes-v2026.5.16 两个 variant 同步改并通过
diff -q 验证字节一致。上游 hardline 命令（rm -rf /、mkfs、dd raw、
shutdown、fork bomb、kill -1 等）仍由 hermes-agent 硬拦，本配置不影响。

测试：每个 variant tests/test_render_config_yaml.py 新增 1 个 case
断言 approvals 段就位。镜像构建期 Dockerfile 的 RUN pytest 会把新 case
当作构建门禁——任意 variant 渲染遗漏此段则镜像构建失败。

Spec: docs/superpowers/specs/2026-05-24-hermes-runtime-skip-approval-design.md
EOF
)"
```

```bash
git log -1 --stat
```

Expected：1 commit、4 files changed（两份 renderer 各 +~15 行、两份测试各 +~9 行）。

---

## Self-Review 结论

- **Spec 覆盖**：spec §4.1 文件清单的 4 个文件，Task 1 全部覆盖；§4 设计正文的 `approvals.mode="off"` + `cron_mode="approve"` 由 Step 6/7 实现；§5 测试策略由 Step 2/3 + Step 9/10 实现（包括 §5 提到的「镜像构建期 RUN pytest 即门禁」——Step 10 跑的命令就是 Dockerfile 里那一条）；§7 提交规范由 Step 11 落实。spec §3 是事实摘要、§6 是风险评估，不需要专门 task。
- **Placeholder 扫描**：所有代码块都是完整可执行内容，无 TBD / TODO / "类似 Task N"。
- **类型一致性**：测试断言的两个 key（`mode`、`cron_mode`）与 renderer 写入的 dict key 完全一致；renderer 缩进对齐现有代码风格。
- **遗漏检查**：spec §4.1 说"不动 Dockerfile、不动 oc-entrypoint、不动 manifest、不动 manager Go、不动数据库、不动 OpenAPI、不动前端"——本计划无任何对这些的改动，✓。
