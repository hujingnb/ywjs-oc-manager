# hermes 运行时文案接入原生 t() catalog 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 hermes 容器内约 140 条用户可见的裸字符串（现被构建期补丁写死中文）改为走 hermes 原生 `t()` i18n catalog，使其随实例 `display.language` 输出中/英，消除 en 实例中英混杂。

**Architecture:** 新增 `oc_overlay.yaml`（`oc.*` 命名空间、中英双语，译文从现补丁迁移），构建期合并进镜像 upstream 的 `locales/en.yaml`、`zh.yaml`；把 `patch_i18n_literals.py` 从「英文片段→中文内联」改造为「完整字符串表达式→`t("oc.key", kw=expr)` 调用」，占位符在调用点求值后作为命名 kwarg 传入；语言由 `t()` 自行从 `config.yaml` 的 `display.language` 解析，renderer/manager 无需改动。一致性守卫 + 构建 fail-loud + 端到端真机验证三重兜底。

**Tech Stack:** Python 3.13（构建期补丁/合并脚本）、PyYAML、pytest、Docker、k3d、微信渠道真机验证。

**设计依据：** `docs/superpowers/specs/2026-06-24-hermes-runtime-i18n-catalog-design.md`

---

## 前置事实（已在本地运行实例实测确认，作为实现约束）

1. **`t()` 契约**（`/usr/local/lib/hermes-agent/agent/i18n.py`）：
   - `t(key, lang=None, **format_kwargs)`：按点分 key 查 `locales/<lang>.yaml`，`str.format(**kwargs)` 注入占位符。
   - 缺 key → 回落 `en.yaml` 同 key；en 也缺 → 返回 key 本身（永不崩）。`format` 失败记 warning 返回未格式化串（降级不崩）。
   - 语言解析：`HERMES_LANGUAGE` 环境变量 > `config.yaml` 的 `display.language` > `"en"`。**`t()` 自己读 config，无需任何外部接线。**
2. **目标文件与现状**：
   - `run.py`（19,935 行）：**第 55 行已有 `from agent.i18n import t`**。
   - `base.py`（4,813 行）：**没有** import `t`，补丁需注入。
   - 二者均已被现补丁打成中文（运行镜像里是中文）；本计划的新补丁针对**未打补丁的 upstream 英文源码**在构建期运行。
3. **现补丁** `patches/patch_i18n_literals.py`：263 条 `(英文片段, 中文片段)`，按注释分组，逐片段 `str.replace` 内部文本。**这份文件就是完整的中英翻译数据源**，本计划据它派生 catalog。
4. **upstream 已自带** 16 个 catalog（`en/zh/...`），key 用 `approval.` / `gateway.` 命名空间；我们的 `oc.` 不冲突。只补 en、zh，其余语言对 `oc.*` 自动回落英文。

---

## 文件结构（先锁定边界）

| 文件 | 动作 | 职责 |
|---|---|---|
| `runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml` | 新增 | 唯一翻译事实源：`oc.*` key → `{en, zh}` |
| `runtime/hermes/hermes-v2026.6.5/patches/merge_oc_locales.py` | 新增 | 构建期把 overlay 深合并进 upstream `en.yaml`/`zh.yaml` 的 `oc:` 顶层块（幂等、冲突 fail-loud） |
| `runtime/hermes/hermes-v2026.6.5/patches/check_oc_i18n_consistency.py` | 新增 | 一致性守卫：patch 用到的 `oc.*` key ↔ overlay 的 key 双向校验；可作脚本与测试复用 |
| `runtime/hermes/hermes-v2026.6.5/patches/patch_i18n_literals.py` | 重写 | 替换表改为「完整英文表达式 → `t("oc.key", kw=expr)`」；新增 base.py import 注入 |
| `runtime/hermes/hermes-v2026.6.5/Dockerfile` | 修改 | 新增 merge + guard 步骤，确定 patch 顺序 |
| `runtime/hermes/hermes-v2026.6.5/tests/test_merge_oc_locales.py` | 新增 | merge 深合并/幂等/冲突 fail-loud |
| `runtime/hermes/hermes-v2026.6.5/tests/test_oc_overlay_consistency.py` | 新增 | 守卫：patch ↔ overlay key 一致 |
| `runtime/hermes/hermes-v2026.6.5/tests/test_patch_i18n_literals.py` | 重写 | 新框架：表达式→t() 替换、import 注入、fail-loud、幂等 |
| `runtime/hermes/hermes-v2026.6.5/tests/test_oc_i18n_runtime.py` | 新增 | 用真实 i18n 机制断言代表性 key 在 zh/en 的解析 |
| `runtime/hermes/hermes-v2026.6.5/renderer/render_config_yaml.py` | 修改 | 删除 `:48-50` 的「已知局限」注释，改述为已走 catalog |

**关键命令前缀**：本仓库用 `make` 跑测试 / 构建。Python 测试在 `runtime/hermes/hermes-v2026.6.5/` 下用 `python -m pytest`。读取已运行实例的 upstream 源码用
`rtk proxy kubectl exec <pod> -n oc-apps -c hermes -- sed -n 'A,Bp' <file>`（绕过 rtk 压缩；pod 名用 `rtk proxy kubectl get pods -n oc-apps` 查）。

---

## 转换规则（所有消息组统一遵循；先读懂再开工）

把现补丁的「片段对」转换成「catalog 条目 + 一条 `t()` 替换」遵循以下规则：

**R1 — 消息边界**：转换单元是源码里一个**相邻字符串字面量组**（Python 隐式拼接，可能跨多物理行），它求值为一条完整用户可见消息。现补丁把这样一组拆成多个片段分别替换；合并回一条 = 一个 `oc.*` key + 一次 `t()` 调用。

**R2 — key 命名**：`oc.<file>.<场景>`，`<file>` 为 `run` 或 `base`，`<场景>` 用蛇形描述，沿用现补丁的注释分组（如 `oc.run.timeout_stuck_tool`）。同组多消息加后缀区分。

