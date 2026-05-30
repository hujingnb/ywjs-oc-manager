# spec-A2b 设计：节点概念删除 + 破坏性 migration + 渠道重载 k8s 化

> Workstream A 拆为：**spec-A1（pod 运行时侧，已完成）** + **spec-A2a（k8s 编排路径，已完成）** +
> **spec-A2b（本文档：节点摘除 + 收尾改造）** + **spec-A2c（完整 A/B/D/E 合并端到端 + 三角色浏览器验证）**。
>
> 父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`。
> A2a 已把 manager 编排切到 k8s（KubernetesAdapter），runtime-agent 节点概念的代码已失去消费方
> 变孤儿；A2b 彻底删除它，并补齐 A2a 显式推迟的两处收尾（runtime_node payload 残留、渠道绑定后
> hermes 重载的 k8s 化）。

## 1. 目标与背景

A2a 完成后，manager 通过 `k8sorch.Orchestrator`（client-go）创建/伸缩/重启/删除 app（Deployment +
Service + Secret），app 数据走 S3，oc-ops 经 Service DNS 寻址。整套 runtime-agent 体系（节点自注册 /
心跳 / 探测 / docker proxy / 按 nodeID 路由 / docker stats 采样）已不再被编排路径消费，仅作为孤儿保留
以保 A2a 编译通过。

**A2b 是迁移的代码收尾**：把这些孤儿连同其表、配置、前端页一次性删干净，做破坏性 DB migration 删除
节点相关 schema，并补齐 A2a 推迟的两处收尾。**全栈合并验证（A/B/D/E 端到端 + 三角色真实浏览器）是
spec-A2c**，本 spec 不做。

## 2. 范围

### 2.1 在本 spec

- 删除 runtime-agent 节点概念全部代码：agent 包、AgentBackedAdapter/runtime 包、imagecoord、
  runtime-agent 二进制与部署、RuntimeNodeService、NodeHealthReconciler、RuntimeNodeProbeReconciler、
  NodeSelector、agent/runtime-nodes/节点指标 API handler 与路由、main/wiring 节点装配与周期任务、
  domain 节点枚举与相关 job 类型、config 的 RuntimeConfig/EnrollmentSecret/Probe、前端节点管理页与
  资源趋势图。
- **删除整个 docker-stats 资源指标功能**：`runtime_refresh_status` / `app_health_check` handler、
  node-instances 指标端点、`node_resource_samples` / `instance_resource_samples` 两表、前端
  `ResourceTrendChart` 与节点详情资源展示。k8s 原生资源指标（metrics-server）是未来独立 spec。
- **破坏性 DB migration**：DROP `runtime_nodes`、`node_resource_samples`、`instance_resource_samples`
  三表；DROP `apps.runtime_node_id` / `apps.container_id` / `apps.container_name` 三列；sqlc 重生。
- **runtime_node payload 残留清理**：`appInitializePayload.RuntimeNodeID`、audit `"runtime_node"`
  元数据、`app_runtime_ops` 入队 payload、reaper 日志/payload、`AppInitializeStore.GetRuntimeNode`
  接口方法、oc-ops `AuthInput.NodeID/ContainerID`。
- **渠道绑定后 hermes 重载的 k8s 化**：`Orchestrator.RolloutRestart` + `ChannelRestarter` 改 appID
  维度，main 注入 orch 驱动的 restarter 取代 docker `runtimeAdapter`。
- **onboarding 去节点选择**：删 NodeSelector，k8s 调度器负责 pod 落点。

### 2.2 不在本 spec

- **完整 A/B/D/E 合并端到端 + 三角色真实浏览器验证** → **spec-A2c**（迁移最终收口）。
- **k8s 原生资源指标**（metrics-server 采样、新的用量趋势）→ 未来独立 spec。
- **new-api 计费用量 / quota**（`pages/usage/*`，实时取自 new-api）：与节点无关，保留不动。

### 2.3 关键取舍（已与用户确认）

| # | 决策点 | 选择 | 理由 / 影响 |
|---|---|---|---|
| A2b.1 | 范围拆分 | **拆 A2b（节点删除收尾）+ A2c（全栈验证收口）** | A2b 体量大（Explore 估 4-6 周含验证）；「代码清理」与「全栈合并验证」是自然接缝，与 A2a 推迟验证的方式一致 |
| A2b.2 | 资源指标命运 | **一并删整个 docker-stats 资源指标功能，k8s 原生指标留未来 spec** | 采集已随 A2a 删 dispatcher 变暗；re-source 自 metrics-server 是 net-new，属独立 spec；node 级指标随节点概念必死 |
| A2b.3 | 渠道重载方式 | **rollout restart**（Orchestrator.RolloutRestart，patch pod template restartedAt 注解） | k8s 惯用法；幂等、保留 replicas、单一操作、走 preStop oc-presync 同步数据；优于 Scale(0→1)（双调用+停窗+reconciler 竞争）与删 pod（语义不如注解明确） |
| A2b.4 | onboarding 节点选择 | **直接删除，k8s 调度器负责落点**，不给用户选节点 | k8s 模型下 app 是 namespace 内 Deployment，落点由调度器决定；manager 侧选节点已无意义 |

> A2b 不引入「真实环境验证已通过」声明——本 spec 保证编译/单测/migration 实跑 + RolloutRestart 的
> k3d 集成测；完整三角色浏览器 + A/B/D/E 全栈合并验证由 A2c 承担（吸收 A2a.4 推迟项）。

## 3. 三处真实行为变更

### 3.1 Onboarding 去节点选择

k8s 调度器负责 pod 落点，manager 不再选节点。

- 删 `service/node_selector.go`（SQLNodeSelector）与单测。
- `MemberOnboardingService`：去掉 NodeSelector 注入与 `selectNode()` 逻辑；
  `OnboardMemberInput` / `CreateAppForMemberInput` 去掉 `NodeID` 字段。
- `CreateApp` query 去掉 `runtime_node_id` 列写入。
- app 创建后无节点绑定；`app_initialize` 的 `EnsureApp` 在 `cfg.Kubernetes.Namespace` 内建
  Deployment，pod 落点交 k8s 调度器。

### 3.2 渠道绑定后 hermes 重载的 k8s 化

微信扫码绑定成功后，凭证由 pod 内 oc-channel-login 落盘 `/opt/data/weixin/accounts/`（S3 同步），
hermes 仅在启动期扫描该目录决定启用哪些 platform，故需重启 hermes 重载。

- `k8sorch.Orchestrator` 新增 `RolloutRestart(ctx, appID) error`；`KubernetesAdapter` 实现为
  patch Deployment 的 `spec.template.metadata.annotations["kubectl.kubernetes.io/restartedAt"]`
  为当前时间戳，触发 Deployment 按 Recreate 策略重建 pod（走 preStop oc-presync 同步数据）。
- `ChannelRestarter` 接口签名由 `RestartContainer(ctx, nodeID, containerID string)` 改为
  `RestartApp(ctx, appID string) error`（或等价）；`channel_login.go` 的 `finalizeChannelBound`
  改调 appID 维度。
- main 注入 orch 驱动的 restarter 取代 docker `runtimeAdapter`。
- oc-ops `AuthInput` 去掉恒空的 `NodeID` / `ContainerID`（k8s 经 OcOpsResolver 的 Endpoint 寻址，
  channel_login `:88-89`、`:205-206` 的注入移除）。

### 3.3 RuntimeOperationService 去 docker inspector（保留触发能力）

`RuntimeOperationService` 驱动用户可见的「启动 / 停止 / 立即重启」触发按钮（前端
`useTriggerRuntimeOperation`，入队生命周期 job，A2a 后走 k8s 编排），**保留**。但其 `InspectApp`
方法用了 docker inspector（`SetInspector(runtimeAdapter)` 注入 `RuntimeInspector`，经 agent docker
代理 inspect 容器；该注入本就**可选**，nil 时仅返库内 status）。

- 删 runtimeAdapter 时不再注入 inspector；`InspectApp` 退回「库内 `apps.status` + `runtime_snapshot_json`」
  （k8s 下 app 运行态本就由 status reconciler 写入 apps.status / 快照，不再需要 docker inspect）。
- 删 `RuntimeInspector` 接口、`SetInspector`、`InspectApp` 的 docker 分支与 `newRuntimeInspectorWrapper`，
  `InspectApp` 简化为读库返回。

### 3.4 破坏性 DB migration

新建 `internal/migrations/000003_drop_node_concept.{up,down}.sql`：

- **up**：
  - `DROP TABLE runtime_nodes`（含 agent token hash——agent token 无独立表，存于该表）。
  - `DROP TABLE node_resource_samples`、`DROP TABLE instance_resource_samples`。
  - `ALTER TABLE apps DROP COLUMN runtime_node_id, DROP COLUMN container_id, DROP COLUMN container_name`。
  - 注意删列前需先删依赖这些列的索引（如 `runtime_node_id` 上的外键/索引）。
- **down**：重建空 schema（表与列结构），**数据不可恢复**——destructive migration，文档与迁移注释
  明确标注 down 仅恢复结构、不恢复数据。
- sqlc 重新生成：`App` struct 去掉三字段；`ListRunningApps` / `ListStaleInits` 去掉节点列返回；
  删除 `ListAppsByRuntimeNode` / 全部 `runtime_nodes` / `*_resource_samples` query。
- `make openapi-gen` + `make web-types-gen` 保持同步（若有 DTO 受影响）。

## 4. 删除清单与安全顺序

三阶段，每阶段保持 `go build ./... && go vet && go test` 全绿（先解耦消费方，再删死代码，最后破坏性
migration），避免任何阶段编译断裂。

### Phase 1 — 解耦消费方（改代码使其不再依赖待删项）

- onboarding 去 NodeSelector（§3.1）。
- channel_login 去 NodeID/ContainerID 注入 + 改 RolloutRestart（§3.2）。
- reaper 去 `"node_id"` 日志 + payload 的 `"runtime_node"`。
- `appInitializePayload.RuntimeNodeID` 字段 + audit `"runtime_node"` 元数据 + `app_runtime_ops`
  入队 `"runtime_node"` 移除；`AppInitializeStore.GetRuntimeNode` 接口方法删。
- `app_runtime_ops` 的 `SetAppContainer` 调用（清空 container_id）移除；`AppRuntimeStore` 接口去
  `SetAppContainer`。
- `RuntimeOperationService` 去 docker inspector（§3.3）：main 不再 `SetInspector(runtimeAdapter)`，
  删 `RuntimeInspector` / `InspectApp` 的 docker 分支 / `newRuntimeInspectorWrapper`。
- `ListRunningApps` / `ListStaleInits` / `CreateApp` query 去节点列（reconciler 等消费方随之改）。

### Phase 2 — 删死代码（孤岛，无外部消费）

- 后端包：`internal/integrations/agent/`、`internal/integrations/runtime/`（AgentBackedAdapter）、
  `internal/runtime/imagecoord/`、`internal/runtime/agent/`、`cmd/runtime-agent/`。
- service：`runtime_node_service`、`reconciler`（NodeHealth）、`probe_reconciler`、`node_selector`
  及各自单测。
- handler：`app_health_check`、`runtime_refresh_status`；API `agent.go`、`runtime_nodes.go`、
  `resource_metrics.go`（节点指标端点）。
- service：`ResourceMetricsService`（仅查资源采样表，随表删）+ 其 handler 注册 + router
  `Dependencies.ResourceMetricsService` 字段。`RuntimeOperationService` **保留**（§3.3），仅去 inspector。
- wiring：`cmd/server/wiring.go` 的 nodeClientResolver / streamingDockerResolver；`main.go` 的
  nodeResolver / nodeHealthTask / nodeProbeTask / runtimeAdapter / streamingResolver /
  agentTokenStore/Resolver/Sink / newHealthCheckDispatcher / newRuntimeRefreshDispatcher /
  appDirInitializerAdapter（注意：`distLocker` 与 `imagecoordRedis` 仍被 reaper 用，**保留**）。
- domain：`RuntimeNodeStatus*` 枚举与 `validRuntimeNodeStatuses`；
  `JobTypeRuntimeNodeHealthReconcile` / `JobTypeRuntimeRefreshStatus` / `JobTypeAppHealthCheck`
  job 类型常量（确认无残留入队/注册后删）。
- config：`RuntimeConfig` / `RuntimeProbeConfig` / `EnrollmentSecret` + loader 校验 + example yaml +
  本地 config。
- 路由：`router.go` 的 agent 路由组、runtime-nodes 路由组、`Dependencies.RuntimeNodeService` /
  `EnrollmentSecret` 字段。

### Phase 3 — 破坏性 migration + sqlc 重生（§3.4）

最后做，前两阶段已确保无代码读这些表/列。本地 k3d MySQL 实跑 up/down 验证。

## 5. 前端清理

- **删**：`pages/runtime-nodes/`（节点管理页 + 详情）、`api/hooks/useRuntimeNodes.ts`、
  `components/ResourceTrendChart.vue`（CPU/内存趋势）、`components/RuntimeStatusTag.vue`、
  导航与路由中的节点入口（`layouts/DashboardLayout.vue`、`app/router.ts`、
  `pages/dashboard/RoleAwareHome.vue`）及相关 spec。
- **改**：`pages/apps/AppRuntimeTab.vue` 去掉节点 / 资源采样展示，保留 k8s 运行状态 / 版本等仍有效项。
- **保留不动**：`pages/usage/*`（new-api 计费用量 / quota，实时取自 new-api，与资源采样无关）。
- `make web-types-gen` 随 OpenAPI 变更重新生成 `web/src/api/generated.ts`。

## 6. 测试与验证

- 每阶段 `go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... ./cmd/...`
  全绿；前端 `vitest` 受影响用例更新（删页对应 spec 同删，改页 spec 同步）。
- `make openapi-check` 保持工作区干净（删 handler / 改签名后 yaml 与代码同步）。
- migration 在本地 k3d MySQL 实跑 up → down → up，验证可逆（结构层面）且不破坏其余 schema。
- `RolloutRestart`：fake clientset 单测（断言 patch 了 `restartedAt` 注解、Deployment 其余不变）
  + 真实 k3d 集成测（EnsureApp 后 RolloutRestart 触发 pod 重建，新 pod 起来）。
- **完整三角色（platform_admin / org_admin / org_member）真实浏览器走查 + A/B/D/E 合并端到端
  → spec-A2c**。本 spec 仅保证编译 / 单测 / migration 实跑 / RolloutRestart k3d 验证。

## 7. 风险与注意

- **破坏性 migration 不可逆（数据层面）**：drop 表/列删除生产数据。本迁移面向「节点概念退场」，这些
  数据（节点记录、docker-stats 采样、apps 的 docker 容器标识）在 k8s 模型下已无意义；执行前应在发版
  说明中明确。
- **job 类型常量删除**：删 `JobTypeRuntimeRefreshStatus` / `JobTypeAppHealthCheck` 前，必须确认
  Redis 队列中无在途同类型 job（A2a 已删其 dispatcher，新 job 不再入队；存量需排空或容忍 handler
  注册消失后该类型 job 报「未知类型」并最终失败丢弃）。
- **distLocker / imagecoordRedis 误删风险**：`imagecoord` 包删除时，`distLocker`（reaper 用）与其
  底层 `imagecoordRedis` client **必须保留**；只删 imagecoord.Coordinator 及其 ProgressBus。
- **container_id 列删除 vs SetAppContainer**：删列前必须先移除 `SetAppContainer` 的所有调用与接口
  方法（Phase 1），否则 sqlc 重生后该 query 引用不存在的列编译失败。
