# 运行节点资源趋势与关联实例设计

## 背景

当前运行节点页面只能展示节点注册、心跳、探测和 endpoint 等管理信息；实例详情的“运行时”tab 只展示最近一次资源快照。平台管理员缺少两个运维视角：

- 在节点列表中快速看到节点当前资源状态。
- 打开某个节点后查看节点一段时间内的资源趋势，并查看该节点关联的实例及实例资源趋势。

现有实例资源链路由 manager worker 周期通过 runtime-agent Docker proxy 拉取 Docker inspect / stats，再覆盖写入 `apps.runtime_snapshot_json`。本设计不沿用覆盖式快照作为展示事实来源，改为持久化资源时序采样；当前值也从最近一条采样计算。

## 目标

- 平台管理员在运行节点列表看到每个节点的当前资源摘要。
- 平台管理员点击节点后在当前页面打开宽抽屉，不跳转独立详情页。
- 节点抽屉展示节点 CPU、内存、磁盘、网络、实例数的时间趋势。
- 节点抽屉展示关联实例只读列表，实例行可展开查看 CPU、内存、磁盘、网络趋势。
- 实例详情“运行时”tab 同步升级为资源趋势视图。
- 资源原始采样每 30 秒一次，保留 30 天。
- 节点资源由 runtime-agent 采集并随心跳上报。
- 实例资源由 manager 保持现有主动拉取模式，通过 runtime-agent Docker proxy 获取。
- 实例容器自身不主动上报资源指标。

## 非目标

- 不新增独立的节点详情页面。
- 不在节点抽屉中提供实例重启、停止、删除等操作；实例操作仍在实例详情页完成。
- 不要求实例容器内埋点或暴露资源上报接口。
- 不建设长期聚合表或容量预测系统；本轮只保存 30 天原始采样，并在查询时按需降采样。
- 不把资源指标写入 new-api 或外部监控系统。

## 采集架构

采用混合采集链路：

- 节点资源：runtime-agent 在节点侧采集，并随心跳请求主动上报 manager。
- 实例资源：manager worker 周期调度运行中实例，通过 runtime-agent Docker proxy 拉取 Docker inspect / stats。
- 存储：manager 将节点和实例资源追加写入时序采样表。
- 展示：节点列表、节点抽屉和实例运行 tab 都从时序采样读取；当前值取最近一条采样。

这样可以保持 manager 对业务实例归属的控制权。实例是否属于某个 app、容器 ID 是否仍有效、用户是否有权查看 app，仍由 manager 数据库和现有权限体系判断；agent 不需要承担 app/container 业务绑定逻辑。

## 数据模型

新增 `node_resource_samples` 表，保存节点级原始采样：

- `id uuid primary key`
- `runtime_node_id uuid not null references runtime_nodes(id)`
- `sampled_at timestamptz not null`
- `cpu_percent double precision null`
- `memory_used_bytes bigint null`
- `memory_total_bytes bigint null`
- `disk_used_bytes bigint null`
- `disk_total_bytes bigint null`
- `network_rx_bytes bigint null`
- `network_tx_bytes bigint null`
- `instance_count integer null`
- `last_error text null`
- `created_at timestamptz not null default now()`

新增 `instance_resource_samples` 表，保存实例级原始采样：

- `id uuid primary key`
- `app_id uuid not null references apps(id)`
- `runtime_node_id uuid not null references runtime_nodes(id)`
- `container_id text not null`
- `sampled_at timestamptz not null`
- `container_status text null`
- `cpu_percent double precision null`
- `memory_used_bytes bigint null`
- `memory_limit_bytes bigint null`
- `disk_read_bytes bigint null`
- `disk_write_bytes bigint null`
- `network_rx_bytes bigint null`
- `network_tx_bytes bigint null`
- `last_error text null`
- `created_at timestamptz not null default now()`

索引：

- `node_resource_samples(runtime_node_id, sampled_at desc)`
- `instance_resource_samples(app_id, sampled_at desc)`
- `instance_resource_samples(runtime_node_id, sampled_at desc)`
- `instance_resource_samples(runtime_node_id, app_id, sampled_at desc)`

`runtime_nodes.resource_snapshot_json` 和 `apps.runtime_snapshot_json` 不再作为资源展示事实来源。实现阶段可以保留字段但停止依赖，后续再单独清理 schema。

## 采集协议

agent 心跳 payload 增加节点资源字段：

