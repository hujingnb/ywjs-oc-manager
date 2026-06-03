# 容器侧 Skill 支持（解 zip / 自创备份恢复 / oc-ops 端点 / reload）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 hermes 容器与 ops 工具链支持实例级 skill 的热装/热删/对账/恢复：`render_skills.py` 支持解 zip（+zip-slip 校验）；`oc-sync`/`oc-restore` 备份与恢复 hermes 自创 skill；镜像构建期生成内置 skill 清单；oc-ops 新增 `/oc/skills` 端点（列/装/删/reload）；manager 侧 ocops client 增对应方法。其中 **reload 程序化触发方式由第一个 spike task 在真实容器内确定**。

**Architecture:** 容器内 oc-ops（Starlette）暴露 skill 端点，manager 经 ocops client 调用（热装/热删/列/reload）。reload 触发优先用 hermes 本地 REST（localhost:8642）或 `hermes skill` CLI（spike 确定）。自创 skill（无 `.oc-managed` 且不在 `skills-builtin.json`）由 oc-sync 备份到 S3 `apps/<id>/skills/`、oc-restore 还原。

**Tech Stack:** Python 3.13 / Starlette（oc-ops）/ bash（ops）/ Docker / Go（manager ocops client）/ pytest / testify。

「Hermes Skill 市场」功能 Plan 5（共 6 个）。**依赖 Plan 1 已合入**。**Plan 3（per-app 装卸）依赖本 plan 的 oc-ops 端点与 ocops client 方法。** 本 plan 只提供容器侧能力与 manager client，不含 app_skills 数据流（属 Plan 3/4）。

容器侧测试用 pytest（容器目录已有 `tests/`），bash 用 `runtime/ops/test/unit_test.sh`，Go 用 testify。容器侧无 testify，用原生 `assert` + pytest fixtures。

---

## File Structure

修改（容器镜像 `runtime/hermes/hermes-v2026.5.16/`）：
- `renderer/render_skills.py` — 加 zip 解压分支 + zip-slip 校验
- `tests/test_render_skills.py` — 加 zip 用例
- `ocops/server.py` — 注册 `/oc/skills*` 路由
- `Dockerfile` — 构建期生成 `/opt/skills-builtin.json`
新建：
- `runtime/hermes/hermes-v2026.5.16/ocops/skills.py` — skill 端点业务逻辑（list/install/delete/reload）
- `runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_skills.py`

修改（ops `runtime/ops/`）：
- `bin/oc-sync` — 加备份自创 skill
- `bin/oc-restore` — 加恢复自创 skill
- `bin/oc-lib.sh` — 加 `sync_user_skills_up` / `restore_user_skills` 函数
- `test/unit_test.sh` — 加断言

修改（manager `internal/integrations/ocops/`）：
- `client_skills.go`（新建）— `SkillList/SkillInstall/SkillDelete/SkillReload`
- `types_skills.go`（新建）— `SkillInfo`
- `client_skills_test.go`（新建）

---

## Task 1: 【SPIKE】确定 reload 程序化触发方式

**目标：** 在真实运行的 app pod 容器内，确定 oc-ops 如何**不经聊天渠道**程序化触发一次 hermes `reload-skills`。产出一个明确结论（A/B/C），写进本文件供 Task 5 实现。

**前置已知：** hermes 有 `agent.skill_commands.reload_skills()`（实测热装即识别）；gateway slash command `/reload-skills`；hermes 上游疑似有 localhost:8642 本地 REST。

- [ ] **Step 1: 选一个运行中的 app pod**

Run（确认本地 k3d，非线上）：`kubectl config current-context`（应为 `k3d-ocm`）；`kubectl get pods -n oc-apps | grep app-`
取一个 Running 的 pod 名记为 `$POD`。

- [ ] **Step 2: 枚举 hermes 本地 REST（候选 A）**