**R3 — 占位符 → kwarg**：源码 f-string 里的 `{表达式}`（含 `{_secs_ago:.0f}`、`{len(bundles)}`、`{platform.value}`、`{result['error']}`、`{'s' if prev!=1 else ''}` 等）在 `t()` 调用点原样作为命名 kwarg 传入；catalog 串里把占位符**重命名为干净的 kwarg 名**（去掉前导下划线、表达式取语义名）。format spec（`:.0f`）保留在 catalog 串里。

**R4 — 中英语法差异**：复数/词序差异由两份 catalog 各自表达。en 用到的 kwarg、zh 可不用（`str.format` 忽略多余 kwarg）。例：`{'s' if prev != 1 else ''}` → en kwarg `plural_s`、catalog en 含 `{plural_s}`、zh 省略，调用点 `plural_s=('s' if prev != 1 else '')`。

**R5 — 三元/分支里的短字面量**：如 `action = "restarting" if … else "shutting down"`，把每个字面量各自替换成 `t("oc.run.action_restarting")` / `t("oc.run.action_shutting_down")`。

**R6 — 锚点即整段表达式**：新补丁 `old` = 未打补丁 upstream 里**完整的字符串表达式源文本**（含 `f"`/引号/跨行缩进与换行）；`new` = `t(...)` 调用。`str.replace` 全量匹配，因此跨行锚点必须含精确空白。**靠构建期 fail-loud 兜底**：anchor 不存在且 `t("oc.key"` 也不在文件中 → 抛错列出，按提示读源码订正。

**R7 — 译文来源**：en 取现补丁的英文片段（`old`）按 R3 规范化占位符；zh 取对应中文片段（`new`）同样规范化。**不新创翻译**，逐字迁移现有译文，保持线上文案不变。

### 四个真实样例（取自实际源码，作为模板）

> 下列「英文源」由现补丁英文片段还原；「中文」即现补丁译文；structure 来自已打补丁运行实例对应行。

**样例 A — 跨行隐式拼接 + 多 kwarg（run.py 闲置超时「卡在工具」分支）**
源码（英文）：
```python
_diag_lines.append(
    f"The agent appears stuck on tool `{_cur_tool}` "
    f"({_secs_ago:.0f}s since last activity, "
    f"iteration {_iter_n}/{_iter_max})."
)
```
catalog（`oc_overlay.yaml`）：
```yaml
oc:
  run:
    timeout_stuck_tool:
      en: "The agent appears stuck on tool `{cur_tool}` ({secs_ago:.0f}s since last activity, iteration {iter_n}/{iter_max})."
      zh: "智能体疑似卡在工具 `{cur_tool}` (距上次活动 {secs_ago:.0f} 秒, 迭代 {iter_n}/{iter_max})。"
```
补丁条目（`REPLACEMENTS_RUN`），`old` 为上面整段三行表达式（含精确缩进换行），`new`：
```python
't("oc.run.timeout_stuck_tool", cur_tool=_cur_tool, secs_ago=_secs_ago, iter_n=_iter_n, iter_max=_iter_max)'
```

**样例 B — 三元短字面量（run.py `_status_action_gerund` / drain 广播）**
源码：`action = "restarting" if self._restart_requested else "shutting down"`
catalog：
```yaml
oc:
  run:
    action_restarting: { en: "restarting", zh: "正在重启" }
    action_shutting_down: { en: "shutting down", zh: "正在关闭" }
    gateway_interrupt:
      en: "⚠️ Gateway {action} — {hint}"
      zh: "⚠️ 网关{action} —— {hint}"
```
补丁条目三条：`"restarting"`→`t("oc.run.action_restarting")`、`"shutting down"`→`t("oc.run.action_shutting_down")`、`f"⚠️ Gateway {action} — {hint}"`→`t("oc.run.gateway_interrupt", action=action, hint=hint)`。
（`hint` 本身由其英文整段表达式另起 key，按 R1 处理。）

**样例 C — 单条整串（run.py /queue）**
源码：`return "Queued for the next turn."`
catalog：`oc.run.queue_queued: { en: "Queued for the next turn.", zh: "已加入队列,将在下一轮处理。" }`
补丁条目：`'"Queued for the next turn."'` → `'t("oc.run.queue_queued")'`（注意 old 含外层引号，整体换成 t() 调用）。

**样例 D — 复杂下标/方法占位符（run.py kanban 完成 / 未设 home channel）**
源码：`f"✔ {tag}Kanban {sub['task_id']} done"` →
catalog `oc.run.kanban_done: { en: "✔ {tag}Kanban {task_id} done", zh: "✔ {tag}看板 {task_id} 已完成" }`，
补丁 `new`：`t("oc.run.kanban_done", tag=tag, task_id=sub['task_id'])`。
源码 `f"📬 No home channel is set for {platform_name.title()}. "` →
`oc.run.no_home_channel: { en: "📬 No home channel is set for {platform_name}. ", zh: "📬 尚未为 {platform_name} 设置主频道。" }`，
`new`：`t("oc.run.no_home_channel", platform_name=platform_name.title())`。

---

## Task 1：合并脚本 merge_oc_locales.py（TDD）

**Files:**
- Create: `runtime/hermes/hermes-v2026.6.5/patches/merge_oc_locales.py`
- Test: `runtime/hermes/hermes-v2026.6.5/tests/test_merge_oc_locales.py`

- [ ] **Step 1: 写失败测试**

