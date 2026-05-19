# Hermes 镜像自包含初始化 · 设计 Spec

> 日期：2026-05-19
> 范围：把 hermes 容器的「输入翻译 + 数据迁移」职责从 manager 迁移到镜像内部，
> 让 manager 只写中性输入数据；镜像内启动脚本负责渲染本镜像版本所需的 hermes
> 文件，并平滑迁移用户长期累积的运行时数据（memories、渠道凭证等）。

## 1 · 背景

当前架构下 manager 是「hermes 配置 schema 渲染器」：

- `internal/integrations/hermes/{config,prompt,skills}.go` 渲染 `config.yaml`、
  `SOUL.md`、`.env`、`skills/kb-*/SKILL.md`，并通过节点 agent file API PUT 到
  挂载点 `apps/<id>/.hermes/`，hermes 容器启动后直接读这些文件。
- `cmd/server/wiring.go` 的 `hermesConfigRefresher` 在 restart 时再次跑同一套
  渲染逻辑；`ChannelCheckBindingHandler` 在扫码 bound 时重写整份 `.env`。
- 所有 app 共用 `manager.yaml hermes.runtime_image` 全局 default 锁定的同一份
  镜像（commit 由 `runtime/hermes/version.txt` 锁定）。

这套架构的核心问题：

1. **manager 与 hermes 内部 schema 紧耦合**：hermes 上游升级（config.yaml
   schema 变化、SKILL.md frontmatter 变化）都要 manager 同步改代码；
2. **镜像版本不能 per-app 差异化**：手工把不同 app 切到不同镜像在当前架构里
   要做大量代码适配；
3. **运行时数据迁移没有承载位置**：当 hermes 自身 schema 变化、`state.db` 或
   `memories/` 格式需要转换时，manager 端没合适的地方放迁移代码。

## 2 · 目标 / 非目标

### 2.1 目标

- manager 只写「业务意图」到一个独立、固定格式的输入目录；不再渲染任何 hermes
  内部 schema 文件。
- 镜像内置启动脚本 `oc-entrypoint` 负责：(a) 读 manager 写入的输入；(b) 渲染本
  镜像版本所需的 hermes 文件；(c) 在镜像版本切换时迁移用户数据；(d) `exec`
  hermes 进程。
- `runtime/hermes/` 目录支持多个 hermes variant 并存维护，每个 variant 完全
  自包含、可独立发版。
- manager 与镜像之间通过一套**稳定的命令清单**通信（绑定渠道、查询状态、诊断
  等），命令实现差异完全被镜像吞掉。
- 当前生产没有老 app，本地测试数据可清理 → 一次性切换，不保留兼容层。

### 2.2 非目标

- 不引入 manifest schema version 字段；manager 写入字段只增不删，靠字段存在性
  做 forward-compat。
- 不实现「平台管理员单 app 改镜像」UI；仅保证 `apps.runtime_image_ref` 既有
  字段语义正确，未来加 UI 不需要改数据层。
- 不改造 hermes 上游自身；本设计只触及 oc-manager 与镜像层。
- 不维护「老挂载布局 → 新挂载布局」的数据迁移；本地与生产都允许清空重建。

## 3 · 架构

### 3.1 三方职责

```text
┌──────────────────────────────────────────────────────────────────────────┐
│ manager                                                                   │
│  · 维护知识库主副本、persona、三层规则原文                                │
│  · 收集 OPENAI 凭证、模型名                                               │
│  · 写两类输入到节点 apps/<id>/input/：                                    │
│      manifest.yaml       固定字段、无 schema_version                       │
│      resources/...md     persona / 三层规则 / 知识库原文                  │
│  · 通过 docker exec 调用镜像提供的稳定命令完成「绑定/状态/诊断」          │
└──────────────────────────────────────────────────────────────────────────┘
                                       │ docker create + bind mounts
                                       ▼
┌──────────────────────────────────────────────────────────────────────────┐
│ hermes runtime 镜像 (per-variant)                                         │
│  ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint                     │
│  oc-entrypoint:                                                           │
│    ① 读 /opt/oc-input/manifest.yaml + resources/                          │
│    ② 必要时跑迁移 (基于 /opt/data/.oc-state.json)                         │
│    ③ 渲染本版本所需的 hermes 文件到 /opt/data/                            │
│    ④ exec hermes gateway run                                              │
│  对外命令: oc-info, oc-doctor, oc-channel-login, oc-channel-status,       │
│            oc-channel-unbind, oc-healthcheck                              │
└──────────────────────────────────────────────────────────────────────────┘
                                       │ rw / ro bind
                                       ▼
   /opt/oc-input (ro)  ← apps/<id>/input/   manager 唯一写入面
   /opt/data     (rw)  ← apps/<id>/data/    hermes runtime：state.db /
                                            sessions / memories /
                                            weixin/accounts / logs /
                                            skills 内置类目 / workspace
```

