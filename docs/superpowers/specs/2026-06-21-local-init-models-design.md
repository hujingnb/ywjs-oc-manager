# 本地一键重建即得完整初始环境（new-api / RAGFlow 模型与 token 自动化）设计

## 背景与目标

本地 k3d 环境通过 `make local-up` 起全栈、`make local-reset` 清空数据重建。当前 `local-up`
已自动完成：各组件起容器并就绪、`make local-seed` 种子 manager 平台管理员
（`admin/admin123`）、`make local-mc-init` 建 MinIO bucket。

但 **new-api 与 RAGFlow 的初始化（管理员、模型渠道/提供商、默认模型）以及二者绑定到
`secret.yaml` 的管理 token，目前全靠人工点 UI**。每次 `make local-reset` 后这套都要重做一遍
（步骤已记录在 `docs/deployment-embedding.md`，但纯手动、易漏）。

目标：让重建后**无需任何手动 UI 操作**即得到一个完整可用的初始环境，包括：

1. 各组件运行正常（现状已满足，本设计不改）。
2. 各管理员账号按 `AGENTS.md`「本地调试账号」创建：
   - manager `admin/admin123`（现状 `local-seed` 已做）。
   - new-api `admin/admin123`（**本设计新增自动化**）。
   - RAGFlow `admin@ragflow.io/admin`（RAGFlow 初始化自建，无需我方创建）。
   - MinIO `ocm/ocmsecret123`（secret 下发，现状已满足）。
3. `secret.yaml` 的实例绑定配置正确：`newapi.admin_token`、`ragflow.api_key`
   （**本设计新增自动回填**）。
4. new-api 渠道（DeepSeek）与 RAGFlow 模型提供商（SiliconFlow `BAAI/bge-m3` embedding +
   DeepSeek chat）及默认模型已配（**本设计新增自动化**）。
5. 真实厂商 API key 放在 gitignored 的 `.env`，由脚本读取（**本设计新增**）。

## 非目标（YAGNI）

- 不做模型清单的可配置抽象：DeepSeek + SiliconFlow `BAAI/bge-m3` 是写死的本地 fixture，
  改模型直接改脚本常量。
- 不动 manager 管理员与 MinIO 的现有 seed 路径。
- 不涉及生产环境（生产模型由人工在控制台配置，见 `docs/deployment-embedding.md`「线上/生产」段）。

## 关键决策（brainstorm 已确认）

- **Token 策略**：随机值。脚本调 new-api/RAGFlow 官方 API 生成随机 token，再**回填**进
  `deploy/k8s/local/secret.yaml` 的 `newapi.admin_token` / `ragflow.api_key` 两行。
  - 已知副作用：每次重建这两行变化，`git status` 显示 `secret.yaml` 脏。用户已接受。
- **配置方式**：尽量走官方 HTTP **API**（对组件升级更稳、不深耦合 DB schema）。
  个别 RAGFlow 内部 API 实在不顺手的步骤，允许该步回落到直接写 RAGFlow DB
  （RAGFlow 镜像已锁 `infiniflow/ragflow:v0.25.6`，DB 写安全；具体哪几步在实现 plan 钉死）。
- **落点**：纳入 `make local-up` 末尾自动执行；缺 `.env` 则跳过、不阻断 `local-up`；
  另暴露 `make local-init-models` 供单独重跑。
- **语言**：`python3`（stdlib `urllib`/`json`，host 已有，无额外依赖）——多步带会话 + JSON，
  比 bash+jq 清晰可靠。

## 架构与组件

```
make local-up
  └─ …（建集群/起组件/seed manager/建 bucket，现状不变）
     └─ 末尾新增： .local-init-models
                     └─ test -f .env 且 DEEPSEEK_API_KEY/SILICONFLOW_API_KEY 非空？
                          ├─ 否 → 打印「跳过模型初始化（缺 .env）」并 exit 0（不阻断）
                          └─ 是 → python3 scripts/local-init-models.py
```

`scripts/local-init-models.py`（host 侧运行）职责：

