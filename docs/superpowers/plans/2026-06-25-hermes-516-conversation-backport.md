# 5.16 variant 对话能力对齐（忠实移植 6.5）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `hermes-v2026.5.16` variant 补齐与 6.5 完全一致的会话能力（api_server `/api/sessions/*` 全套端点 + oc-ops `/oc/conversations` 转发），让被 pin 到 5.16 的实例对话页正常工作；manager 与 6.5 variant 零改动。

**Architecture:** 三层移植——① 构建期补丁 `patch_api_server_sessions.py` 把 6.5 api_server 的会话块（含 `_turn_transcript_messages`）+ 9 行路由注入 5.16 镜像内的 `gateway/platforms/api_server.py`（沿用 `patch_api_server_reload.py` 的锚点/fail-loud/幂等机制，内嵌字符串承载注入内容）；② 复制 6.5 `ocops/conversation.py` 转发层；③ 5.16 `ocops/server.py` 补 7 条 `/oc/conversations` Starlette 路由。底层 `SessionDB`/`_run_agent`/`_response_messages_turn_start_index` 等依赖在 5.16 已就绪，无需改 `hermes_state`。

**Tech Stack:** Python（aiohttp api_server 补丁 + Starlette oc-ops server）、pytest、Docker 构建、本地 k3d 真机验证。

**关联文档：** 设计 spec `docs/superpowers/specs/2026-06-25-hermes-516-conversation-backport-design.md`；构建/部署 runbook 见记忆 `project-hermes-516-port`、构建坑见 `project-hermes-build-gotchas`。

**前置条件：** 本地存在 `hermes-runtime:v2026.6.5-dev` 与 `hermes-runtime:v2026.5.16-dev` 镜像（`docker images | grep hermes` 可见）。若缺 6.5 镜像，先 `make build-hermes-runtime HERMES_VARIANT=hermes-v2026.6.5`。所有路径相对仓库根 `/home/hujing/dir/software/ywjs/oc-manager`。变量约定：`V516=runtime/hermes/hermes-v2026.5.16`、`V65=runtime/hermes/hermes-v2026.6.5`。

---

## Task 1: 提取 6.5 api_server 会话源码片段（注入内容的权威来源）

把要注入的两段源码从 6.5 镜像逐字提取到临时文件，作为 Task 2 内嵌字符串的内容来源。逐字提取保证「与 6.5 一致」。

**Files:**
- 临时产物（不入 git）：`/tmp/oc_sessions_block.txt`、`/tmp/oc_ttm.txt`

- [ ] **Step 1: 提取会话主体块（L1267–1700，434 行）**

Run:
```bash
docker run --rm --entrypoint sh hermes-runtime:v2026.6.5-dev \
  -c 'sed -n "1267,1700p" /usr/local/lib/hermes-agent/gateway/platforms/api_server.py' \
  > /tmp/oc_sessions_block.txt
wc -l /tmp/oc_sessions_block.txt
```
Expected: `434 /tmp/oc_sessions_block.txt`

- [ ] **Step 2: 校验块边界干净（首行是 `# /api/sessions` 注释，尾行是 `return response` 后空行）**

Run:
```bash
head -3 /tmp/oc_sessions_block.txt; echo '----'; tail -3 /tmp/oc_sessions_block.txt
```
Expected: 头部出现 `# /api/sessions — thin client/session resource API`；尾部最后一条非空行是 `        return response`。

- [ ] **Step 3: 提取 `_turn_transcript_messages` classmethod（L3364–3401，含 `@classmethod` 装饰器，38 行）**

Run:
```bash
docker run --rm --entrypoint sh hermes-runtime:v2026.6.5-dev \
  -c 'sed -n "3364,3401p" /usr/local/lib/hermes-agent/gateway/platforms/api_server.py' \
  > /tmp/oc_ttm.txt
head -2 /tmp/oc_ttm.txt; echo '----'; wc -l /tmp/oc_ttm.txt
```
Expected: 首两行为 `    @classmethod` 和 `    def _turn_transcript_messages(`；`38 /tmp/oc_ttm.txt`。