### 3.2 职责切分对照

| 角色 | 旧 | 新 |
|---|---|---|
| manager | 渲染所有 hermes 内部 schema 文件 | 只写业务意图的 manifest.yaml + 原始 markdown |
| 节点 agent | 通过 file API 接受 manager 写入 `.hermes/` | 路径前缀改为 `input/` 与 `data/`；只允许 manager 写 `input/`，`data/` 由 hermes 进程负责常规读写，agent 仅承担 manager 触发的 session 清理（`DELETE /sessions`）等少量受控操作 |
| runtime 镜像 | 透传 hermes + 少量辅助命令 | 内置 oc-entrypoint 负责输入翻译与迁移；hermes 进程由它 exec 拉起 |

## 4 · manager 写入面 · manifest 与 resources

### 4.1 节点目录结构

```text
<nodeDataRoot>/apps/<app_id>/
├── input/                              # 只读挂载到 /opt/oc-input
│   ├── manifest.yaml
│   └── resources/
│       ├── persona.md                  # 已替换占位符的「成品」markdown
│       ├── platform-rules.md
│       ├── organization-rules.md
│       ├── application-rules.md
│       └── knowledge/
│           ├── org/<rel_path>          # 组织级主副本树，原样镜像
│           └── app/<rel_path>          # 应用级主副本树，原样镜像
└── data/                               # 读写挂载到 /opt/data
    └── (hermes 运行时数据，含 .oc-state.json)
```

### 4.2 manifest.yaml 字段（manager 写）

```yaml
# manager 写入的「中性输入」清单。无 schema_version。
# 字段语义：未来新增字段，老 variant 自动忽略（字段存在性兼容）；
# 不允许字段语义变更或删除。
app:
  id:    "<app uuid>"          # 仅审计 / 日志
  name:  "<app name>"          # 仅日志
  model: "<model id>"          # 进 hermes config.yaml model.default

credentials:
  openai:
    api_key:  "sk-..."          # new-api 创建的 token (manager 解密后明文写入)
    base_url: "http://new-api:3000"   # 不带 /v1，由 variant renderer 自行拼

resources:
  persona:               "resources/persona.md"
  rules:
    platform:     "resources/platform-rules.md"
    organization: "resources/organization-rules.md"
    application:  "resources/application-rules.md"
# knowledge 不在 manifest 里：固定为 resources/knowledge/{org,app}/，
# 镜像 renderer 自行递归扫描。
# 微信凭证不在 manifest 里：由 hermes 在扫码后自管。
```

### 4.3 manager 端写入操作

| 触发场景 | manager 操作 |
|---|---|
| app 创建 | 一次写 manifest.yaml + resources/persona.md + 三份 rules.md + knowledge/ 全量主副本 |
| 改模型 | 重写 manifest.yaml（仅 `app.model` 变） + 触发容器 restart |
| 改 platform/org/app rules | 重写对应 `resources/*-rules.md` + 触发容器 restart |
| 改 persona | 重写 `resources/persona.md` + 触发容器 restart |
| 知识库增/删/改（`knowledge_sync_node` job） | 增量改 `resources/knowledge/{org,app}/<rel>` 单文件；不重写 manifest；触发 restart 由调用方决定 |
| 扫码 bound | manager **不写文件**；调 `oc-channel-login` 命令，由镜像内自管凭证；如该 variant 内部未实现自重启 hermes 进程，manager 再触发一次容器 restart |

### 4.4 占位符替换

`persona.md` 与三份 `*-rules.md` 在 manager 端写入前完成 `{org_name}` /
`{app_name}` / `{owner_name}` 占位符替换；镜像不知道占位符存在。

## 5 · 镜像工程目录

### 5.1 目录结构

