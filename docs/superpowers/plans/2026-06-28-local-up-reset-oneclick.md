# 本地一键起栈与重置初始化 实现计划（make local-up / local-reset）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `make local-up` 一条命令把本地 k3d 全栈拉起并完成配置初始化（new-api/RAGFlow/manager），`make local-reset` 清空所有本地数据后重跑 local-up 全过程，迭代修复直到端到端跑通。

**Architecture:** 纯本地编排改动，集中在 `Makefile`（必要时微调 `scripts/local-init-models.py`）。核心可靠性修复：`local-preload` 由不可靠的 `docker save | ctr import` 改为 k3d 原生 `k3d image import`，并预载全部重镜像（含 ragflow/new-api）；`local-up` 末尾恢复真正执行配置初始化并在其前补 new-api/ragflow 就绪等待；`local-reset` 追加 `local-up`。

**Tech Stack:** GNU Make、k3d/k3s、kubectl、docker、Python（local-init-models.py）。验证以「跑真实命令观察」为准——不是单元测试，而是 `make -n` 校验语法 + `make local-reset` 端到端实跑。

设计来源：`docs/superpowers/specs/2026-06-28-local-up-reset-oneclick-design.md`

---

## 文件结构

| 文件 | 职责 | 本次动作 |
|---|---|---|
| `Makefile` | 本地编排目标 | 改 `LOCAL_PRELOAD_IMAGES`/`local-preload`/`local-up` 末尾/`local-reset` |
| `scripts/local-init-models.py` | new-api/RAGFlow/manager 配置初始化 | 仅在验证循环中按需修（如 embedding 落 bge-m3、就绪等待） |
| `deploy/k8s/local/*.yaml` | 本地 manifest | 仅在验证循环中按需修（如 ragflow 前置） |

约束（项目规范）：改动聚焦本地编排，不动业务代码；注释中文；不做无关重构。

事实基线（实现时已确认）：new-api Deployment 名 `new-api`、ragflow Deployment 名 `ragflow`；集群名 `$(K3D_CLUSTER)`=`ocm`，节点容器 `k3d-ocm-server-0`；local-up 末尾当前是「仅打印提示」块（需替换为真正 init）。

---

## Task 1: local-preload 改用 k3d image import 并预载全部重镜像

**Files:**
- Modify: `Makefile`（`LOCAL_PRELOAD_IMAGES` 与 `local-preload` 配方，当前在 190-205 行附近）

- [ ] **Step 1: 替换 LOCAL_PRELOAD_IMAGES 定义与注释**

把当前这段（注释 + 变量）：

```make
# LOCAL_PRELOAD_IMAGES：local-up 用 `rollout status` 阻塞等待的有状态后端镜像。节点 containerd 经
# 镜像源拉这些 docker.io 镜像较慢，曾致 mysql/es 的 rollout 超时；改为宿主 docker 拉取（走宿主 daemon
# 镜像源）后用 `docker save | ctr import` 直接灌入节点 containerd 的 k8s.io 命名空间，pod 调度即命中
# 本地镜像，不再节点慢拉。仅收录「会阻塞 local-up」的闸门镜像：busybox（es 的 initContainer）、mysql、
# elasticsearch。new-api/ragflow 不是 rollout 闸门，故不在此列——它们在节点后台按需拉取，不阻塞 local-up
# 完成；ragflow 几 GB 首拉慢，就绪后由用户手动跑 make local-init-models 完成初始化（见 local-up 末尾提示）。
# redis/minio 走 ACR/pgsty 节点直拉无瓶颈，也不入此列。宿主 docker 镜像缓存跨集群重建保留，首拉后续仅秒级导入。
LOCAL_PRELOAD_IMAGES := busybox:1.36 mysql:8.0 elasticsearch:8.11.3
```

替换为：