```python
# runtime/hermes/hermes-v2026.6.5/tests/test_merge_oc_locales.py
"""merge_oc_locales 构建期 catalog 合并的单元测试。"""
import sys
from pathlib import Path

import pytest
import yaml

# 沿用现有测试约定：把 patches/ 加入 sys.path 后扁平 import
sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from merge_oc_locales import merge_lang, MergeConflict


def _overlay():
    # 构造最小 overlay：两个 key，含 en/zh 两语言
    return {
        "oc": {
            "run": {
                "queue_queued": {"en": "Queued.", "zh": "已加入队列。"},
                "kanban_done": {"en": "{tag}done", "zh": "{tag}已完成"},
            }
        }
    }


def test_merge_lang_injects_oc_namespace_for_zh():
    # 场景：把 overlay 的 zh 文案合并进 upstream zh.yaml；upstream 原有键保持不变，新增 oc 顶层块
    upstream = {"gateway": {"draining": "排空中"}}
    merged = merge_lang(upstream, _overlay(), "zh")
    assert merged["gateway"]["draining"] == "排空中"  # upstream 原键不动
    assert merged["oc"]["run"]["queue_queued"] == "已加入队列。"  # 注入 zh 文案
    assert merged["oc"]["run"]["kanban_done"] == "{tag}已完成"


def test_merge_lang_picks_correct_language_leaf():
    # 场景：合并 en 时取 en 叶子，不混入 zh
    merged = merge_lang({}, _overlay(), "en")
    assert merged["oc"]["run"]["queue_queued"] == "Queued."


def test_merge_lang_idempotent():
    # 场景：对已合并结果再合并一次，结果不变（幂等）
    once = merge_lang({}, _overlay(), "zh")
    twice = merge_lang(once, _overlay(), "zh")
    assert once == twice


def test_merge_lang_conflict_with_existing_oc_key_raises():
    # 场景：upstream 已存在同名 oc.* key 且值不同 → 冲突 fail-loud，禁止静默覆盖
    upstream = {"oc": {"run": {"queue_queued": "别的值"}}}
    with pytest.raises(MergeConflict):
        merge_lang(upstream, _overlay(), "zh")
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_merge_oc_locales.py -v`
Expected: FAIL，`ModuleNotFoundError: patches.merge_oc_locales`

- [ ] **Step 3: 实现 merge_oc_locales.py**

```python
#!/usr/bin/env python3
# patches/merge_oc_locales.py
"""构建期：把 locales/oc_overlay.yaml 的 oc.* 文案分语言深合并进 upstream
en.yaml / zh.yaml 的 oc 顶层块。

overlay 结构：oc.<path>.<key> = {en: "...", zh: "..."}。合并时按目标语言取
对应叶子，写成 upstream catalog 的同构嵌套（t() 会拍平成点分键）。

约束：
- 幂等：目标已含同 key 同值 → 跳过。
- 冲突 fail-loud：目标已含同 key 但值不同 → 抛 MergeConflict（防止覆盖 upstream）。
"""
import pathlib
import sys

import yaml

OVERLAY = pathlib.Path(__file__).resolve().parent.parent / "locales" / "oc_overlay.yaml"
UPSTREAM_LOCALES = pathlib.Path("/usr/local/lib/hermes-agent/locales")
LANGS = ("en", "zh")


class MergeConflict(RuntimeError):
    """upstream 已存在同名 oc.* key 且值不同时抛出。"""


def _project_lang(node, lang):
    """把 overlay 的 {en,zh} 叶子投影成单语言嵌套 dict。"""
    # 叶子：形如 {"en": "...", "zh": "..."}
    if isinstance(node, dict) and set(node.keys()) <= {"en", "zh"} and node:
        if lang not in node:
            raise MergeConflict(f"overlay 叶子缺少语言 {lang}: {node!r}")
        return node[lang]
    if isinstance(node, dict):
        return {k: _project_lang(v, lang) for k, v in node.items()}
    raise MergeConflict(f"overlay 非法节点(非 dict/叶子): {node!r}")


def _deep_merge(dst, src, path=""):
    """把 src 深合并进 dst；同标量 key 值不同 → 冲突。返回 dst。"""
    for key, sval in src.items():
        cur = f"{path}.{key}" if path else key
        if key not in dst:
            dst[key] = sval
        elif isinstance(dst[key], dict) and isinstance(sval, dict):
            _deep_merge(dst[key], sval, cur)
        elif dst[key] == sval:
            continue  # 幂等
        else:
            raise MergeConflict(f"键 {cur} 冲突：upstream={dst[key]!r} overlay={sval!r}")
    return dst


def merge_lang(upstream: dict, overlay: dict, lang: str) -> dict:
    """把 overlay 投影到 lang 后深合并进 upstream（拷贝语义由调用方保证）。"""
    projected = _project_lang(overlay, lang)  # {"oc": {...}}
    return _deep_merge(upstream, projected)


def main() -> int:
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8"))
    for lang in LANGS:
        target = UPSTREAM_LOCALES / f"{lang}.yaml"
        if not target.exists():
            print(f"[merge_oc_locales] 目标 catalog 不存在: {target}", file=sys.stderr)
            return 1
        upstream = yaml.safe_load(target.read_text(encoding="utf-8")) or {}
        merged = merge_lang(upstream, overlay, lang)
        target.write_text(
            yaml.safe_dump(merged, allow_unicode=True, sort_keys=False),
            encoding="utf-8",
        )
        print(f"[merge_oc_locales] 已合并 oc.* → {target}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 4: 建最小 overlay 占位以便 import 不报错（后续 Task 6 起填真内容）**

```bash
mkdir -p runtime/hermes/hermes-v2026.6.5/locales
printf 'oc:\n  run: {}\n  base: {}\n' > runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_merge_oc_locales.py -v`
Expected: PASS（4 passed）

- [ ] **Step 6: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/patches/merge_oc_locales.py \
        runtime/hermes/hermes-v2026.6.5/tests/test_merge_oc_locales.py \
        runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml
git commit -m "feat(hermes-runtime): 增加 oc_overlay catalog 构建期合并脚本

新增 merge_oc_locales.py：把 oc_overlay.yaml 的 oc.* 双语文案按语言深合并进
upstream en.yaml/zh.yaml 的 oc 顶层块，幂等、冲突 fail-loud。附单元测试与空 overlay。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2：一致性守卫 check_oc_i18n_consistency.py（TDD）

**Files:**
- Create: `runtime/hermes/hermes-v2026.6.5/patches/check_oc_i18n_consistency.py`
- Test: `runtime/hermes/hermes-v2026.6.5/tests/test_oc_overlay_consistency.py`

- [ ] **Step 1: 写失败测试**

```python
# runtime/hermes/hermes-v2026.6.5/tests/test_oc_overlay_consistency.py
"""patch 使用的 oc.* key 与 overlay 定义的 key 双向一致性守卫测试。"""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from check_oc_i18n_consistency import (
    extract_patch_keys, extract_overlay_keys, check_consistency, ConsistencyError,
)


