"""以 manager 正式 API 幂等补齐本地演示所需的助手版本与企业配置。"""

from dataclasses import dataclass, field

from local_seed_demo.client import APIError, UncertainWrite


# manager 的助手版本 DTO 要求完整提交八个辅助路由槽位；空值表示回落到主模型。
_ROUTING_SLOTS = (
    "vision",
    "compression",
    "web_extract",
    "session_search",
    "title_generation",
    "approval",
    "skills_hub",
    "mcp",
)

# 企业普通 PATCH 是完整资料更新，新增 allowlist 时必须逐项从响应 round-trip。
_ORGANIZATION_PROFILE_FIELDS = (
    "name",
    "contact_name",
    "contact_phone",
    "remark",
    "credit_warning_threshold",
    "max_instance_count",
    "knowledge_quota_bytes",
    "default_app_knowledge_quota_bytes",
    "assistant_version_ids",
)


class SeedConflict(RuntimeError):
    """表示演示数据无法通过唯一稳定身份安全识别或补齐。"""


@dataclass(frozen=True)
class VersionSpec:
    """描述一个只在缺失时创建、存在时绝不覆盖的固定助手版本。"""

    name: str
    description: str
    system_prompt: str


@dataclass(frozen=True)
class OrganizationSpec:
    """描述企业稳定 code、版本顺序以及后续资源需求。"""

    code: str
    name: str
    version_names: tuple
    needs_app: bool
    needs_aicc: bool


@dataclass
class SeedState:
    """汇总各阶段已确认的服务端对象，供后续 app 与 AICC 播种阶段复用。"""

    versions: dict
    organizations: dict
    apps: dict = field(default_factory=dict)
    jobs: dict = field(default_factory=dict)
    agents: dict = field(default_factory=dict)


# 固定版本文案只用于首次创建；重复执行以服务端既有内容为准。
VERSION_SPECS = (
    VersionSpec(
        name="本地通用助手版",
        description="本地普通实例演示版本",
        system_prompt="你是专业、可靠的企业工作助手。",
    ),
    VersionSpec(
        name="本地智能客服版",
        description="本地智能客服演示版本",
        system_prompt="你是专业、友好的企业智能客服。",
    ),
)

# demo-full 的版本顺序具有业务语义：缺少客服智能体时必须优先使用智能客服版。
ORGANIZATION_SPECS = (
    OrganizationSpec(
        code="demo-full",
        name="完整演示企业",
        version_names=("本地智能客服版", "本地通用助手版"),
        needs_app=True,
        needs_aicc=True,
    ),
    OrganizationSpec(
        code="demo-app",
        name="普通实例演示企业",
        version_names=("本地通用助手版",),
        needs_app=True,
        needs_aicc=False,
    ),
    OrganizationSpec(
        code="demo-aicc",
        name="智能客服演示企业",
        version_names=("本地智能客服版",),
        needs_app=False,
        needs_aicc=True,
    ),
)