```text
runtime/hermes/
├── README.md                       # 仓库级：怎么新增 variant、命名规范、发版
└── hermes-main/                    # 第一个 variant，对应当前 version.txt=main
    ├── Dockerfile
    ├── version.txt                 # 锁定 hermes 上游 git ref
    ├── CONTRACT.md
    ├── oc-entrypoint.py
    ├── oc-doctor.py
    ├── oc-info.py
    ├── oc-channel-login.py          # 入口分发；按 --channel 参数路由到 weixin/其他实现
    ├── oc-channel-status.py
    ├── oc-channel-unbind.py
    ├── healthcheck.sh
    ├── lib/                        # 本 variant 专用 Python 模块
    │   ├── manifest.py
    │   ├── state.py                # /opt/data/.oc-state.json 读写
    │   ├── atomic.py
    │   └── logging.py
    ├── renderer/
    │   ├── render_config_yaml.py
    │   ├── render_soul_md.py
    │   ├── render_env.py
    │   └── render_skills.py
    ├── migrator/
    │   └── from_<prev_variant>.py  # 上一个本仓库 variant → 本 variant 的迁移
    └── tests/                      # 镜像内 Python 单测
        ├── test_manifest.py
        ├── test_state.py
        ├── test_render_*.py
        └── test_migrator.py
```

**variant 命名风格：** `hermes-<upstream-ref>`。当前唯一 variant 是
`hermes-main`（对应 version.txt=main）。未来上游切到某个 release tag 时新
增 `hermes-v0.5/` 等。

**完全解耦：** 不存在跨 variant 共享目录。新建 variant 时整体复制上一个目录
后改名 + 改 version.txt + 改 Dockerfile 的 `OC_IMAGE_VARIANT` 构建参数。

### 5.2 Dockerfile 模板

```dockerfile
# runtime/hermes/<variant>/Dockerfile
ARG DOCKER_HUB_MIRROR=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public
FROM ${DOCKER_HUB_MIRROR}/python:3.13-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    HERMES_HOME=/opt/data \
    PATH=/root/.local/bin:$PATH

ARG DEBIAN_MIRROR_HOST=mirrors.aliyun.com
RUN set -eux; for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
      [ -f "$f" ] && sed -i \
        -e "s|deb.debian.org|${DEBIAN_MIRROR_HOST}|g" \
        -e "s|security.debian.org|${DEBIAN_MIRROR_HOST}|g" "$f" || true; \
    done

RUN apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git ripgrep ffmpeg tini xz-utils && \
    rm -rf /var/lib/apt/lists/*

ARG HERMES_REF
RUN curl -fsSL https://hermes-agent.nousresearch.com/install.sh \
      | bash -s -- --skip-setup --branch ${HERMES_REF}

RUN mv /opt/data/node /opt/hermes-node && \
    ln -sfn /opt/hermes-node/bin/node /root/.local/bin/node && \
    ln -sfn /opt/hermes-node/bin/npm  /root/.local/bin/npm && \
    ln -sfn /opt/hermes-node/bin/npx  /root/.local/bin/npx

RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      aiohttp cryptography qrcode

# oc-entrypoint Python 依赖
RUN uv pip install --system --no-cache-dir pyyaml

# 本目录内 COPY（build context = 本 variant 目录）
COPY oc-entrypoint.py     /usr/local/bin/oc-entrypoint
COPY oc-doctor.py         /usr/local/bin/oc-doctor
COPY oc-info.py           /usr/local/bin/oc-info
COPY oc-channel-login.py  /usr/local/bin/oc-channel-login
COPY oc-channel-status.py /usr/local/bin/oc-channel-status
COPY oc-channel-unbind.py /usr/local/bin/oc-channel-unbind
COPY healthcheck.sh       /usr/local/bin/oc-healthcheck
COPY lib/                 /usr/local/lib/oc-entrypoint/lib/
COPY renderer/            /usr/local/lib/oc-entrypoint/renderer/
COPY migrator/            /usr/local/lib/oc-entrypoint/migrator/

RUN chmod +x /usr/local/bin/oc-entrypoint /usr/local/bin/oc-doctor \
            /usr/local/bin/oc-info /usr/local/bin/oc-channel-login \
            /usr/local/bin/oc-channel-status /usr/local/bin/oc-channel-unbind \
            /usr/local/bin/oc-healthcheck

# 镜像身份信息写到固定位置，供 oc-info 读取
ARG OC_IMAGE_VARIANT
ARG OC_BUILD_TS
RUN printf '{"variant":"%s","hermes_upstream_ref":"%s","built_at":"%s"}\n' \
      "$OC_IMAGE_VARIANT" "$HERMES_REF" "$OC_BUILD_TS" \
      > /etc/oc-image.json

ENV OC_IMAGE_VARIANT=${OC_IMAGE_VARIANT}

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/oc-entrypoint"]
CMD []
```