```make
# LOCAL_PRELOAD_IMAGES：local-up 需要的 docker.io 重镜像。节点 containerd 经镜像源拉这些镜像
# 时快时卡（mysql/es 曾致 rollout 超时，ragflow 几 GB 更易卡），故改为宿主 docker 拉取（走宿主
# daemon 镜像源，必要时换更快的源）后用 k3d 原生 `k3d image import` 灌入集群，pod 调度即命中本地
# 镜像、不再走节点慢拉。local-up 含配置初始化，需 new-api 与 ragflow Running，故二者一并预载。
# redis 走 ACR、minio 走 pgsty，节点直拉无瓶颈，不入此列。宿主 docker 镜像缓存跨 local-reset
# （删集群）保留，首拉成功后续仅做秒级导入。
LOCAL_PRELOAD_IMAGES := busybox:1.36 mysql:8.0 elasticsearch:8.11.3 calciumion/new-api:latest infiniflow/ragflow:v0.25.6
```

- [ ] **Step 2: 替换 local-preload 配方为 k3d image import**

把当前 `local-preload` 配方：

```make
local-preload: ## 宿主拉取重镜像并灌入 k3d 节点（规避节点慢拉导致 rollout 超时；local-up 内部调用）
	@for img in $(LOCAL_PRELOAD_IMAGES); do \
		echo "==> 预载 $$img"; \
		docker image inspect $$img >/dev/null 2>&1 || docker pull $$img || exit 1; \
		docker save $$img | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr -n k8s.io images import - || exit 1; \
	done
	@echo "✅ 基础镜像已灌入节点 k8s.io"
```

替换为（仅改灌入机制为 `k3d image import`；拉取沿用宿主 docker + 缓存判断）：

```make
local-preload: ## 宿主拉取重镜像并 k3d image import 灌入集群（规避节点慢拉导致 rollout 超时；local-up 内部调用）
	@for img in $(LOCAL_PRELOAD_IMAGES); do \
		echo "==> 预载 $$img"; \
		docker image inspect $$img >/dev/null 2>&1 || docker pull $$img || exit 1; \
		k3d image import $$img -c $(K3D_CLUSTER) || exit 1; \
	done
	@echo "✅ 基础镜像已 import 到集群"
```

- [ ] **Step 3: 校验语法与展开**

Run: `cd /home/hujing/dir/software/ywjs/oc-manager && make -n local-preload`
Expected: 展开出 `for img in busybox:1.36 mysql:8.0 elasticsearch:8.11.3 calciumion/new-api:latest infiniflow/ragflow:v0.25.6`，且每镜像执行 `k3d image import $img -c ocm`（不再有 `docker save | ctr import`）。

- [ ] **Step 4: 提交**

```bash
git add Makefile
git commit -m "chore(local): local-preload 改用 k3d image import 并预载 ragflow/new-api

放弃对部分镜像清单不可靠的 docker save|ctr import（曾致 busybox content
digest not found），改用 k3d 原生 k3d image import 灌入集群。local-up 含
配置初始化、需 new-api 与 ragflow Running，故将二者加入预载清单。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: local-up 补 new-api/ragflow 就绪等待并恢复执行配置初始化

**Files:**
- Modify: `Makefile`（local-up 末尾，当前 240-258 行附近的「rollout manager + seed + 仅提示」块）

- [ ] **Step 1: 替换 local-up 末尾块**

把当前这段：

```make
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-api --timeout=300s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-web --timeout=300s
	# 5) 种子平台管理员（幂等）
	$(MAKE) local-seed
	# 6) new-api/RAGFlow 模型与 token 初始化不在此自动执行：该步骤需 new-api 与 ragflow
	#    完全就绪（ragflow 首启拉镜像 + 下模型耗时较长），过早执行会因服务未就绪而失败。
	#    集群与控制面就绪后由用户在 new-api/ragflow Ready 时手动运行 make local-init-models。
	@echo "✅ 本地集群与控制面已就绪："
	@echo "   manager 控制台 http://ocm.localhost"
	@echo "   new-api 后台    http://newapi.localhost"
	@echo "   ragflow 控制台  http://ragflow.localhost"
	@echo ""
	@echo "⏭️  下一步（初始化配置）：待 new-api 与 ragflow 状态 Running 后，运行"
	@echo "      make local-init-models"
	@echo "   完成 new-api/RAGFlow 模型与管理 token 的初始化（读 .env 厂商 key，幂等）。"
	@echo "   查看就绪状态：make local-status"