- [ ] **Step 4: 确认两段均无 `'''`（保证可用 `r'''...'''` 原始三引号包裹）**

Run:
```bash
grep -c "'''" /tmp/oc_sessions_block.txt /tmp/oc_ttm.txt
```
Expected: 两文件均 `:0`。

无需提交（临时产物）。进入 Task 2 时这两个文件的内容将被粘贴进补丁脚本。

---

## Task 2: 编写 `patch_api_server_sessions.py` 补丁脚本（TDD）

结构对齐现有 `$V516/patches/patch_api_server_reload.py`：同锚点机制、fail-loud、幂等。注入内容以内嵌原始字符串承载。

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/patches/patch_api_server_sessions.py`
- Test: `runtime/hermes/hermes-v2026.5.16/tests/test_patch_api_server_sessions.py`

- [ ] **Step 1: 写失败测试**（镜像 `tests/test_patch_api_server_reload.py` 的范式）

写入 `runtime/hermes/hermes-v2026.5.16/tests/test_patch_api_server_sessions.py`：

```python
# tests/test_patch_api_server_sessions.py
"""验证 patch_api_server_sessions.py 补丁脚本的注入行为。

覆盖：
- 正常注入：在 HTTP Handlers 区块前插入会话块（含 _handle_list_sessions 等
  9 个 handler 与 _turn_transcript_messages），在路由区块末尾追加 9 条
  /api/sessions 路由注册行。
- 幂等性：重复执行不累积内容，返回相同对象引用（不触发写文件）。
- 锚点缺失：任一锚点不存在时抛 RuntimeError，阻止静默失败。
"""

import sys
from pathlib import Path

import pytest

# 将 patches/ 目录加入 sys.path，以便直接 import patch 模块
_PATCHES_DIR = Path(__file__).parent.parent / "patches"
sys.path.insert(0, str(_PATCHES_DIR))

import patch_api_server_sessions as _mod  # noqa: E402


# 最小仿真 api_server.py：含两个锚点（路由末尾行 + HTTP Handlers 区块）。
# 路由注册行 12 空格缩进，与 ROUTE_ANCHOR 一致。
_FAKE_API_SERVER = (
    "class APIServerAdapter:\n"
    "\n"
    "    async def connect(self):\n"
    '            self._app.router.add_post("/v1/runs/{run_id}/stop", self._handle_stop_run)\n'
    "            # other setup\n"
    "\n"
    "    # ------------------------------------------------------------------\n"
    "    # HTTP Handlers\n"
    "    # ------------------------------------------------------------------\n"
    "\n"
    "    async def _handle_health(self, request):\n"
    "        pass\n"
)


def test_injects_session_handlers_and_routes():
    # 正常注入：9 个 handler 名与 9 条路由路径都应出现
    result = _mod.patch(_FAKE_API_SERVER)
    for name in (
        "_handle_list_sessions",
        "_handle_create_session",
        "_handle_get_session",
        "_handle_patch_session",
        "_handle_delete_session",
        "_handle_session_messages",
        "_handle_fork_session",
        "_handle_session_chat",
        "_handle_session_chat_stream",
    ):
        assert name in result, f"应注入 handler {name}"
    assert result.count('add_get("/api/sessions"') == 1, "应注入 list 路由"
    assert '/api/sessions/{session_id}/chat/stream' in result, "应注入 chat/stream 路由"


def test_injects_turn_transcript_helper():
    # chat/stream 依赖的 classmethod helper 必须随块注入
    result = _mod.patch(_FAKE_API_SERVER)
    assert "_turn_transcript_messages" in result, "应注入 _turn_transcript_messages"


def test_handlers_before_http_handlers_section():
    # 会话块应插入到 HTTP Handlers 区块之前
    result = _mod.patch(_FAKE_API_SERVER)
    assert result.index("_handle_list_sessions") < result.index("# HTTP Handlers")


