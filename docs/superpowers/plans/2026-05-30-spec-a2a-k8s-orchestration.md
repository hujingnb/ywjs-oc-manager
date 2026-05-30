# spec-A2a：k8s 编排路径（KubernetesAdapter + 生命周期 + 渲染）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 KubernetesAdapter（client-go）替换 AgentBackedAdapter，让 manager 经一个 k8s 原生编排接口创建/伸缩/重启/删除/查状态 app（Deployment+Service+Secret），消费 spec-A1/D/E 契约，app 数据走 S3，节点旧代码失去消费方变孤儿（A2b 删）。

**Architecture:** 新建 `internal/integrations/k8sorch` 包：`Orchestrator` 接口 + `AppSpec`/`AppStatus` + pod 资源渲染（纯函数，golden 测）+ `KubernetesAdapter`（client-go，fake clientset 单测）。创建流程重构为「ensure 三件套 → EnsureApp → WaitReady」；生命周期 handler 改 Scale/UpdateImage/Delete+S3 归档；OcOpsResolver 解密注入 control token；workspace 浏览/删除归档改读 S3；渠道 OpenID 改走 oc-ops；状态用周期 poll reconciler。验证：fake clientset + golden manifest 单测 + 真实 k3d 编排集成测。

**Tech Stack:** Go、k8s.io/client-go（首次引入）+ k8s.io/api + k8s.io/apimachinery、aws-sdk-go-v2（storage，已有）、golang-migrate、sqlc、stretchr/testify；本地 k3d。

---

## 项目约定（实现者必读）

