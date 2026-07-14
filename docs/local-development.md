# 本地开发环境（k3d）

本地开发统一用 k3d 跑全栈，已取代旧的 docker-compose 联调栈
（根 `docker-compose.yml` 已删除）。

## 前置依赖

- Docker、`k3d`、`kubectl`、Go 1.25、Node 22（后两者用于本机跑测试/构建）。
- 一次性配置：把 k3d registry 主机名指向本机：
  ```bash
  echo '127.0.0.1 k3d-ocm-registry.localhost' | sudo tee -a /etc/hosts
  ```
- 集群创建会通过 `deploy/k8s/local/registries.yaml` 把 docker.io 拉取走公共镜像站
  （本地网络直连 docker.io 受限时，k3s 系统镜像 rancher/* 才能拉下来）。网络可直连
  docker.io 时该镜像源同样可用，无需改动；若该镜像站不可用，改其中 endpoint。

## 一键起停

```bash
make local-up      # 建集群→构建镜像→部署全栈→建桶→种子管理员
make local-status  # 查看 pod / ingress
make local-stop    # 停止集群但不删除（保数据/镜像，重启不丢）
make local-start   # 启动已停止的集群（数据与已部署对象原样恢复）
make local-down    # 删除集群（数据在 .k3d-data 固定目录保留，下次 local-up 复用；清空用 local-reset）
make local-reset   # 删集群并清空 .k3d-data，干净重建（随后再 make local-up）
```

## 访问入口（traefik Ingress, *.localhost → 127.0.0.1:80）

| 服务 | 地址 | 账号 | 密码 |
|---|---|---|---|
| manager 后台 | http://ocm.localhost | `admin`（组织标识留空）| `admin123` |
| new-api 后台 | http://newapi.localhost | `admin` | `admin123` |
| ragflow 控制台 | http://ragflow.localhost | `admin@ragflow.io` | `admin` |
| MinIO 控制台 | http://minio.localhost | `ocm` | `ocmsecret123` |

> 若本机设置了 `http_proxy` / `https_proxy`（如 Clash），访问 `*.localhost` 需让代理直连：
> 命令行用 `curl --noproxy '*' http://ocm.localhost/...` 或 `export NO_PROXY=.localhost,127.0.0.1`；
> 浏览器（Chrome）默认对 `*.localhost` 直连，无需额外配置。

## 改代码后

- 改 Go / 前端代码：`make local-build`（重建 :dev 镜像 + 推 registry + 滚动重启）。
- 跑测试/检查（本机工具链）：`make test`、`make vet`、`make web-test`、`make web-typecheck`。
- 数据库迁移：`make local-migrate`（或 `make migrate-up`，等价）。
- e2e fixture：`make local-seed-e2e`（或 `make seed-e2e`）。
- 看日志 / 进容器：`make local-logs svc=manager-api`、`make local-shell svc=manager-api`。

## 数据持久化

- 有状态件（MySQL/Redis/ES/MinIO）的数据落在宿主 `<repo>/.k3d-data` 下**按服务
  命名的固定目录**（`mysql`/`redis`/`minio`/`elasticsearch`）。各 StatefulSet
  用固定 `hostPath` 而非动态 PVC，目录名不随集群重建变化。
- **保数据重启用 `make local-stop` / `make local-start`**：集群只停不删，数据原样恢复。
- **`make local-down` 删集群后数据仍在**：固定 hostPath 目录留在 `.k3d-data`，下次
  `make local-up` 直接复用、数据不丢（不再像旧 local-path 那样按新 PVC uid 新建目录而
  丢数据）。
- `make local-reset` 显式清空 `.k3d-data`，用于彻底干净重建（如换库、重置 ES 索引）。
- ES 以 uid 1000 运行且拒绝 root，固定 hostPath 默认 root:root 0755 它写不进，故
  `elasticsearch.yaml` 用一个 `fix-data-perms` initContainer 放开目录权限；其余服务以
  root 运行可直接写入。
- 旧 compose 的 `.local/` 数据已不再使用，但暂时保留未删（过渡期安全网）。

## AICC 异步消息观测（仅本地 k3d）

AICC 公开消息任务以 MySQL 的 `aicc_message_tasks` 为事实来源，Redis 只负责低延迟唤醒。
本地可在发送一条公开客服消息后查看 manager-api 日志，确认任务完成时出现 `completed`；
就绪扫描仅更新队列 gauge，不会为每次扫描输出 `queued`。上游 429、503 或超时会记录 `retry`，连续过载会记录
`circuit_open`，进程重启后回收过期租约会记录 `lease_recovered`。日志只含 `agent_id`、
`org_id`、`upstream`、`result` 标签及 `queue_wait_ms`、`inflight` 等数值，绝不应包含访客
原文、会话标识或令牌。

进程内指标注册表为现有日志/监控桥接保留安全快照：`aicc_message_transitions_total`、
`aicc_message_retries_total`、`aicc_message_failures_total`、`aicc_message_circuit_open_total`、
`aicc_message_lease_recoveries_total` 与 `aicc_message_queue_depth`。生命周期计数标签仅为
`org`、`agent`、`upstream`、`result`；`queue_wait_ms` 为累计等待值，`inflight` 为当前在飞 gauge。
扫描就绪任务只更新 gauge，不产生 `queued` 日志事件，避免积压时产生日志风暴。当前项目未部署
Prometheus，注册表通过 `SlogAICCDispatchObserver.Metrics()` 提供给既有日志或监控桥接层；不得从
指标中加入访客内容、token、session/message 原文或执行 pod 的非受控名称。

平台管理员可使用 Bearer access token 读取受保护的 `GET /api/v1/platform/aicc/metrics` JSON 快照；
该端点仅用于本地或受控监控采集，不属于健康检查，企业管理员及匿名请求均会被拒绝。

```bash
make local-logs svc=manager-api
```

安全模拟只在本地 k3d 进行：先用本地公开客服页发送消息，再临时让本地 Hermes 服务返回
429、503 或延迟超过请求超时，观察任务进入重试并在依赖恢复后完成。不要复制生产配置、
凭证或地址到本地；也不要通过删除 MySQL/Redis 数据来制造恢复场景。需要验证重启恢复时，
只滚动重启本地 `manager-api`，随后观察 `lease_recovered` 和任务最终状态。

HPA 不是静态部署清单：manager 的 AICC Kubernetes adapter 在创建或启动隐藏应用时，
通过 `internal/integrations/k8sorch/adapter.go` 动态渲染并收敛对应 HPA；停止或删除应用时会移除它。
因此先在本地创建并启动一个 AICC 智能体，再使用当前 kubeconfig 的本地集群上下文观察其资源：

```bash
kubectl config current-context
kubectl -n oc-aicc get hpa,pods
kubectl -n oc-aicc describe hpa app-<本地隐藏应用ID>
```

HPA 默认只依据 CPU（70%）和内存（75%）目标扩缩。local k3d 没有部署
`external.metrics.k8s.io` adapter，因此不要在本地配置 `k8s.aicc_hpa_business_metrics.enabled`；
否则 HPA 会因指标 API 不可用而显示 unknown。若需要验证业务突发扩缩，先在测试集群部署
external metrics adapter（例如 Prometheus Adapter），由它从受控监控桥接层导出每个隐藏应用的
`aicc_message_queue_depth{app_id="..."}` 与 `aicc_dispatch_inflight{app_id="..."}`。多 manager
副本时，前者来自相同 MySQL 扫描，adapter 必须按 `app_id` 取最大值；后者来自各副本实际执行的
租约任务，adapter 必须按 `app_id` 求和。配置开启后 HPA 用固定 `app_id` 标签筛选对应 app 的两项
External 指标；管理员 JSON 端点
`/api/v1/platform/aicc/metrics` 仍只供受控读取，不能作为 HPA provider。manager 不需要读取
external metrics API 的 RBAC，查询由 Kubernetes HPA controller 完成。若当前上下文不是本地 k3d
集群，停止执行这些命令。

## 已知限制（依赖 spec-A/B/E）

- 创建真实 app 实例、渠道绑定等依赖 k8s 编排 / oc-ops 的路径，在 spec-A/B/E
  落地前不可用。当前可验证：登录、组织/成员管理、new-api provision、助手版本、
  知识库等不依赖 app pod 编排的功能。
