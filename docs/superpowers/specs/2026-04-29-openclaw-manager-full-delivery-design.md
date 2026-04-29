# OpenClaw Manager Full Delivery Design

日期：2026-04-29

关联文档：

- `docs/openclaw-manager-design.md`
- `docs/openclaw-manager-technical-design.md`

## 1. 目标

本规格定义 OpenClaw Manager 的完整交付路线。项目允许分阶段实现，但最终目标是完成需求文档和技术设计文档描述的完整产品，不以 MVP 作为结束点。

阶段的作用是控制风险、限定测试边界、保证每次交付都可运行、可验证、可继续迭代。每个阶段完成后必须测试，测试失败则修复并重新测试，直到该阶段通过后才能进入下一阶段。

## 2. 范围

完整范围包括：

- Go Manager API、worker、scheduler。
- Vue 3 管理后台。
- PostgreSQL 业务库、migration、sqlc 查询。
- Redis job queue、延迟重试和 reconciler。
- 账号、组织、成员、三角色权限和审计。
- Runtime Node CRUD、bootstrap 注册、agent 心跳和状态翻转。
- Agent-backed Docker runtime adapter。
- Agent 文件 API client。
- new-api 账号、充值、api_key 和用量查询 adapter。
- 创建成员账号时同步创建应用，并通过 `app_initialize` 初始化容器。
- 平台默认 prompt、组织人设、应用人设三层拼接。
- 通用 ChannelAdapter，第一版实现微信渠道绑定。
- 组织级与应用级知识库目录主副本、节点同步和同步状态展示。
- 应用工作目录浏览、单文件下载、目录打包下载。
- 容器启停、重启、日志、资源监控和健康检查。
- 平台管理员、组织管理员、组织成员三类后台页面。
- 本地 docker compose 开发环境和完整测试链路；本地调试环境统一由 Docker 管理，容器目录持久化统一使用本地目录 bind mount，不使用 Docker named volume。
- OpenClaw runtime 镜像由本项目自行构建，镜像内必须包含 OpenClaw 安装、微信插件安装和运行所需依赖，方便本地加载、调试和版本固定。

不在第一版范围内的内容沿用原设计文档边界：真实支付、发票、复杂 RBAC、多节点调度、统一反向代理、告警平台、工作目录写入入口、OpenClaw 知识库 OCR/embedding 处理等。

## 3. 分阶段交付设计

### 阶段 1：工程基础与运行基线

建立项目可运行骨架：

- Go 后端目录、Gin router、配置加载、结构化错误、健康检查。
- Vue 3 + Vite + TypeScript + Naive UI 前端目录、路由、布局壳、登录占位页。
- PostgreSQL migration 框架、Redis client、测试框架。
- docker compose 基础服务：manager-postgres、redis、new-api、new-api-postgres、ollama、manager-api、manager-web，预留 agent 服务。
- manager-api 本地调试必须在容器中运行，使用 `air` 热重载 Go 代码。
- manager-web 本地调试必须在容器中运行，使用 Vite dev server 承载前端页面。
- compose 中所有持久化目录必须挂载到仓库 `./data/...` 或明确的本地路径，不定义 Docker named volume。
- 创建 OpenClaw runtime 镜像构建目录和 Dockerfile，构建内容包括 OpenClaw 安装、微信插件安装、插件依赖、健康检查或 CLI 探测脚本。
- `Makefile` 提供 OpenClaw runtime 镜像构建目标，便于本地执行和 compose 引用。
- 创建 `Makefile`，统一封装本地开发、测试、构建、migration、OpenAPI 生成和浏览器验证前的启动命令。
- 本地配置模板、环境变量示例、数据目录和 `.gitignore`。
- 基础 OpenAPI 输出位置或占位生成命令。
- Ollama 调试脚本或 make target：确认服务启动正常、API 可访问，并能拉取或检测一个小模型用于链路验证。
- new-api 调试脚本或 make target：确认服务启动正常、数据库连接正常、管理端基础访问或健康接口正常。

验收：

