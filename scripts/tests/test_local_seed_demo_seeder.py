"""验证本地演示数据播种器对版本、企业与不确定写入的幂等处理。"""

import copy
import unittest

from local_seed_demo import client as client_module
from local_seed_demo.client import APIError, UncertainWrite
from local_seed_demo.seeder import DemoSeeder, SeedConflict, SeedRuntimeError, SeedState


# 测试密码仅用于校验本地初始化 DTO，不把完整字面值写入失败消息。
_ADMIN_PASSWORD = "admin" + "123"


class FakeManagerAPI:
    """以内存对象模拟 manager 正式 envelope，并记录所有可观察 HTTP 操作。"""

    def __init__(
        self,
        images=None,
        versions=None,
        organizations=None,
        members=None,
        apps=None,
        agents=None,
        jobs=None,
    ):
        """复制输入以隔离用例，并允许逐次配置写入中断是否已在服务端生效。"""
        self.images = copy.deepcopy(
            [{"id": "hermes-dev", "label": "Hermes Dev"}]
            if images is None
            else images
        )
        self.versions = copy.deepcopy(versions or [])
        self.organizations = copy.deepcopy(organizations or [])
        self.members = copy.deepcopy(members or {})
        self.apps = copy.deepcopy(apps or {})
        self.agents = copy.deepcopy(agents or {})
        self.jobs = copy.deepcopy(jobs or {})
        self.calls = []
        self.uncertain = {}
        self.login_error = None
        self.member_response_overrides = {}
        self.agent_list_response_overrides = {}
        self.agent_list_response_queues = {}
        self.agent_detail_response_overrides = {}
        self.agent_create_response_overrides = []
        self.agent_start_response_overrides = {}
        self.app_state_queues = {}
        self.agent_state_queues = {}
        self.deadline_clock = None
        self.get_duration_queues = {}
        self._next_version = 1
        self._next_organization = 1
        self._next_member = 1
        self._next_app = 1
        self._next_job = 1
        self._next_agent = 1

    @classmethod
    def complete_runtime_fixture(cls, *, agent_names=None):
        """构造成员、普通实例和 AICC 都已存在的完整运行时事实。"""
        versions = [
            {"id": "general", "name": "本地通用助手版"},
            {"id": "customer", "name": "本地智能客服版"},
        ]
        organizations = [
            cls.organization(
                "org-full", "demo-full", "完整", ["customer", "general"],
                aicc_enabled=True,
            ),
            cls.organization("org-app", "demo-app", "普通", ["general"]),
            cls.organization(
                "org-aicc", "demo-aicc", "客服", ["customer"],
                aicc_enabled=True,
            ),
        ]
        members = {
            "org-full": [cls.member("member-full", "org-full", "member", active_app_id="app-full")],
            "org-app": [cls.member("member-app", "org-app", "member", active_app_id="app-app")],
            "org-aicc": [cls.member("member-aicc", "org-aicc", "member")],
        }
        apps = {
            "app-full": cls.app(
                "app-full", "org-full", "member-full", status="binding_waiting", runtime_phase="ready"
            ),
            "app-app": cls.app(
                "app-app", "org-app", "member-app", status="binding_waiting", runtime_phase="ready"
            ),
        }
        names = agent_names if agent_names is not None else ["演示智能客服"]
        agents = {"org-full": [], "org-aicc": []}
        for org_id in agents:
            for index, name in enumerate(names, start=1):
                app_id = f"aicc-app-{org_id}-{index}"
                agent_id = f"agent-{org_id}-{index}"
                apps[app_id] = cls.app(
                    app_id, org_id, "platform-admin", name,
                    status="binding_waiting", runtime_phase="ready",
                )
                agents[org_id].append(
                    cls.agent(
                        agent_id, org_id, app_id, name,
                        status="active", runtime_status="receiving",
                    )
                )
        return cls(
            versions=versions,
            organizations=organizations,
            members=members,
            apps=apps,
            agents=agents,
        )

    def queue_app_states(self, app_id, states):
        """为 app 详情轮询按顺序返回状态，最后一个状态持续保留。"""
        self.app_state_queues[app_id] = copy.deepcopy(states)

    def queue_agent_states(self, agent_id, states):
        """为 agent 详情轮询按顺序返回状态，最后一个状态持续保留。"""
        self.agent_state_queues[agent_id] = copy.deepcopy(states)

    def login(self, org_code, username, password):
        """模拟企业管理员登录；认证失败时保留 APIError 的正式安全字段。"""
        self.calls.append(("LOGIN", org_code, username))
        if self.login_error is not None:
            raise self.login_error
        self.logged_in_org = org_code
        return {"username": username, "role": "org_admin"}

    def interrupt(self, method, path, *applied_before_disconnect):
        """为指定写操作依次注入连接中断；True 表示服务端已完成该次写入。"""
        self.uncertain[(method, path)] = list(applied_before_disconnect)

    def get(self, path, deadline=None):
        """返回深拷贝，避免播种器意外直接修改 fake 的服务端状态。"""
        self.calls.append(("GET", path, None))
        durations = self.get_duration_queues.get(path)
        if durations:
            if self.deadline_clock is None:
                raise AssertionError("模拟 GET 耗时前必须配置虚拟时钟")
            duration = durations.pop(0)
            remaining = deadline - self.deadline_clock.monotonic()
            if remaining <= 0:
                raise client_module.RequestDeadlineExceeded(f"GET {path}")
            self.deadline_clock.sleep(min(duration, remaining))
            if duration >= remaining:
                raise client_module.RequestDeadlineExceeded(f"GET {path}")

        # 镜像列表分支锁定正式 handler 的 images envelope。
        if path == "/api/v1/runtime-images":
            return {"images": copy.deepcopy(self.images)}

        # 版本列表分支用于按稳定名称重新查询创建结果。
        if path == "/api/v1/assistant-versions":
            return {"versions": copy.deepcopy(self.versions)}

        # 企业列表分支同时承担创建和 PATCH 不确定结果的回查。
        if path == "/api/v1/organizations?limit=100&offset=0":
            return {"organizations": copy.deepcopy(self.organizations)}

        # 成员列表按企业隔离，响应沿用正式 members envelope。
        if path.startswith("/api/v1/organizations/") and path.endswith(
            "/members?limit=100&offset=0"
        ):
            org_id = path.split("/")[4]
            if org_id in self.member_response_overrides:
                return copy.deepcopy(self.member_response_overrides[org_id])
            return {"members": copy.deepcopy(self.members.get(org_id, []))}

        # 应用详情用于确认 active_app_id 的真实归属，名称和密码都不参与复用判断。
        if path.startswith("/api/v1/apps/"):
            app_id = path.rsplit("/", 1)[1]
            self._advance_state(self.apps, self.app_state_queues, app_id)
            return {"app": copy.deepcopy(self.apps[app_id])}

        # Job 详情返回真实 job envelope，并允许队列模拟异步终态推进。
        if path.startswith("/api/v1/jobs/"):
            job_id = path.rsplit("/", 1)[1]
            job = self.jobs[job_id]
            if isinstance(job, list):
                current = copy.deepcopy(job[0])
                if len(job) > 1:
                    job.pop(0)
                return {"job": current}
            return {"job": copy.deepcopy(job)}

        # AICC 列表严格按企业隔离，支持注入异常 200 envelope 验证禁止误写。
        if path.startswith("/api/v1/aicc/agents?org_id="):
            org_id = path.split("org_id=", 1)[1].split("&", 1)[0]
            queued = self.agent_list_response_queues.get(org_id)
            if queued:
                response = copy.deepcopy(queued[0])
                queued.pop(0)
                return response
            if org_id in self.agent_list_response_overrides:
                return copy.deepcopy(self.agent_list_response_overrides[org_id])
            return {"agents": copy.deepcopy(self.agents.get(org_id, []))}

        # AICC 详情用于确认启动不确定结果以及等待 receiving 事实。
        if path.startswith("/api/v1/aicc/agents/"):
            agent_id = path.rsplit("/", 1)[1]
            if agent_id in self.agent_detail_response_overrides:
                return copy.deepcopy(self.agent_detail_response_overrides[agent_id])
            org_id = next(
                key for key, items in self.agents.items()
                if any(item["id"] == agent_id for item in items)
            )
            self._advance_agent_state(org_id, agent_id)
            agent = next(item for item in self.agents[org_id] if item["id"] == agent_id)
            return {"agent": copy.deepcopy(agent)}

        raise AssertionError(f"出现未声明的 GET 路径: {path}")

    def post(self, path, body, authenticated=True):
        """模拟两个创建端点，并在配置的时机抛出 UncertainWrite。"""
        del authenticated
        safe_body = copy.deepcopy(body)
        self.calls.append(("POST", path, safe_body))

        # 版本创建分支只返回 AssistantVersionResult 字段，不回显请求专用的行业库 ID 数组。
        if path == "/api/v1/assistant-versions":
            created = {
                "id": f"version-{self._next_version}",
                "name": safe_body["name"],
                "description": safe_body["description"],
                "system_prompt": safe_body["system_prompt"],
                "image_id": safe_body["image_id"],
                "main_model": safe_body["main_model"],
                # service 会丢弃空模型槽位，因此正式响应中的 routing 是空对象。
                "routing": {},
                "skills": [],
                "revision": 1,
                "industry_knowledge_bases": [],
            }
            self._next_version += 1
            return self._finish_write("POST", path, created, self.versions, "version")

        # 企业创建分支剔除只写管理员密码，并补齐正式响应中的业务字段。
        if path == "/api/v1/organizations":
            created = self.organization(
                f"organization-{self._next_organization}",
                safe_body["code"],
                safe_body["name"],
                safe_body["assistant_version_ids"],
            )
            self._next_organization += 1
            return self._finish_write(
                "POST", path, created, self.organizations, "organization"
            )

        # 企业管理员普通创建成员，仅写 member，不隐式创建普通实例。
        if path.startswith("/api/v1/organizations/") and path.endswith("/members"):
            org_id = path.split("/")[4]
            created = self.member(
                f"member-{self._next_member}", org_id, safe_body["username"]
            )
            self._next_member += 1
            collection = self.members.setdefault(org_id, [])
            return self._finish_write("POST", path, created, collection, "member")

        # onboarding 的成员、微信渠道、应用和 job 在真实服务中同事务提交；fake 只暴露响应事实。
        if path.endswith("/members/onboard"):
            org_id = path.split("/")[4]
            member = self.member(
                f"member-{self._next_member}", org_id, safe_body["username"]
            )
            app = self.app(
                f"app-{self._next_app}", org_id, member["id"], safe_body["app_name"]
            )
            job_id = f"job-{self._next_job}"
            self._next_member += 1
            self._next_app += 1
            self._next_job += 1

            def apply_onboarding():
                member["active_app_id"] = app["id"]
                self.members.setdefault(org_id, []).append(copy.deepcopy(member))
                self.apps[app["id"]] = copy.deepcopy(app)

            return self._finish_composite(
                "POST",
                path,
                apply_onboarding,
                {"onboarding": {"member": member, "app": app, "job_id": job_id}},
            )

        # 已有成员复建实例由平台管理员调用，响应使用 member_app envelope。
        if "/members/" in path and path.endswith("/apps"):
            parts = path.split("/")
            org_id, member_id = parts[4], parts[6]
            app = self.app(
                f"app-{self._next_app}", org_id, member_id, safe_body["app_name"]
            )
            job_id = f"job-{self._next_job}"
            self._next_app += 1
            self._next_job += 1

            def apply_member_app():
                target = next(
                    item for item in self.members[org_id] if item["id"] == member_id
                )
                target["active_app_id"] = app["id"]
                self.apps[app["id"]] = copy.deepcopy(app)

            return self._finish_composite(
                "POST",
                path,
                apply_member_app,
                {"member_app": {"app": app, "job_id": job_id}},
            )

        # AICC 创建会同步返回主记录和隐藏 app id；隐藏 app 初始化状态由测试队列推进。
        if path == "/api/v1/aicc/agents":
            org_id = safe_body["org_id"]
            app_id = f"aicc-app-{self._next_agent}"
            created = self.agent(
                f"agent-{self._next_agent}", org_id, app_id, safe_body["name"],
                status="draft", runtime_status="starting",
            )
            self._next_agent += 1
            hidden_app = self.app(
                app_id, org_id, "platform-admin", safe_body["name"],
                status="starting", runtime_phase="starting",
            )
            collection = self.agents.setdefault(org_id, [])

            def apply_agent_create():
                collection.append(copy.deepcopy(created))
                self.apps[app_id] = copy.deepcopy(hidden_app)
                # 新建隐藏 app 首次 GET 仍在 starting，第二次开始稳定为 ready。
                self.queue_app_states(app_id, [
                    {"status": "starting", "runtime_phase": "starting"},
                    {"status": "binding_waiting", "runtime_phase": "ready"},
                ])

            response = self._finish_composite(
                "POST", path, apply_agent_create, {"agent": created}
            )
            if self.agent_create_response_overrides:
                return copy.deepcopy(self.agent_create_response_overrides.pop(0))
            return response

        # 启动只改变接待意图；不确定中断可发生在服务端状态落库前或后。
        if path.startswith("/api/v1/aicc/agents/") and path.endswith("/start"):
            agent_id = path.split("/")[5]
            org_id, agent = self._find_agent(agent_id)

            def apply_start():
                agent["status"] = "active"
                agent["runtime_status"] = "receiving"

            response = self._finish_agent_start(path, org_id, agent, apply_start)
            if path in self.agent_start_response_overrides:
                return copy.deepcopy(self.agent_start_response_overrides[path])
            return response

        raise AssertionError(f"出现未声明的 POST 路径: {path}")

    def patch(self, path, body):
        """模拟企业资料与 AICC 配置更新，所有更新都返回 organization envelope。"""
        safe_body = copy.deepcopy(body)
        self.calls.append(("PATCH", path, safe_body))
        org_id = path.split("/")[4]
        organization = next(item for item in self.organizations if item["id"] == org_id)

        # AICC 分支只更新三项配置，便于验证播种器没有改动企业资料。
        if path.endswith("/aicc-config"):
            updated = copy.deepcopy(organization)
            updated["aicc_enabled"] = safe_body["enabled"]
            # OrganizationResult 对 NULL 上限使用 omitempty；有限值才会出现在 JSON 响应中。
            if safe_body["agent_limit"] is None:
                updated.pop("aicc_agent_limit", None)
            else:
                updated["aicc_agent_limit"] = safe_body["agent_limit"]
            updated["industry_knowledge_base_ids"] = copy.deepcopy(
                safe_body["industry_knowledge_base_ids"]
            )
            return self._finish_patch(path, organization, updated)

        # 普通资料分支使用完整 DTO 覆盖对应字段，复现 handler 的 round-trip 约束。
        updated = copy.deepcopy(organization)
        updated.update(safe_body)
        return self._finish_patch(path, organization, updated)

    def _finish_write(self, method, path, created, collection, envelope):
        """按中断脚本决定先落库再断连、先断连不落库或正常返回。"""
        outcomes = self.uncertain.get((method, path), [])
        if outcomes:
            applied = outcomes.pop(0)
            if applied:
                collection.append(copy.deepcopy(created))
            raise UncertainWrite(f"{method} {path}")
        collection.append(copy.deepcopy(created))
        return {envelope: copy.deepcopy(created)}

    def _finish_patch(self, path, current, updated):
        """PATCH 中断时仅在 applied=True 时替换服务端对象。"""
        outcomes = self.uncertain.get(("PATCH", path), [])
        if outcomes:
            applied = outcomes.pop(0)
            if applied:
                current.clear()
                current.update(copy.deepcopy(updated))
            raise UncertainWrite(f"PATCH {path}")
        current.clear()
        current.update(copy.deepcopy(updated))
        return {"organization": copy.deepcopy(current)}

    def _finish_composite(self, method, path, apply, response):
        """模拟事务写入在响应前后断连，并确保 applied=True 只提交一次完整事实。"""
        outcomes = self.uncertain.get((method, path), [])
        if outcomes:
            if outcomes.pop(0):
                apply()
            raise UncertainWrite(f"{method} {path}")
        apply()
        return copy.deepcopy(response)

    def _finish_agent_start(self, path, org_id, agent, apply):
        """模拟启动响应中断，并保留真实 agent envelope。"""
        outcomes = self.uncertain.get(("POST", path), [])
        if outcomes:
            if outcomes.pop(0):
                apply()
            raise UncertainWrite(f"POST {path}")
        apply()
        current = next(item for item in self.agents[org_id] if item["id"] == agent["id"])
        return {"agent": copy.deepcopy(current)}

    @staticmethod
    def _advance_state(collection, queues, item_id):
        """把轮询队列当前状态合并进对象，并只消费非末尾状态。"""
        states = queues.get(item_id)
        if not states:
            return
        collection[item_id].update(copy.deepcopy(states[0]))
        if len(states) > 1:
            states.pop(0)

    def _advance_agent_state(self, org_id, agent_id):
        """推进指定 agent 的详情状态，不影响同企业其它 agent。"""
        states = self.agent_state_queues.get(agent_id)
        if not states:
            return
        agent = next(item for item in self.agents[org_id] if item["id"] == agent_id)
        agent.update(copy.deepcopy(states[0]))
        if len(states) > 1:
            states.pop(0)

    def _find_agent(self, agent_id):
        """按 id 返回 agent 所属企业和可变服务端对象。"""
        for org_id, agents in self.agents.items():
            for agent in agents:
                if agent["id"] == agent_id:
                    return org_id, agent
        raise AssertionError(f"不存在测试 agent: {agent_id}")

    @staticmethod
    def organization(org_id, code, name, version_ids, **overrides):
        """构造具备全部可编辑字段的企业响应，默认值贴近正式 service 结果。"""
        result = {
            "id": org_id,
            "code": code,
            "name": name,
            "contact_name": "",
            "contact_phone": "",
            "remark": "",
            "credit_warning_threshold": None,
            "max_instance_count": None,
            "knowledge_quota_bytes": 10_000,
            "default_app_knowledge_quota_bytes": 1_000,
            "assistant_version_ids": list(version_ids),
            "aicc_enabled": False,
            # 新企业数据库上限为 NULL，真实 JSON 因 omitempty 不返回 aicc_agent_limit。
            "industry_knowledge_base_ids": [],
        }
        result.update(copy.deepcopy(overrides))
        # OrganizationResult 对空上限始终 omitempty，即使构造参数显式传入 None 也不应留字段。
        if result.get("aicc_agent_limit", "not-present") is None:
            result.pop("aicc_agent_limit")
        return result

    @staticmethod
    def member(member_id, org_id, username, **overrides):
        """构造成员列表项，默认是目标企业内可登录的普通成员。"""
        result = {
            "id": member_id,
            "org_id": org_id,
            "username": username,
            "display_name": "演示成员",
            "role": "org_member",
            "status": "active",
        }
        result.update(copy.deepcopy(overrides))
        return result

    @staticmethod
    def app(app_id, org_id, owner_user_id, name="演示助手", **overrides):
        """构造应用详情；允许用例覆盖名称、状态和归属来验证安全复用。"""
        result = {
            "id": app_id,
            "org_id": org_id,
            "owner_user_id": owner_user_id,
            "name": name,
            "status": "draft",
        }
        result.update(copy.deepcopy(overrides))
        return result

    @staticmethod
    def agent(agent_id, org_id, app_id, name="演示智能客服", **overrides):
        """构造不含 public/widget token 的安全 AICC 管理响应。"""
        result = {
            "id": agent_id,
            "org_id": org_id,
            "app_id": app_id,
            "name": name,
            "status": "draft",
            "runtime_status": "starting",
            "privacy_mode": "notice",
            "retention_days": 180,
            "allowed_domains": [],
        }
        result.update(copy.deepcopy(overrides))
        return result