Run：`kubectl exec -n oc-apps $POD -c oc-ops -- sh -c 'curl -s http://127.0.0.1:8642/ ; echo; curl -s http://127.0.0.1:8642/api/ ; echo; curl -s -X POST http://127.0.0.1:8642/api/skills/reload'`
记录：8642 是否可达、有无 skill 相关端点、reload 端点路径与响应。

- [ ] **Step 3: 检查 hermes CLI（候选 B）**

Run：`kubectl exec -n oc-apps $POD -c hermes -- sh -c 'hermes skill --help 2>&1 | head -40; echo "---"; hermes --help 2>&1 | grep -i skill'`
记录：是否有 `hermes skill reload` / `hermes skill list` 子命令。

- [ ] **Step 4: 检查信号路径（候选 C，兜底）**

Run：`kubectl exec -n oc-apps $POD -c hermes -- sh -c 'pgrep -af "hermes gateway"'`
记录 gateway PID 获取方式（供 SIGUSR1 兜底，仅当 A/B 都不可用时考虑）。

- [ ] **Step 5: 实测选定方式真能触发 reload**

按 Step 2-4 找到的可行方式，实际放一个测试 skill 到容器 `/opt/data/skills/spike-test/SKILL.md` 后触发 reload，再确认 hermes 识别（如调 list 端点或看日志）。Run（示例，按实际方式调整）：
```bash
kubectl exec -n oc-apps $POD -c hermes -- sh -c 'mkdir -p /opt/data/skills/spike-test && printf -- "---\nname: spike-test\ndescription: spike\n---\nx\n" > /opt/data/skills/spike-test/SKILL.md'
# 然后用选定的触发方式（A 的 curl / B 的 hermes skill reload）触发，确认返回 added: spike-test
kubectl exec -n oc-apps $POD -c hermes -- sh -c 'rm -rf /opt/data/skills/spike-test'   # 清理
```

- [ ] **Step 6: 记录结论（不写代码，更新本 plan）**

把结论写进本文件 Task 5 上方的「reload 触发结论」小节：选定 A（HTTP `POST <url>`）/ B（`subprocess hermes skill reload`）/ C（signal），含确切命令/URL/响应格式。Task 5 的 `skills_reload` handler 按此实现。

> 本 task 无 commit（纯调研）。结论决定 Task 5 的 reload 实现分支。**若 A/B/C 全不可行**，BLOCKED 上报——可能需在 hermes 镜像加一个小 reload 触发钩子（升级 spike 为开发任务）。

---

### reload 触发结论（Task 1 spike 完成后填写）

> _待 spike 填写：选定方式 + 确切命令/URL/响应。Task 5 据此实现 `skills_reload`。_

---

## Task 2: render_skills.py 支持解 zip + zip-slip 校验

**Files:** Modify `renderer/render_skills.py`、`tests/test_render_skills.py`（容器目录 `runtime/hermes/hermes-v2026.5.16/`）

- [ ] **Step 1: 写 zip 失败测试** — 在 `tests/test_render_skills.py` 加（参照现有 `_make_skill_tar`，新增 `_make_skill_zip`）：

```python
import zipfile

def _make_skill_zip(zip_path, top_dir, skill_name):
    """构造一个含 <top_dir>/SKILL.md 的 zip，供 zip 解压用例使用。"""
    zip_path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path, "w") as zf:
        zf.writestr(f"{top_dir}/SKILL.md", f"---\nname: {skill_name}\n---\n")

def test_extract_zip_skill(tmp_input, tmp_data):
    # zip 格式的版本 skill 也能解压到 skills/<top>/，并打 .oc-managed 标记。
    _make_skill_zip(tmp_input / "resources" / "skills" / "weather.zip", "weather", "weather")
    render(_manifest(["resources/skills/weather.zip"]), tmp_input, tmp_data)
    assert (tmp_data / "skills" / "weather" / "SKILL.md").exists()
    assert (tmp_data / "skills" / "weather" / ".oc-managed").exists()

def test_zip_rejects_traversal(tmp_input, tmp_data):
    # 含 ../ 越界条目的 zip 被拒，不写出 skills/ 之外（zip-slip 防护）。
    zip_path = tmp_input / "resources" / "skills" / "evil.zip"
    zip_path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path, "w") as zf:
        zf.writestr("../evil/SKILL.md", "x")
    with pytest.raises(Exception):
        render(_manifest(["resources/skills/evil.zip"]), tmp_input, tmp_data)
```