def test_extract_patch_keys_from_replacement_strings():
    # 场景：从补丁 new 串里抽出所有 t("oc.X") 的 key
    repls = [("old1", 't("oc.run.queue_queued")'),
             ("old2", 't("oc.run.kanban_done", tag=tag)')]
    assert extract_patch_keys(repls) == {"oc.run.queue_queued", "oc.run.kanban_done"}


def test_extract_overlay_keys_requires_both_langs():
    # 场景：overlay 叶子必须同时含 en 和 zh，缺一即非法
    overlay = {"oc": {"run": {"a": {"en": "A", "zh": "甲"}}}}
    assert extract_overlay_keys(overlay) == {"oc.run.a"}
    bad = {"oc": {"run": {"a": {"en": "A"}}}}  # 缺 zh
    with pytest.raises(ConsistencyError):
        extract_overlay_keys(bad)


def test_check_consistency_detects_missing_and_orphan():
    # 场景：patch 用了 overlay 没有的 key（missing），或 overlay 有 patch 没用的 key（orphan）→ 报错
    patch_keys = {"oc.run.a", "oc.run.b"}
    overlay_keys = {"oc.run.a", "oc.run.c"}
    with pytest.raises(ConsistencyError) as e:
        check_consistency(patch_keys, overlay_keys)
    assert "oc.run.b" in str(e.value)  # patch 用了但 overlay 缺
    assert "oc.run.c" in str(e.value)  # overlay 有但 patch 未用


def test_check_consistency_ok_when_equal():
    # 场景：两侧 key 集合相等 → 通过，不抛
    check_consistency({"oc.run.a"}, {"oc.run.a"})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_oc_overlay_consistency.py -v`
Expected: FAIL，`ModuleNotFoundError`

- [ ] **Step 3: 实现 check_oc_i18n_consistency.py**

```python
#!/usr/bin/env python3
# patches/check_oc_i18n_consistency.py
"""一致性守卫：补丁引入的每个 t("oc.X") key 必须在 oc_overlay.yaml 同时有 en+zh；
反之 overlay 每个 key 都应被补丁用到。任一不满足即 fail-loud，防止 key 与译文漂移。

可作脚本（构建期）运行，也被单元测试 import。"""
import pathlib
import re
import sys

import yaml

OVERLAY = pathlib.Path(__file__).resolve().parent.parent / "locales" / "oc_overlay.yaml"
_KEY_RE = re.compile(r't\(\s*"(oc\.[a-zA-Z0-9_.]+)"')


class ConsistencyError(RuntimeError):
    """patch 与 overlay 的 key 集合不一致时抛出。"""


def extract_patch_keys(replacements) -> set:
    """从 (old, new) 列表的 new 串里抽取所有 t("oc.X") 的 key。"""
    keys = set()
    for _old, new in replacements:
        keys.update(_KEY_RE.findall(new))
    return keys


def _flatten(node, prefix, out):
    # 叶子：{en,zh}
    if isinstance(node, dict) and set(node.keys()) <= {"en", "zh"} and node:
        if "en" not in node or "zh" not in node:
            raise ConsistencyError(f"overlay 叶子 {prefix} 缺 en 或 zh: {node!r}")
        out.add(prefix)
        return
    if isinstance(node, dict):
        for k, v in node.items():
            _flatten(v, f"{prefix}.{k}" if prefix else k, out)
        return
    raise ConsistencyError(f"overlay 非法节点 {prefix}: {node!r}")


def extract_overlay_keys(overlay: dict) -> set:
    out = set()
    _flatten(overlay, "", out)
    return out


def check_consistency(patch_keys: set, overlay_keys: set) -> None:
    missing = patch_keys - overlay_keys  # patch 用了但 overlay 没有
    orphan = overlay_keys - patch_keys   # overlay 有但 patch 没用
    if missing or orphan:
        raise ConsistencyError(
            f"oc i18n key 不一致：\n  patch 缺译文(missing): {sorted(missing)}\n"
            f"  overlay 多余(orphan): {sorted(orphan)}"
        )


def main() -> int:
    # 作为脚本运行时本目录(patches/)在 sys.path[0]，扁平 import 同目录补丁模块
    from patch_i18n_literals import REPLACEMENTS_RUN, REPLACEMENTS_BASE
    patch_keys = extract_patch_keys(REPLACEMENTS_RUN + REPLACEMENTS_BASE)
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8"))
    overlay_keys = extract_overlay_keys(overlay)
    try:
        check_consistency(patch_keys, overlay_keys)
    except ConsistencyError as e:
        print(f"[check_oc_i18n_consistency] {e}", file=sys.stderr)
        return 1
    print(f"[check_oc_i18n_consistency] OK：{len(patch_keys)} 个 oc.* key 双侧一致。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_oc_overlay_consistency.py -v`
Expected: PASS（4 passed）

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/patches/check_oc_i18n_consistency.py \
        runtime/hermes/hermes-v2026.6.5/tests/test_oc_overlay_consistency.py
git commit -m "feat(hermes-runtime): 增加 oc i18n key 一致性守卫

新增 check_oc_i18n_consistency.py：构建期校验补丁引入的 t(\"oc.X\") key 与
oc_overlay.yaml 的 key 集合双向一致、且每叶子含 en+zh，防止 key 与译文漂移。附测试。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3：改写 patch_i18n_literals.py 框架（TDD）

> 仅改框架与 base.py import 注入；替换表此刻先清空（Task 6+ 逐组填）。`patch()`
> 函数核心（`str.replace` + fail-loud + 幂等）保持不变——它对「整段表达式→t()调用」
> 同样适用。

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/patches/patch_i18n_literals.py`
- Modify: `runtime/hermes/hermes-v2026.6.5/tests/test_patch_i18n_literals.py`

- [ ] **Step 1: 重写测试**

```python
# runtime/hermes/hermes-v2026.6.5/tests/test_patch_i18n_literals.py
"""patch_i18n_literals 新框架（表达式→t() 替换 + base.py import 注入）单元测试。"""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "patches"))
from patch_i18n_literals import patch, ensure_i18n_import


