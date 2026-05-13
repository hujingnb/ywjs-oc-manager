# OpenClaw → Hermes 运行时完全替换 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 oc-manager 的 agent runtime backend 从 OpenClaw 完全替换为 Hermes,删除所有 OpenClaw 相关代码,保留 manager 的全部业务功能(多组织/多 app/微信渠道/知识库/对话)。

**Architecture:** 单仓库内 in-place 替换,无双 runtime 抽象。新增 `runtime/hermes/` 镜像目录与 `internal/integrations/hermes/` 集成层 → 切 worker handler 调用 → 重命名 config 与 adapter 接口 → 删除 `runtime/openclaw/` 与 `internal/integrations/openclaw/` → 更新 schema 注释与 openapi/前端类型/文档。按 AGENTS.md 业务边界拆 8 个有序 commit。

**Tech Stack:** Go 1.25 + sqlc + Goose-style numbered migrations + Docker SDK (`github.com/docker/docker/client`) + stdcopy 分流 + Python 3.13 (Hermes 上游) + testify (`require`) + swag/openapi-gen + Naive UI 前端。

**Spec:** `docs/superpowers/specs/2026-05-14-openclaw-to-hermes-design.md`(commit `4e52183`)

---

## File Structure

### 新增

```
runtime/hermes/
├── Dockerfile                # 基于 python:3.13-slim + install.sh + 预装 weixin 依赖
├── CONTRACT.md               # manager ↔ Hermes 集成约定文档
├── version.txt               # 锁定 hermes-agent 上游 ref(branch 或 commit)
├── scripts/
│   ├── oc-weixin-login.py    # docker exec 调用的扫码登录脚本
│   └── healthcheck.sh        # Dockerfile HEALTHCHECK 用

internal/integrations/hermes/
├── doc.go                    # 包注释
├── prompt.go                 # Render(PromptInput) (PromptResult, error) - 渲染 SOUL.md
├── prompt_test.go
├── config.go                 # RenderConfigYAML / RenderEnv - 渲染 config.yaml 与 .env
├── config_test.go
├── skills.go                 # RenderKnowledgeSkill - 知识库 → SKILL.md
├── skills_test.go
├── wechat_runner.go          # DockerCommandRunner - docker exec oc-weixin-login + stdcopy 分流
└── wechat_runner_test.go

internal/migrations/
├── 000016_runtime_to_hermes.up.sql      # COMMENT 更新
└── 000016_runtime_to_hermes.down.sql    # 反向 COMMENT
```

### 修改

```
internal/config/config.go                 # OpenClawConfig → HermesConfig,yaml key 改名
internal/integrations/runtime/adapter.go  # ContainerExec 注释去 OpenClaw 字样
internal/integrations/runtime/agent_backed.go  # SyncOpenClawImage → SyncRuntimeImage + WaitForOpenClawHealthy 改成等 docker health.status=healthy
internal/runtime/imagesync/service.go     # SyncOpenClawImage → SyncRuntimeImage
internal/worker/handlers/app_initialize.go        # prompt 注入 → SOUL.md/config.yaml/.env/skills 写入
internal/worker/handlers/app_health_check.go      # /healthz curl → docker inspect Health.Status
internal/worker/handlers/channel_login.go         # 调用切到 internal/integrations/hermes
internal/worker/handlers/runtime_refresh_status.go # 只 import path 更新(无强耦合)
internal/integrations/channel/wechat_runner.go    # 委托给 internal/integrations/hermes/wechat_runner
openapi/openapi.yaml                      # title 中性化(make openapi-gen 自动生成)
web/src/api/generated.ts                  # make web-types-gen 自动生成
docs/openclaw-manager-design.md           # 重命名 + 改内文
docs/openclaw-manager-technical-design.md # 重命名 + 改内文
README.md / AGENTS.md / CLAUDE.md         # OpenClaw → Hermes / agent runtime
```

### 删除

```
runtime/openclaw/                         # 整个目录
internal/integrations/openclaw/           # 整个目录
internal/integrations/runtime/agent_backed.go 中的 WaitForOpenClawHealthy / execCurlExitCode  # 函数级删除
```

---

## Phase 1: 新增 hermes runtime 镜像构建(Commit 1)

孤立提交,产出可独立 build 的 hermes 镜像,manager 业务代码不动。

### Task 1.1: 新增 runtime/hermes/version.txt 与 CONTRACT.md 占位

**Files:**
- Create: `runtime/hermes/version.txt`
- Create: `runtime/hermes/CONTRACT.md`

- [ ] **Step 1: 创建 version.txt**

```
main
```

(注:首期锁定 main 分支;后续生产化时改成具体 commit hash 或 tag。`install.sh --branch` 接此值。)

- [ ] **Step 2: 创建 CONTRACT.md(写关键约定,占位但内容完整)**

```markdown
# Hermes 集成契约

本文件汇总 manager 与 Hermes Agent 上游(NousResearch/hermes-agent)集成的关键约定,
便于离线审阅。

## 上游版本

- 上游仓库:`https://github.com/NousResearch/hermes-agent`
- 安装方式:`curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash -s -- --skip-setup`
- 锁定版本:见 `runtime/hermes/version.txt`

## 容器入口

| 项 | 值 |
|---|---|
| ENTRYPOINT | `tini -g -- hermes` |
| CMD | `gateway run` |
| 监听端口 | 无,Hermes gateway 出站长轮询 iLink API |
| HEALTHCHECK | `/usr/local/bin/oc-healthcheck`(内部 `hermes gateway status`,退出码 0 = healthy) |
| start-period | 60s |

## 容器内目录约定

- `HERMES_HOME=/opt/data` —— Hermes 主数据目录(挂载点)
- `/opt/data/config.yaml` —— model provider + auxiliary 配置(manager 写入)
- `/opt/data/.env` —— 凭证(OPENAI_API_KEY + WEIXIN_*)
- `/opt/data/SOUL.md` —— agent identity / system prompt(manager 写入)
- `/opt/data/skills/kb-<scope>-<slug>/SKILL.md` —— 知识库映射(manager 写入)
- `/opt/data/workspace/` —— agent 工作目录(Hermes 自动)
- `/opt/data/sessions/` —— 会话记录(Hermes 自动)
- `/opt/data/logs/` —— 日志(Hermes 自动)

## 微信渠道扫码

`/usr/local/bin/oc-weixin-login` 由 manager 通过 docker exec 调用:
- stdout 单行 JSON: `{"account_id":"<hex>@im.bot","token":"<...>","base_url":"<...>","user_id":"<...>"}`
- stderr 单行 URL: `https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3`
- exit code 0 = 登录成功;exit 2 = 超时或失败

manager 端实现位置:`internal/integrations/hermes/wechat_runner.go`
```

- [ ] **Step 3: Commit(本步只暂存,后续 Task 一起提交)**

不单独 commit。Phase 1 在 Task 1.3 之后合并 commit。

### Task 1.2: 新增 runtime/hermes/scripts/oc-weixin-login.py 与 healthcheck.sh

**Files:**
- Create: `runtime/hermes/scripts/oc-weixin-login.py`
- Create: `runtime/hermes/scripts/healthcheck.sh`

- [ ] **Step 1: 写 oc-weixin-login.py**

```python
#!/usr/bin/env python3
"""manager docker exec 调用的微信扫码登录入口。

stdout:  单行 JSON,含 account_id/token/base_url/user_id 等凭证字段
stderr:  二维码 URL(供 manager 流式转发给前端展示)
exit 0:  登录成功
exit 2:  登录失败或超时
"""
import asyncio
import contextlib
import io
import json
import sys

from gateway.platforms.weixin import qr_login


async def main() -> int:
    # qr_login 内部会 print 二维码 URL 到 stdout。我们把整段 qr_login 输出捕获到
    # 缓冲区,从中提取 URL 写 stderr,保证 manager 端 stdout 只有最终 JSON。
    captured = io.StringIO()
    with contextlib.redirect_stdout(captured):
        cred = await qr_login("/opt/data", bot_type="3", timeout_seconds=480)

    for line in captured.getvalue().splitlines():
        stripped = line.strip()
        if stripped.startswith("https://liteapp.weixin.qq.com/"):
            print(stripped, file=sys.stderr, flush=True)

    if not cred:
        print("LOGIN_FAILED_OR_TIMEOUT", file=sys.stderr, flush=True)
        return 2

    json.dump(cred, sys.stdout)
    sys.stdout.write("\n")
    sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
```

- [ ] **Step 2: 写 healthcheck.sh**

```sh
#!/bin/sh
# Dockerfile HEALTHCHECK 入口:Hermes 没有 HTTP /healthz,但提供 gateway status CLI。
# 退出码:0 = healthy(gateway 进程在,platform 连接 OK);非 0 = unhealthy。
exec hermes gateway status >/dev/null 2>&1
```

- [ ] **Step 3: 加可执行位**

```bash
chmod +x runtime/hermes/scripts/oc-weixin-login.py runtime/hermes/scripts/healthcheck.sh
```

不单独 commit。

### Task 1.3: 写 runtime/hermes/Dockerfile + 本地验证 build

**Files:**
- Create: `runtime/hermes/Dockerfile`

- [ ] **Step 1: 写 Dockerfile**

```dockerfile
# Hermes Agent runtime 镜像,对应 OpenClaw 时代的 runtime/openclaw/Dockerfile。
# manager 创建 app 时通过 docker run 启动本镜像,挂载 apps/<app_id>/.hermes 到 /opt/data。
# 容器启动即 ready,不允许运行时再装任何依赖。

FROM python:3.13-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    HERMES_HOME=/opt/data \
    PATH=/root/.local/bin:$PATH