def test_routes_after_stop_run_route():
    # /api/sessions 路由应注册在 /v1/runs/{run_id}/stop 之后
    result = _mod.patch(_FAKE_API_SERVER)
    assert result.index('add_get("/api/sessions"') > result.index("/v1/runs/{run_id}/stop")


def test_idempotent():
    # 幂等：已注入则 early-return 相同对象，不重复累积
    first = _mod.patch(_FAKE_API_SERVER)
    second = _mod.patch(first)
    assert second is first
    assert first.count("_handle_list_sessions") == 1


def test_raises_if_handler_anchor_missing():
    # HTTP Handlers 锚点缺失 → RuntimeError
    bad = _FAKE_API_SERVER.replace("# HTTP Handlers\n", "# SomethingElse\n")
    with pytest.raises(RuntimeError, match="HTTP Handlers 锚点"):
        _mod.patch(bad)


def test_raises_if_route_anchor_missing():
    # 路由锚点缺失 → RuntimeError
    bad = _FAKE_API_SERVER.replace(
        'add_post("/v1/runs/{run_id}/stop", self._handle_stop_run)',
        'add_post("/v1/runs/{run_id}/stop", self._handle_other)',
    )
    with pytest.raises(RuntimeError, match="路由锚点"):
        _mod.patch(bad)
```

- [ ] **Step 2: 运行测试，确认失败（模块不存在）**

Run:
```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_patch_api_server_sessions.py -q
```
Expected: 收集即失败，`ModuleNotFoundError: No module named 'patch_api_server_sessions'`。

- [ ] **Step 3: 写补丁脚本骨架**

写入 `runtime/hermes/hermes-v2026.5.16/patches/patch_api_server_sessions.py`，先写除注入字符串外的全部逻辑（注入常量留占位，下一步填充）：

```python
#!/usr/bin/env python3
# patches/patch_api_server_sessions.py
"""构建期补丁：给 hermes api_server 注入 /api/sessions/* 会话端点（与 6.5 一致）。

在 Dockerfile RUN 阶段执行，修改镜像内
/usr/local/lib/hermes-agent/gateway/platforms/api_server.py：
  1. 在 APIServerAdapter 的 HTTP Handlers 区块前插入 SESSIONS_BLOCK——会话块
     （5 个块内 helper + 9 个 _handle_*session* handler，含完整 chat_stream）
     与 _turn_transcript_messages classmethod。
  2. 在 connect() 路由注册块末尾追加 9 条 /api/sessions 路由。

SESSIONS_BLOCK 逐字提取自 6.5 api_server.py（会话块 L1267-1700 + classmethod
_turn_transcript_messages L3364-3401），其依赖的 db.*、_check_auth/
_ensure_session_db/_parse_session_key_header/_run_agent/
_response_messages_turn_start_index 与模块级 web/asyncio/time/logger/json 在
5.16 api_server.py 均已存在，故无需改 hermes_state。

端点鉴权沿用上游 api_server 自身的 _check_auth（与 6.5 行为一致）。
"""

import pathlib
import sys

TARGET = pathlib.Path(
    "/usr/local/lib/hermes-agent/gateway/platforms/api_server.py"
)

# --------------------------------------------------------------------------
# 注入片段 1：会话块 + _turn_transcript_messages。
# 内容逐字提取自 6.5 api_server.py（见模块 docstring 行号）。原始三引号包裹，
# 保留源码中的 \n 等转义字符不被 Python 解释（注入的是源码文本）。
# 行首已是类体方法缩进（4 空格），插入到 HANDLER_ANCHOR 前即落在类体内。
# --------------------------------------------------------------------------
SESSIONS_BLOCK = r'''<<<在此粘贴 /tmp/oc_sessions_block.txt 全文，随后空一行再粘贴 /tmp/oc_ttm.txt 全文>>>'''

