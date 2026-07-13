# tests/test_ocops_server_skills.py
"""覆盖 server skills 端点（4 个）：list/install/delete/reload 的 HTTP handler 契约验证。

涉及真实文件系统操作（list/install/delete）通过
monkeypatch ocops.skills.SKILLS_DIR / BUILTIN_FILE 指到 tmp_path 打桩，避免操作 /opt/data。
reload 通过 monkeypatch ocops.skills.reload_skills 打桩，避免真连 hermes 8642 端点。
每条用例相邻中文注释说明覆盖场景。
"""
from __future__ import annotations

import io
import json
import zipfile
from pathlib import Path

import pytest
from jsonschema import validate
from starlette.testclient import TestClient


# ---------------------------------------------------------------------------
# 公共辅助
# ---------------------------------------------------------------------------

def _client(monkeypatch, tmp_path: Path) -> TestClient:
    """构造带固定 token 与 tmp SKILLS_DIR/BUNDLED_MANIFEST 的测试 client。

    OC_OPS_TOKEN 固定为 t0ken，SKILLS_DIR 指向 tmp_path/skills，
    BUNDLED_MANIFEST 指向 tmp_path/skills/.bundled_manifest（避免读 /opt 真实目录）。
    每次调用都重新从 ocops.server 导入 app，确保环境变量生效。
    """
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    # info.py 需要 OC_INFO_FILE，给一个不会 INTERNAL 的假文件
    info_file = tmp_path / "oc-image.json"
    info_file.write_text(json.dumps({
        "variant": "hermes-v2026.7.1",
        "hermes_upstream_ref": "abc",
        "built_at": "2026-05-29",
    }))
    monkeypatch.setenv("OC_INFO_FILE", str(info_file))

    # 将 SKILLS_DIR / BUNDLED_MANIFEST 指向 tmp 目录，隔离真实 /opt/data
    import ocops.skills as skills_mod
    skills_dir = tmp_path / "skills"
    skills_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.setattr(skills_mod, "SKILLS_DIR", skills_dir)
    monkeypatch.setattr(skills_mod, "BUNDLED_MANIFEST", skills_dir / ".bundled_manifest")

    from ocops.server import app
    return TestClient(app)


def _auth() -> dict:
    """返回带正确 Bearer token 的请求头，供所有 skills 端点测试使用。"""
    return {"Authorization": "Bearer t0ken"}


def _make_zip(filename: str = "skill.zip", inner: str = "main.py") -> bytes:
    """生成含单个文件的最小 zip 归档，用于 install 端点 multipart 上传测试。"""
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        zf.writestr(inner, "# skill entry\n")
    buf.seek(0)
    return buf.read()


# ---------------------------------------------------------------------------
# GET /oc/skills
# ---------------------------------------------------------------------------