- [ ] **Step 2: 运行确认失败** — `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_render_skills.py -k zip -v`，Expected: FAIL。

- [ ] **Step 3: 实现 zip 分支** — 在 `renderer/render_skills.py` 的 `_extract_version_skills` 里，按文件类型分流。在文件顶部 import 加 `import zipfile`，并把单个 tar 的解压抽出为按类型分流：

```python
def _extract_one_skill(tar_path, skills_root):
    """按归档类型解压单个 skill：.zip 走 zipfile（含 zip-slip 校验），其余走 tarfile。返回顶层目录名集合。"""
    name = tar_path.name.lower()
    if name.endswith(".zip"):
        return _extract_zip(tar_path, skills_root)
    return _extract_tar(tar_path, skills_root)

def _extract_zip(zip_path, skills_root):
    """解压 zip 到 skills_root，逐条用 _is_safe_member_path 防 zip-slip。"""
    top_dirs = set()
    with zipfile.ZipFile(zip_path, "r") as zf:
        for member in zf.namelist():
            if not _is_safe_member_path(member):
                raise ValueError(f"zip 含越界路径条目: {member} ({zip_path.name})")
            top = member.split("/", 1)[0]
            if top:
                top_dirs.add(top)
        # 逐条 extract 而非 extractall，避免 zipfile 对 .. 的宽松处理；路径已校验。
        for member in zf.namelist():
            zf.extract(member, skills_root)
    return top_dirs
```

把原 `_extract_version_skills` 内 `tarfile.open(...)` 那段抽成 `_extract_tar(tar_path, skills_root)`（返回 top_dirs，逻辑不变：逐条 `_is_safe_member_path` + `extractall(filter="data")`），主循环改为调 `_extract_one_skill`，对每个返回的 top 目录写 `.oc-managed` 标记（保持原有逻辑）。

> 注意：`_is_safe_member_path` 已拒绝 `..` 段与绝对路径，zip 同样复用；zipfile 无 tar 的 `filter="data"`，故逐条校验是主要防线。

- [ ] **Step 4: 运行确认通过** — `python -m pytest tests/test_render_skills.py -v`，Expected: 全 PASS（原 tar 用例 + 新 zip 用例）。

- [ ] **Step 5: 提交**：
```bash
git add runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py
git commit -m "feat(skill): render_skills 支持解 zip 并补 zip-slip 校验

按归档扩展名分流 tar/zip；zip 逐条校验路径段防穿越，与 tar 的 filter=data
双重防护对齐。覆盖 zip 解压与越界拒绝用例。"
```

---

## Task 3: 镜像构建期生成 skills-builtin.json

**Files:** Modify `runtime/hermes/hermes-v2026.5.16/Dockerfile`；Modify `internal/config/config.go`（RuntimeImageConfig 加内置清单字段）

- [ ] **Step 1: Dockerfile 生成内置清单** — 在 hermes install 之后、入口之前加一步，扫描镜像内置 skill 目录生成 `/opt/skills-builtin.json`。**先确认内置 skill 实际目录**（spike Task 1 时或本 task `kubectl exec ... ls /opt/data/skills` 确认；若构建期 `/opt/data/skills` 为空，则内置 skill 在 hermes 首次启动生成——此时改为 entrypoint 首次启动时生成清单）。Dockerfile 步骤（构建期目录可用时）：

```dockerfile
# 生成镜像内置 skill 清单，供 manager 已安装列表区分「内置」与「自创」。
RUN python3 - <<'PY'
import json
from pathlib import Path
d = Path("/opt/data/skills")
names = sorted(p.name for p in d.iterdir() if p.is_dir() and not (p / ".oc-managed").exists()) if d.exists() else []
Path("/opt/skills-builtin.json").write_text(json.dumps({"builtin": names}) + "\n")
PY
```