def test_patch_replaces_full_expression_with_t_call():
    # 场景：把完整字符串表达式整体换成 t() 调用
    src = 'return "Queued for the next turn."\n'
    out = patch(src, [('"Queued for the next turn."', 't("oc.run.queue_queued")')])
    assert out == 'return t("oc.run.queue_queued")\n'


def test_patch_idempotent_when_t_call_present():
    # 场景：t() 调用已存在（已打过补丁）→ 幂等跳过，不抛
    src = 'return t("oc.run.queue_queued")\n'
    out = patch(src, [('"Queued for the next turn."', 't("oc.run.queue_queued")')])
    assert out == src


def test_patch_fail_loud_when_anchor_and_new_both_absent():
    # 场景：英文锚点与 t() 调用都不在源码 → 上游结构变更，抛错列出缺失
    with pytest.raises(RuntimeError) as e:
        patch("无关内容\n", [('"missing anchor"', 't("oc.run.x")')])
    assert "missing anchor" in str(e.value)


def test_ensure_i18n_import_adds_when_absent():
    # 场景：base.py 无 i18n import → 注入 from agent.i18n import t
    src = "import os\n\n\nclass Base:\n    pass\n"
    out = ensure_i18n_import(src)
    assert "from agent.i18n import t" in out


def test_ensure_i18n_import_idempotent():
    # 场景：已有 import → 不重复注入
    src = "import os\nfrom agent.i18n import t\n"
    assert ensure_i18n_import(src) == src
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_patch_i18n_literals.py -v`
Expected: FAIL（`ensure_i18n_import` 未定义 / 替换表非空导致行为不符）

- [ ] **Step 3: 改写 patch_i18n_literals.py**

把文件头 docstring 改述为「接入 t() catalog」；`REPLACEMENTS_RUN`、`REPLACEMENTS_BASE`
**清空为 `[]`**（Task 6+ 填）；保留 `patch()`；新增 `ensure_i18n_import()`；`main()`
对 base.py 先注入 import 再替换。核心代码：

```python
#!/usr/bin/env python3
# patches/patch_i18n_literals.py
"""构建期补丁：把 hermes 里漏翻的用户可见英文裸字符串接入原生 t() catalog。

把每条完整字符串表达式整体替换为 t("oc.<file>.<key>", kw=expr) 调用；中英译文在
locales/oc_overlay.yaml，由 merge_oc_locales.py 构建期合并进 upstream en/zh.yaml。
语言由 t() 自行从 config.yaml 的 display.language 解析。覆盖范围仅微信走的平台无关
路径（run.py + 所有适配器共用的 base.py），其它平台专属文案不在范围内。

约定：
- old = 未打补丁 upstream 里完整的字符串表达式源文本（含 f"/引号/跨行空白）。
- new = t(...) 调用字符串。
- old 不存在且 new 也不存在 → 上游结构变更，收集后一次性抛错。
- new 已存在即视为已打过补丁，跳过（幂等）。
- 当某 old 是另一 old 的子串时，长串排前。
"""
import pathlib
import re
import sys

RUN = pathlib.Path("/usr/local/lib/hermes-agent/gateway/run.py")
BASE = pathlib.Path("/usr/local/lib/hermes-agent/gateway/platforms/base.py")

I18N_IMPORT = "from agent.i18n import t"

# 替换表：(完整英文表达式源文本, t() 调用字符串)。由 Task 6+ 按组填充。
REPLACEMENTS_RUN: list[tuple[str, str]] = []
REPLACEMENTS_BASE: list[tuple[str, str]] = []

TARGETS: list[tuple[pathlib.Path, list[tuple[str, str]], bool]] = [
    (RUN, REPLACEMENTS_RUN, False),   # run.py 已有 import
    (BASE, REPLACEMENTS_BASE, True),  # base.py 需注入 import
]


def patch(content: str, replacements: list[tuple[str, str]]) -> str:
    """逐条把 old 整体替换为 new；fail-loud + 幂等（语义同旧框架）。"""
    replaced, already, missing = [], [], []
    for old, new in replacements:
        if old in content:
            content = content.replace(old, new)
            replaced.append(old)
        elif new in content:
            already.append(old)
        else:
            missing.append(old)
    print(f"[patch_i18n_literals] 已替换 {len(replaced)} 条，"
          f"幂等跳过 {len(already)} 条，缺失 {len(missing)} 条。")
    if missing:
        detail = "\n".join(f"  - {m!r}" for m in missing)
        raise RuntimeError(
            "patch_i18n_literals: 以下英文锚点找不到——上游文案结构可能已变更，"
            f"请更新补丁脚本：\n{detail}"
        )
    return content


def ensure_i18n_import(content: str) -> str:
    """若文件未导入 t 则注入 import；幂等。插在最后一条顶层 import 之后。"""
    if I18N_IMPORT in content:
        return content
    lines = content.splitlines(keepends=True)
    last_import = -1
    for i, line in enumerate(lines):
        if re.match(r"^(import |from )", line):
            last_import = i
    insert_at = last_import + 1 if last_import >= 0 else 0
    lines.insert(insert_at, I18N_IMPORT + "\n")
    return "".join(lines)


def main() -> int:
    for target, repls, need_import in TARGETS:
        if not target.exists():
            print(f"[patch_i18n_literals] 目标文件不存在: {target}", file=sys.stderr)
            return 1
        content = target.read_text(encoding="utf-8")
        original = content
        if need_import:
            content = ensure_i18n_import(content)
        content = patch(content, repls)
        if content != original:
            target.write_text(content, encoding="utf-8")
            print(f"[patch_i18n_literals] 已写回 {target}")
        else:
            print(f"[patch_i18n_literals] {target} 内容未变化（全部幂等跳过）")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_patch_i18n_literals.py -v`
Expected: PASS（5 passed）

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/patches/patch_i18n_literals.py \
        runtime/hermes/hermes-v2026.6.5/tests/test_patch_i18n_literals.py
git commit -m "refactor(hermes-runtime): 改写 i18n 补丁框架为表达式→t() 调用

patch_i18n_literals 从「英文片段→中文内联」改为「完整字符串表达式→t(oc.key)」；
新增 base.py 的 from agent.i18n import t 注入（run.py 上游已有）；替换表清空待逐组
填充。patch() 的 str.replace+fail-loud+幂等语义保持不变。重写对应单元测试。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4：Dockerfile 接线

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/Dockerfile`