# 系统依赖:hermes install.sh 需要 curl + git,运行时需要 ripgrep + ffmpeg(hermes 工具默认要求),
# tini 用于在 PID 1 收割 hermes 派生的子进程(MCP stdio / git 等)。
RUN apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git ripgrep ffmpeg tini && \
    rm -rf /var/lib/apt/lists/*

# 上游 install.sh 装 hermes-agent 主体 + 依赖(uv 自动拉 Python 3.11+,装 venv,clone repo)。
# --skip-setup 跳过交互式向导;ref 由 version.txt 决定(后续可参数化)。
ARG HERMES_REF=main
RUN curl -fsSL https://hermes-agent.nousresearch.com/install.sh \
      | bash -s -- --skip-setup --branch ${HERMES_REF}

# 显式预装 weixin platform 必需的依赖,避免容器启动时走 lazy_deps.py(用户硬约束:
# 容器启动即 ready,运行时不装任何东西)。
RUN /root/.hermes/hermes-agent/venv/bin/pip install --no-cache-dir \
      aiohttp cryptography qrcode

# manager 项目自带的入口脚本。
COPY scripts/oc-weixin-login.py /usr/local/bin/oc-weixin-login
COPY scripts/healthcheck.sh /usr/local/bin/oc-healthcheck
RUN chmod +x /usr/local/bin/oc-weixin-login /usr/local/bin/oc-healthcheck

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data

ENTRYPOINT ["/usr/bin/tini", "-g", "--", "hermes"]
CMD ["gateway", "run"]
```

- [ ] **Step 2: 本地试 build**

Run: `cd runtime/hermes && docker build -t hermes-runtime:dev .`
Expected: 全程不报错;成功结尾 `naming to docker.io/library/hermes-runtime:dev`;`docker images hermes-runtime:dev` 应当展示 2-3 GB 镜像。

- [ ] **Step 3: 本地试启容器验证镜像 entrypoint 不挂(不挂载 ~/.hermes,只看 hermes --version)**

Run: `docker run --rm --entrypoint /opt/hermes/.venv/bin/hermes hermes-runtime:dev --version`
Expected: 输出形如 `Hermes Agent v0.13.x (...)`,exit 0。

- [ ] **Step 4: 验证微信预装依赖**

Run:
```bash
docker run --rm --entrypoint /root/.hermes/hermes-agent/venv/bin/python hermes-runtime:dev \
  -c "import aiohttp, cryptography, qrcode; print('OK')"
```
Expected: 输出 `OK`,exit 0。三个包都能 import。

- [ ] **Step 5: Commit Phase 1**

```bash
git add runtime/hermes/
git commit -m "$(cat <<'EOF'
feat(runtime): 新增 hermes runtime 镜像构建

新增 runtime/hermes/ 目录,与 OpenClaw 时代的 runtime/openclaw/ 平级:

- Dockerfile:基于 python:3.13-slim,走上游 install.sh --skip-setup,
  显式 pip install aiohttp/cryptography/qrcode 预装微信 platform 依赖,
  保证容器启动即 ready,运行时不再走 lazy_deps.py。
- scripts/oc-weixin-login.py:docker exec 调用的扫码登录入口,
  stdout 单行 JSON 凭证 / stderr 二维码 URL / exit 码区分成功失败。
- scripts/healthcheck.sh:Dockerfile HEALTHCHECK 入口,
  内部 hermes gateway status 替代 OpenClaw 时代的 curl /healthz。
- CONTRACT.md:manager ↔ Hermes 上游集成约定。
- version.txt:首期锁定 main 分支。

本 commit 不触及 manager 业务代码,镜像可独立 build 通过(docker build .)。
EOF
)"
```

---

## Phase 2: 新增 hermes 集成层(Commit 2)

纯新增 `internal/integrations/hermes/` 包,manager 业务路径仍走旧 OpenClaw,**编译不依赖此 commit**。

### Task 2.1: 包注释 + Render 渲染 SOUL.md

**Files:**
- Create: `internal/integrations/hermes/doc.go`
- Create: `internal/integrations/hermes/prompt.go`
- Test: `internal/integrations/hermes/prompt_test.go`

- [ ] **Step 1: 写 doc.go**

```go
// Package hermes 提供 manager 与 Hermes Agent runtime 镜像的协议封装。
//
// 该包替代 internal/integrations/openclaw,承担以下职责:
//   - prompt.go: 渲染 SOUL.md(Hermes 启动时注入 system prompt 的 agent identity 文档)
//   - config.go: 渲染 config.yaml(model provider)与 .env(凭证)
//   - skills.go: 渲染知识库文档为 Hermes skills 目录(SKILL.md frontmatter + 正文)
//   - wechat_runner.go: docker exec 调用 oc-weixin-login.py + stdcopy 分流 stdout/stderr
//
// 所有 Render* 函数均返回字节内容,不直接写文件;真正落盘由 worker handler 负责。
package hermes
```

- [ ] **Step 2: 写 failing prompt_test.go(table-driven 三层拼接 + 占位符替换)**

```go
package hermes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRender(t *testing.T) {
	cases := []struct {
		// name 标识该测试场景。
		name string
		// input 是 Render 的输入。
		input PromptInput
		// wantContains 必须出现在 SOUL.md 中的子串。
		wantContains []string
		// wantOrder 期待的 CompositionOrder。
		wantOrder []string
		// wantErr 是否期待错误(nil = 期待成功)。
		wantErr error
	}{
		{
			// 覆盖三层全填充 + 变量替换的正常路径。
			name: "三层都有 + 变量替换",
			input: PromptInput{
				PlatformPrompt: "平台:{platform_name}",
				OrgPrompt:      "组织:{org_name}",
				AppPrompt:      "应用:{app_name}",
				Variables: map[string]string{
					"platform_name": "oc-manager",
					"org_name":      "test-org",
					"app_name":      "demo",
				},
			},
			wantContains: []string{"平台:oc-manager", "组织:test-org", "应用:demo"},
			wantOrder:    []string{"platform", "organization", "app"},
		},
		{
			// 覆盖某层为空时被跳过,CompositionOrder 不应包含空层。
			name: "组织层为空,被跳过",
			input: PromptInput{
				PlatformPrompt: "平台",
				OrgPrompt:      "",
				AppPrompt:      "应用",
			},
			wantContains: []string{"平台", "应用"},
			wantOrder:    []string{"platform", "app"},
		},
		{
			// 覆盖占位符未被 Variables 覆盖时返回 ErrPromptUnresolvedPlaceholder。
			name: "占位符未替换,返回错误",
			input: PromptInput{
				PlatformPrompt: "平台:{missing}",
				Variables:      map[string]string{},
			},
			wantErr: ErrPromptUnresolvedPlaceholder,
		},
		{
			// 覆盖三层全空时返回 ErrPromptEmpty。
			name: "三层全空,返回错误",
			input:   PromptInput{},
			wantErr: ErrPromptEmpty,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.input)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			for _, sub := range tc.wantContains {
				require.True(t, strings.Contains(got.Prompt, sub),
					"SOUL.md 应包含 %q,实际:%s", sub, got.Prompt)
			}
			require.Equal(t, tc.wantOrder, got.CompositionOrder)
		})
	}
}
```

- [ ] **Step 3: Run failing test**

Run: `go test ./internal/integrations/hermes/... -run TestRender -v`
Expected: 编译失败 `undefined: Render` / `PromptInput` / `PromptResult` / `ErrPromptUnresolvedPlaceholder` 等。

- [ ] **Step 4: 写 prompt.go 实现(SOUL.md 格式 markdown,但保持与 OpenClaw 时代相同的 PromptInput/PromptResult 契约)**

```go
package hermes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PromptInput 是 Render 的输入,与 OpenClaw 时代签名相同。
// 平台 / 组织 / 应用三层 prompt 任一为空时跳过该层;Variables 覆盖占位符 {var}。
type PromptInput struct {
	PlatformPrompt string
	OrgPrompt      string
	AppPrompt      string
	Variables      map[string]string
}

// PromptResult 是 Render 的输出。
// Prompt 是渲染后的完整 SOUL.md 文档内容(markdown 文本)。
// CompositionOrder 记录实际拼接的层级,空层不计。
type PromptResult struct {
	Prompt           string   `json:"prompt"`
	CompositionOrder []string `json:"composition_order"`
}

// 渲染错误。
var (
	// ErrPromptUnresolvedPlaceholder 当 Variables 未覆盖模板中的某个 {var} 时返回。
	ErrPromptUnresolvedPlaceholder = errors.New("prompt 仍存在未替换的占位符")
	// ErrPromptEmpty 三层 prompt 全为空。
	ErrPromptEmpty = errors.New("prompt 三层全部为空")
)

// 占位符匹配 {var};变量名仅允许字母数字下划线。
var placeholderPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Render 按 platform → organization → app 顺序拼接,返回 Hermes SOUL.md 内容。
// 与 OpenClaw 时代的 Render 在签名上一致,产出格式从 OpenClaw config patch 字符串
// 改为 markdown 文档(适用于 Hermes 直接写入 ~/.hermes/SOUL.md)。
func Render(input PromptInput) (PromptResult, error) {
	layers := []struct {
		key   string
		title string
		value string
	}{
		{"platform", "平台层", input.PlatformPrompt},
		{"organization", "组织层", input.OrgPrompt},
		{"app", "应用层", input.AppPrompt},
	}

	var b strings.Builder
	order := make([]string, 0, len(layers))
	for _, l := range layers {
		if strings.TrimSpace(l.value) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## %s\n\n%s", l.title, l.value)
		order = append(order, l.key)
	}
	if len(order) == 0 {
		return PromptResult{}, ErrPromptEmpty
	}

	rendered, err := replacePlaceholders(b.String(), input.Variables)
	if err != nil {
		return PromptResult{}, err
	}

	header := "# Agent Identity (SOUL.md)\n\n本文件由 oc-manager 在 app_initialize 时生成,Hermes 启动后注入到 system prompt。\n\n"
	return PromptResult{
		Prompt:           header + rendered,
		CompositionOrder: order,
	}, nil
}

// replacePlaceholders 用 Variables 替换 {var} 占位符,任一未替换则返回错误。
func replacePlaceholders(in string, vars map[string]string) (string, error) {
	missing := make([]string, 0)
	out := placeholderPattern.ReplaceAllStringFunc(in, func(match string) string {
		name := match[1 : len(match)-1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%w: %s", ErrPromptUnresolvedPlaceholder, strings.Join(missing, ","))
	}
	return out, nil
}

// VariablesFromContext 给三层 prompt 提供常用变量字典。
// 跟 OpenClaw 时代同名同语义,迁移调用方仅需改 import path。
func VariablesFromContext(orgName, appName, ownerName string) map[string]string {
	return map[string]string{
		"org_name":   orgName,
		"app_name":   appName,
		"owner_name": ownerName,
	}
}
```

- [ ] **Step 5: Run tests pass**

Run: `go test ./internal/integrations/hermes/... -run TestRender -v`
Expected: 全部 PASS。

- [ ] **Step 6: 不单独 commit。Phase 2 在 Task 2.4 后合并 commit。**

### Task 2.2: Render config.yaml + .env

**Files:**
- Create: `internal/integrations/hermes/config.go`
- Test: `internal/integrations/hermes/config_test.go`

- [ ] **Step 1: 写 failing config_test.go**

```go
package hermes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderConfigYAML(t *testing.T) {
	// 覆盖完整字段渲染:provider/base_url/api_key/model + 4 个 auxiliary 全 main。
	got, err := RenderConfigYAML(ConfigInput{
		ModelName:   "qwen3.5:27b",
		NewAPIURL:   "http://new-api:3000",
		NewAPIToken: "sk-test-xxx",
	})
	require.NoError(t, err)
	for _, sub := range []string{
		`default: "qwen3.5:27b"`,
		`provider: "custom"`,
		`base_url: "http://new-api:3000/v1"`,
		`api_key: "sk-test-xxx"`,
		`vision:`,
		`provider: main`,
	} {
		require.True(t, strings.Contains(got, sub),
			"config.yaml 应包含 %q,实际:\n%s", sub, got)
	}
}

func TestRenderConfigYAML_缺字段返回错误(t *testing.T) {
	// 覆盖必填字段缺失场景。
	_, err := RenderConfigYAML(ConfigInput{ModelName: "", NewAPIURL: "x", NewAPIToken: "y"})
	require.ErrorIs(t, err, ErrConfigMissingField)
}

func TestRenderEnv(t *testing.T) {
	// 覆盖 .env 渲染:OPENAI_API_KEY/OPENAI_BASE_URL 两行。
	got := RenderEnv(EnvInput{
		NewAPIURL:   "http://new-api:3000",
		NewAPIToken: "sk-abc",
	})
	require.Equal(t, "OPENAI_API_KEY=sk-abc\nOPENAI_BASE_URL=http://new-api:3000/v1\n", got)
}
```

- [ ] **Step 2: Run failing test**

Run: `go test ./internal/integrations/hermes/... -run TestRenderConfig -v`
Expected: 编译失败 `undefined: RenderConfigYAML / ConfigInput / ErrConfigMissingField / RenderEnv / EnvInput`。

- [ ] **Step 3: 写 config.go**

```go
package hermes

import (
	"errors"
	"fmt"
	"strings"
)

// ConfigInput 是 RenderConfigYAML 的输入。
// 所有字段均必填:ModelName 是 app 当前选择的模型,
// NewAPIURL 是 new-api 内网 URL(不带 /v1),NewAPIToken 是 manager 端创建的 sk-xxx。
type ConfigInput struct {
	ModelName   string
	NewAPIURL   string
	NewAPIToken string
}

// EnvInput 是 RenderEnv 的输入,字段同 ConfigInput 子集。
type EnvInput struct {
	NewAPIURL   string
	NewAPIToken string
}

// ErrConfigMissingField ConfigInput 必填字段为空。
var ErrConfigMissingField = errors.New("config: 必填字段为空")

// RenderConfigYAML 渲染 Hermes config.yaml。
// 写入 model.{default,provider,base_url,api_key} + auxiliary 全 main + memory/terminal 默认值。
// 输出可直接写到 apps/<app_id>/.hermes/config.yaml,Hermes 启动时读取。
func RenderConfigYAML(in ConfigInput) (string, error) {
	if strings.TrimSpace(in.ModelName) == "" ||
		strings.TrimSpace(in.NewAPIURL) == "" ||
		strings.TrimSpace(in.NewAPIToken) == "" {
		return "", ErrConfigMissingField
	}
	return fmt.Sprintf(`# Hermes 配置 - 由 oc-manager 在 app_initialize 时生成
# 模型 provider 走本地 new-api(OpenAI 兼容 endpoint)。

model:
  default: %q
  provider: "custom"
  base_url: %q
  api_key: %q

# auxiliary 全部走 main,避免 Hermes 默认去拨 OpenRouter。
auxiliary:
  vision:         { provider: main }
  compression:    { provider: main }
  web_extract:    { provider: main }
  session_search: { provider: main }

memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 2200
  user_char_limit: 1375

terminal:
  backend: "local"
  cwd: "."
  timeout: 180
  lifetime_seconds: 300
`, in.ModelName, in.NewAPIURL+"/v1", in.NewAPIToken), nil
}

// RenderEnv 渲染 Hermes .env 文件内容。
// 只放 OPENAI_API_KEY / OPENAI_BASE_URL,作为 auxiliary.provider=main 的兜底凭据。
// WEIXIN_* 凭证由扫码 runner 在登录成功后追加(不在此处)。
func RenderEnv(in EnvInput) string {
	return fmt.Sprintf("OPENAI_API_KEY=%s\nOPENAI_BASE_URL=%s/v1\n", in.NewAPIToken, in.NewAPIURL)
}
```

- [ ] **Step 4: Run tests pass**

Run: `go test ./internal/integrations/hermes/... -run TestRenderConfig -v && go test ./internal/integrations/hermes/... -run TestRenderEnv -v`
Expected: 全部 PASS。

- [ ] **Step 5: 不单独 commit。**

### Task 2.3: Render 知识库 skills(SKILL.md frontmatter + 正文)

**Files:**
- Create: `internal/integrations/hermes/skills.go`
- Test: `internal/integrations/hermes/skills_test.go`

- [ ] **Step 1: 写 failing skills_test.go**

```go
package hermes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderKnowledgeSkill(t *testing.T) {
	// 覆盖标准 skill 渲染:frontmatter 含 name/description/scope,正文为知识库内容。
	got, err := RenderKnowledgeSkill(KnowledgeDoc{
		Scope:   ScopeOrg,
		Slug:    "billing-rules",
		Title:   "计费规则",
		Summary: "组织内部的计费规则汇总",
		Body:    "## 规则一\n月度结算。",
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got.SkillMD, "---\n"))
	require.True(t, strings.Contains(got.SkillMD, "name: kb-org-billing-rules"))
	require.True(t, strings.Contains(got.SkillMD, "description: 组织内部的计费规则汇总"))
	require.True(t, strings.Contains(got.SkillMD, "## 规则一"))
	require.Equal(t, "kb-org-billing-rules", got.DirName)
}

func TestRenderKnowledgeSkill_Slug非法返回错误(t *testing.T) {
	// 覆盖 slug 含非法字符场景。
	_, err := RenderKnowledgeSkill(KnowledgeDoc{
		Scope: ScopeApp,
		Slug:  "has space",
		Title: "x",
		Body:  "y",
	})
	require.ErrorIs(t, err, ErrInvalidSlug)
}

func TestRenderKnowledgeSkill_Scope非法返回错误(t *testing.T) {
	// 覆盖未知 scope 场景。
	_, err := RenderKnowledgeSkill(KnowledgeDoc{
		Scope: "bad",
		Slug:  "a",
		Title: "t",
		Body:  "b",
	})
	require.ErrorIs(t, err, ErrInvalidScope)
}
```

- [ ] **Step 2: Run failing test**

Run: `go test ./internal/integrations/hermes/... -run TestRenderKnowledgeSkill -v`
Expected: 编译失败 `undefined: RenderKnowledgeSkill / KnowledgeDoc / ScopeOrg / ScopeApp / ErrInvalidSlug / ErrInvalidScope`。

- [ ] **Step 3: 写 skills.go**

```go
package hermes

import (
	"errors"
	"fmt"
	"regexp"
)

// SkillScope 表示知识库 skill 的作用域。
type SkillScope string

const (
	// ScopeOrg 组织级 skill,目录前缀 kb-org-。
	ScopeOrg SkillScope = "org"
	// ScopeApp 应用级 skill,目录前缀 kb-app-。
	ScopeApp SkillScope = "app"
)

// KnowledgeDoc 是 RenderKnowledgeSkill 的输入。
// Slug 用作目录与 skill name 的稳定 id,要求小写字母数字加连字符。
// Title 是 SKILL.md frontmatter name 的可读形式(用 Slug 拼接成 name)。
// Summary 进入 frontmatter description,影响 agent 发现 skill。
// Body 是 markdown 正文,直接写入 SKILL.md 主体。
type KnowledgeDoc struct {
	Scope   SkillScope
	Slug    string
	Title   string
	Summary string
	Body    string
}

// SkillRender 是 RenderKnowledgeSkill 的输出。
// DirName 是宿主机/容器内 skills 目录名(不含父路径)。
// SkillMD 是 SKILL.md 完整内容(frontmatter + 正文)。
type SkillRender struct {
	DirName string
	SkillMD string
}

var (
	// ErrInvalidSlug Slug 含非法字符。
	ErrInvalidSlug = errors.New("skills: 非法 slug")
	// ErrInvalidScope Scope 不是 org/app。
	ErrInvalidScope = errors.New("skills: 非法 scope")
)

// slugPattern 限制 slug 仅含小写字母数字与连字符,首尾不能是连字符。
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// RenderKnowledgeSkill 把一份知识库文档渲染成 Hermes skill 内容。
// 调用方拿到 SkillRender 后,自己负责创建宿主机目录 ~/.hermes/skills/<DirName>/
// 并把 SkillMD 写入该目录下的 SKILL.md。
func RenderKnowledgeSkill(d KnowledgeDoc) (SkillRender, error) {
	if d.Scope != ScopeOrg && d.Scope != ScopeApp {
		return SkillRender{}, ErrInvalidScope
	}
	if !slugPattern.MatchString(d.Slug) {
		return SkillRender{}, ErrInvalidSlug
	}

	dir := fmt.Sprintf("kb-%s-%s", d.Scope, d.Slug)
	desc := d.Summary
	if desc == "" {
		desc = d.Title
	}

	skillMD := fmt.Sprintf(`---
name: %s
description: %s
scope: %s
---

# %s

%s
`, dir, desc, d.Scope, d.Title, d.Body)

	return SkillRender{
		DirName: dir,
		SkillMD: skillMD,
	}, nil
}
```

- [ ] **Step 4: Run tests pass**

Run: `go test ./internal/integrations/hermes/... -run TestRenderKnowledgeSkill -v`
Expected: 3 个测试全部 PASS。

- [ ] **Step 5: 不单独 commit。**

### Task 2.4: docker exec 微信扫码 runner(stdcopy 分流 JSON / QR URL)

**Files:**
- Create: `internal/integrations/hermes/wechat_runner.go`
- Test: `internal/integrations/hermes/wechat_runner_test.go`

- [ ] **Step 1: 写 failing wechat_runner_test.go**

```go
package hermes

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeExecutor 模拟 ContainerExecutor 接口,允许测试驱动 stdout/stderr/exit code。
type fakeExecutor struct {
	stdoutFrames [][]byte
	stderrFrames [][]byte
	exitCode     int
	err          error
}

func (f *fakeExecutor) ExecAttach(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	// stdcopy multiplex 格式:首字节 stream type (1=stdout/2=stderr), 1-3 字节保留, 4-7 字节 BE length。
	buf := &bytes.Buffer{}
	writeFrame := func(stream byte, payload []byte) {
		header := make([]byte, 8)
		header[0] = stream
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		buf.Write(header)
		buf.Write(payload)
	}
	for _, p := range f.stderrFrames {
		writeFrame(2, p)
	}
	for _, p := range f.stdoutFrames {
		writeFrame(1, p)
	}
	return io.NopCloser(buf), nil
}

func (f *fakeExecutor) ExecExitCode(ctx context.Context) (int, error) {
	return f.exitCode, nil
}

func TestStreamWeChatLogin_SuccessYieldsQRThenBound(t *testing.T) {
	// 覆盖正常路径:扫码 → 收 QR 事件 → 收 bound 事件。
	exec := &fakeExecutor{
		stderrFrames: [][]byte{[]byte("https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3\n")},
		stdoutFrames: [][]byte{[]byte(`{"account_id":"610@im.bot","token":"t","base_url":"https://ilink","user_id":"u"}` + "\n")},
		exitCode:     0,
	}
	runner := NewWeixinRunner(exec)

	events, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.NoError(t, err)

	var qr, bound *WeixinEvent
	for ev := range events {
		switch ev.Type {
		case WeixinEventQRCode:
			qr = &ev
		case WeixinEventBound:
			bound = &ev
		}
	}
	require.NotNil(t, qr, "应收到 qrcode 事件")
	require.Equal(t, "https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3", qr.QRCodeURL)
	require.NotNil(t, bound, "应收到 bound 事件")
	require.Equal(t, "610@im.bot", bound.AccountID)
	require.Equal(t, "t", bound.Token)
}

func TestStreamWeChatLogin_NonZeroExitYieldsFailedEvent(t *testing.T) {
	// 覆盖失败路径:exit != 0 时发 failed 事件,不带 bound。
	exec := &fakeExecutor{
		stderrFrames: [][]byte{[]byte("LOGIN_FAILED_OR_TIMEOUT\n")},
		exitCode:     2,
	}
	runner := NewWeixinRunner(exec)
	events, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.NoError(t, err)

	var failed *WeixinEvent
	for ev := range events {
		if ev.Type == WeixinEventFailed {
			failed = &ev
		}
	}
	require.NotNil(t, failed)
	require.Contains(t, failed.Error, "LOGIN_FAILED_OR_TIMEOUT")
}

func TestStreamWeChatLogin_ExecAttachError(t *testing.T) {
	// 覆盖 docker exec 启动就失败的场景。
	exec := &fakeExecutor{err: errors.New("docker daemon down")}
	runner := NewWeixinRunner(exec)
	_, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "docker daemon down")
}
```

- [ ] **Step 2: Run failing test**

Run: `go test ./internal/integrations/hermes/... -run TestStreamWeChatLogin -v`
Expected: 编译失败 `undefined: NewWeixinRunner / WeixinEvent / WeixinEventQRCode / WeixinEventBound / WeixinEventFailed / ContainerExecutor`。

- [ ] **Step 3: 写 wechat_runner.go**

```go
package hermes

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/pkg/stdcopy"
)

// ContainerExecutor 是 docker SDK 的最小抽象,便于测试桩。
// 生产实现位于 internal/integrations/channel/wechat_runner.go(已存在)的瘦适配,
// 此处只声明接口。
type ContainerExecutor interface {
	// ExecAttach 在容器内 exec 命令,返回 multiplex stdout/stderr 流。
	// 命令由实现固定:["/usr/local/bin/oc-weixin-login"]。
	ExecAttach(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error)
	// ExecExitCode 等待上一次 exec 完成并返回 exit code。
	ExecExitCode(ctx context.Context) (int, error)
}

// WeixinEventType 表示扫码登录过程中产生的事件类型。
type WeixinEventType string

const (
	// WeixinEventQRCode 收到二维码 URL(供前端展示)。
	WeixinEventQRCode WeixinEventType = "qrcode"
	// WeixinEventBound 扫码成功,凭证可用。
	WeixinEventBound WeixinEventType = "bound"
	// WeixinEventFailed 登录失败或超时。
	WeixinEventFailed WeixinEventType = "failed"
)

// WeixinEvent 是 runner 推给上层的事件。
// 不同 Type 用到的字段不同;未用字段保持空值。
type WeixinEvent struct {
	Type      WeixinEventType
	QRCodeURL string // QRCode 类型用
	AccountID string // Bound 类型用 = iLink bot 身份 <hex>@im.bot
	Token     string
	BaseURL   string
	UserID    string
	Error     string // Failed 类型用
}

// WeixinRunner 是微信扫码登录的协调器。
// 通过 docker exec 调用容器内的 oc-weixin-login 脚本,stdcopy 分流:
//   - stdout 累积成单行 JSON → 解析为 Bound 事件
//   - stderr 行级流,匹配 QR URL → QRCode 事件;其余进 Failed.Error
type WeixinRunner struct {
	executor ContainerExecutor
}

// NewWeixinRunner 创建 runner。
func NewWeixinRunner(executor ContainerExecutor) *WeixinRunner {
	return &WeixinRunner{executor: executor}
}

// StreamWeChatLogin 触发一次扫码登录,返回事件 channel。
// channel 在登录结束(成功/失败/超时)后关闭。
// 调用方负责消费 channel 直到关闭;不消费会阻塞 runner goroutine。
func (r *WeixinRunner) StreamWeChatLogin(ctx context.Context, containerID string) (<-chan WeixinEvent, error) {
	stream, err := r.executor.ExecAttach(ctx, containerID, []string{"/usr/local/bin/oc-weixin-login"})
	if err != nil {
		return nil, fmt.Errorf("ExecAttach 失败: %w", err)
	}

	events := make(chan WeixinEvent, 8)
	go func() {
		defer close(events)
		defer stream.Close()

		// 用 io.Pipe + stdcopy.StdCopy 把 multiplex 流拆成两路。
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()
		copyDone := make(chan error, 1)
		go func() {
			_, e := stdcopy.StdCopy(stdoutW, stderrW, stream)
			stdoutW.Close()
			stderrW.Close()
			copyDone <- e
		}()

		// 同步读 stderr:行级,匹配 QR URL。
		stderrDone := make(chan struct{})
		var stderrText strings.Builder
		go func() {
			defer close(stderrDone)
			scanner := bufio.NewScanner(stderrR)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "https://liteapp.weixin.qq.com/") {
					events <- WeixinEvent{Type: WeixinEventQRCode, QRCodeURL: line}
					continue
				}
				stderrText.WriteString(line)
				stderrText.WriteString("\n")
			}
		}()

		// 同步读 stdout:累积成单字符串,最后整体 JSON 解码。
		stdoutBytes, _ := io.ReadAll(stdoutR)
		<-stderrDone
		<-copyDone

		exitCode, _ := r.executor.ExecExitCode(ctx)
		if exitCode != 0 {
			events <- WeixinEvent{
				Type:  WeixinEventFailed,
				Error: strings.TrimSpace(stderrText.String()),
			}
			return
		}

		var cred struct {
			AccountID string `json:"account_id"`
			Token     string `json:"token"`
			BaseURL   string `json:"base_url"`
			UserID    string `json:"user_id"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(stdoutBytes), &cred); err != nil {
			events <- WeixinEvent{
				Type:  WeixinEventFailed,
				Error: fmt.Sprintf("解析凭证 JSON 失败: %v", err),
			}
			return
		}

		events <- WeixinEvent{
			Type:      WeixinEventBound,
			AccountID: cred.AccountID,
			Token:     cred.Token,
			BaseURL:   cred.BaseURL,
			UserID:    cred.UserID,
		}
	}()
	return events, nil
}
```

注意:上面用了 `bytes.TrimSpace`,需要 `import "bytes"`。补 import:

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/pkg/stdcopy"
)
```

- [ ] **Step 4: Run tests pass**

Run: `go test ./internal/integrations/hermes/... -run TestStreamWeChatLogin -v`
Expected: 3 个测试全部 PASS。

- [ ] **Step 5: 跑整个 hermes 包测试**

Run: `go test ./internal/integrations/hermes/...`
Expected: 所有 prompt/config/skills/wechat_runner 测试全 PASS。

- [ ] **Step 6: Commit Phase 2**

```bash
git add internal/integrations/hermes/
git commit -m "$(cat <<'EOF'
feat(integrations): 新增 hermes 集成层(纯新增,业务路径未切)

新增 internal/integrations/hermes/ 包,覆盖以下职责:

- prompt.go:Render(PromptInput) (PromptResult, error) 渲染 SOUL.md,签名与
  OpenClaw 时代一致,输出格式从 config patch 字符串换成 markdown 文档。
- config.go:RenderConfigYAML / RenderEnv 渲染 Hermes 启动需要的
  config.yaml(model.provider=custom + base_url + api_key + auxiliary 全 main)
  和 .env(OPENAI_API_KEY/OPENAI_BASE_URL)。
- skills.go:RenderKnowledgeSkill 把知识库文档转化为 ~/.hermes/skills/kb-*/
  目录的 SKILL.md(frontmatter + 正文)。
