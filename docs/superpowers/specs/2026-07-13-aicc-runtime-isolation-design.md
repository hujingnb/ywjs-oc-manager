# AICC 客服专用 Hermes 运行时隔离设计

## 背景

当前 AICC 隐藏应用与普通实例共用 `hermes.runtime_images`。该列表同时承担普通实例的版本选择和 AICC 运行时来源，导致客服与通用实例的镜像生命周期、功能改造和发布边界耦合。

本设计将 AICC 使用的 Hermes 运行时拆分为独立镜像、独立配置和独立发布流程。AICC 是 AI Customer Care 的缩写，本设计中的 AICC 均指在线智能客服子系统。

## 目标

- 新增单一配置 `aicc.runtime_image`，作为所有 AICC 隐藏应用唯一的运行时镜像来源。
- 新增独立构建上下文与镜像仓库 `oc-manager-hermes-aicc`，不再与普通实例的 Hermes 变体共用发布物。
- 普通实例继续仅使用 `hermes.runtime_images`，其版本选择、授权和发布流程保持不变。
- 客服镜像升级可将既有 AICC 隐藏应用受控地更新到新镜像，并可回滚到上一不可变镜像。

## 非目标

- 不为企业管理员增加客服运行时镜像选择界面。
- 不修改普通实例的镜像版本模型或企业版本授权规则。
- 不复用或覆盖 `latest` 等可变镜像标签。

## 配置模型

在 manager 配置根级增加独立 AICC 段：

```yaml
aicc:
  runtime_image: "registry.example.com/app/oc-manager-hermes-aicc:2026-07-13-12-00-00-abcdef12"
```

`aicc.runtime_image` 为非空、合法的容器镜像引用。配置加载时和 AICC 运行时启动前均需校验该字段；字段缺失、为空或不合法时，创建或启动 AICC 智能体必须失败并返回明确配置错误，不能回退到 `hermes.runtime_images` 或企业授权的普通实例镜像。

`hermes.runtime_images` 仍是普通实例的可选镜像列表。AICC 不读取该列表，也不再以企业已授权版本的第一个版本作为默认镜像。

## 构建与发布边界

新增自包含构建目录 `runtime/hermes/hermes-aicc`，其中只包含 AICC 客服所需的 Hermes 基础、契约工件和未来客服专属改造。

新增镜像仓库引用 `oc-manager-hermes-aicc`。每次构建使用由时间戳与 Git commit 组成的不可变 tag，保留上一版 tag 用于回滚。

新增命令：

```bash
make prod-deploy-aicc-runtime
```

该命令依次构建并推送客服专用镜像、仅更新 `deploy/k8s/prod/secret.yaml` 中的 `aicc.runtime_image`、执行既有配置更新流程，并发起 AICC 运行时受控升级。既有 `prod-deploy-hermes` 和 `prod-deploy-hermes-all` 只更新普通实例的 Hermes 变体，不得修改 AICC 镜像引用。

## AICC 创建与启动链路

创建 AICC 智能体前，服务端读取并校验 `aicc.runtime_image`。创建对应隐藏应用及其运行时定义时，直接写入该镜像引用。普通实例的 `RuntimeImageResolver` 不参与 AICC 镜像决策。

后续启动、重建和恢复 AICC 隐藏应用时，同样以当前 `aicc.runtime_image` 为唯一来源。已存在隐藏应用不应因为单纯加载配置而立即被无序替换；镜像替换由下述受控升级流程执行。

## 既有客服镜像更新

客服镜像发布后，服务端找出运行时镜像与 `aicc.runtime_image` 不一致的 AICC 隐藏应用，并按智能体逐个升级：

1. 标记当前智能体进入运行时升级状态，短暂拒绝新的公开消息并返回“服务升级中”提示。
2. 停止或替换旧运行时 Pod，使用新镜像创建运行时 Pod。
3. 等待新 Pod 通过就绪检查后，更新该隐藏应用记录的镜像引用并恢复接待。
4. 再处理下一智能体，避免全量客服同时不可用。

单个智能体升级失败时，保留其原运行时，记录可观测错误并继续处理其他智能体。运营人员可以查看失败项并触发重试。会话与消息存储在 manager 数据库中；升级不会删除历史消息，恢复后继续使用原会话标识。

## 回滚

回滚将 `aicc.runtime_image` 恢复为上一条已验证的不可变 tag，并复用相同的逐个升级流程。回滚完成前不能删除上一版客服镜像。该回滚仅影响 AICC 隐藏应用，不改变普通实例 Hermes 镜像。

## 测试与验收

- 配置单测覆盖 `aicc.runtime_image` 的缺失、空值、非法值与合法值。
- AICC 创建服务测试证明隐藏应用使用客服镜像，且不读取普通实例镜像列表。
- 缺少客服镜像配置时，验证不会创建隐藏应用并返回可识别错误。
- 普通实例服务回归测试证明继续从 `hermes.runtime_images` 选择镜像。
- 升级协调逻辑测试覆盖逐个升级、单项失败保留旧版本、重试与回滚。
- 本地构建客服专用镜像后，使用真实浏览器验证创建客服、启动运行时、公开页发送消息、刷新恢复会话和知识库问答。
- 发布前以真实浏览器在受控测试企业完成上述核心链路，并检查 manager-api 与 AICC 运行时日志。
