# 对话功能支持「文件」消息 — 设计文档

- 日期：2026-06-28
- 状态：设计待评审
- 关联：[2026-06-23-instance-conversations-design.md](./2026-06-23-instance-conversations-design.md)（对话功能基础设计）

## 1. 背景与目标

manager 后台已有「实例对话」功能：前端 `AppConversationsTab.vue` →
manager `/api/v1/apps/:appId/hermes/conversations/*` → service → oc-ops
`/oc/conversations/*` → hermes `api_server`（pod 内 127.0.0.1:8642
`/api/sessions/*`）。消息结构本就是多模态的：`content` 透传
`字符串 | [{type, text|image_url}]`，前端 `ConversationMessageView.vue` 已能渲染
`text` 与 `image_url` part。

当前缺口：**不支持「文件」消息**——前端输入框没有选/拖文件能力，历史里的文件消息
也无法渲染。本设计补齐两个方向：

1. **网页端发送文件给 AI**：用户在对话框上传文件（文档 / 图片），随消息发给 AI 处理。
2. **历史渲染与下载**：历史消息里的文件渲染成卡片 / 图片，并可下载。

目标语义：让 AI **读取并理解文件内容**（文档：PDF/Word/txt/代码/表格…；图片：截图/照片）。

## 2. 关键约束（来自引擎现状调研）

调研 hermes 引擎（上游 `NousResearch/hermes-agent`，装在 app pod 内
`/usr/local/lib/hermes-agent/`）后确认的硬约束，决定了本设计的形态：

- **api_server 的 chat 端点只接受 `text` + `image_url`/`input_image` part，显式拒绝
  `file`/`input_file` part**（`_normalize_multimodal_content`，
  `unsupported_content_type`）。它不构建 `MessageEvent`、不碰 `media_urls`，直接把
  归一化后的 `content` 喂给 `_run_agent`，并把该 `content` 原样存进 transcript。
- **渠道（微信/飞书等）能处理文件，是另一条路径**：各渠道适配器从渠道 CDN 下载字节
  → `cache_media_bytes()` 落盘成 agent 可见路径 → `MessageEvent.media_urls` →
  `run.py` 把文档转成文字注记 `[The user sent a document: 'x'. The file is saved
  at: <path>...]`，图片走 vision。**这条路径 api_server 走不到。**
- **manager 不拥有对话存储**：历史由引擎 transcript 保存，manager 实时读取、不缓存。
  因此 manager 发什么、引擎就原样存什么——若内联 base64，base64 会撑大 transcript。
- **app pod 内 hermes 与 oc-ops 同 pod 共享 `/opt/data` 卷**（`render.go`：两容器都挂
  `data` 卷）。`TERMINAL_ENV` 为空 → `local` 后端 →
  `to_agent_visible_cache_path()` **原样返回路径**，即 oc-ops 写到 `/opt/data/...`
  的文件，hermes agent 用同一路径就能读。**oc-ops 容器实测可 import
  `gateway.platforms.base.cache_media_bytes` 与
  `tools.credential_files.to_agent_visible_cache_path`。**
- pod 内已有 `s3-sync` sidecar（`oc-sync`/`oc-presync`）把 `/opt/data` 持久化到 S3，
  及 init `restore`。`/opt/data` 本体是 emptyDir（pod 重启即弃）。

## 3. 方案选择与理由

参考项目 `nesquena/hermes-webui` 的做法是：**与 agent 同机共享 `~/.hermes` 卷**，
上传文件落本地盘，图片转 base64 `image_url` 内联、文档留盘让 agent 用文件工具按路径读，
**完全不改引擎**。它能这么做依赖两个前提——①与 agent 同卷②自己拥有会话存储——
**这两点 manager 都不具备**（独立服务、不共享盘、不拥有 transcript）。

候选方案对比：

| 方案 | 是否改上游引擎 | transcript | 共享盘 | 评价 |
|---|---|---|---|---|
| A. patch api_server 接受 file part | 是（构建期补丁×2 variant） | 紧凑 | 不需 | 上游漂移风险 + per-variant 补丁维护痛 |
| B. S3 FUSE 挂进引擎 pod | 否 | 紧凑 | 常驻挂载 | 每 pod 多个 FUSE，可靠性/一致性风险 |
| **C. oc-ops 落盘（选定）** | **否** | **紧凑** | **复用同 pod 共享盘** | **逻辑全在自有代码 oc-ops，hermes 升级零负担** |