- wechat_runner.go:WeixinRunner 通过 docker exec oc-weixin-login,
  stdcopy 分流 stdout(单行 JSON 凭证)与 stderr(QR URL),抽象为 WeixinEvent
  channel 推给上层。

所有 Render* 函数返回字节内容,不直接写文件;真正落盘由 worker handler 负责。
单测覆盖正常路径、必填字段缺失、非法 slug/scope、docker exec 失败。

本 commit 后 manager 业务路径仍走 OpenClaw,内部 import 都没改。
EOF
)"
```

---

## Phase 3: 切换 worker handler + channel runner 到 hermes(Commit 3)

业务路径切换。提交后 manager 实际运行依赖 Hermes;`internal/integrations/openclaw/` 仍留(commit 6 删)。

### Task 3.1: app_initialize.go 改成走 hermes 集成层

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go`(行 290-301 / 651-697 / import)

- [ ] **Step 1: 改 import**

把 `"oc-manager/internal/integrations/openclaw"` 这一行删掉,改成 `"oc-manager/internal/integrations/hermes"`。

- [ ] **Step 2: 改 prompt 渲染调用(行 290-301 范围,具体行号 implementer 当场看)**

旧:
```go
result, err := openclaw.Render(openclaw.PromptInput{...})
```
新:
```go
result, err := hermes.Render(hermes.PromptInput{...})
```