# --------------------------------------------------------------------------
# 注入片段 2：9 条路由注册行，追加到 connect() 末尾已有路由之后。
# 逐字对齐 6.5 api_server.py L4130-4138（12 空格缩进）。
# --------------------------------------------------------------------------
ROUTE_INJECT = (
    '            # OC 对齐路由（oc-manager 注入）：/api/sessions 会话资源，转发自 oc-ops\n'
    '            self._app.router.add_get("/api/sessions", self._handle_list_sessions)\n'
    '            self._app.router.add_post("/api/sessions", self._handle_create_session)\n'
    '            self._app.router.add_get("/api/sessions/{session_id}", self._handle_get_session)\n'
    '            self._app.router.add_patch("/api/sessions/{session_id}", self._handle_patch_session)\n'
    '            self._app.router.add_delete("/api/sessions/{session_id}", self._handle_delete_session)\n'
    '            self._app.router.add_get("/api/sessions/{session_id}/messages", self._handle_session_messages)\n'
    '            self._app.router.add_post("/api/sessions/{session_id}/fork", self._handle_fork_session)\n'
    '            self._app.router.add_post("/api/sessions/{session_id}/chat", self._handle_session_chat)\n'
    '            self._app.router.add_post("/api/sessions/{session_id}/chat/stream", self._handle_session_chat_stream)\n'
)

# 路由锚点：connect() 中最后一条已有路由（与 reload 补丁同一锚点）
ROUTE_ANCHOR = (
    '            self._app.router.add_post("/v1/runs/{run_id}/stop",'
    " self._handle_stop_run)\n"
)

# HTTP Handlers 区块锚点（在此之前插入会话块）
HANDLER_ANCHOR = (
    "    # ------------------------------------------------------------------\n"
    "    # HTTP Handlers\n"
    "    # ------------------------------------------------------------------\n"
)


def patch(content: str) -> str:
    # 校验两个锚点都存在，任一缺失则报错中断构建
    if HANDLER_ANCHOR not in content:
        raise RuntimeError(
            "patch_api_server_sessions: 找不到 HTTP Handlers 锚点——"
            "上游 api_server.py 结构变更，请更新补丁脚本。"
        )
    if ROUTE_ANCHOR not in content:
        raise RuntimeError(
            "patch_api_server_sessions: 找不到路由锚点（/v1/runs/{run_id}/stop）——"
            "上游 api_server.py 结构变更，请更新补丁脚本。"
        )
    # 幂等检查：已注入则跳过，避免重复执行累积
    if "_handle_list_sessions" in content:
        print("patch_api_server_sessions: 已注入，跳过。", flush=True)
        return content

    # 插入会话块（在 HTTP Handlers 区块之前）
    content = content.replace(HANDLER_ANCHOR, SESSIONS_BLOCK + "\n" + HANDLER_ANCHOR)
    # 追加路由注册行（在最后一条路由之后）
    content = content.replace(ROUTE_ANCHOR, ROUTE_ANCHOR + ROUTE_INJECT)
    return content


if __name__ == "__main__":
    original = TARGET.read_text(encoding="utf-8")
    patched = patch(original)
    if patched is not original:
        TARGET.write_text(patched, encoding="utf-8")
        print(
            "patch_api_server_sessions: 成功注入 /api/sessions 会话端点。",
            flush=True,
        )
    sys.exit(0)
