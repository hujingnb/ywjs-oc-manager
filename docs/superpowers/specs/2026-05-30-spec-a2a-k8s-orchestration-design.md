# spec-A2a 设计：k8s 编排路径（KubernetesAdapter + 生命周期 + 渲染）

> 状态：设计待评审（2026-05-30）。
> 父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`（§4 Workstream A、D5/D6/D9/D10、§4.1-4.6）。
> 本 spec 是 k8s 迁移 Workstream A 的后半「manager 编排侧」按决策拆出的前半——**A2a（k8s 编排路径）**。
> Workstream A 拆为：**spec-A1（pod 运行时侧，已完成）** + **spec-A2a（本文档）** + **spec-A2b（节点摘除 + 合并验证）**。
> 依赖契约：spec-A1 的 pod 内部契约（`runtime/ops/README.md`）、spec-B 的 bootstrap + storage、
> spec-D 的 app-pod 契约（`deploy/k8s/contracts/app-pod.deployment.yaml`）、spec-E 的 oc-ops HTTP。

## 1. 背景与目标

### 1.1 现状

manager 通过 **runtime-agent 节点**间接管理 app：`internal/integrations/runtime` 的
`AgentBackedAdapter` 实现 `runtime.Adapter`（14 个 docker 形状方法，全带 `nodeID`），worker
handler（`app_initialize.go` 的 5 阶段、`app_runtime_ops.go` 的启停删）经它把 docker 命令代理到
节点 daemon、用 docker exec 跑 oc-* / 健康探针、用 agent file API 读写 app 文件。节点选择按
`runtime_nodes` 表容量、apps.runtime_node_id 外键绑定。

前序 spec 已就位：spec-C（MySQL）、spec-E（oc-* 收敛为 pod 内 oc-ops HTTP）、spec-D（k3d 部署 +
app-pod 契约样例）、spec-B（S3 数据层 + bootstrap 端点 + control token 三用统一 + storage 抽象）、
spec-A1（pod 运行时侧 ops 镜像 + restore/sync 脚本 + pod 内部契约）。

但 **manager 编排侧仍是 docker/agent 路径**——没有任何东西用 k8s API 管理 app。这正是 A2a 要补的。

### 1.2 目标

用 **KubernetesAdapter（client-go）替换 AgentBackedAdapter**，manager **只走 k8s** 编排 app：

1. 新建 **k8s 原生编排接口**，重构创建流程与生命周期 handler 消费它。
2. 渲染 app pod 资源（Deployment/Service/Secret），消费 spec-A1 ops 契约 + spec-D pod 契约 + spec-E oc-ops。
3. docker→k8s 生命周期映射（create→建资源+watch Ready、start/stop→scale、restart、delete+归档）。
4. OcOpsResolver 真实 Service DNS 寻址 + per-app control token 注入；文件操作改读 S3；渠道
   DockerBindingResolver 改走 oc-ops。
5. app 状态用周期 poll reconciler 同步；config 新增 `k8s.*`；RBAC manifest；apps schema 最小适配。
6. 节点旧代码失去消费方变**孤儿**（不删，A2b 删）。

**本 spec 是 manager 编排侧的核心硬骨头**；节点删除与全栈合并验证是 A2b。

## 2. 范围与边界

### 2.1 本 spec 交付（已与用户确认）

1. 新包 `internal/integrations/k8sorch`：`Orchestrator` 接口 + `KubernetesAdapter`（client-go）实现 + `AppSpec`→pod 资源渲染。
2. 创建流程重构（worker handler）：5 阶段 → ensure 三件套 → EnsureApp → WaitReady。
3. 生命周期 handler 改造（start/stop→Scale、restart→UpdateImage/删 pod、delete→Delete+归档）。
4. OcOpsResolver 真实化（Service DNS + 解密 control token）；workspace 浏览 + 删除归档改读 S3；
   DockerBindingResolver 改 oc-ops。
5. 状态 poll reconciler；config `k8s.*`；RBAC manifest；apps schema 最小适配。
6. client-go 依赖引入；main 装配从 AgentBackedAdapter 切换为 KubernetesAdapter。
7. 单测（fake clientset + golden manifest）+ 对真实 k3d 的编排集成测。

### 2.2 不在本 spec（归 spec-A2b）

- **删除节点概念全部代码**：runtime_nodes 表/queries/sqlc、RuntimeNodeService、probe_reconciler +
  agent/probe.go、agent.go enroll/heartbeat handler、node_selector、runtime_refresh_status job、
  `runtime/agent/` 二进制、`deploy/runtime-agent/`、router agent 路由组、main 节点装配与节点周期任务。
- **DB migration**：真删 `apps.runtime_node_id` / `container_id` / `container_name` 列与
  `runtime_nodes` 表（A2a 仅改 nullable / 停写）。
- **完整 A/B/D/E 合并端到端 + 三角色真实浏览器验证**（迁移收口）。

### 2.3 关键取舍（已与用户确认）

| # | 决策点 | 选择 | 理由 / 影响 |
|---|---|---|---|
| A2a.1 | A2 拆分 | **拆 A2a（编排路径）→ A2b（节点删除 + 合并验证）**，本 spec 做 A2a | A2 体量最大；「搭 k8s 路径」与「删旧 + 收口验证」是自然接缝，前者核心后者删除+收口 |
| A2a.2 | Adapter 接口 | **新建 k8s 原生编排接口**（Orchestrator），非复用 docker `runtime.Adapter` | docker Adapter 的 CreateContainer/ContainerExec/文件操作/nodeID 在 k8s 不成立；新接口贴 k8s 语义、无残余方法 |
| A2a.3 | 状态同步 | **周期 poll reconciler**（持续）+ 创建时一次性 scoped watch（等 pod Ready）| 复用现有周期 reconcile 模式、简单、无 informer 缓存复杂度；pod 崩溃重启交 Deployment 控制器 |
| A2a.4 | 验证 | **单测（fake clientset + golden manifest）+ 真实 k3d 编排集成测**；三角色/合并/节点删除 → A2b | A2a 编排可对真实 k3d 跑（节点孤儿不干扰）；外部行为须对真实 k8s API 证明 |

> A2a.4 是对项目「真实环境验证」要求的一次有界偏离（与 spec-B B6 / spec-E E4 / spec-A1 A1.4 同性质）：
> A2a 验证 k8s 编排在真实 k3d 上创建可调度 pod；完整三角色浏览器 + A/B/D/E 全栈合并验证待 A2b。

## 3. 目标架构

```
manager（in-cluster InClusterConfig 或本地 go run + KUBECONFIG）
  ├ client-go ──▶ k8s API（ns oc-apps）
  │     EnsureApp / Scale / UpdateImage / Delete / Status
  │        app pod = initContainer(restore, A1) + hermes(版本镜像)
  │                + oc-ops(同 hermes 镜像覆盖 CMD, E) + sidecar(s3-sync, A1)
  │        emptyDir oc-input + data；app 数据持久在 S3（bootstrap 拉 + sidecar 同步, B）
  ├ poll reconciler ──▶ 读 pod 状态同步 apps.status / health_state_json / runtime_snapshot_json
  ├ OcOpsResolver ──▶ Service DNS(app-<id>-ocops.oc-apps.svc:8080) + 解密 control token
  └ workspace 浏览 / 删除归档 ──▶ S3（storage 抽象, B）
  孤儿（A2b 删）：AgentBackedAdapter / runtime.Adapter / 节点概念全套
