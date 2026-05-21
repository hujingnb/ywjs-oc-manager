# 镜像 tag 增加 Git Commit ID 设计

## 背景

当前 Makefile 为本仓库构建发布的镜像统一使用本地时间戳作为 tag。这个 tag 能区分构建批次，但不能直接定位对应源码 commit。发布、回滚和排查线上问题时，需要额外查发布记录才能知道某个镜像来自哪次代码提交。

本设计将本仓库构建的发布镜像 tag 扩展为“时间戳 + 当前 git commit 前 8 位”，在保留按时间排序习惯的同时，让镜像引用具备源码追溯能力。

## 范围

本次只覆盖本仓库直接构建和发布的 4 个镜像：

- `manager-api`
- `runtime-agent`
- `manager-web`
- `oc-manager-hermes`

不覆盖外部依赖镜像，例如 new-api、ollama、postgres、redis、nginx。它们不是由本仓库源码构建，使用本仓库 commit id 作为 tag 语义不成立。

## Tag 规则

Makefile 顶层新增统一变量：

- `GIT_COMMIT_SHORT`：当前 `HEAD` 的 8 位短 commit id。
- `IMAGE_TAG`：`$(IMAGE_TIMESTAMP)-$(GIT_COMMIT_SHORT)`。

普通服务镜像使用统一的 `IMAGE_TAG`：

```text
$(API_IMAGE_REPO):$(IMAGE_TAG)
$(AGENT_IMAGE_REPO):$(IMAGE_TAG)
$(WEB_IMAGE_REPO):$(IMAGE_TAG)
```

Hermes runtime 镜像继续保留 Hermes 上游版本前缀：

```text
$(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TAG)
```

示例：

```text
oc-manager-api:2026-05-21-12-00-00-be70e40
oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00-be70e40
```

## Git 状态校验

新增 Makefile guard：`.guard-image-git-state`。

该 guard 用于本仓库发布镜像的构建入口，确保 tag 中的 commit id 能准确代表镜像源码：

- 当前目录必须是 git worktree。
- 必须能读取 `HEAD` 的 8 位 commit id。
- tracked 工作区必须干净：
  - `git diff --quiet`
  - `git diff --cached --quiet`
- 若 tracked 文件存在 unstaged 或 staged 改动，发布镜像构建直接失败，并提示先提交再构建镜像。

untracked 文件不阻塞。当前仓库长期存在未跟踪的文档草稿、缓存目录和本地文件；这些文件不属于 commit id 的语义边界，且不应让发布构建被无关文件阻断。

## 覆盖保护

使用 `override` 固定由 Makefile 生成的 tag 变量：

- `GIT_COMMIT_SHORT`
- `IMAGE_TAG`
- `HERMES_IMAGE`

这样命令行传入 `IMAGE_TAG=latest`、`GIT_COMMIT_SHORT=main` 或 `HERMES_IMAGE=repo:latest` 时不会改变最终镜像 tag。

仓库变量如 `API_IMAGE_REPO`、`AGENT_IMAGE_REPO`、`WEB_IMAGE_REPO`、`HERMES_IMAGE_REPO` 仍允许覆盖，方便切换 registry 或命名空间。覆盖仓库地址不改变 tag 规则。

## Makefile 影响范围

以下 target 需要改为使用 `IMAGE_TAG`：

- `build-api-image`
- `push-api-image`
- `release-api-image`
- `build-agent-image`
- `push-agent-image`
- `release-agent-image`
- `build-web-image`
- `push-web-image`
- `release-web-image`
- `deploy-api`
- `deploy-web`
- `deploy-agent`

Hermes 相关 target 保持 `HERMES_IMAGE` 作为唯一完整镜像引用，但 `HERMES_IMAGE` 的 tag 部分改为 `$(HERMES_VERSION)-$(IMAGE_TAG)`：

- `build-hermes-image`
- `push-hermes-image`
- `release-hermes-image`

`build-hermes-runtime` 是本地 dev stub 构建入口，仍使用 `hermes-runtime:$(HERMES_VERSION)-dev`，不纳入本次“发布镜像 tag 增加 commit id”的范围。

## 文档影响范围

需要同步更新说明中“生产镜像 tag 取时间戳”的表述，改为“生产镜像 tag 使用时间戳 + git commit 前 8 位”。

主要文档位置：

- `README.md`
- `deploy/operations.md`
- `deploy/manage/README.md`
- `deploy/runtime-agent/README.md`

如果文档中只是外部依赖镜像的 tag 或 digest 说明，不做修改。

## 验证计划

实现完成后验证以下行为：

1. `make --dry-run build-api-image` 输出 `$(API_IMAGE_REPO):<timestamp>-<commit8>`。
2. `make --dry-run release-agent-image` 的 build、push、echo 使用同一个 `IMAGE_TAG`。
3. `make --dry-run build-web-image` 输出 `$(WEB_IMAGE_REPO):<timestamp>-<commit8>`。
4. `make --dry-run build-hermes-image` 输出 `$(HERMES_IMAGE_REPO):v2026.5.16-<timestamp>-<commit8>`。
5. 命令行传入 `IMAGE_TAG=latest GIT_COMMIT_SHORT=main HERMES_IMAGE=repo:latest` 时，dry-run 仍使用 Makefile 生成的真实 commit id tag。
6. 临时制造 tracked dirty 状态时，发布镜像构建入口失败；恢复后重新通过。
7. 运行 `git diff --check`，确认文档和 Makefile 无空白错误。

本次主要修改 Makefile 和文档，不改变 Go 业务逻辑；因此不要求全量 Go 单元测试作为必需验证。

## 风险与取舍

`IMAGE_TAG` 使用当前 `HEAD`，所以 dirty tracked 工作区必须失败。这个约束牺牲了临时本地构建的便利性，但能保证发布镜像 tag 可审计、可复现。

untracked 文件不阻塞发布构建。这个选择符合当前仓库状态，也避免本地文档草稿影响发布；代价是如果有人依赖未跟踪文件参与 Docker build，guard 不会发现。按当前 Dockerfile 和发布流程，发布镜像应只依赖已纳入 git 的源码和构建上下文。

普通服务镜像不额外写入版本前缀，只使用 `timestamp-commit8`。Hermes runtime 已有独立上游版本号，因此保留 `v2026.5.16-timestamp-commit8`。
