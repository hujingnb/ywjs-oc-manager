# spec-A2c 设计：k8s 迁移最终验证收口（全栈部署就绪 + 全功能真实环境验证）

> Workstream A 拆为：A1（pod 运行时侧，完成）+ A2a（manager k8s 编排，完成）+ A2b（节点概念删除，完成）+
> **A2c（本文档：迁移最终验证收口）**。
> 父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`。
>
> 迁移功能代码（C/E/D/B/A1/A2a/A2b）已全部落到 master。组件级验证（fake clientset 单测、golden
> manifest、真实 k3d 资源创建/Delete/RolloutRestart 集成测、真实 MySQL migration up/down）已做，但
> **完整链路从未端到端跑过一次**，且 k3d 部署环境未跟上 A2a/B 引入的新配置。A2c 把全栈真正部署起来并
> 做**全功能真实环境验证**。

## 1. 目标与背景

A2c 不是橡皮图章式「点个测试」，包含三类实打实的工作：

1. **部署就绪**：k3d 的 `deploy/k8s/local/secret.yaml`（manager.yaml 配置）从未配过 A2a/B 引入的
   `kubernetes.*` 与 `storage.s3.*` 段——不补则 k8s 编排 + S3 + bootstrap 在部署环境里**根本是关的**。
   且 k3d 当前跑的是旧 manager 镜像（pre-A2b），MySQL 卡在 migration v2，日志狂报「runtime_nodes 不存在 /
   runtime_node_id 未知列」。
2. **第一次全链端到端**：「创建 app → pod 拉真实 hermes 达 Ready → bootstrap 交付配置 + S3 → oc-ops 经
   Service DNS 调通 → 渠道绑定 → 真实 Hermes 对话」这条完整链路一次都没跑过。
3. **修集成暴露的 bug**：前两个 spec 的评审/真实验证每个都抓到过 Critical（A2a 的 Start/Stop 接缝、A2b 的
   daemon 整树漏删）。全链集成更易暴露问题，**「发现真 bug 就修，修完复验」是预期的一部分**。

**A2c 范围**：让 k3d 全栈健康部署 A2b 代码 + 把 manager 后台**全部功能**在真实环境逐项走查验证（不只
迁移链路）+ 真实 Hermes 对话 + 三角色权限验证，带截图与命令证据；发现不正常就修，修完复验，直到正确。

## 2. 关键取舍（已与用户确认）

| # | 决策点 | 选择 | 影响 |
|---|---|---|---|
| A2c.1 | 验证深度 | **含真实 Hermes 对话（最高线）** | 需全栈 infra 健康（new-api/ragflow/MinIO/STS/真实 hermes 镜像/真实 LLM） |
| A2c.2 | 验证方式与证据 | **chrome-devtools MCP 驱动真实浏览器逐项走查 + 每步截图**；证据包入 `docs/reports/`（不入 git） | 一次性迁移收口证据，不进 CI |
| A2c.3 | infra 风险取舍 | **死磕到底，无有界偏离** | A2c 不算完成直到 new-api+deepseek 真实对话 + ragflow 知识库全跑通；外部 infra 不健康就修（网络/镜像源/代理），不接受「编排已证、对话受限」式妥协 |
| A2c.4 | 测试覆盖 | **全面测试所有页面与功能，不遗漏** | 不限于 A/B/D/E 迁移链路，manager 后台全部功能逐项走查 |

## 3. LLM 渠道（真实对话后端）

new-api 配一个真实 deepseek 渠道供 Hermes 对话推理：

- 类型：OpenAI；Base URL：`https://api.deepseek.com`；Model：`deepseek-v4-pro`。
- **API Key 是用户提供的真实密钥**：**运行时经 new-api 后台（newapi.localhost admin）配置写入 new-api 自身
  数据库，绝不写入任何 git 跟踪文件**（spec 文档、`deploy/k8s/local/secret.yaml`、configmap 均不含该 key）。
  manager 侧 app 创建时由 new-api 颁发 app 级 api_key（sk-），经该渠道路由到 deepseek。
- manager.yaml 的 `hermes.llm.default_model` 需与该渠道可路由的模型一致（当前占位 `qwen2.5:0.5b`，按实际
  deepseek 渠道模型名调整）。

## 4. 验证不变量（红线）

