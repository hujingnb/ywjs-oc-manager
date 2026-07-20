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

### Task 4: 将单次健康门禁收紧为连续稳定窗口

> 首次 Task 3 真实验收发现：健康 GET 可命中即将退出的旧 Pod，紧接的登录 POST 仍可能在
> EndpointSlice 与 Traefik 路由切换窗口收到 502。因此必须让健康门禁跨越一段经过重复验证的
> 稳定窗口，不能以单次成功作为登录条件。

**Files:**
- Modify: `scripts/local_seed_demo/client.py`
- Modify: `scripts/tests/test_local_seed_demo_client.py`

- [ ] **Step 1: 写连续三次成功与瞬时失败清零的失败测试**

将测试服务器的 `health_retry` 场景改为前两次成功、第三次 502、随后三次成功：

```python
# 前两次成功后模拟旧 Pod 摘除窗口；连续成功计数必须在第三次 502 时清零。
if self.server.scenario == "health_retry":
    if len(self.server.requests) == 3:
        self._send_json(502, {"code": "bad_gateway", "message": "入口切换中"})
    else:
        self._send_json(200, {"status": "ok"})
    return
```

把原健康恢复测试收紧为：

```python
# 覆盖两次健康后发生路由切换，要求失败清零并重新取得三次连续成功。
def test_wait_ready_resets_stability_after_transient_502(self):
    server = self._start_server("health_retry")
    sleeps = []
    client = ManagerAPI(
        f"http://127.0.0.1:{server.server_port}",
        sleep=sleeps.append,
    )

    result = client.wait_ready()

    self.assertEqual({"status": "ok"}, result)
    self.assertEqual(6, len(server.requests))
    self.assertEqual([1, 1, 1, 1, 1], sleeps)
```

新增正常稳定窗口测试：

```python
# 覆盖稳定入口必须经过三次匿名健康探测和两段间隔才允许返回。
def test_wait_ready_requires_three_consecutive_successes(self):
    server = self._start_server("health_ok")
    sleeps = []
    client = ManagerAPI(
        f"http://127.0.0.1:{server.server_port}",
        sleep=sleeps.append,
    )

    self.assertEqual({"status": "ok"}, client.wait_ready())

    self.assertEqual(3, len(server.requests))
    self.assertEqual([1, 1], sleeps)
    self.assertEqual(
        [None, None, None],
        [request["authorization"] for request in server.requests],
    )
```

同步调整原来假定单次成功的匿名、默认 deadline 和 worker 正常返回测试，使其断言三次调用；
非法 envelope、非法 JSON、4xx 和控制流异常仍应在第一次探测立即失败。

- [ ] **Step 2: 运行稳定窗口测试并确认 RED**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts.tests.test_local_seed_demo_client.ManagerAPITest.test_wait_ready_resets_stability_after_transient_502 \
  scripts.tests.test_local_seed_demo_client.ManagerAPITest.test_wait_ready_requires_three_consecutive_successes
```

Expected: 现有实现会在第一次 `status=ok` 后返回，请求数和间隔断言 FAIL。

- [ ] **Step 3: 实现单次探测模式与连续稳定计数**

在客户端定义固定稳定门槛：

```python
# 三次成功覆盖两段一秒间隔，避免单次健康命中即将退出的旧 Pod。
_READINESS_SUCCESSES = 3
_READINESS_INTERVAL = 1
```

让 `_request()` 接受仅供内部健康门禁使用的可选重试序列；默认值继续保持普通 GET 的五档退避
和 POST/PATCH 的零重试：

```python
def _request(
    self,
    method,
    path,
    body,
    authenticated,
    deadline=None,
    retry_delays=None,
):
    """发送 JSON 请求，并允许健康门禁显式观察每一次入口探测结果。"""
    if retry_delays is None:
        retry_delays = _GET_RETRY_DELAYS if method == "GET" else ()
