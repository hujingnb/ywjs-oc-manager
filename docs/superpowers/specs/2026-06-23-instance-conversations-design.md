# 实例对话功能设计（org_member 跨渠道会话）

> 状态：设计已与需求方对齐，待评审 → writing-plans。
> 日期：2026-06-23。

## 1. 背景与目标

org_member（企业普通成员，即实例主）目前只能通过外部渠道（微信等）与自己实例的
AI 助手对话，manager 后台里没有任何「看会话 / 在浏览器里直接对话」的入口。

本功能在**实例详情页**新增一个「对话」tab，让 org_member：

- 查看**本实例下的所有会话**（跨渠道：微信 / web 等），按最近活跃排序，带渠道来源标签；
- 选中任一会话查看历史消息（文字 + 图片，含入站文档的引用/下载）；
- 在选中会话里**续聊**：页面输入作为该会话的新一轮 user 输入喂给 agent，agent 回复
  并流式呈现；续聊微信会话时回复投递回该微信用户；
- **管理会话**：新建 web 会话、切换、删除。

非目标（本期明确不做）：

- 不面向平台管理员的「运营接管」视角，操作者就是实例主本人；
- 不做「按终端用户身份映射」——会话以**实例**为单位聚合，不需要把某条微信会话
  归属到某个具体的 manager 账号；
- 不做出站文档（pdf/docx 等）发送，不做「文档驱动 bot」；
- 不做任意会话的全量实时推送（仅发送时流式 + 列表轮询）。

## 2. 关键事实与约束（已查证）

本设计建立在对运行时镜像 `hermes-runtime:v2026.6.5-dev` 内上游源码的实地查证之上。

### 2.1 上游已具备的能力

hermes 主容器内运行一个 agent **api_server**（`gateway/platforms/api_server.py`，
监听 `127.0.0.1:8642`），已提供完整的 session/chat API：

| 能力 | 端点 |
|---|---|
| 列出持久化会话（按 `source` 过滤、按最近活跃排序、分页） | `GET /api/sessions` |
| 读会话历史消息 | `GET /api/sessions/{id}/messages` |
| 单轮同步对话（驱动 agent 回复） | `POST /api/sessions/{id}/chat` |
| 单轮流式对话 | `POST /api/sessions/{id}/chat/stream` |
| 新建会话 | `POST /api/sessions` |
| 删除会话 | `DELETE /api/sessions/{id}` |
| 设置标题 | 上游 `set_session_title` 已存在（`create_session` 接受 title） |

所有渠道会话与 API 会话都存在**同一个 session DB**，用 `source` 字段区分
（如 `weixin` / `api_server` / `web`），因此「列出实例下跨渠道会话」天然可得。

### 2.2 文件/附件约束

`POST /api/sessions/{id}/chat` 实现**只接受文字与图片**（`image_url` /
`input_image`，支持 http(s) URL 或 `data:image/...` base64）。源码显式拒绝上传文件
与文档输入（"uploaded files and document inputs are not supported on this endpoint"）。

因此：

| 方向 | 文字 | 图片 | 文档 |
|---|---|---|---|
| 看历史（入站） | ✅ | ✅ | ✅ 显示引用 / 下载 |
| 续聊发送（出站，驱动 bot） | ✅ | ✅ | ❌ chat 端点不支持 |

出站发文档唯一通道是 `WeixinAdapter.send_document`（直发渠道用户、绕过 bot），
本期不做。

### 2.3 投递不确定项（头号实现 spike）

续聊**微信**会话时，`POST /chat` 产生的回复目前是**返回给 HTTP 调用方**的；它是否
会**同时投递到微信终端用户**尚未实测确认。本期处理策略：

- 若自动投递 → oc-ops 直接转发 `/chat` 即可，无需任何额外动作；
- 若**不**自动投递 → **v1 暂不处理**（不投递到微信终端用户，页面仍正常显示回复）。
  不引入显式投递补丁、不改上游，留待后续按需再做。

这意味着 v1 **不需要任何上游 patch**。web 会话因 org_member 自己就是用户、回复在
页面显示，无论上述结果如何都不受影响。

### 2.4 注入与寻址前提

- oc-ops sidecar 与 hermes 主容器**同 pod、共享 localhost**，已有
  `oc-ops → http://127.0.0.1:8642/oc/skills/reload` 的先例，访问 api_server 成立。
- 若需要给上游注入端点（如显式投递），沿用构建期 patch 机制
  （`runtime/hermes/.../patches/patch_api_server_reload.py` 的锚点替换 + 幂等 + 缺锚点即
  构建失败的模式）。
- manager → oc-ops 寻址与 per-app token 注入复用现有 `OcOpsResolver`
  （`internal/service` 中 cron/kanban 同款）。

## 3. 架构与链路

全链路复用现有 Kanban / Cron 的端到端模式，不引入新范式：

```
前端 web/src/pages/apps/AppConversationsTab.vue
  → manager API   /api/v1/apps/:appId/conversations/...
    → handlers/hermes_conversation.go
      → service/hermes_conversation.go  (OcOpsResolver: appID → per-app oc-ops 坐标 + token)
        → integrations/ocops/client_conversation.go
          → oc-ops sidecar :8080   (新增 ocops/conversation.py + server.py 路由)
            → hermes api_server  http://127.0.0.1:8642/api/sessions...
```