```

替换为：

```make
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-api --timeout=300s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-web --timeout=300s
	# 5) 等 new-api 与 ragflow 就绪：配置初始化依赖二者 Running（ragflow 首启需下
	#    tiktoken/模型，给足超时）。
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/new-api --timeout=600s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/ragflow --timeout=900s
	# 6) 种子平台管理员（幂等）
	$(MAKE) local-seed
	# 7) 配置初始化：new-api 渠道/令牌 + RAGFlow 模型 + manager secret 回填并重启
	#    （读 .env 厂商 key，幂等；脚本内部还会再等 new-api/RAGFlow 接口就绪）。
	$(MAKE) local-init-models
	@echo "✅ 本地全栈就绪并已完成配置初始化："
	@echo "   manager 控制台 http://ocm.localhost"
	@echo "   new-api 后台    http://newapi.localhost"
	@echo "   ragflow 控制台  http://ragflow.localhost"
```

- [ ] **Step 2: 校验语法与展开**

Run: `cd /home/hujing/dir/software/ywjs/oc-manager && make -n local-up`
Expected: 末尾出现 `rollout status deploy/new-api`、`rollout status deploy/ragflow`、`make local-seed`、`make local-init-models`（真正执行 init，而非只打印提示）；预载步骤 `make local-preload` 仍在有状态件部署前。

- [ ] **Step 3: 提交**

```bash
git add Makefile
git commit -m "chore(local): local-up 末尾恢复执行配置初始化并补 new-api/ragflow 就绪等待

撤销先前「init 仅打印提示」的临时改动：local-up 末尾在 manager 之后增加
new-api(600s)/ragflow(900s) 的 rollout 等待，确保配置初始化前依赖已就绪，
随后真正执行 local-seed + local-init-models，跑完即完整可用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: local-reset 追加 local-up（清空后干净重建到可用）

**Files:**
- Modify: `Makefile`（`local-reset` 配方，当前 171-176 行附近）

- [ ] **Step 1: 在 local-reset 末尾追加 local-up**

把当前 `local-reset`：

```make
local-reset: local-down ## 删集群并清空 .k3d-data，干净重建（不自动 up）
	# .k3d-data 内是集群内 root 进程写入的 PVC 数据（如 redis appendonly），宿主用户
	# 无权直接 rm；先用一次性 root 容器清空目录内容（镜像走可达的移动云 public alpine），再删空目录。
	-docker run --rm -v $(K3D_DATA_DIR):/data $(PROD_PUBLIC_REPO)/alpine:3.22 sh -c 'rm -rf /data/* /data/.[!.]* 2>/dev/null'
	rm -rf $(K3D_DATA_DIR)
	@echo "✅ 已清空 $(K3D_DATA_DIR)；跑 make local-up 干净重建"
```

替换为：

```make
local-reset: local-down ## 清空所有本地数据并重跑 local-up（全新干净重建到可用，含配置初始化）
	# .k3d-data 内是集群内 root 进程写入的 PVC 数据（如 redis appendonly），宿主用户
	# 无权直接 rm；先用一次性 root 容器清空目录内容（镜像走可达的移动云 public alpine），再删空目录。
	-docker run --rm -v $(K3D_DATA_DIR):/data $(PROD_PUBLIC_REPO)/alpine:3.22 sh -c 'rm -rf /data/* /data/.[!.]* 2>/dev/null'
	rm -rf $(K3D_DATA_DIR)
	@echo "✅ 已清空 $(K3D_DATA_DIR)，开始全新重建..."
	$(MAKE) local-up
```

- [ ] **Step 2: 校验语法与展开**

Run: `cd /home/hujing/dir/software/ywjs/oc-manager && make -n local-reset 2>&1 | tail -30`
Expected: 先 `k3d cluster delete ocm`（local-down）→ 清空 .k3d-data → 进入 `make local-up` 全流程（cluster-create/local-build/local-preload/部署/init）。

- [ ] **Step 3: 提交**

