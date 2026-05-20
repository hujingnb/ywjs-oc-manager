# Hermes 任务看板 tab 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在实例详情页新增「任务」tab，让组织成员查看与管理本实例 Hermes Kanban 任务（看板浏览、详情、实时事件流、写操作）。

**Architecture:** manager 通过已有的 agent 透明 docker 代理，在 hermes 容器内执行 `hermes kanban ... --json` CLI 并解析输出。读操作走一次性 exec，实时事件走流式 exec（`hermes kanban watch`）转 SSE 推前端。前端是左右分屏：左侧按 status 分组的任务列表，右侧任务详情。

**Tech Stack:** Go（gin + docker SDK + testify）、Vue 3 + TypeScript（TanStack Query + Naive UI + Vitest）、Hermes Kanban CLI。

---

## 与设计 spec 的偏差说明

设计 spec：`docs/superpowers/specs/2026-05-19-hermes-task-dashboard-design.md`。

实施阶段代码探索发现一处可简化点，本计划据此调整：

- **spec §6 / §7.1** 假设「agent 端新增 `/docker-exec` 与 `/docker-exec-stream` 两个 endpoint」。
  实际上 agent 已经是**透明 docker API 代理**（`internal/integrations/agent/docker_proxy.go`，
  前缀 `/v1/docker`），manager 侧已有 `runtime.Adapter.ContainerExec`
  （`internal/integrations/runtime/agent_backed.go`）。
- **本计划改为**：不动 agent 仓库；在 manager 的 `runtime.Adapter` 接口新增两个 exec
  能力（一次性不截断 + 流式），实现复用现有 docker SDK client 工厂。
- 其余 spec 内容（CLI 通路本质、UI、权限、错误处理）不变。

---

## 文件结构

### 后端新增 / 修改

| 文件 | 职责 |
|---|---|
| `internal/integrations/runtime/adapter.go` | 修改：`Adapter` 接口加 `ContainerExecJSON` / `ContainerExecStream`；加 `ExecJSONResult` / `ExecStreamHandle` 类型 |
| `internal/integrations/runtime/agent_backed.go` | 修改：`AgentBackedAdapter` 实现上述两个方法 |
| `internal/service/hermes_kanban.go` | 新增：`HermesKanbanService` —— 每个 CLI verb 一个方法，参数白名单 + JSON 解析 |
| `internal/service/hermes_kanban_types.go` | 新增：`KanbanBoard` / `KanbanTask` / `KanbanTaskDetail` / `KanbanTaskRun` / `KanbanComment` / `KanbanEvent` / `KanbanStats` 等强类型 |
| `internal/service/errors.go` | 修改：加 kanban 相关 sentinel error |
| `internal/api/handlers/hermes_kanban.go` | 新增：`HermesKanbanHandler` + `RegisterHermesKanbanRoutes` |
| `internal/api/handlers/dto.go` | 修改：加 `CreateKanbanTaskRequest` 等请求 DTO |
| `internal/api/handlers/request_errors.go` | 修改：`mappedServiceErrorRules` 加 kanban error 映射 |
| `internal/auth/authorizer.go` | 修改：加 `CanViewAppKanban` / `CanManageAppKanban` |
| `internal/api/router.go` | 修改：`Dependencies` 加 `HermesKanbanService`，装配 kanban 路由 |
| `runtime/hermes/hermes-main/version.txt` | 修改：pin hermes 上游 ref |
| `runtime/hermes/hermes-main/tests/test_kanban_contract.py` | 新增：kanban CLI JSON 契约测试 |

### 前端新增 / 修改

| 文件 | 职责 |
|---|---|
| `web/src/api/hooks/useKanban.ts` | 新增：所有 kanban query / mutation hooks + SSE 订阅 |
| `web/src/pages/apps/AppKanbanTab.vue` | 新增：tab 顶层 wrapper，左右分屏 |
| `web/src/pages/apps/kanban/KanbanTaskList.vue` | 新增：左侧按 status 分组列表 |
| `web/src/pages/apps/kanban/KanbanTaskRow.vue` | 新增：单个任务行 |
| `web/src/pages/apps/kanban/KanbanTaskDetail.vue` | 新增：右侧详情面板 |
| `web/src/pages/apps/kanban/KanbanTaskActions.vue` | 新增：按状态显示的操作按钮组 |
| `web/src/pages/apps/kanban/KanbanCreateModal.vue` | 新增：新建任务模态框 |
| `web/src/pages/apps/AppDetailPage.vue` | 修改：`allTabs` 加「任务」 |
| `web/src/app/router.ts` | 修改：加 `kanban` 子路由 |
| `web/src/pages/apps/AppKanbanTab.spec.ts` | 新增：tab 单测 |

### 不改动

- agent 仓库（`runtime/agent/`）—— 透明 docker 代理已够用
- 生产 `runtime/hermes/hermes-main/Dockerfile`（已装真实 hermes）
- 其余 6 个实例详情 tab

---

## Phase A：Hermes Kanban CLI 契约保障

### Task A1：pin hermes 上游版本

**Files:**
- Modify: `runtime/hermes/hermes-main/version.txt`

- [ ] **Step 1: 查看当前 version.txt 内容**

Run: `cat runtime/hermes/hermes-main/version.txt`

当前大概率是 `main`（浮动 ref）。Kanban CLI 的 `--json` 输出格式跨版本可能变化，必须 pin。

- [ ] **Step 2: 把 version.txt 改成固定 tag**

把内容从 `main` 改为一个已实测含 `kanban` 子命令的 hermes-agent 版本号。
brainstorming 阶段实测 `hermes-agent==0.14.0` 含完整 kanban CLI。若 `install.sh`
的 `--branch` 接受 git tag，用对应 tag（如 `v0.14.0`）；若只接受分支名，保留
一个明确的 release 分支名并在文件加注释说明。

文件内容（示例）：

```
v0.14.0
```

- [ ] **Step 3: 提交**

```bash
git add runtime/hermes/hermes-main/version.txt
git commit -m "chore(hermes-runtime): pin hermes 上游版本以稳定 kanban CLI 契约

kanban --json 输出格式跨版本可能 break，把浮动 ref 固定到已实测含完整
kanban 子命令的版本。"
```

### Task A2：Kanban CLI JSON 契约测试

**Files:**
- Create: `runtime/hermes/hermes-main/tests/test_kanban_contract.py`

- [ ] **Step 1: 查看现有 tests 目录的测试风格**

Run: `ls runtime/hermes/hermes-main/tests/ && head -40 runtime/hermes/hermes-main/tests/*.py`

了解现有 pytest 用法、是否有 e2e/contract 分类标记。

- [ ] **Step 2: 写契约测试**

新建 `runtime/hermes/hermes-main/tests/test_kanban_contract.py`。该测试**只在装了
真实 hermes 的镜像内运行**（stub 镜像跳过）。它验证 kanban CLI 的 `--json` 输出
可被解析、含设计依赖的关键字段。

```python
"""Hermes Kanban CLI JSON 输出契约测试。

manager 的 hermes_kanban service 依赖 `hermes kanban ... --json` 输出的字段名。
该测试在构建出的生产镜像里运行，确保上游版本变化不会悄悄 break manager 解析。
stub 镜像没有真实 hermes，整文件跳过。
"""

import json
import shutil
import subprocess

import pytest

# stub 镜像的 /usr/local/bin/hermes 是 shell 脚本，不支持 kanban 子命令；
# 用 `hermes kanban --help` 探测：真实 hermes 返回 0，stub 返回 2。
def _has_real_hermes() -> bool:
    if shutil.which("hermes") is None:
        return False
    proc = subprocess.run(
        ["hermes", "kanban", "--help"], capture_output=True, text=True
    )
    return proc.returncode == 0


pytestmark = pytest.mark.skipif(
    not _has_real_hermes(), reason="stub 镜像无真实 hermes kanban CLI"
)


def _run_kanban(*args: str) -> str:
    """跑一条 kanban 命令，返回 stdout；非零退出直接 fail。"""
    proc = subprocess.run(
        ["hermes", "kanban", *args], capture_output=True, text=True, timeout=30
    )
    assert proc.returncode == 0, f"hermes kanban {args} 失败: {proc.stderr}"
    return proc.stdout


def test_kanban_init_idempotent():
    """init 创建 kanban.db，幂等可重复执行。"""
    _run_kanban("init")
    _run_kanban("init")  # 第二次不应报错


def test_kanban_list_json_parseable():
    """list --json 输出必须是合法 JSON 数组（空看板时为 []）。"""
    _run_kanban("init")
    out = _run_kanban("list", "--json")
    tasks = json.loads(out)
    assert isinstance(tasks, list)


def test_kanban_stats_json_has_status_counts():
    """stats --json 输出含 manager 工具栏依赖的 per-status 计数。"""
    _run_kanban("init")
    out = _run_kanban("stats", "--json")
    stats = json.loads(out)
    assert isinstance(stats, dict)


def test_kanban_create_show_roundtrip():
    """create 后 show --json 能取回任务，含 manager 依赖的核心字段。"""
    _run_kanban("init")
    create_out = _run_kanban(
        "create", "contract-test 任务", "--assignee", "default", "--json"
    )
    task = json.loads(create_out)
    task_id = task["id"]
    assert task_id

    show_out = _run_kanban("show", task_id, "--json")
    detail = json.loads(show_out)
    # manager hermes_kanban_types.go 依赖以下字段名
    for field in ("id", "title", "status", "assignee", "created_at"):
        assert field in detail, f"kanban show 输出缺字段 {field}"
```

- [ ] **Step 3: 确认测试在 stub 环境下正确跳过**

Run: `cd runtime/hermes/hermes-main && python -m pytest tests/test_kanban_contract.py -v`
Expected: 全部 SKIPPED（本机/stub 无真实 hermes），不报错

- [ ] **Step 4: 提交**

```bash
git add runtime/hermes/hermes-main/tests/test_kanban_contract.py
git commit -m "test(hermes-runtime): 新增 kanban CLI JSON 契约测试

验证 hermes kanban --json 输出可解析且含 manager 依赖的字段；stub 镜像
自动跳过，仅在装真实 hermes 的生产镜像内有效。"
```

---

## Phase B：runtime adapter exec 能力

### Task B1：扩展 runtime.Adapter 接口与类型

**Files:**
- Modify: `internal/integrations/runtime/adapter.go`

- [ ] **Step 1: 在 adapter.go 加 exec 结果类型**

在 `ExecResult` 定义之后追加。`ContainerExec`（旧）截断到 4KB 且不分离 stdout/stderr，
不适合 kanban JSON 解析；新增类型支持完整、干净的 stdout。

```go
// ExecJSONResult 是 ContainerExecJSON 返回的一次性命令执行结果。
// 与 ExecResult 不同：Stdout 不截断、已用 stdcopy 与 stderr 分离，可直接 JSON 解析。
type ExecJSONResult struct {
	// ExitCode 是容器内命令退出码。
	ExitCode int
	// Stdout 是分离后的标准输出全文（kanban --json 的 JSON 体）。
	Stdout string
	// Stderr 是分离后的标准错误全文（CLI 失败时的人类可读信息）。
	Stderr string
}

// ExecStreamHandle 是 ContainerExecStream 返回的流式执行句柄。
// 用于 hermes kanban watch 这类长连接 NDJSON 输出。
type ExecStreamHandle struct {
	// Lines 逐行投递容器 stdout（已剥离 docker multiplexed 帧头）。
	// 流正常结束或出错时该 channel 被关闭。
	Lines <-chan string
	// Err 在流结束后可读，nil 表示正常结束。读取前必须先确认 Lines 已关闭。
	Err func() error
	// Close 主动终止流并释放底层连接，可重复调用。
	Close func()
}
```

- [ ] **Step 2: 在 Adapter 接口加两个方法**

在 `ContainerExec` 方法声明之后追加：

```go
	// ContainerExecJSON 在容器内执行一次性命令，返回完整未截断的 stdout/stderr 与 exit code。
	// 用于 hermes kanban 读 / 写 verb（输出是单段 JSON）。
	ContainerExecJSON(ctx context.Context, nodeID, containerID string, cmd []string) (ExecJSONResult, error)
	// ContainerExecStream 在容器内执行流式命令，逐行投递 stdout。
	// 用于 hermes kanban watch（NDJSON 长连接）。调用方负责 Close。
	ContainerExecStream(ctx context.Context, nodeID, containerID string, cmd []string) (ExecStreamHandle, error)
```

- [ ] **Step 3: 编译确认接口声明无语法错误**

Run: `cd /home/hujing/dir/software/ywjs/oc-manager && go build ./internal/integrations/runtime/...`
Expected: 编译失败，报 `AgentBackedAdapter` 未实现新方法 —— 符合预期，Task B2 补实现

- [ ] **Step 4: 提交（与 B2 一起提交，此步先不单独 commit）**

跳过，B2 完成后统一提交。

### Task B2：AgentBackedAdapter 实现 exec 方法

**Files:**
- Modify: `internal/integrations/runtime/agent_backed.go`
- Test: `internal/integrations/runtime/agent_backed_test.go`（若不存在则创建）

- [ ] **Step 1: 查看现有 imports 和 dockerClient 辅助方法**

Run: `grep -n 'import\|func (a \*AgentBackedAdapter) dockerClient\|stdcopy\|streamingDockerClient' internal/integrations/runtime/agent_backed.go`

确认：(a) 是否已 import `github.com/docker/docker/pkg/stdcopy`；(b) `dockerClient`
辅助方法签名；(c) 是否已有 streaming client 构造。`ContainerExec`（行 ~199）已用
`a.dockerClient(ctx, nodeID)` 拿带 timeout 的 client。流式需要无 timeout 的 client。

- [ ] **Step 2: 实现 ContainerExecJSON**

在 `ContainerExec` 方法之后追加。relative 现有 `ContainerExec`，区别：用
`stdcopy.StdCopy` 把 multiplexed 流分离成干净 stdout/stderr，且不做 4KB 截断。

```go
// ContainerExecJSON 在容器内执行一次性命令，返回完整 stdout/stderr。
// 与 ContainerExec 区别：用 stdcopy 分离 stdout/stderr，stdout 不截断，
// 便于上层对 hermes kanban --json 输出做 JSON 解析。
func (a *AgentBackedAdapter) ContainerExecJSON(ctx context.Context, nodeID, containerID string, cmd []string) (ExecJSONResult, error) {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return ExecJSONResult{}, err
	}
	resp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecJSONResult{}, fmt.Errorf("创建 exec 失败: %w", err)
	}
	att, err := cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecJSONResult{}, fmt.Errorf("附加 exec 失败: %w", err)
	}
	defer att.Close()
	// docker multiplexed 流：用 stdcopy 拆成干净的 stdout / stderr 两段。
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, att.Reader); err != nil {
		return ExecJSONResult{}, fmt.Errorf("读取 exec 输出失败: %w", err)
	}
	// exec 结束后 inspect 拿退出码；attach 已读到 EOF，命令通常已退出，仍轮询保险。
	const inspectPollMax = 50
	for i := 0; i < inspectPollMax; i++ {
		insp, err := cli.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			return ExecJSONResult{}, fmt.Errorf("inspect exec 失败: %w", err)
		}
		if !insp.Running {
			return ExecJSONResult{
				ExitCode: insp.ExitCode,
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
			}, nil
		}
		select {
		case <-ctx.Done():
			return ExecJSONResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return ExecJSONResult{}, fmt.Errorf("exec 超时")
}
```

