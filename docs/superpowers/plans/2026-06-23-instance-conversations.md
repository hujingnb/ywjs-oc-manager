# 实例对话功能 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在实例详情页新增「对话」能力，让 org_member 读取本实例下跨渠道会话、续聊驱动 bot 回复（文字+图片、流式）、并管理会话。

**Architecture:** 全链路复用现有 Kanban/Cron 模式：前端 `AppConversationsTab.vue` → manager `/api/v1/apps/:appId/hermes/conversations/*`（handler→service→ocops client）→ oc-ops sidecar `:8080`（新增 `conversation.py`）→ 同 pod hermes api_server `127.0.0.1:8642/api/sessions/*`。manager 不落库，会话数据实时经 oc-ops 透传。

**Tech Stack:** Go（gin / testify / 窄接口 + 假实现单测）、Python（Starlette / urllib / pytest，oc-ops sidecar）、Vue 3 + TS（Naive UI / vitest）。

**Spec:** `docs/superpowers/specs/2026-06-23-instance-conversations-design.md`

---

## 关键既有模式参照（实现前必读）

| 角色 | 参照文件 |
|---|---|
| oc-ops client（非流式） | `internal/integrations/ocops/client_kanban.go` |
| oc-ops client（SSE） | `internal/integrations/ocops/client_sse.go`（`openStream`/`scanSSE`） |
| oc-ops client 类型 | `internal/integrations/ocops/types_channel.go` |
| 窄接口 + 编译期断言 + 错误映射 | `internal/service/ocops.go`（`kanbanOps`/`mapOcOpsKanbanErr`/`OcOpsResolver`） |
| service（resolve+鉴权+校验+透传） | `internal/service/hermes_kanban.go` |
| handler + SSE 写出 | `internal/api/handlers/hermes_kanban.go`（`StreamEvents`） |
| 错误码映射规则 | `internal/api/handlers/request_errors.go`（`mappedServiceErrorRules`） |
| router 装配 | `internal/api/router.go`（`RouterDeps` 字段 + 注册块）、`cmd/server/main.go:506` |
| auth 谓词 | `internal/auth/authorizer.go`（`CanViewAppKanban`/`CanManageAppKanban`） |
| oc-ops pod server | `runtime/hermes/hermes-v2026.6.5/ocops/server.py`、`skills.py`（调 127.0.0.1:8642 先例） |
| 前端 tab | `web/src/pages/apps/AppKanbanTab.vue`、`AppChannelsTab.vue` |
| 前端路由 | `web/src/app/router.ts`（apps detail 子路由） |
| org_member 左侧菜单 | `web/src/layouts/DashboardLayout.vue`（`memberAppTabKey('kanban')` 等） |
| 详情页 tab 列表 | `web/src/pages/apps/AppDetailPage.vue`（`allTabs`） |

**约定提醒（来自 AGENTS.md）：**
- 权限谓词只放 `internal/auth/authorizer.go`，service 不写本地 `canX`。
- 新增 handler 请求体放 `internal/api/handlers/dto.go` 并导出大写命名；响应用 `service.XxxResult`。
- 改 handler 签名/请求体/响应/路由后必须跑 `make openapi-gen` + `make web-types-gen`，二者入 git。
- 单测用 testify `require`/`assert`；每个测试方法/子测试/table 行都要相邻中文注释。
- 新增/重构代码补中文注释（包/文件/方法/字段/参数/代码段）。

---

## Phase 0：运行时 Spike（必须最先做，决定 oc-ops 鉴权与投递策略）

### Task 1: Spike —— 在真实运行 pod 上查实 api_server 可达性、鉴权与投递

**这是非 TDD 的调查任务。** 产出写入计划末尾「Spike 结论」节并据此微调 Task 2/3。

**背景（已静态确认 —— 关键前提成立，鉴权已解）：**
- **api_server 在每个真实 app pod 都启用**：manager 渲染 app pod 时给 hermes 容器注入
  `API_SERVER_ENABLED=true`（`internal/integrations/k8sorch/render.go:107-111`，并有
  `render_test.go:133` 单测守卫）。据 `gateway/config.py:1508-1518`，
  `api_server_enabled or api_server_key` 为真即启用 `Platform.API_SERVER`，监听 `127.0.0.1:8642`。
- **api_server 不鉴权**：仓库**未**注入 `API_SERVER_KEY`，故 `self._api_key=""`，
  `_check_auth` 命中 `if not self._api_key: return None` 直接放行。**oc-ops 调
  `/api/sessions` 无需任何 Bearer token**（与 `/oc/skills/reload` 免鉴权同理）。
- 因此 Spike 的头号问题（api_server 跑不跑 / 怎么鉴权）**静态已定 = 路径 A，无需 patch**。
  本 Task 在真实 pod 上**复核**该结论，并补齐两项非阻塞细节：真实 JSON 字段、`/chat` 微信投递行为。
- 注意本地 `-dev` 镜像会被 manager `OcOpsResolverFromStore` 判 `Supported=false` → 对话
  service 返回「不支持」。本地浏览器验证需临时放开该 gate 或用非 -dev 镜像；Spike 经
  `kubectl exec` 直连 pod api_server，不受此 gate 影响。

- [ ] **Step 1: 找一个运行中的真实（非 -dev）app pod**

Run:
```bash
rtk proxy kubectl get pods -n oc-apps
```
Expected: 至少一个 `app-<id>-...` pod 处于 Running（2/2，含 ocops sidecar）。若本地无，则在 k3d 起一个真实实例（参考 docs/local-development.md）。

- [ ] **Step 2: 确认 api_server 在跑、端口监听**

Run（在 hermes 主容器内）:
```bash
rtk proxy kubectl exec -n oc-apps <pod> -c hermes -- sh -c 'cat /opt/data/config.yaml 2>/dev/null | grep -iA3 api_server; ss -ltnp 2>/dev/null | grep 8642 || netstat -ltn 2>/dev/null | grep 8642'
```
记录：config.yaml 是否含 `api_server` 段及其 `key`；8642 是否 LISTEN。

- [ ] **Step 3: 判定 oc-ops 能否拿到 API_SERVER_KEY**

依次确认（按优先级，命中即停）：
1. oc-ops sidecar 容器 env 是否有 `API_SERVER_KEY`：
   `rtk proxy kubectl exec -n oc-apps <pod> -c ocops -- printenv API_SERVER_KEY`
2. 共享卷 `/opt/data/config.yaml` 的 `api_server.key` 是否可被 ocops 容器读到：
   `rtk proxy kubectl exec -n oc-apps <pod> -c ocops -- sh -c 'grep -iA3 api_server /opt/data/config.yaml'`

记录命中哪条 → 决定 Task 2 的 `_api_server_key()` 实现来源。

- [ ] **Step 4: 实测 `/api/sessions` 列表与 `/messages` 真实 JSON 形状**

Run（容器内，带上一步拿到的 key）:
```bash
rtk proxy kubectl exec -n oc-apps <pod> -c hermes -- sh -c \
 'curl -s -H "Authorization: Bearer $API_SERVER_KEY" "http://127.0.0.1:8642/api/sessions?limit=5" | head -c 2000; echo; \
  SID=$(curl -s -H "Authorization: Bearer $API_SERVER_KEY" "http://127.0.0.1:8642/api/sessions?limit=1" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d[\"data\"][0][\"id\"] if d.get(\"data\") else \"\")"); \
  echo SID=$SID; \
  curl -s -H "Authorization: Bearer $API_SERVER_KEY" "http://127.0.0.1:8642/api/sessions/$SID/messages" | head -c 2000'
```
**抄下 session 对象与 message 对象的精确字段名**（id/source/title/model/时间戳/计数/role/content/图片结构）→ 用于校准 Task 4 的 DTO。

- [ ] **Step 5: 实测续聊微信会话时 `/chat` 是否投递到微信用户**

挑一条 `source=weixin` 的会话，向其 `/chat` 发一条测试消息，观察该微信用户是否真的收到。
记录结论 → 写入 §2.3 行为：若**不**投递，v1 不处理（设计已定，不补投递）。

- [ ] **Step 6: 记录 Spike 结论并选定 oc-ops 鉴权路径**

把结论填入本文件末「Spike 结论」节，二选一：
- **路径 A（默认期望，无 patch）**：oc-ops 能拿到 key → Task 2 直接带 Bearer 调 `/api/sessions`。
- **路径 B（兜底）**：oc-ops 拿不到 key 且 api_server 未对内网免鉴权 → 仿 `patch_api_server_reload.py` 注入免鉴权 `/oc/sessions-proxy/*` 端点（构建期 patch）。**选 B 需在此暂停，告知需求方「v1 需引入一个上游 patch」再继续。**

- [ ] **Step 7: 提交 Spike 结论**

```bash
git add docs/superpowers/plans/2026-06-23-instance-conversations.md
git commit -m "docs(spike): 记录实例对话 api_server 可达性与投递结论"
```

> 以下 Task 2/3 按**路径 A**编写（最可能）。若 Spike 选路径 B，按 §Spike 结论 调整 `conversation.py` 鉴权方式与新增 patch 任务，其余 Task（manager + 前端）不变。

---

## Phase 1：oc-ops sidecar 转发层（Python）

### Task 2: oc-ops `conversation.py` —— 转发到 api_server 的核心逻辑

**Files:**
- Create: `runtime/hermes/hermes-v2026.6.5/ocops/conversation.py`
- Test: `runtime/hermes/hermes-v2026.6.5/tests/test_conversation.py`

- [ ] **Step 1: 写失败测试**

