# Makefile 双远程同步命令设计

## 背景

本仓库配置了两个远程：

- `github` → `git@github.com:hujingnb/ywjs-oc-manager.git`：个人 GitHub，承载 Claude Code 云端开发分支（如 `claude/*`）。
- `origin` → `git@codeup.aliyun.com:...../oc-manager.git`：ywjs 内部 aliyun codeup 仓库。

日常需要在两个远程之间同步当前分支，并对单个远程做常规 pull/push。手敲 `git push <remote> <branch>` 易写错远程名或分支名，因此在 Makefile 中固化为一组命令。

## 目标

在 Makefile 新增一组 `##@ Git 远程同步` 分组命令：

1. 对单个远程的基础 pull/push（github、origin 各一对）。
2. 两个远程之间、经过本地当前分支的双向同步。

均针对**当前所在分支**（不固定 master、不写死分支名）。

## 非目标

- 不做自动 force push / 自动 rebase。分叉时仅停下并提示手动命令。
- 不引入 tag / 多分支批量同步等扩展能力（YAGNI）。
- 不切换分支、不动除当前分支外的任何本地分支。

## 变量

沿用 Makefile 既有 `?=` 可覆盖风格：

```makefile
GITHUB_REMOTE ?= github   # 个人 GitHub(含 Claude Code 云端分支)
ORIGIN_REMOTE ?= origin   # ywjs 内部 aliyun codeup
```

分支名不设变量，所有 recipe 在运行时取当前分支：

```sh
BRANCH=$$(git rev-parse --abbrev-ref HEAD)
```

## 对外命令（6 个，均进入 help）

基础单远程操作（针对当前分支）：

| target | 行为 |
|---|---|
| `pull-github` | 从 github 拉当前分支到本地（ff-only） |
| `push-github` | 把本地当前分支推到 github |
| `pull-origin` | 从 origin 拉当前分支到本地（ff-only） |
| `push-origin` | 把本地当前分支推到 origin |

跨远程同步（经过本地当前分支 = 先 pull 源 + 再 push 目标）：

| target | 行为 |
|---|---|
| `sync-github-to-origin` | `pull-github` 后接 `push-origin` |
| `sync-origin-to-github` | `pull-origin` 后接 `push-github` |

## 行为约定

### pull（ff-only）

1. 守门：工作区必须干净（无 staged / unstaged 改动），否则报错退出，避免 merge 触及未提交改动。
2. `git fetch <remote> <branch>`。
3. `git merge --ff-only <remote>/<branch>`，把当前分支快进到远程。
4. 分叉无法快进时：停下，打印手动命令（如 `git reset --hard <remote>/<branch>`），并以非零码退出。不自动 force。

> 边界：若本地当前分支领先于源远程（本地有源没有的提交），`merge --ff-only` 视为「已最新」不报错，是预期行为。

### push

1. `git push <remote> <branch>`，默认即 fast-forward push。
2. 远程已分叉导致 push 被拒时：停下，打印 `git push --force-with-lease <remote> <branch>` 供手动执行，并以非零码退出。不自动 force。

### sync

串联「pull 源远程」+「push 目标远程」，任一步失败立即中止（`make` 默认行为）。

## 复用结构

为避免复制脚本（遵循项目 CLAUDE.md「只差一个参数就合并为一个带参函数」），用 3 个 `.` 前缀私有 target 承载真正逻辑，对外 6 个 target 仅用 `$(MAKE)` 调用并传参。`.` 前缀名不进 help（help 的 awk 正则 `^[a-zA-Z]` 拒绝 dot-prefix）。

```makefile
# 私有: 从 REMOTE 拉当前分支(ff-only)
.git-pull:
	@BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	 git diff --quiet && git diff --cached --quiet || { echo "工作区有未提交改动,请先提交或暂存" >&2; exit 1; }; \
	 git fetch $(REMOTE) $$BRANCH; \
	 git merge --ff-only $(REMOTE)/$$BRANCH || { \
	   echo "本地 $$BRANCH 与 $(REMOTE)/$$BRANCH 分叉,无法快进。" >&2; \
	   echo "如确认以远程为准,手动执行: git reset --hard $(REMOTE)/$$BRANCH" >&2; \
	   exit 1; }

# 私有: 把当前分支推到 REMOTE
.git-push:
	@BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	 git push $(REMOTE) $$BRANCH || { \
	   echo "推送被拒(远程可能已分叉)。" >&2; \
	   echo "如确认以本地为准,手动执行: git push --force-with-lease $(REMOTE) $$BRANCH" >&2; \
	   exit 1; }

# 私有: FROM 拉到本地再推到 TO
.git-sync:
	@$(MAKE) .git-pull REMOTE=$(FROM)
	@$(MAKE) .git-push REMOTE=$(TO)

pull-github: ## 从 github 拉当前分支到本地(ff-only)
	@$(MAKE) .git-pull REMOTE=$(GITHUB_REMOTE)
push-github: ## 把本地当前分支推到 github
	@$(MAKE) .git-push REMOTE=$(GITHUB_REMOTE)
pull-origin: ## 从 origin 拉当前分支到本地(ff-only)
	@$(MAKE) .git-pull REMOTE=$(ORIGIN_REMOTE)
push-origin: ## 把本地当前分支推到 origin
	@$(MAKE) .git-push REMOTE=$(ORIGIN_REMOTE)
sync-github-to-origin: ## 同步 github 当前分支到 origin(经本地)
	@$(MAKE) .git-sync FROM=$(GITHUB_REMOTE) TO=$(ORIGIN_REMOTE)
sync-origin-to-github: ## 同步 origin 当前分支到 github(经本地)
	@$(MAKE) .git-sync FROM=$(ORIGIN_REMOTE) TO=$(GITHUB_REMOTE)
```

6 个对外 target + 3 个私有 target 加入文件首行 `.PHONY` 列表。

## 验证

- `make help` 能看到 `##@ Git 远程同步` 分组下 6 个 target 及中文描述。
- 在干净的非 master 分支上 `make push-github`，确认推送的是当前分支而非写死的 master。
- 构造分叉场景，确认 `pull-*` / `push-*` 不自动 force，而是打印手动命令并以非零码退出。
- `make sync-github-to-origin` 在源落后/已最新两种情况下行为正确。