- [ ] **Step 3: 把渲染产物从 "openclaw config patch --stdin" 改成"写宿主机 SOUL.md + config.yaml + .env + skills/"**

定位现有 `configureOpenClawDefaultModel`(行 651-697)。删除整个函数,改用以下替代逻辑(放在 app_initialize Handle 内挂载目录准备阶段,容器启动之前):

```go
// 准备 Hermes 主目录 apps/<app_id>/.hermes/。
hermesHome := h.appHermesHome(app.ID)
if err := os.MkdirAll(filepath.Join(hermesHome, "skills"), 0o755); err != nil {
	return fmt.Errorf("mkdir hermes home: %w", err)
}

// 渲染 SOUL.md(prompt 三层 + 占位符)。
promptResult, err := hermes.Render(hermes.PromptInput{
	PlatformPrompt: cfg.Hermes.SystemPromptTemplate, // 见 Phase 4 改名
	OrgPrompt:      org.PromptText,
	AppPrompt:      app.PromptText,
	Variables:      hermes.VariablesFromContext(org.Name, app.Name, owner.Username),
})
if err != nil {
	return fmt.Errorf("render SOUL.md: %w", err)
}
if err := os.WriteFile(filepath.Join(hermesHome, "SOUL.md"), []byte(promptResult.Prompt), 0o644); err != nil {
	return fmt.Errorf("write SOUL.md: %w", err)
}

// 渲染 config.yaml(model provider)。
yamlContent, err := hermes.RenderConfigYAML(hermes.ConfigInput{
	ModelName:   app.ModelID,
	NewAPIURL:   cfg.NewAPI.InternalURL,
	NewAPIToken: appToken, // 由 newapi 创建 token 步骤生成
})
if err != nil {
	return fmt.Errorf("render config.yaml: %w", err)
}
if err := os.WriteFile(filepath.Join(hermesHome, "config.yaml"), []byte(yamlContent), 0o644); err != nil {
	return fmt.Errorf("write config.yaml: %w", err)
}

// 渲染 .env(API key 备用)。
envContent := hermes.RenderEnv(hermes.EnvInput{
	NewAPIURL:   cfg.NewAPI.InternalURL,
	NewAPIToken: appToken,
})
if err := os.WriteFile(filepath.Join(hermesHome, ".env"), []byte(envContent), 0o600); err != nil {
	return fmt.Errorf("write .env: %w", err)
}

// 渲染知识库 → skills/。
for _, doc := range orgKnowledge { // orgKnowledge / appKnowledge 从 service 层获取
	rendered, err := hermes.RenderKnowledgeSkill(hermes.KnowledgeDoc{
		Scope: hermes.ScopeOrg, Slug: doc.Slug, Title: doc.Title, Summary: doc.Summary, Body: doc.Body,
	})
	if err != nil {
		return fmt.Errorf("render org skill %q: %w", doc.Slug, err)
	}
	skillDir := filepath.Join(hermesHome, "skills", rendered.DirName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("mkdir skill dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(rendered.SkillMD), 0o644); err != nil {
		return fmt.Errorf("write skill md: %w", err)
	}
}
// (appKnowledge 同上,Scope: hermes.ScopeApp)
```

