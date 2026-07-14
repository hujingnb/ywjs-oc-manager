# Runtime AiGoWork 品牌文案 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将全部受支持 runtime variant 的终端用户中英文产品名称统一为 AiGoWork，同时保留真实 Hermes CLI 命令及上游技术标识。

**Architecture:** 各 variant 在构建期将 `locales/oc_overlay.yaml` 合并到上游 i18n catalog，故仅需更新其中的显示文本；不修改 i18n key、`patch_i18n_literals.py` 锚点和运行时行为。四个 variant 独立维护同一套 overlay，必须同步更新，避免镜像切换后品牌回退。

**Tech Stack:** YAML、Python pytest、Hermes runtime 构建期 i18n overlay。

---

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `runtime/hermes/hermes-aicc/locales/oc_overlay.yaml` | AICC runtime 的中英文聊天文案。 |
| `runtime/hermes/hermes-v2026.5.16/locales/oc_overlay.yaml` | 旧版可选 runtime 的中英文聊天文案。 |
| `runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml` | 旧版可选 runtime 的中英文聊天文案。 |
| `runtime/hermes/hermes-v2026.7.1/locales/oc_overlay.yaml` | 当前默认 runtime 的中英文聊天文案。 |

不创建新的 i18n key 或测试文件。既有 `test_oc_i18n_runtime.py` 已覆盖每个 overlay 的中英文叶子、占位符命名和语言字段一致性；本次变更不涉及占位符或逻辑。

### Task 1: 为四个 runtime overlay 统一用户可见品牌

**Files:**
- Modify: `runtime/hermes/hermes-aicc/locales/oc_overlay.yaml:355-376,513-514,657-658`
- Modify: `runtime/hermes/hermes-v2026.5.16/locales/oc_overlay.yaml:325-346,486-487,521-522,661-662`
- Modify: `runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml:355-376,693-694,537-538,681-682`
- Modify: `runtime/hermes/hermes-v2026.7.1/locales/oc_overlay.yaml:355-376,513-514,657-658`
- Test: `runtime/hermes/{hermes-aicc,hermes-v2026.5.16,hermes-v2026.6.5,hermes-v2026.7.1}/tests/test_oc_i18n_runtime.py`

- [ ] **Step 1: 先运行既有 i18n overlay 一致性测试，确认基线通过**

Run: `for variant in hermes-aicc hermes-v2026.5.16 hermes-v2026.6.5 hermes-v2026.7.1; do python3 -m pytest "runtime/hermes/${variant}/tests/test_oc_i18n_runtime.py" -v -p no:cacheprovider; done`

Expected: 每个 variant 的两条测试均 `PASSED`。

- [ ] **Step 2: 在四个 overlay 中替换产品名称，但不动真实命令**

将下列自然语言中的独立品牌名 `Hermes` 改为 `AiGoWork`，中英文两侧都要修改：`no_home_channel`、`update_finished`、`update_failed`、`update_timeout`、`update_finished_output`、`update_failed_output`、`update_finished_ok`、`update_failed_hint`、`gateway_online`，以及含运行环境说明的旧 variant 中的 `Hermes venv`。

主频道和网关上线的最终值必须为：

```yaml
# no_home_channel
en: "📬 No home channel is set for {platform_name}. A home channel is where AiGoWork delivers cron job results and cross-platform messages.\n\nType {sethome_cmd} to make this chat your home channel, or ignore to skip."
zh: "📬 尚未为 {platform_name} 设置主频道。主频道是 AiGoWork 投递定时任务结果和跨平台消息的地方。\n\n输入 {sethome_cmd} 即可将本聊天设为主频道,或忽略以跳过。"

# gateway_online
en: "♻️ Gateway online — AiGoWork is back and ready."
zh: "♻️ 网关已上线 —— AiGoWork 已恢复就绪。"
```

下列命令和上游 skill 标识必须逐字保留：`hermes skills config`、`hermes skills install {install_path}`、`hermes pairing approve {platform_name} {code}`、`hermes bundles create <name> --skill <s1> --skill <s2>`、`hermes gateway restart`、`hermes update`、`/skill hermes-agent-setup`。特别是 `update_failed_hint` 中反引号包裹的 `hermes update` 不得改为 AiGoWork。

- [ ] **Step 3: 运行 overlay 一致性测试，验证中英文占位符和 YAML 结构未受影响**

Run: `for variant in hermes-aicc hermes-v2026.5.16 hermes-v2026.6.5 hermes-v2026.7.1; do python3 -m pytest "runtime/hermes/${variant}/tests/test_oc_i18n_runtime.py" -v -p no:cacheprovider; done`

Expected: 每个 variant 的两条测试均 `PASSED`。

- [ ] **Step 4: 对四个 variant 运行 i18n key 一致性测试**

Run: `for variant in hermes-aicc hermes-v2026.5.16 hermes-v2026.6.5 hermes-v2026.7.1; do python3 -m pytest "runtime/hermes/${variant}/tests/test_oc_overlay_consistency.py" -v -p no:cacheprovider; done`

Expected: 每个 variant 的五条测试均 `PASSED`，证明 overlay 的 `oc.*` key 与补丁替换表仍一一对应。

- [ ] **Step 5: 检索并人工复核用户显示文本与命令边界**

Run: `rg -n -i 'Hermes|AiGoWork' runtime/hermes/hermes-aicc/locales/oc_overlay.yaml runtime/hermes/hermes-v2026.5.16/locales/oc_overlay.yaml runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml runtime/hermes/hermes-v2026.7.1/locales/oc_overlay.yaml`

Expected: `AiGoWork` 出现在主频道、更新、网关上线及 venv 的自然语言提示中；剩余 `Hermes` 仅存在于真实命令、上游 skill 名称或注释中。

- [ ] **Step 6: 复核 diff 并提交品牌文案变更**

Run: `git diff --check`，然后只暂存四个 `locales/oc_overlay.yaml` 文件，并使用下列提交信息：

```text
fix(runtime): 统一 AiGoWork 用户可见品牌文案

将各 runtime variant 的主频道、更新、网关恢复和运行环境提示中的 Hermes 品牌统一为 AiGoWork。

保留 hermes CLI 命令、上游 skill 名称和技术标识，确保操作指引可执行。
```

Expected: 仅四个 locale overlay 被提交；不包含技术标识或无关文件。
