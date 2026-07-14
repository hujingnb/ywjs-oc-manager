# AICC 运行时独立命名空间实现计划

**Goal:** 将 `aicc_hidden=true` 应用的运行时资源固定到 `oc-aicc`，普通实例保留在 `oc-apps`，并保持 AICC 对话、知识库检索和普通实例可用。

**Architecture:** manager 保留两个 namespace-bound `KubernetesAdapter`。新增路由编排器：`EnsureApp` 按 `AppSpec.AICCHidden` 选择 adapter；其他操作按 app ID 读取 `aicc_hidden` 再选择。AICC 公开对话独立使用 `oc-aicc` 的 OcOps Service DNS。

**约束:** 不迁移旧资源；本地验证前 `make local-reset`。普通应用绝不因 AICC 配置问题切换 namespace。生产集群写操作由发布人执行。

## Task 1: 配置和集群清单

**Files:** `internal/config/config.go`、`internal/config/loader.go`、配置测试、`deploy/k8s/{local,prod}/00-namespace.yaml`、`deploy/k8s/{local,prod}/manager-rbac.yaml`、`deploy/k8s/local/secret.yaml`、`deploy/k8s/prod/secret.example.yaml`、`deploy/k8s/prod/README.md`。

1. 在 `KubernetesConfig` 添加 `AICCNamespace string \`yaml:"aicc_namespace"\``；k8s 启用且未配置时默认 `oc-aicc`，并测试默认和显式配置。
2. 在本地和生产 namespace 清单增加 `oc-aicc`。
3. 在本地和生产 RBAC 增加 `manager-aicc-orchestrator` Role/RoleBinding，绑定 `ocm/manager-api`；仅授予 deployments、services、secrets、pods、pods/log，不复制 web-publish 的 configmaps/ingresses 权限。
4. 在本地配置与生产模板 `k8s:` 下添加 `aicc_namespace: "oc-aicc"`；生产 README 说明集群管理员必须预先在 `oc-aicc` 创建同名 image pull Secret。

**Verify:** `go test ./internal/config/...`，并对四份 namespace/RBAC YAML 运行 `kubectl apply --dry-run=client`。

**Commit:** `feat(aicc): 增加独立运行时命名空间配置`

## Task 2: 按应用类型路由 Kubernetes 编排

**Files:** `internal/integrations/k8sorch/orchestrator.go`、新增 `routing.go`/`routing_test.go`、`adapter_test.go`。

1. 给 `AppSpec` 增加不渲染到 Pod 的 `AICCHidden bool`。
2. 新增 `AppKindResolver` 窄接口（`IsAICCHidden(ctx, appID)`）和 `RoutingOrchestrator`，持有普通 adapter、AICC adapter、resolver。
3. `EnsureApp` 使用 spec 标志；`WaitReady`、`Scale`、`UpdateImage`、`Delete`、`Status`、`RolloutRestart` 用 resolver 选择 adapter。resolver/AICC adapter 失效时返回明确错误，禁止回退。
4. 路由器实现 `PatchSecretKeys`，使飞书等渠道凭据更新也选择正确 Secret namespace。
5. 用 fake clientset 覆盖普通/AICC 资源只在各自 namespace 创建，以及生命周期、状态和 Secret 路由与错误路径。

**Verify:** `go test ./internal/integrations/k8sorch/...`

**Commit:** `feat(aicc): 按应用类型路由 Kubernetes 编排`

## Task 3: manager 双 adapter 装配和 OcOps 寻址

**Files:** `cmd/server/main.go`、新增 `cmd/server/aicc_namespace_router.go`/测试、`internal/worker/handlers/app_initialize.go`/测试、`internal/service/ocops*_test.go`。

1. 在 `cmd/server` 提供从 `dbStore.Queries.GetApp` 读取 `AiccHidden` 的 `AppKindResolver` 实现。
2. k8s 启用时用同一 clientset 构造 `oc-apps` 和 `oc-aicc` adapter，并将路由器作为既有 `orch` 注入初始化、生命周期、渠道和状态巡检。
3. 保留普通 adapter 的独立变量给 web-publish wiring，保证 wildcard Ingress/TLS 只写 `oc-apps`。
4. 初始化构建 AppSpec 时写入 `app.AiccHidden`。
5. 普通 OcOps resolver 保持 `k8s.namespace`；AICC public resolver 改为 `k8s.aicc_namespace`，测试两种 DNS 不同且普通 resolver 的隐藏应用保护不变。

**Verify:** `go test ./cmd/server/... ./internal/worker/handlers/... ./internal/service/...` 与 `go vet ./cmd/server/... ./internal/worker/handlers/... ./internal/integrations/k8sorch/...`。

**Commit:** `feat(aicc): 装配独立命名空间运行时`

## Task 4: k3d 重置和集成验证

**Files:** `internal/integrations/k8sorch/k3d_integration_test.go`，必要时 `Makefile` 和 `docs/local-development.md`。

1. 扩展集成测试，用两个 adapter 创建普通和 AICC fixture，真实 API 断言 Deployment、Service、Secret 只在正确 namespace，最后清理两侧资源。
2. 跑 `make local-reset`，确认 `oc-aicc`、RBAC 和 image pull Secret 已就绪；若 local-up 未复制 pull Secret，加入最小复制步骤及 dry-run 验证。
3. 使用本地 k3d kubeconfig 运行集成测试，绝不使用生产 kubeconfig。
4. 创建真实普通实例与 AICC 智能体，以 `kubectl get deployment,service,secret,pod -n oc-apps` 和 `-n oc-aicc` 硬断言资源位置。

**Verify:** `make local-reset`、`OC_K8S_TEST_KUBECONFIG="$HOME/.k3d/kubeconfig-ocm.yaml" go test -tags=integration ./internal/integrations/k8sorch/...`。

**Commit:** `test(aicc): 覆盖独立命名空间运行时`

## Task 5: 真实浏览器端到端回归

1. Chrome 登录 `http://ocm.localhost` 平台管理员，开启企业 AICC；以企业管理员进入 AICC 控制台创建/选择智能体。
2. 打开公开页或嵌入测试页发送知识库命中问题，确认完整回复且没有 AICC 转发或上游调用错误。
3. 检查会话、线索、知识库页面，再以 kubectl 核验其隐藏应用资源在 `oc-aicc`。
4. 创建或启动普通 app 并打开会话，确认资源和 OcOps 调用仍在 `oc-apps`。
5. 运行受影响 Go 测试、前端 tests/typecheck/build，检查 `git status` 无无关文件。

**Verify:**

```bash
go test ./cmd/server/... ./internal/config/... ./internal/integrations/k8sorch/... ./internal/service/... ./internal/worker/handlers/...
npm --prefix web test -- --run
npm --prefix web run typecheck
npm --prefix web run build
git status --short
```

## 计划自检

- [x] 覆盖双 adapter、AICC OcOps DNS、namespace、RBAC 和 image pull Secret。
- [x] 覆盖初始化、生命周期、状态巡检、渠道 Secret 和公开对话转发。
- [x] 明确保护普通实例和 web-publish 的 `oc-apps` 行为。
- [x] 包含 unit、真实 k3d、真实浏览器三层验证，生产写操作不由 agent 执行。
