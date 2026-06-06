# Hermes 镜像更新长期记忆迁移与镜像能力契约设计

## 背景

Hermes 应用运行时从镜像和 `/opt/data` 两类来源共同组成：

- 镜像提供 Hermes 代码、入口脚本、renderer、migrator、内置 skill 与 `ocops.server`。
- `/opt/data` 保存 app 级运行时数据，包括长期记忆、workspace、会话、SQLite 状态、微信凭证、kanban 数据等。

镜像更新时，如果把所有 `/opt/data` 都当作可丢弃内容，会丢失用户长期记忆；如果把所有 `/opt/data` 都原样恢复，又可能让新镜像继续读取旧会话快照，导致新 `SOUL.md`、模型配置或平台规则没有进入新 session。

本设计只解决 Hermes 长期记忆在镜像更新时的保留策略，并补充 `runtime/hermes/AGENTS.md`，用于约束未来新增 Hermes 镜像必须具备的能力。

## 目标

- 镜像更新后保留 Hermes 长期记忆。
- 区分长期记忆、会话快照、镜像生成文件三类数据。
- 明确 `runtime/ops` 镜像与 Hermes variant 的版本边界。
- 在 `runtime/hermes/AGENTS.md` 中记录未来新增 Hermes 镜像的通用能力契约。
- 保持改动范围聚焦，不引入与长期记忆迁移无关的运行时重构。

## 非目标

- 不迁移旧会话上下文。
- 不把 `sessions/` 或 `state.db*` 定义为长期记忆。
- 不改变 RAGFlow 知识库数据流。
- 不把 `runtime/ops` 镜像改成随 Hermes variant 发版。
- 不拆分 `ocops.server` 到独立 ops 镜像；如未来需要拆分，应另起设计。

## 数据分类

### 长期记忆：必须保留

以下数据属于 Hermes 对用户稳定偏好、长期事实或用户画像的持久化结果，镜像更新时必须保留：

- `/opt/data/memories/`
- `/opt/data/MEMORY.md`
- `/opt/data/USER.md`

如果这些文件或目录不存在，按首启或尚未产生长期记忆处理，不应阻塞启动。

### 会话快照：不按长期记忆保留

以下数据属于会话级状态，可能冻结旧 `SOUL.md` 或旧 runtime 配置。镜像更新或配置变更需要创建新 session 时，可以清理：

- `/opt/data/sessions/`
- `/opt/data/state.db`
- `/opt/data/state.db-shm`
- `/opt/data/state.db-wal`

清理失败应让升级流程失败并暴露错误，避免新镜像误读旧会话。

### 镜像入口生成文件：启动时重渲染

以下文件由新镜像入口负责从 `/opt/oc-input` 和 Hermes 自管数据生成，镜像更新后应由当前镜像重渲染：

- `/opt/data/config.yaml`
- `/opt/data/SOUL.md`
- `/opt/data/.env`
- `/opt/data/skills/oc-kb/`

renderer 不得覆盖长期记忆文件或目录。

## 镜像更新数据流

镜像更新时使用现有 k8s `Recreate` 策略：旧 pod 停止后再启动新 pod，保证同一 app 只有一个 `/opt/data` 写者。

目标数据流：

1. manager 更新 Hermes image，Deployment 触发 Recreate。
2. 旧 pod 停止前，`oc-presync` 将需要持久化的 `/opt/data` 数据同步到 S3。
3. 新 pod 的 `oc-restore` 调用 bootstrap 端点，拿到 manifest、skills 预签名 URL、S3 恢复 URL 与写凭证。
4. `oc-restore` 从 S3 恢复 app 级运行时数据。
5. 新镜像 `oc-entrypoint` 读取 `/opt/oc-input` 与 `/opt/data/.oc-state.json`。
6. 如果发现 variant 变化，先执行 migrator。
7. `oc-entrypoint` 重渲染它拥有的文件，并启动 Hermes gateway。

长期记忆跟随 app 级运行时数据恢复；旧会话快照不作为长期记忆迁移。

## S3 同步与恢复边界

S3 中 app 级数据使用 `apps/<appID>/` 前缀。未来实现或文档应保持以下语义：

