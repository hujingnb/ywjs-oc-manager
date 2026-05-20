# Hermes runtime 镜像

## 目录约定

每个子目录是一个独立 variant，完全自包含：

- 命名：`hermes-<upstream-ref>`，例如 `hermes-main`（version.txt=`main`）
- 内部布局见 spec docs/superpowers/specs/2026-05-19-hermes-image-self-init-design.md §5.1

## 新增 variant

整体复制上一个目录后改名 + 改 `version.txt` + 改 `Dockerfile` 的
`OC_IMAGE_VARIANT` 默认值；如需要从上一个 variant 迁数据，新增
`migrator/from_<prev_variant>.py`。

## 构建

```bash
make build-hermes-runtime HERMES_VARIANT=hermes-main      # 本地 dev
make build-hermes-image  HERMES_VARIANT=hermes-main       # 生产镜像
make release-hermes-image HERMES_VARIANT=hermes-main      # 构建 + 推送
```

Hermes runtime 的 pytest 自检已经写入 Dockerfile，构建过程中会自动执行。
