# 生产部署清单（spec-D，只生成不自动部署）

本目录是标准 k8s YAML，不含有状态后端（MySQL/Redis/ES/对象存储外置）。
manager-api/web、new-api、ragflow 与 RBAC 与本地一致，差异仅镜像 ref、
外部连接、imagePullSecrets、域名/TLS。

## 必填清单

1. `secret.example.yaml` → 复制为 `secret.yaml`，填：
   - manager.yaml：外部 MySQL DSN、Redis、master_key（base64 32B）、
     jwt/csrf secrets、public_base_url/cookie_domain（真实域名）、
     runtime.enrollment_secret（base64 32B）、ragflow.api_key。
   - 散装值：new-api/ragflow 连外部后端的 mysql/redis/es/minio 凭证。
   - `acr-pull`：阿里云 ACR 拉取凭证（见下）。
2. 各工作负载 YAML 的镜像 `REPLACE_TAG` → 实际发布 tag（Makefile release-*-image 产出）。
3. `ingress.yaml` 的 `REPLACE_WITH_*_DOMAIN` 与 TLS secret。
4. 业务若需 PVC，按云厂商改 `storageclass.example.yaml` 的 provisioner。

## 生成 ACR imagePullSecret

```bash
kubectl create secret docker-registry acr-pull -n ocm \
  --docker-server=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com \
  --docker-username='<ACR 用户名>' --docker-password='<ACR 密码>' \
  --dry-run=client -o yaml
```

## apply 顺序

```bash
kubectl apply -f 00-namespace.yaml
kubectl apply -f secret.yaml            # 你填好的真值
kubectl apply -f storageclass.example.yaml   # 按需
kubectl apply -f manager-rbac.yaml
kubectl apply -f manager-api.yaml -f manager-web.yaml -f new-api.yaml -f ragflow.yaml
kubectl apply -f ingress.yaml
```

## 范围外

生产集群创建、从 docker-compose 的 cutover、外部托管 DB/OSS 的实际接入与
数据迁移不在本 spec 内（依赖 spec-A/B/E）。