- `workspace/`、长期记忆、kanban、微信凭证、日志等非敏感运行时数据可以由 sync 机制保存。
- `sessions/` 与 `state.db*` 是会话快照，是否保存可以由 sync 机制决定，但镜像更新策略不得把它们当长期记忆。
- `api_key`、control token、RAGFlow API key 等敏感凭证不得落 S3；它们由 manager bootstrap 通过认证通道下发，DB 加密字段是唯一持久真相源。
- S3 恢复失败时不能静默启动空数据实例，应让 pod 初始化失败并暴露错误。

## 迁移策略

当前 `hermes-v2026.5.16` 的 migrator 是 no-op，因为已知持久化布局兼容。未来新增 Hermes variant 时：

- 新镜像必须能读取旧 `/opt/data`。
- 如果长期记忆格式不兼容，必须在新 variant 的 `migrator/` 中做原地迁移。
- migrator 必须保持幂等；重复启动不能重复破坏数据。
- 迁移失败必须阻止启动并保留原始数据，不能删除或清空长期记忆。
- `.oc-state.json` 仍作为判断前后 variant 的本地状态锚点。

## ops 镜像与 Hermes 镜像边界

`runtime/ops` 镜像不跟 Hermes variant 版本走。它是平台基础设施镜像，负责通用的 pod 恢复与 S3 同步能力：

- `oc-restore`
- `oc-sync`
- `oc-presync`

这些命令应跟 manager bootstrap、S3 key 约定和 k8s 编排契约保持兼容。

`ocops.server` 当前仍属于 Hermes 镜像能力，跟 Hermes variant 版本走。原因是它直接操作该 variant 内的 Python 包、Hermes 内部命令、`/opt/data` 布局、cron、kanban、channel 和 skill 热加载能力。若未来要把 `ocops.server` 拆到独立 ops 镜像，需要另起设计，定义跨镜像读写 `/opt/data` 的稳定 ABI。

## `runtime/hermes/AGENTS.md` 设计

新增 `runtime/hermes/AGENTS.md`，作用范围覆盖 `runtime/hermes/**`。它是未来新增 Hermes 镜像的通用维护规范，不替代每个 variant 自己的 `CONTRACT.md`。

文档应包含以下章节：

- 目录与版本约定：每个 variant 一个目录，包含 `Dockerfile`、`version.txt`、`CONTRACT.md`、entrypoint、renderer、migrator、ocops 和测试。
- 持久化边界：列出长期记忆、会话快照、重渲染文件、敏感凭证的归属。
- S3 保存与恢复：说明 `oc-restore`、`oc-sync`、`oc-presync` 的职责，以及哪些数据可以保存、哪些不能落盘。
- 对外能力：说明主容器入口、`oc-healthcheck`、`oc-kb`、`ocops.server`、Bearer token 鉴权和主要 HTTP 能力。
- 启动流程：描述 bootstrap、restore、migrator、renderer、Hermes gateway 的顺序。
- 升级兼容：说明新镜像如何读旧数据、何时写 migrator、迁移失败如何处理。
- 测试要求：列出 renderer、migrator、ocops、长期记忆保留和 S3 边界相关测试。

## 错误处理

- S3 恢复失败：启动失败，不静默降级为空实例。
- 长期记忆不存在：正常启动。
- 长期记忆迁移失败：启动失败并保留原始数据。
- 清理 `sessions/` 或 `state.db*` 失败：升级流程失败，避免旧会话污染新配置。
- renderer 写长期记忆文件：视为镜像契约违规，应由测试捕获。

## 测试与验证

后续实施应补充或确认以下测试：

- `AppRestartContainerHandler` 镜像变更路径不删除长期记忆。
- 会话快照清理逻辑只覆盖 `sessions/` 与 `state.db*`，不覆盖 `memories/`、`MEMORY.md`、`USER.md`。
- Hermes renderer 测试确认不触碰长期记忆文件。
- migrator 测试覆盖首启、同版本、跨版本 no-op、未来格式迁移和幂等。
- 浏览器验证：创建实例写入长期记忆，更新 Hermes 镜像后确认长期记忆仍可被读取。

## 验收标准

- `runtime/hermes/AGENTS.md` 存在，并明确未来 Hermes 镜像的持久化、S3、接口、恢复、迁移和测试约束。
- 文档明确 `runtime/ops` 镜像独立于 Hermes variant 发版。
- 文档明确 `ocops.server` 当前随 Hermes variant 走。
- 长期记忆保留项与会话快照清理项没有混淆。
- 没有改动无关前端、OpenAPI 或业务逻辑文件。