### 5.3 Makefile

```makefile
HERMES_VARIANT       ?= hermes-main
HERMES_VARIANT_DIR   := runtime/hermes/$(HERMES_VARIANT)
HERMES_IMAGE_REPO    ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
HERMES_IMAGE         := $(HERMES_IMAGE_REPO):$(HERMES_VARIANT)-$(IMAGE_TIMESTAMP)

build-hermes-runtime: ## 本地 dev 构建 (tag: hermes-runtime:<variant>-dev)
	docker build \
	  -t hermes-runtime:$(HERMES_VARIANT)-dev \
	  --build-arg HERMES_REF=$$(cat $(HERMES_VARIANT_DIR)/version.txt) \
	  --build-arg OC_IMAGE_VARIANT=$(HERMES_VARIANT) \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR)

build-hermes-image: ## 本地生产镜像构建
	docker build \
	  -t $(HERMES_IMAGE) \
	  --build-arg HERMES_REF=$$(cat $(HERMES_VARIANT_DIR)/version.txt) \
	  --build-arg OC_IMAGE_VARIANT=$(HERMES_VARIANT) \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR)

push-hermes-image: ## 推送已构建的生产镜像到 ACR
	docker push $(HERMES_IMAGE)

release-hermes-image: build-hermes-image push-hermes-image ## 一步构建 + 推送（日常发版入口）
	@echo "✅ hermes 镜像 $(HERMES_IMAGE) 已构建并推送"

verify-hermes-runtime: ## 跑 verify-hermes-runtime.sh 校验镜像（含 oc-entrypoint Python 单测）
	./scripts/verify-hermes-runtime.sh $(HERMES_VARIANT)
```

## 6 · oc-entrypoint 启动流程

容器 PID 1 为 tini，oc-entrypoint 是 PID 2，完成翻译 + 迁移后 `exec` 替换自己
为 hermes 进程。hermes 仍是面向 tini 的 PID 2，信号传递、HEALTHCHECK 不变。

### 6.1 阶段

| phase | 行为 | 失败处理 |
|---|---|---|
| 1 load manifest | 读 `/opt/oc-input/manifest.yaml`，校验必填字段 (`app.model`、`credentials.openai.api_key`、`credentials.openai.base_url`) | 退出码 1 |
| 2 load state | 读 `/opt/data/.oc-state.json` 拿 `prev_variant`；不存在视为首次启动 | 文件损坏视为「未知」当首次启动 |
| 3 migrate | `prev_variant == nil` → skip；`prev_variant == curr_variant` → skip；否则跑 `migrator/from_<prev_variant>.py`，逐项迁移 memories、state.db 等。Migrator 内部约定：临时目录写 + rename，失败保留 `/opt/data` 原样 | 退出码 1 |
| 4 render | 调 renderer 模块全部跑一遍。**每次启动都跑**，幂等；atomic write（临时文件 + rename） | 退出码 1 |
| 5 commit state | 写 `.oc-state.json`：`image_variant` / `last_render_at` / `last_migrate_from` / `manifest_sha256` | 失败但前面成功 → 仍 exec hermes，下次启动按首次处理 |
| 6 exec hermes | `os.execvp("hermes", ["hermes","gateway","run"])` | 不到此 phase 不退出 |

### 6.2 渲染产物对照

| 文件 | renderer 模块 | 数据来源 |
|---|---|---|
| `/opt/data/config.yaml` | render_config_yaml | manifest.app.model + manifest.credentials.openai + 本 variant 约定的 auxiliary/memory/terminal 默认值 |
| `/opt/data/SOUL.md` | render_soul_md | resources/persona.md + 三层 rules + resources/knowledge/ inline (8 KiB 截断) |
| `/opt/data/.env` | render_env | 本 variant 约定的「行为开关」：`GATEWAY_ALLOW_ALL_USERS=true`、`WEIXIN_DM_POLICY=open`。**不含 `OPENAI_*`**（这些在 config.yaml 里） |
| `/opt/data/skills/kb-<scope>-<slug>/SKILL.md` | render_skills | 扫 `resources/knowledge/{org,app}/` 全部 markdown，按本 variant slug 算法生成 |

### 6.3 .oc-state.json schema（镜像私有，manager 不读不写）

```json
{
  "image_variant": "hermes-main",
  "last_render_at": "2026-05-19T10:23:01Z",
  "last_migrate_from": null,
  "manifest_sha256": "ab12cd...",
  "renderer_outputs": ["config.yaml", "SOUL.md", ".env",
                      "skills/kb-org-policies-refund/SKILL.md"]
}
```