def test_skills_list_managed_and_builtin(monkeypatch, tmp_path, ocops_schema):
    """list 端点：含 .oc-managed 目录 managed=True，出现在 .bundled_manifest 中的 builtin=True。

    构造两个含 SKILL.md 的 skill 目录：skill-a（带 .oc-managed，出现在内置基线）、skill-b（普通目录）；
    验证递归扫描后 list 返回的标注字段正确。
    """
    # 使用 _client 设置 token，再额外 monkeypatch SKILLS_DIR/BUNDLED_MANIFEST
    import ocops.skills as skills_mod
    c = _client(monkeypatch, tmp_path)
    skills_dir = skills_mod.SKILLS_DIR  # 已由 _client 指向 tmp_path/skills
    bundled_manifest = skills_dir / ".bundled_manifest"
    monkeypatch.setattr(skills_mod, "BUNDLED_MANIFEST", bundled_manifest)

    # skill-a：manager 安装的 skill——含 SKILL.md + .oc-managed 标记，不在内置基线。
    # SKILL.md 带 description，验证 list 端点回传该字段（供详情页展示）。
    skill_a = skills_dir / "skill-a"
    skill_a.mkdir()
    (skill_a / "SKILL.md").write_text("---\nname: skill-a\ndescription: A 测试技能\n---\n")
    (skill_a / ".oc-managed").write_text('{"source":"app-install"}\n')
    # skill-b：镜像内置 skill——无 .oc-managed，且「目录叶子名(skill-b) ≠ SKILL.md 规范名(skill-b-canonical)」，
    # 验证 builtin 判断走 SKILL.md 规范名匹配 .bundled_manifest，而对外 name 仍是目录叶子名。
    skill_b = skills_dir / "skill-b"
    skill_b.mkdir()
    (skill_b / "SKILL.md").write_text("---\nname: skill-b-canonical\n---\n")
    # 内置基线含 skill-b 的规范名（而非目录名），格式 "<规范名>:<hash>" 每行一个。
    bundled_manifest.write_text("skill-b-canonical:deadbeef0123\n")

    r = c.get("/oc/skills", headers=_auth())
    assert r.status_code == 200
    body = r.json()
    validate(body, ocops_schema("skills/list.schema.json"))
    # 返回体必须含 skills 列表
    assert "skills" in body
    by_name = {s["name"]: s for s in body["skills"]}
    # skill-a：managed=True（有 .oc-managed）、builtin=False（manager 安装，不在内置基线）
    assert by_name["skill-a"]["managed"] is True
    assert by_name["skill-a"]["builtin"] is False
    # description 取自 SKILL.md frontmatter，供详情页展示。
    assert by_name["skill-a"]["description"] == "A 测试技能"
    # skill-b：managed=False、builtin=True（规范名 skill-b-canonical 在基线，验证按规范名而非目录名匹配）；
    # 且对外 name 仍是目录叶子名 skill-b。
    assert by_name["skill-b"]["managed"] is False
    assert by_name["skill-b"]["builtin"] is True
    assert "skill-b" in by_name and "skill-b-canonical" not in by_name


def test_skills_list_empty_dir(monkeypatch, tmp_path, ocops_schema):
    """list 端点：SKILLS_DIR 为空目录时返回空 skills 列表。"""
    # 使用 _client 确保 OC_OPS_TOKEN 已设置并指向 tmp SKILLS_DIR
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/skills", headers=_auth())
    assert r.status_code == 200
    validate(r.json(), ocops_schema("skills/list.schema.json"))
    # 目录为空，返回 skills 为空列表
    assert r.json() == {"skills": []}


def test_skills_list_requires_auth(monkeypatch, tmp_path, ocops_schema):
    """list 端点：不带 Authorization 头 → 401 UNAUTHORIZED。"""
    c = _client(monkeypatch, tmp_path)
    r = c.get("/oc/skills")
    assert r.status_code == 401
    validate(r.json(), ocops_schema("common/error.schema.json"))
    assert r.json()["code"] == "UNAUTHORIZED"


# ---------------------------------------------------------------------------
# POST /oc/skills（install）
# ---------------------------------------------------------------------------

def test_skills_install_zip(monkeypatch, tmp_path, ocops_schema):
    """install 端点：上传合法 zip 归档 → 200，目标目录及 .oc-managed 出现。

    multipart form-data 包含 name 字段与 archive 文件字段。
    """
    c = _client(monkeypatch, tmp_path)
    import ocops.skills as skills_mod
    zip_bytes = _make_zip()
    r = c.post(
        "/oc/skills",
        headers=_auth(),
        data={"name": "my-skill"},
        files={"archive": ("my-skill.zip", zip_bytes, "application/zip")},
    )
    assert r.status_code == 200
    body = r.json()
    validate(body, ocops_schema("skills/action.schema.json"))
    # 返回体必须含 name 字段
    assert body.get("name") == "my-skill"
    # skills 目录下应出现 my-skill/ 目录及 .oc-managed 标记
    dest = skills_mod.SKILLS_DIR / "my-skill"
    assert dest.is_dir()
    assert (dest / ".oc-managed").exists()