| 模块 | 通信方式 | 产出 |
|---|---|---|
| new-api 初始化 + 渠道 + 自用模式 + 生成 admin token | HTTP API（`http://newapi.localhost`，`--noproxy` 直连 traefik） | 随机 `admin_token` |
| RAGFlow 模型提供商 + 默认模型 + 生成 api key | HTTP API（`http://ragflow.localhost`）；个别步骤可回落 DB | 随机 `api_key` |
| 回填 secret.yaml 两行 | 就地文本替换 `deploy/k8s/local/secret.yaml` | 更新后的 secret |
| 生效 | `kubectl -n ocm apply -f secret.yaml` + `rollout restart deploy/manager-api` | manager 用新 token |
| 自检 | HTTP API | 通过/失败摘要 |

> 厂商 key 只从 `.env` 读、只在请求体里发给 new-api/RAGFlow，落各组件自身 DB，**绝不写入任何
> git 跟踪文件**（含 secret.yaml、脚本、文档），遵循 `docs/deployment-embedding.md`「安全约束」。

## 详细流程

### `.env` / `.env.example`（gitignore 已含 `.env`）

各加两行（`.env.example` 留空占位 + 注释；`.env` 由用户填真值）：

```
DEEPSEEK_API_KEY=        # DeepSeek 控制台 key，仅本地，勿提交
SILICONFLOW_API_KEY=     # 硅基流动 key，仅本地，勿提交
```

### new-api（纯 API）

1. `POST /api/setup` 建管理员 `admin/admin123`、使用模式置「自用模式」（已初始化则接口报已完成 → 跳过）。
2. `POST /api/user/login` 取会话（cookie / `New-Api-User` + access token 头，实现阶段确认）。
3. 确保「自用模式」开启（`PUT /api/option` `SelfUseModeEnabled=true`），避免请求报「模型价格未配置」。
4. `GET /api/channel` 查是否已有 DeepSeek 渠道；无则 `POST /api/channel` 建：
   - 类型 `DeepSeek`（内置 base `https://api.deepseek.com`）、key=`$DEEPSEEK_API_KEY`
   - 模型 `deepseek-chat,deepseek-reasoner,deepseek-v4-flash,deepseek-v4-flash-none,deepseek-v4-flash-max,deepseek-v4-pro,deepseek-v4-pro-none,deepseek-v4-pro-max`
5. `GET /api/user/token` 生成 admin 系统访问令牌 → 捕获（随机值，作为 `admin_token`）。

### RAGFlow（纯 API；`admin@ragflow.io/admin` 由 RAGFlow 初始化自建）

1. `POST /v1/user/login`（`admin@ragflow.io`/`admin`）取鉴权头。
2. embedding：加 `OpenAI-API-Compatible` 提供商/模型——base `https://api.siliconflow.cn/v1`、
   key=`$SILICONFLOW_API_KEY`、模型 `BAAI/bge-m3`、类型 `embedding`、max_tokens `8192`。
3. chat：加 `DeepSeek` 提供商——key=`$DEEPSEEK_API_KEY`、模型 `deepseek-v4-pro`、`deepseek-v4-flash`。
4. 设默认：embedding=`BAAI/bge-m3`、LLM=`deepseek-v4-pro`。
5. 创建 API key → 捕获（随机值，作为 `api_key`）。

> RAGFlow 内部 API 端点未全部验证（已知 `POST /v1/system/new_token` 在 v0.25.6 返回 404，
> UI「Create new key」走的是别的端点）。**实现阶段先在浏览器抓真实前端请求确认每个端点**；
> 凡 API 不顺手的步骤（疑似 api_token、add_llm、set_tenant_info）回落到直接写 RAGFlow DB
> （`tenant_llm` / `tenant` / `api_token` 表，参照 `scripts/reparse-knowledge-base.sh` 的 DB 访问模式）。

### 回填 secret.yaml 并生效

- 就地替换 `deploy/k8s/local/secret.yaml` 中 `newapi.admin_token:` 与 `ragflow.api_key:` 两行的值
  （按 yaml key 精确匹配，不误伤其它行）。