### 6.4 stderr 日志协议

每行一条 JSON：

```json
{"phase":"render","level":"error","msg":"render_skills failed","detail":{"file":"resources/knowledge/org/policies/refund.md","err":"file not found"}}
```

- stderr 承载所有中间事件与错误；
- stdout 不被 oc-entrypoint 写入，留给 hermes 进程；
- 退出码统一为 1（启动前任意失败），详细信息看 stderr。

## 7 · 镜像对外命令清单（manager ↔ 镜像稳定协议）

manager 通过 agent docker exec 反向代理调用以下命令。命令的输入参数、stdout
JSON schema、退出码语义跨 variant 稳定；命令实现细节由 variant 内部决定。

| 命令 | 用途 | 输入 | stdout (单行 JSON) | stderr | 退出码 |
|---|---|---|---|---|---|
| `oc-info` | 镜像身份与构建信息 | 无 | `{"variant":"hermes-main","hermes_upstream_ref":"<sha>","oc_entrypoint_version":"1","built_at":"..."}` | — | 0=成功；1=失败 |
| `oc-doctor` | 诊断快照 | 无 | `{"variant":"hermes-main","last_render_at":"...","manifest_sha256":"...","hermes_pid":1234,"hermes_status":"running","issues":[]}` | — | 0=健康；1=有 issue |
| `oc-channel-login` | 触发渠道绑定 | `--channel weixin` | `{"status":"bound"\|"failed"\|"timeout","reason":"..."}` | 中间事件 JSON 行（含二维码 URL） | 0=bound；1=其他 |
| `oc-channel-status` | 查询当前绑定状态 | `--channel weixin` | `{"channel":"weixin","bound":true,"account_id":"<hex>@im.bot","bound_at":"..."}` 或 `{"bound":false}` | — | 0=查询成功；1=失败 |
| `oc-channel-unbind` | 解绑渠道 | `--channel weixin` | `{"status":"unbound"}` | — | 0=成功；1=失败 |
| `oc-healthcheck` | docker HEALTHCHECK | 无 | —（不输出） | — | 0=healthy；非 0=unhealthy |

**协议约定：**

1. stdout 永远是「最终结构化结果」，单行 JSON；
2. stderr 是「中间事件 / 进度」，每行一条 JSON；
3. 退出码只用 0/1；
4. 每个 variant 的 Dockerfile 必须装齐这套命令，新增 variant 时 README 自检
   清单包含「全部命令是否就位」一项。

**`oc-channel-login` 内部约定（以微信为例）：**

1. hermes 上游 SDK 启动扫码长轮询；
2. stderr 输出 `{"event":"qrcode","url":"..."}`；
3. 扫码成功 → 镜像内部把 token 写入 hermes 自有路径（如
   `/opt/data/weixin/accounts/`），并调用 hermes 进程的 reload 钩子或自重启；
4. stdout 一行 `{"status":"bound"}`，exit 0。

**manager 端封装：** `internal/integrations/hermes/commands.go` 集中实现一组
Go 函数 `RunChannelLogin / RunChannelStatus / RunChannelUnbind / RunDoctor /
RunInfo`；统一通过 agent docker exec 反向代理触发。调用方（worker handler、
service）只调这层函数，不再直接构造命令字符串。

## 8 · manager 端代码改造清单

### 8.1 退役（直接删除）

| 文件 / 函数 | 退役理由 |
|---|---|
| `internal/integrations/hermes/config.go`（`RenderConfigYAML`、`RenderEnv`、`ConfigInput`、`EnvInput`） | hermes 内部 schema 不再由 manager 渲染 |
| `internal/integrations/hermes/skills.go`（`RenderKnowledgeSkill`、`BuildKnowledgeSummary`、`SlugifyKnowledgePath`） | slug 算法、SKILL.md 拼装全部下沉到镜像 renderer |
| `internal/worker/handlers/app_initialize.go` 的 `writeHermesFiles`、`writeSkillsFromKnowledge`、`uploadKnowledgeSkills`、`collectKnowledgeForSoul` | 入口替换为 `WriteAppInput` |
| `cmd/server/wiring.go` 的 `hermesConfigRefresher` 及其注入点 | restart 时不再 refresh，由镜像每次启动重建 |
| `ChannelCheckBindingHandler.SetRuntimeFileWriter` / `SetCipher` / `.env` 全量重写逻辑（line 228-256） | 微信凭证由 hermes 自管，manager 不写 |

