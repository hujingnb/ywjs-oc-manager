# Local Demo Seed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `make local-up` 与 `make local-reset` 自动、幂等地创建三类本地演示企业，并等待普通实例和智能客服真实可用。

**Architecture:** 使用 Python 标准库实现宿主机侧 manager HTTP API 客户端和演示数据编排器。平台管理员负责版本、企业、企业配置、实例复建和 AICC，企业管理员只在缺少成员时负责成员创建/onboarding；所有异步资源通过 Job 和资源详情接口等待到真实就绪。

**Tech Stack:** Python 3 标准库（`urllib`、`dataclasses`、`unittest`）、GNU Make、manager REST API、k3d/Kubernetes、Playwright 无头 Chromium。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `scripts/local_seed_demo/__init__.py` | 声明可导入的本地种子包 |
| `scripts/local_seed_demo/client.py` | manager HTTP、Bearer 登录、只读重试和安全错误映射 |
| `scripts/local_seed_demo/seeder.py` | 固定数据定义、幂等补齐、冲突检查和运行时等待 |
| `scripts/local_seed_demo/cli.py` | `.env` 预检、依赖装配、顶层执行与安全汇总 |
| `scripts/local-seed-demo.py` | 与现有脚本命名一致的极薄可执行入口 |
| `scripts/tests/test_local_seed_demo_client.py` | HTTP 客户端单元测试 |
| `scripts/tests/test_local_seed_demo_seeder.py` | 版本、企业、成员、实例、AICC 与轮询单元测试 |
| `scripts/tests/test_local_seed_demo_cli.py` | Key 预检、退出码与日志脱敏单元测试 |
| `Makefile` | 在 `local-init-models` 后执行 `local-seed-demo` |
| `CLAUDE.md` | 增加三个演示企业的本地调试账号 |
| `docs/local-development.md` | 说明自动数据、严格失败和重跑语义 |

不修改 handler、service、OpenAPI 或生成的前端类型；计划使用的接口均已存在。

### Task 1: 建立可测试的 manager HTTP 客户端

**Files:**
- Create: `scripts/local_seed_demo/__init__.py`
- Create: `scripts/local_seed_demo/client.py`
- Create: `scripts/tests/test_local_seed_demo_client.py`

- [ ] **Step 1: 写 HTTP 客户端失败测试**

在 `scripts/tests/test_local_seed_demo_client.py` 使用 `unittest` 和本地
`ThreadingHTTPServer` 写三个相邻带中文注释的测试：

```python
class ManagerAPITest(unittest.TestCase):
    # 覆盖平台管理员登录后自动携带 Bearer token 的正常路径。
    def test_login_and_authenticated_get(self):
        api = ManagerAPI(self.base_url, sleep=lambda _: None)
        api.login("", "admin", "admin123")
        self.assertEqual([{"id": "v1"}], api.get("/api/v1/assistant-versions")["versions"])
        self.assertEqual("Bearer token-1", self.server.last_authorization)

    # 覆盖 GET 遭遇 503 后退避重试并最终成功的暂时故障路径。
    def test_get_retries_transient_status(self):
        self.server.responses = [(503, {"code": "INTERNAL"}), (200, {"ok": True})]
        api = ManagerAPI(self.base_url, sleep=lambda _: None)
        self.assertEqual({"ok": True}, api.get("/healthz"))
        self.assertEqual(2, self.server.request_count)

    # 覆盖 POST 响应中断时不盲目重发写请求的幂等保护路径。
    def test_post_raises_uncertain_write_without_retry(self):
        self.server.close_without_response = True
        api = ManagerAPI(self.base_url, sleep=lambda _: None)
        with self.assertRaises(UncertainWrite):
            api.post("/api/v1/organizations", {"code": "demo-full"})
        self.assertEqual(1, self.server.request_count)
```

测试 server 必须只记录方法、路径、Authorization 和 JSON body；不得把登录密码写入失败
消息。测试文件中的每个测试方法和 server 分支都添加相邻中文场景注释。

- [ ] **Step 2: 运行客户端测试确认失败**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v scripts/tests/test_local_seed_demo_client.py
```

Expected: FAIL，提示 `local_seed_demo.client` 或 `ManagerAPI` 不存在。

- [ ] **Step 3: 实现最小 HTTP 客户端**

`scripts/local_seed_demo/client.py` 定义以下稳定接口：

```python
TRANSIENT_STATUSES = {429, 502, 503, 504}


class APIError(RuntimeError):
    def __init__(self, operation, status, code, message):
        self.operation = operation
        self.status = status
        self.code = code
        self.safe_message = message
        super().__init__(f"{operation} 失败: HTTP {status} {code}: {message}")