- **真实环境、真实数据、真实交互**：所有验证在 k3d 全栈真实环境，经真实浏览器操作真实账号，禁止 curl/API
  替代前端逻辑验证（curl 仅作辅助证据）。
- **逐项带证据**：交付逐项验证矩阵（功能项 × 角色 × 截图/命令证据）。
- **不伪造、不跳过**：不正常就修，修完复验；未达项必须如实标注原因（A2c 取舍是死磕，故原则上无未达项，除非
  用户配合环节未就绪）。
- **密钥安全**：deepseek key、new-api admin_token 等真实密钥不入 git；证据截图避免泄露密钥明文。
- **微信依赖环节由用户配合**：凡需微信扫码绑定、微信端发消息触发对话的步骤，**暂停并通知用户配合**，不擅自跳过。

## 5. Phase 1 — k3d 部署就绪

让 A2b 代码在 k3d 真跑起来。

- **补 `deploy/k8s/local/secret.yaml` 的 manager.yaml 配置**（A2a/B 引入但 k3d 未配）：
  - `kubernetes:` — `enabled: true`、`namespace: oc-apps`、`ops_image: k3d-ocm-registry.localhost:5000/oc-manager-ops:dev`、
    `bootstrap_base_url: http://manager-api:8080`、`image_pull_secret: ""`、`resources`（requests/limits cpu/mem）。
  - `storage.s3:` — `enabled: true`、`endpoint: http://minio:9000`、`region: us-east-1`、`bucket: oc-apps`、
    `access_key_id/secret_access_key: ocm/ocmsecret123`、`use_path_style: true`、`sts_role_arn`（MinIO 占位）、`presign_ttl: 15m`。
  - 删残留 `agent:` 段（A2b config 已删 AgentConfig，KnownFields(true) 下该段虽被识别但属死配置）。
- **重建并部署 A2b 代码**：`make local-build`（manager-api/web）+ `make local-build-ops`，滚动重启；
  manager-api 启动 autoMigrate 自动跑 **migration 000003**（k3d MySQL v2→v3：删 runtime_nodes/采样表 + apps
  三列；这正是 A2b 目标，表已无用）。
- **确认 `manager-rbac.yaml`** 已 apply（manager SA 对 oc-apps 的 Deployment/Service/Secret/ConfigMap CRUD +
  Pod get/list/watch + pods/log，无 pods/exec）。
- **清理废弃 e2e**：删 `web/tests/e2e/runtime-nodes.spec.ts`（引用 A2b 已删的节点页面）。
- **验收**：manager-api pod Ready；日志无 schema/column 错误且不再启动已删的节点周期任务；
  `SELECT version FROM schema_migrations` = 3；`SHOW TABLES LIKE 'runtime%'` 为空；`SHOW COLUMNS FROM apps` 无
  runtime_node_id/container_id/container_name。

## 6. Phase 2 — 全栈 infra 健康

支撑真实对话与全功能。死磕到健康（A2c.3）。

- **new-api**（当前 ImagePullBackOff）：排查镜像源/网络（daocloud 镜像站 + 7890 代理，见本地 k3d 约定），拉起
  健康（`/api/status` 通）；它负责 app api_key 创建 + LLM relay。**经其后台配置 deepseek 渠道**（§3）。
- **ragflow**（当前 CrashLoop）：查 Elasticsearch/MySQL/MinIO 后端配置，拉起健康；知识库功能需要。
- **MinIO + STS**：bucket `oc-apps` 建好（`make local-mc-init`）；STS AssumeRole 端点可用（bootstrap 颁发
  临时凭证）。
- **真实 hermes 镜像**：确认 `hermes.runtime_images` 的 ref（v2026.5.16）在 k3d 可拉；pod 能拉起达 gateway Ready。
- **deepseek 渠道连通**：new-api 后台加 deepseek 渠道后，测试该渠道可达（new-api 自带测试或经一次真实对话证实）。

## 7. Phase 3 — A/B/D/E 端到端编排验证

真实操作创建 app，逐链验证并存证（kubectl/curl + 浏览器）：

- **A 编排**：app_initialize 入队 → `kubectl get deploy/svc/secret -n oc-apps` 见 `app-<id>` / `app-<id>-ocops` /
  `app-<id>-token` 三件套 → pod 拉真实 hermes 达 Ready → status `creating_container→starting→binding_waiting→running`。