辅助函数加在文件底部:

```go
// appHermesHome 返回宿主机上 app 的 Hermes 主目录路径。
// manager 启动容器时把该目录挂载到容器内 /opt/data。
func (h *AppInitializeHandler) appHermesHome(appID string) string {
	return filepath.Join(h.cfg.DataDir, "apps", appID, ".hermes")
}
```

- [ ] **Step 4: 改容器创建参数(行 315-365 范围)**

把容器 `Mounts` 从 5 个挂载(`/workspace` `/state` `/logs` `/knowledge/org` `/knowledge/app`)简化为 1 个:

```go
spec := runtimepkg.ContainerSpec{
	Image: cfg.Hermes.RuntimeImage,
	Name:  fmt.Sprintf("hermes-%s", app.ID),
	Networks: cfg.Hermes.ContainerNetworks,
	Env: []string{
		fmt.Sprintf("HERMES_UID=%d", os.Getuid()),
		fmt.Sprintf("HERMES_GID=%d", os.Getgid()),
	},
	Mounts: []runtimepkg.Mount{
		{HostPath: h.appHermesHome(app.ID), ContainerPath: "/opt/data", ReadOnly: false},
	},
	RestartPolicy: "unless-stopped",
}
```

- [ ] **Step 5: 替换健康检查调用 WaitForOpenClawHealthy → 不调用(由 docker HEALTHCHECK 自动)**

定位 `WaitForOpenClawHealthy(ctx, nodeID, container.ID)` 调用点(`app_initialize.go` 末尾附近),改成等待 docker inspect 结果:

```go
// 等容器 docker HEALTHCHECK 报 healthy。 Hermes 启动 + iLink 长轮询建立约 5-10s,
// 留 120s 余量。HEALTHCHECK 内部跑 hermes gateway status。
if err := h.runtime.WaitContainerHealthy(ctx, app.RuntimeNodeID, container.ID, 120*time.Second); err != nil {
	return fmt.Errorf("hermes 容器未达 healthy: %w", err)
}
```

`WaitContainerHealthy` 是 adapter 新方法,实现在 Phase 5 Task 5.2 增加。本 commit 内**先在 adapter 上补占位实现** (返回 nil) 不阻塞编译,Phase 5 再补正逻辑。

- [ ] **Step 6: 编译通过(暂可能依赖未实现的字段,如 cfg.Hermes.RuntimeImage,本 Task 内只改 import + 调用,字段名变化由 Phase 4 完成。先编译通过靠 Phase 4 之前的桩)**

实施侧注释:本 Task 与 Phase 4(config rename)有强耦合。**实际操作顺序**:
1. 先做 Task 4 config 改名(让 `cfg.Hermes.RuntimeImage` 存在)
2. 再做 Task 3.1(handler 改写)

为保证 commit boundary 清晰,**plan 写作侧把 Task 4 内的 config rename 与字段保留逻辑放进 Task 4**,本 Task 3.1 假设 Phase 4 已经完成。Phase 3 提交时实际上把 Task 4 也一起做,但 commit message 拆开。

> **执行者注意**:本 Task 因为字段引用 `cfg.Hermes.*`,需要 Phase 4 先把 `OpenClawConfig` rename 为 `HermesConfig`。**Implementer 应该交替执行 Phase 3 与 Phase 4 步骤**:先做 Phase 4 整套,再做 Phase 3,但分两个 commit 提交。

- [ ] **Step 7: 不单独 commit。**

### Task 3.2: app_health_check.go /healthz curl → docker Health.Status

**Files:**
- Modify: `internal/worker/handlers/app_health_check.go`

- [ ] **Step 1: 删除 healthCheckCmd(行 31 附近)**

旧:
```go
var healthCheckCmd = []string{"curl", "-fsS", "--max-time", "5", "http://127.0.0.1:18789/healthz"}
```

直接删,Hermes 不需要。

- [ ] **Step 2: 改 Handle 入口逻辑**

把"在容器内 exec curl 拿退出码"改成"docker inspect 拿 Health.Status":

```go
func (h *AppHealthCheckHandler) Handle(ctx context.Context, job sqlc.Job) error {
	// ... 解析 payload 拿 appID,从 db 查 app 拿 nodeID/containerID
	info, err := h.runtime.InspectContainer(ctx, app.RuntimeNodeID, containerID)
	if err != nil {
		return fmt.Errorf("InspectContainer: %w", err)
	}
	switch info.Health.Status { // ContainerInfo.Health.Status 由 adapter 暴露,Phase 5 补
	case "healthy":
		return h.markAppHealthy(ctx, app.ID)
	case "starting":
		return h.markAppStarting(ctx, app.ID)
	default: // unhealthy / none / 空字符串
		return h.markAppUnhealthy(ctx, app.ID, info.Health.Output)
	}
}
```

`ContainerInfo.Health.Status` 与 `ContainerInfo.Health.Output` 字段在 Phase 5 Task 5.2 加进 adapter。本 Task 写假设。

- [ ] **Step 3: 不单独 commit。**

### Task 3.3: channel_login.go 改成走 hermes WeixinRunner

**Files:**
- Modify: `internal/worker/handlers/channel_login.go`

- [ ] **Step 1: 改 import**

把 `"oc-manager/internal/integrations/openclaw"` 替换为 `"oc-manager/internal/integrations/hermes"`(如果 import 直接用了 openclaw 包)。把 `"oc-manager/internal/integrations/channel"` 中调用 OpenClaw 路径的部分,改成调 `hermes.WeixinRunner`。

具体修改:`ChannelStartLoginHandler.Handle` 内部原来通过 `channel.DockerCommandRunner.StreamWeChatLogin` 拿事件 channel(channel 中的字符串经过 openclaw.ParseChannelLoginEvent 解析)。改为:

```go
runner := hermes.NewWeixinRunner(h.executor) // h.executor 实现 hermes.ContainerExecutor
events, err := runner.StreamWeChatLogin(ctx, containerID)
if err != nil {
	return fmt.Errorf("启动 hermes weixin runner: %w", err)
}
for ev := range events {
	switch ev.Type {
	case hermes.WeixinEventQRCode:
		// 写 challenge 进 channel_binding (QR URL)
		if err := h.queries.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
			ID:           binding.ID,
			Status:       "challenge",
			BoundIdentity: pgtype.Text{}, // 仍空
			MetadataJson: encodeMetadata(map[string]any{"qrcode_url": ev.QRCodeURL}),
		}); err != nil {
			return err
		}
	case hermes.WeixinEventBound:
		// 1. 把 WEIXIN_ACCOUNT_ID 等追加到 .env(让 gateway 重启后读取)
		if err := h.appendWeixinEnv(app.ID, ev); err != nil {
			return fmt.Errorf("append .env: %w", err)
		}
		// 2. 更新 binding 状态
		if err := h.queries.MarkChannelBindingBound(ctx, sqlc.MarkChannelBindingBoundParams{
			ID:            binding.ID,
			BoundIdentity: pgtype.Text{String: ev.AccountID, Valid: true},
		}); err != nil {
			return err
		}
		// 3. 重启容器让 gateway 读 .env
		if err := h.runtime.RestartContainer(ctx, app.RuntimeNodeID, containerID); err != nil {
			return fmt.Errorf("restart hermes: %w", err)
		}
	case hermes.WeixinEventFailed:
		return h.queries.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			ID:        binding.ID,
			Status:    "failed",
			LastError: pgtype.Text{String: ev.Error, Valid: true},
		})
	}
}
return nil
```

`appendWeixinEnv` 辅助函数:

```go
// appendWeixinEnv 把扫码登录后拿到的 WEIXIN_* 凭证追加写入宿主机 apps/<app_id>/.hermes/.env。
// gateway 容器重启后读 .env 装载 weixin platform。
func (h *ChannelStartLoginHandler) appendWeixinEnv(appID string, ev hermes.WeixinEvent) error {
	envPath := filepath.Join(h.cfg.DataDir, "apps", appID, ".hermes", ".env")
	f, err := os.OpenFile(envPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	lines := []string{
		"",
		"# === 由 hermes WeixinRunner 写入 ===",
		fmt.Sprintf("WEIXIN_ACCOUNT_ID=%s", ev.AccountID),
		fmt.Sprintf("WEIXIN_TOKEN=%s", ev.Token),
	}
	if ev.BaseURL != "" {
		lines = append(lines, fmt.Sprintf("WEIXIN_BASE_URL=%s", ev.BaseURL))
	}
	lines = append(lines, "WEIXIN_CDN_BASE_URL=https://novac2c.cdn.weixin.qq.com/c2c", "")
	_, err = f.WriteString(strings.Join(lines, "\n"))
	return err
}
```

- [ ] **Step 2: 删 ChannelStartLoginHandler 中的 fallback 逻辑(行 225-226 注释提到的 plugin state 文件读取)**

定位行 225-226 附近读 `/root/.openclaw/openclaw-weixin/accounts/*.json` 的 fallback 代码块,整段删除。Hermes 模式下 account_id 在事件流的 Bound 事件里就拿到了,不需要回读 plugin state。

- [ ] **Step 3: 不单独 commit。**