- 后端单元测试通过。
- migration up/down 或至少初始 up 可执行。
- 前端类型检查和构建通过。
- docker compose 基础服务、manager-api air 容器、manager-web dev 容器可启动。
- `Makefile` 中的第一阶段目标命令可执行，至少覆盖 `make dev-up`、`make dev-down`、`make test`、`make build`、`make migrate-up`、`make migrate-down`。
- `Makefile` 中必须包含 OpenClaw runtime 镜像构建目标，例如 `make build-openclaw-runtime`。
- OpenClaw runtime 镜像可构建成功，并能在容器中验证 OpenClaw 和微信插件安装结果。
- Ollama 容器可启动并通过调试命令确认 API 正常；至少验证版本/模型列表接口，条件允许时拉取小模型。
- new-api 容器和 new-api 数据库可启动并通过调试命令确认服务正常；至少验证 HTTP 可访问和数据库连接正常。
- 检查 compose 文件不包含顶层 named volumes，service-level 挂载使用本地 bind mount。
- 浏览器通过 chrome-devtools MCP 打开前端页面，确认页面加载、布局无明显重叠、健康状态可见。

### 阶段 2：账号、组织、权限与审计基础

实现平台基础业务：

- users、organizations、organization_personas、recharge_records、audit_logs、refresh_tokens schema。
- 平台管理员种子或初始化命令。
- 登录、刷新、登出、当前用户。
- 平台管理员组织 CRUD、启用禁用、充值记录。
- 组织管理员和组织成员账号管理基础。
- 固定三角色权限函数和 middleware。
- 审计日志写入与查询。

验收：

- 权限边界单元测试覆盖平台、组织、成员场景。
- refresh token 生命周期测试通过。
- 组织禁用后用户不可登录或访问 API。
- 前端登录、组织列表、成员列表、审计列表用 chrome-devtools MCP 验证。

### 阶段 3：Runtime Node 与 Agent 通信模型

实现节点生命周期：

- runtime_nodes schema 和 service。
- 平台管理员节点 CRUD、禁用启用、删除约束。
- bootstrap token 生成、hash 存储、过期和 rotate。
- agent 注册端点，注册成功后一次性返回 agent_token。
- agent 心跳端点，更新资源摘要、版本和状态。
- AgentFileClient 和 RuntimeAdapter 接口及 fake 实现。
- TLS、Bearer token、节点 client 缓存的边界设计。

验收：

- bootstrap token 单次消费、过期、并发注册防御测试通过。
- 心跳超时和恢复测试通过。
- fake agent contract 测试覆盖认证、错误映射和路径。
- 前端 Runtime Node 页面用浏览器验证创建、查看、rotate、状态展示。

### 阶段 4：异步 Job 与状态机

实现跨系统流程基础：

- jobs schema。
- Redis ready queue、delayed queue、分布式锁或互斥策略。
- worker 领取、锁定、执行、重试、失败、取消。
- scheduler/reconciler 从 PostgreSQL 补队列。
- app 状态机、job 状态机、错误码。
- app_initialize、容器操作、知识库同步、节点健康检查等 job handler 框架。

验收：

- job backoff、max attempts、幂等和 Redis 丢队列恢复测试通过。
- worker 中断后的 pending job 可恢复。
- 状态机非法转换被拒绝。
- 后台 job 状态页面或详情面板可用浏览器验证。

### 阶段 5：成员创建联动应用初始化

实现账号即客户端核心流程：

- apps、channel_bindings schema。
- `POST /members` 在同一事务中创建 user、app、channel_binding、audit log。
- 事务提交后入队 `app_initialize`。
- new-api adapter 创建组织账号、充值、创建/禁用/恢复 api_key、查询余额和用量的接口与 fake server 测试。
- prompt 模板渲染和三层拼接。
- agent 文件 API 初始化目录、同步 org/app 知识库主副本。
- agent-backed Docker runtime adapter 创建、启动、inspect、logs、stats、exec。
- 初始化失败补偿和重试。

验收：