```json
{
  "agent_token": "...",
  "agent_version": "v1.0.x",
  "sampled_at": "2026-05-12T10:00:00Z",
  "node_resource": {
    "cpu_percent": 42.3,
    "memory_used_bytes": 123,
    "memory_total_bytes": 456,
    "disk_used_bytes": 789,
    "disk_total_bytes": 1000,
    "network_rx_bytes": 111,
    "network_tx_bytes": 222,
    "instance_count": 12,
    "last_error": ""
  }
}
```

节点指标采集口径：

- CPU：节点整体 CPU 使用率。
- 内存：节点已用内存与总内存。
- 磁盘：agent `data_root` 所在文件系统已用与总容量。
- 网络：节点网卡 RX/TX 字节计数。
- 实例数：当前节点上符合 manager 容器命名/标签规则的实例容器数量；实现时应复用现有容器命名规则，避免把非 manager 容器计入容量。
- 错误：单项采集失败时尽量保留其他字段，并写入 `last_error`。

实例指标不放入心跳 payload。manager 继续使用现有 runtime inspector，经 Docker proxy 调 Docker inspect / stats：

- CPU、内存、网络来自 Docker stats。
- 容器状态、镜像、名称来自 Docker inspect。
- 磁盘优先使用 Docker stats 中的 block IO 读写字节；环境不提供时写 `last_error`，前端显示未采集。
- 工作目录占用不作为本轮必须指标，避免每 30 秒递归扫描目录造成节点 IO 压力。

## API 设计

`GET /api/v1/runtime-nodes`

- 平台管理员专用。
- 继续返回节点列表。
- 每个节点增加 `current_resource`，来自最近一条 `node_resource_samples`。
- 列表只展示当前摘要，不返回趋势点。

`GET /api/v1/runtime-nodes/:nodeId/resources`

- 平台管理员专用。
- query：`from`、`to`、`bucket`。
- 默认时间范围为最近 7 天。
- 返回节点资源采样点；`bucket` 存在时返回展示用聚合点。

`GET /api/v1/runtime-nodes/:nodeId/instances`

- 平台管理员专用。
- 返回关联实例只读列表：实例 ID、名称、组织、状态、容器 ID、最近实例资源摘要、最近采样时间。
- 最近实例资源摘要来自 `instance_resource_samples` 最新点。

`GET /api/v1/runtime-nodes/:nodeId/instances/:appId/resources`

- 平台管理员专用。
- query：`from`、`to`、`bucket`。
- 校验 app 必须关联该 node。
- 返回该实例在该节点下的资源采样点。

`GET /api/v1/apps/:appId/resources`

- 沿用 app 查看权限，平台管理员、组织管理员、所属成员按现有 `CanViewApp` 规则访问。
- query：`from`、`to`、`bucket`。
- 返回实例资源采样点，用于实例详情“运行时”tab。

错误语义：

- 无采样返回空 `samples`，前端显示“未采集”。
- 节点或实例不存在返回 404。
- 无权限返回 403。
- 单条采样包含 `last_error` 时接口仍返回 200，前端在图表旁展示采集异常。

## 查询与降采样

数据库保存 30 天原始采样。前端默认看最近 7 天，并提供 1h、24h、7d、30d 选项。

展示层不直接绘制 30 天全量 30 秒点，否则单对象约 86,400 个点，容易造成页面卡顿。查询策略：

- 1h：默认原始点。
- 24h：默认 `bucket=5m`。
- 7d：默认 `bucket=5m`。
- 30d：默认 `bucket=1h`。

`bucket` 聚合规则：

- CPU、内存百分比、磁盘百分比：取平均值，tooltip 可补充最大值。
- 网络 RX/TX：按相邻采样点的字节计数差值换算为平均速率，展示为 B/s、KB/s 或 MB/s。
- 实例数：取平均值和最大值，列表/摘要优先展示当前值和峰值。
- `last_error`：聚合窗口内存在错误时返回最近一条错误。

## 节点列表交互

`/runtime-nodes` 保持平台管理员入口。列表新增“当前资源”列：

- CPU 百分比。
- 内存百分比。
- 磁盘百分比。
- 最近采样时间。

实例数列展示当前实例数和 `max_apps`：

- `12 / 20` 表示当前 12 个实例，上限 20。
- `12 / 不限` 表示 `max_apps` 为 null。
- 无采样时显示“未采集”。

点击节点名称或“查看”打开宽抽屉。抽屉不写 URL query，刷新后回到节点列表。关闭抽屉后保留列表筛选、排序和滚动位置。

## 节点抽屉交互

抽屉为全高宽抽屉，桌面建议宽度 70-80vw，移动端全屏。

顶部摘要：

- 节点名称、状态、最近心跳。
- 最近资源采样时间。
- agent 版本。
- Docker endpoint、File endpoint。
- 数据根目录。

趋势区：