- **B bootstrap+S3**：pod initContainer restore 经 bootstrap 端点拉配置（`/internal/apps/<id>/bootstrap` 鉴权返回
  STS 凭证 + 配置）；sidecar s3-sync 把 workspace/sessions/state.db 同步到 MinIO（验证 bucket 内对象）。
- **D 部署**：manager 在 ocm、app pod 在 oc-apps 调度；ingress 路由通。
- **E oc-ops**：manager 经 Service DNS `app-<id>-ocops.oc-apps.svc:8080` + 解密 control token 调 ChannelStatus /
  Kanban / Cron 通。
- **渠道绑定**：微信扫码绑定流程 → RolloutRestart 重载 hermes → status running。**扫码环节通知用户配合**。

## 8. Phase 4 — 全功能三角色浏览器走查 + 真实对话（chrome-devtools 截图证据）

用 chrome-devtools MCP 驱动真实浏览器，**逐角色把所有可见页面与功能走一遍**（不遗漏），每步截图存证。
下列为骨架清单，执行时以前端实际路由/菜单为准穷举（凡可点、可填、可提交的功能均覆盖）：

- **platform_admin**（admin/admin123，组织标识空）：登录 → 首页/概览 → 组织管理（列表/创建/编辑/删除/级联）→
  成员管理（全局）→ 助手版本管理（创建/编辑/skill 上传/模型校验）→ 审计日志 → 充值记录 → 用量/计费页 →
  平台概览统计 → 确认**节点管理页已消失/404**（A2b 删）。
- **org_admin**（onboarding/seed 创建）：登录 → 首页 → 应用管理（创建应用：选版本/渠道；启停/重启/删除触发；
  版本切换）→ 应用详情（运行状态、k8s 状态、概览/运行时/渠道/知识库各 tab；确认无节点/资源采样残留）→
  渠道绑定（微信扫码——**通知用户配合**）→ 成员管理（组织内增删）→ 知识库管理（创建数据集/上传文档/解析/检索，
  依赖 ragflow）→ 用量查看。
- **org_member**（onboarding/seed 创建）：登录 → 首页 → 应用只读 → 应用详情只读 → 确认**不可见/不可操作**
  创建/删除/成员管理等越权项（权限红线逐项验证）。
- **真实 Hermes 对话**：进绑定后的 app，经微信端或 app 对话入口发问（微信发消息——**通知用户配合**），hermes 经
  new-api+deepseek 真回复；截图存证对话往返。
- **知识库增强对话**：上传文档到 app 知识库 → 提问触发 oc-kb 检索 → 回复含知识内容（验证 RAGFlow 链路）。

> 凡走查中页面报错、按钮无效、数据不一致、权限越界、对话失败等，**记录 → 修复（小型修复+复验循环）→ 重新走查
> 该项**，直到正常。

## 9. 交付物与验收

- **证据包**：截图 + kubectl/curl 输出，组织成**逐项验证矩阵**（功能项 × 三角色 × 证据），存 `docs/reports/`
  下（**不入 git**，符合既有约定）。
- **完成报告**：A/B/D/E 各项 + 全功能走查逐项 ✅/⚠️、三角色权限结论、真实对话与知识库证据、发现并修复的 bug
  清单（含复验）、需用户配合的微信环节记录。
- **验收线**：全栈 infra 健康 + A/B/D/E 闭环 + 全功能三角色走查通过 + 真实 Hermes 对话（deepseek 真回复）+
  ragflow 知识库全部跑通，逐项带证据；无伪造、无遗漏。

## 10. 风险与约定

- **最大风险：外部 infra 与用户配合环节**。new-api/ragflow 当前 ImagePull/CrashLoop——按 A2c.3 死磕修到健康。
  微信扫码/发消息环节依赖用户实时配合，遇到即暂停通知。
- **破坏性**：redeploy 触发 migration 000003 删 k3d 的 runtime_nodes/采样表 + apps 三列（已无用，A2b 目标，安全）。
- **密钥安全**：deepseek key 等真实密钥仅运行时注入 new-api，不入 git；证据截图避免泄露明文。
- **不做新功能**：A2c 只做部署配置补全、infra 修复、验证、以及验证暴露的 bug 修复；不新增业务功能（k8s 原生
  资源指标 metrics-server 等仍是未来独立 spec）。