- [ ] **Step 3: 实现 ContainerExecStream**

流式 exec 必须用无 timeout 的 docker client（`agent.NewStreamingDockerClientForNode`），
否则 30s 后底层连接被强制关闭。参考 `docker_proxy.go` 对该工厂的注释。

```go
// ContainerExecStream 在容器内执行流式命令，逐行投递 stdout。
// 用无 timeout 的 streaming docker client，避免 hermes kanban watch 长连接被掐断。
func (a *AgentBackedAdapter) ContainerExecStream(ctx context.Context, nodeID, containerID string, cmd []string) (ExecStreamHandle, error) {
	cli, err := a.streamingDockerClient(ctx, nodeID)
	if err != nil {
		return ExecStreamHandle{}, err
	}
	resp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecStreamHandle{}, fmt.Errorf("创建 exec 失败: %w", err)
	}
	att, err := cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecStreamHandle{}, fmt.Errorf("附加 exec 失败: %w", err)
	}

	lines := make(chan string, 64)
	var streamErr error
	streamCtx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(lines)
		defer att.Close()
		// stdcopy 把 multiplexed 流拆出 stdout 写进 pipe，再按行扫描。
		pr, pw := io.Pipe()
		go func() {
			_, copyErr := stdcopy.StdCopy(pw, io.Discard, att.Reader)
			pw.CloseWithError(copyErr)
		}()
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case <-streamCtx.Done():
				return
			case lines <- scanner.Text():
			}
		}
		if err := scanner.Err(); err != nil && streamCtx.Err() == nil {
			streamErr = err
		}
	}()

	return ExecStreamHandle{
		Lines: lines,
		Err:   func() error { return streamErr },
		Close: cancel,
	}, nil
}
```

- [ ] **Step 4: 补 streamingDockerClient 辅助方法（若不存在）**

确认 `agent_backed.go` 有没有无 timeout client 的构造。`dockerClient` 用的是
`agent.NewDockerClientForNode`。若没有 streaming 版，加一个，与 `dockerClient`
同结构但调 `agent.NewStreamingDockerClientForNode`：

```go
// streamingDockerClient 构造无 timeout 的 docker client，用于 exec 长连接。
func (a *AgentBackedAdapter) streamingDockerClient(ctx context.Context, nodeID string) (*client.Client, error) {
	// 与 dockerClient 一致地解析 node 的 endpoint / token / caCert，
	// 仅把 agent.NewDockerClientForNode 换成 agent.NewStreamingDockerClientForNode。
	// 具体节点信息解析照抄 dockerClient 实现。
}
```

执行时：先读 `dockerClient` 的完整实现，复制其节点信息解析逻辑，只替换 client 工厂函数。

- [ ] **Step 5: 补 imports**

确保 `agent_backed.go` import 了：`bytes`、`bufio`、`io`、`context`、
`github.com/docker/docker/pkg/stdcopy`。

- [ ] **Step 6: 写单测**

`internal/integrations/runtime/agent_backed_test.go` 加测试。docker SDK 难以纯
mock，这里测**纯逻辑可单测的部分**：用一个假的 `io.Reader` 模拟 docker
multiplexed 帧，验证 stdcopy 解析。若现有测试已对 docker client 有 fake，沿用。

```go
// TestExecStreamLineSplitting 验证 ContainerExecStream 的行扫描逻辑：
// docker multiplexed stdout 帧被 stdcopy 还原后，按行投递到 Lines channel。
func TestExecStreamLineSplitting(t *testing.T) {
	// 构造 docker multiplexed stdout 帧：8 字节头(stream=1) + payload。
	frame := func(payload string) []byte {
		hdr := make([]byte, 8)
		hdr[0] = 1 // stdout
		binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
		return append(hdr, []byte(payload)...)
	}
	raw := append(frame("{\"type\":\"a\"}\n"), frame("{\"type\":\"b\"}\n")...)

	var stdout bytes.Buffer
	_, err := stdcopy.StdCopy(&stdout, io.Discard, bytes.NewReader(raw))
	require.NoError(t, err)

	scanner := bufio.NewScanner(&stdout)
	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	// 两行 NDJSON 应被正确切分
	assert.Equal(t, []string{`{"type":"a"}`, `{"type":"b"}`}, got)
}
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/integrations/runtime/... -run TestExecStream -v`
Expected: PASS

- [ ] **Step 8: 全包编译**

Run: `go build ./internal/integrations/runtime/...`
Expected: 编译通过

- [ ] **Step 9: 提交**

```bash
git add internal/integrations/runtime/adapter.go internal/integrations/runtime/agent_backed.go internal/integrations/runtime/agent_backed_test.go
git commit -m "feat(runtime): runtime adapter 新增容器 exec JSON 与流式能力

ContainerExecJSON 用 stdcopy 分离 stdout/stderr 且不截断，供 hermes
kanban --json 输出解析；ContainerExecStream 用无 timeout 的 streaming
docker client 逐行投递 stdout，供 hermes kanban watch 长连接。"
```

---

## Phase C：service 层

### Task C1：Kanban 强类型定义

**Files:**
- Create: `internal/service/hermes_kanban_types.go`

- [ ] **Step 1: 实测一次 kanban --json 输出确定字段（若有真实 hermes 环境）**

若手边有装真实 hermes 的容器：`hermes kanban init && hermes kanban create "t" --assignee default --json`
看实际字段名。无环境则按调研报告
`docs/superpowers/plans/2026-05-19-hermes-task-dashboard.md` §4.2 的 `tasks` 表结构。

- [ ] **Step 2: 写类型文件**

```go
// Package service —— hermes_kanban_types.go 定义 Hermes Kanban CLI --json
// 输出对应的强类型。字段名以 hermes kanban CLI 输出为准，解析时未知字段忽略，
// 缺失字段取零值，避免上游小版本变化直接 break。
package service

// KanbanBoard 对应 `hermes kanban boards list --json` 的单个 board。
type KanbanBoard struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
}

// KanbanTask 对应 `hermes kanban list --json` 的单个任务（列表视图字段）。
type KanbanTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`   // triage|todo|ready|running|blocked|done|archived
	Assignee    string `json:"assignee"`
	Priority    int    `json:"priority"`
	Body        string `json:"body,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	StartedAt   int64  `json:"started_at,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
	Skills      string `json:"skills,omitempty"`
}

// KanbanComment 对应任务详情里的一条评论。
type KanbanComment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

// KanbanEvent 对应任务事件流的一条事件（task_events / watch 输出）。
type KanbanEvent struct {
	Kind      string `json:"kind"`
	Payload   string `json:"payload,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// KanbanTaskRun 对应 `hermes kanban runs <id> --json` 的一次历史执行。
type KanbanTaskRun struct {
	Profile   string `json:"profile"`
	Status    string `json:"status"`
	WorkerPID int    `json:"worker_pid,omitempty"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Error     string `json:"error,omitempty"`
}

// KanbanTaskDetail 对应 `hermes kanban show <id> --json` 的完整任务详情。
// 在 KanbanTask 基础上补 worker / workspace / 评论 / 事件等。
type KanbanTaskDetail struct {
	KanbanTask
	WorkspaceKind   string          `json:"workspace_kind,omitempty"`
	WorkspacePath   string          `json:"workspace_path,omitempty"`
	WorkerPID       int             `json:"worker_pid,omitempty"`
	LastHeartbeatAt int64           `json:"last_heartbeat_at,omitempty"`
	ParentID        string          `json:"parent_id,omitempty"`
	Result          string          `json:"result,omitempty"`
	Comments        []KanbanComment `json:"comments,omitempty"`
	Events          []KanbanEvent   `json:"events,omitempty"`
}

// KanbanStats 对应 `hermes kanban stats --json`，用于工具栏徽标。
// 用 map 承接 per-status 计数，避免上游状态枚举变化。
type KanbanStats struct {
	StatusCounts map[string]int `json:"status_counts"`
}
```

> 注：实际字段名以 Step 1 实测为准；若与上面不符，以实测为准修改 json tag，
> 并同步更新 Task A2 契约测试断言的字段名。

- [ ] **Step 3: 编译确认**

Run: `go build ./internal/service/...`
Expected: 编译通过（仅类型定义，无依赖）

- [ ] **Step 4: 提交（与 C2 合并提交，此步先不 commit）**

跳过，C2 完成后统一提交。

### Task C2：HermesKanbanService —— CLI 执行核心与读 verb

**Files:**
- Create: `internal/service/hermes_kanban.go`
- Modify: `internal/service/errors.go`
- Test: `internal/service/hermes_kanban_test.go`

- [ ] **Step 1: 在 errors.go 加 sentinel error**

在 `internal/service/errors.go` 现有 `var (...)` 块内追加：

```go
	// ErrKanbanForbidden 表示当前 principal 无权访问该实例的 Kanban。
	ErrKanbanForbidden = errors.New("无权访问该实例任务看板")
	// ErrKanbanRuntimeUnavailable 表示实例容器未运行，无法执行 kanban CLI。
	ErrKanbanRuntimeUnavailable = errors.New("实例容器未运行")
	// ErrKanbanNotSupported 表示该实例运行的是 dev stub 镜像，不含真实 hermes。
	ErrKanbanNotSupported = errors.New("该实例镜像不支持任务看板")
	// ErrKanbanCLI 表示 hermes kanban CLI 非零退出。
	ErrKanbanCLI = errors.New("kanban 命令执行失败")
	// ErrKanbanOutputInvalid 表示 kanban CLI 输出不是合法 JSON。
	ErrKanbanOutputInvalid = errors.New("kanban 输出解析失败")
	// ErrKanbanBadRequest 表示 kanban 请求参数非法（board slug / status / task id 等白名单校验失败）。
	ErrKanbanBadRequest = errors.New("kanban 请求参数非法")
```

- [ ] **Step 2: 写 service 骨架 —— 依赖、构造、appID 解析、runCLI**

新建 `internal/service/hermes_kanban.go`。核心是 `runCLI`：解析 appID →
拿 node + container → 调 `runtime.Adapter.ContainerExecJSON`。

```go
// Package service —— hermes_kanban.go 实现 Hermes Kanban 任务看板能力。
// manager 不持有 kanban 数据，全部通过在 hermes 容器内执行 `hermes kanban`
// CLI 并解析 --json 输出获得；写操作同样走 CLI verb。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/runtime"
)

// kanbanExecer 抽象在容器内执行命令的能力，便于单测注入假实现。
type kanbanExecer interface {
	ContainerExecJSON(ctx context.Context, nodeID, containerID string, cmd []string) (runtime.ExecJSONResult, error)
	ContainerExecStream(ctx context.Context, nodeID, containerID string, cmd []string) (runtime.ExecStreamHandle, error)
}

// kanbanAppLocator 把 appID 解析为执行 kanban CLI 所需的运行时坐标。
type kanbanAppLocator interface {
	// LocateApp 返回 app 的归属信息与运行时坐标。
	// stub 表示该 app 运行的是 dev stub 镜像；containerID 为空表示容器未运行。
	LocateApp(ctx context.Context, appID string) (KanbanAppLocation, error)
}

// KanbanAppLocation 是执行 kanban CLI 所需的全部 app 运行时信息。
type KanbanAppLocation struct {
	OrgID       string // app 归属组织，用于权限判断
	OwnerUserID string // app 拥有者，用于 org_member 权限判断
	NodeID      string // app 所在 runtime node
	ContainerID string // hermes 容器 ID，空表示未运行
	Stub        bool   // 是否 dev stub 镜像
}

// boardSlugRe 是 board slug 白名单正则（与 hermes-web-ui normalizeBoardSlug 一致）。
var boardSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// kanbanStatuses 是合法的任务状态枚举白名单。
var kanbanStatuses = map[string]bool{
	"triage": true, "todo": true, "ready": true, "running": true,
	"blocked": true, "done": true, "archived": true,
}

// HermesKanbanService 暴露 Kanban 看板的读写能力。
type HermesKanbanService struct {
	execer  kanbanExecer
	locator kanbanAppLocator
}

// NewHermesKanbanService 构造 service。
func NewHermesKanbanService(execer kanbanExecer, locator kanbanAppLocator) *HermesKanbanService {
	return &HermesKanbanService{execer: execer, locator: locator}
}

// resolve 解析 appID 并做读权限校验，返回执行坐标。
func (s *HermesKanbanService) resolve(ctx context.Context, principal auth.Principal, appID string) (KanbanAppLocation, error) {
	loc, err := s.locator.LocateApp(ctx, appID)
	if err != nil {
		return KanbanAppLocation{}, err
	}
	if !auth.CanViewAppKanban(principal, loc.OrgID, loc.OwnerUserID) {
		return KanbanAppLocation{}, ErrKanbanForbidden
	}
	if loc.Stub {
		return KanbanAppLocation{}, ErrKanbanNotSupported
	}
	if strings.TrimSpace(loc.ContainerID) == "" {
		return KanbanAppLocation{}, ErrKanbanRuntimeUnavailable
	}
	return loc, nil
}

// runCLI 在 hermes 容器内执行一条 kanban 命令并返回 stdout。
// args 必须已是白名单校验过的 argv 切片（不含 "hermes kanban" 前缀）。
func (s *HermesKanbanService) runCLI(ctx context.Context, loc KanbanAppLocation, args []string) ([]byte, error) {
	cmd := append([]string{"hermes", "kanban"}, args...)
	res, err := s.execer.ContainerExecJSON(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanCLI, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if len(msg) > 4096 {
			msg = msg[:4096]
		}
		return nil, fmt.Errorf("%w: exit %d: %s", ErrKanbanCLI, res.ExitCode, msg)
	}
	return []byte(res.Stdout), nil
}

// validateBoard 校验 board slug，空值回退到 "default"。
func validateBoard(board string) (string, error) {
	board = strings.TrimSpace(board)
	if board == "" {
		return "default", nil
	}
	if !boardSlugRe.MatchString(board) {
		return "", fmt.Errorf("%w: 非法 board slug", ErrKanbanBadRequest)
	}
	return board, nil
}
```

- [ ] **Step 3: 加读 verb 方法**

在 `hermes_kanban.go` 追加。读 verb：ListBoards / ListTasks / ShowTask /
TaskRuns / Stats。

```go
// ListBoards 返回实例的所有 kanban board。
func (s *HermesKanbanService) ListBoards(ctx context.Context, principal auth.Principal, appID string) ([]KanbanBoard, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	out, err := s.runCLI(ctx, loc, []string{"boards", "list", "--all", "--json"})
	if err != nil {
		return nil, err
	}
	var boards []KanbanBoard
	if err := json.Unmarshal(out, &boards); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return boards, nil
}

// KanbanTaskFilter 是 ListTasks 的过滤条件。
type KanbanTaskFilter struct {
	Board    string
	Status   string // 空表示不过滤
	Assignee string // 空表示不过滤
}

// ListTasks 返回某 board 的任务列表。
func (s *HermesKanbanService) ListTasks(ctx context.Context, principal auth.Principal, appID string, f KanbanTaskFilter) ([]KanbanTask, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	board, err := validateBoard(f.Board)
	if err != nil {
		return nil, err
	}
	args := []string{"list", "--board", board, "--json"}
	if f.Status != "" {
		if !kanbanStatuses[f.Status] {
			return nil, fmt.Errorf("%w: 非法 status", ErrKanbanBadRequest)
		}
		args = append(args, "--status", f.Status)
	}
	if f.Assignee != "" {
		if !boardSlugRe.MatchString(f.Assignee) {
			return nil, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
		}
		args = append(args, "--assignee", f.Assignee)
	}
	out, err := s.runCLI(ctx, loc, args)
	if err != nil {
		return nil, err
	}
	var tasks []KanbanTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return tasks, nil
}

