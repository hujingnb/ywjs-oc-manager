# Local Seed Ingress Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在演示数据登录前确认同一 Traefik Ingress 已能稳定访问 manager-api，消除 `local-reset` 滚动重启后的瞬时登录 502。

**Architecture:** `ManagerAPI` 提供一个有 60 秒总预算的匿名 `/healthz` 就绪检查，复用现有 GET 瞬时错误重试和 deadline 约束。CLI 创建平台客户端后先完成该检查，再且仅再提交一次登录 POST；普通写请求的“不盲目重试”规则保持不变。

**Tech Stack:** Python 3 标准库、`unittest`、本地 k3d、Traefik Ingress、Playwright Chromium

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `scripts/local_seed_demo/client.py` | 封装匿名健康检查、总 deadline 与健康响应契约 |
| `scripts/tests/test_local_seed_demo_client.py` | 验证瞬时 502、异常 envelope 和 deadline 行为 |
| `scripts/local_seed_demo/cli.py` | 在平台登录前调用就绪检查 |
| `scripts/tests/test_local_seed_demo_cli.py` | 锁定“健康检查 → 单次登录 → seeder”的顺序与失败短路 |

### Task 1: 增加 manager Ingress 就绪检查

**Files:**
- Modify: `scripts/local_seed_demo/client.py`
- Modify: `scripts/tests/test_local_seed_demo_client.py`

- [ ] **Step 1: 写瞬时 502 恢复和异常 envelope 的失败测试**

在 `_ScenarioHandler.do_GET()` 增加两个受控场景：

```python
# 健康检查第一次命中入口层瞬时 502，第二次恢复为 manager 的正式健康响应。
if self.server.scenario == "health_retry":
    if len(self.server.requests) == 1:
        self._send_json(502, {"code": "bad_gateway"})
    else:
        self._send_json(200, {"status": "ok"})
    return

# 200 只能证明入口可达；业务 envelope 不健康时仍必须拒绝登录。
if self.server.scenario == "health_invalid":
    self._send_json(200, {"status": "starting"})
    return
```

在 `ManagerAPITest` 追加：

```python
# 覆盖 Traefik 短暂 502 后恢复，健康检查只重试 GET 且受既有首档退避控制。
def test_wait_ready_retries_transient_502_then_accepts_ok(self):
    server = self._start_server("health_retry")
    sleeps = []
    client = ManagerAPI(
        f"http://127.0.0.1:{server.server_port}",
        sleep=sleeps.append,
    )

    result = client.wait_ready()

    self.assertEqual({"status": "ok"}, result)
    self.assertEqual([1], sleeps)
    self.assertEqual(["GET", "GET"], [item["method"] for item in server.requests])

# 覆盖入口返回 200 但健康 envelope 非 ok 时立即失败，不把异常状态当成可登录。
def test_wait_ready_rejects_non_ok_envelope(self):
    server = self._start_server("health_invalid")
    client = ManagerAPI(f"http://127.0.0.1:{server.server_port}")

    with self.assertRaises(APIError) as raised:
        client.wait_ready()

    self.assertEqual(200, raised.exception.status)
    self.assertEqual("invalid_health_response", raised.exception.code)
    self.assertEqual(1, len(server.requests))
```

- [ ] **Step 2: 运行客户端测试并确认 RED**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts.tests.test_local_seed_demo_client.ManagerAPITest.test_wait_ready_retries_transient_502_then_accepts_ok \
  scripts.tests.test_local_seed_demo_client.ManagerAPITest.test_wait_ready_rejects_non_ok_envelope
```

Expected: 两项均因 `ManagerAPI` 尚无 `wait_ready` 而 ERROR。

- [ ] **Step 3: 实现最小健康检查方法**

在 `ManagerAPI` 中增加：

```python
def wait_ready(self, timeout=60):
    """经与登录相同的入口等待 manager 健康，并以总预算限制请求及退避。"""
    deadline = self.monotonic() + timeout
    payload = self.get("/healthz", deadline=deadline)
    if not isinstance(payload, dict) or payload.get("status") != "ok":
        raise APIError(
            "GET /healthz",
            200,
            "invalid_health_response",
            "manager 健康检查响应异常",
        )
    return payload
```

该方法不得设置 token、不得调用 POST，也不得改变普通 `get()` 和写请求的默认行为。

- [ ] **Step 4: 运行客户端完整测试并确认 GREEN**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v scripts/tests/test_local_seed_demo_client.py
```

Expected: 全部 client 测试 PASS；瞬时健康检查只产生两次 GET 和一次 1 秒退避。

- [ ] **Step 5: 提交客户端就绪检查**

```bash
git add scripts/local_seed_demo/client.py scripts/tests/test_local_seed_demo_client.py
git commit -m "fix(local): 等待 manager 入口就绪" \
  -m "在登录前通过同一 Ingress 执行有界健康检查。\n\n瞬时 502 仅重试幂等 GET，登录 POST 仍只提交一次。"
```

### Task 2: 将 CLI 登录接到就绪门禁

**Files:**
- Modify: `scripts/local_seed_demo/cli.py`
- Modify: `scripts/tests/test_local_seed_demo_cli.py`

