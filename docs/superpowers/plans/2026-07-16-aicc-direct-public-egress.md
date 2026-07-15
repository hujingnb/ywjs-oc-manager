# AICC 直连公网检索实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 AICC 保留 Hermes 原生只读网页检索能力并直接访问公网，移除对专用受控代理出口的配置和启动依赖。

**Architecture:** Hermes 的 `web_search` / `web_extract` 是原生工具，不新增替代工具。Kubernetes 为 AICC Pod 保留逐应用 egress NetworkPolicy：DNS、manager-api、new-api 按既有 Pod 选择器放行，公网改为直接放行 TCP 80/443；工具策略继续阻止终端、文件、进程、登录、表单和网络写能力。删除仅为专用代理存在的配置字段、初始化失败条件、环境变量和部署样例。

**Tech Stack:** Go、Kubernetes API、Testify、Python pytest、Playwright Chrome Stable。

---

## 文件结构

- `internal/config/config.go`：移除 `kubernetes.aicc_egress_proxy_url`。
- `internal/worker/handlers/app_initialize.go`：移除空代理 URL 的初始化拒绝及字段传递。
- `internal/integrations/k8sorch/orchestrator.go`：移除 AICC 专属代理字段。
- `internal/integrations/k8sorch/render.go`：AICC 不注入 HTTP(S)_PROXY；NetworkPolicy 放行公网 TCP 80/443。
- `internal/integrations/k8sorch/render_test.go`、`internal/integrations/k8sorch/testdata/deployment-aicc.golden.yaml`：验证直接公网出口且没有代理环境变量。
- `deploy/k8s/{local,prod}/secret.yaml`、`deploy/k8s/prod/secret.example.yaml`：删除废弃配置键和说明。
- `docs/testing/aicc-conversation-{requirement-matrix,validation-report}.md`：更新验收前置条件。

### Task 1: 以失败测试定义 AICC 的直连公网 NetworkPolicy

**Files:**
- Modify: `internal/integrations/k8sorch/render_test.go:58-147`
- Modify: `internal/integrations/k8sorch/render.go:20-112,218-231`
- Modify: `internal/integrations/k8sorch/testdata/deployment-aicc.golden.yaml`

- [ ] **Step 1: 将 AICC 渲染测试改为直连公网预期**

```go
// 客服依赖 Hermes 原生只读网页检索；其 Pod 直接访问 HTTP/HTTPS 公网，
// 不应再强制注入专属代理配置。
require.Len(t, policy.Spec.Egress, 4)
public := policy.Spec.Egress[3]
require.Len(t, public.To, 1)
require.NotNil(t, public.To[0].IPBlock)
assert.Equal(t, "0.0.0.0/0", public.To[0].IPBlock.CIDR)
assert.Equal(t, int32(80), public.Ports[0].Port.IntVal)
assert.Equal(t, int32(443), public.Ports[1].Port.IntVal)
assert.Nil(t, envByName(hermes, "HTTP_PROXY"))
assert.Nil(t, envByName(hermes, "HTTPS_PROXY"))
assert.Nil(t, envByName(hermes, "NO_PROXY"))
```

