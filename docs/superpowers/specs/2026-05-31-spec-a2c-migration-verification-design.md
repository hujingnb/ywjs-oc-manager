# spec-A2c 设计：k8s 迁移最终验证收口

> Workstream A 拆为：A1（pod 运行时，已完成）+ A2a（k8s 编排，已完成）+ A2b（节点删除，已完成）+
> **A2c（本文档：全栈端到端 + 三角色浏览器验证）**。父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`。
>
> A2c 是整个 k8s 迁移的**最终验证收口**：把 A2b 代码真正部署到本地 k3d 全栈、第一次端到端跑通
> A/B/D/E、用真实浏览器逐角色走查全部功能、发起真实 Hermes 对话，带证据。**这套迁移的完整链路
> （app→pod Ready→bootstrap→oc-ops→渠道绑定→对话）此前从未端到端跑过**——各子 spec 只验了孤立
> 部件（fake clientset、golden manifest、k3d 单独建资源、单独跑 migration）。

## 1. 目标与性质

- A2c **不写新功能代码**；做的是：①部署配置补全（让 k3d 能跑 A2b 代码）②全栈 infra 修复健康
  ③真实环境全功能浏览器走查 ④真实 Hermes 对话验证 ⑤**验证中发现的真 bug 就地修复并复验**。
- 验证机制：**chrome-devtools MCP 驱动真实浏览器**逐页走查 + 每步截图存证；证据包落
  `docs/reports/`（**不入 git**，符合仓库约定）。
- 验收性质：**硬性、无有界偏离**（与 B6/E4/A1.4/A2a.4 不同）——必须把全栈 infra 弄健康、A/B/D/E
  闭环、三角色走查、**真实对话（new-api 接 DeepSeek 真回复）+ ragflow 知识库**全部跑通；外部 infra
  不健康就死磕修（网络/镜像源/代理），不接受妥协、不伪造。

## 2. 关键取舍（已与用户确认）

| # | 决策点 | 选择 |
|---|---|---|
| A2c.1 | 验证深度 | **含真实 Hermes 对话**（最高线）：app→pod 拉真实 hermes 达 Ready→bootstrap 交付配置+S3→oc-ops 调通→渠道绑定→真实对话 |
| A2c.2 | 验证方式与证据 | **chrome-devtools MCP 走查 + 截图**（一次性迁移收口证据包，不进 CI） |
| A2c.3 | infra 风险取舍 | **死磕到底，无有界偏离**：new-api+DeepSeek 对话、ragflow 知识库必须全跑通 |
| A2c.4 | LLM 后端 | new-api 接 **DeepSeek**（OpenAI 兼容渠道：https://api.deepseek.com，model deepseek-v4-pro）；API key 配进 new-api，**不入 git** |

## 3. Phase 1 — k3d 部署就绪（让 A2b 代码真跑起来）

现状（Explore）：k3d 跑的是旧 manager 镜像（2026-05-29），MySQL 卡 v2，日志报「runtime_nodes 不存在 /
runtime_node_id 未知列」；`deploy/k8s/local/secret.yaml` 的 manager.yaml **缺 `kubernetes.*` 与
`storage.s3.*` 整段**（A2a/B 引入但部署侧从未配），且残留 `agent:` 段。

- **补 `deploy/k8s/local/secret.yaml`**：加 `kubernetes:`（enabled=true、namespace=oc-apps、
  ops_image=k3d registry ops:dev、bootstrap_base_url（manager 在 ocm 的 Service 基址）、
  image_pull_secret 空、resources requests/limits）；加 `storage.s3:`（enabled=true、
  endpoint=http://minio:9000、region、bucket=oc-apps、ak/sk=ocm/ocmsecret123、use_path_style=true、
  sts_role_arn 占位、presign_ttl）；删残留 `agent:` 段。
- **重建并部署 A2b 代码**：`make local-build`（manager-api/web）+ `make local-build-ops`，滚动重启。
  manager-api 启动自动 autoMigrate 跑 **migration 000003**（k3d MySQL v2→v3，删 runtime_nodes/采样表
  + apps 三列）。
- **manager-rbac** 确认 apply（client-go 操作 oc-apps Deployment/Service/Secret CRUD + pod read）。
- **清理**：删废弃 `web/tests/e2e/runtime-nodes.spec.ts`（引用已删页面）。
- 验收：manager-api pod Ready；日志无 schema/column 错误；`schema_migrations.version=3`；
  `SHOW TABLES LIKE 'runtime%'` 空。

## 4. Phase 2 — 全栈 infra 健康（支撑真实对话）

- **new-api**（ImagePullBackOff）：排查镜像源/网络（daocloud 镜像站 + 7890 代理），拉起健康
  （`/api/status` 通）；配 **DeepSeek 渠道**（类型 OpenAI、base https://api.deepseek.com、key 注入、
  model deepseek-v4-pro），供 app api_key 创建 + LLM relay。
- **ragflow**（CrashLoop）：查 ES/MySQL 后端，拉起健康（知识库需要）。
- **MinIO + STS**：bucket oc-apps 建好（`make local-mc-init`）；STS AssumeRole 端点可用（bootstrap 颁发临时凭证）。
- **真实 hermes 镜像**：确认 runtime_images ref 在 k3d 可拉（v2026.5.16）；pod 拉起达 gateway Ready。
- 验收：new-api/ragflow/minio/redis/mysql/es 全 Ready；DeepSeek 渠道在 new-api 测通；hermes 镜像可拉。

## 5. Phase 3 — A/B/D/E 端到端编排验证（kubectl + curl 证据）

真实创建 app，逐链验证存证：
- **A 编排**：app_initialize 入队 → `kubectl get deploy/svc/secret -n oc-apps` 见
  `app-<id>`/`app-<id>-ocops`/`app-<id>-token` → pod 拉真实 hermes 达 Ready → status
  `creating_container→starting→binding_waiting→running`。
- **B bootstrap+S3**：pod initContainer restore 经 bootstrap 端点拉配置（鉴权返回 STS 凭证）；
  S3 同步 workspace/sessions/state.db。
- **D 部署**：manager 在 ocm、app pod 在 oc-apps 调度；ingress 路由通。
- **E oc-ops**：manager 经 Service DNS `app-<id>-ocops.oc-apps.svc:8080` + 解密 control token 调
  ChannelStatus/Kanban/Cron 通。
- 渠道绑定（微信扫码需用户配合）→ RolloutRestart 重载 → status running。

## 6. Phase 4 — 三角色浏览器走查 + 真实 Hermes 对话（chrome-devtools 截图）

用 chrome-devtools MCP 驱动真实浏览器逐角色走查 + 每步截图：
- **platform_admin**（admin/admin123，组织标识空）：组织/成员/审计/版本管理；**确认节点管理页已消失**。
- **org_admin**：创建应用（选版本/渠道）→app 详情（启停/重启触发、k8s 状态、无节点/资源采样残留）→
  成员/知识库管理。
- **org_member**：只读应用、受限不可见管理项。
- **真实对话**：进 app 对话入口发问，hermes 经 new-api+DeepSeek 真回复，截图存证。
- **微信扫码 / 发消息环节**：需用户配合时**通知用户**处理。
- 交付**逐项验证矩阵**（链路项 × 三角色 × 截图/命令证据）。

## 7. 交付物

- 证据包：截图 + kubectl/curl 输出，组织成逐项矩阵（`docs/reports/`，不入 git）。
- 完成报告：A/B/D/E 各项结论、三角色走查结论、真实对话证据、发现并修复的 bug 清单、迁移收口结论。
- **发现真 bug**：就地修（小型修复 + 复验），记入报告。

## 8. 风险与约定

- **最大风险：外部 infra 健康**（new-api/ragflow/镜像源/网络）——验收无偏离，须死磕修通。
- **密钥安全**：DeepSeek API key 仅配进 new-api（k3d 内），**绝不写入任何 git 跟踪文件**。
- 破坏性：redeploy 触发 migration 000003 删 k3d 的 runtime_nodes 等（已无用，A2b 目标，安全）。
- 真实环境逐项带证据（符合用户一贯验证严格度）。
- 微信相关环节（扫码 / 发消息）需用户配合，届时通知用户。