- [ ] **Step 1: 定位现有补丁应用段**

Run: `grep -n 'patch_i18n_literals\|patch_api_server_reload\|COPY patches\|COPY renderer' runtime/hermes/hermes-v2026.6.5/Dockerfile`
Expected: 看到现有 `COPY` 与 `python .../patch_i18n_literals.py` 调用行（约 116-121 行）。

- [ ] **Step 2: 确保 locales 与新脚本被 COPY 进镜像**

在现有 `COPY patches/ ...` 段附近，确保 `locales/oc_overlay.yaml`、`patches/merge_oc_locales.py`、`patches/check_oc_i18n_consistency.py` 都进镜像。若现有用 `COPY patches/ /opt/oc/patches/` 整目录，则三脚本自动包含；`locales/` 需新增一行：

```dockerfile
COPY locales/ /opt/oc/locales/
```

- [ ] **Step 3: 在 patch_i18n_literals 之前插入合并、之后插入守卫**

把原本单独跑 `patch_i18n_literals.py` 的 RUN，改为按序执行（路径按仓库现有 COPY 落点调整）：

```dockerfile
RUN set -e; \
    python /opt/oc/patches/merge_oc_locales.py; \
    python /opt/oc/patches/patch_i18n_literals.py; \
    python /opt/oc/patches/check_oc_i18n_consistency.py; \
    python /opt/oc/patches/patch_api_server_reload.py
```

（顺序：先合并 catalog → 再改源码插 t() 调用 → 再守卫校验 key 一致 → 最后无关的 reload 补丁。任一步非零退出则 build 中断、不缓存坏层。）

- [ ] **Step 4: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/Dockerfile
git commit -m "build(hermes-runtime): Dockerfile 接入 oc catalog 合并与一致性守卫

COPY locales/ 进镜像；构建期按序执行 merge_oc_locales → patch_i18n_literals →
check_oc_i18n_consistency → patch_api_server_reload，任一失败即中断构建。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5：运行期解析冒烟测试（TDD，锁住「t() 真能切语言」）

**Files:**
- Create: `runtime/hermes/hermes-v2026.6.5/tests/test_oc_i18n_runtime.py`

> 不依赖 upstream，自建最小 catalog + 复刻 t() 的解析语义，确保 overlay 文案能被
> `str.format` 正确渲染、占位符与 kwarg 对得上。这是对「转换规则 R3/R4」的回归保护。

- [ ] **Step 1: 写测试**

```python
# runtime/hermes/hermes-v2026.6.5/tests/test_oc_i18n_runtime.py
"""验证 oc_overlay 文案能被 str.format 用规划的 kwarg 正确渲染（中英）。"""
import pathlib
import yaml

OVERLAY = pathlib.Path(__file__).resolve().parent.parent / "locales" / "oc_overlay.yaml"


def _leaves(node, prefix=""):
    if isinstance(node, dict) and set(node.keys()) <= {"en", "zh"} and node:
        yield prefix, node
        return
    if isinstance(node, dict):
        for k, v in node.items():
            yield from _leaves(v, f"{prefix}.{k}" if prefix else k)


def test_every_leaf_has_both_langs_and_no_underscore_placeholders():
    # 场景：每条 oc.* 都含 en+zh；占位符已按 R3 规范化（不含前导下划线变量名）。
    # overlay 为空时（Task 6+ 填充前）循环空跑通过，不引入红测试破坏 CI。
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8"))
    for key, leaf in _leaves(overlay):
        assert "en" in leaf and "zh" in leaf, f"{key} 缺语言"
        # 规范化后不应再出现源码内部变量名（如 {_secs_ago}）
        assert "{_" not in leaf["en"] and "{_" not in leaf["zh"], f"{key} 占位符未规范化"


def test_zh_placeholders_subset_of_en():
    # 场景：zh 用到的占位符必须是 en 占位符的子集（R4：zh 可少不可多）
    import string
    overlay = yaml.safe_load(OVERLAY.read_text(encoding="utf-8"))
    for key, leaf in _leaves(overlay):
        en_fields = {f for _, f, _, _ in string.Formatter().parse(leaf["en"]) if f}
        zh_fields = {f for _, f, _, _ in string.Formatter().parse(leaf["zh"]) if f}
        assert zh_fields <= en_fields, f"{key}: zh 占位符 {zh_fields - en_fields} 不在 en 中"
```