class UncertainWrite(RuntimeError):
    """写请求已发出但未收到响应；调用方必须先重新查询稳定身份。"""


class ManagerAPI:
    def __init__(self, base_url, sleep=time.sleep, timeout=60):
        self.base_url = base_url.rstrip("/")
        self.sleep = sleep
        self.timeout = timeout
        self.access_token = ""

    def login(self, org_code, username, password):
        data = self.post("/api/v1/auth/login", {
            "org_code": org_code, "username": username, "password": password,
        }, authenticated=False)
        self.access_token = data["tokens"]["access_token"]
        return data["user"]

    def get(self, path):
        return self._request("GET", path, None, authenticated=True, retry_read=True)

    def post(self, path, body, authenticated=True):
        return self._request("POST", path, body, authenticated=authenticated, retry_read=False)

    def patch(self, path, body):
        return self._request("PATCH", path, body, authenticated=True, retry_read=False)
```

`_request()` 使用 `urllib.request.Request`，只对 GET 的连接错误和
`TRANSIENT_STATUSES` 按 `1, 2, 4, 8, 16` 秒退避。HTTP 错误只读取 `code` 和
`message` 构造 `APIError`；POST/PATCH 在连接中断时抛 `UncertainWrite`，不得自动重发。
Authorization 只在内存中设置，异常字符串不得包含请求 headers 或 body。

`scripts/local_seed_demo/__init__.py` 仅包含中文包注释，避免形成无用导出层。

- [ ] **Step 4: 运行客户端测试确认通过**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v scripts/tests/test_local_seed_demo_client.py
```

Expected: 3 tests PASS，且输出不含 `admin123`、Bearer token 或请求 body。

- [ ] **Step 5: 提交 HTTP 客户端**

```bash
git add scripts/local_seed_demo/__init__.py scripts/local_seed_demo/client.py scripts/tests/test_local_seed_demo_client.py
git commit -m "feat(local): 增加演示数据 API 客户端" \
  -m "为本地演示数据初始化提供 Bearer 登录、只读退避重试和不确定写保护。\n\n使用 Python 标准库实现，错误输出不包含密码、token 或请求体。"
```

### Task 2: 补齐助手版本与企业配置

**Files:**
- Create: `scripts/local_seed_demo/seeder.py`
- Create: `scripts/tests/test_local_seed_demo_seeder.py`

- [ ] **Step 1: 写版本与企业幂等测试**

构造记录调用的 `FakeManagerAPI`，先写以下测试：

```python
class DemoSeederPlatformDataTest(unittest.TestCase):
    # 覆盖空环境创建两个固定版本和三个差异化企业的正常路径。
    def test_ensure_platform_data_creates_fixed_matrix(self):
        api = FakeManagerAPI(images=[{"id": "hermes-dev", "label": "Dev"}])
        state = DemoSeeder(api, client_factory=lambda: FakeManagerAPI()).ensure_platform_data()
        self.assertEqual({"本地通用助手版", "本地智能客服版"}, set(state.versions))
        self.assertEqual({"demo-full", "demo-app", "demo-aicc"}, set(state.organizations))
        self.assertEqual(
            [state.versions["本地智能客服版"]["id"], state.versions["本地通用助手版"]["id"]],
            state.organizations["demo-full"]["assistant_version_ids"],
        )

    # 覆盖第二次执行复用既有对象且不覆盖版本提示词、企业名称和额外 allowlist 的幂等路径。
    def test_ensure_platform_data_only_appends_missing_fields(self):
        api = FakeManagerAPI.from_complete_fixture(extra_version_id="custom-v")
        before = copy.deepcopy(api.state)
        DemoSeeder(api, client_factory=lambda: FakeManagerAPI()).ensure_platform_data()
        self.assertEqual(before["versions"], api.state["versions"])
        self.assertIn("custom-v", api.org("demo-full")["assistant_version_ids"])
        self.assertFalse(any(call.method == "POST" for call in api.calls))

    # 覆盖缺失客服但 allowlist 首项错误时拒绝静默使用错误行为版本的冲突路径。
    def test_aicc_version_must_be_first_before_missing_agent_is_created(self):
        api = FakeManagerAPI.from_complete_fixture(with_agents=False)
        api.org("demo-full")["assistant_version_ids"].reverse()
        with self.assertRaisesRegex(SeedConflict, "demo-full.*allowlist 首项"):
            DemoSeeder(api, client_factory=lambda: FakeManagerAPI()).validate_aicc_version_order()
```