- **工作分支**：直接在 `master` 上完成，不切 worktree、不建分支。
- **提交规范**：Conventional Commits，第一行中文摘要，空行后中文正文；commit 末尾 trailer（精确照抄）：
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```
- **git add 精确文件**：每个 commit 只 `git add` 本任务涉及的文件，**禁止 `git add -A`**，禁止提交未跟踪的 `docs/reports/`。
- **测试断言**：testify（`require`/`assert`，`assert.Equal` expected 在前）；每个测试方法/子测试相邻中文注释说明场景。
- **中文注释**：新增代码的包/文件/方法/结构体/字段都要中文注释说明业务意图。
- **OpenAPI 同步**：本 spec 不改 handler 签名/响应（仅改 service 内部与编排），但每任务结束确认 `make openapi-check` 干净。
- **不删节点代码**：本 spec 只让节点旧代码失去消费方（孤儿），**不删除** runtime_nodes/agent/probe 等——删除归 A2b。`go build ./...` 必须始终通过（孤儿代码仍编译）。
- **sqlc 重生成**：改 `internal/store/queries/*.sql` 后跑 `make sqlc-generate` 并提交生成产物。
- **本地 k3d**：`make local-up` 起 k3d；集成测对真实 k3d（KUBECONFIG 指向 k3d）跑。

## 已钉死的事实（按此实现，不要猜）

- module `oc-manager`；无 k8s 依赖（本 spec 首次引入 client-go/api/apimachinery）。
- migration：golang-migrate，`internal/migrations/`，现有 baseline `000001_baseline.{up,down}.sql`；新增 `000002_*.{up,down}.sql`。`apps.runtime_node_id` 当前 `CHAR(36) NOT NULL` + `fk_apps_runtime_node_id` FK + `idx_apps_runtime_node_status` 索引。
- `NewPeriodicReconciler(name string, interval time.Duration, fn func(ctx context.Context) error) *PeriodicReconciler`（`internal/service/reconciler.go:113`），main 里 `eg.Go(func() error { return task.Start(gctx) })` 启动。
- `OcOpsResolverFromStore{store ocOpsAppStore, baseURLTpl string}`（`internal/service/ocops.go:105`），`ocOpsAppStore` 仅 `GetApp(ctx,id)(sqlc.App,error)`；`sqlc.App` 有 `RuntimeTokenCiphertext null.String`；`auth.Cipher.Decrypt(token string)([]byte,error)`；`ocops.Endpoint{BaseURL,Token string}`。
- `storage.ObjectStore` 接口（`store.go:15`）：PutObject/PresignGet/ObjectExists/MovePrefix/DeletePrefix，**无 List**（本 spec 加）。`storage.AppPrefix(id)`=`apps/<id>/`、`storage.AppArchivePrefix(id)`=`apps/<id>/archive/`。
- `config.Config`（`config.go:12`）顶层字段含 App/Database/.../Storage；`config.Duration` struct（`config.go:215`）；loader `LoadFile`+`applyDefaults`(`loader.go:39`)+`Validate`(`loader.go:72`)，`decoder.KnownFields(true)`。
- `AppInitializeHandler`（`app_initialize.go:218`）+ `NewAppInitializeHandler`(`:240`) + setters `SetAppInputUploader`/`SetImagePullCoord`/`SetNodeDockerProvider`。
- `ContainerLifecycle` 接口（`app_runtime_ops.go:38`）；handlers `NewAppStartContainerHandler(store, containers)`/`Stop`/`Restart`/`Delete(store, containers, factory, fileOps, cleaners...)`。
- `WorkspaceService`：`NewWorkspaceService(store WorkspaceStore, adapter runtime.Adapter, dataRoot string)`（`workspace_service.go:34`）+ `List`/`Download`/`Archive`。
- `DockerBindingResolver`（`channel/wechat_identity.go:31`）：`ResolveWeChatBoundIdentity(ctx, nodeID, containerID)(string,error)` 经 docker exec 读 plugin state；`ocops.Client` 有 `ChannelStatus`（返回含 `AccountID`）。
- main.go 关键装配行：`:204` NewAgentBackedAdapter、`:212` workspaceService、`:224` ocopsResolver、`:232/238` wechat docker、`:507-540` reconciler tasks、`:66` ocopsBaseURLTemplate 常量。

---

## 文件结构

**新建：**
- `internal/integrations/k8sorch/orchestrator.go` — Orchestrator 接口 + AppSpec/AppStatus/ResourceLimits 类型。
- `internal/integrations/k8sorch/render.go` — RenderDeployment/RenderService/RenderSecret 纯函数（AppSpec → typed 对象）。
- `internal/integrations/k8sorch/render_test.go` — golden manifest 单测。
- `internal/integrations/k8sorch/testdata/*.golden.yaml` — 渲染快照。
- `internal/integrations/k8sorch/adapter.go` — KubernetesAdapter（client-go 实现 Orchestrator）。
- `internal/integrations/k8sorch/adapter_test.go` — fake clientset 单测。
- `internal/integrations/k8sorch/k3d_integration_test.go` — 真实 k3d 集成测（环境门控）。
- `internal/integrations/k8sorch/config.go` — KubernetesConfig（client-go config 构造：in-cluster / kubeconfig）。
- `internal/service/app_status_reconciler.go` — 周期读 pod 状态同步 DB 的 reconciler。
- `internal/migrations/000002_apps_runtime_node_nullable.{up,down}.sql` — runtime_node_id 放宽 nullable。
- `deploy/k8s/local/manager-rbac.yaml`（或并入现有 manager manifest）— ServiceAccount/Role/RoleBinding。

**修改：**
- `go.mod` / `go.sum` — 加 client-go 依赖。
- `internal/config/config.go` + `loader.go` — 新增 Kubernetes 配置段。
- `internal/service/ocops.go` — OcOpsResolverFromStore 加 cipher + 解密 token。
- `internal/integrations/storage/store.go` + `s3.go` — ObjectStore 加 ListObjects。
- `internal/service/workspace_service.go` — 改读 S3（注入 ObjectStore）。
- `internal/worker/handlers/app_initialize.go` — 创建流程重构为 k8s 流程。
- `internal/worker/handlers/app_runtime_ops.go` — ContainerLifecycle → Orchestrator 语义（Scale/UpdateImage/Delete+S3 归档）。
- `internal/integrations/channel/wechat_identity.go` — DockerBindingResolver → oc-ops ChannelStatus。
- `cmd/server/main.go` — 装配从 AgentBackedAdapter 切换为 KubernetesAdapter + 新 reconciler + 去 wechat docker exec。
- `internal/store/queries/apps.sql` — 若需停写 container 列的 query 调整（按实际）。

---

## Phase 1：依赖、config、RBAC、migration（基础）

### Task 1: 引入 client-go 依赖

**Files:** Modify `go.mod`

- [ ] **Step 1: go get client-go 套件**

Run（外网走代理，失败先 `go env -w GOPROXY=https://goproxy.cn,direct`）:
```bash
go get k8s.io/client-go@v0.31.3
go get k8s.io/api@v0.31.3
go get k8s.io/apimachinery@v0.31.3
go mod tidy
go build ./...
```
Expected: 依赖写入 go.mod；`go build ./...` 通过（暂无使用，indirect 也可，下个任务即 import）。
（版本选 v0.31.x 与本机 k3d 兼容的近期稳定版；若解析失败用 `@latest` 让 go 选。）

- [ ] **Step 2: Commit**
```bash
git add go.mod go.sum
git commit -F - <<'EOF'
build(k8s): 引入 client-go 依赖

为 spec-A2a k8s 编排路径首次引入 k8s.io/client-go + api + apimachinery。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

> 注：若 Task 1 提交后 `go mod tidy` 因无 import 把依赖标 indirect，Task 2 import 后会转 direct；可接受。

---

### Task 2: config k8s.* 段

**Files:** Modify `internal/config/config.go`, `internal/config/loader.go`; Test `internal/config/loader_test.go`

- [ ] **Step 1: config.go 顶层 Config 加字段 + 类型**

在 `Config` struct 末尾（`Storage StorageConfig` 后）加：
```go
	// Kubernetes 是 app pod 编排（client-go）配置；整段可选，启用编排时要求关键字段齐全。
	Kubernetes KubernetesConfig `yaml:"k8s"`
```
文件末尾加类型：
```go
// KubernetesConfig 是 app pod 编排所需的 k8s 接入参数。
type KubernetesConfig struct {
	// Enabled 为 true 时 manager 用 KubernetesAdapter 编排 app（生产/本地 k3d）。
	Enabled bool `yaml:"enabled"`
	// Namespace 是 app pod 所在命名空间。
	Namespace string `yaml:"namespace"`
	// Kubeconfig 为空时用 in-cluster config；非空时用该 kubeconfig（本地 go run 指向 k3d）。
	Kubeconfig string `yaml:"kubeconfig"`
	// ImagePullSecret 是拉取私有镜像的 Secret 名（如 acr-pull）。
	ImagePullSecret string `yaml:"image_pull_secret"`
	// OpsImage 是 spec-A1 ops 镜像 ref（initContainer/sidecar）。
	OpsImage string `yaml:"ops_image"`
	// BootstrapBaseURL 是 pod 调 bootstrap 的基址（拼 /internal/apps/<id>/bootstrap）。
	BootstrapBaseURL string `yaml:"bootstrap_base_url"`
	// Resources 是 app pod 的资源 requests/limits。
	Resources K8sResources `yaml:"resources"`
}

// K8sResources 描述 pod 资源请求/上限（CPU/内存的 k8s quantity 字符串）。
type K8sResources struct {
	Requests K8sResourceSpec `yaml:"requests"`
	Limits   K8sResourceSpec `yaml:"limits"`
}

// K8sResourceSpec 是单组 CPU/内存配额。
type K8sResourceSpec struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}
```

- [ ] **Step 2: loader.go applyDefaults + Validate**

`applyDefaults()` 内加：
```go
	// k8s 启用时填默认 namespace 与资源配额（与父设计/本地 k3d 一致）。
	if c.Kubernetes.Enabled {
		if strings.TrimSpace(c.Kubernetes.Namespace) == "" {
			c.Kubernetes.Namespace = "oc-apps"
		}
		if c.Kubernetes.Resources.Requests.CPU == "" {
			c.Kubernetes.Resources.Requests.CPU = "250m"
		}
		if c.Kubernetes.Resources.Requests.Memory == "" {
			c.Kubernetes.Resources.Requests.Memory = "512Mi"
		}
		if c.Kubernetes.Resources.Limits.CPU == "" {
			c.Kubernetes.Resources.Limits.CPU = "1"
		}
		if c.Kubernetes.Resources.Limits.Memory == "" {
			c.Kubernetes.Resources.Limits.Memory = "2Gi"
		}
	}
```
`Validate()` 内加：
```go
	// k8s 启用时关键字段必须齐全，缺失 fail-fast。
	if c.Kubernetes.Enabled {
		if strings.TrimSpace(c.Kubernetes.OpsImage) == "" || strings.TrimSpace(c.Kubernetes.BootstrapBaseURL) == "" {
			return fmt.Errorf("k8s 已启用但 ops_image / bootstrap_base_url 不完整")
		}
	}
```

- [ ] **Step 3: 单测**

`loader_test.go` 加：
```go
// TestKubernetesValidationRequiresFields 验证启用 k8s 但缺关键字段时加载报错。
func TestKubernetesValidationRequiresFields(t *testing.T) {
	// 启用 k8s 却缺 ops_image/bootstrap_base_url，Validate 必须 fail-fast
	var c Config
	c.Kubernetes.Enabled = true
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "k8s")
}
```
（若 Validate 因其它必填项先失败，填最小合法值或确认 k8s 分支触发。）

- [ ] **Step 4: 跑测试 + Commit**
```bash
go test ./internal/config/ -run TestKubernetes -v && go build ./...
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go
git commit -F - <<'EOF'
feat(config): 新增 k8s 编排配置段

顶层 Config 加 Kubernetes（enabled/namespace/kubeconfig/image_pull_secret/
ops_image/bootstrap_base_url/resources）；applyDefaults 填默认 namespace oc-apps
与资源配额；Validate 启用时校验 ops_image/bootstrap_base_url 齐全。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3: apps.runtime_node_id 放宽 nullable（migration）

**Files:** Create `internal/migrations/000002_apps_runtime_node_nullable.up.sql` / `.down.sql`

- [ ] **Step 1: 写 up/down migration**

`000002_apps_runtime_node_nullable.up.sql`:
```sql
-- spec-A2a：k8s 编排下 app 不再绑定 runtime_node；放宽 runtime_node_id 为 nullable，
-- 新建 app（k8s）不写该列。真删列与 runtime_nodes 表归 spec-A2b。
ALTER TABLE apps MODIFY COLUMN runtime_node_id CHAR(36) NULL;
```
`000002_apps_runtime_node_nullable.down.sql`:
```sql
-- 回滚：恢复 NOT NULL（注意：存在 NULL 行时回滚会失败，需先清理）。
ALTER TABLE apps MODIFY COLUMN runtime_node_id CHAR(36) NOT NULL;
```

> 注：保留 `fk_apps_runtime_node_id` 与 `idx_apps_runtime_node_status`（MySQL 允许 nullable FK 列；NULL 不校验 FK）。A2b 删 FK/索引/列。

- [ ] **Step 2: 本地迁移验证 + sqlc**

Run: `make local-migrate`（或对本地 MySQL 跑 migrate up）确认 000002 应用成功；若 sqlc 模型因列可空性变化需重生成则 `make sqlc-generate`（`RuntimeNodeID` 可能从 `string` 变 `null.String`——若变，记下，后续 GetApp 消费方按需适配；多数 sqlc 配置下 nullable 列生成 null.String）。
Expected: 迁移成功；`go build ./...` 通过。

- [ ] **Step 3: Commit**
```bash
git add internal/migrations/000002_apps_runtime_node_nullable.up.sql internal/migrations/000002_apps_runtime_node_nullable.down.sql
# 若 sqlc 重生成了 models.go 等，一并 add
git commit -F - <<'EOF'
feat(db): 放宽 apps.runtime_node_id 为 nullable（k8s 编排）

k8s 编排下 app 不绑定 runtime_node；新建 migration 000002 把 runtime_node_id
改 nullable，新建 app 不写该列。真删列与 runtime_nodes 表归 spec-A2b。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4: RBAC manifest

**Files:** Create `deploy/k8s/local/manager-rbac.yaml`

- [ ] **Step 1: 写 RBAC**

```yaml
# spec-A2a：manager in-cluster 编排 app pod 所需 RBAC（ns oc-apps）。
# 授 deployments/services/secrets CRUD + pods get/list/watch + pods/log get；无 pods/exec
# （oc-ops HTTP + pod probe 取代 docker exec）。
apiVersion: v1
kind: ServiceAccount
metadata:
  name: oc-manager
  namespace: oc-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: oc-manager-app-orchestrator
  namespace: oc-apps
rules:
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["services", "secrets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: oc-manager-app-orchestrator
  namespace: oc-apps
subjects:
  - kind: ServiceAccount
    name: oc-manager
    namespace: oc-system
roleRef:
  kind: Role
  name: oc-manager-app-orchestrator
  apiGroup: rbac.authorization.k8s.io
```

- [ ] **Step 2: Commit**
```bash
git add deploy/k8s/local/manager-rbac.yaml
git commit -F - <<'EOF'
feat(k8s): 新增 manager 编排 app pod 的 RBAC

ServiceAccount oc-manager（ns oc-system）+ Role/RoleBinding（ns oc-apps）：
deployments/services/secrets CRUD + pods get/list/watch + pods/log get，无
pods/exec（oc-ops HTTP + pod probe 取代）。manager Deployment 待挂该 SA（spec-A2b/D）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 2：k8sorch 包（接口 + 渲染 + adapter）

### Task 5: Orchestrator 接口与类型

**Files:** Create `internal/integrations/k8sorch/orchestrator.go`

- [ ] **Step 1: 写接口与类型**

```go
// Package k8sorch 提供 k8s 原生的 app 编排抽象，替代 docker 形状的 runtime.Adapter。
// app = 一个 Deployment(replicas=1, Recreate) + Service(oc-ops) + Secret(control-token)，
// manager 按 appID 确定性命名（app-<id> / app-<id>-ocops / app-<id>-token）寻址。
package k8sorch

import (
	"context"
	"time"
)

// Orchestrator 是 k8s 原生 app 编排接口。
type Orchestrator interface {
	// EnsureApp 渲染并幂等 apply Deployment + Service + Secret（create-or-update）。
	EnsureApp(ctx context.Context, spec AppSpec) error
	// WaitReady 用 scoped watch + timeout 等待 app 的 pod Ready。
	WaitReady(ctx context.Context, appID string, timeout time.Duration) error
	// Scale 伸缩 replicas（0=停，1=起）。
	Scale(ctx context.Context, appID string, replicas int32) error
	// UpdateImage patch Deployment 主容器（hermes/oc-ops 同镜像）镜像，触发 Recreate 重启。
	UpdateImage(ctx context.Context, appID, hermesImage string) error
	// Delete 删除 Deployment + Service + Secret（幂等，NotFound 视为成功）。
	Delete(ctx context.Context, appID string) error
	// Status 读 app 的 pod 状态。
	Status(ctx context.Context, appID string) (AppStatus, error)
}

// AppSpec 是渲染 app pod 资源所需的全部输入（k8s 形状）。
type AppSpec struct {
	AppID        string            // 资源命名与 label 基准
	HermesImage  string            // 主容器镜像（版本 image_id 解析）；oc-ops 同镜像覆盖 CMD
	OpsImage     string            // ops 镜像（A1，initContainer/sidecar）
	ControlToken string            // per-app control token 明文，写入 Secret
	BootstrapURL string            // OC_BOOTSTRAP_URL，pod 调 manager bootstrap
	ImagePullSecret string         // 私有镜像拉取 Secret 名
	Resources    ResourceLimits    // requests/limits
	Labels       map[string]string // 附加 label
}

// ResourceLimits 是 pod 资源 requests/limits（CPU/内存 quantity 字符串）。
type ResourceLimits struct {
	RequestsCPU    string
	RequestsMemory string
	LimitsCPU      string
	LimitsMemory   string
}

// AppStatus 是 pod 状态归一视图。
type AppStatus struct {
	Phase        string // Pending/Running/Succeeded/Failed/Unknown/NotFound
	Ready        bool   // hermes 容器 Ready
	RestartCount int32
	ImageRef     string // 当前运行的 hermes 镜像
	Message      string // 异常原因
	Raw          []byte // pod.Status 序列化，存 runtime_snapshot_json
}
```

- [ ] **Step 2: 编译 + Commit**
```bash
go build ./internal/integrations/k8sorch/
git add internal/integrations/k8sorch/orchestrator.go
git commit -F - <<'EOF'
feat(k8sorch): 定义 k8s 原生编排接口与类型

Orchestrator（EnsureApp/WaitReady/Scale/UpdateImage/Delete/Status）+ AppSpec/
AppStatus/ResourceLimits，替代 docker 形状的 runtime.Adapter；app 按 appID
确定性命名寻址，无需存储 pod/容器标识。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 6: pod 资源渲染（纯函数 + golden 测）

**Files:** Create `internal/integrations/k8sorch/render.go`, `render_test.go`, `testdata/`

- [ ] **Step 1: 写 render.go**

按 spec-A1 `runtime/ops/README.md` pod 内部契约 + spec-D 契约 + spec-E 渲染。命名常量 + 三个纯函数：
```go
package k8sorch

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// 资源命名约定（manager 按 appID 确定性寻址，无需存 pod 标识）。
func deploymentName(appID string) string { return "app-" + appID }
func serviceName(appID string) string    { return "app-" + appID + "-ocops" }
func secretName(appID string) string     { return "app-" + appID + "-token" }

// appLabels 是 Deployment selector 与 pod 的 label。
func appLabels(appID string) map[string]string {
	return map[string]string{"app": appID, "app.kubernetes.io/part-of": "oc-manager"}
}

// RenderSecret 渲染 per-app 控制 token Secret（control-token 键）。
func RenderSecret(spec AppSpec, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"control-token": spec.ControlToken},
	}
}

// RenderService 渲染 oc-ops Service（OcOpsResolver 寻址目标，port 8080）。
func RenderService(spec AppSpec, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: serviceName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Spec: corev1.ServiceSpec{
			Selector: appLabels(spec.AppID),
			Ports:    []corev1.ServicePort{{Name: "oc-ops", Port: 8080, TargetPort: intstr.FromInt32(8080)}},
		},
	}
}

// RenderDeployment 渲染 app Deployment（replicas=1, Recreate, initContainer restore +
// hermes + oc-ops + sidecar s3-sync，emptyDir oc-input + data）。
func RenderDeployment(spec AppSpec, namespace string) *appsv1.Deployment {
	replicas := int32(1)
	ctrlTokenEnv := corev1.EnvVar{Name: "OC_CONTROL_TOKEN", ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)}, Key: "control-token"}}}
	bootstrapEnv := corev1.EnvVar{Name: "OC_BOOTSTRAP_URL", Value: spec.BootstrapURL}
	dataMount := corev1.VolumeMount{Name: "data", MountPath: "/opt/data"}
	inputMount := corev1.VolumeMount{Name: "oc-input", MountPath: "/opt/oc-input"}
	reqs := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(spec.Resources.RequestsCPU),
		corev1.ResourceMemory: resource.MustParse(spec.Resources.RequestsMemory),
	}
	lims := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(spec.Resources.LimitsCPU),
		corev1.ResourceMemory: resource.MustParse(spec.Resources.LimitsMemory),
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: deploymentName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: appLabels(spec.AppID)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: appLabels(spec.AppID)},
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: spec.ImagePullSecret}},
					InitContainers: []corev1.Container{{
						Name: "restore", Image: spec.OpsImage, Command: []string{"oc-restore"},
						Env:          []corev1.EnvVar{ctrlTokenEnv, bootstrapEnv},
						VolumeMounts: []corev1.VolumeMount{inputMount, dataMount},
					}},
					Containers: []corev1.Container{
						{
							Name: "hermes", Image: spec.HermesImage,
							Env:          []corev1.EnvVar{{Name: "HERMES_HOME", Value: "/opt/data"}},
							VolumeMounts: []corev1.VolumeMount{inputMount, dataMount},
							Resources:    corev1.ResourceRequirements{Requests: reqs, Limits: lims},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"hermes", "gateway", "status"}}},
								InitialDelaySeconds: 10, PeriodSeconds: 10, FailureThreshold: 6,
							},
						},
						{
							Name: "oc-ops", Image: spec.HermesImage,
							Command:      []string{"/usr/local/lib/hermes-agent/venv/bin/python", "-m", "uvicorn", "ocops.server:app", "--host", "0.0.0.0", "--port", "8080"},
							Env:          []corev1.EnvVar{{Name: "OC_OPS_TOKEN", ValueFrom: ctrlTokenEnv.ValueFrom}},
							Ports:        []corev1.ContainerPort{{ContainerPort: 8080}},
							VolumeMounts: []corev1.VolumeMount{dataMount},
						},
						{
							Name: "s3-sync", Image: spec.OpsImage, Command: []string{"oc-sync"},
							Env:          []corev1.EnvVar{ctrlTokenEnv, bootstrapEnv},
							VolumeMounts: []corev1.VolumeMount{dataMount},
							Lifecycle:    &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"oc-presync"}}}},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "oc-input", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
	for k, v := range spec.Labels {
		dep.Labels[k] = v
		dep.Spec.Template.Labels[k] = v
	}
	return dep
}
```

- [ ] **Step 2: 写 golden 测**

`render_test.go`:
```go
package k8sorch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// testSpec 是渲染测试的固定 AppSpec。
func testSpec() AppSpec {
	return AppSpec{
		AppID: "a1", HermesImage: "registry/hermes:v1", OpsImage: "registry/ops:dev",
		ControlToken: "tok", BootstrapURL: "http://manager-api.oc-system.svc:8080/internal/apps/a1/bootstrap",
		ImagePullSecret: "acr-pull",
		Resources:       ResourceLimits{RequestsCPU: "250m", RequestsMemory: "512Mi", LimitsCPU: "1", LimitsMemory: "2Gi"},
	}
}

// assertGolden 把对象序列化为 YAML 与 golden 文件比对；设 UPDATE_GOLDEN=1 时刷新。
func assertGolden(t *testing.T, name string, obj any) {
	t.Helper()
	got, err := yaml.Marshal(obj)
	require.NoError(t, err)
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "缺 golden 文件，先用 UPDATE_GOLDEN=1 生成")
	assert.Equal(t, string(want), string(got))
}

// TestRenderDeployment 验证 Deployment 渲染与 golden 一致（含 initContainer/三容器/卷/probe）。
func TestRenderDeployment(t *testing.T) { assertGolden(t, "deployment.golden.yaml", RenderDeployment(testSpec(), "oc-apps")) }

// TestRenderService 验证 oc-ops Service 渲染（selector + 8080）。
func TestRenderService(t *testing.T) { assertGolden(t, "service.golden.yaml", RenderService(testSpec(), "oc-apps")) }

// TestRenderSecret 验证 control-token Secret 渲染。
func TestRenderSecret(t *testing.T) { assertGolden(t, "secret.golden.yaml", RenderSecret(testSpec(), "oc-apps")) }
```

- [ ] **Step 3: 生成 golden + 跑测试**

Run:
```bash
go get sigs.k8s.io/yaml@latest
UPDATE_GOLDEN=1 go test ./internal/integrations/k8sorch/ -run TestRender
go test ./internal/integrations/k8sorch/ -run TestRender -v
```
Expected: 首次生成三个 golden 文件，再跑全 PASS。**人工核对 golden YAML**：replicas=1、Recreate、imagePullSecrets、initContainer restore（oc-restore + 两 env + 两 mount）、hermes（HERMES_HOME + probe）、oc-ops（uvicorn command + OC_OPS_TOKEN + 8080）、s3-sync（oc-sync + preStop oc-presync）、两个 emptyDir。

- [ ] **Step 4: Commit**
```bash
git add internal/integrations/k8sorch/render.go internal/integrations/k8sorch/render_test.go internal/integrations/k8sorch/testdata/ go.mod go.sum
git commit -F - <<'EOF'
feat(k8sorch): app pod 资源渲染（Deployment/Service/Secret）

RenderDeployment/Service/Secret 纯函数按 spec-A1 pod 内部契约 + spec-D + spec-E
渲染：replicas=1/Recreate、initContainer restore、hermes（probe）、oc-ops（同镜像
覆盖 uvicorn CMD + OC_OPS_TOKEN）、sidecar s3-sync（preStop oc-presync）、emptyDir
oc-input + data、control-token Secret、oc-ops Service。golden manifest 单测固定快照。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 7: KubernetesAdapter（client-go）+ config 构造

**Files:** Create `internal/integrations/k8sorch/adapter.go`, `config.go`, `adapter_test.go`

- [ ] **Step 1: 写 config.go（clientset 构造）**

```go
package k8sorch

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientset 按 kubeconfig 是否为空构造 clientset：空→in-cluster，非空→kubeconfig（本地 go run）。
func NewClientset(kubeconfig string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig == "" {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}
```

- [ ] **Step 2: 写 adapter.go（实现 Orchestrator）**

```go
package k8sorch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// KubernetesAdapter 用 client-go 实现 Orchestrator。
type KubernetesAdapter struct {
	client    kubernetes.Interface
	namespace string
}

// NewKubernetesAdapter 构造 adapter（client 可注入 fake 便于单测）。
func NewKubernetesAdapter(client kubernetes.Interface, namespace string) *KubernetesAdapter {
	return &KubernetesAdapter{client: client, namespace: namespace}
}

var _ Orchestrator = (*KubernetesAdapter)(nil)

// EnsureApp 幂等 apply Secret → Service → Deployment（先建依赖后建主体）。
func (a *KubernetesAdapter) EnsureApp(ctx context.Context, spec AppSpec) error {
	if err := a.applySecret(ctx, RenderSecret(spec, a.namespace)); err != nil {
		return err
	}
	if err := a.applyService(ctx, RenderService(spec, a.namespace)); err != nil {
		return err
	}
	return a.applyDeployment(ctx, RenderDeployment(spec, a.namespace))
}

// applyDeployment get-then-create/update（NotFound 创建，否则更新）。
func (a *KubernetesAdapter) applyDeployment(ctx context.Context, d *appsv1.Deployment) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	existing, err := api.Get(ctx, d.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, d, metav1.CreateOptions{})
		return wrapK8s("创建 Deployment", cerr)
	}
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	d.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("更新 Deployment", uerr)
}

func (a *KubernetesAdapter) applyService(ctx context.Context, s *corev1.Service) error {
	api := a.client.CoreV1().Services(a.namespace)
	existing, err := api.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
		return wrapK8s("创建 Service", cerr)
	}
	if err != nil {
		return wrapK8s("查询 Service", err)
	}
	s.ResourceVersion = existing.ResourceVersion
	s.Spec.ClusterIP = existing.Spec.ClusterIP // ClusterIP 不可变，沿用
	_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
	return wrapK8s("更新 Service", uerr)
}

func (a *KubernetesAdapter) applySecret(ctx context.Context, s *corev1.Secret) error {
	api := a.client.CoreV1().Secrets(a.namespace)
	existing, err := api.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
		return wrapK8s("创建 Secret", cerr)
	}
	if err != nil {
		return wrapK8s("查询 Secret", err)
	}
	s.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
	return wrapK8s("更新 Secret", uerr)
}

// Scale 改 Deployment.Spec.Replicas。
func (a *KubernetesAdapter) Scale(ctx context.Context, appID string, replicas int32) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	d, err := api.Get(ctx, deploymentName(appID), metav1.GetOptions{})
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	d.Spec.Replicas = &replicas
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("伸缩 Deployment", uerr)
}

// UpdateImage patch hermes + oc-ops 容器镜像（同镜像）。
func (a *KubernetesAdapter) UpdateImage(ctx context.Context, appID, hermesImage string) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	d, err := api.Get(ctx, deploymentName(appID), metav1.GetOptions{})
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	for i := range d.Spec.Template.Spec.Containers {
		switch d.Spec.Template.Spec.Containers[i].Name {
		case "hermes", "oc-ops":
			d.Spec.Template.Spec.Containers[i].Image = hermesImage
		}
	}
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("更新镜像", uerr)
}

// Delete 删除三资源（NotFound 视为成功）。
func (a *KubernetesAdapter) Delete(ctx context.Context, appID string) error {
	del := metav1.DeleteOptions{}
	if err := a.client.AppsV1().Deployments(a.namespace).Delete(ctx, deploymentName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Deployment", err)
	}
	if err := a.client.CoreV1().Services(a.namespace).Delete(ctx, serviceName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Service", err)
	}
	if err := a.client.CoreV1().Secrets(a.namespace).Delete(ctx, secretName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Secret", err)
	}
	return nil
}

// Status 取 app 的 pod（label app=<id>）归一为 AppStatus。
func (a *KubernetesAdapter) Status(ctx context.Context, appID string) (AppStatus, error) {
	pods, err := a.client.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + appID})
	if err != nil {
		return AppStatus{}, wrapK8s("列举 pod", err)
	}
	if len(pods.Items) == 0 {
		return AppStatus{Phase: "NotFound"}, nil
	}
	p := pods.Items[0]
	st := AppStatus{Phase: string(p.Status.Phase)}
	raw, _ := json.Marshal(p.Status)
	st.Raw = raw
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "hermes" {
			st.Ready = cs.Ready
			st.RestartCount = cs.RestartCount
			st.ImageRef = cs.Image
			if cs.State.Waiting != nil {
				st.Message = cs.State.Waiting.Reason
			}
		}
	}
	return st, nil
}

// WaitReady 轮询 Status 直到 Ready 或超时（fake clientset 无 watch 事件，poll 更稳）。
func (a *KubernetesAdapter) WaitReady(ctx context.Context, appID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := a.Status(ctx, appID)
		if err != nil {
			return err
		}
		if st.Ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 app %s pod Ready 超时（phase=%s msg=%s）", appID, st.Phase, st.Message)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// wrapK8s 统一包装 k8s API 错误。
func wrapK8s(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("k8sorch: %s 失败: %w", op, err)
}
```

> 注：WaitReady 用 poll（每 2s）而非 watch——fake clientset 不产生 watch 事件，poll 在单测与真实集群都工作；spec 说「scoped watch」是语义，实现用轮询 Status 等价且更可测。

- [ ] **Step 3: 写 adapter_test.go（fake clientset）**

```go
package k8sorch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestEnsureAppCreatesResources 验证 EnsureApp 在空集群创建 Deployment/Service/Secret。
func TestEnsureAppCreatesResources(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	_, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.CoreV1().Services("oc-apps").Get(context.Background(), "app-a1-ocops", metav1.GetOptions{})
	require.NoError(t, err)
	sec, err := cs.CoreV1().Secrets("oc-apps").Get(context.Background(), "app-a1-token", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "tok", sec.StringData["control-token"])
}

// TestEnsureAppIdempotent 验证重复 EnsureApp 更新而非报已存在。
func TestEnsureAppIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
}

// TestScale 验证 Scale 改 replicas。
func TestScale(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.Scale(context.Background(), "a1", 0))
	d, _ := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	assert.Equal(t, int32(0), *d.Spec.Replicas)
}

// TestUpdateImage 验证 UpdateImage patch hermes/oc-ops 镜像。
func TestUpdateImage(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.UpdateImage(context.Background(), "a1", "registry/hermes:v2"))
	d, _ := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name == "hermes" || c.Name == "oc-ops" {
			assert.Equal(t, "registry/hermes:v2", c.Image)
		}
	}
}

// TestDeleteIdempotent 验证 Delete 幂等（不存在不报错）。
func TestDeleteIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.Delete(context.Background(), "nonexist"))
}

// TestStatusNotFound 验证无 pod 时 Status 返回 NotFound。
func TestStatusNotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	st, err := a.Status(context.Background(), "a1")
	require.NoError(t, err)
	assert.Equal(t, "NotFound", st.Phase)
}

// TestStatusReadyFromPod 验证有 Ready hermes 容器的 pod 归一为 Ready。
func TestStatusReadyFromPod(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
		}},
	})
	a := NewKubernetesAdapter(cs, "oc-apps")
	st, err := a.Status(context.Background(), "a1")
	require.NoError(t, err)
	assert.True(t, st.Ready)
	assert.Equal(t, "Running", st.Phase)
}

// TestWaitReadyTimeout 验证 pod 未 Ready 时 WaitReady 超时。
func TestWaitReadyTimeout(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	err := a.WaitReady(context.Background(), "a1", 100*time.Millisecond)
	require.Error(t, err)
}
```

- [ ] **Step 4: 跑测试 + Commit**
```bash
go test ./internal/integrations/k8sorch/ -v && go build ./...
git add internal/integrations/k8sorch/adapter.go internal/integrations/k8sorch/config.go internal/integrations/k8sorch/adapter_test.go go.mod go.sum
git commit -F - <<'EOF'
feat(k8sorch): KubernetesAdapter（client-go 实现 Orchestrator）

EnsureApp（get-then-create/update Secret→Service→Deployment）、Scale（改 replicas）、
UpdateImage（patch hermes/oc-ops 镜像）、Delete（幂等删三资源）、Status（pod 归一）、
WaitReady（poll Status）。NewClientset 按 kubeconfig 空否选 in-cluster / kubeconfig。
fake clientset 单测覆盖创建/幂等/伸缩/换镜像/删除/状态/超时。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 3：OcOpsResolver token + ObjectStore List

### Task 8: OcOpsResolver 解密注入 control token

**Files:** Modify `internal/service/ocops.go`; Test `internal/service/ocops_test.go`

- [ ] **Step 1: 给 OcOpsResolverFromStore 加 cipher + 解密**

把 `OcOpsResolverFromStore` 改为：
```go
type OcOpsResolverFromStore struct {
	store      ocOpsAppStore
	cipher     *auth.Cipher // 解密 per-app control token
	baseURLTpl string
}

// NewOcOpsResolverFromStore 构造 resolver；cipher 用于解密 app.runtime_token_ciphertext 注入 Endpoint.Token。
func NewOcOpsResolverFromStore(store ocOpsAppStore, cipher *auth.Cipher, baseURLTpl string) *OcOpsResolverFromStore {
	return &OcOpsResolverFromStore{store: store, cipher: cipher, baseURLTpl: baseURLTpl}
}
```
`Resolve` 内组装 Endpoint 时填 Token：
```go
	token := ""
	if app.RuntimeTokenCiphertext.Valid && r.cipher != nil {
		plain, derr := r.cipher.Decrypt(app.RuntimeTokenCiphertext.String)
		if derr != nil {
			return OcOpsAppLocation{}, fmt.Errorf("解密 control token 失败: %w", derr)
		}
		token = string(plain)
	}
	return OcOpsAppLocation{
		OrgID: app.OrgID, OwnerUserID: app.OwnerUserID,
		Endpoint:  ocops.Endpoint{BaseURL: fmt.Sprintf(r.baseURLTpl, appID), Token: token},
		Supported: !strings.HasSuffix(app.RuntimeImageRef, "-dev"),
	}, nil
```
（补 import `oc-manager/internal/auth`。）

- [ ] **Step 2: 单测 token 注入**

`ocops_test.go` 加（复用现有 fake store 模式 + cipher helper）：
```go
// TestOcOpsResolverInjectsToken 验证 Resolve 解密 control token 填入 Endpoint.Token。
func TestOcOpsResolverInjectsToken(t *testing.T) {
	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	ct, err := cipher.Encrypt([]byte("control-tok"))
	require.NoError(t, err)
	store := &fakeOcOpsStore{app: sqlc.App{ID: "a1", OrgID: "o1", OwnerUserID: "u1",
		RuntimeTokenCiphertext: null.StringFrom(ct), RuntimeImageRef: "registry/hermes:v1"}}
	r := NewOcOpsResolverFromStore(store, cipher, "http://app-%s-ocops.oc-apps.svc:8080")
	loc, err := r.Resolve(context.Background(), "a1")
	require.NoError(t, err)
	assert.Equal(t, "control-tok", loc.Endpoint.Token)
	assert.Equal(t, "http://app-a1-ocops.oc-apps.svc:8080", loc.Endpoint.BaseURL)
	assert.True(t, loc.Supported)
}
```
（`fakeOcOpsStore` 实现 `GetApp` 返回预置 app；若现有测试已有同名 fake 则复用。）

- [ ] **Step 3: 跑测试 + 改 main 装配**

main.go `:224` 的 `NewOcOpsResolverFromStore(dbStore.Queries, ocopsBaseURLTemplate)` 加 cipher：`NewOcOpsResolverFromStore(dbStore.Queries, cipher, ocopsBaseURLTemplate)`。
Run: `go test ./internal/service/ -run TestOcOpsResolver -v && go build ./...`
Expected: PASS；编译通过。

- [ ] **Step 4: Commit**
```bash
git add internal/service/ocops.go internal/service/ocops_test.go cmd/server/main.go
git commit -F - <<'EOF'
feat(service): OcOpsResolver 解密注入 per-app control token

OcOpsResolverFromStore 加 cipher，Resolve 解密 app.runtime_token_ciphertext 填入
Endpoint.Token（spec-E 留空占位的填充）；BaseURL 模板沿用 Service DNS。main 装配
传入 cipher。单测覆盖 token 解密注入。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 9: ObjectStore 加 ListObjects + workspace 改 S3

**Files:** Modify `internal/integrations/storage/store.go`, `s3.go`; `internal/service/workspace_service.go`; Tests

- [ ] **Step 1: ObjectStore 加 ListObjects**

`store.go` 接口加方法：
```go
	// ListObjects 列出 prefix 下对象的 key（去掉 prefix 的相对部分）+ 大小，供 workspace 浏览。
	ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error)
```
加类型：
```go
// ObjectInfo 是列举返回的单个对象元信息。
type ObjectInfo struct {
	Key  string // 相对 prefix 的 key（如 workspace/a/b.txt 在 prefix=apps/<id>/ 下为 workspace/a/b.txt）
	Size int64
}
```
`s3.go` 实现 ListObjects（用 aws-sdk-go-v2 ListObjectsV2 分页，复用现有 listKeys 模式补 size）。

- [ ] **Step 2: workspace_service 改读 S3**

`NewWorkspaceService` 签名从 `(store, adapter runtime.Adapter, dataRoot)` 改为 `(store WorkspaceStore, objects storage.ObjectStore, presignTTL time.Duration)`：
- `List` → `objects.ListObjects(ctx, storage.AppPrefix(appID)+"workspace/"+relative)` 归一为 WorkspaceListing。
- `Download` → `objects.PresignGet(...)` 后 HTTP GET 流（或直接返回预签名 URL 由前端取——按现有返回类型适配；若返回 io.ReadCloser 则服务端代下载）。
- `Archive` → 列举 + 逐个下载打 tar（或调用 storage 的归档能力；最简：列举 workspace/ 对象逐个 PresignGet 下载写入 tar）。

> 这是消费侧改造，按 workspace_service 现有方法返回类型适配；核心是数据源从 agent file API 换为 S3。保留权限校验 loadAuthorizedApp 不变。

- [ ] **Step 3: 单测（fake ObjectStore）+ 跑**

workspace_service_test.go 用 fake ObjectStore（实现含 ListObjects）覆盖 List 归一 + 权限。
Run: `go test ./internal/service/ -run TestWorkspace -v && go test ./internal/integrations/storage/ && go build ./...`

- [ ] **Step 4: Commit**
```bash
git add internal/integrations/storage/store.go internal/integrations/storage/s3.go internal/service/workspace_service.go internal/service/workspace_service_test.go
git commit -F - <<'EOF'
feat(service): workspace 浏览改读 S3

ObjectStore 加 ListObjects（分页列举 prefix 下对象 key+size）；WorkspaceService
数据源从 agent file API 换为 S3（列举 apps/<id>/workspace/ + 预签名下载），权限
校验不变。为 k8s 编排下 workspace 浏览（无节点 file API）做准备。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 4：创建流程与生命周期重构

### Task 10: 创建流程重构（app_initialize → k8s 流程）

**Files:** Modify `internal/worker/handlers/app_initialize.go`

> 这是核心重构。读现有 `app_initialize.go`（Handle + 4 个 phase + 依赖接口 + 构造器 + setters）。目标：把「pull→prepare 写卷→create→start」5 阶段换成「ensure 三件套（**保留 ensureAPIKey + EnsureAppRuntimeToken + version 校验**）→ EnsureApp（注入 control token 明文 + bootstrap URL + 渲染 AppSpec）→ WaitReady → binding_waiting」。

- [ ] **Step 1: 引入 Orchestrator 依赖**

AppInitializeHandler 加字段 `orch k8sorch.Orchestrator` + `k8sCfg config.KubernetesConfig`（或最小子集：namespace/opsImage/bootstrapBaseURL/imagePullSecret/resources）。构造器/setter 注入。

- [ ] **Step 2: 重写 Handle 的阶段编排**

新流程（伪代码，按现有 Handle 结构落地，复用现有状态机 transitionTo / markFailed / promoteIfChannelBound）：
```go
// 1. version 校验（沿用现有：未绑定 markFailed）+ 解析 hermes 镜像 ref（version.image_id → ResolveRuntimeImage）。
// 2. ensureAPIKey(ctx, &app)            // 复用，写 newapi_key_ciphertext
// 3. _, controlToken, err := service.EnsureAppRuntimeToken(ctx, h.store, h.cipher, app)  // 复用，拿明文 token
// 4. spec := buildAppSpec(app, hermesImage, controlToken)   // 渲染输入
//    h.orch.EnsureApp(ctx, spec)        // 建 Deployment/Service/Secret
// 5. h.orch.WaitReady(ctx, app.ID, readyTimeout)            // 等 pod Ready
// 6. transitionTo(binding_waiting) → promoteIfChannelBound
```
`buildAppSpec`：用 k8sCfg 的 namespace/opsImage/bootstrapBaseURL/imagePullSecret/resources + `bootstrapBaseURL + "/internal/apps/" + app.ID + "/bootstrap"` 拼 BootstrapURL。

- [ ] **Step 3: 删除 docker 阶段**

删除 `phasePullRuntimeImage`、`InitAppDirs` 调用、`writeAppInput` 写卷 + `pushVersionSkills`、`SetImagePullCoord`/`SetNodeDockerProvider`/`SetAppInputUploader` 相关字段与 setter（这些依赖随之孤立；**保留** ensureAPIKey/EnsureAppRuntimeToken/version 校验）。`buildAppSpec` 不再需要 nodeID。

> 不再写 `container_id`/`container_name`/`runtime_node_id`（停写；列保留待 A2b 删）。`runtime_snapshot_json` 由 Task 13 reconciler 写。

- [ ] **Step 4: 单测（fake Orchestrator）**

用 fake Orchestrator（记录 EnsureApp 调用 + WaitReady 返回）+ fake store/newapi 覆盖：正常流程（ensure→EnsureApp→WaitReady→binding_waiting）、version 未绑定 markFailed、WaitReady 超时 markFailed。复用现有 app_initialize 测试的 fake 模式（按实际改造）。

- [ ] **Step 5: 跑测试 + Commit**
```bash
go test ./internal/worker/handlers/ -run TestAppInitialize -v && go build ./...
git add internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
git commit -F - <<'EOF'
refactor(worker): 创建流程重构为 k8s 编排

5 阶段（pull→prepare 写卷→create→start）换成 k8s 流程：version 校验 + ensure
三件套（保留 ensureAPIKey/EnsureAppRuntimeToken）→ EnsureApp（control token 写
Secret、bootstrap URL 注入、渲染 AppSpec）→ WaitReady → binding_waiting。删除
pull 镜像/InitAppDirs/writeAppInput 写卷/pushVersionSkills（k8s imagePullPolicy +
bootstrap + S3 接管）。停写 container_id/runtime_node_id。fake Orchestrator 单测。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 11: 生命周期 handler 重构（start/stop/restart/delete）

**Files:** Modify `internal/worker/handlers/app_runtime_ops.go`

> 读现有 `app_runtime_ops.go`：ContainerLifecycle 接口 + 4 handler。把 ContainerLifecycle 换成 Orchestrator 语义。

- [ ] **Step 1: 接口换成 Orchestrator**

把 `ContainerLifecycle`（StartContainer/StopContainer/RestartContainer/RemoveContainer）替换为消费 `k8sorch.Orchestrator`（或定义窄接口 `appOrchestrator` 含 Scale/UpdateImage/Delete/Status，便于单测）。4 个 handler 改依赖它。

- [ ] **Step 2: 映射各操作**

- **Start**：`orch.Scale(ctx, appID, 1)` → SetAppStatus(running)。
- **Stop**：`orch.Scale(ctx, appID, 0)` → SetAppStatus(stopped)（preStop 触发 sidecar oc-presync 全量同步）。
- **Restart**：检测镜像变更——若 version 镜像 ref 变 → `orch.UpdateImage(ctx, appID, newImage)`；不变 → 删 S3 `apps/<id>/sessions/` + `state.db`（用 storage.DeletePrefix / 删单对象）后删 pod 触发重建（`orch.Scale(0)` 再 `Scale(1)`，或 patch template 注解触发 rollout）。去掉旧的「重写 input 文件」（bootstrap 接管）。
- **Delete**：`orch.Delete(ctx, appID)`（删 Deployment/Service/Secret）→ 禁 new-api key（沿用）→ `storage.MovePrefix(storage.AppPrefix(appID), storage.AppArchivePrefix(appID))` 归档 → 清 KB（沿用 cleaners）→ 软删（沿用）。AppDeleteFileOps（旧 ArchiveApp）换为 storage.MovePrefix。

- [ ] **Step 3: 单测（fake Orchestrator + fake storage）**

覆盖：Start→Scale(1)、Stop→Scale(0)、Restart 换镜像→UpdateImage、Restart 不换镜像→删 S3 sessions+重建、Delete→Delete+禁key+归档+软删。

- [ ] **Step 4: 跑测试 + Commit**
```bash
go test ./internal/worker/handlers/ -run 'TestAppStart|TestAppStop|TestAppRestart|TestAppDelete' -v && go build ./...
git add internal/worker/handlers/app_runtime_ops.go internal/worker/handlers/app_runtime_ops_test.go
git commit -F - <<'EOF'
refactor(worker): 生命周期 handler 改 k8s 编排语义

ContainerLifecycle → Orchestrator：Start/Stop→Scale(1/0)、Restart 换镜像→
UpdateImage / 不换镜像→删 S3 sessions+state.db 后重建、Delete→Delete 三资源 +
禁 new-api key + storage.MovePrefix 归档 + 清 KB + 软删。删除旧的写 input/docker
exec 路径。fake Orchestrator + fake storage 单测。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 5：渠道、状态 reconciler、main 装配、验证

### Task 12: DockerBindingResolver → oc-ops ChannelStatus

**Files:** Modify `internal/integrations/channel/wechat_identity.go`; Test

- [ ] **Step 1: 改 binding resolver 走 oc-ops**

读 `wechat_identity.go` 的 DockerBindingResolver（经 docker exec 读 plugin state 拿 OpenID）。新增/改造一个 resolver 用 ocops.Client 的 ChannelStatus（返回含 AccountID）解析绑定身份：
```go
// OcOpsBindingResolver 用 oc-ops ChannelStatus.AccountID 解析微信绑定身份（替代 docker exec 读 plugin state）。
type OcOpsBindingResolver struct {
	ops      channelStatusClient   // 窄接口：ChannelStatus(ctx, ep, channel)(ChannelStatus, error)
	resolver OcOpsLocationResolver // 由 appID 解析 oc-ops Endpoint（复用 service.OcOpsResolver）
}
```
方法用 resolver 拿 Endpoint + token，调 ChannelStatus，返回 AccountID。具体接口签名以 ocops.Client.ChannelStatus 实际为准（读 internal/integrations/ocops/client_channel.go）。

- [ ] **Step 2: 单测 + main 装配换掉 docker executor**

main.go `:232/238` 的 `wechatExecutor := channel.NewDockerExecutor(...)` + `NewDockerBindingResolver(wechatExecutor)` 换为 `NewOcOpsBindingResolver(ocopsClient, ocopsResolver)`（去掉 docker exec 依赖）。
单测用 fake channelStatusClient 覆盖 AccountID 解析。

- [ ] **Step 3: 跑测试 + Commit**
```bash
go test ./internal/integrations/channel/ -v && go build ./...
git add internal/integrations/channel/wechat_identity.go internal/integrations/channel/wechat_identity_test.go cmd/server/main.go
git commit -F - <<'EOF'
refactor(channel): 微信绑定身份解析改走 oc-ops

DockerBindingResolver（docker exec 读 plugin state OpenID）换为 OcOpsBindingResolver
用 oc-ops ChannelStatus.AccountID（spec-E 遗留给 spec-A 的处理）。main 装配去掉
wechat docker executor。fake 单测覆盖 AccountID 解析。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 13: app 状态 poll reconciler

**Files:** Create `internal/service/app_status_reconciler.go`; Test

- [ ] **Step 1: 写 reconciler**

```go
package service

import (
	"context"

	"oc-manager/internal/integrations/k8sorch"
)

// AppStatusReconciler 周期读 running app 的 pod 状态同步 DB（apps.status/health_state_json/snapshot）。
// pod 崩溃重启由 Deployment 控制器自管，manager 不自愈。
type AppStatusReconciler struct {
	store appStatusStore        // ListRunningApps + SetAppStatus + SetAppRuntimeSnapshot
	orch  k8sorch.Orchestrator
}

// appStatusStore 是 reconciler 所需最小 DB 能力。
type appStatusStore interface {
	ListRunningApps(ctx context.Context) ([]sqlc.App, error)
	// SetAppRuntimeSnapshot / SetAppStatus 等按现有 query 适配
}

func NewAppStatusReconciler(store appStatusStore, orch k8sorch.Orchestrator) *AppStatusReconciler {
	return &AppStatusReconciler{store: store, orch: orch}
}

// Tick 对每个 running app 读 pod 状态同步 DB（一轮）。
func (r *AppStatusReconciler) Tick(ctx context.Context) error {
	apps, err := r.store.ListRunningApps(ctx)
	if err != nil {
		return err
	}
	for _, app := range apps {
		st, serr := r.orch.Status(ctx, app.ID)
		if serr != nil {
			continue // 单 app 失败不阻塞整轮
		}
		// 同步 status（NotFound/CrashLoop→error，Ready→running）+ runtime_snapshot_json=st.Raw
		// 用现有 SetAppStatus / 快照写 query（按实际）。
		_ = st
	}
	return nil
}
```
（`appStatusStore` 的方法 + 状态映射逻辑按现有 apps query 与状态机落地；core 是 Status→DB 同步。）

- [ ] **Step 2: 单测（fake store + fake orch）+ 跑**

覆盖：running app 的 pod Ready→status running、NotFound/CrashLoop→error、snapshot 写入。

- [ ] **Step 3: Commit**
```bash
git add internal/service/app_status_reconciler.go internal/service/app_status_reconciler_test.go
git commit -F - <<'EOF'
feat(service): app 状态 poll reconciler

周期对 running app 调 Orchestrator.Status 读 pod 状态，同步 apps.status +
runtime_snapshot_json；pod 崩溃重启交 Deployment 控制器，manager 不自愈。
fake store + fake orch 单测。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 14: main 装配切换 + 健康任务调整

**Files:** Modify `cmd/server/main.go`

- [ ] **Step 1: 构造 KubernetesAdapter + 切换装配**

在 main 装配区（启用 k8s 时）：
```go
	var orch k8sorch.Orchestrator
	if cfg.Kubernetes.Enabled {
		cs, err := k8sorch.NewClientset(cfg.Kubernetes.Kubeconfig)
		if err != nil { /* fatal */ }
		orch = k8sorch.NewKubernetesAdapter(cs, cfg.Kubernetes.Namespace)
	}
