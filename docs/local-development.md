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
make local-down    # 删集群（.k3d-data 数据保留）
make local-reset   # 删集群并清空 .k3d-data，干净重建（随后再 make local-up）
```

## 访问入口（traefik Ingress, *.localhost → 127.0.0.1:80）

| 服务 | 地址 | 账号 | 密码 |
|---|---|---|---|
| manager 后台 | http://ocm.localhost | `admin`（组织标识留空）| `admin123` |
| new-api 后台 | http://newapi.localhost | `admin` | `admin123!` |
| ragflow 控制台 | http://ragflow.localhost | `admin@ragflow.io` | `admin` |

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

- 有状态件（MySQL/Redis/ES/MinIO）的 PVC 数据落在宿主 `<repo>/.k3d-data`，
  跨 `make local-down`/`local-up` 持久；`make local-reset` 才清空。
- 旧 compose 的 `.local/` 数据已不再使用，但暂时保留未删（过渡期安全网）。
- `scripts/check-compose-bind-mounts.sh` 是 compose 时代遗留校验脚本，已失效。

## 已知限制（依赖 spec-A/B/E）

- 创建真实 app 实例、渠道绑定等依赖 k8s 编排 / oc-ops 的路径，在 spec-A/B/E
  落地前不可用。当前可验证：登录、组织/成员管理、new-api provision、助手版本、
  知识库等不依赖 app pod 编排的功能。