- 创建成员 + 应用复合事务成功和回滚测试通过。
- prompt 变量注入、占位符未替换检测、拼接顺序测试通过。
- new-api 和 agent fake 测试覆盖错误映射。
- Docker adapter 参数构造和多节点路由测试通过。
- 前端创建成员页面、应用详情初始化状态用 chrome-devtools MCP 验证。

### 阶段 6：渠道绑定与 OpenClaw 集成

实现通用渠道模型和微信第一版：

- ChannelAdapter registry。
- WeChatAdapter 通过 runtime exec 启动登录命令。
- AuthChallenge 解析，第一版支持 `qr_code`。
- 绑定状态轮询、失败、过期、重试、解绑。
- 渠道绑定前端组件按 `AuthChallenge.type` 渲染。

验收：

- OpenClaw CLI 输出解析测试通过。
- 微信二维码 payload、过期、失败重试测试通过。
- fake adapter contract 测试覆盖 registry 路由。
- 浏览器验证渠道绑定页面、二维码展示、错误提示、轮询刷新。

### 阶段 7：知识库与工作目录

实现文件能力：

- manager 本地组织级和应用级知识库主副本。
- 上传、删除、列表、权限、文件类型和大小校验。
- 应用级知识库同步推送到单节点。
- 组织级知识库异步推送到组织下所有应用所在节点，提供 sync-status。
- 工作目录浏览、单文件下载、目录 archive，全部经由 agent 文件 API proxy。
- 路径沙箱、防 `..`、防 URL 编码逃逸、防 symlink、大小和条目数上限。
- 所有访问写审计。

验收：

- 路径安全单元测试覆盖绝对路径、相对回退、URL 编码、符号链接、非常规文件。
- 应用级同步失败回滚主副本测试通过。
- 组织级同步 job 重试测试通过。
- 前端知识库上传、删除、同步状态、工作目录面包屑、下载入口用浏览器验证。

### 阶段 8：统计、运维和完整后台页面

补齐完整运营后台：

- 用量查询：应用、成员、组织、平台维度，每次直查 new-api。
- 容器启动、停止、重启、删除、日志、资源监控、健康检查。
- 平台后台：总览、组织、充值、全平台应用、用量、Runtime Node、平台管理员、审计。
- 组织后台：总览、成员、应用、组织 persona、组织知识库、用量、审计。
- 成员后台：总览、我的应用、渠道绑定、应用知识库、组织知识库只读、工作目录、我的用量、个人设置。
- 状态标签、确认弹窗、错误提示、轮询刷新。

验收：

- 关键 API 的权限和错误分支测试通过。
- 前端类型检查和构建通过。
- chrome-devtools MCP 验证关键页面加载、按钮可点击、表单校验、异步刷新、错误提示和文本不重叠。
- Docker/OpenClaw 相关能力用 fake 或本地测试环境验证最小链路。

### 阶段 9：完整验收与加固

完成产品级验收：

- 跑完整后端单元测试、集成测试、前端类型检查、构建。
- 生成并验证 OpenAPI schema，前端 client 可生成。
- 本地 docker compose 文档和启动流程验证；验证 compose 只使用本地目录 bind mount，不使用 Docker named volume。
- OpenClaw runtime 镜像从 Dockerfile 重新构建并验证 OpenClaw 与微信插件安装正常。
- Ollama 安装调试通过：服务可访问、模型列表正常，至少完成一次小模型拉取或已安装模型检测。
- new-api 安装调试通过：服务可访问、数据库连接正常，管理 API 或健康检查可用。
- new-api/ollama 小模型最小调用链路验证。
- 关键 E2E 场景验证：登录、创建组织、创建节点、agent 注册、创建成员联动应用、初始化、渠道绑定、知识库、工作目录、容器运维、删除。
- 修复所有发现的问题并重测。

验收：

- 自动化测试通过。
- 浏览器验证通过。
- 本地部署说明可按步骤执行。
- 需求文档验收范围中的第一版项目全部覆盖，未实现项必须属于已明确排除范围。

