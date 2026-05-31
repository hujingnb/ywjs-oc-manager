# 生产部署清单（spec-D，只生成不自动部署）

本目录是标准 k8s YAML，不含有状态后端（MySQL/Redis/ES/对象存储外置）。
manager-api/web、new-api、ragflow 与 RBAC 与本地一致，差异仅镜像 ref、
外部连接、imagePullSecrets、域名/TLS。

## 必填清单

1. `secret.example.yaml` → 复制为 `secret.yaml`，填（**所有外部后端连接配置都集中在这里，
   new-api.yaml / ragflow.yaml 不用改**）：
   - manager.yaml：外部 MySQL DSN、Redis、master_key（base64 32B）、
     jwt/csrf secrets、public_base_url/cookie_domain（真实域名）、
     ragflow.api_key；**k8s 编排**段的 `ops_image` tag（make build-ops-runtime 发布的实际
     tag）；**storage.s3** 段的 `endpoint/region/bucket/access_key_id/secret_access_key/
     sts_role_arn`（app pod 的 workspace/数据走标准 S3+STS AssumeRole 同步，必填齐全否则
     manager 启动即 fail-fast）。
     > 集群内地址（`newapi.base_url`、`hermes.manager_runtime_base_url`、
     > `k8s.bootstrap_base_url`）已用跨 namespace FQDN（`*.ocm.svc.cluster.local`）写死——
     > app pod 在 oc-apps、后端 Service 在 ocm，短名跨 namespace 解析不到，勿改回短名。
   - new-api：`new-api-sql-dsn` / `new-api-redis-conn` 完整连接串。
   - ragflow：`ragflow-mysql-*`（host/port/dbname/user/password）、
     `ragflow-minio-*`（host/port/user/password）、`ragflow-es-*`（host/port/user）+
     `elastic-password`、`ragflow-redis-*`（host/port/db/username）+ `redis-password`。
     连接参数（host/port/db/库名/账号）全部拆字段进 secret，ragflow.yaml 不含任何硬编码连接值。
     账号均为专用普通账号，非 root；ragflow 用 rag_flow 库，需预建库并对该库授权，
     详见 secret.example.yaml 注释。
   - `acr-pull`：阿里云 ACR 拉取凭证（见下）。secret.example.yaml 已在 **ocm 与 oc-apps
     两个 namespace 各建一份**——ocm 供 manager-api/web/new-api/ragflow 拉镜像，oc-apps
     供 app pod（Hermes 实例）拉 hermes/ops 镜像（imagePullSecrets 是 namespace 级，缺则
     app pod ImagePullBackOff）。两份填同一份 ACR 凭证。

   > 所有待填值统一用 `__FILL_*__` 前缀标记。复制后跑 `grep -n '__FILL_' secret.yaml`
   > 列出全部待填项，替换完到 grep 无输出即填写完整。
2. 各工作负载 YAML 的镜像 `REPLACE_TAG` → 实际发布 tag（Makefile release-*-image 产出）。
3. `ingress.yaml` 的 `REPLACE_WITH_*_DOMAIN` 与 TLS secret。

> 连接配置（MySQL/Redis/ES/MinIO 的 host/port/库名/账号口令）只在 `secret.yaml` 填一处即可，
> 工作负载 YAML 里已无 `EXTERNAL_*` 占位符；剩余的文件级改动仅镜像 tag（2）和域名/TLS（3）。

## 生成 ACR imagePullSecret

直接填好 secret.yaml 里 ocm + oc-apps 两份 acr-pull 的 `.dockerconfigjson` 即可；
若想用 kubectl 生成，注意**两个 namespace 各生成一份**：

```bash
for ns in ocm oc-apps; do
  kubectl create secret docker-registry acr-pull -n $ns \
    --docker-server=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com \
    --docker-username='<ACR 用户名>' --docker-password='<ACR 密码>' \
    --dry-run=client -o yaml
done
```

## apply 顺序

```bash
kubectl apply -f 00-namespace.yaml
kubectl apply -f secret.yaml            # 你填好的真值
kubectl apply -f manager-rbac.yaml
kubectl apply -f manager-api.yaml -f manager-web.yaml -f new-api.yaml -f ragflow.yaml
kubectl apply -f ingress.yaml
```

## 范围外

生产集群创建、从 docker-compose 的 cutover、外部托管 DB/OSS 的实际接入与
数据迁移不在本 spec 内（依赖 spec-A/B/E）。