```

在 `wait_ready()` 内新增 `wait_for_stable_health()`，由既有 daemon worker 调用它；该函数循环
执行禁用底层重试的匿名 GET：

```python
consecutive_successes = 0
while consecutive_successes < _READINESS_SUCCESSES:
    try:
        payload = self._request(
            "GET",
            "/healthz",
            None,
            authenticated=False,
            deadline=deadline,
            retry_delays=(),
        )
    except APIError as error:
        transient = (
            error.status in TRANSIENT_STATUSES
            or (error.status is None and error.code == "connection_error")
        )
        if not transient:
            raise
        consecutive_successes = 0
    else:
        if not isinstance(payload, dict) or payload.get("status") != "ok":
            raise APIError(
                operation,
                200,
                "invalid_health_response",
                "manager 健康检查响应异常",
            )
        consecutive_successes += 1
        if consecutive_successes == _READINESS_SUCCESSES:
            return payload
    self._sleep_before_retry(
        _READINESS_INTERVAL,
        deadline,
        operation,
    )
```

既有 `request_health()` 调整为调用 `wait_for_stable_health()` 后再向容量为一的 Queue 写入最终
payload；捕获 `BaseException` 并由主线程原样传播的逻辑保持不变：

```python
def request_health():
    """在 daemon worker 中等待稳定健康，并搬运结果或控制流异常。"""
    try:
        payload = wait_for_stable_health()
    except BaseException as error:
        result_queue.put_nowait((False, error))
        return
    result_queue.put_nowait((True, payload))
```

外层 Queue deadline 继续覆盖 DNS 阻塞。不得改变 `login()`、普通 `get()`、POST/PATCH 或 CLI。

- [ ] **Step 4: 运行客户端和完整本地播种回归并确认 GREEN**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v scripts/tests/test_local_seed_demo_client.py
PYTHONPATH=scripts python3 -m unittest discover \
  -s scripts/tests -p 'test_local_seed_demo_*.py' -v
python3 -m py_compile scripts/local-seed-demo.py scripts/local_seed_demo/*.py
git diff --check
```

Expected: 全部测试 PASS；普通 GET 仍保留 `[1, 2, 4, 8, 16]` 退避，健康稳定路径固定为三次
匿名 GET 和两次一秒间隔。

- [ ] **Step 5: 提交稳定窗口修复**

```bash
git add scripts/local_seed_demo/client.py scripts/tests/test_local_seed_demo_client.py
git commit -m "fix(local): 等待 manager 入口稳定" \
  -m "健康门禁要求三次连续成功，瞬时失败会清零后重新确认。\n\n登录 POST 仍只发送一次，普通 GET 的有限退避策略保持不变。"
```

### Task 5: 重新执行真实竞态与浏览器验收

**Files:**
- Verify only; do not add permanent E2E fixtures

- [ ] **Step 1: 在本地 k3d 制造相同滚动窗口**

Run:

```bash
test "$(kubectl config current-context)" = "k3d-ocm"
kubectl -n ocm rollout restart deploy/manager-api
kubectl -n ocm rollout status deploy/manager-api --timeout=300s
make local-seed-demo
```

Expected: rollout 结束后不加额外 sleep；健康门禁跨过路由切换窗口，登录不再返回 502，播种输出
`2 个助手版本 / 3 个企业 / 2 个普通实例 / 2 个智能客服`。

- [ ] **Step 2: 真实无头浏览器验证平台登录**

使用临时 Python Playwright 脚本访问 `http://ocm.localhost/login`，等待 `networkidle` 后输入
平台管理员本地账号，断言最终 URL 为 `/console`、已登录导航可见、登录按钮消失，并断言
`console error` 与 `pageerror` 均为空。脚本只放 `/tmp`，不得提交截图、凭据或永久 spec。

- [ ] **Step 3: 最终定向回归与工作区检查**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest discover \
  -s scripts/tests -p 'test_local_seed_demo_*.py' -v
python3 -m py_compile scripts/local-seed-demo.py scripts/local_seed_demo/*.py
git diff --check
git status --short
```

Expected: 自动化回归全部通过，工作区干净，没有 token、临时脚本或浏览器产物进入 git。