class FakeClock:
    """用虚拟单调时钟使 900 秒超时测试瞬时完成。"""

    def __init__(self):
        """时钟从零开始，每次 sleep 精确推进请求秒数。"""
        self.now = 0

    def sleep(self, seconds):
        """记录轮询等待而不阻塞测试进程。"""
        self.now += seconds

    def monotonic(self):
        """返回当前虚拟单调时间。"""
        return self.now


class FakeOrgAdminAPI:
    """模拟与平台客户端隔离的企业管理员客户端，并共享 manager 服务端数据。"""

    def __init__(self, backend, expected_code):
        """固定允许登录的企业 code；所有写入必须与该 code 对应的 org_id 一致。"""
        self.backend = backend
        self.expected_code = expected_code
        self.logged_in_code = None
        self.calls = []

    def login(self, org_code, username, password):
        """只有目标企业 admin 使用本地默认密码才能取得写权限。"""
        self.calls.append(("LOGIN", org_code, username))
        if org_code != self.expected_code:
            raise AssertionError("企业管理员客户端登录到了错误企业")
        if username != "admin" or password != _ADMIN_PASSWORD:
            raise AssertionError("企业管理员客户端登录凭据不符合本地约定")
        self.logged_in_code = org_code
        return {"username": username, "role": "org_admin"}

    def post(self, path, body, authenticated=True):
        """只实现成员相关企业写入，并在落库前校验登录企业与路径企业一致。"""
        del authenticated
        safe_body = copy.deepcopy(body)
        self.calls.append(("POST", path, safe_body))
        org_id = path.split("/")[4]
        expected_org = next(
            item for item in self.backend.organizations if item["code"] == self.expected_code
        )
        if self.logged_in_code != self.expected_code or org_id != expected_org["id"]:
            raise AssertionError("企业管理员写入路径与登录企业不一致")

        # AICC-only 企业只创建普通成员，不产生 app 或 job。
        if path.endswith("/members"):
            member = FakeManagerAPI.member(
                f"member-{org_id}", org_id, safe_body["username"]
            )
            self.backend.members.setdefault(org_id, []).append(copy.deepcopy(member))
            return {"member": member}

        # 普通实例企业通过 onboarding 同事务创建成员、应用和初始化 job。
        if path.endswith("/members/onboard"):
            member = FakeManagerAPI.member(
                f"member-{org_id}", org_id, safe_body["username"]
            )
            app = FakeManagerAPI.app(
                f"app-{org_id}", org_id, member["id"], safe_body["app_name"]
            )
            member["active_app_id"] = app["id"]
            self.backend.members.setdefault(org_id, []).append(copy.deepcopy(member))
            self.backend.apps[app["id"]] = copy.deepcopy(app)
            return {
                "onboarding": {
                    "member": member,
                    "app": app,
                    "job_id": f"job-{org_id}",
                }
            }

        raise AssertionError(f"企业管理员 fake 收到未声明路径: {path}")