从 `TestRenderDeploymentAICC` 删除 `spec.AICCEgressProxyURL = ...`，并更新 golden 预期。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/integrations/k8sorch -run 'TestRenderDeploymentAICC|TestRenderAICCNetworkPolicy' -count=1`

Expected: FAIL；当前实现仍渲染专属代理 Pod 选择器及 HTTP(S)_PROXY。

- [ ] **Step 3: 最小化调整渲染实现**

```go
publicInternet := networkingv1.NetworkPolicyPeer{
    IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"},
}
// 在既有 DNS、manager-api、new-api 规则之后加入：
{To: []networkingv1.NetworkPolicyPeer{publicInternet}, Ports: []networkingv1.NetworkPolicyPort{
    port(protocolTCP, 80), port(protocolTCP, 443),
}},
```

删除 `aiccWebEgressNoProxy` 和 AICC 专用 `runtimeProxyEnv` 分支。不得改变标准应用的 `PodProxy` 注入逻辑。

- [ ] **Step 4: 更新 golden 并运行渲染测试**

Run: `UPDATE_GOLDEN=1 go test ./internal/integrations/k8sorch -run TestRenderDeploymentAICC -count=1 && go test ./internal/integrations/k8sorch -count=1`

Expected: PASS；golden 中没有 AICC 代理环境变量，NetworkPolicy 仅允许公网 TCP 80/443。

- [ ] **Step 5: 提交渲染边界变更**

```bash
git add internal/integrations/k8sorch/render.go internal/integrations/k8sorch/render_test.go internal/integrations/k8sorch/testdata/deployment-aicc.golden.yaml
git commit -m "feat(aicc): 允许原生检索直连公网" -m "AICC 保留 Hermes 原生只读网页检索，移除专属代理环境变量，并在逐应用 NetworkPolicy 中直接放行 HTTP/HTTPS 公网出口。"
```

### Task 2: 删除代理配置和初始化失败路径

**Files:**
- Modify: `internal/config/config.go:322-337`
- Modify: `internal/worker/handlers/app_initialize.go:120-135,385-400,505-520`
- Modify: `internal/integrations/k8sorch/orchestrator.go:55-70`
- Test: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1: 添加“无代理配置仍可初始化 AICC”的失败测试**

在 `app_initialize_test.go` 复用 AICC 初始化 fixture，不设置代理字段：

```go
// 客服网页检索使用 Hermes 原生公网能力，不能因不存在专属代理而拒绝初始化。
func TestInitializeAICCDoesNotRequireEgressProxy(t *testing.T) {
    err := handler.handle(context.Background(), job)
    require.NoError(t, err)
    require.Len(t, orchestrator.created, 1)
    assert.Equal(t, domain.AppTypeAICC, orchestrator.created[0].AppType)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/worker/handlers -run TestInitializeAICCDoesNotRequireEgressProxy -count=1`

Expected: FAIL，错误包含 `AICC_EGRESS_PROXY_REQUIRED`。

- [ ] **Step 3: 删除专属代理字段与拒绝逻辑**

从 `KubernetesConfig`、`k8sorch.AppSpec` 和初始化 handler 删除 `AICCEgressProxyURL`；删除 `strings.TrimSpace(...AICCEgressProxyURL...)` 拒绝分支及其错误。若 `strings` 因此不再使用则删除 import。普通 `PodProxy` 必须保留。

- [ ] **Step 4: 运行 worker 与编排测试**

Run: `go test ./internal/worker/handlers ./internal/integrations/k8sorch -count=1`

Expected: PASS。

- [ ] **Step 5: 提交配置移除**

```bash
git add internal/config/config.go internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go internal/integrations/k8sorch/orchestrator.go
git commit -m "fix(aicc): 移除检索代理启动依赖" -m "删除 AICC 专属代理配置和空配置拒绝逻辑，使直连公网的 Hermes 原生网页检索可正常初始化。"
```

### Task 3: 收敛部署样例、验证说明与真实 Chrome 验收

**Files:**
- Modify: `deploy/k8s/local/secret.yaml:150-170`
- Modify: `deploy/k8s/prod/secret.yaml:115-135`
- Modify: `deploy/k8s/prod/secret.example.yaml:125-145`
- Modify: `docs/testing/aicc-conversation-requirement-matrix.md`
- Modify: `docs/testing/aicc-conversation-validation-report.md`
- Test: `web/tests/e2e/aicc-conversation-security.spec.ts`

- [ ] **Step 1: 删除三个部署配置中的废弃键**

删除 `aicc_egress_proxy_url` 及其代理 token、Service label 和“留空拒绝启动”说明。保留普通 `pod_proxy` 配置。

- [ ] **Step 2: 更新验收要求和报告前置条件**

矩阵新增或修订 AICC-NET：Hermes 原生网页检索直接访问公网 TCP 80/443，渲染结果无 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`，且 terminal/file/process/browser action 工具仍不可见。报告将“部署受控网页检索代理”替换为“公网 DNS 和 TCP 80/443 可达”。

- [ ] **Step 3: 运行静态与契约验证**

Run: `go test ./internal/... ./cmd/server -count=1 && PYTHONPATH=runtime/hermes/hermes-aicc pytest -q runtime/hermes/hermes-aicc/tests && make openapi-check && git diff --check`

Expected: PASS；OpenAPI 无漂移。

- [ ] **Step 4: 使用本地 k3d 和 Chrome Stable 执行 AICC E2E**

先确认 current context 为 `k3d-ocm`，仅使用本地部署文件滚动更新。运行：

```bash
cd web
OCM_AICC_CONVERSATION_E2E=1 OCM_AICC_KNOWLEDGE_FIXTURE=1 OCM_AICC_FAULT_INJECTION=1 \
npm run test:e2e -- --project=chrome aicc-conversation-security.spec.ts aicc-conversation-intent.spec.ts aicc-conversation-runtime.spec.ts
```

Expected: 34 个 Chrome Stable 场景执行，不因代理 URL 或代理 Service 缺失跳过/失败；若知识 fixture、模型或公网不可用，报告实际阻塞原因，不得将跳过标记为通过。

- [ ] **Step 5: 提交验证和文档变更**

```bash
git add deploy/k8s/local/secret.yaml deploy/k8s/prod/secret.yaml deploy/k8s/prod/secret.example.yaml docs/testing/aicc-conversation-requirement-matrix.md docs/testing/aicc-conversation-validation-report.md
git commit -m "docs(aicc): 更新直连公网验证前置条件" -m "移除受控检索代理配置样例，明确 Hermes 原生网页检索的公网连通要求及真实 Chrome 验收结果。"
```
