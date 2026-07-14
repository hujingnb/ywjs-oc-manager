# Runtime AiGoWork 品牌文案设计

## 目标

将 Hermes runtime 在终端用户聊天中显示的产品名称由 `Hermes` 统一改为 `AiGoWork`，使企业微信等渠道的主频道提示与产品品牌一致。

## 范围

修改以下四个仍可构建、可被版本选择或 AICC 使用的 runtime variant：

- `runtime/hermes/hermes-aicc`
- `runtime/hermes/hermes-v2026.5.16`
- `runtime/hermes/hermes-v2026.6.5`
- `runtime/hermes/hermes-v2026.7.1`

每个 variant 的 `locales/oc_overlay.yaml` 保持中英文同步，将用户可见文本中作为产品名称独立出现的 `Hermes` 改为 `AiGoWork`，覆盖：

- 未设置主频道提示；
- 更新成功、失败与超时提示；
- 网关恢复上线提示；
- 运行环境（venv）说明中的产品名称。

## 不在范围内

下列内容是运行命令、上游依赖或实现标识，不作替换：

- 可执行命令及示例，如 `hermes update`、`hermes skills config`、`hermes gateway restart`；
- 上游 skill 名称 `hermes-agent-setup`；
- Python 包路径、镜像名、variant 目录名、配置键、HTTP 协议字段；
- runtime 契约、历史设计与实施文档中用于说明上游 Hermes 引擎的技术文字。

这样既完成面向用户的 AiGoWork 品牌展示，也不会令可复制执行的真实命令失效。

## 实现与验证

本次只改 locale overlay，不变更 i18n key、占位符、构建期替换锚点或运行时逻辑。现有构建流程会将 overlay 合并到上游 catalog，因此各 variant 可继续复用现有 i18n 补丁机制。

验证包括：

1. 对每个 variant 运行现有 i18n 一致性与格式化相关测试；
2. 检索四个 overlay，确认用户可见的独立品牌名不再是 `Hermes`；
3. 人工复核命令文本仍保持 `hermes`，确保操作指引可用。