// taskIDRe 是 kanban 任务 ID 白名单（hermes 形如 t_xxxxxxxx）。
var taskIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ShowTask 返回单个任务的完整详情。
func (s *HermesKanbanService) ShowTask(ctx context.Context, principal auth.Principal, appID, board, taskID string) (KanbanTaskDetail, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	out, err := s.runCLI(ctx, loc, []string{"show", taskID, "--board", b, "--json"})
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	var detail KanbanTaskDetail
	if err := json.Unmarshal(out, &detail); err != nil {
		return KanbanTaskDetail{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return detail, nil
}

// TaskRuns 返回任务的历次执行记录。
func (s *HermesKanbanService) TaskRuns(ctx context.Context, principal auth.Principal, appID, board, taskID string) ([]KanbanTaskRun, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return nil, err
	}
	if !taskIDRe.MatchString(taskID) {
		return nil, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	out, err := s.runCLI(ctx, loc, []string{"runs", taskID, "--board", b, "--json"})
	if err != nil {
		return nil, err
	}
	var runs []KanbanTaskRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return runs, nil
}

// Stats 返回某 board 的 per-status 统计。
func (s *HermesKanbanService) Stats(ctx context.Context, principal auth.Principal, appID, board string) (KanbanStats, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanStats{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return KanbanStats{}, err
	}
	out, err := s.runCLI(ctx, loc, []string{"stats", "--board", b, "--json"})
	if err != nil {
		return KanbanStats{}, err
	}
	var stats KanbanStats
	if err := json.Unmarshal(out, &stats); err != nil {
		return KanbanStats{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return stats, nil
}
```

- [ ] **Step 4: 写读 verb 单测**

`internal/service/hermes_kanban_test.go`。用假 execer + 假 locator。

```go
package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
)

// fakeKanbanExecer 记录最后一次执行的 cmd，并按预设返回结果。
type fakeKanbanExecer struct {
	lastCmd []string
	result  runtime.ExecJSONResult
	err     error
}

func (f *fakeKanbanExecer) ContainerExecJSON(_ context.Context, _, _ string, cmd []string) (runtime.ExecJSONResult, error) {
	f.lastCmd = cmd
	return f.result, f.err
}

func (f *fakeKanbanExecer) ContainerExecStream(_ context.Context, _, _ string, cmd []string) (runtime.ExecStreamHandle, error) {
	f.lastCmd = cmd
	return runtime.ExecStreamHandle{}, f.err
}

// fakeKanbanLocator 返回预设的 app 运行时坐标。
type fakeKanbanLocator struct {
	loc KanbanAppLocation
	err error
}

func (f *fakeKanbanLocator) LocateApp(_ context.Context, _ string) (KanbanAppLocation, error) {
	return f.loc, f.err
}

// healthyLoc 是一个正常运行、可访问的 app 坐标。
func healthyLoc() KanbanAppLocation {
	return KanbanAppLocation{OrgID: "org-1", OwnerUserID: "u-1", NodeID: "n-1", ContainerID: "c-1"}
}

// orgAdmin 是 org-1 的组织管理员 principal。
func kanbanOrgAdmin() auth.Principal {
	return auth.Principal{UserID: "admin-1", OrgID: "org-1", Role: domain.UserRoleOrgAdmin}
}

// TestListTasksHappy 验证：正常 app 上 ListTasks 解析 CLI JSON 输出。
func TestListTasksHappy(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   `[{"id":"t_1","title":"任务一","status":"running","assignee":"devops","priority":3}]`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	tasks, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t_1", tasks[0].ID)
	assert.Equal(t, "running", tasks[0].Status)
	// 校验 argv：board 缺省回退 default、带 --json
	assert.Equal(t, []string{"hermes", "kanban", "list", "--board", "default", "--json"}, execer.lastCmd)
}

// TestListTasksRejectsBadStatus 验证：非法 status 过滤值被白名单拦截，不下发 CLI。
func TestListTasksRejectsBadStatus(t *testing.T) {
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{Status: "bogus; rm -rf"})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Nil(t, execer.lastCmd) // 非法输入不应触达 execer
}

// TestResolveForbidden 验证：非本组织用户访问 Kanban 被拒。
func TestResolveForbidden(t *testing.T) {
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: healthyLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}

	_, err := svc.ListTasks(context.Background(), outsider, "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanForbidden)
}

// TestResolveStubUnsupported 验证：dev stub 镜像实例返回 ErrKanbanNotSupported。
func TestResolveStubUnsupported(t *testing.T) {
	loc := healthyLoc()
	loc.Stub = true
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: loc})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanNotSupported)
}

// TestResolveRuntimeUnavailable 验证：容器未运行返回 ErrKanbanRuntimeUnavailable。
func TestResolveRuntimeUnavailable(t *testing.T) {
	loc := healthyLoc()
	loc.ContainerID = ""
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: loc})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanRuntimeUnavailable)
}

// TestRunCLINonZeroExit 验证：CLI 非零退出被包成 ErrKanbanCLI 且带 stderr。
func TestRunCLINonZeroExit(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 2, Stderr: "unknown task"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanCLI)
	assert.Contains(t, err.Error(), "unknown task")
}

// TestListTasksInvalidJSON 验证：CLI 输出非法 JSON 返回 ErrKanbanOutputInvalid。
func TestListTasksInvalidJSON(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: "not json"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanOutputInvalid)
}
```

- [ ] **Step 5: 运行读 verb 测试**

Run: `go test ./internal/service/ -run 'TestListTasks|TestResolve|TestRunCLI' -v`
Expected: 全部 PASS（依赖 Task D1 的 `CanViewAppKanban`，若尚未实现先做 D1 Step 1-2）

> 注意：本 task 用到 `auth.CanViewAppKanban`，请先完成 **Task D1 Step 1-2**
> （加权限函数）再跑测试。或把 D1 Step 1-2 提到本 task 之前执行。

- [ ] **Step 6: 提交**

```bash
git add internal/service/hermes_kanban.go internal/service/hermes_kanban_types.go internal/service/hermes_kanban_test.go internal/service/errors.go
git commit -m "feat(kanban): 新增 HermesKanbanService 读能力

service 通过容器内 hermes kanban CLI 获取 board / 任务 / 详情 / 历史执行
/ 统计；所有参数走白名单校验，输出按强类型解析。覆盖权限、stub 镜像、
容器未运行、CLI 失败、JSON 非法等路径的单测。"
```

### Task C3：HermesKanbanService —— 写 verb

**Files:**
- Modify: `internal/service/hermes_kanban.go`
- Modify: `internal/service/hermes_kanban_test.go`

- [ ] **Step 1: 加写权限辅助 + CreateTask**

写 verb 需要管理权限。先加一个内部 helper，再实现 CreateTask（字段最复杂）。

```go
// resolveManage 解析 appID 并做写权限校验。
func (s *HermesKanbanService) resolveManage(ctx context.Context, principal auth.Principal, appID string) (KanbanAppLocation, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanAppLocation{}, err
	}
	if !auth.CanManageAppKanban(principal, loc.OrgID, loc.OwnerUserID) {
		return KanbanAppLocation{}, ErrKanbanForbidden
	}
	return loc, nil
}

// CreateKanbanTaskInput 是新建任务的输入。
// 基础字段所有可写角色都能填；高级字段仅平台管理员可填，handler 层按角色 strip。
type CreateKanbanTaskInput struct {
	Board    string
	Title    string // 必填
	Body     string
	Assignee string // 必填
	Priority int
	// 以下为高级字段（仅平台管理员）
	Skills        string
	WorkspaceKind string
	WorkspacePath string
	ParentID      string
	MaxRetries    int
}

// CreateTask 创建一个新任务，返回新任务详情。
func (s *HermesKanbanService) CreateTask(ctx context.Context, principal auth.Principal, appID string, in CreateKanbanTaskInput) (KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	board, err := validateBoard(in.Board)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 标题不能为空", ErrKanbanBadRequest)
	}
	if !boardSlugRe.MatchString(in.Assignee) {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
	}
	if in.Priority < 0 || in.Priority > 9 {
		return KanbanTaskDetail{}, fmt.Errorf("%w: priority 越界", ErrKanbanBadRequest)
	}
	// title / body 等自由文本作为独立 argv 元素传入，绝不拼 shell。
	args := []string{"create", in.Title, "--board", board, "--assignee", in.Assignee,
		"--priority", fmt.Sprintf("%d", in.Priority), "--json"}
	if in.Body != "" {
		args = append(args, "--body", in.Body)
	}
	if in.Skills != "" {
		args = append(args, "--skills", in.Skills)
	}
	if in.WorkspaceKind != "" {
		args = append(args, "--workspace-kind", in.WorkspaceKind)
	}
	if in.WorkspacePath != "" {
		args = append(args, "--workspace-path", in.WorkspacePath)
	}
	if in.ParentID != "" {
		if !taskIDRe.MatchString(in.ParentID) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 parent id", ErrKanbanBadRequest)
		}
		args = append(args, "--parent", in.ParentID)
	}
	if in.MaxRetries > 0 {
		args = append(args, "--max-retries", fmt.Sprintf("%d", in.MaxRetries))
	}
	out, err := s.runCLI(ctx, loc, args)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	var detail KanbanTaskDetail
	if err := json.Unmarshal(out, &detail); err != nil {
		return KanbanTaskDetail{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return detail, nil
}
```

> 注：`create` 的 flag 名（`--body` / `--skills` / `--workspace-kind` / `--parent`
> / `--max-retries`）以 `hermes kanban create --help` 实测为准；执行 Task C1 Step 1
> 时一并记录，与此处不符则修正。

- [ ] **Step 2: 加其余写 verb（结构同构）**

以下 7 个写 verb 结构一致：`resolveManage` → `validateBoard` → 校验 taskID →
`runCLI` → 不需要解析返回值（或解析为更新后的 task）。给出 `Comment` 完整实现作
范例，其余按表格套用同一模板。

```go
// Comment 给任务追加一条评论。
func (s *HermesKanbanService) Comment(ctx context.Context, principal auth.Principal, appID, board, taskID, body string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w: 评论内容不能为空", ErrKanbanBadRequest)
	}
	_, err = s.runCLI(ctx, loc, []string{"comment", taskID, body, "--board", b})
	return err
}
```

其余 6 个 verb 按同一模板实现，差异如下表（`<id>` = 已校验 taskID，
`-b` = `"--board", b`）：

| 方法 | 签名补充参数 | runCLI argv | 额外校验 |
|---|---|---|---|
| `Complete` | `result string` | `["complete", <id>, "--result", result, -b]`（result 空则省略 `--result`） | 无 |
| `Block` | `reason string` | `["block", <id>, reason, -b]` | reason 非空 |
| `Unblock` | — | `["unblock", <id>, -b]` | 无 |
| `Archive` | — | `["archive", <id>, -b]` | 无 |
| `Reassign` | `profile string` | `["reassign", <id>, "--to", profile, -b]` | `boardSlugRe.MatchString(profile)` |
| `Reclaim` | — | `["reclaim", <id>, -b]` | 无 |

每个方法签名形如：
`func (s *HermesKanbanService) Complete(ctx context.Context, principal auth.Principal, appID, board, taskID, result string) error`

每个方法体照抄 `Comment` 的结构（resolveManage / validateBoard / taskID 校验 /
额外校验 / runCLI / return err），只换 argv 与额外校验。

> verb 的具体 flag 名以 `hermes kanban <verb> --help` 实测为准。

- [ ] **Step 3: 写写 verb 单测**

在 `hermes_kanban_test.go` 追加。

```go
// TestCreateTaskHappy 验证：CreateTask 拼出正确 argv 并解析返回详情。
func TestCreateTaskHappy(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   `{"id":"t_new","title":"新任务","status":"todo","assignee":"devops","priority":2}`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	detail, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops", Priority: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, "t_new", detail.ID)
	// 自由文本 title 必须作为独立 argv 元素
	assert.Contains(t, execer.lastCmd, "新任务")
}

// TestCreateTaskRejectsEmptyTitle 验证：空标题被拦截。
func TestCreateTaskRejectsEmptyTitle(t *testing.T) {
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: healthyLoc()})
	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "  ", Assignee: "devops",
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
}

// TestWriteVerbForbiddenForOutsider 验证：非本组织成员不能写。
func TestWriteVerbForbiddenForOutsider(t *testing.T) {
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: healthyLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}
	err := svc.Comment(context.Background(), outsider, "app-1", "default", "t_1", "hi")
	require.ErrorIs(t, err, ErrKanbanForbidden)
}

// TestCommentRejectsBadTaskID 验证：非法 task id 不下发 CLI。
func TestCommentRejectsBadTaskID(t *testing.T) {
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
	err := svc.Comment(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1; rm -rf /", "hi")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Nil(t, execer.lastCmd)
}

// TestCompleteHappy 验证：Complete 拼出含 --result 的 argv。
func TestCompleteHappy(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: "ok"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
	err := svc.Complete(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "已完成")
	require.NoError(t, err)
	assert.Contains(t, execer.lastCmd, "complete")
	assert.Contains(t, execer.lastCmd, "已完成")
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/service/ -run 'TestCreateTask|TestWriteVerb|TestComment|TestComplete' -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/service/hermes_kanban.go internal/service/hermes_kanban_test.go
git commit -m "feat(kanban): HermesKanbanService 新增写能力

新增 CreateTask / Comment / Complete / Block / Unblock / Archive /
Reassign / Reclaim 写 verb，统一走管理权限校验与参数白名单；自由文本
作为独立 argv 传入，杜绝 shell 注入。"
```

### Task C4：HermesKanbanService —— 实时事件流

**Files:**
- Modify: `internal/service/hermes_kanban.go`
- Modify: `internal/service/hermes_kanban_test.go`

- [ ] **Step 1: 加 StreamEvents 方法**

```go
// StreamEvents 在 hermes 容器内执行 `kanban watch` 并把每行 NDJSON 投递到回调。
// 该方法阻塞直到 ctx 取消、流结束或出错。board watch 覆盖整个看板所有任务事件。
func (s *HermesKanbanService) StreamEvents(ctx context.Context, principal auth.Principal, appID, board string, onLine func(line string)) error {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	cmd := []string{"hermes", "kanban", "watch", "--board", b, "--json"}
	handle, err := s.execer.ContainerExecStream(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrKanbanCLI, err)
	}
	defer handle.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-handle.Lines:
			if !ok {
				if e := handle.Err(); e != nil {
					return fmt.Errorf("%w: %v", ErrKanbanCLI, e)
				}
				return nil
			}
			onLine(line)
		}
	}
}
```

> `kanban watch` 是否支持 `--json` flag 以实测为准；若不支持则去掉，输出按
> 原始行投递（前端按文本展示）。

- [ ] **Step 2: 写 StreamEvents 单测**

```go
// fakeStreamExecer 是支持流式输出的假 execer。
type fakeStreamExecer struct {
	lines []string
}

func (f *fakeStreamExecer) ContainerExecJSON(_ context.Context, _, _ string, _ []string) (runtime.ExecJSONResult, error) {
	return runtime.ExecJSONResult{}, nil
}