```bash
git add Makefile
git commit -m "chore(local): local-reset 清空数据后自动重跑 local-up

将 local-reset 从「只删集群+清数据」改为「清空所有本地数据后重跑 local-up
全过程」，作为重置到全新可用状态（含配置初始化）的单一入口。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: 端到端验证循环（make local-reset 实跑，修到达标）

**Files:**
- 视失败点而定：`Makefile`（超时/顺序）、`scripts/local-init-models.py`（就绪等待/embedding=bge-m3）、`deploy/k8s/local/*.yaml`（ragflow 前置）

本任务无单元测试，验证 = 跑真实命令并观察。每轮失败定位后做**最小修复**并提交，再以 `make local-reset` 重验。

- [ ] **Step 1: 起一次全新重建（后台长跑 + 日志）**

Run（在仓库根）：
```bash
nohup make local-reset > /tmp/local-reset.log 2>&1 &
```
然后用轮询/监视观察 `/tmp/local-reset.log`。Expected（成功）：日志末尾出现 `✅ 本地全栈就绪并已完成配置初始化`，且 `make` 退出码 0。

- [ ] **Step 2: 若失败，按失败点定位并最小修复**

逐项排查（与 spec「时序与依赖」一致）：
- **镜像预载失败**：宿主 `docker pull <img>` 卡/失败 → 该镜像源对此镜像不通，换可用源重拉（如先 `docker pull docker.1ms.run/<repo>:<tag>` 再 `docker tag` 成 manifest 引用名，使 `docker image inspect` 命中缓存），或重试；`k3d image import` 报错则确认集群节点存在（`k3d cluster list`）。
- **mysql/es/minio/redis rollout 超时**：确认对应镜像已被 `local-preload` import（`docker exec k3d-ocm-server-0 crictl images | grep <img>`）；不足则提高对应 `--timeout`。
- **ragflow CrashLoop**：依 spec 优先查宿主 `lsmod | grep br_netfilter`（缺则 `sudo modprobe br_netfilter`）与集群内 `host.k3d.internal` 解析（`kubectl run ... busybox -- nslookup host.k3d.internal`）；看 `make local-logs svc=ragflow`。
- **local-init-models 报错**：`python3 scripts/local-init-models.py` 直接跑看栈；确认 `.env` 厂商 key 已读到；new-api/ragflow 接口是否就绪。
- **embedding 错配**：确认脚本把 RAGFlow 默认 embedding 落到 `bge-m3`（非 512-token 上限的 bce-embedding-base_v1），否则大文档解析失败。

每次修复后：`git add <改动文件> && git commit -m "fix(local): <具体修了什么>"`。

- [ ] **Step 3: 修复后重验**

Run: `nohup make local-reset > /tmp/local-reset.log 2>&1 &`，重复 Step 2 直到 Step 1 的成功条件达成。

- [ ] **Step 4: 达标判定（浏览器端到端，CLAUDE.md 要求）**

`make local-reset` 零错误退出后，用真实浏览器验证（非 curl）：
1. 登录 manager 后台 http://ocm.localhost（admin/admin123，组织标识留空）正常进入控制台。
2. new-api 后台 http://newapi.localhost（admin/admin123）存在 DeepSeek 渠道、自用模式开启。
3. ragflow 模型为 bge-m3（http://ragflow.localhost 或 `make local-init-models` 输出确认）。
4. 在 manager 行业/组织知识库上传一个文件，**解析状态最终为成功**（端到端验证 init 链路打通）。

任一项不达标 → 回 Step 2 修复并重验。

- [ ] **Step 5: 收尾**

确认 `git status` 无遗留无关改动；本计划相关的 Makefile/脚本改动均已分提交；在交付说明里写明最终 `make local-reset` 实跑结果与浏览器验证证据。

---

## Self-Review 记录

- **Spec 覆盖**：镜像可靠灌入（Task 1：k3d image import + 预载含 ragflow/new-api）✓；local-up 含 init + 就绪等待（Task 2）✓；local-reset = 清空+重跑 local-up（Task 3）✓；撤销先前临时改动（Task 1/2 内）✓；验证循环 + 成功判定（Task 4）✓；时序/embedding/br_netfilter 约束（Task 4 Step 2/4）✓。
- **占位符扫描**：Task 1-3 均给出「当前内容→替换内容」完整文本与精确 `make -n` 校验；Task 4 为empirical 循环，列出穷举失败分支与对应修复手段，非占位。
- **一致性**：Deployment 名 `new-api`/`ragflow`、集群名 `ocm`、节点 `k3d-ocm-server-0`、`$(K3D_CLUSTER)` 用法贯穿一致；`local-preload`→`local-up`→`local-reset` 调用链闭合。