- `kubectl -n ocm apply -f deploy/k8s/local/secret.yaml`
- `kubectl -n ocm rollout restart deploy/manager-api`（让 manager 加载新 token）。

## 幂等性

可反复执行 `make local-init-models` 而不产生重复或报错：

- new-api setup 已完成 → 接口返回已初始化，跳过。
- 自用模式、默认模型 → 幂等设置（直接置目标值）。
- DeepSeek 渠道、RAGFlow 模型提供商 → 先查后建（按名称/factory 判重）。
- token / api key → 每次重新生成并回填（覆盖 secret.yaml 两行），最终态一致。

## 错误处理与门控

- **缺 `.env` 或 key 为空**：`local-up` 的 `.local-init-models` 步骤打印清晰提示
  「跳过 new-api/RAGFlow 模型初始化：缺 .env 或厂商 key（见 .env.example）」并 `exit 0`，
  不让 `local-up` 失败。`make local-init-models` 单独跑时同样优雅提示。
- **任一 API 步骤失败**（HTTP 非 2xx / JSON `success=false` / 厂商 key 无效导致 verify 失败）：
  python 抛异常、脚本非零退出，并指明失败步骤与响应摘要，便于定位。
- **厂商 key 校验**：配 embedding/chat 后做一次真实调用（new-api chat、RAGFlow embedding）
  确认 key 有效；失败即报错而非静默。

## 验证（脚本结尾自检）

- new-api（chat 通路）：`GET /api/user/self`（带新 `admin_token`）返回 admin、role=100；
  并 `POST /v1/chat/completions`（model `deepseek-v4-pro`）能回话，确认 DeepSeek 渠道 + key 有效。
- RAGFlow（embedding 通路，**直连 SiliconFlow，不经 new-api**）：`GET /api/v1/datasets`
  （带新 `api_key`）`code=0`；并触发一次 embedding（RAGFlow 模型提供商 verify，或建临时库做最小
  解析/检索后清理）确认 SiliconFlow `BAAI/bge-m3` + key 有效。
- 打印「✅ 初始环境就绪」摘要：两个 token 已回填、模型已配、manager 已重启。

> 说明：本地架构中 new-api 只承接 DeepSeek **chat**（供 manager 实例统一网关调用），
> RAGFlow 的 **embedding 直连 SiliconFlow**、不经 new-api，故两条通路分开验证。

> 交付前按 `AGENTS.md`「交付前检查」用**真实浏览器**复核一遍：new-api 渠道、RAGFlow 模型与默认
> 设置、manager 后台「new-api 实时」与「行业知识库」加载正常。

## 涉及文件

| 文件 | 改动 |
|---|---|
| `scripts/local-init-models.py` | 新增：核心初始化脚本 |
| `Makefile` | 新增 `.local-init-models`（内部，门控）+ `local-init-models`（公开）；`local-up` 末尾调用；`.PHONY` 更新 |
| `.env.example` | 新增 `DEEPSEEK_API_KEY` / `SILICONFLOW_API_KEY` 占位 + 注释 |
| `.env`（gitignored，本地） | 用户填真值；不入 git |
| `deploy/k8s/local/secret.yaml` | 运行时被脚本回填两行（git 跟踪，会变脏；非 spec 静态改动） |
| `docs/deployment-embedding.md` | 由手动 runbook 改为「`make local-up` 自动完成；缺 .env 时回退手动」说明 |

## 风险与缓解

1. **RAGFlow 内部 API 端点不稳/未验证** → 实现阶段先抓真实请求确认；不顺手的步骤回落 DB
   （RAGFlow 已锁版本，安全）。
2. **new-api 镜像 `calciumion/new-api:latest` 未锁版本** → 走 API 而非 DB 已规避 schema 耦合；
   若 API 也变，脚本会在对应步骤清晰报错。
3. **secret.yaml 每次重建变脏** → 已与用户确认接受；提示用户可不提交这两行的变化。
4. **host 需能访问 `*.localhost` ingress** → 已具备（`/etc/hosts` + traefik:80，curl 实测可达）；
   脚本用 `--noproxy`/`no_proxy` 绕宿主 clash。