**选定方案 C**（用户提出）：把「下载 S3 文件 → 落到共享盘 → 改写消息」放进 **oc-ops**
（本仓库自有 Python 代码 `ocops/`，与 hermes 同 pod 共享磁盘）。这是 webui「共享盘」
模型在 k8s 下的正确落地：oc-ops 充当 manager 与 api_server 之间的改写层，不碰任何上游
代码。相比方案 A/B：无上游补丁、无 FUSE、transcript 紧凑、复用引擎自身的
`cache_media_bytes` 分类与落盘。

## 4. 架构与数据流

### 4.1 发送（网页 → AI）

```
前端选/拖文件
  → manager 上传(复用知识库分片/直传基建)
      → S3 (durable): apps/{appId}/conversations/{sessionId}/{fileId}/{filename}
      → 落 conversation_files 记录(manager 自身操作记录)
  → manager 调 oc-ops chat，message content 携带 file part:
      {type:"input_file", file_id, file_url(presigned GET), filename, mime, size}
  → oc-ops(ocops/，本仓库代码):
      对每个 input_file part:
        1) HTTP GET file_url 下载字节(presigned，无需 S3 凭证)
        2) cache_media_bytes(data, filename) → 落 /opt/data/cache/{documents|images}
           → 得 agent 可见 path + kind(image/document) + mime
        3) 改写 message content:
           · 文档/图片统一转成 text 注记(引擎同款文案) + 机器可解析 fileId 标记:
             "[The user sent a {kind}: '{filename}'. The file is saved at: {path}.
              Ask the user what they'd like you to do.] <oc-file:{file_id}>"
           · 移除原 input_file part(api_server 不认)
      → 转发 api_server /api/sessions/{sid}/chat[/stream] (一如现状)
  → hermes agent 按共享盘路径读文件 / 对图片用 vision_analyze
```

设计要点：

- **图片与文档统一走「共享盘路径 + 文字注记」**（等同 webui 的
  `image_input_mode=text`）：transcript 紧凑、逻辑单一、不依赖 provider 抓取 URL；
  图片由 agent 的 `vision_analyze` 工具按路径分析。代价是图片画质略逊于原生多模态，
  v1 接受；如需原生 vision 可后续增强。
- oc-ops 写入的是 **当轮临时工作副本**：`/opt/data` 为 emptyDir，pod 重启即弃。
  历史下载不依赖它（见 4.2）。

### 4.2 历史渲染与下载

```
前端读 /messages
  → content 文字里含 "<oc-file:{file_id}>" 标记
  → manager 解析标记 → 用 conversation_files 取 filename/mime/size
  → 渲染: 图片=<img src=manager文件端点> / 文档=📎卡片(名+大小+下载)
  → 下载/预览: GET /api/v1/apps/:appId/hermes/conversations/:sid/files/:fileId
      → manager 按 file_id 查 conversation_files
        → 校验 (file 属于该 app+session) + 权限
        → 重新签名 S3 presigned 或流式回源 → 返回
```

- 历史可下载靠 **manager 自有的 `conversation_files` + durable S3**，与引擎
  transcript / `/opt/data` 解耦。引擎 transcript 只存一个稳定的 `file_id` 标记。
- `<oc-file:{file_id}>` 标记对 AI 是无害的尾随文本；manager 用正则可靠解析。

## 5. 改动分布

### 5.1 oc-ops（`runtime/hermes/hermes-v2026.6.5/ocops/` 与
`hermes-v2026.5.16/ocops/` 两 variant）

- 新增 `ocops/conversation_files.py`（或并入 `conversation.py`）：
  - `materialize_files(content) -> rewritten_content`：扫描 content 里的 `input_file`
    part，下载 → `cache_media_bytes` → 注入注记 + `<oc-file:id>` 标记 → 去除原 part。
  - 复用引擎 helper：`from gateway.platforms.base import cache_media_bytes`、
    `from tools.credential_files import to_agent_visible_cache_path`；import 失败
    **fail-loud**（启动期或首次调用即报错，不静默降级）。
  - 下载：标准库 `urllib`，仅对 presigned URL 做 HTTP GET；超时 + 大小上限校验。
- 改 `conversation.chat` 与 `conversation.chat_stream`：转发前对 `body["message"]`
  调 `materialize_files`。
- 两 variant 代码一致（本仓库自有代码，非补丁）；需确认 5.16 的
  `cache_media_bytes`/`to_agent_visible_cache_path` 签名一致（已知 6.5 可用）。