Fake API 返回真实 envelope：`{"images": [...]}`、`{"versions": [...]}`、
`{"organizations": [...]}`、`{"organization": {...}}`。所有测试方法、假响应分支和
table 数据添加相邻中文注释。

- [ ] **Step 2: 运行版本与企业测试确认失败**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts/tests/test_local_seed_demo_seeder.py
```

Expected: FAIL，提示 `DemoSeeder`、`SeedConflict` 或固定数据定义不存在。

- [ ] **Step 3: 实现固定数据和平台资源补齐**

在 `seeder.py` 定义不可变规格和结果：

```python
@dataclass(frozen=True)
class VersionSpec:
    name: str
    description: str
    system_prompt: str


@dataclass(frozen=True)
class OrganizationSpec:
    code: str
    name: str
    version_names: tuple[str, ...]
    needs_app: bool
    needs_aicc: bool


@dataclass
class SeedState:
    versions: dict[str, dict]
    organizations: dict[str, dict]
    apps: dict[str, dict] = field(default_factory=dict)
    agents: dict[str, dict] = field(default_factory=dict)


VERSION_SPECS = (
    VersionSpec("本地通用助手版", "本地普通实例演示版本", "你是专业、可靠的企业工作助手。"),
    VersionSpec("本地智能客服版", "本地智能客服演示版本", "你是专业、友好的企业智能客服。"),
)

ORG_SPECS = (
    OrganizationSpec("demo-full", "完整演示企业", ("本地智能客服版", "本地通用助手版"), True, True),
    OrganizationSpec("demo-app", "普通实例演示企业", ("本地通用助手版",), True, False),
    OrganizationSpec("demo-aicc", "智能客服演示企业", ("本地智能客服版",), False, True),
)
```

`DemoSeeder.ensure_platform_data()` 必须：

1. GET `/api/v1/runtime-images`，拒绝空列表或空 `id`，固定选择首项；
2. GET `/api/v1/assistant-versions`，按精确名称查询，缺失时 POST 创建；
3. GET `/api/v1/organizations?limit=100&offset=0`，按 `code` 查询；
4. 缺失企业 POST 创建，请求包含固定 allowlist、`admin/admin123` 和显示名；
5. 已有企业的 allowlist 只追加缺失 ID，PATCH 时 round-trip 响应中的 name、联系人、备注、
   配额字段，禁止用默认值覆盖；
6. AICC 企业 PATCH `/:id/aicc-config` 时保留行业库 ID，`enabled=true`，limit 为 `None`
   时仍保持不限，否则提升到至少 1；
7. `demo-app` 已开启 AICC 时不关闭。

所有 POST/PATCH 捕获 `UncertainWrite` 后重新 GET 列表：稳定对象已经出现即继续，否则抛出
包含操作与目标 code/name 的安全错误。

在 `seeder.py` 同时定义写结果确认 helper；`lookup` 返回 `None` 表示仍不存在：

```python
def ensure_uncertain_write(create, lookup):
    try:
        return create()
    except UncertainWrite:
        existing = lookup()
        if existing is None:
            return create()
        return existing
```

每个调用点负责把 `lookup()` 的既有对象包装成与创建接口相同的 envelope，避免调用方同时
处理两种响应形状。

- [ ] **Step 4: 运行版本与企业测试确认通过**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts/tests/test_local_seed_demo_client.py \
  scripts/tests/test_local_seed_demo_seeder.py
```

Expected: 当前全部测试 PASS。

- [ ] **Step 5: 提交平台数据补齐**

```bash
git add scripts/local_seed_demo/seeder.py scripts/tests/test_local_seed_demo_seeder.py
git commit -m "feat(local): 补齐演示版本与企业" \
  -m "通过 manager 正式 API 幂等创建两个助手版本和三类演示企业。\n\n保留已有企业字段，仅单向补齐版本授权与 AICC 能力。"
```

### Task 3: 补齐企业成员和普通实例

**Files:**
- Modify: `scripts/local_seed_demo/seeder.py`
- Modify: `scripts/tests/test_local_seed_demo_seeder.py`

- [ ] **Step 1: 写成员权限和实例测试**

追加以下测试：