- [ ] **Step 2: 跑测试**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_oc_i18n_runtime.py -v`
Expected: PASS（2 passed）。overlay 当前为空，循环空跑通过；Task 6+ 填充后断言对每条生效。

- [ ] **Step 3: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/tests/test_oc_i18n_runtime.py
git commit -m "test(hermes-runtime): 增加 oc catalog 运行期渲染回归测试

校验每条 oc.* 含 en+zh、占位符已规范化、zh 占位符为 en 子集；overlay 为空时空跑通过，
填充后逐条生效。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6–N：逐组迁移 263 片段 → catalog + t() 调用

> **统一做法（每组都按转换规则 R1–R7 + 四个样例执行）：**
>
> 1. 打开当前 git 中**旧补丁的备份**作为译文数据源。由于 Task 3 已清空替换表，
>    迁移前先从 git 历史取回原始片段表：
>    `git show HEAD~3:runtime/hermes/hermes-v2026.6.5/patches/patch_i18n_literals.py > /tmp/old_patch.py`
>    （`HEAD~3` 为 Task 3 提交前；实际用 `git log --oneline` 确认指向「改写框架」提交之父）。
> 2. 对该组每条消息：按 R1 合并片段为完整消息 → 起 key（R2）→ 规范化占位符为 kwarg（R3/R4）
>    → en 取英文片段、zh 取中文片段（R7）写入 `locales/oc_overlay.yaml`。
> 3. 读未打补丁 upstream 源码确认**完整表达式锚点**（含跨行空白）：
>    用中文锚点在运行实例里定位结构、用英文片段还原英文锚点；不确定时在本地构建时
>    靠 fail-loud 报错逐条订正。
> 4. 把 `(完整英文表达式, t("oc.key", kw=expr))` 加进 `REPLACEMENTS_RUN`/`_BASE`。
>    保持「长串排前」（R6 子串顺序）。
> 5. 跑 `python -m pytest tests/test_oc_i18n_runtime.py tests/test_oc_overlay_consistency.py -v`
>    应通过（key 双侧一致、占位符规范）。
> 6. 提交该组。

每个 Task 对应旧补丁里的一个注释分组，**逐组独立提交**。组清单与 key 前缀如下
（行号指旧补丁文件 `/tmp/old_patch.py`）：

**run.py 组（`REPLACEMENTS_RUN`，key 前缀 `oc.run.`）：**

- [ ] **Task 6** 智能体超时诊断（gateway_timeout，旧行 36–50）→ keys: `timeout_last_activity`、`timeout_waiting_api`、`timeout_increase_limit`、`timeout_try_reset`
- [ ] **Task 7** provider 错误回包（旧行 52–66）→ `provider_auth_failed`、`provider_rejected`、`provider_rate_limited`、`provider_failed_retries`
- [ ] **Task 8** 会话过大 + 请求失败兜底（旧行 68–80）→ `session_too_large`、`use_compact`、`request_failed`、`try_reset_fresh`
- [ ] **Task 9** 处理中止/无回复 + 关闭重启通知（旧行 82–104）→ `processing_stopped`、`no_response_generated`、`action_restarting`、`action_shutting_down`、`task_interrupted`、`task_interrupted_resume`、`gateway_interrupt`
- [ ] **Task 10** 工具结果后无回复 + 会话自动重置（旧行 106–120）→ `no_response_after_tool`、`session_auto_reset`
- [ ] **Task 11** Steer / queue（旧行 122–144）→ `steer_failed`、`steer_queued`、`steer_usage*`、`steer_*`、`queue_usage`、`queue_queued`、`queue_depth`
- [ ] **Task 12** 智能体运行中命令受限（旧行 146–163）→ `agent_running_switch_model`、`agent_running_change_runtime`、`agent_running_goal`、`agent_running_midturn`、`force_stopped`
- [ ] **Task 13** 网关 draining 排队（旧行 165–180）→ `gateway_queued_*`、`gateway_not_accepting_*`、`command_blocked_hook`
- [ ] **Task 14** /new /reset 确认 + 快捷命令（旧行 182–205）→ `destructive_confirm`、`cancelled_unchanged`、`quick_cmd_*`
- [ ] **Task 15** 未知斜杠命令（旧行 207–215）→ `unknown_command`、`type_commands_*`
- [ ] **Task 16** 技能未装/禁用 + 配对流程（旧行 217–243）→ `skill_disabled`、`skill_not_installed`、`skill_disabled_platform`、`pairing_*`
- [ ] **Task 17** 更新进程交互 + 上下文压缩中止（旧行 245–269）→ `update_*`、`compression_aborted_*`
- [ ] **Task 18** 目标/子目标命令（旧行 271–288）→ `goal_none`、`subgoal_*`
- [ ] **Task 19** 权限层级 /access（旧行 289–306）→ `access_admin_only`、`access_you`、`access_tier_*`、`access_slash_*`
- [ ] **Task 20** 技能 bundles + 后台任务（旧行 307–328）→ `bundles_*`、`bgtask_*`
- [ ] **Task 21** 危险命令审批（旧行 330–344）→ `confirm_*`、`approve_*`
- [ ] **Task 22** hermes 自更新 + 代理模式错误（旧行 346–370）→ `update_finished*`、`update_failed*`、`gateway_restarted`、`proxy_*`
- [ ] **Task 23** run_agent 鉴权/通用错误 + 澄清送达（旧行 372–388）→ `provider_auth_failed_detail`、`generic_error`、`config_load_failed`、`context_injection_refused`、`clarify_*`
- [ ] **Task 24** 无活动超时提醒（旧行 390–402）→ `no_activity`、`timeout_countdown`、`continue_or_reset`
- [ ] **Task 25** 二次审计：忙碌状态明细 + 忙时 steer/queue/interrupt（旧行 412–425）→ `status_*`、`steered_into_run`、`queued_next_turn`、`interrupting_task`
- [ ] **Task 26** Kanban 子任务通知（旧行 427–436）→ `kanban_done`、`kanban_blocked`、`kanban_gave_up`、`kanban_crashed`、`kanban_timed_out`
- [ ] **Task 27** 语音 STT 未配置（旧行 438–448 + 6.5 专属 588–592）→ `voice_*`
- [ ] **Task 28** 会话自动重置通知（旧行 450–460）→ `reset_reason_*`、`reset_notice`、`reset_history_cleared`、`reset_resume_hint`、`reset_adjust_timing`
- [ ] **Task 29** 未设 home channel + 推理展示（旧行 462–474）→ `no_home_channel`、`home_channel_purpose`、`reasoning_label`、`more_lines`
- [ ] **Task 30** /model 会话信息 + 非管理员斜杠提示（旧行 476–493）→ `model_*`、`nonadmin_*`
- [ ] **Task 31** /platform 平台管理（旧行 495–529）→ `platform_*`
- [ ] **Task 32** /subgoal 错误 + /undo 确认 + 审批骨架 + 后台错误前缀 + 上线广播 + 自更新失败 + /subgoal clear（旧行 531–558）→ `subgoal_error`、`undo_confirm`、`confirm_choose`、`confirm_text_fallback`、`bgtask_error`、`gateway_online`、`update_failed_block`、`subgoal_cleared`
- [ ] **Task 33** 销毁性确认关闭提示（旧行 560–565）→ `destructive_disabled_*`
- [ ] **Task 34** 闲置超时「卡在工具」分支（旧行 567–575）→ `timeout_inactive`、`timeout_no_api`、`timeout_stuck_tool`
- [ ] **Task 35** 6.5 专属：长任务心跳 + subagent 降级 + /undo 多轮 + bundles 命令 + 压缩回退（旧行 577–609）→ `working_heartbeat`、`subagent_working`、`undo_confirm_multi`、`skill_bundles_list`、`bundle_*`、`compression_fallback_*`

**base.py 组（`REPLACEMENTS_BASE`，key 前缀 `oc.base.`）：**

- [ ] **Task 36** base 适配器文案（旧行 615–639）→ `format_failed_plaintext`、`generic_error`、`try_reset_fresh`、`clarify_reply_hint`、`media_audio`、`media_video`、`media_file`、`media_image`、`delivery_failed`、`delivery_failed_retry`

> 每个 Task 6–36 的 5-step 形态相同（按上方「统一做法」6 步），提交信息形如：
> `feat(hermes-runtime): 迁移<组名>文案至 t() catalog`。逐组提交便于回溯与 review。

---

## Task 37：本地构建镜像并验证补丁链

**Files:** 无（构建验证）

- [ ] **Step 1: 全量单测通过**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/ -v`
Expected: 全绿，含 `test_oc_i18n_runtime`（overlay 已填满）、`test_oc_overlay_consistency`。

