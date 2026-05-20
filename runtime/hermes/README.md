# Hermes runtime 镜像

## 目录约定

每个子目录是一个独立 variant，完全自包含：

- 命名形如 `hermes-v2026.5.16`，目录版本号与 version.txt 的 `v2026.5.16` 保持一致
- 内部布局见 spec docs/superpowers/specs/2026-05-19-hermes-image-self-init-design.md §5.1

## 新增 variant

整体复制上一个目录后改名并修改 `version.txt`；如需要从上一个 variant 迁数据，
新增迁移模块，例如从 `hermes-v2026.5.16` 迁移时使用
`from_hermes_v2026_5_16.py`。

## 构建

```bash
make build-hermes-runtime                         # 本地 dev，输出 hermes-runtime:v2026.5.16-dev
make build-hermes-image                           # 生产镜像，输出 crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00 这种时间戳 tag
make release-hermes-image                         # 构建 + 推送
make build-hermes-runtime HERMES_VARIANT=hermes-v2026.5.16
```

Hermes runtime 的 pytest 自检已经写入 Dockerfile，构建过程中会自动执行。