`tests/test_conversation.py`：
```python
# 覆盖 conversation 模块对 api_server 的转发：鉴权头注入、非 2xx → OpsError、列表/历史/新建/删除 path 拼装。
import json
import urllib.error
from unittest import mock

import pytest

from ocops import conversation
from ocops.errors import OpsError


class _FakeResp:
    """模拟 urllib urlopen 的上下文管理返回体。"""
    def __init__(self, body: bytes):
        self._body = body
    def __enter__(self):
        return self
    def __exit__(self, *a):
        return False
    def read(self):
        return self._body


# 列会话：带 source/limit query，注入 Bearer，透传 api_server JSON 的 data 数组。
def test_list_sessions_forwards_and_unwraps():
    payload = json.dumps({"object": "list", "data": [{"id": "s1", "source": "weixin"}]}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.list_sessions(source="weixin", limit=50, offset=0)
    assert out == [{"id": "s1", "source": "weixin"}]
    req = op.call_args[0][0]
    assert req.get_header("Authorization") == "Bearer k"
    assert "source=weixin" in req.full_url and "limit=50" in req.full_url


# 读历史：path 含 session id（转义），返回 api_server messages 数组。
def test_session_messages_path_and_passthrough():
    payload = json.dumps({"data": [{"role": "user", "content": "hi"}]}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.session_messages("s 1")
    assert out == [{"role": "user", "content": "hi"}]
    assert "/api/sessions/s%201/messages" in op.call_args[0][0].full_url


# api_server 返回 404 → 抛 OpsError("NOT_FOUND")，供 server 映射 404。
def test_non_2xx_maps_to_opserror():
    err = urllib.error.HTTPError("u", 404, "nf", {}, None)
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", side_effect=err):
        with pytest.raises(OpsError) as ei:
            conversation.session_messages("nope")
    assert ei.value.code == "NOT_FOUND"


# 续聊：POST /chat，body 透传 message，返回 assistant 回复对象。
def test_chat_posts_message():
    payload = json.dumps({"session_id": "s1", "message": {"role": "assistant", "content": "ok"}}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.chat("s1", {"message": "hi"})
    assert out["message"]["content"] == "ok"
    assert op.call_args[0][0].method == "POST"
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=/usr/local/lib python -m pytest tests/test_conversation.py -v`
Expected: FAIL（`No module named 'ocops.conversation'`）。本地缺 starlette 依赖时改在镜像构建期自检验证（见 Task 3 Step 4）。

- [ ] **Step 3: 写实现**

`ocops/conversation.py`：
```python
# ocops/conversation.py
"""会话转发层：把 manager 经 oc-ops 发来的会话请求转发到同 pod 内的 hermes
api_server（127.0.0.1:8642 /api/sessions/*），注入 Bearer 鉴权并把非 2xx 响应
映射为 OpsError。manager 不持有会话数据，oc-ops 仅做带 token 的透传 + 字段裁剪。

鉴权来源见 _api_server_key：优先 env API_SERVER_KEY，回退共享卷 config.yaml 的
api_server.key（由 Spike Task 1 Step 3 确认实际命中项）。"""
from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

from ocops.errors import OpsError

# api_server 容器内回环地址；与 skills.py RELOAD_URL 同一进程。
_API_BASE = "http://127.0.0.1:8642"
# 单次转发超时（秒）。续聊可能触发一次 agent 回合，给足时间但不无限阻塞。
_TIMEOUT = 120


def _api_server_key() -> str:
    """解析 api_server 的 Bearer key。

    顺序（命中即返回）：环境变量 API_SERVER_KEY → /opt/data/config.yaml 的
    api_server.key。两者都没有时返回空串（api_server 无 key 时 _check_auth 放行）。
    """
    env = os.environ.get("API_SERVER_KEY", "").strip()
    if env:
        return env
    cfg = Path(os.environ.get("OC_DATA_DIR", "/opt/data")) / "config.yaml"
    try:
        import yaml  # 镜像内随 hermes venv 提供
        data = yaml.safe_load(cfg.read_text(encoding="utf-8")) or {}
        return str((data.get("api_server") or {}).get("key", "") or "")
    except Exception:
        return ""


def _request(method: str, path: str, body: dict | None = None) -> bytes:
    """对 api_server 发一次请求，返回原始响应体；非 2xx / 网络错误映射为 OpsError。"""
    url = _API_BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    key = _api_server_key()
    if key:
        req.add_header("Authorization", "Bearer " + key)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=_TIMEOUT) as resp:
            return resp.read()
    except urllib.error.HTTPError as e:
        # 把 api_server 的 HTTP 状态码翻译成 oc-ops 契约错误码
        code = {400: "BAD_REQUEST", 401: "INTERNAL", 404: "NOT_FOUND"}.get(e.code, "INTERNAL")
        raise OpsError(code, f"api_server {e.code}: {e.reason}")
    except Exception as e:
        raise OpsError("INTERNAL", f"调用 api_server 失败: {e}")


def _json(method: str, path: str, body: dict | None = None) -> dict:
    """同 _request，但把响应体解析为 dict。"""
    raw = _request(method, path, body)
    try:
        return json.loads(raw)
    except Exception as e:
        raise OpsError("INTERNAL", f"api_server 响应非 JSON: {e}")


def list_sessions(source: str = "", limit: int = 50, offset: int = 0) -> list:
    """列出会话；source 非空时按渠道来源过滤。返回 api_server 的 data 数组。"""
    q = {"limit": str(limit), "offset": str(offset)}
    if source:
        q["source"] = source
    out = _json("GET", "/api/sessions?" + urllib.parse.urlencode(q))
    return out.get("data", [])


def session_messages(session_id: str) -> list:
    """读某会话的历史消息数组。"""
    sid = urllib.parse.quote(session_id, safe="")
    out = _json("GET", f"/api/sessions/{sid}/messages")
    return out.get("data", out) if isinstance(out, dict) else out


def create_session(body: dict) -> dict:
    """新建会话；body 透传（source/title 等），返回新建会话对象。"""
    return _json("POST", "/api/sessions", body)


def delete_session(session_id: str) -> None:
    """删除会话。"""
    sid = urllib.parse.quote(session_id, safe="")
    _request("DELETE", f"/api/sessions/{sid}")


def chat(session_id: str, body: dict) -> dict:
    """单轮续聊（非流式），body 含 message（文字/图片 parts）。返回 assistant 回复对象。"""
    sid = urllib.parse.quote(session_id, safe="")
    return _json("POST", f"/api/sessions/{sid}/chat", body)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=/usr/local/lib python -m pytest tests/test_conversation.py -v`
Expected: PASS（4 passed）。本地缺 yaml/urllib mock 无碍——测试已 mock urlopen 与 _api_server_key。

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/conversation.py runtime/hermes/hermes-v2026.6.5/tests/test_conversation.py
git commit -m "feat(ocops): 新增 conversation 转发模块对接 hermes api_server"
```

### Task 3: oc-ops server.py —— 注册会话路由 + 流式转发

**Files:**
- Modify: `runtime/hermes/hermes-v2026.6.5/ocops/server.py`
- Test: `runtime/hermes/hermes-v2026.6.5/tests/test_server_conversation.py`

- [ ] **Step 1: 写失败测试（用 Starlette TestClient，mock conversation 模块）**

`tests/test_server_conversation.py`：
```python
# 覆盖 oc-ops server 的会话路由：鉴权透传、list/messages/create/delete/chat 落到 conversation 模块、OpsError→HTTP 状态码。
from unittest import mock
from starlette.testclient import TestClient

import ocops.server as server
from ocops.errors import OpsError

# 测试用 token，绕过 AuthMiddleware（与既有 kanban server 测试同手法）。
_H = {"Authorization": "Bearer test-token"}


def _client(monkeypatch):
    monkeypatch.setenv("OC_OPS_TOKEN", "test-token")
    return TestClient(server.app)


# GET /oc/conversations → 调 conversation.list_sessions，透传 source/limit。
def test_list_route(monkeypatch):
    with mock.patch("ocops.server.conversation.list_sessions", return_value=[{"id": "s1"}]) as m:
        r = _client(monkeypatch).get("/oc/conversations?source=weixin&limit=10", headers=_H)
    assert r.status_code == 200 and r.json() == [{"id": "s1"}]
    assert m.call_args.kwargs.get("source") == "weixin"


# GET /oc/conversations/{sid}/messages，NOT_FOUND → 404。
def test_messages_notfound(monkeypatch):
    with mock.patch("ocops.server.conversation.session_messages", side_effect=OpsError("NOT_FOUND", "x")):
        r = _client(monkeypatch).get("/oc/conversations/nope/messages", headers=_H)
    assert r.status_code == 404 and r.json()["code"] == "NOT_FOUND"


# POST /oc/conversations/{sid}/chat 透传 body 给 conversation.chat。
def test_chat_route(monkeypatch):
    with mock.patch("ocops.server.conversation.chat", return_value={"message": {"content": "ok"}}) as m:
        r = _client(monkeypatch).post("/oc/conversations/s1/chat", headers=_H, json={"message": "hi"})
    assert r.status_code == 200 and r.json()["message"]["content"] == "ok"
    assert m.call_args[0][0] == "s1"
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=/usr/local/lib python -m pytest tests/test_server_conversation.py -v`
Expected: FAIL（404 路由不存在）。

- [ ] **Step 3: 写实现**

在 `ocops/server.py` 顶部 import 增加 `conversation`：
```python
from ocops import channel, conversation, cron, doctor, info, kanban, skills
```

在 cron/kanban handler 区块之后，`routes = [...]` 之前，新增 handler：
```python
# ---------------------------------------------------------------------------
# 会话（conversation）端点：转发到同 pod hermes api_server /api/sessions/*。
# manager 仅做带 per-app token 的透传，会话数据不在 oc-ops 落地。
# ---------------------------------------------------------------------------

async def conversation_list(request):
    """GET /oc/conversations?source=&limit=&offset= —— 列实例下会话。"""
    try:
        q = request.query_params
        data = conversation.list_sessions(
            source=q.get("source", ""),
            limit=int(q.get("limit", "50") or "50"),
            offset=int(q.get("offset", "0") or "0"),
        )
        return _ok(data)
    except OpsError as e:
        return _err(e)


async def conversation_messages(request):
    """GET /oc/conversations/{sid}/messages —— 读会话历史。"""
    try:
        return _ok(conversation.session_messages(request.path_params["sid"]))
    except OpsError as e:
        return _err(e)


async def conversation_create(request):
    """POST /oc/conversations —— 新建会话，body 透传（source/title）。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    try:
        return _ok(conversation.create_session(body), status=201)
    except OpsError as e:
        return _err(e)


async def conversation_delete(request):
    """DELETE /oc/conversations/{sid} —— 删除会话。"""
    try:
        conversation.delete_session(request.path_params["sid"])
        return Response(status_code=204)
    except OpsError as e:
        return _err(e)


async def conversation_chat(request):
    """POST /oc/conversations/{sid}/chat —— 单轮续聊，body 含 message。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    try:
        return _ok(conversation.chat(request.path_params["sid"], body))
    except OpsError as e:
        return _err(e)