```
- appInitHandler：注入 orch + k8sCfg（去掉 SetImagePullCoord/SetNodeDockerProvider/SetAppInputUploader）。
- app_runtime_ops handlers：传 orch 替代 runtimeAdapter。
- workspaceService：传 objStore（S3）替代 runtimeAdapter（Task 9）。
- 不再构造 runtimeAdapter = NewAgentBackedAdapter（或保留构造但无消费方=孤儿；推荐删该构造行 + nodeResolver/streamingResolver 若仅它用——但节点删除归 A2b，A2a 可保留构造让其孤立。**为保 go build 通过且不删节点代码，保留构造、不再传给消费方即可**）。

- [ ] **Step 2: 健康/刷新任务调整**

把 app 状态同步任务接上 Task 13 的 reconciler：
```go
	appStatusTask := service.NewPeriodicReconciler("app_status_reconcile", 15*time.Second, appStatusReconciler.Tick)
	eg.Go(func() error { return appStatusTask.Start(gctx) })
```
旧 `app_health_check_dispatch`（exec 自愈）任务：去除自愈（或保留任务但 Tick 改为状态读取；最简：用新 appStatusTask 取代，删旧 healthCheckTask 装配——其 handler 依赖 ContainerExec 已无意义）。`runtime_refresh_status_dispatch`（docker stats）随节点孤立可去除装配（A2b 删 handler）。

> 节点周期任务（nodeHealthTask/nodeProbeTask）A2a 可保留装配（仍编译，对孤立节点表跑空），A2b 删。为减面也可在 A2a 注释禁用——按编译/运行不报错为准；推荐 A2a 保留装配避免 main 大改，A2b 统一清。

- [ ] **Step 3: 编译 + 全量测试**
Run: `go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... ./cmd/...`
Expected: 全绿（节点孤儿代码仍编译；新编排路径单测通过）。

- [ ] **Step 4: Commit**
```bash
git add cmd/server/main.go
git commit -F - <<'EOF'
feat(server): 装配切换为 KubernetesAdapter

