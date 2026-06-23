# 实例对话功能 — 真实浏览器验证报告

> 日期：2026-06-23/24。环境：本地 k3d（manager-api/web + 真实 hermes 实例 pod）。
> 实例：`L6 成员's instance`（app `cf7264a2…`，版本 v2026.6.5，Supported=true，
> 镜像 `oc-manager-hermes:v2026.6.5-dev1`，模型 deepseek-chat）。

## 一、结论

实例详情页「对话」功能在真实浏览器中**端到端、三角色（platform_admin / org_admin /
org_member）、含越权 403 边界、含跨渠道微信、含 bot 真实回复**验证通过。验证过程中发现
并修复了 **3 个仅靠 mock 单测无法暴露、只有真实 pod 才能发现的代码缺陷**（第三节）+
**1 个环境配额问题**（第四节）。

**本期范围说明（图片消息留待下一迭代）**：设计 §2.2 把「图片」列入 v1 范围，但本期
**未实现出站图片发送**（composer 仅文字输入），入站图片显示虽已写入
`ConversationMessageView`（`<img>` 渲染 image_url part）但**未经浏览器验证**（验证期间
无图片消息）。经与需求方确认，**图片消息（发送 + 入站显示验证）顺延到下一迭代**，本期
以文字 + 跨渠道 + 全角色闭环交付。

## 二、逐项验证矩阵

### platform_admin（经实例详情页 tab）

| 功能 | 结果 |
|---|---|
| 「对话」tab 渲染 + i18n | ✅ |
| 列会话（跨来源，带 source 标签） | ✅ |
| 选中会话 → 读历史 | ✅ |
| 流式续聊（SSE 管线） | ✅ |
| 新建会话 | ✅ |
| 重命名（弹窗 → PATCH → 列表刷新） | ✅ |
| 删除（→ 列表移除） | ✅ |

### org_member（l6-user，经左侧菜单——org_member 不显示 tab）

| 功能 | 结果 |
|---|---|
| 左侧菜单出现「Conversations」入口 | ✅ |
| 列会话（鉴权放行实例 owner） | ✅ |
| 新建会话（写权限） | ✅ |
| 流式发送（用户消息持久化 + 显示） | ✅ |

### org_admin（l6-admin，经实例详情页 tab）

| 功能 | 结果 |
|---|---|
| 实例详情页「对话」tab 可见（org_admin 看 tab） | ✅ |
| 列会话（同组织 org_admin 放行） | ✅ |
| 选中会话 → 读历史（Messages 端点放行） | ✅ |

### 越权拒绝（authz 边界，带真实 token 的浏览器 fetch）

| 场景 | 结果 |
|---|---|
| org_admin 访问**本组织**实例会话端点 | ✅ 200 返回会话 |
| org_admin 访问**跨组织**实例（e2e-app）会话端点 | ✅ 403 `{"code":"CONVERSATION_FORBIDDEN","message":"无权访问该实例会话"}` |

### 跨渠道（微信，核心功能）

| 步骤 | 结果 |
|---|---|
| Channels → 微信「Begin login」生成二维码 | ✅ 生成 liteapp.weixin.qq.com 登录二维码 |
| 用户扫码绑定 | ✅ 实例状态变为 Running |
| 用户从微信发消息「你好」 | ✅ 消息进入 hermes |
| **微信会话出现在 web Conversations 统一列表** | ✅ 顶部出现 `weixin` 来源会话 `20260623_190958_…` |
| 从 web 读微信会话历史 | ✅ 显示用户「你好」 |

### bot 真实回复（配额修复后）

| 功能 | 结果 |
|---|---|
| 续聊「用一句话介绍你自己」→ deepseek 真实回复 | ✅ 渲染出 assistant：「我是 Hermes Agent 上的一位 AI 助手…」，流式管线把真实回复逐字呈现 |

## 三、真实 pod 验证发现并修复的代码缺陷（mock 单测均未暴露）

1. **api_server 不启动（commit 303adf3）**：上游 api_server 即使 `API_SERVER_ENABLED=true`
   也硬性要求 `API_SERVER_KEY`，否则拒绝启动（`Refusing to start: API_SERVER_KEY is
   required`，含 loopback 绑定）。此前只注入 ENABLED → api_server 未启动 → 对话功能整体
   不可用。修复：给 hermes + oc-ops 两容器注入 `API_SERVER_KEY`（复用 per-app control-token）。
   静态 Spike 误判「无 key=不鉴权」，被真机推翻。

2. **create/rename 未解包 session（commit e7dccc8）**：api_server 的 `POST /api/sessions`
   与 `PATCH /api/sessions/{id}` 把会话包在 `{"object","session":{…}}` 里（不同于 list 的
   `{data:[]}`）。oc-ops 未解包 → manager 解出空 id → 前端「非法 session id」。修复：解包 session 键。

3. **时间戳类型不匹配（commit a928ecd）**：api_server 返回 `started_at`/`last_active`/
   `timestamp` 为数字（Unix 秒），DTO 声明成 `string` → manager json 解码失败 → 整条端点
   502 `OUTPUT_INVALID`。修复：`StartedAt/LastActive` 改 float64、`Timestamp` 改 any。

## 四、环境配额问题（非代码，已就地修复）

- **现象**：bot 回复为空 / 微信侧回「用户额度不足」。
- **根因**：new-api 中 L6 org 的用户 `l6-org-mdvau7` 配额=0（deepseek 渠道 key 本身有效）。
- **修复**：给该 new-api 用户充配额后，bot 即正常产出真实回复（见第二节末行）。生产环境
  应经 manager 充值流程给组织充额度，此处为本地验证就地设置。
- **其它无害提示**：agent 回复中出现 `Tool 'oc-kb' does not exist`——实例 manifest 未装
  oc-kb 知识库工具，与对话功能无关，且不影响最终回复。

## 五、自动化测试基线（交付前全绿）

- oc-ops pytest：219 passed
- Go：`go test ./...` 全绿（含 ocops/service/handlers/k8sorch）
- openapi-check：PASS（契约与 generated.ts 同步）
- 前端：vitest（apps + i18n 149 passed）+ vue-tsc 干净