class DemoSeeder:
    """编排平台级演示数据；企业客户端工厂留给后续资源阶段使用。"""

    def __init__(self, platform, client_factory):
        """注入已认证平台客户端，避免播种逻辑接触 token 或登录密码。"""
        self.platform = platform
        self.client_factory = client_factory

    def ensure_platform_data(self):
        """按版本名和企业 code 唯一识别对象，并只执行缺失项或单向补齐。"""
        image_id = self._runtime_image_id()
        versions = self._ensure_versions(image_id)
        organizations = self._ensure_organizations(versions)
        return SeedState(versions=versions, organizations=organizations)

    def ensure_members_and_apps(self, state):
        """按企业权限补齐固定成员，并只为普通实例企业创建或复用应用。"""
        general_version_id = state.versions["本地通用助手版"]["id"]
        for spec in ORGANIZATION_SPECS:
            organization = state.organizations[spec.code]
            org_id = organization["id"]
            member = self.find_member(self.platform, org_id, "member")
            onboarding = None

            if member is None:
                # 平台管理员只有观察权限；缺成员时必须切换为对应企业管理员执行写入。
                org_api = self.client_factory()
                try:
                    org_api.login(spec.code, "admin", "admin" + "123")
                except APIError as error:
                    if error.status == 401:
                        # 只点名稳定企业 code 和账号身份，不回显密码、请求体或 token。
                        raise SeedConflict(
                            f"企业 {spec.code} 的企业管理员 admin 登录失败，"
                            "请确认本地默认管理员密码未被修改"
                        ) from error
                    raise

                if spec.needs_app:
                    onboarding = self._onboard_member(
                        org_api,
                        org_id,
                        spec.code,
                        general_version_id,
                    )
                    member = onboarding["member"]
                else:
                    member = self._create_member(org_api, org_id, spec.code)

            self._validate_member(member, org_id, spec.code)
            if not spec.needs_app:
                continue

            if onboarding is not None:
                app = onboarding["app"]
                job_id = onboarding.get("job_id")
            elif member.get("active_app_id"):
                app = self._get_member_app(member["active_app_id"], member, spec.code)
                job_id = None
            else:
                created = self._create_app_for_member(
                    org_id,
                    member,
                    spec.code,
                    general_version_id,
                )
                app = created["app"]
                job_id = created.get("job_id")

            self._validate_app(app, member, org_id, spec.code)
            state.apps[spec.code] = app
            # 响应丢失后无法从成员或应用详情恢复 job id；后续阶段应直接等待 app 事实。
            if job_id:
                state.jobs[spec.code] = job_id
        return state

    def find_member(self, api, org_id, username):
        """按企业成员列表中的精确 username 唯一查找，拒绝重复稳定身份。"""
        path = f"/api/v1/organizations/{org_id}/members?limit=100&offset=0"
        response = api.get(path)
        # 缺字段、JSON null、错误类型或非对象列表项都属于契约异常，不能误判成缺成员后写入。
        if (
            not isinstance(response, dict)
            or "members" not in response
            or not isinstance(response["members"], list)
            or any(not isinstance(item, dict) for item in response["members"])
        ):
            # 仅暴露稳定 org id 便于定位，不拼接可能含敏感成员信息的原始响应。
            raise SeedConflict(f"企业 {org_id} 的成员列表响应格式异常")
        members = response["members"]
        return self.unique_by(members, "username", username, "企业成员")

    def _create_member(self, org_api, org_id, code):
        """以企业管理员创建纯成员；不为仅 AICC 企业附带普通应用。"""
        path = f"/api/v1/organizations/{org_id}/members"
        body = {
            "username": "member",
            "display_name": "演示成员",
            "password": "member" + "123",
            "role": "org_member",
        }

        # 写入响应不确定时改用平台只读权限重新查询，避免依赖企业客户端登录状态。
        def lookup_member():
            found = self.find_member(self.platform, org_id, "member")
            return None if found is None else {"member": found}

        response = self.ensure_uncertain_write(
            lambda: org_api.post(path, body),
            lookup_member,
            f"创建企业成员 code={code} username=member",
        )
        return response["member"]

    def _onboard_member(self, org_api, org_id, code, version_id):
        """以企业管理员事务创建成员、微信渠道、普通应用和初始化 job。"""
        path = f"/api/v1/organizations/{org_id}/members/onboard"
        body = {
            "username": "member",
            "display_name": "演示成员",
            "password": "member" + "123",
            "role": "org_member",
            "app_name": "演示助手",
            "channel_type": "wechat",
            "version_id": version_id,
        }

        # onboarding 是事务写；成员和 active app 同时出现即可证明提交，job id 无法安全回查。
        def lookup_onboarding():
            found = self.find_member(self.platform, org_id, "member")
            if found is None or not found.get("active_app_id"):
                return None
            self._validate_member(found, org_id, code)
            app = self._get_member_app(found["active_app_id"], found, code)
            return {"onboarding": {"member": found, "app": app}}

        response = self.ensure_uncertain_write(
            lambda: org_api.post(path, body),
            lookup_onboarding,
            f"onboarding code={code} username=member app=演示助手",
        )
        return response["onboarding"]

    def _create_app_for_member(self, org_id, member, code, version_id):
        """使用平台管理员正式复建接口，为无 active app 的既有成员创建普通实例。"""
        path = f"/api/v1/organizations/{org_id}/members/{member['id']}/apps"
        body = {
            "app_name": "演示助手",
            "channel_type": "wechat",
            "version_id": version_id,
        }

        # 每次中断后重新列成员；只有 active_app_id 出现且详情归属正确才视为已提交。
        def lookup_member_app():
            found = self.find_member(self.platform, org_id, "member")
            if found is None or not found.get("active_app_id"):
                return None
            self._validate_member(found, org_id, code)
            app = self._get_member_app(found["active_app_id"], found, code)
            return {"member_app": {"app": app}}

        response = self.ensure_uncertain_write(
            lambda: self.platform.post(path, body),
            lookup_member_app,
            f"创建成员实例 code={code} username=member app=演示助手",
        )
        return response["member_app"]

    def _get_member_app(self, app_id, member, code):
        """读取 active app 并立即验证租户和 owner，禁止跨成员误复用。"""
        app = self.platform.get(f"/api/v1/apps/{app_id}")["app"]
        self._validate_app(app, member, member["org_id"], code)
        return app

    @staticmethod
    def _validate_member(member, org_id, code):
        """既有演示账号必须是目标企业内的 active 普通成员。"""
        if member.get("org_id") != org_id:
            raise SeedConflict(f"企业 {code} 的成员 member 企业归属冲突")
        if member.get("role") != "org_member":
            raise SeedConflict(f"企业 {code} 的成员 member 角色冲突")
        if member.get("status") != "active":
            raise SeedConflict(f"企业 {code} 的成员 member 状态冲突")

    @staticmethod
    def _validate_app(app, member, org_id, code):
        """应用详情必须同时属于目标企业和目标成员，名称及运行状态均保持服务端原值。"""
        if app.get("org_id") != org_id:
            raise SeedConflict(f"企业 {code} 的演示应用企业归属冲突")
        if app.get("owner_user_id") != member.get("id"):
            raise SeedConflict(f"企业 {code} 的演示应用成员归属冲突")

    def _runtime_image_id(self):
        """固定选择配置顺序中的首个非空镜像 ID；全部为空时拒绝创建版本。"""
        response = self.platform.get("/api/v1/runtime-images")
        images = response.get("images", [])
        for image in images:
            image_id = image.get("id")
            if isinstance(image_id, str) and image_id.strip():
                return image_id
        raise SeedConflict("runtime image 列表缺失有效 id")

    def _ensure_versions(self, image_id):
        """创建缺失版本；同名既有版本直接复用且不更新任何内容。"""
        listed = self._list_versions()
        versions = {}
        for spec in VERSION_SPECS:
            existing = self.unique_by(listed, "name", spec.name, "助手版本")
            if existing is None:
                body = {
                    "name": spec.name,
                    "description": spec.description,
                    "system_prompt": spec.system_prompt,
                    "image_id": image_id,
                    "main_model": "deepseek-chat",
                    "routing": {slot: "" for slot in _ROUTING_SLOTS},
                    "industry_knowledge_base_ids": [],
                }

                # 回查闭包把列表对象包装为与 POST 相同的 version envelope。
                def lookup_version(name=spec.name):
                    found = self.unique_by(
                        self._list_versions(), "name", name, "助手版本"
                    )
                    return None if found is None else {"version": found}

                response = self.ensure_uncertain_write(
                    lambda body=body: self.platform.post(
                        "/api/v1/assistant-versions", body
                    ),
                    lookup_version,
                    f"创建助手版本 {spec.name}",
                )
                existing = response["version"]
                listed.append(existing)
            versions[spec.name] = existing
        return versions

    def _ensure_organizations(self, versions):
        """创建三家企业，并对既有企业只追加版本和单向开启所需 AICC。"""
        listed = self._list_organizations()
        organizations = {}
        for spec in ORGANIZATION_SPECS:
            required_ids = [versions[name]["id"] for name in spec.version_names]
            existing = self.unique_by(listed, "code", spec.code, "企业")
            if existing is None:
                body = {
                    "name": spec.name,
                    "code": spec.code,
                    "assistant_version_ids": required_ids,
                    "admin_username": "admin",
                    "admin_display_name": "演示管理员",
                    "admin_password": "admin" + "123",
                }

                # 企业创建中断后只按不可变 code 回查，禁止依据展示名称猜测。
                def lookup_organization(code=spec.code):
                    found = self._lookup_organization(code)
                    return None if found is None else {"organization": found}

                response = self.ensure_uncertain_write(
                    lambda body=body: self.platform.post(
                        "/api/v1/organizations", body
                    ),
                    lookup_organization,
                    f"创建企业 {spec.code}",
                )
                existing = response["organization"]
                listed.append(existing)
            else:
                existing = self._append_missing_versions(existing, required_ids)

            if spec.needs_aicc:
                existing = self._enable_required_aicc(existing)
            organizations[spec.code] = existing
        return organizations

    def _append_missing_versions(self, organization, required_ids):
        """保持既有 allowlist 原顺序和额外版本，仅把规格中缺失的 ID 追加到末尾。"""
        current_ids = list(organization.get("assistant_version_ids") or [])
        desired_ids = current_ids + [item for item in required_ids if item not in current_ids]
        if desired_ids == current_ids:
            return organization

        body = self._organization_profile_body(organization)
        body["assistant_version_ids"] = desired_ids
        path = f"/api/v1/organizations/{organization['id']}"
        code = organization["code"]

        # 只有回查到同 code 且 allowlist 已精确达到目标，才能判定 PATCH 已生效。
        def lookup_profile():
            found = self._lookup_organization(code)
            if found is None or found.get("assistant_version_ids") != desired_ids:
                return None
            return {"organization": found}

        response = self.ensure_uncertain_write(
            lambda: self.platform.patch(path, body),
            lookup_profile,
            f"补齐企业版本 {code}",
        )
        return response["organization"]

    def _enable_required_aicc(self, organization):
        """只开启 AICC 并把有限数量下限补到 1；None 继续表示不限。"""
        current_enabled = bool(organization.get("aicc_enabled", False))
        current_limit = organization.get("aicc_agent_limit")
        desired_limit = None if current_limit is None else max(current_limit, 1)
        if current_enabled and desired_limit == current_limit:
            return organization

        body = {
            "enabled": True,
            "agent_limit": desired_limit,
            "industry_knowledge_base_ids": list(
                organization.get("industry_knowledge_base_ids") or []
            ),
        }
        path = f"/api/v1/organizations/{organization['id']}/aicc-config"
        code = organization["code"]

        # AICC 回查同时比较开通状态、上限和行业库，防止把部分写入误判为完成。
        def lookup_aicc():
            found = self._lookup_organization(code)
            if found is None:
                return None
            reached = (
                bool(found.get("aicc_enabled", False))
                and found.get("aicc_agent_limit") == desired_limit
                and list(found.get("industry_knowledge_base_ids") or [])
                == body["industry_knowledge_base_ids"]
            )
            return {"organization": found} if reached else None

        response = self.ensure_uncertain_write(
            lambda: self.platform.patch(path, body),
            lookup_aicc,
            f"补齐企业 AICC {code}",
        )
        return response["organization"]

    @staticmethod
    def ensure_uncertain_write(create, lookup, target_context):
        """写入中断后先回查；二次中断用安全目标上下文转换为可定位的冲突。"""
        try:
            return create()
        except UncertainWrite:
            existing = lookup()
            if existing is not None:
                return existing
            try:
                return create()
            except UncertainWrite as error:
                # 上下文由固定操作和稳定 name/code 组成，异常链仅保留脱敏的网络操作名。
                raise SeedConflict(f"{target_context} 第二次写入结果仍不确定") from error

    def validate_aicc_version_order(
        self, versions, organizations=None, agents=None
    ):
        """客服智能体缺失时，保证 AICC 企业首个版本是固定智能客服版。"""
        if isinstance(versions, SeedState):
            state = versions
            versions = state.versions
            organizations = state.organizations
            agents = state.agents
        organizations = organizations or {}
        agents = agents or {}
        customer_id = versions["本地智能客服版"]["id"]

        for spec in ORGANIZATION_SPECS:
            if not spec.needs_aicc or spec.code not in organizations:
                continue
            # 已有客服智能体不会依赖 allowlist 默认首项，因而无需阻断后续幂等执行。
            if agents.get(spec.code):
                continue
            allowlist = organizations[spec.code].get("assistant_version_ids") or []
            if not allowlist or allowlist[0] != customer_id:
                # 实际首项只来自服务端版本 ID；空列表使用固定安全文本，便于定位配置而不泄露请求体。
                actual_first = allowlist[0] if allowlist else "<empty>"
                raise SeedConflict(
                    f"企业 {spec.code} 缺少客服智能体时，allowlist 首项实际为 "
                    f"{actual_first}，期望为本地智能客服版（{customer_id}）"
                )

    def _list_versions(self):
        """读取正式 versions envelope，并复制为可在本轮追加的普通列表。"""
        return list(self.platform.get("/api/v1/assistant-versions").get("versions", []))

    def _list_organizations(self):
        """固定以单页 100 条读取本地演示企业，符合平台列表正式查询参数。"""
        response = self.platform.get("/api/v1/organizations?limit=100&offset=0")
        return list(response.get("organizations", []))

    def _lookup_organization(self, code):
        """每次不确定写入后重新读取，并再次执行 code 唯一性检查。"""
        return self.unique_by(
            self._list_organizations(), "code", code, "企业"
        )

    @staticmethod
    def unique_by(items, field_name, expected, target):
        """按稳定字段精确匹配；重复结果属于冲突，不选择任意一条继续写入。"""
        matches = [item for item in items if item.get(field_name) == expected]
        if len(matches) > 1:
            raise SeedConflict(f"{target}稳定身份 {expected} 存在重复记录")
        return matches[0] if matches else None

    # 保留 Task 2 已有内部调用兼容性；新阶段公开使用语义更明确的 unique_by。
    _unique_by = unique_by

    @staticmethod
    def _organization_profile_body(organization):
        """从响应构造完整企业 PATCH DTO，缺省 omitempty 字段恢复其零值语义。"""
        string_fields = {"contact_name", "contact_phone", "remark"}
        nullable_fields = {"credit_warning_threshold", "max_instance_count"}
        body = {}
        for field_name in _ORGANIZATION_PROFILE_FIELDS:
            if field_name in string_fields:
                body[field_name] = organization.get(field_name, "")
            elif field_name in nullable_fields:
                body[field_name] = organization.get(field_name)
            else:
                body[field_name] = organization[field_name]
        return body