```

在 `routes = [` 列表末尾（skills 路由之后）追加：
```python
    Route("/oc/conversations", conversation_list, methods=["GET"]),
    Route("/oc/conversations", conversation_create, methods=["POST"]),
    Route("/oc/conversations/{sid}/messages", conversation_messages, methods=["GET"]),
    Route("/oc/conversations/{sid}/chat", conversation_chat, methods=["POST"]),
    Route("/oc/conversations/{sid}", conversation_delete, methods=["DELETE"]),
```

> 本 Task 先打通非流式 `/chat`（单轮同步回合）作为基础与测试/降级路径；**流式 `/chat/stream` 在 Task 12 全链路实现**，是前端主路径（需求方锁定 v1 必须流式）。

- [ ] **Step 4: 跑测试确认通过 + 镜像构建期自检**

Run: `cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=/usr/local/lib python -m pytest tests/test_server_conversation.py tests/test_conversation.py -v`
Expected: PASS。
（镜像级回归：`make build-hermes-runtime` 的构建期 `pytest tests/` 会一并跑到。）

- [ ] **Step 5: 提交**

```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/server.py runtime/hermes/hermes-v2026.6.5/tests/test_server_conversation.py
git commit -m "feat(ocops): server 注册会话路由转发 api_server"
```

---

## Phase 2：manager ocops 客户端（Go）

### Task 4: ocops client 类型 + 方法

**Files:**
- Create: `internal/integrations/ocops/types_conversation.go`
- Create: `internal/integrations/ocops/client_conversation.go`
- Test: `internal/integrations/ocops/client_conversation_test.go`

> DTO 字段名以 **Task 1 Step 4 抄回的真实 JSON** 为准。下方按 `api_server` 源码观测的字段（session：id/source/title/model/created_at/last_active；message：role/content；chat：session_id/message{role,content}/usage）编写，执行时若 Spike 抄回字段不同，按实际调整 json tag。

- [ ] **Step 1: 写失败测试**

`client_conversation_test.go`（参照 `client_channel_test.go` 用 httptest）：
```go
package ocops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ListSessions：GET /oc/conversations 带 source query，解析为 []ConversationSession。
func TestListSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oc/conversations", r.URL.Path)
		assert.Equal(t, "weixin", r.URL.Query().Get("source")) // source 透传
		assert.Equal(t, "Bearer tk", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`[{"id":"s1","source":"weixin","title":"张三"}]`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	out, err := c.ListSessions(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "weixin", 50, 0)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "s1", out[0].ID)        // id 解析
	assert.Equal(t, "weixin", out[0].Source) // source 解析
}