func (f *fakeStreamExecer) ContainerExecStream(_ context.Context, _, _ string, _ []string) (runtime.ExecStreamHandle, error) {
	ch := make(chan string, len(f.lines))
	for _, l := range f.lines {
		ch <- l
	}
	close(ch)
	return runtime.ExecStreamHandle{
		Lines: ch,
		Err:   func() error { return nil },
		Close: func() {},
	}, nil
}

// TestStreamEventsDeliversLines 验证：StreamEvents 把流式行逐条交给回调。
func TestStreamEventsDeliversLines(t *testing.T) {
	execer := &fakeStreamExecer{lines: []string{`{"kind":"claimed"}`, `{"kind":"heartbeat"}`}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	var got []string
	err := svc.StreamEvents(context.Background(), kanbanOrgAdmin(), "app-1", "default", func(l string) {
		got = append(got, l)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`{"kind":"claimed"}`, `{"kind":"heartbeat"}`}, got)
}
```

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/service/ -run TestStreamEvents -v`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/service/hermes_kanban.go internal/service/hermes_kanban_test.go
git commit -m "feat(kanban): HermesKanbanService 新增实时事件流

StreamEvents 在容器内跑 hermes kanban watch，把 NDJSON 逐行交给回调，
供 handler 转 SSE 推送前端。"
```

---

## Phase D：handler、路由、权限

### Task D1：权限谓词

**Files:**
- Modify: `internal/auth/authorizer.go`
- Test: `internal/auth/authorizer_test.go`

- [ ] **Step 1: 加权限函数**

在 `authorizer.go` 的 `CanManageApp` 等函数附近追加。Kanban 读写权限直接复用
应用读权限（spec §7.4：所有能看实例详情的角色都能读写 Kanban）。

```go
// CanViewAppKanban 判断 principal 能否查看应用的任务看板。
// 与查看应用详情同权限：平台管理员、本组织管理员、应用拥有者本人。
func CanViewAppKanban(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanManageAppKanban 判断 principal 能否对任务看板做写操作（评论 / 完成 / 阻塞等）。
// spec 规定：所有能查看实例详情的角色都可写，因此与 CanViewApp 一致。
func CanManageAppKanban(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}
```

- [ ] **Step 2: 写权限单测**

在 `authorizer_test.go` 追加：

```go
// TestCanViewAppKanban 验证 Kanban 读权限三层角色判断。
func TestCanViewAppKanban(t *testing.T) {
	// 平台管理员：跨组织可看
	assert.True(t, CanViewAppKanban(
		Principal{Role: domain.UserRolePlatformAdmin}, "org-1", "owner-1"))
	// 本组织管理员：可看本组织应用
	assert.True(t, CanViewAppKanban(
		Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}, "org-1", "owner-1"))
	// 外组织管理员：不可看
	assert.False(t, CanViewAppKanban(
		Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-2"}, "org-1", "owner-1"))
	// 应用拥有者本人：可看
	assert.True(t, CanViewAppKanban(
		Principal{Role: domain.UserRoleOrgMember, UserID: "owner-1"}, "org-1", "owner-1"))
	// 非拥有者的普通成员：不可看
	assert.False(t, CanViewAppKanban(
		Principal{Role: domain.UserRoleOrgMember, UserID: "other"}, "org-1", "owner-1"))
}

// TestCanManageAppKanban 验证 Kanban 写权限：与读权限一致（所有可见角色可写）。
func TestCanManageAppKanban(t *testing.T) {
	// 应用拥有者本人可写
	assert.True(t, CanManageAppKanban(
		Principal{Role: domain.UserRoleOrgMember, UserID: "owner-1"}, "org-1", "owner-1"))
	// 外组织成员不可写
	assert.False(t, CanManageAppKanban(
		Principal{Role: domain.UserRoleOrgMember, UserID: "x"}, "org-1", "owner-1"))
}
```

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/auth/ -run TestCan.*Kanban -v`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go
git commit -m "feat(auth): 新增任务看板读写权限谓词

CanViewAppKanban / CanManageAppKanban 复用应用读权限，与设计一致：
所有能查看实例详情的角色均可读写其任务看板。"
```

### Task D2：请求 DTO 与错误映射

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/request_errors.go`

- [ ] **Step 1: 加请求 DTO**

在 `dto.go` 追加（导出大写名供 swag 扫描）：

```go
// CreateKanbanTaskRequest 是新建 Kanban 任务的请求体。
// 高级字段（Skills/WorkspaceKind/WorkspacePath/ParentID/MaxRetries）仅平台
// 管理员可生效，handler 对非平台管理员会忽略这些字段。
type CreateKanbanTaskRequest struct {
	Board         string `json:"board"`
	Title         string `json:"title" binding:"required"`
	Body          string `json:"body"`
	Assignee      string `json:"assignee" binding:"required"`
	Priority      int    `json:"priority"`
	Skills        string `json:"skills"`
	WorkspaceKind string `json:"workspace_kind"`
	WorkspacePath string `json:"workspace_path"`
	ParentID      string `json:"parent_id"`
	MaxRetries    int    `json:"max_retries"`
}

// KanbanCommentRequest 是给任务加评论的请求体。
type KanbanCommentRequest struct {
	Board string `json:"board"`
	Body  string `json:"body" binding:"required"`
}

// KanbanCompleteRequest 是标记任务完成的请求体。
type KanbanCompleteRequest struct {
	Board  string `json:"board"`
	Result string `json:"result"`
}

// KanbanBlockRequest 是阻塞任务的请求体。
type KanbanBlockRequest struct {
	Board  string `json:"board"`
	Reason string `json:"reason" binding:"required"`
}

// KanbanReassignRequest 是重新分配任务的请求体。
type KanbanReassignRequest struct {
	Board string `json:"board"`
	To    string `json:"to" binding:"required"`
}

// KanbanBoardRequest 是仅需指定 board 的写操作（unblock / archive / reclaim）请求体。
type KanbanBoardRequest struct {
	Board string `json:"board"`
}
```

- [ ] **Step 2: 加错误映射规则**

在 `request_errors.go` 的 `mappedServiceErrorRules` 切片追加（参考现有规则结构，
字段名以现有 struct 为准）：

```go
	{target: service.ErrKanbanForbidden, statusCode: http.StatusForbidden, code: "KANBAN_FORBIDDEN", message: "无权访问该实例任务看板"},
	{target: service.ErrKanbanRuntimeUnavailable, statusCode: http.StatusServiceUnavailable, code: "RUNTIME_NOT_AVAILABLE", message: "实例容器未运行，请先在运行时 tab 启动"},
	{target: service.ErrKanbanNotSupported, statusCode: http.StatusServiceUnavailable, code: "KANBAN_NOT_SUPPORTED_ON_STUB", message: "该实例运行的是 dev 镜像，任务看板不可用"},
	{target: service.ErrKanbanBadRequest, statusCode: http.StatusBadRequest, code: "KANBAN_BAD_REQUEST", message: "任务看板请求参数非法"},
	{target: service.ErrKanbanCLI, statusCode: http.StatusBadGateway, code: "KANBAN_CLI_ERROR", message: "任务看板命令执行失败", safe: true},
	{target: service.ErrKanbanOutputInvalid, statusCode: http.StatusBadGateway, code: "KANBAN_OUTPUT_INVALID", message: "Hermes 版本可能不兼容，请联系平台管理员"},
```

> 先 `grep -n 'mappedServiceErrorRules\|type.*errorRule' internal/api/handlers/request_errors.go`
> 确认 rule struct 的实际字段名（`target` / `statusCode` / `code` / `message`
> / `safe` 等），按实际结构调整。

- [ ] **Step 3: 编译确认**

Run: `go build ./internal/api/handlers/...`
Expected: 编译通过

- [ ] **Step 4: 提交（与 D3 合并，此步暂不 commit）**

跳过，D3 完成后统一提交。

### Task D3：HermesKanbanHandler —— 读端点

**Files:**
- Create: `internal/api/handlers/hermes_kanban.go`
- Test: `internal/api/handlers/hermes_kanban_test.go`

- [ ] **Step 1: 写 handler 骨架与读端点**

```go
// Package handlers —— hermes_kanban.go 暴露实例任务看板的 HTTP 端点。
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// hermesKanbanService 抽象 handler 依赖的 Kanban 业务能力，便于单测注入 stub。
type hermesKanbanService interface {
	ListBoards(ctx context.Context, p auth.Principal, appID string) ([]service.KanbanBoard, error)
	ListTasks(ctx context.Context, p auth.Principal, appID string, f service.KanbanTaskFilter) ([]service.KanbanTask, error)
	ShowTask(ctx context.Context, p auth.Principal, appID, board, taskID string) (service.KanbanTaskDetail, error)
	TaskRuns(ctx context.Context, p auth.Principal, appID, board, taskID string) ([]service.KanbanTaskRun, error)
	Stats(ctx context.Context, p auth.Principal, appID, board string) (service.KanbanStats, error)
	StreamEvents(ctx context.Context, p auth.Principal, appID, board string, onLine func(string)) error
	CreateTask(ctx context.Context, p auth.Principal, appID string, in service.CreateKanbanTaskInput) (service.KanbanTaskDetail, error)
	Comment(ctx context.Context, p auth.Principal, appID, board, taskID, body string) error
	Complete(ctx context.Context, p auth.Principal, appID, board, taskID, result string) error
	Block(ctx context.Context, p auth.Principal, appID, board, taskID, reason string) error
	Unblock(ctx context.Context, p auth.Principal, appID, board, taskID string) error
	Archive(ctx context.Context, p auth.Principal, appID, board, taskID string) error
	Reassign(ctx context.Context, p auth.Principal, appID, board, taskID, profile string) error
	Reclaim(ctx context.Context, p auth.Principal, appID, board, taskID string) error
}

// HermesKanbanHandler 处理 /api/v1/apps/:appId/hermes/kanban/* 路由。
type HermesKanbanHandler struct {
	service hermesKanbanService
}

// NewHermesKanbanHandler 构造 handler。
func NewHermesKanbanHandler(svc hermesKanbanService) *HermesKanbanHandler {
	return &HermesKanbanHandler{service: svc}
}

// RegisterHermesKanbanRoutes 注册任务看板路由。
func RegisterHermesKanbanRoutes(router gin.IRouter, h *HermesKanbanHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/kanban")
	g.GET("/boards", h.ListBoards)
	g.GET("/tasks", h.ListTasks)
	g.GET("/tasks/:taskId", h.ShowTask)
	g.GET("/tasks/:taskId/runs", h.TaskRuns)
	g.GET("/events", h.StreamEvents) // board 级订阅，不带 taskId（spec §9.2 整 board watch）
	g.GET("/stats", h.Stats)
	g.POST("/tasks", h.CreateTask)
	g.POST("/tasks/:taskId/comment", h.Comment)
	g.POST("/tasks/:taskId/complete", h.Complete)
	g.POST("/tasks/:taskId/block", h.Block)
	g.POST("/tasks/:taskId/unblock", h.Unblock)
	g.POST("/tasks/:taskId/archive", h.Archive)
	g.POST("/tasks/:taskId/reassign", h.Reassign)
	g.POST("/tasks/:taskId/reclaim", h.Reclaim)
}

// writeKanbanError 把 service sentinel error 映射为 HTTP 响应。
func writeKanbanError(c *gin.Context, err error) {
	writeMappedServiceError(c, err, http.StatusInternalServerError, "任务看板服务暂不可用")
}

// ListBoards GET /api/v1/apps/{appId}/hermes/kanban/boards
//
// @Summary      列出实例任务看板的 board
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string][]service.KanbanBoard
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/boards [get]
func (h *HermesKanbanHandler) ListBoards(c *gin.Context) {
	boards, err := h.service.ListBoards(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"boards": boards})
}

// ListTasks GET /api/v1/apps/{appId}/hermes/kanban/tasks
//
// @Summary      列出某 board 的任务
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path      string  true   "应用 ID"
// @Param        board     query     string  false  "board slug，缺省 default"
// @Param        status    query     string  false  "按状态过滤"
// @Param        assignee  query     string  false  "按 assignee 过滤"
// @Success      200       {object}  map[string][]service.KanbanTask
// @Failure      403       {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks [get]
func (h *HermesKanbanHandler) ListTasks(c *gin.Context) {
	tasks, err := h.service.ListTasks(c.Request.Context(), principalFromCtx(c), c.Param("appId"), service.KanbanTaskFilter{
		Board:    c.Query("board"),
		Status:   c.Query("status"),
		Assignee: c.Query("assignee"),
	})
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

// ShowTask GET /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}
//
// @Summary      查询单个任务详情
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string  true   "应用 ID"
// @Param        taskId  path      string  true   "任务 ID"
// @Param        board   query     string  false  "board slug"
// @Success      200     {object}  map[string]service.KanbanTaskDetail
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId} [get]
func (h *HermesKanbanHandler) ShowTask(c *gin.Context) {
	detail, err := h.service.ShowTask(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"), c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// TaskRuns GET /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/runs
//
// @Summary      查询任务历次执行
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string  true   "应用 ID"
// @Param        taskId  path      string  true   "任务 ID"
// @Param        board   query     string  false  "board slug"
// @Success      200     {object}  map[string][]service.KanbanTaskRun
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/runs [get]
func (h *HermesKanbanHandler) TaskRuns(c *gin.Context) {
	runs, err := h.service.TaskRuns(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"), c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

// Stats GET /api/v1/apps/{appId}/hermes/kanban/stats
//
// @Summary      查询任务看板统计
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true   "应用 ID"
// @Param        board  query     string  false  "board slug"
// @Success      200    {object}  map[string]service.KanbanStats
// @Router       /apps/{appId}/hermes/kanban/stats [get]
func (h *HermesKanbanHandler) Stats(c *gin.Context) {
	stats, err := h.service.Stats(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}
```

- [ ] **Step 2: 写读端点单测**

`internal/api/handlers/hermes_kanban_test.go`：

```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// kanbanServiceStub 是 hermesKanbanService 的可控 stub。
type kanbanServiceStub struct {
	tasks      []service.KanbanTask
	detail     service.KanbanTaskDetail
	createIn   service.CreateKanbanTaskInput // 记录最后一次 CreateTask 入参
	err        error
}

func (s *kanbanServiceStub) ListBoards(_ context.Context, _ auth.Principal, _ string) ([]service.KanbanBoard, error) {
	return nil, s.err
}
func (s *kanbanServiceStub) ListTasks(_ context.Context, _ auth.Principal, _ string, _ service.KanbanTaskFilter) ([]service.KanbanTask, error) {
	return s.tasks, s.err
}
func (s *kanbanServiceStub) ShowTask(_ context.Context, _ auth.Principal, _, _, _ string) (service.KanbanTaskDetail, error) {
	return s.detail, s.err
}
func (s *kanbanServiceStub) TaskRuns(_ context.Context, _ auth.Principal, _, _, _ string) ([]service.KanbanTaskRun, error) {
	return nil, s.err
}
func (s *kanbanServiceStub) Stats(_ context.Context, _ auth.Principal, _, _ string) (service.KanbanStats, error) {
	return service.KanbanStats{}, s.err
}
func (s *kanbanServiceStub) StreamEvents(_ context.Context, _ auth.Principal, _, _ string, _ func(string)) error {
	return s.err
}
func (s *kanbanServiceStub) CreateTask(_ context.Context, _ auth.Principal, _ string, in service.CreateKanbanTaskInput) (service.KanbanTaskDetail, error) {
	s.createIn = in
	return s.detail, s.err
}
func (s *kanbanServiceStub) Comment(_ context.Context, _ auth.Principal, _, _, _, _ string) error { return s.err }
func (s *kanbanServiceStub) Complete(_ context.Context, _ auth.Principal, _, _, _, _ string) error { return s.err }
func (s *kanbanServiceStub) Block(_ context.Context, _ auth.Principal, _, _, _, _ string) error   { return s.err }
func (s *kanbanServiceStub) Unblock(_ context.Context, _ auth.Principal, _, _, _ string) error    { return s.err }
func (s *kanbanServiceStub) Archive(_ context.Context, _ auth.Principal, _, _, _ string) error    { return s.err }
func (s *kanbanServiceStub) Reassign(_ context.Context, _ auth.Principal, _, _, _, _ string) error { return s.err }
func (s *kanbanServiceStub) Reclaim(_ context.Context, _ auth.Principal, _, _, _ string) error    { return s.err }

// newKanbanTestRouter 构造挂载了 kanban 路由的测试 router。
func newKanbanTestRouter(t *testing.T, svc hermesKanbanService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	RegisterHermesKanbanRoutes(r, NewHermesKanbanHandler(svc))
	return r
}

// TestKanbanListTasksHappy 验证：列任务端点返回 200 与 tasks 字段。
func TestKanbanListTasksHappy(t *testing.T) {
	stub := &kanbanServiceStub{tasks: []service.KanbanTask{{ID: "t_1", Title: "任务一"}}}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "t_1")
}

// TestKanbanListTasksForbidden 验证：service 返回 ErrKanbanForbidden 时端点返回 403。
func TestKanbanListTasksForbidden(t *testing.T) {
	stub := &kanbanServiceStub{err: service.ErrKanbanForbidden}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-2"})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestKanbanStubReturns503 验证：stub 镜像实例返回 503。
func TestKanbanStubReturns503(t *testing.T) {
	stub := &kanbanServiceStub{err: service.ErrKanbanNotSupported}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "KANBAN_NOT_SUPPORTED_ON_STUB")
}
```

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/api/handlers/ -run TestKanban -v`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/api/handlers/hermes_kanban.go internal/api/handlers/hermes_kanban_test.go internal/api/handlers/dto.go internal/api/handlers/request_errors.go
git commit -m "feat(kanban): 新增任务看板 HTTP handler 读端点

HermesKanbanHandler 暴露 boards/tasks/show/runs/stats 读端点，统一错误
映射；请求 DTO 与 service 错误映射规则一并补全。"
```

### Task D4：HermesKanbanHandler —— 写端点与 SSE

**Files:**
- Modify: `internal/api/handlers/hermes_kanban.go`
- Modify: `internal/api/handlers/hermes_kanban_test.go`

- [ ] **Step 1: 加 CreateTask 端点（含字段级权限 strip）**

```go
// CreateTask POST /api/v1/apps/{appId}/hermes/kanban/tasks
//
// @Summary      新建任务
// @Description  创建一个 Kanban 任务。Skills/WorkspaceKind/WorkspacePath/ParentID/MaxRetries 仅平台管理员可生效。
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                   true  "应用 ID"
// @Param        body   body      CreateKanbanTaskRequest  true  "新建任务请求"
// @Success      200    {object}  map[string]service.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks [post]
func (h *HermesKanbanHandler) CreateTask(c *gin.Context) {
	var req CreateKanbanTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	principal := principalFromCtx(c)
	in := service.CreateKanbanTaskInput{
		Board:    req.Board,
		Title:    req.Title,
		Body:     req.Body,
		Assignee: req.Assignee,
		Priority: req.Priority,
	}
	// 高级字段仅平台管理员生效：非平台管理员的高级字段被静默丢弃（spec §5.5）。
	if principal.Role == domain.UserRolePlatformAdmin {
		in.Skills = req.Skills
		in.WorkspaceKind = req.WorkspaceKind
		in.WorkspacePath = req.WorkspacePath
		in.ParentID = req.ParentID
		in.MaxRetries = req.MaxRetries
	}
	detail, err := h.service.CreateTask(c.Request.Context(), principal, c.Param("appId"), in)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}
```

> 需 import `"oc-manager/internal/domain"`。

- [ ] **Step 2: 加其余写端点**

`Comment` 完整范例：

```go
// Comment POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/comment
//
// @Summary      给任务加评论
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string                true  "应用 ID"
// @Param        taskId  path      string                true  "任务 ID"
// @Param        body    body      KanbanCommentRequest  true  "评论请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/comment [post]
func (h *HermesKanbanHandler) Comment(c *gin.Context) {
	var req KanbanCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	err := h.service.Comment(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"), req.Body)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
```

其余 6 个写端点同构，差异：

| 方法 | 请求 DTO | service 调用 |
|---|---|---|
| `Complete` | `KanbanCompleteRequest` | `h.service.Complete(ctx, p, appId, req.Board, taskId, req.Result)` |
| `Block` | `KanbanBlockRequest` | `h.service.Block(ctx, p, appId, req.Board, taskId, req.Reason)` |
| `Unblock` | `KanbanBoardRequest` | `h.service.Unblock(ctx, p, appId, req.Board, taskId)` |
| `Archive` | `KanbanBoardRequest` | `h.service.Archive(ctx, p, appId, req.Board, taskId)` |
| `Reassign` | `KanbanReassignRequest` | `h.service.Reassign(ctx, p, appId, req.Board, taskId, req.To)` |
| `Reclaim` | `KanbanBoardRequest` | `h.service.Reclaim(ctx, p, appId, req.Board, taskId)` |

每个方法体照抄 `Comment` 结构（bind → 调 service → 成功 204 / 失败
`writeKanbanError`），换 DTO 类型与 service 调用。各方法补对应 swag 注解。

- [ ] **Step 3: 加 SSE 事件流端点**

```go
// StreamEvents GET /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/events
//
// @Summary      订阅任务看板实时事件流（SSE）
// @Description  以 Server-Sent Events 推送 hermes kanban watch 的 NDJSON 事件。board 维度订阅。
// @Tags         hermes-kanban
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        appId  path      string  true   "应用 ID"
// @Param        board  query     string  false  "board slug"
// @Success      200
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/events [get]
func (h *HermesKanbanHandler) StreamEvents(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁止反代缓冲

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务端不支持流式响应"))
		return
	}

	err := h.service.StreamEvents(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"), func(line string) {
		// 每行 NDJSON 包成一个 SSE data 事件。
		_, _ = c.Writer.WriteString("data: " + line + "\n\n")
		flusher.Flush()
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		// 流已经开始时无法再改 HTTP 状态码，只能写一个 error 事件后结束。
		_, _ = c.Writer.WriteString("event: error\ndata: " + err.Error() + "\n\n")
		flusher.Flush()
	}
}
```

> 该路由在 D3 Step 1 已注册为 `/events`（board 级订阅，不带 taskId —— spec §9.2
> 是整 board watch）。前端 Task G1 连的也是 `/hermes/kanban/events`。

- [ ] **Step 4: 写写端点单测**

在 `hermes_kanban_test.go` 追加：

```go
// TestKanbanCreateStripsAdvancedFieldsForOrgAdmin 验证：
// 组织管理员提交高级字段（skills 等）时被 handler 静默丢弃。
func TestKanbanCreateStripsAdvancedFieldsForOrgAdmin(t *testing.T) {
	stub := &kanbanServiceStub{detail: service.KanbanTaskDetail{KanbanTask: service.KanbanTask{ID: "t_new"}}}
	r := newKanbanTestRouter(t, stub)

	body := `{"title":"x","assignee":"devops","skills":"bash","workspace_kind":"worktree","max_retries":5}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 组织管理员的高级字段必须被 strip
	assert.Empty(t, stub.createIn.Skills, "org_admin 的 skills 应被丢弃")
	assert.Empty(t, stub.createIn.WorkspaceKind, "org_admin 的 workspace_kind 应被丢弃")
	assert.Zero(t, stub.createIn.MaxRetries, "org_admin 的 max_retries 应被丢弃")
}

// TestKanbanCreateKeepsAdvancedFieldsForPlatformAdmin 验证：
// 平台管理员提交高级字段时原样透传。
func TestKanbanCreateKeepsAdvancedFieldsForPlatformAdmin(t *testing.T) {
	stub := &kanbanServiceStub{detail: service.KanbanTaskDetail{KanbanTask: service.KanbanTask{ID: "t_new"}}}
	r := newKanbanTestRouter(t, stub)

	body := `{"title":"x","assignee":"devops","skills":"bash","max_retries":5}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "admin", Role: domain.UserRolePlatformAdmin})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "bash", stub.createIn.Skills, "平台管理员的 skills 应透传")
	assert.Equal(t, 5, stub.createIn.MaxRetries, "平台管理员的 max_retries 应透传")
}

// TestKanbanCommentHappy 验证：评论端点成功返回 204。
func TestKanbanCommentHappy(t *testing.T) {
	stub := &kanbanServiceStub{}
	r := newKanbanTestRouter(t, stub)

	body := `{"board":"default","body":"一条评论"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/comment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}
```

- [ ] **Step 5: 运行全部 handler 测试**

Run: `go test ./internal/api/handlers/ -run TestKanban -v`
Expected: 全部 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/api/handlers/hermes_kanban.go internal/api/handlers/hermes_kanban_test.go
git commit -m "feat(kanban): 任务看板 HTTP handler 写端点与 SSE

新增 create/comment/complete/block/unblock/archive/reassign/reclaim 写
端点；新建任务的高级字段按角色 strip；events 端点以 SSE 推送实时事件流。"
```

### Task D5：路由装配与 OpenAPI 同步

**Files:**
- Modify: `internal/api/router.go`
- Modify: `openapi/openapi.yaml`（生成产物）
- Modify: `web/src/api/generated.ts`（生成产物）

- [ ] **Step 1: 在 Dependencies 加字段**

`router.go` 的 `Dependencies` struct 加：

```go
	// HermesKanbanService 提供实例任务看板能力；nil 时不注册 kanban 路由。
	HermesKanbanService *service.HermesKanbanService
```

- [ ] **Step 2: 在 NewRouter 装配路由**

在 user 组的其他 app 路由附近追加：

```go
	if dep.HermesKanbanService != nil {
		handlers.RegisterHermesKanbanRoutes(user, handlers.NewHermesKanbanHandler(dep.HermesKanbanService))
	}
```

- [ ] **Step 3: 实现 KanbanAppLocatorFromStore**

`HermesKanbanService` 依赖 `kanbanAppLocator`（Task C2 定义的接口）。在
`internal/service/hermes_kanban.go` 同包内新增一个基于 app store 的实现。
`sqlc.App` 已有 `OrgID` / `OwnerUserID` / `RuntimeNodeID`（均 `pgtype.UUID`）、
`ContainerID`（`pgtype.Text`）、`RuntimeImageRef`（`string`）。

```go
// kanbanAppStore 是 KanbanAppLocatorFromStore 依赖的最小 app 查询能力。
type kanbanAppStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// KanbanAppLocatorFromStore 基于 app store 把 appID 解析为 Kanban 执行坐标。
type KanbanAppLocatorFromStore struct {
	store kanbanAppStore
}

// NewKanbanAppLocatorFromStore 构造 locator。
func NewKanbanAppLocatorFromStore(store kanbanAppStore) *KanbanAppLocatorFromStore {
	return &KanbanAppLocatorFromStore{store: store}
}

// LocateApp 查 app 行并组装 KanbanAppLocation。
func (l *KanbanAppLocatorFromStore) LocateApp(ctx context.Context, appID string) (KanbanAppLocation, error) {
	id, err := parseUUID(appID) // 项目已有的 string→pgtype.UUID helper；执行时 grep 确认函数名
	if err != nil {
		return KanbanAppLocation{}, fmt.Errorf("%w: 非法 app id", ErrKanbanBadRequest)
	}
	app, err := l.store.GetApp(ctx, id)
	if err != nil {
		return KanbanAppLocation{}, ErrNotFound
	}
	loc := KanbanAppLocation{
		OrgID:       uuidToString(app.OrgID),       // service 包已有 uuidToString helper
		OwnerUserID: uuidToString(app.OwnerUserID),
		NodeID:      uuidToString(app.RuntimeNodeID),
	}
	if app.ContainerID.Valid {
		loc.ContainerID = app.ContainerID.String
	}
	// stub 判定：dev stub 镜像 tag 约定以 -dev 结尾（见 hermes-runtime:hermes-main-dev）。
	// spec §10.2 的精确方案（读 /etc/oc-image.json）留作后续；后缀判定足够触发降级提示。
	loc.Stub = strings.HasSuffix(app.RuntimeImageRef, "-dev")
	return loc, nil
}
```

需在 `hermes_kanban.go` 补 import：`github.com/jackc/pgx/v5/pgtype`、
`oc-manager/internal/store/sqlc`。`parseUUID` 若项目内实际叫别的名字
（先 `grep -rn 'func.*string.*pgtype.UUID' internal/service/` 确认），按实际改；
`uuidToString` 在 service 包已存在（`runtime_operation_service.go` 在用）。
`ErrNotFound` 在 `errors.go` 已存在。

- [ ] **Step 4: 在 main wiring 处构造 service 并注入**

找到组装 `Dependencies` 的地方（`cmd/server/main.go` 或 `internal/app` 下的
wiring 代码 —— 先 `grep -rn 'Dependencies{' cmd/ internal/` 定位）。

- `kanbanExecer`：现有的 `runtime.AgentBackedAdapter` 实例已实现 `ContainerExecJSON`
  / `ContainerExecStream`，直接传入。
- `kanbanAppLocator`：用 Step 3 的 `KanbanAppLocatorFromStore`，传入现有 app store。

```go
	kanbanLocator := service.NewKanbanAppLocatorFromStore(appStore /* 现有 app store */)
	deps.HermesKanbanService = service.NewHermesKanbanService(agentAdapter, kanbanLocator)
```

- [ ] **Step 5: 编译并跑全量后端测试**

Run: `go build ./... && go test ./internal/...`
Expected: 全部 PASS

- [ ] **Step 6: 重新生成 OpenAPI 与前端类型**

Run: `make openapi-gen && make web-types-gen`
Expected: `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 更新，含 kanban 端点

- [ ] **Step 7: 校验 OpenAPI 同步**

Run: `make openapi-check`
Expected: git 工作区干净（生成产物与代码一致）

- [ ] **Step 8: 提交**

```bash
git add internal/api/router.go internal/service/hermes_kanban.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(kanban): 装配任务看板路由并同步 OpenAPI

router 装配 HermesKanbanService 与 kanban 路由；新增 KanbanAppLocator
解析 app 运行时坐标；重新生成 openapi.yaml 与前端类型。"
```

---

## Phase E：前端 API 层

### Task E1：Kanban API hooks —— 读 query

**Files:**
- Create: `web/src/api/hooks/useKanban.ts`

- [ ] **Step 1: 写类型与读 query hooks**

参考 `web/src/api/hooks/useApps.ts` 的 TanStack Query 模式。

```typescript
// useKanban.ts —— 实例任务看板的 API hooks。
// 数据来自 manager 的 /api/v1/apps/{appId}/hermes/kanban/* 端点。
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'
import { apiRequest } from '@/api/client'

// Kanban 任务状态枚举（与 hermes 一致）。
export type KanbanStatus =
  | 'triage' | 'todo' | 'ready' | 'running' | 'blocked' | 'done' | 'archived'

// KanbanBoard 是一个看板。
export interface KanbanBoard {
  slug: string
  name: string
  description?: string
  archived?: boolean
}

// KanbanTask 是列表视图的任务。
export interface KanbanTask {
  id: string
  title: string
  status: KanbanStatus
  assignee: string
  priority: number
  body?: string
  created_at: number
  started_at?: number
  completed_at?: number
  skills?: string
}

// KanbanComment / KanbanEvent / KanbanTaskRun / KanbanTaskDetail：
// 字段与后端 service.KanbanXxx 对应。
export interface KanbanComment { author: string; body: string; created_at: number }
export interface KanbanEvent { kind: string; payload?: string; created_at: number }
export interface KanbanTaskRun {
  profile: string; status: string; worker_pid?: number
  started_at: number; ended_at?: number; outcome?: string; summary?: string; error?: string
}
export interface KanbanTaskDetail extends KanbanTask {
  workspace_kind?: string
  workspace_path?: string
  worker_pid?: number
  last_heartbeat_at?: number
  parent_id?: string
  result?: string
  comments?: KanbanComment[]
  events?: KanbanEvent[]
}
export interface KanbanStats { status_counts: Record<string, number> }

// queryKey 约定：['kanban', 子类, appId, ...]
const boardsKey = (appId: string | undefined) => ['kanban', 'boards', appId] as const
const tasksKey = (appId: string | undefined, board: string) => ['kanban', 'tasks', appId, board] as const
const taskKey = (appId: string | undefined, board: string, taskId: string) =>
  ['kanban', 'task', appId, board, taskId] as const

// useKanbanBoardsQuery 拉取实例的所有 board。
export function useKanbanBoardsQuery(appId: Ref<string | undefined>) {
  return useQuery<KanbanBoard[]>({
    queryKey: ['kanban', 'boards', appId],
    enabled: () => Boolean(appId.value),
    queryFn: async () => {
      const res = await apiRequest<{ boards: KanbanBoard[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/boards`,
      )
      return res.boards ?? []
    },
  })
}

// useKanbanTasksQuery 拉取某 board 的任务列表，5s 轮询。
export function useKanbanTasksQuery(appId: Ref<string | undefined>, board: Ref<string>) {
  return useQuery<KanbanTask[]>({
    queryKey: ['kanban', 'tasks', appId, board],
    enabled: () => Boolean(appId.value),
    refetchInterval: 5000,
    queryFn: async () => {
      const res = await apiRequest<{ tasks: KanbanTask[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks`,
        { query: { board: board.value } },
      )
      return res.tasks ?? []
    },
  })
}

// useKanbanTaskQuery 拉取单个任务详情。
export function useKanbanTaskQuery(
  appId: Ref<string | undefined>,
  board: Ref<string>,
  taskId: Ref<string | undefined>,
) {
  return useQuery<KanbanTaskDetail | null>({
    queryKey: ['kanban', 'task', appId, board, taskId],
    enabled: () => Boolean(appId.value && taskId.value),
    queryFn: async () => {
      if (!taskId.value) return null
      const res = await apiRequest<{ task: KanbanTaskDetail }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId.value}`,
        { query: { board: board.value } },
      )
      return res.task
    },
  })
}

// useKanbanRunsQuery 拉取任务历次执行。
export function useKanbanRunsQuery(
  appId: Ref<string | undefined>,
  board: Ref<string>,
  taskId: Ref<string | undefined>,
) {
  return useQuery<KanbanTaskRun[]>({
    queryKey: ['kanban', 'runs', appId, board, taskId],
    enabled: () => Boolean(appId.value && taskId.value),
    queryFn: async () => {
      if (!taskId.value) return []
      const res = await apiRequest<{ runs: KanbanTaskRun[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId.value}/runs`,
        { query: { board: board.value } },
      )
      return res.runs ?? []
    },
  })
}

// 给后续 Task E2 / G1 用的导出工具。
export { boardsKey, tasksKey, taskKey }
```

> `apiRequest` 的 `query` 选项用法以 `web/src/api/client.ts` 实际签名为准
> （探索报告确认支持 `query`）。

- [ ] **Step 2: 类型检查**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无类型错误

- [ ] **Step 3: 提交**

```bash
git add web/src/api/hooks/useKanban.ts
git commit -m "feat(kanban-web): 新增任务看板读 query hooks

useKanban.ts 提供 boards/tasks/task/runs 的 TanStack Query hooks 与类型
定义；任务列表 5s 轮询。"
```

### Task E2：Kanban API hooks —— 写 mutation

**Files:**
- Modify: `web/src/api/hooks/useKanban.ts`

- [ ] **Step 1: 加写 mutation hooks**

```typescript
// useCreateKanbanTask 新建任务。
export function useCreateKanbanTask(appId: Ref<string | undefined>, board: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: {
      title: string; assignee: string; priority: number; body?: string
      skills?: string; workspace_kind?: string; workspace_path?: string
      parent_id?: string; max_retries?: number
    }) => {
      const res = await apiRequest<{ task: KanbanTaskDetail }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks`,
        { method: 'POST', body: { board: board.value, ...payload } },
      )
      return res.task
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['kanban', 'tasks', appId, board] })
    },
  })
}

// kanbanWriteAction 是除 create 外的写操作类型。
type KanbanWriteAction =
  | { verb: 'comment'; taskId: string; body: string }
  | { verb: 'complete'; taskId: string; result?: string }
  | { verb: 'block'; taskId: string; reason: string }
  | { verb: 'unblock'; taskId: string }
  | { verb: 'archive'; taskId: string }
  | { verb: 'reassign'; taskId: string; to: string }
  | { verb: 'reclaim'; taskId: string }

// useKanbanTaskAction 是统一的任务写操作 mutation（comment/complete/block/...）。
// 单 hook 覆盖所有非 create 写操作，避免 7 个几乎相同的 hook。
export function useKanbanTaskAction(appId: Ref<string | undefined>, board: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (action: KanbanWriteAction) => {
      const { verb, taskId, ...rest } = action
      await apiRequest<void>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId}/${verb}`,
        { method: 'POST', body: { board: board.value, ...rest } },
      )
    },
    onSuccess: (_data, action) => {
      void client.invalidateQueries({ queryKey: ['kanban', 'tasks', appId, board] })
      void client.invalidateQueries({ queryKey: ['kanban', 'task', appId, board, action.taskId] })
    },
  })
}
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无类型错误