> **现场判定**：若构建期内置 skill 目录为空（hermes 运行时才生成内置 skill），改为在 `oc-entrypoint.py` 首次启动渲染前生成 `/opt/skills-builtin.json`（记录当前无 `.oc-managed` 标记的目录为内置基线）。spike Task 1 已能在容器内确认实际情况，按实际选 Dockerfile 还是 entrypoint 生成。

- [ ] **Step 2: manager 登记内置清单（供已安装列表 builtin 判定）** — 读 `internal/config/config.go` 的 `RuntimeImageConfig`，加字段：
```go
	// BuiltinSkills 是该镜像内置 skill 名单，供「已安装列表」区分 builtin/self_created。
	BuiltinSkills []string `yaml:"builtin_skills"`
```
（Plan 3 的实时对账会用 `config.ResolveRuntimeImage` + 此字段；本 task 只加字段与默认空，不接业务。）

- [ ] **Step 3: 编译 + 配置测试** — `go build ./internal/config/... && go test ./internal/config/...`，PASS。

- [ ] **Step 4: 提交**：
```bash
git add runtime/hermes/hermes-v2026.5.16/Dockerfile internal/config/config.go
git commit -m "feat(skill): 镜像构建生成内置 skill 清单并在配置登记

Dockerfile 生成 /opt/skills-builtin.json（内置 skill 名单）；
RuntimeImageConfig 加 builtin_skills 字段供已安装列表区分内置/自创。"
```

---

## Task 4: oc-sync / oc-restore 备份与恢复自创 skill

**Files:** Modify `runtime/ops/bin/oc-lib.sh`、`bin/oc-sync`、`bin/oc-restore`、`test/unit_test.sh`

- [ ] **Step 1: oc-lib.sh 加函数** — 加 `sync_user_skills_up`（备份自创 skill）与 `restore_user_skills`（恢复）：

```bash
# sync_user_skills_up <data_dir>：把 skills/ 下「无 .oc-managed 且不在 /opt/skills-builtin.json」的
# 目录（= hermes 自创 skill）增量同步到 s3://.../apps/<id>/skills/<name>/。
sync_user_skills_up() {
  local data_dir="$1"
  local skills_dir="$data_dir/skills"
  [ -d "$skills_dir" ] || return 0
  local builtin_file="/opt/skills-builtin.json"
  local name
  for dir in "$skills_dir"/*/; do
    [ -d "$dir" ] || continue
    name=$(basename "$dir")
    [ -f "$dir/.oc-managed" ] && continue   # manager 管理的（版本/安装）跳过
    if [ -f "$builtin_file" ] && jq -e --arg n "$name" '.builtin | index($n)' "$builtin_file" >/dev/null 2>&1; then
      continue                              # 镜像内置跳过
    fi
    aws_s3 sync "$dir" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}skills/${name}/"
  done
}

# restore_user_skills <data_dir>：把 s3://.../apps/<id>/skills/ 还原到 skills/（自创 skill）。
restore_user_skills() {
  local data_dir="$1"
  mkdir -p "$data_dir/skills"
  aws_s3 sync "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}skills/" "$data_dir/skills/"
}
```

- [ ] **Step 2: oc-sync 调用** — 在 `oc-sync` 的 `run_once`（现有 `sync_weixin_up "$DATA_DIR"` 之后）加 `sync_user_skills_up "$DATA_DIR"`；测试模式分支同样加。

- [ ] **Step 3: oc-restore 调用** — 在 `oc-restore` 现有 app 数据恢复段（`aws_s3 sync workspace` 附近）加 `restore_user_skills "$DATA_DIR"`。注意：自创 skill 无 `.oc-managed`，render_skills 的 `_wipe_managed_skills` 不会删它，恢复后保留。