- 补丁单测 + 真机逐路径验证（图片 / 文本文档 / 二进制文档 / 不支持类型 / 多文件）。

### 5.2 manager 后端（Go）

- DTO：`ConversationChatRequest.Message` 由 `string` 改为 `any`（多模态 parts），
  service / oc-ops client 透传（`internal/api/handlers/dto.go`、
  `internal/integrations/ocops/types_conversation.go` 的 `ConversationChatReq.Message`
  已是 `any`，确认链路一致）。
- 上传端点：复用知识库分片/直传基建（`knowledge_service_multipart.go`、
  `xhrUpload.ts`），新增对话文件上传，落 S3 + `conversation_files`。
- 新表 `conversation_files`（MySQL，所有表与字段带 SQL COMMENT）：
  `id, app_id, session_id, s3_key, filename, mime, size, created_at`（按需加索引）。
- 下载端点 `GET /apps/:appId/hermes/conversations/:sid/files/:fileId`：权限校验 +
  归属校验 + 重签名 / 回源。
- 权限谓词放 `internal/auth/authorizer.go`：上传/发送复用
  `CanManageAppConversations`，下载复用 `CanViewAppConversations`。
- presigned URL 的 host 必须 **oc-ops 容器可达**：生产用 EOS 公网 endpoint；本地 k3d
  不能用 `*.localhost`/`127.0.0.1`，需集群内 S3 endpoint 或 `host.k3d.internal`
  （见记忆 [[local-k3d-env]] / [[project-local-k3d-host-internal-dns]]）。
- 重新生成 `make openapi-gen` + `make web-types-gen`。

### 5.3 前端（Vue/TS）

- `AppConversationsTab.vue` 输入框：加文件选择 + 拖拽（复用 `xhrUpload`/
  `uploadProgress`），多文件、进度、类型/大小前校验；发送时组多模态 parts
  （文字 + 每文件一个 `input_file` part，携带上传返回的 `file_id` 等）。
- `ConversationMessageView.vue`：解析 `<oc-file:id>` 标记，渲染图片预览 / 文档卡片
  （图标 + 文件名 + 大小 + 下载按钮，指向 manager 文件端点）。
- `conversations.ts`：`chatStream`/`chat` 的 `message` 支持 parts；新增上传与下载
  调用。

## 6. 类型与大小

- **支持类型**：对齐引擎 `SUPPORTED_DOCUMENT_TYPES`（pdf/docx/doc/odt/rtf/txt/md/
  epub/xlsx/xls/csv/tsv/json/xml/yaml/pptx/ppt/zip/tar/gz/7z/html…）+ 常见图片
  （jpg/jpeg/png/gif/webp/bmp）。前后端都校验，不支持类型拒绝。
- **大小**：复用分片上传；单文件上限可配（建议默认 100MB，超大文件走知识库）。
- **多文件**：允许一条消息多文件。

## 7. 错误处理

- 上传失败 / 类型不支持 / 超限：前端拦截 + manager 返回结构化错误（走现有
  apierror i18n catalog，见 [[project-backend-error-i18n]]）。
- oc-ops 下载失败 / `cache_media_bytes` 失败：该文件跳过并在注记中标注「文件不可用」，
  其余文件与文字正常发送；oc-ops 以 Warn 日志暴露，不让整轮 chat 失败。
- 下载端点：file_id 不存在 / 不属于该 app+session / 无权限 → 404/403。

## 8. 测试

- oc-ops：单测覆盖 `materialize_files`（图片 / 文档 / 多文件 / 下载失败 / 不支持类型 /
  标记注入正确性）；真机逐路径验证 agent 能按路径读到文件。
- manager：上传 / 下载端点权限矩阵（platform_admin / org_admin / org_member）、
  归属校验、conversation_files 落库；service 透传 parts。
- 前端：上传交互、parts 组装、历史卡片/图片渲染、下载链接。
- 交付前用真实浏览器全角色验证（见 [[feedback_verification-rigor]]）。

## 9. 非目标 / 后续

- 图片原生多模态 vision（base64/URL）画质增强——v1 走 vision_analyze，后续可选。
- 渠道（微信/飞书）历史文件的下载——其文件在引擎临时缓存、无 durable 副本，本设计
  不覆盖；历史渲染对渠道文件可降级为只读卡片（无下载）。
- 超大文件（>100MB）走对话——引导用知识库。
