# 5.16 variant 对话能力对齐（忠实移植 6.5）设计

日期：2026-06-25
状态：已评审待实现
关联记忆：`project-conversation-516-gap`、`project-hermes-516-port`、`project-hermes-build-gotchas`

## 背景与目标

manager 后台「实例详情 → 对话」页通过 `manager → oc-ops → 同 pod hermes
api_server /api/sessions/*` 透传会话数据，manager 自身不持有会话。该能力随
hermes **6.5** 那代引入。被 pin 到 **v2026.5.16** 的实例打开对话页必报
`NOT_FOUND`。

产品决策：**5.16 与 6.5 长期共存**，6.5 的新能力需持续对齐回 5.16，对话是
第一个要对齐的能力。本设计给 `hermes-v2026.5.16` variant 补齐与 6.5 **完全
一致**的会话能力（全部 9 个 api_server 端点 + 7 条 oc-ops 转发路由），
manager 与前端**零改动**。

## 可行性结论（已逐项实测，基于本地 `hermes-runtime:v2026.5.16-dev` /
`:v2026.6.5-dev` 镜像源码对比）

记忆里「唯一解是迁到 6.5」是基于「5.16 根本无会话能力」的判断；实测后**比
该判断乐观得多**：5.16 缺的只是 api_server 的「HTTP 暴露层」，底层存储与运行
机制都已就绪。

| 层 | 5.16 现状 | 结论 |
|---|---|---|
| 会话存储 `hermes_state.py::SessionDB` | 已存在。`list_sessions_rich` 在 5.16，签名仅比 6.5 少 4 个新 kwarg（`min_message_count`/`include_archived`/`archived_only`/`id_query`） | 现成可用 |
| handler 依赖的 10 个 `db.*` 方法 | 全部存在：`get_session` `set_session_title` `get_messages` `end_session` `delete_session` `create_session` `replace_messages` `list_sessions_rich` `get_next_title_in_lineage` `get_messages_as_conversation` | 无需改 `hermes_state` |
| agent 续聊 `_run_agent` | 已存在（与 6.5 同 4 处） | 需构建后核对调用契约 |
| api_server 4 个外部 helper：`_check_auth` `_ensure_session_db` `_parse_session_key_header` `_run_agent` | 全部已存在 | 现成可用 |
| api_server `/api/sessions` 全套 handler + 路由（含块内自带 5 个 helper：`_parse_nonnegative_int` `_session_response` `_message_response` `_read_json_body` `_conversation_history_for_session`） | **完全缺失** | 需注入 |
| chat/stream 用到的 classmethod helper `_turn_transcript_messages`（6.5 `api_server.py` L3364–3401，在会话块外）；其转依赖 `_response_messages_turn_start_index` | `_turn_transcript_messages` **缺失**；`_response_messages_turn_start_index` 5.16 **已有**（`@staticmethod`，签名与 6.5 一致） | `_turn_transcript_messages` 随会话块一并注入；链终止 |
| oc-ops `ocops/conversation.py` 转发层 + `ocops/server.py` 路由 | **缺失** | 从 6.5 复制 |
| manager 侧链路 | 版本无关，且会话解码已加固（commit a076419 `decodeListLenient`） | 不动 |

**关键事实**：6.5 api_server 的会话特性主体是一段**自包含连续块**（
`api_server.py` **1267–1700，~434 行**，含块尾的 `_handle_session_chat_stream`
完整体），其依赖的 `db.*` 在 5.16 全有、4 个外部 helper（`_check_auth`/
`_ensure_session_db`/`_parse_session_key_header`/`_run_agent`）在 5.16 全有、块内
另带 5 个 helper；chat/stream 额外用到的 classmethod `_turn_transcript_messages`
（6.5 L3364–3401，块外）需随块一并注入，其转依赖 `_response_messages_turn_start_index`
在 5.16 已有（`@staticmethod`，签名一致），依赖链至此终止。模块级符号
`web`/`asyncio`/`time`/`logger`/`json` 在 5.16 `api_server.py` 均已 import。且
6.5 handler **实测 0 次引用**那 4 个 5.16 缺失的新 kwarg。因此移植**无需改
`hermes_state`、无需任何签名适配**，纯属把会话块 + `_turn_transcript_messages`
+ 9 行路由注入 5.16 的 `api_server.py`。

## 方案

选定方向：**构建期补丁注入，忠实对齐 6.5**（非自创共享模块）。注入内容以
**内嵌字符串**承载（沿用现有 `patch_api_server_reload.py` 的 `HANDLER_CODE`
风格），不另起 vendored 数据文件。

全部改动落在 `runtime/hermes/hermes-v2026.5.16/`，共 4 处，manager 不动：

### 1. 新增 `patches/patch_api_server_sessions.py`

结构与现有 `patch_api_server_reload.py` 同款（同一锚点机制、fail-loud、幂等）：

- **注入目标**：镜像内 `/usr/local/lib/hermes-agent/gateway/platforms/api_server.py`。
- **注入内容（内嵌字符串常量）**：从 6.5 `api_server.py` 逐字提取的两段——
  ① 会话块 **L1267–1700**（5 个块内 helper + 9 个 `_handle_*session*` handler，
  含完整 chat_stream 体）；② classmethod helper `_turn_transcript_messages`
  **L3364–3401**。字符串常量头部注释标注出处行号，便于未来 6.5 块变更时重新
  提取覆盖。