```python
class DemoSeederMemberAppTest(unittest.TestCase):
    # 覆盖新企业使用企业管理员 onboarding 一次创建成员、普通实例和 Job 的正常路径。
    def test_missing_member_uses_org_admin_onboarding(self):
        platform = FakeManagerAPI.from_platform_data()
        org_admin = FakeManagerAPI(login_user={"role": "org_admin"})
        seeder = DemoSeeder(platform, client_factory=lambda: org_admin)
        state = seeder.ensure_members_and_apps(seeder.ensure_platform_data())
        self.assertEqual({"demo-full", "demo-app"}, set(state.apps))
        self.assertEqual("demo-full", org_admin.logins[0][0])
        self.assertEqual("member", org_admin.onboard_requests[0]["username"])

    # 覆盖成员已存在但没有实例时由平台管理员调用复建接口且不需要企业管理员密码的路径。
    def test_existing_member_missing_app_uses_platform_create_app(self):
        platform = FakeManagerAPI.from_platform_data(existing_members=True, existing_apps=False)
        seeder = DemoSeeder(platform, client_factory=FailIfCalledFactory())
        seeder.ensure_members_and_apps(seeder.ensure_platform_data())
        self.assertEqual(2, len(platform.member_app_requests))

    # 覆盖管理员密码已修改且成员缺失时拒绝重置密码或绕过权限的冲突路径。
    def test_missing_member_with_changed_admin_password_fails_safely(self):
        platform = FakeManagerAPI.from_platform_data(existing_members=False)
        org_admin = FakeManagerAPI(login_error=APIError("登录", 401, "UNAUTHORIZED", "用户名或密码错误"))
        with self.assertRaisesRegex(SeedConflict, "demo-full.*企业管理员"):
            DemoSeeder(platform, client_factory=lambda: org_admin).ensure_members_and_apps(
                DemoSeeder(platform, client_factory=lambda: org_admin).ensure_platform_data()
            )
        self.assertEqual([], platform.password_reset_requests)

    # 覆盖已有成员实例被改名后仍按 owner 的 active_app_id 复用、不创建重复实例的路径。
    def test_existing_renamed_app_is_preserved(self):
        platform = FakeManagerAPI.from_complete_fixture(app_name="我的联调实例")
        DemoSeeder(platform, client_factory=FailIfCalledFactory()).ensure_members_and_apps(
            DemoSeeder(platform, client_factory=FailIfCalledFactory()).ensure_platform_data()
        )
        self.assertEqual("我的联调实例", platform.app("demo-full")["name"])
        self.assertEqual([], platform.member_app_requests)
```

- [ ] **Step 2: 运行成员和实例测试确认失败**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts/tests/test_local_seed_demo_seeder.py
```

Expected: 新测试 FAIL，提示 `ensure_members_and_apps` 或 fake 调用记录不存在。

- [ ] **Step 3: 实现成员与实例编排**

`ensure_members_and_apps(state)` 对每个企业执行：

```python
members = platform.get(f"/api/v1/organizations/{org_id}/members?limit=100&offset=0")["members"]
member = unique_by(members, "username", "member", f"{spec.code} 普通成员")

if member is None:
    org_api = client_factory()
    try:
        org_api.login(spec.code, "admin", "admin123")
    except APIError as exc:
        raise SeedConflict(f"{spec.code} 缺少 member，且无法使用默认企业管理员补齐: {exc.safe_message}") from exc
    if spec.needs_app:
        result = ensure_uncertain_write(
            create=lambda: org_api.post(f"/api/v1/organizations/{org_id}/members/onboard", onboard_body),
            lookup=lambda: find_member(platform, org_id, "member"),
        )["onboarding"]
        member, app, job_id = result["member"], result["app"], result["job_id"]
    else:
        member = ensure_uncertain_write(
            create=lambda: org_api.post(f"/api/v1/organizations/{org_id}/members", member_body),
            lookup=lambda: find_member(platform, org_id, "member"),
        )["member"]

if spec.needs_app and not member.get("active_app_id"):
    result = platform.post(
        f"/api/v1/organizations/{org_id}/members/{member['id']}/apps",
        {"app_name": "演示助手", "channel_type": "wechat", "version_id": general_version_id},
    )["member_app"]
```

实现 `unique_by()`：无匹配返回 `None`，唯一匹配返回对象，多匹配抛 `SeedConflict`。已有成员
必须是 `org_member` 且属于目标企业；否则报冲突。已有 `active_app_id` 时 GET app 详情并确认
`owner_user_id` 与 member ID 一致，不比较名称、不覆盖字段。

helper 使用以下完整语义：

```python
def unique_by(items, field_name, expected, target):
    matches = [item for item in items if item.get(field_name) == expected]
    if len(matches) > 1:
        raise SeedConflict(f"{target} 存在 {len(matches)} 条重复记录")
    return matches[0] if matches else None


def find_member(api, org_id, username):
    members = api.get(
        f"/api/v1/organizations/{org_id}/members?limit=100&offset=0"
    )["members"]
    return unique_by(members, "username", username, f"企业 {org_id} 成员 {username}")