- [ ] **Step 1: 扩展 Fake 并写 CLI 顺序失败测试**

让 `FakeAPI` 记录健康检查与登录的先后事实：

```python
class FakeAPI:
    def __init__(self, base_url):
        self.base_url = base_url
        self.logins = []
        self.calls = []

    def wait_ready(self):
        self.calls.append("ready")
        return {"status": "ok"}

    def login(self, org_code, username, password):
        self.calls.append("login")
        self.logins.append((org_code, username, password))
```

在成功测试增加：

```python
self.assertEqual(["ready", "login"], factory.clients[0].calls)
```

并新增失败短路测试：

```python
# 覆盖入口健康检查失败时不提交登录 POST，也不启动任何播种写入。
def test_manager_readiness_failure_stops_before_login_and_seed(self):
    self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")

    class UnreadyAPI(FakeAPI):
        def wait_ready(self):
            self.calls.append("ready")
            raise APIError("GET /healthz", 502, "http_error", "bad gateway")

    factory = RecordingFactory()
    factory.client_type = UnreadyAPI
    output = io.StringIO()
    with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
        self.assertEqual(1, main(root=self.root, stdout=output, api_factory=factory))

    self.assertEqual(["ready"], factory.clients[0].calls)
    self.assertEqual([], factory.clients[0].logins)
    seeder.assert_not_called()
    self.assertNotIn("bad gateway", output.getvalue())
```

同步把 `RecordingFactory.__call__()` 改为从默认 `FakeAPI` 或测试覆盖的 `client_type` 创建实例：

```python
def __init__(self, client_type=FakeAPI):
    self.clients = []
    self.client_type = client_type

def __call__(self, base_url):
    client = self.client_type(base_url)
    self.clients.append(client)
    return client
```

- [ ] **Step 2: 运行 CLI 测试并确认 RED**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts.tests.test_local_seed_demo_cli.LocalSeedDemoCLITest.test_complete_run_logs_in_and_uses_independent_clients \
  scripts.tests.test_local_seed_demo_cli.LocalSeedDemoCLITest.test_manager_readiness_failure_stops_before_login_and_seed
```

Expected: 成功场景缺少 `ready` 调用；失败场景继续执行登录或 seeder，至少一项断言 FAIL。

- [ ] **Step 3: 在 CLI 登录前调用健康检查**

将平台客户端装配调整为：

```python
# 健康检查必须经过与登录相同的 Traefik Ingress，避免只证明 Pod 内部可达。
platform = api_factory("http://ocm.localhost")
platform.wait_ready()
platform.login("", "admin", "admin" + "123")
```

不在 CLI 新增 sleep，不捕获新的宽泛异常，不修改企业客户端创建方式。

- [ ] **Step 4: 运行全部 Python 定向回归**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest discover \
  -s scripts/tests -p 'test_local_seed_demo_*.py' -v
python3 -m py_compile scripts/local-seed-demo.py scripts/local_seed_demo/*.py
git diff --check
```

Expected: 所有测试 PASS，编译与 diff 检查退出 0。

- [ ] **Step 5: 提交 CLI 门禁**

```bash
git add scripts/local_seed_demo/cli.py scripts/tests/test_local_seed_demo_cli.py
git commit -m "fix(local): 登录前确认 manager 可达" \
  -m "演示数据入口先等待 Traefik 健康检查成功，再提交一次平台登录。\n\n入口未就绪时安全失败且不产生任何播种写入。"
```

### Task 3: 真实滚动竞态与浏览器验收

**Files:**
- Verify only; do not add permanent E2E fixtures

- [ ] **Step 1: 确认工作区与当前集群健康**

Run:

```bash
git status --short
kubectl -n ocm get deploy,pod,endpoints manager-api
curl --noproxy '*' -fsS http://ocm.localhost/healthz >/dev/null
```

Expected: 工作区干净，manager-api Ready 且健康检查成功。

- [ ] **Step 2: 制造真实滚动窗口并立即运行播种**

Run:

```bash
kubectl -n ocm rollout restart deploy/manager-api
kubectl -n ocm rollout status deploy/manager-api --timeout=300s
make local-seed-demo
```

Expected: 即使 Traefik Endpoint 同步稍有滞后，CLI 也等待健康检查；最终输出
`2 个助手版本 / 3 个企业 / 2 个普通实例 / 2 个智能客服`。

- [ ] **Step 3: 用真实无头浏览器验证登录仍正常**

使用仓库 Playwright Chromium，经 `http://ocm.localhost/login` 输入平台管理员账号；断言离开
`/login`、平台首页可见且 console 无 error。浏览器脚本放在临时位置或内联执行，不提交 token、
截图或永久 spec。

Expected: 登录成功；浏览器 console 无与就绪检查或认证相关的 error。

- [ ] **Step 4: 最终检查**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest discover \
  -s scripts/tests -p 'test_local_seed_demo_*.py' -v
python3 -m py_compile scripts/local-seed-demo.py scripts/local_seed_demo/*.py
git diff --check
git status --short
git log --oneline -5
```

Expected: 自动化回归全部通过，工作区干净，两个修复提交按客户端与 CLI 边界拆分。