- [ ] **Step 3: 提交**

```bash
git add web/src/api/hooks/useKanban.ts
git commit -m "feat(kanban-web): 新增任务看板写 mutation hooks

useCreateKanbanTask 新建任务；useKanbanTaskAction 用单 hook 统一覆盖
comment/complete/block/unblock/archive/reassign/reclaim 写操作，成功后
失效相关查询缓存。"
```

---

## Phase F：前端组件

### Task F1：路由与 tab 入口

**Files:**
- Modify: `web/src/app/router.ts`
- Modify: `web/src/pages/apps/AppDetailPage.vue`

- [ ] **Step 1: 在 router.ts 加 kanban 子路由**

在 `apps/:appId` 的 `children` 数组里、`overview` 之后插入：

```typescript
    { path: 'kanban', component: AppKanbanTab, props: true },
```

并在文件顶部 import：

```typescript
import AppKanbanTab from '@/pages/apps/AppKanbanTab.vue'
```

- [ ] **Step 2: 在 AppDetailPage.vue 的 allTabs 加「任务」**

把 `allTabs` 改为（在 `overview` 之后插入 `kanban`）：

```typescript
const allTabs: ReadonlyArray<{ path: string; label: string }> = [
  { path: 'overview', label: '概览' },
  { path: 'kanban', label: '任务' },
  { path: 'runtime', label: '运行时' },
  { path: 'channels', label: '渠道' },
  { path: 'knowledge', label: '实例知识库' },
  { path: 'workspace', label: '工作目录' },
  { path: 'audit', label: '审计' },
]
```