```

保留 onboarding 和复建返回的 `job_id`，供 Task 4 统一等待。所有不确定写都先重新读取成员
或 active app 后再决定是否重试。

- [ ] **Step 4: 运行成员和实例测试确认通过**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts/tests/test_local_seed_demo_seeder.py
```

Expected: 成员权限、onboarding、复建和改名保留测试全部 PASS。

- [ ] **Step 5: 提交成员和实例补齐**

```bash
git add scripts/local_seed_demo/seeder.py scripts/tests/test_local_seed_demo_seeder.py
git commit -m "feat(local): 初始化演示成员与实例" \
  -m "按现有权限边界使用企业管理员创建缺失成员，并通过正式 onboarding 或复建接口创建实例。\n\n已有成员密码和改名后的实例保持不变。"
```

### Task 4: 创建智能客服并等待所有运行时就绪

**Files:**
- Modify: `scripts/local_seed_demo/seeder.py`
- Modify: `scripts/tests/test_local_seed_demo_seeder.py`

- [ ] **Step 1: 写 AICC 识别、启动和失败测试**

追加测试：

```python
class DemoSeederRuntimeTest(unittest.TestCase):
    # 覆盖缺少客服时创建、等待隐藏 app ready、再启动到 receiving 的完整路径。
    def test_missing_agent_waits_runtime_before_start(self):
        api = FakeManagerAPI.from_complete_fixture(with_agents=False)
        api.queue_app_states("aicc-app", [
            {"status": "starting", "runtime_phase": "starting"},
            {"status": "running", "runtime_phase": "ready"},
        ])
        state = DemoSeeder(api, client_factory=FailIfCalledFactory(), sleep=lambda _: None).run()
        self.assertEqual(["aicc-app"], api.ready_waits_before_start)
        self.assertEqual("active", state.agents["demo-full"]["status"])

    # 覆盖唯一客服被人工改名后复用且不重复创建的路径。
    def test_single_renamed_agent_is_reused(self):
        api = FakeManagerAPI.from_complete_fixture(agent_name="售前机器人")
        DemoSeeder(api, client_factory=FailIfCalledFactory(), sleep=lambda _: None).run()
        self.assertEqual([], api.agent_create_requests)

    # 覆盖多个非固定名客服时拒绝猜测资源归属的冲突路径。
    def test_multiple_renamed_agents_are_ambiguous(self):
        api = FakeManagerAPI.from_complete_fixture(agent_names=["客服甲", "客服乙"])
        with self.assertRaisesRegex(SeedConflict, "无法识别演示智能客服"):
            DemoSeeder(api, client_factory=FailIfCalledFactory(), sleep=lambda _: None).run()

    # 覆盖普通实例 Job 失败时立即输出 Job ID 和 last_error 的异常路径。
    def test_failed_job_stops_immediately(self):
        api = FakeManagerAPI.from_complete_fixture(job={
            "id": "job-1", "status": "failed", "last_error": "pull image failed",
        })
        with self.assertRaisesRegex(SeedRuntimeError, "job-1.*pull image failed"):
            DemoSeeder(api, client_factory=FailIfCalledFactory(), sleep=lambda _: None).run()

    # 覆盖等待超过 15 分钟时包含目标对象的超时路径。
    def test_runtime_wait_timeout_names_target(self):
        clock = FakeClock()
        api = FakeManagerAPI.from_complete_fixture(app_runtime_phase="starting")
        with self.assertRaisesRegex(SeedRuntimeError, "demo-full.*演示助手.*900"):
            DemoSeeder(api, client_factory=FailIfCalledFactory(), sleep=clock.sleep, monotonic=clock.monotonic).run()
```

- [ ] **Step 2: 运行 AICC 与轮询测试确认失败**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts/tests/test_local_seed_demo_seeder.py
```

Expected: 新测试 FAIL，提示 AICC ensure/wait 方法尚不存在。

- [ ] **Step 3: 实现 AICC 与状态门禁**

实现以下核心方法：

```python
def wait_job(self, job_id, target):
    return self._wait(target, lambda: self._job_state(job_id), timeout=900)

def _job_state(self, job_id):
    job = self.platform.get(f"/api/v1/jobs/{job_id}")["job"]
    if job["status"] == "failed":
        raise SeedRuntimeError(f"Job {job_id} 失败: {job.get('last_error', '')}")
    return job if job["status"] == "succeeded" else None

def _wait(self, target, check, timeout):
    deadline = self.monotonic() + timeout
    while True:
        result = check()
        if result is not None:
            return result
        if self.monotonic() >= deadline:
            raise SeedRuntimeError(f"等待 {target} 超时（{timeout}s）")
        self.sleep(5)