class DemoSeederTest(unittest.TestCase):
    """覆盖平台数据播种的创建、补齐、冲突与不确定写入恢复。"""

    def _seeder(self, api):
        """构造仅使用平台客户端的播种器，Task 2 不创建企业侧资源。"""
        return DemoSeeder(api, client_factory=lambda *_args, **_kwargs: None)

    # 覆盖空环境首次播种：创建固定两版本和三企业，并保持完整企业 allowlist 的业务顺序。
    def test_empty_platform_creates_fixed_versions_and_organizations(self):
        api = FakeManagerAPI()

        state = self._seeder(api).ensure_platform_data()

        self.assertEqual({"本地通用助手版", "本地智能客服版"}, set(state.versions))
        self.assertEqual({"demo-full", "demo-app", "demo-aicc"}, set(state.organizations))
        customer_id = state.versions["本地智能客服版"]["id"]
        general_id = state.versions["本地通用助手版"]["id"]
        self.assertEqual(
            [customer_id, general_id],
            state.organizations["demo-full"]["assistant_version_ids"],
        )
        self.assertEqual(2, len(api.versions))
        self.assertEqual(3, len(api.organizations))

        version_posts = [
            body
            for method, path, body in api.calls
            if method == "POST" and path == "/api/v1/assistant-versions"
        ]
        self.assertEqual(2, len(version_posts))
        self.assertTrue(all(body["image_id"] == "hermes-dev" for body in version_posts))
        self.assertTrue(
            all(body["main_model"] == "deepseek-chat" for body in version_posts)
        )
        self.assertTrue(
            all(body["industry_knowledge_base_ids"] == [] for body in version_posts)
        )
        expected_version_text = {
            # 通用版本固定普通实例说明与可靠工作助手提示词。
            "本地通用助手版": (
                "本地普通实例演示版本",
                "你是专业、可靠的企业工作助手。",
            ),
            # 客服版本固定智能客服说明与友好客服提示词。
            "本地智能客服版": (
                "本地智能客服演示版本",
                "你是专业、友好的企业智能客服。",
            ),
        }
        self.assertEqual(
            expected_version_text,
            {
                body["name"]: (body["description"], body["system_prompt"])
                for body in version_posts
            },
        )
        expected_slots = {
            "vision", "compression", "web_extract", "session_search",
            "title_generation", "approval", "skills_hub", "mcp",
        }
        self.assertTrue(
            all(set(body["routing"]) == expected_slots for body in version_posts)
        )
        self.assertTrue(all(set(body["routing"].values()) == {""} for body in version_posts))

        organization_posts = [
            body
            for method, path, body in api.calls
            if method == "POST" and path == "/api/v1/organizations"
        ]
        self.assertEqual(3, len(organization_posts))
        self.assertTrue(
            all(body["admin_username"] == "admin" for body in organization_posts)
        )
        self.assertTrue(
            all(body["admin_display_name"] == "演示管理员" for body in organization_posts)
        )
        self.assertTrue(
            all(body["admin_password"] == _ADMIN_PASSWORD for body in organization_posts)
        )

        # 新企业的 AICC 上限来自真实 NULL/omitempty，首次单向开通必须继续提交不限。
        for code in ("demo-full", "demo-aicc"):
            organization = state.organizations[code]
            aicc_path = (
                f"/api/v1/organizations/{organization['id']}/aicc-config"
            )
            aicc_body = next(
                body
                for method, path, body in api.calls
                if method == "PATCH" and path == aicc_path
            )
            self.assertIsNone(aicc_body["agent_limit"])
            self.assertTrue(organization["aicc_enabled"])
            self.assertNotIn("aicc_agent_limit", organization)
            self.assertIsNone(organization.get("aicc_agent_limit"))

    # 覆盖新建版本进入 SeedState 时的正式响应形状，禁止把创建请求专用字段误作响应字段。
    def test_created_version_state_matches_assistant_version_result(self):
        state = self._seeder(FakeManagerAPI()).ensure_platform_data()

        expected_fields = {
            "id", "name", "description", "system_prompt", "image_id", "main_model",
            "routing", "skills", "revision", "industry_knowledge_bases",
        }
        # 两个固定版本都由同一正式 handler 返回，必须具备完全一致的响应结构。
        for version in state.versions.values():
            self.assertEqual(expected_fields, set(version))
            self.assertEqual({}, version["routing"])
            self.assertEqual([], version["skills"])
            self.assertEqual(1, version["revision"])
            self.assertEqual([], version["industry_knowledge_bases"])
            self.assertNotIn("industry_knowledge_base_ids", version)

    # 覆盖完整环境重复执行：既有内容和额外 allowlist 原样保留，且不发送任何写请求。
    def test_complete_platform_second_run_does_not_overwrite_or_write(self):
        api = FakeManagerAPI()
        seeder = self._seeder(api)
        state = seeder.ensure_platform_data()
        state.versions["本地通用助手版"]["description"] = "人工维护的版本说明"
        api.versions[0]["description"] = "人工维护的版本说明"
        extra_id = "version-extra"
        full = next(item for item in api.organizations if item["code"] == "demo-full")
        full["name"] = "人工维护的企业名"
        full["assistant_version_ids"].append(extra_id)
        api.calls.clear()

        second = seeder.ensure_platform_data()

        self.assertEqual(
            "人工维护的版本说明",
            second.versions["本地通用助手版"]["description"],
        )
        self.assertEqual("人工维护的企业名", second.organizations["demo-full"]["name"])
        self.assertIn(extra_id, second.organizations["demo-full"]["assistant_version_ids"])
        self.assertEqual([], [call for call in api.calls if call[0] in {"POST", "PATCH"}])

    # 覆盖既有企业只补缺失版本与 AICC 单向开通，同时完整保留企业资料和行业库授权。
    def test_existing_organizations_only_append_and_enable_required_aicc(self):
        versions = [
            {"id": "general", "name": "本地通用助手版", "description": "旧通用"},
            {"id": "customer", "name": "本地智能客服版", "description": "旧客服"},
        ]
        organizations = [
            FakeManagerAPI.organization(
                "org-full", "demo-full", "自定义完整企业", ["extra", "customer"],
                contact_name="联系人", contact_phone="13800000000", remark="保留备注",
                credit_warning_threshold=12, max_instance_count=9,
                knowledge_quota_bytes=88_000, default_app_knowledge_quota_bytes=7_000,
                aicc_enabled=False, aicc_agent_limit=0,
                industry_knowledge_base_ids=["industry-1"],
            ),
            FakeManagerAPI.organization(
                "org-app", "demo-app", "自定义普通企业", ["general"],
                aicc_enabled=True, aicc_agent_limit=0,
                industry_knowledge_base_ids=["industry-app"],
            ),
            FakeManagerAPI.organization(
                "org-aicc", "demo-aicc", "自定义客服企业", ["customer"],
                aicc_enabled=True, aicc_agent_limit=None,
                industry_knowledge_base_ids=["industry-unlimited"],
            ),
        ]
        api = FakeManagerAPI(versions=versions, organizations=organizations)

        state = self._seeder(api).ensure_platform_data()

        full = state.organizations["demo-full"]
        self.assertEqual(["extra", "customer", "general"], full["assistant_version_ids"])
        self.assertEqual("自定义完整企业", full["name"])
        self.assertEqual("联系人", full["contact_name"])
        self.assertEqual("13800000000", full["contact_phone"])
        self.assertEqual("保留备注", full["remark"])
        self.assertEqual(12, full["credit_warning_threshold"])
        self.assertEqual(9, full["max_instance_count"])
        self.assertEqual(88_000, full["knowledge_quota_bytes"])
        self.assertEqual(7_000, full["default_app_knowledge_quota_bytes"])
        self.assertTrue(full["aicc_enabled"])
        self.assertEqual(1, full["aicc_agent_limit"])
        self.assertEqual(["industry-1"], full["industry_knowledge_base_ids"])
        self.assertNotIn("aicc_agent_limit", state.organizations["demo-aicc"])
        self.assertEqual(
            ["industry-unlimited"],
            state.organizations["demo-aicc"]["industry_knowledge_base_ids"],
        )

        app_patches = [
            call
            for call in api.calls
            if call[0] == "PATCH" and "/org-app/" in call[1]
        ]
        self.assertEqual([], app_patches)
        profile_body = next(
            body
            for method, path, body in api.calls
            if method == "PATCH" and path == "/api/v1/organizations/org-full"
        )
        self.assertEqual(
            {
                "name", "contact_name", "contact_phone", "remark",
                "credit_warning_threshold", "max_instance_count",
                "knowledge_quota_bytes", "default_app_knowledge_quota_bytes",
                "assistant_version_ids",
            },
            set(profile_body),
        )

    # 覆盖镜像列表为空的冲突：没有可绑定镜像时不得创建任何版本。
    def test_empty_runtime_images_raise_seed_conflict(self):
        api = FakeManagerAPI(images=[])

        with self.assertRaisesRegex(SeedConflict, "runtime image"):
            self._seeder(api).ensure_platform_data()

        self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖镜像列表含空白与 null 候选：必须固定选择第一个非空 ID。
    def test_runtime_images_select_first_non_empty_id(self):
        api = FakeManagerAPI(
            images=[{"id": "  "}, {"id": None}, {"id": "later-image"}]
        )

        self._seeder(api).ensure_platform_data()

        version_posts = [
            body
            for method, path, body in api.calls
            if method == "POST" and path == "/api/v1/assistant-versions"
        ]
        self.assertTrue(all(body["image_id"] == "later-image" for body in version_posts))

    # 覆盖镜像 ID 全为空白或 JSON null 的冲突：不能字符串化 null 后作为合法 ID 提交。
    def test_runtime_images_without_non_empty_id_raise_seed_conflict(self):
        api = FakeManagerAPI(images=[{"id": "  "}, {"id": None}])

        with self.assertRaisesRegex(SeedConflict, "runtime image"):
            self._seeder(api).ensure_platform_data()

    # 覆盖同名版本出现多条时拒绝猜测稳定身份，并在错误中提供安全版本名。
    def test_duplicate_version_name_raises_seed_conflict(self):
        api = FakeManagerAPI(
            versions=[
                {"id": "v1", "name": "本地通用助手版"},
                {"id": "v2", "name": "本地通用助手版"},
            ]
        )

        with self.assertRaisesRegex(SeedConflict, "本地通用助手版"):
            self._seeder(api).ensure_platform_data()

    # 覆盖同 code 企业出现多条时拒绝继续补齐，避免向错误租户写入版本或 AICC 配置。
    def test_duplicate_organization_code_raises_seed_conflict(self):
        versions = [
            {"id": "general", "name": "本地通用助手版"},
            {"id": "customer", "name": "本地智能客服版"},
        ]
        api = FakeManagerAPI(
            versions=versions,
            organizations=[
                FakeManagerAPI.organization("o1", "demo-full", "企业一", ["customer", "general"]),
                FakeManagerAPI.organization("o2", "demo-full", "企业二", ["customer", "general"]),
            ],
        )

        with self.assertRaisesRegex(SeedConflict, "demo-full"):
            self._seeder(api).ensure_platform_data()

    # 覆盖版本创建响应中断但对象已出现：重新 GET 后继续，禁止重复 POST。
    def test_uncertain_create_uses_object_found_by_lookup(self):
        api = FakeManagerAPI()
        api.interrupt("POST", "/api/v1/assistant-versions", True)

        state = self._seeder(api).ensure_platform_data()

        self.assertIn("本地通用助手版", state.versions)
        version_posts = [
            call
            for call in api.calls
            if call[:2] == ("POST", "/api/v1/assistant-versions")
        ]
        self.assertEqual(2, len(version_posts))

    # 覆盖版本创建首次中断且确认不存在：最多补发一次创建，并使用补发响应继续。
    def test_uncertain_create_retries_once_after_confirmed_absence(self):
        api = FakeManagerAPI()
        api.interrupt("POST", "/api/v1/assistant-versions", False)

        state = self._seeder(api).ensure_platform_data()

        self.assertEqual(2, len(state.versions))
        version_posts = [
            call
            for call in api.calls
            if call[:2] == ("POST", "/api/v1/assistant-versions")
        ]
        self.assertEqual(3, len(version_posts))

    # 覆盖版本两次创建均中断：冲突需包含安全版本名并保留底层异常，不得第三次写入。
    def test_second_uncertain_version_create_reports_safe_target(self):
        api = FakeManagerAPI()
        api.interrupt("POST", "/api/v1/assistant-versions", False, False)

        with self.assertRaises(SeedConflict) as raised:
            self._seeder(api).ensure_platform_data()

        message = str(raised.exception)
        self.assertIn("本地通用助手版", message)
        self.assertIn("创建助手版本", message)
        self.assertIn("不确定", message)
        self.assertIsInstance(raised.exception.__cause__, UncertainWrite)
        version_posts = [
            call
            for call in api.calls
            if call[:2] == ("POST", "/api/v1/assistant-versions")
        ]
        self.assertEqual(2, len(version_posts))

    # 覆盖企业两次创建均中断：冲突需包含安全企业 code 并保留异常链，不得第三次写入。
    def test_second_uncertain_organization_create_reports_safe_target(self):
        versions = [
            {"id": "general", "name": "本地通用助手版"},
            {"id": "customer", "name": "本地智能客服版"},
        ]
        api = FakeManagerAPI(versions=versions)
        api.interrupt("POST", "/api/v1/organizations", False, False)

        with self.assertRaises(SeedConflict) as raised:
            self._seeder(api).ensure_platform_data()

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("创建企业", message)
        self.assertIn("不确定", message)
        self.assertIsInstance(raised.exception.__cause__, UncertainWrite)
        organization_posts = [
            call
            for call in api.calls
            if call[:2] == ("POST", "/api/v1/organizations")
        ]
        self.assertEqual(2, len(organization_posts))

    # 覆盖 AICC PATCH 两次均中断：冲突需包含企业 code 与操作类型并保留异常链。
    def test_second_uncertain_aicc_patch_reports_safe_target(self):
        versions = [
            {"id": "general", "name": "本地通用助手版"},
            {"id": "customer", "name": "本地智能客服版"},
        ]
        organizations = [
            FakeManagerAPI.organization(
                "full", "demo-full", "完整", ["customer", "general"],
                aicc_enabled=False, aicc_agent_limit=0,
            ),
            FakeManagerAPI.organization("app", "demo-app", "普通", ["general"]),
            FakeManagerAPI.organization(
                "aicc", "demo-aicc", "客服", ["customer"],
                aicc_enabled=True, aicc_agent_limit=1,
            ),
        ]
        api = FakeManagerAPI(versions=versions, organizations=organizations)
        path = "/api/v1/organizations/full/aicc-config"
        api.interrupt("PATCH", path, False, False)

        with self.assertRaises(SeedConflict) as raised:
            self._seeder(api).ensure_platform_data()

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("AICC", message)
        self.assertIn("不确定", message)
        self.assertIsInstance(raised.exception.__cause__, UncertainWrite)
        patch_calls = [call for call in api.calls if call[:2] == ("PATCH", path)]
        self.assertEqual(2, len(patch_calls))

    # 覆盖企业 PATCH 响应中断但目标状态已落库：回查确认后不重复 PATCH。
    def test_uncertain_patch_uses_target_state_found_by_lookup(self):
        versions = [
            {"id": "general", "name": "本地通用助手版"},
            {"id": "customer", "name": "本地智能客服版"},
        ]
        organizations = [
            FakeManagerAPI.organization(
                "full", "demo-full", "完整", ["customer", "general"],
                aicc_enabled=False, aicc_agent_limit=0,
            ),
            FakeManagerAPI.organization("app", "demo-app", "普通", ["general"]),
            FakeManagerAPI.organization(
                "aicc", "demo-aicc", "客服", ["customer"],
                aicc_enabled=True, aicc_agent_limit=1,
            ),
        ]
        api = FakeManagerAPI(versions=versions, organizations=organizations)
        path = "/api/v1/organizations/full/aicc-config"
        api.interrupt("PATCH", path, True)

        state = self._seeder(api).ensure_platform_data()

        self.assertTrue(state.organizations["demo-full"]["aicc_enabled"])
        patch_calls = [call for call in api.calls if call[:2] == ("PATCH", path)]
        self.assertEqual(1, len(patch_calls))

    # 覆盖企业 PATCH 首次中断且目标未出现：确认后只补发一次，并最终得到目标状态。
    def test_uncertain_patch_retries_once_after_target_absent(self):
        versions = [
            {"id": "general", "name": "本地通用助手版"},
            {"id": "customer", "name": "本地智能客服版"},
        ]
        organizations = [
            FakeManagerAPI.organization(
                "full", "demo-full", "完整", ["customer", "general"],
                aicc_enabled=False, aicc_agent_limit=0,
            ),
            FakeManagerAPI.organization("app", "demo-app", "普通", ["general"]),
            FakeManagerAPI.organization(
                "aicc", "demo-aicc", "客服", ["customer"],
                aicc_enabled=True, aicc_agent_limit=1,
            ),
        ]
        api = FakeManagerAPI(versions=versions, organizations=organizations)
        path = "/api/v1/organizations/full/aicc-config"
        api.interrupt("PATCH", path, False)

        state = self._seeder(api).ensure_platform_data()

        self.assertTrue(state.organizations["demo-full"]["aicc_enabled"])
        patch_calls = [call for call in api.calls if call[:2] == ("PATCH", path)]
        self.assertEqual(2, len(patch_calls))

    # 覆盖缺失客服智能体时的版本顺序保护：allowlist 首项错误必须点名企业和违规条件。
    def test_validate_aicc_version_order_rejects_wrong_first_allowlist_item(self):
        versions = {
            "本地通用助手版": {"id": "general", "name": "本地通用助手版"},
            "本地智能客服版": {"id": "customer", "name": "本地智能客服版"},
        }
        organizations = {
            "demo-full": FakeManagerAPI.organization(
                "full", "demo-full", "完整", ["general", "customer"]
            )
        }
        seeder = self._seeder(FakeManagerAPI())

        with self.assertRaises(SeedConflict) as raised:
            seeder.validate_aicc_version_order(versions, organizations, agents={})

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("allowlist 首项", message)
        self.assertIn("general", message)
        self.assertIn("customer", message)
        self.assertIn("本地智能客服版", message)

    # 覆盖空 allowlist 的冲突详情：实际首项必须使用固定安全文本而非空字符串。
    def test_validate_aicc_version_order_reports_empty_allowlist(self):
        versions = {
            "本地通用助手版": {"id": "general", "name": "本地通用助手版"},
            "本地智能客服版": {"id": "customer", "name": "本地智能客服版"},
        }
        organizations = {
            "demo-full": FakeManagerAPI.organization(
                "full", "demo-full", "完整", []
            )
        }
        seeder = self._seeder(FakeManagerAPI())

        with self.assertRaises(SeedConflict) as raised:
            seeder.validate_aicc_version_order(versions, organizations, agents={})

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("allowlist 首项", message)
        self.assertIn("<empty>", message)
        self.assertIn("customer", message)
        self.assertIn("本地智能客服版", message)

    def _member_state(self):
        """构造 Task 3 所需版本与三企业状态，隔离平台初始化对成员断言的噪声。"""
        from local_seed_demo.seeder import SeedState

        return SeedState(
            versions={
                "本地通用助手版": {"id": "general"},
                "本地智能客服版": {"id": "customer"},
            },
            organizations={
                "demo-full": {"id": "org-full", "code": "demo-full"},
                "demo-app": {"id": "org-app", "code": "demo-app"},
                "demo-aicc": {"id": "org-aicc", "code": "demo-aicc"},
            },
        )

    # 覆盖三企业均缺成员：两个普通实例企业走企业管理员 onboarding，AICC 企业只建成员。
    def test_missing_members_use_org_admin_and_create_only_required_apps(self):
        platform = FakeManagerAPI(
            organizations=[
                FakeManagerAPI.organization("org-full", "demo-full", "完整", []),
                FakeManagerAPI.organization("org-app", "demo-app", "普通", []),
                FakeManagerAPI.organization("org-aicc", "demo-aicc", "客服", []),
            ]
        )
        expected_codes = iter(("demo-full", "demo-app", "demo-aicc"))
        org_apis = []

        def client_factory():
            """每个缺成员企业都取得独立客户端，且服务端事实与平台只读客户端共享。"""
            org_api = FakeOrgAdminAPI(platform, next(expected_codes))
            org_apis.append(org_api)
            return org_api

        state = DemoSeeder(platform, client_factory).ensure_members_and_apps(
            self._member_state()
        )

        self.assertEqual(3, len(org_apis))
        self.assertEqual(
            [("LOGIN", code, "admin") for code in ("demo-full", "demo-app", "demo-aicc")],
            [call for org_api in org_apis for call in org_api.calls if call[0] == "LOGIN"],
        )
        onboard_posts = [
            call
            for org_api in org_apis
            for call in org_api.calls
            if call[0] == "POST" and call[1].endswith("/onboard")
        ]
        self.assertEqual(2, len(onboard_posts))
        for _method, _path, body in onboard_posts:
            self.assertEqual(
                {
                    "username": "member",
                    "display_name": "演示成员",
                    "password": "member" + "123",
                    "role": "org_member",
                    "app_name": "演示助手",
                    "channel_type": "wechat",
                    "version_id": "general",
                },
                body,
            )
        aicc_posts = [
            call
            for org_api in org_apis
            for call in org_api.calls
            if call[:2] == ("POST", "/api/v1/organizations/org-aicc/members")
        ]
        self.assertEqual(1, len(aicc_posts))
        self.assertEqual([], [call for call in platform.calls if call[0] == "POST"])
        self.assertEqual({"demo-full", "demo-app"}, set(state.apps))
        self.assertTrue(all(state.jobs[code] for code in state.apps))
        self.assertEqual(2, len(platform.apps))

    # 覆盖成员响应缺失 members 字段：格式异常不得误判为缺成员后执行企业写入。
    def test_member_list_missing_members_field_raises_without_writes(self):
        api = FakeManagerAPI()
        api.member_response_overrides["org-full"] = {}

        with self.assertRaises(SeedConflict) as raised:
            DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        self.assertIn("org-full", str(raised.exception))
        self.assertNotIn("{}", str(raised.exception))
        self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖 members 为 JSON null：非数组响应不得静默降级为空列表或触发成员创建。
    def test_member_list_null_members_raises_without_writes(self):
        api = FakeManagerAPI()
        api.member_response_overrides["org-full"] = {"members": None}

        with self.assertRaises(SeedConflict) as raised:
            DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        self.assertIn("org-full", str(raised.exception))
        self.assertNotIn("None", str(raised.exception))
        self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖既有成员无实例：平台管理员直接复建，不创建企业客户端也不依赖默认管理员密码。
    def test_existing_member_without_app_uses_platform_create_app(self):
        api = FakeManagerAPI(
            members={
                "org-full": [FakeManagerAPI.member("member-full", "org-full", "member")],
                "org-app": [FakeManagerAPI.member("member-app", "org-app", "member")],
                "org-aicc": [FakeManagerAPI.member("member-aicc", "org-aicc", "member")],
            }
        )

        def forbidden_factory():
            """既有成员分支触发工厂即失败，用于锁定无需企业凭据的权限路径。"""
            raise AssertionError("既有成员不应创建企业客户端")

        state = DemoSeeder(api, forbidden_factory).ensure_members_and_apps(
            self._member_state()
        )

        app_posts = [call for call in api.calls if call[0] == "POST" and call[1].endswith("/apps")]
        self.assertEqual(2, len(app_posts))
        self.assertEqual({"demo-full", "demo-app"}, set(state.apps))

    # 覆盖既有成员已有改名实例：只按 active_app_id GET 并复用，不覆盖人工名称或密码。
    def test_existing_renamed_app_is_reused_without_writes(self):
        api = FakeManagerAPI(
            members={
                "org-full": [FakeManagerAPI.member("m-full", "org-full", "member", active_app_id="app-full")],
                "org-app": [FakeManagerAPI.member("m-app", "org-app", "member", active_app_id="app-app")],
                "org-aicc": [FakeManagerAPI.member("m-aicc", "org-aicc", "member")],
            },
            apps={
                "app-full": FakeManagerAPI.app("app-full", "org-full", "m-full", "人工改名一"),
                "app-app": FakeManagerAPI.app("app-app", "org-app", "m-app", "人工改名二"),
            },
        )

        state = DemoSeeder(api, lambda: (_ for _ in ()).throw(AssertionError())).ensure_members_and_apps(
            self._member_state()
        )

        self.assertEqual("人工改名一", state.apps["demo-full"]["name"])
        self.assertEqual("人工改名二", state.apps["demo-app"]["name"])
        self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖默认管理员密码已改：登录 401 转为点名企业与管理员的安全冲突，不尝试旁路重置。
    def test_missing_member_with_changed_admin_password_raises_safe_conflict(self):
        api = FakeManagerAPI()
        api.login_error = APIError("POST /api/v1/auth/login", 401, "UNAUTHORIZED", "认证失败")

        with self.assertRaises(SeedConflict) as raised:
            DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("企业管理员", message)
        self.assertNotIn(_ADMIN_PASSWORD, message)
        self.assertEqual([], [call for call in api.calls if call[0] in {"PATCH", "DELETE"}])

    # 覆盖成员稳定身份与安全属性：重复用户名、错误角色、错误企业和停用状态均拒绝继续。
    def test_existing_member_conflicts_are_rejected(self):
        cases = (
            # 同 username 多条时不能任选成员继续创建实例。
            ("重复", [FakeManagerAPI.member("m1", "org-full", "member"), FakeManagerAPI.member("m2", "org-full", "member")]),
            # 目标账号若是企业管理员，不能把普通演示实例绑定给高权限账号。
            ("角色", [FakeManagerAPI.member("m1", "org-full", "member", role="org_admin")]),
            # 列表异常返回其他企业成员时必须阻断跨租户写入。
            ("归属", [FakeManagerAPI.member("m1", "other-org", "member")]),
            # 已停用账号不得被播种器静默启用或继续绑定实例。
            ("状态", [FakeManagerAPI.member("m1", "org-full", "member", status="disabled")]),
        )
        for expected, members in cases:
            # 每条用例独立构造服务端状态，确保冲突发生在 demo-full 首个目标。
            with self.subTest(expected=expected):
                api = FakeManagerAPI(members={"org-full": members})
                with self.assertRaisesRegex(SeedConflict, expected):
                    DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

    # 覆盖 active_app_id 指向错误 owner 或企业：应用详情不可信时不得复用或重新创建。
    def test_active_app_ownership_conflicts_are_rejected(self):
        cases = (
            # owner_user_id 不同意味着 active id 与成员列表事实矛盾。
            ("成员归属", FakeManagerAPI.app("app-1", "org-full", "other-member")),
            # org_id 不同意味着跨租户应用被错误挂到了当前成员列表。
            ("企业归属", FakeManagerAPI.app("app-1", "other-org", "m1")),
        )
        for expected, app in cases:
            # 两类归属冲突均应在任何写请求前失败。
            with self.subTest(expected=expected):
                api = FakeManagerAPI(
                    members={"org-full": [FakeManagerAPI.member("m1", "org-full", "member", active_app_id="app-1")]},
                    apps={"app-1": app},
                )
                with self.assertRaisesRegex(SeedConflict, expected):
                    DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())
                self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖 onboarding 已提交但响应丢失：按成员和 active app 事实恢复，job id 可以缺失。
    def test_uncertain_onboarding_recovers_committed_app_without_job_id(self):
        api = FakeManagerAPI()
        path = "/api/v1/organizations/org-full/members/onboard"
        api.interrupt("POST", path, True)

        state = DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        self.assertIn("demo-full", state.apps)
        self.assertFalse(state.jobs.get("demo-full"))
        self.assertEqual(1, len([call for call in api.calls if call[:2] == ("POST", path)]))

    # 覆盖普通成员创建与已有成员复建首次未提交：回查确认不存在后各自最多补发一次。
    def test_uncertain_member_and_app_writes_retry_once_after_absence(self):
        api = FakeManagerAPI()
        member_path = "/api/v1/organizations/org-aicc/members"
        api.interrupt("POST", member_path, False)
        # demo-full 预置成员以单独触发平台复建实例的不确定写恢复。
        api.members["org-full"] = [FakeManagerAPI.member("m-full", "org-full", "member")]
        app_path = "/api/v1/organizations/org-full/members/m-full/apps"
        api.interrupt("POST", app_path, False)

        DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        self.assertEqual(2, len([call for call in api.calls if call[:2] == ("POST", member_path)]))
        self.assertEqual(2, len([call for call in api.calls if call[:2] == ("POST", app_path)]))

    # 覆盖复建实例连续两次响应不确定：安全冲突包含 code、username 与 app 目标且不泄露密码。
    def test_second_uncertain_create_app_reports_safe_target(self):
        api = FakeManagerAPI(
            members={"org-full": [FakeManagerAPI.member("m-full", "org-full", "member")]}
        )
        path = "/api/v1/organizations/org-full/members/m-full/apps"
        api.interrupt("POST", path, False, False)

        with self.assertRaises(SeedConflict) as raised:
            DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("member", message)
        self.assertIn("演示助手", message)
        self.assertNotIn("member123", message)
        self.assertEqual(2, len([call for call in api.calls if call[:2] == ("POST", path)]))

    # 覆盖 onboarding 连续两次未确认提交：冲突使用固定 code、username、app 目标定位。
    def test_second_uncertain_onboarding_reports_safe_target(self):
        api = FakeManagerAPI()
        path = "/api/v1/organizations/org-full/members/onboard"
        api.interrupt("POST", path, False, False)

        with self.assertRaises(SeedConflict) as raised:
            DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        message = str(raised.exception)
        self.assertIn("demo-full", message)
        self.assertIn("member", message)
        self.assertIn("演示助手", message)
        self.assertNotIn("member123", message)
        self.assertEqual(2, len([call for call in api.calls if call[:2] == ("POST", path)]))

    # 覆盖纯成员创建连续两次未确认提交：冲突使用 code 与 username，不泄露成员密码。
    def test_second_uncertain_member_create_reports_safe_target(self):
        api = FakeManagerAPI(
            members={
                "org-full": [FakeManagerAPI.member("m-full", "org-full", "member", active_app_id="app-full")],
                "org-app": [FakeManagerAPI.member("m-app", "org-app", "member", active_app_id="app-app")],
            },
            apps={
                "app-full": FakeManagerAPI.app("app-full", "org-full", "m-full"),
                "app-app": FakeManagerAPI.app("app-app", "org-app", "m-app"),
            },
        )
        path = "/api/v1/organizations/org-aicc/members"
        api.interrupt("POST", path, False, False)

        with self.assertRaises(SeedConflict) as raised:
            DemoSeeder(api, lambda: api).ensure_members_and_apps(self._member_state())

        message = str(raised.exception)
        self.assertIn("demo-aicc", message)
        self.assertIn("member", message)
        self.assertNotIn("member123", message)
        self.assertEqual(2, len([call for call in api.calls if call[:2] == ("POST", path)]))