> `tabs` computed 里对 `runtime` 的平台管理员过滤逻辑不动；kanban 不在过滤名单，
> 所有角色可见。

- [ ] **Step 3: 建一个最小可渲染的 AppKanbanTab.vue 占位（让路由先通）**

```vue
<template>
  <n-card :bordered="true">
    <template #header>
      <p class="eyebrow">Instance · Kanban</p>
      <h2 style="margin: 0">任务</h2>
    </template>
    <p class="state-text">任务看板加载中…</p>
  </n-card>
</template>

<script setup lang="ts">
import { NCard } from 'naive-ui'
// AppKanbanTab 是实例任务看板顶层组件，后续 Task F2 起逐步填充。
defineProps<{ appId: string }>()
</script>
```

- [ ] **Step 4: 启动前端确认 tab 出现且可点开**

Run: `cd web && npm run dev`（后台），浏览器打开某实例详情页

Expected: tab 栏出现「任务」，点击进入显示占位卡片，无控制台报错

- [ ] **Step 5: 提交**

```bash
git add web/src/app/router.ts web/src/pages/apps/AppDetailPage.vue web/src/pages/apps/AppKanbanTab.vue
git commit -m "feat(kanban-web): 实例详情页新增「任务」tab 入口

router 加 kanban 子路由，AppDetailPage 的 tab 栏加「任务」，所有角色
可见；AppKanbanTab 先放占位卡片，后续逐步填充。"
```

### Task F2：左侧任务列表组件

**Files:**
- Create: `web/src/pages/apps/kanban/KanbanTaskRow.vue`
- Create: `web/src/pages/apps/kanban/KanbanTaskList.vue`

- [ ] **Step 1: 写 KanbanTaskRow.vue**

单个任务行。参考 mockup v4 的列表行：标题（2 行截断）+ assignee/priority/skills
标签 + 相对时间；running 行显示最新事件预览、blocked 行显示阻塞原因。

```vue
<template>
  <div
    class="task-row"
    :class="{ selected: selected }"
    @click="emit('select', task.id)"
  >
    <div class="row-title">{{ task.title }}</div>
    <div class="row-meta">
      <n-tag size="tiny" type="info">{{ task.assignee }}</n-tag>
      <n-tag v-if="task.priority >= 2" size="tiny" :type="priorityType">{{ priorityLabel }}</n-tag>
      <span class="row-time">{{ relativeTime }}</span>
    </div>
    <div v-if="latestEvent" class="row-running">● {{ latestEvent }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag } from 'naive-ui'
import type { KanbanTask } from '@/api/hooks/useKanban'

// KanbanTaskRow 渲染左侧列表的单个任务行。
const props = defineProps<{
  task: KanbanTask
  selected: boolean
  latestEvent?: string // running 任务的最新事件预览，由父组件按事件流注入
}>()
const emit = defineEmits<{ select: [taskId: string] }>()

// priorityType / priorityLabel：priority>=3 高(红)、==2 中(橙)。
const priorityType = computed(() => (props.task.priority >= 3 ? 'error' : 'warning'))
const priorityLabel = computed(() => (props.task.priority >= 3 ? 'high' : 'medium'))

// relativeTime：把 created_at（秒级 epoch）转成相对时间中文。
const relativeTime = computed(() => {
  const diff = Date.now() / 1000 - props.task.created_at
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
})
</script>

<style scoped>
.task-row {
  padding: 10px 14px;
  border-left: 2px solid transparent;
  cursor: pointer;
}
.task-row:hover { background: var(--n-color-embedded, #1f1f24); }
.task-row.selected {
  background: var(--n-color-embedded, #1f1f24);
  border-left-color: var(--primary-color, #18a058);
}
.row-title {
  font-size: 13px;
  font-weight: 500;
  margin-bottom: 5px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.row-meta { display: flex; align-items: center; gap: 5px; }
.row-time { margin-left: auto; color: var(--n-text-color-3, #707078); font-size: 11px; }
.row-running {
  margin-top: 6px;
  font-size: 10px;
  color: var(--primary-color, #18a058);
  font-family: ui-monospace, monospace;
}
</style>
```

- [ ] **Step 2: 写 KanbanTaskList.vue**

按 status 分组、可折叠（用 `NCollapse`），折叠态存 localStorage。

```vue
<template>
  <n-card :bordered="true" content-style="padding: 0">
    <n-collapse :default-expanded-names="expandedGroups" @update:expanded-names="onExpandChange">
      <n-collapse-item
        v-for="group in groups"
        :key="group.status"
        :name="group.status"
        :title="`${group.label} (${group.tasks.length})`"
      >
        <KanbanTaskRow
          v-for="task in group.tasks"
          :key="task.id"
          :task="task"
          :selected="task.id === selectedId"
          :latest-event="latestEvents[task.id]"
          @select="emit('select', $event)"
        />
        <p v-if="group.tasks.length === 0" class="empty-hint">无任务</p>
      </n-collapse-item>
    </n-collapse>
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NCollapse, NCollapseItem } from 'naive-ui'
import KanbanTaskRow from './KanbanTaskRow.vue'
import type { KanbanTask, KanbanStatus } from '@/api/hooks/useKanban'

// KanbanTaskList 把任务按状态分组渲染为可折叠列表。
const props = defineProps<{
  tasks: KanbanTask[]
  selectedId?: string
  appId: string
  latestEvents: Record<string, string> // taskId → 最新事件预览文本
}>()
const emit = defineEmits<{ select: [taskId: string] }>()

// 状态分组顺序与中文标签。
const GROUP_DEFS: ReadonlyArray<{ status: KanbanStatus; label: string }> = [
  { status: 'running', label: 'Running' },
  { status: 'ready', label: 'Ready' },
  { status: 'todo', label: 'Todo' },
  { status: 'blocked', label: 'Blocked' },
  { status: 'triage', label: 'Triage' },
  { status: 'done', label: 'Done' },
  { status: 'archived', label: 'Archived' },
]

// groups 把 tasks 按状态分桶。
const groups = computed(() =>
  GROUP_DEFS.map((def) => ({
    ...def,
    tasks: props.tasks.filter((t) => t.status === def.status),
  })),
)

// 折叠态 localStorage key（含 appId，按实例隔离）。
const storageKey = computed(() => `kanban-expanded-${props.appId}`)

// expandedGroups 初值：localStorage 有则用，否则默认展开活跃状态。
const expandedGroups = computed<string[]>(() => {
  const saved = localStorage.getItem(storageKey.value)
  if (saved) {
    try { return JSON.parse(saved) as string[] } catch { /* 忽略损坏数据 */ }
  }
  return ['running', 'ready', 'todo', 'blocked']
})

// onExpandChange 持久化折叠态。
function onExpandChange(names: Array<string | number>) {
  localStorage.setItem(storageKey.value, JSON.stringify(names))
}
</script>

<style scoped>
.empty-hint {
  padding: 12px 14px;
  color: var(--n-text-color-3, #707078);
  font-size: 11px;
  text-align: center;
}
</style>
```

- [ ] **Step 3: 类型检查**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无类型错误

- [ ] **Step 4: 提交**

```bash
git add web/src/pages/apps/kanban/KanbanTaskRow.vue web/src/pages/apps/kanban/KanbanTaskList.vue
git commit -m "feat(kanban-web): 新增任务看板左侧列表组件

KanbanTaskList 按状态分组、可折叠（折叠态存 localStorage）；KanbanTaskRow
渲染单行任务，含 assignee/priority 标签与相对时间。"
```

