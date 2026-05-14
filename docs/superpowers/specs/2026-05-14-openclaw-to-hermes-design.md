# OpenClaw → Hermes 运行时完全替换 设计规格

## 背景

oc-manager 当前的 agent runtime 是 [OpenClaw](https://github.com/openclaw/openclaw),
作为每个 app 独立容器中运行的对话执行环境。manager 通过 `runtime/openclaw/`
镜像、`internal/integrations/openclaw/` 的 stdout 文本协议解析、`openclaw
channels login` CLI 命令以及 `openclaw config patch --stdin` 模板注入与之
深度集成。

本次替换决策:把 OpenClaw 整体替换为
[Hermes Agent (NousResearch)](https://github.com/NousResearch/hermes-agent)。
触发原因不在本规格内讨论;本规格只描述如何完成替换。

替换前已确认事实(来自 brainstorming 阶段):

- 本地 manager 数据库现状:`apps`=2 行,`channel_bindings`=2 行,其中
  `bound_identity` 非空的 = **0 行**。**无微信绑定数据需要迁移**。
- 已实测 Hermes 镜像可 build 成功(2.65 GB),并验证 weixin platform 通过
  iLink Bot API 走扫码 → 拿 credential → gateway run → 连 new-api qwen3.5:27b
  → 收发消息的完整链路。
- Hermes 通过 `gateway/platforms/weixin.py:qr_login` 函数实现扫码,iLink
  返回完整 URL(`https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3`),
  绑定后身份格式 `<hex>@im.bot`(不是 wxid)。
- Hermes 没有 HTTP `/healthz` 端点,但提供 `hermes gateway status` CLI 子命令。
- 官方 `install.sh` 支持 `--skip-setup` 非交互安装,适合 Dockerfile `RUN` 层使用。

## 目标

- 用 Hermes 完成 OpenClaw 在 oc-manager 中承担的全部职责:容器化 agent
  runtime、微信渠道接入、模型 provider 注入、知识库提供、prompt/身份注入、
  健康检查。
- 替换实现方式遵循 **Hermes 原生范式**:不沿用 OpenClaw 时代的挂载/协议
  约定,改用 Hermes 自身的 `~/.hermes/` 目录结构、`skills` 机制、`SOUL.md`
  身份注入。
- 不保留 OpenClaw 代码或双 runtime 抽象层,直接 in-place 替换。
- 数据库语义变更(`channel_bindings.bound_identity` 从 wxid 改为 iLink bot
  id)不引入数据迁移负担(0 行 bound)。
- 替换工作按 AGENTS.md "提交按业务/功能边界拆"原则拆为 8 个有序 commit。

## 非目标

- 不保留双 runtime 并存或 feature flag 切换路径。
- 不为 master 中间提交保留"可发布"状态。每个 commit 内部必须一致可
  review;但提交 1-7 之间的中间态可能不能跑通完整功能。
- 不修改 oc-manager 项目本身的名字(项目名是 "OpenClaw Manager" 缩写)。
  这是后续单独议题。
- 不重构 `RuntimeAdapter` 接口整体形状,只对包含 "OpenClaw" 字样的方法名
  做精确 rename。
- 不变更前端 channel login 页的整体交互流程,只调整 QR 字段(URL 含义不变,
  bot 身份格式不同)。
- 不为 Hermes 配置 OpenRouter/Anthropic 等公网 provider 回退;auxiliary
  模型全部强制 `provider: main`(走本地 new-api)。

## 架构对比

| 维度 | OpenClaw 时代 | Hermes 时代(本设计) |
|---|---|---|
| 镜像构建 | `runtime/openclaw/Dockerfile`,上游 `install.sh --no-onboard --no-prompt` | `runtime/hermes/Dockerfile`,上游 `install.sh --skip-setup`,再 `pip install aiohttp cryptography qrcode` 预装 weixin 平台依赖 |
| 启动命令 | `openclaw gateway --allow-unconfigured --bind lan` | `hermes gateway run` |
| 监听端口 | 18789 HTTP(Control UI + /healthz) | 无,出站长轮询 iLink API |
| 健康检查 | curl `http://127.0.0.1:18789/healthz` | Dockerfile `HEALTHCHECK` 跑 `hermes gateway status`;`app_health_check.go` 读 `docker inspect .State.Health.Status` |
| 微信登录 | `docker exec ... openclaw channels login --channel openclaw-weixin --verbose`,解析中文 stdout 文本 | `docker exec ... oc-weixin-login`(我们 COPY 进镜像的 Python 脚本),输出单行 JSON 凭证 |
| Prompt 注入 | `openclaw config patch --stdin` 写 openclaw.json | manager 在 app_initialize 时把 `prompt.go` 渲染的字符串写到挂载目录的 `~/.hermes/SOUL.md` |
| 模型 provider | OpenClaw 插件 + openclaw.json `models.providers` 段 | `~/.hermes/config.yaml` 的 `model.{provider:custom,base_url,api_key}` + `.env` 的 `OPENAI_API_KEY/OPENAI_BASE_URL` 作 fallback |
| 知识库 | `/knowledge/org`、`/knowledge/app` ro 双挂载 + system prompt 提示 agent 去读 | 每个知识库文档生成一个 Hermes skill (`~/.hermes/skills/kb-<scope>-<slug>/SKILL.md`) |
| 工作目录 | `/workspace` rw 挂载 | `~/.hermes/workspace/`(Hermes 自动管理) |
| 状态 | `/state` rw 挂载 | `~/.hermes/sessions/`、`~/.hermes/cron/`(Hermes 自动管理) |
| 日志 | `/logs` rw 挂载 | `~/.hermes/logs/`(Hermes 自动管理) |
| 容器挂载点数 | 5 个独立挂载 | 1 个顶层挂载 `apps/<app_id>/.hermes:/opt/data` |
| 微信身份 | `<wxid>@im.wechat`(OpenClaw plugin state) | `<hex>@im.bot`(iLink Bot id) |

## 详细设计

### 镜像构建(`runtime/hermes/`)

目录结构:

```
runtime/hermes/
├── Dockerfile
├── CONTRACT.md          # manager 与 Hermes 上游集成的关键约定
├── version.txt          # 锁定 hermes-agent 版本 / commit
├── scripts/
│   ├── oc-weixin-login.py  # docker exec 调用的扫码登录脚本
│   └── healthcheck.sh       # Dockerfile HEALTHCHECK 用
```

Dockerfile 骨架(以下为设计草图,实施时根据上游 install.sh 实际行为微调):

```dockerfile
FROM python:3.13-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    HERMES_HOME=/opt/data \
    PATH=/root/.local/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git ripgrep ffmpeg tini && \
    rm -rf /var/lib/apt/lists/*

# 上游 install.sh 装 hermes-agent 主体 + 依赖(uv 自动拉 Python 3.11+)
RUN curl -fsSL https://hermes-agent.nousresearch.com/install.sh \
    | bash -s -- --skip-setup

# 显式预装 weixin platform 必需的依赖,容器启动后不再走 lazy_deps.py
RUN /root/.hermes/hermes-agent/venv/bin/pip install --no-cache-dir \
      aiohttp cryptography qrcode

# 我们自己的扫码登录脚本
COPY scripts/oc-weixin-login.py /usr/local/bin/oc-weixin-login
COPY scripts/healthcheck.sh /usr/local/bin/oc-healthcheck
RUN chmod +x /usr/local/bin/oc-weixin-login /usr/local/bin/oc-healthcheck

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "hermes"]
CMD ["gateway", "run"]
```

约束:

- 镜像 build 完后,容器**启动即可用**,**不允许运行时再装任何依赖**。
- 镜像大小预期 ~2.5-3 GB,跟实测官方 Dockerfile 镜像规模相当。
- 上游 hermes-agent 升级时,只需要更新 `version.txt`(对应 install.sh 的
  `--branch` 或 `--ref` 参数)+ rebuild,Dockerfile 本身基本不动。

### 容器生命周期

沿用 OpenClaw 时代的"每 app 一个容器"模型,不变。

- manager-api 处理 app 创建请求时,生成宿主机目录
  `apps/<app_id>/.hermes/` 并写入 `config.yaml`、`.env`、`SOUL.md`、
  `skills/` 等初始内容。
- worker handler `app_initialize` 跑:
  - `docker run -d --name hermes-<app_id> --restart unless-stopped \
       --network <project network> \
       -v apps/<app_id>/.hermes:/opt/data \
       -e HERMES_UID=$(id -u) -e HERMES_GID=$(id -g) \
       <hermes image tag>`
- 容器内 entrypoint 直接 `hermes gateway run`,无外部初始化步骤。

### 微信扫码登录

`runtime/hermes/scripts/oc-weixin-login.py`:

```python
#!/usr/bin/env python3
"""docker exec 调用的微信扫码登录入口。

stdout: 单行 JSON, 含 account_id/token/base_url/user_id 等凭证字段
stderr: 二维码 URL(供 manager 流式转发给前端展示)
exit 0: 登录成功
exit 2: 登录失败或超时
"""
import asyncio, json, sys
from gateway.platforms.weixin import qr_login

async def main() -> int:
    cred = await qr_login("/opt/data", bot_type="3", timeout_seconds=480)
    if not cred:
        print("LOGIN_FAILED_OR_TIMEOUT", file=sys.stderr)
        return 2
    json.dump(cred, sys.stdout)
    sys.stdout.write("\n")
    return 0

if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
```

注意:`qr_login` 内部已经会 `print` 二维码 URL 到 stdout。我们的脚本要
把 URL 改向 stderr(或者在 `qr_login` 前 hook stdout),保证 stdout 只有
最终 JSON,避免污染 manager 端解析。具体实现细节在 implementation plan
阶段确定。

`internal/integrations/channel/wechat_runner.go` 重写:

- 调用 `docker exec hermes-<app_id> oc-weixin-login`(用 docker SDK,而
  非 shell)。
- 通过 stdcopy 分离 stdout / stderr 两路:
  - stderr: 行级 stream,匹配二维码 URL 行 → 转 `qrcode` event 推给前端
  - stdout: 累积到结束,反序列化 JSON → 拿 `account_id` 等
- 容器 exit code 0 + 拿到 JSON = 登录成功 → 调 service 层:
  - 把 `account_id` 等写到 `apps/<app_id>/.hermes/.env`(append
    `WEIXIN_ACCOUNT_ID=` 等四行)
  - 更新 `channel_bindings.bound_identity = account_id`,
    `status = bound`
  - 触发容器重启(让 gateway run 重新加载 .env 中的 weixin 凭证)
- exit code != 0 → 报错给前端

### 模型 provider 注入(`config.yaml` + `.env`)

manager 在 app_initialize 时生成 `apps/<app_id>/.hermes/config.yaml`:

```yaml
model:
  default: "<app.model_name>"           # 来自 apps 表
  provider: "custom"
  base_url: "<new-api 内网 URL>/v1"
  api_key: "<token from new-api>"

# 所有 auxiliary 走 main 不走 OpenRouter,避免外网依赖
auxiliary:
  vision:        { provider: main }
  compression:   { provider: main }
  web_extract:   { provider: main }
  session_search:{ provider: main }

memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 2200
  user_char_limit: 1375

terminal:
  backend: local
  cwd: "."
```

同步生成 `apps/<app_id>/.hermes/.env`:

```
OPENAI_API_KEY=<token from new-api>
OPENAI_BASE_URL=<new-api 内网 URL>/v1
```

`.env` 的 OPENAI_* 作为 auxiliary `provider: main` 的兜底凭据。

注:旧 `internal/integrations/openclaw/prompt.go` 的逻辑产物里
"将模型配置写入 openclaw.json" 这部分彻底删除,不再走 `openclaw config patch`。

### 知识库 → Hermes Skills

每个组织级 / 应用级知识库文档,在容器初始化阶段被 manager 生成为一个
Hermes skill 目录:

```
apps/<app_id>/.hermes/skills/
├── kb-org-<doc-slug>/
│   ├── SKILL.md          # frontmatter (name/description/triggers) + 正文
│   └── (可选) 附件文件
├── kb-app-<doc-slug>/
│   ├── SKILL.md
│   └── ...
```

`SKILL.md` frontmatter 包含 `description` 字段供 Hermes agent 列出可用
skill。manager 生成时把每个知识库文档的标题/摘要写到 description,正文
写到 SKILL.md 主体。

知识库更新流程:

- manager 改宿主机 skill 文件(直接写文件,不走 docker exec)
- 触发容器重启 → Hermes entrypoint 跑 `skills_sync.py`(上游
  `~/.hermes/skills_sync` 已有逻辑)→ 装载新 skill

不实现"运行时热更新"——重启即可。

### Prompt / 身份注入(`SOUL.md`)

`internal/integrations/hermes/prompt.go`:

- 包路径 `internal/integrations/openclaw/` → `internal/integrations/hermes/`,
  函数名 `Render(input PromptInput) string` 不变(向后兼容签名)。
- 渲染输出:Hermes `SOUL.md` 格式 = 自由文本 markdown,描述 agent 身份、
  目标、约束、可用工具。OpenClaw 时代是 JSON config patch 的一个字符串
  字段,内容大致一致,**只调整输出格式 + 接收容器写入方式**。

manager 在 app_initialize 时把渲染结果写到
`apps/<app_id>/.hermes/SOUL.md`。Hermes 启动时自动注入 SOUL.md 内容到
system prompt(上游已有行为)。

模型相关配置不写 SOUL.md(放 config.yaml,见上节)。

### 健康检查

- Dockerfile `HEALTHCHECK CMD ["/usr/local/bin/oc-healthcheck"]`
- `oc-healthcheck` 脚本:`exec hermes gateway status`,退出码 0 =
  healthy,非 0 = unhealthy。
- worker handler `app_health_check.go` 改成读
  `docker inspect <container> --format '{{.State.Health.Status}}'`,
  期待值 `healthy`(初始 60s 内为 `starting`)。
- 完全去掉 OpenClaw 时代的 `/healthz` HTTP 调用代码、容器端口暴露和
  curl 依赖。

### 数据库 schema 变更

- `apps` 表:**无新增列**。完全替换路径不需要 `runtime_kind` 区分字段。
- `channel_bindings.bound_identity` 字段:**类型不变(TEXT)**。语义变化:
  - OpenClaw 时代:`<wxid>@im.wechat`(OpenClaw plugin 写入的 userId)
  - Hermes 时代:`<hex>@im.bot`(iLink Bot 身份)
  - 由于当前 0 行 bound,**无数据迁移需要**。只通过新 migration 更新
    `COMMENT ON COLUMN` 注释:

  ```sql
  -- internal/store/migrations/2026XXXX_runtime_to_hermes.sql
  COMMENT ON COLUMN channel_bindings.bound_identity IS
    '微信渠道 iLink Bot 身份(格式 <hex>@im.bot,Hermes runtime 时代);'
    '历史:OpenClaw runtime 时代为 wxid。';
  ```

- sqlc 重新生成,sqlc/models.go 上的 Go 注释相应更新。

### 命名 / 目录 重组清单

按 AGENTS.md L4 命名层级要求,**所有含 "OpenClaw" 字样、且新语境下不
适用的命名**必须改:

| 旧 | 新 |
|---|---|
| `runtime/openclaw/` (目录) | `runtime/hermes/` |
| `internal/integrations/openclaw/` (包) | `internal/integrations/hermes/` |
| `OpenClawConfig` struct (`internal/config/config.go`) | `HermesConfig` |
| 应用配置 yaml 顶级 key `openclaw:` | `hermes:` |
| `RuntimeAdapter.SyncOpenClawImage()` | `SyncRuntimeImage()`(runtime 无关命名,未来仍可继续替换) |
| `internal/runtime/imagesync/` 中所有 OpenClaw 字样 | 改成 "runtime image" / "agent runtime" 中性表述 |
| `openapi.yaml` `info.title` 中的 "OpenClaw Manager" 字样 | "Agent Runtime Manager"(中性,不绑 Hermes) |
| `docs/openclaw-manager-design.md` | `docs/agent-runtime-manager-design.md` |
| `docs/openclaw-manager-technical-design.md` | `docs/agent-runtime-manager-technical-design.md` |
| `README.md`、`AGENTS.md`、`CLAUDE.md` 中 "OpenClaw" 字样 | 按上下文改成 "Hermes" 或 "agent runtime" |

**项目名 `oc-manager`(原本为 OpenClaw Manager 缩写)不在本规格范围内**,
作为后续单独议题。`oc-` 前缀的可执行文件名(如 `oc-weixin-login`)保留,
取其"oc-manager 项目自身脚本"含义,不再特指 OpenClaw。

## 提交边界(Commit Plan)

按 AGENTS.md "提交必须按业务/功能边界拆分"原则,本次替换分为
**8 个有序 commit**。每个 commit 内部一致可 review;提交 1-7 中间态
manager 整体可能不能完整跑通,但接受这种代价以换取 review 友好。

1. **`feat(runtime): 新增 hermes runtime 镜像构建`**
   - `runtime/hermes/` 全套(Dockerfile / CONTRACT.md / version.txt /
     scripts/),孤立提交,不接到 manager 业务代码。
   - 提交后 `docker build` 应能成功产出 `hermes-runtime:dev` 镜像。

2. **`feat(integrations): 新增 hermes 集成层(纯新增,业务路径未切)`**
   - 新增 `internal/integrations/hermes/` 包:`prompt.go`(渲染
     SOUL.md)、`config.go`(渲染 config.yaml / .env)、`skills.go`
     (生成 kb-* skill 目录)、`wechat_runner.go`(`docker exec ...
     oc-weixin-login` 调用 + stdcopy 分流 stdout(JSON) / stderr(QR
     URL))。
   - 现有 `internal/integrations/channel/wechat_runner.go`(旧
     OpenClaw stdout 文本协议解析)和 `internal/integrations/openclaw/`
     全部**不动**,业务路径仍走老实现。
   - 此 commit 后 hermes 集成层代码完整、有单测覆盖,但 manager
     运行时还是 OpenClaw。

3. **`feat(worker): 切换 worker handler + channel runner 到 hermes`**
   - 改 worker handler:`app_initialize.go`、`app_health_check.go`、
     `channel_login.go`、`runtime_refresh_status.go` 改 import +
     改调用,目标改成 `internal/integrations/hermes/`。
   - 同步改 `internal/integrations/channel/wechat_runner.go`:或
     重写其内部直接调 hermes 包,或在 channel 包内引入瘦适配把
     调用委托给 hermes 包(具体形态由实施阶段定,目标是 channel
     抽象层指向 Hermes)。
   - 此 commit 后 manager 业务逻辑已经实际依赖 Hermes;但
     `internal/integrations/openclaw/` 还在文件系统里(等 commit 6
     删)。

4. **`refactor(config): OpenClawConfig 改名为 HermesConfig`**
   - `internal/config/config.go` 重命名 struct + 字段。
   - 应用配置 yaml 顶级 key `openclaw:` → `hermes:`,所有
     `internal/config` 测试同步,启动时不再支持老 key(完全替换)。

5. **`refactor(adapter): SyncOpenClawImage → SyncRuntimeImage`**
   - `internal/integrations/runtime/agent_backed.go` 接口方法改名。
   - `internal/runtime/imagesync/service.go` 内部命名跟随。
   - 调用方一并更新。

6. **`chore(cleanup): 删除 internal/integrations/openclaw/ 和 runtime/openclaw/`**
   - 删除两个目录的全部代码 + 关联测试 + fixture。
   - 此 commit 后 OpenClaw 在仓库中无任何痕迹。

7. **`chore(db): channel_bindings.bound_identity 语义注释更新`**
   - 新增 migration 改 column COMMENT。
   - sqlc generate,sqlc/models.go Go 注释跟随。
   - 数据 0 行迁移,migration 只动 schema 注释。

8. **`docs+openapi: 同步 API 契约和文档`**
   - 跑 `make openapi-gen` + `make web-types-gen`,提交差异。
   - 重命名 `docs/openclaw-manager-*.md`,改内文。
   - 更新 `README.md` / `AGENTS.md` / `CLAUDE.md` 中的 OpenClaw
     表述。

## 测试策略

### 单元测试

- `internal/integrations/hermes/prompt_test.go`:覆盖
  `Render(PromptInput)` 多种输入下的 SOUL.md 输出
- `internal/integrations/hermes/config_test.go`:覆盖 config.yaml /
  .env 渲染、表/对象/字段完整性
- `internal/integrations/hermes/skills_test.go`:覆盖知识库 → skill
  目录生成、SKILL.md frontmatter 正确性、目录命名规则
- `internal/integrations/channel/wechat_runner_test.go`:用伪造 docker
  exec stdout/stderr 流验证 JSON 解析、二维码 URL 提取、错误处理
- `internal/worker/handlers/*_test.go`:更新现有用例,模拟
  hermes 集成层调用

### 集成测试

- 本地 docker-compose:起一个 hermes runtime container,跑
  `channel_login` 流程,人工扫码 → 等待 bound → 验证 `bound_identity`
  落 db 是 `<hex>@im.bot` 格式
- 完整对话验证:通过微信向 bot 发消息 → manager 容器内 hermes
  调 new-api qwen3.5:27b → 回复送达微信(已在 brainstorming 阶段实测)

### 验证清单(交付前 manual)

按 AGENTS.md "新功能开发完成后必须调用浏览器进行全面功能验证":

- 平台管理员登录,创建组织,组织内创建 app → 容器启动 healthy
- 在 app 内创建微信渠道,触发扫码登录 → 拿到二维码 URL → 手机微信
  扫码 → manager 显示绑定成功 → channel_bindings.bound_identity
  落 `<hex>@im.bot`
- 给 bot 发消息 → 收到 qwen 回复
- 修改 app 模型(参考 `2026-05-13-app-model-governance-design.md`)→
  容器重启 → 用新模型继续对话验证
- 知识库内容验证:上传知识库文档 → 触发容器初始化 → docker exec
  容器内确认 `~/.hermes/skills/kb-*/` 目录生成 → 对话中 agent 能
  load 对应 skill

## 回滚

- 因为完全替换、无双 runtime,回滚 = `git revert <commit 8..1>`
  反向 revert。
- 回滚后 OpenClaw 代码恢复,但 Hermes 时代任何扫码绑定的
  `bound_identity` 在 OpenClaw 下不可用(语义错位)。
  - 现有 0 行 bound 数据,无破坏。
  - 如果在 master Hermes 上线后有用户扫码登录,然后回滚 → 需要
    `UPDATE channel_bindings SET bound_identity = NULL, status =
    'unbound'` 让用户重扫(OpenClaw 协议下重新登录)。回滚操作清单
    在实施计划阶段固化。

## 已知限制

- **模型 latency**:在 brainstorming 实测中,公网 qwen 渠道(`36.133.243.164`)
  端到端 latency ~30s。这是渠道本身的延迟,跟 runtime 无关;切换 Hermes
  不改善。
- **首次 image build 慢**:Hermes 镜像 ~10-30 分钟首次 build(install.sh
  内部装 uv + Playwright + uv sync 全套),CI/本地都受影响。设计上不优化,
  原因:install.sh 黑盒不便干预;镜像 build 通常 cache 命中后秒级。
- **微信好友身份变更**:用户原本加的 OpenClaw bot 好友在 Hermes 时代
  不可用;每个微信渠道用户必须加新的 iLink bot(`<hex>@im.bot`)。
  这是用户感知最强的影响,需要在产品/帮助文档中说明。
- **iLink 服务依赖**:Hermes 微信走 iLink Bot,是第三方服务
  (`ilink.weixin.qq.com`,腾讯下游服务)。可用性、合规性、长期稳定性
  由 iLink 决定,manager 无法控制。
- **Skill 数量上限**:Hermes 默认带 87 个 bundled skill;再加上知识库
  转化的 kb-* skill,可能影响启动加载时间与 system prompt 大小。建议
  在实施阶段验证大组织知识库(50+ 文档)场景下的启动时间和 token
  消耗。

## 后续议题(本规格范围外)

- 项目名 `oc-manager` 改名议题
- Hermes 多平台扩展:Telegram / Discord / Slack 等(本设计只保留微信,
  这些是 future work)
- Hermes 的 dashboard / API server 是否在 oc-manager 中暴露使用
- 上游 Hermes 重大版本升级的兼容性策略

## Phase 9 修订:多节点架构

(本节为 Phase 1-8 实施后补充。)

Phase 3 实施时把 SOUL.md / config.yaml / .env / skills 用 `os.WriteFile`
写到 manager 本机 DataDir,然后 bind mount 到容器 `/opt/data`。这隐式假设
manager 进程与 Docker daemon 同机,多节点部署会 broken。

Phase 9 修订:

- manager 不再 `os.WriteFile` 到本机;改通过 `RuntimeAdapter.UploadAppRuntimeFile`
  把每个文件 PUT 到目标节点 `runtime-agent` 的
  `/v1/scopes/apps/<appID>/runtime/file?path=<relPath>` endpoint,agent 写到节点
  本地 `<dataRoot>/apps/<appID>/.hermes/<relPath>`。
- 容器 `Mounts.HostPath` 改为 `<nodeDataRoot>/apps/<appID>/.hermes`(节点本地路径),
  不再用 manager 本机 DataDir。
- `AppInitializeConfig.DataDir` 字段在 Hermes 文件分发路径上不再使用。
- `runtime/agent/scopes.go` 的 sandbox 路径从 `openclaw-config` 重命名为
  `.hermes`,handleAppInit 子目录列表从 6 个精简为 2 个(`.hermes` + `knowledge`)。
- `AppRuntimeFileWriter` 接口从"向后兼容备用"升级为 Hermes 文件分发的
  唯一路径,nil 注入时直接报错。

至此多节点部署可行:manager 与 runtime-agent + Docker daemon 可在不同节点。
