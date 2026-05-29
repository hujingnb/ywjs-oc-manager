# oc-ops HTTP 契约

> spec-E 落地。oc-ops 是 app pod 内的第二容器（与 hermes 主容器**复用同一镜像**，
> 仅覆盖启动命令为 `python -m uvicorn ocops.server:app --host 0.0.0.0 --port 8080`），
> 把原来通过 docker/k8s exec 调用的 `oc-*` 运维命令收敛为类型化 HTTP 服务。
> manager 经 **per-app Bearer token** 调用，彻底取消 `pods/exec`。
>
> - pod 侧实现：`runtime/hermes/hermes-v2026.5.16/ocops/`（`server.py` 路由、
>   `cron.py`/`kanban.py`/`channel.py`/`info.py`/`doctor.py` 核心逻辑、`auth.py` 鉴权、
>   `errors.py` 错误模型）。`oc-*` CLI 保留为薄 shim，与 ocops 包共用同一份逻辑。
> - manager 侧客户端：`internal/integrations/ocops/`（`client.go` 传输、
>   `client_cron.go`/`client_kanban.go`/`client_channel.go`/`client_sse.go` 方法、
>   `types.go`/`types_channel.go` 契约 DTO）。

## 1. 鉴权

除 `GET /healthz` 外，所有请求须带：

```
Authorization: Bearer <OC_OPS_TOKEN>
```

`OC_OPS_TOKEN` 由 pod env 注入（来源见 §6 spec-A 对齐点）。常量时间比较；缺失 / 不匹配 / 服务端未配置 token → **401** `{"code":"UNAUTHORIZED","message":"invalid token"}`。

## 2. 错误码 → HTTP 状态码

成功响应：**200**（`DELETE` 用 **204**），body 直接是类型化对象，**不再包 `{ok,data}` 信封**。
失败响应：下表状态码 + body `{"code":"...","message":"..."}`。

| 契约错误码 | HTTP 状态 | manager 侧哨兵错误 |
|---|---|---|
| `BAD_REQUEST` | 400 | `ocops.ErrBadRequest` → `ErrCronBadRequest`/`ErrKanbanBadRequest` |
| `UNAUTHORIZED` | 401 | `ocops.ErrUnauthorized` |
| `NOT_FOUND` | 404 | `ocops.ErrNotFound` → `service.ErrNotFound` |
| `UNSUPPORTED` | 409 | `ocops.ErrUnsupported` → `ErrCronNotSupported`/`ErrKanbanNotSupported` |
| `INTERNAL` | 500 | `ocops.ErrOutputInvalid` → `ErrCronOutputInvalid`/`ErrKanbanOutputInvalid` |
| `HERMES_CLI_FAILED`（及未知码） | 502 | `ocops.ErrCLI` → `ErrCronCLI`/`ErrKanbanCLI` |

## 3. 镜像 / 健康 / 渠道端点

| Method | Path | 请求 | 成功响应 | manager 方法 |
|---|---|---|---|---|
| GET | `/healthz` | — | `200 ok`（无鉴权） | — |
| GET | `/oc/info` | — | `ocops.Info` | `Client.Info` |
| GET | `/oc/doctor` | — | `ocops.Doctor` | `Client.Doctor` |
| GET | `/oc/channels/{channel}/status` | — | `ocops.ChannelStatus` | `Client.ChannelStatus` |
| POST | `/oc/channels/{channel}/unbind` | — | `ocops.ChannelResult` | `Client.ChannelUnbind` |

当前 `channel` 仅支持 `weixin`；未知 channel → 400 `BAD_REQUEST`。

## 4. Cron 端点

| Method | Path | 请求 | 成功响应 | manager 方法 |
|---|---|---|---|---|
| GET | `/oc/cron/capabilities` | — | `ocops.CronCapabilities` | `CronCapabilities` |
| GET | `/oc/cron/status` | — | `ocops.CronStatus` | `CronStatus` |
| GET | `/oc/cron/jobs` | query `all=<bool>` | `[]ocops.CronJob` | `CronList` |
| GET | `/oc/cron/jobs/{id}` | — | `ocops.CronJob` | `CronShow` |
| POST | `/oc/cron/jobs` | `ocops.CronCreateReq` | `ocops.CronJob` | `CronCreate` |
| PATCH | `/oc/cron/jobs/{id}` | `ocops.CronUpdateReq` | `ocops.CronJob` | `CronUpdate` |
| POST | `/oc/cron/jobs/{id}/toggle` | `{"enabled":<bool>}` | `ocops.CronJob` | `CronToggle` |
| POST | `/oc/cron/jobs/{id}/run` | — | `ocops.CronJob` | `CronRun` |
| DELETE | `/oc/cron/jobs/{id}` | — | `204` | `CronDelete` |
| GET | `/oc/cron/jobs/{id}/history` | — | `[]ocops.CronRunEntry` | `CronHistory` |
| GET | `/oc/cron/jobs/{id}/output` | query `file=<name>` | `ocops.CronRunOutput` | `CronOutput` |

`CronCreateReq`/`CronUpdateReq` 字段：`name`/`schedule`/`prompt`/`deliver`/`repeat`/
`script`/`no_agent`/`workdir`/`skills`/`model`/`provider`/`base_url`（update 额外
`agent`/`clear_skills`，并用指针表达「未提交」partial update 语义）。
manager 侧保留全部输入校验（长度/正则/白名单），再发请求（纵深防御）。