### Task F3：右侧详情与操作组件

**Files:**
- Create: `web/src/pages/apps/kanban/KanbanTaskActions.vue`
- Create: `web/src/pages/apps/kanban/KanbanTaskDetail.vue`

- [ ] **Step 1: 写 KanbanTaskActions.vue**

按 task 当前状态显示对应操作按钮（spec §5.4 矩阵）。

```vue
<template>
  <n-space :size="6">
    <n-button v-if="show('complete')" size="small" type="primary" @click="emit('action', 'complete')">标记完成</n-button>
    <n-button v-if="show('block')" size="small" @click="emit('action', 'block')">阻塞</n-button>
    <n-button v-if="show('unblock')" size="small" type="primary" @click="emit('action', 'unblock')">解除阻塞</n-button>
    <n-button v-if="show('reclaim')" size="small" @click="emit('action', 'reclaim')">释放 claim</n-button>
    <n-button v-if="show('reassign')" size="small" @click="emit('action', 'reassign')">重新分配</n-button>
    <n-button v-if="show('comment')" size="small" @click="emit('action', 'comment')">评论</n-button>
    <n-button v-if="show('archive')" size="small" type="error" @click="emit('action', 'archive')">归档</n-button>
  </n-space>
</template>

<script setup lang="ts">
import { NButton, NSpace } from 'naive-ui'
import type { KanbanStatus } from '@/api/hooks/useKanban'

// KanbanTaskActions 按任务状态决定显示哪些操作按钮。
const props = defineProps<{ status: KanbanStatus }>()
const emit = defineEmits<{ action: [verb: string] }>()

// ACTION_MATRIX：每个状态可执行的操作集合（spec §5.4）。
const ACTION_MATRIX: Record<KanbanStatus, string[]> = {
  triage: ['comment', 'block', 'archive', 'reassign'],
  todo: ['comment', 'block', 'archive', 'reassign'],
  ready: ['comment', 'block', 'archive', 'reassign'],
  running: ['comment', 'complete', 'block', 'reclaim', 'archive'],
  blocked: ['comment', 'unblock', 'archive', 'reassign'],
  done: ['comment', 'archive'],
  archived: ['comment'],
}

// show 判断某操作按钮在当前状态下是否显示。
function show(verb: string): boolean {
  return ACTION_MATRIX[props.status]?.includes(verb) ?? false
}
</script>
```

- [ ] **Step 2: 写 KanbanTaskDetail.vue**

右侧详情：状态条 + 操作栏 + 元信息 + body + 实时执行流 + 历次执行 + 评论。

```vue
<template>
  <n-card :bordered="true">
    <template v-if="!task">
      <p class="state-text">从左侧选择一个任务查看详情。</p>
    </template>
    <template v-else>
      <div class="detail-head">
        <div class="status-bar">● {{ task.status.toUpperCase() }}</div>
        <h3 class="detail-title">{{ task.title }}</h3>
        <p class="detail-sub">task_id <code>{{ task.id }}</code> · board <code>{{ board }}</code></p>
      </div>

      <KanbanTaskActions :status="task.status" @action="emit('action', $event)" />

      <!-- 元信息 -->
      <div class="section">
        <p class="section-title">元信息</p>
        <div class="meta-grid">
          <div><span class="k">assignee</span><span class="v">{{ task.assignee }}</span></div>
          <div><span class="k">priority</span><span class="v">{{ task.priority }}</span></div>
          <div v-if="task.skills"><span class="k">skills</span><span class="v">{{ task.skills }}</span></div>
          <div v-if="task.worker_pid"><span class="k">worker</span><span class="v">pid {{ task.worker_pid }}</span></div>
        </div>
      </div>

      <!-- body -->
      <div v-if="task.body" class="section">
        <p class="section-title">任务 body</p>
        <p class="body-block">{{ task.body }}</p>
      </div>

      <!-- 实时执行流 -->
      <div v-if="task.status === 'running'" class="section">
        <p class="section-title">实时执行流 <span class="live">● LIVE</span></p>
        <div class="events-pane">
          <div v-for="(ev, i) in liveEvents" :key="i" class="ev-line">{{ ev }}</div>
          <p v-if="liveEvents.length === 0" class="state-text">等待事件…</p>
        </div>
      </div>

      <!-- 历次执行 -->
      <div class="section">
        <p class="section-title">历次执行</p>
        <p v-if="runs.length === 0" class="state-text">暂无执行记录。</p>
        <table v-else class="runs-table">
          <thead><tr><th>状态</th><th>profile</th><th>结果</th></tr></thead>
          <tbody>
            <tr v-for="(run, i) in runs" :key="i">
              <td>{{ run.status }}</td>
              <td>{{ run.profile }}</td>
              <td>{{ run.error || run.summary || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 评论 -->
      <div class="section">
        <p class="section-title">评论 ({{ task.comments?.length ?? 0 }})</p>
        <div v-for="(c, i) in task.comments ?? []" :key="i" class="comment">
          <div class="comment-head">{{ c.author }}</div>
          <div class="comment-body">{{ c.body }}</div>
        </div>
      </div>
    </template>
  </n-card>
</template>

<script setup lang="ts">
import { NCard } from 'naive-ui'
import KanbanTaskActions from './KanbanTaskActions.vue'
import type { KanbanTaskDetail, KanbanTaskRun } from '@/api/hooks/useKanban'

// KanbanTaskDetail 渲染右侧任务详情面板。
defineProps<{
  task: KanbanTaskDetail | null
  board: string
  runs: KanbanTaskRun[]
  liveEvents: string[] // 当前任务的实时事件文本行
}>()
const emit = defineEmits<{ action: [verb: string] }>()
</script>

<style scoped>
.detail-head { margin-bottom: 12px; }
.status-bar { color: var(--primary-color, #18a058); font-size: 12px; font-weight: 500; }
.detail-title { margin: 4px 0; font-size: 16px; }
.detail-sub { color: var(--n-text-color-3, #707078); font-size: 11px; }
.section { margin-top: 14px; border-top: 1px solid var(--n-border-color, #2a2a30); padding-top: 12px; }
.section-title { font-size: 11px; text-transform: uppercase; color: var(--n-text-color-3, #707078); margin: 0 0 8px; }
.live { color: var(--primary-color, #18a058); }
.meta-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; font-size: 12px; }
.meta-grid .k { color: var(--n-text-color-3, #707078); margin-right: 8px; }
.body-block { font-size: 12px; white-space: pre-wrap; color: var(--n-text-color-2, #a0a0a8); }
.events-pane {
  background: var(--n-color, #101014);
  border-radius: 3px; padding: 10px;
  font-family: ui-monospace, monospace; font-size: 11px;
  max-height: 180px; overflow-y: auto;
}
.runs-table { width: 100%; border-collapse: collapse; font-size: 12px; }
.runs-table th, .runs-table td {
  text-align: left; padding: 6px 8px;
  border-bottom: 1px solid var(--n-border-color, #2a2a30);
}
.comment { background: var(--n-color-embedded, #1f1f24); border-radius: 3px; padding: 8px 10px; margin-bottom: 6px; }
.comment-head { font-size: 11px; color: var(--n-text-color-3, #707078); }
.comment-body { font-size: 12px; color: var(--n-text-color-2, #a0a0a8); }
</style>
```

- [ ] **Step 3: 类型检查**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无类型错误

- [ ] **Step 4: 提交**

```bash
git add web/src/pages/apps/kanban/KanbanTaskActions.vue web/src/pages/apps/kanban/KanbanTaskDetail.vue
git commit -m "feat(kanban-web): 新增任务看板右侧详情与操作组件

KanbanTaskActions 按任务状态显示对应操作按钮；KanbanTaskDetail 渲染
元信息、body、实时执行流、历次执行、评论。"
```

### Task F4：新建任务模态框

**Files:**
- Create: `web/src/pages/apps/kanban/KanbanCreateModal.vue`

- [ ] **Step 1: 写 KanbanCreateModal.vue**

按角色显示字段集：平台管理员看到高级字段，其他角色只看必填字段。

```vue
<template>
  <n-modal :show="show" preset="card" title="新建任务" style="width: 520px" @update:show="emit('update:show', $event)">
    <n-form>
      <n-form-item label="标题" required>
        <n-input v-model:value="form.title" placeholder="任务标题" />
      </n-form-item>
      <n-form-item label="assignee" required>
        <n-input v-model:value="form.assignee" placeholder="处理该任务的 profile" />
      </n-form-item>
      <n-form-item label="优先级">
        <n-select v-model:value="form.priority" :options="priorityOptions" />
      </n-form-item>
      <n-form-item label="任务描述">
        <n-input v-model:value="form.body" type="textarea" placeholder="任务详细说明" />
      </n-form-item>

      <!-- 高级字段：仅平台管理员可见 -->
      <template v-if="isPlatformAdmin">
        <n-form-item label="skills">
          <n-input v-model:value="form.skills" placeholder="逗号分隔的技能" />
        </n-form-item>
        <n-form-item label="workspace_kind">
          <n-input v-model:value="form.workspace_kind" placeholder="scratch / dir:<path> / worktree" />
        </n-form-item>
        <n-form-item label="parent_id">
          <n-input v-model:value="form.parent_id" placeholder="父任务 ID（可选）" />
        </n-form-item>
        <n-form-item label="max_retries">
          <n-input-number v-model:value="form.max_retries" :min="0" />
        </n-form-item>
      </template>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="submitting" :disabled="!canSubmit" @click="onSubmit">创建</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NSelect, NButton, NSpace } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'

// KanbanCreateModal 是新建任务模态框，高级字段按角色显隐。
defineProps<{ show: boolean; submitting: boolean }>()
const emit = defineEmits<{
  'update:show': [value: boolean]
  submit: [payload: Record<string, unknown>]
}>()

const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// form 是新建任务表单的响应式状态。
const form = reactive({
  title: '', assignee: '', priority: 1, body: '',
  skills: '', workspace_kind: '', parent_id: '', max_retries: 0,
})

const priorityOptions = [
  { label: '低 (1)', value: 1 },
  { label: '中 (2)', value: 2 },
  { label: '高 (3)', value: 3 },
]

// canSubmit：标题与 assignee 必填。
const canSubmit = computed(() => form.title.trim() !== '' && form.assignee.trim() !== '')

// onSubmit 按角色组装 payload：非平台管理员不带高级字段。
function onSubmit() {
  const payload: Record<string, unknown> = {
    title: form.title.trim(),
    assignee: form.assignee.trim(),
    priority: form.priority,
    body: form.body.trim() || undefined,
  }
  if (isPlatformAdmin.value) {
    payload.skills = form.skills.trim() || undefined
    payload.workspace_kind = form.workspace_kind.trim() || undefined
    payload.parent_id = form.parent_id.trim() || undefined
    payload.max_retries = form.max_retries || undefined
  }
  emit('submit', payload)
}
</script>
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无类型错误

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/apps/kanban/KanbanCreateModal.vue
git commit -m "feat(kanban-web): 新增任务看板新建任务模态框

KanbanCreateModal 表单按角色显隐：平台管理员可填 skills/workspace_kind
/parent_id/max_retries 高级字段，其他角色仅填必填字段。"
```

### Task F5：AppKanbanTab 组装

**Files:**
- Modify: `web/src/pages/apps/AppKanbanTab.vue`

- [ ] **Step 1: 组装左右分屏 + 工具栏 + 写操作**

把占位组件替换为完整组装。