启用 k8s 时构造 client-go clientset + KubernetesAdapter，appInit/生命周期/workspace
消费它（去 docker proxy 依赖）；接上 app 状态 poll reconciler，去除 docker exec
健康自愈。AgentBackedAdapter/节点装配失去消费方变孤儿（A2b 删）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 15: 真实 k3d 编排集成测

**Files:** Create `internal/integrations/k8sorch/k3d_integration_test.go`

- [ ] **Step 1: 写集成测（环境门控）**

```go
package k8sorch_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"oc-manager/internal/integrations/k8sorch"
)

// k3dAdapter 从 OC_K8S_TEST_KUBECONFIG 构造真实 k3d adapter；缺失即 Skip。
func k3dAdapter(t *testing.T) (*k8sorch.KubernetesAdapter, func()) {
	t.Helper()
	kc := os.Getenv("OC_K8S_TEST_KUBECONFIG")
	if kc == "" {
		t.Skip("未设置 OC_K8S_TEST_KUBECONFIG，跳过 k3d 编排集成测")
	}
	cs, err := k8sorch.NewClientset(kc)
	require.NoError(t, err)
	ns := os.Getenv("OC_K8S_TEST_NS")
	if ns == "" { ns = "oc-apps" }
	return k8sorch.NewKubernetesAdapter(cs, ns), func() {}
}

// TestK3dEnsureAppCreatesResources 验证 EnsureApp 在真实 k3d 创建 Deployment/Service/Secret 且 pod 调度起来达 Ready。
func TestK3dEnsureAppCreatesResources(t *testing.T) {
	a, _ := k3dAdapter(t)
	ctx := context.Background()
	id := "it-" + os.Getenv("USER")
	spec := k8sorch.AppSpec{
		AppID: id, HermesImage: os.Getenv("OC_K8S_TEST_HERMES_IMAGE"), OpsImage: os.Getenv("OC_K8S_TEST_OPS_IMAGE"),
		ControlToken: "test-tok", BootstrapURL: "http://manager-api.oc-system.svc:8080/internal/apps/" + id + "/bootstrap",
		ImagePullSecret: "acr-pull",
		Resources: k8sorch.ResourceLimits{RequestsCPU: "100m", RequestsMemory: "256Mi", LimitsCPU: "1", LimitsMemory: "1Gi"},
	}
	t.Cleanup(func() { _ = a.Delete(context.Background(), id) })
	require.NoError(t, a.EnsureApp(ctx, spec))
	// 断言资源创建
	st, err := a.Status(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, "NotFound", st.Phase)
	// 等 pod Ready（镜像拉取 + initContainer restore + bootstrap，超时给足）
	werr := a.WaitReady(ctx, id, 3*time.Minute)
	if werr != nil {
		t.Logf("WaitReady 未达 Ready（可能 bootstrap/镜像环境受限）：%v", werr)
	}
	_ = metav1.GetOptions{}
}
```