### 8.2 改造（保留语义、改实现）

| 文件 / 函数 | 改造内容 |
|---|---|
| `internal/integrations/hermes/prompt.go` | 保留占位符替换；只暴露 `RenderPersonaText` / `RenderRuleText`（输入 prompt + 变量字典，输出已替换占位符的纯 markdown）；不再拼合 SOUL.md |
| `app_initialize.go writeHermesFiles → WriteAppInput` | 渲染 persona/三份 rules.md + manifest.yaml，通过新 file API PUT 到 `input/` |
| `internal/worker/handlers/knowledge_sync.go` | 路径前缀从 `apps/<id>/knowledge/` 改为 `apps/<id>/input/resources/knowledge/{org,app}/` |
| `AppRestartContainerHandler` (`app_runtime_ops.go`) | 删 `RefreshConfigYAML` 调用；restart 流程精简为 `stop → clear sessions → start`，clear sessions 保留 |
| `internal/integrations/agent/file_client.go` | 新增 `UploadAppInputFile(node, app, relPath, body)`；老的 `UploadAppRuntimeFile` 退役 |
| `ChannelCheckBindingHandler` | 去掉 `.env` 写入逻辑；扫码成功后只：(1) 更新 `channel_bindings` 表状态、(2) 触发 RestartContainer（如有需要） |

### 8.3 新增