oc-ops 侧只做：鉴权（per-app Bearer）、转发到 api_server、裁剪/规整字段为类型化
DTO、错误码映射（沿用 `errors.py` 既有 `BAD_REQUEST/NOT_FOUND/...` 模型）。

## 4. 接口设计（manager 对前端）

挂在 `/api/v1/apps/:appId/conversations` 下：

| Method | Path | 说明 | 后端落到 |
|---|---|---|---|
| GET | `/conversations` | 列本实例会话（query: `source`/`limit`/`offset`） | api_server `GET /api/sessions` |
| GET | `/conversations/:sid/messages` | 读历史 | `GET /api/sessions/{sid}/messages` |
| POST | `/conversations` | 新建 web 会话（body: 可选 title） | `POST /api/sessions`（source=web） |
| POST | `/conversations/:sid/chat` | 续聊（非流式，body: message=文字/图片） | `POST /api/sessions/{sid}/chat` |
| POST | `/conversations/:sid/chat/stream` | 续聊（SSE 流式） | `POST /api/sessions/{sid}/chat/stream` |
| DELETE | `/conversations/:sid` | 删除会话 | `DELETE /api/sessions/{sid}` |
| PATCH | `/conversations/:sid` | 重命名（可选，body: title） | `set_session_title` |

请求体类型放 `internal/api/handlers/dto.go` 并导出大写命名；响应用
`service.XxxResult`（swag 跨包扫描）。改完跑 `make openapi-gen` + `make web-types-gen`
保持 yaml 与 `web/src/api/generated.ts` 同步。

流式端点沿用 manager 现有 SSE 处理（kanban watch / channel login 同款），oc-ops 侧
复用 `client_sse.go` 的 `openStream`，逐帧 `data: <json>` 透传 agent 增量。

## 5. 前端

- `AppDetailPage.vue` 新增「对话」tab → `AppConversationsTab.vue`，组件结构参考
  ChatGPT 式两栏：左侧会话列表（渠道来源标签 + 最近活跃 + 新建/删除/重命名），右侧
  消息区 + 底部 composer（文字输入 + 图片粘贴/上传）。
- 实时：发送时订阅 `/chat/stream` 逐字渲染 assistant 回复；会话列表与历史用定时
  轮询 + 手动刷新（v1 不做任意会话的实时推送）。
- 国际化：所有可见文案走 i18n（遵循仓库现有全站 i18n 守卫），不硬编码中英文。
- 组件测试沿用 `AppKanbanTab.spec.ts` 模式。

## 6. 权限

- 复用实例详情现有授权链：org_member 仅能访问**自己实例**的会话；
  org_admin / platform_admin 依 `internal/auth/authorizer.go` 既有 `Can*` 谓词放行。
- 不在 handler / service 内联 `if role == ...`；如需新增规则，扩展 `authorizer.go`。

## 7. 数据与持久化

- 会话、消息历史一律实时从 hermes（经 oc-ops）读取，**manager 不本地缓存、不落库**，
  与仓库「余额/用量不本地缓存」「manager 只写中性输入」的既有约束一致。
- manager 不为本功能新增数据库表。

## 8. 错误处理

- oc-ops：沿用 `errors.py` 契约错误码 → HTTP 状态码映射；api_server 不可达 / 非 2xx
  归入既有 `ErrCLI` / `ErrNotFound` 等哨兵。
- manager：service 把 ocops 哨兵错误翻成 `service.ErrForbidden/ErrNotFound/...`，
  handler 沿用现有 `writeChannelError` 同款 switch 映射 HTTP 状态。
- 会话不存在 → 404；无权 → 403；api_server / 实例未就绪 → 503。

## 9. 测试与验证

- oc-ops 侧：pytest 覆盖转发逻辑、鉴权、错误映射、SSE 帧解析；进镜像构建期自检
  （Dockerfile RUN pytest）。
- manager 侧：handler httptest + service 单测，用 fake `OcOpsResolver`
  （沿用 `hermes_cron_test.go` 模式），覆盖正常路径、403/404/503、流式分支。
- 前端：`AppConversationsTab.spec.ts`。
- 交付前真实浏览器三角色验证（org_member 为主，覆盖列会话 / 续聊流式 / 新建 / 删除 /
  续聊微信会话投递）。

## 10. 实现里程碑（供 writing-plans 细化）

1. **Spike：验证并记录 `/chat` 对微信会话的投递行为**（仅记录；若不自动投递，v1 不处理，见 §2.3）。
2. oc-ops 转发层（`conversation.py` + 路由 + pytest）。
3. manager 客户端 + service + handler + 路由 + DTO + openapi/web-types 同步。
4. 前端「对话」tab（列表 / 历史 / 流式 composer / 管理操作 / i18n / spec）。
5. 三角色浏览器验证 + 文档。

## 11. 风险

| 风险 | 缓解 |
|---|---|
| `/chat` 不自动投递微信 | v1 暂不处理（§2.3）；页面仍显示回复，留待后续按需再做 |
| 流式 SSE 经 oc-ops 二次转发的连接/超时 | 复用 `client_sse.go` 无 Timeout 的 streamHTTP 既有实践 |
| 上游 api_server 端点结构在版本升级中变更 | oc-ops 转发层加契约测试，版本升级时回归 |
