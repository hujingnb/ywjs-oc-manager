# OpenClaw Manager v1.0 RC 交付设计

日期：2026-05-03

关联文档：

- [产品设计](../../openclaw-manager-design.md)
- [技术实现设计](../../openclaw-manager-technical-design.md)
- [验证报告](../../verification-report.md)
- [商用 v1 交付设计](./2026-05-02-manager-v1-design.md)
- [OpenClaw 集成契约](./2026-05-02-openclaw-integration-contract.md)

## 1. 目标

v1.0 RC 是商用候选版本，不是只用于演示的中间版本。交付后应满足：

- 平台管理员可以完成组织、Runtime Node、充值、跨组织查看和审计管理。
- 组织管理员可以完成成员开户、应用初始化、组织人设、知识库和用量查看。
- 组织成员可以完成微信绑定、应用级知识库维护、工作目录浏览下载和基础运行操作。
- OpenClaw 容器、微信扫码、文件生成、workspace 下载形成真实闭环。
- 管理端具备基础运维、健康判断、自动恢复、安全加固和可部署文档。
- 所有可自动化项由 agent 自行验证；需要真实微信扫码或微信发消息时，明确暂停并通知用户处理。

## 2. 当前基线

截至 `docs/verification-report.md` 最新记录：

- Sprint 1+2 已跑通到 `binding_waiting + healthy` 容器。
- 已验证节点目录、容器 bind mount、OpenClaw 健康检查、三层 prompt 环境变量。
- 已合入 agent 文件 API、workspace scope、app_delete archive、QR PNG、绑定成功后状态联动等主线能力。
- 自动化检查已通过：`go test ./... -count=1`、`go vet ./...`、`go build ./...`、`npm run typecheck`、`npm test -- --run`。

仍未达到 v1.0 RC 的主要缺口：

- 多节点跨机真实部署未验证。
- 组织级知识库多节点同步状态 UI 未完整验收。
- 平台总览、资源监控、自动重启策略、api_key 风控仍需补齐。
- CSRF、日志脱敏、refresh token 生命周期 E2E、错误响应去敏仍需 hardening。
- Playwright 6 个核心场景未自动化。
- 部署、备份、升级文档还不足以支撑商用交付。

## 3. 交付范围

### 阶段 A：端到端业务闭环

目标：真实用户能从后台开通应用，并通过微信让 OpenClaw 生成文件。

必须交付：

- 组织创建、Runtime Node 注册、agent register/heartbeat。
- 创建成员账号时同步创建应用，应用初始化到 `binding_waiting`。
- 应用容器健康检查通过，容器目录和 bind mount 正确。
- 微信扫码绑定，`channel_bindings.status` 进入 `bound`，应用状态进入 `running`。
- 应用级知识库上传、删除并同步推送到应用所在节点。
- 工作目录列表、单文件下载、目录 zip 打包下载。
- 应用停止、启动、重启、删除；删除时容器删除、api_key 禁用、节点目录归档。
- 关键操作写审计日志。

自行验证：

- 后端全量测试、vet、build。
- 前端 typecheck、unit test、build。
- docker compose 启动 manager、web、postgres、redis、agent、OpenClaw runtime。
- 通过 chrome-devtools MCP 验证登录、组织、节点、成员、应用详情 5 个 tab、工作目录下载。
- 通过 API 或页面完成 onboard，并轮询应用到 `binding_waiting`。
- 用容器命令写入 workspace 测试文件，浏览器下载并校验内容。

需要用户介入：

- 微信扫码绑定：agent 暂停并通知用户扫码。
- 微信发消息触发 OpenClaw 生成文件：agent 给出具体消息内容，用户发送后通知 agent 继续验证。

### 阶段 B：组织治理

目标：组织管理员能治理组织内知识、成员、应用和用量。

必须交付：

- 组织 AI 人设编辑与成员覆盖策略。
- 组织级知识库上传、删除，异步同步到组织下所有应用所在节点。
- `/orgs/{orgId}/knowledge/sync-status` 返回每节点同步状态、最近错误和最近成功时间。
- OrgKnowledgePage 展示节点同步状态、失败原因和重试入口。
- 应用、成员、组织、平台 4 维度 token 用量报表，数据直查 new-api。
- 平台管理员跨组织查看成员、应用、审计和用量。
- 审计日志覆盖知识库、工作目录、节点生命周期、渠道绑定、运行操作。

自行验证：

- 使用两个本地 agent 实例模拟多节点，验证组织级知识库同步到两个节点。
- 关闭一个节点后上传组织级文件，页面显示该节点失败；恢复后重试到成功。
- 用 mock 或 staging new-api 验证 4 维度用量接口和页面。
- chrome-devtools MCP 验证 platform_admin 跨组织视图切换。

需要用户介入：

- 若需要验证真实 new-api 管理后台数据一致性，agent 通知用户在 new-api 页面确认余额、api_key 或用量记录。

### 阶段 C：运维增强

目标：管理员能判断运行状态，并处理常见容器和 api_key 故障。

必须交付：

- Runtime tab 展示容器状态、健康状态、最近日志、CPU、内存、网络、磁盘指标。
- `runtime_refresh_status` job 周期刷新容器状态和资源数据。
- `app_health_check` job 周期检查 OpenClaw 健康状态。
- 自动重启策略：`none`、`on_failure`、`always`，并限制单位时间重启次数。
- 平台总览：组织数、成员数、应用数、运行中容器数、异常应用数、余额/用量摘要。
- `newapi_disable_key` / `newapi_restore_key` job。
- 管理员可手动禁用/恢复应用 api_key，前端展示“容器运行但 api_key 已禁用”的状态。
- 高风险操作二次确认：删除应用、停止容器、重置密码、充值、禁用 api_key。