- [ ] **Step 4: 加 bash 断言测试** — 在 `runtime/ops/test/unit_test.sh` 加一个用例：构造一个含 `.oc-managed` 的目录、一个内置名目录、一个自创目录，mock `aws_s3`/`jq` 后断言 `sync_user_skills_up` 只对自创目录调用 sync。（参照现有 unit_test.sh 的 mock 与 assert_eq 风格。）

- [ ] **Step 5: 运行 ops 测试** — `bash runtime/ops/test/unit_test.sh`，Expected: 全 PASS。

- [ ] **Step 6: 提交**：
```bash
git add runtime/ops/bin/oc-lib.sh runtime/ops/bin/oc-sync runtime/ops/bin/oc-restore runtime/ops/test/unit_test.sh
git commit -m "feat(skill): oc-sync/oc-restore 备份与恢复 hermes 自创 skill

oc-sync 把 skills/ 下非内置、无 .oc-managed 的自创 skill 同步到 S3
apps/<id>/skills/；oc-restore 还原；带内置/受管目录跳过的单测。"
```

---

## Task 5: oc-ops `/oc/skills` 端点（list / install / delete / reload）

**Files:** Create `runtime/hermes/hermes-v2026.5.16/ocops/skills.py`、`tests/test_ocops_server_skills.py`；Modify `ocops/server.py`

> **依赖 Task 1 spike 结论**：`run_reload` 的实现按「reload 触发结论」小节填的方式（HTTP 8642 / `hermes skill reload` CLI / signal）。

- [ ] **Step 1: 写 skills.py 业务逻辑** — Create `ocops/skills.py`（参照 `ocops/cron.py` 的 OpsError 风格；`SKILLS_DIR = /opt/data/skills`）：

```python
"""oc-ops skill 端点业务逻辑：列出 / 热装 / 热删 / 触发 reload 容器内 skill。"""
import json
import shutil
import subprocess
import zipfile
import tarfile
from pathlib import Path

from ocops.errors import OpsError

SKILLS_DIR = Path("/opt/data/skills")
BUILTIN_FILE = Path("/opt/skills-builtin.json")


def list_skills():
    """列出 skills/ 下所有 skill 目录，标注 managed（有 .oc-managed）/ builtin（在内置清单）。"""
    builtin = set()
    if BUILTIN_FILE.exists():
        builtin = set(json.loads(BUILTIN_FILE.read_text()).get("builtin", []))
    out = []
    if SKILLS_DIR.exists():
        for d in sorted(SKILLS_DIR.iterdir()):
            if not d.is_dir():
                continue
            out.append({
                "name": d.name,
                "managed": (d / ".oc-managed").exists(),
                "builtin": d.name in builtin,
            })
    return {"skills": out}


def install_skill(name, archive_path, managed=True):
    """把归档（tar/zip）解压进 skills/<name>/，写 .oc-managed 标记（managed=True 时）。"""
    if not name or "/" in name or name in (".", ".."):
        raise OpsError("INVALID", f"非法 skill 名: {name}")
    dest = SKILLS_DIR / name
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True, exist_ok=True)
    _safe_extract(archive_path, dest)
    if managed:
        (dest / ".oc-managed").write_text('{"source":"app-install"}\n')
    return {"name": name}


def delete_skill(name):
    """热删 skills/<name>/ 目录。"""
    if not name or "/" in name or name in (".", ".."):
        raise OpsError("INVALID", f"非法 skill 名: {name}")
    dest = SKILLS_DIR / name
    if dest.exists():
        shutil.rmtree(dest)
    return {"name": name}


def reload_skills():
    """触发 hermes 重新扫描 skills/（不重启进程）。实现方式由 spike 确定。"""
    # === 按 Task 1 spike 结论实现，三选一（删掉未选的）===
    # 候选 A（HTTP 8642）:
    #   import urllib.request
    #   urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:8642/api/skills/reload", method="POST"), timeout=5)
    # 候选 B（CLI）:
    #   subprocess.run(["hermes", "skill", "reload"], check=True, timeout=15)
    # 候选 C（signal）:
    #   pid = subprocess.check_output(["pgrep", "-f", "hermes gateway"]).split()[0]
    #   os.kill(int(pid), signal.SIGUSR1)
    raise NotImplementedError("按 spike 结论实现")  # ← 实现时替换为选定方式


def _safe_extract(archive_path, dest):
    """解压 tar/zip 到 dest，拒绝越界条目（zip-slip / tar 穿越）。"""
    p = Path(archive_path)
    if p.name.lower().endswith(".zip"):
        with zipfile.ZipFile(p) as zf:
            for m in zf.namelist():
                if m.startswith("/") or ".." in Path(m).parts:
                    raise OpsError("INVALID", f"归档含越界条目: {m}")
            zf.extractall(dest)
    else:
        with tarfile.open(p) as tf:
            tf.extractall(dest, filter="data")
```

