# 生产部署清单（spec-D，只生成不自动部署）

本目录是标准 k8s YAML，不含有状态后端（MySQL/Redis/ES/对象存储外置），
也不含集群外部入口（Ingress / LoadBalancer / TLS 由生产网关自行提供）。
manager-api/web、new-api、ragflow 与 RBAC 与本地一致，差异仅镜像 ref、
外部连接、imagePullSecrets、域名。

## 必填清单

1. `secret.example.yaml` → 复制为 `secret.yaml`，填（**所有外部后端连接配置都集中在这里，
   new-api.yaml / ragflow.yaml 不用改**）：
   - manager.yaml：外部 MySQL DSN、Redis、master_key（base64 32B）、
     jwt/csrf secrets、public_base_url/cookie_domain（真实域名）、
     ragflow.api_key；**k8s 编排**段的 `ops_image` tag（make build-ops-runtime 发布的实际
     tag）；**storage.s3** 段的 `endpoint/region/bucket/access_key_id/secret_access_key`
     （app pod 的 workspace/数据走标准 S3 同步，必填齐全否则 manager 启动即 fail-fast；
     目标存储无需支持 STS，sidecar 写回直接复用这对长期凭证，详见 secret.example.yaml 注释）。
     > 集群内地址（`newapi.base_url`、`hermes.manager_runtime_base_url`、
     > `k8s.bootstrap_base_url`）已用跨 namespace FQDN（`*.ocm.svc.cluster.local`）写死——
     > app pod 在 oc-apps 或 oc-aicc、后端 Service 在 ocm，短名跨 namespace 解析不到，勿改回短名。
   - new-api：`new-api-sql-dsn` / `new-api-redis-conn` 完整连接串。
   - ragflow：`ragflow-mysql-*`（host/port/dbname/user/password）、
     `ragflow-s3-*`（endpoint-url/region/bucket/access-key/secret-key）、
     `ragflow-es-*`（host/port/user）+ `elastic-password`、
     `ragflow-redis-*`（host/port/db/username）+ `redis-password`。
     连接参数（host/port/db/库名/账号）全部拆字段进 secret，ragflow.yaml 不含任何硬编码连接值。
     **对象存储用标准 S3（非 MinIO）**：ragflow.yaml 注入 `STORAGE_IMPL=AWS_S3`、path 寻址，
     用一个独立 bucket（须预先建好，access key 仅对该 bucket 授权）；endpoint-url 要带 scheme。
     账号均为专用普通账号，非 root；ragflow 用 rag_flow 库，需预建库并对该库授权，
     详见 secret.example.yaml 注释。
   - 镜像拉取 Secret `secret-registry-ywjs-26257ea5.ecis.huabei-3.cmecloud.cn`：移动云
     仓库拉取凭证，由集群侧预先创建，**已存在于 ocm 与 oc-apps 两个 namespace**——ocm 供
     manager-api/web/new-api/ragflow 拉镜像，oc-apps 与 oc-aicc 供 app pod（Hermes 实例与 AICC）拉
     hermes/ops 镜像（imagePullSecrets 是 namespace 级，缺则对应 namespace 的 pod
     ImagePullBackOff）。不在本仓库 secret.yaml 管理。

   > 所有待填值统一用 `__FILL_*__` 前缀标记。复制后跑 `grep -n '__FILL_' secret.yaml`
   > 列出全部待填项，替换完到 grep 无输出即填写完整。
2. manager-api.yaml / manager-web.yaml 的镜像 tag → 实际发布 tag（Makefile release-*-image 产出）。

> 连接配置（MySQL/Redis/ES/MinIO 的 host/port/库名/账号口令）只在 `secret.yaml` 填一处即可，
> 工作负载 YAML 里无其它占位符；剩余的文件级改动仅镜像 tag（2）。

## 镜像拉取 Secret（外部托管）

镜像仓库为移动云 `ywjs-26257ea5.ecis.huabei-3.cmecloud.cn`（自有走 `app`、上游与构建期
基础镜像走 `public`）。拉取 Secret `secret-registry-ywjs-26257ea5.ecis.huabei-3.cmecloud.cn`
由集群侧预先创建并已存在于 ocm、oc-apps 与 oc-aicc，本仓库只按名引用、不再内嵌凭证。

## apply 顺序

```bash
kubectl apply -f 00-namespace.yaml
kubectl apply -f secret.yaml            # 你填好的真值
kubectl apply -f manager-rbac.yaml
kubectl apply -f manager-api.yaml -f manager-web.yaml -f new-api.yaml -f ragflow.yaml
```

> 本目录不含 Ingress：manager-web（:80）、manager-api（`/api`、`/healthz` @ :8080）、
> new-api（:3000）、ragflow（:80/:9380）的对外暴露、域名路由与 TLS 由生产网关
> （云 LoadBalancer / 自建 Ingress Controller）按真实域名自行配置。

## 范围外

生产集群创建、集群外部入口（Ingress/LB/TLS）、外部托管 DB/OSS 的实际接入与
数据迁移不在本目录范围内。