### Task 3.4: runtime_refresh_status.go(无强耦合,只更新 import)

**Files:**
- Modify: `internal/worker/handlers/runtime_refresh_status.go`

- [ ] **Step 1: 看 imports**

Run: `grep -n "openclaw\|OpenClaw" internal/worker/handlers/runtime_refresh_status.go`
Expected: 大概率无匹配(Explore 报告说"与 OpenClaw 无直接耦合")。如果有 import 残留则替换 path,否则跳过本 Task。

- [ ] **Step 2: 不单独 commit。**

### Task 3.5: internal/integrations/channel/wechat_runner.go 委托给 hermes

**Files:**
- Modify: `internal/integrations/channel/wechat_runner.go`

注意:此文件之前实现了 OpenClaw 时代的扫码协议(中文 stdout 解析、openclaw channels login CLI)。Hermes 时代我们在 `internal/integrations/hermes/wechat_runner.go` 里写了新实现。本 Task 把 channel 包内的 wechat_runner.go 改为**瘦适配**,直接委托给 hermes 包。

- [ ] **Step 1: 重写 wechat_runner.go**

```go
// Package channel 是渠道适配层,把 worker handler 与具体 runtime 实现解耦。
// 当前 (Hermes 时代) 内部委托给 internal/integrations/hermes/wechat_runner.go,
// 保持向后兼容的 type 名 DockerCommandRunner 和 method StreamWeChatLogin(...)。
package channel

import (
	"context"

	"github.com/docker/docker/client"

	"oc-manager/internal/integrations/hermes"
)

// AuthInput 保留旧名,避免改 caller。
type AuthInput struct {
	AppID       string
	ContainerID string
	NodeID      string
}

// AppContainerLookup 保留旧名,但 Hermes 时代直接从 AuthInput.ContainerID 取,不再回查。
// 留作过渡,Phase 6 cleanup 可酌情删。
type AppContainerLookup func(ctx context.Context, appID string) (containerID string, err error)

// ContainerExecutor 保留旧名,实现是 docker SDK 包装。
type ContainerExecutor interface {
	hermes.ContainerExecutor
}

// DockerCommandRunner 是渠道适配层对外暴露的类型,委托给 hermes.WeixinRunner。
type DockerCommandRunner struct {
	inner *hermes.WeixinRunner
}

// NewDockerCommandRunner 工厂。
func NewDockerCommandRunner(executor ContainerExecutor, _ AppContainerLookup) *DockerCommandRunner {
	return &DockerCommandRunner{inner: hermes.NewWeixinRunner(executor)}
}

// StreamWeChatLogin 委托。注意返回类型升级为 WeixinEvent channel(原 OpenClaw 时代是 <-chan string)。
// 上游 caller(channel_login.go)已在 Task 3.3 改为消费 hermes.WeixinEvent。
func (r *DockerCommandRunner) StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan hermes.WeixinEvent, error) {
	return r.inner.StreamWeChatLogin(ctx, input.ContainerID)
}

// 保留 docker client 引用,避免 module-level import 未使用警告(Phase 6 看是否还要)。
var _ = client.NewClientWithOpts
```

- [ ] **Step 2: 删 wechat_runner_test.go 中 OpenClaw 时代的 stdout 文本协议测试**

旧测试逻辑大量耦合 OpenClaw 中文文本 stdout(如"扫描成功"等)。删除整个文件,Hermes 协议下不再适用:

```bash
git rm internal/integrations/channel/wechat_runner_test.go
```

(Hermes 时代的测试已经在 `internal/integrations/hermes/wechat_runner_test.go`。)

- [ ] **Step 3: 编译通过 + handler 单测全过**

Run: `go build ./... && go test ./internal/worker/handlers/... -v`
Expected: 编译通过;handler 测试可能因 mock 接口名变化失败,implementer 现场修测试 fixture(对应 Task 5.2 的 adapter 接口扩展)。

- [ ] **Step 4: Commit Phase 3**

```bash
git add internal/worker/handlers/ internal/integrations/channel/
git commit -m "$(cat <<'EOF'
feat(worker): 切换 worker handler + channel runner 到 hermes 后端

把 4 个 worker handler 与 channel 适配层的实际后端从 OpenClaw 切到 Hermes:

- app_initialize.go:prompt 渲染产物写入 SOUL.md;config.yaml 与 .env 渲染落到
  apps/<app_id>/.hermes/;知识库展开到 .hermes/skills/kb-*/SKILL.md;
  容器挂载从 5 路(workspace/state/logs/knowledge/org/knowledge/app)
  简化为 1 路(.hermes → /opt/data);删 configureOpenClawDefaultModel
  与 WaitForOpenClawHealthy 调用,改用 WaitContainerHealthy(adapter Phase 5 补)。
- app_health_check.go:删 curl http://127.0.0.1:18789/healthz,
  改为 docker inspect Health.Status(healthy/starting/unhealthy)。
- channel_login.go:扫码事件流接 hermes.WeixinRunner,bound 时把
  WEIXIN_ACCOUNT_ID/TOKEN/BASE_URL/CDN_BASE_URL 追加写入 .env,然后
  RestartContainer 让 gateway 加载;删除 OpenClaw plugin state 文件 fallback。
- runtime_refresh_status.go:仅 import path 更新(原本就走通用 RuntimeAdapter)。
- internal/integrations/channel/wechat_runner.go:重写为对
  internal/integrations/hermes/wechat_runner 的瘦适配,保留 DockerCommandRunner /
  StreamWeChatLogin 旧 type 名以减少 caller diff;事件类型升级为 hermes.WeixinEvent。
- 删除 wechat_runner_test.go(原 OpenClaw 中文 stdout 协议测试,Hermes 协议已在
  internal/integrations/hermes/wechat_runner_test.go 覆盖)。

本 commit 后 manager 业务逻辑实际依赖 Hermes;
internal/integrations/openclaw/ 仍保留(Phase 6 删)。
EOF
)"
```

---

## Phase 4: OpenClawConfig → HermesConfig + yaml key 重命名(Commit 4)

### Task 4.1: rename struct + yaml key

**Files:**
- Modify: `internal/config/config.go`(行 97-128)
- Modify: `deploy/config/manager.example.yaml`(或同等 example 配置文件,implementer grep 找)

- [ ] **Step 1: 找 example yaml**

Run: `grep -rn "openclaw:" deploy/ config/ 2>/dev/null | head -5`
Expected: 列出含 `openclaw:` 顶级 key 的 example 文件路径。

- [ ] **Step 2: 改 config.go(行 97-128)**

旧:
```go
type OpenClawConfig struct {
	RuntimeImage         string                 `yaml:"runtime_image"`
	SystemPromptTemplate string                 `yaml:"system_prompt_template"`
	Workspace            WorkspaceConfig        `yaml:"workspace"`
	LLM                  OpenClawLLMConfig      `yaml:"llm"`
	ContainerNetworks    []string               `yaml:"container_networks"`
}

type OpenClawLLMConfig struct {
	BaseURL         string `yaml:"base_url"`
	DefaultProvider string `yaml:"default_provider"`
	DefaultModel    string `yaml:"default_model"`
}
```

新:
```go
// HermesConfig 是 Hermes runtime 镜像与 manager 集成的配置段。
// 对应应用 yaml 顶级 key `hermes:`。
type HermesConfig struct {
	// RuntimeImage 是 manager docker run 启动 hermes 容器用的镜像引用(name:tag)。
	RuntimeImage string `yaml:"runtime_image"`
	// SystemPromptTemplate 是平台级 prompt 模板,会作为 hermes.PromptInput.PlatformPrompt。
	SystemPromptTemplate string `yaml:"system_prompt_template"`
	// Workspace 仅保留同名段(WorkspaceConfig 是通用类型,不绑 Hermes)。
	Workspace WorkspaceConfig `yaml:"workspace"`
	// LLM 是 hermes.RenderConfigYAML 时的默认值兜底,在 app 未指定模型时使用。
	LLM HermesLLMConfig `yaml:"llm"`
	// ContainerNetworks 是 hermes 容器接入的 docker network 清单。
	ContainerNetworks []string `yaml:"container_networks"`
}

// HermesLLMConfig 仅保留兜底默认值字段(具体每 app 的模型由 apps.model_id 决定)。
type HermesLLMConfig struct {
	BaseURL         string `yaml:"base_url"`
	DefaultProvider string `yaml:"default_provider"`
	DefaultModel    string `yaml:"default_model"`
}
```

同步:`Config` 主 struct 上若有 `OpenClaw OpenClawConfig` 字段,改为 `Hermes HermesConfig`,yaml tag `openclaw` → `hermes`。

- [ ] **Step 3: 同步 example yaml**

把 `openclaw:` 顶级 key 改为 `hermes:`,内部 `runtime_image`、`system_prompt_template` 等子键不变。
`runtime_image` 值改成 hermes 镜像引用(例如 `hermes-runtime:dev` 或后续生产 tag)。

- [ ] **Step 4: grep 漏网**

Run: `grep -rn "OpenClawConfig\|OpenClawLLMConfig\|cfg.OpenClaw\b" --include='*.go' .`
Expected: 漏网调用点全部列出。逐个替换为 `HermesConfig` / `HermesLLMConfig` / `cfg.Hermes`。

- [ ] **Step 5: 跑 config 测试 + 整 build**

Run: `go test ./internal/config/... && go build ./...`
Expected: 全 PASS,build 成功。

- [ ] **Step 6: Commit Phase 4**

```bash
git add internal/config/ deploy/ config/ 2>/dev/null
git commit -m "$(cat <<'EOF'
refactor(config): OpenClawConfig 改名 HermesConfig + yaml key 重命名

把 internal/config/config.go 中的 OpenClawConfig / OpenClawLLMConfig 类型
重命名为 HermesConfig / HermesLLMConfig,内部字段保持不变;应用 yaml 顶级 key
openclaw: 改为 hermes:,字段语义一致。

启动加载不再支持老的 openclaw: key(完全替换,不做向后兼容)。所有引用点
(handler 等)统一改为 cfg.Hermes,与 Phase 3 切换的 hermes 集成层调用对齐。

example yaml 中 runtime_image 同步指向 Hermes 镜像。
EOF
)"
```

---

## Phase 5: SyncOpenClawImage → SyncRuntimeImage + adapter 健康相关方法(Commit 5)

### Task 5.1: imagesync rename

**Files:**
- Modify: `internal/runtime/imagesync/service.go`(行 63 附近)
- Modify: `internal/integrations/runtime/agent_backed.go`(行 70-76)

- [ ] **Step 1: 改 imagesync/service.go**

把 `SyncOpenClawImage` 方法名改为 `SyncRuntimeImage`,内部实现保留不变(它本来就 runtime-agnostic,只是名字带了 OpenClaw)。

