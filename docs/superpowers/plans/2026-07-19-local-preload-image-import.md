# Local Preload Image Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复单节点 k3d 环境的基础镜像预载，让 `make local-reset` 无需人工补导镜像即可完成。

**Architecture:** 保留 `local-preload` 的宿主镜像缓存与按需拉取逻辑，只把会吞掉节点导入错误的 `k3d image import` 替换为项目已有的 `linux/amd64` 流式归档直导节点 containerd。扩展现有静态契约脚本锁定该命令，完整重建作为最终集成验收。

**Tech Stack:** GNU Make、Bash、Docker CLI、k3d、containerd `ctr`、kubectl、ripgrep

---

## 文件结构

- 修改 `deploy/k8s/contracts/check-local-build-images.sh`：增加基础镜像预载命令的静态契约检查。
- 修改 `Makefile`：让 `local-preload` 使用指定平台的 Docker 归档直接导入当前单个 k3d server 节点。

### Task 1: 为 local-preload 增加失败契约测试

**Files:**
- Modify: `deploy/k8s/contracts/check-local-build-images.sh`
- Test: `deploy/k8s/contracts/check-local-build-images.sh`

- [ ] **Step 1: 写入能够复现旧实现的契约检查**

在现有 manager 镜像循环之后加入：

```bash
# 基础镜像也必须限定为节点运行平台后直导 containerd；k3d image import 可能在 ctr
# 报 content digest 缺失时仍返回成功，导致 local-reset 直到部署阶段才回退远端慢拉。
if ! rg -Fq 'docker save --platform linux/amd64 $$img | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import - || exit 1' "$root/Makefile"; then
  echo "Makefile 的 local-preload 未将 linux/amd64 基础镜像直接导入 k3d 节点" >&2
  exit 1
fi
```

- [ ] **Step 2: 运行契约测试并确认 RED**

Run:

```bash
./deploy/k8s/contracts/check-local-build-images.sh .
```

Expected: 退出码为 1，并输出：

```text
Makefile 的 local-preload 未将 linux/amd64 基础镜像直接导入 k3d 节点
```

### Task 2: 使用平台限定归档直导基础镜像

**Files:**
- Modify: `Makefile`
- Test: `deploy/k8s/contracts/check-local-build-images.sh`

- [ ] **Step 1: 最小修改 local-preload 的导入命令和相邻注释**

把 `LOCAL_PRELOAD_IMAGES` 上方关于导入方式的说明改为：

```make
# daemon 镜像源，必要时换更快的源）后以 linux/amd64 归档直接灌入节点 containerd，pod 调度
# 即命中本地镜像、不再走节点慢拉。k3d image import 可能在节点 ctr 报 content digest 缺失时
# 仍返回成功，不能用于此处。local-up 含配置初始化，需 new-api 与 ragflow Running，故二者
# 一并预载。
```

将循环中的旧导入命令：

```make
		k3d image import $$img -c $(K3D_CLUSTER) || exit 1; \
```

替换为：

```make
		docker save --platform linux/amd64 $$img | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import - || exit 1; \
```

- [ ] **Step 2: 运行契约测试并确认 GREEN**

Run:

```bash
./deploy/k8s/contracts/check-local-build-images.sh .
```

Expected: 退出码为 0，无错误输出。

- [ ] **Step 3: 检查补丁格式与改动范围**

Run:

```bash
git diff --check
git diff -- Makefile deploy/k8s/contracts/check-local-build-images.sh
```

Expected: `git diff --check` 退出码为 0；diff 仅包含契约检查、`local-preload` 注释和导入命令。

- [ ] **Step 4: 提交契约测试与修复**

```bash
git add Makefile deploy/k8s/contracts/check-local-build-images.sh
git commit -m "fix(local): 修复基础镜像预载假成功" \
  -m "local-preload 改为按 linux/amd64 平台直接导入单节点 containerd。\n\n增加静态契约检查，避免 k3d image import 吞掉 ctr 内容摘要错误后回退远端慢拉。"
```

### Task 3: 完整重建并验收本地环境

**Files:**
- Verify only; no source changes expected.

- [ ] **Step 1: 执行真实全量本地重建**

Run:

```bash
make local-reset
```

Expected: 退出码为 0；`local-preload` 中每张基础镜像均由 `ctr images import` 输出成功解包，后续打印“本地全栈就绪并已完成配置初始化”。

- [ ] **Step 2: 验证全部业务 Pod 就绪**

Run:

```bash
kubectl -n ocm get pods --no-headers
```

Expected: mysql、redis、minio、elasticsearch、manager-api、manager-web、new-api、ragflow 全部为 `1/1 Running`。

- [ ] **Step 3: 绕过宿主代理验证三个入口**

Run:

```bash
for url in http://ocm.localhost http://newapi.localhost http://ragflow.localhost; do
  status=$(curl --noproxy '*' --max-time 10 -sS -o /dev/null -w '%{http_code}' "$url")
  test "$status" = 200 || { echo "$url 返回 HTTP $status" >&2; exit 1; }
done
```

Expected: 循环退出码为 0，三个入口均返回 HTTP 200。

- [ ] **Step 4: 核对最终仓库状态**

Run:

```bash
git status --short
git log -2 --oneline
```

Expected: 工作区没有未提交改动；最新提交包含修复提交，以及 `local-init-models` 仅在 token 变化时生成的独立 `secret.yaml` 回填提交。