- **锚点**：复用现成的
  - `HANDLER_ANCHOR`（`# HTTP Handlers` 区块）→ 在其前插入会话块 +
    `_turn_transcript_messages`（两段拼接为同一注入字符串，均为类体方法缩进）；
  - `ROUTE_ANCHOR`（最后一条已有路由 `/v1/runs/{run_id}/stop`）→ 其后追加 9
    行 `add_get/add_post/add_patch/add_delete("/api/sessions...", self._handle_*)`
    路由注册。
- **fail-loud**：任一锚点缺失即 `raise RuntimeError` 中断构建（上游改版即时
  暴露）；已含 `_handle_list_sessions` 则幂等跳过。

### 2. 复制 oc-ops 转发层 `ocops/conversation.py`

把 6.5 的 `ocops/conversation.py` **整文件逐字拷入** 5.16 ocops。该文件仅做
「带 Bearer token 透传到 `127.0.0.1:8642/api/sessions/*` + 字段裁剪 + SSE 帧
规整」，**版本无关**，字节一致即可。

### 3. 5.16 `ocops/server.py` 补会话路由

按 6.5 `server.py` 补：

- import 行加入 `conversation`（`from ocops import channel, conversation, ...`）；
- 7 个 `conversation_*` async handler（list/messages/create/delete/rename/chat/
  chat_stream，逐字对齐 6.5 580–718 区块，`chat_stream` 用 Starlette
  `StreamingResponse`）；
- routes 列表加 7 条 `Route("/oc/conversations...", ...)`（GET/POST list+create、
  messages、chat、chat/stream、DELETE、PATCH）。
- 实现时确认 5.16 `server.py` 已 import `StreamingResponse`（6.5 用到），缺则
  补 import。

### 4. Dockerfile patch 步骤追加一行

在 `RUN set -e; ...` 的 patch 链末尾（`patch_api_server_reload.py` 之后）追加：

```
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_sessions.py
```

并在上方步骤注释块补「5) patch_api_server_sessions：注入 /api/sessions 会话端点」
说明。

## 数据流（移植后，与 6.5 完全一致）

```
前端 /api/v1/apps/:appId/hermes/conversations
  → manager handler/service（OcOpsResolver app→endpoint，版本无关，不动）
  → ocops client /oc/conversations*
  → oc-ops server.py conversation_*（新增）
  → ocops/conversation.py 转发（新增）
  → 同 pod hermes api_server /api/sessions/*（补丁注入的 handler）
  → SessionDB（5.16 已有）
```

## 风险与缓解

- **唯一实质风险：`chat`/`chat_stream` 的 `_run_agent` 调用契约**。5.16 有
  `_run_agent`，但参数/返回需构建后实跑核对。缓解：阶段验证时 `chat` 端点单独
  冒烟；若签名有差异，在注入块内做**最小适配**，仍以「行为对齐 6.5」为准（不
  改 6.5 的语义，只补差异参数）。
- **上游锚点漂移**：构建 fail-loud 即时抓（同 i18n catalog 与 reload 补丁的
  既有经验）。
- **构建缓存坑**：旧 variant 重建可能撞陈旧 install.sh 缓存层，必要时
  `NO_CACHE=1 HERMES_VARIANT=hermes-v2026.5.16 make build-hermes-runtime`
  （见 `project-hermes-build-gotchas`）。

## 验证（CLAUDE.md 硬要求：真实浏览器逐项）

1. **构建 fail-loud 过**：`make build-hermes-runtime
   HERMES_VARIANT=hermes-v2026.5.16` 不报锚点缺失。
2. **路由存在性**：注入后 pod 内 `curl 127.0.0.1:8642/api/sessions` 返回 **401**
   （有路由有鉴权）而非 404（与 6.5 行为一致）。
3. **本地 k3d 真机浏览器全链路**（按 `project-hermes-516-port` runbook：构建→
   push 本地 registry→后台改实例助手版本为 5.16→镜像变更触发滚动重建→pod
   digest 校验）：对话 tab 逐项过——会话**列表**、打开**历史消息**、**新建**
   会话、**发消息**（chat）、**流式**回复（chat/stream）、**重命名**、**删除**。
4. **跨版本回归**：6.5 实例对话功能不受影响（本次零改动 6.5 与 manager）。

## 非目标（YAGNI）

- 不搭「通用能力对齐框架」。本次只确立「构建期补丁 + oc-ops 拷贝」这一可复用
  范式，未来其他 6.5 能力照此办理，不预先抽象。
- 不改 manager、前端、`hermes_state.py`、6.5 variant。
- 不动线上实例版本 pin 策略（迁移与否是独立运营决策）。

## 交付边界提示

补丁改的是 variant 构建产物，需 `make build-hermes-runtime
HERMES_VARIANT=hermes-v2026.5.16` 重建镜像并按既有 runbook 部署/灰度才生效；
线上写操作（update-config / 发版）按 `prod-cluster-ops` 铁律由用户执行。