def test_skills_install_zip_filename_without_ext(monkeypatch, tmp_path):
    """install 端点：multipart filename 不带 .zip 后缀（模拟 manager 实际行为）时仍按内容正确解压。

    复现真实 bug：manager 的 SkillInstall 把 multipart 文件名设为 skill 名（如 "Skill Vetter"，
    不含扩展名）。旧实现按文件名后缀判断格式，非 .zip 即当 tar 解压，导致 ClawHub 的 zip
    被 tarfile.open 失败、目录建了却为空、热装失败。修复后 _safe_extract 按内容（zip 魔数）判定，
    故 filename 无扩展名也能解出 SKILL.md。
    """
    c = _client(monkeypatch, tmp_path)
    import ocops.skills as skills_mod
    # 归档内含 SKILL.md（扁平结构，与真实 ClawHub zip 一致）
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        zf.writestr("SKILL.md", "---\nname: skill-vetter\n---\n")
    buf.seek(0)
    zip_bytes = buf.read()
    # 关键：filename 设为带空格、不含 .zip 后缀的 skill 名，模拟 manager CreateFormFile("archive", name)
    r = c.post(
        "/oc/skills",
        headers=_auth(),
        data={"name": "Skill Vetter"},
        files={"archive": ("Skill Vetter", zip_bytes, "application/octet-stream")},
    )
    assert r.status_code == 200
    dest = skills_mod.SKILLS_DIR / "Skill Vetter"
    # 目录非空：SKILL.md 必须真实解压落地（旧实现此处为空目录）
    assert (dest / "SKILL.md").exists()
    assert (dest / ".oc-managed").exists()


def test_skills_install_missing_name(monkeypatch, tmp_path):
    """install 端点：缺少 name 字段 → 400（OpsError BAD_REQUEST）。"""
    c = _client(monkeypatch, tmp_path)
    zip_bytes = _make_zip()
    r = c.post(
        "/oc/skills",
        headers=_auth(),
        files={"archive": ("x.zip", zip_bytes, "application/zip")},
    )
    assert r.status_code == 400


def test_skills_install_missing_archive(monkeypatch, tmp_path):
    """install 端点：缺少 archive 字段 → 400（OpsError BAD_REQUEST）。"""
    c = _client(monkeypatch, tmp_path)
    r = c.post(
        "/oc/skills",
        headers=_auth(),
        data={"name": "my-skill"},
    )
    assert r.status_code == 400


def test_skills_install_invalid_name(monkeypatch, tmp_path, ocops_schema):
    """install 端点：name 含路径分隔符 → _validate_name 拒绝 → 400 BAD_REQUEST。"""
    c = _client(monkeypatch, tmp_path)
    zip_bytes = _make_zip()
    r = c.post(
        "/oc/skills",
        headers=_auth(),
        data={"name": "../evil"},
        files={"archive": ("evil.zip", zip_bytes, "application/zip")},
    )
    # _validate_name 检查 "/" → OpsError(BAD_REQUEST) → HTTP 400
    assert r.status_code == 400
    validate(r.json(), ocops_schema("common/error.schema.json"))
    assert r.json()["code"] == "BAD_REQUEST"


def test_skills_install_idempotent(monkeypatch, tmp_path):
    """install 端点：目录已存在时先删后装（幂等覆盖），返回 200。"""
    c = _client(monkeypatch, tmp_path)
    import ocops.skills as skills_mod
    # 预先创建同名目录含旧文件，模拟已安装的 skill
    old_dir = skills_mod.SKILLS_DIR / "existing-skill"
    old_dir.mkdir(parents=True, exist_ok=True)
    (old_dir / "old.py").write_text("old content")

    zip_bytes = _make_zip(inner="main.py")
    r = c.post(
        "/oc/skills",
        headers=_auth(),
        data={"name": "existing-skill"},
        files={"archive": ("existing-skill.zip", zip_bytes, "application/zip")},
    )
    assert r.status_code == 200
    # 旧文件应被清除，新解压文件（main.py）存在
    dest = skills_mod.SKILLS_DIR / "existing-skill"
    assert (dest / "main.py").exists()
    # 旧文件不应再存在（整个目录被重建）
    assert not (dest / "old.py").exists()