// SessionChat：POST /oc/conversations/{sid}/chat 透传 message，解析回复。
func TestSessionChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/conversations/s1/chat", r.URL.Path)
		_, _ = w.Write([]byte(`{"session_id":"s1","message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	out, err := c.SessionChat(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"},
		"s1", ConversationChatReq{Message: "hi"})
	require.NoError(t, err)
	assert.Equal(t, "ok", out.Message.Content) // assistant content 解析
}

// 404 → ErrNotFound（沿用 statusToErr 映射）。
func TestSessionMessagesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"x"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	_, err := c.SessionMessages(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "nope")
	require.ErrorIs(t, err, ErrNotFound)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/integrations/ocops/ -run 'TestListSessions|TestSessionChat|TestSessionMessagesNotFound' -v`
Expected: FAIL（未定义 ListSessions 等）。

- [ ] **Step 3: 写类型 + 方法**

`types_conversation.go`：
```go
// types_conversation.go — ocops 会话端点的 DTO。
// 字段镜像 hermes api_server /api/sessions 响应（经 oc-ops 透传）。
// 字段名以 Spike 抄回的真实 JSON 为准，此处为观测默认值。
package ocops

// ConversationSession 是一条会话（跨渠道；source 标识来源渠道）。
// 字段名对齐 api_server `_session_response` safe_keys（已读源码确认）。
type ConversationSession struct {
	ID           string `json:"id"`
	Source       string `json:"source"`                  // 渠道来源：weixin / web / api_server 等
	UserID       string `json:"user_id,omitempty"`       // 会话归属用户标识（渠道侧）
	Title        string `json:"title,omitempty"`         // 会话标题（可空）
	Model        string `json:"model,omitempty"`         // 绑定模型（可空）
	StartedAt    string `json:"started_at,omitempty"`    // 会话开始时间
	LastActive   string `json:"last_active,omitempty"`   // 最近活跃时间（列表按此排序）
	MessageCount int    `json:"message_count,omitempty"` // 消息数（列表展示）
	Preview      string `json:"preview,omitempty"`       // 末条消息预览（列表展示）
}

// ConversationMessage 是一条历史消息。content 可能是字符串或多模态 parts，
// 用 any 容纳文字/图片两种形态，由前端按 type 渲染。
// 字段名对齐 api_server `_message_response` safe_keys（已读源码确认）。
type ConversationMessage struct {
	Role          string `json:"role"`                      // user / assistant
	Content       any    `json:"content"`                   // 字符串或 [{type,text|image_url}]
	Timestamp     string `json:"timestamp,omitempty"`       // 消息时间戳
	ToolCalls     any    `json:"tool_calls,omitempty"`      // 工具调用（透传，前端可忽略）
	FinishReason  string `json:"finish_reason,omitempty"`
}

// ConversationChatReq 是续聊请求体。Message 为文字字符串；图片走 multimodal parts
// 时由前端构造 ContentParts 并序列化进 Message（api_server 接受 message 为
// 字符串或 parts 数组），v1 文字优先。
type ConversationChatReq struct {
	Message any `json:"message"` // 文字字符串或多模态 parts 数组
}

// ConversationChatResult 是续聊回复。
type ConversationChatResult struct {
	SessionID string              `json:"session_id"`
	Message   ConversationMessage `json:"message"`
	Usage     any                 `json:"usage,omitempty"`
}

// ConversationCreateReq 是新建会话请求体。
type ConversationCreateReq struct {
	Source string `json:"source,omitempty"` // 默认 web
	Title  string `json:"title,omitempty"`
}
```

`client_conversation.go`：
```go
// client_conversation.go — ocops 会话端点的类型化客户端，转发 oc-ops /oc/conversations/*。
package ocops

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// ListSessions 列出实例下会话；source 非空时按渠道过滤。
// GET /oc/conversations?source=&limit=&offset=
func (c *Client) ListSessions(ctx context.Context, ep Endpoint, source string, limit, offset int) ([]ConversationSession, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	if source != "" {
		q.Set("source", source)
	}
	var out []ConversationSession
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/conversations?"+q.Encode(), nil, &out)
	return out, err
}

// SessionMessages 读某会话历史消息。
// GET /oc/conversations/{sid}/messages
func (c *Client) SessionMessages(ctx context.Context, ep Endpoint, sid string) ([]ConversationMessage, error) {
	var out []ConversationMessage
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/conversations/"+url.PathEscape(sid)+"/messages", nil, &out)
	return out, err
}

// CreateSession 新建会话（默认 web 来源），返回新建会话对象。
// POST /oc/conversations
func (c *Client) CreateSession(ctx context.Context, ep Endpoint, req ConversationCreateReq) (ConversationSession, error) {
	var out ConversationSession
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/conversations", req, &out)
	return out, err
}

// DeleteSession 删除会话。
// DELETE /oc/conversations/{sid}
func (c *Client) DeleteSession(ctx context.Context, ep Endpoint, sid string) error {
	return c.DoJSON(ctx, ep, http.MethodDelete, "/oc/conversations/"+url.PathEscape(sid), nil, nil)
}

// SessionChat 续聊一轮，返回 assistant 回复。
// POST /oc/conversations/{sid}/chat
func (c *Client) SessionChat(ctx context.Context, ep Endpoint, sid string, req ConversationChatReq) (ConversationChatResult, error) {
	var out ConversationChatResult
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/conversations/"+url.PathEscape(sid)+"/chat", req, &out)
	return out, err
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/integrations/ocops/ -run 'TestListSessions|TestSessionChat|TestSessionMessagesNotFound' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/integrations/ocops/types_conversation.go internal/integrations/ocops/client_conversation.go internal/integrations/ocops/client_conversation_test.go
git commit -m "feat(ocops-client): 新增会话端点类型化客户端"
```

---

## Phase 3：manager service 层 + auth 谓词 + 错误（Go）

### Task 5: auth 谓词 + service 错误哨兵

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/service/errors.go`（错误哨兵定义文件，按现有 `ErrKanban*` 所在文件）
- Test: `internal/auth/authorizer_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/auth/authorizer_test.go` 追加（参照既有 `CanViewAppKanban` 测试）：
```go
// org_member 可查看自己实例的会话；非本人 org_member 不可。
func TestCanViewAppConversations(t *testing.T) {
	owner := Principal{Role: "org_member", OrgID: "o1", UserID: "u1"}
	assert.True(t, CanViewAppConversations(owner, "o1", "u1"))  // 实例主可看
	other := Principal{Role: "org_member", OrgID: "o1", UserID: "u2"}
	assert.False(t, CanViewAppConversations(other, "o1", "u1")) // 同组他人不可看
	admin := Principal{Role: "platform_admin"}
	assert.True(t, CanViewAppConversations(admin, "o1", "u1"))  // 平台管理员可看
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/auth/ -run TestCanViewAppConversations -v`
Expected: FAIL（未定义）。

- [ ] **Step 3: 写实现**

在 `internal/auth/authorizer.go` 追加（紧随 `CanManageAppKanban` 之后，复用 `CanViewApp`/`CanManageApp` 委托，与 kanban 谓词同结构）：
```go
// CanViewAppConversations 判断 principal 能否查看实例会话。
// 语义与 CanViewApp 一致：平台管理员、实例所属组织的 org_admin、实例 owner 本人可看。
func CanViewAppConversations(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanManageAppConversations 判断 principal 能否在实例会话里发消息 / 新建 / 删除会话。
// 当前与查看权限等价（委托 CanViewApp），保留独立函数以便将来收紧写权限。
func CanManageAppConversations(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}
```

在 service 错误文件（`ErrKanban*` 同文件）追加哨兵：
```go
// 会话功能错误哨兵（语义对齐 kanban 同名错误，便于 handler 复用映射规则）。
var (
	ErrConversationForbidden        = errors.New("conversation: forbidden")
	ErrConversationNotSupported     = errors.New("conversation: not supported")
	ErrConversationRuntimeUnavailable = errors.New("conversation: runtime unavailable")
	ErrConversationBadRequest       = errors.New("conversation: bad request")
	ErrConversationCLI              = errors.New("conversation: upstream failed")
	ErrConversationOutputInvalid    = errors.New("conversation: invalid output")
)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/auth/ -run TestCanViewAppConversations -v && go build ./...`
Expected: PASS + 编译通过。

- [ ] **Step 5: 提交**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go internal/service/errors.go
git commit -m "feat(auth): 新增会话查看/管理权限谓词与 service 错误哨兵"
```

### Task 6: HermesConversationService

**Files:**
- Create: `internal/service/hermes_conversation.go`
- Modify: `internal/service/ocops.go`（新增 `conversationOps` 窄接口 + 编译期断言 + `mapOcOpsConversationErr`）
- Test: `internal/service/hermes_conversation_test.go`

- [ ] **Step 1: 写失败测试**

`hermes_conversation_test.go`（参照 `hermes_kanban_test.go` 的 `fakeOcOpsResolver`）：
```go
package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// fakeConversationOps 是 conversationOps 的假实现，记录入参并返回预设值。
type fakeConversationOps struct {
	sessions []ocops.ConversationSession
	chatOut  ocops.ConversationChatResult
	gotSID   string
}

func (f *fakeConversationOps) ListSessions(_ context.Context, _ ocops.Endpoint, _ string, _, _ int) ([]ocops.ConversationSession, error) {
	return f.sessions, nil
}
func (f *fakeConversationOps) SessionMessages(_ context.Context, _ ocops.Endpoint, sid string) ([]ocops.ConversationMessage, error) {
	f.gotSID = sid
	return nil, nil
}
func (f *fakeConversationOps) CreateSession(_ context.Context, _ ocops.Endpoint, _ ocops.ConversationCreateReq) (ocops.ConversationSession, error) {
	return ocops.ConversationSession{ID: "new"}, nil
}
func (f *fakeConversationOps) DeleteSession(_ context.Context, _ ocops.Endpoint, _ string) error { return nil }
func (f *fakeConversationOps) SessionChat(_ context.Context, _ ocops.Endpoint, sid string, _ ocops.ConversationChatReq) (ocops.ConversationChatResult, error) {
	f.gotSID = sid
	return f.chatOut, nil
}

// 有权用户可列会话，透传 ops 返回。
func TestConversationServiceList(t *testing.T) {
	ops := &fakeConversationOps{sessions: []ocops.ConversationSession{{ID: "s1"}}}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	p := auth.Principal{Role: "org_member", OrgID: "o1", UserID: "u1"} // 实例主
	out, err := svc.ListSessions(context.Background(), p, "app-1", "", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, "s1", out[0].ID)
}

// 无权用户被拒。
func TestConversationServiceForbidden(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	p := auth.Principal{Role: "org_member", OrgID: "o1", UserID: "u2"} // 非本人
	_, err := svc.ListSessions(context.Background(), p, "app-1", "", 50, 0)
	require.ErrorIs(t, err, ErrConversationForbidden)
}

// 续聊空消息被校验拒绝。
func TestConversationServiceChatEmpty(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	p := auth.Principal{Role: "org_member", OrgID: "o1", UserID: "u1"}
	_, err := svc.Chat(context.Background(), p, "app-1", "s1", "   ")
	require.ErrorIs(t, err, ErrConversationBadRequest)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestConversationService' -v`
Expected: FAIL（未定义）。

- [ ] **Step 3: 写实现**

在 `internal/service/ocops.go` 的窄接口区追加 `conversationOps` + 断言 + 错误映射：
```go
// conversationOps 抽象 oc-ops 的 5 个会话方法，供 HermesConversationService 注入假实现。
type conversationOps interface {
	ListSessions(ctx context.Context, ep ocops.Endpoint, source string, limit, offset int) ([]ocops.ConversationSession, error)
	SessionMessages(ctx context.Context, ep ocops.Endpoint, sid string) ([]ocops.ConversationMessage, error)
	CreateSession(ctx context.Context, ep ocops.Endpoint, req ocops.ConversationCreateReq) (ocops.ConversationSession, error)
	DeleteSession(ctx context.Context, ep ocops.Endpoint, sid string) error
	SessionChat(ctx context.Context, ep ocops.Endpoint, sid string, req ocops.ConversationChatReq) (ocops.ConversationChatResult, error)
}
```
在已有 `var ( _ cronOps ... )` 断言块追加 `_ conversationOps = (*ocops.Client)(nil)`。
在 `mapOcOpsKanbanErr` 之后追加（同结构）：
```go
// mapOcOpsConversationErr 把 ocops 哨兵错误翻成 service 会话哨兵错误。
func mapOcOpsConversationErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ocops.ErrBadRequest):
		return ErrConversationBadRequest
	case errors.Is(err, ocops.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ocops.ErrUnsupported):
		return ErrConversationNotSupported
	case errors.Is(err, ocops.ErrOutputInvalid):
		return ErrConversationOutputInvalid
	default:
		return ErrConversationCLI
	}
}
```

`hermes_conversation.go`：
```go
// Package service —— hermes_conversation.go 实现实例会话能力。
// manager 不持有会话数据，所有读写经 oc-ops HTTP 转发到 app 实例内 hermes
// api_server，manager 仅做权限判断与最小输入校验。
package service

import (
	"context"
	"fmt"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// sessionIDRe 复用 kanban taskIDRe 风格：会话 id 白名单（防路径越界）。
// hermes session id 形如 weixin:xxx / api_xxx，放宽到常见可见字符但禁控制字符。
// 这里直接限制长度与禁止斜杠/空白，避免越界与注入。
func validateSessionID(sid string) error {
	sid = strings.TrimSpace(sid)
	if sid == "" || len(sid) > 256 || strings.ContainsAny(sid, " \t\r\n/") {
		return fmt.Errorf("%w: 非法 session id", ErrConversationBadRequest)
	}
	return nil
}

// HermesConversationService 暴露实例会话的读写能力。
type HermesConversationService struct {
	ops      conversationOps
	resolver OcOpsResolver
}

// NewHermesConversationService 构造 service。
func NewHermesConversationService(ops conversationOps, resolver OcOpsResolver) *HermesConversationService {
	return &HermesConversationService{ops: ops, resolver: resolver}
}

// resolve 解析 appID、校验读权限、确保实例可调用 oc-ops。
func (s *HermesConversationService) resolve(ctx context.Context, p auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanViewAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrConversationForbidden
	}
	if !loc.Supported {
		return OcOpsAppLocation{}, ErrConversationNotSupported
	}
	if strings.TrimSpace(loc.Endpoint.BaseURL) == "" {
		return OcOpsAppLocation{}, ErrConversationRuntimeUnavailable
	}
	return loc, nil
}

// resolveManage 在 resolve 基础上加写权限校验。
func (s *HermesConversationService) resolveManage(ctx context.Context, p auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolve(ctx, p, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanManageAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrConversationForbidden
	}
	return loc, nil
}

// ListSessions 列出实例下会话；source 非空时按渠道过滤。
func (s *HermesConversationService) ListSessions(ctx context.Context, p auth.Principal, appID, source string, limit, offset int) ([]ocops.ConversationSession, error) {
	loc, err := s.resolve(ctx, p, appID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50 // 兜底默认，避免越界透传
	}
	if offset < 0 {
		offset = 0
	}
	out, err := s.ops.ListSessions(ctx, loc.Endpoint, source, limit, offset)
	if err != nil {
		return nil, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// Messages 读会话历史。
func (s *HermesConversationService) Messages(ctx context.Context, p auth.Principal, appID, sid string) ([]ocops.ConversationMessage, error) {
	loc, err := s.resolve(ctx, p, appID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionID(sid); err != nil {
		return nil, err
	}
	out, err := s.ops.SessionMessages(ctx, loc.Endpoint, sid)
	if err != nil {
		return nil, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// CreateSession 新建一条 web 会话。
func (s *HermesConversationService) CreateSession(ctx context.Context, p auth.Principal, appID, title string) (ocops.ConversationSession, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationSession{}, err
	}
	out, err := s.ops.CreateSession(ctx, loc.Endpoint, ocops.ConversationCreateReq{Source: "web", Title: strings.TrimSpace(title)})
	if err != nil {
		return ocops.ConversationSession{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// DeleteSession 删除会话。
func (s *HermesConversationService) DeleteSession(ctx context.Context, p auth.Principal, appID, sid string) error {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return err
	}
	if err := validateSessionID(sid); err != nil {
		return err
	}
	if err := s.ops.DeleteSession(ctx, loc.Endpoint, sid); err != nil {
		return mapOcOpsConversationErr(err)
	}
	return nil
}

// Chat 续聊一轮（文字）。message 为空白时拒绝。图片续聊在 handler 层构造 parts 后走同一 ops。
func (s *HermesConversationService) Chat(ctx context.Context, p auth.Principal, appID, sid, message string) (ocops.ConversationChatResult, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationChatResult{}, err
	}
	if err := validateSessionID(sid); err != nil {
		return ocops.ConversationChatResult{}, err
	}
	if strings.TrimSpace(message) == "" {
		return ocops.ConversationChatResult{}, fmt.Errorf("%w: 消息内容不能为空", ErrConversationBadRequest)
	}
	out, err := s.ops.SessionChat(ctx, loc.Endpoint, sid, ocops.ConversationChatReq{Message: message})
	if err != nil {
		return ocops.ConversationChatResult{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -run 'TestConversationService' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/service/hermes_conversation.go internal/service/ocops.go internal/service/hermes_conversation_test.go
git commit -m "feat(service): 新增实例会话 service（列表/历史/续聊/新建/删除）"
```

---

## Phase 4：manager handler + 路由 + 装配（Go）

### Task 7: handler + DTO + 错误映射规则

**Files:**
- Create: `internal/api/handlers/hermes_conversation.go`
- Modify: `internal/api/handlers/dto.go`（新增 `ConversationChatRequest`/`CreateConversationRequest`）
- Modify: `internal/api/handlers/request_errors.go`（追加 conversation 映射规则）
- Test: `internal/api/handlers/hermes_conversation_test.go`

- [ ] **Step 1: 写失败测试**

`hermes_conversation_test.go`（参照 `channels_test.go` 用 stub service + httptest gin）：
```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/service"
)

// stubConversationService 实现 handler 依赖的窄接口。
type stubConversationService struct {
	sessions []ocops.ConversationSession
	err      error
	gotMsg   string
}

func (s *stubConversationService) ListSessions(_ context.Context, _ auth.Principal, _, _ string, _, _ int) ([]ocops.ConversationSession, error) {
	return s.sessions, s.err
}
func (s *stubConversationService) Messages(_ context.Context, _ auth.Principal, _, _ string) ([]ocops.ConversationMessage, error) {
	return nil, s.err
}
func (s *stubConversationService) CreateSession(_ context.Context, _ auth.Principal, _, _ string) (ocops.ConversationSession, error) {
	return ocops.ConversationSession{ID: "new"}, s.err
}
func (s *stubConversationService) DeleteSession(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.err
}
func (s *stubConversationService) Chat(_ context.Context, _ auth.Principal, _, _, msg string) (ocops.ConversationChatResult, error) {
	s.gotMsg = msg
	return ocops.ConversationChatResult{Message: ocops.ConversationMessage{Role: "assistant", Content: "ok"}}, s.err
}

func newConvTestRouter(svc conversationHandlerService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(principalCtxKey, auth.Principal{Role: "org_member", OrgID: "o1", UserID: "u1"}) })
	RegisterHermesConversationRoutes(r, NewHermesConversationHandler(svc))
	return r
}

// GET 列会话返回 200 + sessions 包。
func TestHandlerListConversations(t *testing.T) {
	svc := &stubConversationService{sessions: []ocops.ConversationSession{{ID: "s1"}}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/conversations", nil)
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "s1")
}

// 续聊透传 message，返回 assistant 回复。
func TestHandlerChat(t *testing.T) {
	svc := &stubConversationService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/conversations/s1/chat",
		strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hi", svc.gotMsg)
}

// 无权（service 返回 ErrConversationForbidden）映射 403。
func TestHandlerForbidden(t *testing.T) {
	svc := &stubConversationService{err: service.ErrConversationForbidden}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/conversations", nil)
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}
```

> 注：`principalCtxKey` / `principalFromCtx` 用 handlers 包既有符号（见 `principal.go`）。若测试注入 principal 的方式与既有 handler 测试不同，按 `channels_test.go` / `hermes_kanban_test.go` 既有写法对齐。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/handlers/ -run 'TestHandlerListConversations|TestHandlerChat|TestHandlerForbidden' -v`
Expected: FAIL（未定义 handler/路由）。

- [ ] **Step 3: 写实现**

`dto.go` 追加：
```go
// CreateConversationRequest 是 POST /hermes/conversations 的请求体（新建 web 会话）。
type CreateConversationRequest struct {
	Title string `json:"title"` // 可选会话标题
}

// ConversationChatRequest 是续聊请求体。v1 仅文字 Message；图片在后续增强中以 parts 承载。
type ConversationChatRequest struct {
	Message string `json:"message" binding:"required"` // 文字内容，必填
}
```

`request_errors.go` 的 `mappedServiceErrorRules` 追加（紧随 kanban 节）：
```go
	{target: service.ErrConversationForbidden, statusCode: http.StatusForbidden, code: "FORBIDDEN", message: "无权访问会话"},
	{target: service.ErrConversationRuntimeUnavailable, statusCode: http.StatusServiceUnavailable, code: "CONVERSATION_RUNTIME_UNAVAILABLE", message: "实例运行时尚未就绪"},
	{target: service.ErrConversationNotSupported, statusCode: http.StatusServiceUnavailable, code: "CONVERSATION_NOT_SUPPORTED", message: "当前实例不支持会话"},
	validationErrorRule(service.ErrConversationBadRequest, http.StatusBadRequest, "CONVERSATION_BAD_REQUEST"),
	{target: service.ErrConversationCLI, statusCode: http.StatusBadGateway, code: "CONVERSATION_UPSTREAM_FAILED", message: "会话上游暂不可用"},
	{target: service.ErrConversationOutputInvalid, statusCode: http.StatusBadGateway, code: "CONVERSATION_OUTPUT_INVALID", message: "会话上游返回异常"},
```

`hermes_conversation.go`：
```go
// Package handlers —— hermes_conversation.go 暴露实例会话 HTTP 端点：
// 列会话 / 读历史 / 续聊 / 新建 / 删除。链路转发到 oc-ops 再到 hermes api_server。
package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// conversationHandlerService 抽象 handler 依赖的会话业务能力，便于单测注入 stub。
type conversationHandlerService interface {
	ListSessions(ctx context.Context, p auth.Principal, appID, source string, limit, offset int) ([]ocops.ConversationSession, error)
	Messages(ctx context.Context, p auth.Principal, appID, sid string) ([]ocops.ConversationMessage, error)
	CreateSession(ctx context.Context, p auth.Principal, appID, title string) (ocops.ConversationSession, error)
	DeleteSession(ctx context.Context, p auth.Principal, appID, sid string) error
	Chat(ctx context.Context, p auth.Principal, appID, sid, message string) (ocops.ConversationChatResult, error)
}

// HermesConversationHandler 处理 /api/v1/apps/:appId/hermes/conversations/* 路由。
type HermesConversationHandler struct {
	service conversationHandlerService
}

// NewHermesConversationHandler 构造 handler。
func NewHermesConversationHandler(svc conversationHandlerService) *HermesConversationHandler {
	return &HermesConversationHandler{service: svc}
}

// RegisterHermesConversationRoutes 注册实例会话路由。
func RegisterHermesConversationRoutes(router gin.IRouter, h *HermesConversationHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/conversations")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:sid/messages", h.Messages)
	g.POST("/:sid/chat", h.Chat)
	g.DELETE("/:sid", h.Delete)
}

// writeConversationError 把 service 哨兵错误映射为 HTTP 响应。
func writeConversationError(c *gin.Context, err error) {
	writeMappedServiceError(c, err, http.StatusInternalServerError, "会话服务暂不可用")
}

// List GET /api/v1/apps/{appId}/hermes/conversations
//
// @Summary      列出实例会话
// @Tags         hermes-conversation
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string  true   "应用 ID"
// @Param        source  query  string  false  "渠道来源过滤，如 weixin"
// @Success      200     {object}  map[string][]ocops.ConversationSession
// @Failure      403     {object}  ErrorResponse
// @Failure      503     {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations [get]
func (h *HermesConversationHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	out, err := h.service.ListSessions(c.Request.Context(), principalFromCtx(c),
		c.Param("appId"), c.Query("source"), limit, offset)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": out})
}

// Messages GET /api/v1/apps/{appId}/hermes/conversations/{sid}/messages
//
// @Summary      读会话历史
// @Tags         hermes-conversation
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string  true  "应用 ID"
// @Param        sid    path  string  true  "会话 ID"
// @Success      200    {object}  map[string][]ocops.ConversationMessage
// @Router       /apps/{appId}/hermes/conversations/{sid}/messages [get]
func (h *HermesConversationHandler) Messages(c *gin.Context) {
	out, err := h.service.Messages(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"))
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": out})
}

// Create POST /api/v1/apps/{appId}/hermes/conversations
//
// @Summary      新建 web 会话
// @Tags         hermes-conversation
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string                     true  "应用 ID"
// @Param        body   body  CreateConversationRequest  false "新建会话请求"
// @Success      201    {object}  map[string]ocops.ConversationSession
// @Router       /apps/{appId}/hermes/conversations [post]
func (h *HermesConversationHandler) Create(c *gin.Context) {
	var req CreateConversationRequest
	_ = bindOptionalJSON(c, &req) // title 可选，空 body 允许
	out, err := h.service.CreateSession(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Title)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session": out})
}

// Delete DELETE /api/v1/apps/{appId}/hermes/conversations/{sid}
//
// @Summary      删除会话
// @Tags         hermes-conversation
// @Security     BearerAuth
// @Param        appId  path  string  true  "应用 ID"
// @Param        sid    path  string  true  "会话 ID"
// @Success      204    "删除成功"
// @Router       /apps/{appId}/hermes/conversations/{sid} [delete]
func (h *HermesConversationHandler) Delete(c *gin.Context) {
	if err := h.service.DeleteSession(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid")); err != nil {
		writeConversationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Chat POST /api/v1/apps/{appId}/hermes/conversations/{sid}/chat
//
// @Summary      续聊一轮
// @Tags         hermes-conversation
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string                   true  "应用 ID"
// @Param        sid    path  string                   true  "会话 ID"
// @Param        body   body  ConversationChatRequest  true  "续聊请求"
// @Success      200    {object}  map[string]ocops.ConversationChatResult
// @Failure      400    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid}/chat [post]
func (h *HermesConversationHandler) Chat(c *gin.Context) {
	var req ConversationChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: ErrorBody{Code: "CONVERSATION_BAD_REQUEST", Message: "消息内容不能为空"}})
		return
	}
	out, err := h.service.Chat(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"), req.Message)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": out})
}
```

> `ErrorResponse`/`ErrorBody` 字段名以 `request_errors.go` / `dto.go` 既有定义为准，执行时对齐既有写法（参照 channels handler 的错误响应构造）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/handlers/ -run 'TestHandlerListConversations|TestHandlerChat|TestHandlerForbidden' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/api/handlers/hermes_conversation.go internal/api/handlers/dto.go internal/api/handlers/request_errors.go internal/api/handlers/hermes_conversation_test.go
git commit -m "feat(api): 新增实例会话 handler 与错误映射"
```

### Task 8: 装配 —— router + main 注入

**Files:**
- Modify: `internal/api/router.go`（`RouterDeps` 新字段 + 注册块）
- Modify: `cmd/server/main.go`（构造 service + 赋值 dep）

- [ ] **Step 1: 接线**

`router.go` 的 `RouterDeps` 结构追加字段（紧随 `HermesKanbanService`）：
```go
	// HermesConversationService 提供实例会话能力；nil 时不注册会话路由。
	HermesConversationService *service.HermesConversationService
```
注册块追加（紧随 kanban 注册之后）：
```go
	if dep.HermesConversationService != nil {
		handlers.RegisterHermesConversationRoutes(user, handlers.NewHermesConversationHandler(dep.HermesConversationService))
	}
```

`cmd/server/main.go`（在 `hermesKanbanService := ...` 旁，复用 `ocopsClient` + `ocopsResolver`）：
```go
	// 实例会话 service：复用 oc-ops 客户端与坐标解析器（与 kanban/cron 同源）。
	hermesConversationService := service.NewHermesConversationService(ocopsClient, ocopsResolver)
```
并在组装 `RouterDeps{...}` 处加：
```go
		HermesConversationService: hermesConversationService,
```

- [ ] **Step 2: 编译 + 全量后端测试**

Run: `go build ./... && go test ./internal/... -count=1`
Expected: 全绿。

- [ ] **Step 3: 提交**

```bash
git add internal/api/router.go cmd/server/main.go
git commit -m "feat(api): 装配实例会话 service 与路由注册"
```

### Task 9: openapi + web 类型同步

**Files:** 自动生成 `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 生成**

Run: `make openapi-gen && make web-types-gen`

- [ ] **Step 2: 校验工作区干净（除生成物）**

Run: `make openapi-check`
Expected: 跑完 git 工作区仅有 `openapi/openapi.yaml` 与 `generated.ts` 的预期变更。

- [ ] **Step 3: 提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步实例会话端点契约与前端类型"
```

---

## Phase 5：前端（Vue 3 + TS）

### Task 10: 前端 API 模块 + 会话 tab 组件

**Files:**
- Create: `web/src/api/conversations.ts`（封装 5 个端点调用，参照 `web/src/api/` 既有模块）
- Create: `web/src/pages/apps/AppConversationsTab.vue`
- Test: `web/src/pages/apps/AppConversationsTab.spec.ts`

- [ ] **Step 1: 写失败组件测试**

`AppConversationsTab.spec.ts`（参照 `AppKanbanTab.spec.ts`，mock api 模块）：
```ts
// 覆盖会话 tab：加载会话列表、选中会话拉历史、发送消息调 chat。
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AppConversationsTab from './AppConversationsTab.vue'
import * as api from '@/api/conversations'

vi.mock('@/api/conversations')

describe('AppConversationsTab', () => {
  beforeEach(() => {
    vi.mocked(api.listConversations).mockResolvedValue([{ id: 's1', source: 'weixin', title: '张三' }] as any)
    vi.mocked(api.listMessages).mockResolvedValue([{ role: 'user', content: 'hi' }] as any)
    vi.mocked(api.chat).mockResolvedValue({ message: { role: 'assistant', content: 'ok' } } as any)
  })

  // 挂载后加载并展示会话列表。
  it('loads sessions on mount', async () => {
    const w = mount(AppConversationsTab, { props: { appId: 'app-1' } })
    await flushPromises()
    expect(api.listConversations).toHaveBeenCalledWith('app-1', expect.anything())
    expect(w.text()).toContain('张三')
  })

  // 选中会话后拉取历史。
  it('loads messages when a session is selected', async () => {
    const w = mount(AppConversationsTab, { props: { appId: 'app-1' } })
    await flushPromises()
    await w.find('[data-test="session-s1"]').trigger('click')
    await flushPromises()
    expect(api.listMessages).toHaveBeenCalledWith('app-1', 's1')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/pages/apps/AppConversationsTab.spec.ts`
Expected: FAIL（组件/模块不存在）。

- [ ] **Step 3: 写 api 模块**

`web/src/api/conversations.ts`：
```ts
// 实例会话 API 封装：列会话 / 读历史 / 续聊 / 新建 / 删除。
// 经统一 http 客户端调用 manager /api/v1/apps/:appId/hermes/conversations/*。
import { http } from '@/api/http' // 以仓库既有 http 客户端为准（参照其它 api 模块的 import）

export interface ConversationSession { id: string; source: string; title?: string; last_active?: string }
export interface ConversationMessage { role: string; content: unknown; created_at?: string }
export interface ChatResult { session_id?: string; message: ConversationMessage }

const base = (appId: string) => `/api/v1/apps/${appId}/hermes/conversations`

export async function listConversations(appId: string, source = ''): Promise<ConversationSession[]> {
  const { data } = await http.get(base(appId), { params: source ? { source } : {} })
  return data.sessions ?? []
}
export async function listMessages(appId: string, sid: string): Promise<ConversationMessage[]> {
  const { data } = await http.get(`${base(appId)}/${encodeURIComponent(sid)}/messages`)
  return data.messages ?? []
}
export async function chat(appId: string, sid: string, message: string): Promise<ChatResult> {
  const { data } = await http.post(`${base(appId)}/${encodeURIComponent(sid)}/chat`, { message })
  return data.reply
}
export async function createConversation(appId: string, title = ''): Promise<ConversationSession> {
  const { data } = await http.post(base(appId), { title })
  return data.session
}
export async function deleteConversation(appId: string, sid: string): Promise<void> {
  await http.delete(`${base(appId)}/${encodeURIComponent(sid)}`)
}
```
> `http` 客户端 import 路径以仓库既有 api 模块为准（执行时对齐 `web/src/api/` 现有写法，可能是 `generated` client 或自封装 axios）。

- [ ] **Step 4: 写组件**

`AppConversationsTab.vue`（两栏：左会话列表 + 右消息区 + composer；所有文案走 i18n `t('apps.conversations.*')`）：
```vue
<template>
  <div class="conv-tab">
    <!-- 左：会话列表（渠道来源标签 + 标题），含新建按钮 -->
    <aside class="conv-list">
      <header>
        <button data-test="new-session" @click="onCreate">{{ t('apps.conversations.new') }}</button>
      </header>
      <ul>
        <li v-for="s in sessions" :key="s.id"
            :data-test="`session-${s.id}`"
            :class="{ active: s.id === currentId }"
            @click="selectSession(s.id)">
          <span class="src">{{ s.source }}</span>
          <span class="title">{{ s.title || s.id }}</span>
        </li>
      </ul>
    </aside>

    <!-- 右：消息历史 + 输入框 -->
    <section class="conv-main">
      <div class="messages">
        <div v-for="(m, i) in messages" :key="i" :class="['msg', m.role]">
          <ConversationMessageView :message="m" />
        </div>
      </div>
      <footer class="composer">
        <textarea v-model="draft" :placeholder="t('apps.conversations.placeholder')" />
        <button data-test="send" :disabled="sending || !draft.trim()" @click="onSend">
          {{ sending ? t('apps.conversations.sending') : t('apps.conversations.send') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<script setup lang="ts">
// 实例会话 tab：列会话 → 选中拉历史 → 续聊驱动 bot 回复。
// v1 文字优先；图片消息历史由 ConversationMessageView 按 content parts 渲染。
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import * as api from '@/api/conversations'
import ConversationMessageView from './ConversationMessageView.vue'

const props = defineProps<{ appId: string }>()
const { t } = useI18n()

const sessions = ref<api.ConversationSession[]>([])
const messages = ref<api.ConversationMessage[]>([])
const currentId = ref('')
const draft = ref('')
const sending = ref(false)

// 加载会话列表（不区分渠道，跨渠道统一展示）。
async function loadSessions() {
  sessions.value = await api.listConversations(props.appId)
}
// 选中会话并拉取其历史。
async function selectSession(sid: string) {
  currentId.value = sid
  messages.value = await api.listMessages(props.appId, sid)
}
// 新建 web 会话后刷新列表并选中。
async function onCreate() {
  const s = await api.createConversation(props.appId)
  await loadSessions()
  await selectSession(s.id)
}
// 续聊：发送文字 → 追加 assistant 回复 → 重新拉历史保证一致。
async function onSend() {
  if (!draft.value.trim() || !currentId.value) return
  sending.value = true
  try {
    await api.chat(props.appId, currentId.value, draft.value.trim())
    draft.value = ''
    await selectSession(currentId.value)
  } finally {
    sending.value = false
  }
}

onMounted(loadSessions)
</script>
```

`ConversationMessageView.vue`（按 content 渲染文字/图片）：
```vue
<template>
  <div class="message-view">
    <!-- content 为字符串：纯文字 -->
    <p v-if="typeof message.content === 'string'">{{ message.content }}</p>
    <!-- content 为 parts 数组：逐 part 渲染文字 / 图片 -->
    <template v-else-if="Array.isArray(message.content)">
      <template v-for="(p, i) in message.content" :key="i">
        <p v-if="p.type === 'text'">{{ p.text }}</p>
        <img v-else-if="p.type === 'image_url'" :src="imageUrl(p)" alt="" class="msg-image" />
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
// 单条消息渲染：兼容字符串 content 与多模态 parts（文字/图片）。
import type { ConversationMessage } from '@/api/conversations'
defineProps<{ message: ConversationMessage }>()
// 从 image_url part 取 URL（兼容 {image_url:{url}} 与 {image_url:'...'} 两种形态）。
function imageUrl(p: any): string {
  const v = p.image_url
  return typeof v === 'string' ? v : (v?.url ?? '')
}
</script>
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/pages/apps/AppConversationsTab.spec.ts`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/api/conversations.ts web/src/pages/apps/AppConversationsTab.vue web/src/pages/apps/ConversationMessageView.vue web/src/pages/apps/AppConversationsTab.spec.ts
git commit -m "feat(web): 新增实例会话 tab 组件与 API 模块"
```

### Task 11: 路由 + tab 注册 + org_member 左侧菜单 + i18n

**Files:**
- Modify: `web/src/app/router.ts`（apps detail 子路由加 `conversations`）
- Modify: `web/src/pages/apps/AppDetailPage.vue`（`allTabs` 加一项）
- Modify: `web/src/layouts/DashboardLayout.vue`（org_member 菜单加 `memberAppTabKey('conversations')`）
- Modify: i18n 文案文件（`web/src/i18n/`，zh + en 两套，加 `apps.detail.tabs.conversations`、`apps.conversations.*`、`layout.nav.conversations`）
- Test: 复用 `DashboardLayout.spec.ts` 既有断言模式补一条

- [ ] **Step 1: 加路由**

`web/src/app/router.ts` 在 apps detail children（`kanban` 那组）加：
```ts
            { path: 'conversations', component: () => import('@/pages/apps/AppConversationsTab.vue'), props: true },
```

- [ ] **Step 2: 加详情页 tab**

`AppDetailPage.vue` 的 `allTabs` 数组合适位置（如 channels 之后）加：
```ts
  { path: 'conversations', label: t('apps.detail.tabs.conversations') },
```

- [ ] **Step 3: 加 org_member 左侧菜单项**

`DashboardLayout.vue` 在 `memberAppTabKey('channels')` 等同组位置加：
```ts
      { key: memberAppTabKey('conversations'), label: t('layout.nav.conversations'), icon: () => h(MessageSquare, { size: 18 }) },
```
（`MessageSquare` 从既有图标库 import，参照同文件其它 icon import。）

- [ ] **Step 4: 加 i18n 文案（zh + en）**

zh：
```
apps.detail.tabs.conversations = 对话
layout.nav.conversations = 对话
apps.conversations.new = 新建会话
apps.conversations.send = 发送
apps.conversations.sending = 发送中…
apps.conversations.placeholder = 输入消息，回车发送
```
en（对应英文，遵循仓库 i18n 完整性单测，键必须 zh/en 成对）：
```
apps.detail.tabs.conversations = Conversations
layout.nav.conversations = Conversations
apps.conversations.new = New chat
apps.conversations.send = Send
apps.conversations.sending = Sending…
apps.conversations.placeholder = Type a message, press Enter to send
```

- [ ] **Step 5: 跑前端校验（i18n 完整性 + 组件 + 类型）**

Run:
```bash
cd web && npx vitest run src/i18n && npx vitest run src/pages/apps/AppConversationsTab.spec.ts src/layouts/DashboardLayout.spec.ts && npx vue-tsc --noEmit
```
Expected: 全绿（i18n 完整性单测确认无中英缺漏）。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/router.ts web/src/pages/apps/AppDetailPage.vue web/src/layouts/DashboardLayout.vue web/src/i18n
git commit -m "feat(web): 实例会话 tab 接入路由、详情页与成员左侧菜单及 i18n"
```

---

## Phase 5b：流式续聊 + 会话重命名（v1 必含）

> 需求方锁定 v1 必须流式发送、必须含重命名。二者上游均原生支持，无需 patch：
> 流式 `POST /api/sessions/{id}/chat/stream`（命名事件 SSE）、重命名 `PATCH /api/sessions/{id}`（body `title`）。

### Task 12: 流式续聊全链路（oc-ops + Go client + service + handler）

**Files:**
- Modify: `runtime/hermes/.../ocops/conversation.py`、`ocops/server.py`、`tests/test_server_conversation.py`
- Modify: `internal/integrations/ocops/client_sse.go`（`openStream` 支持 body）、`client_conversation.go`、`types_conversation.go`、`client_conversation_test.go`
- Modify: `internal/service/ocops.go`（接口加 `SessionChatStream`）、`hermes_conversation.go`、`hermes_conversation_test.go`
- Modify: `internal/api/handlers/hermes_conversation.go`、`hermes_conversation_test.go`

- [ ] **Step 1（oc-ops）：写流式转发 + 路由测试**

`tests/test_server_conversation.py` 追加：
```python
# 流式续聊：server 把 conversation.chat_stream 的逐帧 bytes 透传为 text/event-stream。
def test_chat_stream_route(monkeypatch):
    def fake_stream(sid, body):
        yield b'data: {"event":"assistant.delta","payload":{"delta":"he"}}\n\n'
        yield b'data: {"event":"assistant.completed","payload":{}}\n\n'
    with mock.patch("ocops.server.conversation.chat_stream", side_effect=fake_stream):
        r = _client(monkeypatch).post("/oc/conversations/s1/chat/stream", headers=_H, json={"message": "hi"})
    assert r.status_code == 200
    assert "assistant.delta" in r.text and "assistant.completed" in r.text
```

- [ ] **Step 2（oc-ops）：实现 chat_stream + 路由**

`conversation.py` 追加：
```python
def chat_stream(session_id: str, body: dict):
    """流式续聊：读取 api_server /chat/stream 的命名事件 SSE，把每个 `event:`+`data:`
    规整成单条 `data: {"event","payload"}` 帧（bytes）逐条 yield，供 server 直接转发。
    上游非 2xx / 网络异常映射为 OpsError（由 server 包成 event: error 帧）。"""
    sid = urllib.parse.quote(session_id, safe="")
    url = _API_BASE + f"/api/sessions/{sid}/chat/stream"
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST")
    key = _api_server_key()
    if key:
        req.add_header("Authorization", "Bearer " + key)
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "text/event-stream")
    try:
        resp = urllib.request.urlopen(req, timeout=_TIMEOUT)
    except urllib.error.HTTPError as e:
        code = {400: "BAD_REQUEST", 401: "INTERNAL", 404: "NOT_FOUND"}.get(e.code, "INTERNAL")
        raise OpsError(code, f"api_server {e.code}: {e.reason}")
    except Exception as e:
        raise OpsError("INTERNAL", f"调用 api_server 失败: {e}")
    event_name = ""
    for raw in resp:  # 逐行读取上游 SSE
        line = raw.decode("utf-8", "replace").rstrip("\r\n")
        if line.startswith("event:"):
            event_name = line[6:].strip()
        elif line.startswith("data:"):
            payload = line[5:].strip()
            try:
                parsed = json.loads(payload)
            except Exception:
                parsed = payload
            frame = json.dumps({"event": event_name or "message", "payload": parsed})
            yield ("data: " + frame + "\n\n").encode()
        elif line == "":
            event_name = ""  # 帧分隔，重置事件名
```

`server.py` 追加 handler + 路由：
```python
async def conversation_chat_stream(request):
    """POST /oc/conversations/{sid}/chat/stream —— 流式续聊，转发 api_server SSE。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    sid = request.path_params["sid"]

    def gen():
        try:
            yield from conversation.chat_stream(sid, body)
        except OpsError as e:
            yield ("event: error\ndata: " + json.dumps({"code": e.code, "message": e.message}) + "\n\n").encode()

    return StreamingResponse(gen(), media_type="text/event-stream")
```
`routes` 追加：`Route("/oc/conversations/{sid}/chat/stream", conversation_chat_stream, methods=["POST"]),`

跑：`PYTHONPATH=/usr/local/lib python -m pytest tests/test_server_conversation.py -v` → PASS。

- [ ] **Step 3（Go client）：`openStream` 支持 body + `SessionChatStream`**

`client_sse.go` 把 `openStream` 重构为支持可选 body（保持既有无 body 调用不变）：
```go
// openStream 发起流式请求（无请求体）；保持既有 WatchKanban/ChannelLogin 调用签名不变。
func (c *Client) openStream(ctx context.Context, ep Endpoint, method, path string) (*http.Response, error) {
	return c.openStreamBody(ctx, ep, method, path, nil, "")
}

// openStreamBody 同 openStream，但支持携带请求体（POST 流式，如续聊 chat/stream）。
func (c *Client) openStreamBody(ctx context.Context, ep Endpoint, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, ep.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Accept", "text/event-stream")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.streamHTTP.Do(req)
	if err != nil {
		return nil, ErrCLI
	}
	if sentinel := statusToErr(resp.StatusCode); sentinel != nil {
		resp.Body.Close()
		return nil, sentinel
	}
	return resp, nil
}
```
（删除原 `openStream` 内联实现，import 增加 `io`。）

`types_conversation.go` 追加：
```go
// ConversationStreamEvent 是 oc-ops 规整后的流式帧：event 为事件名（assistant.delta 等），
// payload 为对应 JSON（delta 文本在 payload.delta）。
type ConversationStreamEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}
```
（`types_conversation.go` import 增加 `encoding/json`。）

`client_conversation.go` 追加（import 加 `bytes`、`encoding/json`）：
```go
// SessionChatStream 流式续聊，返回逐帧事件 channel；流结束/ctx 取消时关闭。
// POST /oc/conversations/{sid}/chat/stream
func (c *Client) SessionChatStream(ctx context.Context, ep Endpoint, sid string, req ConversationChatReq) (<-chan ConversationStreamEvent, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.openStreamBody(ctx, ep, http.MethodPost,
		"/oc/conversations/"+url.PathEscape(sid)+"/chat/stream", bytes.NewReader(b), "application/json")
	if err != nil {
		return nil, err
	}
	ch := make(chan ConversationStreamEvent, sseChanBuffer)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanSSE(ctx, resp.Body, func(data []byte) bool {
			var ev ConversationStreamEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return true // 跳过无法解析的帧
			}
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return ch, nil
}
```
client 测试 `client_conversation_test.go` 追加一条：httptest 返回两条 `data:` 帧，断言 channel 收到 `assistant.delta` 与 `assistant.completed` 两个 Event。

- [ ] **Step 4（service + handler）：ChatStream 透传**

`ocops.go` 的 `conversationOps` 接口追加：
```go
	SessionChatStream(ctx context.Context, ep ocops.Endpoint, sid string, req ocops.ConversationChatReq) (<-chan ocops.ConversationStreamEvent, error)
```
`hermes_conversation.go` 追加（resolveManage + 校验 + 非空，同 `Chat`，但返回 channel）：
```go
// ChatStream 流式续聊，返回事件 channel，由 handler 逐帧转 SSE。
func (s *HermesConversationService) ChatStream(ctx context.Context, p auth.Principal, appID, sid, message string) (<-chan ocops.ConversationStreamEvent, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionID(sid); err != nil {
		return nil, err
	}
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("%w: 消息内容不能为空", ErrConversationBadRequest)
	}
	ch, err := s.ops.SessionChatStream(ctx, loc.Endpoint, sid, ocops.ConversationChatReq{Message: message})
	if err != nil {
		return nil, mapOcOpsConversationErr(err)
	}
	return ch, nil
}
```
（service 测试在 `fakeConversationOps` 加 `SessionChatStream` 返回一个预填 channel，断言 ChatStream 透传。）

`hermes_conversation.go`（handler）接口 `conversationHandlerService` 追加 `ChatStream`，并加 SSE handler（参照 `hermes_kanban.go` 的 `StreamEvents`）+ 路由 `g.POST("/:sid/chat/stream", h.ChatStream)`：
```go
// ChatStream POST /api/v1/apps/{appId}/hermes/conversations/{sid}/chat/stream —— 流式续聊（SSE）。
func (h *HermesConversationHandler) ChatStream(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: ErrorBody{Code: "INTERNAL", Message: "服务端不支持流式响应"}})
		return
	}
	var req ConversationChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: ErrorBody{Code: "CONVERSATION_BAD_REQUEST", Message: "消息内容不能为空"}})
		return
	}
	// resolve+鉴权在 ChatStream 内完成，此时尚未写响应头，错误可正常映射状态码。
	events, err := h.service.ChatStream(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"), req.Message)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher.Flush()
	for ev := range events {
		payload, mErr := json.Marshal(ev)
		if mErr != nil {
			continue
		}
		_, _ = c.Writer.WriteString("data: " + string(payload) + "\n\n")
		flusher.Flush()
	}
}
```
（handler import 加 `encoding/json`；测试加一条：stub 的 ChatStream 返回预填 channel，断言响应含 `assistant.delta`。注意 gin httptest 的 `httptest.NewRecorder` 实现了 `http.Flusher`。）

- [ ] **Step 5：全量测试 + 提交**

Run: `go build ./... && go test ./internal/... -count=1 && cd runtime/hermes/hermes-v2026.6.5 && PYTHONPATH=/usr/local/lib python -m pytest tests/ -q`
Expected: 全绿。
```bash
git add -A && git commit -m "feat: 实例会话流式续聊全链路（oc-ops/client/service/handler）"
```

### Task 13: 会话重命名全链路（PATCH）

**Files:** `ocops/conversation.py`、`server.py`、`client_conversation.go`、`ocops.go`、`hermes_conversation.go`(service+handler)、`dto.go` + 各 `_test`

- [ ] **Step 1（oc-ops）：update_title + 路由 + 测试**

`conversation.py` 追加：
```python
def update_title(session_id: str, title: str) -> dict:
    """重命名会话：PATCH /api/sessions/{id}，body {"title": ...}，返回更新后的会话对象。"""
    sid = urllib.parse.quote(session_id, safe="")
    return _json("PATCH", f"/api/sessions/{sid}", {"title": title})
```
`server.py` 加 handler + 路由：
```python
async def conversation_rename(request):
    """PATCH /oc/conversations/{sid} —— 重命名会话，body {"title"}。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    try:
        return _ok(conversation.update_title(request.path_params["sid"], str(body.get("title", ""))))
    except OpsError as e:
        return _err(e)
```
`routes` 追加：`Route("/oc/conversations/{sid}", conversation_rename, methods=["PATCH"]),`（与既有 DELETE 同 path 不同 method）。
test 追加：mock `conversation.update_title`，PATCH 返回 200 且透传 title。

- [ ] **Step 2（Go client）：UpdateSessionTitle**

`client_conversation.go` 追加：
```go
// UpdateSessionTitle 重命名会话。
// PATCH /oc/conversations/{sid}
func (c *Client) UpdateSessionTitle(ctx context.Context, ep Endpoint, sid, title string) (ConversationSession, error) {
	var out ConversationSession
	err := c.DoJSON(ctx, ep, http.MethodPatch, "/oc/conversations/"+url.PathEscape(sid),
		map[string]string{"title": title}, &out)
	return out, err
}
```
`conversationOps` 接口加 `UpdateSessionTitle(...)`；`fakeConversationOps` 实现它。

- [ ] **Step 3（service + handler + DTO）：Rename**

`hermes_conversation.go`(service) 追加：
```go
// Rename 重命名会话；title 不能为空白。
func (s *HermesConversationService) Rename(ctx context.Context, p auth.Principal, appID, sid, title string) (ocops.ConversationSession, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationSession{}, err
	}
	if err := validateSessionID(sid); err != nil {
		return ocops.ConversationSession{}, err
	}
	if strings.TrimSpace(title) == "" {
		return ocops.ConversationSession{}, fmt.Errorf("%w: 标题不能为空", ErrConversationBadRequest)
	}
	out, err := s.ops.UpdateSessionTitle(ctx, loc.Endpoint, sid, strings.TrimSpace(title))
	if err != nil {
		return ocops.ConversationSession{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}
```
`dto.go` 追加：
```go
// RenameConversationRequest 是 PATCH /hermes/conversations/:sid 的请求体。
type RenameConversationRequest struct {
	Title string `json:"title" binding:"required"` // 新标题，必填
}
```
handler 接口加 `Rename`，加 handler + 路由 `g.PATCH("/:sid", h.Rename)`：
```go
// Rename PATCH /api/v1/apps/{appId}/hermes/conversations/{sid} —— 重命名会话。
func (h *HermesConversationHandler) Rename(c *gin.Context) {
	var req RenameConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: ErrorBody{Code: "CONVERSATION_BAD_REQUEST", Message: "标题不能为空"}})
		return
	}
	out, err := h.service.Rename(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"), req.Title)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": out})
}
```
各层补单测（重命名透传 title；空 title → 400）。

- [ ] **Step 4：测试 + openapi 同步 + 提交**

Run: `go build ./... && go test ./internal/... -count=1 && make openapi-gen && make web-types-gen`
```bash
git add -A && git commit -m "feat: 实例会话重命名全链路（PATCH 透传 api_server）"
```

### Task 14: 前端接入流式续聊 + 重命名

**Files:** `web/src/api/conversations.ts`、`AppConversationsTab.vue`、`AppConversationsTab.spec.ts`

- [ ] **Step 1：api 模块加 chatStream（fetch 流式）+ renameConversation**

`conversations.ts` 追加：
```ts
// 流式续聊：fetch POST，逐帧解析 oc-ops 规整后的 SSE（data: {event,payload}）。
// onDelta 收到增量文本，onDone 流结束。鉴权头由全局 fetch 拦截/统一封装注入（对齐仓库既有 http 封装）。
export async function chatStream(
  appId: string, sid: string, message: string,
  cb: { onDelta: (d: string) => void; onDone: () => void; onError?: (m: string) => void },
): Promise<void> {
  const resp = await fetch(`${base(appId)}/${encodeURIComponent(sid)}/chat/stream`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeader() }, // authHeader 以仓库既有方式取 token
    body: JSON.stringify({ message }),
  })
  if (!resp.ok || !resp.body) { cb.onError?.('stream failed'); return }
  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    const frames = buf.split('\n\n')
    buf = frames.pop() ?? ''
    for (const f of frames) {
      const line = f.split('\n').find(l => l.startsWith('data:'))
      if (!line) continue
      try {
        const evt = JSON.parse(line.slice(5).trim())
        if (evt.event === 'assistant.delta') cb.onDelta(evt.payload?.delta ?? '')
        else if (evt.event === 'assistant.completed') cb.onDone()
        else if (evt.event === 'error') cb.onError?.(evt.message ?? 'error')
      } catch { /* 跳过无法解析的帧 */ }
    }
  }
  cb.onDone()
}

export async function renameConversation(appId: string, sid: string, title: string): Promise<ConversationSession> {
  const { data } = await http.patch(`${base(appId)}/${encodeURIComponent(sid)}`, { title })
  return data.session
}
```
> `authHeader()`/`http.patch` 以仓库既有 api 封装为准（执行时对齐 `web/src/api/` 既有 token 注入方式）。

- [ ] **Step 2：组件 onSend 改为流式 + 加重命名交互**

`AppConversationsTab.vue` 的 `onSend` 改为：乐观追加 user 消息 + assistant 空占位，调 `api.chatStream`，`onDelta` 累加占位 content，`onDone` 重新 `selectSession` 校正历史。会话列表项加「重命名」入口调 `api.renameConversation` 后刷新列表。

```ts
async function onSend() {
  const text = draft.value.trim()
  if (!text || !currentId.value) return
  sending.value = true
  draft.value = ''
  messages.value.push({ role: 'user', content: text })
  const asst = reactive<api.ConversationMessage>({ role: 'assistant', content: '' })
  messages.value.push(asst)
  try {
    await api.chatStream(props.appId, currentId.value, text, {
      onDelta: (d) => { asst.content = (asst.content as string) + d },
      onDone: () => {},
    })
    await selectSession(currentId.value) // 流结束后用权威历史校正
  } finally {
    sending.value = false
  }
}
async function onRename(sid: string, title: string) {
  await api.renameConversation(props.appId, sid, title)
  await loadSessions()
}
```
（`reactive` 从 vue import。）

- [ ] **Step 3：组件测试补流式 + 重命名**

`AppConversationsTab.spec.ts` 追加：mock `api.chatStream`（同步触发 onDelta('ok')+onDone）断言占位 assistant 内容变为 `ok`；mock `api.renameConversation` 断言重命名后 `listConversations` 重新调用。

- [ ] **Step 4：前端校验 + 提交**

Run: `cd web && npx vitest run src/pages/apps/AppConversationsTab.spec.ts && npx vue-tsc --noEmit`
```bash
git add -A && git commit -m "feat(web): 实例会话接入流式续聊与重命名"
```

---

## Phase 6：端到端验证

### Task 15: 真实浏览器三角色验证

**非 TDD 任务。** 依据 AGENTS.md「交付前必须真实浏览器验证」。

- [ ] **Step 1: 构建并起本地环境**

Run: `make build-hermes-runtime`（含构建期 pytest 自检）、`make local-up`（k3d，参照 docs/local-development.md），确保至少一个真实实例 + 一条微信会话存在。

- [ ] **Step 2: org_member 主路径验证**

用 org_member 账号登录 `http://ocm.localhost` → 左侧菜单进「对话」：
- 会话列表展示本实例跨渠道会话（含 weixin 来源标签）；
- 选中一条会话能看到文字/图片历史；
- 新建会话 → 发送文字 → 看到 assistant 回复**逐字流式呈现**（非一次性整段）；
- 重命名会话生效（列表标题即时更新）；
- 删除会话生效。

- [ ] **Step 3: 续聊微信会话验证（依 Spike 结论）**

选一条 weixin 会话续聊：按 Task 1 Step 5 结论确认行为（若上游自动投递，则微信用户收到；若不投递，页面显示回复即视为符合 v1 设计）。

- [ ] **Step 4: org_admin / platform_admin 验证**

分别用 org_admin、platform_admin 登录，经实例详情页「对话」tab 验证可见与可用；越权访问他人实例返回 403。

- [ ] **Step 5: 记录验证矩阵并提交**

把逐项结果（角色 × 功能）写入验证记录文档，发现问题先修复再交付。
```bash
git add docs/superpowers/reports/2026-06-23-instance-conversations-verification.md
git commit -m "docs: 记录实例对话功能三角色浏览器验证"
```

---

## Spike 结论（Task 1 执行后填写）

> 执行 Task 1 后在此记录：api_server 是否运行、API_SERVER_KEY 来源（env / config.yaml）、
> 选定鉴权路径（A 无 patch / B 注入 patch）、`/api/sessions` 与 `/messages` 真实字段、
> `/chat` 对微信会话是否自动投递。后续 Task 按此结论校准。

- api_server 运行 & 端口：**已静态确认** = 启用（`API_SERVER_ENABLED=true`，render.go:107），监听 127.0.0.1:8642。
- API_SERVER_KEY 来源：**无**（仓库未注入）→ api_server 不鉴权。
- 鉴权路径：**A（无 patch、无 token）**——oc-ops 直接调 `/api/sessions`，`_api_server_key()` 返回空、不发 Authorization 头。
- session JSON 字段：**已读源码确认**（`_session_response` safe_keys）= id/source/user_id/model/title/started_at/last_active/message_count/preview…，包装 `{object:list,data:[]}`。
- message JSON 字段：**已读源码确认**（`_message_response` safe_keys）= id/role/content/timestamp/tool_calls/finish_reason/reasoning…，包装 `{object:list,session_id,data:[]}`。
- `/chat` 微信投递行为：待真实 pod 复核（不投递则 v1 不处理，设计已定）——唯一仍需 pod 的 Spike 项。

---

## 自检对照（spec 覆盖）

- §1 列会话/读历史/续聊/管理 → Task 2/3/4/6/7/10/11 ✅
- §2.2 文件：文字+图片，文档仅入站显示 → DTO 多模态 content + `ConversationMessageView` 图片渲染；出站文字优先 ✅
- §2.3 微信投递不处理 → Task 1 Step 5 记录、Task 12 Step 3 验证 ✅
- §4 端点（list/messages/create/chat/chat-stream/delete/rename）→ Task 7 + Task 12（流式）+ Task 13（重命名 PATCH）✅
- §5 权限 → Task 5 谓词 + Task 6 resolve ✅
- §6 会话管理（新建/切换/删除/重命名）→ Task 10 + Task 14 组件 ✅
- §7 不落库 → service/handler 全透传，无新表 ✅
- §8 错误处理 → Task 7 映射规则 ✅
- §9 测试 → 各 Task 含 pytest / go test / vitest ✅
- 流式（需求方锁定「v1 必须流式」）→ Task 12 全链路（oc-ops SSE 规整 + Go `openStreamBody`/`SessionChatStream` + handler SSE + 前端 fetch 流式）✅
- 重命名（需求方锁定「v1 纳入重命名」）→ Task 13 全链路 PATCH（上游 `PATCH /api/sessions/{id}` 原生支持，无需 patch）✅

> 实现顺序：Task 2-11 先打通非流式闭环（含完整 CRUD + 单测），Task 12-14 在其上叠加流式与重命名。非流式 `/chat` 端点保留（作为测试与降级路径），流式 `/chat/stream` 为前端主路径。全程 v1 不需要任何上游 patch。