```

- [ ] **Step 4: 填充 SESSIONS_BLOCK 内嵌字符串**

把 `SESSIONS_BLOCK = r'''<<<...>>>'''` 占位行替换为真实内容：在 `r'''` 与 `'''` 之间，先粘贴 `/tmp/oc_sessions_block.txt` 全文，空一行，再粘贴 `/tmp/oc_ttm.txt` 全文。

机械化做法（避免手抖）——用脚本就地拼接：
```bash
python3 - <<'PY'
import pathlib
block = pathlib.Path("/tmp/oc_sessions_block.txt").read_text()
ttm = pathlib.Path("/tmp/oc_ttm.txt").read_text()
inner = block.rstrip("\n") + "\n\n" + ttm.rstrip("\n") + "\n"
assert "'''" not in inner, "块内含三引号，需改包裹方式"
p = pathlib.Path("runtime/hermes/hermes-v2026.5.16/patches/patch_api_server_sessions.py")
src = p.read_text()
placeholder = "SESSIONS_BLOCK = r'''<<<在此粘贴 /tmp/oc_sessions_block.txt 全文，随后空一行再粘贴 /tmp/oc_ttm.txt 全文>>>'''"
assert placeholder in src, "占位行未找到，确认 Step 3 已原样写入"
p.write_text(src.replace(placeholder, "SESSIONS_BLOCK = r'''\n" + inner + "'''"))
print("SESSIONS_BLOCK 填充完成，长度", len(inner), "字符")
PY
```
Expected: 打印 `SESSIONS_BLOCK 填充完成，长度 ... 字符`。

- [ ] **Step 5: 校验补丁脚本可被 Python 解析（语法/引号无误）**

Run:
```bash
python3 -c "import ast, pathlib; ast.parse(pathlib.Path('runtime/hermes/hermes-v2026.5.16/patches/patch_api_server_sessions.py').read_text()); print('OK')"
```
Expected: `OK`。

- [ ] **Step 6: 运行测试，确认通过**

Run:
```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_patch_api_server_sessions.py -q
```
Expected: `7 passed`。

- [ ] **Step 7: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/patches/patch_api_server_sessions.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_patch_api_server_sessions.py
git commit -m "feat(hermes-5.16): 新增 api_server 会话端点注入补丁

新增 patch_api_server_sessions.py，构建期把 6.5 api_server 的 /api/sessions
会话块(5 helper + 9 handler + _turn_transcript_messages)与 9 条路由注入
5.16 镜像内的 api_server.py，沿用 patch_api_server_reload 的锚点/fail-loud/
幂等机制。注入内容逐字提取自 6.5，依赖在 5.16 已就绪。附补丁单测。"
```

---

## Task 3: 把补丁接入 5.16 Dockerfile 构建链

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/Dockerfile`（patch 步骤 RUN 块与上方注释）

- [ ] **Step 1: 在 patch 链末尾追加补丁调用**

把 Dockerfile 中这段：
```
RUN set -e; \
    python3 /usr/local/lib/oc-entrypoint/patches/merge_oc_locales.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_i18n_literals.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/check_oc_i18n_consistency.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_reload.py
```
改为（最后一行加分号续行 + 新增一行）：
```
RUN set -e; \
    python3 /usr/local/lib/oc-entrypoint/patches/merge_oc_locales.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_i18n_literals.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/check_oc_i18n_consistency.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_reload.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_sessions.py
```

- [ ] **Step 2: 在该 RUN 上方的注释编号块补一条说明**

在 `#   4) patch_api_server_reload：...` 那段注释之后、`RUN set -e; \` 之前，插入：
```
#   5) patch_api_server_sessions：给 hermes api_server 注入 /api/sessions 会话端点
#      （list/create/get/patch/delete/messages/fork/chat/chat_stream，与 6.5 一致），
#      使被 pin 到 5.16 的实例对话页可用；锚点缺失即 exit 1 让构建失败。
```

- [ ] **Step 3: 校验 Dockerfile 仍为合法构建文件（语法静态检查）**

Run:
```bash
grep -n "patch_api_server_sessions" runtime/hermes/hermes-v2026.5.16/Dockerfile
```
Expected: 两行命中（注释 1 行 + RUN 1 行）。

- [ ] **Step 4: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/Dockerfile
git commit -m "build(hermes-5.16): patch 链接入会话端点注入补丁