class DemoSeederRuntimeTest(unittest.TestCase):
    """覆盖普通实例和 AICC 从异步创建到真实可用的统一状态门禁。"""

    def _seeder(self, api, clock=None):
        """构造禁止企业客户端回退的完整 fixture 播种器。"""
        clock = clock or FakeClock()

        def forbidden_factory():
            """完整 fixture 不应因运行时等待再次创建企业客户端。"""
            raise AssertionError("运行时阶段不应创建企业客户端")

        return DemoSeeder(
            api,
            client_factory=forbidden_factory,
            sleep=clock.sleep,
            monotonic=clock.monotonic,
        )

    # 覆盖缺少客服时按固定 DTO 创建、等待隐藏 app ready、启动并等到 receiving 的完整路径。
    def test_missing_agents_are_created_after_hidden_apps_become_ready(self):
        api = FakeManagerAPI.complete_runtime_fixture(agent_names=[])

        state = self._seeder(api).run()

        self.assertEqual({"demo-full", "demo-aicc"}, set(state.agents))
        self.assertTrue(all(agent["status"] == "active" for agent in state.agents.values()))
        create_calls = [call for call in api.calls if call[:2] == ("POST", "/api/v1/aicc/agents")]
        self.assertEqual(2, len(create_calls))
        self.assertEqual(
            {
                "org_id": "org-full",
                "name": "演示智能客服",
                "scenario": "本地企业智能客服演示",
                "greeting": "您好，我是演示智能客服，请问有什么可以帮您？",
                "answer_boundary": "仅回答企业服务相关问题；不确定时明确告知用户。",
                "privacy_mode": "notice",
                "privacy_text": "",
                "retention_days": 180,
                "allowed_domains": [],
            },
            create_calls[0][2],
        )
        first_start_index = next(
            index for index, call in enumerate(api.calls)
            if call[0] == "POST" and call[1].endswith("/start")
        )
        self.assertTrue(
            any(
                call[:2] == ("GET", "/api/v1/apps/aicc-app-1")
                for call in api.calls[:first_start_index]
            )
        )

    # 覆盖唯一客服被人工改名：按单条资源安全复用，不覆盖名称也不重复创建。
    def test_single_renamed_agent_is_reused_without_create(self):
        api = FakeManagerAPI.complete_runtime_fixture(agent_names=["售前机器人"])

        state = self._seeder(api).run()

        self.assertEqual("售前机器人", state.agents["demo-full"]["name"])
        self.assertEqual([], [call for call in api.calls if call[:2] == ("POST", "/api/v1/aicc/agents")])

    # 覆盖多个非固定名客服：无法确定哪个是演示资源时必须停止且不写入。
    def test_multiple_renamed_agents_are_ambiguous_without_writes(self):
        api = FakeManagerAPI.complete_runtime_fixture(agent_names=["客服甲", "客服乙"])

        with self.assertRaisesRegex(SeedConflict, "demo-full.*无法识别演示智能客服"):
            self._seeder(api).run()

        self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖普通实例 Job 失败：立即抛出安全 Job ID 和 last_error，不再等待 app。
    def test_failed_job_stops_immediately_with_safe_error(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        api.jobs["00000000-0000-0000-0000-000000000001"] = {
            "id": "00000000-0000-0000-0000-000000000001",
            "status": "failed",
            "last_error": "pull image failed",
        }

        with self.assertRaisesRegex(
            SeedRuntimeError,
            "00000000-0000-0000-0000-000000000001.*pull image failed",
        ):
            self._seeder(api).wait_job(
                "00000000-0000-0000-0000-000000000001",
                "企业 demo-full 的演示助手",
            )

    # 覆盖 Job canceled 终态：立即携带 Job ID、状态和安全错误终止，不进入超时轮询。
    def test_canceled_job_stops_immediately(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        job_id = "00000000-0000-0000-0000-000000000003"
        api.jobs[job_id] = {
            "id": job_id,
            "status": "canceled",
            "last_error": "operator canceled",
        }

        with self.assertRaisesRegex(
            SeedRuntimeError, f"{job_id}.*canceled.*operator canceled"
        ):
            self._seeder(api, clock).wait_job(job_id, "企业 demo-full 的演示助手")

        self.assertEqual(0, clock.now)

    # 覆盖 Job 返回未知状态：契约异常必须立即失败，不能把拼写错误当作处理中。
    def test_unknown_job_status_is_rejected_immediately(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        job_id = "00000000-0000-0000-0000-000000000004"
        api.jobs[job_id] = {"id": job_id, "status": "mystery"}

        with self.assertRaisesRegex(SeedConflict, f"{job_id}.*状态.*mystery"):
            self._seeder(api, clock).wait_job(job_id, "企业 demo-full 的演示助手")

        self.assertEqual(0, clock.now)

    # 覆盖 Job succeeded 只代表任务终态：随后仍须读取 app 并确认双轴 ready 事实。
    def test_succeeded_job_is_followed_by_app_readiness_check(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        job_id = "00000000-0000-0000-0000-000000000002"
        api.jobs[job_id] = {"id": job_id, "status": "succeeded"}
        api.apps["app-full"].update({"status": "starting", "runtime_phase": "starting"})
        api.queue_app_states("app-full", [
            {"status": "starting", "runtime_phase": "starting"},
            {"status": "binding_waiting", "runtime_phase": "ready"},
        ])
        state = SeedState(
            versions={},
            organizations={},
            apps={"demo-full": copy.deepcopy(api.apps["app-full"])},
            jobs={"demo-full": job_id},
        )

        self._seeder(api).wait_apps_ready(state)

        job_index = api.calls.index(("GET", f"/api/v1/jobs/{job_id}", None))
        app_index = api.calls.index(("GET", "/api/v1/apps/app-full", None))
        self.assertLess(job_index, app_index)
        self.assertEqual("ready", state.apps["demo-full"]["runtime_phase"])

    # 覆盖普通实例共享 900 秒预算：Job 消耗约 500 秒后 app 只能使用剩余时间。
    def test_job_and_app_share_one_900_second_deadline(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        job_id = "00000000-0000-0000-0000-000000000005"
        api.jobs[job_id] = [
            *({"id": job_id, "status": "pending"} for _ in range(101)),
            {"id": job_id, "status": "succeeded"},
        ]
        api.apps["app-full"].update({"status": "starting", "runtime_phase": "starting"})
        state = SeedState(
            versions={},
            organizations={},
            apps={"demo-full": copy.deepcopy(api.apps["app-full"])},
            jobs={"demo-full": job_id},
        )

        with self.assertRaisesRegex(SeedRuntimeError, "demo-full.*900"):
            self._seeder(api, clock).wait_apps_ready(state)

        self.assertEqual(900, clock.now)

    # 覆盖 app 进入 error：输出稳定企业、资源名和服务端安全错误字段。
    def test_app_error_stops_with_initialization_details(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        api.apps["app-full"].update({
            "status": "error",
            "runtime_phase": "unknown",
            "last_error_status": "pulling_runtime_image",
            "last_error_message": "image unavailable",
        })

        with self.assertRaisesRegex(
            SeedRuntimeError,
            "demo-full.*演示助手.*pulling_runtime_image.*image unavailable",
        ):
            self._seeder(api).wait_app_ready(
                "app-full", "企业 demo-full 的演示助手", "org-full"
            )

    # 覆盖 agent runtime error：立即输出 runtime_message，且不包含管理 token 字段。
    def test_agent_runtime_error_stops_with_safe_message(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        agent.update({"runtime_status": "error", "runtime_message": "hidden app unavailable"})

        with self.assertRaisesRegex(
            SeedRuntimeError,
            "demo-full.*演示智能客服.*hidden app unavailable",
        ):
            self._seeder(api).wait_agent_receiving(
                agent["id"],
                "企业 demo-full 的演示智能客服",
                "org-full",
                agent["app_id"],
            )

    # 覆盖单目标等待 900 秒：错误必须包含企业、资源名和固定超时秒数。
    def test_runtime_timeout_names_target_and_900_seconds(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        api.apps["app-full"].update({"status": "starting", "runtime_phase": "starting"})

        with self.assertRaisesRegex(SeedRuntimeError, "demo-full.*演示助手.*900"):
            self._seeder(api, clock).wait_app_ready(
                "app-full", "企业 demo-full 的演示助手", "org-full"
            )

        self.assertEqual(900, clock.now)

    # 覆盖临近共享 deadline 的最后一次 GET：客户端只能使用剩余预算且超期映射为目标超时。
    def test_last_app_get_cannot_cross_shared_deadline(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        api.deadline_clock = clock
        api.get_duration_queues["/api/v1/apps/app-full"] = [2]
        deadline = clock.monotonic() + 1

        with self.assertRaisesRegex(SeedRuntimeError, "demo-full.*演示助手.*900"):
            self._seeder(api, clock).wait_app_ready(
                "app-full",
                "企业 demo-full 的演示助手",
                "org-full",
                deadline=deadline,
            )

        self.assertEqual(1, clock.now)
        self.assertEqual(
            1,
            len([
                call for call in api.calls
                if call[:2] == ("GET", "/api/v1/apps/app-full")
            ]),
        )

    # 覆盖完整数据第二次执行：只读确认两个普通实例和两个客服，不产生任何写请求。
    def test_second_run_is_read_only(self):
        api = FakeManagerAPI.complete_runtime_fixture()

        state = self._seeder(api).run()

        self.assertEqual(2, len(state.apps))
        self.assertEqual(2, len(state.agents))
        self.assertEqual([], [call for call in api.calls if call[0] in {"POST", "PATCH"}])

    # 覆盖 AICC 列表异常 200：缺字段不能被当作空列表触发客服创建。
    def test_malformed_agent_envelope_fails_without_writes(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        api.agent_list_response_overrides["org-full"] = {"unexpected": []}

        with self.assertRaisesRegex(SeedConflict, "demo-full.*智能客服列表响应格式异常"):
            self._seeder(api).run()

        self.assertEqual([], [call for call in api.calls if call[0] == "POST"])

    # 覆盖客服创建响应中断但服务端已落库：回查复用且只发送一次创建请求。
    def test_uncertain_agent_create_requeries_before_retry(self):
        api = FakeManagerAPI.complete_runtime_fixture(agent_names=[])
        api.interrupt("POST", "/api/v1/aicc/agents", True)

        state = self._seeder(api).run()

        self.assertEqual(2, len(state.agents))
        create_calls = [call for call in api.calls if call[:2] == ("POST", "/api/v1/aicc/agents")]
        self.assertEqual(2, len(create_calls))

    # 覆盖客服启动响应中断但 active 已落库：回查确认后不重复启动。
    def test_uncertain_agent_start_requeries_active_status(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        agent.update({"status": "draft", "runtime_status": "ready"})
        path = f"/api/v1/aicc/agents/{agent['id']}/start"
        api.interrupt("POST", path, True)

        state = self._seeder(api).run()

        self.assertEqual("active", state.agents["demo-full"]["status"])
        self.assertEqual(1, len([call for call in api.calls if call[:2] == ("POST", path)]))

    # 覆盖 AICC 隐藏 app 详情漂移到其他企业：租户冲突必须发生在 start 之前。
    def test_cross_org_hidden_app_fails_before_agent_start(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        agent.update({"status": "draft", "runtime_status": "ready"})
        api.apps[agent["app_id"]]["org_id"] = "org-aicc"

        with self.assertRaisesRegex(SeedConflict, "demo-full.*应用企业归属冲突"):
            self._seeder(api).run()

        self.assertEqual(
            [],
            [call for call in api.calls if call[0] == "POST" and call[1].endswith("/start")],
        )

    # 覆盖普通 app 初始归属正确但详情漂移：等待阶段必须按 state 中的企业事实再次校验。
    def test_cross_org_normal_app_detail_is_rejected(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        state = SeedState(
            versions={},
            organizations={},
            apps={"demo-full": copy.deepcopy(api.apps["app-full"])},
        )
        api.apps["app-full"]["org_id"] = "org-aicc"

        with self.assertRaisesRegex(SeedConflict, "demo-full.*应用企业归属冲突"):
            self._seeder(api).wait_apps_ready(state)

    # 覆盖普通 app owner 在详情轮询中漂移：即使企业未变也不得复用到其他成员。
    def test_normal_app_owner_drift_is_rejected(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        state = SeedState(
            versions={},
            organizations={},
            apps={"demo-full": copy.deepcopy(api.apps["app-full"])},
        )
        api.apps["app-full"]["owner_user_id"] = "member-other"

        with self.assertRaisesRegex(SeedConflict, "demo-full.*应用成员归属冲突"):
            self._seeder(api).wait_apps_ready(state)

    # 覆盖 agent 详情缺 runtime_status：异常 200 必须立即失败而非轮询到 900 秒。
    def test_agent_detail_missing_runtime_status_fails_immediately(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        malformed = copy.deepcopy(agent)
        malformed.pop("runtime_status")
        api.agent_detail_response_overrides[agent["id"]] = {"agent": malformed}

        with self.assertRaisesRegex(SeedConflict, "demo-full.*智能客服响应格式异常"):
            self._seeder(api, clock).run()

        self.assertEqual(0, clock.now)

    # 覆盖 create agent envelope 缺 runtime_status：不得继续读取隐藏 app 或发送 start。
    def test_agent_create_missing_runtime_status_fails_before_runtime_actions(self):
        api = FakeManagerAPI.complete_runtime_fixture(agent_names=[])
        api.agent_create_response_overrides.append({
            "agent": {
                "id": "agent-bad",
                "org_id": "org-full",
                "app_id": "aicc-app-1",
                "status": "draft",
            }
        })

        with self.assertRaisesRegex(SeedConflict, "demo-full.*智能客服响应格式异常"):
            self._seeder(api).run()

        self.assertEqual(
            [],
            [call for call in api.calls if call[0] == "POST" and call[1].endswith("/start")],
        )

    # 覆盖 start agent envelope 缺 status：写响应异常必须立即失败且不能进入 receiving 轮询。
    def test_agent_start_missing_status_fails_before_receiving_wait(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        agent.update({"status": "draft", "runtime_status": "ready"})
        path = f"/api/v1/aicc/agents/{agent['id']}/start"
        malformed = copy.deepcopy(agent)
        malformed.pop("status")
        api.agent_start_response_overrides[path] = {"agent": malformed}

        with self.assertRaisesRegex(SeedConflict, "demo-full.*智能客服响应格式异常"):
            self._seeder(api).run()

        detail_calls = [
            call for call in api.calls
            if call[:2] == ("GET", f"/api/v1/aicc/agents/{agent['id']}")
        ]
        self.assertEqual([], detail_calls)

    # 覆盖 active+starting 的启动响应不是完成条件：继续轮询详情直到 receiving。
    def test_start_response_active_starting_waits_for_receiving_detail(self):
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        agent.update({"status": "draft", "runtime_status": "ready"})
        path = f"/api/v1/aicc/agents/{agent['id']}/start"
        api.agent_start_response_overrides[path] = {
            "agent": {**copy.deepcopy(agent), "status": "active", "runtime_status": "starting"}
        }
        api.queue_agent_states(agent["id"], [
            {"status": "active", "runtime_status": "starting"},
            {"status": "active", "runtime_status": "receiving"},
        ])

        state = self._seeder(api).run()

        detail_calls = [
            call for call in api.calls
            if call[:2] == ("GET", f"/api/v1/aicc/agents/{agent['id']}")
        ]
        self.assertEqual(2, len(detail_calls))
        self.assertEqual("receiving", state.agents["demo-full"]["runtime_status"])

    # 覆盖单个 AICC 共享 900 秒预算：hidden app 消耗后 agent 只能使用剩余时间。
    def test_hidden_app_and_agent_share_one_900_second_deadline(self):
        clock = FakeClock()
        api = FakeManagerAPI.complete_runtime_fixture()
        agent = api.agents["org-full"][0]
        agent.update({"status": "active", "runtime_status": "starting"})
        api.queue_app_states(agent["app_id"], [
            *(
                {"status": "starting", "runtime_phase": "starting"}
                for _ in range(101)
            ),
            {"status": "binding_waiting", "runtime_phase": "ready"},
        ])

        with self.assertRaisesRegex(SeedRuntimeError, "demo-full.*900"):
            self._seeder(api, clock).run()

        self.assertEqual(900, clock.now)

    # 覆盖创建响应中断时并发出现唯一改名客服：回查不能把无关资源当作本次创建结果。
    def test_uncertain_create_ignores_concurrent_single_renamed_agent(self):
        api = FakeManagerAPI.complete_runtime_fixture(agent_names=[])
        unrelated = FakeManagerAPI.agent(
            "agent-unrelated",
            "org-full",
            "aicc-app-unrelated",
            "并发售前客服",
            status="active",
            runtime_status="receiving",
        )
        api.agents["org-full"].append(copy.deepcopy(unrelated))
        api.apps["aicc-app-unrelated"] = FakeManagerAPI.app(
            "aicc-app-unrelated",
            "org-full",
            "platform-admin",
            "并发售前客服",
            status="binding_waiting",
            runtime_phase="ready",
        )
        # 首次识别模拟并发资源尚不可见；创建中断后的回查才看到该改名资源。
        api.agent_list_response_queues["org-full"] = [{"agents": []}]
        api.interrupt("POST", "/api/v1/aicc/agents", False)

        state = self._seeder(api).run()

        full_creates = [
            call for call in api.calls
            if call[:2] == ("POST", "/api/v1/aicc/agents")
            and call[2]["org_id"] == "org-full"
        ]
        self.assertEqual(2, len(full_creates))
        self.assertEqual("演示智能客服", state.agents["demo-full"]["name"])


if __name__ == "__main__":
    unittest.main()
