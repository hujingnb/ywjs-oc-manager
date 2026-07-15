# 共享 Firecrawl 网页提取服务实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在独立无持久化 namespace 部署可弹性扩缩的 Firecrawl，并让 AICC 与普通 Hermes 在启动时统一获得 DDGS 搜索和 Firecrawl 正文提取能力。

**Architecture:** `oc-firecrawl` 部署固定 Firecrawl 版本的 API、scrape/extract/NuQ/prefetch worker、Playwright 与 Redis/RabbitMQ/PostgreSQL 临时依赖。六个无状态计算组件分别 HPA；依赖组件仅用 `emptyDir`。所有 Hermes renderer 启动时写入 DDGS 搜索和 Firecrawl 提取配置，Deployment 只向 Hermes 注入内部服务 URL；AICC 白名单不变。

**Tech Stack:** Kubernetes Deployment/Service/HPA/NetworkPolicy、Firecrawl、Go Testify、Python pytest、Playwright Chrome Stable。

---

## 文件结构

- `deploy/k8s/{local,prod}/00-namespace.yaml`：增加 `oc-firecrawl`。
- `deploy/k8s/{local,prod}/firecrawl.yaml`：组件、HPA、NetworkPolicy、临时卷。
- `deploy/k8s/contracts/check-firecrawl.sh`：部署结构静态检查。
- `runtime/hermes/*/renderer/render_config_yaml.py` 及测试：所有 variant 的启动网页后端。
- `internal/integrations/k8sorch/render.go`、测试与 golden：注入 Firecrawl URL。
- `docs/testing/firecrawl-shared-validation-report.md`：真实验证证据。

### Task 1: 部署无持久化、可扩缩 Firecrawl

**Files:**
- Modify: `deploy/k8s/local/00-namespace.yaml`
- Modify: `deploy/k8s/prod/00-namespace.yaml`
- Create: `deploy/k8s/local/firecrawl.yaml`
- Create: `deploy/k8s/prod/firecrawl.yaml`
- Create: `deploy/k8s/contracts/check-firecrawl.sh`

- [ ] **Step 1: 写失败的结构检查**

```bash
kubectl apply --dry-run=client -f "$1" >/dev/null
! rg -q 'PersistentVolumeClaim|kind: PersistentVolume' "$1"
test "$(rg -c '^kind: HorizontalPodAutoscaler$' "$1")" -eq 6
```

Run: `bash deploy/k8s/contracts/check-firecrawl.sh deploy/k8s/local/firecrawl.yaml`

Expected: FAIL，manifest 尚不存在。

- [ ] **Step 2: 创建 namespace 与组件**

在两个 namespace manifest 添加 `oc-firecrawl`。使用固定 Firecrawl image tag（不得 `latest`）创建 API、scrape worker、extract worker、NuQ worker、NuQ prefetch worker、Playwright Deployment，以及 Redis、RabbitMQ、PostgreSQL 单副本 Deployment。后 3 个使用 `emptyDir: {}`，不得创建 PVC、S3 或 Ingress。

每个计算 Deployment 都有 `autoscaling/v2` HPA：`minReplicas: 1`；API 最大 3、四类 worker 最大 5、Playwright 最大 4；CPU 70%、内存 75%。API Service 固定为：

```yaml
apiVersion: v1
kind: Service
metadata: { name: firecrawl-api, namespace: oc-firecrawl }
spec:
  selector: { app: firecrawl-api }
  ports: [{ name: http, port: 3002, targetPort: 3002 }]
```

- [ ] **Step 3: 实现内部网络边界**

API ingress 仅允许 `oc-apps` 与 `oc-aicc` namespace 的 Pod TCP 3002；内部组件只开放依赖端口；计算组件 egress 仅 DNS、内部依赖与公网 TCP 80/443。Firecrawl 不限制 `/interact`；AICC 限制仍由 Hermes 工具策略完成。

- [ ] **Step 4: 校验与本地启动**

Run: `bash deploy/k8s/contracts/check-firecrawl.sh deploy/k8s/local/firecrawl.yaml && kubectl config current-context && kubectl apply -f deploy/k8s/local/00-namespace.yaml -f deploy/k8s/local/firecrawl.yaml`