自行验证：

- 用测试容器制造 CPU/内存负载，chrome-devtools MCP 验证 Runtime tab 指标变化。
- kill OpenClaw 主进程，验证健康检查失败、状态展示和自动重启。
- 禁用 api_key 后验证应用仍显示容器运行，但前端展示 api_key 不可用。
- 通过日志和审计确认所有运行操作有记录。

需要用户介入：

- 若真实微信对话在 api_key 禁用后需要验证“消息失败/恢复后消息成功”，agent 通知用户分别发送测试消息。

### 阶段 D：商用 hardening 与发布验收

目标：达到商用候选质量，可以进入 UAT。

必须交付：

- CSRF double-submit cookie 覆盖所有写操作。
- 日志脱敏覆盖 password、api_key、bootstrap_token、agent_token、refresh_token、master_key、Bearer token。
- refresh token 生命周期 E2E：登录、刷新、登出撤销、过期拒绝、旧 refresh 失效。
- 错误响应去敏：不泄露 SQL、stack trace、内部路径、密钥片段。
- master_key 缺失、长度错误、base64 错误时 fail-fast。
- DB 中 `newapi_key_ciphertext` 和 agent token 相关字段必须为密文或 hash。
- gosec、npm audit 无 high 级别问题。
- 多节点跨机部署演练：manager 一台，agent 至少两台。
- 部署文档、备份恢复文档、版本升级文档。
- Playwright 6 个核心场景自动化。
- UAT 准备：账号、测试组织、反馈表、问题分级规则、release notes。

自行验证：

- 静态检查和安全扫描。
- CSRF 正反用例。
- refresh token 生命周期自动化。
- chrome-devtools MCP 跑主要页面与 Playwright 场景。
- 多节点跨机演练除物理扫码和真实微信消息外均由 agent 执行。
- 按部署文档在干净环境完成一次从零部署。

需要用户介入：

- 提供或确认跨机节点访问条件：机器地址、端口、防火墙/VPN、agent 启动权限。
- 真实微信扫码和消息发送。
- UAT 组织和测试人员确认。

## 4. 验证策略

### 4.1 每次交付必须跑的自动化检查

后端：

```text
go test ./... -count=1
go vet ./...
go build ./...
```

前端：

```text
npm run typecheck
npm test -- --run
npm run build
```

数据库和运行环境：

```text
docker compose up -d manager-postgres manager-redis manager-api manager-web oc-runtime-agent
go run ./cmd/migrate up
curl -fsS http://localhost:3001/healthz
```

浏览器验证：

- 必须使用 chrome-devtools MCP 打开本地 web。
- 验证登录、首页、组织、成员、应用、应用详情 tabs、Runtime Node、知识库、审计。
- 检查 console error、关键 API 请求状态和主要 UI 状态。

### 4.2 需要用户介入的验证协议

当任务进入真实微信验证时，agent 必须停下并说明：

- 当前要验证的目标。
- 用户需要做的动作。
- 用户完成后需要回复的内容。
- agent 继续验证会检查哪些状态。

示例：

```text
现在需要你用微信扫码验证应用绑定。
请打开页面上显示的二维码完成扫码和确认。
完成后回复“已扫码”，我会继续检查 channel_bindings.status、bound_identity 和 apps.status。
```

微信发消息验证示例：

```text
现在需要你给刚绑定的微信助手发送消息：
“请生成一个 hello.txt 到工作目录，内容为 OpenClaw workspace ok”
发送完成后回复“已发送”，我会继续检查 workspace 文件列表和下载内容。
```

### 4.3 退出标准

v1.0 RC 只有在以下条件全部满足时才能标记完成：

- 阶段 A、B、C、D 的必须交付项全部完成。
- 所有自动化检查通过。
- chrome-devtools MCP 页面验证通过，无关键 console error。
- Playwright 6 个核心场景通过。
- 多节点跨机演练成功。
- 真实微信扫码绑定成功，真实微信消息能生成 workspace 文件并下载。
- 安全扫描无 high 级别问题。
- 部署、备份、升级文档完成。
- UAT 前无 P0/P1 已知问题。

## 5. 推荐实施顺序

1. 阶段 A：先补业务闭环，确保用户能真实跑通。
2. 阶段 B：再补组织治理和多节点知识库一致性。
3. 阶段 C：补运维能力和故障恢复。
4. 阶段 D：最后做安全、部署、E2E 和 UAT 准备。

这个顺序保留每阶段可交付产物，并避免在业务闭环尚未稳定时过早投入大规模 hardening。

## 6. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| 微信扫码或消息链路不稳定 | v1.0 RC 核心体验受阻 | 每次需要真实微信时明确通知用户介入；失败时保留日志、截图和 DB 状态 |
| new-api 管理 API 能力不足 | 充值、api_key、用量报表受阻 | 使用 staging 或本地 new-api 提前验证；缺口转为适配层或产品降级决策 |
| 多节点跨机 TLS / 网络配置复杂 | 部署失败或节点不可达 | 阶段 D 单独做跨机演练和部署文档，不把跨机问题混入业务功能开发 |
| OpenClaw 上游行为变化 | parser、wrapper、健康检查失效 | 保持 OpenClaw 集成契约和镜像版本锁定；升级必须先跑契约测试 |
| hardening 改动影响已有前端请求 | 大量写操作 403 或登录失效 | CSRF 和 refresh token 先写自动化测试，再逐步接入 |

## 7. 不在 v1.0 RC 范围

- 多节点自动调度。
- 完整 RBAC。
- 邀请注册。
- Prometheus metrics 和告警平台。
- 日志全文检索。
- master_key 自动轮换。
- API / worker 进程拆分。
- WebSocket / SSE 替代 polling。

