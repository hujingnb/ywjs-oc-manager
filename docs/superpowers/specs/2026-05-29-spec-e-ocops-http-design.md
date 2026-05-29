# spec-E：oc-* 收敛为 oc-ops HTTP 服务 设计

> 状态：设计待评审（2026-05-29）。
> 父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`（§4.3 D9、§10）。
> 本 spec 是 k8s 迁移 §9 拆分的 **Workstream E**，被 spec-A（manager 改调 HTTP 编排）依赖。

## 1. 背景与目标

### 1.1 现状

manager 通过 **docker / k8s exec** 在 hermes 容器内跑 `oc-*` 脚本来管理 app 的
镜像身份、健康、渠道、Cron、看板能力：

- `internal/integrations/hermes/commands.go`：`oc-info` / `oc-doctor` /
  `oc-channel-{status,login,unbind}`，经 `ContainerExecer`（agent docker proxy
  反向代理 exec）跑命令、解析 stdout JSON。
- `internal/service/hermes_cron.go`：`oc-cron`，经 `cronExecer.ContainerExecJSON`
  拼 argv、解析 `{ok,data,error}` 统一信封。
- `internal/service/hermes_kanban.go`：`oc-kanban`，经
  `kanbanExecer.{ContainerExecJSON,ContainerExecStream}`（含 `watch` 流式）。
- `internal/integrations/hermes/wechat_runner.go`：`oc-channel-login` 经
  `ExecAttach` 流式上报二维码 URL 与登录结果。

### 1.2 目标

把 `oc-*` 收敛为 app pod 内一个 **oc-ops HTTP 服务**（pod 第二容器，
**与 hermes 主容器复用同一镜像**，仅覆盖启动命令），manager 改用 **HTTP + per-app
控制 token** 调用，**彻底取消 docker / k8s exec**。收益（父设计 D9）：

- 解决 exec 流式（stdout 与日志混流、退出码与流帧语义模糊）的脆弱性。
- manager 对 `oc-apps` ns 不再需要 `pods/exec` 权限，安全姿态收窄。
- 把 stdout 信封解析升级为**类型化 HTTP 契约**（HTTP 状态码 + 类型化 body）。

### 1.3 起手 spike 结论（已完成，前置风险已排除）

逐个核对 `runtime/hermes/hermes-v2026.5.16/oc-*.py`，**没有任何脚本依赖常驻
hermes 进程**：

- `oc-info` / `oc-doctor` / `oc-channel-status` / `oc-channel-unbind`：纯文件读写。
- `oc-channel-login`：`from gateway.platforms.weixin import qr_login`——依赖 hermes
  **venv 包**（aiohttp/cryptography/qrcode，镜像已预装），非活进程；`qr_login`
  是独立 async 库、自动落盘凭证到 `/opt/data/weixin/accounts/`。
- `oc-cron` / `oc-kanban`：`subprocess` 调 `hermes <cron|kanban>` CLI——每次起独立
  进程跑完即退，数据落 `jobs.json` / kanban sqlite；manager 串行调用，无并发锁风险。
- `oc-kanban watch`：`subprocess.Popen` 流式 NDJSON。

结论：父设计 §10 的「若依赖活进程则第二容器方案不成立」风险**不成立**，
`shareProcessNamespace` / 容器内协进程退路用不上。第二容器方案成立。

## 2. 范围与边界

### 2.1 本 spec 交付（已与用户确认）

1. **pod 侧 oc-ops 服务**：Python ASGI 服务，跑 hermes venv，监听 `:8080`，
   per-app token 鉴权，共享 `/opt/data`，类型化 REST + 两个 SSE 端点。
2. **oc-* 逻辑重构**：每个 `oc-*.py` 核心逻辑抽成可 import 的 `ocops/` 包模块
   （允许全量调整目录结构），oc-ops server import 这些模块。
3. **同镜像打包**：`ocops/` 包 + starlette/uvicorn 打进 **hermes 镜像**；oc-ops
   容器 = hermes 镜像 ref + 覆盖 CMD 起 `uvicorn`。**不再有独立 oc-ops 镜像**。
4. **manager 侧 HTTP-only 客户端**：新增类型化 `internal/integrations/ocops`
   客户端，cron/kanban/channel service 改依赖它；**删除现有 exec 调用通道**。
5. **HTTP 契约文档** + 双侧单元测试。

### 2.2 不在本 spec（归属其他 spec / 保持原样）

- **`oc-kb`**：hermes 自身当 skill **出站**调 manager runtime API 的客户端
  （`X-OC-App-Token`），不是 manager 入站调的运维命令，保持原样。
- **`oc-entrypoint`**：hermes 主容器入口（读 manifest、render、exec hermes），
  保持原样。
- **app pod 编排**（创建 Deployment/Service/Secret、注入 token、Service DNS 发现
  oc-ops 地址）：**spec-A**。spec-E 只定义 manager 侧「由 appID 解析 oc-ops 端点
  + token」的接口与最小实现，真实 k8s 寻址与 per-app token 生成/注入由 spec-A 落地。
- **app 数据 S3 化 / bootstrap 回调**：spec-B。

### 2.3 关键取舍（已与用户确认）

| # | 决策点 | 选择 | 理由 / 影响 |
|---|---|---|---|
| E1 | oc-ops 镜像 | **与 hermes 复用同一镜像**，仅覆盖 CMD | 版本助手 / 更新只管一个镜像标签；代价是 hermes 镜像体积+starlette/uvicorn |
| E2 | manager transport | **HTTP-only，直接删除 exec 通道**（不保留双 transport） | 代码更简、彻底实现 D9；代价：spec-E 落地到 spec-A 落地之间，cron/kanban/channel 不可跑（由 E4 吸收） |
| E3 | HTTP 契约形态 | **类型化 REST**（HTTP 状态码 + 类型化 body，不再塞信封） | 父设计 D9 本意；spec-A 长期消费的形态；manager 不再拼 shell argv |
| E4 | 本 spec 验证范围 | **仅单元测试**（Python pytest + Go httptest）；浏览器 / 集成验证推迟到 A/B/D/E 全改完一起验 | app pod 编排是 spec-A，oc-ops 在 E 阶段无法作为 sidecar 真实运行，全链路验证不具备条件 |
| E5 | oc-ops server 框架 | Starlette + uvicorn（hermes venv 内） | 轻量、原生 async SSE；装进 hermes venv 以便 import weixin SDK |

> **E4 是对项目「所有新功能须真实浏览器验证」要求的一次显式、有界的偏离**，仅限本
> 迁移序列：spec-E 交付并单测的 HTTP 通道，待 spec-A/D 把 oc-ops 接进 app pod 后，
> 与 A/B/D 一起做端到端 + 三角色浏览器验证。本 spec 不单独宣称功能「已验证可用」。

## 3. 目标架构

```
                                app pod（spec-A 渲染并 apply；spec-D 契约已钉）
