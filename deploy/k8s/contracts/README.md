# A/B/E 契约样例（不 apply）

本目录是 spec-D 为尚未实现的 spec-A/B/E 预先固定的部署契约，**仅文档化**，
不参与 make local-up / prod apply。spec-A/B/E 落地时据此对齐字段。

## app-pod.deployment.yaml

`app_type='standard'` 实例的目标形态：
- `Deployment replicas=1` + `strategy: Recreate`：同一 app 至多一个写者，
  避免 S3 工作区脑裂。
- 主容器 `hermes`（镜像由助手版本 image_id 解析）+ 第二容器 `oc-ops`
  （spec-E，基于 hermes 镜像、同版本标签，HTTP+token）。
- 共享 `emptyDir /opt/data`：零 PVC，工作区同步到 S3。
- `app-<APP_ID>-token` Secret：manager↔pod 双向复用的 per-app 控制 token
  （pod→manager bootstrap 拉配置、manager→oc-ops 调命令）。

`app_type='aicc'` 实例使用独立的无状态 Pod 形态：

- initContainer `oc-bootstrap` 仅通过 control token 拉取 bootstrap manifest 并初始化
  `/opt/oc-input`、`/opt/data`；不进行 S3 恢复。
- 容器为 `hermes` 与 `oc-ops`，均使用当前 AICC 镜像和共享 `emptyDir`。
- 不创建 `s3-sync` sidecar，不配置 `oc-presync` preStop hook，也不下发运行时 S3
  凭证；Pod 删除后 `/opt/data` 可以丢弃。
- Deployment、Service、Secret 位于 `oc-aicc`，而普通实例保留在 `oc-apps`。

## manager RBAC

manager 对 oc-apps ns 的权限已在 `../local/manager-rbac.yaml` /
`../prod/manager-rbac.yaml` 定义（deploy/svc/secret/cm CRUD + pods/log 读，
不含 pods/exec）。spec-A 的 client-go 编排直接复用该 SA。

## S3 bucket 布局（仅 `app_type='standard'`）

- app 工作区 prefix、删除时 `archive/` 归档前缀（设计 §5）。
- manifest(含 api_key) 不落 S3，由 manager bootstrap 端点内存渲染。

AICC 启动 bootstrap 只返回 manifest，不返回 S3 写凭证、运行时文件恢复 URL 或从对象
存储下载的自定义 skill。