```

- manager 只对 **k8s API + S3 + MySQL + Redis + new-api** 说话；无 manager→节点 docker/file 通道。
- 不再需要 `pods/exec`（oc-* 走 oc-ops HTTP，健康走 pod probe）。

## 4. 新编排接口（`internal/integrations/k8sorch`）

```go
// Orchestrator 是 k8s 原生的 app 编排抽象（替代 docker 形状的 runtime.Adapter）。
// app = 一个 Deployment(replicas=1, Recreate) + Service + Secret；manager 按 appID 确定性命名
// （app-<id> / app-<id>-ocops / app-<id>-token）寻址，无需存储 pod/容器标识。
type Orchestrator interface {
	// EnsureApp 渲染并幂等 apply Deployment + Service + Secret（create-or-update）。
	EnsureApp(ctx context.Context, spec AppSpec) error
	// WaitReady 用 scoped watch + timeout 等待 app 的 pod Ready。
	WaitReady(ctx context.Context, appID string, timeout time.Duration) error
	// Scale 伸缩 replicas（0=停，1=起）。
	Scale(ctx context.Context, appID string, replicas int32) error
	// UpdateImage patch Deployment 主容器镜像（换镜像重启，Recreate 自动停旧起新）。
	UpdateImage(ctx context.Context, appID, hermesImage string) error
	// Delete 删除 Deployment + Service + Secret（幂等，不存在视为成功）。
	Delete(ctx context.Context, appID string) error
	// Status 读 app 的 pod 状态（供 reconciler 同步 DB 与创建流程判定）。
	Status(ctx context.Context, appID string) (AppStatus, error)
}