- [ ] **Step 2: 本地构建镜像（注意构建缓存坑）**

Run（参考 memory「hermes 构建两大坑」，必要时 `NO_CACHE=1`）：
`make build-hermes-runtime`（或仓库实际 target；先 `grep -rn 'build-hermes-runtime\|hermes-runtime' Makefile`）
Expected: 构建成功；日志含 `[merge_oc_locales] 已合并`、`[patch_i18n_literals] 已替换 NNN 条`、`[check_oc_i18n_consistency] OK`。**若 patch 报「英文锚点找不到」**，按报错逐条读 upstream 源码订正对应 Task 的锚点后重构建。

- [ ] **Step 3: 验证镜像内 catalog 与源码已改**

```bash
# 用构建出的镜像跑一次性容器检查
docker run --rm <built-image> sh -c \
 'grep -c "^  run:" /usr/local/lib/hermes-agent/locales/zh.yaml; \
  grep -c "t(\"oc.run" /usr/local/lib/hermes-agent/gateway/run.py; \
  grep -c "from agent.i18n import t" /usr/local/lib/hermes-agent/gateway/platforms/base.py'
```
Expected: zh.yaml 含 `oc:` 块；run.py 含多处 `t("oc.run`；base.py 含 1 处 import。

---

## Task 38：k3d 真机端到端验证（中英双语，CLAUDE.md 强制）

> curl 不能替代——必须真实微信对话。参考 memory「验证标准要求」「本地 k3d 环境」。

- [ ] **Step 1: 部署改后镜像到本地 k3d**

按仓库现有流程把新镜像推到本地 registry 并 rollout（`grep -rn 'k3d-ocm-registry\|rollout' Makefile docs/local-development.md`）。

- [ ] **Step 2: 起两个实例 zh / en**

实例 A：所有者 UI 语言 zh（→ `apps.locale=zh` → `display.language=zh`）。
实例 B：所有者 UI 语言 en（→ `display.language=en`）。
确认各自 pod 内 `/opt/data/config.yaml` 的 `display.language` 正确。

- [ ] **Step 3: 逐场景触发并截图核对**

在微信对话分别触发并记录中/英输出：智能体超时诊断、provider 鉴权失败/限流、`/reset`、关闭与重启（drain）通知、会话过大、技能未装提示、配对流程、`/platform list`、kanban 子任务通知、语音消息无 STT。
Expected：实例 A 全中文、实例 B 全英文，**零中英混杂**；触发未覆盖文案时回落英文而非 key 路径（如出现 `oc.run.xxx` 字面，说明该 key 漏进 catalog，回到对应 Task 补）。

- [ ] **Step 4: 记录验证矩阵**

按 memory「验证标准要求」产出逐场景 × 双语言的验证矩阵（场景 / zh 截图 / en 截图 / 结论），存入 `docs/reports/`。

---

## Task 39：清理 renderer 过期注释

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/renderer/render_config_yaml.py:44-51`

- [ ] **Step 1: 改注释**

把「注意：run.py / base.py 里未走 t() 的裸字符串由构建期补丁……属已知局限，需另行改造该补丁。」改为：「run.py / base.py 的裸字符串已由 patch_i18n_literals.py 接入 oc.* catalog（locales/oc_overlay.yaml，构建期合并进 en/zh.yaml），随 display.language 输出中/英。」保留 `"display": {"language": (m.app_language or "en")}` 行不变。

- [ ] **Step 2: 跑 renderer 测试**

Run: `cd runtime/hermes/hermes-v2026.6.5 && python -m pytest tests/test_render_config_yaml.py -v`
Expected: PASS（注释改动不影响行为）。

- [ ] **Step 3: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/renderer/render_config_yaml.py
git commit -m "docs(hermes-runtime): renderer 注释更新为裸字符串已走 catalog

裸字符串已接入 oc.* t() catalog 随 display.language 切换，移除「已知局限」描述。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 完成判据

- `python -m pytest tests/` 全绿（含新增 merge / consistency / runtime 测试）。
- 镜像构建成功，日志含合并/替换/守卫三条 OK；无「英文锚点找不到」。
- k3d 真机：zh 实例全中文、en 实例全英文、零混杂、无 key 字面外泄；验证矩阵归档。
- `git log` 中各组迁移、基础设施、Dockerfile、注释清理按业务边界分开提交。

## 风险与回退

- **锚点失配**（最大风险）：构建期 fail-loud 列出缺失，逐条读 upstream 源码订正；不会静默漏翻。
- **跨行锚点空白不精确**：同上由 fail-loud 暴露；订正时从源码原样拷贝整段。
- **5.16 variant 同步**：本计划仅 v2026.6.5。若线上仍用 5.16，按 memory「variant 升级需带走补丁」另起任务同步（实现前用 `prod-cluster-ops` 确认线上在用版本）。
- **回退**：每组独立提交，发现某组译文/锚点有误可单独 revert 不影响其它组。
