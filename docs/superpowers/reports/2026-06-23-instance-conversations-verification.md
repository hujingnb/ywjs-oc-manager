# 实例对话功能 — 真实浏览器验证报告

> 日期：2026-06-23。环境：本地 k3d（manager-api/web + 真实 hermes 实例 pod）。
> 实例：`L6 成员's instance`（app `cf7264a2…`，版本 v2026.6.5，Supported=true，
> 镜像 `oc-manager-hermes:v2026.6.5-dev1`，模型 deepseek-chat）。

## 一、结论

平台管理员视角下，实例详情页「对话」tab 的**全部功能操作在真实浏览器中验证通过**：
列会话、选中读历史、流式续聊（SSE 管线）、新建、重命名、删除。验证过程发现并修复了
**3 个仅靠 mock 单测无法暴露、只有真实 pod 才能发现的缺陷**（见第三节）。

## 二、逐项验证矩阵（platform_admin）

| 功能 | 操作 | 结果 |
|---|---|---|
| tab 渲染 + i18n | 进入实例详情 → Conversations tab | ✅ 渲染，文案走 i18n（New chat / No conversations / Send…） |
| 列会话（跨来源） | 加载会话列表 | ✅ 7 条会话，每条带来源标签（api_server）+ id/标题 |
| 选中会话 | 点选某会话 | ✅ 右侧加载历史消息（USER「hi」），composer 启用 |
| 流式续聊 | 输入文字 → Send | ✅ SSE 管线贯通：oc-ops 把上游命名事件规整为单 data 帧 `{event,payload}`，逐帧流出（run.started/message.started/assistant.completed/run.completed/done）；前端无错误、草稿清空。⚠️ bot 文本为空——deepseek 渠道未配有效 key，content=null，与本功能代码无关 |
| 新建会话 | New chat | ✅ 创建并加入列表 |
| 重命名 | Rename → 弹窗输入 → Confirm | ✅ 标题更新（「浏览器验证改名」），列表刷新 |
| 删除 | Delete | ✅ 会话从列表移除（7→6），列表刷新 |
| Supported gate | v2026.6.5 镜像（非 -dev 后缀） | ✅ 判 Supported=true，tab 不被网关 |

## 三、真实 pod 验证发现并修复的缺陷（mock 单测均未暴露）

1. **api_server 不启动（commit 303adf3）**：上游 api_server 即使 `API_SERVER_ENABLED=true`
   也硬性要求 `API_SERVER_KEY`，否则拒绝启动（日志 `Refusing to start: API_SERVER_KEY
   is required`，含 loopback 绑定）。此前只注入 ENABLED 没注入 key → api_server 未启动 →
   对话功能整体不可用。修复：给 hermes + oc-ops 两容器注入 `API_SERVER_KEY`（复用 per-app
   control-token）。静态 Spike 分析误判为「无 key 即不鉴权」，真机推翻。

2. **create/rename 未解包 session（commit e7dccc8）**：api_server 的 `POST /api/sessions`
   与 `PATCH /api/sessions/{id}` 把会话包在 `{"object":"hermes.session","session":{…}}`
   里（不同于 list 的 `{data:[]}`）。oc-ops 未解包导致 manager 解出空 id，前端 `selectSession("")`
   触发「非法 session id」。修复：create_session/update_title 解包 `session` 键。

3. **时间戳类型不匹配（commit e7dccc8 后续）**：api_server 返回 `started_at`/`last_active`/
   `timestamp` 为数字（Unix 秒），DTO 声明成 `string` → manager json 解码「数字→字符串」
   失败 → 整条端点返回 `OUTPUT_INVALID`（502，"Hermes 版本可能不兼容"）。修复：
   `ConversationSession.StartedAt/LastActive` 改 float64，`ConversationMessage.Timestamp` 改 any。

## 四、未覆盖项与说明

- **org_member 左侧菜单入口未单独浏览器验证**：本次以 platform_admin 经 tab 导航验证全流程
  （admin 看 tab，org_member 走左侧菜单 `memberAppTabKey('conversations')`）。org_member 入口
  与鉴权（`CanViewAppConversations` 允许实例 owner）已有单测覆盖且为同一服务代码路径；
  浏览器侧入口未验证仅因缺该成员账号口令。建议后续用 l6-user 账号补一次左侧菜单进入验证。
- **bot 文本回复为空**：本地 new-api 的 deepseek 渠道未配有效上游 key，agent 产出 content=null。
  流式管线本身（连接、规整、逐帧、前端渲染分发）已验证贯通；配好模型 key 即可产出文本。

## 五、自动化测试基线（交付前全绿）

- oc-ops pytest：219 passed
- Go：`go test ./...` 全绿（含 ocops/service/handlers/k8sorch）
- openapi-check：PASS（契约与 generated.ts 同步）
- 前端：vitest（apps + i18n 149 passed）+ vue-tsc 干净