- [ ] **Step 2: 写 pytest 测试** — Create `tests/test_ocops_server_skills.py`，用 Starlette TestClient（参照 `test_ocops_server_cron.py`）测：GET /oc/skills 列出（含 managed/builtin 标注）、POST 装一个 zip、DELETE 删除。reload 端点在单测里 monkeypatch `reload_skills` 避免真调 hermes。设置 `OC_OPS_TOKEN` 并带 Bearer。先写失败测试。

- [ ] **Step 3: 运行确认失败** — `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/test_ocops_server_skills.py -v`，FAIL。

- [ ] **Step 4: 注册路由** — 在 `ocops/server.py` 加 handler（async def，参照 cron_create 的 `_ok`/`_err`）与 routes：
```python
from ocops import skills

async def skills_list(request):
    try: return _ok(skills.list_skills())
    except OpsError as e: return _err(e)

async def skills_install(request):
    # multipart：form 字段 name + 文件 archive；存临时文件后 skills.install_skill
    ...

async def skills_delete(request):
    try: return _ok(skills.delete_skill(request.path_params["name"]))
    except OpsError as e: return _err(e)

async def skills_reload(request):
    try: skills.reload_skills(); return _ok({"reloaded": True})
    except OpsError as e: return _err(e)
```
routes 追加：
```python
Route("/oc/skills",        skills_list,    methods=["GET"]),
Route("/oc/skills",        skills_install, methods=["POST"]),
Route("/oc/skills/{name}", skills_delete,  methods=["DELETE"]),
Route("/oc/skills/reload", skills_reload,  methods=["POST"]),
```
并按「reload 触发结论」把 `reload_skills` 的 NotImplementedError 替换为选定实现。

- [ ] **Step 5: 运行确认通过** — `python -m pytest tests/test_ocops_server_skills.py -v`，PASS。

- [ ] **Step 6: 提交**：
```bash
git add runtime/hermes/hermes-v2026.5.16/ocops/skills.py runtime/hermes/hermes-v2026.5.16/ocops/server.py runtime/hermes/hermes-v2026.5.16/tests/test_ocops_server_skills.py
git commit -m "feat(skill): oc-ops 增加 /oc/skills 端点（列/装/删/reload）

list 标注 managed/builtin；install 解 tar/zip 进 skills/ 打 .oc-managed；
delete 热删；reload 按 spike 结论触发 hermes 重扫 skills（不重启进程）。"
```

---

## Task 6: manager 侧 ocops client skill 方法

**Files:** Create `internal/integrations/ocops/types_skills.go`、`client_skills.go`、`client_skills_test.go`

- [ ] **Step 1: 写类型 + client 失败测试** — 先读 `internal/integrations/ocops/client.go` 的 `Client`/`Endpoint`/`DoJSON` 签名。Create `types_skills.go`：
```go
package ocops

// SkillInfo 是 oc-ops GET /oc/skills 返回的单个 skill 状态。
type SkillInfo struct {
	Name    string `json:"name"`
	Managed bool   `json:"managed"`
	Builtin bool   `json:"builtin"`
}
```
Create `client_skills_test.go`（用 httptest mock oc-ops，参照现有 client 测试）测 `SkillList` 解析、`SkillDelete`/`SkillReload` 发对路径。

