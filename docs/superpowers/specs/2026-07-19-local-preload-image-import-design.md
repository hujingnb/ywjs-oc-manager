# 本地镜像预载修复设计

## 背景

`make local-reset` 会删除并重建单节点 k3d 集群，随后由 `local-preload` 将 MySQL、
Elasticsearch、new-api、RAGFlow 等基础镜像从宿主 Docker 导入集群节点。当前实现使用
`k3d image import`。在宿主 Docker 的现有镜像存储格式下，节点 `ctr` 解包归档时会报告
`content digest ... not found`，但 k3d 仍返回成功，导致部署阶段重新从远端拉取镜像并长时间
阻塞。

项目中 `local-build` 和 AICC 就绪脚本已经使用
`docker save --platform linux/amd64 | docker exec ... ctr images import -`，该路径在同一环境中
能够完整导入镜像。

## 目标与范围

- 保证当前单节点集群执行 `make local-reset` 时无需人工补导镜像即可完成。
- 所有 `LOCAL_PRELOAD_IMAGES` 均显式导出 `linux/amd64` 平台并导入
  `k3d-ocm-server-0`。
- 保留宿主镜像不存在时自动 `docker pull` 的现有行为。
- 不扩展多 server/agent 节点发现逻辑，不修改 Kubernetes manifest 或其他构建流程。

## 方案

在 `local-preload` 的现有循环中，将 `k3d image import` 替换为：

```sh
docker save --platform linux/amd64 "$img" |
  docker exec -i "k3d-$(K3D_CLUSTER)-server-0" ctr images import -
```

管道任一环节失败都必须使 Make 目标失败，避免再次出现日志报错但目标继续执行的假成功。
实现保持在 Makefile 内，不新增仅使用一次的辅助脚本。

## 错误处理

- 宿主缺少镜像且 `docker pull` 失败时立即退出。
- `docker save`、节点容器访问或 `ctr images import` 失败时立即退出。
- 只有全部镜像导入成功后才打印完成信息并继续部署资源。

## 测试与验收

1. 新增本地构建镜像契约测试，要求 `local-preload` 使用带
   `--platform linux/amd64` 的 `docker save`，并通过节点 `ctr images import` 导入。
2. 修改前运行该测试并确认因旧的 `k3d image import` 实现而失败。
3. 修改后运行契约测试并确认通过。
4. 完整执行 `make local-reset`，确认命令退出码为 0。
5. 确认 `ocm` namespace 中全部 Pod 为 Ready，并确认 manager、new-api、RAGFlow 三个
   `.localhost` 入口均返回 HTTP 200。