```vue
<template>
  <div class="kanban-tab">
    <!-- 工具栏 -->
    <n-card :bordered="true" class="toolbar-card">
      <n-space align="center" :size="8">
        <n-select
          v-model:value="currentBoard"
          :options="boardOptions"
          size="small"
          style="width: 180px"
        />
        <n-input v-model:value="search" size="small" placeholder="搜索任务标题" style="width: 200px" />
        <span class="spacer" />
        <span v-if="streamConnected" class="live-tag">● 实时</span>
        <n-button v-else size="small" tertiary @click="reconnectStream">重连实时流</n-button>
        <n-button size="small" type="primary" @click="showCreate = true">+ 新建任务</n-button>
      </n-space>
    </n-card>

    <!-- stub 镜像降级提示 -->
    <n-card v-if="isStubInstance" :bordered="true">
      <n-empty description="该实例运行的是本地 dev 镜像，任务看板不可用；切换到生产镜像后该功能自动启用。" />
    </n-card>

    <!-- 左右分屏 -->
    <div v-else class="split">
      <div class="list-col">
        <p v-if="tasksQuery.isLoading.value" class="state-text">加载中…</p>
        <p v-else-if="tasksQuery.error.value" class="state-text danger">{{ errorText }}</p>
        <KanbanTaskList
          v-else
          :tasks="filteredTasks"
          :selected-id="selectedTaskId"
          :app-id="appId"
          :latest-events="latestEvents"
          @select="onSelect"
        />
      </div>
      <div class="detail-col">
        <KanbanTaskDetail
          :task="taskQuery.data.value ?? null"
          :board="currentBoard"
          :runs="runsQuery.data.value ?? []"
          :live-events="selectedLiveEvents"
          @action="onAction"
        />
      </div>
    </div>

    <KanbanCreateModal
      v-model:show="showCreate"
      :submitting="createMutation.isPending.value"
      @submit="onCreate"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NSpace, NSelect, NInput, NButton, NEmpty, useMessage, useDialog } from 'naive-ui'
import KanbanTaskList from './kanban/KanbanTaskList.vue'
import KanbanTaskDetail from './kanban/KanbanTaskDetail.vue'
import KanbanCreateModal from './kanban/KanbanCreateModal.vue'
import {
  useKanbanBoardsQuery, useKanbanTasksQuery, useKanbanTaskQuery,
  useKanbanRunsQuery, useCreateKanbanTask, useKanbanTaskAction,
} from '@/api/hooks/useKanban'
import { useKanbanEventStream } from './kanban/useKanbanEventStream'

// AppKanbanTab 是实例任务看板顶层组件，组装工具栏 + 左右分屏 + 写操作。
const props = defineProps<{ appId: string }>()
const appId = computed(() => props.appId)
const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()

// URL query 同步：board 与选中 task。
const currentBoard = computed<string>({
  get: () => (route.query.board as string) || 'default',
  set: (v) => router.replace({ query: { ...route.query, board: v } }),
})
const selectedTaskId = computed<string | undefined>(() => route.query.task as string | undefined)
const search = ref('')
const showCreate = ref(false)

// 查询。
const boardsQuery = useKanbanBoardsQuery(appId)
const tasksQuery = useKanbanTasksQuery(appId, currentBoard)
const taskIdRef = computed(() => selectedTaskId.value)
const taskQuery = useKanbanTaskQuery(appId, currentBoard, taskIdRef)
const runsQuery = useKanbanRunsQuery(appId, currentBoard, taskIdRef)

// isStubInstance：tasksQuery 报 KANBAN_NOT_SUPPORTED_ON_STUB 时降级。
const isStubInstance = computed(() =>
  String(tasksQuery.error.value?.message ?? '').includes('KANBAN_NOT_SUPPORTED_ON_STUB'),
)
const errorText = computed(() => String(tasksQuery.error.value?.message ?? '加载失败'))

// board 下拉选项。
const boardOptions = computed(() =>
  (boardsQuery.data.value ?? [{ slug: 'default', name: 'default' }]).map((b) => ({
    label: b.name || b.slug, value: b.slug,
  })),
)

// 任务搜索过滤。
const filteredTasks = computed(() => {
  const all = tasksQuery.data.value ?? []
  const q = search.value.trim().toLowerCase()
  return q ? all.filter((t) => t.title.toLowerCase().includes(q)) : all
})

// 实时事件流（Task G1 实现）。
const { latestEvents, eventsByTask, connected: streamConnected, reconnect: reconnectStream }
  = useKanbanEventStream(appId, currentBoard)
const selectedLiveEvents = computed(() =>
  selectedTaskId.value ? eventsByTask.value[selectedTaskId.value] ?? [] : [],
)

// onSelect：选中任务写入 URL query。
function onSelect(taskId: string) {
  router.replace({ query: { ...route.query, task: taskId } })
}

// 写操作。
const createMutation = useCreateKanbanTask(appId, currentBoard)
const actionMutation = useKanbanTaskAction(appId, currentBoard)

// onCreate：提交新建任务。
async function onCreate(payload: Record<string, unknown>) {
  try {
    await createMutation.mutateAsync(payload as never)
    showCreate.value = false
    message.success('任务已创建')
  } catch (e) {
    message.error(e instanceof Error ? e.message : '创建失败')
  }
}

// onAction：处理详情面板发来的操作。comment/block/complete/reassign 需要补充输入。
async function onAction(verb: string) {
  const taskId = selectedTaskId.value
  if (!taskId) return
  // 需要文本输入的操作用 dialog 收集。
  const NEEDS_INPUT: Record<string, { title: string; key: string }> = {
    comment: { title: '添加评论', key: 'body' },
    block: { title: '阻塞原因', key: 'reason' },
    complete: { title: '完成结果（可选）', key: 'result' },
    reassign: { title: '重新分配给（profile）', key: 'to' },
  }
  // 高风险操作（archive/reclaim/complete）二次确认。
  const NEEDS_CONFIRM = new Set(['archive', 'reclaim'])

  try {
    if (verb in NEEDS_INPUT) {
      const cfg = NEEDS_INPUT[verb]
      const value = await promptText(cfg.title)
      if (value === null) return
      await actionMutation.mutateAsync({ verb, taskId, [cfg.key]: value } as never)
    } else if (NEEDS_CONFIRM.has(verb)) {
      const ok = await new Promise<boolean>((resolve) => {
        dialog.warning({
          title: '确认操作',
          content: `确定要执行「${verb}」吗？`,
          positiveText: '确认', negativeText: '取消',
          onPositiveClick: () => resolve(true),
          onNegativeClick: () => resolve(false),
          onClose: () => resolve(false),
        })
      })
      if (!ok) return
      await actionMutation.mutateAsync({ verb, taskId } as never)
    } else {
      await actionMutation.mutateAsync({ verb, taskId } as never)
    }
    message.success('操作成功')
  } catch (e) {
    message.error(e instanceof Error ? e.message : '操作失败')
  }
}

// promptText 用 dialog 收集一行文本输入，取消返回 null。
function promptText(title: string): Promise<string | null> {
  return new Promise((resolve) => {
    let input = ''
    dialog.create({
      title,
      content: () =>
        // 用 naive 的 render 函数渲染一个输入框
        // 简化实现：用原生 prompt 收集，避免额外 render 复杂度。
        undefined,
      positiveText: '确认',
      negativeText: '取消',
      onPositiveClick: () => resolve(input || ''),
      onNegativeClick: () => resolve(null),
      onClose: () => resolve(null),
    })
    // 简化：直接用浏览器 prompt（项目若禁用可换 NInput 弹框）
    const v = window.prompt(title)
    resolve(v)
  })
}
</script>

<style scoped>
.kanban-tab { display: grid; gap: 12px; }
.toolbar-card :deep(.n-card__content) { padding: 10px 14px; }
.spacer { flex: 1; }
.live-tag { color: var(--primary-color, #18a058); font-size: 11px; }
.split { display: grid; grid-template-columns: 380px 1fr; gap: 12px; align-items: start; }
@media (max-width: 1200px) {
  .split { grid-template-columns: 1fr; }
}
.danger { color: var(--error-color, #d03050); }
</style>
```

> `promptText` 上面给了一个简化实现（直接 `window.prompt`）。若项目规范禁止
> `window.prompt`，改为一个独立的小输入弹框组件；本计划为控制范围用 `window.prompt`，
> 执行者可按项目规范替换。`dialog.create` 那段冗余代码请删除，只保留 `window.prompt`
> 版本：
> ```typescript
> function promptText(title: string): Promise<string | null> {
>   return Promise.resolve(window.prompt(title))
> }
> ```

- [ ] **Step 2: 类型检查（此时 useKanbanEventStream 还不存在，会报错）**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 报 `useKanbanEventStream` 找不到 —— 符合预期，Task G1 补上

- [ ] **Step 3: 先不提交，等 G1 完成**

跳过，Task G1 完成后一起提交。

---

## Phase G：实时事件流与收尾

### Task G1：前端 SSE 事件流 composable

**Files:**
- Create: `web/src/pages/apps/kanban/useKanbanEventStream.ts`

- [ ] **Step 1: 写 SSE composable**

```typescript
// useKanbanEventStream.ts —— 订阅任务看板实时事件流（SSE）。
// 连接 manager 的 /hermes/kanban/events 端点，把 NDJSON 事件按 task 分发。
import { ref, watch, onUnmounted, type Ref } from 'vue'

// KanbanStreamEvent 是从后端 SSE 收到的单条事件（NDJSON 解析后）。
interface KanbanStreamEvent {
  task_id?: string
  kind?: string
  [k: string]: unknown
}

export function useKanbanEventStream(appId: Ref<string | undefined>, board: Ref<string>) {
  // eventsByTask：taskId → 该任务的事件文本行（详情面板用）。
  const eventsByTask = ref<Record<string, string[]>>({})
  // latestEvents：taskId → 最新一条事件的简短预览（列表行用）。
  const latestEvents = ref<Record<string, string>>({})
  const connected = ref(false)

  let source: EventSource | null = null
  let retries = 0

  // describe 把事件对象转成一行可读文本。
  function describe(ev: KanbanStreamEvent): string {
    return `${ev.kind ?? 'event'}${ev.payload ? ' · ' + String(ev.payload) : ''}`
  }

  function connect() {
    if (!appId.value) return
    close()
    const url = `/api/v1/apps/${appId.value}/hermes/kanban/events?board=${encodeURIComponent(board.value)}`
    source = new EventSource(url, { withCredentials: true })

    source.onopen = () => { connected.value = true; retries = 0 }

    source.onmessage = (msg) => {
      try {
        const ev = JSON.parse(msg.data) as KanbanStreamEvent
        const text = describe(ev)
        if (ev.task_id) {
          const lines = eventsByTask.value[ev.task_id] ?? []
          eventsByTask.value = { ...eventsByTask.value, [ev.task_id]: [...lines, text].slice(-100) }
          latestEvents.value = { ...latestEvents.value, [ev.task_id]: text }
        }
      } catch { /* 非 JSON 行忽略 */ }
    }

    source.onerror = () => {
      connected.value = false
      close()
      // 1s / 3s / 5s 三次重连，之后放弃（由 5s 轮询兜底）。
      if (retries < 3) {
        const delay = [1000, 3000, 5000][retries] ?? 5000
        retries += 1
        setTimeout(connect, delay)
      }
    }
  }

  function close() {
    if (source) { source.close(); source = null }
  }

  // reconnect 供用户手动点「重连实时流」按钮调用。
  function reconnect() {
    retries = 0
    connect()
  }

  // appId / board 变化时重连。
  watch([appId, board], () => connect(), { immediate: true })
  onUnmounted(close)

  return { eventsByTask, latestEvents, connected, reconnect }
}
```

> 后端 SSE 端点是否在事件 JSON 里带 `task_id`，取决于 `hermes kanban watch`
> 输出格式。若 watch 输出不含 task 维度归属，则 `eventsByTask` 退化为全局事件
> 列表，`latestEvents` 无法按行匹配 —— 此时把列表行的「最新事件预览」降级为
> 不显示，详情面板的实时流显示全局事件。执行 Task A2 契约测试时一并确认
> `watch` 输出结构，按实际调整 `describe` 与分发逻辑。

- [ ] **Step 2: 类型检查整个 kanban 模块**

Run: `cd web && npx vue-tsc --noEmit`
Expected: 无类型错误（AppKanbanTab 的 `useKanbanEventStream` 引用现在可解析）

- [ ] **Step 3: 提交 F5 + G1**

```bash
git add web/src/pages/apps/AppKanbanTab.vue web/src/pages/apps/kanban/useKanbanEventStream.ts
git commit -m "feat(kanban-web): 组装任务看板 tab 与实时事件流

AppKanbanTab 组装工具栏、board 选择、左右分屏、写操作与 stub 降级提示；
useKanbanEventStream 以 SSE 订阅 kanban watch，断线 1/3/5s 三次重连，
失败后由 5s 轮询兜底。"
```

### Task G2：AppKanbanTab 单元测试

**Files:**
- Create: `web/src/pages/apps/AppKanbanTab.spec.ts`

- [ ] **Step 1: 写组件测试**

参考 `web/src/pages/apps/AppRuntimeTab.spec.ts` 的 mock 模式。

```typescript
import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import AppKanbanTab from './AppKanbanTab.vue'

// mock 路由
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}))

// mock 任务看板 hooks：提供两条不同状态的任务。
vi.mock('@/api/hooks/useKanban', () => ({
  useKanbanBoardsQuery: () => ({ data: ref([{ slug: 'default', name: 'default' }]) }),
  useKanbanTasksQuery: () => ({
    data: ref([
      { id: 't_1', title: '运行中任务', status: 'running', assignee: 'devops', priority: 3, created_at: 0 },
      { id: 't_2', title: '待办任务', status: 'todo', assignee: 'analyst', priority: 1, created_at: 0 },
    ]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useKanbanTaskQuery: () => ({ data: ref(null) }),
  useKanbanRunsQuery: () => ({ data: ref([]) }),
  useCreateKanbanTask: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useKanbanTaskAction: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
}))

// mock SSE composable（jsdom 无 EventSource）。
vi.mock('./kanban/useKanbanEventStream', () => ({
  useKanbanEventStream: () => ({
    eventsByTask: ref({}), latestEvents: ref({}),
    connected: ref(true), reconnect: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isPlatformAdmin: false }),
}))

describe('AppKanbanTab', () => {
  // 验证：任务按状态分组渲染，运行中与待办任务都出现。
  it('按状态分组渲染任务', () => {
    const wrapper = mount(AppKanbanTab, {
      props: { appId: 'app-1' },
      global: {
        stubs: {
          NCard: { template: '<div><slot /></div>' },
          NModal: true, NSpace: { template: '<div><slot /></div>' },
          NSelect: true, NInput: true, NButton: true, NEmpty: true,
        },
      },
    })
    expect(wrapper.text()).toContain('运行中任务')
    expect(wrapper.text()).toContain('待办任务')
  })
})
```

> 若 mount 报 Naive UI provider 缺失（`useMessage`/`useDialog` 需要
> `NMessageProvider`/`NDialogProvider`），在 `global.stubs` 里把 `useMessage`
> / `useDialog` 也 mock 掉，或包一层 provider。参考 AppRuntimeTab.spec.ts 的处理。

- [ ] **Step 2: 运行测试**

Run: `cd web && npx vitest run src/pages/apps/AppKanbanTab.spec.ts`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/apps/AppKanbanTab.spec.ts
git commit -m "test(kanban-web): 新增 AppKanbanTab 组件单测

覆盖任务按状态分组渲染。"
```

---

## Phase H：端到端验证

### Task H1：整体验证与浏览器手测

**Files:** 无（仅验证）

- [ ] **Step 1: 后端全量测试**

Run: `go build ./... && go test ./internal/...`
Expected: 全部 PASS

- [ ] **Step 2: OpenAPI 同步校验**

Run: `make openapi-check`
Expected: git 工作区干净

- [ ] **Step 3: 前端测试与类型检查**

Run: `cd web && npx vue-tsc --noEmit && npx vitest run`
Expected: 全部 PASS

- [ ] **Step 4: 构建生产 hermes 镜像并跑契约测试**

Run: `docker build -f runtime/hermes/hermes-main/Dockerfile -t hermes-runtime:kanban-verify runtime/hermes/hermes-main`
然后在容器内跑 `pytest tests/test_kanban_contract.py -v`

Expected: 契约测试 PASS（真实 hermes 环境下不再 SKIP）

> 若网络受限无法 build 生产镜像，记录原因，改为在已有的真实 hermes 部署上跑
> `hermes kanban list --json` 手工验证字段，并在交付说明中注明。

- [ ] **Step 5: 浏览器手测（按 CLAUDE.md 要求，必须真实浏览器）**

启动本地环境，用三种角色分别验证：

平台管理员（组织标识留空，admin / admin123）+ 组织管理员 + 组织成员，
对一个运行中的真实 hermes 实例：

- [ ] 「任务」tab 出现在「概览」之后，可点开
- [ ] 左侧任务按状态分组、分组可折叠、刷新后折叠态保留
- [ ] 点击任务，右侧详情显示元信息 / body / 历次执行 / 评论
- [ ] running 任务详情显示实时执行流，有事件持续到达
- [ ] 新建任务：平台管理员能看到 skills 等高级字段，组织管理员/成员看不到
- [ ] 评论 / 完成 / 阻塞 / 解除阻塞 / 归档 / 重新分配 / 释放 claim 各执行一次，
      操作后列表与详情正确刷新
- [ ] archive / reclaim 弹二次确认
- [ ] 对一个 dev stub 镜像实例，「任务」tab 显示降级提示卡片
- [ ] 选中任务后刷新页面，URL 的 `?task=` 让选中态保留

- [ ] **Step 6: 记录验证结果**

把 Step 5 的勾选结果写入交付说明。若有失败项，回到对应 Task 修复后重测。

- [ ] **Step 7: 最终提交（若手测中有修复）**

```bash
git add -A
git commit -m "fix(kanban): 浏览器手测发现的问题修复

<按实际修复内容描述>"
```

---

## 自检对照（spec 覆盖）

| spec 章节 | 对应 Task |
|---|---|
| §3.2 CLI 通路 | B1/B2（exec 能力）、C2（runCLI） |
| §3.3 stub 镜像 | C2（resolve stub 判定）、D5（locator stub 判定）、F5（前端降级） |
| §5.1 入口与权限 | F1（tab 入口）、D1（权限） |
| §5.2 左右分屏 | F5（split 布局，<1200px 退化单列） |
| §5.3 左侧 status 分组列表 | F2 |
| §5.4 按状态决定操作按钮 | F3（KanbanTaskActions 矩阵） |
| §5.5 新建任务字段按角色分两套 | F4（前端显隐）、D4（后端 strip） |
| §6 数据通路与白名单 | C2（白名单正则）、B2（argv 不走 shell） |
| §7.2 service 各 verb | C2/C3/C4 |
| §7.3 handler 路由 | D3/D4/D5 |
| §7.4 权限谓词 | D1 |
| §9 实时事件流 | C4（StreamEvents）、D4（SSE 端点）、G1（前端 SSE） |
| §10.3 版本 pin + 契约测试 | A1/A2 |
| §11 错误处理矩阵 | C2（sentinel error）、D2（错误映射） |
| §12 测试 | 各 Task 的测试 step、G2、H1 |