- [ ] **Step 2: 实现 client 方法** — Create `client_skills.go`（参照现有 DoJSON 方法）：
```go
// SkillList 列出 app 容器内 skill 状态（GET /oc/skills）。
func (c *Client) SkillList(ctx context.Context, ep Endpoint) ([]SkillInfo, error) {
	var out struct{ Skills []SkillInfo `json:"skills"` }
	if err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/skills", nil, &out); err != nil {
		return nil, err
	}
	return out.Skills, nil
}
// SkillDelete 热删容器内 skill（DELETE /oc/skills/{name}）。
func (c *Client) SkillDelete(ctx context.Context, ep Endpoint, name string) error {
	return c.DoJSON(ctx, ep, http.MethodDelete, "/oc/skills/"+url.PathEscape(name), nil, nil)
}
// SkillReload 触发容器内 hermes 重扫 skills（POST /oc/skills/reload）。
func (c *Client) SkillReload(ctx context.Context, ep Endpoint) error {
	return c.DoJSON(ctx, ep, http.MethodPost, "/oc/skills/reload", nil, nil)
}
```
（`SkillInstall` 走 multipart，单独按现有上传模式实现；若 DoJSON 不支持 multipart，加一个 `doMultipart` 或在本文件内构造 multipart 请求——参照仓库其它 multipart 上传处。）

- [ ] **Step 3: 测试 + 提交** — `go test ./internal/integrations/ocops/... && go build ./...`，PASS。
```bash
git add internal/integrations/ocops/types_skills.go internal/integrations/ocops/client_skills.go internal/integrations/ocops/client_skills_test.go
git commit -m "feat(skill): manager ocops client 增加 skill 列/删/reload 方法

供 Plan 3 的 per-app 装卸调用容器内 oc-ops /oc/skills 端点。"
```

---

## Task 7: 整体校验

- [ ] **Step 1: 容器侧 pytest 全量** — `cd runtime/hermes/hermes-v2026.5.16 && python -m pytest tests/ -q`，PASS。
- [ ] **Step 2: ops bash 测试** — `bash runtime/ops/test/unit_test.sh`，PASS。
- [ ] **Step 3: Go 全量** — `go build ./... && go test ./internal/... && go vet ./...`，PASS。
- [ ] **Step 4: 镜像构建冒烟（best-effort）** — `make hermes-image`（或仓库实际的 hermes 镜像构建 target，先 `grep -i hermes Makefile` 找）确认 Dockerfile 改动可 build、`/opt/skills-builtin.json` 生成。连不上/无 target 则跳过说明。
- [ ] **Step 5: 提交（若有未提交的生成/收尾）** — 通常无新增，若有则提交。

---

## Self-Review 备注

- **Spec 覆盖**：render_skills 解 zip + zip-slip；oc-sync/oc-restore 自创 skill 备份恢复；镜像内置清单 + 配置登记；oc-ops /oc/skills 端点；ocops client 方法；reload 触发（spike）。
- **未覆盖（属其它 plan）**：app_skills 数据流、bootstrap 下发 app_skills 清单、oc-restore 改读 app_skills（Plan 3/4）；实时对账聚合（Plan 3）；前端（Plan 6）。本 plan 的 oc-restore 只加「恢复自创 skill」，版本 skill 恢复保持现状直到 Plan 3 改造。
- **关键风险/spike**：reload 程序化触发（Task 1）是本 plan 唯一不确定点，已设为首个 spike task 并要求实测确认；A/B/C 全不可行则升级为「给镜像加 reload 钩子」开发任务。
- **现场确认项**：内置 skill 实际目录（构建期 vs 运行时生成 skills-builtin.json）;ocops DoJSON 是否支持 multipart（SkillInstall）;模块路径 `oc-manager`;hermes 镜像构建 target 名。