manager-api                     ┌───────────────────────────────────────────────┐
┌─────────────────────────┐     │ container: hermes（镜像零改，默认 ENTRYPOINT）    │
│ cron / kanban / channel  │     │   tini → oc-entrypoint → hermes gateway run     │
│ service                  │     │                                                 │
│   └ 依赖 OcOps 类型化接口 │     │ container: oc-ops（同一镜像，覆盖 CMD）          │
│        │                 │HTTP │   uvicorn ocops.server:app --port 8080          │
│        ▼                 │ +   │   Bearer OC_OPS_TOKEN 鉴权                       │
│ internal/integrations/   │token│   import ocops.{info,doctor,channel,cron,kanban}│
│   ocops.Client (HTTP)    │────▶│     ├ 纯文件读写 /opt/data                       │
│   + OcOpsResolver        │:8080│     └ subprocess `hermes {cron,kanban}` CLI      │
└─────────────────────────┘     │ 共享 emptyDir /opt/data                          │
   删除：commands.go 的          └───────────────────────────────────────────────┘
   ContainerExecer、cron/kanban    manager 对 oc-apps 不再需要 pods/exec
   的 ExecJSON/ExecStream 通道
```

- manager → oc-ops 单向 HTTP（manager 是客户端）。
- oc-ops → `/opt/data` 文件 + `hermes` CLI 子进程；不反向调 manager。
- 端点地址（k8s Service DNS）与 per-app token 来源由 spec-A 注入；spec-E 经
  `OcOpsResolver` 接口解耦。

## 4. pod 侧 oc-ops 服务

### 4.1 代码结构（重构 oc-*）

在 `runtime/hermes/hermes-v2026.5.16/` 下新增 `ocops/` 包（随 variant，因其包裹
version 专属 oc-* 逻辑）：

```
ocops/
  __init__.py
  server.py          # Starlette app：路由 + 鉴权中间件 + 错误→HTTP 状态码
  auth.py            # Bearer OC_OPS_TOKEN 常量时间校验
  errors.py          # 统一错误类型 → (HTTP status, {code,message})
  info.py            # oc-info 核心逻辑（由 oc-info.py 抽取）
  doctor.py          # oc-doctor 核心逻辑
  channel.py         # status / unbind（纯文件）+ login（async qr_login，SSE）
  cron.py            # 调 `hermes cron` CLI + jobs.json 读写
  kanban.py          # 调 `hermes kanban` CLI + watch 流