Expected: context 为 `k3d-ocm`，所有 Pod Ready。生产文件只运行 `kubectl apply --dry-run=client`，不得连接生产。

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/local deploy/k8s/prod deploy/k8s/contracts/check-firecrawl.sh
git commit -m "feat(firecrawl): 部署无状态弹性网页提取服务" -m "新增独立 Firecrawl namespace、临时依赖和六个计算组件 HPA，供 Hermes 共享使用。"
```

### Task 2: 在所有 Hermes 启动时配置网页能力

**Files:**
- Modify: `runtime/hermes/hermes-aicc/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v0.18.2/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.6.5/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-v2026.7.1/renderer/render_config_yaml.py`
- Modify: 每个对应 `tests/test_render_config_yaml.py`

- [ ] **Step 1: 为所有 renderer 写失败断言**

```python
# 每个 Hermes bootstrap 必须固定 DDGS 搜索与 Firecrawl 正文提取。
assert out["web"] == {"search_backend": "ddgs", "extract_backend": "firecrawl"}
```

普通 variant 还要断言 API server 向模型注册 `web` toolset，且不覆盖已有 toolset；AICC 断言仍只有 `aicc/web/skills/vision`，操作工具策略继续拒绝。

- [ ] **Step 2: 运行红测**

Run: `PYTHONPATH=runtime/hermes/hermes-aicc pytest -q runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py`

Expected: FAIL，当前 AICC 仍是单一 DDGS backend，普通 variant 未配置网页后端。

- [ ] **Step 3: 最小实现**

每个 renderer 写入：

```python
"web": {"search_backend": "ddgs", "extract_backend": "firecrawl"},
```

普通 Hermes 必须在现有 API server toolset 基础上追加 `web`，不可用单元素列表覆盖默认能力；AICC 不增加 terminal、browser 或写工具。

- [ ] **Step 4: 运行所有 Hermes 测试**

Run: `for d in runtime/hermes/hermes-aicc runtime/hermes/hermes-v0.18.2 runtime/hermes/hermes-v2026.5.16 runtime/hermes/hermes-v2026.6.5 runtime/hermes/hermes-v2026.7.1; do PYTHONPATH="$d" pytest -q "$d/tests"; done`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add runtime/hermes
git commit -m "feat(hermes): 启动时配置共享网页检索后端" -m "所有 Hermes variant 固定 DDGS 搜索与 Firecrawl 提取，AICC 工具白名单保持不变。"
```

### Task 3: 向全部 Hermes Pod 注入内部 Firecrawl URL

**Files:**
- Modify: `internal/integrations/k8sorch/render.go`
- Modify: `internal/integrations/k8sorch/render_test.go`
- Modify: `internal/integrations/k8sorch/testdata/deployment.golden.yaml`
- Modify: `internal/integrations/k8sorch/testdata/deployment-aicc.golden.yaml`

- [ ] **Step 1: 写失败断言**

```go
// Hermes 在 Pod 启动时必须取得共享 Firecrawl 地址，不依赖人工配置。
assert.Equal(t, "http://firecrawl-api.oc-firecrawl.svc.cluster.local:3002",
    envByName(hermes, "FIRECRAWL_API_URL").Value)
```

普通及 AICC Deployment 都要断言。AICC 仍无 HTTP_PROXY、HTTPS_PROXY、NO_PROXY；普通 PodProxy 测试保持。

- [ ] **Step 2: 运行红测**

Run: `go test ./internal/integrations/k8sorch -run 'TestRenderDeployment(AICC|$)|TestRenderAICCNetworkPolicy' -count=1`

Expected: FAIL，当前未注入 `FIRECRAWL_API_URL`。

- [ ] **Step 3: 最小实现和 golden**

```go
{Name: "FIRECRAWL_API_URL", Value: "http://firecrawl-api.oc-firecrawl.svc.cluster.local:3002"},
```

只添加到 Hermes 常驻容器，不添加给 `oc-ops`、initContainer 或前端。运行 `UPDATE_GOLDEN=1` 刷新两个 golden。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/integrations/k8sorch ./internal/worker/handlers ./cmd/server -count=1 && git diff --check`

Expected: PASS。

```bash
git add internal/integrations/k8sorch
git commit -m "feat(hermes): 注入共享 Firecrawl 服务地址" -m "普通 Hermes 与 AICC 均在 Pod 启动时获得 Firecrawl 地址，不增加客服操作权限。"
```

### Task 4: 真实验证与证据

**Files:**
- Create: `docs/testing/firecrawl-shared-validation-report.md`
- Modify: `docs/testing/aicc-conversation-requirement-matrix.md`
- Modify: `docs/testing/aicc-conversation-validation-report.md`

- [ ] **Step 1: 建立可判定验收表**

记录：无 PVC、六个 HPA、API 不公开、普通/AICC Hermes 均为 DDGS+Firecrawl、AICC 高危工具仍拒绝、至少一个计算组件从 1 扩至 2、删除临时 PostgreSQL 后在飞提取失败且新请求恢复。

- [ ] **Step 2: 自动化验证**

Run: `go test ./internal/... ./cmd/server -count=1 && make openapi-check && cd web && npm test -- --run && npm run typecheck && npm run build`

Expected: PASS；环境阻塞必须记录真实包名和错误，不能写 PASS。

- [ ] **Step 3: 本地 k3d + Chrome Stable**

确认 context 为 `k3d-ocm`，验证 Firecrawl API 抽取公开 HTTPS 页面；普通 Hermes 与 AICC 分别完成一次 `web_search` 与 `web_extract`；制造动态页面并发并观察一个计算组件 HPA 扩至至少 2；删除 PostgreSQL Pod 后验证在飞请求失败、恢复后新请求成功。最后运行现有 AICC Chrome E2E，bootstrap 401 若仍存在必须单列 BLOCKED。

- [ ] **Step 4: Commit**

```bash
git add docs/testing
git commit -m "test(firecrawl): 记录共享网页能力真实验证" -m "记录无状态 Firecrawl、Hermes 搜索提取、HPA 与 Chrome 验证的实际结果。"
```