在 Dockerfile patch RUN 链末尾追加 patch_api_server_sessions.py，并补注释
说明第 5 步注入 /api/sessions 端点。"
```

---

## Task 4: 复制 oc-ops 转发层 `conversation.py` 及其单测

6.5 的 `ocops/conversation.py` 版本无关（仅带 token 透传到 `127.0.0.1:8642/api/sessions/*`），逐字复制。

**Files:**
- Create: `runtime/hermes/hermes-v2026.5.16/ocops/conversation.py`（复制自 6.5）
- Create: `runtime/hermes/hermes-v2026.5.16/tests/test_conversation.py`（复制自 6.5）

- [ ] **Step 1: 逐字复制转发层与单测**

Run:
```bash
cp runtime/hermes/hermes-v2026.6.5/ocops/conversation.py \
   runtime/hermes/hermes-v2026.5.16/ocops/conversation.py
cp runtime/hermes/hermes-v2026.6.5/tests/test_conversation.py \
   runtime/hermes/hermes-v2026.5.16/tests/test_conversation.py
```

- [ ] **Step 2: 确认两文件与 6.5 字节一致**

Run:
```bash
diff runtime/hermes/hermes-v2026.6.5/ocops/conversation.py runtime/hermes/hermes-v2026.5.16/ocops/conversation.py && echo SAME_OCOPS
diff runtime/hermes/hermes-v2026.6.5/tests/test_conversation.py runtime/hermes/hermes-v2026.5.16/tests/test_conversation.py && echo SAME_TEST
```
Expected: `SAME_OCOPS` 与 `SAME_TEST` 均打印（无 diff 输出）。

- [ ] **Step 3: 运行转发层单测**

Run:
```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_conversation.py -q
```
Expected: 全部 passed（与 6.5 同套用例，行数 84 行那份）。

- [ ] **Step 4: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/conversation.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_conversation.py
git commit -m "feat(hermes-5.16): 复制 oc-ops 会话转发层 conversation.py

逐字复制 6.5 的 ocops/conversation.py 及其单测；该层版本无关，仅带 Bearer
token 透传到同 pod api_server /api/sessions/* 并裁剪字段、规整 SSE 帧。"
```

---

## Task 5: 5.16 `ocops/server.py` 补 7 条 `/oc/conversations` 路由

把 6.5 server.py 的 import、7 个 `conversation_*` handler、7 条 Route 逐字补入 5.16；并复制 server 层会话单测。

**Files:**
- Modify: `runtime/hermes/hermes-v2026.5.16/ocops/server.py`（import 行、handler 块、routes 列表）
- Create: `runtime/hermes/hermes-v2026.5.16/tests/test_server_conversation.py`（复制自 6.5）

- [ ] **Step 1: import 行加入 `conversation`**

把 `runtime/hermes/hermes-v2026.5.16/ocops/server.py` 中：
```python
from ocops import channel, cron, doctor, info, kanban, skills
```
改为（按字母序插入 `conversation`，与 6.5 一致）：
```python
from ocops import channel, conversation, cron, doctor, info, kanban, skills
```

- [ ] **Step 2: 复制 7 个 `conversation_*` handler 到 5.16 server.py**

提取 6.5 的会话 handler 块（580–718 行，含「会话端点」分隔注释）：
```bash
sed -n '580,718p' runtime/hermes/hermes-v2026.6.5/ocops/server.py > /tmp/conv_handlers.txt
head -2 /tmp/conv_handlers.txt; echo ...; tail -2 /tmp/conv_handlers.txt
```
确认首行是 `# 会话（conversation）端点：...` 注释、尾行是 `conversation_chat_stream` 的 `return StreamingResponse(...)`。

在 5.16 `server.py` 的 `routes = [` 定义行（第 581 行附近）**之前**、最后一个已有 handler 函数之后，插入 `/tmp/conv_handlers.txt` 全文。
（这 7 个 handler 仅依赖 `_ok`、`_err`、`conversation.*`、`StreamingResponse`——前三者 5.16 已有，`StreamingResponse` 已在 server.py 第 19 行 import。）

- [ ] **Step 3: routes 列表追加 7 条 `/oc/conversations` Route**

在 5.16 `server.py` 的 `routes = [ ... ]` 列表内（紧随既有 `/oc/*` 路由之后、列表 `]` 之前）追加，逐字对齐 6.5：
```python
    Route("/oc/conversations", conversation_list, methods=["GET"]),
    Route("/oc/conversations", conversation_create, methods=["POST"]),
    Route("/oc/conversations/{sid}/messages", conversation_messages, methods=["GET"]),
    Route("/oc/conversations/{sid}/chat", conversation_chat, methods=["POST"]),
    Route("/oc/conversations/{sid}/chat/stream", conversation_chat_stream, methods=["POST"]),
    Route("/oc/conversations/{sid}", conversation_delete, methods=["DELETE"]),
    Route("/oc/conversations/{sid}", conversation_rename, methods=["PATCH"]),
```

- [ ] **Step 4: 复制 server 层会话单测**

Run:
```bash
cp runtime/hermes/hermes-v2026.6.5/tests/test_server_conversation.py \
   runtime/hermes/hermes-v2026.5.16/tests/test_server_conversation.py
diff runtime/hermes/hermes-v2026.6.5/tests/test_server_conversation.py \
     runtime/hermes/hermes-v2026.5.16/tests/test_server_conversation.py && echo SAME
```
Expected: `SAME`。

- [ ] **Step 5: 校验 server.py 语法 + 运行 server 会话单测**

Run:
```bash
python3 -c "import ast,pathlib; ast.parse(pathlib.Path('runtime/hermes/hermes-v2026.5.16/ocops/server.py').read_text()); print('SYNTAX_OK')"
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest tests/test_server_conversation.py -q
```
Expected: `SYNTAX_OK`；server 会话单测全部 passed。

- [ ] **Step 6: 跑整套 variant 测试，确认无回归**

Run:
```bash
cd runtime/hermes/hermes-v2026.5.16 && python3 -m pytest -q
```
Expected: 全绿（新增的会话相关用例 + 既有用例均 passed）。

- [ ] **Step 7: 提交**

```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/server.py \
        runtime/hermes/hermes-v2026.5.16/tests/test_server_conversation.py
git commit -m "feat(hermes-5.16): oc-ops server 补 7 条 /oc/conversations 路由

import conversation 模块，补入与 6.5 一致的 7 个 conversation_* handler 与
Route(list/create/messages/chat/chat-stream/delete/rename)，并复制 server 层
会话单测。handler 仅依赖已有的 _ok/_err/StreamingResponse 与转发层。"
```

---

## Task 6: 构建镜像（fail-loud 闸门）+ 路由存在性冒烟

**Files:** 无（构建与运行时验证）

- [ ] **Step 1: 构建 5.16 镜像，确认补丁 fail-loud 不报锚点缺失**

Run:
```bash
make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.16
```
Expected: 构建成功；日志出现 `patch_api_server_sessions: 成功注入 /api/sessions 会话端点。`。若报 `找不到 ... 锚点`，说明上游 5.16 api_server 结构与预期不符，需回到 Task 2 核对锚点字符串。
（若撞 install.sh 旧布局缓存致 `/opt/data/bin/uv` 失败，按 `project-hermes-build-gotchas`：`NO_CACHE=1 HERMES_VARIANT=hermes-v2026.5.16 make build-hermes-runtime`。）

- [ ] **Step 2: 验证注入后 api_server.py 含 9 条路由**

Run:
```bash
docker run --rm --entrypoint sh hermes-runtime:v2026.5.16-dev \
  -c 'grep -c "add_.*\"/api/sessions" /usr/local/lib/hermes-agent/gateway/platforms/api_server.py'
```
Expected: `9`。

- [ ] **Step 3: 起容器冒烟 `/api/sessions` 返回 401（有路由有鉴权）而非 404**

Run（最小化起 api_server 较重，改为静态确认 handler+route 均注入、Python 可导入该模块语法）：
```bash
docker run --rm --entrypoint sh hermes-runtime:v2026.5.16-dev -c '
python3 -c "import ast; ast.parse(open(\"/usr/local/lib/hermes-agent/gateway/platforms/api_server.py\").read()); print(\"api_server.py SYNTAX_OK\")"
grep -c "def _handle_session_chat_stream\|def _turn_transcript_messages\|def _handle_list_sessions" /usr/local/lib/hermes-agent/gateway/platforms/api_server.py'
```
Expected: `api_server.py SYNTAX_OK`；计数 `3`（三个关键方法都在）。

> 真正的 401-vs-404 行为验证放到 Task 7 的真机环境（需完整 gateway 运行起来），此处只做静态确保注入合法、不破坏 api_server 模块语法。

无需提交（构建产物）。

---

## Task 7: 本地 k3d 真机浏览器端到端验证（CLAUDE.md 硬要求）

按 `project-hermes-516-port` runbook 部署到本地 k3d 一个 5.16 实例，用真实浏览器逐项验证对话功能。**这是交付前的硬性验收，curl 不能替代。**

**Files:** 无（部署 + 浏览器验证）

- [ ] **Step 1: 推送镜像到本地 registry**

Run:
```bash
docker tag hermes-runtime:v2026.5.16-dev k3d-ocm-registry.localhost:5000/oc-manager-hermes:v2026.5.16-dev8
docker push k3d-ocm-registry.localhost:5000/oc-manager-hermes:v2026.5.16-dev8
```
（dev 序号自增，避免撞缓存；同步更新 secret.yaml local 的 5.16 条目 ref 指向 dev8。）

- [ ] **Step 2: 把某本地实例助手版本切到 5.16 并触发镜像滚动重建**

后台 http://ocm.localhost（admin/admin123）→「助手版本」编辑实例所绑版本 → 镜像改 Hermes v2026.5.16(dev8) → 保存 → 实例详情「立即重启」（镜像变更触发 deployment 滚动重建 pod）。
校验 pod 拉到新镜像：
```bash
rtk proxy kubectl --kubeconfig ~/.kube/config get pod -n oc-apps -o jsonpath='{..imageID}' | tr ' ' '\n' | grep hermes
```
Expected: digest == 本地 `hermes-runtime:v2026.5.16-dev` 的 digest。

- [ ] **Step 3: pod 内冒烟 `/api/sessions` 返回 401（不再 404）**

Run（在该实例 pod 的 hermes 容器内）：
```bash
rtk proxy kubectl --kubeconfig ~/.kube/config exec -n oc-apps <pod> -c hermes -- \
  python3 -c "import urllib.request,urllib.error
try:
    urllib.request.urlopen('http://127.0.0.1:8642/api/sessions')
    print('200/无鉴权')
except urllib.error.HTTPError as e:
    print('HTTP', e.code)"
```
Expected: `HTTP 401`（有路由有鉴权，与 6.5 一致；旧 5.16 此处为 404）。

- [ ] **Step 4: 浏览器逐项验证对话功能**

浏览器打开后台该实例详情 →「对话」tab，逐项过并确认无报错：
1. **会话列表**加载（非 NOT_FOUND / 非整页 500）。
2. 打开一条会话看**历史消息**。
3. **新建**会话。
4. 在会话内**发消息**（chat，非流式路径可用、有 assistant 回复）。
5. **流式回复**（chat/stream：观察 SSE 增量逐字出现、tool 事件、最终 completed）。
6. **重命名**会话，列表标题更新。
7. **删除**会话，列表移除。

若任一步失败：chat/stream 相关失败优先排查 `_run_agent` 调用契约（设计 spec「主要风险」），按需在 SESSIONS_BLOCK 注入块内做最小适配后回到 Task 6 重建。其它失败按 `project-conversation-516-gap` 的分层 exec 诊断法逐跳定位（manager→oc-ops→api_server）。

- [ ] **Step 5: 跨版本回归确认**

确认本次零改动的 6.5 实例对话功能不受影响：把一个实例保持/切到 6.5，浏览器重复 Step 4 关键项（列表 + 发消息）正常。

- [ ] **Step 6: 记录验证证据并收尾**

在交付说明中给出逐项验证矩阵（含 pod digest、401 冒烟输出、7 项浏览器结果截图/描述）。本地验证环境按记忆习惯可在验证后恢复基线（实例版本改回原状）。

---

## 交付须知

- 本计划改动均为 5.16 variant 构建产物；生效需 `make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.16` 重建并按 runbook 部署/灰度。
- **线上写操作（update-config / 发版 / 实例版本切换）按 `prod-cluster-ops` 铁律由用户执行**，本计划不直接动线上。
- manager、前端、`hermes_state.py`、6.5 variant 全程零改动。