```

- 各 `oc-*.py` 的核心逻辑下沉到对应 `ocops` 模块的**纯函数**（输入类型化参数，
  返回类型化结果或抛 `errors` 里的异常）；保留 `/usr/local/bin/oc-*` 薄 CLI shim
  （调用同一函数、保留 stdout 信封输出），用于镜像内调试与构建期自检，**低风险、
  不破坏现有镜像对外命令契约**。
- `cron.py` / `kanban.py` 内部仍 `subprocess` 调 `hermes` CLI（spike 已确认无活
  进程依赖），只是把入口从「被 exec 的脚本」换成「被 import 的函数 / HTTP handler」。

### 4.2 鉴权

- 入站请求须带 `Authorization: Bearer <OC_OPS_TOKEN>`；`OC_OPS_TOKEN` 由 env 注入
  （spec-D 契约：`app-<APP_ID>-token` Secret 的 `control-token` 键）。
- 常量时间比较（`hmac.compare_digest`）；缺失 / 不匹配 → `401`。

### 4.3 端点契约（类型化 REST）

错误统一映射（替代信封 `error.code`）：

| 异常 / 错误码 | HTTP 状态 | body |
|---|---|---|
| BAD_REQUEST | 400 | `{"code":"BAD_REQUEST","message":"..."}` |
| NOT_FOUND | 404 | `{"code":"NOT_FOUND",...}` |
| UNSUPPORTED（老镜像 / dev stub） | 409 | `{"code":"UNSUPPORTED",...}` |
| INTERNAL（输出非法等） | 500 | `{"code":"INTERNAL",...}` |
| HERMES_CLI_FAILED（hermes CLI 非零 / 超时） | 502 | `{"code":"HERMES_CLI_FAILED",...}` |
| 鉴权失败 | 401 | `{"code":"UNAUTHORIZED",...}` |

镜像 / 健康 / 渠道：

| 现 verb | HTTP | 成功 body |
|---|---|---|
| `oc-info` | `GET /oc/info` | `Info{variant,hermes_upstream_ref,oc_entrypoint_version,built_at}` |
| `oc-doctor` | `GET /oc/doctor` | `Doctor{variant,last_render_at,manifest_sha256,hermes_pid,hermes_status,issues}` |
| `oc-channel-status --channel X` | `GET /oc/channels/{channel}/status` | `ChannelStatus{channel,bound,account_id?}` |
| `oc-channel-unbind --channel X` | `POST /oc/channels/{channel}/unbind` | `ChannelResult{status}` |
| `oc-channel-login --channel X` | `POST /oc/channels/{channel}/login`（**SSE**） | 事件流：`qr{url}` → `bound` / `failed{reason}` |

Cron（请求体 / query 取代 argv；校验同时保留在 manager 侧，见 §5）：

| 现 verb | HTTP | 备注 |
|---|---|---|
| `capabilities` | `GET /oc/cron/capabilities` | `CronCapabilities` |
| `status` | `GET /oc/cron/status` | `CronStatus` |
| `list [--all]` | `GET /oc/cron/jobs?all={bool}` | `[]CronJob` |
| `show --id` | `GET /oc/cron/jobs/{id}` | `CronJob` |
| `create ...` | `POST /oc/cron/jobs` | 类型化 body → `CronJob` |
| `update --id ...` | `PATCH /oc/cron/jobs/{id}` | 类型化 body → `CronJob` |
| `toggle --id --enabled b` | `POST /oc/cron/jobs/{id}/toggle` `{enabled}` | `CronJob`（pause/resume 复用） |
| `run --id` | `POST /oc/cron/jobs/{id}/run` | `CronJob` |
| `delete --id` | `DELETE /oc/cron/jobs/{id}` | `204` |
| `history --id` | `GET /oc/cron/jobs/{id}/history` | `[]CronRunEntry` |
| `output --id --file f` | `GET /oc/cron/jobs/{id}/output?file={f}` | `CronRunOutput` |

Kanban：现有 `oc-kanban` 全部 verb **1:1 映射为 REST**（读 verb→GET、写 verb→
POST/PATCH、删除→DELETE），返回各自类型化对象；`watch` →
`GET /oc/kanban/watch?board={slug}`（**SSE**，事件为现 NDJSON 的类型化形态）。
完整 verb 枚举在实现计划逐条列出，映射规则同上。

### 4.4 镜像与构建（同镜像方案）

改 `runtime/hermes/hermes-v2026.5.16/Dockerfile`：

- 往 hermes venv 装 server 依赖：
  `uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir starlette uvicorn`。
- `COPY ocops/ /usr/local/lib/ocops/`（或装为可 import 包）。
- 镜像默认 `ENTRYPOINT`/`CMD` **不变**（hermes 主容器照旧 `oc-entrypoint`→
  `hermes gateway run`）。oc-ops 容器在 pod spec 里**覆盖 command** 为
  `["/usr/local/lib/hermes-agent/venv/bin/python","-m","uvicorn","ocops.server:app","--host","0.0.0.0","--port","8080"]`。
- 构建期 pytest 自检（Dockerfile 末尾 `python -m pytest .../tests/`）新增
  `ocops` 测试；ocops 测试随 variant `tests/` 走现有自检流程。

> spec-D 契约样例 `deploy/k8s/contracts/app-pod.deployment.yaml` 里 oc-ops 容器
> 的 `<OC_OPS_IMAGE_REF>` 随本决策**退化为 = `<HERMES_IMAGE_REF>`**；spec-A 渲染
> pod 时两容器用同一 image ref，oc-ops 容器加 command 覆盖。本 spec 不改 spec-D
> 已交付的契约文件本体，只在交付说明中标注该对齐点供 spec-A 落实。

## 5. manager 侧

### 5.1 类型化 OcOps 接口与 HTTP 客户端

- 新增包 `internal/integrations/ocops`：
  - `Client`：持 `*http.Client`，方法对应 §4.3 各 verb，入参为类型化请求结构、
    出参为现有 service DTO（`hermes.Info`/`Doctor`、`service.CronJob` 等复用，
    必要时上移到独立 types 文件避免循环依赖），SSE 端点返回事件 channel。
  - HTTP 状态码 → service 哨兵错误（`ErrNotFound`/`ErrCronBadRequest`/
    `ErrCronNotSupported`/`ErrCronCLI` 等）映射，复刻现 `mapCronErrorCode` /
    `mapKanbanErrorCode` 语义。
- 定义 `OcOps` 接口（cron/kanban/channel service 依赖它，便于单测注入假实现）。

### 5.2 端点解析（OcOpsResolver）

- 现有 `CronAppLocation{NodeID,ContainerID,Stub,OrgID,OwnerUserID}` 与
  `KanbanAppLocation` 改为 `OcOpsAppLocation{OrgID,OwnerUserID,Endpoint,Supported}`：
  - `OrgID`/`OwnerUserID`：权限判断保留不变（`CanViewAppCron` 等不动）。
  - `Endpoint{BaseURL,Token}`：oc-ops 地址 + per-app token。
  - `Supported`：替代 `Stub`（dev stub 镜像 / 不支持 → `UNSUPPORTED`）。
- `OcOpsResolver` 接口：`Resolve(ctx, appID) (OcOpsAppLocation, error)`。
- **spec-E 的最小实现**：从 app store 读取必要字段构造 Endpoint；BaseURL 按
  oc-apps Service 命名约定拼装、Token 读 per-app 来源。**真实 k8s Service DNS 寻址
  与 per-app token 的生成 / 存储 / 注入是 spec-A**——spec-E 把它隔离在 resolver
  实现后，单测用假 resolver，不依赖 k8s 即可测 service + client。

### 5.3 service 改造

- `hermes_cron.go` / `hermes_kanban.go`：删 `runOCCron` / `runOCKanban` 的拼 argv +
  信封解析，改调 `OcOps` 客户端的类型化方法；**保留**所有输入校验（`validateCron*`、
  `boardSlugRe` 等，纵深防御，避免坏输入打到 oc-ops），但产出类型化请求体而非 argv。
- `commands.go`（info/doctor/channel-status/login/unbind）：`ContainerExecer` →
  `OcOps`；`wechat_runner.go` 的 `ExecAttach` 流 → `OcOps` 的 login SSE channel。

### 5.4 删除清单

- `internal/integrations/hermes/commands.go` 的 `ContainerExecer` 接口与 exec 实现路径。
- `hermes_cron.go` / `hermes_kanban.go` 的 `cronExecer` / `kanbanExecer`（`ContainerExecJSON`
  / `ContainerExecStream`）依赖。
- `runtime.Adapter` 上仅为 oc-* 服务的 `ContainerExec` / `ContainerExecJSON` /
  `ContainerExecStream` / `ExecAttach`，**若**无其他调用方则随之删除；若 spec-A 尚需
  其他 exec 能力则保留接口、仅摘除 oc-* 用法（实现计划中逐一核对调用方再定）。

> exec 通道删除后，cron/kanban/channel 在 spec-A 把 oc-ops 接进 app pod 前不可端到端
> 运行——已由 E2/E4 接受并记录。

## 6. 验证策略（仅单测；E4）

- **Python（pytest，随镜像构建期自检）**：
  - `ocops` 各模块纯函数单测（沿用现有 `oc-*` 测试场景 / `tests/test_cron_contract.py`
    等已有断言）。
  - server 路由 + 鉴权（401/正常）+ 错误→HTTP 状态码映射 + SSE 事件序列
    （用 Starlette TestClient / httpx）。
- **Go（httptest）**：
  - `ocops.Client` 各方法对 `httptest.Server` 的请求构造、响应解码、HTTP 状态→哨兵
    错误映射、SSE 解析（login / kanban watch）。
  - cron/kanban/channel service 用假 `OcOps` 覆盖正常路径 + 权限拒绝 +
    `UNSUPPORTED`/`NOT_FOUND` 等边界（迁移现有 service 单测）。
- **不做**：构建并运行 oc-ops 容器的集成测试、真实浏览器走查——推迟到 A/B/D/E 全改
  完后统一端到端 + 三角色浏览器验证。

## 7. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| 删 exec 后功能空窗 | spec-E→spec-A 之间 cron/kanban/channel 不可跑 | E2/E4 已接受；A/B/D/E 合并验证；不单独宣称可用 |
| 端点 / token 解析跨 spec | 真实寻址与 token 注入在 spec-A | `OcOpsResolver` 接口隔离，spec-E 单测用假实现 |
| hermes 镜像体积增大 | 多 starlette/uvicorn + ocops 包 | 用户接受（换版本管理简化 E1）；依赖装进既有 venv，无新基础层 |
| oc-ops 与 hermes 同版本耦合 | 同镜像，hermes 升级 oc-ops 自然同步 | 即 E1 的目的，正向收益 |
| SSE 与现 NDJSON / exec 流语义差异 | login / kanban watch 行为须等价 | 单测覆盖事件序列；契约文档固定事件形态 |
| 校验从 argv 白名单移到类型化字段 | 注入面变化 | manager 侧保留全部校验（§5.3），oc-ops 侧再校验一层 |

## 8. 待 spec-A（契约就绪，本 spec 不做）

- app pod 渲染：两容器同 image ref、oc-ops 覆盖 command、port 8080、`OC_OPS_TOKEN`
  Secret 注入。
- manager client-go 编排 + `OcOpsResolver` 真实实现（Service DNS 寻址 + per-app
  token 生成 / 存储 / 注入）。
- A/B/D/E 合并后的端到端 + 三角色真实浏览器验证（吸收 E4 推迟项）。
