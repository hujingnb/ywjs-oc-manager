"""验证本地演示数据播种器对版本、企业与不确定写入的幂等处理。"""

import copy
import unittest

from local_seed_demo.client import UncertainWrite
from local_seed_demo.seeder import DemoSeeder, SeedConflict


# 测试密码仅用于校验本地初始化 DTO，不把完整字面值写入失败消息。
_ADMIN_PASSWORD = "admin" + "123"


class FakeManagerAPI:
    """以内存对象模拟 manager 正式 envelope，并记录所有可观察 HTTP 操作。"""

    def __init__(self, images=None, versions=None, organizations=None):
        """复制输入以隔离用例，并允许逐次配置写入中断是否已在服务端生效。"""
        self.images = copy.deepcopy(
            [{"id": "hermes-dev", "label": "Hermes Dev"}]
            if images is None
            else images
        )
        self.versions = copy.deepcopy(versions or [])
        self.organizations = copy.deepcopy(organizations or [])
        self.calls = []
        self.uncertain = {}
        self._next_version = 1
        self._next_organization = 1

    def interrupt(self, method, path, *applied_before_disconnect):
        """为指定写操作依次注入连接中断；True 表示服务端已完成该次写入。"""
        self.uncertain[(method, path)] = list(applied_before_disconnect)

    def get(self, path):
        """返回深拷贝，避免播种器意外直接修改 fake 的服务端状态。"""
        self.calls.append(("GET", path, None))

        # 镜像列表分支锁定正式 handler 的 images envelope。
        if path == "/api/v1/runtime-images":
            return {"images": copy.deepcopy(self.images)}

        # 版本列表分支用于按稳定名称重新查询创建结果。
        if path == "/api/v1/assistant-versions":
            return {"versions": copy.deepcopy(self.versions)}

        # 企业列表分支同时承担创建和 PATCH 不确定结果的回查。
        if path == "/api/v1/organizations?limit=100&offset=0":
            return {"organizations": copy.deepcopy(self.organizations)}

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
        return result


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
            self.assertIsNone(organization["aicc_agent_limit"])

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
        self.assertIsNone(state.organizations["demo-aicc"]["aicc_agent_limit"])
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


if __name__ == "__main__":
    unittest.main()