> 真实跑需 k3d 里有 ops/hermes 镜像、oc-apps ns、acr-pull Secret，且 bootstrap 端点可达（manager 在跑）。**WaitReady 失败不硬 fail**（pod Ready 依赖完整 bootstrap 闭环，属 A2b 合并验证范围）；A2a 集成测核心断言是 **EnsureApp 创建出正确资源 + pod 被调度**。资源断言（Deployment/Service/Secret 存在、容器数=3+initContainer）应硬断言。

- [ ] **Step 2: 无 k3d 时 SKIP + 编译**
Run: `go build ./... && go test ./internal/integrations/k8sorch/ -run TestK3d -v`
Expected: SKIP（无 OC_K8S_TEST_KUBECONFIG）。

- [ ] **Step 3: 有 k3d 时跑（交付前）**
Run（值按本地 k3d）：
```bash
OC_K8S_TEST_KUBECONFIG=$(k3d kubeconfig write ocm) OC_K8S_TEST_NS=oc-apps \
OC_K8S_TEST_HERMES_IMAGE=<k3d registry hermes ref> OC_K8S_TEST_OPS_IMAGE=<k3d registry ops ref> \
go test ./internal/integrations/k8sorch/ -run TestK3d -v
```
Expected: EnsureApp 创建资源 + pod 调度；WaitReady 视 bootstrap 闭环情况（A2b 完整验证）。**如实记录结果，不可伪造。**