- 时间范围切换：1h、24h、7d、30d，默认 7d。
- 节点 CPU 趋势。
- 节点内存趋势。
- 节点磁盘趋势。
- 节点网络 RX/TX 趋势。
- 实例数趋势与容量摘要。

关联实例区：

- 只读表格：实例名、组织、状态、容器 ID、最近资源摘要、最近采样时间、跳转详情。
- 表格支持状态、组织、实例名筛选。
- 点击展开行，在当前抽屉内加载该实例同一时间范围的 CPU、内存、磁盘、网络趋势。
- 不提供实例启动、停止、重启、删除操作。

空态与异常：

- 节点无采样：列表当前资源显示“未采集”，抽屉趋势区显示空态。
- 节点失联：仍可查看历史趋势，顶部说明“趋势截至最后采样”。
- 采样有错误：保留已有曲线，在图表旁展示最近错误。

## 实例运行 Tab

实例详情页“运行时”tab 从当前快照卡片升级为趋势视图。

保留内容：

- 启动、停止、重启、删除操作。
- 删除和停止的二次确认。
- 最近运行操作 job 进度面板。

新增/替换内容：

- 当前 app 状态继续来自 app 数据。
- 当前容器状态来自最近一条实例采样的 `container_status`。
- 时间范围切换：1h、24h、7d、30d，默认 7d。
- CPU、内存、磁盘、网络 RX/TX 趋势图。
- 最近采样时间、采样间隔 30 秒、数据保留 30 天说明。

停止态：

- 实例停止后仍展示历史趋势。
- 顶部显示“实例当前未运行，趋势截至最后采样”。

空态：

- 无实例采样时显示“资源指标尚未采集”。
- 如果 runtime-agent 或 Docker proxy 不可用，显示采样错误，不让运行操作区域消失。

## 清理策略

新增周期清理任务删除 30 天前采样：

- `node_resource_samples.sampled_at < now() - interval '30 days'`
- `instance_resource_samples.sampled_at < now() - interval '30 days'`

清理任务按表分批删除，避免一次删除大量数据造成锁表或长事务。保留期先固定 30 天，不做页面配置。

## 权限与安全

- 节点列表、节点抽屉、节点关联实例接口仅平台管理员可访问。
- 实例详情资源接口沿用 app 查看权限。
- agent 心跳继续通过 agent token 校验。
- manager 通过 Docker proxy 拉取实例指标时仍以 runtime node token 和现有 agent proxy 安全链路访问。
- 返回错误信息使用安全文本，不暴露节点内部敏感路径、token 或 Docker daemon 细节。

## 测试与验收

后端单元测试：

- agent 心跳写入节点资源采样。
- 节点列表 `current_resource` 取最近一条采样。
- 节点资源接口按时间范围过滤。
- `bucket` 聚合结果符合 CPU、内存、磁盘、网络和实例数口径。
- 节点关联实例接口只返回指定节点下未删除实例。
- 实例资源接口校验 app 与 node 的关联关系。
- manager worker 通过 Docker proxy 拉取实例资源后写入 `instance_resource_samples`。
- 清理任务删除 30 天前采样并保留边界内数据。

agent 测试：

- 节点资源采集正常生成 CPU、内存、磁盘、网络、实例数字段。
- 单项采集失败时写 `last_error`，不中断心跳。
- 心跳 payload 包含 `node_resource` 和 `sampled_at`。

前端测试：

- 节点列表展示当前资源列。
- 点击查看打开抽屉且不改变 URL。
- 时间范围切换触发节点资源接口重新查询。
- 关联实例展开行加载实例趋势。
- 实例运行 tab 展示趋势、空态和停止态说明。

浏览器验收：

- 实现完成后必须启动本地前端页面，并用浏览器实际验证 `/runtime-nodes`。
- 以平台管理员登录，确认节点列表当前资源列、实例数列和查看入口正常。
- 打开节点资源抽屉，确认抽屉不改 URL，关闭后仍停留在节点列表上下文。
- 切换 1h、24h、7d、30d，确认趋势图重新加载且无控制台错误。
- 展开关联实例行，确认实例趋势图加载、空态和错误态显示正常。
- 打开实例详情“运行时”tab，确认运行操作仍可用，资源趋势、停止态和无采样空态符合设计。
- 浏览器验收发现问题时必须在本轮修复后重新验证，不把已知前端逻辑问题留到交付说明。

交付验证：

- `go test ./... -count=1`
- `cd web && npm run typecheck`
- 相关前端单元测试。
- 修改 OpenAPI 后运行 `make openapi-gen`、`make web-types-gen`、`make openapi-check`。
- 浏览器验收通过后再交付。