## 5. Kanban 端点

| Method | Path | 请求 | 成功响应 | manager 方法 |
|---|---|---|---|---|
| GET | `/oc/kanban/capabilities` | — | `ocops.KanbanCapabilities` | `KanbanCapabilities` |
| GET | `/oc/kanban/boards` | — | `[]ocops.KanbanBoard` | `KanbanBoards` |
| GET | `/oc/kanban/tasks` | query `board`/`status`/`assignee` | `[]ocops.KanbanTask` | `KanbanList` |
| GET | `/oc/kanban/tasks/{id}` | query `board` | `ocops.KanbanTaskDetail` | `KanbanShow` |
| GET | `/oc/kanban/tasks/{id}/runs` | query `board` | `[]ocops.KanbanTaskRun` | `KanbanRuns` |
| GET | `/oc/kanban/stats` | query `board` | `ocops.KanbanStats` | `KanbanStats` |
| POST | `/oc/kanban/tasks` | `ocops.KanbanCreateReq` | `ocops.KanbanTaskDetail` | `KanbanCreate` |
| POST | `/oc/kanban/tasks/{id}/comment` | `{board,body}` | `ocops.KanbanTaskDetail` | `KanbanComment` |
| POST | `/oc/kanban/tasks/{id}/complete` | `{board,result}` | `ocops.KanbanTaskDetail` | `KanbanComplete` |
| POST | `/oc/kanban/tasks/{id}/block` | `{board,reason}` | `ocops.KanbanTaskDetail` | `KanbanBlock` |
| POST | `/oc/kanban/tasks/{id}/unblock` | `{board}` | `ocops.KanbanTaskDetail` | `KanbanUnblock` |
| POST | `/oc/kanban/tasks/{id}/archive` | `{board}` | `ocops.KanbanTaskDetail` | `KanbanArchive` |
| POST | `/oc/kanban/tasks/{id}/reassign` | `{board,to}` | `ocops.KanbanTaskDetail` | `KanbanReassign` |
| POST | `/oc/kanban/tasks/{id}/reclaim` | `{board}` | `ocops.KanbanTaskDetail` | `KanbanReclaim` |

`board` 缺省为 `default`。除 `capabilities` 外，所有 verb 先做 `has_real_hermes()` 守卫，
dev stub 镜像 → 409 `UNSUPPORTED`。

## 6. SSE 流式端点

均返回 `Content-Type: text/event-stream`，逐帧 `data: <json>\n\n`。

### `GET /oc/kanban/watch?board=<slug>` → `Client.WatchKanban`
订阅 board 事件流，每帧 data 是一个 `ocops.KanbanEvent`。watch 启动失败发
`event: error\ndata: {"code","message"}\n\n`。manager 客户端把 data 帧解析为
`<-chan ocops.KanbanEvent`，流结束 / ctx 取消时关闭 channel。

### `POST /oc/channels/{channel}/login` → `Client.ChannelLogin`
微信扫码登录事件流。data 帧是 `ocops.ChannelLoginEvent{event,url?,reason?}`：

| event | 附带 | 含义 |
|---|---|---|
| `qrcode` | `url` | 二维码链接（前端展示，可多次）|
| `bound` | — | 扫码成功，凭证已由 hermes 落盘 |
| `timeout` | — | 等待扫码超时 |
| `failed` | `reason` | 未知 channel / SDK 不可用 / 登录异常 |

manager 侧 `WeixinRunner` 把上述事件翻译成 `hermes.WeixinEvent`
（`qrcode`→QRCode、`bound`→Bound、`timeout`/`failed`→Failed）。

## 7. spec-A 对齐点（待办）

spec-E 交付并单测了上述契约的**实体**（pod 侧服务 + manager 侧类型化客户端），
但以下接入点由 **spec-A（编排 k8s 化）** 落地：

- **镜像与命令**：oc-ops 容器 image ref = hermes image ref（spec-D 契约样例
  `deploy/k8s/contracts/app-pod.deployment.yaml` 里的 `<OC_OPS_IMAGE_REF>` 退化为
  = `<HERMES_IMAGE_REF>`），command 覆盖为 uvicorn，端口 8080。
- **token 注入**：`OC_OPS_TOKEN` 来自 per-app Secret `app-<APP_ID>-token` 的
  `control-token` 键（spec-D 契约已钉）。spec-A 在创建 app pod 时生成并注入。
- **端点寻址**：manager 侧 `service.OcOpsResolver` 当前为占位实现
  （`OcOpsResolverFromStore`：baseURL 用常量模板
  `http://app-%s-ocops.oc-apps.svc:8080`、`Endpoint.Token` 留空）。spec-A 替换为
  client-go 真实 Service DNS 寻址 + per-app token 注入。
- **DockerBindingResolver**：微信绑定身份解析（读容器 plugin state 取 OpenID）
  仍依赖 docker exec，未纳入 spec-E（语义与 oc-ops `ChannelStatus.account_id`
  不能确证等价），待 spec-A k8s 编排一并改造。
- **验证**：spec-E 仅做单元测试（pod 侧 pytest、manager 侧 httptest）。
  端到端 + 三角色真实浏览器验证随 spec-A/B/D 合并后统一执行。