def wait_app_ready(self, app_id, target):
    def check():
        app = self.platform.get(f"/api/v1/apps/{app_id}")["app"]
        if app["status"] == "error":
            raise SeedRuntimeError(
                f"{target} 初始化失败: {app.get('last_error_status', '')}: {app.get('last_error_message', '')}"
            )
        return app if app["runtime_phase"] == "ready" and app["status"] in {"binding_waiting", "running"} else None
    return self._wait(target, check, timeout=900)

def wait_agent_receiving(self, agent_id, target):
    def check():
        agent = self.platform.get(f"/api/v1/aicc/agents/{agent_id}")["agent"]
        if agent["runtime_status"] == "error":
            raise SeedRuntimeError(f"{target} 运行时失败: {agent.get('runtime_message', '')}")
        return agent if agent["status"] == "active" and agent["runtime_status"] == "receiving" else None
    return self._wait(target, check, timeout=900)
```

`ensure_agents()` 对 AICC 企业 GET
`/api/v1/aicc/agents?org_id=<uuid>&limit=100&offset=0`：精确名优先、仅一条时复用、多条无精确名
时报冲突。缺失时先验证 allowlist 首项是智能客服版，再 POST 创建固定资料。拿到 `app_id` 后
先 `wait_app_ready()`；agent 非 active 时 POST `/:id/start`；最后
`wait_agent_receiving()`。

Job `status=succeeded` 后仍必须 GET app 验证真实运行时；`status=failed` 立即抛出含 Job ID
和 `last_error` 的异常。轮询间隔 5 秒、单目标 timeout 900 秒，使用注入的 `sleep` 与
`monotonic` 保证测试不真实等待。

- [ ] **Step 4: 运行所有 seeder 测试确认通过**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v \
  scripts/tests/test_local_seed_demo_client.py \
  scripts/tests/test_local_seed_demo_seeder.py
```

Expected: 所有 HTTP、幂等、权限、冲突、Job 和 runtime 测试 PASS。

- [ ] **Step 5: 提交 AICC 和运行时门禁**

```bash
git add scripts/local_seed_demo/seeder.py scripts/tests/test_local_seed_demo_seeder.py
git commit -m "feat(local): 等待演示运行时真实就绪" \
  -m "创建并启动两家企业的演示智能客服，统一等待普通实例 Job、runtime phase 和客服接待状态。\n\n初始化失败、资源歧义或超时会携带安全上下文终止 local-up。"
```

### Task 5: 增加 CLI 预检、入口和安全汇总

**Files:**
- Create: `scripts/local_seed_demo/cli.py`
- Create: `scripts/local-seed-demo.py`
- Create: `scripts/tests/test_local_seed_demo_cli.py`

- [ ] **Step 1: 写 Key 预检和退出码测试**

```python
class LocalSeedDemoCLITest(unittest.TestCase):
    # 覆盖缺少单个厂商 Key 时明确失败且不输出已存在 Key 值的安全路径。
    def test_missing_vendor_key_returns_failure_without_secret(self):
        env_file = self.write_env("DEEPSEEK_API_KEY=secret-deepseek\n")
        output = io.StringIO()
        code = main(root=self.tempdir, stdout=output, api_factory=FailIfCalledFactory())
        self.assertEqual(1, code)
        self.assertIn("SILICONFLOW_API_KEY", output.getvalue())
        self.assertNotIn("secret-deepseek", output.getvalue())

    # 覆盖完整配置执行 seeder 并打印固定数量汇总的正常路径。
    def test_complete_run_prints_safe_summary(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        output = io.StringIO()
        code = main(root=self.tempdir, stdout=output, api_factory=self.fake_factory)
        self.assertEqual(0, code)
        self.assertIn("2 个助手版本 / 3 个企业 / 2 个普通实例 / 2 个智能客服", output.getvalue())
        self.assertNotIn("token", output.getvalue().lower())

    # 覆盖业务冲突转换为非零退出码且不打印 traceback 的命令行路径。
    def test_seed_conflict_returns_failure(self):
        self.fake_factory.error = SeedConflict("demo-full 资源冲突")
        output = io.StringIO()
        self.assertEqual(1, main(root=self.tempdir, stdout=output, api_factory=self.fake_factory))
        self.assertEqual("❌ demo-full 资源冲突\n", output.getvalue())
```