| 文件 | 用途 |
|---|---|
| `internal/integrations/hermes/manifest.go` | `Manifest` 结构 + `MarshalManifestYAML(m Manifest) ([]byte, error)`，YAML 字段顺序稳定 |
| `internal/integrations/hermes/app_input.go` | `WriteAppInput(ctx, w AppInputWriter, in AppInputData) error`：一次写完 manifest.yaml + 全部 resources/*.md + knowledge 主副本 |
| `internal/integrations/hermes/commands.go` | manager 端 docker exec 命令封装：`RunChannelLogin / RunChannelStatus / RunChannelUnbind / RunDoctor / RunInfo` |
| `internal/integrations/hermes/manifest_test.go` | 单测：manifest YAML 字段顺序稳定、YAML decode 回原结构等价 |
| `internal/integrations/hermes/app_input_test.go` | 单测：写入路径列表与文件内容、占位符替换、写失败错误透传 |
| `internal/integrations/hermes/commands_test.go` | 单测：命令封装的 docker exec 调用形态、stdout JSON 解析 |

### 8.4 节点 agent 改动

`runtime/agent/scopes.go`：

- `handleAppInit` MkdirAll 路径变更：
  - 旧：`apps/<id>/.hermes/`、`apps/<id>/.hermes/workspace/`、`apps/<id>/knowledge/`
  - 新：`apps/<id>/input/resources/knowledge/org/`、`apps/<id>/input/resources/knowledge/app/`、`apps/<id>/data/`、`apps/<id>/data/workspace/`
- 新增路由 `POST /v1/scopes/apps/<id>/input/file?path=<rel>`：相对路径限制在
  `input/` sandbox 内；其它语义沿用现 runtime/file 路由。
- 老路由 `POST /v1/scopes/apps/<id>/runtime/file` 删除。
- `DELETE /v1/scopes/apps/<id>/sessions`：路径不变，作用目标改为
  `apps/<id>/data/sessions/`、`apps/<id>/data/state.db*`。
- workspace 浏览 / 下载 / 打包 API：宿主机路径前缀改为 `apps/<id>/data/workspace/`，
  对外 API 形态不变。

### 8.5 docker create 改造

`internal/integrations/runtime/agent_backed.go CreateContainer`：

```go
// 旧：单挂载
// {HostPath: nodeDataRoot/apps/<id>/.hermes, ContainerPath: /opt/data, Mode: "rw"}

// 新：两条挂载
{HostPath: nodeDataRoot/apps/<id>/input, ContainerPath: "/opt/oc-input", Mode: "ro"},
{HostPath: nodeDataRoot/apps/<id>/data,  ContainerPath: "/opt/data",     Mode: "rw"},
```

- `WorkingDir` 保持 `/opt/data/workspace`；
- `Networks` 不变；
- `Env` **不再注入** `OPENAI_API_KEY / OPENAI_BASE_URL` 等业务变量。业务配置
  统一走 manifest.yaml → oc-entrypoint 渲染 config.yaml；`Env` 只保留
  docker 平台级变量（当前可清空）。

## 9 · 流程对齐对照

| 操作 | 旧链路 | 新链路 |
|---|---|---|
| app 创建 | `writeHermesFiles` 写 4 + N 个 hermes 文件 → CreateContainer → StartContainer | `WriteAppInput` 写 manifest.yaml + 4 个 markdown + N 个知识库文件 → CreateContainer（双挂载）→ StartContainer（oc-entrypoint 自动渲染 + exec hermes） |
| 改模型 | `RefreshConfigYAML` 重写 config.yaml/SOUL.md/skills/* → restart | manager 重写 `input/manifest.yaml` → restart → oc-entrypoint 重新渲染 |
| 改三层 prompt | `RefreshConfigYAML` 重渲染 SOUL.md → restart | manager 重写对应 `input/resources/*-rules.md` → restart → oc-entrypoint 重渲染 |
| 改 persona | `RefreshConfigYAML` 重渲染 SOUL.md → restart | manager 重写 `input/resources/persona.md` → restart → oc-entrypoint 重渲染 |
| 知识库增/删/改 | `knowledge_sync_node` 写 legacy `apps/<id>/knowledge/` | `knowledge_sync_node` 写 `apps/<id>/input/resources/knowledge/{org,app}/` |
| 扫码 bound | `oc-weixin-login` 返回 token JSON → manager `RenderEnv` → 重写 .env → restart | `oc-channel-login` 自存凭证 + 自重启 hermes；manager 仅读 stdout 更新 channel_bindings |
| 查渠道绑定状态 | manager 查 `channel_bindings` 表 | manager 查表 + 必要时调 `oc-channel-status` 复核 |
| hermes 镜像升级 | manager 改全局 default + 下次 recreate | 改 `manager.yaml` default 或 per-app `apps.runtime_image_ref`；下次 restart 时 oc-entrypoint 自动迁移 |

## 10 · 测试策略

### 10.1 manager 端单测（Go）

| 测试目标 | 测试文件 | 关键场景 |
|---|---|---|
| manifest YAML 字段稳定性 | `internal/integrations/hermes/manifest_test.go` | model/openai/resources 字段顺序与值；YAML 解码回原结构等价 |
| persona / rules 占位符替换 | `internal/integrations/hermes/prompt_test.go` | `{org_name}` / `{app_name}` / `{owner_name}` 替换；未替换占位符报错 |
| WriteAppInput 完整路径 | `internal/integrations/hermes/app_input_test.go` | 给定 fixture → 验证写入路径列表与文件内容；写失败错误透传 |
| channel handler 去 .env 化 | `channel_login_test.go` | bound 时仅更新 `channel_bindings`、触发 RestartContainer、不调 file API |
| docker create 双挂载 | `app_initialize_test.go` 改造 | CreateContainer 调用参数包含两条挂载、Env 不含 OPENAI_* |
| knowledge_sync 路径变更 | `knowledge_sync_test.go` | 写路径前缀为 `input/resources/knowledge/{org,app}/` |
| commands docker exec 封装 | `commands_test.go` | exec 参数构造、stdout JSON 解析、退出码判定 |

### 10.2 镜像内 Python 单测

| 测试目标 | 测试文件 | 关键场景 |
|---|---|---|
| manifest 解析 | `tests/test_manifest.py` | 必填缺失退出码 1；未知字段忽略；YAML 解析失败退出码 1 |
| state 读写 | `tests/test_state.py` | 首次启动 prev_variant=nil；同 variant 不触发迁移；不同 variant 触发对应 migrator |
| renderer config_yaml | `tests/test_render_config.py` | manifest 字段精确映射到 hermes config.yaml |
| renderer soul_md | `tests/test_render_soul.py` | 三层 rules 顺序；persona 拼接；知识库 inline 8 KiB 截断；空层跳过 |
| renderer env | `tests/test_render_env.py` | GATEWAY_ALLOW_ALL_USERS / WEIXIN_DM_POLICY 固定写；不含 OPENAI_* |
| renderer skills | `tests/test_render_skills.py` | knowledge 扫描；slug 算法；非 ASCII 文件名 fallback sha256 |
| migrator skeleton | `tests/test_migrator.py` | 不存在的 from_<prev_variant>.py 跳过；失败保留旧 `/opt/data` |
| atomic 写 | `tests/test_atomic.py` | 临时文件 + rename，进程中断不留半文件 |

镜像测试在 CI 里通过 `docker run --rm --entrypoint python <image> -m pytest /usr/local/lib/oc-entrypoint/tests/` 触发，单独的
`make verify-hermes-runtime HERMES_VARIANT=hermes-main` target 调用。

### 10.3 端到端验证（本地 docker-compose）

1. `scripts/seed-e2e` 准备一个 org + 一个 app；
2. `make build-hermes-runtime HERMES_VARIANT=hermes-main` 构建镜像；
3. 触发 app_initialize → 验证 `apps/<id>/input/` 与 `apps/<id>/data/` 双目录、
   容器 healthy；
4. `docker exec hermes-<id> oc-info` 拿到 variant 字符串；
5. `docker exec hermes-<id> oc-doctor` 输出 last_render_at；
6. `docker exec hermes-<id> oc-channel-login --channel weixin`，扫码后验证 stdout
   `{"status":"bound"}`；
7. `docker exec hermes-<id> oc-channel-status --channel weixin` 返回 `bound=true`；
8. 修改主副本里某个知识库文件 → restart → 验证 `data/skills/kb-*-*/SKILL.md`
   内容已更新；
9. 改 `apps.model_id` → restart → 验证 `data/config.yaml` 的 `model.default`
   已更新；
10. 浏览器走完一次「应用创建 → 扫码 → 对话」完整路径（按 CLAUDE.md 的交付前
    检查要求，不用 curl 替代）。

## 11 · 失败处理矩阵

| 失败位置 | 现象 | manager 行为 | 排查首选 |
|---|---|---|---|
| manifest 必填字段缺失 | 容器 start 后立即 exit code 1 | `app_health_check` 看到 `exited`，写 `last_error_message`；restart_policy 熔断 | `docker logs hermes-<id>` stderr JSON `phase=load_manifest` |
| renderer 失败（资源文件读不到） | 容器 exit 1 | 同上 | `docker logs` `phase=render`；查 `apps/<id>/input/resources/...` |
| migrator 失败 | 容器 exit 1；`data/` 保持原样 | 同上；管理员可改 `apps.runtime_image_ref` 回滚 prev_variant 重启 | `docker logs` `phase=migrate`；查 `.oc-state.json` 当前 variant |
| 渠道扫码命令失败 | `oc-channel-login` stdout `{"status":"failed"}`、exit 1 | `ChannelCheckBindingHandler` 写 `channel_bindings.status=failed` | manager-api 日志 + 容器 stderr 中间事件 |
| hermes 进程退出（启动后） | docker `Status=exited` / HEALTHCHECK 不健康 | 沿用现 `restart_policy` budget 自愈 | `docker logs` 看 hermes 进程输出 |

## 12 · 上线步骤（本地测试数据可清理）

由于没有新老 app 共存：

1. **代码侧** 在一个 PR 内同时完成 manager 改造 + 节点 agent 改造 + 镜像
   `runtime/hermes/hermes-main/` 全量；保持原子性。
2. **构建镜像** `make build-hermes-runtime HERMES_VARIANT=hermes-main`、
   `make verify-hermes-runtime HERMES_VARIANT=hermes-main`。
3. **清本地数据** `docker compose down -v`、删本地 `<nodeDataRoot>/apps/`、
   `make dev-up` 重起。
4. **跑端到端** 按 §10.3 全流程。
5. **生产发版** `make release-hermes-image HERMES_VARIANT=hermes-main` 推 ACR；
   改 `manager.yaml hermes.runtime_image` 指向新 image tag；老
   `apps.runtime_image_ref` 字段在数据库里清零（生产没有 app，无影响）。

## 13 · 可观测

- `apps.runtime_image_ref` 既有字段，spec 明示其语义「按 app 冻结、平台
  管理员可改」；本期不实现修改 UI，但字段读写链路保留可用。
- audit log 记录两件新事：
  - `app.created` 事件追加 `runtime_image_ref` 字段；
  - 新增 `app.runtime_image_changed` 事件类型，记录 from/to 镜像 tag（管理员
    手动改镜像时触发）。
- 容器内 stderr JSON 行交由 `docker logs` 收集，未来若接 ELK 可结构化检索。

## 14 · 未决事项 / 后续工作

- 平台管理员"为某个 app 单独升级镜像"的 UI 与权限校验（本期仅保证数据层支持）。
- 多 variant 并存的镜像清单 / 选择器在 manager 端的展示（后续 product 需求）。
- 镜像内 oc-entrypoint 跨 variant 的 Python 共享库提取（如果未来发现重复严重，
  可以引入 `_common/`，但只在出现明确重复痛点时做）。
