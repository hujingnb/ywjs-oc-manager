# AICC 无状态运行时与应用类型设计

## 背景

当前 `apps.aicc_hidden` 是为 AICC 单独增加的布尔标记。它不仅控制普通应用列表隐藏，还影响运行时镜像、命名空间、权限、知识库和 AICC 运行时升级判断。该模型无法承载后续更多运行时场景。

同时，AICC Pod 复用了普通应用的 S3 恢复和同步闭环。AICC 客服的业务真相已经位于 manager 数据库、RAGFlow 和既有对象存储中；其 Pod 内 `/opt/data` 不需要跨重建保留。继续下发 S3 凭证、恢复文件并同步运行时数据会增加不必要的依赖和状态边界。

## 目标与边界

- 用可扩展的 `apps.app_type` 枚举替换 `apps.aicc_hidden`。
- AICC 类型 Pod 每次启动都自行初始化运行时文件，不恢复或保存 `/opt/data`。
- AICC Pod 不持有用于运行时文件同步或恢复的 S3 凭证，也不运行 S3 同步容器。
- 普通应用继续使用既有 S3 恢复、同步和 preStop 最终同步链路。
- 公开会话、消息、图片和知识库仍使用既有 manager 数据库、对象存储与 RAGFlow，不属于本次取消范围。

## 应用类型模型

`apps` 表新增 `app_type` 字段，以字符串枚举表达运行时类型：

| 值 | 含义 |
| --- | --- |
| `standard` | 普通应用，也是默认值。 |
| `aicc` | AICC 客服运行时。 |

数据库迁移必须将已有 `aicc_hidden = TRUE` 的记录回填为 `aicc`，其他记录回填为 `standard`，再删除旧字段。所有 sqlc 查询、Go 模型、服务判断和测试统一改用 `app_type`，不保留双字段兼容分支。

原有「一个用户最多一个活动普通应用」的生成列和唯一索引，改为仅在 `app_type = 'standard'` 且应用未删除时产生 owner key。普通应用列表、计数与查询显式过滤 `app_type = 'standard'`；AICC 运行时升级扫描、AICC namespace 选择和 AICC 专属服务逻辑显式过滤 `app_type = 'aicc'`。

后续接入新的运行时场景时，只增加受约束的枚举值和对应策略，例如 `workflow` 或 `copilot`，不再新增专用布尔字段。

## AICC Pod 启动架构

AICC Deployment 的 `/opt/oc-input` 和 `/opt/data` 均使用随 Pod 生命周期销毁的 `emptyDir`。Pod 包含以下容器：

1. 配置初始化 initContainer：使用 per-app control token 请求 manager bootstrap，只将启动 manifest 写入 `/opt/oc-input` 并创建必要目录。
2. `hermes` 主容器：读取 manifest，使用 AICC 镜像中内置的代码、默认 skill 与模板渲染运行配置并启动网关。
3. `oc-ops` 容器：继续提供 manager 到运行时的控制和对话转发接口。

AICC Pod 不创建 `s3-sync` sidecar，不设置其 preStop hook，也不调用通用 `oc-restore` 的 S3 恢复逻辑。普通 `standard` 应用的 `oc-restore`、`s3-sync` 和 `oc-presync` 形态保持不变。

## Bootstrap 与数据边界

为 AICC 提供只服务启动初始化的 bootstrap 路径或响应模式。它只返回 manifest，不能返回以下内容：

- S3 写凭证；
- 工作区、记忆或其他运行时文件的恢复预签名 URL；
- 从对象存储下载的自定义 skill。

每次 AICC Pod 创建、重启或替换时，`/opt/data` 均由当前 manager 配置与当前 AICC 镜像重新生成。Hermes 会话快照、工作区、长期记忆、渠道登录凭证、Cron、Kanban 和运行时创建的 skill 都不是 AICC Pod 的持久化内容，Pod 终止后允许丢失且不支持恢复。

公开客服会话和消息继续以 manager 数据库为真相源；公开图片继续走既有对象存储；知识库继续通过 manager 运行时 API 和 RAGFlow 使用。这些平台业务数据不进入 AICC Pod 的文件持久化机制。

## 错误处理

- bootstrap 鉴权、响应校验或 manifest 写入失败时，初始化容器必须失败并阻止 Pod 启动；不得以空配置降级启动。
- AICC Pod 启动和重启不应因 S3 文件恢复、同步或 S3 临时凭证问题失败。
- AICC 类型不能回退为普通应用的持久化运行时路径，普通应用也不能因 AICC 初始化逻辑改变其 S3 行为。

## 验证

1. 迁移和查询单元测试覆盖旧布尔字段的回填、`standard` 默认值、普通应用唯一约束以及按类型过滤。
2. 服务和编排测试覆盖 AICC 与普通应用在镜像、namespace、权限和运行时策略上的选择。
3. Deployment 渲染测试确认 AICC 仅含初始化容器、`hermes` 和 `oc-ops`，没有 S3 环境变量、`s3-sync` 或 preStop；普通应用 golden 保持现有持久化形态。
4. Bootstrap 测试确认 AICC 响应不包含 S3 凭证、恢复 URL 或外部 skill 下载项，普通应用响应保持不变。
5. 本地真实浏览器验证创建客服、启动接待、公开页对话、重启 AICC Pod 后再次接待；同时回归普通应用的文件恢复能力。