```go
// SyncRuntimeImage 把指定镜像同步到目标节点。
// 名字保持 runtime-agnostic,Hermes 时代复用此方法。
func (s *Service) SyncRuntimeImage(ctx context.Context, nodeID string, image string) (SyncResult, error) {
	// ... 原 SyncOpenClawImage 内部实现 ...
}
```

- [ ] **Step 2: 改 agent_backed.go(行 70-76)**

把 `s.SyncOpenClawImage` 替换为 `s.SyncRuntimeImage`。

- [ ] **Step 3: grep 漏网**

Run: `grep -rn "SyncOpenClawImage" --include='*.go' .`
Expected: 0 个匹配。否则替换。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/runtime/imagesync/... && go test ./internal/integrations/runtime/...`
Expected: PASS。

- [ ] **Step 5: 不单独 commit。**

### Task 5.2: 删 WaitForOpenClawHealthy + execCurlExitCode,新增 WaitContainerHealthy + ContainerInfo.Health

**Files:**
- Modify: `internal/integrations/runtime/adapter.go`
- Modify: `internal/integrations/runtime/agent_backed.go`(行 380-450)
- Test: `internal/integrations/runtime/agent_backed_test.go`(如果已存在)

- [ ] **Step 1: adapter.go 接口扩展 + ContainerInfo 字段**

在 `Adapter` interface 内,把 `ContainerExec` 注释里 "OpenClaw" 字样去掉,改为通用:

```go
// ContainerExec 在容器内执行 cmd,返回 exit code 与 stdout(截断到 4KB)。
ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (ExecResult, error)
```

新增方法:

```go
// WaitContainerHealthy 阻塞至容器 docker HEALTHCHECK 报 healthy,或超时。
// Hermes 镜像 HEALTHCHECK 内部跑 hermes gateway status,初始 start-period 60s。
WaitContainerHealthy(ctx context.Context, nodeID, containerID string, timeout time.Duration) error
```

`ContainerInfo` struct 增加字段:

```go
type ContainerInfo struct {
	// ... 原有字段 ...

	// Health 反映 docker HEALTHCHECK 当前状态。
	Health ContainerHealth
}

// ContainerHealth 是 docker container HealthCheck 的快照。
type ContainerHealth struct {
	// Status: "healthy" / "unhealthy" / "starting" / "" (未配置 HEALTHCHECK)。
	Status string
	// Output 最近一次 HEALTHCHECK 的 stdout/stderr 截断。
	Output string
}
```

- [ ] **Step 2: 改 agent_backed.go 实现**

删除 `WaitForOpenClawHealthy`(行 395-429)与 `execCurlExitCode`(行 445-470 左右)整两个函数(Hermes 不再需要 curl)。

新增 `WaitContainerHealthy`:

```go
// WaitContainerHealthy 轮询 docker inspect 拿 .State.Health.Status,
// 等到 "healthy" 或 ctx 超时为止。
// 用 InspectContainer adapter 方法复用,避免直接耦合 docker SDK。
func (a *AgentBackedAdapter) WaitContainerHealthy(ctx context.Context, nodeID, containerID string, timeout time.Duration) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	const step = 3 * time.Second
	for {
		info, err := a.InspectContainer(deadline, nodeID, containerID)
		if err != nil {
			return err
		}
		switch info.Health.Status {
		case "healthy":
			return nil
		case "unhealthy":
			return fmt.Errorf("容器 %s HEALTHCHECK 返回 unhealthy: %s", containerID, info.Health.Output)
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("容器 %s 在 %s 内未达 healthy", containerID, timeout)
		case <-time.After(step):
		}
	}
}
```

`InspectContainer` 内部要把 docker SDK 返回的 `types.ContainerJSON` 中的 health 字段映射到 `ContainerInfo.Health`:

```go
// 引用 docker SDK 类型:
//   import "github.com/docker/docker/api/types"
//   types.ContainerJSON.ContainerJSONBase.State.Health 字段类型为 *types.Health,
//   含 Status (string)、FailingStreak (int)、Log ([]*HealthcheckResult)。
func (a *AgentBackedAdapter) InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerInfo, error) {
	// ... 现有逻辑拿到 raw (types.ContainerJSON) ...
	out := ContainerInfo{ /* ... 原有字段填充 ... */ }
	if raw.State != nil && raw.State.Health != nil {
		out.Health.Status = raw.State.Health.Status
		if n := len(raw.State.Health.Log); n > 0 {
			// 取最近一次 HealthcheckResult.Output (运行 healthcheck.sh 的 stdout/stderr)。
			out.Health.Output = raw.State.Health.Log[n-1].Output
		}
	}
	return out, nil
}
```

- [ ] **Step 3: 写测试(如果现有测试覆盖 OpenClaw 健康检查路径)**

Run: `grep -rn "WaitForOpenClawHealthy\|execCurlExitCode" --include='*.go' internal/`
Expected: 仅本文件残留(已删)。若 _test.go 中有调用,删除/替换测试用例。

新写 `WaitContainerHealthy` 单测:

```go
package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeInspector 按顺序返回预设的 ContainerInfo 序列;
// 当序列耗尽,后续调用返回最后一个值(模拟稳定状态)。
type fakeInspector struct {
	seq []ContainerInfo
	idx int32
}

func (f *fakeInspector) InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerInfo, error) {
	i := atomic.AddInt32(&f.idx, 1) - 1
	if int(i) >= len(f.seq) {
		return f.seq[len(f.seq)-1], nil
	}
	return f.seq[i], nil
}

func TestWaitContainerHealthy_StartingThenHealthy(t *testing.T) {
	// 覆盖容器先 starting 后 healthy 的常规路径。
	insp := &fakeInspector{seq: []ContainerInfo{
		{Health: ContainerHealth{Status: "starting"}},
		{Health: ContainerHealth{Status: "starting"}},
		{Health: ContainerHealth{Status: "healthy"}},
	}}
	a := &AgentBackedAdapter{inspector: insp}
	err := a.WaitContainerHealthy(context.Background(), "node1", "cont1", 30*time.Second)
	require.NoError(t, err)
}