- [ ] **Step 2: 运行 CLI 测试确认失败**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest -v scripts/tests/test_local_seed_demo_cli.py
```

Expected: FAIL，提示 `local_seed_demo.cli` 或 `main` 不存在。

- [ ] **Step 3: 实现预检和可执行入口**

`cli.py` 的 `main()`：

```python
def main(root=None, stdout=sys.stdout, api_factory=ManagerAPI):
    root = root or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    os.environ["NO_PROXY"] = os.environ["no_proxy"] = "localhost,127.0.0.1,.localhost"
    missing = missing_vendor_keys(os.path.join(root, ".env"))
    if missing:
        print(f"❌ 本地演示数据需要模型配置，缺少: {', '.join(missing)}", file=stdout)
        return 1
    try:
        api = api_factory("http://ocm.localhost")
        api.login("", "admin", "admin123")
        state = DemoSeeder(api, client_factory=lambda: api_factory("http://ocm.localhost")).run()
    except (APIError, SeedConflict, SeedRuntimeError, UncertainWrite) as exc:
        print(f"❌ {exc}", file=stdout)
        return 1
    print("✅ 本地演示数据就绪：2 个助手版本 / 3 个企业 / 2 个普通实例 / 2 个智能客服", file=stdout)
    return 0
```

`missing_vendor_keys()` 只解析 `.env` 的两个固定键，支持空行、注释、单/双引号和行尾注释，
返回缺失键名，不返回值。顶层不得 catch `KeyboardInterrupt`。

`scripts/local-seed-demo.py` 仅负责把 `scripts/` 加入 import 路径并执行：

```python
#!/usr/bin/env python3
"""本地 k3d 演示数据初始化入口。"""
from local_seed_demo.cli import main

