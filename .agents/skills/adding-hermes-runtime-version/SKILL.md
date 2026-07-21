---
name: adding-hermes-runtime-version
description: Use when adding or upgrading a normal Hermes runtime/instance version in oc-manager, including new runtime/hermes/hermes-v* variants, HERMES_VARIANT defaults, runtime_images entries, hermes-ref metadata, or upstream Hermes Agent tag compatibility checks.
---

# 添加 Hermes Runtime 版本

仅用于普通实例 Hermes 版本（`runtime/hermes/hermes-v*`）。AICC 使用
`runtime/hermes/hermes-aicc`，生命周期独立，不走本 skill。

## 必读上下文

修改前先读：

- Root `AGENTS.md`
- `runtime/hermes/AGENTS.md`
- `runtime/hermes/README.md`
- Previous variant `CONTRACT.md`, `Dockerfile`, `version.txt`, `hermes-ref.txt`

除非用户明确要求，不要创建 git worktree；本仓库版本工作默认直接在当前 `master` 完成。

## 工作流

1. **确认版本关系**
   - 产品版本：对用户可见的 variant 版本，例如 `v0.19.0`。
   - variant 目录：`runtime/hermes/hermes-<产品版本>`。
   - 上游 ref：Docker `--branch` 使用的不可变 Hermes Agent Git tag，写入 `hermes-ref.txt`。
   - 必须确认上游 ref 对应产品版本：

   ```bash
   git ls-remote --tags https://github.com/NousResearch/hermes-agent.git | rg '<candidate-tag>'
   curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/<tag>/pyproject.toml | rg -n '^(name|version)\s*='
   ```

2. **先写失败守卫**
   - 在 `scripts/hermes-version-guard_test.sh` 增加新真实 variant 用例。
   - 运行 `./scripts/hermes-version-guard_test.sh`；此时应因新目录或版本元数据不存在而失败。

3. **创建 variant**
   - 复制最新普通 variant：

   ```bash
   cp -a runtime/hermes/hermes-v<old> runtime/hermes/hermes-v<new>
   ```

   - 只在新目录内替换版本标识。
   - `version.txt` 写产品版本。
   - `hermes-ref.txt` 写已验证的上游 tag。
   - 更新 `CONTRACT.md`：基线、上游版本/tag、迁移说明。

4. **检查上游兼容性**
   - 对比旧上游 ref 与新 ref，聚焦构建和运行时相关文件：

   ```bash
   tmp=/tmp/oc-manager-hermes-agent-diff
   [ -d "$tmp/.git" ] || git clone --filter=blob:none --no-checkout https://github.com/NousResearch/hermes-agent.git "$tmp"
   git -C "$tmp" fetch --tags --force --quiet
   git -C "$tmp" diff --name-only <old-ref> <new-ref> -- pyproject.toml Dockerfile agent gateway tools toolsets.py
   git -C "$tmp" diff <old-ref> <new-ref> -- agent/skill_commands.py gateway/platforms/api_server.py gateway/run.py gateway/slash_commands.py gateway/kanban_watchers.py gateway/platforms/base.py tools/lazy_deps.py
   ```

   - 如果上游移动了 patch 锚点，先补定向测试，再改 patch 脚本。
   - 不要只等 Docker build 报错；应先用真实上游源码离线模拟 `patch_i18n_literals.py` 和
     `patch_api_server_reload.py`。
   - 如果 `tools/lazy_deps.py` 的 pin 变化，且本仓库 Dockerfile 预装了对应依赖，必须同步预装版本，
     避免运行期 lazy install。

5. **同步版本选择入口**
   - 更新 `Makefile` 默认 `HERMES_VARIANT`。
   - 在 `deploy/k8s/local/secret.yaml` 的 `runtime_images` 首位增加新 id。
   - 更新 `deploy/k8s/prod/secret.example.yaml`。
   - 不要暂存本地/私有文件，例如 `config/manager.yaml` 或 `deploy/k8s/prod/secret.yaml`；如需本地可用，
     可改但交付时说明它们不入 git。
   - 不要手工编辑生成产物 OpenAPI 或 web generated 文件；此流程不应改变 API 契约。

## 验证

按顺序运行：

```bash
./scripts/hermes-version-guard_test.sh
PYTHONPATH=/usr/local/lib python3 -m pytest tests -q
make -n build-hermes-runtime
make build-hermes-runtime HERMES_VARIANT=hermes-v<new>
docker image inspect hermes-runtime:<product-version>-dev --format '{{.Id}} {{.RepoTags}}'
```

pytest 的 `workdir` 使用 `runtime/hermes/hermes-v<new>`。

如果真实 Docker build 失败：

- patch / i18n / 测试层失败视为代码问题，必须修复。
- 浏览器下载慢或失败只有在 Docker 最终成功，或失败明确不在本仓库 patch/runtime 层时，才可按网络问题说明。

交付前清理新 variant 内的本地测试/构建缓存：

```bash
find runtime/hermes/hermes-v<new> -type f -name '*.pyc' -delete
find runtime/hermes/hermes-v<new> -type d -name '__pycache__' -empty -delete
find runtime/hermes/hermes-v<new>/.pytest_cache -type f -delete 2>/dev/null || true
find runtime/hermes/hermes-v<new>/.pytest_cache -type d -empty -delete 2>/dev/null || true
```

确认 `git status --short --untracked-files=all` 中没有缓存目录或构建注入的 `ocops-contract` 副本。

## 提交纪律

使用一个聚焦的 Conventional Commit，例如：

```text
feat(hermes): 添加 v0.19.0 运行时版本

新增 hermes-v0.19.0 variant，并将默认构建版本切换到 v0.19.0。

适配上游 <tag> 的构建期补丁差异，并同步运行时镜像配置。

验证：<commands actually run>
```
