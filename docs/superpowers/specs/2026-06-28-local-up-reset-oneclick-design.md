# 本地环境一键起栈与重置初始化设计（make local-up / local-reset）

- 日期：2026-06-28
- 范围：本地 k3d 联调环境编排（`Makefile`、`deploy/k8s/local/`、`scripts/local-init-models.py`）
- 类型：开发者体验 / 本地编排可靠性

## 背景与问题

本地 k3d 全栈由 `make local-up` 拉起。当前存在两个问题：

1. **`make local-up` 不能稳定一键跑通**：节点 containerd 经镜像源拉 docker.io 大镜像
   （mysql / elasticsearch / 尤其 ragflow 几 GB）时快时卡，导致 `rollout status` 超时；
   先前引入的 `docker save | ctr import` 预载管道对部分镜像清单不可靠（实测 busybox 报
   `ctr: content digest ...: not found`，local-up 在预载第一步即失败）。
2. **缺少「清空并初始化到可用状态」的单一命令**：配置初始化逻辑（new-api 渠道/令牌、
   RAGFlow 模型、manager token 回填）已存在于 `scripts/local-init-models.py`，但其执行
   时机与 `local-reset` 语义未理顺；`local-reset` 当前仅删集群+清数据，不做初始化。

## 目标

1. **`make local-up`**：一条命令把本地环境**完整**拉起并**完成配置初始化**，跑完即可用——
   能登录 manager 后台、new-api/RAGFlow 模型就绪、知识库上传能正常解析。
2. **`make local-reset`**：清空所有本地数据后**重跑 local-up 全过程**，回到全新可用状态。

## 非目标

- 不预置业务 fixture（默认企业/成员/实例/助手版本/示例知识库等）——配置初始化范围以
  `local-init-models.py` + `local-seed` 现有能力为准（用户确认的 A 范围）。
- 不改造 manifest 的镜像引用名、不引入向本地 registry 推送基础镜像的方案。
- 不解决公共镜像源本身的速度波动（环境因素）；设计只保证「拿到镜像后可靠灌入」与
  「宿主缓存复用」，把对镜像源的依赖降到一次性首拉。

## 设计

### 1. 镜像可靠灌入（local-preload 改用 k3d image import）

核心可靠性修复：放弃 `docker save | ctr import`（对某些清单格式报 digest not found），
改用 k3d 原生 `k3d image import`。先前观察到的「k3d image import 报成功却未生效」是因为
当时手动 `docker restart` 过节点、破坏了 k3d 托管状态；正常 `cluster-create` 出来的干净
集群上 `k3d image import` 可靠。

- `LOCAL_PRELOAD_IMAGES` 收录所有「会拖慢/阻塞 local-up」的 docker.io 镜像：
  `busybox:1.36 mysql:8.0 elasticsearch:8.11.3 calciumion/new-api:latest infiniflow/ragflow:v0.25.6`
  （up 含 init，new-api/ragflow 必须 Running，故一并预载；redis 走 ACR、minio 走 pgsty，
  节点直拉无瓶颈，不入此列）。
- `local-preload` 对每个镜像：`docker image inspect <img> || docker pull <img>`（宿主拉取，
  走宿主 daemon 镜像源），再 `k3d image import <img> -c $(K3D_CLUSTER)`。
- 宿主 docker 镜像缓存跨 `local-reset`（删集群）保留，故首拉之后每次 local-up 仅做秒级导入。
- 任一镜像宿主拉取失败 → local-preload 非零退出 → local-up 明确失败（交由验证循环重试/修复）。

### 2. local-up 流程（含配置初始化）

在现有 local-up 基础上：

- 预载步骤（`$(MAKE) local-preload`）保留在有状态件部署之前。
- 在控制面 `rollout status` 段，**新增对 `deploy/new-api` 与 `deploy/ragflow` 的就绪等待**
  （ragflow 首启需下 tiktoken/模型，给足超时，如 ragflow 900s），确保 init 前依赖已 Running。
- 末尾**恢复执行配置初始化**：`$(MAKE) local-seed` 后执行 `local-init-models`（撤销先前改成
  「仅打印提示」的改动）。`local-init-models.py` 内部已有 `_wait_until` 等 new-api API / RAGFlow
  就绪，二者叠加保证时序。
- 跑完打印「✅ 本地全栈就绪 + 访问地址」。

### 3. local-reset 流程（清空 + 重跑 local-up）

`local-reset` 在现有「删集群 + 清空 `.k3d-data`」之后，**追加 `$(MAKE) local-up`**：
```
local-reset: local-down → 清空 .k3d-data → make local-up
```
即 `local-reset` = 全新干净重建到可用（含 init），作为「重置到已知良好状态」与本任务
验证循环的单一入口。

### 4. 撤销先前的临时改动

先前为「local-up 不阻塞 ragflow / init 仅提示」所做的改动需回退到本设计：
- 预载清单加回 new-api/ragflow；
- local-up 末尾恢复真正执行 `local-init-models`（不再只打印提示）；
- 保留/恢复初始化执行所需的内部 wiring（如需要再引入内部 `.local-init-models` 或直接调用
  `local-init-models`，以幂等、缺 .env 优雅跳过为准）。

## 时序与依赖（关键约束）

- 配置初始化依赖 new-api 与 RAGFlow **运行就绪**：local-up 必须先等二者 Running 再 init。
- RAGFlow 首启依赖宿主 `br_netfilter` 已加载 + CoreDNS 能解析 `host.k3d.internal`（经 7890 代理
  下模型），这两个本地环境前置在历史上已持久化；若 ragflow CrashLoop，优先排查这两项。
- RAGFlow 默认 embedding 必须为 8000-token 的 `bge-m3`（而非 512-token 上限的
  bce-embedding-base_v1），否则大文档 chunk 超 512 被向量化接口 400 拒、解析失败——
  `local-init-models.py` 的 RAGFlow 模型直写须落到 bge-m3，作为「知识库解析正常」的验证点。
- 配置初始化依赖仓库根 `.env`（DeepSeek/SiliconFlow 厂商 key）；缺失时脚本优雅跳过模型配置
  （此时视为「未完整」）。本地已具备 .env。

## 验证（迭代到达标）

实现后进入「修复—清空—重跑」循环，单一入口为 `make local-reset`（= 全新重建 + init）：

1. 跑 `make local-reset`，全程无人值守等待。
2. 观察失败点（镜像预载 / rollout 超时 / init 脚本报错 / ragflow CrashLoop / embedding 错配）。
3. 修复对应环节（Makefile 超时与顺序、预载机制、init 脚本、ragflow 前置），再次 `make local-reset` 重验。
4. 重复直到满足**成功判定**：
   - `make local-up`（经 local-reset）全程零错误退出；
   - 浏览器登录 manager 后台（admin/admin123）正常；
   - new-api 后台有 DeepSeek 渠道、RAGFlow 模型为 bge-m3；
   - 上传一个知识库文件能正常解析成功（端到端验证 init 链路）。

## 影响范围与风险

- 仅本地编排（`Makefile` + `local-init-models.py` 视情况微调）；不动业务代码、不影响线上。
- 首次 local-up 仍受公共镜像源首拉速度影响（尤其 ragflow 几 GB）；缓解为宿主缓存复用，
  首拉成功后续 reset 秒级导入。
- `k3d image import` 依赖 k3d 托管的干净节点；流程内不再手动 `docker restart` 节点。