if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 4: 运行全部 Python 定向测试**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest discover -s scripts/tests -p 'test_local_seed_demo_*.py' -v
python3 -m py_compile scripts/local-seed-demo.py scripts/local_seed_demo/*.py
```

Expected: 所有测试 PASS，`py_compile` 无输出且退出 0。

- [ ] **Step 5: 提交 CLI**

```bash
git add scripts/local_seed_demo/cli.py scripts/local-seed-demo.py scripts/tests/test_local_seed_demo_cli.py
git commit -m "feat(local): 增加演示数据初始化入口" \
  -m "为本地种子流程增加模型 Key 预检、严格退出码和脱敏汇总。\n\n缺少配置或资源初始化失败时阻断 local-up。"
```

### Task 6: 接入 local-up 并同步本地文档

**Files:**
- Modify: `Makefile:1,345-390`
- Modify: `CLAUDE.md:13-29`
- Modify: `docs/local-development.md:17-60`

- [ ] **Step 1: 先记录 Make dry-run 的失败基线**

Run:

```bash
make -n local-up | rg -n 'local-init-models|local-seed-demo'
```

Expected: 只出现 `local-init-models`，`local-seed-demo` 不存在，证明接线尚未实现。

- [ ] **Step 2: 修改 Makefile 调用顺序**

把 `local-seed-demo` 加入 `.PHONY`，在 `local-up` 的 `local-init-models` 后追加：

```make
	# 8) 演示数据：通过 manager 正式 API 补齐版本/企业/实例/AICC，并等待运行时真实就绪。
	#    缺厂商 key、资源冲突、Job 失败或超时均阻断 local-up；重复执行只补缺不覆盖。
	$(MAKE) local-seed-demo
```

新增内部目标：

```make
# local-seed-demo：通过 manager API 幂等补齐本地演示数据并等待运行时就绪。
# local-up 自动调用；失败后可修复配置并单独重跑，故不在 help 列出。
local-seed-demo:
	python3 scripts/local-seed-demo.py
```

更新 `local-up` 注释和最终成功文案，明确包含演示数据。`local-reset` 不增加重复调用，因为它
已经依赖 `local-up`。

- [ ] **Step 3: 更新调试账号与本地开发文档**

在 `CLAUDE.md` 账号表增加六行 manager 企业账号：

```markdown
| manager 后台 | http://ocm.localhost | `demo-full` / `admin` | `admin123` |
| manager 后台 | http://ocm.localhost | `demo-full` / `member` | `member123` |
| manager 后台 | http://ocm.localhost | `demo-app` / `admin` | `admin123` |
| manager 后台 | http://ocm.localhost | `demo-app` / `member` | `member123` |
| manager 后台 | http://ocm.localhost | `demo-aicc` / `admin` | `admin123` |
| manager 后台 | http://ocm.localhost | `demo-aicc` / `member` | `member123` |
```

紧邻表格注明：密码是 clean `local-reset` 的首次默认值；复用 `.k3d-data` 时脚本不会重置
修改过的密码。

在 `docs/local-development.md` 更新一键起停说明：`local-up`/`local-reset` 会创建两个版本、
三个企业、两个普通实例和两个 AICC；缺厂商 Key 或运行时未就绪会失败；修复后可单独执行
`make local-seed-demo` 继续补齐。

- [ ] **Step 4: 验证 Make 顺序和文档内容**

Run:

```bash
make -n local-up > /tmp/ocm-local-up-dry-run.txt
python3 - <<'PY'
from pathlib import Path
text = Path('/tmp/ocm-local-up-dry-run.txt').read_text()
assert text.index('scripts/local-init-models.py') < text.index('scripts/local-seed-demo.py')
print('local init order: PASS')
PY
rg -n 'demo-full|demo-app|demo-aicc|member123' CLAUDE.md docs/local-development.md
git diff --check
```

Expected: 输出 `local init order: PASS`；三个 code 和默认成员密码均可查到；
`git diff --check` 无输出。

- [ ] **Step 5: 提交 Makefile 与文档**

```bash
git add Makefile CLAUDE.md docs/local-development.md
git commit -m "feat(local): 自动初始化演示数据" \
  -m "在模型配置完成后创建三类演示企业并等待实例与智能客服真实就绪。\n\n同步记录企业调试账号、严格失败条件和幂等重跑方式。"
```

### Task 7: 定向回归、真实重建与浏览器验收

**Files:**
- Verify only; do not create a permanent full-suite E2E fixture

- [ ] **Step 1: 运行全部最小自动化回归**

Run:

```bash
PYTHONPATH=scripts python3 -m unittest discover -s scripts/tests -p 'test_local_seed_demo_*.py' -v
python3 -m py_compile scripts/local-seed-demo.py scripts/local_seed_demo/*.py
make -n local-up >/tmp/ocm-local-up-dry-run.txt
git diff --check
```

Expected: unittest 全部 PASS；其余命令退出 0。

- [ ] **Step 2: 真实执行 clean local-reset**

先确认根 `.env` 同时存在两个非空厂商 Key，但绝不打印值：

```bash
python3 - <<'PY'
from pathlib import Path
keys = {'DEEPSEEK_API_KEY': False, 'SILICONFLOW_API_KEY': False}
for raw in Path('.env').read_text().splitlines():
    line = raw.strip()
    if not line or line.startswith('#') or '=' not in line:
        continue
    key, value = line.split('=', 1)
    if key.strip() in keys and value.split('#', 1)[0].strip().strip('"\''):
        keys[key.strip()] = True
assert all(keys.values()), '缺少本地模型厂商 Key'
print('vendor key preflight: PASS')
PY
make local-reset
```

Expected: `make local-reset` 成功，末尾汇总 `2/3/2/2`，相关普通与 AICC runtime Pod Ready。
若失败，只针对失败对象修复并重跑 `make local-seed-demo`，不要先跑全量 E2E。

- [ ] **Step 3: 验证幂等重跑**

Run:

```bash
make local-up
```

Expected: 成功；日志显示固定对象被复用，没有创建第二份同名版本、企业、实例或客服。

- [ ] **Step 4: 使用真实无头浏览器定向验证管理页面**

使用仓库 Playwright 配置或浏览器自动化工具，逐项验证：

1. `admin/admin123`、企业标识空：助手版本页显示“本地通用助手版”“本地智能客服版”，企业页
   显示三个 demo code；
2. `demo-full/admin/admin123`：普通实例列表有一条，AICC 列表有一条且为接待中；
3. `demo-app/admin/admin123`：普通实例列表有一条，AICC 没有预置客服；
4. `demo-aicc/admin/admin123`：普通实例列表无普通实例，AICC 列表有一条且为接待中；
5. 三个企业的 `member/member123` 均可登录；`demo-full` 与 `demo-app` 成员能看到自己的实例。

Expected: 所有断言通过；浏览器 console 无与初始化数据相关的 error。

- [ ] **Step 5: 使用两个公开客服页面完成真实问答**

分别从 `demo-full` 与 `demo-aicc` 管理页面打开公开链接，在两个独立 browser context 中发送
“请介绍一下你能提供什么帮助”，等待真实回复完成。

Expected: 两个客服均返回非空助手消息，页面无 runtime unavailable/timeout；不得把公开 token
记录进文档、提交或测试快照。

- [ ] **Step 6: 交付前检查**

Run:

```bash
git status --short
git log --oneline -8
git diff HEAD~5 --stat
```

Expected: 工作区干净；提交按 HTTP 客户端、平台数据、成员实例、运行时、CLI、Make/文档边界
拆分；没有 Secret、浏览器临时文件或无关改动。若实施中产生新的相关修复，使用独立中文
Conventional Commit 并在交付说明列出验证结果。