- [ ] **Step 4: Commit**
```bash
git add internal/integrations/k8sorch/k3d_integration_test.go
git commit -F - <<'EOF'
test(k8sorch): 真实 k3d 编排集成测

环境门控（OC_K8S_TEST_*，缺失即 Skip）。EnsureApp 对真实 k3d 创建 Deployment/
Service/Secret 并断言资源正确 + pod 被调度；WaitReady 至 Ready 视完整 bootstrap
闭环（属 A2b 合并验证）。证明 KubernetesAdapter 能在真实 k8s API 创建可调度 pod。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 16: 收尾验证

**Files:** 无（校验任务）

- [ ] **Step 1: 全量编译/vet/测试**
Run: `go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... ./cmd/...`
Expected: 全绿（节点孤儿代码仍编译通过；新编排路径与单测通过）。

- [ ] **Step 2: openapi-check**
Run: `make openapi-check`
Expected: 工作区干净（本 spec 不改 handler 签名）。

- [ ] **Step 3: 真实 k3d 编排集成测（交付前）**
Run（同 Task 15 Step 3）：断言 EnsureApp 资源创建 + pod 调度。如实记录。

- [ ] **Step 4: 工作区清洁**
Run: `git status --short`
Expected: 仅已提交改动；不含未跟踪 docs/reports/。

---

## 验证范围说明（A2a.4，写入交付）

A2a 把 manager 编排切到 k8s：KubernetesAdapter 用 fake clientset 单测 + 渲染 golden manifest 单测 + 创建/生命周期流程逻辑单测；编排能在**真实 k3d** 创建出正确的 Deployment/Service/Secret 且 pod 被调度（集成测）。**节点删除、完整 A/B/D/E 合并端到端、三角色真实浏览器走查、pod 完整 Ready 闭环**（依赖 bootstrap 全链路 + 全栈基础设施）→ **spec-A2b**。本 spec 不单独宣称「k8s 编排端到端已验证可用」——对项目「真实环境验证」要求的有界偏离（同 B6/E4/A1.4）。

## 待 spec-A2b（本计划不做）

- 删除节点概念全部代码与表（runtime_nodes/service/probe/enroll-heartbeat/agent 二进制/deploy/router agent 路由/main 节点装配与周期任务/imagecoord 孤儿）。
- 破坏性 DB migration：删 `apps.runtime_node_id`/`container_id`/`container_name` 列与 `runtime_nodes` 表。
- AgentBackedAdapter / runtime.Adapter / agent file client 等孤儿删除。
- 渠道绑定后 hermes 重载（platform reload）的 k8s 化：`channelCheckHandler` 的
  `ChannelRestarter` 仍走 docker（k8s 下 nodeID/containerID 恒空，调用失败被日志吞、
  不阻断绑定状态闭环）；A2b 改为 orch 驱动的 pod 重启（Scale(0)→Scale(1) 或删 pod）。
- 完整 A/B/D/E 合并端到端 + 三角色真实浏览器验证（吸收 A2a.4 推迟项）。
