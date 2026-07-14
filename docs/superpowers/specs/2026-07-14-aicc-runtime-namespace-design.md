# AICC 运行时独立命名空间设计

## 背景

普通实例和 AICC 隐藏应用当前都由 manager-api 编排到 `oc-apps`。AICC 已使用独立镜像和发布流程，运行时资源也应独立到 `oc-aicc`，以便后续分别设置资源配额、网络策略和观测规则。

本次尚未发布生产环境，不迁移历史 AICC 应用。本地环境允许重置既有 AICC 测试数据。

## 目标与边界

- 新建 AICC 隐藏应用的 Deployment、Service、Secret、Pod 全部位于 `oc-aicc`。
- 普通实例继续位于 `oc-apps`，现有行为和镜像选择不变。
- AICC 公开入口仍由 `ocm` 中的 manager-api 提供，不新增 Ingress。
- 不迁移已有 AICC 资源，不修改数据库结构，不复制对象存储数据。

## 架构

配置增加 `k8s.aicc_namespace`，默认值为 `oc-aicc`；普通应用继续使用 `k8s.namespace`（默认 `oc-apps`）。

manager 启动时构造两个 KubernetesAdapter：

| Adapter | 适用应用 | 命名空间 |
| --- | --- | --- |
| 普通 Adapter | 非 AICC 应用 | `k8s.namespace` |
| AICC Adapter | `aicc_hidden=true` 应用 | `k8s.aicc_namespace` |

应用初始化、启动、停止、删除、镜像升级和渠道凭证 Secret 更新必须按应用类型选择对应 Adapter。AICC OcOps 服务地址使用 `http://app-<app-id>-ocops.oc-aicc.svc:8080`；普通实例保留 `oc-apps.svc` 地址。

## Kubernetes 资源

本地和生产清单新增 `oc-aicc` Namespace。manager-api 的 ServiceAccount 在 `oc-aicc` 获得仅限 Deployment、Service、Secret 的最小 Role/RoleBinding，不授予 pods/exec 或跨项目权限。

`acr-pull` 镜像拉取 Secret 在 `oc-aicc` 创建。AICC Pod 使用现有跨命名空间 FQDN 访问 `ocm` 的 manager-api、new-api、RAGFlow 和 MinIO；现有 bootstrap、LLM、知识库和对象存储配置不改变。

## 错误处理

- Kubernetes 启用时，缺失 `aicc_namespace` 使用 `oc-aicc` 默认值；显式空白或无法创建资源时返回明确编排错误。
- AICC 编排失败不得回退到 `oc-apps`，避免隔离失效。
- 普通实例不能因 AICC namespace 或 AICC RBAC 配置错误改变目标命名空间。

## 验证

1. 配置和 adapter 单元测试覆盖 AICC/普通应用的 namespace 选择与 OcOps 地址。
2. 本地 k3d 重置后，创建普通实例与 AICC 智能体，核验各自的 Deployment、Service、Secret、Pod 分别在 `oc-apps` 和 `oc-aicc`。
3. 真实浏览器完成平台管理员开通 AICC、企业管理员创建客服、启动接待和公开页发送消息，确认跨 namespace OcOps 转发正常。
4. 回归普通实例初始化与会话调用，确认仍指向 `oc-apps`。