# ---------------------------------------------------------------------------
# DELETE /oc/skills/{name}
# ---------------------------------------------------------------------------

def test_skills_delete_existing(monkeypatch, tmp_path, ocops_schema):
    """delete 端点：删除已存在的 skill 目录 → 200，目录消失。"""
    c = _client(monkeypatch, tmp_path)
    import ocops.skills as skills_mod
    # 预先创建 skill 目录
    skill_dir = skills_mod.SKILLS_DIR / "to-delete"
    skill_dir.mkdir(parents=True, exist_ok=True)
    (skill_dir / ".oc-managed").write_text('{"source":"app-install"}\n')

    r = c.delete("/oc/skills/to-delete", headers=_auth())
    assert r.status_code == 200
    # 目录应已被删除
    assert not skill_dir.exists()
    validate(r.json(), ocops_schema("skills/action.schema.json"))
    assert r.json().get("name") == "to-delete"


def test_skills_delete_nonexistent_idempotent(monkeypatch, tmp_path):
    """delete 端点：目录不存在时幂等成功 → 200，返回 name 字段。"""
    c = _client(monkeypatch, tmp_path)
    r = c.delete("/oc/skills/ghost-skill", headers=_auth())
    # 幂等：不存在时不报错，返回 200
    assert r.status_code == 200
    assert r.json().get("name") == "ghost-skill"


def test_skills_delete_invalid_name(monkeypatch, tmp_path):
    """delete 端点：name 为 "." 或 ".." → _validate_name 拒绝或路由不匹配 → 4xx。

    "." 在 URL 路径中会被 Starlette 规范化为父路径，导致匹配到 /oc/skills 的 GET/POST
    路由而非 DELETE {name} 路由，返回 405；".." 则路由不匹配返回 404。
    两者都属于请求被拒绝的正确行为（4xx），不会执行业务逻辑。
    """
    c = _client(monkeypatch, tmp_path)
    # "." → Starlette 路径规范化后不命中 DELETE {name} 路由 → 4xx（405 或 400）
    r_dot = c.delete("/oc/skills/.", headers=_auth())
    assert r_dot.status_code in (400, 404, 405)
    # ".." → 路径规范化后路由不匹配 → 404
    r_dotdot = c.delete("/oc/skills/..", headers=_auth())
    assert r_dotdot.status_code in (400, 404, 405)


# ---------------------------------------------------------------------------
# POST /oc/skills/reload
# ---------------------------------------------------------------------------

def test_skills_reload_returns_mocked_result(monkeypatch, tmp_path, ocops_schema):
    """reload 端点：monkeypatch reload_skills 返回假结果，验证 200 + 结果透传。

    避免真连容器内 127.0.0.1:8642，只测 handler 是否正确调用并透传结果。
    """
    import ocops.skills as skills_mod
    # 打桩 reload_skills，返回模拟的 hermes api_server 响应体
    fake_result = {"added": ["new-skill"], "removed": [], "total": 3}
    monkeypatch.setattr(skills_mod, "reload_skills", lambda: fake_result)

    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/skills/reload", headers=_auth())
    assert r.status_code == 200
    body = r.json()
    validate(body, ocops_schema("skills/reload.schema.json"))
    # handler 应透传 reload_skills 返回的全部字段
    assert body.get("added") == ["new-skill"]
    assert body.get("total") == 3


def test_skills_reload_requires_auth(monkeypatch, tmp_path):
    """reload 端点：不带 Authorization 头 → 401 UNAUTHORIZED。"""
    import ocops.skills as skills_mod
    monkeypatch.setattr(skills_mod, "reload_skills", lambda: {})
    c = _client(monkeypatch, tmp_path)
    r = c.post("/oc/skills/reload")
    assert r.status_code == 401