## 4. 架构原则

- 后端以 service 层承载权限、事务、状态机和业务决策。
- handler 只处理 HTTP、DTO、认证上下文和响应。
- adapter 封装外部系统，不写组织、成员、知识库等业务判断。
- PostgreSQL 是业务状态和 job 事实来源，Redis 只负责运行时分发。
- manager 不直接访问 Docker socket 或节点文件系统，所有节点操作经由 agent。
- 知识库以 manager 主副本目录为事实来源，不建知识库 DB 表。
- 工作目录只存在 runtime node，manager 只代理读取和下载。
- 所有敏感数据必须 hash、加密或脱敏，不进入日志和审计明文。
- 前端远程数据由 TanStack Query 管理，Pinia 只保存登录态、权限菜单和 UI 状态。
- 本地调试环境统一使用 Docker Compose 管理。Go 后端通过 manager-api 容器内的 `air` 运行，前端通过 manager-web 容器内的 Vite dev server 运行；宿主机不作为默认运行环境，只执行 docker、git 和测试/维护命令。
- 所有容器目录挂载必须使用本地目录 bind mount，例如 `./data/...:/container/path`；禁止使用 Docker named volume，避免状态落到不可见的 Docker volume 中。
- 项目根目录必须提供 `Makefile` 作为统一入口，开发者优先通过 make targets 执行 compose、测试、构建、migration 和生成任务。
- OpenClaw runtime 镜像属于项目交付物，必须从仓库内 Dockerfile 自行构建；不能只依赖外部预制镜像。镜像构建应固定 OpenClaw、微信插件和依赖安装流程，提供可重复验证的安装检查命令。

## 5. 测试与质量闸门

每个阶段的完成定义包括：

- 代码实现完成。
- 自动化测试覆盖该阶段核心逻辑。
- 配置、migration、构建或生成步骤通过。
- 涉及页面时，必须通过 chrome-devtools MCP 调用浏览器验证。
- 测试或浏览器验证发现问题，必须修复并重新执行相关验证。
- 阶段完成后记录已执行的验证命令和结果。

通用测试基线：

- Go：`go test ./...`，必要时补充 `go vet ./...`。
- 数据库：migration up/down 或可执行 up 验证。
- 前端：typecheck、build、必要的组件或 hook 测试。
- OpenAPI：schema 生成和 TypeScript client 生成验证。
- Job：入队、领取、失败重试、状态回写和 reconciler。
- 页面：chrome-devtools MCP 验证加载、交互、错误状态和布局。
- 本地环境：通过 `Makefile` 目标启动和停止 compose；检查 compose 挂载策略为本地 bind mount。
- 外部基础服务：通过 `Makefile` 目标调试 Ollama 和 new-api，确认容器安装和服务启动正常。
- OpenClaw runtime：通过 `Makefile` 目标构建镜像，并在容器中执行 OpenClaw 与微信插件安装检查。

## 6. 风险控制

主要风险和处理策略：

- new-api 管理 API 能力不完整：在相关阶段先做 adapter spike 和 fake 测试，必要时保留人工绑定外部 ID 的替代路径。
- OpenClaw CLI 输出不稳定：解析器独立测试；必要时在 runtime 镜像加入 JSON wrapper。
- Docker socket 权限高：agent 端限制网络来源、TLS、Bearer token、最小挂载和审计。
- 跨系统状态不一致：所有长流程 job 化，外部动作幂等，失败可见且可重试。
- 路径越权：manager 和 agent 双层路径沙箱，单元测试覆盖逃逸案例。
- 大文件下载和 archive：manager 与 agent 双重限制大小、条目数和超时。
- 节点心跳误判：使用 `3 × heartbeat_interval_seconds` 阈值，恢复后节点自动 active，应用由管理员手动恢复。

## 7. 下一步

本设计通过后，下一步只进入 implementation plan 编写，不直接实现代码。实施计划应按上述九个阶段拆成可执行任务，每个任务包含验收命令、浏览器验证点和失败重测要求。