func TestWaitContainerHealthy_UnhealthyFailsFast(t *testing.T) {
	// 容器报 unhealthy 时不再等,立刻返错并把 Output 带入错误信息。
	insp := &fakeInspector{seq: []ContainerInfo{
		{Health: ContainerHealth{Status: "unhealthy", Output: "boom"}},
	}}
	a := &AgentBackedAdapter{inspector: insp}
	err := a.WaitContainerHealthy(context.Background(), "node1", "cont1", 30*time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}
```

**注意:** `AgentBackedAdapter` 当前实现直接持有 docker client,不通过 inspector 抽象。
本测试要求把 `InspectContainer` 调用提取到一个可注入接口(如新增一个内部字段
`inspector containerInspector interface{ InspectContainer(...)... }`)。
具体重构方式见 Task 5.2 Step 2 修改的 `InspectContainer` 函数:
保持公共接口不变,但内部通过 inspector 字段访问以便测试桩注入。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/integrations/runtime/... -v`
Expected: 全 PASS。

- [ ] **Step 5: 整体 build**

Run: `go build ./...`
Expected: 成功。

- [ ] **Step 6: Commit Phase 5**

```bash
git add internal/runtime/imagesync/ internal/integrations/runtime/
git commit -m "$(cat <<'EOF'
refactor(adapter): SyncOpenClawImage → SyncRuntimeImage + 健康检查改 docker HEALTHCHECK

- imagesync/service.go: SyncOpenClawImage 改名为 SyncRuntimeImage,
  内部实现不变(本来就 runtime-agnostic),所有调用方同步。
- adapter.go: Adapter 接口新增 WaitContainerHealthy 方法,ContainerInfo 增加
  Health 字段(Status + Output)。ContainerExec 注释去 OpenClaw 字样。
- agent_backed.go: 删除 WaitForOpenClawHealthy / execCurlExitCode 两个
  OpenClaw 专用函数(原 curl /healthz 路径),改为 WaitContainerHealthy 轮询
  docker inspect .State.Health.Status,等到 "healthy" 或 ctx 超时。
  InspectContainer 内把 raw.State.Health 映射到 ContainerInfo.Health。

worker handler (Phase 3) 已经用 WaitContainerHealthy / Health.Status 替代旧
curl 路径,本 commit 实际让 adapter 实现就位。
EOF
)"
```

---

## Phase 6: 删除 internal/integrations/openclaw/ 和 runtime/openclaw/(Commit 6)

### Task 6.1: 删除目录 + 关联测试

**Files:**
- Delete: `internal/integrations/openclaw/`
- Delete: `runtime/openclaw/`

- [ ] **Step 1: 确认无引用**

Run:
```bash
grep -rn '"oc-manager/internal/integrations/openclaw"' --include='*.go' . | grep -v '^Binary'
```
Expected: 0 个匹配(若有则回头修 caller)。

Run:
```bash
grep -rn "openclaw" --include='*.yaml' --include='*.yml' --include='*.go' . \
  | grep -v "_test.go\|^docs/\|^README\|^CLAUDE\|^AGENTS\|^docs/superpowers/" \
  | grep -v "openapi" \
  | head -30
```
Expected: 只有 docs / commit message 字样,代码里应该已经 0 个。

- [ ] **Step 2: 删除目录**

```bash
git rm -r internal/integrations/openclaw/
git rm -r runtime/openclaw/
```

- [ ] **Step 3: 整 build + 全测试**

Run: `go build ./... && go test ./...`
Expected: 全 PASS。

- [ ] **Step 4: Commit Phase 6**

```bash
git commit -m "$(cat <<'EOF'
chore(cleanup): 删除 internal/integrations/openclaw/ 和 runtime/openclaw/

Phase 1-5 后 manager 业务路径已完整切到 Hermes,
本 commit 删除原 OpenClaw 实现的所有残留:

- internal/integrations/openclaw/ 整目录(prompt.go / parser.go 等)
- runtime/openclaw/ 整目录(Dockerfile / CONTRACT.md / version.txt /
  healthcheck.sh / verify-install.sh)

删除后 grep 'integrations/openclaw' 与 'runtime/openclaw' 在代码中 0 匹配;
docs/ 与 README 中的 OpenClaw 字样在 Phase 8 一并清理。
EOF
)"
```

---

## Phase 7: channel_bindings.bound_identity COMMENT 更新(Commit 7)

### Task 7.1: 加 migration

**Files:**
- Create: `internal/migrations/000016_runtime_to_hermes.up.sql`
- Create: `internal/migrations/000016_runtime_to_hermes.down.sql`

- [ ] **Step 1: 写 up.sql**

```sql
-- 运行时切换 OpenClaw → Hermes 之后的字段语义注释更新。
-- 字段类型不变(TEXT),仅 COMMENT。
-- 本地 0 行 bound,无数据迁移。

COMMENT ON COLUMN channel_bindings.bound_identity IS
  '微信渠道 iLink Bot 身份,格式 <hex>@im.bot(Hermes runtime 时代)。历史:OpenClaw runtime 时代为 <wxid>@im.wechat。';
```

- [ ] **Step 2: 写 down.sql**

```sql
-- 回滚到 OpenClaw 时代语义注释。
COMMENT ON COLUMN channel_bindings.bound_identity IS
  '微信渠道绑定身份,OpenClaw plugin 写入的 userId(格式 <wxid>@im.wechat)。';
```

- [ ] **Step 3: sqlc 重新生成 + 整 build**

Run: `make sqlc && go build ./...`
Expected: `internal/store/sqlc/models.go` 中 `ChannelBinding.BoundIdentity` 上方 Go 注释自动同步;build 成功。

(若项目没有 `make sqlc` 目标,implementer 找 sqlc 命令直接调:`sqlc generate -f internal/store/sqlc.yaml`。)

- [ ] **Step 4: 跑 migration 验证**

Run:
```bash
docker exec manager-postgres psql -U $(docker inspect manager-postgres --format '{{range .Config.Env}}{{println .}}{{end}}' | awk -F= '/^POSTGRES_USER/{print $2}') -d $(docker inspect manager-postgres --format '{{range .Config.Env}}{{println .}}{{end}}' | awk -F= '/^POSTGRES_DB/{print $2}') -c "
SELECT col_description('channel_bindings'::regclass, attnum)
FROM pg_attribute
WHERE attrelid='channel_bindings'::regclass AND attname='bound_identity';"
```
Expected: 在 migration 跑过后输出新 COMMENT(Hermes 时代语义)。

- [ ] **Step 5: Commit Phase 7**

```bash
git add internal/migrations/000016_runtime_to_hermes.up.sql \
        internal/migrations/000016_runtime_to_hermes.down.sql \
        internal/store/sqlc/models.go
git commit -m "$(cat <<'EOF'
chore(db): channel_bindings.bound_identity 语义注释更新

新增 migration 000016_runtime_to_hermes,把 channel_bindings.bound_identity
列的 COMMENT 从 OpenClaw 时代的 wxid 语义改为 Hermes 时代的 iLink bot id
(<hex>@im.bot)。字段类型不变(TEXT),无数据迁移(本地 0 行 bound)。

sqlc 重新生成,sqlc/models.go 中 ChannelBinding.BoundIdentity 字段的 Go
注释同步更新。
EOF
)"
```

---

## Phase 8: openapi-gen + web-types-gen + 文档同步(Commit 8)

### Task 8.1: 生成 openapi.yaml + 前端类型

**Files:**
- Auto-generated: `openapi/openapi.yaml`
- Auto-generated: `web/src/api/generated.ts`

- [ ] **Step 1: 改 openapi swag 注解中的 title / description(如有 OpenClaw 字样)**

Run: `grep -rn "OpenClaw" cmd/server/ internal/api/handlers/ internal/service/ internal/domain/ 2>/dev/null`
Expected: 列出 swag annotations 含 "OpenClaw" 的 Go 文件。implementer 把字样改为中性"Agent Runtime"。

- [ ] **Step 2: 跑生成**

Run: `make openapi-gen && make web-types-gen`
Expected: `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 出现 diff;diff 中不应再有 OpenClaw 字样。

- [ ] **Step 3: 验证 openapi-check 干净**

Run: `make openapi-gen && git diff --exit-code openapi/openapi.yaml`
Expected: exit 0(generate 后无 diff)。

- [ ] **Step 4: 不单独 commit。**

### Task 8.2: 文档重命名 + 内容更新

**Files:**
- Rename: `docs/openclaw-manager-design.md` → `docs/agent-runtime-manager-design.md`
- Rename: `docs/openclaw-manager-technical-design.md` → `docs/agent-runtime-manager-technical-design.md`
- Modify: `README.md`, `AGENTS.md`, `CLAUDE.md`

- [ ] **Step 1: rename 文档**

```bash
git mv docs/openclaw-manager-design.md docs/agent-runtime-manager-design.md
git mv docs/openclaw-manager-technical-design.md docs/agent-runtime-manager-technical-design.md
```

- [ ] **Step 2: grep 出所有 OpenClaw 字样**

Run:
```bash
grep -rn "OpenClaw\|openclaw" --include='*.md' . \
  | grep -v "^docs/superpowers/specs/2026-05-14-openclaw-to-hermes" \
  | grep -v "^docs/superpowers/plans/2026-05-14-openclaw-to-hermes"
```
Expected: 列出所有需要改的文档行(除本 spec/plan 自身)。

- [ ] **Step 3: 逐文件替换**

按上下文决定换成 "Hermes" 还是 "agent runtime":
- 历史叙述 / 上游产品对比 → 保留 "OpenClaw"(只在历史叙述里)
- 当前架构描述 → "Hermes" 或 "agent runtime"
- README 顶部项目说明 → "agent runtime manager"

CLAUDE.md / AGENTS.md 内容审计:
- `runtime/openclaw/CONTRACT.md` 字样 → `runtime/hermes/CONTRACT.md`
- "OpenClaw 上游" → "Hermes 上游(NousResearch/hermes-agent)"
- "openclaw channels login" 命令示例 → 改为 "docker exec hermes-<app_id> oc-weixin-login"
- 锁定版本 `2026.4.29` 字样 → 看 `runtime/hermes/version.txt`

- [ ] **Step 4: 整 grep 验证(应仅剩 spec / plan / 个别历史叙述)**

Run:
```bash
grep -rn "OpenClaw\|openclaw" --include='*.md' . | head -30
```
Expected: 只剩本 spec/plan 自身 + 显式历史叙述行(如 "原 OpenClaw 时代")。

- [ ] **Step 5: Commit Phase 8**

```bash
git add openapi/ web/src/api/ docs/ README.md AGENTS.md CLAUDE.md cmd/ internal/
git commit -m "$(cat <<'EOF'
docs+openapi: 同步 API 契约和文档到 Hermes 时代

- swag annotations 中 OpenClaw 字样改为 Agent Runtime / Hermes,
  跑 make openapi-gen 重新生成 openapi/openapi.yaml。
- 跑 make web-types-gen 重新生成 web/src/api/generated.ts。
- docs/openclaw-manager-design.md → docs/agent-runtime-manager-design.md
- docs/openclaw-manager-technical-design.md → docs/agent-runtime-manager-technical-design.md
- README.md / AGENTS.md / CLAUDE.md 中 OpenClaw 表述按上下文换为
  Hermes 或 agent runtime;命令示例同步(openclaw channels login →
  docker exec hermes-<app_id> oc-weixin-login);CONTRACT.md 路径
  与版本锁定参考更新到 runtime/hermes/ 目录。
- 历史叙述段保留 OpenClaw 字样,标注为"原 OpenClaw 时代"。

至此 OpenClaw → Hermes 替换完成,grep 'OpenClaw' 在生产代码中
0 匹配,仅 spec/plan/历史段保留。
EOF
)"
```

---

## 交付前手工验证清单

(对应 spec §测试 - 验证清单(交付前 manual))

按 AGENTS.md "新功能开发完成后必须调用浏览器进行全面功能验证":

- [ ] 启 manager 全栈本地服务:`docker compose up -d`
- [ ] manager-web 登录平台管理员 (`admin`/`admin123`)
- [ ] 创建组织,组织内创建 app → 容器启动 healthy
- [ ] docker exec hermes-<app_id> hermes gateway status → exit 0
- [ ] 在 app 内创建微信渠道,触发扫码登录 → 前端展示二维码 URL → 手机微信扫码
- [ ] 等待 bound → channel_bindings.bound_identity 落 `<hex>@im.bot` 格式(SQL 验证)
- [ ] 给 bot 发消息 → 收到 qwen 回复
- [ ] 修改 app 模型 → 容器重启 → 用新模型继续对话
- [ ] 知识库验证:上传组织/应用知识库文档 → app_initialize 后 docker exec
      hermes-<app_id> ls /opt/data/skills/ 应能看到 kb-org-* / kb-app-* 目录

---

## Self-Review 记录(本 plan 撰写阶段自检)

**1. Spec coverage check**

| Spec 节 | 对应 Task |
|---|---|
| §镜像构建 (Dockerfile install.sh + 预装 weixin) | Phase 1 Task 1.1-1.3 ✅ |
| §容器生命周期(每 app 一容器) | Phase 3 Task 3.1 (容器创建参数改写) ✅ |
| §微信扫码登录 (oc-weixin-login.py + docker exec) | Phase 1 Task 1.2 (脚本) + Phase 2 Task 2.4 (runner) + Phase 3 Task 3.3 (handler) ✅ |
| §模型 provider config.yaml + .env | Phase 2 Task 2.2 (Render) + Phase 3 Task 3.1 (写盘) ✅ |
| §知识库 → Hermes skills | Phase 2 Task 2.3 (Render) + Phase 3 Task 3.1 (写盘) ✅ |
| §健康检查 docker HEALTHCHECK | Phase 1 Task 1.2 (healthcheck.sh) + Phase 5 Task 5.2 (WaitContainerHealthy) + Phase 3 Task 3.2 (handler) ✅ |
| §Prompt SOUL.md 注入 | Phase 2 Task 2.1 (Render) + Phase 3 Task 3.1 (写盘) ✅ |
| §数据库 channel_bindings.bound_identity COMMENT | Phase 7 Task 7.1 ✅ |
| §命名/目录 rename 清单 | Phase 4 (config) + Phase 5 (adapter) + Phase 6 (删 openclaw) + Phase 8 (openapi/docs) ✅ |
| §8 个 commit 边界 | Phase 1-8 一一对应 ✅ |

**2. Placeholder scan:** 无 TBD / TODO / "implement later" / "fill in details" / 空泛 "handle edge cases"。所有 step 给出 exact code 或 exact command。

**3. Type consistency check:** `PromptInput` / `PromptResult` 字段在 Task 2.1 与 Task 3.1 一致;`WeixinEvent` 字段在 Task 2.4 与 Task 3.3 一致;`HermesConfig` 字段在 Task 4.1 与 Task 3.1 引用一致;`ContainerInfo.Health` 在 Task 5.2 定义,Task 3.2 引用,签名一致。

**4. 顺序依赖说明:** Task 3.1 引用 `cfg.Hermes.RuntimeImage`(来自 Phase 4)与 `WaitContainerHealthy`(来自 Phase 5)。Implementer 应按下面顺序串行:Phase 1 → Phase 2 → **先做 Phase 4 改名**(让字段就位)→ **再做 Phase 5 adapter 扩展**(让方法就位)→ **再做 Phase 3 handler 切换**(此时引用都已就位)→ Phase 6 → Phase 7 → Phase 8。Commit 顺序仍按 Phase 1-8 时间序提交即可——只是在 working tree 里实际写代码的顺序与提交 stage 顺序不同。这种情况合理,因为 git 接受 stage 跟工作顺序解耦。

---