// AppSpec 渲染 app pod 资源所需的全部输入（k8s 形状，非 docker ContainerSpec）。
type AppSpec struct {
	AppID        string            // 资源命名与 label 基准
	HermesImage  string            // 主容器镜像（版本 image_id 解析）；oc-ops 同镜像覆盖 CMD
	OpsImage     string            // ops 镜像（A1，initContainer/sidecar）
	ControlToken string            // per-app control token 明文，写入 Secret
	BootstrapURL string            // OC_BOOTSTRAP_URL，pod 调 manager bootstrap
	Resources    ResourceLimits    // requests/limits
	Labels       map[string]string // 附加 label
}

// AppStatus 是 pod 状态的归一视图（供 reconciler 与创建流程消费）。
type AppStatus struct {
	Phase        string // Pending/Running/Succeeded/Failed/Unknown
	Ready        bool   // pod readiness（hermes 容器 Ready）
	RestartCount int32
	ImageRef     string // 当前实际运行的 hermes 镜像
	Message      string // 异常原因（如镜像拉取失败、CrashLoopBackOff）
	Raw          []byte // pod.Status 序列化，存入 runtime_snapshot_json
}
```

`KubernetesAdapter` 用 `k8s.io/client-go` 实现：clientset 由 in-cluster config 或 kubeconfig
构造；EnsureApp 用 apps/v1 Deployments + core/v1 Services/Secrets 的 server-side apply 或
get-then-create/update；WaitReady 用 pods watch（label selector `app=<id>`）+ timeout；Status
get pod 归一为 AppStatus。

## 5. 生命周期映射（docker → k8s）

| 操作 | 旧（docker/agent） | 新（k8s，A2a） |
|---|---|---|
| 创建 | 5 阶段 pull→prepare(写卷)→create→start | ensure 三件套 → `EnsureApp`（建 Deployment/Service/Secret，control token 写 Secret、bootstrap URL 注入）→ `WaitReady` → binding_waiting |
| 启动 | StartContainer | `Scale(1)` |
| 停止 | StopContainer | `Scale(0)`（preStop 触发 sidecar `oc-presync` 全量同步） |
| 重启-换镜像 | Remove + 重新 init | `UpdateImage`（Recreate 自动停旧起新） |
| 重启-不换镜像 | Stop+ClearSessions+Start | 删 S3 `apps/<id>/sessions/` + `state.db` → 删 pod（Deployment 重建）|
| 删除 | stop→禁 key→archive→清 KB→软删 | `Delete`（Deployment/Service/Secret）→ 禁 new-api key → `storage.MovePrefix(apps/<id>/* → archive/)` → 清 KB → 软删 |

## 6. 创建流程重构（worker handler）

旧 `app_initialize.go` 5 阶段重构为 k8s 流程：

1. **ensure 三件套**（建 pod 前必须就绪，bootstrap 才能成功；**复用现有逻辑**）：
   - api_key：`ensureAPIKey`（new-api CreateAPIKey + 加密入库）。
   - control token：`EnsureAppRuntimeToken`（生成 + 加密入库）。
   - version：已绑定 `app.version_id` 校验（未绑定 markFailed）；镜像 ref 由 `version.image_id` 解析。
2. **EnsureApp**：用解密的 control token + 渲染的 AppSpec 建 Deployment/Service/Secret。
3. **WaitReady**：scoped watch 等 pod Ready（超时 markFailed）。
4. → `binding_waiting` → `running`（渠道绑定后，沿用现有 promoteIfChannelBound）。

**删除的阶段**：`phasePullRuntimeImage`（k8s `imagePullPolicy` + imagePullSecrets，控制器拉取）、
`InitAppDirs`（emptyDir）、`writeAppInput` 写卷 + `pushVersionSkills`（bootstrap + S3 接管）。
三件套仍在 EnsureApp 前完成（manifest/api_key/skills 由 bootstrap 在 pod 启动时交付）。

> imagecoord（跨 manager single-flight 拉镜像）随节点 docker pull 一起失去消费方 → 孤儿（A2b 评估删除；
> k8s 镜像拉取由控制器管，不需要 manager 协调）。

## 7. pod 渲染（AppSpec → 资源）

按 spec-A1 `runtime/ops/README.md` 的 pod 内部契约 + spec-D 契约 + spec-E 渲染：

- **Deployment**（name `app-<id>`，ns oc-apps，replicas=1，strategy Recreate，imagePullSecrets，
  label `app=<id>` + `app.kubernetes.io/part-of=oc-manager`）：
  - initContainer `restore`：OpsImage，command `["oc-restore"]`，env `OC_CONTROL_TOKEN`(Secret) +
    `OC_BOOTSTRAP_URL`，volumeMounts oc-input + data。
  - container `hermes`：HermesImage，env `HERMES_HOME=/opt/data`，volumeMounts oc-input + data，
    readiness/liveness probe（exec `hermes gateway status` 或镜像内 healthcheck）。
  - container `oc-ops`：HermesImage（覆盖 command 为 uvicorn ocops.server:app --port 8080），
    env `OC_OPS_TOKEN`(Secret)，port 8080，volumeMount data。
  - sidecar `s3-sync`：OpsImage，command `["oc-sync"]`，preStop exec `["oc-presync"]`，
    env `OC_CONTROL_TOKEN`(Secret) + `OC_BOOTSTRAP_URL`，volumeMount data。
  - volumes：`oc-input` + `data` 均 emptyDir。
- **Service** `app-<id>-ocops`：selector `app=<id>`，port 8080（OcOpsResolver 寻址目标）。
- **Secret** `app-<id>-token`：`control-token`（manager↔pod 双向复用；oc-ops 与 restore/sync 共用）。

> manifest（含 api_key）不进 Secret/pod spec——由 bootstrap 端点内存渲染经认证通道交付（spec-B D7）。

## 8. OcOpsResolver 真实化 + 文件操作 + 渠道

- **OcOpsResolver**：`OcOpsResolverFromStore` 注入 `cipher`；Resolve 解密 `app.runtime_token_ciphertext`
  填 `Endpoint.Token`，BaseURL 沿用模板 `http://app-<id>-ocops.oc-apps.svc:8080`（已对）。
- **文件操作 → S3**：`workspace_service` 的 ListWorkspace/DownloadWorkspaceFile/StreamWorkspaceArchive
  改读 S3（列举 `apps/<id>/workspace/` + 预签名下载，复用 `storage.ObjectStore`，按需补 List 方法）；
  `app_delete` 的 ArchiveApp → `storage.MovePrefix(apps/<id>/* → archive/<id>/)`。
- **DockerBindingResolver（渠道 OpenID，spec-E 遗留给 spec-A）**：改用 oc-ops `ChannelStatus.AccountID`
  解析微信绑定身份，去掉 `NewDockerExecutor` 的 docker exec 路径。

## 9. 状态 poll reconciler

新增周期 reconciler（复用现有 `NewPeriodicReconciler` 模式）：定期对处于运行态的 app 调
`Orchestrator.Status(appID)` 读 pod 状态 → 同步 `apps.status`（Running/error/...）+ `health_state_json`
+ `runtime_snapshot_json`（pod.Status）。**pod 崩溃重启由 Deployment 控制器自管**，manager 不再
exec 探针自愈。现有 `app_health_check`（exec curl + StartContainer 自愈）→ 去除自愈、改为状态读取
（或并入本 reconciler）。

## 10. config k8s.* + RBAC

```yaml
k8s:
  namespace: oc-apps
  kubeconfig: ""                 # 空=InClusterConfig；非空=本地 go run 用该 kubeconfig 指向 k3d
  image_pull_secret: acr-pull
  ops_image: <ops 镜像 ref>      # spec-A1 ops 镜像（initContainer/sidecar）
  bootstrap_base_url: http://manager-api.oc-system.svc:8080   # 拼 /internal/apps/<id>/bootstrap
  resources:
    requests: { cpu: "250m", memory: "512Mi" }
    limits:   { cpu: "1",    memory: "2Gi" }
```

- hermes/oc-ops 镜像由 `version.image_id` 解析（沿用 `cfg.Hermes.RuntimeImages`）。
- **RBAC**：manager in-cluster ServiceAccount 对 ns oc-apps 授 `deployments`/`services`/`secrets`
  的 CRUD + `pods` get/list/watch + `pods/log` get；**无 `pods/exec`**（oc-ops HTTP + pod probe 取代）。
  Role/RoleBinding/ServiceAccount manifest 落 `deploy/k8s/`（本地 + 生产）。

## 11. apps schema 最小适配（A2b 做大清理）

- `runtime_node_id`：改 **nullable** + 新 app 不写（A2a migration 仅放宽约束；A2b 删列）。
- `container_id` / `container_name`：**不再写**（manager 按 appID 确定性命名寻址，不需存 pod/容器标识；A2b 删列）。
- `runtime_snapshot_json` / `health_state_json`：**复用**存 pod.Status / 健康归一。
- `runtime_image_ref` / `applied_image_ref` / `version_id` / `newapi_key_ciphertext` /
  `runtime_token_hash`/`ciphertext`：**保留不变**（k8s 下仍需）。

> A2a 的 DB 改动尽量小（一条 migration 把 runtime_node_id 放宽 nullable）；删表/删列的破坏性 migration 归 A2b。

## 12. 本地 k3d manager 跑法

- **默认 in-cluster**：spec-D `make local-up` 已把 manager-api 部署为 k3d 内 Deployment；client-go 用
  `rest.InClusterConfig()`；`k8s.bootstrap_base_url` = manager-api Service DNS；pod 经 Service DNS 调 bootstrap。
- **fallback 本地 go run**：`k8s.kubeconfig` 指向 k3d，client-go `BuildConfigFromFlags`；bootstrap URL
  填宿主可达地址（pod→宿主经 k3d 网关）。
- A2a 集成测可用任一模式连 k3d 验证 EnsureApp。

## 13. 验证策略（A2a.4）

- **单测**：
  - KubernetesAdapter 各方法用 `k8s.io/client-go/kubernetes/fake` clientset（EnsureApp 渲染出的对象、
    Scale/UpdateImage 的 patch、Delete 幂等、Status 归一）。
  - pod 渲染 golden manifest 比对（AppSpec → Deployment/Service/Secret YAML，固定快照）。
  - 创建流程逻辑（fake Orchestrator + fake store，验证 ensure 三件套 → EnsureApp → WaitReady 编排）。
  - OcOpsResolver token 解密注入（fake store + cipher）。
- **真实 k3d 编排集成测（环境门控，类 spec-B/A1 模式）**：对本地 k3d 集群，`EnsureApp` 一个测试 app →
  断言 Deployment/Service/Secret 创建正确 + pod 被调度起来达 Ready（用 k3d registry 里已有的
  ops/hermes/oc-ops 镜像）；补充断言 oc-ops Service 可达 / bootstrap 可达。
- **推迟到 A2b**：节点删除、完整 A/B/D/E 合并端到端、三角色真实浏览器走查。

## 14. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| 编排接口重构面大 | 创建流程 + 生命周期 handler + workspace + channel 全改 | 这些本就要重写；新接口边界清晰、单测 fake clientset 覆盖 |
| 节点旧代码暂留为孤儿 | A2a 后 AgentBackedAdapter/节点概念无消费方但仍在 | A2b 统一删除；A2a 确保无消费方引用（编译期可验证孤立） |
| pod Ready 判定 | hermes 启动慢（initContainer restore + bootstrap）| WaitReady 超时足够长（initContainer + 镜像拉取）；probe start-period |
| bootstrap 回调可达性 | pod→manager bootstrap，in-cluster vs go run 地址不同 | `k8s.bootstrap_base_url` 配置化；默认 Service DNS |
| 文件操作迁 S3 行为差异 | workspace 浏览从 agent file API 改 S3 列举/预签名 | 复用 storage 抽象；集成测对真实 MinIO 验证（与 spec-B 一致） |
| schema 适配与 A2b 删列的衔接 | A2a 放宽 nullable、A2b 删列两步 | A2a migration 最小（仅 nullable）；A2b 破坏性删列 |

## 15. 待 spec-A2b（本 spec 不做）

- 删除节点概念全部代码与表（runtime_nodes/service/probe/enroll-heartbeat/agent 二进制/deploy/
  router agent 路由/main 节点装配/节点周期任务/imagecoord 若确认孤儿）。
- 破坏性 DB migration：删 `apps.runtime_node_id`/`container_id`/`container_name` 列与 `runtime_nodes` 表。
- 完整 A/B/D/E 合并端到端 + 三角色真实浏览器验证（吸收 A2a.4 推迟项）。
