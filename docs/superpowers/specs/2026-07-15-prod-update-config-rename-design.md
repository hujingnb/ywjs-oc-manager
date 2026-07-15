# 生产配置更新命令重命名设计

## 背景

当前 Makefile 使用 `make update-config` 应用生产 Secret 并重启 manager-api。该名称没有
环境前缀，容易与本地配置更新混淆；仓库其他生产发布入口均已使用 `prod-` 前缀。

## 目标

- 将生产配置更新入口硬重命名为 `make prod-update-config`。
- 完全移除旧 target，不保留兼容别名，使误用旧命令立即失败。
- 所有生产发布组合命令统一调用新 target。
- 不执行生产 apply、重启或发版。

## 变更范围

Makefile 将同步修改 target、`.PHONY` 声明、帮助文案、注释和以下调用方：

- `prod-deploy-hermes`
- `prod-deploy-hermes-all`
- `prod-deploy-aicc-runtime`
- `prod-deploy-ops`

当前有效的仓库运维文档和本地 `prod-cluster-ops` 指引同步使用新命令。已完成的
`docs/superpowers/specs/`、`docs/superpowers/plans/` 属于历史设计记录，不批量回写，避免
制造与当时实现不一致的历史差异。

## 兼容性与安全

- `make update-config` 不再存在，调用时由 Make 返回 unknown target 错误。
- `prod-update-config` 保持原行为不变：使用生产 kubeconfig apply
  `deploy/k8s/prod/secret.yaml`，随后只重启 `ocm` namespace 的 manager-api。
- 本次只修改入口名称，不改变 kubeconfig、namespace、Secret 文件或滚动等待逻辑。
- 按 `prod-cluster-ops` 边界，所有验证只使用 `make -n`，不得执行真实生产写操作。

## 验证

- 重命名前确认 `make -n prod-update-config` 因 target 不存在而失败。
- 重命名后确认 `make -n prod-update-config` 展开为原 apply/restart 命令。
- 确认 `make -n update-config` 因旧 target 已移除而失败。
- dry-run 各 `prod-deploy-*` 调用链，确认只出现 `prod-update-config`。
- 搜索当前 Makefile 与有效文档，确认没有遗留旧命令。
