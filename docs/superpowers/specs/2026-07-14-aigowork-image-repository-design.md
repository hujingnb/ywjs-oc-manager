# AiGoWork 镜像仓库更名设计

## 目标

将运行时镜像的仓库名称从 `hermes` 更名为 `aigowork`，使镜像对外名称与产品品牌一致。

## 范围

- 普通运行时镜像仓库由 `oc-manager-hermes` 改为 `oc-manager-aigowork`。
- AICC 专用运行时镜像仓库由 `oc-manager-hermes-aicc` 改为 `oc-manager-aigowork-aicc`。
- 同步更新本地与生产部署配置中引用上述仓库的镜像地址。

## 非目标

- 不重命名 `runtime/hermes` 目录、`hermes` 容器名、Makefile 的 `HERMES_*` 构建变量或 Docker build 参数。
- 不修改 Hermes 运行时配置键、API 路径、数据目录、上游 CLI 名称及用户可见的技术说明。
- 不保留或额外推送旧仓库名的兼容镜像。

## 方案

构建入口继续沿用既有的 Hermes runtime 逻辑，只修改镜像仓库变量的默认值。普通运行时和 AICC 运行时都采用 AiGoWork 前缀，避免同一产品出现不一致的镜像命名。

构建目标、推送流程和版本 tag 规则不变；因此生成的镜像仅仓库路径变化，tag 仍由当前 Hermes 版本与源码标识组成。

## 验证

- 检查 Makefile 展开的普通及 AICC 镜像引用均包含 `aigowork`。
- 检索构建和部署配置，确认不再引用 `oc-manager-hermes` 或 `oc-manager-hermes-aicc`。
- 运行与 Makefile 变量解析、部署配置一致性相关的静态检查；本次不构建或推送镜像。

## 风险与迁移

旧仓库镜像不会被删除，但后续构建和部署将转向新仓库。若集群或镜像仓库尚未授权新地址，部署前需要由既有发布流程确认该路径可推送和拉取。
